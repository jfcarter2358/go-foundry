package main

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// occluderLevel builds a level whose only geometry is one wall quad standing at
// z = -5, spanning ±2 in X and Y. The camera set up by setupPickCamera looks
// straight down -Z through the middle of it.
func occluderLevel() *Level {
	wall := Wall{Visible: true, Geometry: Geometry{Vertices: quadVerts(-5, 2)}}
	return &Level{Chunks: []Chunk{{
		ID:       "c0",
		MinBound: mgl32.Vec3{-3, -5, -11},
		MaxBound: mgl32.Vec3{3, 5, -1},
		Sectors: []Sector{{
			ID:       "s0",
			Walls:    []Wall{wall},
			MinBound: mgl32.Vec2{-3, -11},
			MaxBound: mgl32.Vec2{3, -1},
			Floor:    Floor{Height: -3},
			Ceiling:  Ceiling{Height: 3},
		}},
	}}}
}

// Solid shading's whole contract: a point on the far side of a wall is not
// selectable, and one in front of it is.
func TestSolidShadingHidesOccludedHandles(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = occluderLevel()
	Editor.Wireframe = false
	editorVisReset()

	behind := vertexSel{chunkIdx: 0, sectorIdx: 0, vertIdx: 0, pointIdx: 0, isFloor: true}
	front := vertexSel{chunkIdx: 0, sectorIdx: 0, vertIdx: 1, pointIdx: 1, isFloor: true}

	if editorVertexVisible(behind, mgl32.Vec3{0, 0, -10}) {
		t.Error("a point behind the wall reported visible in solid shading")
	}
	if !editorVertexVisible(front, mgl32.Vec3{0, 0, -3}) {
		t.Error("a point in front of the wall reported hidden")
	}
}

// Wireframe is the opposite contract — nothing occludes anything — and it has
// to hold without the occlusion pass having to be re-run to notice.
func TestWireframeMakesEveryHandleVisible(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = occluderLevel()

	behind := vertexSel{chunkIdx: 0, sectorIdx: 0, vertIdx: 0, pointIdx: 0, isFloor: true}

	Editor.Wireframe = false
	editorVisReset()
	if editorVertexVisible(behind, mgl32.Vec3{0, 0, -10}) {
		t.Fatal("setup wrong: the point should start out occluded")
	}

	editorSetWireframe(true)
	if !editorVertexVisible(behind, mgl32.Vec3{0, 0, -10}) {
		t.Error("wireframe still hid a point behind a wall")
	}
	if editorHandleShowPx() != vertexHandleShowAllPx {
		t.Error("wireframe did not switch handles to show-all")
	}

	editorSetWireframe(false)
	if editorVertexVisible(behind, mgl32.Vec3{0, 0, -10}) {
		t.Error("going back to solid did not restore occlusion")
	}
}

// A vertex sits exactly on the faces that meet at it, so the slack in the
// occlusion test has to keep those faces from hiding their own corner.
func TestSurfaceDoesNotOccludeItsOwnVertex(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = occluderLevel()
	Editor.Wireframe = false
	editorVisReset()

	corner := vertexSel{chunkIdx: 0, sectorIdx: 0, vertIdx: 2, pointIdx: 2, isFloor: true}
	if !editorVertexVisible(corner, mgl32.Vec3{0, 0, -5}) {
		t.Error("a point lying on the wall was occluded by that wall")
	}
}

// An edge only has to have one visible end to be worth offering, which is what
// keeps an edge running out from behind a wall grabbable.
func TestEdgeVisibleWhenEitherEndIs(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = occluderLevel()
	Editor.Wireframe = false
	editorVisReset()

	a := vertexSel{chunkIdx: 0, sectorIdx: 0, vertIdx: 0, pointIdx: 0, isFloor: true}
	b := vertexSel{chunkIdx: 0, sectorIdx: 0, vertIdx: 1, pointIdx: 1, isFloor: true}
	hidden, shown := mgl32.Vec3{0, 0, -10}, mgl32.Vec3{0, 0, -3}

	if !editorEdgeVisible(a, b, hidden, shown) {
		t.Error("an edge with one visible end was dropped")
	}
	c := vertexSel{chunkIdx: 0, sectorIdx: 0, vertIdx: 3, pointIdx: 3, isFloor: true}
	if editorEdgeVisible(a, c, hidden, mgl32.Vec3{1, 1, -12}) {
		t.Error("an edge with both ends behind the wall was offered")
	}
}

// ---------- selection modes ----------

// Point and edge are two views of the same vertices, so switching between them
// keeps the selection; face works in another unit, so crossing to it clears.
func TestSelModeSwitchCarriesPointsButNotFaces(t *testing.T) {
	defer saveEditorSelection()()
	Editor.level = occluderLevel()
	Editor.SelMode = editorSelPoint
	sel := []vertexSel{{chunkIdx: 0, sectorIdx: 0, vertIdx: 0, pointIdx: 0, isFloor: true}}
	Editor.SelectedVertices = sel

	editorSetSelMode(editorSelEdge)
	if len(Editor.SelectedVertices) != 1 {
		t.Errorf("point -> edge dropped the selection: %v", Editor.SelectedVertices)
	}

	editorSetSelMode(editorSelPoint)
	if len(Editor.SelectedVertices) != 1 {
		t.Errorf("edge -> point dropped the selection: %v", Editor.SelectedVertices)
	}

	editorSetSelMode(editorSelFace)
	if len(Editor.SelectedVertices) != 0 {
		t.Errorf("point -> face kept a vertex selection: %v", Editor.SelectedVertices)
	}
}

// Crossing back the other way has to drop what face mode selected, or the panel
// would go on showing a wall the visible handles no longer explain.
func TestSelModeLeavingFaceClearsObjects(t *testing.T) {
	defer saveEditorSelection()()
	Editor.level = occluderLevel()
	Editor.SelMode = editorSelFace
	Editor.ActiveSectorIdx = 0
	Editor.ActiveSurface = "wall"
	Editor.ActiveEdgeKind = "wall"
	Editor.ActiveEdgeIdx = 0
	Editor.SpawnSelected = true

	editorSetSelMode(editorSelPoint)
	if Editor.ActiveSurface != "" || Editor.ActiveEdgeIdx != -1 {
		t.Errorf("face -> point left a surface selected: %q #%d",
			Editor.ActiveSurface, Editor.ActiveEdgeIdx)
	}
	if Editor.SpawnSelected {
		t.Error("face -> point left the spawn point selected")
	}
}

// Face mode is the level editor's original click behaviour and stays the
// default, so an editor that has never touched the modes behaves as before.
func TestFaceIsTheDefaultSelMode(t *testing.T) {
	var fresh EditorState
	if fresh.SelMode != editorSelFace {
		t.Errorf("zero-value SelMode = %v, want Face", fresh.SelMode)
	}
	if fresh.Wireframe {
		t.Error("a fresh editor starts in wireframe")
	}
}

// ---------- click routing ----------

// centreClick is a left-click in the middle of the viewport set up by
// setupPickCamera, so the ray runs straight down -Z.
func centreClick() sdl.MouseButtonEvent {
	mx, my := setupPickCamera()
	return sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: mx, Y: my}
}

// Face mode is the only one that reaches through to the world, and it must go
// on selecting sector faces exactly as the pre-mode viewport did.
func TestFaceModeSelectsTheWall(t *testing.T) {
	defer saveEditorSelection()()
	click := centreClick()
	Editor.level = occluderLevel()
	Editor.SpawnPlacementMode = false
	Editor.SelMode = editorSelFace
	editorVisReset()

	Editor3DHandleClick(click)

	if Editor.ActiveSurface != "wall" || Editor.ActiveEdgeIdx != 0 {
		t.Fatalf("face mode selected %q #%d, want the wall",
			Editor.ActiveSurface, Editor.ActiveEdgeIdx)
	}
	if Editor.PanelFocus != panelFocusWall {
		t.Errorf("panel focus = %q, want %q", Editor.PanelFocus, panelFocusWall)
	}
}

// Point and edge mode own their click outright: nothing under the cursor means
// nothing selected, rather than falling through to the face behind. Without
// that, aiming at a corner and missing would silently select the wall.
func TestPointModeMissDoesNotFallThroughToTheFace(t *testing.T) {
	defer saveEditorSelection()()
	click := centreClick()
	Editor.level = occluderLevel()
	Editor.SpawnPlacementMode = false
	editorVisReset()

	for _, mode := range []editorSelMode{editorSelPoint, editorSelEdge} {
		Editor.SelMode = mode
		Editor.ActiveSurface = "wall"
		Editor.ActiveEdgeIdx = 0
		Editor.SelectedVertices = []vertexSel{{chunkIdx: 0}}

		Editor3DHandleClick(click)

		if Editor.ActiveSurface != "" {
			t.Errorf("%v mode: a miss selected the face behind it (%q)", mode, Editor.ActiveSurface)
		}
		if len(Editor.SelectedVertices) != 0 {
			t.Errorf("%v mode: a miss left %d vertices selected", mode, len(Editor.SelectedVertices))
		}
	}
}

// ---------- water ----------

// waterLevel puts a water plane at z = -4, in front of occluderLevel's wall,
// with the Water-kind biome sector it was derived from.
func waterLevel() *Level {
	lvl := occluderLevel()
	ch := &lvl.Chunks[0]
	ch.BiomeSectors = []BiomeSector{
		{ID: "other", Brush: BiomeBrush{Kind: BiomeGrass}},
		{ID: "lake", Brush: BiomeBrush{Kind: BiomeWater}},
	}
	ch.WaterSurfaces = []WaterSurface{{
		ID:       "lake",
		ChunkID:  ch.ID,
		Position: mgl32.Vec3{0, 0, -4},
		Geometry: Geometry{Vertices: quadVerts(0, 2)},
	}}
	return lvl
}

// Water is still pickable now that it is derived from a biome sector rather
// than placed as a primitive, and it wins over the wall behind it.
func TestWaterPlaneIsPickable(t *testing.T) {
	defer saveEditorSelection()()
	mx, my := setupPickCamera()
	Editor.level = waterLevel()

	hit := pick3D(mx, my)
	if hit.kind != pickWater {
		t.Fatalf("pick3D found %q at t=%v, want the water plane", hit.kind, hit.t)
	}
	if hit.chunkIdx != 0 || hit.idx != 0 {
		t.Fatalf("water hit = chunk %d surface %d, want 0/0", hit.chunkIdx, hit.idx)
	}
}

// A water plane has nothing to edit of its own, so clicking one has to land on
// the biome sector that produced it — matched by ID, not by index, since the
// two slices are built independently.
func TestClickingWaterSelectsItsBiomeSector(t *testing.T) {
	defer saveEditorSelection()()
	click := centreClick()
	Editor.level = waterLevel()
	Editor.SpawnPlacementMode = false
	Editor.SelMode = editorSelFace
	editorVisReset()

	Editor3DHandleClick(click)

	if Editor.ActiveBiomeSectorIdx != 1 {
		t.Fatalf("selected biome sector %d, want 1 (\"lake\")", Editor.ActiveBiomeSectorIdx)
	}
	if Editor.PanelFocus != panelFocusWater {
		t.Errorf("panel focus = %q, want %q", Editor.PanelFocus, panelFocusWater)
	}
	if Editor.ActiveSurface != "" {
		t.Errorf("clicking water also selected a face: %q", Editor.ActiveSurface)
	}
}

// A surface whose sector has been deleted must resolve to "no sector" rather
// than to whatever happens to sit at that index.
func TestWaterSectorForHandlesAMissingSector(t *testing.T) {
	defer saveEditorSelection()()
	Editor.level = waterLevel()
	Editor.level.Chunks[0].BiomeSectors = nil

	if got := editorWaterSectorFor(0, 0); got != -1 {
		t.Errorf("orphaned water surface resolved to sector %d, want -1", got)
	}
	if got := editorWaterSectorFor(0, 9); got != -1 {
		t.Errorf("out-of-range surface resolved to sector %d, want -1", got)
	}
}

// Water is transparent, so it must never hide a handle underneath it — the
// point of clicking through a lake is to reach the lakebed's corners.
func TestWaterDoesNotOccludeHandles(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = waterLevel()
	// Drop the wall so the water plane is the only thing on the ray.
	Editor.level.Chunks[0].Sectors[0].Walls = nil
	Editor.Wireframe = false
	editorVisReset()

	under := vertexSel{chunkIdx: 0, sectorIdx: 0, vertIdx: 0, pointIdx: 0, isFloor: true}
	if !editorVertexVisible(under, mgl32.Vec3{0, 0, -8}) {
		t.Error("a point under the water plane was reported hidden")
	}
}
