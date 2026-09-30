package editor

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// terrainTestGrid is 20x20 cells of 1 unit with its first corner at the
// origin, flat, with textures on its first two layers.
func terrainTestGrid() *world.Terrain {
	t := world.NewTerrain(mgl32.Vec3{}, 1, 20, 20)
	t.Layers = [world.TerrainLayerCount]string{"concrete", "default"}
	return t
}

func brushAt(mode terrainBrushMode) terrainBrush {
	return terrainBrush{mode: mode, cx: 10, cz: 10, radius: 3, strength: 1, hardness: 0.3, dt: 0.1}
}

func TestRaisingLiftsInsideTheRadiusAndNothingOutside(t *testing.T) {
	tr := terrainTestGrid()
	changed, x0, z0, x1, z1 := applyTerrainBrush(tr, brushAt(terrainRaise))
	if !changed {
		t.Fatal("raise changed nothing")
	}
	if h := tr.Height(10, 10); h <= 0 {
		t.Fatalf("the centre is at %v after raising", h)
	}
	if tr.Height(10, 10) <= tr.Height(12, 10) {
		t.Fatal("the centre did not rise more than the edge of the falloff")
	}
	if tr.Height(14, 10) != 0 || tr.Height(10, 6) != 0 {
		t.Fatal("raise reached past its radius")
	}
	if x0 > 7 || z0 > 7 || x1 < 13 || z1 < 13 {
		t.Fatalf("the touched rectangle %d,%d..%d,%d does not cover the radius", x0, z0, x1, z1)
	}
	applyTerrainBrush(tr, brushAt(terrainLower))
	applyTerrainBrush(tr, brushAt(terrainLower))
	if tr.Height(10, 10) >= 0 {
		t.Fatal("lowering twice what was raised once left the centre above 0")
	}
}

func TestSmoothingFlattensASpike(t *testing.T) {
	tr := terrainTestGrid()
	tr.Heights[tr.Index(10, 10)] = 5
	for i := 0; i < 20; i++ {
		applyTerrainBrush(tr, brushAt(terrainSmooth))
	}
	if h := tr.Height(10, 10); h > 1 {
		t.Fatalf("a 5-unit spike smoothed only to %v", h)
	}
	if tr.Height(11, 10) <= 0 {
		t.Fatal("smoothing did not spread the spike to its neighbours")
	}
}

func TestFlattenPullsTowardTheTarget(t *testing.T) {
	tr := terrainTestGrid()
	for i := range tr.Heights {
		tr.Heights[i] = float32(i%3) - 1
	}
	b := brushAt(terrainFlatten)
	b.target = 2
	b.hardness = 1
	for i := 0; i < 40; i++ {
		applyTerrainBrush(tr, b)
	}
	if h := tr.Height(10, 10); h < 1.99 || h > 2.01 {
		t.Fatalf("flatten left the centre at %v, want 2", h)
	}
}

func TestPaintingMovesWeightToTheLayer(t *testing.T) {
	tr := terrainTestGrid()
	b := brushAt(terrainPaint)
	b.layer = 1
	for i := 0; i < 30; i++ {
		applyTerrainBrush(tr, b)
	}
	if w := tr.Weight(10, 10, 1); w != 255 {
		t.Fatalf("the centre is %d/255 layer 2 after painting it", w)
	}
	if tr.Weight(16, 10, 1) != 0 {
		t.Fatal("paint reached past the radius")
	}
}

func TestHoleAndFillCutAndMendWholeCells(t *testing.T) {
	tr := terrainTestGrid()
	b := brushAt(terrainHole)
	b.radius = 1.2
	applyTerrainBrush(tr, b)
	if !tr.IsHole(9, 9) || !tr.IsHole(10, 10) || tr.IsHole(12, 10) {
		t.Fatal("the hole brush did not cut the cells whose centres it covers")
	}
	b.mode = terrainFill
	applyTerrainBrush(tr, b)
	if tr.HasHoles() {
		t.Fatal("fill left a hole behind")
	}
	// Smaller than a cell: still the cell under it.
	b = brushAt(terrainHole)
	b.cx, b.cz, b.radius = 3.9, 4.1, 0.2
	applyTerrainBrush(tr, b)
	if !tr.IsHole(3, 4) {
		t.Fatal("a brush smaller than a cell cut nothing")
	}
}

// ---------- the tool, through the editor's own undo ----------

func terrainEditorLevel() *world.Level {
	ch := world.Chunk{ID: "ground", Scale: 1, Terrain: terrainTestGrid()}
	ch.RecomputeBounds()
	return &world.Level{Chunks: []world.Chunk{ch}}
}

func withTerrainTool(t *testing.T) {
	t.Helper()
	prev := terrainTool
	terrainTool = terrainToolState{Mode: terrainRaise, Radius: 3, Strength: 1, Hardness: 0.3, Active: true}
	t.Cleanup(func() { terrainTool = prev })
}

// hoverAt stands in for editorTerrainFrame's ray: the cursor is over (x, z)
// of chunk 0's ground.
func hoverAt(x, z float32) {
	h, _, _ := Editor.level.Chunks[0].Terrain.HeightAt(x, z)
	terrainTool.hoverValid = true
	terrainTool.hoverChunk = 0
	terrainTool.hoverLocal = mgl32.Vec3{x, h, z}
	terrainTool.hoverPoint = terrainTool.hoverLocal
}

func TestATerrainStrokeIsOneUndoStep(t *testing.T) {
	withEditor3DMenu(t, terrainEditorLevel())
	withSpawnMarkerTexture(t)
	withTerrainTool(t)

	hoverAt(10, 10)
	if !editorTerrainHandleMouseDown() {
		t.Fatal("the armed tool did not take the click")
	}
	for i := 0; i < 10; i++ {
		hoverAt(10+float32(i)*0.3, 10)
		editorTerrainApply(0.05)
	}
	editorEndViewportDrag(1) // sdl.ButtonLeft
	if terrainTool.stroking {
		t.Fatal("releasing the button did not end the stroke")
	}
	if levelUndoStack.len() != 1 {
		t.Fatalf("one stroke left %d undo entries", levelUndoStack.len())
	}
	raised := Editor.level.Chunks[0].Terrain.Height(11, 10)
	if raised <= 0 {
		t.Fatal("the stroke raised nothing")
	}

	EditorUndo()
	if h := Editor.level.Chunks[0].Terrain.Height(11, 10); h != 0 {
		t.Fatalf("undo left the ground at %v", h)
	}
	EditorRedo()
	// Redo restores a snapshot, which holds heights to the millimetre.
	if h := Editor.level.Chunks[0].Terrain.Height(11, 10); h < raised-1e-3 || h > raised+1e-3 {
		t.Fatalf("redo put the ground at %v, want %v", h, raised)
	}
}

func TestAStrokeThatChangesNothingLeavesNoUndoStep(t *testing.T) {
	withEditor3DMenu(t, terrainEditorLevel())
	withSpawnMarkerTexture(t)
	withTerrainTool(t)
	terrainTool.Mode = terrainFill // nothing to fill

	hoverAt(10, 10)
	editorTerrainHandleMouseDown()
	editorTerrainApply(0.05)
	editorTerrainEndStroke()
	if levelUndoStack.len() != 0 {
		t.Fatalf("a stroke that filled no holes left %d undo entries", levelUndoStack.len())
	}
}

func TestPaintingALayerWithNoTextureIsRefused(t *testing.T) {
	withEditor3DMenu(t, terrainEditorLevel())
	withSpawnMarkerTexture(t)
	withTerrainTool(t)
	terrainTool.Mode = terrainPaint
	terrainTool.Layer = 3 // unnamed

	hoverAt(10, 10)
	editorTerrainHandleMouseDown()
	if terrainTool.stroking || levelUndoStack.len() != 0 {
		t.Fatal("started painting a layer that shows nothing")
	}
}

func TestTheTerrainToolYieldsToOtherPlacementTools(t *testing.T) {
	withEditor3DMenu(t, terrainEditorLevel())
	withTerrainTool(t)
	ws().disarmOtherPlacements()
	if terrainTool.Active {
		t.Fatal("arming another placement tool left the terrain tool armed")
	}
}

// The Terrain group's body both ways round, and the tool's block: each draws
// and leaves imgui's stacks even. Drawn directly rather than through the
// panel, whose group starts shut on a chunk with no terrain.
func TestTheTerrainPanelsDraw(t *testing.T) {
	defer saveEditorSelection()()
	withTerrainTool(t)
	lvl := terrainEditorLevel()
	lvl.Chunks = append(lvl.Chunks, world.Chunk{ID: "bare", Scale: 1})
	Editor.level = lvl
	editorSelectChunk(0)
	hoverAt(5, 5)

	frame := func(body func()) {
		io := imgui.CurrentIO()
		io.SetDisplaySize(imgui.Vec2{X: 1280, Y: 800})
		io.SetDeltaTime(1.0 / 60.0)
		imgui.NewFrame()
		imgui.Begin("panel")
		body()
		imgui.End()
		imgui.Render()
	}
	withImgui(func() {
		for i := 0; i < 2; i++ {
			frame(func() {
				editorChunkTerrain(&lvl.Chunks[0])
				editorChunkTerrain(&lvl.Chunks[1])
				editorTerrainToolSection()
			})
		}
		// And the whole panel with the chunk selected, tool armed.
		drawPanelOnce(lvl, panelFocusChunk)
		drawPanelOnce(lvl, panelFocusChunk)
	})
}
