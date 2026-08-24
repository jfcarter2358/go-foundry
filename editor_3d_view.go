package main

// editor_3d_view.go — the 3D view's two view-level switches: what a click
// selects, and how the world is shaded.
//
// Both are lifted straight from the model editor so the two viewports are
// driven the same way — 1/2/3 for the selection unit, Z for shading — and both
// mean the same thing here as they do there. The one deliberate difference is
// that this editor's "face" mode is the level editor's original click
// behaviour: it picks walls, floors and ceilings, and also the spawn point,
// because that is not made of the points and edges the other two modes work
// in.

// editorSelMode is what a left click selects — in the 3D view and, since the
// map grew the same modes, in the 2D one too (editor_2d_select.go). One field
// for both, so a selection made in either view is drawn and acted on as the
// same kind of thing in the other.
type editorSelMode int

const (
	// Face is the zero value so a fresh editor behaves exactly as it always
	// has: click a wall, a floor, a ceiling or the spawn point.
	editorSelFace editorSelMode = iota
	editorSelPoint
	editorSelEdge
)

func (m editorSelMode) String() string {
	switch m {
	case editorSelPoint:
		return "geom.Point"
	case editorSelEdge:
		return "Edge"
	default:
		return "Face"
	}
}

// editorSelModeKey is the shortcut shown on a mode's button, matching the model
// editor's 1/2/3 ordering: point, edge, face.
func editorSelModeKey(m editorSelMode) string {
	switch m {
	case editorSelPoint:
		return "1"
	case editorSelEdge:
		return "2"
	default:
		return "3"
	}
}

// editorSelModeTooltip explains what each mode's click reaches, since the crisp
// part of the modes — that a click only ever resolves to one kind of thing — is
// also the surprising part coming from the old always-everything viewport.
func editorSelModeTooltip(m editorSelMode) string {
	switch m {
	case editorSelPoint:
		return "1 — Click a sector corner's handle box.\n" +
			"Repeat clicks cycle overlapping points; Ctrl+click takes them all."
	case editorSelEdge:
		return "2 — Click the prism around a sector edge.\n" +
			"An edge selection is its two endpoints, so the gizmo moves it as one."
	default:
		return "3 — Click a wall, floor, ceiling, water plane or the spawn point.\n" +
			"On the 2D map: a wall or portal line, the spawn marker, or a sector."
	}
}

// editorSelModes are the modes in toolbar order.
var editorSelModes = []editorSelMode{editorSelPoint, editorSelEdge, editorSelFace}

// editorSetSelMode switches what clicks select, dropping whatever the new mode
// cannot express.
//
// geom.Point and edge share their storage — an edge selection *is* the selection of
// its two endpoints (editor_3d_edge_pick.go) — so switching between those two
// converts the selection for free and deliberately keeps it. Face mode works in
// a different unit entirely, so the crossing clears in both directions rather
// than leaving a selection the visible handles no longer explain.
func editorSetSelMode(m editorSelMode) {
	if Editor.SelMode == m {
		return
	}
	prev := Editor.SelMode
	Editor.SelMode = m
	if m == editorSelFace {
		clearVertexSelection()
		Editor.PanelFocus = panelFocusNone
		return
	}
	if prev == editorSelFace {
		editorClearObjectSelection()
	}
}

// editorClearObjectSelection drops everything face mode can select — the spawn
// point and the picked sector surface — leaving any vertex selection alone.
//
// The biome sector behind a clicked water plane is deliberately left as it is.
// That selection is shared with the 2D biome layer, and pressing 1 in the 3D
// view to grab a point should not deselect the polygon someone is painting in
// the popped-out 2D window. A *click* in the 3D viewport does clear it
// (editor3DClearSelection) — that is an explicit "the selection moved".
func editorClearObjectSelection() {
	editorClearSpawnSelection()
	clearFaceSelection()
	Editor.PanelFocus = panelFocusNone
	if Editor.GizmoKind != gizmoKindVertices {
		Editor.GizmoKind = gizmoKindNone
	}
}

// ---------- shading ----------

// editorSetWireframe switches shading. The visibility cache goes with it: which
// handles can be seen changes without the camera or the world having moved.
func editorSetWireframe(on bool) {
	if Editor.Wireframe == on {
		return
	}
	Editor.Wireframe = on
	editorVisReset()
}

// editorShadingLabel names the shading button by the mode it is in.
func editorShadingLabel() string {
	if Editor.Wireframe {
		return "Wireframe [Z]"
	}
	return "Solid [Z]"
}

// editorShowsAllHandles reports whether every handle in range is drawn rather
// than only the ones near the cursor. Wireframe implies it — showing the whole
// cage is what the mode is for — and the show-all toggle asks for it directly
// while still shading solid.
func editorShowsAllHandles() bool {
	return Editor.Wireframe || Editor.ShowPointSelectRadius
}

// editorHandleShowPx is how close the cursor has to be to a point or edge
// before its handle is drawn.
func editorHandleShowPx() float32 {
	if editorShowsAllHandles() {
		return vertexHandleShowAllPx
	}
	return vertexHandleShowPx
}
