package editor

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// terrainTestGrid is 20x8x20 blocks of 1 unit with its first corner at the
// origin, filled to height 4 (its top at Y 4), with textures on its first two
// materials and a meadow in its palette.
func terrainTestGrid() *world.Terrain {
	t := world.NewTerrain(mgl32.Vec3{}, 1, 20, 8, 20)
	t.Layers = [world.TerrainLayerCount]string{"concrete", "default"}
	t.Biomes = []world.TerrainBiome{{Preset: "meadow"}}
	t.Generate(world.TerrainFill{Height: 4, Top: 1, Under: 1, Deep: 1})
	return t
}

// topEdit is an edit aimed at the top face of the column at (x, z).
func topEdit(t *world.Terrain, mode terrainEditMode, x, z, radius int) terrainEdit {
	return terrainEdit{mode: mode, face: t.FaceKey(x, 3, z, world.FaceYPos),
		point: mgl32.Vec3{float32(x) + 0.5, 4, float32(z) + 0.5}, radius: radius, material: 2, biome: 1}
}

func TestMiningAndPlacingTakeAndAddOneBlock(t *testing.T) {
	tr := terrainTestGrid()
	if !applyTerrainEdit(tr, topEdit(tr, terrainMine, 5, 5, 0)) || tr.Solid(5, 3, 5) {
		t.Fatal("mining left the block")
	}
	if !tr.Solid(6, 3, 5) || !tr.Solid(5, 2, 5) {
		t.Fatal("mining one block took its neighbours")
	}
	if !applyTerrainEdit(tr, topEdit(tr, terrainPlace, 8, 8, 0)) || tr.Block(8, 4, 8) != 2 {
		t.Fatal("placing did not put a block of the material on top of the face")
	}
	// Placing against a side face fills the cell beside it.
	e := terrainEdit{mode: terrainPlace, face: tr.FaceKey(8, 4, 8, world.FaceXPos), material: 3}
	if !applyTerrainEdit(tr, e) || tr.Block(9, 4, 8) != 3 {
		t.Fatal("placing against a side did not fill the cell beside it")
	}
}

func TestAReachMinesASphere(t *testing.T) {
	tr := terrainTestGrid()
	applyTerrainEdit(tr, topEdit(tr, terrainMine, 10, 10, 2))
	if tr.Solid(10, 3, 10) || tr.Solid(12, 3, 10) || tr.Solid(10, 1, 10) {
		t.Fatal("a reach-2 mine left blocks within 2 of the target")
	}
	if !tr.Solid(12, 1, 12) {
		t.Fatal("a reach-2 mine took a corner block outside its sphere")
	}
	if !tr.Solid(13, 3, 10) {
		t.Fatal("a reach-2 mine took a block 3 away")
	}
}

func TestRetexturingOnlyChangesSolidBlocks(t *testing.T) {
	tr := terrainTestGrid()
	applyTerrainEdit(tr, topEdit(tr, terrainRetexture, 10, 10, 1))
	if tr.Block(10, 3, 10) != 2 || tr.Block(11, 3, 10) != 2 {
		t.Fatal("re-texturing missed blocks in reach")
	}
	if tr.Solid(10, 4, 10) {
		t.Fatal("re-texturing filled air")
	}
}

func TestPaintingAndErasingABiome(t *testing.T) {
	tr := terrainTestGrid()
	if !applyTerrainEdit(tr, topEdit(tr, terrainPaint, 10, 10, 2)) {
		t.Fatal("painting changed nothing")
	}
	if tr.PaintOf(tr.FaceKey(10, 3, 10, world.FaceYPos)) != 1 || tr.PaintOf(tr.FaceKey(12, 3, 10, world.FaceYPos)) != 1 {
		t.Fatal("faces within reach were not painted")
	}
	if tr.PaintOf(tr.FaceKey(14, 3, 10, world.FaceYPos)) != 0 {
		t.Fatal("paint reached past its reach")
	}
	applyTerrainEdit(tr, topEdit(tr, terrainErase, 10, 10, 2))
	if len(tr.Paint) != 0 {
		t.Fatalf("erasing the same reach left %d faces painted", len(tr.Paint))
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
	terrainTool = terrainToolState{Mode: terrainMine, Material: 2, Biome: 1, Active: true}
	t.Cleanup(func() { terrainTool = prev })
}

// hoverAt stands in for editorTerrainFrame's ray: the cursor is over (x, z)
// of chunk 0's ground, looking straight down.
func hoverAt(x, z float32) {
	tr := Editor.level.Chunks[0].Terrain
	h, ok := tr.Raycast(mgl32.Vec3{x, 50, z}, mgl32.Vec3{0, -1, 0}, 100)
	terrainTool.hoverValid = ok
	terrainTool.hoverChunk = 0
	terrainTool.hoverFace = h.Face
	terrainTool.hoverLocal = mgl32.Vec3{x, 50 - h.T, z}
	terrainTool.hoverPoint = terrainTool.hoverLocal
}

func TestATerrainStrokeIsOneUndoStep(t *testing.T) {
	withEditor3DMenu(t, terrainEditorLevel())
	withSpawnMarkerTexture(t)
	withTerrainTool(t)

	hoverAt(10.5, 10.5)
	if !editorTerrainHandleMouseDown() {
		t.Fatal("the armed tool did not take the click")
	}
	// Held: the repeat digs further down the same column.
	for i := 0; i < 3; i++ {
		editorTerrainFrameStep(terrainRepeatDelay + 0.01)
	}
	editorEndViewportDrag(1) // sdl.ButtonLeft
	if terrainTool.stroking {
		t.Fatal("releasing the button did not end the stroke")
	}
	if levelUndoStack.len() != 1 {
		t.Fatalf("one stroke left %d undo entries", levelUndoStack.len())
	}
	tr := Editor.level.Chunks[0].Terrain
	if tr.Solid(10, 3, 10) || tr.Solid(10, 2, 10) {
		t.Fatal("holding the button did not keep digging")
	}

	EditorUndo()
	if !Editor.level.Chunks[0].Terrain.Solid(10, 3, 10) {
		t.Fatal("undo did not put the block back")
	}
	EditorRedo()
	if Editor.level.Chunks[0].Terrain.Solid(10, 3, 10) {
		t.Fatal("redo did not take it away again")
	}
}

// editorTerrainFrameStep is the part of editorTerrainFrame after the hover
// is found, for tests that set the hover themselves: it re-aims at the same
// column, as the real ray would after the block under it went.
func editorTerrainFrameStep(dt float32) {
	hoverAt(terrainTool.hoverLocal.X(), terrainTool.hoverLocal.Z())
	terrainTool.repeatIn -= dt
	if terrainTool.repeatIn <= 0 {
		terrainTool.repeatIn = terrainRepeatEvery
		editorTerrainApply()
	}
}

func TestAStrokeThatChangesNothingLeavesNoUndoStep(t *testing.T) {
	withEditor3DMenu(t, terrainEditorLevel())
	withSpawnMarkerTexture(t)
	withTerrainTool(t)
	terrainTool.Mode = terrainRetexture
	terrainTool.Material = 1 // what the ground already is

	hoverAt(10.5, 10.5)
	editorTerrainHandleMouseDown()
	editorTerrainEndStroke()
	if levelUndoStack.len() != 0 {
		t.Fatalf("a stroke that changed nothing left %d undo entries", levelUndoStack.len())
	}
}

func TestPaintingABiomeTheTerrainLacksIsRefused(t *testing.T) {
	withEditor3DMenu(t, terrainEditorLevel())
	withSpawnMarkerTexture(t)
	withTerrainTool(t)
	terrainTool.Mode = terrainPaint
	terrainTool.Biome = 4

	hoverAt(10.5, 10.5)
	editorTerrainHandleMouseDown()
	if terrainTool.stroking || levelUndoStack.len() != 0 {
		t.Fatal("started painting a biome that is not in the palette")
	}
}

func TestSwitchingToSmoothIsOneUndoStep(t *testing.T) {
	withEditor3DMenu(t, terrainEditorLevel())
	withSpawnMarkerTexture(t)
	ch := &Editor.level.Chunks[0]
	editorToggleTerrainSmooth(ch)
	if !Editor.level.Chunks[0].Terrain.Smooth {
		t.Fatal("the toggle did not switch to smooth")
	}
	EditorUndo()
	if Editor.level.Chunks[0].Terrain.Smooth {
		t.Fatal("undo did not switch back to blocks")
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
	hoverAt(5.5, 5.5)

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
		// Volume mode's block, before and after a box is down.
		terrainTool.Mode = terrainVolume
		frame(editorTerrainToolSection)
		editorTerrainVolumeStart(mgl32.Vec3{}, mgl32.Vec3{0, -1, 0})
		frame(editorTerrainToolSection)
		editorTerrainVolumeCancel()
		terrainTool.Mode = terrainMine
		// And the whole panel with the chunk selected, tool armed.
		drawPanelOnce(lvl, panelFocusChunk)
		drawPanelOnce(lvl, panelFocusChunk)
	})
}

func withTerrainVolume(t *testing.T) {
	t.Helper()
	withTerrainTool(t)
	terrainTool.Mode = terrainVolume
	editorTerrainVolumeCancel()
	t.Cleanup(editorTerrainVolumeCancel)
}

// dragVolumeFace drags face d of the box by n blocks, as the cursor would.
func dragVolumeFace(d, n int) {
	a := d / 2
	terrainVol.dragFace = d
	terrainVol.dragUnit = 1
	terrainVol.dragStart = 0
	terrainVol.dragFrom = terrainVol.lo[a]
	if world.TerrainDirs[d][a] > 0 {
		terrainVol.dragFrom = terrainVol.hi[a]
	}
	terrainVolumeDragTo(float32(n))
	editorTerrainVolumeEndDrag()
}

func TestARayEntersABoxThroughTheFaceItMeets(t *testing.T) {
	lo, hi := mgl32.Vec3{0, 0, 0}, mgl32.Vec3{2, 3, 4}
	cases := []struct {
		o, d mgl32.Vec3
		want int
	}{
		{mgl32.Vec3{1, 10, 1}, mgl32.Vec3{0, -1, 0}, world.FaceYPos},
		{mgl32.Vec3{1, -5, 1}, mgl32.Vec3{0, 1, 0}, world.FaceYNeg},
		{mgl32.Vec3{-5, 1, 1}, mgl32.Vec3{1, 0, 0}, world.FaceXNeg},
		{mgl32.Vec3{1, 1, 9}, mgl32.Vec3{0, 0.1, -1}, world.FaceZPos},
		{mgl32.Vec3{5, 10, 1}, mgl32.Vec3{0, -1, 0}, -1}, // misses
		{mgl32.Vec3{1, 1, 1}, mgl32.Vec3{0, -1, 0}, -1},  // starts inside
	}
	for i, c := range cases {
		if got := rayBoxEntryFace(c.o, c.d, lo, hi); got != c.want {
			t.Errorf("case %d: face %d, want %d", i, got, c.want)
		}
	}
}

// A box starts in the cell Place would fill, is sized by dragging its faces,
// and fills as one undo step — growing the grid where it reaches past it
// without moving the ground already there.
func TestAVolumeFillsAndGrowsTheGrid(t *testing.T) {
	withEditor3DMenu(t, terrainEditorLevel())
	withSpawnMarkerTexture(t)
	withTerrainVolume(t)

	hoverAt(18.5, 10.5)
	if !editorTerrainVolumeStart(mgl32.Vec3{}, mgl32.Vec3{0, -1, 0}) {
		t.Fatal("no box was put down")
	}
	if terrainVol.lo != [3]int{18, 4, 10} || terrainVol.hi != terrainVol.lo {
		t.Fatalf("box %v..%v, want the one cell above the column", terrainVol.lo, terrainVol.hi)
	}
	dragVolumeFace(world.FaceXPos, 4)  // to x 22, past the grid's 20
	dragVolumeFace(world.FaceYPos, 2)  // three high
	dragVolumeFace(world.FaceZNeg, 12) // back to z -2, past the grid's 0
	dragVolumeFace(world.FaceYNeg, -9) // pushed in, it stops at the top face
	if terrainVol.lo != [3]int{18, 6, -2} || terrainVol.hi != [3]int{22, 6, 10} {
		t.Fatalf("box %v..%v after dragging", terrainVol.lo, terrainVol.hi)
	}
	dragVolumeFace(world.FaceYNeg, 2) // and out, back down to y 4

	before := Editor.level.Chunks[0].Terrain
	groundWas := before.BlockMin(5, 3, 5)
	editorTerrainVolumeCommit(false)
	if terrainVol.active {
		t.Fatal("the box outlived its fill")
	}
	if levelUndoStack.len() != 1 {
		t.Fatalf("a fill left %d undo entries", levelUndoStack.len())
	}
	tr := Editor.level.Chunks[0].Terrain
	if tr.SizeX != 23 || tr.SizeZ != 22 {
		t.Fatalf("grid %d x %d x %d, want it grown to 23 x _ x 22", tr.SizeX, tr.SizeY, tr.SizeZ)
	}
	// Block coordinates moved 2 along Z; the ground did not move.
	if tr.BlockMin(5, 3, 7).Sub(groundWas).Len() > 1e-5 || !tr.Solid(5, 3, 7) || tr.Solid(5, 4, 7) {
		t.Fatal("growing the grid moved the ground")
	}
	for _, c := range [][3]int{{18, 4, 0}, {22, 6, 12}, {20, 5, 5}} {
		if tr.Block(c[0], c[1], c[2]) != 2 {
			t.Fatalf("block %v = %d, want the material", c, tr.Block(c[0], c[1], c[2]))
		}
	}
	if tr.Solid(17, 4, 5) || tr.Solid(18, 7, 5) {
		t.Fatal("the fill spilled out of the box")
	}

	EditorUndo()
	if tr := Editor.level.Chunks[0].Terrain; tr.SizeX != 20 || tr.Solid(18, 4, 10) {
		t.Fatal("undo did not take the fill back")
	}
}

func TestAVolumeCarvesWithoutGrowing(t *testing.T) {
	withEditor3DMenu(t, terrainEditorLevel())
	withSpawnMarkerTexture(t)
	withTerrainVolume(t)

	hoverAt(10.5, 10.5)
	editorTerrainVolumeStart(mgl32.Vec3{}, mgl32.Vec3{0, -1, 0})
	dragVolumeFace(world.FaceYNeg, 3)  // down to y 1
	dragVolumeFace(world.FaceXPos, 30) // far past the grid
	editorTerrainVolumeCommit(true)
	tr := Editor.level.Chunks[0].Terrain
	if tr.SizeX != 20 {
		t.Fatal("carving grew the grid")
	}
	if tr.Solid(10, 1, 10) || tr.Solid(19, 3, 10) || !tr.Solid(10, 0, 10) || !tr.Solid(9, 2, 10) {
		t.Fatal("the carve did not take exactly the box")
	}

	// A carve of nothing but air is no undo step.
	n := levelUndoStack.len()
	hoverAt(3.5, 3.5)
	editorTerrainVolumeStart(mgl32.Vec3{}, mgl32.Vec3{0, -1, 0})
	editorTerrainVolumeCommit(true)
	if levelUndoStack.len() != n {
		t.Fatal("carving air left an undo step")
	}
}
