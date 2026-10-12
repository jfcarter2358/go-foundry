package sdf

import (
	"context"
	"math"
	"testing"
)

// The mesher makes a closed, outward-wound, manifold surface whose volume and
// area are the shape's, with normals along the gradient.
func TestPolygonizeSphereIsClosedAndRight(t *testing.T) {
	r := 1.0
	m, st, err := Polygonize(Sphere(Vec3{0.1, -0.2, 0.3}, r), MeshOptions{Cell: 0.04, Sharpness: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	open, nonManifold := m.OpenEdges()
	if open != 0 || nonManifold != 0 {
		t.Fatalf("%d open, %d non-manifold edges", open, nonManifold)
	}
	wantV := 4.0 / 3 * math.Pi * r * r * r
	if v := m.Volume(); math.Abs(v-wantV)/wantV > 0.01 {
		t.Errorf("volume %.4f, want %.4f", v, wantV)
	}
	wantA := 4 * math.Pi * r * r
	if a := m.Area(); math.Abs(a-wantA)/wantA > 0.02 {
		t.Errorf("area %.4f, want %.4f", a, wantA)
	}
	c := Vec3{0.1, -0.2, 0.3}
	for i, p := range m.Positions {
		if d := math.Abs(p.Sub(c).Len() - r); d > 0.01 {
			t.Fatalf("vertex %d is %.4f off the sphere", i, d)
		}
		if m.Normals[i].Dot(p.Sub(c).Normalize()) < 0.99 {
			t.Fatalf("vertex %d's normal %v is not radial", i, m.Normals[i])
		}
	}
	t.Logf("%d triangles, %d blocks, %d exact subcubes, %d evaluations", m.TriangleCount(), st.Blocks, st.ActiveSubcube, st.Evaluations)
}

// Dual contouring keeps a box's corners: its vertices reach them, and the
// volume is the box's.
func TestPolygonizeKeepsSharpCorners(t *testing.T) {
	h := Vec3{0.7, 0.45, 0.33}
	m, _, err := Polygonize(Rotate(Cuboid(Vec3{}, h), RotAxis(Vec3{1, 2, 0.5}, 0.4)), MeshOptions{Cell: 0.05, Sharpness: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := 8 * h[0] * h[1] * h[2]
	if v := m.Volume(); math.Abs(v-want)/want > 0.01 {
		t.Errorf("volume %.4f, want %.4f", v, want)
	}
	inv := RotAxis(Vec3{1, 2, 0.5}, 0.4).T()
	best := math.Inf(1)
	for _, p := range m.Positions {
		q := inv.Apply(p).Abs()
		best = math.Min(best, q.Sub(h).Len())
	}
	// Within half a cell: the corner's normals come from the sampled
	// field's gradient, which blurs over a cell.
	if best > 0.025 {
		t.Errorf("no vertex near a corner (nearest %.3f)", best)
	}
	m.SplitNonManifold()
	if open, nm := m.OpenEdges(); open != 0 || nm != 0 || !m.ClosedByPosition() {
		t.Fatalf("%d open, %d non-manifold edges after the split", open, nm)
	}
}

// The same mesh on any number of workers, to the bit.
func TestPolygonizeIsDeterministic(t *testing.T) {
	s := Displace(RoundBox(Vec3{}, Vec3{0.8, 0.5, 0.6}, 0.1), FBM(7, 2, 4, 0.5), 0.08)
	a, _, err := Polygonize(s, MeshOptions{Cell: 0.03, Sharpness: 0.8, Weathering: 2, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := Polygonize(s, MeshOptions{Cell: 0.03, Sharpness: 0.8, Weathering: 2, Workers: 13})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Positions) != len(b.Positions) || len(a.Indices) != len(b.Indices) {
		t.Fatalf("%d/%d vertices, %d/%d indices", len(a.Positions), len(b.Positions), len(a.Indices), len(b.Indices))
	}
	for i := range a.Positions {
		if a.Positions[i] != b.Positions[i] || a.Normals[i] != b.Normals[i] {
			t.Fatalf("vertex %d differs", i)
		}
	}
	for i := range a.Indices {
		if a.Indices[i] != b.Indices[i] {
			t.Fatalf("index %d differs", i)
		}
	}
	if open, nm := a.OpenEdges(); open != 0 {
		t.Fatalf("%d open edges (%d non-manifold)", open, nm)
	}
	split := a.SplitNonManifold()
	if open, nm := a.OpenEdges(); open != 0 || nm != 0 || !a.ClosedByPosition() {
		t.Fatalf("after splitting %d vertices: %d open, %d non-manifold edges, closed %v", split, open, nm, a.ClosedByPosition())
	}
}

// Weathering moves convex edges in and leaves flat faces where they were.
func TestWeatheringRoundsConvexEdgesOnly(t *testing.T) {
	box := Cuboid(Vec3{}, Vec3{0.5, 0.5, 0.5})
	sharp, _, _ := Polygonize(box, MeshOptions{Cell: 0.02, Sharpness: 1})
	worn, _, _ := Polygonize(box, MeshOptions{Cell: 0.02, Sharpness: 1, Weathering: 3, WeatherRate: 1})
	if !(worn.Volume() < sharp.Volume()-1e-4) {
		t.Fatalf("weathering did not take anything off (%.5f vs %.5f)", worn.Volume(), sharp.Volume())
	}
	// The face centres stay on the faces.
	for _, p := range worn.Positions {
		q := p.Abs()
		if q[1] < 0.2 && q[2] < 0.2 && math.Abs(q[0]-0.5) > 0.005 {
			t.Fatalf("a face-centre vertex moved off the face: %v", p)
		}
	}
	// The corners are rounded: no vertex reaches one.
	for _, p := range worn.Positions {
		if p.Abs().Sub(Vec3{0.5, 0.5, 0.5}).Len() < 0.015 {
			t.Fatalf("a vertex is still at a corner: %v", p)
		}
	}
}

func TestPolygonizeCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Polygonize(Sphere(Vec3{}, 1), MeshOptions{Cell: 0.01, Ctx: ctx}); err == nil {
		t.Fatal("a cancelled mesh finished")
	}
}

func TestKeepLargestDropsFragments(t *testing.T) {
	s := Union(Sphere(Vec3{}, 1), Sphere(Vec3{3, 0, 0}, 0.2))
	m, _, err := Polygonize(s, MeshOptions{Cell: 0.05})
	if err != nil {
		t.Fatal(err)
	}
	before := m.TriangleCount()
	if dropped := m.KeepLargest(0.05); dropped != 1 {
		t.Fatalf("dropped %d pieces, want 1", dropped)
	}
	if b := m.Bounds(); b.Hi[0] > 1.1 {
		t.Fatalf("the fragment is still there (bounds %v)", b)
	}
	if m.TriangleCount() >= before {
		t.Fatal("nothing went")
	}
	for _, ix := range m.Indices {
		if int(ix) >= len(m.Positions) {
			t.Fatal("compaction left an index out of range")
		}
	}
}

func BenchmarkPolygonizeDisplacedBox(b *testing.B) {
	s := Displace(RoundBox(Vec3{}, Vec3{1, 0.6, 0.8}, 0.1), FBM(7, 2, 5, 0.5), 0.08)
	for i := 0; i < b.N; i++ {
		m, st, _ := Polygonize(s, MeshOptions{Cell: 0.01, Sharpness: 0.8})
		b.ReportMetric(float64(m.TriangleCount()), "tris")
		b.ReportMetric(float64(st.Evaluations), "evals")
	}
}

// A displaced, cracked, weathered shape — sheets a crack's width apart, many
// ambiguous cells — still meshes to a closed manifold with no help: one
// vertex per sheet in a cell.
func TestPolygonizeNoisyCrackIsManifold(t *testing.T) {
	s := Displace(RoundBox(Vec3{}, Vec3{0.8, 0.5, 0.6}, 0.1), FBM(7, 6, 4, 0.5), 0.08)
	s = SmoothSubtract(0.01, s, Capsule(Vec3{-1, 0, 0}, Vec3{1, 0.1, 0}, 0.02))
	m, _, err := Polygonize(s, MeshOptions{Cell: 0.02, Sharpness: 0.9, Weathering: 2})
	if err != nil {
		t.Fatal(err)
	}
	if open, nm := m.OpenEdges(); open != 0 || nm != 0 || !m.ClosedByPosition() {
		t.Fatalf("%d open, %d non-manifold edges, closed by position %v", open, nm, m.ClosedByPosition())
	}
	if v := m.Volume(); v <= 0 {
		t.Fatalf("wound inward (volume %g)", v)
	}
}
