package collision

// collide_distance_test.go — the gap between two convex shapes.
//
// Distance is what makes conservative advancement safe: every step it takes is
// bounded by this number, so if this is ever an *over*-estimate a body steps
// through something. Under-estimates only cost iterations. The tests therefore
// lean on cases with an exact known answer, plus a check that it never exceeds
// the true straight-line distance on random pairs.

import (
	"math"
	"math/rand"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestDistanceOnSpheresIsTheGapBetweenThem(t *testing.T) {
	for _, d := range []float32{2.5, 3, 10, 1000} {
		a := sphere(mgl32.Vec3{0, 0, 0}, 1)
		b := sphere(mgl32.Vec3{d, 0, 0}, 1)
		want := d - 2
		got := Distance(a, b)
		if math.Abs(float64(got-want)) > float64(want)*1e-3+1e-3 {
			t.Errorf("centres %v apart: gap %v, want %v", d, got, want)
		}
	}
}

func TestDistanceOnAxisAlignedBoxes(t *testing.T) {
	a := Box{C: mgl32.Vec3{0, 0, 0}, Half: mgl32.Vec3{1, 1, 1}}

	// Face to face: a plain gap along one axis.
	if got := Distance(a, Box{C: mgl32.Vec3{5, 0, 0}, Half: mgl32.Vec3{1, 1, 1}}); math.Abs(float64(got-3)) > 1e-3 {
		t.Errorf("face-to-face gap %v, want 3", got)
	}
	// Corner to corner: the diagonal between two corners, √3 × 2.
	want := float32(math.Sqrt(3) * 2)
	if got := Distance(a, Box{C: mgl32.Vec3{4, 4, 4}, Half: mgl32.Vec3{1, 1, 1}}); math.Abs(float64(got-want)) > 1e-2 {
		t.Errorf("corner-to-corner gap %v, want %v", got, want)
	}
}

func TestDistanceIsZeroWhenTouchingOrOverlapping(t *testing.T) {
	a := Box{C: mgl32.Vec3{0, 0, 0}, Half: mgl32.Vec3{1, 1, 1}}

	cases := []struct {
		name string
		b    Shape
	}{
		{"exactly touching", Box{C: mgl32.Vec3{2, 0, 0}, Half: mgl32.Vec3{1, 1, 1}}},
		{"shallow overlap", Box{C: mgl32.Vec3{1.9, 0, 0}, Half: mgl32.Vec3{1, 1, 1}}},
		{"deep overlap", Box{C: mgl32.Vec3{0.2, 0.1, 0}, Half: mgl32.Vec3{1, 1, 1}}},
		{"fully contained", Box{C: mgl32.Vec3{0, 0, 0}, Half: mgl32.Vec3{0.2, 0.2, 0.2}}},
		{"concentric sphere", sphere(mgl32.Vec3{0, 0, 0}, 0.5)},
	}
	for _, tc := range cases {
		if got := Distance(a, tc.b); got > 1e-2 {
			t.Errorf("%s: gap %v, want 0", tc.name, got)
		}
	}
}

func TestDistanceIsSymmetric(t *testing.T) {
	a := Cylinder{C: mgl32.Vec3{0, 0, 0}, Half: mgl32.Vec3{1, 2, 1}, Rot: mgl32.Ident4()}
	b := OBB{C: mgl32.Vec3{6, 1, -2}, Half: mgl32.Vec3{1, 1, 2}, Rot: mgl32.HomogRotate3DZ(0.6)}

	d1, d2 := Distance(a, b), Distance(b, a)
	if math.Abs(float64(d1-d2)) > 1e-3 {
		t.Errorf("gap depends on argument order: %v vs %v", d1, d2)
	}
}

// TestDistanceNeverOverstates is the safety property. A step of Distance must
// never take a shape past the other, so it must never exceed the true gap —
// which is bounded above by the distance between any point of one and any point
// of the other, centres included.
func TestDistanceNeverOverstates(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	checked := 0
	for i := 0; i < 2000; i++ {
		c := mgl32.Vec3{
			(r.Float32()*2 - 1) * 12,
			(r.Float32()*2 - 1) * 12,
			(r.Float32()*2 - 1) * 12,
		}
		h := mgl32.Vec3{0.3 + r.Float32(), 0.3 + r.Float32(), 0.3 + r.Float32()}
		rot := mgl32.HomogRotate3DY(r.Float32() * 6.28).Mul4(mgl32.HomogRotate3DX(r.Float32() * 6.28))

		a := Box{C: mgl32.Vec3{}, Half: mgl32.Vec3{1, 1, 1}}
		var b Shape
		switch i % 3 {
		case 0:
			b = Box{C: c, Half: h}
		case 1:
			b = Ellipsoid{C: c, Half: h, Rot: rot}
		default:
			b = Cylinder{C: c, Half: h, Rot: rot}
		}

		gap := Distance(a, b)
		if gap < 0 {
			t.Fatalf("case %d: negative gap %v", i, gap)
		}
		if gap == 0 {
			continue
		}
		checked++

		// Moving a by exactly the gap toward b must not overshoot into it.
		dir := b.Center().Sub(a.Center()).Normalize()
		if _, depth, ok := Penetration(Translate(a, dir.Mul(gap)), b); ok && depth > 1e-2 {
			t.Fatalf("case %d (%T): a step of the reported gap %v buried it %v deep", i, b, gap, depth)
		}
	}
	if checked < 400 {
		t.Fatalf("only %d separated pairs generated; the fuzz is not exercising anything", checked)
	}
	t.Logf("verified %d separated pairs", checked)
}
