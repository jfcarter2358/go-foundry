package collision

// shape_test.go — the support functions, checked against the shapes themselves.
//
// A support function is right when no point of the shape reaches farther along
// the query direction than the point it returns. That is the whole contract, and
// it is checkable without trusting any of the closed forms: scatter points
// inside the shape, and assert none of them beats the answer.
//
// The second check ties this package to world.ShapeContainsPoint. The two
// describe the same volume — one as a surface, one as a membership test — and a
// script's `enter` firing on a volume the resolver pushes differently would be a
// bug nobody would find by reading either file alone.

import (
	"foundry/internal/world"
	"math"
	"math/rand"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// dirs is a fixed spread of query directions: the axes, the diagonals, and a
// deterministic scatter. Fixed rather than random so a failure reproduces.
func dirs() []mgl32.Vec3 {
	out := []mgl32.Vec3{
		{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1},
		{1, 1, 1}, {-1, 1, -1}, {1, -1, 1}, {-1, -1, -1},
		{1, 0.01, 0}, {0, 1, 0.01}, {0.01, 0, 1},
	}
	r := rand.New(rand.NewSource(20260824))
	for i := 0; i < 200; i++ {
		v := mgl32.Vec3{
			float32(r.NormFloat64()), float32(r.NormFloat64()), float32(r.NormFloat64()),
		}
		if v.Len() > 1e-6 {
			out = append(out, v.Normalize())
		}
	}
	return out
}

// samplesInside scatters points strictly inside a shape, in its own local
// parametrisation, then carries them to world space the way the shape does.
func samplesInside(kind world.BoundingType, c, half mgl32.Vec3, rot mgl32.Mat4, n int) []mgl32.Vec3 {
	r := rand.New(rand.NewSource(99))
	out := make([]mgl32.Vec3, 0, n)
	for len(out) < n {
		var p mgl32.Vec3
		switch kind {
		case world.BoundingSphere:
			u := mgl32.Vec3{
				float32(r.NormFloat64()), float32(r.NormFloat64()), float32(r.NormFloat64()),
			}
			if u.Len() < 1e-9 {
				continue
			}
			// A direction times a radius ≤ 1 lands uniformly enough inside the
			// unit ball for a bound check; uniformity is not what is being tested.
			u = u.Normalize().Mul(float32(math.Cbrt(r.Float64())))
			p = mgl32.Vec3{u.X() * half.X(), u.Y() * half.Y(), u.Z() * half.Z()}
		case world.BoundingCylinder:
			t := r.Float64() * 2 * math.Pi
			rad := float32(math.Sqrt(r.Float64()))
			p = mgl32.Vec3{
				rad * float32(math.Cos(t)) * half.X(),
				(float32(r.Float64())*2 - 1) * half.Y(),
				rad * float32(math.Sin(t)) * half.Z(),
			}
		default:
			p = mgl32.Vec3{
				(float32(r.Float64())*2 - 1) * half.X(),
				(float32(r.Float64())*2 - 1) * half.Y(),
				(float32(r.Float64())*2 - 1) * half.Z(),
			}
		}
		out = append(out, c.Add(rotate(rot, p)))
	}
	return out
}

// shapeCase is one kind at one placement, with the entity that produces it.
type shapeCase struct {
	name string
	kind world.BoundingType
	ent  world.Entity
}

func shapeCases() []shapeCase {
	base := func(kind world.BoundingType, half mgl32.Vec3, rot mgl32.Vec3, scale mgl32.Vec3, off mgl32.Vec3) world.Entity {
		return world.Entity{
			ID: "s", Position: mgl32.Vec3{3, -2, 7},
			Rotation: rot, Scale: scale, BoundOffset: off,
			BoundingKind: kind,
			BoundHalfX:   half.X(), BoundHalfY: half.Y(), BoundHalfZ: half.Z(),
		}
	}
	one := mgl32.Vec3{1, 1, 1}
	tilt := mgl32.Vec3{0.4, 0.9, -0.3}
	return []shapeCase{
		{"aabb", world.BoundingAABB, base(world.BoundingAABB, mgl32.Vec3{1, 2, 0.5}, mgl32.Vec3{}, one, mgl32.Vec3{})},
		{"aabb-offset-scaled", world.BoundingAABB, base(world.BoundingAABB, mgl32.Vec3{1, 2, 0.5}, mgl32.Vec3{}, mgl32.Vec3{2, 0.5, 1.5}, mgl32.Vec3{0, 1, 0})},
		{"obb", world.BoundingAABB, base(world.BoundingAABB, mgl32.Vec3{1, 2, 0.5}, tilt, one, mgl32.Vec3{0, 0.7, 0})},
		{"sphere", world.BoundingSphere, base(world.BoundingSphere, mgl32.Vec3{1.5, 1.5, 1.5}, mgl32.Vec3{}, one, mgl32.Vec3{})},
		{"ellipsoid-rotated", world.BoundingSphere, base(world.BoundingSphere, mgl32.Vec3{2, 0.5, 1}, tilt, one, mgl32.Vec3{})},
		{"cylinder", world.BoundingCylinder, base(world.BoundingCylinder, mgl32.Vec3{0.6, 1.8, 0.6}, mgl32.Vec3{}, one, mgl32.Vec3{0, 1.8, 0})},
		{"cylinder-rotated", world.BoundingCylinder, base(world.BoundingCylinder, mgl32.Vec3{0.6, 1.8, 0.9}, tilt, one, mgl32.Vec3{})},
	}
}

func TestSupportIsFarthestPointInTheShape(t *testing.T) {
	for _, tc := range shapeCases() {
		t.Run(tc.name, func(t *testing.T) {
			s, ok := ShapeFor(&tc.ent)
			if !ok {
				t.Fatal("ShapeFor refused a bounded entity")
			}
			rot, _ := entityRotation(&tc.ent)
			pts := samplesInside(tc.kind, tc.ent.BoundCenter(), tc.ent.BoundHalfExtents(), rot, 4000)

			for _, d := range dirs() {
				sup := s.Support(d).Dot(d)
				for _, p := range pts {
					if got := p.Dot(d); got > sup+1e-4 {
						t.Fatalf("dir %v: interior point reached %v, past the support's %v", d, got, sup)
					}
				}
			}
		})
	}
}

func TestSupportPointsLandOnTheShapeItself(t *testing.T) {
	// The support point must be *on* the volume world.ShapeContainsPoint
	// describes: inside when nudged toward the centre, outside when nudged away.
	// This is what keeps the resolver and the script `enter` test agreeing.
	for _, tc := range shapeCases() {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := ShapeFor(&tc.ent)
			c := tc.ent.BoundCenter()
			const nudge = 0.01

			for _, d := range dirs() {
				p := s.Support(d)
				out := p.Sub(c)
				if out.Len() < 1e-6 {
					continue
				}
				n := out.Normalize()

				if !tc.ent.ContainsPoint(p.Sub(n.Mul(nudge))) {
					t.Fatalf("dir %v: just inside the support point reads as outside the volume", d)
				}
				if tc.ent.ContainsPoint(p.Add(n.Mul(nudge * 10))) {
					t.Fatalf("dir %v: well past the support point still reads as inside", d)
				}
			}
		})
	}
}

func TestShapeAABBMatchesTheEntityBox(t *testing.T) {
	for _, tc := range shapeCases() {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := ShapeFor(&tc.ent)
			min, max := ShapeAABB(s)

			// Every interior point must be in the box.
			rot, _ := entityRotation(&tc.ent)
			for _, p := range samplesInside(tc.kind, tc.ent.BoundCenter(), tc.ent.BoundHalfExtents(), rot, 2000) {
				for a := 0; a < 3; a++ {
					if p[a] < min[a]-1e-4 || p[a] > max[a]+1e-4 {
						t.Fatalf("interior point %v outside ShapeAABB %v..%v", p, min, max)
					}
				}
			}

			// For a box, it must agree exactly with the entity's own collision
			// box — the two are different derivations of the same thing.
			if tc.kind == world.BoundingAABB {
				emin, emax, ok := tc.ent.CollisionAABB()
				if !ok {
					t.Fatal("CollisionAABB refused a bounded entity")
				}
				if !vecNear(min, emin, 1e-4) || !vecNear(max, emax, 1e-4) {
					t.Errorf("ShapeAABB %v..%v disagrees with CollisionAABB %v..%v", min, max, emin, emax)
				}
			}
		})
	}
}

func TestShapeForPicksTheAuthoredKind(t *testing.T) {
	e := world.Entity{BoundHalfX: 1, BoundHalfY: 1, BoundHalfZ: 1, Scale: mgl32.Vec3{1, 1, 1}}

	e.BoundingKind = world.BoundingAABB
	if s, _ := ShapeFor(&e); !IsAxisAlignedBox(s) {
		t.Errorf("unrotated aabb became %T, want Box", s)
	}

	e.Rotation = mgl32.Vec3{0, 0.5, 0}
	s, _ := ShapeFor(&e)
	if _, ok := s.(OBB); !ok {
		t.Errorf("rotated aabb became %T, want OBB", s)
	}
	if IsAxisAlignedBox(s) {
		t.Error("a rotated box claimed the axis-aligned fast path")
	}

	e.Rotation = mgl32.Vec3{}
	e.BoundingKind = world.BoundingSphere
	if s, _ := ShapeFor(&e); func() bool { _, ok := s.(Ellipsoid); return !ok }() {
		t.Errorf("sphere became %T, want Ellipsoid", s)
	}
	e.BoundingKind = world.BoundingCylinder
	if s, _ := ShapeFor(&e); func() bool { _, ok := s.(Cylinder); return !ok }() {
		t.Errorf("cylinder became %T, want Cylinder", s)
	}

	// No bounds means no shape, rather than a zero-sized one.
	e.BoundHalfY = 0
	if _, ok := ShapeFor(&e); ok {
		t.Error("ShapeFor produced a shape for an entity with no volume")
	}
	if _, ok := ShapeFor(nil); ok {
		t.Error("ShapeFor produced a shape for a nil entity")
	}
}
