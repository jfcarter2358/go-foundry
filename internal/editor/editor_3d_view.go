package editor

// editor_3d_view.go — the 3D view's view-level switches: which CONTEXT a click
// works in, what it selects within that context, and how the world is shaded.
//
// The selection unit and the shading toggle are lifted straight from the model
// editor so the two viewports are driven the same way — 1/2/3 for the unit, Z
// for shading — and both mean the same thing here as they do there.
//
// The context is what a level has and a single model does not. A level holds
// many brushes, so there are two different things "click a wall" could mean:
// pick up the whole solid it belongs to, or grab that one face of it. Tab
// switches between them, and an author starts in the first — placing and moving
// brushes is what blocking out a level is, and reshaping one is the thing you
// go INTO.

// editorEditContext is what a left click and a gizmo address: whole objects, or
// the parts one brush is made of.
type editorEditContext int

const (
	// Object is the zero value, so a fresh editor — and a fresh level — starts
	// where an author starts: moving brushes about rather than reshaping one.
	editorObjectMode editorEditContext = iota
	editorEditMode
	// Detail shapes one brush's detail mesh — its surface, not its solid. It
	// is entered on a named brush rather than cycled into; see
	// editor_detail.go.
	editorDetailMode
)

func (c editorEditContext) String() string {
	switch c {
	case editorEditMode:
		return "Edit"
	case editorDetailMode:
		return "Detail"
	default:
		return "Object"
	}
}

// editorInEditMode is the predicate everything downstream asks, rather than
// comparing the enum: the negative reads badly at call sites and there are a
// lot of them.
func editorInEditMode() bool { return ws().EditContext == editorEditMode }

// EditorInEditMode is editorInEditMode for outside the package — the keybinding
// reference (internal/app) shows a different block of shortcuts per context.
func EditorInEditMode() bool { return editorInEditMode() }

// editorInMeshContext reports whether a click addresses the PARTS of a brush —
// its points, edges and faces — rather than whole objects. True in edit mode,
// where those parts are the solid's, and in detail mode, where they are the
// detail mesh's.
//
// This is the question every dispatch actually wants to ask, and asking
// editorInEditMode instead is a bug with a characteristic shape: detail mode
// falls through to the object path, so a click in it picks up the whole brush,
// no detail face can ever be selected, and every tool that acts on one reports
// that nothing is selected. Detail mode is a third context, not a variant of
// object mode.
func editorInMeshContext() bool { return editorInEditMode() || editorInDetailMode() }

// editorSetEditContext switches context, carrying the author's place across.
//
// The two selections are different units — a brush and one of its faces — so
// neither survives verbatim, but neither is thrown away either:
//
//   - Going INTO edit mode, the selected brush becomes the brush being shaped.
//     Its faces and corners are what clicks now reach; nothing starts selected,
//     because selecting the whole of it would put a gizmo on a shape the author
//     came here to take apart.
//   - Coming OUT of it, whatever face or corner was selected resolves back to
//     the brush that owns it and that brush becomes the object selection. Tab,
//     tab is therefore a round trip rather than a way to lose your place.
func editorSetEditContext(c editorEditContext) {
	if ws().EditContext == c {
		return
	}
	// Detail mode is entered on a NAMED brush, so there is nothing sensible
	// for a bare switch INTO it to point at; and leaving it has its own
	// unwinding (the selection is in the detail mesh's index space, which
	// means nothing outside). Both go through editor_detail.go.
	if editorInDetailMode() {
		editorExitDetailMode()
		if c == editorObjectMode {
			return
		}
	}
	if c == editorDetailMode {
		editorEnterDetailModeOnSelection()
		return
	}
	if c == editorEditMode {
		// Remember which brush to come back to before the selection that names
		// it is cleared.
		if s := primaryBrushSel(); brushSelValid(s) {
			ws().ActiveChunkIdx, ws().ActiveBrushIdx = s.chunkIdx, s.brushIdx
		}
		clearBrushSelection()
		ws().EditContext = c
		ws().PanelFocus = panelFocusNone
		return
	}

	// Back to objects: the brush under whatever was selected becomes the
	// selection, so the gizmo lands on the solid the author was just inside.
	var back []brushSel
	if f := primaryFaceSel(); faceSelValid(f) {
		back = []brushSel{{chunkIdx: f.chunkIdx, brushIdx: f.brushIdx}}
	} else if len(ws().SelectedVertices) > 0 {
		v := ws().SelectedVertices[0]
		if s := (brushSel{chunkIdx: v.chunkIdx, brushIdx: v.brushIdx}); brushSelValid(s) {
			back = []brushSel{s}
		}
	}
	clearVertexSelection()
	clearFaceSelection()
	ws().EditContext = c
	if back != nil {
		setBrushSelection(back)
		ws().PanelFocus = panelFocusBrush
		return
	}
	ws().PanelFocus = panelFocusNone
}

// editorEnterEditUnit is what 1, 2 and 3 are bound to: pick a unit a brush is
// made of, and go where that unit means something.
//
// Not just editorSetSelMode. Reaching for a corner while in object mode is
// exactly the intent, and a key that silently does nothing because Tab was not
// pressed first is the kind of dead binding that teaches an author the editor
// is broken.
//
// Detail mode is the exception, and stays put: its mesh has points, edges and
// faces of its own, so the three keys already mean something in there. Sending
// an author back out to the solid because they pressed 1 would throw away the
// mode they are working in to answer a question they did not ask.
func editorEnterEditUnit(m editorSelMode) {
	if !editorInDetailMode() {
		editorSetEditContext(editorEditMode)
	}
	editorSetSelMode(m)
}

// editorToggleEditContext is what Tab is bound to.
//
// Tab out of detail mode rather than through it. Detail is a place you go into
// one brush, so the way back out is the same way you came — leaving it in the
// cycle would mean tabbing from a brush you were shaping into the detail mesh
// of whichever brush happened to be selected.
func editorToggleEditContext() {
	switch {
	case editorInDetailMode():
		editorExitDetailMode()
	case editorInEditMode():
		editorSetEditContext(editorObjectMode)
	default:
		editorSetEditContext(editorEditMode)
	}
}

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
		return "Point"
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
		return "3 — Click a brush face, an object, or the spawn point.\n" +
			"Repeat clicks cycle through everything under the cursor;\n" +
			"Ctrl+click adds the nearest to the selection."
	}
}

// editorSelModes are the modes in toolbar order.
var editorSelModes = []editorSelMode{editorSelPoint, editorSelEdge, editorSelFace}

// editorSetSelMode switches what clicks select, dropping whatever the new mode
// cannot express.
//
// Point and edge share their storage — an edge selection *is* the selection of
// its two endpoints (editor_3d_edge_pick.go) — so switching between those two
// converts the selection for free and deliberately keeps it. Face mode works in
// a different unit entirely, so the crossing clears in both directions rather
// than leaving a selection the visible handles no longer explain.
func editorSetSelMode(m editorSelMode) {
	if ws().SelMode == m {
		return
	}
	prev := ws().SelMode
	ws().SelMode = m
	if m == editorSelFace {
		clearVertexSelection()
		ws().PanelFocus = panelFocusNone
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
	editorClearSealSelection()
	clearFaceSelection()
	clearBrushSelection()
	ws().PanelFocus = panelFocusNone
	if ws().GizmoKind != gizmoKindVertices {
		ws().GizmoKind = gizmoKindNone
	}
}

// ---------- shading ----------

// editorSetWireframe switches shading. The visibility cache goes with it: which
// handles can be seen changes without the camera or the world having moved.
func editorSetWireframe(on bool) {
	if ws().Wireframe == on {
		return
	}
	ws().Wireframe = on
	editorVisReset()
}

// editorShadingLabel names the shading button by the mode it is in.
func editorShadingLabel() string {
	if ws().Wireframe {
		return "Wireframe [Z]"
	}
	return "Solid [Z]"
}

// editorShowsAllHandles reports whether every handle in range is drawn rather
// than only the ones near the cursor. Wireframe implies it — showing the whole
// cage is what the mode is for — and the show-all toggle asks for it directly
// while still shading solid.
func editorShowsAllHandles() bool {
	return ws().Wireframe || Editor.ShowPointSelectRadius
}

// editorHandleShowPx is how close the cursor has to be to a point or edge
// before its handle is drawn.
func editorHandleShowPx() float32 {
	if editorShowsAllHandles() {
		return vertexHandleShowAllPx
	}
	return vertexHandleShowPx
}
