package main

// editor_sel_tool.go — which gizmo the level editor's viewports are showing.
//
// The selection unit (point/edge/face, editor_3d_view.go) says what a click
// grabs; this says what the handles around the grabbed thing do. The two are
// independent, and both are shared by the 3D viewport and the top-down 2D map
// so that switching views never silently changes the tool in your hand.
//
// The trio and its shortcuts are the model editor's — G move, R rotate, C
// scale — for the same reason the selection modes are: two viewports driven the
// same way are one thing to learn instead of two.

import imgui "github.com/inkyblackness/imgui-go/v4"

// editorGizmoTool is the active gizmo.
type editorGizmoTool int

const (
	// Move is the zero value: an editor that has never touched the tools
	// behaves exactly as it did when translate was the only gizmo there was.
	editorToolMove editorGizmoTool = iota
	editorToolRotate
	editorToolScale
)

func (t editorGizmoTool) String() string {
	switch t {
	case editorToolRotate:
		return "Rotate"
	case editorToolScale:
		return "Scale"
	default:
		return "Move"
	}
}

// editorGizmoToolKey is the shortcut shown on a tool's button.
func editorGizmoToolKey(t editorGizmoTool) string {
	switch t {
	case editorToolRotate:
		return "R"
	case editorToolScale:
		return "C"
	default:
		return "G"
	}
}

// editorGizmoToolTooltip explains what each tool does to a multi-selection,
// which is where the three differ in ways the names alone do not convey — in
// particular that scale spreads the selection rather than resizing anything.
func editorGizmoToolTooltip(t editorGizmoTool) string {
	switch t {
	case editorToolRotate:
		return "R — Drag a ring to turn the selection about its own centre.\n" +
			"Every selected point swings around that centre, not around itself."
	case editorToolScale:
		return "C — Drag a handle to spread the selection about its own centre.\n" +
			"Points move toward or away from the centre; nothing changes size."
	default:
		return "G — Drag an arm to slide the selection along one axis.\n" +
			"Everything selected moves together, keeping its shape."
	}
}

// editorGizmoTools are the tools in toolbar order.
var editorGizmoTools = []editorGizmoTool{editorToolMove, editorToolRotate, editorToolScale}

// editorSetGizmoTool switches tools, ending any drag in progress first so a
// half-finished translation cannot be completed as a rotation.
func editorSetGizmoTool(t editorGizmoTool) {
	if Editor.GizmoTool == t {
		return
	}
	editorGizmoMouseUp()
	editor2DGizmoMouseUp()
	Editor.GizmoTool = t
}

// editorToolActsOnSelection reports whether the current tool has anything to
// act on. Scale is defined against the selection's own size, so it needs a set
// of points to spread out or something with a scale of its own; a whole chunk
// has neither and only moves.
func editorToolActsOnSelection() bool {
	if Editor.GizmoTool == editorToolMove {
		return true
	}
	// Rotate and scale need something with an orientation or a size. A vertex
	// selection has an extent to swing and spread; an entity has its own
	// rotation and scale, which the two tools rewrite in place.
	if len(Editor.SelectedVertices) > 0 || activeGizmoEntity() != nil {
		return true
	}
	// The spawn marker is the in-between case: no size to spread, but a facing
	// that is the whole point of it, so it turns and does not scale.
	return Editor.GizmoTool == editorToolRotate && activeGizmoSpawn() != nil
}

// editorSelKeysActive reports whether the selection-unit and gizmo-tool
// shortcuts should answer right now: only in the two viewports that have a
// selection to act on, and never while a panel field has the keyboard — typing
// a sector id must not also switch the tool underneath it.
func editorSelKeysActive() bool {
	if Editor.Mode != Mode3D && Editor.Mode != Mode2D {
		return false
	}
	return !imgui.CurrentIO().WantCaptureKeyboard()
}
