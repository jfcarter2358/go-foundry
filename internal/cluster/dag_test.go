package cluster

import (
	"math"
	"math/rand"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// lumpySphere is a closed, outward-wound icosphere of 20*4^level triangles,
// pushed in and out by a few sine lumps so simplification has something to
// keep. Each vertex is x, y, z, u, v, nx, ny, nz; with seam, the vertices on
// the x > 0 half of the z = 0 meridian plane are split into two records whose
// u differs by one, as a spherical UV map's wrap-around seam does.
func lumpySphere(level int, seam bool) Mesh {
	t := float32((1 + math.Sqrt(5)) / 2)
	verts := []mgl32.Vec3{
		{-1, t, 0}, {1, t, 0}, {-1, -t, 0}, {1, -t, 0},
		{0, -1, t}, {0, 1, t}, {0, -1, -t}, {0, 1, -t},
		{t, 0, -1}, {t, 0, 1}, {-t, 0, -1}, {-t, 0, 1},
	}
	for i := range verts {
		verts[i] = verts[i].Normalize()
	}
	faces := [][3]uint32{
		{0, 11, 5}, {0, 5, 1}, {0, 1, 7}, {0, 7, 10}, {0, 10, 11},
		{1, 5, 9}, {5, 11, 4}, {11, 10, 2}, {10, 7, 6}, {7, 1, 8},
		{3, 9, 4}, {3, 4, 2}, {3, 2, 6}, {3, 6, 8}, {3, 8, 9},
		{4, 9, 5}, {2, 4, 11}, {6, 2, 10}, {8, 6, 7}, {9, 8, 1},
	}
	for l := 0; l < level; l++ {
		mid := map[[2]uint32]uint32{}
		midpoint := func(a, b uint32) uint32 {
			k := [2]uint32{min(a, b), max(a, b)}
			if v, ok := mid[k]; ok {
				return v
			}
			verts = append(verts, verts[a].Add(verts[b]).Normalize())
			mid[k] = uint32(len(verts) - 1)
			return mid[k]
		}
		next := make([][3]uint32, 0, len(faces)*4)
		for _, f := range faces {
			a, b, c := midpoint(f[0], f[1]), midpoint(f[1], f[2]), midpoint(f[2], f[0])
			next = append(next, [3]uint32{f[0], a, c}, [3]uint32{f[1], b, a}, [3]uint32{f[2], c, b}, [3]uint32{a, b, c})
		}
		faces = next
	}
	f0 := faces[0]
	if verts[f0[1]].Sub(verts[f0[0]]).Cross(verts[f0[2]].Sub(verts[f0[0]])).Dot(verts[f0[0]]) < 0 {
		for i := range faces {
			faces[i][1], faces[i][2] = faces[i][2], faces[i][1]
		}
	}
	for i, v := range verts {
		r := 1 + 0.08*float32(math.Sin(float64(3*v[0]))*math.Cos(float64(2*v[1]))) + 0.04*float32(math.Sin(float64(5*v[2])))
		verts[i] = v.Mul(r)
	}
	record := func(v mgl32.Vec3, u float32) []float32 {
		n := v.Normalize()
		return []float32{v[0], v[1], v[2], u, v[1]*0.5 + 0.5, n[0], n[1], n[2]}
	}
	uOf := func(v mgl32.Vec3) float32 {
		return float32(math.Atan2(float64(v[2]), float64(v[0]))/(2*math.Pi)) + 0.5
	}
	var pos []float32
	for _, v := range verts {
		pos = append(pos, record(v, uOf(v))...)
	}
	var idx []uint32
	twin := map[uint32]uint32{}
	for _, f := range faces {
		tri := f
		c := verts[f[0]].Add(verts[f[1]]).Add(verts[f[2]])
		if seam && c[0] < 0 {
			// The wrap-around: on the x < 0 side, a triangle whose centre
			// is at z < 0 has u near 0, one at z >= 0 near 1. A corner on
			// the wrong side of that takes a twin record one whole u over.
			low := c[2] < 0
			for k, v := range f {
				u := uOf(verts[v])
				shift := float32(0)
				if low && u > 0.5 {
					shift = -1
				} else if !low && u < 0.5 {
					shift = 1
				}
				if shift == 0 {
					continue
				}
				w, ok := twin[v]
				if !ok {
					w = uint32(len(pos) / 8)
					pos = append(pos, record(verts[v], u+shift)...)
					twin[v] = w
				}
				tri[k] = w
			}
		}
		idx = append(idx, tri[0], tri[1], tri[2])
	}
	return Mesh{Positions: pos, Stride: 8, Indices: idx}
}

// closedByPosition reports whether idx bounds a solid, vertices matched by
// position: every edge used by exactly two triangles running opposite ways.
func closedByPosition(t *testing.T, m Mesh, idx []uint32) bool {
	t.Helper()
	ids := positionIDs(&m)
	type edge struct{ uses, dir int }
	edges := map[[2]int32]edge{}
	for i := 0; i+2 < len(idx); i += 3 {
		a, b, c := ids[idx[i]], ids[idx[i+1]], ids[idx[i+2]]
		if a == b || b == c || a == c {
			continue
		}
		for _, e := range [][2]int32{{a, b}, {b, c}, {c, a}} {
			k, d := e, 1
			if k[0] > k[1] {
				k, d = [2]int32{k[1], k[0]}, -1
			}
			x := edges[k]
			x.uses++
			x.dir += d
			edges[k] = x
		}
	}
	bad := 0
	for _, e := range edges {
		if e.uses != 2 || e.dir != 0 {
			bad++
		}
	}
	if bad > 0 {
		t.Logf("%d of %d edges are not shared by two opposite triangles", bad, len(edges))
	}
	return bad == 0 && len(edges) > 0
}

func surfaceArea(m Mesh, idx []uint32) float64 {
	a := 0.0
	for i := 0; i+2 < len(idx); i += 3 {
		p0, p1, p2 := m.Position(idx[i]), m.Position(idx[i+1]), m.Position(idx[i+2])
		a += float64(p1.Sub(p0).Cross(p2.Sub(p0)).Len()) / 2
	}
	return a
}

func TestSimplifyFlatGridIsFreeAndKeepsItsOutline(t *testing.T) {
	m := gridMesh(16, func(x, z float32) float32 { return 0 })
	// A planar grid with linear attributes: every interior collapse is free.
	var pos []float32
	for v := 0; v < m.VertexCount(); v++ {
		p := m.Position(uint32(v))
		pos = append(pos, p[0], p[1], p[2], p[0]*0.1, p[2]*0.1)
	}
	m = Mesh{Positions: pos, Stride: 5, Indices: m.Indices}
	before := surfaceArea(m, m.Indices)
	res := Simplify(m.Positions, 5, m.Indices, SimplifyOptions{TargetTriangles: 2, AttrWeights: []float32{1, 1}})
	if got := len(res.Indices) / 3; got > 8 {
		t.Fatalf("a flat 512-triangle grid simplified only to %d triangles", got)
	}
	if res.Error > 1e-3 {
		t.Fatalf("a flat grid's simplification reports error %g", res.Error)
	}
	if after := surfaceArea(m, res.Indices); math.Abs(after-before) > 1e-3*before {
		t.Fatalf("area %g became %g: the outline moved", before, after)
	}
}

func TestSimplifyNeverMovesALockedVertex(t *testing.T) {
	m := lumpySphere(3, false)
	locked := map[uint32]bool{}
	for v := 0; v < m.VertexCount(); v += 7 {
		locked[uint32(v)] = true
	}
	used := map[uint32]bool{}
	for _, v := range m.Indices {
		used[v] = true
	}
	res := Simplify(m.Positions, m.Stride, m.Indices, SimplifyOptions{TargetTriangles: 50,
		Locked: func(v uint32) bool { return locked[v] }})
	after := map[uint32]bool{}
	for _, v := range res.Indices {
		after[v] = true
	}
	for v := range locked {
		if used[v] && !after[v] {
			t.Fatalf("locked vertex %d was collapsed away", v)
		}
	}
	if len(res.Indices)/3 >= m.TriangleCount() {
		t.Fatalf("nothing was simplified")
	}
	if !closedByPosition(t, m, res.Indices) {
		t.Fatalf("a closed mesh came out of simplification open")
	}
}

func TestSimplifyKeepsASeamASeam(t *testing.T) {
	m := lumpySphere(3, true)
	res := Simplify(m.Positions, m.Stride, m.Indices, SimplifyOptions{TargetTriangles: m.TriangleCount() / 4,
		AttrWeights: []float32{0.5, 0.5, 0.1, 0.1, 0.1}})
	if !closedByPosition(t, m, res.Indices) {
		t.Fatalf("simplifying across a UV seam opened the mesh")
	}
	// No triangle may straddle the seam with records from both sides: its
	// u would run the whole texture backwards across it.
	for i := 0; i+2 < len(res.Indices); i += 3 {
		lo, hi := float32(10), float32(-10)
		for k := 0; k < 3; k++ {
			u := m.Positions[int(res.Indices[i+k])*8+3]
			lo, hi = min(lo, u), max(hi, u)
		}
		if hi-lo > 0.5 {
			t.Fatalf("triangle %d spans u %.2f..%.2f: the seam was smeared", i/3, lo, hi)
		}
	}
	if len(res.Indices)/3 > m.TriangleCount()/2 {
		t.Fatalf("the seamed sphere simplified only to %d of %d", len(res.Indices)/3, m.TriangleCount())
	}
}

// A grid lit by a step: the left half's baked light record is dark, the
// right half's bright, interpolated across one column of quads. Weighted, the
// simplifier must keep that column's edges; unweighted, it is free to
// smear the step across the whole grid.
func TestSimplifyWeighsABakedLightRecord(t *testing.T) {
	const n = 16
	g := gridMesh(n, func(x, z float32) float32 { return 0 })
	stride := 3 + 7
	var pos []float32
	for v := 0; v < g.VertexCount(); v++ {
		p := g.Position(uint32(v))
		light := float32(0.1)
		if p[0] > n/2 {
			light = 0.9
		}
		pos = append(pos, p[0], p[1], p[2], light, light, light, 0.5, 0, 0, 0)
	}
	m := Mesh{Positions: pos, Stride: stride, Indices: g.Indices}
	// The worst light error over the grid: sample each original vertex's
	// light against the simplified surface's interpolation there.
	lightErr := func(idx []uint32) float32 {
		worst := float32(0)
		for v := 0; v < m.VertexCount(); v++ {
			p := m.Position(uint32(v))
			for i := 0; i+2 < len(idx); i += 3 {
				a, b, c := m.Position(idx[i]), m.Position(idx[i+1]), m.Position(idx[i+2])
				// Barycentrics in the xz plane.
				d := (b[2]-c[2])*(a[0]-c[0]) + (c[0]-b[0])*(a[2]-c[2])
				if d == 0 {
					continue
				}
				l1 := ((b[2]-c[2])*(p[0]-c[0]) + (c[0]-b[0])*(p[2]-c[2])) / d
				l2 := ((c[2]-a[2])*(p[0]-c[0]) + (a[0]-c[0])*(p[2]-c[2])) / d
				l3 := 1 - l1 - l2
				if l1 < -1e-4 || l2 < -1e-4 || l3 < -1e-4 {
					continue
				}
				got := l1*pos[int(idx[i])*stride+3] + l2*pos[int(idx[i+1])*stride+3] + l3*pos[int(idx[i+2])*stride+3]
				want := pos[v*stride+3]
				worst = max(worst, float32(math.Abs(float64(got-want))))
				break
			}
		}
		return worst
	}
	weights := []float32{4, 4, 4, 4, 4, 4, 4}
	weighted := Simplify(m.Positions, stride, m.Indices, SimplifyOptions{TargetTriangles: 2, AttrWeights: weights, MaxError: 0.05})
	plain := Simplify(m.Positions, stride, m.Indices, SimplifyOptions{TargetTriangles: 2})
	if e := lightErr(weighted.Indices); e > 0.05 {
		t.Fatalf("weighted simplification smeared the light step by %.3f", e)
	}
	if e := lightErr(plain.Indices); e < 0.2 {
		t.Fatalf("unweighted simplification kept the light step (%.3f): the test proves nothing", e)
	}
	if len(weighted.Indices) >= len(m.Indices) {
		t.Fatalf("weighted simplification removed nothing")
	}
}

func TestDAGIsMonotonicAndEndsInARoot(t *testing.T) {
	m := lumpySphere(5, true) // 20480 triangles
	d := BuildDAG(m, DAGOptions{AttrWeights: []float32{0.2, 0.2, 0.05, 0.05, 0.05}})
	if d.Levels < 4 {
		t.Fatalf("only %d levels for 20480 triangles", d.Levels)
	}
	if got := d.BaseTriangles(); got != m.TriangleCount() {
		t.Fatalf("level 0 holds %d of %d triangles", got, m.TriangleCount())
	}
	roots := 0
	rootTris := 0
	for i := range d.Clusters {
		c := &d.Clusters[i]
		if c.IsRoot() {
			roots++
			rootTris += int(c.TriCount)
			continue
		}
		if c.ParentError < c.Error {
			t.Fatalf("cluster %d: parent error %g below its own %g", i, c.ParentError, c.Error)
		}
		if c.LODCenter.Sub(c.ParentCenter).Len()+c.LODRadius > c.ParentRadius*(1+1e-5) {
			t.Fatalf("cluster %d's LOD sphere pokes out of its parent's", i)
		}
		if c.Level > 0 && c.Group < 0 {
			t.Fatalf("cluster %d at level %d has no group", i, c.Level)
		}
	}
	if rootTris > m.TriangleCount()/8 {
		t.Fatalf("the roots still hold %d triangles of %d", rootTris, m.TriangleCount())
	}
	t.Logf("%d clusters over %d levels, %d roots with %d triangles", len(d.Clusters), d.Levels, roots, rootTris)
	// Layout: clusters in level order, each cluster's triangles where its
	// TriFirst says, and a group's children one run.
	next := uint32(0)
	for i := range d.Clusters {
		c := &d.Clusters[i]
		if c.TriFirst != next {
			t.Fatalf("cluster %d does not follow on", i)
		}
		next += c.TriCount
		if i > 0 && d.Clusters[i-1].Level > c.Level {
			t.Fatalf("cluster %d breaks level order", i)
		}
	}
	for gi, g := range d.Groups {
		for k := 1; k < len(g.Children); k++ {
			if g.Children[k] != g.Children[k-1]+1 {
				t.Fatalf("group %d's children are not one run: %v", gi, g.Children)
			}
		}
	}
}

func TestEveryDAGCutIsClosed(t *testing.T) {
	m := lumpySphere(5, true)
	d := BuildDAG(m, DAGOptions{AttrWeights: []float32{0.2, 0.2, 0.05, 0.05, 0.05}})
	area := surfaceArea(m, m.Indices)
	maxErr := float32(0)
	for i := range d.Clusters {
		maxErr = max(maxErr, d.Clusters[i].Error)
	}
	prev := m.TriangleCount() + 1
	for _, f := range []float32{0, 0.001, 0.01, 0.05, 0.2, 0.5, 1, 2} {
		thr := f * maxErr
		idx := d.CutIndices(thr)
		if !closedByPosition(t, m, idx) {
			t.Fatalf("the cut at error %g (%d triangles) has cracks", thr, len(idx)/3)
		}
		if a := surfaceArea(m, idx); math.Abs(a-area) > 0.1*area {
			t.Fatalf("the cut at error %g has area %g of %g", thr, a, area)
		}
		if len(idx)/3 > prev {
			t.Fatalf("a coarser threshold drew more: %d after %d", len(idx)/3, prev)
		}
		prev = len(idx) / 3
	}
	if prev*8 > m.TriangleCount() {
		t.Fatalf("the coarsest cut is still %d of %d triangles", prev, m.TriangleCount())
	}
}

// Cuts chosen per cluster from an eye — different detail across the mesh —
// are closed too, and walking the tree finds exactly the clusters testing
// every one does.
func TestViewCutsAreClosedAndTheTreeFindsThemAll(t *testing.T) {
	m := lumpySphere(5, false)
	d := BuildDAG(m, DAGOptions{AttrWeights: []float32{0.2, 0.2, 0.05, 0.05, 0.05}})
	nodes, leafOf := BuildLODTree(d.Clusters, [][2]int32{{0, int32(len(d.Clusters))}})
	if len(nodes) == 0 {
		t.Fatal("no tree")
	}
	// Every cluster is in exactly one leaf, which holds it.
	for ci, l := range leafOf {
		found := false
		for _, n := range nodes {
			if n.Leaf == l {
				found = int32(ci) >= n.ClusterFirst && int32(ci) < n.ClusterFirst+n.ClusterCount
			}
		}
		if !found {
			t.Fatalf("cluster %d is not in its leaf %d", ci, l)
		}
	}
	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 40; trial++ {
		dir := mgl32.Vec3{rng.Float32()*2 - 1, rng.Float32()*2 - 1, rng.Float32()*2 - 1}.Normalize()
		dist := float32(math.Pow(10, rng.Float64()*2.5-0.3)) // 0.5 to 150 radii
		eye := dir.Mul(dist)
		k := float32(1080 / (2 * math.Tan(math.Pi/8)) / 1) // 1px at 1080p, 45°
		var want []int
		var idx []uint32
		for i := range d.Clusters {
			if d.Clusters[i].SelectedFrom(eye, k) {
				want = append(want, i)
				c := &d.Clusters[i]
				idx = append(idx, d.Indices[3*c.TriFirst:3*(c.TriFirst+c.TriCount)]...)
			}
		}
		if !closedByPosition(t, m, idx) {
			t.Fatalf("eye %v: the cut (%d triangles) has cracks", eye, len(idx)/3)
		}
		got := SelectThroughTree(nodes, d.Clusters, eye, k)
		if len(got) != len(want) {
			t.Fatalf("eye %v: the tree found %d clusters, the flat test %d", eye, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("eye %v: the tree's cut differs at %d", eye, i)
			}
		}
	}
}

// The cost falls with distance: a mesh far off draws a small fraction of its
// triangles, and roughly by the area it covers on screen.
func TestViewCutCostsByScreenSize(t *testing.T) {
	m := lumpySphere(6, false) // 81920 triangles
	d := BuildDAG(m, DAGOptions{AttrWeights: []float32{0.2, 0.2, 0.05, 0.05, 0.05}})
	k := float32(1080 / (2 * math.Tan(math.Pi/8)))
	count := func(dist float32) int {
		n := 0
		eye := mgl32.Vec3{0, 0, dist}
		for i := range d.Clusters {
			if d.Clusters[i].SelectedFrom(eye, k) {
				n += int(d.Clusters[i].TriCount)
			}
		}
		return n
	}
	near, mid, far := count(3), count(30), count(300)
	t.Logf("triangles at 3, 30, 300 m: %d, %d, %d of %d", near, mid, far, m.TriangleCount())
	if !(near > mid && mid > far) {
		t.Fatalf("cost does not fall with distance: %d, %d, %d", near, mid, far)
	}
	if far*20 > m.TriangleCount() {
		t.Fatalf("at 300 m the mesh still draws %d of %d triangles", far, m.TriangleCount())
	}
}

func TestDAGIsDeterministic(t *testing.T) {
	m := lumpySphere(4, true)
	a := BuildDAG(m, DAGOptions{AttrWeights: []float32{0.2, 0.2}})
	b := BuildDAG(m, DAGOptions{AttrWeights: []float32{0.2, 0.2}})
	if len(a.Clusters) != len(b.Clusters) || len(a.Indices) != len(b.Indices) {
		t.Fatalf("two builds differ in size")
	}
	for i := range a.Indices {
		if a.Indices[i] != b.Indices[i] {
			t.Fatalf("two builds differ at index %d", i)
		}
	}
	for i := range a.Clusters {
		if a.Clusters[i] != b.Clusters[i] {
			t.Fatalf("two builds differ at cluster %d", i)
		}
	}
}

func BenchmarkBuildDAG(b *testing.B) {
	m := lumpySphere(6, true)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BuildDAG(m, DAGOptions{AttrWeights: []float32{0.2, 0.2, 0.05, 0.05, 0.05}})
	}
}

// BaseOrder names, for each level-0 triangle as laid out, the input triangle
// it is.
func TestDAGBaseOrderNamesTheInputTriangles(t *testing.T) {
	m := lumpySphere(3, false)
	d := BuildDAG(m, DAGOptions{})
	if len(d.BaseOrder) != m.TriangleCount() {
		t.Fatalf("BaseOrder has %d of %d triangles", len(d.BaseOrder), m.TriangleCount())
	}
	seen := make([]bool, m.TriangleCount())
	for k, src := range d.BaseOrder {
		if seen[src] {
			t.Fatalf("input triangle %d appears twice", src)
		}
		seen[src] = true
		for c := 0; c < 3; c++ {
			if d.Indices[3*k+c] != m.Indices[3*int(src)+c] {
				t.Fatalf("level-0 triangle %d is not input triangle %d", k, src)
			}
		}
	}
}

// A leaf's clusters share every number the LOD test reads, so a view takes
// all of a leaf or none of it — what lets a renderer draw a leaf whole.
func TestALeafIsCutWhole(t *testing.T) {
	m := lumpySphere(5, true)
	d := BuildDAG(m, DAGOptions{AttrWeights: []float32{0.2, 0.2, 0.05, 0.05, 0.05}})
	nodes, _ := BuildLODTree(d.Clusters, [][2]int32{{0, int32(len(d.Clusters))}})
	ranges := LeafRanges(nodes)
	next := int32(0)
	for _, r := range ranges {
		if r[0] != next {
			t.Fatalf("leaves do not cover the clusters in order: %v at %d", r, next)
		}
		next += r[1]
		a := &d.Clusters[r[0]]
		for c := r[0] + 1; c < r[0]+r[1]; c++ {
			b := &d.Clusters[c]
			// An own error of 0 (level 0) passes from anywhere, whatever
			// its sphere.
			ownDiffers := a.Error != b.Error || (a.Error > 0 && (a.LODCenter != b.LODCenter || a.LODRadius != b.LODRadius))
			if ownDiffers || a.ParentError != b.ParentError || a.ParentCenter != b.ParentCenter || a.ParentRadius != b.ParentRadius {
				t.Fatalf("leaf %v: clusters %d and %d differ in their LOD numbers", r, r[0], c)
			}
		}
	}
	if int(next) != len(d.Clusters) {
		t.Fatalf("leaves cover %d of %d clusters", next, len(d.Clusters))
	}
	t.Logf("%d clusters in %d leaves", len(d.Clusters), len(ranges))
}
