package meshout

// meshout_test.go — the builder welds and derives normals, a closed height
// tile is watertight and faces out, and a cooked tile loads as a closed
// Nanite solid.

import (
	"math"
	"path/filepath"
	"testing"

	"foundry/internal/nanite"

	"github.com/go-gl/mathgl/mgl32"
)

func TestWeldSharesSoupCorners(t *testing.T) {
	var b Builder
	// Two triangles of a unit quad as a soup: six corners, four positions.
	b.SoupTri(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0, 1}, mgl32.Vec3{1, 0, 1})
	b.SoupTri(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 0, 1}, mgl32.Vec3{1, 0.00001, 0})
	b.Weld(0, false)
	if b.VertexCount() != 4 || b.TriangleCount() != 2 {
		t.Fatalf("welded to %d vertices %d triangles, want 4 and 2", b.VertexCount(), b.TriangleCount())
	}
	m := b.Mesh()
	for i, n := range m.Normals {
		if n.Sub(mgl32.Vec3{0, 1, 0}).Len() > 1e-3 {
			t.Errorf("normal %d = %v, want +Y for a CCW-from-above quad", i, n)
		}
	}
	if len(m.Slots) != 1 || len(m.TriSlot) != 2 {
		t.Errorf("slots %v triSlot %v", m.Slots, m.TriSlot)
	}
}

func TestWeldDropsDegenerates(t *testing.T) {
	var b Builder
	b.SoupTri(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0, 0.00001}, mgl32.Vec3{1, 0, 0})
	b.Weld(0, false)
	if b.TriangleCount() != 0 {
		t.Errorf("a triangle that welds to a line survived")
	}
}

func tileGrid(closed bool) HeightGrid {
	return HeightGrid{X0: 10, Z0: -5, SizeX: 16, SizeZ: 8, NX: 16, NZ: 8, Closed: closed, Base: -10,
		Height: func(i, j int) float32 { return float32(math.Sin(float64(i)*0.4) + math.Cos(float64(j)*0.3)) }}
}

func TestClosedHeightTileIsWatertightAndOutward(t *testing.T) {
	var b Builder
	g := tileGrid(true)
	g.Build(&b)
	if open := b.OpenEdges(); open != 0 {
		t.Fatalf("closed tile has %d open edges", open)
	}
	m := b.Mesh()
	// Outward: the signed volume (sum of each triangle's tetrahedron with the
	// origin) is positive for an outward-wound closed surface, and equals the
	// tile's volume.
	var vol float64
	for t := 0; t+2 < len(m.Indices); t += 3 {
		a, c, d := m.Positions[m.Indices[t]], m.Positions[m.Indices[t+1]], m.Positions[m.Indices[t+2]]
		vol += float64(a.Dot(c.Cross(d))) / 6
	}
	// Roughly 16 x 8 x ~10 (the surface wanders about zero).
	if vol < 16*8*7 || vol > 16*8*13 {
		t.Errorf("signed volume %v: inward-wound, or not the tile", vol)
	}
	open := Builder{}
	tileGrid(false).Build(&open)
	if open.OpenEdges() != 2*(16+8) {
		t.Errorf("an open tile has %d boundary edges, want its rim's %d", open.OpenEdges(), 2*(16+8))
	}
	if r := g.Rect(); r != [4]float32{10, -5, 26, 3} {
		t.Errorf("Rect %v", r)
	}
}

func TestCookedTileLoadsClosed(t *testing.T) {
	var b Builder
	g := tileGrid(true)
	g.Build(&b)
	out := filepath.Join(t.TempDir(), "tile.fnm")
	r := g.Rect()
	skipped, err := Cook(b.Mesh(), out, CookOptions{LockRect: &r})
	if err != nil || skipped {
		t.Fatalf("cook: %v skipped=%v", err, skipped)
	}
	m, err := nanite.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	if !m.ClosedSolid {
		t.Error("the cooked closed tile is not a closed solid")
	}
	if skipped, _ := Cook(b.Mesh(), out, CookOptions{LockRect: &r}); !skipped {
		t.Error("an unchanged tile recooked")
	}
	glb := filepath.Join(t.TempDir(), "tile.glb")
	if err := WriteGLB(glb, b.Mesh()); err != nil {
		t.Fatal(err)
	}
}

// TestQuantBitsForGivesTheStep: the bits QuantBitsFor picks make
// nanite.NewQuant choose exactly the asked step, for meshes of many sizes —
// what lets every tile of a field cook on one grid.
func TestQuantBitsForGivesTheStep(t *testing.T) {
	step := float32(1.0 / 2048)
	for _, ext := range []float32{3, 31.9, 32, 64.5, 128, 200, 255.99, 256} {
		lo := mgl32.Vec3{-ext / 2, -10, -ext / 2}
		hi := mgl32.Vec3{ext / 2, 5, ext / 2}
		bits := QuantBitsFor(lo, hi, step)
		if bits == 0 {
			t.Fatalf("no bits for extent %v", ext)
		}
		if got := nanite.NewQuant(lo, hi, 1, bits, 0, 0).Step; got != step {
			t.Errorf("extent %v: %d bits give a %g step, want %g", ext, bits, got, step)
		}
	}
	if QuantBitsFor(mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, 0.3) != 0 {
		t.Error("a step that is not a power of two was accepted")
	}
	if QuantBitsFor(mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, 4) != 0 {
		t.Error("a step coarser than the mesh was accepted")
	}
}
