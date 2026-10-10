package editor

// editor_3d_edge_pick.go — 3D-mode edge handles.
//
// Every edge of every brush — each pair of consecutive corners around one of
// its faces — gets a selection volume: a rectangular prism wrapped around the
// edge, drawn at a constant on-screen thickness the way vertex handle boxes
// are. Clicking inside a prism selects that edge, repeated clicks cycle through
// every prism the cursor is inside, and ctrl+click takes them all.
//
// A sector's edges came in three flavours — the floor ring, the ceiling ring
// above it, and the vertical run joining a corner's two handles — because a
// sector was a 2D polygon with a height field. A brush is an ordinary
// polyhedron, so its edges are just its edges, found by walking each face's
// index loop and keeping each undirected pair once. Two faces meeting along an
// edge report the same pair, which is what makes the shared edge one handle
// rather than two coincident ones.
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
	"foundry/internal/gizmo"
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	// edgeHandleHalfPx is half the on-screen thickness of an edge's selection
	// prism — narrower than vertexHandleHalfPx so a click near a corner reads
	// as the point.
	edgeHandleHalfPx float32 = 5
)

// edgeSel identifies one selectable brush edge by its two endpoint vertex
// indices. Like vertexSel it stores indices rather than pointers, so it
// survives geometry rebuilds.
//
// a is always the lower index, so the same edge reached from either of the two
// faces that share it is the same value and compares equal.
type edgeSel struct {
	chunkIdx int
	brushIdx int
	a, b     int // indices into the mesh selMesh resolves
	// detail says which of the brush's two meshes the endpoints index — see
	// vertexSel.detail.
	detail bool
}

// newEdgeSel orders the endpoints so an edge has one canonical form.
func newEdgeSel(chunkIdx, brushIdx, a, b int, detail bool) edgeSel {
	if b < a {
		a, b = b, a
	}
	return edgeSel{chunkIdx: chunkIdx, brushIdx: brushIdx, a: a, b: b, detail: detail}
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
	_, b := vertexSelBrush(sel.edgeBrush())
	if b == nil {
		return vertexSel{}, vertexSel{}, false
	}
	n := len(b.Verts)
	if sel.a < 0 || sel.a >= n || sel.b < 0 || sel.b >= n || sel.a == sel.b {
		return vertexSel{}, vertexSel{}, false
	}
	return vertexSel{chunkIdx: sel.chunkIdx, brushIdx: sel.brushIdx, vertIdx: sel.a, detail: sel.detail},
		vertexSel{chunkIdx: sel.chunkIdx, brushIdx: sel.brushIdx, vertIdx: sel.b, detail: sel.detail},
		true
}

// edgeBrush is the vertex selection that names this edge's brush, for reuse of
// vertexSelBrush's bounds checking. The vertex index is deliberately the first
// endpoint's, but nothing here reads it.
func (sel edgeSel) edgeBrush() vertexSel {
	return vertexSel{chunkIdx: sel.chunkIdx, brushIdx: sel.brushIdx, vertIdx: sel.a, detail: sel.detail}
}

// brushEdges lists a brush's distinct edges, walking every face's index loop
// and keeping each undirected pair once. A convex solid's every edge is shared
// by exactly two faces, so without the dedupe every handle would be doubled.
func brushEdges(chunkIdx, brushIdx int, faces [][]int, detail bool) []edgeSel {
	seen := map[[2]int]struct{}{}
	var out []edgeSel
	for _, loop := range faces {
		n := len(loop)
		if n < 2 {
			continue
		}
		for i := 0; i < n; i++ {
			sel := newEdgeSel(chunkIdx, brushIdx, loop[i], loop[(i+1)%n], detail)
			if sel.a == sel.b {
				continue
			}
			key := [2]int{sel.a, sel.b}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, sel)
		}
	}
	return out
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
	view := p.Sub(ws().camPos())
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

// collectEdgeHandles returns every brush edge whose endpoints are both within
// vertexHandleMaxDist of the camera and whose projected segment passes within
// maxPx of (mx, my), nearest first.
func collectEdgeHandles(mx, my int32, maxPx float32) []edgeHandle {
	lvl := ws().level
	if lvl == nil {
		return nil
	}
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	projView := ws().projView()

	var out []edgeHandle
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !ch.IsEnabled() {
			continue // not drawn, so no handles; see world/chunk_enable.go
		}
		for bi := range ch.Brushes {
			m, detail, ok := pickMeshFor(ci, bi, &ch.Brushes[bi])
			if !ok {
				continue
			}
			loops := make([][]int, len(m.Faces))
			for fi := range m.Faces {
				loops[fi] = m.Faces[fi].Indices
			}
			for _, sel := range brushEdges(ci, bi, loops, detail) {
				if h, ok := buildEdgeHandle(sel, projView, cursor, maxPx); ok {
					out = append(out, h)
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
		if a.brushIdx != b.brushIdx {
			return a.brushIdx < b.brushIdx
		}
		if a.a != b.a {
			return a.a < b.a
		}
		return a.b < b.b
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
	if p0.Sub(ws().camPos()).Len() > vertexHandleMaxDist || p1.Sub(ws().camPos()).Len() > vertexHandleMaxDist {
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
		depth:   center.Sub(ws().camPos()).Len(),
	}, true
}

// edgePrismContains reports whether the cursor's view ray passes through a
// handle's prism — the edge equivalent of handleBoxContains.
func edgePrismContains(h edgeHandle, mx, my int32) bool {
	origin, dir := ws().ray(mx, my)
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
// so edges that overlap on screen can all be reached.
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
// what undo records. A sector edge additionally noted the wall or portal it ran
// along, because that was a separate object with its own panel; a brush edge
// runs between two faces and belongs to neither, so there is nothing to note.
func setEdgeSelection(sels []edgeSel) bool {
	verts := edgeVertices(sels)
	if len(verts) == 0 {
		return false
	}
	setVertexSelection(verts)
	return true
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
	return true
}

// drawEdgeHandlePrisms queues the prisms for every edge within showPx of
// (mx, my). Caller sets up the depth state. Selected edges need no mark of
// their own: their endpoints are selected, so both ends already draw yellow.
func drawEdgeHandlePrisms(mx, my int32, showPx float32) {
	for _, h := range collectEdgeHandles(mx, my, showPx) {
		col := mgl32.Vec3{0.24, 0.38, 0.7}
		if edgePrismContains(h, mx, my) {
			col = mgl32.Vec3{1, 1, 1}
		}
		if isEdgeSelected(h.edgeSel) {
			col = mgl32.Vec3{1, 1, 0}
		}
		gizmo.GizmoOBB(h.center, h.axes, h.half, col)
	}
}

// distToSegment2D is the shortest distance from (px, py) to the segment
// (x0, y0)–(x1, y1), in screen pixels. Shared with the model and entity
// editors' own handle picking.
func distToSegment2D(px, py, x0, y0, x1, y1 float32) float32 {
	dx, dy := x1-x0, y1-y0
	lenSq := dx*dx + dy*dy
	if lenSq < 1e-9 {
		return float32(math.Hypot(float64(px-x0), float64(py-y0)))
	}
	t := ((px-x0)*dx + (py-y0)*dy) / lenSq
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return float32(math.Hypot(float64(px-(x0+t*dx)), float64(py-(y0+t*dy))))
}
