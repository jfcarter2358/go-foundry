package gfx

// indexed_mesh_test.go — the weld is invisible: every corner an indexed
// geometry draws carries bit-for-bit the attributes the de-indexed one drew,
// and the pool really is smaller where corners agree.

import (
	"math"
	"math/rand"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// sameBits compares two float slices bit for bit (so -0 and +0 differ, and a
// NaN equals itself) — the standard the weld promises.
func sameBits(a, b []float32) bool {
	if len(a) != len(b) || (a == nil) != (b == nil) {
		return false
	}
	for i := range a {
		if math.Float32bits(a[i]) != math.Float32bits(b[i]) {
			return false
		}
	}
	return true
}

func TestWeldCornersSharesAQuadsDiagonal(t *testing.T) {
	// Two triangles over a quad, de-indexed: corners 0/3 and 2/4 are the same.
	q := [][]float32{{0, 0, 0, 0, 0}, {1, 0, 0, 1, 0}, {1, 1, 0, 1, 1}, {0, 1, 0, 0, 1}}
	var soup []float32
	for _, i := range []int{0, 1, 2, 0, 2, 3} {
		soup = append(soup, q[i]...)
	}
	m := WeldCorners(IndexedMesh{Vertices: soup})
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if m.VertexCount() != 4 || m.TriangleCount() != 2 {
		t.Fatalf("got %d vertices / %d triangles, want 4 / 2", m.VertexCount(), m.TriangleCount())
	}
	want := []uint32{0, 1, 2, 0, 2, 3}
	for i := range want {
		if m.Indices[i] != want[i] {
			t.Fatalf("indices %v, want %v (first use keeps the slot)", m.Indices, want)
		}
	}
}

// A UV seam, a crease and a skin boundary share a position but not a record,
// and each has to stay two vertices or the picture changes.
func TestWeldCornersKeepsSeamsCreasesAndSkinApart(t *testing.T) {
	p := []float32{1, 2, 3}
	rec := func(u float32) []float32 { return append(append([]float32{}, p...), u, 0) }
	soup := IndexedMesh{
		Vertices: append(append(append([]float32{}, rec(0)...), rec(0)...), rec(1)...),
		Normals:  []float32{0, 1, 0, 1, 0, 0, 0, 1, 0},
		SkinIdx:  []float32{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		SkinWt:   []float32{1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0},
	}
	m := WeldCorners(soup)
	if m.VertexCount() != 3 {
		t.Fatalf("same position, different normal or UV: welded to %d vertices, want 3", m.VertexCount())
	}
	// Change only a weight and the otherwise-identical corners 0 and 2's UVs
	// still differ — but corners 0 and 1 now differ only by normal.
	soup.Normals = []float32{0, 1, 0, 0, 1, 0, 0, 1, 0}
	if m := WeldCorners(soup); m.VertexCount() != 2 {
		t.Fatalf("corners 0 and 1 are identical: %d vertices, want 2", m.VertexCount())
	}
	soup.SkinWt[4] = 0.5
	if m := WeldCorners(soup); m.VertexCount() != 3 {
		t.Fatalf("a bone weight differs: %d vertices, want 3", m.VertexCount())
	}
}

// A random soup with every attribute and plenty of repeats comes back out of
// Deindex exactly as it went into WeldCorners.
func TestWeldCornersRoundTripsEveryAttribute(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	const pool, corners = 40, 600
	rec := func(n int) [][]float32 {
		out := make([][]float32, pool)
		for i := range out {
			out[i] = make([]float32, n)
			for j := range out[i] {
				out[i][j] = float32(r.Intn(5)) * 0.25
			}
		}
		return out
	}
	v, n, l, ls, si, sw := rec(5), rec(3), rec(LightStride), rec(LightStride), rec(4), rec(4)
	var soup IndexedMesh
	for k := 0; k < corners; k++ {
		i := r.Intn(pool)
		soup.Vertices = append(soup.Vertices, v[i]...)
		soup.Normals = append(soup.Normals, n[i]...)
		soup.Light = append(soup.Light, l[i]...)
		soup.LightSmooth = append(soup.LightSmooth, ls[i]...)
		soup.SkinIdx = append(soup.SkinIdx, si[i]...)
		soup.SkinWt = append(soup.SkinWt, sw[i]...)
	}
	m := WeldCorners(soup)
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if m.VertexCount() > pool {
		t.Errorf("%d vertices from a %d-record pool", m.VertexCount(), pool)
	}
	back := m.Deindex()
	for _, c := range []struct {
		name string
		a, b []float32
	}{
		{"vertices", soup.Vertices, back.Vertices}, {"normals", soup.Normals, back.Normals},
		{"light", soup.Light, back.Light}, {"smooth light", soup.LightSmooth, back.LightSmooth},
		{"bone indices", soup.SkinIdx, back.SkinIdx}, {"bone weights", soup.SkinWt, back.SkinWt},
	} {
		if !sameBits(c.a, c.b) {
			t.Errorf("%s did not survive weld + de-index", c.name)
		}
	}
	// Re-welding an indexed mesh gives the same mesh.
	again := WeldCorners(m)
	if !sameBits(again.Vertices, m.Vertices) || len(again.Indices) != len(m.Indices) {
		t.Error("re-welding an indexed mesh changed it")
	}
}

func TestWeldCornersDropsARaggedAttribute(t *testing.T) {
	soup := IndexedMesh{
		Vertices: []float32{0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 1, 0, 0, 0},
		Normals:  []float32{0, 1, 0}, // one corner's worth, three corners
	}
	m := WeldCorners(soup)
	if m.Normals != nil {
		t.Error("a normals array that does not cover every corner was welded on")
	}
	if err := m.Validate(); err != nil {
		t.Error(err)
	}
}

func TestIndexedMeshValidateCatchesBadIndices(t *testing.T) {
	m := IndexedMesh{Vertices: make([]float32, 15), Indices: []uint32{0, 1, 3}}
	if m.Validate() == nil {
		t.Error("index 3 into a 3-vertex pool passed")
	}
	m = IndexedMesh{Vertices: make([]float32, 15), Normals: make([]float32, 6), Indices: []uint32{0, 1, 2}}
	if m.Validate() == nil {
		t.Error("2 normals for 3 vertices passed")
	}
}

// cubeCorners is a cube as Geometry.Load takes it: each face its own four
// records (so UV seams on every edge), two triangles per face.
func cubeCorners() ([][]float32, []int) {
	var verts [][]float32
	var idx []int
	faces := [][4]mgl32.Vec3{
		{{-1, -1, 1}, {1, -1, 1}, {1, 1, 1}, {-1, 1, 1}},
		{{1, -1, -1}, {-1, -1, -1}, {-1, 1, -1}, {1, 1, -1}},
		{{-1, 1, 1}, {1, 1, 1}, {1, 1, -1}, {-1, 1, -1}},
		{{-1, -1, -1}, {1, -1, -1}, {1, -1, 1}, {-1, -1, 1}},
		{{1, -1, 1}, {1, -1, -1}, {1, 1, -1}, {1, 1, 1}},
		{{-1, -1, -1}, {-1, -1, 1}, {-1, 1, 1}, {-1, 1, -1}},
	}
	uv := [4][2]float32{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
	for _, f := range faces {
		base := len(verts)
		for i, p := range f {
			verts = append(verts, []float32{p.X(), p.Y(), p.Z(), uv[i][0], uv[i][1]})
		}
		idx = append(idx, base, base+1, base+2, base, base+2, base+3)
	}
	return verts, idx
}

// withIndexedModels runs fn with the switch set and uploads off.
func withIndexedModels(t *testing.T, on bool) {
	t.Helper()
	prevI, prevU := IndexedModels, GeometryUploadsEnabled
	IndexedModels, GeometryUploadsEnabled = on, false
	t.Cleanup(func() { IndexedModels, GeometryUploadsEnabled = prevI, prevU })
}

// Load with Indexed set draws exactly the corners Load without it drew —
// positions, UVs, smoothed normals (crease split included), skin and light.
func TestGeometryIndexedLoadMatchesDeindexed(t *testing.T) {
	verts, idx := cubeCorners()
	skinIdx := make([][]float32, len(verts))
	skinWt := make([][]float32, len(verts))
	light := make([][]float32, len(verts))
	for i := range verts {
		skinIdx[i] = []float32{float32(i % 3), 0, 0, 0}
		skinWt[i] = []float32{1, 0, 0, 0}
		light[i] = []float32{0.5, 0.25, float32(i%2) * 0.5} // short row: padded
	}
	load := func(indexed bool, smoothAngle float32) Geometry {
		g := Geometry{Smooth: true, SmoothAngle: smoothAngle, AutoSmooth: true, Indexed: indexed}
		g.SkinIdx, g.SkinWt, g.VertexLight = skinIdx, skinWt, light
		if err := g.Load(verts, idx, "", mgl32.Vec3{}); err != nil {
			t.Fatal(err)
		}
		return g
	}
	withIndexedModels(t, true)
	// 30° keeps every cube edge a crease; 100° smooths round the corners.
	for _, angle := range []float32{30, 100} {
		soup, ind := load(false, angle), load(true, angle)
		if soup.IsIndexed() || !ind.IsIndexed() {
			t.Fatalf("angle %v: indexed=%v/%v, want false/true", angle, soup.IsIndexed(), ind.IsIndexed())
		}
		mi := ind.Mesh()
		if err := mi.Validate(); err != nil {
			t.Fatal(err)
		}
		back := mi.Deindex()
		ms := soup.Mesh()
		for _, c := range []struct {
			name string
			a, b []float32
		}{
			{"vertices", ms.Vertices, back.Vertices}, {"normals", ms.Normals, back.Normals},
			{"light", ms.Light, back.Light}, {"bone indices", ms.SkinIdx, back.SkinIdx},
			{"bone weights", ms.SkinWt, back.SkinWt},
		} {
			if !sameBits(c.a, c.b) {
				t.Errorf("angle %v: %s differ between the indexed and de-indexed load", angle, c.name)
			}
		}
		if !sameBits(ind.TriangleVertices(), soup.Vertices) {
			t.Errorf("angle %v: TriangleVertices is not the soup", angle)
		}
		if ind.CornerCount() != soup.CornerCount() {
			t.Errorf("angle %v: %d corners indexed, %d de-indexed", angle, ind.CornerCount(), soup.CornerCount())
		}
		// 24 face records at most — the 12 diagonal repeats must merge. Fewer
		// is fine where smoothing made two faces' corners agree outright.
		if got := len(ind.Vertices) / 5; got > 24 || got < 8 {
			t.Errorf("angle %v: %d pool vertices, want 8..24", angle, got)
		}
		var tris int
		ind.ForEachTriangle(func(a, b, c mgl32.Vec3) bool { tris++; return true })
		if tris != 12 {
			t.Errorf("angle %v: ForEachTriangle saw %d triangles, want 12", angle, tris)
		}
	}
}

func TestGeometryIndexedSwitchOff(t *testing.T) {
	withIndexedModels(t, false)
	verts, idx := cubeCorners()
	g := Geometry{Indexed: true}
	if err := g.Load(verts, idx, "", mgl32.Vec3{}); err != nil {
		t.Fatal(err)
	}
	if g.IsIndexed() || len(g.Vertices) != 36*5 {
		t.Errorf("with IndexedModels off an Indexed geometry still welded (%d floats)", len(g.Vertices))
	}
	// Reloading clears an earlier weld rather than keeping a stale index list.
	IndexedModels = true
	_ = g.Load(verts, idx, "", mgl32.Vec3{})
	IndexedModels = false
	_ = g.Load(verts, idx, "", mgl32.Vec3{})
	if g.Indices != nil {
		t.Error("a de-indexed reload kept the previous load's indices")
	}
}

func TestEnvFlag(t *testing.T) {
	for v, want := range map[string]bool{"0": false, "off": false, "1": true, "true": true, "": true, "junk": true} {
		t.Setenv("FOUNDRY_TEST_FLAG", v)
		if got := envFlag("FOUNDRY_TEST_FLAG", true); got != want {
			t.Errorf("%q: got %v, want %v", v, got, want)
		}
	}
}

// BenchmarkWeldCorners is the load-time cost of the weld on a mesh the size
// of the biggest character in the level1-3 cast (soldier.fmdl, 11k corners),
// with normals and skin as an animated attachment carries them.
func BenchmarkWeldCorners(b *testing.B) {
	r := rand.New(rand.NewSource(1))
	const corners, pool = 11052, 4824
	recs := make([][]float32, pool)
	for i := range recs {
		recs[i] = make([]float32, 5+3+4+4)
		for j := range recs[i] {
			recs[i][j] = r.Float32()
		}
	}
	var soup IndexedMesh
	for k := 0; k < corners; k++ {
		rec := recs[r.Intn(pool)]
		soup.Vertices = append(soup.Vertices, rec[:5]...)
		soup.Normals = append(soup.Normals, rec[5:8]...)
		soup.SkinIdx = append(soup.SkinIdx, rec[8:12]...)
		soup.SkinWt = append(soup.SkinWt, rec[12:16]...)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		WeldCorners(soup)
	}
}
