package main

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// setupGizmoView aims the camera at the origin from +Z so all three axis arms
// are well separated on screen, and returns the projection×view matrix.
func setupGizmoView() mgl32.Mat4 {
	WinWidth, WinHeight = 800, 600
	Projection = mgl32.Perspective(mgl32.DegToRad(45.0), float32(WinWidth)/float32(WinHeight), 0.1, 1000.0)
	Cam.Position = mgl32.Vec3{4, 3, 8}
	Cam.Matrix = mgl32.LookAtV(Cam.Position, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})
	return Projection.Mul4(Cam.Matrix)
}

func TestAxisParamAtMouse(t *testing.T) {
	setupGizmoView()
	origin := mgl32.Vec3{0, 0, 0}

	// A pixel is chosen by projecting a known point on the X axis; mapping that
	// pixel back must recover the point's distance along the axis.
	projView := Projection.Mul4(Cam.Matrix)
	target := mgl32.Vec3{2, 0, 0}
	screen, ok := projectToScreen(projView, target)
	if !ok {
		t.Fatal("axis point did not project")
	}
	s, ok := axisParamAtMouse(origin, gizmoAxes[0], int32(screen.X()), int32(screen.Y()))
	if !ok {
		t.Fatal("axisParamAtMouse rejected a usable axis")
	}
	if math.Abs(float64(s-2)) > 0.05 {
		t.Fatalf("axis param = %v, want ~2", s)
	}

	// An axis pointing straight at the camera has no usable mapping.
	Cam.Position = mgl32.Vec3{0, 0, 5}
	Cam.Matrix = mgl32.LookAtV(Cam.Position, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})
	if _, ok := axisParamAtMouse(origin, gizmoAxes[2], WinWidth/2, WinHeight/2); ok {
		t.Fatal("edge-on axis should be rejected")
	}
}

func TestPickTranslateAxis(t *testing.T) {
	projView := setupGizmoView()

	restore := saveEditorSelection()
	defer restore()
	Editor.Mode = Mode3D
	Editor.level = &Level{Entities: []Entity{{ID: "e0", Position: mgl32.Vec3{0, 0, 0}}}}
	Editor.ActiveEntityIdx = 0
	Editor.GizmoKind = gizmoKindEntity
	Editor.GizmoDragging = false

	origin, ok := translateGizmoOrigin()
	if !ok {
		t.Fatal("no gizmo origin for the selected entity")
	}
	armLen := gizmoArmLength(origin)

	for axis := 0; axis < 3; axis++ {
		// Aim at the middle of the arm, where only that arm is nearby.
		mid := origin.Add(gizmoAxes[axis].Mul(armLen * 0.75))
		screen, ok := projectToScreen(projView, mid)
		if !ok {
			t.Fatalf("axis %d midpoint did not project", axis)
		}
		if got := pickTranslateAxis(int32(screen.X()), int32(screen.Y())); got != axis {
			t.Fatalf("clicking axis %d picked %d", axis, got)
		}
	}

	// Well off the gizmo: nothing grabbed.
	if got := pickTranslateAxis(5, 5); got != -1 {
		t.Fatalf("click far from the gizmo picked axis %d", got)
	}
}

// TestGizmoDragEntity drives a whole drag — mouse down on an arm, motion, mouse
// up — and checks the entity slid along that axis only.
func TestGizmoDragEntity(t *testing.T) {
	projView := setupGizmoView()

	restore := saveEditorSelection()
	defer restore()
	Editor.Mode = Mode3D
	Editor.level = &Level{Entities: []Entity{{ID: "e0", Position: mgl32.Vec3{0, 0, 0}}}}
	Editor.ActiveEntityIdx = 0
	Editor.GizmoKind = gizmoKindEntity
	Editor.GizmoDragging = false
	Editor.GizmoDragAxis = -1
	Editor.SnapToGrid = false

	origin := mgl32.Vec3{0, 0, 0}
	armLen := gizmoArmLength(origin)
	grab, _ := projectToScreen(projView, origin.Add(gizmoAxes[0].Mul(armLen*0.75)))
	down := sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: int32(grab.X()), Y: int32(grab.Y())}
	if !translateGizmoMouseDown(down) {
		t.Fatal("mouse down on the X arm did not start a drag")
	}
	if !translateGizmoDragging() {
		t.Fatal("drag did not register as in progress")
	}

	// Drag to where world (3,0,0) appears. The move is relative to where the arm
	// was grabbed, so the entity slides by (3 - grab offset) rather than jumping
	// its centre onto the cursor.
	to, _ := projectToScreen(projView, mgl32.Vec3{3, 0, 0})
	translateGizmoMouseMove(int32(to.X()), int32(to.Y()))

	wantX := 3 - armLen*0.75
	pos := Editor.level.Entities[0].Position
	if math.Abs(float64(pos.X()-wantX)) > 0.05 {
		t.Fatalf("entity X = %v, want ~%v", pos.X(), wantX)
	}
	if pos.Y() != 0 || pos.Z() != 0 {
		t.Fatalf("dragging X moved other axes: %v", pos)
	}

	translateGizmoMouseUp()
	if translateGizmoDragging() {
		t.Fatal("drag still in progress after mouse up")
	}

	// Motion after release must not move anything.
	before := Editor.level.Entities[0].Position
	translateGizmoMouseMove(int32(to.X())+50, int32(to.Y()))
	if Editor.level.Entities[0].Position != before {
		t.Fatal("motion after release still moved the entity")
	}
}

func TestGizmoSnapAndAxisMoved(t *testing.T) {
	restore := saveEditorSelection()
	defer restore()

	Editor.SnapToGrid = false
	Editor.StepSize = 0.25
	if got := axisMoved(mgl32.Vec3{1.1, 2, 3}, 0, 0.37); math.Abs(float64(got.X()-1.47)) > 1e-4 {
		t.Fatalf("unsnapped X = %v, want 1.47", got.X())
	}

	Editor.SnapToGrid = true
	got := axisMoved(mgl32.Vec3{1.1, 2, 3}, 1, 0.37)
	if got.Y() != 2.25 {
		t.Fatalf("snapped Y = %v, want 2.25", got.Y())
	}
	if got.X() != 1.1 || got.Z() != 3 {
		t.Fatalf("snapping the Y axis changed other components: %v", got)
	}
}

// TestGizmoKindIsModeScoped checks that a level-mode selection cannot drag
// scenery and vice versa, so stale state from another workspace is inert.
func TestGizmoKindIsModeScoped(t *testing.T) {
	setupGizmoView()

	restore := saveEditorSelection()
	defer restore()
	Editor.level = &Level{Entities: []Entity{{ID: "e0", Position: mgl32.Vec3{0, 0, 0}}}}
	Editor.ActiveEntityIdx = 0
	Editor.GizmoKind = gizmoKindEntity

	Editor.Mode = Mode3D
	if _, ok := translateGizmoOrigin(); !ok {
		t.Fatal("entity gizmo missing in 3D mode")
	}

	Editor.Mode = Mode2D
	if _, ok := translateGizmoOrigin(); ok {
		t.Fatal("gizmo should not exist in 2D mode")
	}

	prevPrims := Editor.SceneryPrimitives
	prevIdx := Editor.SceneryActivePrimIdx
	defer func() {
		Editor.SceneryPrimitives = prevPrims
		Editor.SceneryActivePrimIdx = prevIdx
	}()

	Editor.Mode = ModeScenery
	Editor.SceneryPrimitives = nil
	Editor.SceneryActivePrimIdx = -1
	if _, ok := translateGizmoOrigin(); ok {
		t.Fatal("scenery mode with nothing selected should have no gizmo")
	}

	Editor.SceneryPrimitives = []Primitive{{ID: "p0", Position: mgl32.Vec3{5, 6, 7}}}
	Editor.SceneryActivePrimIdx = 0
	origin, ok := translateGizmoOrigin()
	if !ok || origin != (mgl32.Vec3{5, 6, 7}) {
		t.Fatalf("scenery gizmo origin = %v (ok=%v), want {5 6 7}", origin, ok)
	}
}
