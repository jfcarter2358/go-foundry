package world

// skirt_test.go — closing the band between a stacked sector and what is below.

import (
	"foundry/internal/testenv"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// storeyChunk is a ground room (floor 0, ceiling 4) with a storey above it
// (floor 4.2, ceiling 8.2) sharing the same 20x20 footprint — leaving the
// 0.2 slab between 4.0 and 4.2 walled by neither.
func storeyChunk(skirt string) *Chunk {
	ch := &Chunk{
		ID:         "block",
		Points:     []mgl32.Vec2{{0, 0}, {20, 0}, {20, 20}, {0, 20}},
		PointNames: make([]string, 4),
	}
	mk := func(id string, layer int, floorY, ceilY float32, ceilOn bool) Sector {
		return Sector{
			ID:           id,
			Layer:        layer,
			PointIndices: []int{0, 1, 2, 3},
			EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
			Floor:        Floor{Enabled: true, Height: floorY, VertexOffsets: []float32{0, 0, 0, 0}},
			Ceiling:      Ceiling{Enabled: ceilOn, Height: ceilY, VertexOffsets: []float32{0, 0, 0, 0}},
			MinBound:     mgl32.Vec2{0, 0},
			MaxBound:     mgl32.Vec2{20, 20},
		}
	}
	ground := mk("ground", 0, 0, 4.0, true)
	upper := mk("upper", 1, 4.2, 8.2, true)
	upper.Floor.Skirt = skirt
	ch.Sectors = []Sector{ground, upper}
	ch.RecomputeBounds()
	return ch
}

// skirtSpan returns the lowest and highest Y in a sector's skirt mesh.
func skirtSpan(t *testing.T, ch *Chunk, s *Sector) (lo, hi float32, n int) {
	t.Helper()
	verts, idx := buildSkirtMesh(ch, s)
	if len(idx) == 0 {
		return 0, 0, 0
	}
	lo, hi = verts[0][1], verts[0][1]
	for _, v := range verts {
		if v[1] < lo {
			lo = v[1]
		}
		if v[1] > hi {
			hi = v[1]
		}
	}
	return lo, hi, len(idx) / 3
}

func TestNoSkirtByDefault(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch := storeyChunk(SkirtNone)
	if _, _, n := skirtSpan(t, ch, &ch.Sectors[1]); n != 0 {
		t.Errorf("a sector with no skirt mode produced %d triangles", n)
	}
	ch.RebuildSkirts()
	if SkirtDrawable(&ch.Sectors[1]) {
		t.Error("a sector with no skirt mode has drawable skirt geometry")
	}
}

// The building case: the band is exactly the slab, ceiling below to floor above.
func TestSkirtToCeilingFillsTheSlab(t *testing.T) {
	ch := storeyChunk(SkirtToCeiling)
	lo, hi, n := skirtSpan(t, ch, &ch.Sectors[1])
	if n == 0 {
		t.Fatal("no skirt geometry was produced")
	}
	if n != 8 {
		t.Errorf("%d triangles, want 8 (two per outline edge)", n)
	}
	if lo != 4.0 {
		t.Errorf("skirt bottom at y=%v, want the ceiling below at 4.0", lo)
	}
	if hi != 4.2 {
		t.Errorf("skirt top at y=%v, want the floor above at 4.2", hi)
	}
}

// The overhang case: the face runs all the way to the ground below.
func TestSkirtToFloorReachesTheGround(t *testing.T) {
	ch := storeyChunk(SkirtToFloor)
	lo, hi, n := skirtSpan(t, ch, &ch.Sectors[1])
	if n == 0 {
		t.Fatal("no skirt geometry was produced")
	}
	if lo != 0 {
		t.Errorf("skirt bottom at y=%v, want the floor below at 0", lo)
	}
	if hi != 4.2 {
		t.Errorf("skirt top at y=%v, want the shelf's own floor at 4.2", hi)
	}
}

// An overhang over open ground — the sector below has no ceiling, so
// SkirtToCeiling has nothing to meet and must draw nothing rather than
// inventing a face. SkirtToFloor is the mode for that shape.
func TestSkirtToCeilingSkipsAnOpenSectorBelow(t *testing.T) {
	ch := storeyChunk(SkirtToCeiling)
	ch.Sectors[0].Ceiling.Enabled = false

	if _, _, n := skirtSpan(t, ch, &ch.Sectors[1]); n != 0 {
		t.Errorf("%d triangles cut across a sector that has no ceiling", n)
	}

	ch.Sectors[1].Floor.Skirt = SkirtToFloor
	lo, _, n := skirtSpan(t, ch, &ch.Sectors[1])
	if n == 0 {
		t.Fatal("SkirtToFloor produced nothing over an open sector")
	}
	if lo != 0 {
		t.Errorf("overhang face bottom at y=%v, want the open ground at 0", lo)
	}
}

// A sector with nothing under it has no band to close.
func TestNoSkirtWithNothingBelow(t *testing.T) {
	ch := storeyChunk(SkirtToCeiling)
	ch.Sectors = ch.Sectors[1:] // drop the ground floor
	if _, _, n := skirtSpan(t, ch, &ch.Sectors[0]); n != 0 {
		t.Errorf("%d triangles hang under a sector with nothing below it", n)
	}
}

// The skirt follows a sloped floor rather than sitting at one flat height.
func TestSkirtFollowsASlopedFloor(t *testing.T) {
	ch := storeyChunk(SkirtToCeiling)
	upper := &ch.Sectors[1]
	upper.Floor.VertexOffsets = []float32{0, 0, 3, 3} // tilt the shelf

	lo, hi, n := skirtSpan(t, ch, upper)
	if n == 0 {
		t.Fatal("no skirt geometry")
	}
	if hi <= 4.2+1e-3 {
		t.Errorf("skirt top reaches only y=%v; the raised side of the floor is at 7.2", hi)
	}
	if lo != 4.0 {
		t.Errorf("skirt bottom at y=%v, want the flat ceiling below at 4.0", lo)
	}
}

// Only the edges that actually overhang something get a face — a shelf that
// covers a room at one end and open air at the other is skirted at one end.
func TestSkirtOnlyWhereSomethingIsBelow(t *testing.T) {
	ch := storeyChunk(SkirtToCeiling)
	// Shrink the ground room to the western half, so the upper storey's east
	// edge hangs over nothing.
	ch.Points = append(ch.Points, mgl32.Vec2{10, 0}, mgl32.Vec2{10, 20})
	ch.PointNames = append(ch.PointNames, "", "")
	ch.Sectors[0].PointIndices = []int{0, 4, 5, 3}
	ch.Sectors[0].MaxBound = mgl32.Vec2{10, 20}

	_, _, n := skirtSpan(t, ch, &ch.Sectors[1])
	if n == 0 {
		t.Fatal("no skirt at all")
	}
	if n >= 8 {
		t.Errorf("%d triangles: every edge was skirted, including the ones over nothing", n)
	}
}

// The derived mesh must not survive a clone — a copy rebuilds it from its own
// neighbours, which may be at different heights entirely.
func TestCloneDropsDerivedSkirtGeometry(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch := storeyChunk(SkirtToCeiling)
	ch.RebuildSkirts()

	cp := ch.Clone("copy", "_2")
	if len(cp.Sectors[1].Floor.SkirtGeometry.Vertices) != 0 {
		t.Error("the clone carried the original's skirt mesh")
	}
	if cp.Sectors[1].Floor.Skirt != SkirtToCeiling {
		t.Errorf("the clone lost the skirt MODE: %q", cp.Sectors[1].Floor.Skirt)
	}
}
