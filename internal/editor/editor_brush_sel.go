package editor

// editor_brush_sel.go — the selected brushes, which is what OBJECT mode
// selects.
//
// The level editor has two contexts, the way the model editor's viewport would
// if a level held more than one mesh (see editor_3d_view.go):
//
//   - Object mode moves a placed brush around. The unit is the whole solid, and
//     the gizmo moves, turns and scales all of it at once.
//   - Edit mode reshapes one. The unit is a point, an edge or a face, and that
//     is the machinery that was already here.
//
// A brush selection is a list for the same reason the face and vertex ones are:
// dragging six brushes into place together is the ordinary way to move a room,
// and a single-selection field would make the menu's "delete" ambiguous the
// moment ctrl+click existed.
//
// # Why the gizmo needs nothing of its own
//
// Moving a brush IS moving every one of its corners — the shape is the vertex
// list, and there is no separate transform on an authored brush to write to
// (Brush.Offset is the run-time placement a script writes, which is a different
// thing and deliberately not what the editor drags). So brushVertexSels below
// resolves a brush selection down to the same []vertexSel a face selection
// resolves to, gizmoVertexSels hands that to the tools, and move, rotate and
// scale all work with no transform code of their own. Exactly the deal
// editor_face_gizmo.go struck for faces.

import (
	"github.com/go-gl/mathgl/mgl32"
)

// brushSel identifies one brush of one chunk.
type brushSel struct {
	chunkIdx int
	brushIdx int
}

// brushSelValid reports whether a selection still names live geometry.
func brushSelValid(s brushSel) bool {
	lvl := ws().level
	if lvl == nil || s.chunkIdx < 0 || s.chunkIdx >= len(lvl.Chunks) {
		return false
	}
	ch := &lvl.Chunks[s.chunkIdx]
	return s.brushIdx >= 0 && s.brushIdx < len(ch.Brushes)
}

// isBrushSelected reports whether s is part of the current selection.
func isBrushSelected(s brushSel) bool {
	for _, sel := range ws().SelectedBrushes {
		if sel == s {
			return true
		}
	}
	return false
}

// primaryBrushSel is the brush the panel edits: the first of the selection, or
// an unresolvable one when nothing is selected.
func primaryBrushSel() brushSel {
	if len(ws().SelectedBrushes) == 0 {
		return brushSel{chunkIdx: -1, brushIdx: -1}
	}
	return ws().SelectedBrushes[0]
}

// ---------- changing the selection ----------

// setBrushSelection makes sels the brush selection and clears every other kind.
func setBrushSelection(sels []brushSel) {
	// Cleared first, then assigned: editorClearObjectSelections empties this
	// very list on its way past, and every caller has already copied.
	editorClearObjectSelections()
	ws().SelectedBrushes = sels
	syncActiveBrush()
}

// addBrushToSelection toggles s in or out of the selection — the ctrl+click
// path, toggling rather than only adding so a mis-aimed click can be taken back
// without starting again.
func addBrushToSelection(s brushSel) {
	if !brushSelValid(s) {
		return
	}
	for i, sel := range ws().SelectedBrushes {
		if sel != s {
			continue
		}
		ws().SelectedBrushes = append(
			append([]brushSel{}, ws().SelectedBrushes[:i]...),
			ws().SelectedBrushes[i+1:]...)
		syncActiveBrush()
		return
	}
	setBrushSelection(append(append([]brushSel{}, ws().SelectedBrushes...), s))
}

// clearBrushSelection drops every selected brush.
func clearBrushSelection() {
	ws().SelectedBrushes = nil
	syncActiveBrush()
}

// syncActiveBrush republishes the primary brush into the fields the panels read
// and hands the gizmo its kind — the brush counterpart of syncActiveFace, and
// the one place ws().GizmoKind becomes gizmoKindBrush.
func syncActiveBrush() {
	if len(ws().SelectedBrushes) == 0 {
		if ws().GizmoKind == gizmoKindBrush {
			ws().GizmoKind = gizmoKindNone
		}
		return
	}
	ws().GizmoKind = gizmoKindBrush
	primary := ws().SelectedBrushes[0]
	ws().ActiveChunkIdx = primary.chunkIdx
	// ActiveBrushIdx is the "which brush is being worked on" field every tool
	// that acts on one brush reads (editor3DActiveBrush), so an object-mode
	// selection publishes it too — otherwise Duplicate would copy whichever
	// brush a face was last picked on rather than the one that is selected.
	ws().ActiveBrushIdx = primary.brushIdx
	ws().ActiveFaceIdx = -1
}

// editorSelectBrushObject takes a brush, either replacing the selection or
// adding to it, and points the side panel at its section. What a click in
// object mode goes through, and what the Explorer's rows go through.
func editorSelectBrushObject(s brushSel, addToSelection bool) {
	if addToSelection {
		addBrushToSelection(s)
	} else {
		setBrushSelection([]brushSel{s})
	}
	ws().PanelFocus = panelFocusBrush
}

// ---------- what the gizmo drags ----------

// brushVertexSels is every corner of every selected brush, without duplicates.
//
// A brush owns its verts outright — unlike a sector's shared point pool — so
// there is nothing to deduplicate WITHIN a brush; the map is there because a
// brush can appear twice in the selection only through a bug, and a corner
// written twice would move twice.
func brushVertexSels() []vertexSel {
	var out []vertexSel
	seen := map[vertexSel]bool{}
	add := func(sel vertexSel) {
		if seen[sel] {
			return
		}
		seen[sel] = true
		out = append(out, sel)
	}
	for _, s := range ws().SelectedBrushes {
		_, b := vertexSelBrush(vertexSel{chunkIdx: s.chunkIdx, brushIdx: s.brushIdx})
		if b == nil {
			continue
		}
		for vi := range b.Verts {
			add(vertexSel{chunkIdx: s.chunkIdx, brushIdx: s.brushIdx, vertIdx: vi})
		}
		// The detail mesh's corners too, if there is one. Moving, turning or
		// scaling a brush has to carry what has been carved into its surface:
		// a detail mesh is authored in the same chunk-local space as the hull
		// and is not derived from it, so nothing else would bring it along and
		// a dragged wall would leave its ledge behind in mid-air.
		if b.Detail == nil {
			continue
		}
		for vi := range b.Detail.Verts {
			add(vertexSel{chunkIdx: s.chunkIdx, brushIdx: s.brushIdx, vertIdx: vi, detail: true})
		}
	}
	return out
}

// selectedBrushesCenter is where the gizmo sits for a brush selection: the
// average of every selected brush's corners, in world space.
func selectedBrushesCenter() (mgl32.Vec3, bool) {
	var sum mgl32.Vec3
	n := 0
	for _, sel := range brushVertexSels() {
		if pos, ok := vertexSelPos(sel); ok {
			sum = sum.Add(pos)
			n++
		}
	}
	if n == 0 {
		return mgl32.Vec3{}, false
	}
	return sum.Mul(1 / float32(n)), true
}

// selectedBrushesBounds is the world AABB of the brush selection, for framing
// the camera on it.
func selectedBrushesBounds() (mgl32.Vec3, mgl32.Vec3, bool) {
	var min, max mgl32.Vec3
	found := false
	for _, sel := range brushVertexSels() {
		pos, ok := vertexSelPos(sel)
		if !ok {
			continue
		}
		if !found {
			min, max, found = pos, pos, true
			continue
		}
		for i := 0; i < 3; i++ {
			if pos[i] < min[i] {
				min[i] = pos[i]
			}
			if pos[i] > max[i] {
				max[i] = pos[i]
			}
		}
	}
	return min, max, found
}
