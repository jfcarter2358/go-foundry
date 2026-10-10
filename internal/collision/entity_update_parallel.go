package collision

// entity_update_parallel.go — EntityUpdate's per-entity work, on every core.
//
// The per-entity half of EntityUpdate — fall, integrate, ease, follow a path,
// resolve against the world, find the chunk, pose the skeleton — is most of a
// busy frame, and each entity's share of it reads only that entity, the
// level's static structure, and state the pass holds still. So it runs as
// three phases:
//
//	A  (parallel)  stepEntityMotion for every entity that moves
//	B  (serial)    crossEntityFrames, in level order
//	C  (parallel)  posing every entity's skeletons
//
// and the result is bit-for-bit the serial loop's, which interleaves the three
// per entity. That holds because nothing one entity's phase A reads is
// written by another entity's phase A or B:
//
//   - a body's resolve reads chunks, brushes, terrain, scenery, frames (whose
//     transforms RefreshFrames publishes after the pass) and the entity solids
//     (fixed for the pass, entity_brushes.go) — never another entity;
//   - phase B moves a walker between frames, which changes its own position,
//     velocity, facing and frame, and a vehicle's occupant list, none of which
//     phase A reads of anyone else; and it reads other entities' frame IDs
//     only, which phase A never writes;
//   - phase C reads the entity's position for its posing distance after
//     phase B has moved it, as the serial loop does.
//
// The three ways a body's step CAN read another entity's live placement are
// checked for up front, and a level using any of them takes the serial loop
// (parallelEntityPass): a gravity volume or a doorway attached to an entity
// (asked about where the entity is right now), and a waypoint path in the
// space of an entity that carries no frame (placed through that entity's
// matrix right now). So is a level whose entity IDs are not unique, since the
// events below are put back in order by entity ID.
//
// # Shared caches
//
// What a resolve fills in lazily is filled in before the workers start, and
// only read while they run: each chunk's brush boxes and convex pieces
// (syncBrushBoxes), each terrain's surface (Terrain.Sections), the scenery
// index (freezeScenery), and the entity solids (beginEntityGeometryPass). A
// brush-box query's scratch is per goroutine (nearScratchPool). Cooked-mesh
// residency and the profiler's counters are behind their own locks, and their
// results do not depend on which goroutine asks first.
//
// # Events
//
// Stepping a body records animation events and waypoint arrivals, and the
// script tick runs their handlers in the order they arrive. The hooks capture
// them for the length of the pass (hooks.BeginEventCapture), and they are put
// back afterwards sorted by entity index, stably — exactly the serial order,
// since each entity's own events were recorded by one goroutine, in its own
// program order, and phase A's before phase C's.

import (
	"fmt"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// ParallelEntityUpdate switches the parallel entity pass on. Off, EntityUpdate
// runs its serial loop, which a test uses as the reference the parallel pass
// must match bit for bit.
var ParallelEntityUpdate = true

// parallelEntityMin is the entity count below which fanning out costs more
// than it saves.
const parallelEntityMin = 24

// entityBlock is how many consecutive entities a worker takes at a time: big
// enough that the shared counter is not contended, small enough that one slow
// body (a long sweep, a step-up) does not leave the other workers idle.
const entityBlock = 4

// parallelEntityPass reports whether this pass can run in parallel, and if so
// returns each entity's index by ID, for putting events back in order.
func parallelEntityPass(lvl *world.Level) (map[string]int, bool) {
	if !ParallelEntityUpdate || len(lvl.Entities) < parallelEntityMin || runtime.GOMAXPROCS(0) < 2 {
		return nil, false
	}
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		if v.Attach.Entity != "" && (v.Gravity != nil || v.Doorway != nil) {
			return nil, false
		}
	}
	ids := entityPassIDs(lvl)
	if ids == nil {
		return nil, false
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if e.Waypoints == nil {
			continue
		}
		a := world.FindWaypointAnimation(lvl, e.Waypoints.Animation)
		if a == nil || a.FromEntity == "" {
			continue
		}
		if f := lvl.Frames.Frame(a.FromEntity); f == nil || f.IsWorld() {
			return nil, false
		}
	}
	return ids, true
}

// entityPassIDs maps every entity's ID to its index, or returns nil when two
// share one.
func entityPassIDs(lvl *world.Level) map[string]int {
	ids := entityPassScratch.ids
	if ids == nil {
		ids = make(map[string]int, len(lvl.Entities))
		entityPassScratch.ids = ids
	}
	clear(ids)
	for i := range lvl.Entities {
		id := lvl.Entities[i].ID
		if _, dup := ids[id]; dup {
			return nil
		}
		ids[id] = i
	}
	return ids
}

// entityUpdateParallel is EntityUpdate's per-entity loop as phases A, B, C.
func entityUpdateParallel(lvl *world.Level, dt float32, motion bool, viewer mgl32.Vec3, ids map[string]int, sp sleepPass) {
	if motion {
		freezeCollision(lvl)
	}
	hooks.BeginEventCapture()
	func() {
		// Thawed however the phases end, so a panic in one leaves the caches
		// writable for whatever handles it.
		defer thawCollision()
		if motion {
			parallelEntities(len(lvl.Entities), func(i int) {
				if e := &lvl.Entities[i]; !e.IsVehicle() {
					stepEntityMotionMemo(lvl, e, i, dt, sp)
				}
			})
			// Off the parallel phase, so the lazily built caches may be
			// written again: crossing a frame portal can ask about chunks
			// the motion did not.
			thawCollision()
			for i := range lvl.Entities {
				if e := &lvl.Entities[i]; !e.IsVehicle() {
					crossEntityFrames(lvl, e)
				}
			}
		}
		parallelEntities(len(lvl.Entities), func(i int) {
			e := &lvl.Entities[i]
			e.UpdateAnimationsPose(dt, world.AnimPoseDue(e, i, animFrame, viewer))
		})
	}()
	anim, wp := hooks.EndEventCapture()
	replayEntityEvents(anim, wp, ids, len(lvl.Entities))
}

// freezeCollision fills every cache a resolve would otherwise fill lazily and
// marks them read-only for the parallel phase; thawCollision ends it.
func freezeCollision(lvl *world.Level) {
	syncBrushBoxes(lvl)
	for i := range lvl.Chunks {
		if t := lvl.Chunks[i].Terrain; t != nil {
			t.Sections()
		}
	}
	freezeScenery(lvl)
	brushBoxesFrozen = true
}

func thawCollision() {
	brushBoxesFrozen = false
	thawScenery()
}

// replayEntityEvents records the captured events into the real buffers in
// level order: stably sorted by the index of the entity each belongs to.
func replayEntityEvents(anim []hooks.AnimEvent, wp []hooks.WaypointEvent, ids map[string]int, n int) {
	at := func(id string) int {
		if i, ok := ids[id]; ok {
			return i
		}
		return n // not an entity of the level: after all of them
	}
	if len(anim) > 0 {
		slices.SortStableFunc(anim, func(a, b hooks.AnimEvent) int { return at(a.Entity) - at(b.Entity) })
		for _, ev := range anim {
			hooks.RecordAnimEvent(ev)
		}
	}
	if len(wp) > 0 {
		slices.SortStableFunc(wp, func(a, b hooks.WaypointEvent) int { return at(a.Object) - at(b.Object) })
		for _, ev := range wp {
			hooks.RecordWaypointEvent(ev)
		}
	}
}

// parallelEntities runs fn for every index in [0, n) on a pool of
// GOMAXPROCS workers taking entityBlock indices at a time. A panic in any
// worker is carried back and raised on the calling goroutine, as the serial
// loop would have raised it.
func parallelEntities(n int, fn func(i int)) {
	workers := min(runtime.GOMAXPROCS(0), (n+entityBlock-1)/entityBlock)
	if workers <= 1 {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	var failure atomic.Value
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					failure.CompareAndSwap(nil, fmt.Sprint(r))
				}
			}()
			for {
				start := int(next.Add(entityBlock)) - entityBlock
				if start >= n {
					return
				}
				for i := start; i < min(start+entityBlock, n); i++ {
					fn(i)
				}
			}
		}()
	}
	wg.Wait()
	if r := failure.Load(); r != nil {
		panic(fmt.Sprintf("collision: parallel entity update: %v", r))
	}
}
