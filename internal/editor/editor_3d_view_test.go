package editor

import (
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// occluderLevel builds a level whose only geometry is one thin box brush
// standing at z = -5, spanning ±2 in X and Y. The camera set up by
// setupPickCamera looks straight down -Z through the middle of it.
func occluderLevel(t *testing.T) *world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	ch := world.Chunk{
		ID: "c0",
		Brushes: []world.Brush{
			world.NewBoxBrush("wall", mgl32.Vec3{0, 0, -5}, mgl32.Vec3{2, 2, 0.1}, "concrete"),
		},
	}
	for bi := range ch.Brushes {
		if err := ch.Brushes[bi].Load(); err != nil {
			t.Fatalf("loading: %v", err)
		}
	}
	ch.RecomputeBounds()
	return &world.Level{Chunks: []world.Chunk{ch}}
}

// aVertex names a corner of occluderLevel's brush. The occlusion cache is keyed
// on the selection, so two tests asking about different world positions have to
// ask about different corners or the second reads the first's answer.
func aVertex(vertIdx int) vertexSel {
	return vertexSel{chunkIdx: 0, brushIdx: 0, vertIdx: vertIdx}
}

// Solid shading's whole contract: a point on the far side of a brush is not
// selectable, and one in front of it is.
func TestSolidShadingHidesOccludedHandles(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = occluderLevel(t)
	Editor.Wireframe = false
	editorVisReset()

	if editorVertexVisible(aVertex(0), mgl32.Vec3{0, 0, -10}) {
		t.Error("a point behind the brush reported visible in solid shading")
	}
	if !editorVertexVisible(aVertex(1), mgl32.Vec3{0, 0, -3}) {
		t.Error("a point in front of the brush reported hidden")
	}
}

// Wireframe is the opposite contract — nothing occludes anything — and it has
// to hold without the occlusion pass having to be re-run to notice.
func TestWireframeMakesEveryHandleVisible(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = occluderLevel(t)

	Editor.Wireframe = false
	editorVisReset()
	if editorVertexVisible(aVertex(0), mgl32.Vec3{0, 0, -10}) {
		t.Fatal("setup wrong: the point should start out occluded")
	}

	editorSetWireframe(true)
	if !editorVertexVisible(aVertex(0), mgl32.Vec3{0, 0, -10}) {
		t.Error("wireframe still hid a point behind a brush")
	}
	if editorHandleShowPx() != vertexHandleShowAllPx {
		t.Error("wireframe did not switch handles to show-all")
	}

	editorSetWireframe(false)
	if editorVertexVisible(aVertex(0), mgl32.Vec3{0, 0, -10}) {
		t.Error("going back to solid did not restore occlusion")
	}
}

// A vertex sits exactly on the faces that meet at it, so the slack in the
// occlusion test has to keep those faces from hiding their own corner.
func TestSurfaceDoesNotOccludeItsOwnVertex(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = occluderLevel(t)
	Editor.Wireframe = false
	editorVisReset()

	// Corner 6 is (-x, +y, +z) — see world.CornerSign — so it is on the brush's
	// NEAR face, lying exactly on the faces that meet there. That is the case
	// the occlusion test's slack exists for. (A corner on the far side is
	// genuinely behind the near face and correctly hidden, which is what the
	// first test in this file checks.)
	corner, ok := vertexSelPos(aVertex(6))
	if !ok {
		t.Fatal("the corner has no position")
	}
	if !editorVertexVisible(aVertex(6), corner) {
		t.Error("a point lying on the brush was occluded by that brush")
	}
}

// An edge only has to have one visible end to be worth offering, which is what
// keeps an edge running out from behind a wall grabbable.
func TestEdgeVisibleWhenEitherEndIs(t *testing.T) {
	defer saveEditorSelection()()
	setupPickCamera()
	Editor.level = occluderLevel(t)
	Editor.Wireframe = false
	editorVisReset()

	hidden, shown := mgl32.Vec3{0, 0, -10}, mgl32.Vec3{0, 0, -3}
	if !editorEdgeVisible(aVertex(0), aVertex(1), hidden, shown) {
		t.Error("an edge with one visible end was dropped")
	}
	if editorEdgeVisible(aVertex(0), aVertex(3), hidden, mgl32.Vec3{1, 1, -12}) {
		t.Error("an edge with both ends behind the brush was offered")
	}
}

// ---------- selection modes ----------

// Point and edge are two views of the same vertices, so switching between them
// keeps the selection; face works in another unit, so crossing to it clears.
func TestSelModeSwitchCarriesPointsButNotFaces(t *testing.T) {
	defer saveEditorSelection()()
	Editor.level = occluderLevel(t)
	Editor.SelMode = editorSelPoint
	Editor.SelectedVertices = []vertexSel{aVertex(0)}

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
// would go on showing a face the visible handles no longer explain.
func TestSelModeLeavingFaceClearsObjects(t *testing.T) {
	defer saveEditorSelection()()
	Editor.level = occluderLevel(t)
	Editor.SelMode = editorSelFace
	Editor.ActiveChunkIdx = 0
	setFaceSelection([]faceSel{brushFaceSel(0, 0, boxFaceNegZ)})
	Editor.SpawnSelected = true

	editorSetSelMode(editorSelPoint)
	if len(Editor.SelectedFaces) != 0 {
		t.Errorf("face -> point left %d faces selected", len(Editor.SelectedFaces))
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

// Face mode is the only one that reaches through to the world, and it selects
// the brush face the ray meets.
func TestFaceModeSelectsTheBrushFace(t *testing.T) {
	defer saveEditorSelection()()
	click := centreClick()
	Editor.level = occluderLevel(t)
	Editor.SpawnPlacementMode = false
	Editor.SelMode = editorSelFace
	editorVisReset()

	Editor3DHandleClick(click)

	if len(Editor.SelectedFaces) != 1 {
		t.Fatalf("face mode selected %d faces, want 1", len(Editor.SelectedFaces))
	}
	// Looking down -Z at a box centred at z=-5: the near side is +Z.
	if got := Editor.SelectedFaces[0]; got.brushIdx != 0 || got.faceIdx != boxFacePosZ {
		t.Fatalf("selected %+v, want brush 0's +Z face (%d)", got, boxFacePosZ)
	}
	if Editor.PanelFocus != panelFocusFace {
		t.Errorf("panel focus = %q, want %q", Editor.PanelFocus, panelFocusFace)
	}
}

// Point and edge mode own their click outright: nothing under the cursor means
// nothing selected, rather than falling through to the face behind. Without
// that, aiming at a corner and missing would silently select the wall.
func TestPointModeMissDoesNotFallThroughToTheFace(t *testing.T) {
	defer saveEditorSelection()()
	click := centreClick()
	Editor.level = occluderLevel(t)
	Editor.SpawnPlacementMode = false
	Editor.ActiveChunkIdx = 0
	editorVisReset()

	for _, mode := range []editorSelMode{editorSelPoint, editorSelEdge} {
		Editor.SelMode = mode
		setFaceSelection([]faceSel{brushFaceSel(0, 0, boxFacePosZ)})
		Editor.SelectedVertices = []vertexSel{aVertex(0)}

		Editor3DHandleClick(click)

		if len(Editor.SelectedFaces) != 0 {
			t.Errorf("%v mode: a miss selected the face behind it (%+v)", mode, Editor.SelectedFaces)
		}
		if len(Editor.SelectedVertices) != 0 {
			t.Errorf("%v mode: a miss left %d vertices selected", mode, len(Editor.SelectedVertices))
		}
	}
}
