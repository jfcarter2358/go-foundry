package collision

// entity_shape_push_test.go — the push-apart, now that it knows what shape
// things are.
//
// The rule these guard: an axis-aligned box pair must behave exactly as it
// always did, because every stack in every existing level is tuned against it.
// Anything rotated or round gets the true minimum translation instead of the
// axis-aligned approximation of it, which is a behaviour change and is the
// point.

import (
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// roundEntity is solidEntity's counterpart for the non-box kinds.
func roundEntity(id string, kind world.BoundingType, pos mgl32.Vec3, half mgl32.Vec3) world.Entity {
	return world.Entity{
		ID: id, Position: pos, Scale: mgl32.Vec3{1, 1, 1},
		BoundingKind: kind,
		BoundHalfX:   half.X(), BoundHalfY: half.Y(), BoundHalfZ: half.Z(),
		Visible:   true,
		Collision: world.EntityCollision{World: true, Entities: true, Gravity: true, Movable: true},
	}
}

func TestSpheresPushApartAlongTheLineBetweenThem(t *testing.T) {
	// Diagonally offset spheres. As boxes they would have come apart along an
	// axis — a visibly wrong direction for two round things touching.
	const s = float32(1.0606602) // 1.5/√2: centres 1.5 apart, radii 1+1
	a := roundEntity("a", world.BoundingSphere, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 1, 1})
	b := roundEntity("b", world.BoundingSphere, mgl32.Vec3{s, s, 0}, mgl32.Vec3{1, 1, 1})

	before := b.Position.Sub(a.Position)
	movedA, movedB := resolveEntityOverlap(&a, &b)
	if !movedA || !movedB {
		t.Fatalf("neither sphere moved (a=%v b=%v)", movedA, movedB)
	}

	// Both are free and equal, so each takes half along the centre line.
	pushA := a.Position
	if math.Abs(float64(pushA.X()-pushA.Y())) > 1e-3 {
		t.Errorf("a moved %v — not along the diagonal", pushA)
	}
	if math.Abs(float64(pushA.Z())) > 5e-3 {
		t.Errorf("a picked up %v of Z from a push with no Z in it", pushA.Z())
	}

	// And they are now exactly touching rather than overlapping.
	after := b.Position.Sub(a.Position)
	if got := after.Len(); math.Abs(float64(got-2)) > 1e-2 {
		t.Errorf("centres ended %v apart, want 2 (the sum of the radii)", got)
	}
	if after.Normalize().Sub(before.Normalize()).Len() > 5e-3 {
		t.Errorf("the push rotated the pair: %v -> %v", before, after)
	}
}

func TestRotatedBoxPushesAlongItsOwnFace(t *testing.T) {
	// A box turned 45° about Y, overlapped corner-on. This is the configuration
	// where knowing the shape actually changes the answer: the diamond's own
	// face normal is 0.076 deep, while the axis-aligned box around it reads
	// 0.407 on either axis. The old resolver would have shoved this five times
	// too far, in a direction the geometry does not have a face along.
	a := solidEntity("a", mgl32.Vec3{0, 0, 0})
	a.Rotation = mgl32.Vec3{0, math.Pi / 4, 0}
	a.Collision.Movable = false // immovable, so all of the push lands on b

	start := mgl32.Vec3{0.8, 0, 0.8}
	b := solidEntity("b", start)

	if _, movedB := resolveEntityOverlap(&a, &b); !movedB {
		t.Fatal("the movable box was not pushed out of the rotated one")
	}
	d := b.Position.Sub(start)
	if d.Len() < 1e-4 {
		t.Fatal("b did not actually move")
	}
	if got := d.Len(); math.Abs(float64(got-0.0757)) > 5e-3 {
		t.Errorf("pushed %v, want the face depth 0.076 rather than the box reading of 0.407", got)
	}
	n := d.Normalize()
	if math.Abs(float64(n.X()-n.Z())) > 0.02 {
		t.Errorf("pushed along %v — want the 45° face normal, with X ≈ Z", n)
	}
	if math.Abs(float64(n.Y())) > 0.02 {
		t.Errorf("pushed along %v — a horizontal overlap should not lift anything", n)
	}
}

func TestShapeAwarePushActuallySeparates(t *testing.T) {
	// Whatever shape the pair is, one resolve must take the overlap away.
	//
	// "Away" means depth ≈ 0 rather than Overlap() == false: the resolver
	// separates to exactly touching, and an exact touch is still contact as far
	// as GJK is concerned. Leaving them touching is the intended result — a
	// crate resting against a wall is touching it.
	kinds := []world.BoundingType{world.BoundingAABB, world.BoundingSphere, world.BoundingCylinder}
	names := []string{"aabb", "sphere", "cylinder"}

	for ia, ka := range kinds {
		for ib, kb := range kinds {
			a := roundEntity("a", ka, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 1.4, 0.8})
			b := roundEntity("b", kb, mgl32.Vec3{0.9, 0.5, 0.4}, mgl32.Vec3{0.7, 1, 1.2})
			a.Rotation = mgl32.Vec3{0.2, 0.6, -0.1}

			before := penetrationDepth(t, &a, &b)
			if before <= 0 {
				t.Fatalf("%s vs %s: the test case does not overlap to begin with", names[ia], names[ib])
			}
			resolveEntityOverlap(&a, &b)
			after := penetrationDepth(t, &a, &b)

			if after > 1e-2 {
				t.Errorf("%s vs %s: depth went %v -> %v, want ~0", names[ia], names[ib], before, after)
			}
		}
	}
}

// penetrationDepth is how deeply two entities currently interpenetrate, or 0
// when they do not.
func penetrationDepth(t *testing.T, a, b *world.Entity) float32 {
	t.Helper()
	sa, oka := ShapeFor(a)
	sb, okb := ShapeFor(b)
	if !oka || !okb {
		t.Fatal("an entity in this test has no bounding volume")
	}
	_, depth, ok := Penetration(sa, sb)
	if !ok {
		return 0
	}
	return depth
}

func TestGroundedRoundEntityIsNotPushedDown(t *testing.T) {
	// The stacking invariant, on a shape that does not go through the
	// axis-aligned path: a grounded entity must never take the downward part of
	// a separation, or the world resolve spends every frame undoing it.
	ground := roundEntity("ground", world.BoundingSphere, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 1, 1})
	ground.Collision.OnGround = true
	above := roundEntity("above", world.BoundingSphere, mgl32.Vec3{0, 1.5, 0}, mgl32.Vec3{1, 1, 1})

	startY := ground.Position.Y()
	resolveEntityOverlap(&ground, &above)

	if ground.Position.Y() < startY-1e-6 {
		t.Errorf("a grounded entity was pushed down to %v from %v", ground.Position.Y(), startY)
	}
	if above.Position.Y() <= 1.5 {
		t.Errorf("the whole separation should have gone to the airborne one; it is at %v", above.Position.Y())
	}
}

func TestAxisAlignedBoxesKeepTheirOwnPath(t *testing.T) {
	// Not a behaviour test so much as a guard on the short-circuit staying
	// reachable: if this stops being true, every level's tuned stacking
	// silently starts going through EPA instead.
	e := solidEntity("a", mgl32.Vec3{})
	if !isAxisAlignedBox(&e) {
		t.Fatal("a plain unrotated box did not take the axis-aligned path")
	}
	e.Rotation = mgl32.Vec3{0, 0.01, 0}
	if isAxisAlignedBox(&e) {
		t.Error("a rotated box claimed the axis-aligned path")
	}
	e.Rotation = mgl32.Vec3{}
	e.BoundingKind = world.BoundingCylinder
	if isAxisAlignedBox(&e) {
		t.Error("a cylinder claimed the axis-aligned path")
	}
}
