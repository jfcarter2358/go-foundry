package sdf

import (
	"math"
	"testing"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// The primitives and transforms give the distances they should.
func TestPrimitivesAndTransforms(t *testing.T) {
	s := Sphere(Vec3{1, 2, 3}, 0.5)
	if d := s.At(Vec3{1, 2, 4}); !near(d, 0.5, 1e-12) {
		t.Errorf("sphere: %g", d)
	}
	b := RoundBox(Vec3{}, Vec3{1, 0.5, 0.25}, 0.1)
	if d := b.At(Vec3{2, 0, 0}); !near(d, 1, 1e-12) {
		t.Errorf("round box face: %g", d)
	}
	if d := b.At(Vec3{}); !near(d, -0.25, 1e-12) {
		t.Errorf("round box centre: %g", d)
	}
	r := Rotate(Translate(Cuboid(Vec3{}, Vec3{1, 0.2, 0.2}), Vec3{2, 0, 0}), RotY(math.Pi/2))
	// +X turned a quarter about Y is −Z: the bar now lies along −Z.
	if d := r.At(Vec3{0, 0, -2}); !near(d, -0.2, 1e-9) {
		t.Errorf("rotated bar centre: %g", d)
	}
	if b := r.Box; !near(b.Lo[2], -3, 1e-9) || !near(b.Hi[2], -1, 1e-9) {
		t.Errorf("rotated box %v", b)
	}
	sc := Scale(Sphere(Vec3{}, 1), 3)
	if d := sc.At(Vec3{5, 0, 0}); !near(d, 2, 1e-12) {
		t.Errorf("scaled sphere: %g", d)
	}
	// Half-spaces bound nothing on their own and cut by intersection.
	cut := Intersect(Sphere(Vec3{}, 1), HalfSpace(Vec3{0, 0.5, 0}, Vec3{0, 1, 0}))
	if cut.At(Vec3{0, 0.8, 0}) <= 0 || cut.At(Vec3{0, 0.2, 0}) >= 0 {
		t.Error("the half-space did not cut the sphere at y = 0.5")
	}
	if !HalfSpace(Vec3{}, Vec3{1, 0, 0}).Box.IsInfinite() || cut.Box.IsInfinite() {
		t.Error("boxes: a half-space should be infinite, its intersection with a sphere not")
	}
}

// The smooth operators blend within k and agree with the hard ones outside.
func TestSmoothOperators(t *testing.T) {
	a, b := Sphere(Vec3{-0.6, 0, 0}, 0.5), Sphere(Vec3{0.6, 0, 0}, 0.5)
	hard, soft := Union(a, b), SmoothUnion(0.4, a, b)
	if d := soft.At(Vec3{0, 0, 0}); !(d < hard.At(Vec3{0, 0, 0})) {
		t.Error("the smooth union did not fill the waist")
	}
	if !near(soft.At(Vec3{-1.5, 0, 0}), hard.At(Vec3{-1.5, 0, 0}), 1e-12) {
		t.Error("the smooth union changed a point far from the join")
	}
	sub := SmoothSubtract(0.1, Sphere(Vec3{}, 1), Sphere(Vec3{1, 0, 0}, 0.5))
	if sub.At(Vec3{0.8, 0, 0}) <= 0 || sub.At(Vec3{-0.5, 0, 0}) >= 0 {
		t.Error("the subtraction is wrong")
	}
}

// Strata: soft beds weather back further than hard ones, the inset never
// changes faster than MaxSlope, and the laminations add grooves without
// jumps.
func TestStrataInsetProfile(t *testing.T) {
	st := Strata{Layers: []Layer{{1, 0.1}, {1, 0.95}}, Inset: 0.3, Undercut: 1, MaxSlope: 3,
		Lamination: 0.004, LamPeriod: 0.05, CrossBed: 0.4, Seed: 9}
	soft := st.InsetAt(Vec3{0, 0.9, 0}) // high in the soft bed, under the hard one
	hard := st.InsetAt(Vec3{0, 1.5, 0})
	low := st.InsetAt(Vec3{0, 0.1, 0}) // the soft bed's base: flush (undercut)
	if !(soft > hard+0.1 && soft > low) {
		t.Errorf("insets: soft top %g, soft base %g, hard %g", soft, low, hard)
	}
	prev := st.InsetAt(Vec3{0.3, -2, 0.2})
	step := 0.001
	worst := 0.0
	for y := -2 + step; y < 4; y += step {
		v := st.InsetAt(Vec3{0.3, y, 0.2})
		worst = math.Max(worst, math.Abs(v-prev)/step)
		prev = v
	}
	// MaxSlope from the beds, plus the laminations' own slope.
	// The beds' own slope (an undercut bed recedes as it rises) adds to the
	// boundary's.
	if limit := 3*1.35 + 3*math.Pi*st.Lamination/st.LamPeriod*1.4; worst > limit {
		t.Errorf("the inset changes at %g m/m, past %g", worst, limit)
	}
	idx, _, frac, _ := st.LayerAt(1.25)
	if idx != 1 || !near(frac, 0.25, 1e-9) {
		t.Errorf("LayerAt(1.25) = %d, %g", idx, frac)
	}
	if idx, rep, _, _ := st.LayerAt(-0.5); idx != 1 || rep != -1 {
		t.Errorf("the stack does not repeat downward: %d, %d", idx, rep)
	}
}

// A Voronoi border is where two cells' points are equidistant, and
// BlockDisplace stays continuous across it.
func TestJointsBordersAndBlocks(t *testing.T) {
	j := Joints{Seed: 4, Spacing: Vec3{1, 0.6, 1.4}, Jitter: 1}
	b, id := j.Border(Vec3{0.3, 0.2, 0.1})
	if b < 0 {
		t.Fatalf("border distance %g", b)
	}
	// Walking along a line, the cell changes only where the border
	// distance reaches zero.
	prevID := id
	for x := 0.3; x < 4; x += 0.002 {
		bb, id := j.Border(Vec3{x, 0.2, 0.1})
		if id != prevID && bb > 0.004 {
			t.Fatalf("the cell changed %g from a border", bb)
		}
		prevID = id
	}
	flat := Plane(Vec3{0, 0, 1}, 0)
	bd := BlockDisplace(flat, j, 0.1, 0.03)
	prev := bd.At(Vec3{-2, 0.3, 0})
	for x := -2.0; x < 2; x += 0.0005 {
		v := bd.At(Vec3{x, 0.3, 0})
		if math.Abs(v-prev) > 4*0.1/0.03*0.0005 {
			t.Fatalf("a jump of %g at x = %g", v-prev, x)
		}
		prev = v
	}
}

// A facet cuts only near where it was placed, and leaves a flat floor.
func TestFacetsCutLocally(t *testing.T) {
	s := Sphere(Vec3{}, 1)
	fs := []Facet{{Point: Vec3{0, 0.9, 0}, Normal: Vec3{0, 1, 0}, Center: Vec3{0, 1, 0}, Radius: 0.5, Seed: 1}}
	c := CutFacets(s, fs, 0.01)
	if c.At(Vec3{0, 0.95, 0}) <= 0 {
		t.Error("the facet did not cut the cap")
	}
	if !near(c.At(Vec3{0, -0.95, 0}), s.At(Vec3{0, -0.95, 0}), 1e-12) || !near(c.At(Vec3{0.95, 0, 0}), s.At(Vec3{0.95, 0, 0}), 1e-12) {
		t.Error("the facet reached the far side")
	}
	if d := c.At(Vec3{0.05, 0.9, 0.05}); math.Abs(d) > 0.02 {
		t.Errorf("the floor is not at the plane: %g", d)
	}
}

// AO is near 1 on an open face and low in a slot; the Laplacian is positive
// on convex surfaces and negative in concave ones.
func TestAOAndLaplacian(t *testing.T) {
	ground := Plane(Vec3{0, 1, 0}, 0)
	if ao := ground.AO(Vec3{}, Vec3{0, 1, 0}, 0.5, 5); ao < 0.95 {
		t.Errorf("open plane AO %g", ao)
	}
	// A slot 0.1 wide and deep, its floor at y = 0.
	slot := Union(Plane(Vec3{0, 1, 0}, 0), Cuboid(Vec3{-0.55, 0.25, 0}, Vec3{0.5, 0.25, 5}), Cuboid(Vec3{0.55, 0.25, 0}, Vec3{0.5, 0.25, 5}))
	if ao := slot.AO(Vec3{0, 0, 0}, Vec3{0, 1, 0}, 0.5, 5); ao > 0.6 {
		t.Errorf("slot floor AO %g", ao)
	}
	if l := Sphere(Vec3{}, 1).Laplacian(Vec3{1, 0, 0}, 0.05); l <= 0 {
		t.Errorf("sphere Laplacian %g", l)
	}
	hollow := Subtract(Cuboid(Vec3{}, Vec3{2, 2, 2}), Sphere(Vec3{}, 1))
	if l := hollow.Laplacian(Vec3{1, 0, 0}, 0.05); l >= 0 {
		t.Errorf("hollow Laplacian %g", l)
	}
}
