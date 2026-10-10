package script

// script_events.go — turning what the engine just did into messages.
//
// Every event here is derived rather than pushed: the engine does not know about
// scripts, and nothing in EntityUpdate or the collision resolver calls into this
// file. Once a frame, after the world has settled, this looks at what changed
// and posts the corresponding atoms.
//
// The `enter`/`exit` pair is the interesting one. Neither is a thing the engine
// computes — what it has is "who is inside what, now". The events are the
// *difference* between that set and last frame's, which is why the runtime keeps
// the previous set around and diffs it. Doing it any other way would mean the
// collision code carrying event-shaped state it has no use for.

import (
	"math"
	"reflect"
	"slices"

	"foundry/internal/ai"
	"foundry/internal/flang"
	"foundry/internal/hooks"
	"foundry/internal/platform"
	"foundry/internal/ui"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// overlapPair is one "A contains B" relationship, by ID rather than by ref so
// the set survives the level being rebuilt underneath it.
type overlapPair struct {
	container flang.RefVal
	other     flang.RefVal
}

// scriptEventState is what deriving events needs to remember between frames.
type scriptEventState struct {
	// overlaps is last frame's containment set. `enter` is what appeared in it
	// this frame and `exit` is what left.
	overlaps map[overlapPair]bool
	// spawned tracks which objects have had `:spawn:` posted, so it fires once
	// per object per session rather than every frame.
	spawned map[flang.RefVal]bool
	// timers are pending `after(...)` messages.
	timers []scriptTimer
	// elapsed is play time in seconds, which is what `time()` returns. It
	// stands still while a script has the game paused.
	elapsed float64
	// stackElapsed is the screen stack's clock, which a pause does not stop.
	// A timer addressed to a screen or a widget counts against it: a menu's
	// own `after` has to come due under the pause the menu is sitting over.
	stackElapsed float64
	// interactHeld tracks the interact key, so `:interact:` fires on the press
	// rather than every frame the key is down.
	interactHeld bool
	// paused is `@level.prop.paused`. Kept here rather than on the runtime so
	// that ScriptEventsReset lifts it — a level load, entering or leaving play
	// — since a pause outliving the menu that set it would leave nothing on
	// screen able to lift it. See script_pause.go.
	paused bool
	// checkpoints are the checkpoints the player has reached this play-through,
	// by name — a checkpoint volume's ID or `checkpoint`'s argument. Part of a
	// save, so a restore does not reach them again. See reachCheckpoint.
	checkpoints map[string]bool
	// grounded is the player's footing as the last tick left it, and
	// groundedKnown says whether there has been a tick to leave one.
	// `:landed:` and `:left_ground:` are the edges between this and what the
	// engine reports now — derived rather than posted by whoever changed it,
	// because walking off a ledge has nothing that could post it.
	grounded      bool
	groundedKnown bool
}

type scriptTimer struct {
	due  float64
	to   flang.RefVal
	atom string
	args []flang.Value
	pos  flang.Pos
	// onStack says the timer counts against stackElapsed rather than play
	// time, because it is addressed to a screen or a widget.
	onStack bool
}

// A timer is always a script send — `after` can only be called from a handler —
// so postTimerEvents says so explicitly rather than letting post infer it from
// rt.self, which is no longer set by the time the timer comes due.
const timerIsScriptSend = true

var scriptEvents = newScriptEventState()

func newScriptEventState() scriptEventState {
	return scriptEventState{
		overlaps:    map[overlapPair]bool{},
		spawned:     map[flang.RefVal]bool{},
		checkpoints: map[string]bool{},
	}
}

// forgetNamesake drops everything this ref's last occupant left behind, so that
// a new object handed its name starts the way any other new object does.
//
// A name is freed the moment the object holding it is destroyed, and `spawn`
// hands out the first free name of a type — so the grenade thrown after one has
// gone off is `grenade` again, and so is the one after that. Three pieces of
// per-object bookkeeping are keyed by that name and would otherwise carry
// across:
//
//   - `spawned`, which is what makes `:spawn:` fire once per object rather than
//     every frame. Inherited, the new grenade never hears `:spawn:` at all —
//     its fuse is never lit, and it lies on the floor forever.
//   - `overlaps`, last frame's containment. Inherited, an object born inside
//     the volume its namesake died in is never seen to enter it.
//   - pending timers. Inherited, the dead one's `after(2500, :detonate:)`
//     lands on the live one, which goes off early through no fault of its own.
//
// Called from `spawn` rather than from `destroy`, so that an object's own
// `:destroy:` handler — which is delivered after the removal — still has all
// of it.
func forgetNamesake(r flang.RefVal) {
	delete(scriptEvents.spawned, r)
	for p := range scriptEvents.overlaps {
		if p.container == r || p.other == r {
			delete(scriptEvents.overlaps, p)
		}
	}
	kept := scriptEvents.timers[:0]
	for _, t := range scriptEvents.timers {
		if t.to != r {
			kept = append(kept, t)
		}
	}
	scriptEvents.timers = kept
}

// ScriptEventsReset clears the derived-event state. Called whenever the level is
// replaced: the previous frame's overlaps describe objects that no longer exist.
func ScriptEventsReset() {
	scriptEvents = newScriptEventState()
	// The engine's side of the footing too, or a player who was mid-jump when
	// the level ended lands in the first frame of the next one.
	hooks.ResetPlayerGround()
	// The AI controllers start their rounds again at the same moments: a guard
	// resuming a patrol from a play session that has ended, or chasing a
	// player from a level that has been replaced, would be chasing a ghost.
	ai.ResetSession()
	hooks.TakeAIEvents()
	// A press made on the last level, or a hover over something in it, must
	// not arrive on this one.
	resetPointerTrack()
	// Likewise a key press, though a key still held stays held.
	platform.AdvanceKeys(0)
}

// ScriptEventsTick derives and posts one frame's events.
//
// While a script has the game paused only the screen stack's half runs — see
// script_pause.go. The stack's clock advances either way; play time does not.
func ScriptEventsTick(rt *ScriptRuntime, lvl *world.Level, dt float32) {
	scriptEvents.stackElapsed += float64(dt)
	postKeyEvents(rt, lvl, dt, scriptEvents.paused)
	postSongEvents(rt, lvl, scriptEvents.paused)
	if scriptEvents.paused {
		postPausedEvents(rt, dt)
		return
	}
	scriptEvents.elapsed += float64(dt)

	// Walked once for both uses: nothing between the two is delivered, so the
	// set of scripted objects cannot change in between.
	refs := scriptedRefs(rt, lvl)
	postSpawnEvents(rt, refs)
	postOverlapEvents(rt, lvl)
	postCollideEvents(rt)
	postAnimEvents(rt)
	postWaypointEvents(rt)
	postAIEvents(rt)
	postVehicleEvents(rt)
	postInteractEvent(rt, lvl)
	postWeaponInputEvents(rt)
	postPlayerGroundEvents(rt)
	postUIEvents(rt)
	postPointerEvents(rt, lvl)
	postTimerEvents(rt)
	postTickEvents(rt, refs, dt)
}

// postWaypointEvents fires `:waypoint:` as an object reaches each point of the
// waypoint animation it is travelling, and `:waypoints_end:` once it reaches
// the last and stops following it.
//
// Posted to the object, with the animation as an argument, on the shape
// `:anim_end:` has: the thing that moved hears about its own motion, and one
// handler can serve several paths by looking at which one arrived.
//
// A point's own events follow its `:waypoint:`, in the order the point lists
// them, each as `(:atom:, animation ref)` — the atom is the author's, so the
// only thing left to say is which path it came from.
func postWaypointEvents(rt *ScriptRuntime) {
	for _, ev := range hooks.TakeWaypointEvents() {
		src := flang.RefVal{Kind: waypointRefKind(ev.Kind), A: ev.Object}
		anim := flang.RefVal{Kind: flang.RefWaypoints, A: ev.Animation}
		if ev.Ended {
			_ = rt.post(src, "waypoints_end", []flang.Value{src, anim}, flang.Pos{})
			continue
		}
		_ = rt.post(src, "waypoint", []flang.Value{src, anim, flang.IntVal(ev.Index)}, flang.Pos{})
		for _, atom := range ev.Events {
			_ = rt.post(src, atom, []flang.Value{anim}, flang.Pos{})
		}
	}
}

// waypointRefKind is the ref kind that names an object of a waypoint event's
// kind.
func waypointRefKind(k hooks.WaypointObject) flang.Kind_ {
	switch k {
	case hooks.WaypointEntity:
		return flang.RefEntity
	case hooks.WaypointSprite:
		return flang.RefSprite
	case hooks.WaypointEmitter:
		return flang.RefEmitter
	case hooks.WaypointSound:
		return flang.RefSound
	case hooks.WaypointLight:
		return flang.RefLight
	case hooks.WaypointVolume:
		return flang.RefVolume
	}
	return flang.RefBrush
}

// scriptedRefs returns every object in the level that has a script, so the
// per-object events do not have to walk objects that could not react anyway.
//
// The slice is the runtime's scratch, refilled each call: ScriptEventsTick
// uses it for :spawn: and :tick: and lets go of it before anything is
// delivered.
func scriptedRefs(rt *ScriptRuntime, lvl *world.Level) []flang.RefVal {
	if lvl == nil {
		return nil
	}
	out := rt.refsBuf[:0]
	// The player is engine-owned and not in Entities, so it is asked for by
	// name; its script comes from the level's player block.
	if world.PlayerEntity != nil && (world.PlayerEntity.Script != "" || world.PlayerEntity.AITree != "") {
		out = append(out, flang.RefVal{Kind: flang.RefEntity, A: "player"})
	}
	// An entity with a decision tree and no script of its own is scripted as
	// far as the runtime is concerned: :spawn: and :tick: have to reach it, or
	// a tree whose only root is one of them would never run.
	for i := range lvl.Entities {
		if lvl.Entities[i].Script != "" || lvl.Entities[i].AITree != "" {
			out = append(out, flang.RefVal{Kind: flang.RefEntity, A: lvl.Entities[i].ID})
		}
	}
	for i := range lvl.Volumes {
		if lvl.Volumes[i].Script != "" {
			out = append(out, flang.RefVal{Kind: flang.RefVolume, A: lvl.Volumes[i].ID})
		}
	}
	for i := range lvl.Sprites {
		if lvl.Sprites[i].Script != "" {
			out = append(out, flang.RefVal{Kind: flang.RefSprite, A: lvl.Sprites[i].ID})
		}
	}
	for i := range lvl.Emitters {
		if lvl.Emitters[i].Script != "" {
			out = append(out, flang.RefVal{Kind: flang.RefEmitter, A: lvl.Emitters[i].ID})
		}
	}
	for i := range lvl.Sounds {
		if lvl.Sounds[i].Script != "" {
			out = append(out, flang.RefVal{Kind: flang.RefSound, A: lvl.Sounds[i].ID})
		}
	}
	for i := range lvl.Effects {
		if lvl.Effects[i].Script != "" {
			out = append(out, flang.RefVal{Kind: flang.RefEffect, A: lvl.Effects[i].ID})
		}
	}
	for i := range lvl.Decals {
		if lvl.Decals[i].Script != "" {
			out = append(out, flang.RefVal{Kind: flang.RefDecal, A: lvl.Decals[i].ID})
		}
	}
	scripted := rt.scriptedBrushes(lvl)
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if ch.Script != "" {
			out = append(out, flang.RefVal{Kind: flang.RefChunk, A: ch.ID})
		}
		// A switched-off chunk keeps its own script — that is how one can turn
		// itself back on — but nothing inside it fires. Its brushes describe
		// geometry that is not loaded and not in the spatial hash, so a
		// `:tick:` there would be reacting to a room that is not in the world.
		// See world/chunk_enable.go.
		if !ch.IsEnabled() {
			continue
		}
		for _, bi := range scripted[ci].idx {
			out = append(out, flang.RefVal{Kind: flang.RefBrush, A: ch.Brushes[bi].ID})
		}
	}
	for i := range lvl.Lights.Directional {
		if lvl.Lights.Directional[i].Script != "" {
			out = append(out, flang.RefVal{Kind: flang.RefLight, A: lvl.Lights.Directional[i].ID})
		}
	}
	for i := range lvl.Lights.Points {
		if lvl.Lights.Points[i].Script != "" {
			out = append(out, flang.RefVal{Kind: flang.RefLight, A: lvl.Lights.Points[i].ID})
		}
	}
	if len(lvl.Scripts) > 0 {
		out = append(out, flang.RefVal{Kind: flang.RefLevel})
	}
	out = appendScriptedUIRefs(out)
	rt.refsBuf = out
	return out
}

// chunkScripted is which of one chunk's brushes carry a script, and what the
// chunk's brush list looked like when that was worked out.
type chunkScripted struct {
	first *world.Brush // &Brushes[0], which names the backing array
	n     int
	gen   uint64 // world.BrushMeshGeneration() at the time
	idx   []int32
}

// scriptedBrushes returns, per chunk, the indices of its brushes that carry a
// script.
//
// Brushes are most of a level — thousands of them — and almost none have a
// script, so asking each one every frame was a walk of the whole level to find
// a handful. A chunk's answer is kept while its brush list is the same list:
// the same backing array, the same length, and no mesh edit, load or unload
// anywhere since (BrushMeshGeneration). A brush's script is only ever set by
// loading it — nothing in play writes one — and every load either replaces the
// slice or bumps the generation; ScriptRuntimeReset drops the whole cache too.
func (rt *ScriptRuntime) scriptedBrushes(lvl *world.Level) []chunkScripted {
	gen := world.BrushMeshGeneration()
	if len(rt.brushScripts) != len(lvl.Chunks) {
		rt.brushScripts = append(rt.brushScripts[:0], make([]chunkScripted, len(lvl.Chunks))...)
	}
	for ci := range lvl.Chunks {
		bs := lvl.Chunks[ci].Brushes
		cs := &rt.brushScripts[ci]
		var first *world.Brush
		if len(bs) > 0 {
			first = &bs[0]
		}
		if cs.first == first && cs.n == len(bs) && cs.gen == gen && (first != nil || cs.n == 0) && cs.idx != nil {
			continue
		}
		cs.first, cs.n, cs.gen = first, len(bs), gen
		cs.idx = cs.idx[:0]
		if cs.idx == nil {
			cs.idx = []int32{}
		}
		for bi := range bs {
			if bs[bi].Script != "" {
				cs.idx = append(cs.idx, int32(bi))
			}
		}
	}
	return rt.brushScripts
}

// scriptedUIRefs is the screen-stack half of scriptedRefs.
//
// Only screens that are up, and only the ones with a script. A menu sitting in
// resources/ui that nobody has opened must not be receiving :tick: sixty times
// a second — that is a bug, not a feature, and it is the difference between a
// screen stack and a list of files.
func scriptedUIRefs() []flang.RefVal { return appendScriptedUIRefs(nil) }

// appendScriptedUIRefs is scriptedUIRefs appending to out.
func appendScriptedUIRefs(out []flang.RefVal) []flang.RefVal {
	if uiStack == nil {
		return out
	}
	for _, s := range uiStack.Screens() {
		if s.Script != "" {
			out = append(out, flang.RefVal{Kind: flang.RefScreen, A: s.ID})
		}
		screen := s
		s.Root.Walk(func(_, n *ui.Node) bool {
			if n.Script != "" {
				out = append(out, flang.RefVal{Kind: flang.RefWidget, A: screen.ID, B: n.ID})
			}
			return true
		})
	}
	return out
}

// postSpawnEvents fires `:spawn:` once per scripted object in refs. An object
// left out — a level object while the game is paused — has not been spawned,
// and hears it on the first tick it is included.
func postSpawnEvents(rt *ScriptRuntime, refs []flang.RefVal) {
	for _, r := range refs {
		if scriptEvents.spawned[r] {
			continue
		}
		scriptEvents.spawned[r] = true
		_ = rt.post(r, "spawn", nil, flang.Pos{})
	}
}

// postTickEvents fires `:tick:` on every scripted object in refs that has a
// handler for it, with the frame time.
//
// An object whose script and tree have no `:tick:` clause is not sent one. It
// used to be, and the message then travelled the whole delivery path to be
// found unanswered — the common case, since most scripts react to events
// rather than to frames. Nothing observable rides on the unanswered message: an
// engine event that no clause takes is neither warned about nor counted as
// delivered. (The one corner it reaches is a destroyed object's namesake,
// spawned mid-drain, which no longer inherits a :tick: its predecessor would
// have ignored.)
//
// Every :tick: shares one argument slice. Nothing writes to a message's
// arguments: CallAtom copies them into the clause's own bindings, and a tree
// binds them by name.
func postTickEvents(rt *ScriptRuntime, refs []flang.RefVal, dt float32) {
	var args []flang.Value
	for _, r := range refs {
		if !rt.answers(r, "tick") {
			continue
		}
		if args == nil {
			args = []flang.Value{flang.FloatVal(dt)}
		}
		_ = rt.post(r, "tick", args, flang.Pos{})
	}
}

// placedMover is one thing whose position can trigger a containment event —
// the player, or an entity — and where it is, in world space.
//
// World space specifically: an entity riding in a frame stores its position in
// that frame's coordinates, and comparing those against a container somewhere
// else in the level would be subtracting two numbers that do not describe the
// same space. Every containment test below is in world coordinates for that
// reason.
type placedMover struct {
	ref flang.RefVal
	pos mgl32.Vec3
}

// placeMovers fills the runtime's scratch with every mover, the player first
// and then the entities in level order — the order enter events go out in.
//
// Walked in the level's own order rather than looked up one by one: FindEntity
// is a walk of the entities, so asking it for every mover was quadratic in the
// entity count.
func placeMovers(rt *ScriptRuntime, lvl *world.Level) []placedMover {
	out := rt.moversBuf[:0]
	if world.PlayerEntity != nil {
		out = append(out, placedMover{flang.RefVal{Kind: flang.RefEntity, A: "player"}, lvl.Frames.WorldPosition(world.PlayerEntity)})
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		out = append(out, placedMover{flang.RefVal{Kind: flang.RefEntity, A: e.ID}, lvl.Frames.WorldPosition(e)})
	}
	rt.moversBuf = out
	return out
}

// overlapContainer is one thing that can contain a mover: a volume, or a
// scripted entity with bounds. A struct rather than a closure per container per
// frame, which is what the test used to be.
type overlapContainer struct {
	ref flang.RefVal
	// vol is set for a volume; ent, entFrame, centre and reach2 for an entity.
	vol *world.Volume
	// volFrame and volInv carry a world point into an attached volume's own
	// space exactly as Volume.LocalPoint does — worked out once per frame
	// instead of once per mover: LocalPoint finds the carrier by walking the
	// level's entities, and inverts its matrix, on every call.
	volMode  volSpace
	volFrame *world.Frame
	volInv   mgl32.Mat4

	ent      *world.Entity
	entFrame *world.Frame
	centre   mgl32.Vec3
	reach2   float32

	scripted bool
	// loadLevel is the level this container is an exit to, or empty. See
	// world.Volume.LoadLevel — it is why a container can be worth walking
	// even when nothing is listening for its `:enter:`.
	loadLevel string
	// checkpoint says the container is a checkpoint volume — see
	// world.Volume.Checkpoint — which likewise needs walking unscripted.
	checkpoint bool
	// carrier is the entity a volume rides, which it does not report as
	// entering it. A guard's sight cone is centred on the guard; telling its
	// script "the guard came into view" the moment the level starts, and on
	// every frame the guard walks, is noise nobody wants a handler for.
	carrier string

	// lo and hi bound, in world space, every point the container could
	// possibly contain — the broad phase. empty says it contains nothing at
	// all (a disabled or unbounded volume).
	lo, hi mgl32.Vec3
	empty  bool
}

// volSpace is how a volume's test carries a world point into its own space,
// mirroring the three arms of Volume.LocalPoint.
type volSpace uint8

const (
	volWorld  volSpace = iota // rides nothing: the point as it is
	volFrame                  // rides a frame: through the frame's inverse
	volMatrix                 // rides an entity or a bone: through volInv
)

// contains is the exact containment test — the same arithmetic the closures
// it replaced did, so the same answers.
func (c *overlapContainer) contains(p mgl32.Vec3) bool {
	if c.vol != nil {
		switch c.volMode {
		case volFrame:
			p = c.volFrame.FromWorld(p)
		case volMatrix:
			r := c.volInv.Mul4x1(p.Vec4(1))
			p = mgl32.Vec3{r.X(), r.Y(), r.Z()}
		}
		return c.vol.ContainsPoint(p)
	}
	local := c.entFrame.FromWorld(p)
	if local.Sub(c.centre).LenSqr() > c.reach2 {
		return false
	}
	return c.ent.ContainsPoint(local)
}

// mayContain is the broad phase: false means contains(p) is certainly false.
func (c *overlapContainer) mayContain(p mgl32.Vec3) bool {
	return p.X() >= c.lo.X() && p.X() <= c.hi.X() &&
		p.Y() >= c.lo.Y() && p.Y() <= c.hi.Y() &&
		p.Z() >= c.lo.Z() && p.Z() <= c.hi.Z()
}

// boundPad widens a broad-phase box past any rounding the exact test could do
// differently from it. Generous on purpose — the box only decides which movers
// are worth the exact test, so too large costs a little time and too small
// would lose an event.
func boundPad(centre mgl32.Vec3, radius float32) float32 {
	m := radius
	for i := 0; i < 3; i++ {
		if a := geom32Abs(centre[i]); a > m {
			m = a
		}
	}
	return 0.05 + m*1e-4
}

func geom32Abs(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

// linearBound is an upper bound on how far the matrix can stretch a vector —
// the Frobenius norm of its 3x3 part, which is never less than the largest
// singular value whatever rotation, scale or shear the matrix holds.
func linearBound(m mgl32.Mat4) float32 {
	var sum float32
	for col := 0; col < 3; col++ {
		for row := 0; row < 3; row++ {
			v := m.At(row, col)
			sum += v * v
		}
	}
	return float32(math.Sqrt(float64(sum)))
}

// boxAround sets the container's broad-phase box to a world-space sphere's.
func (c *overlapContainer) boxAround(centre mgl32.Vec3, radius float32) {
	r := radius + boundPad(centre, radius)
	if r != r || r > 1e30 {
		// A NaN or runaway transform: no box, so every mover is tested, as
		// they all were before there was a broad phase.
		c.lo = mgl32.Vec3{float32(math.Inf(-1)), float32(math.Inf(-1)), float32(math.Inf(-1))}
		c.hi = mgl32.Vec3{float32(math.Inf(1)), float32(math.Inf(1)), float32(math.Inf(1))}
		return
	}
	c.lo = centre.Sub(mgl32.Vec3{r, r, r})
	c.hi = centre.Add(mgl32.Vec3{r, r, r})
}

// overlapScratch is postOverlapEvents' working storage, kept on the runtime so
// a frame allocates nothing: the containers, the mover grid, a candidate list,
// and the two containment sets it swaps between.
type overlapScratch struct {
	containers []overlapContainer
	grid       moverGrid
	cand       []int32
	// sets are this runtime's own containment sets. Each frame fills the one
	// that is not scriptEvents.overlaps and installs it; a set it did not make
	// (a reset, a save restore) is never cleared or reused, so nothing else
	// holding one sees it change.
	sets [2]map[overlapPair]bool
}

// moverGrid buckets movers by a cell of the XZ plane, so a container is tested
// against the movers near it rather than every mover in the level.
type moverGrid struct {
	cells   map[[2]int32]int32 // cell -> bucket
	keys    [][2]int32         // bucket -> cell
	buckets [][]int32          // bucket -> mover indices, ascending
	used    int
}

// moverCellSize is the grid's cell edge, in world units: a few bodies wide, so
// a trigger or a soldier's bounds spans a handful of cells.
const moverCellSize = 6

// moverCellCoord is the cell a coordinate falls in, clamped so a wild position
// cannot overflow, and with NaN filed under 0 — such a mover is never contained
// anyway, and only needs to be somewhere.
func moverCellCoord(f float32) int32 {
	c := math.Floor(float64(f) / moverCellSize)
	switch {
	case c != c:
		return 0
	case c < -1e9:
		return -1e9
	case c > 1e9:
		return 1e9
	}
	return int32(c)
}

// build files every mover in its cell.
func (g *moverGrid) build(movers []placedMover) {
	if g.cells == nil {
		g.cells = map[[2]int32]int32{}
	}
	clear(g.cells)
	for i := 0; i < g.used; i++ {
		g.buckets[i] = g.buckets[i][:0]
	}
	g.used = 0
	for i := range movers {
		k := [2]int32{moverCellCoord(movers[i].pos.X()), moverCellCoord(movers[i].pos.Z())}
		b, ok := g.cells[k]
		if !ok {
			b = int32(g.used)
			g.used++
			if int(b) == len(g.buckets) {
				g.buckets = append(g.buckets, nil)
				g.keys = append(g.keys, k)
			}
			g.keys[b] = k
			g.cells[k] = b
		}
		g.buckets[b] = append(g.buckets[b], int32(i))
	}
}

// query appends to out the index of every mover in a cell the box touches, in
// ascending order — the movers' own order, which is the order enter events go
// out in. ok is false when the box covers so much of the grid that testing
// every mover is the cheaper way.
func (g *moverGrid) query(lo, hi mgl32.Vec3, out []int32, movers int) ([]int32, bool) {
	x0, x1 := moverCellCoord(lo.X()), moverCellCoord(hi.X())
	z0, z1 := moverCellCoord(lo.Z()), moverCellCoord(hi.Z())
	span := (int64(x1) - int64(x0) + 1) * (int64(z1) - int64(z0) + 1)
	if span >= int64(movers) {
		return out, false
	}
	if span <= int64(g.used) {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if b, ok := g.cells[[2]int32{x, z}]; ok {
					out = append(out, g.buckets[b]...)
				}
			}
		}
	} else {
		// Fewer occupied cells than cells in the box: walk the occupied ones.
		for b := 0; b < g.used; b++ {
			k := g.keys[b]
			if k[0] >= x0 && k[0] <= x1 && k[1] >= z0 && k[1] <= z1 {
				out = append(out, g.buckets[b]...)
			}
		}
	}
	slices.Sort(out)
	return out, true
}

// postOverlapEvents derives `:enter:` and `:exit:` by diffing this frame's
// containment set against last frame's.
//
// The containers are volumes and entities with bounds; the movers are the player
// and every entity. An object never contains itself, and a container with no
// script is skipped — nothing would hear the event.
//
// Every container used to be asked about every mover. Now the movers are filed
// in a grid once a frame and each container asks only the ones in the cells its
// bounds touch — but in the same order, container by container and then mover
// by mover, so every event goes out exactly when and where it always did.
func postOverlapEvents(rt *ScriptRuntime, lvl *world.Level) {
	if lvl == nil {
		return
	}
	sc := &rt.overlap
	now := sc.freeSet()

	sc.containers = sc.containers[:0]
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		if v.Script == "" && !v.IsLevelTrigger() && !v.IsCheckpointTrigger() {
			continue
		}
		c := overlapContainer{
			ref:        flang.RefVal{Kind: flang.RefVolume, A: v.ID},
			vol:        v,
			scripted:   v.Script != "",
			loadLevel:  v.LoadLevel,
			checkpoint: v.Checkpoint,
			carrier:    v.Attach.Entity,
		}
		// A volume is level-scoped, but not necessarily world-space: one riding
		// a vehicle (a doorway, a trigger in a hold) or an entity (a sight cone)
		// describes its region in the carrier's coordinates, so the world-space
		// point is carried in — by the same route Volume.LocalPoint takes.
		toWorld := mgl32.Ident4()
		switch {
		case v.Attach.IsZero():
			c.volMode = volWorld
		case v.Attach.Entity == "":
			if f := lvl.Frames.Frame(v.Attach.Frame); f != nil {
				c.volMode, c.volFrame = volFrame, f
				if !f.IsWorld() {
					toWorld = f.World
				}
			}
		default:
			m := v.Attach.Matrix(lvl)
			c.volMode, c.volInv, toWorld = volMatrix, m.Inv(), m
		}
		switch {
		case !v.Enabled || !v.HasBounds():
			c.empty = true
		case c.volMode == volWorld && v.Rotation == (mgl32.Vec3{}):
			h := v.HalfExtents()
			pad := boundPad(v.Position, h.Len())
			h = h.Add(mgl32.Vec3{pad, pad, pad})
			c.lo, c.hi = v.Position.Sub(h), v.Position.Add(h)
		default:
			// Whatever the rotation, a point in the shape is no further from
			// its centre than the extents' length, and the carrier stretches
			// that by at most linearBound.
			centre := v.Position
			radius := v.HalfExtents().Len()
			if c.volMode != volWorld {
				r := toWorld.Mul4x1(centre.Vec4(1))
				centre = mgl32.Vec3{r.X(), r.Y(), r.Z()}
				radius *= linearBound(toWorld)
			}
			c.boxAround(centre, radius)
		}
		sc.containers = append(sc.containers, c)
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if e.Script == "" || !e.HasBounds() {
			continue
		}
		// An entity in a frame describes its volume in that frame's
		// coordinates, so the world-space point is carried in rather than the
		// volume carried out — which keeps a rotated volume the shape it was
		// authored as.
		ef := lvl.Frames.FrameFor(e)
		// A sphere round the bounds, worked out once per frame, turns away
		// nearly every mover before the exact shape test: with a hundred
		// scripted soldiers in a level that is a hundred containers asked
		// about a hundred movers each, and the exact test rebuilds the
		// entity's rotation every time it is asked.
		centre := e.BoundCenter()
		reach := e.BoundHalfExtents().Len()
		c := overlapContainer{
			ref:      flang.RefVal{Kind: flang.RefEntity, A: e.ID},
			ent:      e,
			entFrame: ef,
			centre:   centre,
			reach2:   reach * reach,
			scripted: true,
		}
		if ef.IsWorld() {
			c.boxAround(centre, reach)
		} else {
			c.boxAround(ef.ToWorld(centre), reach*linearBound(ef.World))
		}
		sc.containers = append(sc.containers, c)
	}

	// Each mover's position once, not once per container, and filed in the
	// grid: posting (below) only queues, so nothing moves while the containers
	// are asked.
	movers := placeMovers(rt, lvl)
	sc.grid.build(movers)

	for ci := range sc.containers {
		c := &sc.containers[ci]
		if c.empty {
			continue
		}
		cand, gridded := sc.grid.query(c.lo, c.hi, sc.cand[:0], len(movers))
		sc.cand = cand
		n := len(movers)
		if gridded {
			n = len(cand)
		}
		for k := 0; k < n; k++ {
			mi := k
			if gridded {
				mi = int(cand[k])
			}
			pm := &movers[mi]
			m := pm.ref
			if m == c.ref || (c.carrier != "" && m.Kind == flang.RefEntity && m.A == c.carrier) {
				continue
			}
			if !c.mayContain(pm.pos) || !c.contains(pm.pos) {
				continue
			}
			pair := overlapPair{container: c.ref, other: m}
			now[pair] = true
			if scriptEvents.overlaps[pair] {
				continue
			}
			if c.scripted {
				_ = rt.post(c.ref, "enter", []flang.Value{m}, flang.Pos{})
			}
			// A level exit, taken only by the player: see Volume.LoadLevel.
			// The request is recorded rather than acted on, exactly as the
			// `load_level` verb does — the frame loop starts the load between
			// frames, which leaves the `:enter:` posted just above free to run
			// first, in the level that is still standing.
			if c.loadLevel != "" && m.Kind == flang.RefEntity && m.A == "player" {
				hooks.RequestLevelLoad(c.loadLevel)
			}
			// A checkpoint, likewise the player's alone, and reached once.
			if c.checkpoint && m.Kind == flang.RefEntity && m.A == "player" {
				reachCheckpoint(rt, c.ref.A)
			}
		}
	}
	// Let go of the level's objects until next frame.
	clear(sc.containers)

	for pair := range scriptEvents.overlaps {
		if !now[pair] {
			// The container may itself have been destroyed, in which case the
			// send is dropped at delivery. Posting anyway keeps the diff simple
			// and the drop path is already the right one.
			_ = rt.post(pair.container, "exit", []flang.Value{pair.other}, flang.Pos{})
		}
	}
	scriptEvents.overlaps = now
}

// freeSet returns an empty containment set to fill this frame: whichever of
// the runtime's own two is not the one installed as last frame's.
func (sc *overlapScratch) freeSet() map[overlapPair]bool {
	live := reflect.ValueOf(scriptEvents.overlaps).Pointer()
	for i := range sc.sets {
		if sc.sets[i] == nil {
			sc.sets[i] = map[overlapPair]bool{}
			return sc.sets[i]
		}
		if reflect.ValueOf(sc.sets[i]).Pointer() != live {
			clear(sc.sets[i])
			return sc.sets[i]
		}
	}
	// Unreachable — two sets cannot both be the live one — but a fresh set is
	// always a correct answer.
	return map[overlapPair]bool{}
}

// The sector-scoped `:enter:`/`:exit:` pair is gone with sectors themselves:
// a brush is a solid, not a room, so "which one is this body in" is not a
// question it can answer. A Volume already fires exactly that pair for a
// region an author draws (postOverlapEvents), which is what a level wanting
// room events uses now.

// postCollideEvents fires `:collide:` for each pair the resolver pushed apart
// this frame. Both sides hear it: which one a script cares about is its business.
func postCollideEvents(rt *ScriptRuntime) {
	for _, pair := range hooks.TakeCollisionPairs() {
		a := flang.RefVal{Kind: flang.RefEntity, A: pair[0]}
		b := flang.RefVal{Kind: flang.RefEntity, A: pair[1]}
		_ = rt.post(a, "collide", []flang.Value{b}, flang.Pos{})
		_ = rt.post(b, "collide", []flang.Value{a}, flang.Pos{})
	}
}

// postAnimEvents fires `:anim_start:` and `:anim_end:` for the clip transitions
// this frame produced.
func postAnimEvents(rt *ScriptRuntime) {
	for _, ev := range hooks.TakeAnimEvents() {
		atom := "anim_start"
		if ev.Ended {
			atom = "anim_end"
		}
		r := flang.RefVal{Kind: flang.RefEntity, A: ev.Entity}
		_ = rt.post(r, atom, []flang.Value{
			r,
			flang.StrVal(ev.Model),
			flang.StrVal(ev.Animation),
		}, flang.Pos{})
	}
}

// postUIEvents drains the buttons pressed, sliders dragged and screens opened
// since the last tick.
//
// internal/ui works all of this out without knowing that scripts exist, and
// records it in a hooks buffer; this is where it becomes a message. The
// indirection is the point, and it is the same one the collision pairs and the
// animation events use: input code stays about input.
//
// Each event goes to the widget, or to its screen when the widget carries no
// script of its own. One level of bubbling — a widget's owner is its screen,
// and a full routing chain through every panel in between would make where a
// handler runs depend on the layout.
func postUIEvents(rt *ScriptRuntime) {
	for _, ev := range hooks.TakeUIEvents() {
		target := uiWidgetRef(ev.Screen, ev.Widget)
		if ev.Widget != "" && !rt.hasScript(target) {
			target = flang.RefVal{Kind: flang.RefScreen, A: ev.Screen}
		}
		_ = rt.post(target, ev.Atom, uiEventArgs(ev), flang.Pos{})
	}
}

// uiEventArgs is what each UI atom carries.
//
// A click carries the widget itself, so one handler can serve several buttons
// and tell them apart. A change carries its new value, typed to match the
// widget's kind, because the alternative is every handler reading the property
// back and racing the next frame's poll.
func uiEventArgs(ev hooks.UIEvent) []flang.Value {
	src := uiWidgetRef(ev.Screen, ev.Widget)
	switch ev.Atom {
	case "cancel", "shown", "hidden":
		return nil
	case "change":
		return []flang.Value{src, uiChangeValue(ev)}
	case "submit":
		return []flang.Value{src, flang.StrVal(ev.Str)}
	case "bound":
		return []flang.Value{src, flang.StrVal(ev.Str), flang.IntVal(int64(ev.Num))}
	// A drag's positions are in the screen's canvas units — what `pos` and
	// `translate` read and write — so a handler can move the widget by the
	// delta it was handed without converting anything.
	case "drag_start":
		return []flang.Value{src, canvasVec(ev.X, ev.Y)}
	case "drag":
		return []flang.Value{src, canvasVec(ev.X, ev.Y), canvasVec(ev.DX, ev.DY)}
	case "drop":
		var onto flang.Value = flang.NullVal{}
		if ev.Other != "" {
			onto = uiWidgetRef(ev.OtherScreen, ev.Other)
		}
		return []flang.Value{src, onto, canvasVec(ev.X, ev.Y)}
	}
	return []flang.Value{src}
}

func canvasVec(x, y float64) flang.Value { return flang.VecVal{x, y, 0} }

func uiChangeValue(ev hooks.UIEvent) flang.Value {
	switch {
	case ev.Text || ev.Str != "":
		return flang.StrVal(ev.Str)
	case ev.Toggle || ev.Bool:
		return flang.BoolVal(ev.Bool)
	}
	return flang.FloatVal(ev.Num)
}

// hasScript reports whether a ref names something with a handler of its own.
func (rt *ScriptRuntime) hasScript(r flang.RefVal) bool {
	if rt.cache == nil {
		return false
	}
	d := rt.cache.scriptDataFor(r)
	return d != nil && d.Script != ""
}

// postInteractEvent fires `:interact:` on whatever the player is standing in.
//
// Containment rather than a camera raycast, because EntitiesContaining and
// VolumesContaining already exist and a ray does not. The nearest container
// wins, so a small trigger inside a large room volume is still reachable.
func postInteractEvent(rt *ScriptRuntime, lvl *world.Level) {
	pressed := hooks.ScriptInteractPressed()
	if !pressed || world.PlayerEntity == nil || lvl == nil {
		return
	}
	// World space, so a player riding in a frame is compared against the level's
	// volumes on the same terms as one standing on the ground.
	p := lvl.Frames.WorldPosition(world.PlayerEntity)
	player := flang.RefVal{Kind: flang.RefEntity, A: "player"}

	best := flang.RefVal{}
	bestDist := float32(-1)
	consider := func(r flang.RefVal, centre mgl32.Vec3) {
		d := centre.Sub(p).Len()
		if bestDist < 0 || d < bestDist {
			best, bestDist = r, d
		}
	}
	for _, v := range world.VolumesContaining(lvl, p) {
		if v.Script != "" {
			consider(flang.RefVal{Kind: flang.RefVolume, A: v.ID}, v.WorldPosition(lvl))
		}
	}
	for _, e := range world.EntitiesContaining(lvl, p) {
		if e.Script != "" {
			consider(flang.RefVal{Kind: flang.RefEntity, A: e.ID}, lvl.Frames.WorldBoundCenter(e))
		}
	}
	if best.Kind != flang.RefNone {
		_ = rt.post(best, "interact", []flang.Value{player}, flang.Pos{})
	}
}

// postWeaponInputEvents fires the viewmodel's fire/throw/switch atoms,
// straight to `@player` rather than through a containment lookup —
// `postInteractEvent`'s "nearest container" question does not apply here,
// since these are about the player itself, not what the player is near.
func postWeaponInputEvents(rt *ScriptRuntime) {
	// Drained unconditionally, same as postInteractEvent's own pressed flag —
	// a pending edge from a frame with no player would otherwise sit in the
	// buffer and fire late the moment one appears, rather than being the
	// no-op it should be.
	primary := hooks.TakeScriptPrimaryFireEdges()
	secondary := hooks.TakeScriptSecondaryFireEdges()
	threw := hooks.ScriptThrowPressed()
	reloaded := hooks.ScriptReloadPressed()
	slots := hooks.TakeScriptWeaponSlotRequests()
	cycles := hooks.TakeScriptWeaponCycleRequests()

	if world.PlayerEntity == nil {
		return
	}
	player := flang.RefVal{Kind: flang.RefEntity, A: "player"}

	// In the order they happened, so a release and the next press in one
	// frame leave the script holding the trigger, not letting it go.
	postFireEdges(rt, player, primary, "primary_fire")
	postFireEdges(rt, player, secondary, "secondary_fire")
	if threw {
		_ = rt.post(player, "throw", nil, flang.Pos{})
	}
	if reloaded {
		_ = rt.post(player, "reload", nil, flang.Pos{})
	}
	for _, slot := range slots {
		_ = rt.post(player, "weapon_slot", []flang.Value{flang.IntVal(slot)}, flang.Pos{})
	}
	for _, step := range cycles {
		_ = rt.post(player, "weapon_cycle", []flang.Value{flang.IntVal(step)}, flang.Pos{})
	}
}

// postFireEdges posts one button's edges: `:<atom>:` for a press and
// `:<atom>_end:` for a release.
func postFireEdges(rt *ScriptRuntime, player flang.RefVal, edges []bool, atom string) {
	for _, down := range edges {
		if down {
			_ = rt.post(player, atom, nil, flang.Pos{})
		} else {
			_ = rt.post(player, atom+"_end", nil, flang.Pos{})
		}
	}
}

// postPlayerGroundEvents fires `:landed:` and `:left_ground:` on the player.
//
// Derived from the engine's own footing rather than posted by whatever changed
// it, for the reason `:enter:` is derived from containment: most of the ways a
// player leaves the ground are not events at all. Jumping is, but walking off a
// ledge, being thrown by a blast, having the floor moved out from under them
// and having gravity switched off are not — and a rule that fired for the jump
// and stayed silent for the rest would be worse than no rule.
//
// `:landed:` carries the speed the body arrived at, in units a second, because
// that is the number a fall-damage rule is written against and this is the only
// moment it exists: the resolve zeroes the fall on the frame it stops it, so a
// handler reading `@player.prop.velocity` always finds nothing.
//
// The first tick of a session posts nothing. There is no previous footing to
// compare against, and taking the zero value as "was in the air" would fire a
// `:landed:` at the feet of every player who ever spawned standing up.
func postPlayerGroundEvents(rt *ScriptRuntime) {
	g := hooks.CurrentPlayerGround()
	if !g.Known || world.PlayerEntity == nil {
		return
	}
	was, knew := scriptEvents.grounded, scriptEvents.groundedKnown
	scriptEvents.grounded, scriptEvents.groundedKnown = g.OnGround, true
	if !knew || was == g.OnGround {
		return
	}
	player := flang.RefVal{Kind: flang.RefEntity, A: "player"}
	if g.OnGround {
		_ = rt.post(player, "landed", []flang.Value{flang.FloatVal(g.Impact)}, flang.Pos{})
		return
	}
	_ = rt.post(player, "left_ground", nil, flang.Pos{})
}

// postTimerEvents delivers every `after(...)` whose time has come.
func postTimerEvents(rt *ScriptRuntime) {
	if len(scriptEvents.timers) == 0 {
		return
	}
	kept := scriptEvents.timers[:0]
	for _, t := range scriptEvents.timers {
		if timerClock(t.onStack) >= t.due {
			_ = rt.postFrom(t.to, t.atom, t.args, t.pos, timerIsScriptSend)
			continue
		}
		kept = append(kept, t)
	}
	scriptEvents.timers = kept
}

// scriptAddTimer queues a delayed message. Timers are play-time based, so a
// script's pause holds them, and they do not survive a level reload.
//
// The exception is a timer addressed to a screen or a widget, which counts on
// the stack's clock instead: the pause does not stop the stack, and a menu's
// own `after` has to come due under the pause the menu is sitting over. Chosen
// by where the message is going rather than who set it, because the receiver's
// clock is the one that is or is not running when it arrives.
func scriptAddTimer(delayMS float64, to flang.RefVal, atom string, args []flang.Value, pos flang.Pos) {
	onStack := to.Kind == flang.RefScreen || to.Kind == flang.RefWidget
	scriptEvents.timers = append(scriptEvents.timers, scriptTimer{
		due:     timerClock(onStack) + delayMS/1000.0,
		to:      to,
		atom:    atom,
		args:    args,
		pos:     pos,
		onStack: onStack,
	})
}

// timerClock is the clock a timer counts against.
func timerClock(onStack bool) float64 {
	if onStack {
		return scriptEvents.stackElapsed
	}
	return scriptEvents.elapsed
}

// scriptElapsed is play time in seconds, which is what `time()` returns.
func scriptElapsed() float64 { return scriptEvents.elapsed }

// postVehicleEvents turns this frame's boardings into messages.
//
// Posted to the *vehicle*, following the shape every other engine event has:
// the atom, then a ref to whatever caused it, then whatever else the event is
// about. The vehicle is the thing with a script worth writing here — "someone
// got in" is the door's business, not the passenger's.
func postVehicleEvents(rt *ScriptRuntime) {
	for _, ev := range hooks.TakeVehicleEvents() {
		_ = rt.post(
			flang.RefVal{Kind: flang.RefEntity, A: ev.Vehicle},
			ev.Atom,
			[]flang.Value{
				flang.RefVal{Kind: flang.RefEntity, A: ev.Occupant},
				flang.StrVal(ev.Seat),
			},
			flang.Pos{},
		)
	}
}
