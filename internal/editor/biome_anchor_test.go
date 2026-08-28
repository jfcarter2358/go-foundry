package editor

import (
	"foundry/internal/biome"
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// anchorTestLevel builds a 4×4 square sector with a biome whose first vertex
// sits at the sector's centre and whose other two are far outside it, so one
// call exercises both the "follows the floor" and "nothing to follow" paths.
// Floor and ceiling stay disabled to keep Sector.Load off the GPU.
func anchorTestLevel() *world.Chunk {
	Editor.level = &world.Level{Chunks: []world.Chunk{{
		ID:      "c0",
		Points:  []mgl32.Vec2{{0, 0}, {4, 0}, {4, 4}, {0, 4}},
		Sectors: []world.Sector{{ID: "s0", PointIndices: []int{0, 1, 2, 3}}},
		BiomeSectors: []world.BiomeSector{{
			ID:     "paint",
			Points: []mgl32.Vec2{{2, 2}, {50, 50}, {54, 50}},
		}},
	}}}
	ch := &Editor.level.Chunks[0]
	_ = world.ReloadSector(ch, &ch.Sectors[0])
	ch.BiomeSectors[0].HeightDirty = false
	ch.BiomeSectors[0].GroundDirty = false
	return ch
}

func wantPoint(t *testing.T, got, want mgl32.Vec2, what string) {
	t.Helper()
	if math.Abs(float64(got.X()-want.X())) > 1e-4 || math.Abs(float64(got.Y()-want.Y())) > 1e-4 {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// TestMeanValueCoordsReproducePoint pins the property the whole binding rests
// on: the weights put the point back exactly where it was.
func TestMeanValueCoordsReproducePoint(t *testing.T) {
	square := []mgl32.Vec2{{0, 0}, {4, 0}, {4, 4}, {0, 4}}

	// The centre of a square is an equal blend of its corners.
	w := biome.MeanValueCoords(mgl32.Vec2{2, 2}, square)
	if w == nil {
		t.Fatal("no coordinates for the centre of a square")
	}
	for i, got := range w {
		if math.Abs(float64(got-0.25)) > 1e-4 {
			t.Fatalf("centre weight %d = %v, want 0.25 (%v)", i, got, w)
		}
	}

	// Any interior point reconstructs, on a non-convex polygon too.
	arrow := []mgl32.Vec2{{0, 0}, {6, 0}, {6, 6}, {3, 2}, {0, 6}}
	for _, v := range []mgl32.Vec2{{1, 1}, {3, 0.5}, {5, 3}, {2.5, 1.5}} {
		w := biome.MeanValueCoords(v, arrow)
		if w == nil {
			t.Fatalf("no coordinates for %v", v)
		}
		var sum float32
		var back mgl32.Vec2
		for i, weight := range w {
			sum += weight
			back = back.Add(arrow[i].Mul(weight))
		}
		if math.Abs(float64(sum-1)) > 1e-4 {
			t.Fatalf("weights for %v sum to %v, want 1", v, sum)
		}
		wantPoint(t, back, v, "reconstructed point")
	}

	// A vertex and a point on an edge are the degenerate cases the formula has
	// to special-case rather than divide by zero on.
	if w := biome.MeanValueCoords(square[1], square); w == nil || w[1] != 1 {
		t.Fatalf("coordinates at a corner = %v, want all the weight on corner 1", w)
	}
	if w := biome.MeanValueCoords(mgl32.Vec2{2, 0}, square); w == nil ||
		math.Abs(float64(w[0]-0.5)) > 1e-4 || math.Abs(float64(w[1]-0.5)) > 1e-4 {
		t.Fatalf("coordinates mid-edge = %v, want half on each end of that edge", w)
	}
	if biome.MeanValueCoords(mgl32.Vec2{0, 0}, []mgl32.Vec2{{0, 0}, {1, 1}}) != nil {
		t.Fatal("a two-point polygon should not bind")
	}
}

// TestBiomeFollowsEdgeDrag is the reported case: drag one edge of a sector and
// the paint inside it stretches with the floor rather than staying put.
func TestBiomeFollowsEdgeDrag(t *testing.T) {
	restore := saveEditorSelection()
	defer restore()
	ch := anchorTestLevel()

	// Drag the x=4 edge (points 1 and 2) out to x=6, the way an edge selection
	// drives both of its endpoints at once.
	Editor.SelectedVertices = []vertexSel{
		{chunkIdx: 0, sectorIdx: 0, vertIdx: 1, pointIdx: 1, isFloor: true},
		{chunkIdx: 0, sectorIdx: 0, vertIdx: 2, pointIdx: 2, isFloor: true},
	}
	setSelectedVertexPoints(func(i int, sel vertexSel, pt mgl32.Vec2) mgl32.Vec2 {
		return mgl32.Vec2{6, pt.Y()}
	})

	// The centre of a square is the average of its corners, so it lands at
	// x = (0+6+6+0)/4 = 3 — half of the two units the edge moved.
	wantPoint(t, ch.BiomeSectors[0].Points[0], mgl32.Vec2{3, 2}, "paint inside the sector")
	wantPoint(t, ch.BiomeSectors[0].Points[1], mgl32.Vec2{50, 50}, "paint outside the sector")
	if !ch.BiomeSectors[0].HeightDirty || !ch.BiomeSectors[0].GroundDirty {
		t.Fatal("moved paint was not marked for a rebuild")
	}
}

// TestBiomeFollowsWholeSectorMove checks the rigid case: move every corner by
// the same offset and the paint translates by exactly that offset.
func TestBiomeFollowsWholeSectorMove(t *testing.T) {
	restore := saveEditorSelection()
	defer restore()
	ch := anchorTestLevel()

	Editor.SelectedVertices = nil
	for i := 0; i < 4; i++ {
		Editor.SelectedVertices = append(Editor.SelectedVertices,
			vertexSel{chunkIdx: 0, sectorIdx: 0, vertIdx: i, pointIdx: i, isFloor: true})
	}
	setSelectedVertexPoints(func(i int, sel vertexSel, pt mgl32.Vec2) mgl32.Vec2 {
		return pt.Add(mgl32.Vec2{5, -1})
	})

	wantPoint(t, ch.BiomeSectors[0].Points[0], mgl32.Vec2{7, 1}, "paint inside the sector")
	wantPoint(t, ch.BiomeSectors[0].Points[1], mgl32.Vec2{50, 50}, "paint outside the sector")
}

// TestChunkMoveLeavesItsPaintAlone checks moveChunk's whole point: a chunk's
// points — biome paint included, spilled paint too — are chunk-local and never
// baked with its placement, so moving it (changing Origin) touches none of
// them. They ride along automatically through Chunk.ModelMatrix, the same way
// its structural geometry does; there is no separate "carry" step left to do.
func TestChunkMoveLeavesItsPaintAlone(t *testing.T) {
	restore := saveEditorSelection()
	defer restore()
	ch := anchorTestLevel()
	before0, before1 := ch.BiomeSectors[0].Points[0], ch.BiomeSectors[0].Points[1]

	moveChunk(ch, ch.Origin.Add(mgl32.Vec3{5, 2, -3}))

	wantPoint(t, ch.BiomeSectors[0].Points[0], before0, "paint inside the sector")
	wantPoint(t, ch.BiomeSectors[0].Points[1], before1, "paint outside the sector")
	if ch.BiomeSectors[0].HeightDirty || ch.BiomeSectors[0].GroundDirty {
		t.Error("moveChunk touched nothing about the paint's local shape; it should not have marked a rebuild")
	}
	if ch.Origin != (mgl32.Vec3{5, 2, -3}) {
		t.Errorf("ch.Origin = %v, want {5 2 -3}", ch.Origin)
	}
}

// TestPaintOnAnotherSectorIsUntouched checks that editing one sector leaves
// paint over a neighbouring, unedited sector alone.
func TestPaintOnAnotherSectorIsUntouched(t *testing.T) {
	restore := saveEditorSelection()
	defer restore()
	Editor.level = &world.Level{Chunks: []world.Chunk{{
		ID: "c0",
		Points: []mgl32.Vec2{
			{0, 0}, {4, 0}, {4, 4}, {0, 4}, // sector 0
			{10, 0}, {14, 0}, {14, 4}, {10, 4}, // sector 1
		},
		Sectors: []world.Sector{
			{ID: "s0", PointIndices: []int{0, 1, 2, 3}},
			{ID: "s1", PointIndices: []int{4, 5, 6, 7}},
		},
		BiomeSectors: []world.BiomeSector{{ID: "paint", Points: []mgl32.Vec2{{12, 2}, {13, 2}, {12, 3}}}},
	}}}
	ch := &Editor.level.Chunks[0]
	for si := range ch.Sectors {
		_ = world.ReloadSector(ch, &ch.Sectors[si])
	}

	Editor.SelectedVertices = []vertexSel{
		{chunkIdx: 0, sectorIdx: 0, vertIdx: 1, pointIdx: 1, isFloor: true},
	}
	setSelectedVertexPoints(func(i int, sel vertexSel, pt mgl32.Vec2) mgl32.Vec2 {
		return mgl32.Vec2{40, 40}
	})

	wantPoint(t, ch.BiomeSectors[0].Points[0], mgl32.Vec2{12, 2}, "paint over the untouched sector")
}
