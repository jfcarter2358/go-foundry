package main

// editor_2d.go — top-down orthographic sector editor.
//
// Points are stored in the chunk's shared pool (Chunk.Points).
// Sectors reference them via PointIndices.
//
// Shift+click  : on a sector edge, insert a vertex on that edge — the edge is
//                split into two halves that both inherit the original wall's
//                (or portal's) properties, and every sector sharing the edge is
//                split with it. Away from any edge, places a new free point.
// Ctrl+click   : add everything under the cursor to the selection (point and
//                edge modes). Ctrl+clicking around an outline and pressing J
//                joins the picked points into edges — see editor_2d_join.go.
// Right-click  : open the context menu (editor_2d_context_menu.go), selecting
//                whatever is under the cursor first when nothing is selected
// Left-click   : select whatever the current mode selects — a corner, an edge,
//                or a wall/object/sector — and drag a single corner
// Middle-mouse : pan the view

import (
	"fmt"
	"log"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const editor2DSelectRadius = float32(8.0) // screen pixels for click proximity

// Editor2DRender draws the 2D top-down map using the gizmo line renderer.
func Editor2DRender() {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveChunkIdx < 0 || Editor.ActiveChunkIdx >= len(lvl.Chunks) {
		return
	}
	ch := &lvl.Chunks[Editor.ActiveChunkIdx]
	ortho := editor2DOrtho()

	// Draw background grid before any geometry
	drawEditorGrid(editor2DView())

	white := mgl32.Vec3{1, 1, 1}
	cyan := mgl32.Vec3{0, 1, 1}
	yellow := mgl32.Vec3{1, 1, 0}
	blue := mgl32.Vec3{0.3, 0.5, 1}
	red := mgl32.Vec3{1, 0.3, 0.3}
	green := mgl32.Vec3{0.2, 0.9, 0.2}
	grey := mgl32.Vec3{0.5, 0.5, 0.5}

	// Track which chunk points are used by any sector
	usedPoints := make(map[int]bool)

	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		nPts := len(s.PointIndices)
		if nPts < 2 {
			continue
		}
		for _, idx := range s.PointIndices {
			usedPoints[idx] = true
		}

		// Draw floor edges (walls = white, portals = cyan, selected = yellow)
		for ei := 0; ei < nPts; ei++ {
			a := ch.Points[s.PointIndices[ei]]
			b := ch.Points[s.PointIndices[(ei+1)%nPts]]
			pa := mgl32.Vec3{a.X(), s.Floor.Height, a.Y()}
			pb := mgl32.Vec3{b.X(), s.Floor.Height, b.Y()}

			kind := "wall"
			if ei < len(s.EdgeKinds) {
				kind = s.EdgeKinds[ei]
			}
			_, kindIdx := edgeKindAndIndex(s, ei)
			isSelected := si == Editor.ActiveSectorIdx &&
				Editor.ActiveEdgeKind == kind &&
				Editor.ActiveEdgeIdx == kindIdx

			// Check visibility of the underlying wall/portal
			visible := true
			if kind == "wall" && kindIdx < len(s.Walls) {
				visible = s.Walls[kindIdx].Visible
			} else if kind == "portal" && kindIdx < len(s.Portals) {
				visible = s.Portals[kindIdx].Visible
			}

			col := white
			if kind == "portal" {
				col = cyan
			}
			if !visible {
				col = grey
			}
			if isSelected {
				col = yellow
			}
			GizmoLine(pa, pb, col)
		}

		// Draw floor vertex dots (red crosses) — bright when editing floor, dim otherwise
		floorCol := red
		if !Editor.EditFloorVerts {
			floorCol = mgl32.Vec3{0.5, 0.15, 0.15} // dim red
		}
		for vi, pIdx := range s.PointIndices {
			pt := ch.Points[pIdx]
			col := floorCol
			// Every selected corner is marked, not merely the one being
			// dragged: a multi-selection the gizmo is about to move has to be
			// visible as the group it is.
			if editor2DVertexSelected(si, vi, pIdx, true) {
				col = yellow
			}
			if si == Editor.ActiveSectorIdx && vi == Editor.DragVertexVI && Editor.DragIsFloor {
				col = yellow
			}
			y := s.Floor.VertexHeight(vi)
			gp := mgl32.Vec3{pt.X(), y, pt.Y()}
			crossSz := screenToWorldSize(4, ortho)
			GizmoLine(gp.Sub(mgl32.Vec3{crossSz, 0, 0}), gp.Add(mgl32.Vec3{crossSz, 0, 0}), col)
			GizmoLine(gp.Sub(mgl32.Vec3{0, 0, crossSz}), gp.Add(mgl32.Vec3{0, 0, crossSz}), col)
		}

		// Draw ceiling vertex dots (blue crosses) — bright when editing ceiling, dim otherwise
		if s.Ceiling.Enabled {
			ceilCol := blue
			if Editor.EditFloorVerts {
				ceilCol = mgl32.Vec3{0.15, 0.25, 0.5} // dim blue
			}
			for vi, pIdx := range s.PointIndices {
				pt := ch.Points[pIdx]
				col := ceilCol
				if editor2DVertexSelected(si, vi, pIdx, false) {
					col = yellow
				}
				if si == Editor.ActiveSectorIdx && vi == Editor.DragVertexVI && !Editor.DragIsFloor {
					col = yellow
				}
				y := s.Ceiling.VertexHeight(vi)
				gp := mgl32.Vec3{pt.X(), y, pt.Y()}
				crossSz := screenToWorldSize(4, ortho)
				GizmoLine(gp.Sub(mgl32.Vec3{crossSz * 1.4, 0, 0}), gp.Add(mgl32.Vec3{crossSz * 1.4, 0, 0}), col)
				GizmoLine(gp.Sub(mgl32.Vec3{0, 0, crossSz * 1.4}), gp.Add(mgl32.Vec3{0, 0, crossSz * 1.4}), col)
			}
		}
	}

	// Optionally draw a box around every point showing its click-proximity
	// radius, so points can be found even when hidden behind geometry or
	// packed too closely to tell apart.
	if Editor.ShowPointSelectRadius {
		magenta := mgl32.Vec3{1, 0.2, 1}
		r := screenToWorldSize(editor2DSelectRadius, ortho)
		for _, pt := range ch.Points {
			x, z := pt.X(), pt.Y()
			c00 := mgl32.Vec3{x - r, 0, z - r}
			c10 := mgl32.Vec3{x + r, 0, z - r}
			c11 := mgl32.Vec3{x + r, 0, z + r}
			c01 := mgl32.Vec3{x - r, 0, z + r}
			GizmoLine(c00, c10, magenta)
			GizmoLine(c10, c11, magenta)
			GizmoLine(c11, c01, magenta)
			GizmoLine(c01, c00, magenta)
		}
	}

	// Draw free points (points in no sector) as white crosses, yellow when
	// selected — a picked free point is a corner of the sector a Join is about
	// to make, so it has to be as visible as a sector's own corners are.
	for pi, pt := range ch.Points {
		if usedPoints[pi] {
			continue
		}
		col := white
		if isVertexSelected(freePointSel(Editor.ActiveChunkIdx, pi)) {
			col = yellow
		}
		gp := mgl32.Vec3{pt.X(), 0, pt.Y()}
		crossSz := screenToWorldSize(5, ortho)
		GizmoLine(gp.Sub(mgl32.Vec3{crossSz, 0, 0}), gp.Add(mgl32.Vec3{crossSz, 0, 0}), col)
		GizmoLine(gp.Sub(mgl32.Vec3{0, 0, crossSz}), gp.Add(mgl32.Vec3{0, 0, crossSz}), col)
	}

	// Draw pending edges as green lines
	for _, e := range Editor.PendingEdges {
		if e[0] < len(ch.Points) && e[1] < len(ch.Points) {
			a := ch.Points[e[0]]
			b := ch.Points[e[1]]
			GizmoLine(mgl32.Vec3{a.X(), 0, a.Y()}, mgl32.Vec3{b.X(), 0, b.Y()}, green)
		}
	}

	// Objects the side panel can edit — the spawn point, the point lights.
	Editor2DRenderObjects(lvl, ch)

	// Biome sector layer (always visible, dim when not the active tool).
	Editor2DRenderBiomeSectors(ch)

	// Last, so its handles sit on top of everything they act on.
	mx, my := editor2DCursor()
	Editor2DRenderGizmo(mx, my)

	GizmoFlush(ortho)
}

// Editor2DRenderLabels draws sector name labels as an ImGui overlay.
// Must be called within an ImGui NewFrame/Render bracket.
func Editor2DRenderLabels() {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveChunkIdx < 0 || Editor.ActiveChunkIdx >= len(lvl.Chunks) {
		return
	}
	ch := &lvl.Chunks[Editor.ActiveChunkIdx]

	drawList := imgui.ForegroundDrawList()
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if len(s.PointIndices) < 1 {
			continue
		}
		// Compute centroid
		var cx, cz float32
		for _, pIdx := range s.PointIndices {
			pt := ch.Points[pIdx]
			cx += pt.X()
			cz += pt.Y()
		}
		n := float32(len(s.PointIndices))
		cx /= n
		cz /= n
		sx, sy := worldToScreen(cx, cz)
		drawList.AddText(imgui.Vec2{X: sx - 20, Y: sy - 6}, imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 1, Z: 0.5, W: 0.9}), s.ID)
	}
}

// editor2DView returns the current 2D map's orthoView (see
// editor_orthoview.go), sized to whichever window the map is being drawn
// into — the main window, or the popped-out 2D window when one is open (see
// editor2DViewportSize in editor_2d_window.go).
func editor2DView() orthoView {
	vw, vh := editor2DViewportSize()
	return orthoView{pan: Editor.Pan2D, zoom: Editor.Zoom2D, viewW: vw, viewH: vh}
}

// editor2DOrtho returns the orthographic projection×view matrix for the 2D map.
func editor2DOrtho() mgl32.Mat4 {
	return editor2DView().ortho()
}

// screenToWorldSize converts a pixel distance to world units at current zoom.
func screenToWorldSize(screenPx float32, _ mgl32.Mat4) float32 {
	return editor2DView().screenToWorldSize(screenPx)
}

// worldToScreen converts a world XZ point to screen pixel coordinates.
func worldToScreen(wx, wz float32) (sx, sy float32) {
	return editor2DView().worldToScreen(wx, wz)
}

// screenToWorld converts screen pixels to world XZ.
func screenToWorld(sx, sy int32) (wx, wz float32) {
	return editor2DView().screenToWorld(sx, sy)
}

// findChunkPointNear returns the index of the chunk point nearest to (wx,wz)
// within threshold, or -1.
func findChunkPointNear(ch *Chunk, wx, wz, threshold float32) int {
	bestIdx := -1
	bestDist := threshold
	for i, pt := range ch.Points {
		d := dist2D(wx, wz, pt.X(), pt.Y())
		if d < bestDist {
			bestDist = d
			bestIdx = i
		}
	}
	return bestIdx
}

// Editor2DZoom applies one mouse-wheel notch of zoom to the 2D map, clamped
// to a usable range. Shared by the main window's event handler (editor.go)
// and the pop-out's (editor_2d_window.go).
func Editor2DZoom(wheelY int32) {
	Editor.Zoom2D *= float32(math.Pow(1.1, float64(wheelY)))
	if Editor.Zoom2D < 2 {
		Editor.Zoom2D = 2
	}
	if Editor.Zoom2D > 200 {
		Editor.Zoom2D = 200
	}
}

// Editor2DHandleClick processes a mouse button down event in 2D mode.
func Editor2DHandleClick(t sdl.MouseButtonEvent) {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveChunkIdx < 0 || Editor.ActiveChunkIdx >= len(lvl.Chunks) {
		return
	}
	ch := &lvl.Chunks[Editor.ActiveChunkIdx]
	wx, wz := screenToWorld(t.X, t.Y)
	mods := sdl.GetModState()
	shiftHeld := mods&sdl.KMOD_SHIFT != 0
	ctrlHeld := mods&sdl.KMOD_CTRL != 0

	// An armed placement is an explicit request and outranks every other
	// tool, including the biome brush.
	if editor2DHandlePlacement(t, ch, wx, wz) {
		return
	}

	// Biome brush intercepts clicks (except middle-mouse pan).
	if biomeHandleClick(t) {
		return
	}

	// The gizmo's handles are drawn on top of everything and sit right where
	// the selection's own marks are, so a click aimed at one must be resolved
	// before picking gets a look at it.
	//
	// Only an unmodified click, though. Shift+click drops a point and ctrl+click
	// adds to the selection, and both are aimed at points that may well sit
	// under the gizmo — a handle that swallowed those would make it impossible
	// to extend a selection near its own centre.
	if !shiftHeld && !ctrlHeld && editor2DGizmoMouseDown(t) {
		return
	}

	threshold := screenToWorldSize(editor2DSelectRadius, editor2DOrtho())

	// --- Right-click: the context menu ---
	//
	// It used to delete the point under the cursor outright, and clear the
	// selection when it hit nothing. Both are still here, as rows — the menu is
	// where "join these points" had to live, and one button cannot both open a
	// menu and destroy geometry without asking.
	if t.Button == sdl.ButtonRight {
		editor2DContextPressed(t.X, t.Y)
		return
	}

	if t.Button == sdl.ButtonMiddle {
		Editor.LastMouse2D = [2]int32{t.X, t.Y}
		return
	}

	if t.Button != sdl.ButtonLeft {
		return
	}

	// --- Shift+click: split the nearest edge, else place a new free point ---
	if shiftHeld {
		if splitSectorEdgeNear(ch, wx, wz, threshold*2) {
			return
		}
		EditorPushUndo()
		ch.AddPoint(mgl32.Vec2{wx, wz})
		return
	}

	// --- Left-click: select whatever the current mode selects ---
	//
	// Ctrl adds to the selection here, as it does in the 3D view. It used to
	// draw an edge to the last point clicked; that is now two steps — pick the
	// points, then Join them (editor_2d_join.go) — which frees the modifier and
	// lets a half-picked outline be looked at before it is committed to.

	// Point and edge mode own their click outright, exactly as in the 3D
	// viewport: hitting nothing there clears the selection rather than falling
	// through to the sector behind, which is what makes the modes predictable.
	switch Editor.SelMode {
	case editorSelPoint:
		if !editor2DHandlePointClick(ch, wx, wz, threshold, ctrlHeld, true) && !ctrlHeld {
			editor2DClearSelection()
		}
		return
	case editorSelEdge:
		if !editor2DHandleEdgeClick(ch, wx, wz, threshold*2, ctrlHeld) && !ctrlHeld {
			editor2DClearSelection()
		}
		return
	}

	if !editor2DHandleFaceClick(lvl, ch, wx, wz, threshold, ctrlHeld) && !ctrlHeld {
		editor2DClearSelection()
	}
}

// Editor2DHandleRelease processes mouse button release to stop vertex dragging.
func Editor2DHandleRelease(t sdl.MouseButtonEvent) {
	biomeHandleRelease(t)
	if t.Button == sdl.ButtonLeft {
		editor2DGizmoMouseUp()
		Editor.DragVertexSI = -1
		Editor.DragVertexVI = -1
	}
}

// Editor2DHandleMouseMove processes mouse motion in 2D mode.
func Editor2DHandleMouseMove(t sdl.MouseMotionEvent) {
	lvl := Editor.level
	if lvl == nil {
		return
	}

	// A gizmo drag owns the mouse until release — before the pan check, so a
	// stray middle button cannot slide the map out from under it.
	if editor2DGizmoDragging() {
		Editor.LastMouse2D = [2]int32{t.X, t.Y}
		editor2DGizmoMouseMove(t.X, t.Y)
		return
	}

	// Middle-mouse pan
	if t.State&sdl.ButtonMMask != 0 {
		dx := float32(t.X-Editor.LastMouse2D[0]) / Editor.Zoom2D
		dy := float32(t.Y-Editor.LastMouse2D[1]) / Editor.Zoom2D
		Editor.Pan2D[0] -= dx
		Editor.Pan2D[1] -= dy
	}
	Editor.LastMouse2D = [2]int32{t.X, t.Y}

	// Biome brush: track cursor and paint while the button is held.
	biomeHandleMouseMove(t)

	// Vertex drag
	if Editor.DragVertexSI >= 0 && Editor.DragVertexVI >= 0 &&
		Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks) {
		ch := &lvl.Chunks[Editor.ActiveChunkIdx]
		if Editor.DragVertexSI < len(ch.Sectors) {
			s := &ch.Sectors[Editor.DragVertexSI]
			wx, wz := screenToWorld(t.X, t.Y)
			vi := Editor.DragVertexVI
			if Editor.DragIsFloor {
				if vi < len(s.PointIndices) {
					pIdx := s.PointIndices[vi]
					was := ch.Points[pIdx]
					ch.Points[pIdx] = mgl32.Vec2{wx, wz}
					carryBiomesWithPoints(ch, []pointMove{{pointIdx: pIdx, from: was}})
					// Rebuild all sectors that share this point
					reloadSectorsUsingPoint(ch, pIdx)
				}
			}
		}
	}
}

// rebuildSectorEdgePoints re-derives wall/portal StartPoint/EndPoint from chunk points,
// using s.EdgeKinds to correctly handle mixed wall/portal sectors.
func rebuildSectorEdgePoints(ch *Chunk, s *Sector) {
	nPts := len(s.PointIndices)
	wallIdx, portalIdx := 0, 0
	for ei := 0; ei < nPts; ei++ {
		a := ch.Points[s.PointIndices[ei]]
		b := ch.Points[s.PointIndices[(ei+1)%nPts]]
		if ei < len(s.EdgeKinds) && s.EdgeKinds[ei] == "portal" {
			if portalIdx < len(s.Portals) {
				s.Portals[portalIdx].StartPoint = a
				s.Portals[portalIdx].EndPoint = b
				portalIdx++
			}
		} else {
			if wallIdx < len(s.Walls) {
				s.Walls[wallIdx].StartPoint = a
				s.Walls[wallIdx].EndPoint = b
				wallIdx++
			}
		}
	}
}

// reloadSectorsUsingPoint rebuilds edge points and geometry for all sectors
// in the chunk that reference the given chunk point index, then regenerates
// portal gap-fill geometry so it follows the moved vertex.
func reloadSectorsUsingPoint(ch *Chunk, pointIdx int) {
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		for _, pIdx := range s.PointIndices {
			if pIdx == pointIdx {
				rebuildSectorEdgePoints(ch, s)
				_ = reloadSector(ch, s)
				break
			}
		}
	}
	ch.RebuildGaps()
}

// minSplitSegLen is the shortest wall half (world units) a split may produce.
// Splitting closer than this to an existing vertex would leave a zero-length
// wall whose endpoints alias its neighbour's, which Sector.Load resolves by
// position and would then mis-match.
const minSplitSegLen = float32(0.01)

// edgeSplitRef names one sector edge to split, with t measured in that sector's
// own winding order.
type edgeSplitRef struct {
	si, ei int
	t      float32
}

// edgeSplitPlan is a resolved edge-insertion: where the new vertex goes, which
// sector edges it splits, and which edge selection survives it. kind/kindIdx
// describe the clicked edge as seen from sector si.
type edgeSplitPlan struct {
	newPt   mgl32.Vec2
	refs    []edgeSplitRef
	si      int
	kind    string
	kindIdx int
}

// planEdgeSplit resolves the sector edge nearest to (wx,wz) within threshold
// into an edgeSplitPlan, or reports false if there is no edge close enough (or
// the split would land on top of an existing vertex).
//
// The new vertex is snapped onto the edge so the sector outline keeps its exact
// shape. Every sector that walks the same pair of chunk points is included: a
// portal shared by two sectors is only recognised while both sides still have
// matching reverse edges, so splitting one side alone would make InferPortals
// demote both halves back to walls.
func planEdgeSplit(ch *Chunk, wx, wz, threshold float32) (edgeSplitPlan, bool) {
	// Nearest edge across all sectors, not merely the first within threshold.
	bestSI, bestEI := -1, -1
	bestDist := threshold
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		nPts := len(s.PointIndices)
		if nPts < 3 {
			continue
		}
		for ei := 0; ei < nPts; ei++ {
			a := ch.Points[s.PointIndices[ei]]
			b := ch.Points[s.PointIndices[(ei+1)%nPts]]
			if d := distToSegment2D(wx, wz, a.X(), a.Y(), b.X(), b.Y()); d < bestDist {
				bestDist, bestSI, bestEI = d, si, ei
			}
		}
	}
	if bestSI < 0 {
		return edgeSplitPlan{}, false
	}

	best := &ch.Sectors[bestSI]
	nBest := len(best.PointIndices)
	pIdxA := best.PointIndices[bestEI]
	pIdxB := best.PointIndices[(bestEI+1)%nBest]
	a := ch.Points[pIdxA]
	b := ch.Points[pIdxB]

	edgeLen := b.Sub(a).Len()
	t := segmentParam2D(wx, wz, a.X(), a.Y(), b.X(), b.Y())
	if edgeLen < 2*minSplitSegLen || t*edgeLen < minSplitSegLen || (1-t)*edgeLen < minSplitSegLen {
		return edgeSplitPlan{}, false
	}

	// One ref per sector, so the recorded positions all stay valid once the
	// first insert shifts indices.
	var refs []edgeSplitRef
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		nPts := len(s.PointIndices)
		for ei := 0; ei < nPts; ei++ {
			q0 := s.PointIndices[ei]
			q1 := s.PointIndices[(ei+1)%nPts]
			if q0 == pIdxA && q1 == pIdxB {
				refs = append(refs, edgeSplitRef{si, ei, t})
			} else if q0 == pIdxB && q1 == pIdxA {
				// Opposite winding, so the split sits at 1-t along this edge.
				refs = append(refs, edgeSplitRef{si, ei, 1 - t})
			} else {
				continue
			}
			break
		}
	}

	kind, kindIdx := edgeKindAndIndex(best, bestEI)
	return edgeSplitPlan{
		newPt:   a.Add(b.Sub(a).Mul(t)),
		refs:    refs,
		si:      bestSI,
		kind:    kind,
		kindIdx: kindIdx,
	}, true
}

// applyEdgeSplit adds the plan's vertex to the chunk point pool and splits every
// edge the plan names. Geometry is left stale; the caller reloads.
func applyEdgeSplit(ch *Chunk, plan edgeSplitPlan) int {
	newPtIdx := ch.AddPoint(plan.newPt)
	for _, r := range plan.refs {
		if r.si < 0 || r.si >= len(ch.Sectors) {
			continue
		}
		insertSectorVertex(ch, &ch.Sectors[r.si], r.ei, newPtIdx, r.t)
	}
	return newPtIdx
}

// splitSectorEdgeNear inserts a new vertex on the sector edge nearest to
// (wx,wz) within threshold, splitting that edge into two halves that both
// inherit the split wall's (or portal's) properties. Reports whether a split
// happened; pushes undo only in that case.
func splitSectorEdgeNear(ch *Chunk, wx, wz, threshold float32) bool {
	plan, ok := planEdgeSplit(ch, wx, wz, threshold)
	if !ok {
		return false
	}

	EditorPushUndo()
	newPtIdx := applyEdgeSplit(ch, plan)
	reloadSectorsUsingPoint(ch, newPtIdx)

	// The first half keeps the split edge's wall/portal index, so leave that
	// selected; vertex positions after the insert have shifted, so drop those.
	Editor.ActiveSectorIdx = plan.si
	Editor.ActiveEdgeKind = plan.kind
	Editor.ActiveEdgeIdx = plan.kindIdx
	clearVertexSelection()
	return true
}

// insertSectorVertex inserts a chunk point (already in ch.Points at newPtIdx)
// into a sector at edge position ei, splitting the edge. t is where the new
// point sits along the edge (0 = edge start, 1 = edge end) in this sector's
// winding order. Geometry is left stale — the caller must reload the sector.
func insertSectorVertex(ch *Chunk, s *Sector, ei, newPtIdx int, t float32) {
	nPts := len(s.PointIndices)
	if ei < 0 || ei >= nPts || newPtIdx < 0 || newPtIdx >= len(ch.Points) {
		return
	}
	// EdgeKinds is meant to be index-aligned with PointIndices; pad a malformed
	// sector rather than bail, so the split still lands.
	for len(s.EdgeKinds) < nPts {
		s.EdgeKinds = append(s.EdgeKinds, "wall")
	}

	// Split the wall/portal covering edge ei while the indices are still the
	// pre-insert ones.
	newPt := ch.Points[newPtIdx]
	wKind, wIdx := edgeKindAndIndex(s, ei)
	switch {
	case wKind == "wall" && wIdx < len(s.Walls):
		first, second := splitWall(s.Walls[wIdx], newPt, t)
		s.Walls = append(s.Walls, Wall{})
		copy(s.Walls[wIdx+2:], s.Walls[wIdx+1:])
		s.Walls[wIdx] = first
		s.Walls[wIdx+1] = second
	case wKind == "portal" && wIdx < len(s.Portals):
		first, second := splitPortal(s.Portals[wIdx], newPt)
		s.Portals = append(s.Portals, Portal{})
		copy(s.Portals[wIdx+2:], s.Portals[wIdx+1:])
		s.Portals[wIdx] = first
		s.Portals[wIdx+1] = second
	default:
		log.Printf("insertSectorVertex: sector %q edge %d has kind %q with no matching wall/portal entry; skipping split", s.ID, ei, wKind)
		return
	}

	// Insert into PointIndices
	newIndices := make([]int, nPts+1)
	copy(newIndices, s.PointIndices[:ei+1])
	newIndices[ei+1] = newPtIdx
	copy(newIndices[ei+2:], s.PointIndices[ei+1:])
	s.PointIndices = newIndices

	// Extend VertexOffsets: lerp the new vertex's offset between its neighbours
	// at t so the floor/ceiling surface through the split edge is unchanged.
	interpOffset := func(offsets []float32) []float32 {
		// Offsets are meant to be index-aligned with PointIndices; pad a short
		// slice so the insert below can't run off the end.
		for len(offsets) < nPts {
			offsets = append(offsets, 0)
		}
		prevOff := offsets[ei]
		nextOff := offsets[(ei+1)%nPts]
		newOff := make([]float32, len(offsets)+1)
		copy(newOff, offsets[:ei+1])
		newOff[ei+1] = prevOff + (nextOff-prevOff)*t
		copy(newOff[ei+2:], offsets[ei+1:])
		return newOff
	}
	s.Floor.VertexOffsets = interpOffset(s.Floor.VertexOffsets)
	s.Ceiling.VertexOffsets = interpOffset(s.Ceiling.VertexOffsets)

	// Insert EdgeKind for the new edge (same kind as the edge being split)
	newKinds := make([]string, len(s.EdgeKinds)+1)
	copy(newKinds, s.EdgeKinds[:ei+1])
	newKinds[ei+1] = s.EdgeKinds[ei]
	copy(newKinds[ei+2:], s.EdgeKinds[ei+1:])
	s.EdgeKinds = newKinds
}

// splitWall divides w at newPt, which must lie on the segment at parameter t
// (0 = StartPoint, 1 = EndPoint). Both halves inherit every property of the
// original — texture, collision, visibility, pinning, UV transform, procedural
// detail — so the split is invisible until one half is edited. Extrusions and
// terrain points go to whichever half now covers them.
func splitWall(w Wall, newPt mgl32.Vec2, t float32) (first, second Wall) {
	splitU := w.EndPoint.Sub(w.StartPoint).Len() * t

	first = w
	second = w
	// Geometry holds live GL handles; both halves get fresh ones from Load.
	first.Geometry = Geometry{}
	first.ExtrusionGeometries = nil
	second.Geometry = Geometry{}
	second.ExtrusionGeometries = nil

	first.EndPoint = newPt
	second.StartPoint = newPt

	// Wall U/V is measured in world units from StartPoint (see
	// buildWallWithExtrusions), so the second half's features shift back by the
	// split distance rather than being rescaled.
	first.Extrusions, second.Extrusions = splitExtrusionsAtU(w.Extrusions, splitU)
	first.TerrainPoints, second.TerrainPoints = splitTerrainPointsAtU(w.TerrainPoints, splitU)
	return first, second
}

// splitPortal divides p at newPt. Both halves keep the same target sector and
// gap-fill settings; gap geometry is regenerated by Chunk.RebuildGaps.
func splitPortal(p Portal, newPt mgl32.Vec2) (first, second Portal) {
	first = p
	second = p
	first.FloorGap = Geometry{}
	first.CeilGap = Geometry{}
	second.FloorGap = Geometry{}
	second.CeilGap = Geometry{}

	first.EndPoint = newPt
	second.StartPoint = newPt
	return first, second
}

// splitExtrusionsAtU distributes a wall's extrusions between the two halves of
// a wall split at splitU units from StartPoint. An extrusion that straddles the
// split can't span two walls, so each half gets the part of it on that side,
// clamped to the seam; U on the second half is rebased to its own StartPoint.
func splitExtrusionsAtU(exts []Extrusion, splitU float32) (first, second []Extrusion) {
	for _, ext := range exts {
		minU, maxU := extrusionURange(ext)
		switch {
		case maxU <= splitU:
			first = append(first, ext)
		case minU >= splitU:
			second = append(second, offsetExtrusionU(ext, -splitU))
		default:
			// Skip a sliver too thin to trianglate into anything meaningful.
			if splitU-minU > minSplitSegLen {
				first = append(first, clampExtrusionU(ext, minU, splitU))
			}
			if maxU-splitU > minSplitSegLen {
				second = append(second, offsetExtrusionU(clampExtrusionU(ext, splitU, maxU), -splitU))
			}
		}
	}
	return first, second
}

// extrusionURange returns the min and max U over an extrusion's outer and inner
// rings. An extrusion with no points reports a degenerate range at 0.
func extrusionURange(ext Extrusion) (minU, maxU float32) {
	seen := false
	visit := func(pts []ExtrusionPoint) {
		for _, p := range pts {
			if !seen || p.U < minU {
				minU = p.U
			}
			if !seen || p.U > maxU {
				maxU = p.U
			}
			seen = true
		}
	}
	visit(ext.Outer)
	visit(ext.Inner)
	return minU, maxU
}

// offsetExtrusionU shifts every U coordinate of an extrusion by delta.
func offsetExtrusionU(ext Extrusion, delta float32) Extrusion {
	out := ext
	shift := func(pts []ExtrusionPoint) []ExtrusionPoint {
		moved := make([]ExtrusionPoint, len(pts))
		for i, p := range pts {
			moved[i] = ExtrusionPoint{U: p.U + delta, V: p.V}
		}
		return moved
	}
	out.Outer = shift(ext.Outer)
	out.Inner = shift(ext.Inner)
	return out
}

// clampExtrusionU clamps every U coordinate of an extrusion into [lo,hi],
// keeping the vertex count (Outer and Inner must stay index-aligned).
func clampExtrusionU(ext Extrusion, lo, hi float32) Extrusion {
	out := ext
	clamp := func(pts []ExtrusionPoint) []ExtrusionPoint {
		clamped := make([]ExtrusionPoint, len(pts))
		for i, p := range pts {
			u := p.U
			if u < lo {
				u = lo
			}
			if u > hi {
				u = hi
			}
			clamped[i] = ExtrusionPoint{U: u, V: p.V}
		}
		return clamped
	}
	out.Outer = clamp(ext.Outer)
	out.Inner = clamp(ext.Inner)
	return out
}

// splitTerrainPointsAtU distributes a wall's terrain points between the two
// halves of a wall split at splitU units from StartPoint, rebasing U for the
// second half. A point sitting on the seam goes to both halves so the two
// displacement meshes agree there.
func splitTerrainPointsAtU(pts []TerrainPoint, splitU float32) (first, second []TerrainPoint) {
	for _, p := range pts {
		if p.U <= splitU+minSplitSegLen {
			first = append(first, p)
		}
		if p.U >= splitU-minSplitSegLen {
			second = append(second, TerrainPoint{U: p.U - splitU, V: p.V, Depth: p.Depth})
		}
	}
	return first, second
}

// edgeKindAndIndex returns the edge type and its index within Walls or Portals
// for polygon edge ei, using EdgeKinds for reliable mixed-type lookup.
func edgeKindAndIndex(s *Sector, ei int) (string, int) {
	if ei < 0 || ei >= len(s.EdgeKinds) {
		return "wall", 0
	}
	kind := s.EdgeKinds[ei]
	idx := 0
	for i := 0; i < ei; i++ {
		if i < len(s.EdgeKinds) && s.EdgeKinds[i] == kind {
			idx++
		}
	}
	return kind, idx
}

// sectorVertexPos returns the position within s.PointIndices that holds chunk point index pi, or -1.
func sectorVertexPos(s *Sector, pi int) int {
	for i, idx := range s.PointIndices {
		if idx == pi {
			return i
		}
	}
	return -1
}

// adjacentInSector checks if two positions in a sector's PointIndices are adjacent (including wrap).
func adjacentInSector(s *Sector, posA, posB int) bool {
	n := len(s.PointIndices)
	if n < 2 {
		return true
	}
	d := posA - posB
	if d < 0 {
		d = -d
	}
	return d == 1 || d == n-1
}

// splitSector splits sector at sectorIdx into two sectors along a new edge
// between vertices at positions posA and posB within the sector's PointIndices.
func splitSector(ch *Chunk, sectorIdx, posA, posB int) {
	s := &ch.Sectors[sectorIdx]
	n := len(s.PointIndices)
	if posA > posB {
		posA, posB = posB, posA
	}

	// Sector A: posA..posB (inclusive)
	var indicesA []int
	var edgeKindsA []string
	var wallsA []Wall
	var portalsA []Portal
	for i := posA; i <= posB; i++ {
		indicesA = append(indicesA, s.PointIndices[i])
	}
	for i := posA; i < posB; i++ {
		ek := s.EdgeKinds[i]
		edgeKindsA = append(edgeKindsA, ek)
		kind, kIdx := edgeKindAndIndex(s, i)
		if kind == "wall" && kIdx < len(s.Walls) {
			wallsA = append(wallsA, s.Walls[kIdx])
		} else if kind == "portal" && kIdx < len(s.Portals) {
			portalsA = append(portalsA, s.Portals[kIdx])
		}
	}
	// New closing edge for sector A (posB → posA): wall
	edgeKindsA = append(edgeKindsA, "wall")
	ptB := ch.Points[s.PointIndices[posB]]
	ptA := ch.Points[s.PointIndices[posA]]
	wallsA = append(wallsA, Wall{StartPoint: ptB, EndPoint: ptA, Texture: "concrete", Collision: true, Visible: true, DoubleSided: true, Alpha: 1.0})

	// Sector B: posB..n-1, 0..posA (wrapping, inclusive)
	var indicesB []int
	var edgeKindsB []string
	var wallsB []Wall
	var portalsB []Portal
	for i := posB; i < n; i++ {
		indicesB = append(indicesB, s.PointIndices[i])
	}
	for i := 0; i <= posA; i++ {
		indicesB = append(indicesB, s.PointIndices[i])
	}
	// Edges for B: posB..n-1 (wrap at n-1→0), 0..posA-1, then new closing edge
	for i := posB; i < n; i++ {
		ei := i % n
		ek := s.EdgeKinds[ei]
		edgeKindsB = append(edgeKindsB, ek)
		kind, kIdx := edgeKindAndIndex(s, ei)
		if kind == "wall" && kIdx < len(s.Walls) {
			wallsB = append(wallsB, s.Walls[kIdx])
		} else if kind == "portal" && kIdx < len(s.Portals) {
			portalsB = append(portalsB, s.Portals[kIdx])
		}
	}
	for i := 0; i < posA; i++ {
		ek := s.EdgeKinds[i]
		edgeKindsB = append(edgeKindsB, ek)
		kind, kIdx := edgeKindAndIndex(s, i)
		if kind == "wall" && kIdx < len(s.Walls) {
			wallsB = append(wallsB, s.Walls[kIdx])
		} else if kind == "portal" && kIdx < len(s.Portals) {
			portalsB = append(portalsB, s.Portals[kIdx])
		}
	}
	// New closing edge for sector B (posA → posB): wall
	edgeKindsB = append(edgeKindsB, "wall")
	wallsB = append(wallsB, Wall{StartPoint: ptA, EndPoint: ptB, Texture: "concrete", Collision: true, Visible: true, DoubleSided: true, Alpha: 1.0})

	// Distribute vertex offsets
	floorOffsA := distributeOffsets(s.Floor.VertexOffsets, posA, posB, n)
	ceilOffsA := distributeOffsets(s.Ceiling.VertexOffsets, posA, posB, n)
	floorOffsB := distributeOffsetsWrap(s.Floor.VertexOffsets, posB, posA, n)
	ceilOffsB := distributeOffsetsWrap(s.Ceiling.VertexOffsets, posB, posA, n)

	sectorA := Sector{
		ID:           s.ID,
		PointIndices: indicesA,
		EdgeKinds:    edgeKindsA,
		Walls:        wallsA,
		Portals:      portalsA,
		Floor:        Floor{Enabled: s.Floor.Enabled, Hidden: s.Floor.Hidden, Height: s.Floor.Height, Texture: s.Floor.Texture, Terrain: s.Floor.Terrain, DoubleSided: s.Floor.DoubleSided, Alpha: s.Floor.Alpha, VertexOffsets: floorOffsA},
		Ceiling:      Ceiling{Enabled: s.Ceiling.Enabled, Height: s.Ceiling.Height, Texture: s.Ceiling.Texture, Terrain: s.Ceiling.Terrain, DoubleSided: s.Ceiling.DoubleSided, Alpha: s.Ceiling.Alpha, VertexOffsets: ceilOffsA},
	}
	sectorB := Sector{
		ID:           s.ID + "_split",
		PointIndices: indicesB,
		EdgeKinds:    edgeKindsB,
		Walls:        wallsB,
		Portals:      portalsB,
		Floor:        Floor{Enabled: s.Floor.Enabled, Hidden: s.Floor.Hidden, Height: s.Floor.Height, Texture: s.Floor.Texture, Terrain: s.Floor.Terrain, DoubleSided: s.Floor.DoubleSided, Alpha: s.Floor.Alpha, VertexOffsets: floorOffsB},
		Ceiling:      Ceiling{Enabled: s.Ceiling.Enabled, Height: s.Ceiling.Height, Texture: s.Ceiling.Texture, Terrain: s.Ceiling.Terrain, DoubleSided: s.Ceiling.DoubleSided, Alpha: s.Ceiling.Alpha, VertexOffsets: ceilOffsB},
	}

	// Replace original sector with A, append B
	ch.Sectors[sectorIdx] = sectorA
	ch.Sectors = append(ch.Sectors, sectorB)
	ch.InferPortalsAndReload()
	Editor.ActiveSectorIdx = sectorIdx
}

// distributeOffsets extracts vertex offsets for a contiguous range [from..to] inclusive.
func distributeOffsets(offsets []float32, from, to, n int) []float32 {
	count := to - from + 1
	result := make([]float32, count)
	for i := 0; i < count; i++ {
		idx := from + i
		if idx < len(offsets) {
			result[i] = offsets[idx]
		}
	}
	return result
}

// distributeOffsetsWrap extracts vertex offsets for a wrapping range [from..n-1, 0..to] inclusive.
func distributeOffsetsWrap(offsets []float32, from, to, n int) []float32 {
	var result []float32
	for i := from; i < n; i++ {
		if i < len(offsets) {
			result = append(result, offsets[i])
		} else {
			result = append(result, 0)
		}
	}
	for i := 0; i <= to; i++ {
		if i < len(offsets) {
			result = append(result, offsets[i])
		} else {
			result = append(result, 0)
		}
	}
	return result
}

// removeChunkPoint removes a point from the chunk and all sectors/pending edges referencing it.
func removeChunkPoint(ch *Chunk, pi int) {
	// Remove from each sector that uses this point
	for si := len(ch.Sectors) - 1; si >= 0; si-- {
		s := &ch.Sectors[si]
		removeSectorPoint(ch, s, pi)
		// If the sector is degenerate (< 3 points), remove it
		if len(s.PointIndices) < 3 {
			ch.Sectors = append(ch.Sectors[:si], ch.Sectors[si+1:]...)
			if Editor.ActiveSectorIdx == si {
				Editor.ActiveSectorIdx = -1
			} else if Editor.ActiveSectorIdx > si {
				Editor.ActiveSectorIdx--
			}
			// Clear all stale selection indices that referenced the deleted sector
			Editor.ActiveEdgeIdx = -1
			clearVertexSelection()
			clearFaceSelection()
			Editor.DragVertexSI = -1
			Editor.DragVertexVI = -1
		}
	}

	// Remove pending edges referencing this point
	var kept [][2]int
	for _, e := range Editor.PendingEdges {
		if e[0] != pi && e[1] != pi {
			kept = append(kept, e)
		}
	}
	Editor.PendingEdges = kept

	// Remove the point from ch.Points and adjust all indices
	ch.RemovePoint(pi)
	// Adjust PointIndices in all sectors
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		for vi := range s.PointIndices {
			if s.PointIndices[vi] > pi {
				s.PointIndices[vi]--
			}
		}
	}
	// Adjust pending edges
	for i := range Editor.PendingEdges {
		if Editor.PendingEdges[i][0] > pi {
			Editor.PendingEdges[i][0]--
		}
		if Editor.PendingEdges[i][1] > pi {
			Editor.PendingEdges[i][1]--
		}
	}
	// Deselect vertices that might now be invalid
	clearVertexSelection()
	clearFaceSelection()
	Editor.DragVertexSI = -1
	Editor.DragVertexVI = -1
}

// removeSectorPoint removes chunk point pi from a sector's boundary, merging adjacent edges.
func removeSectorPoint(ch *Chunk, s *Sector, pi int) {
	pos := sectorVertexPos(s, pi)
	if pos < 0 {
		return
	}
	n := len(s.PointIndices)
	if n <= 3 {
		// Will be removed as degenerate by caller; just clear
		return
	}

	// The two edges meeting at pos are (pos-1→pos) and (pos→pos+1).
	// Merge them: remove pos from PointIndices, remove one EdgeKind entry,
	// remove the corresponding wall/portal.
	prevEdge := (pos - 1 + n) % n
	currEdge := pos

	// Remove the wall/portal for currEdge (pos→pos+1)
	if currEdge < len(s.EdgeKinds) {
		kind, kIdx := edgeKindAndIndex(s, currEdge)
		if kind == "wall" && kIdx < len(s.Walls) {
			s.Walls = append(s.Walls[:kIdx], s.Walls[kIdx+1:]...)
		} else if kind == "portal" && kIdx < len(s.Portals) {
			s.Portals = append(s.Portals[:kIdx], s.Portals[kIdx+1:]...)
		}
	}

	// Remove from EdgeKinds
	if currEdge < len(s.EdgeKinds) {
		s.EdgeKinds = append(s.EdgeKinds[:currEdge], s.EdgeKinds[currEdge+1:]...)
	}

	// Remove from PointIndices
	s.PointIndices = append(s.PointIndices[:pos], s.PointIndices[pos+1:]...)

	// Remove from VertexOffsets
	if pos < len(s.Floor.VertexOffsets) {
		s.Floor.VertexOffsets = append(s.Floor.VertexOffsets[:pos], s.Floor.VertexOffsets[pos+1:]...)
	}
	if pos < len(s.Ceiling.VertexOffsets) {
		s.Ceiling.VertexOffsets = append(s.Ceiling.VertexOffsets[:pos], s.Ceiling.VertexOffsets[pos+1:]...)
	}

	// Update the surviving edge's endpoints
	_ = prevEdge
	rebuildSectorEdgePoints(ch, s)
	_ = reloadSector(ch, s)
}

// tryCreateSectorFromPendingEdges checks if pending edges form a closed polygon
// and creates a new sector if so. Reports whether a sector was created.
func tryCreateSectorFromPendingEdges(ch *Chunk) bool {
	if len(Editor.PendingEdges) < 3 {
		return false
	}

	// Build adjacency from pending edges
	adj := map[int][]int{}
	for _, e := range Editor.PendingEdges {
		adj[e[0]] = append(adj[e[0]], e[1])
		adj[e[1]] = append(adj[e[1]], e[0])
	}

	// Try to find a cycle starting from the last added edge's first vertex
	lastEdge := Editor.PendingEdges[len(Editor.PendingEdges)-1]
	start := lastEdge[0]
	target := lastEdge[1]

	// DFS from target to find a path back to start (excluding direct edge)
	visited := map[int]bool{target: true, start: true}
	path := []int{target}
	if cycle := dfsFindPath(adj, path, start, visited); cycle != nil {
		// Found a cycle: start + cycle forms the polygon
		polyIndices := append([]int{start}, cycle...)

		// Remove the used edges from pending
		usedEdgeSet := make(map[[2]int]bool)
		for i := 0; i < len(polyIndices); i++ {
			a := polyIndices[i]
			b := polyIndices[(i+1)%len(polyIndices)]
			usedEdgeSet[[2]int{a, b}] = true
			usedEdgeSet[[2]int{b, a}] = true
		}
		var remaining [][2]int
		for _, e := range Editor.PendingEdges {
			if !usedEdgeSet[e] && !usedEdgeSet[[2]int{e[1], e[0]}] {
				remaining = append(remaining, e)
			}
		}
		Editor.PendingEdges = remaining

		// Create sector from polygon
		createSectorFromPolygon(ch, polyIndices)
		return true
	}
	return false
}

// dfsFindPath searches for a path from the last element of path to target through adj.
func dfsFindPath(adj map[int][]int, path []int, target int, visited map[int]bool) []int {
	current := path[len(path)-1]
	for _, next := range adj[current] {
		if next == target && len(path) >= 2 {
			return path
		}
		if !visited[next] {
			visited[next] = true
			result := dfsFindPath(adj, append(path, next), target, visited)
			if result != nil {
				return result
			}
			delete(visited, next)
		}
	}
	return nil
}

// createSectorFromPolygon creates a new sector from the given chunk point indices.
func createSectorFromPolygon(ch *Chunk, polyIndices []int) {
	// Ensure CCW winding
	pts := make([]mgl32.Vec2, len(polyIndices))
	for i, idx := range polyIndices {
		pts[i] = ch.Points[idx]
	}
	if signedArea2D(pts) < 0 {
		for i, j := 0, len(polyIndices)-1; i < j; i, j = i+1, j-1 {
			polyIndices[i], polyIndices[j] = polyIndices[j], polyIndices[i]
		}
	}

	// Build walls for each edge
	var walls []Wall
	edgeKinds := make([]string, len(polyIndices))
	for i := range polyIndices {
		a := ch.Points[polyIndices[i]]
		b := ch.Points[polyIndices[(i+1)%len(polyIndices)]]
		walls = append(walls, Wall{
			StartPoint:  a,
			EndPoint:    b,
			Texture:     "concrete",
			Collision:   true,
			Visible:     true,
			DoubleSided: true,
			Alpha:       1.0,
		})
		edgeKinds[i] = "wall"
	}

	// Default properties
	floorH := float32(0)
	ceilH := float32(4)
	floorTex := "stone_floor"
	ceilTex := "concrete"

	// Copy from first sector if available
	if len(ch.Sectors) > 0 {
		ref := &ch.Sectors[0]
		floorH = ref.Floor.Height
		ceilH = ref.Ceiling.Height
		if ref.Floor.Texture != "" {
			floorTex = ref.Floor.Texture
		}
		if ref.Ceiling.Texture != "" {
			ceilTex = ref.Ceiling.Texture
		}
	}
	// Wall/Floor/Ceiling.Alpha and DoubleSided have no zero-value-safe default
	// like the YAML loader's float32Default/boolDefault helpers apply, so an
	// unset Alpha here means fully transparent (invisible), not opaque.
	_ = ensureTextureLoaded(floorTex)
	_ = ensureTextureLoaded(ceilTex)

	sector := Sector{
		ID:           fmt.Sprintf("sector_%d", len(ch.Sectors)),
		PointIndices: polyIndices,
		EdgeKinds:    edgeKinds,
		Walls:        walls,
		Floor:        Floor{Enabled: true, Height: floorH, Texture: floorTex, DoubleSided: true, Alpha: 1.0},
		Ceiling:      Ceiling{Enabled: true, Height: ceilH, Texture: ceilTex, DoubleSided: true, Alpha: 1.0},
	}
	ch.Sectors = append(ch.Sectors, sector)
	ch.InferPortalsAndReload()
	Editor.ActiveSectorIdx = len(ch.Sectors) - 1
}

// ---------- edge removal / sector merge ----------

// removeSelectedEdge handles Delete-key on the currently selected edge.
// Wall (external) edge → delete the sector, keep chunk points as free vertices.
// Portal (internal) edge → merge the two connected sectors.
func removeSelectedEdge(ch *Chunk) {
	si := Editor.ActiveSectorIdx
	if si < 0 || si >= len(ch.Sectors) {
		return
	}
	s := &ch.Sectors[si]
	kind := Editor.ActiveEdgeKind
	kindIdx := Editor.ActiveEdgeIdx
	if kindIdx < 0 {
		return
	}

	if kind == "wall" {
		removeSectorKeepPoints(ch, si)
	} else if kind == "portal" && kindIdx < len(s.Portals) {
		mergeSectorsAtPortal(ch, si, kindIdx)
	}

	// Deselect
	Editor.ActiveSectorIdx = -1
	Editor.ActiveEdgeIdx = -1
	Editor.ActiveEdgeKind = ""
	clearVertexSelection()
}

// removeSectorKeepPoints removes a sector from the chunk but leaves all chunk
// points intact so the user can re-create sectors from the free vertices.
// Portals in other sectors that referenced the removed sector are converted to walls.
func removeSectorKeepPoints(ch *Chunk, si int) {
	removedID := ch.Sectors[si].ID
	// Nothing reloads the sector that is going away, so mark the biomes over
	// its footprint here: with the floor gone they have new heights (none) to
	// bake, and would otherwise keep draping over a surface that no longer
	// exists.
	markBiomesDirtyOverBox(ch, ch.Sectors[si].MinBound, ch.Sectors[si].MaxBound)
	ch.Sectors = append(ch.Sectors[:si], ch.Sectors[si+1:]...)

	// Convert portals in remaining sectors that pointed to the removed sector into walls.
	for i := range ch.Sectors {
		convertPortalsToWalls(ch, &ch.Sectors[i], removedID)
		rebuildSectorEdgePoints(ch, &ch.Sectors[i])
		_ = reloadSector(ch, &ch.Sectors[i])
	}
	ch.RebuildGaps()
}

// convertPortalsToWalls converts all portals in s that reference targetSectorID
// into wall edges, updating EdgeKinds, Walls, and Portals slices.
func convertPortalsToWalls(ch *Chunk, s *Sector, targetSectorID string) {
	changed := false
	for pi := len(s.Portals) - 1; pi >= 0; pi-- {
		if s.Portals[pi].ToSector != targetSectorID {
			continue
		}
		p := s.Portals[pi]

		// Find this portal's position in EdgeKinds
		ei := portalEdgePosition(s, pi)
		if ei >= 0 && ei < len(s.EdgeKinds) {
			s.EdgeKinds[ei] = "wall"
		}

		// Insert a wall at the position where this portal was
		wallIdx := 0
		for k := 0; k < ei; k++ {
			if k < len(s.EdgeKinds) && s.EdgeKinds[k] == "wall" {
				wallIdx++
			}
		}
		newWall := Wall{
			StartPoint:  p.StartPoint,
			EndPoint:    p.EndPoint,
			Texture:     "concrete",
			Collision:   true,
			Visible:     true,
			DoubleSided: true,
			Alpha:       1.0,
		}
		// Insert wall at wallIdx
		s.Walls = append(s.Walls, Wall{})
		copy(s.Walls[wallIdx+1:], s.Walls[wallIdx:])
		s.Walls[wallIdx] = newWall

		// Remove the portal
		s.Portals = append(s.Portals[:pi], s.Portals[pi+1:]...)
		changed = true
	}
	if changed {
		rebuildSectorEdgePoints(ch, s)
	}
}

// portalEdgePosition returns the edge position in EdgeKinds for portal index pi.
func portalEdgePosition(s *Sector, pi int) int {
	count := 0
	for i, ek := range s.EdgeKinds {
		if ek == "portal" {
			if count == pi {
				return i
			}
			count++
		}
	}
	return -1
}

// mergeSectorsAtPortal merges the sector at siA with the sector connected by
// portal portalIdx, producing a single combined sector.
func mergeSectorsAtPortal(ch *Chunk, siA, portalIdx int) {
	sA := &ch.Sectors[siA]
	if portalIdx >= len(sA.Portals) {
		return
	}
	portal := sA.Portals[portalIdx]

	// Find sector B by ID
	siB := -1
	for i := range ch.Sectors {
		if ch.Sectors[i].ID == portal.ToSector {
			siB = i
			break
		}
	}
	if siB < 0 || siB == siA {
		return
	}
	sB := &ch.Sectors[siB]

	// Find edge position of the portal in A
	eA := portalEdgePosition(sA, portalIdx)
	if eA < 0 {
		return
	}

	nA := len(sA.PointIndices)
	nB := len(sB.PointIndices)
	ptIdxA1 := sA.PointIndices[eA]
	ptIdxA2 := sA.PointIndices[(eA+1)%nA]

	// Find the reciprocal portal edge in B (goes from ptIdxA2 → ptIdxA1)
	eB := -1
	for i := 0; i < nB; i++ {
		p1 := sB.PointIndices[i]
		p2 := sB.PointIndices[(i+1)%nB]
		if p1 == ptIdxA2 && p2 == ptIdxA1 {
			eB = i
			break
		}
	}
	if eB < 0 {
		return
	}

	// Build merged polygon.
	// A's vertices:  a(eA+1), a(eA+2), ..., a(eA)         → nA vertices
	// B's vertices:  b(eB+2), b(eB+3), ..., b(eB-1)       → nB-2 vertices
	// (b(eB) == a(eA+1) and b(eB+1) == a(eA) are shared, so skipped from B)
	// Total: nA + nB - 2
	mergedN := nA + nB - 2
	mergedIndices := make([]int, 0, mergedN)
	mergedFloorOff := make([]float32, 0, mergedN)
	mergedCeilOff := make([]float32, 0, mergedN)

	// Helper to safely read vertex offsets
	floorOff := func(s *Sector, pos int) float32 {
		if pos < len(s.Floor.VertexOffsets) {
			return s.Floor.VertexOffsets[pos]
		}
		return 0
	}
	ceilOff := func(s *Sector, pos int) float32 {
		if pos < len(s.Ceiling.VertexOffsets) {
			return s.Ceiling.VertexOffsets[pos]
		}
		return 0
	}

	// A's vertices: positions (eA+1), (eA+2), ..., (eA) — all nA
	for i := 0; i < nA; i++ {
		pos := (eA + 1 + i) % nA
		mergedIndices = append(mergedIndices, sA.PointIndices[pos])
		mergedFloorOff = append(mergedFloorOff, floorOff(sA, pos))
		mergedCeilOff = append(mergedCeilOff, ceilOff(sA, pos))
	}

	// B's vertices: positions (eB+2), (eB+3), ..., (eB-1) — nB-2 vertices
	for i := 2; i < nB; i++ {
		pos := (eB + 1 + i) % nB
		mergedIndices = append(mergedIndices, sB.PointIndices[pos])
		mergedFloorOff = append(mergedFloorOff, floorOff(sB, pos))
		mergedCeilOff = append(mergedCeilOff, ceilOff(sB, pos))
	}

	// Build merged edges.
	// A's edges: positions (eA+1), (eA+2), ..., (eA-1) — nA-1 edges (skip portal at eA)
	// B's edges: positions (eB+1), (eB+2), ..., (eB-1) — nB-1 edges (skip portal at eB)
	mergedEdgeKinds := make([]string, 0, mergedN)
	mergedWalls := make([]Wall, 0)
	mergedPortals := make([]Portal, 0)

	removedIDs := map[string]bool{sA.ID: true, sB.ID: true}

	// Collect A's edges (skip portal at eA)
	for i := 1; i < nA; i++ {
		pos := (eA + i) % nA
		ek := "wall"
		if pos < len(sA.EdgeKinds) {
			ek = sA.EdgeKinds[pos]
		}
		kind, kIdx := edgeKindAndIndex(sA, pos)
		if kind == "wall" && kIdx < len(sA.Walls) {
			w := sA.Walls[kIdx]
			// Portal to the other merged sector becomes a wall
			mergedEdgeKinds = append(mergedEdgeKinds, ek)
			mergedWalls = append(mergedWalls, w)
		} else if kind == "portal" && kIdx < len(sA.Portals) {
			p := sA.Portals[kIdx]
			if removedIDs[p.ToSector] {
				// Portal between the two merging sectors → convert to wall
				mergedEdgeKinds = append(mergedEdgeKinds, "wall")
				mergedWalls = append(mergedWalls, Wall{
					StartPoint:  p.StartPoint,
					EndPoint:    p.EndPoint,
					Texture:     "concrete",
					Collision:   true,
					Visible:     true,
					DoubleSided: true,
					Alpha:       1.0,
				})
			} else {
				mergedEdgeKinds = append(mergedEdgeKinds, "portal")
				mergedPortals = append(mergedPortals, p)
			}
		}
	}

	// Collect B's edges (skip portal at eB)
	for i := 1; i < nB; i++ {
		pos := (eB + i) % nB
		kind, kIdx := edgeKindAndIndex(sB, pos)
		if kind == "wall" && kIdx < len(sB.Walls) {
			mergedEdgeKinds = append(mergedEdgeKinds, "wall")
			mergedWalls = append(mergedWalls, sB.Walls[kIdx])
		} else if kind == "portal" && kIdx < len(sB.Portals) {
			p := sB.Portals[kIdx]
			if removedIDs[p.ToSector] {
				mergedEdgeKinds = append(mergedEdgeKinds, "wall")
				mergedWalls = append(mergedWalls, Wall{
					StartPoint:  p.StartPoint,
					EndPoint:    p.EndPoint,
					Texture:     "concrete",
					Collision:   true,
					Visible:     true,
					DoubleSided: true,
					Alpha:       1.0,
				})
			} else {
				mergedEdgeKinds = append(mergedEdgeKinds, "portal")
				mergedPortals = append(mergedPortals, p)
			}
		}
	}

	// Update portals in other sectors that referenced B to point to A's ID
	for i := range ch.Sectors {
		if i == siA || i == siB {
			continue
		}
		for pi := range ch.Sectors[i].Portals {
			if ch.Sectors[i].Portals[pi].ToSector == sB.ID {
				ch.Sectors[i].Portals[pi].ToSector = sA.ID
			}
		}
	}

	// Build the merged sector (keep A's ID and floor/ceiling properties)
	merged := Sector{
		ID:           sA.ID,
		PointIndices: mergedIndices,
		EdgeKinds:    mergedEdgeKinds,
		Walls:        mergedWalls,
		Portals:      mergedPortals,
		Floor:        Floor{Enabled: sA.Floor.Enabled, Hidden: sA.Floor.Hidden, Height: sA.Floor.Height, Texture: sA.Floor.Texture, Terrain: sA.Floor.Terrain, DoubleSided: sA.Floor.DoubleSided, Alpha: sA.Floor.Alpha, VertexOffsets: mergedFloorOff},
		Ceiling:      Ceiling{Enabled: sA.Ceiling.Enabled, Height: sA.Ceiling.Height, Texture: sA.Ceiling.Texture, Terrain: sA.Ceiling.Terrain, DoubleSided: sA.Ceiling.DoubleSided, Alpha: sA.Ceiling.Alpha, VertexOffsets: mergedCeilOff},
	}

	// Replace A with the merged sector
	ch.Sectors[siA] = merged
	rebuildSectorEdgePoints(ch, &ch.Sectors[siA])
	_ = reloadSector(ch, &ch.Sectors[siA])

	// Remove B (adjust index if B comes before A)
	if siB > siA {
		ch.Sectors = append(ch.Sectors[:siB], ch.Sectors[siB+1:]...)
	} else {
		ch.Sectors = append(ch.Sectors[:siB], ch.Sectors[siB+1:]...)
	}

	ch.RebuildGaps()
}

// ---------- helpers ----------

// segmentParam2D returns the parameter in [0,1] of the point on segment
// (ax,ay)-(bx,by) closest to (px,py). A degenerate segment reports 0.
func segmentParam2D(px, py, ax, ay, bx, by float32) float32 {
	dx := bx - ax
	dy := by - ay
	d2 := dx*dx + dy*dy
	if d2 == 0 {
		return 0
	}
	t := ((px-ax)*dx + (py-ay)*dy) / d2
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return t
}

// distToSegment2D returns the minimum distance from point (px,py) to segment (ax,ay)-(bx,by).
func distToSegment2D(px, py, ax, ay, bx, by float32) float32 {
	t := segmentParam2D(px, py, ax, ay, bx, by)
	return dist2D(px, py, ax+t*(bx-ax), ay+t*(by-ay))
}

func dist2D(ax, ay, bx, by float32) float32 {
	dx := ax - bx
	dy := ay - by
	return float32(math.Sqrt(float64(dx*dx + dy*dy)))
}

func approxEq2(a, b mgl32.Vec2) bool {
	const eps = 0.001
	return math.Abs(float64(a.X()-b.X())) < eps &&
		math.Abs(float64(a.Y()-b.Y())) < eps
}

// drawEditorGrid draws a background grid with lines every 5 units and
// more distinctive lines at the x and z axes.
func drawEditorGrid(v orthoView) {
	gridColor := mgl32.Vec3{0.2, 0.2, 0.2}
	axisColor := mgl32.Vec3{0.4, 0.4, 0.4}

	pan := v.pan
	vw, vh := v.size()
	halfW := vw / 2 / v.zoom
	halfH := vh / 2 / v.zoom
	minX := pan.X() - halfW
	maxX := pan.X() + halfW
	minZ := pan.Y() - halfH
	maxZ := pan.Y() + halfH

	gridStep := float32(5)
	startX := float32(math.Floor(float64(minX/gridStep))) * gridStep
	endX := float32(math.Ceil(float64(maxX/gridStep))) * gridStep
	startZ := float32(math.Floor(float64(minZ/gridStep))) * gridStep
	endZ := float32(math.Ceil(float64(maxZ/gridStep))) * gridStep

	y := float32(0)

	for x := startX; x <= endX; x += gridStep {
		col := gridColor
		if math.Abs(float64(x)) < 0.01 {
			col = axisColor
		}
		GizmoLine(mgl32.Vec3{x, y, minZ}, mgl32.Vec3{x, y, maxZ}, col)
	}

	for z := startZ; z <= endZ; z += gridStep {
		col := gridColor
		if math.Abs(float64(z)) < 0.01 {
			col = axisColor
		}
		GizmoLine(mgl32.Vec3{minX, y, z}, mgl32.Vec3{maxX, y, z}, col)
	}
}
