package editor

import "foundry/internal/world"

// editor_face_sel.go — the selected faces.
//
// Points and edges have been multi-selectable for a while (Editor.SelectedVertices);
// faces were the odd one out, kept as a single ActiveSurface + sector + edge
// index. That was fine while a face selection only ever opened a panel, and
// stopped being fine the moment the menu grew actions worth doing to a dozen
// walls at once — retexturing a room, turning collision off across a floor
// plan, converting a run of walls into portals.
//
// So faces get a list too, and the same shape as the other two: the first entry
// is the primary, and it is mirrored into ActiveSurface / ActiveSectorIdx /
// ActiveEdgeKind / ActiveEdgeIdx so that every panel, outline and action that
// was written against those four fields keeps working untouched. Those fields
// are now *derived*: everything that selects or deselects a face goes through
// this file, and nothing else writes them.
//
// A face is identified by index rather than by pointer, as vertexSel and
// edgeSel are, so a selection survives the geometry rebuild that follows almost
// every edit made to it.

// faceSel identifies one selectable face of one brush.
//
// It used to carry a kind — floor, ceiling, floor_slab, ceiling_slab, wall or
// portal — because a sector's faces were six different things with six
// different field paths behind them. A brush has one kind of face, so the
// kind is gone and what is left is a plain three-index address.
type faceSel struct {
	chunkIdx int
	brushIdx int
	faceIdx  int
	// detail says which of the brush's two meshes faceIdx indexes — see
	// vertexSel.detail, which carries the same flag for the same reason.
	detail bool
}

// brushFaceSel names one face of one brush's HULL.
func brushFaceSel(chunkIdx, brushIdx, faceIdx int) faceSel {
	return faceSel{chunkIdx: chunkIdx, brushIdx: brushIdx, faceIdx: faceIdx}
}

// brushDetailFaceSel names one face of one brush's DETAIL mesh.
func brushDetailFaceSel(chunkIdx, brushIdx, faceIdx int) faceSel {
	return faceSel{chunkIdx: chunkIdx, brushIdx: brushIdx, faceIdx: faceIdx, detail: true}
}

// faceSelBrush resolves the chunk and brush a face belongs to, or nil when the
// indices no longer point at live geometry.
func faceSelBrush(f faceSel) (*world.Chunk, *world.Brush) {
	lvl := Editor.level
	if lvl == nil || f.chunkIdx < 0 || f.chunkIdx >= len(lvl.Chunks) {
		return nil, nil
	}
	ch := &lvl.Chunks[f.chunkIdx]
	if f.brushIdx < 0 || f.brushIdx >= len(ch.Brushes) {
		return ch, nil
	}
	return ch, &ch.Brushes[f.brushIdx]
}

// faceSelFace resolves a selection all the way to the face it names, or nil.
func faceSelFace(f faceSel) *world.BrushFace {
	_, b := faceSelBrush(f)
	m := selMesh(b, f.detail)
	if m == nil || f.faceIdx < 0 || f.faceIdx >= len(m.Faces) {
		return nil
	}
	return &m.Faces[f.faceIdx]
}

// faceSelValid reports whether a face still names something that exists.
func faceSelValid(f faceSel) bool { return faceSelFace(f) != nil }

// isFaceSelected reports whether f is part of the current selection.
func isFaceSelected(f faceSel) bool {
	for _, sel := range Editor.SelectedFaces {
		if sel == f {
			return true
		}
	}
	return false
}

// selectedFaceCount is how many faces are selected — what the menu heading
// counts and what decides whether an action is about one face or many.
func selectedFaceCount() int { return len(Editor.SelectedFaces) }

// ---------- changing the selection ----------

// setFaceSelection makes sels the face selection and clears every other kind,
// since a face is a different unit from a point, an entity or the spawn marker
// and having two live at once is what makes a menu ambiguous.
//
// The first entry is the primary: it drives the four Active* fields the panels
// read, and it is the one an action takes its "before" value from when it has
// to pick one (see editor3DToggleSurfaceFlag).
func setFaceSelection(sels []faceSel) {
	// Cleared first, then assigned: editorClearObjectSelections empties this
	// very list on its way past. sels is always a fresh slice by here — the
	// ctrl+click paths copy before calling — so nothing is lost to it.
	editorClearObjectSelections()
	Editor.SelectedFaces = sels
	syncActiveFace()
}

// addFaceToSelection toggles f in or out of the selection — the ctrl+click
// path. Toggling rather than only adding is what lets a mis-aimed click be
// taken back without starting the whole selection again.
//
// Clicking a face while something else is selected replaces that selection
// rather than joining it: the two are different units.
func addFaceToSelection(f faceSel) {
	if !faceSelValid(f) {
		return
	}
	for i, sel := range Editor.SelectedFaces {
		if sel != f {
			continue
		}
		Editor.SelectedFaces = append(
			append([]faceSel{}, Editor.SelectedFaces[:i]...),
			Editor.SelectedFaces[i+1:]...)
		syncActiveFace()
		return
	}
	setFaceSelection(append(append([]faceSel{}, Editor.SelectedFaces...), f))
}

// clearFaceSelection drops every selected face, and the mirrored fields with
// them.
func clearFaceSelection() {
	Editor.SelectedFaces = nil
	syncActiveFace()
}

// pruneFaceSelection drops entries that no longer name live geometry, which an
// edit that deletes a sector or converts an edge can leave behind.
func pruneFaceSelection() {
	if len(Editor.SelectedFaces) == 0 {
		return
	}
	kept := Editor.SelectedFaces[:0]
	for _, sel := range Editor.SelectedFaces {
		if faceSelValid(sel) {
			kept = append(kept, sel)
		}
	}
	Editor.SelectedFaces = kept
	syncActiveFace()
}

// syncActiveFace republishes the primary face into the four fields the panels,
// the outline and the surface actions were all written against. Everything that
// touches Editor.SelectedFaces ends here, which is what keeps the mirror from
// drifting out of step with the list.
//
// The gizmo is part of that mirror. A selected face is something the move,
// rotate and scale tools can act on — through the corner handles it is built
// from, see editor_face_gizmo.go — so the kind is set here rather than at each
// of the several places a face can be picked from.
func syncActiveFace() {
	if len(Editor.SelectedFaces) == 0 {
		Editor.ActiveBrushIdx = -1
		Editor.ActiveFaceIdx = -1
		// Only our own kind is cleared: this runs from
		// editorClearObjectSelections, which every other selection calls on its
		// way to setting a kind of its own, and from editorClearObjectSelection,
		// which deliberately leaves a live vertex selection alone.
		if Editor.GizmoKind == gizmoKindFace {
			Editor.GizmoKind = gizmoKindNone
		}
		return
	}
	Editor.GizmoKind = gizmoKindFace
	primary := Editor.SelectedFaces[0]
	Editor.ActiveChunkIdx = primary.chunkIdx
	Editor.ActiveBrushIdx = primary.brushIdx
	Editor.ActiveFaceIdx = primary.faceIdx
}

// faceSelPanelFocus is the properties section a face belongs to. One kind of
// face, one section — the floor/ceiling/wall/slab split the sector model
// needed is gone with it.
func faceSelPanelFocus(faceSel) string { return panelFocusFace }

// faceSelFromHit turns a 3D pick into the face it named. False for everything
// that is not brush geometry — the spawn marker, an entity, or a miss — none
// of which a face selection can hold.
func faceSelFromHit(hit pickHit) (faceSel, bool) {
	if hit.kind == pickBrushFace {
		return faceSel{chunkIdx: hit.chunkIdx, brushIdx: hit.brushIdx,
			faceIdx: hit.faceIdx, detail: hit.detail}, true
	}
	return faceSel{}, false
}

// editorSelectFace takes a face, either replacing the selection or adding to
// it, and points the side panel at the section it belongs to. This is what both
// viewports' clicks go through, so a face picked in either one lands the same
// way.
func editorSelectFace(f faceSel, addToSelection bool) {
	if addToSelection {
		addFaceToSelection(f)
	} else {
		setFaceSelection([]faceSel{f})
	}
	Editor.PanelFocus = faceSelPanelFocus(f)
}
