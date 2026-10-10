package collision

// frame_entity_test.go — entities that live in a moving frame.
//
// The claim Phase 2 rests on is that nothing in the resolve path had to learn
// what a frame is: an entity in a frame stores its position in that frame's
// coordinates, resolves against that frame's chunks, and the arithmetic in
// between is the arithmetic that was already there. These check that, and check
// the one thing that genuinely changes — that a rider is carried by its frame
// without being moved.

import (
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// shipLevel is collisionTestLevel's two sectors, mounted in a carrier entity's
// frame rather than in the world, plus the carrier itself out in the world.
func shipLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := collisionTestLevel()
	lvl.Chunks[0].FrameOwner = "ship"
	lvl.Entities = []world.Entity{{
		ID: "ship", Position: mgl32.Vec3{0, 0, 0}, Scale: mgl32.Vec3{1, 1, 1},
	}}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	return lvl
}

func moveShip(t *testing.T, lvl *world.Level, pos mgl32.Vec3, rot mgl32.Vec3) {
	t.Helper()
	s := world.FindEntity(lvl, "ship")
	s.Position, s.Rotation = pos, rot
	world.RefreshFrames(lvl, lvl.Frames)
}

func TestEntityInAFrameResolvesAgainstTheFrameNotTheWorld(t *testing.T) {
	withCollisionMasters(t, true, true)
	lvl := shipLevel(t)
	moveShip(t, lvl, mgl32.Vec3{900, 40, -600}, mgl32.Vec3{0, 1.1, 0})

	// A crate dropped inside the ship, in the ship's coordinates.
	crate := solidEntity("crate", mgl32.Vec3{5, 4, 5})
	crate.Frame = "ship"
	crate.BoundOffset = mgl32.Vec3{0, 0.5, 0}
	lvl.Entities = append(lvl.Entities, crate)

	for i := 0; i < 300; i++ {
		EntityUpdate(lvl, 1.0/60.0, true, nil)
	}
	got := world.FindEntity(lvl, "crate")

	// It lands on the ship's deck — which is y=0 in the ship's coordinates,
	// nowhere near y=0 in the world.
	if math.Abs(float64(got.Position.Y())) > 1e-2 {
		t.Errorf("crate came to rest at local y=%v, want 0 (the deck)", got.Position.Y())
	}
	if !got.Collision.OnGround {
		t.Error("crate is resting on the deck but does not report being grounded")
	}
	if got.CurrentChunk == "" {
		t.Error("crate is resting on the deck but is in no chunk")
	}
	// And in world terms it is up where the ship is.
	if w := lvl.Frames.WorldPosition(got); math.Abs(float64(w.Y()-40)) > 1e-2 {
		t.Errorf("crate is at world y=%v, want 40 (the ship's altitude)", w.Y())
	}
}

func TestARiderIsCarriedByItsFrameWithoutMoving(t *testing.T) {
	withCollisionMasters(t, true, true)
	lvl := shipLevel(t)

	crate := solidEntity("crate", mgl32.Vec3{5, 4, 5})
	crate.Frame = "ship"
	crate.BoundOffset = mgl32.Vec3{0, 0.5, 0}
	lvl.Entities = append(lvl.Entities, crate)
	for i := 0; i < 300; i++ {
		EntityUpdate(lvl, 1.0/60.0, true, nil)
	}

	settled := world.FindEntity(lvl, "crate").Position

	// Fly the ship a long way and spin it. The rider's own coordinates must not
	// change at all — being carried is the absence of motion, not a correction
	// applied to it.
	moveShip(t, lvl, mgl32.Vec3{5000, 900, -3000}, mgl32.Vec3{0.3, 2.4, -0.7})
	for i := 0; i < 60; i++ {
		EntityUpdate(lvl, 1.0/60.0, true, nil)
	}

	after := world.FindEntity(lvl, "crate").Position
	if !vecNear(after, settled, 1e-3) {
		t.Errorf("the rider drifted from %v to %v while its frame moved", settled, after)
	}

	// But it *has* moved in the world, by exactly what the frame did.
	wantWorld := lvl.Frames.Frame("ship").ToWorld(after)
	if got := lvl.Frames.WorldPosition(world.FindEntity(lvl, "crate")); !vecNear(got, wantWorld, 1e-3) {
		t.Errorf("world position %v disagrees with the frame's own transform %v", got, wantWorld)
	}
}

func TestFrameEntityIsBlockedByTheFramesOwnWalls(t *testing.T) {
	withCollisionMasters(t, true, true)
	lvl := shipLevel(t)
	moveShip(t, lvl, mgl32.Vec3{100, 10, 100}, mgl32.Vec3{0, 0.8, 0})

	crate := solidEntity("crate", mgl32.Vec3{5, 0.5, 5})
	crate.Frame = "ship"
	crate.BoundOffset = mgl32.Vec3{0, 0.5, 0}
	crate.Collision.Gravity = false
	crate.Velocity = mgl32.Vec3{-8, 0, 0} // straight at the ship's x=0 bulkhead
	lvl.Entities = append(lvl.Entities, crate)

	for i := 0; i < 120; i++ {
		EntityUpdate(lvl, 1.0/60.0, true, nil)
	}
	got := world.FindEntity(lvl, "crate")
	if got.Position.X() < 0 {
		t.Errorf("crate went through the bulkhead to local x=%v", got.Position.X())
	}
}

func TestEntitiesInDifferentFramesDoNotPushEachOther(t *testing.T) {
	// A rider is inside its vehicle's hull by definition. Resolving that overlap
	// would eject it through the wall, so pairs in different frames are skipped.
	withCollisionMasters(t, true, true)
	lvl := shipLevel(t)

	// The hull: a big solid box out in the world, wrapping the ship's origin.
	hull := solidEntity("hull", mgl32.Vec3{0, 0, 0})
	hull.BoundHalfX, hull.BoundHalfY, hull.BoundHalfZ = 30, 10, 30
	hull.Collision.Gravity = false
	hull.Collision.Movable = false
	lvl.Entities = append(lvl.Entities, hull)

	inside := solidEntity("inside", mgl32.Vec3{5, 1, 5})
	inside.Frame = "ship"
	inside.Collision.Gravity = false
	lvl.Entities = append(lvl.Entities, inside)

	before := world.FindEntity(lvl, "inside").Position
	for i := 0; i < 30; i++ {
		EntityUpdate(lvl, 1.0/60.0, true, nil)
	}
	after := world.FindEntity(lvl, "inside").Position
	if !vecNear(after, before, 1e-4) {
		t.Errorf("the rider was shoved from %v to %v by the hull it lives inside", before, after)
	}
}

func TestWorldEntitiesAreUnaffectedByFramesExisting(t *testing.T) {
	// The regression that matters most: adding frames to a level must not
	// change how anything already in it behaves.
	withCollisionMasters(t, true, true)

	plain := collisionTestLevel()
	if err := world.RebuildIndexes(plain); err != nil {
		t.Fatal(err)
	}
	withFrames := collisionTestLevel()
	withFrames.Entities = []world.Entity{{ID: "ship", Position: mgl32.Vec3{700, 300, 700}, Scale: mgl32.Vec3{1, 1, 1}}}
	// A second chunk, in a frame, that the world entity must never notice.
	interior := collisionTestLevel().Chunks[0]
	interior.ID = "interior"
	interior.FrameOwner = "ship"
	withFrames.Chunks = append(withFrames.Chunks, interior)
	if err := world.RebuildIndexes(withFrames); err != nil {
		t.Fatal(err)
	}

	for _, lvl := range []*world.Level{plain, withFrames} {
		e := solidEntity("crate", mgl32.Vec3{5, 4, 5})
		e.BoundOffset = mgl32.Vec3{0, 0.5, 0}
		lvl.Entities = append(lvl.Entities, e)
	}
	for i := 0; i < 300; i++ {
		EntityUpdate(plain, 1.0/60.0, true, nil)
		EntityUpdate(withFrames, 1.0/60.0, true, nil)
	}

	a := world.FindEntity(plain, "crate").Position
	b := world.FindEntity(withFrames, "crate").Position
	if !vecNear(a, b, 1e-5) {
		t.Errorf("a world entity settled at %v with no frames and %v with frames present", a, b)
	}
}
