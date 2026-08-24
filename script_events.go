package main

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
	// sectorOf is each entity's last known sector, for the sector-scoped
	// enter/exit pair. Entity.CurrentSector is already refreshed every frame by
	// EntityUpdate, so this costs one string comparison per entity.
	sectorOf map[string]string
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
	sectorOf: map[string]string{},
	spawned:  map[flang.RefVal]bool{},
}

// ScriptEventsReset clears the derived-event state. Called whenever the level is
// replaced: the previous frame's overlaps describe objects that no longer exist.
func ScriptEventsReset() {
	scriptEvents = scriptEventState{
		overlaps: map[overlapPair]bool{},
		sectorOf: map[string]string{},
		spawned:  map[flang.RefVal]bool{},
	}
}

// ScriptEventsTick derives and posts one frame's events.
func ScriptEventsTick(rt *ScriptRuntime, lvl *Level, dt float32) {
	scriptEvents.elapsed += float64(dt)

	postSpawnEvents(rt, lvl)
	postOverlapEvents(rt, lvl)
	postSectorEvents(rt, lvl)
	postCollideEvents(rt)
	postAnimEvents(rt)
	postInteractEvent(rt, lvl)
	postTimerEvents(rt)
	postTickEvents(rt, lvl, dt)
}

// scriptedRefs returns every object in the level that has a script, so the
// per-object events do not have to walk objects that could not react anyway.
func scriptedRefs(rt *ScriptRuntime, lvl *Level) []flang.RefVal {
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
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if ch.Script != "" {
			out = append(out, flang.RefVal{Kind: flang.RefChunk, A: ch.ID})
		}
		for si := range ch.Sectors {
			s := &ch.Sectors[si]
			if s.Script != "" {
				out = append(out, flang.RefVal{Kind: flang.RefSector, A: ch.ID, B: s.ID})
			}
			if s.Floor.Script != "" {
				out = append(out, flang.RefVal{Kind: flang.RefFloor, A: ch.ID, B: s.ID})
			}
			if s.Ceiling.Script != "" {
				out = append(out, flang.RefVal{Kind: flang.RefCeiling, A: ch.ID, B: s.ID})
			}
			ensureEdgeIDs(s)
			for ei, id := range s.EdgeIDs {
				kind, idx := edgeKindAndIndex(s, ei)
				script := ""
				if kind == "wall" && idx < len(s.Walls) {
					script = s.Walls[idx].Script
				} else if kind == "portal" && idx < len(s.Portals) {
					script = s.Portals[idx].Script
				}
				if script != "" {
					out = append(out, flang.RefVal{Kind: flang.RefEdge, A: ch.ID, B: s.ID, C: id})
				}
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
	return out
}

// postSpawnEvents fires `:spawn:` once per scripted object.
func postSpawnEvents(rt *ScriptRuntime, lvl *Level) {
	for _, r := range scriptedRefs(rt, lvl) {
		if scriptEvents.spawned[r] {
			continue
		}
		scriptEvents.spawned[r] = true
		_ = rt.post(r, "spawn", nil, flang.Pos{})
	}
}

// postTickEvents fires `:tick:` on every scripted object, with the frame time.
func postTickEvents(rt *ScriptRuntime, lvl *Level, dt float32) {
	for _, r := range scriptedRefs(rt, lvl) {
		_ = rt.post(r, "tick", []flang.Value{flang.FloatVal(dt)}, flang.Pos{})
	}
}

// movers returns everything whose position can trigger a containment event: the
// player, and every entity.
func movers(lvl *Level) []flang.RefVal {
	var out []flang.RefVal
	if PlayerEntity != nil {
		out = append(out, flang.RefVal{Kind: flang.RefEntity, A: "player"})
	}
	if lvl != nil {
		for i := range lvl.Entities {
			out = append(out, flang.RefVal{Kind: flang.RefEntity, A: lvl.Entities[i].ID})
		}
	}
	return out
}

// moverPosition returns where a mover currently is.
func moverPosition(lvl *Level, r flang.RefVal) (mgl32.Vec3, bool) {
	if r.A == "player" {
		if PlayerEntity == nil {
			return mgl32.Vec3{}, false
		}
		return PlayerEntity.Position, true
	}
	if e := FindEntity(lvl, r.A); e != nil {
		return e.Position, true
	}
	return mgl32.Vec3{}, false
}

// postOverlapEvents derives `:enter:` and `:exit:` by diffing this frame's
// containment set against last frame's.
//
// The containers are volumes and entities with bounds; the movers are the player
// and every entity. An object never contains itself, and a container with no
// script is skipped — nothing would hear the event.
func postOverlapEvents(rt *ScriptRuntime, lvl *Level) {
	if lvl == nil {
		return
	}
	now := map[overlapPair]bool{}

	moverList := movers(lvl)
	type container struct {
		ref      flang.RefVal
		test     func(mgl32.Vec3) bool
		scripted bool
	}
	var containers []container

	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		if v.Script == "" {
			continue
		}
		vv := v
		containers = append(containers, container{
			ref:      flang.RefVal{Kind: flang.RefVolume, A: v.ID},
			test:     func(p mgl32.Vec3) bool { return vv.ContainsPoint(p) },
			scripted: true,
		})
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if e.Script == "" || !e.HasBounds() {
			continue
		}
		ee := e
		containers = append(containers, container{
			ref:      flang.RefVal{Kind: flang.RefEntity, A: e.ID},
			test:     func(p mgl32.Vec3) bool { return ee.ContainsPoint(p) },
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
			if !scriptEvents.overlaps[pair] {
				_ = rt.post(c.ref, "enter", []flang.Value{m}, flang.Pos{})
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

// postSectorEvents fires the sector-scoped enter/exit pair.
//
// Free, near enough: EntityUpdate already refreshes Entity.CurrentSector every
// frame, so this is one string comparison per entity rather than a containment
// test.
func postSectorEvents(rt *ScriptRuntime, lvl *Level) {
	if lvl == nil {
		return
	}
	check := func(id, sector string, mover flang.RefVal) {
		prev := scriptEvents.sectorOf[id]
		if prev == sector {
			return
		}
		scriptEvents.sectorOf[id] = sector
		if prev != "" {
			if r, ok := rt.cache.refFor(prev); ok && r.Kind == flang.RefSector {
				_ = rt.post(r, "exit", []flang.Value{mover}, flang.Pos{})
			}
		}
		if sector != "" {
			if r, ok := rt.cache.refFor(sector); ok && r.Kind == flang.RefSector {
				_ = rt.post(r, "enter", []flang.Value{mover}, flang.Pos{})
			}
		}
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		check(e.ID, e.CurrentSector, flang.RefVal{Kind: flang.RefEntity, A: e.ID})
	}
	if PlayerEntity != nil {
		check("player", PlayerEntity.CurrentSector, flang.RefVal{Kind: flang.RefEntity, A: "player"})
	}
}

// postCollideEvents fires `:collide:` for each pair the resolver pushed apart
// this frame. Both sides hear it: which one a script cares about is its business.
func postCollideEvents(rt *ScriptRuntime) {
	for _, pair := range TakeCollisionPairs() {
		a := flang.RefVal{Kind: flang.RefEntity, A: pair[0]}
		b := flang.RefVal{Kind: flang.RefEntity, A: pair[1]}
		_ = rt.post(a, "collide", []flang.Value{b}, flang.Pos{})
		_ = rt.post(b, "collide", []flang.Value{a}, flang.Pos{})
	}
}

// postAnimEvents fires `:anim_start:` and `:anim_end:` for the clip transitions
// this frame produced.
func postAnimEvents(rt *ScriptRuntime) {
	for _, ev := range TakeAnimEvents() {
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

// postInteractEvent fires `:interact:` on whatever the player is standing in.
//
// Containment rather than a camera raycast, because EntitiesContaining and
// VolumesContaining already exist and a ray does not. The nearest container
// wins, so a small trigger inside a large room volume is still reachable.
func postInteractEvent(rt *ScriptRuntime, lvl *Level) {
	pressed := ScriptInteractPressed()
	if !pressed || PlayerEntity == nil || lvl == nil {
		return
	}
	p := PlayerEntity.Position
	player := flang.RefVal{Kind: flang.RefEntity, A: "player"}

	best := flang.RefVal{}
	bestDist := float32(-1)
	consider := func(r flang.RefVal, centre mgl32.Vec3) {
		d := centre.Sub(p).Len()
		if bestDist < 0 || d < bestDist {
			best, bestDist = r, d
		}
	}
	for _, v := range VolumesContaining(lvl, p) {
		if v.Script != "" {
			consider(flang.RefVal{Kind: flang.RefVolume, A: v.ID}, v.Position)
		}
	}
	for _, e := range EntitiesContaining(lvl, p) {
		if e.Script != "" {
			consider(flang.RefVal{Kind: flang.RefEntity, A: e.ID}, e.BoundCenter())
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
