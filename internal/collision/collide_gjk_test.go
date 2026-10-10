package collision

// collide_gjk_test.go — GJK/EPA against cases whose answers are known by hand,
// and then against the only property that matters in general: push by what EPA
// reported and the overlap must be gone.
//
// That last one is worth more than any analytic case. It needs no ground truth,
// it covers every shape pair at once, and it fails loudly on exactly the bug
// that would matter in play — a depth or a normal that does not actually
// separate anything.

import (
	"math"
	"math/rand"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func sphere(c mgl32.Vec3, r float32) Ellipsoid {
	return Ellipsoid{C: c, Half: mgl32.Vec3{r, r, r}, Rot: mgl32.Ident4()}
}

func TestOverlapOnSpheres(t *testing.T) {
	a := sphere(mgl32.Vec3{0, 0, 0}, 1)

	if !Overlap(a, sphere(mgl32.Vec3{1.5, 0, 0}, 1)) {
		t.Error("spheres 1.5 apart with radii 1+1 should overlap")
	}
	if Overlap(a, sphere(mgl32.Vec3{2.5, 0, 0}, 1)) {
		t.Error("spheres 2.5 apart with radii 1+1 should not overlap")
	}
	if !Overlap(a, sphere(mgl32.Vec3{0, 0, 0}, 0.2)) {
		t.Error("a sphere entirely inside another should overlap")
	}
}

func TestPenetrationDepthOnSpheresMatchesTheAnalyticAnswer(t *testing.T) {
	// Two spheres overlap by (r1+r2) − d, along the line between their centres.
	for _, d := range []float32{0.25, 0.9, 1.5, 1.99} {
		a := sphere(mgl32.Vec3{0, 0, 0}, 1)
		b := sphere(mgl32.Vec3{d, 0, 0}, 1)

		n, depth, ok := Penetration(a, b)
		if !ok {
			t.Fatalf("d=%v: reported no contact", d)
		}
		want := 2 - d
		if math.Abs(float64(depth-want)) > 1e-3 {
			t.Errorf("d=%v: depth %v, want %v", d, depth, want)
		}
		// Normal points from a toward b, which here is +X.
		if math.Abs(float64(n.X()-1)) > 1e-3 {
			t.Errorf("d=%v: normal %v, want +X", d, n)
		}
	}
}

func TestPenetrationOnBoxesPicksTheShallowestAxis(t *testing.T) {
	// A unit box overlapping another by 0.2 in X and 0.8 in Z must come apart
	// along X — that is what makes a dropped crate settle on top of another
	// rather than squirting out sideways.
	a := Box{C: mgl32.Vec3{0, 0, 0}, Half: mgl32.Vec3{1, 1, 1}}
	b := Box{C: mgl32.Vec3{1.8, 0, 1.2}, Half: mgl32.Vec3{1, 1, 1}}

	n, depth, ok := Penetration(a, b)
	if !ok {
		t.Fatal("reported no contact between overlapping boxes")
	}
	if math.Abs(float64(depth-0.2)) > 1e-3 {
		t.Errorf("depth %v, want 0.2", depth)
	}
	if math.Abs(float64(n.X())) < 0.99 {
		t.Errorf("normal %v, want it along X", n)
	}
}

func TestPenetrationIsSymmetric(t *testing.T) {
	a := Box{C: mgl32.Vec3{0, 0, 0}, Half: mgl32.Vec3{1, 1, 1}}
	b := sphere(mgl32.Vec3{1.4, 0.3, 0.2}, 0.8)

	n1, d1, ok1 := Penetration(a, b)
	n2, d2, ok2 := Penetration(b, a)
	if !ok1 || !ok2 {
		t.Fatal("contact reported in only one direction")
	}
	if math.Abs(float64(d1-d2)) > 1e-3 {
		t.Errorf("depth disagreed by direction: %v vs %v", d1, d2)
	}
	if !vecNear(n1, n2.Mul(-1), 1e-2) {
		t.Errorf("normals are not opposite: %v vs %v", n1, n2)
	}
}

func TestNoContactBetweenSeparatedShapes(t *testing.T) {
	cases := []struct {
		name string
		a, b Shape
	}{
		{"boxes", Box{C: mgl32.Vec3{0, 0, 0}, Half: mgl32.Vec3{1, 1, 1}},
			Box{C: mgl32.Vec3{5, 0, 0}, Half: mgl32.Vec3{1, 1, 1}}},
		{"box-sphere", Box{C: mgl32.Vec3{0, 0, 0}, Half: mgl32.Vec3{1, 1, 1}},
			sphere(mgl32.Vec3{0, 4, 0}, 1)},
		{"cylinder-obb", Cylinder{C: mgl32.Vec3{0, 0, 0}, Half: mgl32.Vec3{1, 2, 1}, Rot: mgl32.Ident4()},
			OBB{C: mgl32.Vec3{6, 0, 0}, Half: mgl32.Vec3{1, 1, 1}, Rot: mgl32.HomogRotate3DY(0.7)}},
		{"corner-gap", Box{C: mgl32.Vec3{0, 0, 0}, Half: mgl32.Vec3{1, 1, 1}},
			Box{C: mgl32.Vec3{2.05, 2.05, 2.05}, Half: mgl32.Vec3{1, 1, 1}}},
	}
	for _, tc := range cases {
		if Overlap(tc.a, tc.b) {
			t.Errorf("%s: reported an overlap between separated shapes", tc.name)
		}
		if _, _, ok := Penetration(tc.a, tc.b); ok {
			t.Errorf("%s: Penetration reported contact between separated shapes", tc.name)
		}
	}
}

// TestPushingByThePenetrationSeparates is the property that makes the whole
// thing usable: whatever EPA reports must actually resolve the overlap.
func TestPushingByThePenetrationSeparates(t *testing.T) {
	r := rand.New(rand.NewSource(4242))
	rndVec := func(spread float32) mgl32.Vec3 {
		return mgl32.Vec3{
			(r.Float32()*2 - 1) * spread,
			(r.Float32()*2 - 1) * spread,
			(r.Float32()*2 - 1) * spread,
		}
	}
	rndRot := func() mgl32.Mat4 {
		return mgl32.HomogRotate3DY(r.Float32() * 6.28).
			Mul4(mgl32.HomogRotate3DX(r.Float32() * 6.28)).
			Mul4(mgl32.HomogRotate3DZ(r.Float32() * 6.28))
	}
	mk := func(kind int, c mgl32.Vec3) Shape {
		h := mgl32.Vec3{0.4 + r.Float32(), 0.4 + r.Float32(), 0.4 + r.Float32()}
		switch kind {
		case 0:
			return Box{C: c, Half: h}
		case 1:
			return OBB{C: c, Half: h, Rot: rndRot()}
		case 2:
			return Ellipsoid{C: c, Half: h, Rot: rndRot()}
		default:
			return Cylinder{C: c, Half: h, Rot: rndRot()}
		}
	}

	tested := 0
	for i := 0; i < 3000; i++ {
		a := mk(i%4, mgl32.Vec3{})
		b := mk((i/4)%4, rndVec(1.6))
		if !Overlap(a, b) {
			continue
		}
		tested++

		n, depth, ok := Penetration(a, b)
		if !ok {
			t.Fatalf("case %d: Overlap said yes but Penetration said no", i)
		}
		if depth < 0 {
			t.Fatalf("case %d: negative depth %v", i, depth)
		}
		// A hair past the reported depth, to clear the tolerance EPA stops at.
		moved := Translate(b, n.Mul(depth+2e-3))
		if Overlap(a, moved) {
			t.Fatalf("case %d (%T vs %T): still overlapping after pushing %v by %v",
				i, a, b, n, depth)
		}
	}
	if tested < 500 {
		t.Fatalf("only %d overlapping cases generated; the fuzz is not exercising anything", tested)
	}
	t.Logf("verified separation on %d overlapping pairs", tested)
}
