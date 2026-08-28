package editor

// editor_drag_release_test.go — a viewport drag ends wherever it is released.
//
// The press handlers sit behind ImGui's WantCaptureMouse, and so did the
// release handlers. ImGui claims the mouse whenever the cursor is over one of
// its windows, so a camera turn or a gizmo drag that started on the viewport
// and finished over the side panel never got its release: the flag stayed set,
// mouse motion is not gated, and the camera went on following the cursor until
// an unrelated right-click happened to clear it.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

func TestReleasingTheRightButtonEndsACameraTurn(t *testing.T) {
	defer saveEditorSelection()()
	Editor.Mode = Mode3D
	Editor.MouseDragging3D = true

	// Reported as not-consumed: the caller still has to reach
	// editor3DContextReleased, which decides whether the gesture was a turn or
	// a click on the context menu.
	if editorEndViewportDrag(sdl.ButtonRight) {
		t.Error("a right-button release consumed the event; the context menu never sees it")
	}
	if Editor.MouseDragging3D {
		t.Error("the camera turn is still running after the button came up")
	}
}

func TestReleasingTheMiddleButtonEndsATurnAndAPan(t *testing.T) {
	defer saveEditorSelection()()
	Editor.Mode = Mode3D
	Editor.MiddleDragging3D = true
	Editor.PanDragging3D = true

	editorEndViewportDrag(sdl.ButtonMiddle)

	if Editor.MiddleDragging3D || Editor.PanDragging3D {
		t.Errorf("middle drag still running: turn=%v pan=%v",
			Editor.MiddleDragging3D, Editor.PanDragging3D)
	}
}

func TestReleasingTheLeftButtonEndsAGizmoDrag(t *testing.T) {
	defer saveEditorSelection()()
	Editor.Mode = Mode3D
	Editor.GizmoDragging = true
	Editor.GizmoDragAxis = 0

	if !editorEndViewportDrag(sdl.ButtonLeft) {
		t.Error("a left release over a running gizmo drag was not consumed")
	}
	if editorGizmoDragging() {
		t.Error("the gizmo is still being dragged after the button came up")
	}
}

func TestReleasingTheLeftButtonEndsASurfaceDrag(t *testing.T) {
	defer saveEditorSelection()()
	Editor.Mode = Mode3D
	Editor.SurfaceDragDepth = true
	Editor.SurfaceDragPts = []int{1, 2}
	Editor.SurfaceDragStart = []float32{0, 0}

	if !editorEndViewportDrag(sdl.ButtonLeft) {
		t.Error("a left release over a running surface drag was not consumed")
	}
	if Editor.SurfaceDragDepth || Editor.SurfaceDragPts != nil {
		t.Error("the surface drag is still running after the button came up")
	}
}

// Nothing in progress: the release is left alone, so an ordinary click in the
// panel is not swallowed on its way to ImGui.
func TestAReleaseWithNoDragIsNotConsumed(t *testing.T) {
	defer saveEditorSelection()()
	Editor.Mode = Mode3D
	Editor.GizmoDragging = false
	Editor.SurfaceDragDepth = false

	for _, b := range []sdl.Button{sdl.ButtonLeft, sdl.ButtonRight, sdl.ButtonMiddle} {
		if editorEndViewportDrag(b) {
			t.Errorf("button %d consumed a release with no drag in progress", b)
		}
	}
}

// Losing focus mid-gesture takes the button-up with it, so the window layer
// ends every drag at once instead.
func TestLosingFocusEndsEveryDrag(t *testing.T) {
	defer saveEditorSelection()()
	Editor.Mode = Mode3D
	Editor.MouseDragging3D = true
	Editor.MiddleDragging3D = true
	Editor.PanDragging3D = true
	Editor.GizmoDragging = true

	EditorEndDrags()

	if Editor.MouseDragging3D || Editor.MiddleDragging3D || Editor.PanDragging3D || editorGizmoDragging() {
		t.Errorf("a drag survived the focus loss: right=%v middle=%v pan=%v gizmo=%v",
			Editor.MouseDragging3D, Editor.MiddleDragging3D, Editor.PanDragging3D, editorGizmoDragging())
	}
}
