package main

// editor_2d.go — top-down orthographic sector editor.
//
// Points are stored in the chunk's shared pool (Chunk.Points).
// Sectors reference them via PointIndices.
//
// Shift+click  : place a new free point (also splits an edge if near one)
// Ctrl+click   : connect two points with an edge (creates/splits sectors)
// Right-click  : remove a point and all connected edges
// Left-click   : select vertex/edge/sector, drag vertices
// Middle-mouse : pan the view

import (
	"fmt"
	"log"
	"math"
	"sort"

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
	drawEditorGrid(Editor.Pan2D, Editor.Zoom2D)

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

	// Draw free points (not in any sector) as white crosses
	for pi, pt := range ch.Points {
		if usedPoints[pi] {
			continue
		}
		col := white
		if pi == Editor.ConnectFromIdx {
			col = green
		}
		gp := mgl32.Vec3{pt.X(), 0, pt.Y()}
		crossSz := screenToWorldSize(5, ortho)
		GizmoLine(gp.Sub(mgl32.Vec3{crossSz, 0, 0}), gp.Add(mgl32.Vec3{crossSz, 0, 0}), col)
		GizmoLine(gp.Sub(mgl32.Vec3{0, 0, crossSz}), gp.Add(mgl32.Vec3{0, 0, crossSz}), col)
	}

	// Highlight ConnectFrom point in green if it's a sector point
	if Editor.ConnectFromIdx >= 0 && Editor.ConnectFromIdx < len(ch.Points) && usedPoints[Editor.ConnectFromIdx] {
		pt := ch.Points[Editor.ConnectFromIdx]
		gp := mgl32.Vec3{pt.X(), 0, pt.Y()}
		crossSz := screenToWorldSize(7, ortho)
		GizmoLine(gp.Sub(mgl32.Vec3{crossSz, 0, 0}), gp.Add(mgl32.Vec3{crossSz, 0, 0}), green)
		GizmoLine(gp.Sub(mgl32.Vec3{0, 0, crossSz}), gp.Add(mgl32.Vec3{0, 0, crossSz}), green)
	}

	// Draw pending edges as green lines
	for _, e := range Editor.PendingEdges {
		if e[0] < len(ch.Points) && e[1] < len(ch.Points) {
			a := ch.Points[e[0]]
			b := ch.Points[e[1]]
			GizmoLine(mgl32.Vec3{a.X(), 0, a.Y()}, mgl32.Vec3{b.X(), 0, b.Y()}, green)
		}
	}

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

// editor2DOrtho returns the orthographic projection×view matrix for the 2D map.
func editor2DOrtho() mgl32.Mat4 {
	halfW := float32(WinWidth) / 2 / Editor.Zoom2D
	halfH := float32(WinHeight) / 2 / Editor.Zoom2D
	cx := Editor.Pan2D.X()
	cy := Editor.Pan2D.Y()

	return mgl32.Ortho(-halfW, halfW, -halfH, halfH, -100, 100).
		Mul4(mgl32.LookAtV(
			mgl32.Vec3{cx, 50, cy},
			mgl32.Vec3{cx, 0, cy},
			mgl32.Vec3{0, 0, -1},
		))
}

// screenToWorldSize converts a pixel distance to world units at current zoom.
func screenToWorldSize(screenPx float32, _ mgl32.Mat4) float32 {
	return screenPx / Editor.Zoom2D
}

// worldToScreen converts a world XZ point to screen pixel coordinates.
func worldToScreen(wx, wz float32) (sx, sy float32) {
	halfW := float32(WinWidth) / 2
	halfH := float32(WinHeight) / 2
	cx := Editor.Pan2D.X()
	cy := Editor.Pan2D.Y()
	sx = (wx-cx)*Editor.Zoom2D + halfW
	sy = (wz-cy)*Editor.Zoom2D + halfH
	return
}

// screenToWorld converts screen pixels to world XZ.
func screenToWorld(sx, sy int32) (wx, wz float32) {
	halfW := float32(WinWidth) / 2
	halfH := float32(WinHeight) / 2
	cx := Editor.Pan2D.X()
	cy := Editor.Pan2D.Y()
	wx = (float32(sx)-halfW)/Editor.Zoom2D + cx
	wz = (float32(sy)-halfH)/Editor.Zoom2D + cy
	return
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

	threshold := screenToWorldSize(editor2DSelectRadius, editor2DOrtho())

	// --- Right-click: remove point ---
	if t.Button == sdl.ButtonRight {
		pi := findChunkPointNear(ch, wx, wz, threshold)
		if pi >= 0 {
			removeChunkPoint(ch, pi)
		}
		return
	}

	if t.Button == sdl.ButtonMiddle {
		Editor.LastMouse2D = [2]int32{t.X, t.Y}
		return
	}

	if t.Button != sdl.ButtonLeft {
		return
	}

	// --- Shift+click: place a new free point ---
	if shiftHeld {
		newPt := mgl32.Vec2{wx, wz}
		newIdx := len(ch.Points)
		ch.Points = append(ch.Points, newPt)

		// If near an existing sector edge, also insert into that sector
		for si := range ch.Sectors {
			s := &ch.Sectors[si]
			nPts := len(s.PointIndices)
			for ei := 0; ei < nPts; ei++ {
				a := ch.Points[s.PointIndices[ei]]
				b := ch.Points[s.PointIndices[(ei+1)%nPts]]
				d := distToSegment2D(wx, wz, a.X(), a.Y(), b.X(), b.Y())
				if d < threshold*2 {
					insertSectorVertex(ch, s, si, ei, newIdx)
					return
				}
			}
		}
		return
	}

	// --- Ctrl+click: connect points ---
	if ctrlHeld {
		pi := findChunkPointNear(ch, wx, wz, threshold)
		if pi < 0 {
			return
		}
		if Editor.ConnectFromIdx < 0 {
			Editor.ConnectFromIdx = pi
			return
		}
		if Editor.ConnectFromIdx == pi {
			Editor.ConnectFromIdx = -1
			return
		}
		fromIdx := Editor.ConnectFromIdx
		toIdx := pi
		Editor.ConnectFromIdx = -1

		// Check if both points are on the boundary of the same sector → split
		for si := range ch.Sectors {
			s := &ch.Sectors[si]
			posA := sectorVertexPos(s, fromIdx)
			posB := sectorVertexPos(s, toIdx)
			if posA >= 0 && posB >= 0 && !adjacentInSector(s, posA, posB) {
				splitSector(ch, si, posA, posB)
				return
			}
		}

		// Otherwise add as pending edge and check for polygon completion
		Editor.PendingEdges = append(Editor.PendingEdges, [2]int{fromIdx, toIdx})
		tryCreateSectorFromPendingEdges(ch)
		return
	}

	// --- Normal left-click: select vertex/edge/sector ---

	// 1. Vertex proximity — highest priority (check all chunk points in sectors)
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		for vi, pIdx := range s.PointIndices {
			if pIdx < 0 || pIdx >= len(ch.Points) {
				continue
			}
			pt := ch.Points[pIdx]
			if dist2D(wx, wz, pt.X(), pt.Y()) < threshold {
				Editor.ActiveSectorIdx = si
				Editor.ActiveVertexSI = si
				Editor.ActiveVertexVI = vi
				Editor.ActiveEdgeIdx = -1
				Editor.DragVertexSI = si
				Editor.DragVertexVI = vi
				Editor.DragIsFloor = true
				return
			}
		}
	}

	// 2. Edge proximity (segment distance, with cycling on repeated clicks)
	type edgeCandidate struct {
		si, ei  int
		dist    float32
		kind    string
		kindIdx int
	}
	var edgeCandidates []edgeCandidate
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		nPts := len(s.PointIndices)
		for ei := 0; ei < nPts; ei++ {
			a := ch.Points[s.PointIndices[ei]]
			b := ch.Points[s.PointIndices[(ei+1)%nPts]]
			d := distToSegment2D(wx, wz, a.X(), a.Y(), b.X(), b.Y())
			if d < threshold*2 {
				kind, kindIdx := edgeKindAndIndex(s, ei)
				edgeCandidates = append(edgeCandidates, edgeCandidate{si, ei, d, kind, kindIdx})
			}
		}
	}
	sort.Slice(edgeCandidates, func(i, j int) bool { return edgeCandidates[i].dist < edgeCandidates[j].dist })
	if len(edgeCandidates) > 0 {
		best := edgeCandidates[0]
		if len(edgeCandidates) > 1 &&
			best.si == Editor.ActiveSectorIdx &&
			best.kind == Editor.ActiveEdgeKind &&
			best.kindIdx == Editor.ActiveEdgeIdx {
			best = edgeCandidates[1]
		}
		Editor.ActiveSectorIdx = best.si
		Editor.ActiveEdgeKind = best.kind
		Editor.ActiveEdgeIdx = best.kindIdx
		Editor.ActiveVertexSI = -1
		Editor.ActiveVertexVI = -1
		return
	}

	// 3. Sector interior — point-in-polygon test
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if pointInPolygon(mgl32.Vec2{wx, wz}, SectorPoints(ch, s)) {
			Editor.ActiveSectorIdx = si
			Editor.ActiveEdgeIdx = -1
			Editor.ActiveVertexSI = -1
			Editor.ActiveVertexVI = -1
			return
		}
	}

	// 4. Click on empty space — deselect everything
	Editor.ActiveEdgeIdx = -1
	Editor.ActiveVertexSI = -1
	Editor.ActiveVertexVI = -1
}

// Editor2DHandleRelease processes mouse button release to stop vertex dragging.
func Editor2DHandleRelease(t sdl.MouseButtonEvent) {
	if t.Button == sdl.ButtonLeft {
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

	// Middle-mouse pan
	if t.State&sdl.ButtonMMask != 0 {
		dx := float32(t.X-Editor.LastMouse2D[0]) / Editor.Zoom2D
		dy := float32(t.Y-Editor.LastMouse2D[1]) / Editor.Zoom2D
		Editor.Pan2D[0] -= dx
		Editor.Pan2D[1] -= dy
	}
	Editor.LastMouse2D = [2]int32{t.X, t.Y}

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
					ch.Points[pIdx] = mgl32.Vec2{wx, wz}
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
// in the chunk that reference the given chunk point index.
func reloadSectorsUsingPoint(ch *Chunk, pointIdx int) {
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		for _, pIdx := range s.PointIndices {
			if pIdx == pointIdx {
				rebuildSectorEdgePoints(ch, s)
				_ = s.Load(SectorPoints(ch, s))
				break
			}
		}
	}
}

// insertSectorVertex inserts a chunk point (already in ch.Points at newPtIdx)
// into a sector at edge position ei, splitting the edge.
func insertSectorVertex(ch *Chunk, s *Sector, si, ei, newPtIdx int) {
	// Insert into PointIndices
	newIndices := make([]int, len(s.PointIndices)+1)
	copy(newIndices, s.PointIndices[:ei+1])
	newIndices[ei+1] = newPtIdx
	copy(newIndices[ei+2:], s.PointIndices[ei+1:])
	s.PointIndices = newIndices

	// Extend VertexOffsets: interpolate new vertex's offset between its neighbors
	interpOffset := func(offsets []float32) []float32 {
		n := len(offsets)
		if n == 0 {
			return []float32{0}
		}
		prevOff := offsets[ei%n]
		nextOff := offsets[(ei+1)%n]
		mid := (prevOff + nextOff) / 2
		newOff := make([]float32, n+1)
		copy(newOff, offsets[:ei+1])
		newOff[ei+1] = mid
		copy(newOff[ei+2:], offsets[ei+1:])
		return newOff
	}
	s.Floor.VertexOffsets = interpOffset(s.Floor.VertexOffsets)
	s.Ceiling.VertexOffsets = interpOffset(s.Ceiling.VertexOffsets)

	// Insert EdgeKind for the new edge (same kind as the edge being split)
	if ei < len(s.EdgeKinds) {
		newKinds := make([]string, len(s.EdgeKinds)+1)
		copy(newKinds, s.EdgeKinds[:ei+1])
		newKinds[ei+1] = s.EdgeKinds[ei]
		copy(newKinds[ei+2:], s.EdgeKinds[ei+1:])
		s.EdgeKinds = newKinds
	}

	// Insert a new wall or portal (splitting the existing edge at ei)
	pts := SectorPoints(ch, s)
	newPt := ch.Points[newPtIdx]
	wKind, wIdx := edgeKindAndIndex(s, ei)
	if wKind != "wall" && wKind != "portal" {
		log.Printf("insertSectorVertex: sector %q edge %d has unknown kind %q; skipping split", s.ID, ei, wKind)
		return
	}
	if wKind == "wall" && wIdx < len(s.Walls) {
		existing := s.Walls[wIdx]
		newWall := Wall{
			StartPoint: newPt,
			EndPoint:   existing.EndPoint,
			Texture:    existing.Texture,
			Collision:  existing.Collision,
			Visible:    existing.Visible,
		}
		existing.EndPoint = newPt
		s.Walls[wIdx] = existing
		newWalls := make([]Wall, len(s.Walls)+1)
		copy(newWalls, s.Walls[:wIdx+1])
		newWalls[wIdx+1] = newWall
		copy(newWalls[wIdx+2:], s.Walls[wIdx+1:])
		s.Walls = newWalls
	} else if wKind == "portal" && wIdx < len(s.Portals) {
		existing := s.Portals[wIdx]
		newPortal := Portal{
			StartPoint: newPt,
			EndPoint:   existing.EndPoint,
			ToSector:   existing.ToSector,
			DrawGap:    existing.DrawGap,
			GapTexture: existing.GapTexture,
			Visible:    existing.Visible,
		}
		existing.EndPoint = newPt
		s.Portals[wIdx] = existing
		newPortals := make([]Portal, len(s.Portals)+1)
		copy(newPortals, s.Portals[:wIdx+1])
		newPortals[wIdx+1] = newPortal
		copy(newPortals[wIdx+2:], s.Portals[wIdx+1:])
		s.Portals = newPortals
	}
	_ = s.Load(pts)
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
	wallsA = append(wallsA, Wall{StartPoint: ptB, EndPoint: ptA, Texture: "concrete", Collision: true, Visible: true})

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
	wallsB = append(wallsB, Wall{StartPoint: ptA, EndPoint: ptB, Texture: "concrete", Collision: true, Visible: true})

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
		Floor:        Floor{Enabled: s.Floor.Enabled, Height: s.Floor.Height, Texture: s.Floor.Texture, Terrain: s.Floor.Terrain, DoubleSided: s.Floor.DoubleSided, VertexOffsets: floorOffsA},
		Ceiling:      Ceiling{Enabled: s.Ceiling.Enabled, Height: s.Ceiling.Height, Texture: s.Ceiling.Texture, Terrain: s.Ceiling.Terrain, DoubleSided: s.Ceiling.DoubleSided, VertexOffsets: ceilOffsA},
	}
	sectorB := Sector{
		ID:           s.ID + "_split",
		PointIndices: indicesB,
		EdgeKinds:    edgeKindsB,
		Walls:        wallsB,
		Portals:      portalsB,
		Floor:        Floor{Enabled: s.Floor.Enabled, Height: s.Floor.Height, Texture: s.Floor.Texture, Terrain: s.Floor.Terrain, DoubleSided: s.Floor.DoubleSided, VertexOffsets: floorOffsB},
		Ceiling:      Ceiling{Enabled: s.Ceiling.Enabled, Height: s.Ceiling.Height, Texture: s.Ceiling.Texture, Terrain: s.Ceiling.Terrain, DoubleSided: s.Ceiling.DoubleSided, VertexOffsets: ceilOffsB},
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
			Editor.ActiveVertexSI = -1
			Editor.ActiveVertexVI = -1
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

	// Reset connect-from if it was this point
	if Editor.ConnectFromIdx == pi {
		Editor.ConnectFromIdx = -1
	}

	// Remove the point from ch.Points and adjust all indices
	ch.Points = append(ch.Points[:pi], ch.Points[pi+1:]...)
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
	// Adjust ConnectFromIdx
	if Editor.ConnectFromIdx > pi {
		Editor.ConnectFromIdx--
	}

	// Deselect vertices that might now be invalid
	Editor.ActiveVertexSI = -1
	Editor.ActiveVertexVI = -1
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
	_ = s.Load(SectorPoints(ch, s))
}

// tryCreateSectorFromPendingEdges checks if pending edges form a closed polygon
// and creates a new sector if so.
func tryCreateSectorFromPendingEdges(ch *Chunk) {
	if len(Editor.PendingEdges) < 3 {
		return
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
	}
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
			StartPoint: a,
			EndPoint:   b,
			Texture:    "concrete",
			Collision:  true,
			Visible:    true,
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

	sector := Sector{
		ID:           fmt.Sprintf("sector_%d", len(ch.Sectors)),
		PointIndices: polyIndices,
		EdgeKinds:    edgeKinds,
		Walls:        walls,
		Floor:        Floor{Enabled: true, Height: floorH, Texture: floorTex},
		Ceiling:      Ceiling{Enabled: true, Height: ceilH, Texture: ceilTex},
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
	Editor.ActiveVertexSI = -1
	Editor.ActiveVertexVI = -1
}

// removeSectorKeepPoints removes a sector from the chunk but leaves all chunk
// points intact so the user can re-create sectors from the free vertices.
// Portals in other sectors that referenced the removed sector are converted to walls.
func removeSectorKeepPoints(ch *Chunk, si int) {
	removedID := ch.Sectors[si].ID
	ch.Sectors = append(ch.Sectors[:si], ch.Sectors[si+1:]...)

	// Convert portals in remaining sectors that pointed to the removed sector into walls.
	for i := range ch.Sectors {
		convertPortalsToWalls(ch, &ch.Sectors[i], removedID)
		rebuildSectorEdgePoints(ch, &ch.Sectors[i])
		_ = ch.Sectors[i].Load(SectorPoints(ch, &ch.Sectors[i]))
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
			StartPoint: p.StartPoint,
			EndPoint:   p.EndPoint,
			Texture:    "concrete",
			Collision:  true,
			Visible:    true,
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
					StartPoint: p.StartPoint,
					EndPoint:   p.EndPoint,
					Texture:    "concrete",
					Collision:  true,
					Visible:    true,
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
					StartPoint: p.StartPoint,
					EndPoint:   p.EndPoint,
					Texture:    "concrete",
					Collision:  true,
					Visible:    true,
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
		Floor:        Floor{Enabled: sA.Floor.Enabled, Height: sA.Floor.Height, Texture: sA.Floor.Texture, Terrain: sA.Floor.Terrain, DoubleSided: sA.Floor.DoubleSided, VertexOffsets: mergedFloorOff},
		Ceiling:      Ceiling{Enabled: sA.Ceiling.Enabled, Height: sA.Ceiling.Height, Texture: sA.Ceiling.Texture, Terrain: sA.Ceiling.Terrain, DoubleSided: sA.Ceiling.DoubleSided, VertexOffsets: mergedCeilOff},
	}

	// Replace A with the merged sector
	ch.Sectors[siA] = merged
	rebuildSectorEdgePoints(ch, &ch.Sectors[siA])
	_ = ch.Sectors[siA].Load(SectorPoints(ch, &ch.Sectors[siA]))

	// Remove B (adjust index if B comes before A)
	if siB > siA {
		ch.Sectors = append(ch.Sectors[:siB], ch.Sectors[siB+1:]...)
	} else {
		ch.Sectors = append(ch.Sectors[:siB], ch.Sectors[siB+1:]...)
	}

	ch.RebuildGaps()
}

// ---------- helpers ----------

// distToSegment2D returns the minimum distance from point (px,py) to segment (ax,ay)-(bx,by).
func distToSegment2D(px, py, ax, ay, bx, by float32) float32 {
	dx := bx - ax
	dy := by - ay
	if dx == 0 && dy == 0 {
		return dist2D(px, py, ax, ay)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / (dx*dx + dy*dy)
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return dist2D(px, py, ax+t*dx, ay+t*dy)
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
func drawEditorGrid(pan mgl32.Vec2, zoom float32) {
	gridColor := mgl32.Vec3{0.2, 0.2, 0.2}
	axisColor := mgl32.Vec3{0.4, 0.4, 0.4}

	halfW := float32(WinWidth) / 2 / zoom
	halfH := float32(WinHeight) / 2 / zoom
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
