package cluster

import (
	"math"
	"math/rand"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// gridMesh is an indexed n x n quad grid on y = h(x, z), wound so its normals
// point up.
func gridMesh(n int, h func(x, z float32) float32) Mesh {
	var pos []float32
	for z := 0; z <= n; z++ {
		for x := 0; x <= n; x++ {
			fx, fz := float32(x), float32(z)
			pos = append(pos, fx, h(fx, fz), fz)
		}
	}
	var idx []uint32
	at := func(x, z int) uint32 { return uint32(z*(n+1) + x) }
	for z := 0; z < n; z++ {
		for x := 0; x < n; x++ {
			idx = append(idx, at(x, z), at(x, z+1), at(x+1, z), at(x+1, z), at(x, z+1), at(x+1, z+1))
		}
	}
	return Mesh{Positions: pos, Stride: 3, Indices: idx}
}

// sphereMesh is a closed UV sphere, outward-wound.
func sphereMesh(rings, segs int) Mesh {
	var pos []float32
	for r := 0; r <= rings; r++ {
		th := math.Pi * float64(r) / float64(rings)
		for s := 0; s <= segs; s++ {
			ph := 2 * math.Pi * float64(s) / float64(segs)
			pos = append(pos, float32(math.Sin(th)*math.Cos(ph)), float32(math.Cos(th)), float32(math.Sin(th)*math.Sin(ph)))
		}
	}
	var idx []uint32
	at := func(r, s int) uint32 { return uint32(r*(segs+1) + s) }
	for r := 0; r < rings; r++ {
		for s := 0; s < segs; s++ {
			a, b, c, d := at(r, s), at(r+1, s), at(r, s+1), at(r+1, s+1)
			idx = append(idx, a, c, b, c, d, b)
		}
	}
	return Mesh{Positions: pos, Stride: 3, Indices: idx}
}

// soupOf de-indexes m, with two extra floats per vertex so the stride is
// exercised.
func soupOf(m Mesh) Mesh {
	var pos []float32
	for t := 0; t < m.TriangleCount(); t++ {
		for k := 0; k < 3; k++ {
			p := m.Position(m.Corner(t, k))
			pos = append(pos, p[0], p[1], p[2], 0.25, 0.75)
		}
	}
	return Mesh{Positions: pos, Stride: 5}
}

func checkPartition(t *testing.T, m Mesh, r Result, opt Options) {
	t.Helper()
	opt = opt.resolve()
	nt := m.TriangleCount()
	if len(r.Order) != nt {
		t.Fatalf("order has %d triangles, mesh %d", len(r.Order), nt)
	}
	seen := make([]bool, nt)
	for _, tr := range r.Order {
		if seen[tr] {
			t.Fatalf("triangle %d placed twice", tr)
		}
		seen[tr] = true
	}
	next := uint32(0)
	for ci, c := range r.Clusters {
		if c.TriFirst != next {
			t.Fatalf("cluster %d starts at %d, want %d", ci, c.TriFirst, next)
		}
		next += c.TriCount
		if c.TriCount == 0 || int(c.TriCount) > opt.MaxTriangles {
			t.Fatalf("cluster %d has %d triangles (limit %d)", ci, c.TriCount, opt.MaxTriangles)
		}
		verts := map[uint32]bool{}
		for _, tr := range r.Order[c.TriFirst : c.TriFirst+c.TriCount] {
			for k := 0; k < 3; k++ {
				v := m.Corner(int(tr), k)
				verts[v] = true
				p := m.Position(v)
				for a := 0; a < 3; a++ {
					if p[a] < c.Lo[a] || p[a] > c.Hi[a] {
						t.Fatalf("cluster %d box misses a corner", ci)
					}
				}
				if p.Sub(c.Center).Len() > c.Radius {
					t.Fatalf("cluster %d sphere misses a corner by %g", ci, p.Sub(c.Center).Len()-c.Radius)
				}
			}
		}
		if len(verts) != int(c.Vertices) || len(verts) > opt.MaxVertices {
			t.Fatalf("cluster %d: %d distinct vertices, recorded %d, limit %d", ci, len(verts), c.Vertices, opt.MaxVertices)
		}
	}
	if int(next) != nt {
		t.Fatalf("clusters cover %d triangles of %d", next, nt)
	}
}

func TestBuildPartitionsWithinLimits(t *testing.T) {
	bumpy := func(x, z float32) float32 { return float32(math.Sin(float64(x)*0.3) * math.Cos(float64(z)*0.2)) }
	cases := []struct {
		name string
		m    Mesh
		opt  Options
	}{
		{"grid", gridMesh(40, bumpy), Options{}},
		{"sphere", sphereMesh(24, 48), Options{}},
		{"soup", soupOf(sphereMesh(16, 32)), Options{}},
		{"small limits", gridMesh(20, bumpy), Options{MaxTriangles: 32, MaxVertices: 40}},
		{"vertex bound", soupOf(gridMesh(12, bumpy)), Options{MaxTriangles: 128, MaxVertices: 64}},
		{"one triangle", Mesh{Positions: []float32{0, 0, 0, 1, 0, 0, 0, 1, 0}, Stride: 3}, Options{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Build(tc.m, tc.opt)
			checkPartition(t, tc.m, r, tc.opt)
		})
	}
}

func TestBuildFillsClusters(t *testing.T) {
	// A connected grid should cut into nearly full clusters, not slivers.
	m := gridMesh(64, func(x, z float32) float32 { return 0 })
	r := Build(m, Options{})
	s := r.Summarise()
	if s.MeanTriangles < 96 {
		t.Fatalf("mean %.1f triangles per cluster on a grid; the growth is not following the surface", s.MeanTriangles)
	}
	// And compact: no cluster much wider than a full cluster's patch of the
	// grid (128 triangles is 8x8 quads; allow a generous 3x).
	for ci, c := range r.Clusters {
		if d := c.Hi.Sub(c.Lo); max(d[0], d[2]) > 24 {
			t.Fatalf("cluster %d spans %v", ci, d)
		}
	}
}

func TestBuildSoupFollowsPositions(t *testing.T) {
	// A corner soup shares no indices; position adjacency must still grow
	// clusters as full as the indexed mesh's.
	m := soupOf(gridMesh(32, func(x, z float32) float32 { return 0 }))
	r := Build(m, Options{MaxTriangles: 64, MaxVertices: 255})
	if s := r.Summarise(); s.MeanTriangles < 50 {
		t.Fatalf("soup cut to %.1f triangles per cluster", s.MeanTriangles)
	}
}

func TestBuildDeterministic(t *testing.T) {
	m := sphereMesh(20, 40)
	a, b := Build(m, Options{}), Build(m, Options{})
	if len(a.Order) != len(b.Order) || len(a.Clusters) != len(b.Clusters) {
		t.Fatal("two cuts of one mesh differ in size")
	}
	for i := range a.Order {
		if a.Order[i] != b.Order[i] {
			t.Fatalf("two cuts of one mesh differ at %d", i)
		}
	}
	for i := range a.Clusters {
		if a.Clusters[i] != b.Clusters[i] {
			t.Fatalf("cluster %d differs between cuts", i)
		}
	}
}

func TestConeIsConservative(t *testing.T) {
	// For every cluster and many eyes: whenever the cone says the cluster
	// faces away, every one of its triangles must face away.
	rng := rand.New(rand.NewSource(7))
	for _, m := range []Mesh{
		sphereMesh(24, 48),
		gridMesh(30, func(x, z float32) float32 { return float32(math.Sin(float64(x)*0.5)) * 2 }),
	} {
		r := Build(m, Options{})
		culled, tests := 0, 0
		for ci := range r.Clusters {
			c := &r.Clusters[ci]
			for e := 0; e < 300; e++ {
				eye := mgl32.Vec3{rng.Float32()*80 - 40, rng.Float32()*80 - 40, rng.Float32()*80 - 40}
				if e%3 == 0 {
					// And some close to the cluster, where it matters most.
					eye = c.Center.Add(mgl32.Vec3{rng.Float32() - 0.5, rng.Float32() - 0.5, rng.Float32() - 0.5}.Mul(4 * c.Radius))
				}
				tests++
				if !c.BackfacingFrom(eye) {
					continue
				}
				culled++
				for _, tr := range r.Order[c.TriFirst : c.TriFirst+c.TriCount] {
					a, b, cc := m.Position(m.Corner(int(tr), 0)), m.Position(m.Corner(int(tr), 1)), m.Position(m.Corner(int(tr), 2))
					n := b.Sub(a).Cross(cc.Sub(a))
					if n.Len() < 1e-9 {
						continue
					}
					if n.Normalize().Dot(eye.Sub(a)) > 1e-4 {
						t.Fatalf("cluster %d culled from %v but triangle %d faces the eye", ci, eye, tr)
					}
				}
			}
		}
		if culled == 0 {
			t.Fatalf("the cone culled nothing in %d tests; it is not doing its job", tests)
		}
	}
}

func TestConeOnFlatPatch(t *testing.T) {
	// A flat, upward patch: culled from below, kept from above.
	m := gridMesh(4, func(x, z float32) float32 { return 0 })
	r := Build(m, Options{})
	if len(r.Clusters) != 1 {
		t.Fatalf("%d clusters for 32 triangles", len(r.Clusters))
	}
	c := &r.Clusters[0]
	if c.ConeCutoff >= 1 {
		t.Fatal("a flat patch has no cone")
	}
	if !c.BackfacingFrom(mgl32.Vec3{2, -5, 2}) {
		t.Fatal("flat patch seen from below is not culled")
	}
	if c.BackfacingFrom(mgl32.Vec3{2, 5, 2}) {
		t.Fatal("flat patch seen from above is culled")
	}
	// A double-sided patch (both windings) has no cone at all.
	ds := m
	ds.Indices = append(append([]uint32(nil), m.Indices...), reversed(m.Indices)...)
	r = Build(ds, Options{})
	for _, c := range r.Clusters {
		if c.ConeCutoff < 1 {
			t.Fatal("a cluster holding both sides of a patch claims a cone")
		}
	}
}

func reversed(idx []uint32) []uint32 {
	out := make([]uint32, len(idx))
	for t := 0; t+2 < len(idx); t += 3 {
		out[t], out[t+1], out[t+2] = idx[t], idx[t+2], idx[t+1]
	}
	return out
}

func TestReorder(t *testing.T) {
	m := gridMesh(10, func(x, z float32) float32 { return 0 })
	r := Build(m, Options{MaxTriangles: 16})
	ix := r.Reorder(m.Indices)
	for k, tr := range r.Order {
		for c := 0; c < 3; c++ {
			if ix[3*k+c] != m.Indices[3*int(tr)+c] {
				t.Fatal("Reorder does not follow Order")
			}
		}
	}
	soup := r.Reorder(nil)
	if soup[0] != 3*r.Order[0] {
		t.Fatal("Reorder of a soup does not name its corners")
	}
}

func BenchmarkBuild(b *testing.B) {
	m := sphereMesh(128, 256) // 65k triangles
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Build(m, Options{})
	}
}

func TestBuildSequentialKeepsOrder(t *testing.T) {
	m := soupOf(gridMesh(16, func(x, z float32) float32 { return 0 }))
	opt := Options{MaxTriangles: 50, MaxVertices: 255}
	r := BuildSequential(m, opt)
	checkPartition(t, m, r, opt)
	for k, tr := range r.Order {
		if int(tr) != k {
			t.Fatalf("triangle %d moved to %d", tr, k)
		}
	}
	if len(r.Clusters) != (m.TriangleCount()+49)/50 {
		t.Fatalf("%d clusters for %d triangles at 50 a cluster", len(r.Clusters), m.TriangleCount())
	}
}
