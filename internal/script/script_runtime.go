package script

// script_runtime.go — the script event loop, and the play sandbox around it.
//
// Once per frame during play, ScriptTick derives events from the world the
// engine has just simulated, posts them, and drains the queue to a fixpoint.
// Deferred work — geometry rebuilds, texture loads — is applied later in the
// frame by DrainScriptWork, where the main GL context is current.
//
// Two decisions carry most of the design.
//
// **Drain to a fixpoint, within the frame.** A `hit → damage → die → open_door`
// chain resolves in one frame. Delivering one hop per frame instead would spread
// it over three *variable-length* frames, making behaviour depend on framerate.
// The cost is that a ping-pong between two objects would never end, which is what
// the hop cap is for: at the cap a message is deferred to the next frame rather
// than dropped, so a legitimate deep cascade still completes while a true loop
// degrades to a trickle plus a warning.
//
// **One queue for the whole runtime, not one per object.** Cross-object ordering
// is then deterministic, which is the precondition for script behaviour being
// reproducible and therefore testable at all. Per-object queues would make the
// interleaving depend on map iteration order.

import (
	"fmt"
	"sort"

	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// Script runtime budgets. The mechanism lives in internal/flang; the numbers are
// engine policy and live here, where they can be tuned against a real frame.
const (
	scriptMaxHops            = 64
	scriptMaxMessagesPerTick = 10000
	scriptMaxErrorsPerScript = 20
)

// queuedMsg is one message waiting to be delivered.
type queuedMsg struct {
	to   flang.RefVal
	atom string
	args []flang.Value
	pos  flang.Pos
	hops int
	// fromScript distinguishes a `<-` or an `after` from an engine event.
	//
	// It exists only so that "nothing handled this" can be warned about for the
	// first and not the second. `:tick:` is posted to every scripted object in
	// the level and handled by almost none of them, so warning on every unhandled
	// engine event would be sixty lines a frame of noise; a script that sends
	// `(:open:, @self)` to an object with no `:open:` clause, on the other hand,
	// has a bug and no other way to find out about it.
	fromScript bool
	// from is who sent it, recorded so that a warning about a send is filed
	// under the script that wrote the send rather than the one that failed to
	// answer it. By delivery time rt.self is the *receiver*, so this cannot be
	// recovered later.
	from flang.RefVal
}

// scriptUnit is one loaded script: its program, its interpreter, and how badly
// it has been behaving.
type scriptUnit struct {
	path   string
	prog   *flang.Program
	interp *flang.Interp
	errs   flang.ErrorList
	// failures counts runtime errors this session. Past a threshold the script
	// is disabled: a handler erroring every frame would fill the log and slow
	// the game without ever telling the author anything new.
	failures int
	disabled bool
	// issues are the level-aware checks — names the level does not have,
	// animations an entity never declared. Computed once at load, because that
	// is the moment both the script and the level are in hand, and because a
	// script that would do nothing is worth saying so *before* someone walks
	// into the volume. See script_lint.go.
	issues []ScriptIssue
}

// ScriptStats is what the F3 overlay shows.
type ScriptStats struct {
	Delivered int
	Deferred  int
	Dropped   int
	Errors    int
}

// ScriptRuntime owns every loaded script and the message queue between them.
type ScriptRuntime struct {
	lvl   *world.Level
	cache *refCache
	host  *levelHost

	// units is one entry per distinct script path, shared by every object that
	// names it — a hundred barrels of one type parse their script once.
	units map[string]*scriptUnit

	// self is the object whose handler is currently running, and currentHops is
	// that message's hop count. A message sent while handling one inherits
	// hops+1, which is how a cascade is measured; -1 means nothing is being
	// delivered, so an event handler's own sends start at 0.
	self        flang.RefVal
	currentHops int

	// queue is the single FIFO. head is where the next delivery comes from;
	// draining pops from the front and appends to the back.
	queue []queuedMsg
	// spill holds messages deferred past the hop cap or the per-tick budget,
	// carried into the next frame.
	spill []queuedMsg

	// state is per-object scratch, keyed by ref. Never saved; dropped on reload.
	state map[flang.RefVal]*flang.DictVal

	// dirtySectors are sectors whose geometry a property write invalidated.
	// Coalesced by ref, so writing a height a hundred times in one tick costs
	// one rebuild.
	dirtySectors map[flang.RefVal]bool
	dirtyVolumes map[string]bool
	dirtyTex     map[string]bool

	// warned tracks refs already complained about, so a send to a deleted
	// object logs once rather than every frame.
	warned map[flang.RefVal]bool
	// warnedAt is the same idea for the verbs' wiring warnings, keyed by
	// position and message rather than by ref. A `play_anim` in a `:tick:`
	// handler naming an animation that does not exist would otherwise produce
	// sixty identical lines a second, which buries the one that mattered.
	// Cleared on reload, so a warning returns if the fix did not take.
	warnedAt map[string]bool
	// fanout counts how many objects each script send reached this tick, keyed
	// by the sending line and atom.
	//
	// It exists to tell a mistake from an idiom. `@door <- (:open:)` reaching a
	// door with no `:open:` clause is a bug worth reporting; `#lights <- (:dim:)`
	// reaching eight objects of which three care is the documented way to
	// broadcast, and warning about the other five would make the feature
	// unusable. One target means the first, several mean the second. Reset every
	// tick, because it is only ever read during that tick's drain.
	fanout map[string]int

	stats ScriptStats
}

// Scripts is the engine-wide runtime. One per process, rebuilt when a level is
// loaded or replaced.
var Scripts = &ScriptRuntime{
	units:        map[string]*scriptUnit{},
	state:        map[flang.RefVal]*flang.DictVal{},
	dirtySectors: map[flang.RefVal]bool{},
	dirtyVolumes: map[string]bool{},
	dirtyTex:     map[string]bool{},
	warned:       map[flang.RefVal]bool{},
	warnedAt:     map[string]bool{},
	fanout:       map[string]int{},
}

// ScriptStatsSnapshot returns the last tick's counters, for the debug overlay.
func ScriptStatsSnapshot() ScriptStats { return Scripts.stats }

// ---------- lifecycle ----------

// ScriptRuntimeReset drops every loaded script, queued message and scrap of
// per-object state, and rebinds to lvl.
//
// Called whenever the level is replaced — a load, an undo, the end of a play
// session. Refs are name paths so most would survive, but the objects behind
// those names have been re-instantiated, so anything remembered about them is
// stale by construction.
func ScriptRuntimeReset(lvl *world.Level) {
	rt := Scripts
	rt.lvl = lvl
	rt.cache = buildRefCache(lvl)
	rt.host = &levelHost{rt: rt}
	rt.units = map[string]*scriptUnit{}
	rt.queue = nil
	rt.spill = nil
	rt.state = map[flang.RefVal]*flang.DictVal{}
	rt.dirtySectors = map[flang.RefVal]bool{}
	rt.dirtyVolumes = map[string]bool{}
	rt.dirtyTex = map[string]bool{}
	rt.warned = map[flang.RefVal]bool{}
	rt.warnedAt = map[string]bool{}
	rt.fanout = map[string]int{}
	rt.self = flang.RefVal{}
	rt.stats = ScriptStats{}
}

// ScriptRuntimeLoad parses every script the level references. Diagnostics are
// logged and kept on the unit, so the script editor can show them.
func ScriptRuntimeLoad(lvl *world.Level) {
	ScriptRuntimeReset(lvl)
	if lvl == nil {
		return
	}
	for _, path := range Scripts.scriptPaths() {
		Scripts.loadUnit(path)
	}
}

// scriptPaths returns every distinct script the level names, sorted so loading
// is deterministic and so a diagnostic list does not shuffle between runs.
func (rt *ScriptRuntime) scriptPaths() []string {
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" {
			seen[p] = true
		}
	}
	if rt.lvl != nil {
		for _, p := range rt.lvl.Scripts {
			add(p)
		}
		for i := range rt.lvl.Entities {
			add(rt.lvl.Entities[i].Script)
		}
		for i := range rt.lvl.Volumes {
			add(rt.lvl.Volumes[i].Script)
		}
		for i := range rt.lvl.Sprites {
			add(rt.lvl.Sprites[i].Script)
		}
		for i := range rt.lvl.Emitters {
			add(rt.lvl.Emitters[i].Script)
		}
		for ci := range rt.lvl.Chunks {
			ch := &rt.lvl.Chunks[ci]
			add(ch.Script)
			for si := range ch.Sectors {
				s := &ch.Sectors[si]
				add(s.Script)
				add(s.Floor.Script)
				add(s.Ceiling.Script)
				for i := range s.Walls {
					add(s.Walls[i].Script)
				}
				for i := range s.Portals {
					add(s.Portals[i].Script)
				}
			}
		}
		for i := range rt.lvl.Lights.Directional {
			add(rt.lvl.Lights.Directional[i].Script)
		}
		for i := range rt.lvl.Lights.Points {
			add(rt.lvl.Lights.Points[i].Script)
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// loadUnit parses one script and everything it imports.
func (rt *ScriptRuntime) loadUnit(path string) *scriptUnit {
	if u, ok := rt.units[path]; ok {
		return u
	}
	u := &scriptUnit{path: path}
	rt.units[path] = u

	full := fileutil.ScriptPath(path)
	src, err := ReadScriptFile(full)
	if err != nil {
		u.errs = flang.ErrorList{{Pos: flang.Pos{File: path}, Msg: err.Error()}}
		u.disabled = true
		ScriptLog.Errorf(path, "%v", err)
		return u
	}

	prog, errs := flang.Link(full, src, scriptLoader{})
	u.prog = prog
	u.errs = errs

	interp, ierrs := flang.NewInterp(prog, rt.host, flang.Limits{})
	u.errs = append(u.errs, ierrs...)
	u.interp = interp
	interp.SetOutput(func(s string) { rt.logLine(s) })

	if len(u.errs) > 0 {
		// A script that would not compile is disabled rather than half-run:
		// dispatching into a program with an ambiguous clause table would give
		// behaviour nobody could predict from the source.
		u.disabled = true
		// One entry per diagnostic rather than one blob: each keeps its own
		// position, so the log window can open the script at the right line.
		ScriptLog.Diagnostics(path, u.errs)
		return u
	}

	// Linting only a compiling script, for the same reason the editor does: the
	// tree behind a failed parse is whatever recovery salvaged, and reporting on
	// it would bury the errors that matter under guesses.
	u.issues = LintScript(rt.lvl, rootFileOf(prog, full), programNames(prog), programParamNames(prog))
	for _, s := range u.issues {
		ScriptLog.WarnAt(path, s.Pos, "%s", s.Msg)
	}
	return u
}

// rootFileOf picks the file Link was rooted at, rather than one of its imports.
func rootFileOf(prog *flang.Program, path string) *flang.File {
	if prog == nil {
		return nil
	}
	for _, f := range prog.Files {
		if f.Path == path {
			return f
		}
	}
	return nil
}

// programNames is every function a linked program defines, imports included.
func programNames(prog *flang.Program) map[string]bool {
	if prog == nil {
		return nil
	}
	out := make(map[string]bool, len(prog.Funcs))
	for n := range prog.Funcs {
		out[n] = true
	}
	return out
}

// programParamNames is every name the program declares with `param`, for the
// linter's `@name` check.
func programParamNames(prog *flang.Program) map[string]bool {
	if prog == nil {
		return nil
	}
	out := make(map[string]bool, len(prog.Params))
	for n := range prog.Params {
		out[n] = true
	}
	return out
}

// ScriptDiagnostics returns the diagnostics for one script path, for the editor.
func ScriptDiagnostics(path string) flang.ErrorList {
	if u, ok := Scripts.units[path]; ok {
		return u.errs
	}
	return nil
}

// ScriptIssues returns the level-aware issues for one script path, for the
// editor's file list. Empty for a script the level does not use, which has never
// been loaded and so has never been checked.
func ScriptIssues(path string) []ScriptIssue {
	if u, ok := Scripts.units[path]; ok {
		return u.issues
	}
	return nil
}

// ScriptUsage says how a script is reachable from the level that is loaded:
// whether the level itself names it, and which other scripts import it. Both
// are things worth knowing before deleting a file.
//
// It answers from the runtime's loaded units rather than by re-walking the
// level, which is what makes the second half possible at all: an import is not
// written anywhere in the level, only in the script that imports it, so the only
// record of one is the linked program the runtime is holding.
//
// Paths are compared resolved. A level may name a script "door.flang" or
// "scripts/door.flang" — ScriptPath reads both — and a delete that missed a
// warning over a prefix would be the worst kind of miss.
func ScriptUsage(path string) (named bool, importedBy []string) {
	full := fileutil.ScriptPath(path)
	if full == "" {
		return false, nil
	}
	for p, u := range Scripts.units {
		if fileutil.ScriptPath(p) == full {
			named = true
			continue
		}
		if u.prog == nil {
			continue
		}
		for _, f := range u.prog.Files {
			if f.Path == full {
				importedBy = append(importedBy, p)
				break
			}
		}
	}
	sort.Strings(importedBy)
	return named, importedBy
}

// ScriptReload re-reads every script from disk, keeping the level in place. This
// is what hot reload calls.
func ScriptReload() {
	ScriptRuntimeLoad(Scripts.lvl)
}

// ---------- host callbacks ----------

// logLine is where a script's own `log()` output lands. Attributed to whichever
// script is running, so the window can filter by it.
func (rt *ScriptRuntime) logLine(msg string) {
	ScriptLog.Infof(rt.currentScript(), "%s", msg)
}

// warnAt logs one wiring warning against the line that caused it, at most once
// per distinct message per load.
//
// The dedupe is on the text as well as the position, not the position alone: one
// `play_anim` line can be wrong about several entities in turn, and collapsing
// those to whichever fired first would hide the rest.
func (rt *ScriptRuntime) warnAt(pos flang.Pos, format string, args ...any) {
	rt.warnFor(rt.currentScript(), pos, format, args...)
}

// warnFor is warnAt for a caller that knows which script the warning belongs to.
func (rt *ScriptRuntime) warnFor(script string, pos flang.Pos, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	key := script + "\x00" + pos.String() + "\x00" + msg
	if rt.warnedAt == nil {
		rt.warnedAt = map[string]bool{}
	}
	if rt.warnedAt[key] {
		return
	}
	rt.warnedAt[key] = true
	if pos.File == "" {
		// A verb called from a handler always has a position; one called
		// through `after` or from a test may not, and an unpositioned warning
		// is still worth more than silence.
		ScriptLog.Warnf(script, "%s", msg)
		return
	}
	ScriptLog.WarnAt(script, pos, "%s", msg)
}

// currentScript names the script whose handler is running, or "" between them.
func (rt *ScriptRuntime) currentScript() string {
	if rt.self.IsNone() {
		return ""
	}
	return rt.scriptFor(rt.self)
}

// stateFor returns an object's scratch dict, creating it on first use.
func (rt *ScriptRuntime) stateFor(r flang.RefVal) *flang.DictVal {
	d, ok := rt.state[r]
	if !ok {
		d = flang.NewDict()
		rt.state[r] = d
	}
	return d
}

// markDirty records that a script has changed authored state, so leaving play
// restores the level. A session that wrote nothing costs nothing to leave.
func (rt *ScriptRuntime) markDirty() { PlaySessionMarkDirty() }

// markGeometryDirty queues a rebuild for whatever sector a ref belongs to.
func (rt *ScriptRuntime) markGeometryDirty(r flang.RefVal) {
	switch r.Kind {
	case flang.RefSector, flang.RefFloor, flang.RefCeiling, flang.RefEdge:
		rt.dirtySectors[flang.RefVal{Kind: flang.RefSector, A: r.A, B: r.B}] = true
	case flang.RefVolume:
		rt.dirtyVolumes[r.A] = true
	}
}

// markTextureDirty queues a texture for upload at the end of the frame.
//
// Deferred for the same reason a geometry rebuild is: LoadTexture is GL work
// and the tick runs before the frame's GL work. Coalesced by name, so a script
// flicking one wall between two textures every frame loads each once.
func (rt *ScriptRuntime) markTextureDirty(name string) {
	if name == "" {
		return
	}
	if rt.dirtyTex == nil {
		rt.dirtyTex = map[string]bool{}
	}
	rt.dirtyTex[name] = true
}

// post queues a message. The hop count is inherited from whatever is currently
// being delivered, which is what makes a cascade measurable.
//
// Whether it came from a script is inferred rather than passed: a send can only
// happen while a handler is running, and a handler running is exactly what makes
// rt.self set.
func (rt *ScriptRuntime) post(to flang.RefVal, atom string, args []flang.Value, pos flang.Pos) error {
	return rt.postFrom(to, atom, args, pos, !rt.self.IsNone())
}

// postFrom is post for a caller that knows the origin itself. A timer is the one
// such caller: `after` is a script send, but it is *delivered* from the event
// pass, where rt.self is no longer set and the inference above would read it as
// an engine event.
func (rt *ScriptRuntime) postFrom(to flang.RefVal, atom string, args []flang.Value, pos flang.Pos, fromScript bool) error {
	if fromScript {
		if rt.fanout == nil {
			rt.fanout = map[string]int{}
		}
		rt.fanout[sendKey(atom, pos)]++
	}
	rt.queue = append(rt.queue, queuedMsg{
		to: to, atom: atom, args: args, pos: pos,
		hops: rt.currentHops + 1, fromScript: fromScript, from: rt.self,
	})
	return nil
}

// sendKey identifies one `<-` in one tick: the line it is on and what it sent.
func sendKey(atom string, pos flang.Pos) string { return pos.String() + "\x00" + atom }

// wasBroadcast reports whether a send reached more than one object, which is the
// difference between a mistake and the documented way to address a tag.
//
// A count of zero means the message spilled past the hop cap into a later tick,
// where the counts have been reset. Treated as a single send, because an
// unreported bug is worse than a warning about a broadcast that was cut short.
func (rt *ScriptRuntime) wasBroadcast(m queuedMsg) bool {
	return rt.fanout[sendKey(m.atom, m.pos)] > 1
}

// ---------- delivery ----------

// scriptFor returns the script attached to an object, or "".
func (rt *ScriptRuntime) scriptFor(r flang.RefVal) string {
	if e := rt.host.entityFor(r); e != nil {
		return e.Script
	}
	if d := rt.cache.scriptDataFor(r); d != nil {
		return d.Script
	}
	if r.Kind == flang.RefLevel && rt.lvl != nil && len(rt.lvl.Scripts) > 0 {
		// The level's handlers are several files; delivery tries each.
		return rt.lvl.Scripts[0]
	}
	return ""
}

// unitsFor returns every script that should see a message addressed to r. All
// but the level have exactly one.
func (rt *ScriptRuntime) unitsFor(r flang.RefVal) []*scriptUnit {
	if r.Kind == flang.RefLevel && rt.lvl != nil {
		out := make([]*scriptUnit, 0, len(rt.lvl.Scripts))
		for _, p := range rt.lvl.Scripts {
			if u := rt.units[p]; u != nil && !u.disabled && u.interp != nil {
				out = append(out, u)
			}
		}
		return out
	}
	path := rt.scriptFor(r)
	if path == "" {
		return nil
	}
	u := rt.units[path]
	if u == nil || u.disabled || u.interp == nil {
		return nil
	}
	return []*scriptUnit{u}
}

// deliver runs one message's handler.
func (rt *ScriptRuntime) deliver(m queuedMsg) {
	if !rt.host.Alive(m.to) {
		// The object was destroyed between the send and the delivery, which is
		// ordinary. Warn once per target so a repeated send does not fill the log.
		if !rt.warned[m.to] {
			rt.warned[m.to] = true
			ScriptLog.Warnf(rt.scriptFor(m.to),
				"dropped :%s: for %s, which no longer exists", m.atom, m.to)
		}
		rt.stats.Dropped++
		return
	}
	units := rt.unitsFor(m.to)
	if len(units) == 0 {
		if m.fromScript && !rt.wasBroadcast(m) {
			rt.warnFor(rt.scriptFor(m.from), m.pos,
				"%s received :%s: but %s", m.to, m.atom, noHandlerReason(rt, m.to))
		}
		rt.stats.Dropped++
		return
	}

	prevSelf, prevHops := rt.self, rt.currentHops
	rt.self, rt.currentHops = m.to, m.hops
	defer func() { rt.self, rt.currentHops = prevSelf, prevHops }()

	anyMatched := false
	for _, u := range units {
		u.interp.ResetSteps()
		_, matched, err := u.interp.CallAtom(m.atom, m.args, m.pos)
		if err != nil {
			rt.recordFailure(u, err)
			continue
		}
		if matched {
			anyMatched = true
			rt.stats.Delivered++
		}
	}
	if m.fromScript && !anyMatched && !rt.wasBroadcast(m) {
		// Filed against the *sender*, at the sender's line, which is where the
		// fix is: the receiving script's problem is a clause that is not there
		// to point at.
		rt.warnFor(rt.scriptFor(m.from), m.pos,
			"nothing handled :%s: sent to %s: its script (%s) defines no clause whose first parameter is :%s:",
			m.atom, m.to, rt.scriptFor(m.to), m.atom)
	}
}

// recordFailure logs a script error and disables the script if it keeps
// happening. A handler erroring every frame fills the log and slows the game
// without ever telling the author anything new.
func (rt *ScriptRuntime) recordFailure(u *scriptUnit, err error) {
	rt.stats.Errors++
	u.failures++
	// A runtime error knows where it happened; carrying the position through is
	// what lets the window open the script at the offending line.
	if re, ok := err.(*flang.RuntimeError); ok {
		ScriptLog.ErrorAt(u.path, re.Pos, "%s", re.Msg)
	} else {
		ScriptLog.Errorf(u.path, "%v", err)
	}
	if u.failures >= scriptMaxErrorsPerScript {
		u.disabled = true
		ScriptLog.Warnf(u.path, "disabled after %d errors; fix it and reload", u.failures)
	}
}

// drain delivers queued messages until nothing is left, or a budget stops it.
func (rt *ScriptRuntime) drain() {
	delivered := 0
	for len(rt.queue) > 0 {
		if delivered >= scriptMaxMessagesPerTick {
			// Spill the rest rather than dropping it: a fan-out that overran the
			// budget still means to happen, just not all in one frame.
			rt.spill = append(rt.spill, rt.queue...)
			rt.queue = nil
			ScriptLog.Warnf("", "message budget of %d reached; %d deferred to next frame",
				scriptMaxMessagesPerTick, len(rt.spill))
			return
		}
		m := rt.queue[0]
		rt.queue = rt.queue[1:]

		if m.hops > scriptMaxHops {
			// Deferred, not dropped. A legitimate deep cascade degrades to one
			// hop per frame and still completes; a true ping-pong degrades to a
			// trickle. Either way the frame ends.
			m.hops = 0
			rt.spill = append(rt.spill, m)
			rt.stats.Deferred++
			continue
		}
		rt.deliver(m)
		delivered++
	}
}

// ---------- the tick ----------

// ScriptTick runs one frame of the script event loop.
//
// Called from the main loop immediately after EntityUpdate, so the events it
// derives describe the world as it settled rather than as it was — this frame's
// overlaps produce this frame's `enter`, and an attribute a handler writes lands
// before the frame draws.
//
// play mirrors EntityUpdate's own gate: scripts do not run while the editor is
// up, except through the script editor's explicit run button.
func ScriptTick(lvl *world.Level, dt float32, play bool) {
	rt := Scripts
	if !play || lvl == nil {
		return
	}
	if rt.lvl != lvl {
		// The level was replaced without anyone telling the runtime. Rebinding
		// is better than reading a level that is not there any more.
		ScriptRuntimeLoad(lvl)
	}

	// The cache is rebuilt every tick and never carried across one: anything
	// structural invalidates it, and rebuilding is cheaper than reasoning about
	// exactly when it did not.
	rt.cache = buildRefCache(lvl)
	rt.stats = ScriptStats{}
	rt.fanout = map[string]int{}

	// Messages deferred last frame go first, so a spilled cascade continues in
	// the order it was cut off.
	rt.queue = append(rt.queue, rt.spill...)
	rt.spill = nil

	rt.currentHops = -1
	ScriptEventsTick(rt, lvl, dt)
	rt.drain()
}

// noHandlerReason says why a targeted send reached no script at all.
func noHandlerReason(rt *ScriptRuntime, r flang.RefVal) string {
	if rt.scriptFor(r) == "" {
		return "it has no script attached"
	}
	return "its script did not compile, or has been disabled after repeated errors"
}

// ---------- deferred work ----------

// DrainScriptWork applies everything the tick deferred: geometry rebuilds and
// texture loads.
//
// Called once per frame from the main loop, beside DrainTextureUploads, with the
// main GL context current. It cannot happen inside the tick — ReloadSector ends
// in gl.* calls, and the tick runs before the frame's GL work — which is the
// same reason DrainModelTexEditorRequest exists.
//
// Coalescing by ref means a script writing a floor's height a hundred times in
// one tick costs exactly one rebuild.
func DrainScriptWork(lvl *world.Level) {
	rt := Scripts
	if lvl == nil {
		return
	}
	for name := range rt.dirtyTex {
		if err := gfx.EnsureTextureLoaded(name); err != nil {
			ScriptLog.Warnf("", "texture %q: %v", name, err)
		}
		delete(rt.dirtyTex, name)
	}

	if len(rt.dirtySectors) > 0 {
		cache := buildRefCache(lvl)
		// Chunk-level work is collected rather than done per sector: RebuildGaps
		// and RecomputeBounds both cover the whole chunk, so running them once
		// per dirty sector would repeat the same work for every sector in it.
		dirtyChunks := map[*world.Chunk]bool{}
		// A script can change what a sector looks like, but nothing it can write
		// moves the sector's XZ footprint — and the spatial hash is indexed by
		// XZ alone. Rebuilding it every frame an animated floor moves would cost
		// a whole-level structure for a change that cannot affect it, so it is
		// rebuilt only if the footprint actually moved. Checked rather than
		// assumed, so a property added later that *does* move one still works.
		footprintMoved := false

		for r := range rt.dirtySectors {
			ch, s := cache.sectorFor(r)
			if ch != nil && s != nil {
				// Deliberately not InferPortalsAndReload: that renumbers edges,
				// which would invalidate every address a script holds.
				if s.Floor.Texture != "" {
					_ = gfx.EnsureTextureLoaded(s.Floor.Texture)
				}
				if s.Floor.BlendTexture != "" {
					_ = gfx.EnsureTextureLoaded(s.Floor.BlendTexture)
				}
				if s.Floor.BlendMask != "" {
					_ = gfx.EnsureTextureLoaded(s.Floor.BlendMask)
				}
				if s.Ceiling.Texture != "" {
					_ = gfx.EnsureTextureLoaded(s.Ceiling.Texture)
				}
				if s.Ceiling.BlendTexture != "" {
					_ = gfx.EnsureTextureLoaded(s.Ceiling.BlendTexture)
				}
				if s.Ceiling.BlendMask != "" {
					_ = gfx.EnsureTextureLoaded(s.Ceiling.BlendMask)
				}
				for i := range s.Walls {
					if s.Walls[i].Texture != "" {
						_ = gfx.EnsureTextureLoaded(s.Walls[i].Texture)
					}
					if s.Walls[i].BlendTexture != "" {
						_ = gfx.EnsureTextureLoaded(s.Walls[i].BlendTexture)
					}
					if s.Walls[i].BlendMask != "" {
						_ = gfx.EnsureTextureLoaded(s.Walls[i].BlendMask)
					}
				}
				beforeMin, beforeMax := s.MinBound, s.MaxBound
				_ = world.ReloadSector(ch, s)
				if s.MinBound != beforeMin || s.MaxBound != beforeMax {
					footprintMoved = true
				}
				dirtyChunks[ch] = true
			}
			delete(rt.dirtySectors, r)
		}

		for ch := range dirtyChunks {
			ch.RebuildGaps()
			// The chunk's Y bounds are what FindChunkAndSectorSH tells stacked
			// chunks apart by. A floor a script raised past the recorded maximum
			// would stop being found at all — the player would walk onto a lift
			// and fall straight through it.
			ch.RecomputeBounds()
		}
		if footprintMoved {
			if err := world.RebuildIndexes(lvl); err != nil {
				ScriptLog.Warnf("", "%v", err)
			}
		}
	}

	if len(rt.dirtyVolumes) > 0 {
		for id := range rt.dirtyVolumes {
			if v := world.FindVolume(lvl, id); v != nil {
				_ = v.Load()
			}
			delete(rt.dirtyVolumes, id)
		}
	}
}

// ---------- errors ----------

// scriptLoader resolves an `import(...)` against the scripts root.
type scriptLoader struct{}

func (scriptLoader) Load(fromFile, importPath string) ([]byte, string, error) {
	resolved, err := ResolveScriptImport(fromFile, importPath)
	if err != nil {
		return nil, "", err
	}
	src, err := ReadScriptFile(resolved)
	if err != nil {
		return nil, "", fmt.Errorf("import %q: %w", importPath, err)
	}
	return src, resolved, nil
}

// vec3Value is the conversion the action verbs share.
func vec3Value(v flang.Value) (mgl32.Vec3, bool) { return toVec3(v) }

// RunScriptOnce compiles src and calls one function against lvl, outside the
// event loop.
//
// This is what the script editor's "Run on the editor level" button uses. It is
// deliberately the only way a script touches the world while the editor is up:
// the caller pushes an undo entry first, so a deliberate editor-time run is as
// undoable as any other operation, and everything else stays gated on play.
func RunScriptOnce(lvl *world.Level, name, src, entry string) error {
	rt := Scripts
	prevLvl, prevSelf := rt.lvl, rt.self
	rt.lvl = lvl
	rt.cache = buildRefCache(lvl)
	if rt.host == nil {
		rt.host = &levelHost{rt: rt}
	}
	rt.self = flang.RefVal{Kind: flang.RefLevel}
	defer func() {
		rt.lvl, rt.self = prevLvl, prevSelf
		rt.cache = buildRefCache(rt.lvl)
	}()

	prog, errs := flang.Link(name, []byte(src), scriptLoader{})
	if len(errs) > 0 {
		return fmt.Errorf("%s", errs.Error())
	}
	in, ierrs := flang.NewInterp(prog, rt.host, flang.Limits{})
	if len(ierrs) > 0 {
		return fmt.Errorf("%s", ierrs.Error())
	}
	in.SetOutput(func(s string) { rt.logLine(s) })

	if !in.Has(entry) {
		return fmt.Errorf("%s defines no %s()", name, entry)
	}
	if _, err := in.Call(entry, nil, flang.Pos{File: name}); err != nil {
		return err
	}
	// Whatever the run dirtied still needs its geometry rebuilt, and there is no
	// tick coming to do it.
	DrainScriptWork(lvl)
	return nil
}
