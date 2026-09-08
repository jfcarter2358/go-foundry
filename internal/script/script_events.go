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
	"foundry/internal/flang"
	"foundry/internal/hooks"
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
	// elapsed is play time in seconds, which is what `time()` returns.
	elapsed float64
	// interactHeld tracks the interact key, so `:interact:` fires on the press
	// rather than every frame the key is down.
	interactHeld bool
}

type scriptTimer struct {
	due  float64
	to   flang.RefVal
	atom string
	args []flang.Value
	pos  flang.Pos
}

// A timer is always a script send — `after` can only be called from a handler —
// so postTimerEvents says so explicitly rather than letting post infer it from
// rt.self, which is no longer set by the time the timer comes due.
const timerIsScriptSend = true

var scriptEvents = scriptEventState{
	overlaps: map[overlapPair]bool{},
	spawned:  map[flang.RefVal]bool{},
}

// ScriptEventsReset clears the derived-event state. Called whenever the level is
// replaced: the previous frame's overlaps describe objects that no longer exist.
func ScriptEventsReset() {
	scriptEvents = scriptEventState{
		overlaps: map[overlapPair]bool{},
		spawned:  map[flang.RefVal]bool{},
	}
}

// ScriptEventsTick derives and posts one frame's events.
func ScriptEventsTick(rt *ScriptRuntime, lvl *world.Level, dt float32) {
	scriptEvents.elapsed += float64(dt)

	postSpawnEvents(rt, lvl)
	postOverlapEvents(rt, lvl)
	postCollideEvents(rt)
	postAnimEvents(rt)
	postVehicleEvents(rt)
	postInteractEvent(rt, lvl)
	postUIEvents(rt)
	postTimerEvents(rt)
	postTickEvents(rt, lvl, dt)
}

// scriptedRefs returns every object in the level that has a script, so the
// per-object events do not have to walk objects that could not react anyway.
func scriptedRefs(rt *ScriptRuntime, lvl *world.Level) []flang.RefVal {
	if lvl == nil {
		return nil
	}
	var out []flang.RefVal
	for i := range lvl.Entities {
		if lvl.Entities[i].Script != "" {
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
		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			if b.Script != "" {
				out = append(out, flang.RefVal{Kind: flang.RefBrush, A: b.ID})
			}
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
	out = append(out, scriptedUIRefs()...)
	return out
}

// scriptedUIRefs is the screen-stack half of scriptedRefs.
//
// Only screens that are up, and only the ones with a script. A menu sitting in
// resources/ui that nobody has opened must not be receiving :tick: sixty times
// a second — that is a bug, not a feature, and it is the difference between a
// screen stack and a list of files.
func scriptedUIRefs() []flang.RefVal {
	if uiStack == nil {
		return nil
	}
	var out []flang.RefVal
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

// postSpawnEvents fires `:spawn:` once per scripted object.
func postSpawnEvents(rt *ScriptRuntime, lvl *world.Level) {
	for _, r := range scriptedRefs(rt, lvl) {
		if scriptEvents.spawned[r] {
			continue
		}
		scriptEvents.spawned[r] = true
		_ = rt.post(r, "spawn", nil, flang.Pos{})
	}
}

// postTickEvents fires `:tick:` on every scripted object, with the frame time.
func postTickEvents(rt *ScriptRuntime, lvl *world.Level, dt float32) {
	for _, r := range scriptedRefs(rt, lvl) {
		_ = rt.post(r, "tick", []flang.Value{flang.FloatVal(dt)}, flang.Pos{})
	}
}

// movers returns everything whose position can trigger a containment event: the
// player, and every entity.
func movers(lvl *world.Level) []flang.RefVal {
	var out []flang.RefVal
	if world.PlayerEntity != nil {
		out = append(out, flang.RefVal{Kind: flang.RefEntity, A: "player"})
	}
	if lvl != nil {
		for i := range lvl.Entities {
			out = append(out, flang.RefVal{Kind: flang.RefEntity, A: lvl.Entities[i].ID})
		}
	}
	return out
}

// moverPosition returns where a mover currently is, in world space.
//
// World space specifically: an entity riding in a frame stores its position in
// that frame's coordinates, and comparing those against a container somewhere
// else in the level would be subtracting two numbers that do not describe the
// same space. Every containment test below is in world coordinates for that
// reason.
func moverPosition(lvl *world.Level, r flang.RefVal) (mgl32.Vec3, bool) {
	if r.A == "player" {
		if world.PlayerEntity == nil {
			return mgl32.Vec3{}, false
		}
		return lvl.Frames.WorldPosition(world.PlayerEntity), true
	}
	if e := world.FindEntity(lvl, r.A); e != nil {
		return lvl.Frames.WorldPosition(e), true
	}
	return mgl32.Vec3{}, false
}

// postOverlapEvents derives `:enter:` and `:exit:` by diffing this frame's
// containment set against last frame's.
//
// The containers are volumes and entities with bounds; the movers are the player
// and every entity. An object never contains itself, and a container with no
// script is skipped — nothing would hear the event.
func postOverlapEvents(rt *ScriptRuntime, lvl *world.Level) {
	if lvl == nil {
		return
	}
	now := map[overlapPair]bool{}

	moverList := movers(lvl)
	type container struct {
		ref      flang.RefVal
		test     func(mgl32.Vec3) bool
		scripted bool
		// loadLevel is the level this container is an exit to, or empty. See
		// world.Volume.LoadLevel — it is why a container can be worth walking
		// even when nothing is listening for its `:enter:`.
		loadLevel string
	}
	var containers []container

	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		if v.Script == "" && !v.IsLevelTrigger() {
			continue
		}
		vv := v
		// Volumes are level-scoped and always world-space, so the point arrives
		// in the coordinates the volume is already in.
		containers = append(containers, container{
			ref:       flang.RefVal{Kind: flang.RefVolume, A: v.ID},
			test:      func(p mgl32.Vec3) bool { return vv.ContainsPoint(p) },
			scripted:  v.Script != "",
			loadLevel: v.LoadLevel,
		})
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if e.Script == "" || !e.HasBounds() {
			continue
		}
		ee := e
		// An entity in a frame describes its volume in that frame's
		// coordinates, so the world-space point is carried in rather than the
		// volume carried out — which keeps a rotated volume the shape it was
		// authored as.
		ef := lvl.Frames.FrameFor(e)
		containers = append(containers, container{
			ref:      flang.RefVal{Kind: flang.RefEntity, A: e.ID},
			test:     func(p mgl32.Vec3) bool { return ee.ContainsPoint(ef.FromWorld(p)) },
			scripted: true,
		})
	}

	for _, c := range containers {
		for _, m := range moverList {
			if m == c.ref {
				continue
			}
			p, ok := moverPosition(lvl, m)
			if !ok || !c.test(p) {
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
		}
	}

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
	}
	return []flang.Value{src}
}

func uiChangeValue(ev hooks.UIEvent) flang.Value {
	switch {
	case ev.Str != "":
		return flang.StrVal(ev.Str)
	case ev.Bool:
		return flang.BoolVal(true)
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
			consider(flang.RefVal{Kind: flang.RefVolume, A: v.ID}, v.Position)
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

// postTimerEvents delivers every `after(...)` whose time has come.
func postTimerEvents(rt *ScriptRuntime) {
	if len(scriptEvents.timers) == 0 {
		return
	}
	kept := scriptEvents.timers[:0]
	for _, t := range scriptEvents.timers {
		if scriptEvents.elapsed >= t.due {
			_ = rt.postFrom(t.to, t.atom, t.args, t.pos, timerIsScriptSend)
			continue
		}
		kept = append(kept, t)
	}
	scriptEvents.timers = kept
}

// scriptAddTimer queues a delayed message. Timers are play-time based, so
// pausing the game pauses them, and they do not survive a level reload.
func scriptAddTimer(delayMS float64, to flang.RefVal, atom string, args []flang.Value, pos flang.Pos) {
	scriptEvents.timers = append(scriptEvents.timers, scriptTimer{
		due:  scriptEvents.elapsed + delayMS/1000.0,
		to:   to,
		atom: atom,
		args: args,
		pos:  pos,
	})
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
