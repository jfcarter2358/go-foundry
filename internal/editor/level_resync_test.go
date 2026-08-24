package editor

import (
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// biomeTestLevel builds a chunk with one 4×4 sector, a biome painted inside it,
// and a second biome far away. Floor and ceiling are left disabled so
// Sector.Load stays off the GPU — the marking under test reads the sector's
// bounds, not its uploaded mesh.
func biomeTestLevel() *world.Chunk {
	Editor.level = &world.Level{Chunks: []world.Chunk{{
		ID:      "c0",
		Points:  []mgl32.Vec2{{0, 0}, {4, 0}, {4, 4}, {0, 4}},
		Sectors: []world.Sector{{ID: "s0", PointIndices: []int{0, 1, 2, 3}}},
		BiomeSectors: []world.BiomeSector{
			{ID: "under", Points: []mgl32.Vec2{{1, 1}, {3, 1}, {3, 3}, {1, 3}}},
			{ID: "elsewhere", Points: []mgl32.Vec2{{100, 100}, {104, 100}, {104, 104}}},
		},
	}}}
	ch := &Editor.level.Chunks[0]
	// Bake once, then declare everything current, as it would be after a frame.
	_ = world.ReloadSector(ch, &ch.Sectors[0])
	for i := range ch.BiomeSectors {
		ch.BiomeSectors[i].HeightDirty = false
		ch.BiomeSectors[i].GroundDirty = false
	}
	return ch
}

// biomeDirty reports the two rebuild flags of the named biome sector.
func biomeDirty(ch *world.Chunk, id string) (height, ground bool) {
	for i := range ch.BiomeSectors {
		if ch.BiomeSectors[i].ID == id {
			return ch.BiomeSectors[i].HeightDirty, ch.BiomeSectors[i].GroundDirty
		}
	}
	return false, false
}

// TestVertexHeightEditRefreshesBiomes covers the edge/vertex drag that started
// this: raising a floor vertex has to invalidate the biomes draped over that
// floor, or they keep the flat shape they were baked with.
func TestVertexHeightEditRefreshesBiomes(t *testing.T) {
	restore := saveEditorSelection()
	defer restore()
	ch := biomeTestLevel()

	// The Y-drag path: both endpoints of an edge, raised by one unit.
	Editor.SelectedVertices = []vertexSel{
		{chunkIdx: 0, sectorIdx: 0, vertIdx: 0, pointIdx: 0, isFloor: true},
		{chunkIdx: 0, sectorIdx: 0, vertIdx: 1, pointIdx: 1, isFloor: true},
	}
	setSelectedVertexOffsets(func(i int, sel vertexSel, cur float32) float32 { return cur + 1 })

	if h, g := biomeDirty(ch, "under"); !h || !g {
		t.Fatalf("biome over the raised floor: HeightDirty=%v GroundDirty=%v, want both set", h, g)
	}
	if h, g := biomeDirty(ch, "elsewhere"); h || g {
		t.Fatalf("biome nowhere near the edit was invalidated: HeightDirty=%v GroundDirty=%v", h, g)
	}
}

// TestVertexMoveRefreshesBiomes is the same for an X/Z drag, which reaches the
// sector through reloadSectorsUsingPoint instead.
func TestVertexMoveRefreshesBiomes(t *testing.T) {
	restore := saveEditorSelection()
	defer restore()
	ch := biomeTestLevel()

	Editor.SelectedVertices = []vertexSel{
		{chunkIdx: 0, sectorIdx: 0, vertIdx: 0, pointIdx: 0, isFloor: true},
	}
	setSelectedVertexPoints(func(i int, sel vertexSel, pt mgl32.Vec2) mgl32.Vec2 {
		return mgl32.Vec2{pt.X() - 1, pt.Y() - 1}
	})

	if h, g := biomeDirty(ch, "under"); !h || !g {
		t.Fatalf("biome over the moved floor: HeightDirty=%v GroundDirty=%v, want both set", h, g)
	}
	if h, g := biomeDirty(ch, "elsewhere"); h || g {
		t.Fatalf("biome nowhere near the edit was invalidated: HeightDirty=%v GroundDirty=%v", h, g)
	}
}

// TestReloadSectorMarksVacatedGround checks the footprint the sector *left*, not
// just the one it moved to: a floor dragged out from under a biome changes what
// that biome drapes over just as much as one dragged onto it.
func TestReloadSectorMarksVacatedGround(t *testing.T) {
	restore := saveEditorSelection()
	defer restore()
	ch := biomeTestLevel()

	// Move the whole sector far away, well clear of the biome it covered.
	for i := range ch.Points {
		ch.Points[i] = ch.Points[i].Add(mgl32.Vec2{50, 50})
	}
	_ = world.ReloadSector(ch, &ch.Sectors[0])

	if h, g := biomeDirty(ch, "under"); !h || !g {
		t.Fatalf("biome the floor moved away from: HeightDirty=%v GroundDirty=%v, want both set", h, g)
	}
}

// TestSectorRemovalRefreshesBiomes covers the other way a floor stops being
// under a biome: the sector is deleted outright.
func TestSectorRemovalRefreshesBiomes(t *testing.T) {
	restore := saveEditorSelection()
	defer restore()
	ch := biomeTestLevel()

	removeSectorKeepPoints(ch, 0)

	if h, g := biomeDirty(ch, "under"); !h || !g {
		t.Fatalf("biome over the deleted sector: HeightDirty=%v GroundDirty=%v, want both set", h, g)
	}
	if h, g := biomeDirty(ch, "elsewhere"); h || g {
		t.Fatalf("biome nowhere near the deleted sector was invalidated: HeightDirty=%v GroundDirty=%v", h, g)
	}
}
