package main

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// withScaleGizmoOnEntity aims the camera at the origin, selects a single entity
// there with the scale tool up, and returns the projection×view matrix.
func withScaleGizmoOnEntity(t *testing.T, e Entity) mgl32.Mat4 {
	t.Helper()
	projView := setupGizmoView()

	restore := saveEditorSelection()
	prevTool := editorGizmoTool
	t.Cleanup(func() {
		editorGizmoTool = prevTool
		editorGizmoMouseUp()
		restore()
	})

	Editor.Mode = Mode3D
	Editor.level = &Level{Entities: []Entity{e}}
	Editor.ActiveEntityIdx = 0
	Editor.ActivePrimitiveIdx = -1
	Editor.GizmoKind = gizmoKindEntity
	Editor.GizmoDragging = false
	Editor.GizmoDragAxis = -1
	Editor.GizmoDragTool = modelToolMove
	Editor.SnapToGrid = false
	editorGizmoTool = modelToolScale
	return projView
}

// scaleHandleScreen returns the pixel a scale handle's tip projects to.
func scaleHandleScreen(t *testing.T, projView mgl32.Mat4, axis int, frac float32) (int32, int32) {
	t.Helper()
	e := editorScaleTarget()
	if e == nil {
		t.Fatal("no scale target")
	}
	origin, ok := translateGizmoOrigin()
	if !ok {
		t.Fatal("no gizmo origin")
	}
	armLen := gizmoArmLength(origin) * modelScaleArmFrac
	p := origin.Add(editorScaleAxes(e)[axis].Mul(armLen * frac))
	sp, ok := projectToScreen(projView, p)
	if !ok {
		t.Fatal("scale handle is off screen")
	}
	return int32(sp.X()), int32(sp.Y())
}

func TestEditorScaleHandlesArePickable(t *testing.T) {
	projView := withScaleGizmoOnEntity(t, Entity{ID: "e0"})

	for axis := 0; axis < 3; axis++ {
		mx, my := scaleHandleScreen(t, projView, axis, 1)
		if got := editorPickScaleHandle(mx, my); got != axis {
			t.Errorf("click on the %d arm's tip picked %d", axis, got)
		}
	}

	// The centre belongs to the uniform handle: all three arms start there, so
	// without it being tested first an arm would always win a click on it.
	sp, _ := projectToScreen(projView, mgl32.Vec3{0, 0, 0})
	if got := editorPickScaleHandle(int32(sp.X()), int32(sp.Y())); got != modelScaleUniformAxis {
		t.Errorf("click at the centre picked %d, want the uniform handle", got)
	}

	if got := editorPickScaleHandle(5, 5); got != -1 {
		t.Errorf("click far from the gizmo picked %d", got)
	}
}

// TestEditorScaleGizmoNeedsAnEntity checks the tool is inert on a selection it
// cannot act on, so the move gizmo stays up rather than the viewport losing its
// handles entirely.
func TestEditorScaleGizmoNeedsAnEntity(t *testing.T) {
	withScaleGizmoOnEntity(t, Entity{ID: "e0"})
	if !editorScaleGizmoActive() {
		t.Fatal("scale tool with an entity selected is not active")
	}

	Editor.ActiveEntityIdx = -1
	if editorScaleGizmoActive() {
		t.Error("scale tool stayed active with nothing selected")
	}
	if editorScaleTarget() != nil {
		t.Error("scale target survived losing the selection")
	}

	Editor.ActiveEntityIdx = 0
	Editor.GizmoKind = gizmoKindChunk
	if editorScaleGizmoActive() {
		t.Error("scale tool acted on a chunk selection")
	}
}

// TestEditorScaleDragScalesOneAxis drives a whole drag and checks the entity
// grew along the grabbed axis only, and that its position did not move: a scale
// is about the entity's own origin.
func TestEditorScaleDragScalesOneAxis(t *testing.T) {
	projView := withScaleGizmoOnEntity(t, Entity{ID: "e0", Scale: mgl32.Vec3{1, 1, 1}})

	origin := mgl32.Vec3{0, 0, 0}
	armLen := gizmoArmLength(origin) * modelScaleArmFrac
	grabAt := armLen * 0.8
	sp, _ := projectToScreen(projView, origin.Add(gizmoAxes[0].Mul(grabAt)))
	down := sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: int32(sp.X()), Y: int32(sp.Y())}
	if !editorGizmoMouseDown(down) {
		t.Fatal("mouse down on the X handle did not start a drag")
	}
	if !editorGizmoDragging() || Editor.GizmoDragTool != modelToolScale {
		t.Fatal("drag did not register as a scale drag")
	}

	// Pull the handle out to twice where it was grabbed.
	to, _ := projectToScreen(projView, origin.Add(gizmoAxes[0].Mul(grabAt*2)))
	editorGizmoMouseMove(int32(to.X()), int32(to.Y()))

	got := Editor.level.Entities[0].Scale
	if math.Abs(float64(got.X()-2)) > 0.05 {
		t.Errorf("X scale = %v, want ~2", got.X())
	}
	if got.Y() != 1 || got.Z() != 1 {
		t.Errorf("scaling X changed the other axes: %v", got)
	}
	if pos := Editor.level.Entities[0].Position; pos != origin {
		t.Errorf("a scale moved the entity to %v", pos)
	}

	editorGizmoMouseUp()
	if editorGizmoDragging() {
		t.Fatal("drag still in progress after mouse up")
	}
	before := Editor.level.Entities[0].Scale
	editorGizmoMouseMove(int32(to.X())+80, int32(to.Y()))
	if Editor.level.Entities[0].Scale != before {
		t.Error("motion after release still scaled the entity")
	}
}

// TestEditorScaleDragIsAbsolute checks a drag is applied from the mouse-down
// snapshot rather than composed per event: returning the cursor to where it
// started must restore the original scale exactly, not leave it compounded.
func TestEditorScaleDragIsAbsolute(t *testing.T) {
	projView := withScaleGizmoOnEntity(t, Entity{ID: "e0", Scale: mgl32.Vec3{2, 2, 2}})

	origin := mgl32.Vec3{0, 0, 0}
	armLen := gizmoArmLength(origin) * modelScaleArmFrac
	grab := origin.Add(gizmoAxes[1].Mul(armLen * 0.8))
	sp, _ := projectToScreen(projView, grab)
	if !editorGizmoMouseDown(sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: int32(sp.X()), Y: int32(sp.Y())}) {
		t.Fatal("mouse down on the Y handle did not start a drag")
	}

	for i := 0; i < 5; i++ {
		out, _ := projectToScreen(projView, origin.Add(gizmoAxes[1].Mul(armLen*1.6)))
		editorGizmoMouseMove(int32(out.X()), int32(out.Y()))
	}
	editorGizmoMouseMove(int32(sp.X()), int32(sp.Y()))

	got := Editor.level.Entities[0].Scale
	if math.Abs(float64(got.Y()-2)) > 0.02 {
		t.Errorf("Y scale back at the grab point = %v, want the original 2", got.Y())
	}
}

// TestEditorScaleClampsAboveZero checks a drag through the centre stops at the
// floor instead of mirroring the model and inverting every face's winding.
func TestEditorScaleClampsAboveZero(t *testing.T) {
	projView := withScaleGizmoOnEntity(t, Entity{ID: "e0", Scale: mgl32.Vec3{1, 1, 1}})

	origin := mgl32.Vec3{0, 0, 0}
	armLen := gizmoArmLength(origin) * modelScaleArmFrac
	sp, _ := projectToScreen(projView, origin.Add(gizmoAxes[0].Mul(armLen*0.8)))
	if !editorGizmoMouseDown(sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: int32(sp.X()), Y: int32(sp.Y())}) {
		t.Fatal("mouse down on the X handle did not start a drag")
	}

	// Well past the centre and out the far side.
	to, _ := projectToScreen(projView, origin.Add(gizmoAxes[0].Mul(-armLen*3)))
	editorGizmoMouseMove(int32(to.X()), int32(to.Y()))

	if x := Editor.level.Entities[0].Scale.X(); x < modelScaleMin || x > modelScaleMin*1.001 {
		t.Errorf("X scale dragged through zero to %v, want the %v floor", x, modelScaleMin)
	}
}

// TestEditorScaleUniformHandleScalesEverything checks the centre handle drives
// all three axes together.
func TestEditorScaleUniformHandleScalesEverything(t *testing.T) {
	projView := withScaleGizmoOnEntity(t, Entity{ID: "e0", Scale: mgl32.Vec3{1, 1, 1}})

	sp, _ := projectToScreen(projView, mgl32.Vec3{0, 0, 0})
	// A few pixels off the centre, so the reference is not ~0 and the grab is
	// something a factor can be a ratio of.
	down := sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: int32(sp.X()) + 6, Y: int32(sp.Y())}
	if !editorGizmoMouseDown(down) {
		t.Fatal("mouse down on the uniform handle did not start a drag")
	}
	if Editor.GizmoDragAxis != modelScaleUniformAxis {
		t.Fatalf("grabbed handle = %d, want the uniform one", Editor.GizmoDragAxis)
	}

	editorGizmoMouseMove(int32(sp.X())+18, int32(sp.Y()))
	got := Editor.level.Entities[0].Scale
	if math.Abs(float64(got.X()-3)) > 0.05 {
		t.Errorf("uniform scale = %v, want ~3 on every axis", got)
	}
	if got.X() != got.Y() || got.Y() != got.Z() {
		t.Errorf("uniform drag produced unequal axes: %v", got)
	}
}

// TestEditorScaleUsesTheEntitysOwnAxes checks the handles follow a rotated
// entity. Entity.ModelMatrix scales before it rotates, so component 0 of Scale
// stretches the model along its own X wherever that ends up pointing — handles
// on the world axes would stretch it in a direction they do not point.
func TestEditorScaleUsesTheEntitysOwnAxes(t *testing.T) {
	// A quarter turn about Y puts the model's +X along world -Z.
	projView := withScaleGizmoOnEntity(t, Entity{
		ID:       "e0",
		Rotation: mgl32.Vec3{0, math.Pi / 2, 0},
		Scale:    mgl32.Vec3{1, 1, 1},
	})

	axes := editorScaleAxes(&Editor.level.Entities[0])
	if d := axes[0].Sub(mgl32.Vec3{0, 0, -1}).Len(); d > 1e-4 {
		t.Fatalf("local X points %v, want world -Z", axes[0])
	}

	// The handle is where the model's X now is, not where world X is.
	mx, my := scaleHandleScreen(t, projView, 0, 1)
	if got := editorPickScaleHandle(mx, my); got != 0 {
		t.Fatalf("click on the rotated X handle picked %d", got)
	}

	origin := mgl32.Vec3{0, 0, 0}
	armLen := gizmoArmLength(origin) * modelScaleArmFrac
	sp, _ := projectToScreen(projView, origin.Add(axes[0].Mul(armLen*0.8)))
	if !editorGizmoMouseDown(sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: int32(sp.X()), Y: int32(sp.Y())}) {
		t.Fatal("mouse down on the rotated X handle did not start a drag")
	}
	to, _ := projectToScreen(projView, origin.Add(axes[0].Mul(armLen*1.6)))
	editorGizmoMouseMove(int32(to.X()), int32(to.Y()))

	got := Editor.level.Entities[0].Scale
	if math.Abs(float64(got.X()-2)) > 0.05 {
		t.Errorf("X scale = %v, want ~2", got.X())
	}
	if got.Y() != 1 || got.Z() != 1 {
		t.Errorf("dragging the rotated X handle changed other axes: %v", got)
	}
}

// TestEditorScaleSnapsToTenths checks the drag borrows the model editor's scale
// quantum rather than the grid step, which is a distance and means nothing for
// a ratio.
func TestEditorScaleSnapsToTenths(t *testing.T) {
	projView := withScaleGizmoOnEntity(t, Entity{ID: "e0", Scale: mgl32.Vec3{1, 1, 1}})
	withGridSnap(t, true, 0.25)

	origin := mgl32.Vec3{0, 0, 0}
	armLen := gizmoArmLength(origin) * modelScaleArmFrac
	grabAt := armLen * 0.8
	sp, _ := projectToScreen(projView, origin.Add(gizmoAxes[0].Mul(grabAt)))
	if !editorGizmoMouseDown(sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: int32(sp.X()), Y: int32(sp.Y())}) {
		t.Fatal("mouse down on the X handle did not start a drag")
	}
	// Somewhere between stops: 1.63× lands on 1.6, not on a multiple of 0.25.
	to, _ := projectToScreen(projView, origin.Add(gizmoAxes[0].Mul(grabAt*1.63)))
	editorGizmoMouseMove(int32(to.X()), int32(to.Y()))

	x := Editor.level.Entities[0].Scale.X()
	if math.Abs(float64(x-1.6)) > 1e-3 {
		t.Errorf("snapped X scale = %v, want 1.6", x)
	}
}

// TestEditorScaleTakesOneUndoEntryPerDrag checks the whole drag is one step
// back, not one per motion event.
func TestEditorScaleTakesOneUndoEntryPerDrag(t *testing.T) {
	projView := withScaleGizmoOnEntity(t, Entity{ID: "e0", Scale: mgl32.Vec3{1, 1, 1}})
	prevUndo, prevRedo := levelUndoStack, levelRedoStack
	t.Cleanup(func() { levelUndoStack, levelRedoStack = prevUndo, prevRedo })
	levelUndoStack.clear()
	levelRedoStack.clear()

	origin := mgl32.Vec3{0, 0, 0}
	armLen := gizmoArmLength(origin) * modelScaleArmFrac
	sp, _ := projectToScreen(projView, origin.Add(gizmoAxes[0].Mul(armLen*0.8)))
	if !editorGizmoMouseDown(sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: int32(sp.X()), Y: int32(sp.Y())}) {
		t.Fatal("mouse down on the X handle did not start a drag")
	}
	for i := 1; i <= 4; i++ {
		to, _ := projectToScreen(projView, origin.Add(gizmoAxes[0].Mul(armLen*(0.8+float32(i)*0.2))))
		editorGizmoMouseMove(int32(to.X()), int32(to.Y()))
	}
	editorGizmoMouseUp()

	if n := levelUndoStack.len(); n != 1 {
		t.Errorf("a single drag pushed %d undo entries, want 1", n)
	}
}

// TestEditorGizmoToolSwitchCancelsADrag checks a half-finished move cannot be
// finished as a scale.
func TestEditorGizmoToolSwitchCancelsADrag(t *testing.T) {
	projView := withScaleGizmoOnEntity(t, Entity{ID: "e0", Scale: mgl32.Vec3{1, 1, 1}})

	origin := mgl32.Vec3{0, 0, 0}
	armLen := gizmoArmLength(origin) * modelScaleArmFrac
	sp, _ := projectToScreen(projView, origin.Add(gizmoAxes[0].Mul(armLen*0.8)))
	if !editorGizmoMouseDown(sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: int32(sp.X()), Y: int32(sp.Y())}) {
		t.Fatal("mouse down on the X handle did not start a drag")
	}

	editorSetGizmoTool(modelToolMove)
	if editorGizmoDragging() {
		t.Error("switching tools left the scale drag running")
	}
	if Editor.GizmoDragTool != modelToolMove {
		t.Error("the cancelled drag still claims the scale tool")
	}

	before := Editor.level.Entities[0].Scale
	editorGizmoMouseMove(int32(sp.X())+60, int32(sp.Y()))
	if Editor.level.Entities[0].Scale != before {
		t.Error("motion after the tool switch still scaled the entity")
	}
}

// TestEditorGizmoFallsBackToMoveWithoutAnEntity checks the tool can be left
// armed: with the scale tool up but a selection it cannot act on, clicks go to
// the move gizmo that is actually on screen rather than being swallowed.
func TestEditorGizmoFallsBackToMoveWithoutAnEntity(t *testing.T) {
	projView := withScaleGizmoOnEntity(t, Entity{ID: "e0", Scale: mgl32.Vec3{1, 1, 1}})

	Editor.level.Chunks = []Chunk{{ID: "c0", Primitives: []Primitive{{ID: "p0"}}}}
	Editor.ActiveChunkIdx = 0
	Editor.ActiveEntityIdx = -1
	Editor.ActivePrimitiveIdx = 0
	Editor.GizmoKind = gizmoKindPrimitive
	if editorScaleGizmoActive() {
		t.Fatal("the scale tool claimed a primitive selection")
	}

	// The move arm's tip — where the gizmo that is drawn has its grab box.
	origin := mgl32.Vec3{0, 0, 0}
	sp, _ := projectToScreen(projView, origin.Add(gizmoAxes[1].Mul(gizmoArmLength(origin))))
	if !editorGizmoMouseDown(sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: int32(sp.X()), Y: int32(sp.Y())}) {
		t.Fatal("a click on the move arm was not consumed")
	}
	if Editor.GizmoDragTool != modelToolMove {
		t.Errorf("the drag claims tool %v, want the move gizmo", Editor.GizmoDragTool)
	}
	editorGizmoMouseUp()
}
