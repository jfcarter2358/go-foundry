package nanitecook

// lock_test.go — border locking keeps a tile's edge whole at every LOD, and
// the in-memory cook matches the file cook.

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/model"
	"foundry/internal/nanite"

	"github.com/go-gl/mathgl/mgl32"
)

// bumpyTile is an n x n grid over [x0, x0+size] x [0, size], displaced by a
// smooth function of WORLD position, so two tiles side by side share their
// seam vertices exactly. Open: no skirts, so its rim is an open boundary.
func bumpyTile(n int, x0, size float32) *model.ImportMesh {
	m := &model.ImportMesh{Slots: []model.ImportSlot{{Name: "ground", BaseColor: [4]float32{1, 1, 1, 1}}}}
	h := func(x, z float32) float32 {
		return 2*float32(math.Sin(float64(x)*0.21)*math.Cos(float64(z)*0.17)) + 0.5*float32(math.Sin(float64(x+z)*0.9))
	}
	for j := 0; j <= n; j++ {
		for i := 0; i <= n; i++ {
			x := x0 + size*float32(i)/float32(n)
			z := size * float32(j) / float32(n)
			m.Positions = append(m.Positions, mgl32.Vec3{x, h(x, z), z})
			m.UVs = append(m.UVs, mgl32.Vec2{x / 8, z / 8})
		}
	}
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			a := uint32(j*(n+1) + i)
			b, c, d := a+1, a+uint32(n+1), a+uint32(n+2)
			// Counter-clockwise seen from above.
			m.Indices = append(m.Indices, a, c, b, b, c, d)
			m.TriSlot = append(m.TriSlot, 0, 0)
		}
	}
	return m
}

// seamPositions is the cooked vertices on x == seam, at the cut of the given
// error, as quantised positions.
func seamPositions(m *nanite.Mesh, seam float32, errAt float32) map[[2]int32]bool {
	out := map[[2]int32]bool{}
	for ci := range m.Clusters {
		c := &m.Clusters[ci]
		if !c.Selected(errAt) {
			continue
		}
		for _, ix := range m.Indices[3*c.TriFirst : 3*(c.TriFirst+c.TriCount)] {
			x, y, z := m.Vertices[5*ix], m.Vertices[5*ix+1], m.Vertices[5*ix+2]
			if float32(math.Abs(float64(x-seam))) < 1e-3 {
				out[[2]int32{m.Quant.PosQ(y), m.Quant.PosQ(z)}] = true
			}
		}
	}
	return out
}

func TestLockBorderKeepsTheSeamAtEveryLOD(t *testing.T) {
	left := bumpyTile(48, 0, 32)
	right := bumpyTile(48, 32, 32)
	cook := func(m *model.ImportMesh, lock bool) *nanite.Mesh {
		c, err := CookMesh(m, "tile", Options{LockBorder: lock})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	a, b := cook(left, true), cook(right, true)
	if a.Levels < 3 {
		t.Fatalf("only %d DAG levels: the test needs simplification to happen", a.Levels)
	}
	full := seamPositions(a, 32, 0)
	if len(full) != 49 {
		t.Fatalf("%d seam vertices at full detail, want 49", len(full))
	}
	for _, e := range []float32{0, 0.01, 0.05, 0.2, 1, 1e9} {
		sa, sb := seamPositions(a, 32, e), seamPositions(b, 32, e)
		if len(sa) != len(full) || len(sb) != len(full) {
			t.Errorf("error %g: seam has %d / %d vertices, want %d — it cracks", e, len(sa), len(sb), len(full))
		}
		for p := range sa {
			if !sb[p] {
				t.Errorf("error %g: seam vertex %v of the left tile is not on the right's", e, p)
				break
			}
		}
	}
	// Without the lock the coarse cuts drop seam vertices — or this test
	// proves nothing.
	u := cook(left, false)
	if len(seamPositions(u, 32, 1e9)) >= len(full) {
		t.Error("unlocked, the coarsest cut kept every seam vertex: the test does not discriminate")
	}
}

func TestLockRectLocksTheRectangleEdge(t *testing.T) {
	m := bumpyTile(32, 0, 32)
	rect := [4]float32{0, 0, 32, 32}
	c, err := CookMesh(m, "tile", Options{LockRect: &rect})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(seamPositions(c, 32, 1e9)); got != 33 {
		t.Errorf("LockRect: the coarsest cut keeps %d of 33 edge vertices", got)
	}
}

func TestLocksAreHashedOnlyWhenSet(t *testing.T) {
	dir := t.TempDir()
	src := writeSampleGLB(t, dir, 2)
	plain, _ := HashSource([]string{src}, Options{})
	again, _ := HashSource([]string{src}, Options{LockBorder: false})
	locked, _ := HashSource([]string{src}, Options{LockBorder: true})
	rect := [4]float32{0, 0, 1, 1}
	rected, _ := HashSource([]string{src}, Options{LockRect: &rect})
	if plain != again {
		t.Error("an unset lock changed the hash — every existing cook would recook")
	}
	if locked == plain || rected == plain || rected == locked {
		t.Error("the locks are not in the hash")
	}
}

func TestCookMeshMatchesCookingItsGLB(t *testing.T) {
	dir := t.TempDir()
	m := SampleRock(3, 0.3)
	glb, err := model.EncodeGLB(m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rock.glb")
	if err := os.WriteFile(path, glb, 0o644); err != nil {
		t.Fatal(err)
	}
	fromFile, err := Cook(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Through a GLB round trip, as the file cook reads it, so both start from
	// the same floats.
	back, err := model.LoadGLTF(path)
	if err != nil {
		t.Fatal(err)
	}
	fromMem, err := CookMesh(back, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if fromMem.TriangleCount() != fromFile.TriangleCount() || len(fromMem.Clusters) != len(fromFile.Clusters) {
		t.Errorf("memory cook: %d tris %d clusters; file cook: %d tris %d clusters",
			fromMem.TriangleCount(), len(fromMem.Clusters), fromFile.TriangleCount(), len(fromFile.Clusters))
	}
	out := filepath.Join(dir, "mem.fnm")
	r, err := CookMeshFile(m, out, false, Options{})
	if err != nil || r.Skipped {
		t.Fatalf("first CookMeshFile: %v skipped=%v", err, r.Skipped)
	}
	if r, _ = CookMeshFile(m, out, false, Options{}); !r.Skipped {
		t.Error("an unchanged mesh recooked")
	}
	if r, _ = CookMeshFile(m, out, false, Options{LockBorder: true}); r.Skipped {
		t.Error("a changed option did not recook")
	}
	if _, err := nanite.Load(out); err != nil {
		t.Errorf("the cooked file does not load: %v", err)
	}
}
