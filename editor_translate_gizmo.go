package main

// editor_translate_gizmo.go — three-axis drag gizmo for the 3D editors.
//
// Whatever is selected (sector vertices, a primitive, an entity, a scenery
// primitive, or a whole chunk) gets three arms — X red, Y green, Z blue — drawn
// at a constant on-screen size around it. Dragging an arm slides the selection
// along that world axis only.
//
// The drag maps the mouse to a position along the axis by taking the closest
// point between the axis line and the cursor's view ray, and applies the offset
// from where the drag started to the values captured at mouse-down. Working
// from a snapshot rather than accumulating per-event deltas keeps a long drag
// exact, and makes snap-to-grid land on absolute grid multiples.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// gizmoKind* are the values EditorState.GizmoKind takes: what the translate
// gizmo is currently attached to.
const (
	gizmoKindNone      = ""
	gizmoKindVertices  = "vertices"
	gizmoKindPrimitive = "primitive"
	gizmoKindEntity    = "entity"
	gizmoKindChunk     = "chunk"
	gizmoKindScenery   = "scenery"
)

const (
	// gizmoArmPx is the on-screen length of each axis arm, in pixels.
	gizmoArmPx float32 = 70
	// gizmoTipHalfPx is half the on-screen size of the grab box at an arm's tip.
	gizmoTipHalfPx float32 = 5
	// gizmoPickPx is how close to an arm a click counts as grabbing it.
	gizmoPickPx float32 = 10
	// gizmoMinAxisDot guards against dragging an axis pointing almost straight
	// at the camera, where the mouse-to-axis mapping explodes.
	gizmoMinAxisDot float32 = 0.999
)

// gizmoAxes are the three world axes the gizmo can drag along, and their colors.
var gizmoAxes = [3]mgl32.Vec3{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
var gizmoAxisColors = [3]mgl32.Vec3{{1, 0.25, 0.25}, {0.3, 1, 0.3}, {0.35, 0.5, 1}}

// gizmoEffectiveKind returns what the gizmo drags right now. The scenery editor
// keeps its own selection, so there it always follows the active scenery
// primitive; the 2D modes have no 3D gizmo at all.
func gizmoEffectiveKind() string {
	switch Editor.Mode {
	case ModeScenery:
		if activeSceneryPrimitive() != nil {
			return gizmoKindScenery
		}
		return gizmoKindNone
	case Mode3D:
		if Editor.GizmoKind == gizmoKindScenery {
			return gizmoKindNone // stale scenery selection
		}
		return Editor.GizmoKind
	}
	return gizmoKindNone
}

// translateGizmoOrigin returns where the gizmo sits for the current selection.
func translateGizmoOrigin() (mgl32.Vec3, bool) {
	switch gizmoEffectiveKind() {
	case gizmoKindVertices:
		return selectedVerticesCenter()

	case gizmoKindPrimitive:
		if p := activeGizmoPrimitive(); p != nil {
			return p.Position, true
		}

	case gizmoKindEntity:
		lvl := Editor.level
		if lvl != nil && Editor.ActiveEntityIdx >= 0 && Editor.ActiveEntityIdx < len(lvl.Entities) {
			return lvl.Entities[Editor.ActiveEntityIdx].Position, true
		}

	case gizmoKindChunk:
		if ch := gizmoChunk(); ch != nil {
			// The chunk's Origin field can sit far from its geometry, so anchor
			// the gizmo on the middle of the chunk's bounds instead.
			return ch.MinBound.Add(ch.MaxBound).Mul(0.5), true
		}

	case gizmoKindScenery:
		if p := activeSceneryPrimitive(); p != nil {
			return p.Position, true
		}
	}
	return mgl32.Vec3{}, false
}

// gizmoChunk returns the chunk the gizmo targets, or nil.
func gizmoChunk() *Chunk {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveChunkIdx < 0 || Editor.ActiveChunkIdx >= len(lvl.Chunks) {
		return nil
	}
	return &lvl.Chunks[Editor.ActiveChunkIdx]
}

// activeGizmoPrimitive returns the selected level primitive, or nil.
func activeGizmoPrimitive() *Primitive {
	ch := gizmoChunk()
	if ch == nil || Editor.ActivePrimitiveIdx < 0 || Editor.ActivePrimitiveIdx >= len(ch.Primitives) {
		return nil
	}
	return &ch.Primitives[Editor.ActivePrimitiveIdx]
}

// activeSceneryPrimitive returns the selected scenery primitive, or nil.
func activeSceneryPrimitive() *Primitive {
	if Editor.SceneryActivePrimIdx < 0 || Editor.SceneryActivePrimIdx >= len(Editor.SceneryPrimitives) {
		return nil
	}
	return &Editor.SceneryPrimitives[Editor.SceneryActivePrimIdx]
}

// gizmoArmLength returns the world length of an arm at the gizmo's distance, so
// the gizmo covers the same number of pixels however far away it is.
func gizmoArmLength(origin mgl32.Vec3) float32 {
	return worldSizeForPixels(origin, gizmoArmPx)
}

// axisParamAtMouse returns how far along axis (from origin, in world units) the
// cursor's view ray comes closest. ok is false when the axis is too close to
// edge-on for the mapping to be usable.
func axisParamAtMouse(origin, axis mgl32.Vec3, mx, my int32) (float32, bool) {
	rayO, rayD := screenRay(mx, my)
	b := rayD.Dot(axis)
	if float32(math.Abs(float64(b))) > gizmoMinAxisDot {
		return 0, false
	}
	w0 := rayO.Sub(origin)
	// Closest point between the view ray and the axis line; s is the parameter
	// along the axis (both directions are unit length, so the denominator is
	// 1 - (d·a)²).
	return (axis.Dot(w0) - b*rayD.Dot(w0)) / (1 - b*b), true
}

// pickTranslateAxis returns the axis whose arm a click at (mx, my) grabs, or -1.
func pickTranslateAxis(mx, my int32) int {
	origin, ok := translateGizmoOrigin()
	if !ok {
		return -1
	}
	projView := Projection.Mul4(Cam.Matrix)
	originScreen, ok := projectToScreen(projView, origin)
	if !ok {
		return -1
	}
	armLen := gizmoArmLength(origin)
	cursor := mgl32.Vec2{float32(mx), float32(my)}

	best := -1
	bestD := gizmoPickPx
	for i, axis := range gizmoAxes {
		tipScreen, ok := projectToScreen(projView, origin.Add(axis.Mul(armLen)))
		if !ok {
			continue
		}
		d := distToSegment2D(cursor.X(), cursor.Y(),
			originScreen.X(), originScreen.Y(), tipScreen.X(), tipScreen.Y())
		if d <= bestD {
			bestD = d
			best = i
		}
	}
	return best
}

// gizmoSuppressed reports whether the gizmo should be inert because the next
// click already means something else. The arms are hidden in these states, so
// they must not be clickable either.
func gizmoSuppressed() bool {
	return Editor.PlacementMode || Editor.PrimPlacementMode || Editor.ExtrusionMode ||
		Editor.SceneryPrimPlacement
}

// translateGizmoMouseDown starts an axis drag if the click grabbed an arm.
// Returns true when the click was consumed, so it does not also re-select.
func translateGizmoMouseDown(t sdl.MouseButtonEvent) bool {
	if t.Button != sdl.ButtonLeft || gizmoSuppressed() {
		return false
	}
	axis := pickTranslateAxis(t.X, t.Y)
	if axis < 0 {
		return false
	}
	origin, ok := translateGizmoOrigin()
	if !ok {
		return false
	}
	s, ok := axisParamAtMouse(origin, gizmoAxes[axis], t.X, t.Y)
	if !ok {
		return false
	}

	if !captureGizmoDragStart() {
		return false
	}
	Editor.GizmoDragging = true
	Editor.GizmoDragAxis = axis
	Editor.GizmoDragStartS = s
	Editor.GizmoDragOrigin = origin
	return true
}

// captureGizmoDragStart snapshots the values the drag will offset, and pushes an
// undo entry once for the whole drag (not per motion event).
func captureGizmoDragStart() bool {
	Editor.GizmoDragPoints = nil
	Editor.GizmoDragOffsets = nil

	switch gizmoEffectiveKind() {
	case gizmoKindVertices:
		lvl := Editor.level
		if lvl == nil || len(Editor.SelectedVertices) == 0 {
			return false
		}
		for _, sel := range Editor.SelectedVertices {
			pt := mgl32.Vec2{}
			off := float32(0)
			if sel.chunkIdx >= 0 && sel.chunkIdx < len(lvl.Chunks) {
				ch := &lvl.Chunks[sel.chunkIdx]
				if sel.pointIdx >= 0 && sel.pointIdx < len(ch.Points) {
					pt = ch.Points[sel.pointIdx]
				}
				if sel.sectorIdx >= 0 && sel.sectorIdx < len(ch.Sectors) {
					s := &ch.Sectors[sel.sectorIdx]
					off = vertexOffset(s, sel)
				}
			}
			Editor.GizmoDragPoints = append(Editor.GizmoDragPoints, pt)
			Editor.GizmoDragOffsets = append(Editor.GizmoDragOffsets, off)
		}
		EditorPushUndo()
		return true

	case gizmoKindPrimitive:
		p := activeGizmoPrimitive()
		if p == nil {
			return false
		}
		Editor.GizmoDragStartPos = p.Position
		EditorPushUndo()
		return true

	case gizmoKindEntity:
		lvl := Editor.level
		if lvl == nil || Editor.ActiveEntityIdx < 0 || Editor.ActiveEntityIdx >= len(lvl.Entities) {
			return false
		}
		Editor.GizmoDragStartPos = lvl.Entities[Editor.ActiveEntityIdx].Position
		EditorPushUndo()
		return true

	case gizmoKindChunk:
		ch := gizmoChunk()
		if ch == nil {
			return false
		}
		Editor.GizmoDragStartPos = ch.Origin
		EditorPushUndo()
		return true

	case gizmoKindScenery:
		p := activeSceneryPrimitive()
		if p == nil {
			return false
		}
		Editor.GizmoDragStartPos = p.Position
		SceneryPushUndo()
		return true
	}
	return false
}

// translateGizmoDragging reports whether an axis drag is in progress.
func translateGizmoDragging() bool { return Editor.GizmoDragging }

// translateGizmoMouseUp ends an axis drag.
func translateGizmoMouseUp() {
	Editor.GizmoDragging = false
	Editor.GizmoDragAxis = -1
	Editor.GizmoDragPoints = nil
	Editor.GizmoDragOffsets = nil
}

// translateGizmoMouseMove applies the current cursor position to the drag.
func translateGizmoMouseMove(mx, my int32) {
	if !Editor.GizmoDragging {
		return
	}
	axis := Editor.GizmoDragAxis
	if axis < 0 || axis > 2 {
		return
	}
	s, ok := axisParamAtMouse(Editor.GizmoDragOrigin, gizmoAxes[axis], mx, my)
	if !ok {
		return
	}
	delta := s - Editor.GizmoDragStartS
	if delta == 0 {
		return
	}

	switch gizmoEffectiveKind() {
	case gizmoKindVertices:
		dragVerticesAlongAxis(axis, delta)
	case gizmoKindPrimitive:
		if p := activeGizmoPrimitive(); p != nil {
			p.Position = axisMoved(Editor.GizmoDragStartPos, axis, delta)
			if ch := gizmoChunk(); ch != nil {
				rebuildPrimitiveIndices(ch)
			}
		}
	case gizmoKindEntity:
		lvl := Editor.level
		if lvl != nil && Editor.ActiveEntityIdx >= 0 && Editor.ActiveEntityIdx < len(lvl.Entities) {
			lvl.Entities[Editor.ActiveEntityIdx].Position =
				axisMoved(Editor.GizmoDragStartPos, axis, delta)
		}
	case gizmoKindChunk:
		if ch := gizmoChunk(); ch != nil {
			moveChunk(ch, axisMoved(Editor.GizmoDragStartPos, axis, delta))
		}
	case gizmoKindScenery:
		if p := activeSceneryPrimitive(); p != nil {
			p.Position = axisMoved(Editor.GizmoDragStartPos, axis, delta)
		}
	}
}

// axisMoved returns start offset by delta along one axis, snapped to the grid
// step when snapping is on.
func axisMoved(start mgl32.Vec3, axis int, delta float32) mgl32.Vec3 {
	out := start
	out[axis] = gizmoSnap(start[axis] + delta)
	return out
}

// gizmoSnap rounds v to the editor's step size when snap-to-grid is enabled.
func gizmoSnap(v float32) float32 {
	if Editor.SnapToGrid && Editor.StepSize > 0 {
		return snapValue(v, Editor.StepSize)
	}
	return v
}

// sectorOf resolves the chunk and sector a selected vertex belongs to. Either
// return value is nil when the indices no longer point at live geometry.
func sectorOf(sel vertexSel) (*Chunk, *Sector) {
	lvl := Editor.level
	if lvl == nil || sel.chunkIdx < 0 || sel.chunkIdx >= len(lvl.Chunks) {
		return nil, nil
	}
	ch := &lvl.Chunks[sel.chunkIdx]
	if sel.sectorIdx < 0 || sel.sectorIdx >= len(ch.Sectors) {
		return ch, nil
	}
	return ch, &ch.Sectors[sel.sectorIdx]
}

// setSelectedVertexPoints rewrites the XZ of every selected vertex's chunk point
// through fn, then rebuilds the geometry that moved. Edge endpoints are cached
// per sector, so every sector in a touched chunk is refreshed — not just the
// selected ones, which may not be the only users of a shared point.
func setSelectedVertexPoints(fn func(i int, sel vertexSel, pt mgl32.Vec2) mgl32.Vec2) {
	touched := map[[2]int]struct{}{}
	for i, sel := range Editor.SelectedVertices {
		ch, _ := sectorOf(sel)
		if ch == nil || sel.pointIdx < 0 || sel.pointIdx >= len(ch.Points) {
			continue
		}
		ch.Points[sel.pointIdx] = fn(i, sel, ch.Points[sel.pointIdx])
		touched[[2]int{sel.chunkIdx, sel.pointIdx}] = struct{}{}
	}
	lvl := Editor.level
	for key := range touched {
		ch := &lvl.Chunks[key[0]]
		for si := range ch.Sectors {
			rebuildSectorEdgePoints(ch, &ch.Sectors[si])
		}
		reloadSectorsUsingPoint(ch, key[1])
		ch.RebuildGaps()
	}
}

// setSelectedVertexOffsets rewrites the floor/ceiling height offset of every
// selected vertex through fn, then reloads the sectors that changed.
func setSelectedVertexOffsets(fn func(i int, sel vertexSel, cur float32) float32) {
	touched := map[[2]int]struct{}{}
	for i, sel := range Editor.SelectedVertices {
		_, s := sectorOf(sel)
		if s == nil {
			continue
		}
		setVertexOffset(s, sel, fn(i, sel, vertexOffset(s, sel)))
		touched[[2]int{sel.chunkIdx, sel.sectorIdx}] = struct{}{}
	}
	lvl := Editor.level
	for key := range touched {
		ch := &lvl.Chunks[key[0]]
		s := &ch.Sectors[key[1]]
		_ = s.Load(SectorPoints(ch, s))
		ch.RebuildGaps()
	}
}

// dragVerticesAlongAxis moves every selected vertex by delta along one axis.
// X and Z move the shared chunk point (so every sector using it follows); Y
// adjusts the per-vertex floor or ceiling offset, which is the only per-vertex
// height control a sector has.
func dragVerticesAlongAxis(axis int, delta float32) {
	if axis == 1 {
		setSelectedVertexOffsets(func(i int, sel vertexSel, cur float32) float32 {
			if i >= len(Editor.GizmoDragOffsets) {
				return cur
			}
			return gizmoSnapOffset(sel, Editor.GizmoDragOffsets[i]+delta)
		})
		return
	}
	// Chunk.Points is XZ: component 0 is world X, component 1 world Z.
	comp := 0
	if axis == 2 {
		comp = 1
	}
	setSelectedVertexPoints(func(i int, sel vertexSel, pt mgl32.Vec2) mgl32.Vec2 {
		if i >= len(Editor.GizmoDragPoints) {
			return pt
		}
		out := Editor.GizmoDragPoints[i]
		out[comp] = gizmoSnap(out[comp] + delta)
		return out
	})
}

// gizmoSnapOffset snaps a vertex height offset so the resulting world height —
// not the offset itself — lands on the grid.
func gizmoSnapOffset(sel vertexSel, offset float32) float32 {
	_, s := sectorOf(sel)
	if s == nil {
		return offset
	}
	base := s.Ceiling.Height
	if sel.isFloor {
		base = s.Floor.Height
	}
	return gizmoSnap(base+offset) - base
}

// vertexOffset reads the floor or ceiling height offset for a selected vertex.
func vertexOffset(s *Sector, sel vertexSel) float32 {
	offsets := s.Ceiling.VertexOffsets
	if sel.isFloor {
		offsets = s.Floor.VertexOffsets
	}
	if sel.vertIdx >= 0 && sel.vertIdx < len(offsets) {
		return offsets[sel.vertIdx]
	}
	return 0
}

// setVertexOffset writes the floor or ceiling height offset for a selected
// vertex, growing the offset slice if the sector has never been loaded.
func setVertexOffset(s *Sector, sel vertexSel, value float32) {
	if sel.vertIdx < 0 {
		return
	}
	target := &s.Ceiling.VertexOffsets
	if sel.isFloor {
		target = &s.Floor.VertexOffsets
	}
	for len(*target) <= sel.vertIdx {
		*target = append(*target, 0)
	}
	(*target)[sel.vertIdx] = value
}

// renderTranslateGizmo draws the three axis arms for the current selection.
// The hovered or dragged arm turns yellow.
func renderTranslateGizmo(mx, my int32) {
	origin, ok := translateGizmoOrigin()
	if !ok {
		return
	}
	hover := -1
	if Editor.GizmoDragging {
		hover = Editor.GizmoDragAxis
	} else {
		hover = pickTranslateAxis(mx, my)
	}

	armLen := gizmoArmLength(origin)
	yellow := mgl32.Vec3{1, 1, 0}
	for i, axis := range gizmoAxes {
		col := gizmoAxisColors[i]
		if i == hover {
			col = yellow
		}
		tip := origin.Add(axis.Mul(armLen))
		GizmoLine(origin, tip, col)
		gizmoBox(tip, gizmoTipHalfPx, col)
	}
}
