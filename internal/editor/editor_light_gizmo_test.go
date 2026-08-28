package editor

// editor_light_gizmo_test.go — a point light is a movable object.
//
// It has a position and a marker drawn at it, but nothing ever attached the
// translate gizmo to one, so the only way to move a light was the "Move To
// Camera" button — which meant flying to where you wanted the light and losing
// the view you were lighting.

import (
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// lightTestLevel is a level holding one point light at pos, selected.
func lightTestLevel(pos mgl32.Vec3) *world.Level {
	lvl := &world.Level{}
	lvl.Lights.Points = []world.PointLight{{
		ID: "lamp", Position: pos, Color: mgl32.Vec3{1, 1, 1},
		Brightness: 2, Radius: 10,
	}}
	Editor.level = lvl
	Editor.Mode = Mode3D
	return lvl
}

func TestSelectingAPointLightGivesItTheGizmo(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmoView()
	lvl := lightTestLevel(mgl32.Vec3{3, 2, -1})

	editorSelectLight(lightKindPoint, 0)
	if Editor.GizmoKind != gizmoKindLight {
		t.Fatalf("selecting a point light left the gizmo on %q", Editor.GizmoKind)
	}
	origin, ok := translateGizmoOrigin()
	if !ok {
		t.Fatal("the gizmo has no origin for a selected point light")
	}
	if origin != lvl.Lights.Points[0].Position {
		t.Errorf("gizmo sits at %v, want the light at %v", origin, lvl.Lights.Points[0].Position)
	}
	if !captureGizmoDragStart() {
		t.Fatal("a drag on the light's gizmo could not start")
	}
	if Editor.GizmoDragStartPos != lvl.Lights.Points[0].Position {
		t.Errorf("drag snapshot is %v, want the light's position %v",
			Editor.GizmoDragStartPos, lvl.Lights.Points[0].Position)
	}
}

// The lights with no position must not get arms: a global light is everywhere
// and a directional one is a direction, so there is nowhere to draw them.
func TestPositionlessLightsGetNoGizmo(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmoView()
	lvl := lightTestLevel(mgl32.Vec3{3, 2, -1})
	lvl.Lights.Directional = []world.DirectionalLight{{Brightness: 1}}

	editorSelectLight(lightKindPoint, 0)
	for _, kind := range []string{lightKindGlobal, lightKindDir} {
		editorSelectLight(kind, 0)
		if Editor.GizmoKind == gizmoKindLight {
			t.Errorf("selecting the %s light attached the translate gizmo to it", kind)
		}
		if _, ok := translateGizmoOrigin(); ok {
			t.Errorf("the %s light produced a gizmo origin", kind)
		}
	}
}

// Deselecting has to take the gizmo with it, or the arms keep dragging a light
// nothing on screen says is selected.
func TestClearingTheLightSelectionDropsTheGizmo(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmoView()
	lightTestLevel(mgl32.Vec3{0, 0, 0})

	editorSelectLight(lightKindPoint, 0)
	editorClearLightSelection()
	if Editor.GizmoKind == gizmoKindLight {
		t.Error("the gizmo stayed attached to a light that is no longer selected")
	}
	if activeGizmoPointLight() != nil {
		t.Error("activeGizmoPointLight still resolves after the selection was cleared")
	}
}

// A light's marker is clickable in the 3D view, so the gizmo can be reached
// without going through the Objects list.
func TestClickingALightsMarkerPicksIt(t *testing.T) {
	defer saveEditorSelection()()
	projView := setupGizmoView()
	lvl := lightTestLevel(mgl32.Vec3{0, 0, 0})

	screen, ok := projectToScreen(projView, lvl.Lights.Points[0].Position)
	if !ok {
		t.Fatal("the light did not project to the screen")
	}
	hit := pick3D(int32(screen.X()), int32(screen.Y()))
	if hit.kind != pickLight || hit.idx != 0 {
		t.Fatalf("a click on the light's marker picked %q idx %d, want the light", hit.kind, hit.idx)
	}

	// And a click well clear of it does not.
	off := pick3D(int32(screen.X())+200, int32(screen.Y()))
	if off.kind == pickLight {
		t.Error("a click 200px away from the marker still picked the light")
	}
}

// The marker never shrinks below a clickable size, however small the light's
// radius or however far away it is.
func TestALightMarkerStaysClickablyLarge(t *testing.T) {
	setupGizmoView()
	tiny := world.PointLight{Position: mgl32.Vec3{0, 0, 0}, Radius: 0.001}
	r := pointLightMarkerRadius(&tiny)
	if r <= 0 || math.IsNaN(float64(r)) {
		t.Fatalf("marker radius is %v", r)
	}
	if r <= tiny.Radius*0.1 {
		t.Errorf("marker radius %v was not widened past the light's own %v",
			r, tiny.Radius*0.1)
	}
}
