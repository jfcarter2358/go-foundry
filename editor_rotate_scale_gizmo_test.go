package main

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// gizmo3DVertexSelection puts the camera on setupGizmoView's vantage and
// selects two corners of a small sector, so the 3D gizmos have a selection with
// a real extent to turn and spread.
func gizmo3DVertexSelection(t *testing.T) mgl32.Mat4 {
	t.Helper()
	projView := setupGizmoView()
	Editor.Mode = Mode3D
	Editor.level = &Level{Chunks: []Chunk{{
		ID:     "c0",
		Points: []mgl32.Vec2{{-1, 0}, {1, 0}, {0, 2}},
		Sectors: []Sector{{
			ID:           "s0",
			PointIndices: []int{0, 1, 2},
			Floor:        Floor{Enabled: true, VertexOffsets: []float32{0, 0, 0}},
			Ceiling:      Ceiling{Enabled: true, Height: 4, VertexOffsets: []float32{0, 0, 0}},
		}},
	}}}
	Editor.ActiveChunkIdx = 0
	Editor.SelectedVertices = []vertexSel{
		{chunkIdx: 0, sectorIdx: 0, vertIdx: 0, pointIdx: 0, isFloor: true},
		{chunkIdx: 0, sectorIdx: 0, vertIdx: 1, pointIdx: 1, isFloor: true},
	}
	Editor.GizmoKind = gizmoKindVertices
	Editor.GizmoTool = editorToolMove
	Editor.GizmoDragging = false
	Editor.GizmoDragAxis = -1
	Editor.SnapToGrid = false
	Editor.SpawnPlacementMode = false
	Editor.ExtrusionMode = false
	return projView
}

// Each ring is grabbed where it is drawn, so the picker walks the same polyline
// the renderer does.
func TestPickRotateRing(t *testing.T) {
	defer saveEditorSelection()()
	projView := gizmo3DVertexSelection(t)
	Editor.GizmoTool = editorToolRotate

	origin, ok := rotateGizmoOrigin()
	if !ok {
		t.Fatal("no rotate pivot for the selection")
	}
	radius := gizmoArmLength(origin)
	for i, axis := range gizmoAxes {
		// A quarter of the way round each ring, well clear of where the three
		// cross each other.
		u, v := modelRingBasis(axis)
		p := modelRingPoint(origin, u, v, radius, math.Pi/4)
		screen, ok := projectToScreen(projView, p)
		if !ok {
			t.Fatalf("ring %d sample did not project", i)
		}
		if got := pickRotateRing(int32(screen.X()), int32(screen.Y())); got < 0 {
			t.Errorf("a click on ring %d grabbed nothing", i)
		}
	}
	if got := pickRotateRing(2, 2); got != -1 {
		t.Errorf("a click far from the gizmo picked ring %d", got)
	}
}

// The scale gizmo's centre handle is tested before its arms, since all three
// arms start there and a click at the middle is zero pixels from each.
func TestPickScaleHandlePrefersTheCentre(t *testing.T) {
	defer saveEditorSelection()()
	projView := gizmo3DVertexSelection(t)
	Editor.GizmoTool = editorToolScale

	origin, ok := scaleGizmoOrigin()
	if !ok {
		t.Fatal("no scale centre for the selection")
	}
	screen, _ := projectToScreen(projView, origin)
	if got := pickScaleHandle(int32(screen.X()), int32(screen.Y())); got != modelScaleUniformAxis {
		t.Errorf("clicking the centre picked %d, want the uniform handle", got)
	}

	armLen := gizmoArmLength(origin) * modelScaleArmFrac
	for axis := 0; axis < 3; axis++ {
		tip, ok := projectToScreen(projView, origin.Add(gizmoAxes[axis].Mul(armLen)))
		if !ok {
			continue
		}
		if got := pickScaleHandle(int32(tip.X()), int32(tip.Y())); got != axis {
			t.Errorf("clicking the axis-%d handle picked %d", axis, got)
		}
	}
}

// Rotate and scale are defined against the selection's own centre, so they have
// nothing to act on when the selection is a single anchor. The spawn marker and
// a whole chunk move and nothing else.
func TestRotateAndScaleNeedAVertexSelection(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmoView()
	Editor.Mode = Mode3D
	Editor.level = &Level{Spawn: &SpawnPoint{Position: mgl32.Vec3{0, 0, 0}}}
	Editor.SelectedVertices = nil
	Editor.SpawnSelected = true
	Editor.GizmoKind = gizmoKindSpawn

	if _, ok := translateGizmoOrigin(); !ok {
		t.Fatal("move offered no gizmo for the selected spawn marker")
	}
	if _, ok := rotateGizmoOrigin(); ok {
		t.Error("rotate offered a pivot for a single anchor")
	}
	if _, ok := scaleGizmoOrigin(); ok {
		t.Error("scale offered a centre for a single anchor")
	}
	if !editorToolActsOnSelection() {
		t.Error("move should always have something to act on")
	}
	Editor.GizmoTool = editorToolRotate
	if editorToolActsOnSelection() {
		t.Error("rotate reported something to act on with no vertices selected")
	}
}

// The click dispatcher has to route to whichever gizmo is on screen, and the
// drag has to remember which tool it belongs to so motion is applied by the
// same maths that started it.
func TestEditorGizmoDispatchesOnTheActiveTool(t *testing.T) {
	defer saveEditorSelection()()
	projView := gizmo3DVertexSelection(t)
	Editor.GizmoTool = editorToolRotate

	origin, _ := rotateGizmoOrigin()
	u, v := modelRingBasis(gizmoAxes[1])
	p := modelRingPoint(origin, u, v, gizmoArmLength(origin), math.Pi/4)
	screen, ok := projectToScreen(projView, p)
	if !ok {
		t.Fatal("the ring sample did not project")
	}
	down := sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: int32(screen.X()), Y: int32(screen.Y())}

	if !editorGizmoMouseDown(down) {
		t.Fatal("a click on a rotate ring fell through to selection")
	}
	if !editorGizmoDragging() {
		t.Fatal("the rotate drag did not start")
	}
	if Editor.GizmoDragTool != editorToolRotate {
		t.Errorf("the drag recorded tool %v, want Rotate", Editor.GizmoDragTool)
	}
	if len(Editor.GizmoDragWorld) != 2 {
		t.Errorf("the drag snapshotted %d positions, want 2", len(Editor.GizmoDragWorld))
	}
	editorGizmoMouseUp()
	if editorGizmoDragging() {
		t.Error("the drag survived mouse up")
	}
	if Editor.GizmoDragWorld != nil {
		t.Error("mouse up left the drag snapshot behind")
	}
}

// Switching tools mid-gesture must end the drag: a half-finished translation
// cannot be completed as a rotation.
func TestSetGizmoToolCancelsADragInProgress(t *testing.T) {
	defer saveEditorSelection()()
	gizmo3DVertexSelection(t)
	Editor.GizmoDragging = true
	Editor.GizmoDragAxis = 1
	Editor.Gizmo2DDragging = true
	Editor.Gizmo2DAxis = 0

	editorSetGizmoTool(editorToolScale)

	if Editor.GizmoTool != editorToolScale {
		t.Errorf("tool = %v, want Scale", Editor.GizmoTool)
	}
	if editorGizmoDragging() || editor2DGizmoDragging() {
		t.Error("a drag survived the tool switch")
	}
}

// A tool that is already active is a no-op, so pressing its key again does not
// interrupt a drag that is running under it.
func TestSetGizmoToolIsANoOpForTheActiveTool(t *testing.T) {
	defer saveEditorSelection()()
	gizmo3DVertexSelection(t)
	Editor.GizmoTool = editorToolMove
	Editor.GizmoDragging = true

	editorSetGizmoTool(editorToolMove)
	if !editorGizmoDragging() {
		t.Error("re-selecting the active tool cancelled its own drag")
	}
}

// Move is the zero value, so an editor that has never touched the tools has the
// gizmo it always had.
func TestMoveIsTheDefaultGizmoTool(t *testing.T) {
	var fresh EditorState
	if fresh.GizmoTool != editorToolMove {
		t.Errorf("zero-value GizmoTool = %v, want Move", fresh.GizmoTool)
	}
}

// The rotate drag measures the cursor by putting it on the plane the ring lies
// in, so the same wrist movement means the same angle wherever on the ellipse
// the ring projects to it lands.
func TestRotationAngleForRay(t *testing.T) {
	origin := mgl32.Vec3{0, 0, 0}
	axis := mgl32.Vec3{0, 1, 0}
	u, _ := modelRingBasis(axis)

	// Straight down onto a point one unit along the ring's own zero direction.
	target := origin.Add(u)
	angle, ok := rotationAngleForRay(target.Add(mgl32.Vec3{0, 5, 0}), mgl32.Vec3{0, -1, 0}, origin, axis)
	if !ok {
		t.Fatal("a ray straight onto the ring plane was rejected")
	}
	if math.Abs(float64(angle)) > 1e-4 {
		t.Errorf("angle at the ring's zero = %v, want 0", angle)
	}

	// A ray running along the plane has no usable intersection.
	if _, ok := rotationAngleForRay(mgl32.Vec3{0, 0, -5}, mgl32.Vec3{0, 0, 1}, origin, axis); ok {
		t.Error("an edge-on ray produced an angle")
	}
	// So does one aimed away from the plane.
	if _, ok := rotationAngleForRay(mgl32.Vec3{0, 5, 0}, mgl32.Vec3{0, 1, 0}, origin, axis); ok {
		t.Error("a ray pointing away from the plane produced an angle")
	}
}

// scaleVector spreads a factor over exactly the axes its handle drives.
func TestScaleVector(t *testing.T) {
	if got := scaleVector(modelScaleUniformAxis, 2); got != (mgl32.Vec3{2, 2, 2}) {
		t.Errorf("uniform = %v, want (2,2,2)", got)
	}
	if got := scaleVector(1, 3); got != (mgl32.Vec3{1, 3, 1}) {
		t.Errorf("Y handle = %v, want (1,3,1)", got)
	}
	if got := scaleVector(-1, 3); got != (mgl32.Vec3{1, 1, 1}) {
		t.Errorf("an out-of-range handle = %v, want no change", got)
	}
}
