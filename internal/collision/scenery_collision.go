package collision

// scenery_collision.go — making placed props solid.
//
// Scenery is level furniture: it never moves, never falls, and has no velocity
// to cancel. That makes it the simplest case the push-apart machinery has —
// every pair is "one immovable body against one that can give way" — so this
// file is a walk and a call into the same separation the entity pass uses
// (entity_collision.go), rather than a second resolver with its own idea of
// what an overlap means.
//
// What it is gated on is the *world* master switch rather than the entity one.
// A barrel placed as scenery is part of the level's shape in the way a wall is;
// something that resolves against walls should resolve against it, and an
// entity that has deliberately been left to pass through geometry should pass
// through this too. That also means an author does not have to tick a second
// flag to make a fence a fence.
//
// Reusing the entity path costs one proxy Entity per solid prop, built from the
// model's own bounds. That is deliberate: the alternative is a copy of
// allowedPush, canDisplace, the min-axis box fast path and the GJK fall-back,
// kept in step with the originals by hand.

import (
	"foundry/internal/meshcollide"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// sceneryProxy is the immovable body a prop presents to the separation code.
//
// BoundingAABB with a rotation is exactly what ShapeFor turns into an OBB, so a
// prop dropped at an angle is solid at that angle rather than inside a box
// squared to the world.
//
// Scale stays at identity and BoundOffset at zero: the prop's own scale is
// already in BoundHalfExtents, and its model's origin-to-centre offset is
// already in BoundCenter — so the proxy is simply a box sitting at that centre,
// and nothing here has to re-derive either.
func sceneryProxy(s *world.Scenery) (world.Entity, bool) {
	if !s.Solid || !s.HasBounds() {
		return world.Entity{}, false
	}
	h := s.BoundHalfExtents()
	return world.Entity{
		ID:           s.ID,
		Position:     s.BoundCenter(),
		Rotation:     s.Rotation,
		Scale:        mgl32.Vec3{1, 1, 1},
		BoundingKind: world.BoundingAABB,
		BoundHalfX:   h.X(),
		BoundHalfY:   h.Y(),
		BoundHalfZ:   h.Z(),
		Collision:    world.EntityCollision{}, // immovable: never pushed, only pushing
	}, true
}

// ResolveSceneryCollisions pushes every body that resolves against the world
// out of every solid prop in the box mode, and reports the indices of the
// entities it moved. A cooked mesh in the hulls or mesh mode is not here: it
// is part of the resolve chain a body runs as it moves (scenery_mesh.go).
//
// The props come from the placement broadphase (scenery_index.go), so a body
// is tested against the few whose boxes its own box reaches rather than
// every prop in the level.
//
// Only bodies in world space take part. A prop is authored in world
// coordinates and an entity riding a vehicle describes itself in that vehicle's
// — comparing the two would be subtracting numbers that do not mean the same
// thing, which is the same reason the entity pass skips cross-frame pairs.
func ResolveSceneryCollisions(lvl *world.Level, player *world.Entity) []int {
	if lvl == nil || len(lvl.Scenery) == 0 || !world.EntityWorldCollisionEnabled {
		return nil
	}
	ix := sceneryIndexFor(lvl)
	if ix == nil || ix.byMode[meshcollide.ModeBox] == 0 {
		return nil
	}
	start := profStart()
	defer profStop(start, &sceneryStats.resolveTime)

	// against resolves one body against the box-mode props its box reaches.
	// The proxy is immovable, so only the first result is ever true.
	against := func(e *world.Entity) bool {
		lo, hi, ok := e.CollisionAABB()
		if !ok {
			return false
		}
		moved := false
		var near [16]*sceneryEntry
		cand := near[:0]
		ix.overlapping(lo, hi, func(se *sceneryEntry) bool {
			if se.mode == meshcollide.ModeBox {
				cand = append(cand, se)
			}
			return true
		})
		for _, se := range cand {
			if mv, _ := resolveEntityOverlap(e, &se.proxy); mv {
				moved = true
			}
		}
		return moved
	}

	var out []int
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		// CollidesWithWorld rather than CollidesWithEntities — see the file
		// comment. HasBounds because a body with no volume has nothing to
		// separate, and the resolver would build a degenerate shape for it.
		if e.Frame != "" || !e.CollidesWithWorld() || !e.HasBounds() {
			continue
		}
		if against(e) {
			out = append(out, i)
		}
	}

	// The player takes part unconditionally: it resolves against the world by
	// construction (the main loop's own path, not the flag), so it resolves
	// against the level's furniture too.
	if player != nil && player.Frame == "" && player.HasBounds() {
		against(player)
	}
	return out
}
