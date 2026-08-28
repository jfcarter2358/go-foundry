package editor

// editor_surface_pick.go — clicking and dragging a surface mesh in the 3D view.
//
// Picking is done in screen space rather than by ray-casting the mesh. A point
// or an edge has no area to hit, so a ray test would need a tolerance in world
// units, which is the wrong unit: how close a click has to be is a question
// about pixels, and a tolerance that feels right against a wall you are
// standing at is invisible from across the room. Faces are the exception and
// are picked by the ray, because a face genuinely is a surface the ray either
// meets or does not.
//
// Everything here works in the *chunk's* space and converts at the edges. A
// chunk owned by a frame (a vehicle, a lift) has a model matrix, and a surface
// on it has to be pickable while the whole thing is moving.

import (
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// surfacePickRadiusPx is how close a click has to land to grab a point or an
// edge. Matched to the vertex handles the 3D view already draws, so the thing
// you can see is the thing you can hit.
const surfacePickRadiusPx = float32(10)

// surfacePointScreen projects one mesh point to the screen.
func surfacePointScreen(ch *world.Chunk, frame world.SurfaceFrame, p world.SurfacePoint) (mgl32.Vec2, bool) {
	world3 := ch.LocalToWorld(frame.ToWorld(p.U, p.V, p.Depth))
	return projectToScreen(gfx.Projection.Mul4(gfx.Cam.Matrix), world3)
}

// surfacePickPoint returns the mesh point nearest the cursor within the pick
// radius.
func surfacePickPoint(mx, my int32) (int, bool) {
	ch, _, mesh, frame, ok := surfaceEditTarget()
	if !ok {
		return -1, false
	}
	best, bestD := -1, surfacePickRadiusPx*surfacePickRadiusPx
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	for i, p := range mesh.Points {
		sp, vis := surfacePointScreen(ch, frame, p)
		if !vis {
			continue
		}
		if d := sp.Sub(cursor).LenSqr(); d <= bestD {
			best, bestD = i, d
		}
	}
	return best, best >= 0
}

// surfacePickEdge returns the mesh edge nearest the cursor, measured to the
// projected segment rather than to its ends, so a long edge is grabbable
// anywhere along it.
func surfacePickEdge(mx, my int32) (surfaceEdgeSel, bool) {
	ch, _, mesh, frame, ok := surfaceEditTarget()
	if !ok {
		return surfaceEdgeSel{}, false
	}
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	best, bestD := surfaceEdgeSel{}, surfacePickRadiusPx*surfacePickRadiusPx
	found := false
	seen := map[surfaceEdgeSel]bool{}
	for fi := range mesh.Faces {
		ring := mesh.Faces[fi].Verts
		for i := range ring {
			a, b := ring[i], ring[(i+1)%len(ring)]
			e := makeSurfaceEdgeSel(a, b)
			if seen[e] || a >= len(mesh.Points) || b >= len(mesh.Points) {
				continue
			}
			seen[e] = true
			pa, va := surfacePointScreen(ch, frame, mesh.Points[a])
			pb, vb := surfacePointScreen(ch, frame, mesh.Points[b])
			if !va || !vb {
				continue
			}
			if d := surfacePointSegDist2(cursor, pa, pb); d <= bestD {
				best, bestD, found = e, d, true
			}
		}
	}
	return best, found
}

// surfacePointSegDist2 is the squared distance from p to segment ab.
func surfacePointSegDist2(p, a, b mgl32.Vec2) float32 {
	ab := b.Sub(a)
	l2 := ab.LenSqr()
	if l2 < 1e-9 {
		return p.Sub(a).LenSqr()
	}
	t := p.Sub(a).Dot(ab) / l2
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return p.Sub(a.Add(ab.Mul(t))).LenSqr()
}

// surfacePickFace returns the mesh face the cursor's ray meets, nearest first.
//
// Hole faces are pickable: an opening still has a ring, and being able to
// select it is what lets you close it again or drag its shape. That is the
// whole reason a hole is a face marked open rather than a face deleted.
func surfacePickFace(mx, my int32) (int, mgl32.Vec3, bool) {
	ch, _, mesh, frame, ok := surfaceEditTarget()
	if !ok {
		return -1, mgl32.Vec3{}, false
	}
	origin, dir := screenRay(mx, my)
	origin, dir = ch.WorldToLocal(origin), ch.WorldToLocalDirection(dir)

	best, bestT := -1, float32(-1)
	var bestHit mgl32.Vec3
	for fi := range mesh.Faces {
		ring := mesh.Faces[fi].Verts
		if len(ring) < 3 {
			continue
		}
		pts := make([]mgl32.Vec3, 0, len(ring))
		bad := false
		for _, v := range ring {
			if v < 0 || v >= len(mesh.Points) {
				bad = true
				break
			}
			p := mesh.Points[v]
			pts = append(pts, frame.ToWorld(p.U, p.V, p.Depth))
		}
		if bad {
			continue
		}
		for _, tri := range geom.TriangulatePolygon3D(pts) {
			t, hit := rayTriangleT(origin, dir, pts[tri[0]], pts[tri[1]], pts[tri[2]])
			if !hit || t < 0 {
				continue
			}
			if best < 0 || t < bestT {
				best, bestT = fi, t
				bestHit = origin.Add(dir.Mul(t))
			}
		}
	}
	return best, bestHit, best >= 0
}

// rayTriangleT is Möller–Trumbore, double-sided: a surface can legitimately be
// looked at from behind — a ceiling from above, a wall from the far room — and
// refusing the back face would make half of them unclickable.
func rayTriangleT(origin, dir, a, b, c mgl32.Vec3) (float32, bool) {
	const eps = 1e-7
	e1 := b.Sub(a)
	e2 := c.Sub(a)
	h := dir.Cross(e2)
	det := e1.Dot(h)
	if det > -eps && det < eps {
		return 0, false
	}
	inv := 1 / det
	s := origin.Sub(a)
	u := inv * s.Dot(h)
	if u < 0 || u > 1 {
		return 0, false
	}
	q := s.Cross(e1)
	v := inv * dir.Dot(q)
	if v < 0 || u+v > 1 {
		return 0, false
	}
	return inv * e2.Dot(q), true
}

// SurfaceEditHandleClick processes a click while surface mode is on, returning
// true when it was consumed.
//
// The depth-drag arm is offered first: it sits right where the selection's own
// handles are, so a click aimed at it must never fall through to picking and
// throw away the selection the arm exists to move.
func SurfaceEditHandleClick(t sdl.MouseButtonEvent, ctrlHeld bool) bool {
	if !surfaceEditActive() || t.Button != sdl.ButtonLeft {
		return false
	}
	if surfaceDragMouseDown(t.X, t.Y) {
		return true
	}
	switch Editor.SurfaceMode {
	case surfaceSelPoint:
		i, ok := surfacePickPoint(t.X, t.Y)
		if !ok {
			if !ctrlHeld {
				Editor.SurfaceVerts = nil
			}
			return true
		}
		Editor.SurfaceVerts = toggleIntSel(Editor.SurfaceVerts, i, ctrlHeld)
	case surfaceSelEdge:
		e, ok := surfacePickEdge(t.X, t.Y)
		if !ok {
			if !ctrlHeld {
				Editor.SurfaceEdges = nil
			}
			return true
		}
		Editor.SurfaceEdges = toggleEdgeSel(Editor.SurfaceEdges, e, ctrlHeld)
	case surfaceSelFace:
		fi, _, ok := surfacePickFace(t.X, t.Y)
		if !ok {
			if !ctrlHeld {
				Editor.SurfaceFaces = nil
			}
			return true
		}
		Editor.SurfaceFaces = toggleIntSel(Editor.SurfaceFaces, fi, ctrlHeld)
		// A fresh pick is not a continuation of the last extrude, so E on it
		// starts a new one rather than stacking onto a face it has left.
		Editor.SurfacePending = surfacePendingExtrude{}
	}
	return true
}

// toggleIntSel adds, replaces or removes an index depending on the modifier —
// the same ctrl-click convention the level editor's own selections follow.
func toggleIntSel(cur []int, i int, add bool) []int {
	if !add {
		return []int{i}
	}
	for j, v := range cur {
		if v == i {
			return append(cur[:j:j], cur[j+1:]...)
		}
	}
	return append(cur, i)
}

func toggleEdgeSel(cur []surfaceEdgeSel, e surfaceEdgeSel, add bool) []surfaceEdgeSel {
	if !add {
		return []surfaceEdgeSel{e}
	}
	for j, v := range cur {
		if v == e {
			return append(cur[:j:j], cur[j+1:]...)
		}
	}
	return append(cur, e)
}

// ---------- the drag arms ----------

// surfaceDragAxes are the three directions a surface selection can be dragged,
// in the surface's own terms rather than the world's.
//
// Depth first, and coloured like the Y arm, because it is the one that matters:
// it is what turns a flat face into a bump or a recess, and on a wall it points
// along no world axis at all. Offering only X/Y/Z would mean shaping a wall
// that runs diagonally across the map by dragging two arms at once.
var surfaceDragAxes = [3]struct {
	Name  string
	Color mgl32.Vec3
}{
	{"depth", mgl32.Vec3{0.35, 1, 0.35}},
	{"U", mgl32.Vec3{1, 0.35, 0.35}},
	{"V", mgl32.Vec3{0.35, 0.55, 1}},
}

// surfaceAxisDir is the world direction of one of the drag arms, at the given
// point of the surface.
//
// Derived by sampling the frame a short step away rather than from a formula,
// because a wall's V axis is not vertical when its two ends sit at different
// heights, and a floor's U and V are only world X and Z until the chunk is
// rotated. Asking the frame where a step lands is the one answer that is right
// for every surface and every transform.
func surfaceAxisDir(ch *world.Chunk, frame world.SurfaceFrame, u, v, depth float32, axis int) mgl32.Vec3 {
	const step = float32(0.05)
	base := frame.ToWorld(u, v, depth)
	var to mgl32.Vec3
	switch axis {
	case 0:
		to = frame.ToWorld(u, v, depth+step)
	case 1:
		to = frame.ToWorld(u+step, v, depth)
	default:
		to = frame.ToWorld(u, v+step, depth)
	}
	d := ch.LocalToWorldDirection(to.Sub(base))
	if d.Len() < 1e-6 {
		return mgl32.Vec3{0, 1, 0}
	}
	return d.Normalize()
}

// surfaceDragOrigin is where the arms are drawn, plus the parametric position
// they are sampled at.
func surfaceDragOrigin() (mgl32.Vec3, float32, float32, float32, bool) {
	_, _, mesh, _, ok := surfaceEditTarget()
	if !ok {
		return mgl32.Vec3{}, 0, 0, 0, false
	}
	pts := surfaceSelectedPoints(mesh)
	if len(pts) == 0 {
		return mgl32.Vec3{}, 0, 0, 0, false
	}
	var u, v, d float32
	for _, i := range pts {
		p := mesh.Points[i]
		u, v, d = u+p.U, v+p.V, d+p.Depth
	}
	inv := 1 / float32(len(pts))
	u, v, d = u*inv, v*inv, d*inv
	centre, ok := surfaceSelectionCenter()
	return centre, u, v, d, ok
}

// surfaceDragMouseDown starts an arm drag if the click grabbed one.
func surfaceDragMouseDown(mx, my int32) bool {
	ch, _, mesh, frame, ok := surfaceEditTarget()
	if !ok {
		return false
	}
	origin, u, v, d, ok := surfaceDragOrigin()
	if !ok {
		return false
	}
	axis := surfacePickDragAxis(ch, frame, origin, u, v, d, mx, my)
	if axis < 0 {
		return false
	}
	dir := surfaceAxisDir(ch, frame, u, v, d, axis)
	param, ok := axisParamForRayAt(origin, dir, mx, my)
	if !ok {
		// The arm is edge-on. The click is still consumed — the arms are drawn
		// on top of everything and sit where the handles are, so letting it
		// fall through would clear the selection the arm exists to move.
		return true
	}

	pts := surfaceSelectedPoints(mesh)
	if len(pts) == 0 {
		return true
	}
	EditorPushUndo()
	Editor.SurfaceDragDepth = true
	Editor.SurfaceDragAxis = axis
	Editor.SurfaceDragParam = param
	Editor.SurfaceDragPts = pts
	Editor.SurfaceDragStart = make([]float32, len(pts))
	for i, pi := range pts {
		Editor.SurfaceDragStart[i] = surfaceAxisValue(mesh.Points[pi], axis)
	}
	return true
}

// surfaceAxisValue reads the component of a point the given arm moves.
func surfaceAxisValue(p world.SurfacePoint, axis int) float32 {
	switch axis {
	case 0:
		return p.Depth
	case 1:
		return p.U
	default:
		return p.V
	}
}

// surfaceSetAxisValue writes it back.
//
// A boundary point refuses U and V: its position across the surface belongs to
// the sector's outline, and letting a drag move it here would put the mesh and
// the 2D map into disagreement that the next rebuild would silently undo. Depth
// is allowed, because a floor's corner height is the surface's own business —
// it is what VertexOffsets already is.
func surfaceSetAxisValue(p *world.SurfacePoint, axis int, val float32) {
	switch axis {
	case 0:
		p.Depth = val
	case 1:
		if p.Boundary < 0 {
			p.U = val
		}
	default:
		if p.Boundary < 0 {
			p.V = val
		}
	}
}

// surfacePickDragAxis returns the arm a click grabs, or -1.
func surfacePickDragAxis(ch *world.Chunk, frame world.SurfaceFrame, origin mgl32.Vec3, u, v, d float32, mx, my int32) int {
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
	armLen := gizmoArmLength(origin)
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	base, ok := projectToScreen(projView, origin)
	if !ok {
		return -1
	}
	best, bestD := -1, float32(surfacePickRadiusPx*surfacePickRadiusPx)
	for axis := 0; axis < 3; axis++ {
		dir := surfaceAxisDir(ch, frame, u, v, d, axis)
		tip, ok := projectToScreen(projView, origin.Add(dir.Mul(armLen)))
		if !ok {
			continue
		}
		if dd := surfacePointSegDist2(cursor, base, tip); dd <= bestD {
			best, bestD = axis, dd
		}
	}
	return best
}

// axisParamForRayAt maps the cursor onto an axis through the main window's
// camera — axisParamForRay with screenRay already applied.
func axisParamForRayAt(origin, axis mgl32.Vec3, mx, my int32) (float32, bool) {
	o, dir := screenRay(mx, my)
	return axisParamForRay(o, dir, origin, axis)
}

// SurfaceEditHandleMotion applies an in-progress arm drag.
func SurfaceEditHandleMotion(mx, my int32) bool {
	if !Editor.SurfaceDragDepth {
		return false
	}
	ch, s, mesh, frame, ok := surfaceEditTarget()
	if !ok {
		Editor.SurfaceDragDepth = false
		return false
	}
	origin, u, v, d, ok := surfaceDragOrigin()
	if !ok {
		return true
	}
	dir := surfaceAxisDir(ch, frame, u, v, d, Editor.SurfaceDragAxis)
	param, ok := axisParamForRayAt(origin, dir, mx, my)
	if !ok {
		return true
	}
	// Offset from the snapshot rather than accumulating per-event deltas, so a
	// long drag stays exact.
	delta := param - Editor.SurfaceDragParam
	for i, pi := range Editor.SurfaceDragPts {
		if pi < 0 || pi >= len(mesh.Points) {
			continue
		}
		surfaceSetAxisValue(&mesh.Points[pi], Editor.SurfaceDragAxis, Editor.SurfaceDragStart[i]+delta)
	}
	// An extrude still being sized has to re-derive its sides' UVs as the depth
	// changes; they were created before the depth existed.
	if len(Editor.SurfacePending.Sides) > 0 {
		mesh.RefreshSurfaceSideUVs(Editor.SurfacePending.Sides)
	}
	surfaceRebuild(ch, s)
	surfaceSetStatus("%s %+.3f", surfaceDragAxes[Editor.SurfaceDragAxis].Name, delta)
	return true
}

// SurfaceEditHandleRelease ends an arm drag.
func SurfaceEditHandleRelease() bool {
	if !Editor.SurfaceDragDepth {
		return false
	}
	Editor.SurfaceDragDepth = false
	Editor.SurfaceDragPts = nil
	Editor.SurfaceDragStart = nil
	return true
}
