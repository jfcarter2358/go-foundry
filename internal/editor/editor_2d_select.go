package editor

// editor_2d_select.go — what a left-click selects on the top-down map.
//
// The map used to resolve every click through one fixed ladder: vertex, then
// edge, then object, then sector interior. That made the fine targets always
// win, so picking the wall behind a corner meant nudging the cursor until the
// vertex let go of it — the same problem the 3D viewport had before it grew
// selection modes (editor_3d_view.go).
//
// So the map runs on those same modes, and on the same Editor.SelMode: point
// mode takes corners, edge mode takes edges, face mode takes walls, portals,
// objects and sector interiors. Sharing the field rather than keeping a second
// one is what makes a selection made in one view legible in the other — both
// views draw and act on the same unit at the same time.
//
// Selections themselves are the shared ones too: Editor.SelectedVertices for
// points and edges (an edge selection *is* its two endpoints, exactly as in 3D,
// so one gizmo moves either), and ActiveSurface/ActiveEdgeKind/ActiveEdgeIdx
// for faces. Nothing here needs to tell the 3D view anything; there is only one
// selection, and both views read it.
//
// Ctrl+click adds to that selection, again as in 3D. It used to draw an edge to
// the last point clicked, which is now the two-step Join (editor_2d_join.go) —
// and the reason point mode here reaches free points as well as sector corners,
// since a sector that does not exist yet has only free points to pick.

import (
	"foundry/internal/geom"
	"foundry/internal/world"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// editor2DVertexCandidates returns every point within threshold of (wx,wz),
// nearest first: each sector's corners at whichever height the map is currently
// editing, and the free points that are in no sector at all.
//
// Free points are included because they are the raw material of a new sector —
// shift+click drops them, and joining them is what turns them into one
// (editor_2d_join.go). They are last within a tie, since a corner that already
// belongs to something is the more likely target.
//
// Unlike the 3D handle picker this is not filtered by visibility: the map draws
// everything from above and nothing occludes anything.
func editor2DVertexCandidates(ch *world.Chunk, wx, wz, threshold float32) []vertexSel {
	type cand struct {
		sel  vertexSel
		dist float32
	}
	var cands []cand
	used := make([]bool, len(ch.Points))
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		for vi, pIdx := range s.PointIndices {
			if pIdx < 0 || pIdx >= len(ch.Points) {
				continue
			}
			used[pIdx] = true
			pt := ch.Points[pIdx]
			d := dist2D(wx, wz, pt.X(), pt.Y())
			if d >= threshold {
				continue
			}
			cands = append(cands, cand{vertexSel{
				chunkIdx:  Editor.ActiveChunkIdx,
				sectorIdx: si,
				vertIdx:   vi,
				pointIdx:  pIdx,
				isFloor:   Editor.EditFloorVerts,
			}, d})
		}
	}
	for pIdx, pt := range ch.Points {
		if used[pIdx] {
			continue
		}
		if d := dist2D(wx, wz, pt.X(), pt.Y()); d < threshold {
			cands = append(cands, cand{freePointSel(Editor.ActiveChunkIdx, pIdx), d})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].dist < cands[j].dist })
	out := make([]vertexSel, len(cands))
	for i, c := range cands {
		out[i] = c.sel
	}
	return out
}

// editor2DEdgeCandidates returns every sector edge passing within threshold of
// (wx,wz), nearest first, at whichever height the map is currently editing.
func editor2DEdgeCandidates(ch *world.Chunk, wx, wz, threshold float32) []edgeSel {
	kind := edgeKind3DCeiling
	if Editor.EditFloorVerts {
		kind = edgeKind3DFloor
	}
	type cand struct {
		sel  edgeSel
		dist float32
	}
	var cands []cand
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		n := len(s.PointIndices)
		if n < 2 {
			continue
		}
		for ei := 0; ei < n; ei++ {
			a := ch.Points[s.PointIndices[ei]]
			b := ch.Points[s.PointIndices[(ei+1)%n]]
			d := distToSegment2D(wx, wz, a.X(), a.Y(), b.X(), b.Y())
			if d >= threshold {
				continue
			}
			cands = append(cands, cand{edgeSel{
				chunkIdx:  Editor.ActiveChunkIdx,
				sectorIdx: si,
				kind:      kind,
				vertIdx:   ei,
			}, d})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].dist < cands[j].dist })
	out := make([]edgeSel, len(cands))
	for i, c := range cands {
		out[i] = c.sel
	}
	return out
}

// editor2DCycle returns the index of the candidate a fresh click should take:
// the one after whichever is already selected, so clicking the same spot again
// works through everything stacked under the cursor and wraps back to the
// start. Nothing selected there yet means the nearest.
func editor2DCycle(n int, selected func(int) bool) int {
	if n == 0 {
		return -1
	}
	for i := 0; i < n; i++ {
		if selected(i) {
			return (i + 1) % n
		}
	}
	return 0
}

// ---------- point mode ----------

// editor2DHandlePointClick resolves a click against the sector corners. A plain
// click takes one and cycles through the stack on repeat; addToSelection (the
// map's alt+click — see Editor2DHandleClick) unions every corner under the
// cursor into the selection instead. Returns false when the click missed every
// corner.
func editor2DHandlePointClick(ch *world.Chunk, wx, wz, threshold float32, addToSelection, armDrag bool) bool {
	cands := editor2DVertexCandidates(ch, wx, wz, threshold)
	if len(cands) == 0 {
		return false
	}
	if addToSelection {
		addVertexHandlesToSelection(cands)
		return true
	}

	pick := cands[editor2DCycle(len(cands), func(i int) bool { return isVertexSelected(cands[i]) })]
	setVertexSelection([]vertexSel{pick})
	Editor.ActiveSectorIdx = pick.sectorIdx

	// A single corner can still be dragged straight from the map, which is the
	// gesture the 2D editor has always been built around. A multi-selection
	// cannot: dragging one of its members would pull that corner out of the
	// group instead of moving the group, so those go through the gizmo. A free
	// point has no sector for the drag path to rebuild, so it goes through the
	// gizmo too.
	//
	// armDrag is false when the caller is not a press that will be released —
	// the context menu selects what it opened on (editor_2d_context_menu.go),
	// and a drag armed there would never end, besides leaving an undo entry for
	// a gesture that moved nothing.
	if !armDrag || pick.isFreePoint() {
		return true
	}
	EditorPushUndo() // snapshot at drag start, not per-frame during the drag
	Editor.DragVertexSI = pick.sectorIdx
	Editor.DragVertexVI = pick.vertIdx
	Editor.DragIsFloor = true
	return true
}

// ---------- edge mode ----------

// editor2DHandleEdgeClick resolves a click against the sector edges, cycling on
// repeat and unioning on alt+click the way point mode does. Returns false when
// the click missed every edge.
func editor2DHandleEdgeClick(ch *world.Chunk, wx, wz, threshold float32, addToSelection bool) bool {
	cands := editor2DEdgeCandidates(ch, wx, wz, threshold)
	if len(cands) == 0 {
		return false
	}
	if addToSelection {
		verts := edgeVertices(cands)
		if len(verts) == 0 {
			return false
		}
		addVertexHandlesToSelection(verts)
		return true
	}

	pick := cands[editor2DCycle(len(cands), func(i int) bool { return isEdgeSelected(cands[i]) })]
	if !setEdgeSelection([]edgeSel{pick}) {
		return false
	}
	// setEdgeSelection has already pointed the panel at the wall or portal the
	// edge runs along (noteEdgeSurface), which is what the map draws in the
	// selected colour.
	return true
}

// ---------- face mode ----------

// editor2DHandleFaceClick resolves a click against the things a face selection
// can name: a wall or portal edge, the spawn marker, or a sector interior. This
// is the map's original ladder minus its vertex rung, which point mode now
// owns. Returns false when the click landed on none of them.
func editor2DHandleFaceClick(lvl *world.Level, ch *world.Chunk, wx, wz, threshold float32, addToSelection bool) bool {
	if f, ok := editor2DFaceAt(ch, wx, wz, threshold); ok {
		editorSelectFace(f, addToSelection)
		return true
	}
	// Nothing to add an object to: the spawn marker and the entities are not
	// faces, so a ctrl+click that reaches one is a plain selection of it.
	return editor2DPickObject(lvl, ch, wx, wz)
}

// editor2DFaceAt resolves what face a map click lands on: a wall or portal
// line, else the interior of the sector under the cursor.
//
// Edges come first because a wall is a line up here, and a line drawn across a
// sector is the more specific target than the sector it is drawn across.
// Repeated clicks cycle through the lines stacked under the cursor, so the far
// side of a shared wall is reachable.
func editor2DFaceAt(ch *world.Chunk, wx, wz, threshold float32) (faceSel, bool) {
	if cands := editor2DEdgeCandidates(ch, wx, wz, threshold*2); len(cands) > 0 {
		pick := cands[editor2DCycle(len(cands), func(i int) bool {
			s := &ch.Sectors[cands[i].sectorIdx]
			kind, kindIdx := world.EdgeKindAndIndex(s, cands[i].vertIdx)
			return isFaceSelected(edgeFaceSel(Editor.ActiveChunkIdx, cands[i].sectorIdx, kind, kindIdx))
		})]
		s := &ch.Sectors[pick.sectorIdx]
		kind, kindIdx := world.EdgeKindAndIndex(s, pick.vertIdx)
		return edgeFaceSel(Editor.ActiveChunkIdx, pick.sectorIdx, kind, kindIdx), true
	}

	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if !geom.PointInPolygon(mgl32.Vec2{wx, wz}, world.SectorPoints(ch, s)) {
			continue
		}
		// Which of the two horizontal surfaces the click means is the map's own
		// floor/ceiling switch, the same one that decides which crosses are lit.
		kind := "ceiling"
		if Editor.EditFloorVerts {
			kind = "floor"
		}
		return surfaceFaceSel(Editor.ActiveChunkIdx, si, kind), true
	}
	return faceSel{}, false
}

// editor2DClearSelection drops every kind of map selection at once — the miss
// case for a plain click, where nothing under the cursor means nothing
// selected.
func editor2DClearSelection() {
	clearVertexSelection()
	clearFaceSelection()
	editorClearSpawnSelection()
	editorClearEntitySelection()
	editorClearVolumeSelection()
	Editor.ActiveSurface = ""
	Editor.ActiveEdgeKind = ""
	Editor.ActiveEdgeIdx = -1
	Editor.PanelFocus = panelFocusNone
}

// editor2DVertexSelected reports whether one of the map's drawn corner crosses
// belongs to the current selection. The map draws a floor cross and a ceiling
// cross per corner; each answers for its own height, so a floor selection does
// not light the ceiling mark sitting on top of it.
func editor2DVertexSelected(si, vi, pIdx int, isFloor bool) bool {
	return isVertexSelected(vertexSel{
		chunkIdx:  Editor.ActiveChunkIdx,
		sectorIdx: si,
		vertIdx:   vi,
		pointIdx:  pIdx,
		isFloor:   isFloor,
	})
}

// editor2DHasSelection reports whether anything on the map is selected — points
// or edges through the shared vertex selection, a face, or the spawn marker.
func editor2DHasSelection() bool {
	return len(Editor.SelectedVertices) > 0 || Editor.SpawnSelected ||
		Editor.ActiveEntityIdx >= 0 || Editor.ActiveSurface != ""
}

// editor2DSelectAt selects whatever is under (wx,wz) in the current mode,
// without arming a drag. This is the context menu's "open it on what I pointed
// at" — it deliberately skips the placement, biome and gizmo branches a real
// click goes through, since none of them are what a right-click means.
func editor2DSelectAt(lvl *world.Level, ch *world.Chunk, wx, wz float32) {
	threshold := screenToWorldSize(editor2DSelectRadius, editor2DOrtho())
	switch Editor.SelMode {
	case editorSelPoint:
		editor2DHandlePointClick(ch, wx, wz, threshold, false, false)
	case editorSelEdge:
		editor2DHandleEdgeClick(ch, wx, wz, threshold*2, false)
	default:
		editor2DHandleFaceClick(lvl, ch, wx, wz, threshold, false)
	}
}
