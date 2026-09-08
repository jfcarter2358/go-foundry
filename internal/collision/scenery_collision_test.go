package collision

// scenery_collision_test.go — what a prop is solid to, and what it is not.
//
// The resolver itself is entity_collision.go's and is already covered; what is
// new here is the gate. Scenery is deliberately wired to the *world* switch and
// the world flag rather than the entity ones, so the interesting cases are all
// about which body a prop stops and which it lets through — a wiring mistake
// there is silent, and shows up as a level where half the props are furniture
// and half are fog.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// sceneryBox returns a solid prop whose model bounds are a 2x2x2 box standing
// on its own origin at pos.
func sceneryBox(id string, pos mgl32.Vec3) world.Scenery {
	s := world.NewScenery(id, "models/box.fmdl", pos)
	s.LocalMin = mgl32.Vec3{-1, 0, -1}
	s.LocalMax = mgl32.Vec3{1, 2, 1}
	return s
}

// walkerAt returns an entity that collides with the world, sized like a person,
// standing with its bound box centred on pos.
func walkerAt(id string, pos mgl32.Vec3) world.Entity {
	return world.Entity{
		ID:           id,
		Position:     pos,
		Scale:        mgl32.Vec3{1, 1, 1},
		BoundingKind: world.BoundingAABB,
		BoundHalfX:   0.3,
		BoundHalfY:   0.9,
		BoundHalfZ:   0.3,
		Visible:      true,
		Collision:    world.EntityCollision{World: true, Movable: true},
	}
}

// A walker that has clipped the edge of a prop is pushed back out of it, along
// the shallowest axis — which is what "walked into a rock" looks like.
//
// The overlap is an edge one rather than a body buried in the middle of the
// box. That is not a dodge: the min-axis push separates a shallow overlap in
// one pass by construction, and a body wholly inside a larger box has no
// shallow axis to leave by. Scenery inherits that from the entity resolver it
// reuses, and it is the same for a crate inside a crate.
func TestSolidSceneryPushesAWalkerOut(t *testing.T) {
	const start = float32(1.15) // half-extent 0.3, so it reaches 0.85 into a box ending at 1
	lvl := &world.Level{
		Scenery:  []world.Scenery{sceneryBox("rock", mgl32.Vec3{0, 0, 0})},
		Entities: []world.Entity{walkerAt("guard", mgl32.Vec3{start, 1, 0})},
	}

	moved := ResolveSceneryCollisions(lvl, nil)
	if len(moved) != 1 || moved[0] != 0 {
		t.Fatalf("a walker standing inside a solid prop was not moved: %v", moved)
	}
	if lvl.Entities[0].Position.X() <= start {
		t.Fatalf("pushed the wrong way: x %v, was %v", lvl.Entities[0].Position.X(), start)
	}
	// And it ends up outside, not merely somewhere else. The push lands the two
	// boxes exactly flush, so what is checked is that nothing is left of the
	// overlap beyond float noise — an exact zero would be asserting that
	// 1.15 - 0.15 rounds to 1.0 in float32, which is not what this is about.
	amin, amax, _ := lvl.Entities[0].CollisionAABB()
	bmin, bmax, _ := lvl.Scenery[0].CollisionAABB()
	if overlap, touching := boxOverlaps(amin, amax, bmin, bmax); touching && overlap.X() > 1e-4 {
		t.Errorf("still %v deep after the resolve: %v..%v vs %v..%v",
			overlap.X(), amin, amax, bmin, bmax)
	}
}

func TestSceneryThatIsNotSolidStopsNothing(t *testing.T) {
	s := sceneryBox("grass", mgl32.Vec3{0, 0, 0})
	s.Solid = false
	lvl := &world.Level{
		Scenery:  []world.Scenery{s},
		Entities: []world.Entity{walkerAt("guard", mgl32.Vec3{0.2, 1, 0})},
	}
	if moved := ResolveSceneryCollisions(lvl, nil); len(moved) != 0 {
		t.Fatalf("a prop marked not solid moved something: %v", moved)
	}
	if lvl.Entities[0].Position != (mgl32.Vec3{0.2, 1, 0}) {
		t.Error("a prop marked not solid displaced an entity")
	}
}

// An entity that has deliberately been left to pass through geometry passes
// through props too — that is the whole reason scenery is gated on the world
// flag rather than the entity one.
func TestSceneryLetsThroughWhatTheWorldLetsThrough(t *testing.T) {
	ghost := walkerAt("ghost", mgl32.Vec3{0.2, 1, 0})
	ghost.Collision.World = false
	lvl := &world.Level{
		Scenery:  []world.Scenery{sceneryBox("rock", mgl32.Vec3{0, 0, 0})},
		Entities: []world.Entity{ghost},
	}
	if moved := ResolveSceneryCollisions(lvl, nil); len(moved) != 0 {
		t.Fatalf("an entity with world collision off was pushed by a prop: %v", moved)
	}
}

// The player resolves against the world by construction rather than by a flag,
// so it must be stopped without opting in to anything.
func TestSolidSceneryStopsThePlayer(t *testing.T) {
	lvl := &world.Level{Scenery: []world.Scenery{sceneryBox("rock", mgl32.Vec3{0, 0, 0})}}
	player := world.NewPlayer(mgl32.Vec3{0.2, 0.9, 0})

	ResolveSceneryCollisions(lvl, player)
	if player.Position == (mgl32.Vec3{0.2, 0.9, 0}) {
		t.Fatal("the player walked into a solid prop and stayed there")
	}
}

// A prop is immovable whatever it is shoved with. Nothing writes back to the
// proxy, but a future change that does would silently start drifting a level's
// furniture around during play.
func TestSceneryNeverMoves(t *testing.T) {
	lvl := &world.Level{
		Scenery:  []world.Scenery{sceneryBox("rock", mgl32.Vec3{0, 0, 0})},
		Entities: []world.Entity{walkerAt("guard", mgl32.Vec3{0.2, 1, 0})},
	}
	before := lvl.Scenery[0].Position
	ResolveSceneryCollisions(lvl, nil)
	if lvl.Scenery[0].Position != before {
		t.Errorf("a prop moved: %v, was %v", lvl.Scenery[0].Position, before)
	}
}

// The engine-wide switch (key 8) has to reach scenery as well, or "collision is
// off" would still leave every prop in the level solid.
func TestSceneryHonoursTheWorldCollisionMasterSwitch(t *testing.T) {
	prev := world.EntityWorldCollisionEnabled
	world.EntityWorldCollisionEnabled = false
	t.Cleanup(func() { world.EntityWorldCollisionEnabled = prev })

	lvl := &world.Level{
		Scenery:  []world.Scenery{sceneryBox("rock", mgl32.Vec3{0, 0, 0})},
		Entities: []world.Entity{walkerAt("guard", mgl32.Vec3{0.2, 1, 0})},
	}
	if moved := ResolveSceneryCollisions(lvl, nil); len(moved) != 0 {
		t.Fatalf("props stayed solid with world collision switched off: %v", moved)
	}
}

// A prop authored in world space and a body riding a vehicle describe
// themselves in different coordinate systems; comparing the two would be
// subtracting numbers that do not mean the same thing.
func TestSceneryLeavesFramedEntitiesAlone(t *testing.T) {
	rider := walkerAt("rider", mgl32.Vec3{0.2, 1, 0})
	rider.Frame = "ship"
	lvl := &world.Level{
		Scenery:  []world.Scenery{sceneryBox("rock", mgl32.Vec3{0, 0, 0})},
		Entities: []world.Entity{rider},
	}
	if moved := ResolveSceneryCollisions(lvl, nil); len(moved) != 0 {
		t.Fatalf("a prop resolved against an entity in another frame: %v", moved)
	}
}

// A prop whose model never loaded has no volume. It must not become an
// invisible wall at the origin of its own bounding box.
func TestSceneryWithNoMeshIsNotSolid(t *testing.T) {
	lvl := &world.Level{
		Scenery:  []world.Scenery{world.NewScenery("broken", "models/gone.fmdl", mgl32.Vec3{0, 0, 0})},
		Entities: []world.Entity{walkerAt("guard", mgl32.Vec3{0, 1, 0})},
	}
	if moved := ResolveSceneryCollisions(lvl, nil); len(moved) != 0 {
		t.Fatalf("a prop with no mesh pushed something: %v", moved)
	}
}

// A prop dropped at an angle is solid at that angle. A box turned 45 degrees
// reaches further along its diagonal than an axis-aligned one does, and a body
// standing in that corner has to be pushed out of it.
func TestRotatedSceneryIsSolidAtItsOwnAngle(t *testing.T) {
	// x = 1.6 with a half-extent of 0.3 reaches in to 1.3: past the unrotated
	// box's +X face at 1.0, but inside the turned one, whose corner reaches
	// sqrt(2) ~= 1.414 along X. So the same body is clear of one and inside the
	// other, and the only difference is the rotation.
	const start = float32(1.6)

	square := &world.Level{
		Scenery:  []world.Scenery{sceneryBox("rock", mgl32.Vec3{0, 0, 0})},
		Entities: []world.Entity{walkerAt("guard", mgl32.Vec3{start, 1, 0})},
	}
	if moved := ResolveSceneryCollisions(square, nil); len(moved) != 0 {
		t.Fatalf("an unrotated prop reached past its own face: %v", moved)
	}

	turned := sceneryBox("rock", mgl32.Vec3{0, 0, 0})
	turned.Rotation = mgl32.Vec3{0, mgl32.DegToRad(45), 0}
	lvl := &world.Level{
		Scenery:  []world.Scenery{turned},
		Entities: []world.Entity{walkerAt("guard", mgl32.Vec3{start, 1, 0})},
	}
	if moved := ResolveSceneryCollisions(lvl, nil); len(moved) != 1 {
		t.Fatalf("a turned prop did not stop a body inside its corner: %v", moved)
	}
}
