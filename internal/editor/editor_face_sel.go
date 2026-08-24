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

// faceSel identifies one selectable face of one sector.
type faceSel struct {
	chunkIdx  int
	sectorIdx int
	kind      string // "floor", "ceiling", "wall", "portal"
	idx       int    // index into Sector.Walls or .Portals; -1 for floor/ceiling
}

// surfaceFaceSel names a floor or ceiling, which a sector has at most one of.
func surfaceFaceSel(chunkIdx, sectorIdx int, kind string) faceSel {
	return faceSel{chunkIdx: chunkIdx, sectorIdx: sectorIdx, kind: kind, idx: -1}
}

// edgeFaceSel names one wall or portal of a sector.
func edgeFaceSel(chunkIdx, sectorIdx int, kind string, idx int) faceSel {
	return faceSel{chunkIdx: chunkIdx, sectorIdx: sectorIdx, kind: kind, idx: idx}
}

// isEdgeFace reports whether this face is a wall or a portal — the two that
// carry an index and can be converted into each other.
func (f faceSel) isEdgeFace() bool { return f.kind == "wall" || f.kind == "portal" }

// faceSelSector resolves the chunk and sector a face belongs to, or nil when
// the indices no longer point at live geometry.
func faceSelSector(f faceSel) (*world.Chunk, *world.Sector) {
	lvl := Editor.level
	if lvl == nil || f.chunkIdx < 0 || f.chunkIdx >= len(lvl.Chunks) {
		return nil, nil
	}
	ch := &lvl.Chunks[f.chunkIdx]
	if f.sectorIdx < 0 || f.sectorIdx >= len(ch.Sectors) {
		return ch, nil
	}
	return ch, &ch.Sectors[f.sectorIdx]
}

// faceSelValid reports whether a face still names something that exists.
func faceSelValid(f faceSel) bool {
	_, s := faceSelSector(f)
	if s == nil {
		return false
	}
	switch f.kind {
	case "floor", "ceiling":
		return true
	case "wall":
		return f.idx >= 0 && f.idx < len(s.Walls)
	case "portal":
		return f.idx >= 0 && f.idx < len(s.Portals)
	}
	return false
}

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
	Editor.SelectedFaces = sels
	clearVertexSelection()
	editorClearSpawnSelection()
	editorClearEntitySelection()
	editorClearVolumeSelection()
	Editor.GizmoKind = gizmoKindNone
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
func syncActiveFace() {
	if len(Editor.SelectedFaces) == 0 {
		Editor.ActiveSurface = ""
		Editor.ActiveEdgeKind = ""
		Editor.ActiveEdgeIdx = -1
		return
	}
	primary := Editor.SelectedFaces[0]
	Editor.ActiveChunkIdx = primary.chunkIdx
	Editor.ActiveSectorIdx = primary.sectorIdx
	Editor.ActiveSurface = primary.kind
	if primary.isEdgeFace() {
		Editor.ActiveEdgeKind = primary.kind
		Editor.ActiveEdgeIdx = primary.idx
	} else {
		Editor.ActiveEdgeKind = ""
		Editor.ActiveEdgeIdx = -1
	}
}

// faceSelPanelFocus is the properties section a face belongs to. Portals live
// inside the wall section, which is where their fields are drawn.
func faceSelPanelFocus(f faceSel) string {
	switch f.kind {
	case "floor":
		return panelFocusFloor
	case "ceiling":
		return panelFocusCeiling
	default:
		return panelFocusWall
	}
}

// faceSelFromHit turns a 3D pick into the face it named. False for everything
// that is not a sector face — the spawn marker, an entity, a water plane, or a
// miss — none of which a face selection can hold.
func faceSelFromHit(hit pickHit) (faceSel, bool) {
	switch hit.kind {
	case pickWall:
		return edgeFaceSel(hit.chunkIdx, hit.sectorIdx, "wall", hit.idx), true
	case pickFloor:
		return surfaceFaceSel(hit.chunkIdx, hit.sectorIdx, "floor"), true
	case pickCeiling:
		return surfaceFaceSel(hit.chunkIdx, hit.sectorIdx, "ceiling"), true
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
