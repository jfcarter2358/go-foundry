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
	"math/rand"
	"sort"

	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/tracker"
	"foundry/internal/ui"
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

	// songEvents is reused each frame to drain the level song's events.
	songEvents []tracker.Event

	// units is one entry per distinct script path, shared by every object that
	// names it — a hundred barrels of one type parse their script once.
	units map[string]*scriptUnit

	// trees is one entry per distinct decision tree (.fai), on the same rule —
	// see script_aitree.go. treeParams is the parameter source each script has
	// to be linked against, by script path, because a tree's `params:` are
	// declared to the script it runs beside.
	trees      map[string]*treeUnit
	treeParams map[string][]string

	// self is the object whose handler is currently running, and currentHops is
	// that message's hop count. A message sent while handling one inherits
	// hops+1, which is how a cascade is measured; -1 means nothing is being
	// delivered, so an event handler's own sends start at 0.
	self        flang.RefVal
	currentHops int

	// queue is the single FIFO. head is where the next delivery comes from;
	// draining pops from the front and appends to the back.
	//
	// An index rather than reslicing from the front: `queue = queue[1:]` gives
	// up the slot it steps past, so a queue that is drained every tick and
	// refilled every tick reallocated its whole backing array every tick. The
	// drain resets both to the start once it is empty, and outside a drain head
	// is always 0.
	queue []queuedMsg
	head  int
	// spill holds messages deferred past the hop cap or the per-tick budget,
	// carried into the next frame.
	spill []queuedMsg

	// state is per-object scratch, keyed by ref. Never saved; dropped on reload.
	state map[flang.RefVal]*flang.DictVal

	// dirtyBrushes are brushes whose geometry a property write invalidated.
	// Coalesced by ref, so writing a height a hundred times in one tick costs
	// one rebuild.
	dirtyBrushes map[flang.RefVal]bool
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
	fanout map[sendKey]int

	// hidden collects the screens that leave the stack through
	// ui.OnScreenHidden. Every refCache built reads it, so a screen ref still
	// resolves to one for the tick after it left — the tick its :hidden: and
	// :cancel: are delivered on — and ScriptTick lets go of each once that
	// tick's drain is done.
	hidden []*ui.Screen

	// running is the unit whose handler is on the stack, set for exactly as
	// long as one is. A verb that is itself random — hitscan's spread — draws
	// from that interpreter's generator through randomUnit below, so a seeded
	// run scatters its shots the same way twice. Nothing else needs it: a verb
	// reaches everything else it touches through rt.self and the host.
	running *scriptUnit

	// Scratch reused tick to tick rather than allocated per use — see where
	// each is filled. None of them outlives the call that fills it.
	refsBuf    []flang.RefVal // scriptedRefs
	moversBuf  []placedMover  // postOverlapEvents' movers
	levelUnits []*scriptUnit  // resolveTarget, for the level's several scripts
	overlap    overlapScratch // postOverlapEvents' containers, grid and set

	// brushScripts caches, per chunk, which of its brushes carry a script —
	// see scriptedBrushes.
	brushScripts []chunkScripted

	stats ScriptStats
}

// randomUnit is a draw in [0, 1) from the generator of whichever script is
// running — flang's, so `seed_random` pins it along with everything the script
// rolls for itself.
//
// Outside a handler there is no such generator (nothing calls a verb from
// there today, but a future caller would find nil rather than a panic), so it
// falls back to math/rand — unseeded and unrepeatable, which is the right
// answer for a draw nobody could have seeded anyway.
func (rt *ScriptRuntime) randomUnit() float64 {
	if rt.running != nil && rt.running.interp != nil {
		return rt.running.interp.RandomUnit()
	}
	return rand.Float64()
}

// Scripts is the engine-wide runtime. One per process, rebuilt when a level is
// loaded or replaced.
var Scripts = &ScriptRuntime{
	units:        map[string]*scriptUnit{},
	trees:        map[string]*treeUnit{},
	treeParams:   map[string][]string{},
	state:        map[flang.RefVal]*flang.DictVal{},
	dirtyBrushes: map[flang.RefVal]bool{},
	dirtyVolumes: map[string]bool{},
	dirtyTex:     map[string]bool{},
	warned:       map[flang.RefVal]bool{},
	warnedAt:     map[string]bool{},
	fanout:       map[sendKey]int{},
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
	rt.trees = map[string]*treeUnit{}
	rt.treeParams = map[string][]string{}
	rt.queue = nil
	rt.head = 0
	rt.spill = nil
	rt.brushScripts = nil
	rt.state = map[flang.RefVal]*flang.DictVal{}
	rt.dirtyBrushes = map[flang.RefVal]bool{}
	rt.dirtyVolumes = map[string]bool{}
	rt.dirtyTex = map[string]bool{}
	rt.warned = map[flang.RefVal]bool{}
	rt.warnedAt = map[string]bool{}
	rt.fanout = map[sendKey]int{}
	rt.self = flang.RefVal{}
	// A zoom a script left on (@level.prop.camera_fov) belongs to the session
	// that set it.
	gfx.FieldOfView = gfx.DefaultFieldOfView
	gfx.ViewmodelFieldOfView = gfx.DefaultViewmodelFieldOfView
	gfx.ResetRecoil()
	gfx.StopLookAt()
	// A camera a script detached, and a cursor it freed, likewise: play
	// ending with a cutscene camera up must not leave the editor looking
	// through it, or the next level starting with the mouse let go.
	gfx.ResetCameraRig()
	hooks.ScriptWantsCursor = false

	// Nothing may evaluate a binding against a level that is being replaced, or
	// load a script for a screen shown after the runtime has gone.
	ClearUIBindings(uiStack)
	ui.OnScreenShown = nil
	ui.OnScreenHidden = nil
	rt.hidden = nil
	InvalidateBindingCache()
	rt.stats = ScriptStats{}
}

// ScriptRuntimeLoad parses every script the level references. Diagnostics are
// logged and kept on the unit, so the script editor can show them.
func ScriptRuntimeLoad(lvl *world.Level) {
	ScriptRuntimeReset(lvl)
	if lvl == nil {
		return
	}
	// The player's behaviour comes from the level's player block, and its
	// attributes start over with each session — see world.ApplyPlayerScript
	// for why here and not at level load.
	world.ApplyPlayerScript(lvl)
	// Trees first: a script has to be linked against the parameters the tree
	// beside it declares, and that source has to exist before the link.
	Scripts.loadTrees()
	for _, path := range Scripts.scriptPaths() {
		Scripts.loadUnit(path)
	}

	// The UI's two connections to the runtime, both installed here so that they
	// come and go with the play session.
	//
	// A screen shown later by show_screen is not in the level's list, so
	// nothing would otherwise know to read its script. Loading on demand is
	// file I/O, which a tick may do — the thing it may not do is touch GL.
	InstallUIBindings(uiStack)
	ui.OnScreenShown = func(sc *ui.Screen) {
		if sc == nil {
			return
		}
		Scripts.loadUnit(sc.Script)
		sc.Root.Walk(func(_, n *ui.Node) bool {
			Scripts.loadUnit(n.Script)
			return true
		})
		Scripts.queueScreenTextures(sc)
	}
	// Screens the level put up before any of this was installed never passed
	// through OnScreenShown, so their images are asked for here instead.
	if uiStack != nil {
		for _, sc := range uiStack.Screens() {
			Scripts.queueScreenTextures(sc)
		}
	}
	// A screen leaving the stack is kept for the next tick, so the :hidden:
	// and :cancel: recorded as it left can still reach its script.
	ui.OnScreenHidden = func(sc *ui.Screen) {
		if sc != nil {
			Scripts.hidden = append(Scripts.hidden, sc)
		}
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
		add(rt.lvl.Player.Script)
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
		for i := range rt.lvl.Sounds {
			add(rt.lvl.Sounds[i].Script)
		}
		for i := range rt.lvl.Effects {
			add(rt.lvl.Effects[i].Script)
		}
		for ci := range rt.lvl.Chunks {
			ch := &rt.lvl.Chunks[ci]
			add(ch.Script)
			for bi := range ch.Brushes {
				add(ch.Brushes[bi].Script)
			}
		}
		for i := range rt.lvl.Lights.Directional {
			add(rt.lvl.Lights.Directional[i].Script)
		}
		for i := range rt.lvl.Lights.Points {
			add(rt.lvl.Lights.Points[i].Script)
		}
		// The screens the level puts up, and every scripted widget on them.
		// Read from disk rather than from the stack, because this runs at load
		// time, before play has begun and before anything has been shown.
		//
		// A screen a script opens later with show_screen is not in this list;
		// ui.OnScreenShown loads it on demand instead. Enumerating every .fui
		// on disk here would parse the whole UI folder for a level that uses
		// one screen out of it.
		for _, name := range rt.lvl.UI {
			sc, err := ui.LoadScreen(ui.UIPath(name))
			if err != nil {
				continue
			}
			add(sc.Script)
			sc.Root.Walk(func(_, n *ui.Node) bool { add(n.Script); return true })
		}
	}

	// And whatever is already up. Normally nothing is — entering play clears
	// the stack before the runtime loads — but a ScriptReload with a menu open
	// must not drop that menu's handlers, which is exactly when someone is
	// reloading scripts.
	if uiStack != nil {
		for _, sc := range uiStack.Screens() {
			add(sc.Script)
			sc.Root.Walk(func(_, n *ui.Node) bool { add(n.Script); return true })
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
	// An object with no script of its own is the ordinary case — nearly every
	// wall in a level, and nearly every widget on a screen — so callers hand
	// the empty path straight through rather than each testing for it.
	if path == "" {
		return nil
	}
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

	// The parameters of every decision tree that runs beside this script,
	// declared to it as though it had written them itself. See the header of
	// script_aitree.go for why this is the merge rather than a second table.
	for _, psrc := range rt.treeParams[path] {
		f, perrs := flang.Parse(path+" (ai tree params)", []byte(psrc))
		if len(perrs) == 0 {
			perrs = prog.Add(f)
		}
		u.errs = append(u.errs, perrs...)
	}

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

// markGeometryDirty queues a rebuild for whatever brush a ref belongs to.
func (rt *ScriptRuntime) markGeometryDirty(r flang.RefVal) {
	switch r.Kind {
	case flang.RefBrush:
		rt.dirtyBrushes[flang.RefVal{Kind: flang.RefBrush, A: r.A}] = true
	case flang.RefVolume:
		rt.dirtyVolumes[r.A] = true
	}
}

// markTextureDirty queues a texture for upload at the end of the frame.
//
// Deferred for the same reason a geometry rebuild is: LoadTexture is GL work
// and the tick runs before the frame's GL work. Coalesced by name, so a script
// flicking one wall between two textures every frame loads each once.
// queueScreenTextures asks for every image a screen paints with — a widget's
// own texture, its hover, press and disabled looks, and a bar's fill.
//
// Nothing else loads them. The UI draw only ever looks a texture up
// (uidraw's drawBackground), and one that was never uploaded draws as a flat
// block of its tint colour: a scope overlay, say, becomes a black rectangle
// over the whole view. Queued rather than loaded, because showing a screen
// happens during a tick, where GL must not be touched — DrainScriptWork loads
// them before the frame draws, the rule spawn_effect already follows.
func (rt *ScriptRuntime) queueScreenTextures(sc *ui.Screen) {
	if sc == nil {
		return
	}
	sc.Root.Walk(func(_, n *ui.Node) bool {
		rt.markTextureDirty(n.Style.Texture)
		for _, st := range []*ui.StateStyle{n.Style.Hover, n.Style.Press, n.Style.Disabled} {
			if st != nil {
				rt.markTextureDirty(st.Texture)
			}
		}
		rt.markTextureDirty(n.Value.FillTexture)
		return true
	})
}

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
			rt.fanout = map[sendKey]int{}
		}
		rt.fanout[sendKey{pos: pos, atom: atom}]++
	}
	rt.queue = append(rt.queue, queuedMsg{
		to: to, atom: atom, args: args, pos: pos,
		hops: rt.currentHops + 1, fromScript: fromScript, from: rt.self,
	})
	return nil
}

// sendKey identifies one `<-` in one tick: the line it is on and what it sent.
//
// A struct rather than the two spelled into a string, which cost a Sprintf and
// a concatenation on every send and every delivery that asked.
type sendKey struct {
	pos  flang.Pos
	atom string
}

// wasBroadcast reports whether a send reached more than one object, which is the
// difference between a mistake and the documented way to address a tag.
//
// A count of zero means the message spilled past the hop cap into a later tick,
// where the counts have been reset. Treated as a single send, because an
// unreported bug is worse than a warning about a broadcast that was cut short.
func (rt *ScriptRuntime) wasBroadcast(m queuedMsg) bool {
	return rt.fanout[sendKey{pos: m.pos, atom: m.atom}] > 1
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
	t := rt.resolveTarget(r)
	if t.level {
		return t.levelUnits
	}
	if t.unit != nil {
		return []*scriptUnit{t.unit}
	}
	return nil
}

// deliveryTarget is everything delivering one message to one object needs to
// know about it, looked up once.
//
// Delivery used to ask the same questions several times over — whether the
// object is alive, what script it runs (three times: for its units, for its
// tree's interpreter, and for which unit to draw random numbers from), what
// tree it carries — each answer a walk through the ref cache. One resolve per
// message answers all of them, on the stack.
type deliveryTarget struct {
	// level says the message is the level's, which goes to each of its
	// several scripts — levelUnits, the runtime's scratch.
	level      bool
	levelUnits []*scriptUnit
	// unit is the object's own script when it has a working one — what sees
	// the message, what a tree on it runs on, and what it draws its random
	// numbers from. Not a one-element slice: that made the whole target
	// escape to the heap, once per message.
	unit *scriptUnit
	// tree is the object's decision tree when it has a working one, with the
	// interpreter it runs on.
	tree   *treeUnit
	interp *flang.Interp
}

// resolveTarget answers deliveryTarget's questions for r. The caller has
// already decided r is alive.
func (rt *ScriptRuntime) resolveTarget(r flang.RefVal) (t deliveryTarget) {
	if r.Kind == flang.RefLevel {
		if rt.lvl == nil {
			return t
		}
		rt.levelUnits = rt.levelUnits[:0]
		for _, p := range rt.lvl.Scripts {
			if u := rt.units[p]; u != nil && !u.disabled && u.interp != nil {
				rt.levelUnits = append(rt.levelUnits, u)
			}
		}
		t.level, t.levelUnits = true, rt.levelUnits
		return t
	}
	var script, tree string
	if e := rt.host.entityFor(r); e != nil {
		script, tree = e.Script, e.AITree
	} else if d := rt.cache.scriptDataFor(r); d != nil {
		script = d.Script
	}
	if script != "" {
		if u := rt.units[script]; u != nil && !u.disabled && u.interp != nil {
			t.unit = u
		}
	}
	if tree != "" && r.Kind == flang.RefEntity {
		if tu := rt.loadTree(tree); tu != nil && !tu.disabled && tu.prog != nil {
			// The tree runs on the object's own script interpreter when it has
			// one, and on the tree's bare one when it does not — see
			// script_aitree.go's header.
			in := tu.interp
			if t.unit != nil {
				in = t.unit.interp
			}
			if in != nil {
				t.tree, t.interp = tu, in
			}
		}
	}
	return t
}

// answers reports whether anything on r — a script, or a decision tree — has a
// handler for atom. What lets an engine event nobody listens for go unposted.
func (rt *ScriptRuntime) answers(r flang.RefVal, atom string) bool {
	t := rt.resolveTarget(r)
	if t.tree != nil && t.tree.prog.Events[atom] != nil {
		return true
	}
	if t.unit != nil && t.unit.interp.Answers(atom) {
		return true
	}
	for _, u := range t.levelUnits {
		if u.interp.Answers(atom) {
			return true
		}
	}
	return false
}

// count is how many scripts see a message to this target, and at the i-th.
func (t *deliveryTarget) count() int {
	if t.level {
		return len(t.levelUnits)
	}
	if t.unit != nil {
		return 1
	}
	return 0
}

func (t *deliveryTarget) at(i int) *scriptUnit {
	if t.level {
		return t.levelUnits[i]
	}
	return t.unit
}

// alive is host.Alive with the entity it found, so delivery does not have to
// look an entity up twice.
func (rt *ScriptRuntime) aliveTarget(r flang.RefVal) bool {
	if r.Kind == flang.RefEntity {
		return rt.host.entityFor(r) != nil
	}
	return rt.host.Alive(r)
}

// deliver runs one message's handler.
func (rt *ScriptRuntime) deliver(m queuedMsg) {
	if !rt.aliveTarget(m.to) {
		// The object was destroyed between the send and the delivery, which is
		// ordinary. Warn once per target so a repeated send does not fill the log.
		//
		// Only for a message a SCRIPT sent, on the same reading as the "nothing
		// handled this" warning below: an engine event is posted to everything
		// in the level whether or not it will still be there when the queue
		// drains, so a grenade that goes off during the drain of the very tick
		// it was posted a `:tick:` for is not a mistake anyone made — and a
		// game that throws grenades would otherwise file a line for every one
		// of them.
		if m.fromScript && !rt.warned[m.to] {
			rt.warned[m.to] = true
			ScriptLog.Warnf(rt.scriptFor(m.to),
				"dropped :%s: for %s, which no longer exists", m.atom, m.to)
		}
		rt.stats.Dropped++
		return
	}
	t := rt.resolveTarget(m.to)
	if t.count() == 0 && t.tree == nil {
		if m.fromScript && !rt.wasBroadcast(m) {
			rt.warnFor(rt.scriptFor(m.from), m.pos,
				"%s received :%s: but %s", m.to, m.atom, noHandlerReason(rt, m.to))
		}
		rt.stats.Dropped++
		return
	}

	// One defer for the three, rather than one each.
	prevSelf, prevHops, prevRunning := rt.self, rt.currentHops, rt.running
	rt.self, rt.currentHops = m.to, m.hops
	defer func() { rt.self, rt.currentHops, rt.running = prevSelf, prevHops, prevRunning }()

	anyMatched := false

	// The decision tree first — see script_aitree.go. A tree that answers this
	// event has decided before the script's own clause for it runs, which is
	// what makes the script's handler a consequence of the decision rather
	// than a race with it.
	if t.tree != nil && t.tree.prog.Events[m.atom] != nil {
		// Whatever interpreter the tree is running on is the one a verb should
		// draw at random from, so rt.running names the script backing it (nil
		// for a tree with no script, which draws from its own).
		rt.running = t.unit
		t.interp.ResetSteps()
		handled, err := rt.fireTree(t.tree, t.interp, m)
		switch {
		case err != nil:
			// A run that failed *was* the handler, on deliver's own rule: the
			// author's mistake is the error, not a missing clause.
			anyMatched = true
			rt.recordTreeFailure(t.tree, err, m)
		case handled:
			anyMatched = true
			rt.stats.Delivered++
		}
	}

	for i, n := 0, t.count(); i < n; i++ {
		u := t.at(i)
		rt.running = u
		u.interp.ResetSteps()
		_, matched, err := u.interp.CallAtom(m.atom, m.args, m.pos)
		if err != nil {
			// A clause that matched and then failed *was* the handler. Leaving
			// anyMatched false filed the "defines no clause" warning below on
			// top of the real error, which contradicts it and sends the author
			// looking for a clause that is sitting right there.
			anyMatched = true
			rt.recordFailure(u, err, m)
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
			"nothing handled :%s: sent to %s: %s no clause whose first parameter is :%s:%s",
			m.atom, m.to, handlerSourceOf(rt, m.to), m.atom, treeRootHint(rt, m.to, m.atom))
	}
}

// recordFailure logs a script error and disables the script if it keeps
// happening. A handler erroring every frame fills the log and slows the game
// without ever telling the author anything new.
//
// The entry names the handler and says that the rest of it was skipped. The
// failing line is rarely the problem anyone notices: level1's :skybox: handler
// failed on a light write, and what went missing was the ground, which the
// handler would have switched on fifteen lines further down.
func (rt *ScriptRuntime) recordFailure(u *scriptUnit, err error, m queuedMsg) {
	rt.stats.Errors++
	u.failures++
	stopped := fmt.Sprintf("the :%s: handler on %s stopped here, and nothing after this line in it ran", m.atom, m.to)
	// A runtime error knows where it happened; carrying the position through is
	// what lets the window open the script at the offending line.
	if re, ok := err.(*flang.RuntimeError); ok {
		ScriptLog.ErrorAt(u.path, re.Pos, "%s; %s", re.Msg, stopped)
	} else {
		ScriptLog.Errorf(u.path, "%v; %s", err, stopped)
	}
	if u.failures >= scriptMaxErrorsPerScript {
		u.disabled = true
		ScriptLog.Warnf(u.path, "disabled after %d errors; fix it and reload", u.failures)
	}
}

// drain delivers queued messages until nothing is left, or a budget stops it.
func (rt *ScriptRuntime) drain() {
	delivered := 0
	for rt.head < len(rt.queue) {
		if delivered >= scriptMaxMessagesPerTick {
			// Spill the rest rather than dropping it: a fan-out that overran the
			// budget still means to happen, just not all in one frame.
			rt.spill = append(rt.spill, rt.queue[rt.head:]...)
			rt.clearQueue()
			ScriptLog.Warnf("", "message budget of %d reached; %d deferred to next frame",
				scriptMaxMessagesPerTick, len(rt.spill))
			return
		}
		m := rt.queue[rt.head]
		// Let go of what the slot held, so a message's arguments are not kept
		// alive by a backing array that is about to be reused.
		rt.queue[rt.head] = queuedMsg{}
		rt.head++

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
	rt.clearQueue()
}

// clearQueue empties the queue and keeps its storage for the next tick.
func (rt *ScriptRuntime) clearQueue() {
	clear(rt.queue)
	rt.queue = rt.queue[:0]
	rt.head = 0
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
	rt.syncCache(lvl)
	rt.stats = ScriptStats{}
	clear(rt.fanout)

	// Screens that left the stack before this tick are the ones whose :hidden:
	// and :cancel: it delivers — see ScriptRuntime.hidden.
	departed := len(rt.hidden)

	// Messages deferred last frame go first, so a spilled cascade continues in
	// the order it was cut off.
	rt.queue = append(rt.queue, rt.spill...)
	rt.spill = rt.spill[:0]

	rt.currentHops = -1
	ScriptEventsTick(rt, lvl, dt)
	rt.drain()

	// Delivered now. A screen that left during this drain stays for the next
	// tick, whose events it is; copied so the ones let go are not kept alive
	// by the slice's backing array.
	rt.hidden = append([]*ui.Screen(nil), rt.hidden[departed:]...)
}

// noHandlerReason says why a targeted send reached no script at all.
func noHandlerReason(rt *ScriptRuntime, r flang.RefVal) string {
	script, tree := rt.scriptFor(r), rt.aiTreeFor(r)
	if script == "" && tree == "" {
		return "it has no script and no decision tree attached"
	}
	if script == "" {
		return "its decision tree did not load, or has been disabled after repeated errors"
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
// same reason DrainTexEditorRequest exists.
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

	if len(rt.dirtyBrushes) > 0 {
		// The runtime's own cache when it is on this level, synced; one of its
		// own otherwise.
		cache := rt.cache
		if cache != nil && cache.lvl == lvl {
			cache.sync(lvl)
		} else {
			cache = buildRefCache(lvl)
		}
		// Chunk-level work is collected rather than done per brush:
		// RecomputeBounds covers the whole chunk, so running it once per dirty
		// brush would repeat the same work for every brush in it.
		dirtyChunks := map[*world.Chunk]bool{}

		for r := range rt.dirtyBrushes {
			ch, b := cache.brushOwnerFor(r)
			if ch != nil && b != nil {
				b.EachFace(func(f *world.BrushFace) {
					for _, tex := range []string{f.Texture, f.BlendTexture, f.BlendMask} {
						if tex != "" {
							_ = gfx.EnsureTextureLoaded(tex)
						}
					}
				})
				_ = b.Load()
				dirtyChunks[ch] = true
			}
			delete(rt.dirtyBrushes, r)
		}

		for ch := range dirtyChunks {
			// The chunk's bounds are what FindChunkSH tells stacked chunks
			// apart by. Geometry a script moved past the recorded maximum
			// would stop being found at all — the player would walk onto a
			// lift and fall straight through it.
			ch.RecomputeBounds()
		}
		// A brush's own verts are not something a script can currently write
		// (brushProps exposes only `solid`), so the chunk footprint the
		// spatial hash is keyed on cannot have moved; no RebuildIndexes here.
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
	_, err := RunScriptValue(lvl, name, src, entry, nil)
	return err
}

// RunScriptValue is RunScriptOnce with the two things a read-eval-print loop
// needs that a fire-and-forget run does not: what the call evaluated to, and
// somewhere of its own to put the script's `log()` output.
//
// out nil sends output to the engine log, which is where RunScriptOnce's
// callers want it. The developer console (internal/devui) passes its own sink,
// because a line typed into a console should answer in that console — and it
// tails the engine log as well, so sending it there instead would print
// everything twice.
func RunScriptValue(lvl *world.Level, name, src, entry string, out func(string)) (flang.Value, error) {
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
		return nil, fmt.Errorf("%s", errs.Error())
	}
	in, ierrs := flang.NewInterp(prog, rt.host, flang.Limits{})
	if len(ierrs) > 0 {
		return nil, fmt.Errorf("%s", ierrs.Error())
	}
	if out != nil {
		in.SetOutput(out)
	} else {
		in.SetOutput(func(s string) { rt.logLine(s) })
	}

	if !in.Has(entry) {
		return nil, fmt.Errorf("%s defines no %s()", name, entry)
	}
	v, err := in.Call(entry, nil, flang.Pos{File: name})
	if err != nil {
		return nil, err
	}
	// Whatever the run dirtied still needs its geometry rebuilt, and there is no
	// tick coming to do it.
	DrainScriptWork(lvl)
	return v, nil
}
