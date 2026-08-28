package editor

// editor_3d_edge_pick.go — 3D-mode edge handles.
//
// Every sector edge — the segments between consecutive floor vertices, the
// same segments at ceiling height, and the vertical run joining a vertex's
// floor and ceiling handles — gets a selection volume: a rectangular prism
// wrapped around the edge, drawn at a constant on-screen thickness the way
// vertex handle boxes are. Clicking inside a prism selects that edge, repeated
// clicks cycle through every prism the cursor is inside, and ctrl+click takes
// them all.
//
// The prisms are deliberately smaller than the vertex handle boxes: thinner
// across, and pulled back at both ends by the endpoint boxes' own size, so the
// volume around a corner belongs to the point rather than to the edges meeting
// there. Clicks are resolved points-first as well (see Editor3DHandleClick), so
// the two rules agree.
//
// An edge selection *is* the selection of its two endpoint vertices, so the
// translate gizmo, undo, and the vertex panel all drag an edge without knowing
// edges exist.

import (
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/world"
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// edgeKind3D* are the values edgeSel.kind takes.
const (
	edgeKind3DFloor    = "floor"    // floor vertex vi → floor vertex vi+1
	edgeKind3DCeiling  = "ceiling"  // ceiling vertex vi → ceiling vertex vi+1
	edgeKind3DVertical = "vertical" // floor vertex vi → ceiling vertex vi
)

const (
	// edgeHandleHalfPx is half the on-screen thickness of an edge's selection
	// prism — narrower than vertexHandleHalfPx so a click near a corner reads
	// as the point.
	edgeHandleHalfPx float32 = 5
)

// edgeSel identifies one selectable sector edge. Like vertexSel it stores
// indices rather than pointers, so it survives geometry rebuilds.
type edgeSel struct {
	chunkIdx  int
	sectorIdx int
	kind      string
	vertIdx   int // index into Sector.PointIndices; the first endpoint
}

// edgeHandle is one clickable edge together with the prism that selects it.
type edgeHandle struct {
	edgeSel
	center  mgl32.Vec3
	axes    [3]mgl32.Vec3 // [0] runs along the edge; [1] and [2] are across it
	half    mgl32.Vec3
	screenD float32 // pixel distance from the cursor to the projected edge
	depth   float32 // distance from the camera to the prism centre
}

// edgeSelVertices returns the two vertex handles an edge joins.
func edgeSelVertices(sel edgeSel) (vertexSel, vertexSel, bool) {
	lvl := Editor.level
	if lvl == nil || sel.chunkIdx < 0 || sel.chunkIdx >= len(lvl.Chunks) {
		return vertexSel{}, vertexSel{}, false
	}
	ch := &lvl.Chunks[sel.chunkIdx]
	if sel.sectorIdx < 0 || sel.sectorIdx >= len(ch.Sectors) {
		return vertexSel{}, vertexSel{}, false
	}
	s := &ch.Sectors[sel.sectorIdx]
	n := len(s.PointIndices)
	if n == 0 || sel.vertIdx < 0 || sel.vertIdx >= n {
		return vertexSel{}, vertexSel{}, false
	}

	at := func(vi int, isFloor bool) (vertexSel, bool) {
		pIdx := s.PointIndices[vi]
		if pIdx < 0 || pIdx >= len(ch.Points) {
			return vertexSel{}, false
		}
		return vertexSel{
			chunkIdx:  sel.chunkIdx,
			sectorIdx: sel.sectorIdx,
			vertIdx:   vi,
			pointIdx:  pIdx,
			isFloor:   isFloor,
		}, true
	}

	switch sel.kind {
	case edgeKind3DVertical:
		a, okA := at(sel.vertIdx, true)
		b, okB := at(sel.vertIdx, false)
		return a, b, okA && okB
	case edgeKind3DFloor, edgeKind3DCeiling:
		if n < 2 {
			return vertexSel{}, vertexSel{}, false
		}
		isFloor := sel.kind == edgeKind3DFloor
		a, okA := at(sel.vertIdx, isFloor)
		b, okB := at((sel.vertIdx+1)%n, isFloor)
		return a, b, okA && okB
	}
	return vertexSel{}, vertexSel{}, false
}

// edgeKindsAt returns the edge kinds that start at vertex vi of an n-vertex
// sector. A two-point sector has a single horizontal edge rather than the same
// segment listed twice, once in each direction.
func edgeKindsAt(n, vi int) []string {
	if n < 2 || (n == 2 && vi != 0) {
		return []string{edgeKind3DVertical}
	}
	return []string{edgeKind3DFloor, edgeKind3DCeiling, edgeKind3DVertical}
}

// edgePrism builds the selection volume for an edge running p0 → p1: a box
// centred on the edge, edgeHandleHalfPx pixels thick, with both ends pulled far
// enough in (see edgeTrim) that the corners stay the points'. ok is false when
// the trims leave nothing between them — a very short edge, or one pointing at
// the camera.
func edgePrism(p0, p1 mgl32.Vec3) (center mgl32.Vec3, axes [3]mgl32.Vec3, half mgl32.Vec3, ok bool) {
	d := p1.Sub(p0)
	length := d.Len()
	if length < 1e-5 {
		return
	}
	u := d.Mul(1 / length)

	trim0 := edgeTrim(p0, u)
	trim1 := edgeTrim(p1, u)
	if trim0+trim1 >= length {
		return
	}
	a := p0.Add(u.Mul(trim0))
	b := p1.Sub(u.Mul(trim1))
	center = a.Add(b).Mul(0.5)

	// Any perpendicular pair will do — the cross-section is square.
	v := u.Cross(mgl32.Vec3{0, 1, 0})
	if v.Len() < 1e-4 {
		v = u.Cross(mgl32.Vec3{1, 0, 0})
	}
	v = v.Normalize()
	w := u.Cross(v).Normalize()

	cross := worldSizeForPixels(center, edgeHandleHalfPx)
	return center, [3]mgl32.Vec3{u, v, w}, mgl32.Vec3{(length - trim0 - trim1) / 2, cross, cross}, true
}

// edgeTrim returns how far in from endpoint p an edge's prism has to start to
// clear that endpoint's vertex handle box. Sliding along the edge only moves
// the projection by the part of the edge that runs across the view, so an edge
// pointing at the camera needs a far longer cut than a broadside one — and when
// the cuts swallow the edge, the point boxes already cover all of it on screen
// and there is nothing left to click.
func edgeTrim(p, u mgl32.Vec3) float32 {
	half := worldSizeForPixels(p, vertexHandleHalfPx)
	view := p.Sub(gfx.Cam.Position)
	if view.Len() < 1e-5 {
		return half
	}
	// sin² of the angle between the edge and the line of sight.
	along := u.Dot(view.Normalize())
	across := 1 - along*along
	if across <= 1e-6 {
		return float32(math.Inf(1))
	}
	return half / float32(math.Sqrt(float64(across)))
}

// rayOBBIntersect tests a ray against an oriented box. The ray is rewritten in
// the box's frame, which is a rigid motion, so the returned distance is the
// world-space one.
func rayOBBIntersect(origin, dir, center mgl32.Vec3, axes [3]mgl32.Vec3, half mgl32.Vec3) (bool, float32) {
	rel := origin.Sub(center)
	var localO, localD mgl32.Vec3
	for i := 0; i < 3; i++ {
		localO[i] = rel.Dot(axes[i])
		localD[i] = dir.Dot(axes[i])
	}
	return rayAABBIntersect(localO, localD, half.Mul(-1), half)
}

// collectEdgeHandles returns every sector edge whose endpoints are both within
// vertexHandleMaxDist of the camera and whose projected segment passes within
// maxPx of (mx, my), nearest first.
func collectEdgeHandles(mx, my int32, maxPx float32) []edgeHandle {
	lvl := Editor.level
	if lvl == nil {
		return nil
	}
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)

	var out []edgeHandle
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !ch.IsEnabled() {
			continue // not drawn, so no handles; see world/chunk_enable.go
		}
		for seci := range ch.Sectors {
			s := &ch.Sectors[seci]
			n := len(s.PointIndices)
			for vi := 0; vi < n; vi++ {
				for _, kind := range edgeKindsAt(n, vi) {
					sel := edgeSel{chunkIdx: ci, sectorIdx: seci, kind: kind, vertIdx: vi}
					h, ok := buildEdgeHandle(sel, projView, cursor, maxPx)
					if ok {
						out = append(out, h)
					}
				}
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.screenD != b.screenD {
			return a.screenD < b.screenD
		}
		if a.depth != b.depth {
			return a.depth < b.depth
		}
		if a.chunkIdx != b.chunkIdx {
			return a.chunkIdx < b.chunkIdx
		}
		if a.sectorIdx != b.sectorIdx {
			return a.sectorIdx < b.sectorIdx
		}
		if a.vertIdx != b.vertIdx {
			return a.vertIdx < b.vertIdx
		}
		return a.kind < b.kind
	})
	return out
}

// buildEdgeHandle resolves one edge into a handle, rejecting it when either
// endpoint is out of range, the prism is degenerate, or the projected edge is
// further than maxPx from the cursor — the last of which handleShowsAll waives.
func buildEdgeHandle(sel edgeSel, projView mgl32.Mat4, cursor mgl32.Vec2, maxPx float32) (edgeHandle, bool) {
	va, vb, ok := edgeSelVertices(sel)
	if !ok {
		return edgeHandle{}, false
	}
	p0, ok0 := vertexSelPos(va)
	p1, ok1 := vertexSelPos(vb)
	if !ok0 || !ok1 {
		return edgeHandle{}, false
	}
	// An edge is only reachable where both of its points are, so distant
	// geometry cannot hijack a click here either.
	if p0.Sub(gfx.Cam.Position).Len() > vertexHandleMaxDist || p1.Sub(gfx.Cam.Position).Len() > vertexHandleMaxDist {
		return edgeHandle{}, false
	}

	// Solid shading hides the far side of the world. As in the model editor an
	// edge counts as visible when either endpoint is: one running behind a wall
	// but emerging at one end is still something the user can see and aim at.
	if !editorEdgeVisible(va, vb, p0, p1) {
		return edgeHandle{}, false
	}

	center, axes, half, ok := edgePrism(p0, p1)
	if !ok {
		return edgeHandle{}, false
	}

	endA := center.Sub(axes[0].Mul(half[0]))
	endB := center.Add(axes[0].Mul(half[0]))
	sa, okA := projectToScreen(projView, endA)
	sb, okB := projectToScreen(projView, endB)
	if !okA || !okB {
		return edgeHandle{}, false
	}
	d := distToSegment2D(cursor.X(), cursor.Y(), sa.X(), sa.Y(), sb.X(), sb.Y())
	if d > maxPx && !handleShowsAll(maxPx) {
		return edgeHandle{}, false
	}

	return edgeHandle{
		edgeSel: sel,
		center:  center,
		axes:    axes,
		half:    half,
		screenD: d,
		depth:   center.Sub(gfx.Cam.Position).Len(),
	}, true
}

// edgePrismContains reports whether the cursor's view ray passes through a
// handle's prism — the edge equivalent of handleBoxContains.
func edgePrismContains(h edgeHandle, mx, my int32) bool {
	origin, dir := screenRay(mx, my)
	hit, _ := rayOBBIntersect(origin, dir, h.center, h.axes, h.half)
	return hit
}

// edgeHandlesInPrism returns the handles whose prism the cursor is aimed
// through, nearest projected edge first.
func edgeHandlesInPrism(mx, my int32) []edgeHandle {
	var inPrism []edgeHandle
	for _, h := range collectEdgeHandles(mx, my, vertexHandleShowPx) {
		if edgePrismContains(h, mx, my) {
			inPrism = append(inPrism, h)
		}
	}
	return inPrism
}

// isEdgeSelected reports whether both of an edge's endpoints are selected,
// which is what selecting the edge does.
func isEdgeSelected(sel edgeSel) bool {
	a, b, ok := edgeSelVertices(sel)
	return ok && isVertexSelected(a) && isVertexSelected(b)
}

// pickEdgeHandle returns the edge handle a click at (mx, my) lands on. Clicking
// again without leaving the overlapping prisms advances to the next candidate,
// so a shared edge, or a floor and ceiling edge seen edge-on, can all be
// reached.
func pickEdgeHandle(mx, my int32) (edgeHandle, bool) {
	inPrism := edgeHandlesInPrism(mx, my)
	if len(inPrism) == 0 {
		return edgeHandle{}, false
	}
	for i, h := range inPrism {
		if isEdgeSelected(h.edgeSel) {
			return inPrism[(i+1)%len(inPrism)], true
		}
	}
	return inPrism[0], true
}

// edgeVertices flattens edges into their endpoint vertex handles, without
// duplicates and in order, so a chain of edges selects each shared corner once.
func edgeVertices(sels []edgeSel) []vertexSel {
	var out []vertexSel
	add := func(v vertexSel) {
		for _, existing := range out {
			if existing == v {
				return
			}
		}
		out = append(out, v)
	}
	for _, sel := range sels {
		a, b, ok := edgeSelVertices(sel)
		if !ok {
			continue
		}
		add(a)
		add(b)
	}
	return out
}

// setEdgeSelection replaces the selection with sels' endpoints. Reports false
// when none of the edges resolve, leaving the current selection alone.
//
// The endpoints are the selection proper — that is what the gizmo moves and
// what undo records — but the wall or portal the first edge runs along is noted
// alongside them, so the side panel has something to edit and the 2D map has an
// edge to draw in the selected colour. Both views go through here, which is
// what makes an edge picked in one show up as an edge in the other.
func setEdgeSelection(sels []edgeSel) bool {
	verts := edgeVertices(sels)
	if len(verts) == 0 {
		return false
	}
	setVertexSelection(verts)
	noteEdgeSurface(sels[0])
	return true
}

// noteEdgeSurface points ActiveEdgeKind/ActiveEdgeIdx at the wall or portal an
// edge selection runs along, without disturbing the vertex selection itself.
// A vertical edge joins one corner's floor and ceiling handles and lies along
// no wall, so it clears the note rather than pointing at an unrelated one.
func noteEdgeSurface(sel edgeSel) {
	lvl := Editor.level
	if lvl == nil || sel.chunkIdx < 0 || sel.chunkIdx >= len(lvl.Chunks) {
		return
	}
	ch := &lvl.Chunks[sel.chunkIdx]
	if sel.sectorIdx < 0 || sel.sectorIdx >= len(ch.Sectors) {
		return
	}
	if sel.kind == edgeKind3DVertical {
		Editor.ActiveEdgeKind = ""
		Editor.ActiveEdgeIdx = -1
		return
	}
	kind, kindIdx := world.EdgeKindAndIndex(&ch.Sectors[sel.sectorIdx], sel.vertIdx)
	Editor.ActiveSectorIdx = sel.sectorIdx
	Editor.ActiveEdgeKind = kind
	Editor.ActiveEdgeIdx = kindIdx
}

// handleEdgeClick resolves a click against the edge prisms. A plain click
// selects one edge and cycles through overlapping ones on repeat; ctrl+click
// adds every edge under the cursor to the selection. Returns false when the
// click missed every prism, leaving it to the geometry picker.
func handleEdgeClick(mx, my int32, ctrlHeld bool) bool {
	if !ctrlHeld {
		h, ok := pickEdgeHandle(mx, my)
		if !ok {
			return false
		}
		return setEdgeSelection([]edgeSel{h.edgeSel})
	}

	inPrism := edgeHandlesInPrism(mx, my)
	if len(inPrism) == 0 {
		return false
	}
	sels := make([]edgeSel, 0, len(inPrism))
	for _, h := range inPrism {
		sels = append(sels, h.edgeSel)
	}
	verts := edgeVertices(sels)
	if len(verts) == 0 {
		return false
	}
	addVertexHandlesToSelection(verts)
	noteEdgeSurface(sels[0])
	return true
}

// edgeHandleColor is the resting color of an edge prism: the muted form of the
// vertex handle colors for floor (blue) and ceiling (red) edges, and green for
// the vertical run between them.
func edgeHandleColor(kind string) mgl32.Vec3 {
	switch kind {
	case edgeKind3DCeiling:
		return mgl32.Vec3{0.7, 0.3, 0.24}
	case edgeKind3DVertical:
		return mgl32.Vec3{0.35, 0.7, 0.4}
	default:
		return mgl32.Vec3{0.24, 0.38, 0.7}
	}
}

// drawEdgeHandlePrisms queues the prisms for every edge within showPx of
// (mx, my). Caller sets up the depth state. Selected edges need no mark of
// their own: their endpoints are selected, so both ends already draw yellow.
func drawEdgeHandlePrisms(mx, my int32, showPx float32) {
	for _, h := range collectEdgeHandles(mx, my, showPx) {
		col := edgeHandleColor(h.kind)
		if edgePrismContains(h, mx, my) {
			col = mgl32.Vec3{1, 1, 1}
		}
		if isEdgeSelected(h.edgeSel) {
			col = mgl32.Vec3{1, 1, 0}
		}
		gizmo.GizmoOBB(h.center, h.axes, h.half, col)
	}
}
