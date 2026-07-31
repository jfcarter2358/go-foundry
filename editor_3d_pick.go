package main

// editor_3d_pick.go — 3D-mode click picking.
//
// One ray cast resolves every clickable thing in the world — entities,
// primitives, and sector faces (walls, floors, ceilings) — into a single
// closest hit, so a click always selects whatever is actually nearest the
// camera along the ray. Sector faces and primitives are tested against their
// real triangles rather than their bounding boxes so that curved or thin
// geometry does not steal clicks from the surface behind it.
//
// On top of that, sector vertices get screen-space handle boxes that appear
// when the cursor is near them; clicking inside a box selects that vertex,
// repeated clicks cycle through every vertex whose box covers the cursor, and
// ctrl+click selects all of them at once.

import (
	"math"
	"sort"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// panelFocus* are the values EditorState.PanelFocus takes: the properties
// section a fresh selection wants the side panel to open and scroll to.
const (
	panelFocusNone      = ""
	panelFocusEntity    = "entity"
	panelFocusPrimitive = "primitive"
	panelFocusWall      = "wall"
	panelFocusFloor     = "floor"
	panelFocusCeiling   = "ceiling"
	panelFocusVertex    = "vertex"
)

// ---------- pick result ----------

// pickKind identifies what a 3D click landed on.
type pickKind string

const (
	pickNone      pickKind = ""
	pickEntity    pickKind = "entity"
	pickPrimitive pickKind = "primitive"
	pickWall      pickKind = "wall"
	pickFloor     pickKind = "floor"
	pickCeiling   pickKind = "ceiling"
)

// pickHit is the closest thing found along a picking ray. Which index fields
// are meaningful depends on kind:
//
//	entity     → idx = level entity index
//	primitive  → chunkIdx + idx = primitive index within that chunk
//	wall       → chunkIdx + sectorIdx + idx = wall index within that sector
//	floor/ceil → chunkIdx + sectorIdx
type pickHit struct {
	kind      pickKind
	t         float32
	chunkIdx  int
	sectorIdx int
	idx       int
}

// consider replaces h with a hit at distance t when that hit is closer.
func (h *pickHit) consider(t float32, kind pickKind, chunkIdx, sectorIdx, idx int) {
	if t >= h.t {
		return
	}
	h.kind = kind
	h.t = t
	h.chunkIdx = chunkIdx
	h.sectorIdx = sectorIdx
	h.idx = idx
}

// ---------- geometry ray tests ----------

// pickGeometryT returns the closest effective hit distance among all triangles
// in a [X,Y,Z,U,V] world-space vertex buffer, or +Inf when the ray misses.
func pickGeometryT(origin, dir mgl32.Vec3, verts []float32) float32 {
	best := float32(math.MaxFloat32)
	for i := 0; i+14 < len(verts); i += 15 {
		v0 := mgl32.Vec3{verts[i+0], verts[i+1], verts[i+2]}
		v1 := mgl32.Vec3{verts[i+5], verts[i+6], verts[i+7]}
		v2 := mgl32.Vec3{verts[i+10], verts[i+11], verts[i+12]}
		if h, t, front := rayTriangleIntersect(origin, dir, v0, v1, v2); h {
			if eff := effectivePickT(t, front); eff < best {
				best = eff
			}
		}
	}
	return best
}

// pickGeometriesT is pickGeometryT across a slice of geometries (extrusions).
func pickGeometriesT(origin, dir mgl32.Vec3, geoms []Geometry) float32 {
	best := float32(math.MaxFloat32)
	for i := range geoms {
		if t := pickGeometryT(origin, dir, geoms[i].Vertices); t < best {
			best = t
		}
	}
	return best
}

// pickModelT tests a ray against model-space geometry transformed by model.
// The ray is moved into model space, which leaves t unchanged (the inverse
// transform scales origin and direction together), so the returned distance is
// directly comparable with world-space hits. Returns +Inf on a miss or when the
// matrix is not invertible.
func pickModelT(origin, dir mgl32.Vec3, verts []float32, model mgl32.Mat4) float32 {
	inv := model.Inv()
	if inv == (mgl32.Mat4{}) {
		return float32(math.MaxFloat32)
	}
	o4 := inv.Mul4x1(mgl32.Vec4{origin.X(), origin.Y(), origin.Z(), 1})
	d4 := inv.Mul4x1(mgl32.Vec4{dir.X(), dir.Y(), dir.Z(), 0})
	return pickGeometryT(
		mgl32.Vec3{o4.X(), o4.Y(), o4.Z()},
		mgl32.Vec3{d4.X(), d4.Y(), d4.Z()},
		verts,
	)
}

// ---------- the picker ----------

// pick3D casts a ray through screen pixel (sx, sy) and returns the closest
// clickable thing in the level.
func pick3D(sx, sy int32) pickHit {
	hit := pickHit{kind: pickNone, t: float32(math.MaxFloat32), chunkIdx: -1, sectorIdx: -1, idx: -1}
	lvl := Editor.level
	if lvl == nil {
		return hit
	}
	origin, dir := screenRay(sx, sy)

	// Entities — exact mesh when one is loaded, AABB otherwise (an entity with
	// no model, such as the first-person player, is only its bounding box).
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if len(e.Geometry.Vertices) > 0 {
			if t := pickModelT(origin, dir, e.Geometry.Vertices, e.ModelMatrix()); t < float32(math.MaxFloat32) {
				hit.consider(t, pickEntity, -1, -1, i)
			}
			continue
		}
		bmin := e.Position.Sub(mgl32.Vec3{e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ})
		bmax := e.Position.Add(mgl32.Vec3{e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ})
		if boxHit, boxT := rayAABBIntersect(origin, dir, bmin, bmax); boxHit {
			hit.consider(boxT, pickEntity, -1, -1, i)
		}
	}

	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]

		// Primitives — exact mesh in model space, AABB as a fallback.
		for pi := range ch.Primitives {
			p := &ch.Primitives[pi]
			if len(p.Geometry.Vertices) > 0 {
				if t := pickModelT(origin, dir, p.Geometry.Vertices, p.ModelMatrix()); t < float32(math.MaxFloat32) {
					hit.consider(t, pickPrimitive, ci, -1, pi)
				}
				continue
			}
			bmin, bmax := p.WorldAABB()
			if boxHit, boxT := rayAABBIntersect(origin, dir, bmin, bmax); boxHit {
				hit.consider(boxT, pickPrimitive, ci, -1, pi)
			}
		}

		// Sector faces.
		for seci := range ch.Sectors {
			s := &ch.Sectors[seci]

			// Walls — the wall quad plus any extrusion side/cap geometry, so
			// clicking a protruding trim still selects the wall it belongs to.
			// Back faces are biased outward (see effectivePickT) so coincident
			// walls at chunk boundaries resolve to the one the user can see.
			for wi := range s.Walls {
				w := &s.Walls[wi]
				t := pickGeometryT(origin, dir, w.Geometry.Vertices)
				if et := pickGeometriesT(origin, dir, w.ExtrusionGeometries); et < t {
					t = et
				}
				hit.consider(t, pickWall, ci, seci, wi)
			}

			// Portals are deliberately not hit-tested: clicks pass through the
			// opening to whatever is behind it.

			if s.Floor.Enabled {
				t := pickSurfaceT(origin, dir, ch, s, &s.Floor.Geometry, s.Floor.ExtrusionGeometries,
					s.Floor.VertexHeight, false)
				hit.consider(t, pickFloor, ci, seci, -1)
			}
			if s.Ceiling.Enabled {
				t := pickSurfaceT(origin, dir, ch, s, &s.Ceiling.Geometry, s.Ceiling.ExtrusionGeometries,
					s.Ceiling.VertexHeight, true)
				hit.consider(t, pickCeiling, ci, seci, -1)
			}
		}
	}

	if hit.kind == pickNone {
		hit.t = 0
	}
	return hit
}

// pickSurfaceT ray-tests a floor or ceiling. The uploaded mesh is used when it
// is available (it accounts for terrain displacement, extrusion holes, and the
// real triangulation); a fan over the sector polygon is the fallback for a
// surface whose geometry has not been built yet.
func pickSurfaceT(origin, dir mgl32.Vec3, ch *Chunk, s *Sector, geo *Geometry,
	extrusions []Geometry, heightAt func(int) float32, reversed bool) float32 {

	best := pickGeometryT(origin, dir, geo.Vertices)
	if et := pickGeometriesT(origin, dir, extrusions); et < best {
		best = et
	}
	if len(geo.Vertices) > 0 {
		return best
	}

	pts := SectorPoints(ch, s)
	if len(pts) < 3 {
		return best
	}
	for i := 1; i < len(pts)-1; i++ {
		a := mgl32.Vec3{pts[0].X(), heightAt(0), pts[0].Y()}
		b := mgl32.Vec3{pts[i].X(), heightAt(i), pts[i].Y()}
		c := mgl32.Vec3{pts[i+1].X(), heightAt(i + 1), pts[i+1].Y()}
		if reversed {
			b, c = c, b
		}
		if h, t, front := rayTriangleIntersect(origin, dir, a, b, c); h {
			if eff := effectivePickT(t, front); eff < best {
				best = eff
			}
		}
	}
	return best
}

// ---------- screen-space helpers ----------

// projectToScreen projects a world point to window pixel coordinates through a
// prebuilt projection×view matrix. ok is false when the point is behind the
// camera. (worldToScreen is the 2D editor's top-down map transform — unrelated.)
func projectToScreen(projView mgl32.Mat4, p mgl32.Vec3) (mgl32.Vec2, bool) {
	clip := projView.Mul4x1(mgl32.Vec4{p.X(), p.Y(), p.Z(), 1})
	if clip.W() <= 1e-6 {
		return mgl32.Vec2{}, false
	}
	ndcX := clip.X() / clip.W()
	ndcY := clip.Y() / clip.W()
	return mgl32.Vec2{
		(ndcX*0.5 + 0.5) * float32(WinWidth),
		(1 - (ndcY*0.5 + 0.5)) * float32(WinHeight),
	}, true
}

// worldSizeForPixels returns the world-space half-extent that covers halfPx
// pixels vertically at point p, so a gizmo drawn with it keeps a constant
// on-screen size regardless of distance.
func worldSizeForPixels(p mgl32.Vec3, halfPx float32) float32 {
	f := Projection.At(1, 1)
	if f <= 0 || WinHeight == 0 {
		return 0.05
	}
	d := p.Sub(Cam.Position).Len()
	if d < 0.01 {
		d = 0.01
	}
	return d * halfPx * 2 / (float32(WinHeight) * f)
}

// ---------- vertex handles ----------

const (
	// vertexHandleHalfPx is half the on-screen size of a vertex handle box —
	// also its click target, so a click within this many pixels of the vertex
	// (in both axes) lands "inside the box".
	vertexHandleHalfPx float32 = 9
	// vertexHandleShowPx is how close the cursor must be to a vertex before its
	// handle box is drawn.
	vertexHandleShowPx float32 = 80
	// vertexHandleMaxDist is how far from the camera a vertex can be and still
	// get a handle. Without it, a distant vertex that happens to project under
	// the cursor would hijack clicks meant for the geometry in front of it.
	vertexHandleMaxDist float32 = 30
)

// vertexSel identifies one selected sector vertex handle. Selections are stored
// as indices rather than pointers so they survive geometry rebuilds.
type vertexSel struct {
	chunkIdx  int
	sectorIdx int
	vertIdx   int // index into Sector.PointIndices
	pointIdx  int // index into Chunk.Points
	isFloor   bool
}

// vertexHandle is one clickable sector vertex, at either its floor or its
// ceiling height. A point shared by several sectors yields one handle per
// sector, which is what repeated clicks cycle through.
type vertexHandle struct {
	vertexSel
	pos     mgl32.Vec3
	screen  mgl32.Vec2
	screenD float32 // pixel distance from the cursor
	depth   float32 // distance from the camera
}

// collectVertexHandles returns every sector vertex handle within
// vertexHandleMaxDist of the camera whose on-screen position is within maxPx of
// (mx, my), nearest first.
func collectVertexHandles(mx, my int32, maxPx float32) []vertexHandle {
	lvl := Editor.level
	if lvl == nil {
		return nil
	}
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	projView := Projection.Mul4(Cam.Matrix)

	var out []vertexHandle
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		for seci := range ch.Sectors {
			s := &ch.Sectors[seci]
			for vi, pIdx := range s.PointIndices {
				if pIdx < 0 || pIdx >= len(ch.Points) {
					continue
				}
				pt := ch.Points[pIdx]
				for _, isFloor := range [2]bool{true, false} {
					y := s.Ceiling.VertexHeight(vi)
					if isFloor {
						y = s.Floor.VertexHeight(vi)
					}
					pos := mgl32.Vec3{pt.X(), y, pt.Y()}
					depth := pos.Sub(Cam.Position).Len()
					if depth > vertexHandleMaxDist {
						continue
					}
					screen, ok := projectToScreen(projView, pos)
					if !ok {
						continue
					}
					d := screen.Sub(cursor).Len()
					if d > maxPx {
						continue
					}
					out = append(out, vertexHandle{
						vertexSel: vertexSel{
							chunkIdx:  ci,
							sectorIdx: seci,
							vertIdx:   vi,
							pointIdx:  pIdx,
							isFloor:   isFloor,
						},
						pos:     pos,
						screen:  screen,
						screenD: d,
						depth:   depth,
					})
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
		return a.isFloor && !b.isFloor
	})
	return out
}

// handleBoxContains reports whether the cursor is inside a handle's box.
func handleBoxContains(h vertexHandle, mx, my int32) bool {
	dx := float32(math.Abs(float64(h.screen.X() - float32(mx))))
	dy := float32(math.Abs(float64(h.screen.Y() - float32(my))))
	return dx <= vertexHandleHalfPx && dy <= vertexHandleHalfPx
}

// vertexHandlesInBox returns the handles whose box covers the cursor.
func vertexHandlesInBox(mx, my int32) []vertexHandle {
	var inBox []vertexHandle
	for _, h := range collectVertexHandles(mx, my, vertexHandleShowPx) {
		if handleBoxContains(h, mx, my) {
			inBox = append(inBox, h)
		}
	}
	return inBox
}

// isVertexSelected reports whether sel is part of the current selection.
func isVertexSelected(sel vertexSel) bool {
	for _, s := range Editor.SelectedVertices {
		if s == sel {
			return true
		}
	}
	return false
}

// pickVertexHandle returns the vertex handle a click at (mx, my) lands on.
// Clicking again without moving off the same cluster of boxes advances to the
// next candidate, so overlapping vertices (a shared point, or a floor and
// ceiling vertex seen edge-on) can all be reached.
func pickVertexHandle(mx, my int32) (vertexHandle, bool) {
	inBox := vertexHandlesInBox(mx, my)
	if len(inBox) == 0 {
		return vertexHandle{}, false
	}
	for i, h := range inBox {
		if isVertexSelected(h.vertexSel) {
			return inBox[(i+1)%len(inBox)], true
		}
	}
	return inBox[0], true
}

// selectVertexHandle replaces the selection with h and focuses its panel.
func selectVertexHandle(h vertexHandle) {
	setVertexSelection([]vertexSel{h.vertexSel})
}

// addVertexHandlesToSelection unions sels into the current selection — the
// ctrl+click path, which grabs every vertex whose box covers the cursor so
// they can be dragged or collapsed onto one coordinate together.
func addVertexHandlesToSelection(sels []vertexSel) {
	merged := append([]vertexSel{}, Editor.SelectedVertices...)
	for _, sel := range sels {
		dup := false
		for _, existing := range merged {
			if existing == sel {
				dup = true
				break
			}
		}
		if !dup {
			merged = append(merged, sel)
		}
	}
	setVertexSelection(merged)
}

// setVertexSelection makes sels the selection, clears every other selection,
// and points the panel at the vertex section. The first entry is the primary:
// it drives ActiveVertexSI/VI (shared with the 2D editor) and the panel's
// displayed values.
func setVertexSelection(sels []vertexSel) {
	Editor.SelectedVertices = sels
	Editor.ActiveEntityIdx = -1
	Editor.ActivePrimitiveIdx = -1
	Editor.ActiveEdgeKind = ""
	Editor.ActiveEdgeIdx = -1
	Editor.ActiveSurface = ""

	if len(sels) == 0 {
		Editor.ActiveVertexSI = -1
		Editor.ActiveVertexVI = -1
		Editor.GizmoKind = gizmoKindNone
		return
	}
	primary := sels[0]
	Editor.ActiveChunkIdx = primary.chunkIdx
	Editor.ActiveSectorIdx = primary.sectorIdx
	Editor.ActiveVertexSI = primary.sectorIdx
	Editor.ActiveVertexVI = primary.vertIdx
	Editor.EditFloorVerts = primary.isFloor
	Editor.GizmoKind = gizmoKindVertices
	Editor.PanelFocus = panelFocusVertex
}

// clearVertexSelection drops every selected vertex, and with it the vertex
// translate gizmo.
func clearVertexSelection() {
	Editor.SelectedVertices = nil
	Editor.ActiveVertexSI = -1
	Editor.ActiveVertexVI = -1
	if Editor.GizmoKind == gizmoKindVertices {
		Editor.GizmoKind = gizmoKindNone
	}
}

// vertexSelPos returns the world position of a selected vertex handle.
func vertexSelPos(sel vertexSel) (mgl32.Vec3, bool) {
	lvl := Editor.level
	if lvl == nil || sel.chunkIdx < 0 || sel.chunkIdx >= len(lvl.Chunks) {
		return mgl32.Vec3{}, false
	}
	ch := &lvl.Chunks[sel.chunkIdx]
	if sel.sectorIdx < 0 || sel.sectorIdx >= len(ch.Sectors) {
		return mgl32.Vec3{}, false
	}
	s := &ch.Sectors[sel.sectorIdx]
	if sel.vertIdx < 0 || sel.vertIdx >= len(s.PointIndices) {
		return mgl32.Vec3{}, false
	}
	pIdx := s.PointIndices[sel.vertIdx]
	if pIdx < 0 || pIdx >= len(ch.Points) {
		return mgl32.Vec3{}, false
	}
	pt := ch.Points[pIdx]
	y := s.Ceiling.VertexHeight(sel.vertIdx)
	if sel.isFloor {
		y = s.Floor.VertexHeight(sel.vertIdx)
	}
	return mgl32.Vec3{pt.X(), y, pt.Y()}, true
}

// selectedVerticesCenter returns the average position of the selected vertex
// handles — the anchor the translate gizmo sits on.
func selectedVerticesCenter() (mgl32.Vec3, bool) {
	var sum mgl32.Vec3
	n := 0
	for _, sel := range Editor.SelectedVertices {
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

// gizmoBox queues a wireframe box centred on p whose on-screen size stays
// constant at halfPx pixels regardless of distance.
func gizmoBox(p mgl32.Vec3, halfPx float32, col mgl32.Vec3) {
	half := worldSizeForPixels(p, halfPx)
	hv := mgl32.Vec3{half, half, half}
	GizmoAABB(p.Sub(hv), p.Add(hv), col)
}

// editorRenderInteractiveOverlays draws the handle overlays when the cursor is
// in the viewport and the next click is not already spoken for by a placement
// mode. An in-progress axis drag always draws, so the gizmo does not vanish when
// the cursor crosses the side panel mid-drag.
func editorRenderInteractiveOverlays(projView mgl32.Mat4, withVertexHandles bool) {
	mx, my, _ := sdl.GetMouseState()
	if !translateGizmoDragging() {
		if gizmoSuppressed() || imgui.CurrentIO().WantCaptureMouse() || mx >= panelLeftEdgeX() {
			return
		}
	}
	editorRenderHandleOverlays(projView, mx, my, withVertexHandles)
}

// editorRenderHandleOverlays draws the interactive handles — vertex boxes near
// the cursor (3D mode only) and the translate gizmo — on top of the world.
//
// Any gizmos queued so far are flushed first, then the overlays are drawn with
// depth testing off so a handle is visible wherever it is clickable, including
// the corners of a sector on the far side of a wall.
func editorRenderHandleOverlays(projView mgl32.Mat4, mx, my int32, withVertexHandles bool) {
	GizmoFlush(projView)
	prevDepth := gl.IsEnabled(gl.DEPTH_TEST)
	gl.Disable(gl.DEPTH_TEST)
	defer func() {
		GizmoFlush(projView)
		if prevDepth {
			gl.Enable(gl.DEPTH_TEST)
		}
	}()

	if withVertexHandles {
		// Selected vertices stay marked wherever they are, so a multi-selection
		// is visible while the cursor is over the gizmo instead of the vertices.
		for _, sel := range Editor.SelectedVertices {
			if pos, ok := vertexSelPos(sel); ok {
				gizmoBox(pos, vertexHandleHalfPx, mgl32.Vec3{1, 1, 0})
			}
		}

		// Floor handles are blue and ceiling handles red (matching the 2D
		// editor's vertex-mode buttons); selected is yellow, and the handle
		// under the cursor is white.
		for _, h := range collectVertexHandles(mx, my, vertexHandleShowPx) {
			col := mgl32.Vec3{0.35, 0.55, 1}
			if !h.isFloor {
				col = mgl32.Vec3{1, 0.45, 0.35}
			}
			if handleBoxContains(h, mx, my) {
				col = mgl32.Vec3{1, 1, 1}
			}
			if isVertexSelected(h.vertexSel) {
				col = mgl32.Vec3{1, 1, 0}
			}
			gizmoBox(h.pos, vertexHandleHalfPx, col)
		}
	}

	renderTranslateGizmo(mx, my)
}

// handleVertexClick resolves a click against the vertex handle boxes. A plain
// click selects one vertex and cycles through overlapping ones on repeat;
// ctrl+click adds every vertex under the cursor to the selection so they can be
// dragged or collapsed onto a shared coordinate together. Returns false when the
// click missed every handle, leaving it to the geometry picker.
func handleVertexClick(mx, my int32, ctrlHeld bool) bool {
	if !ctrlHeld {
		h, ok := pickVertexHandle(mx, my)
		if !ok {
			return false
		}
		selectVertexHandle(h)
		return true
	}

	inBox := vertexHandlesInBox(mx, my)
	if len(inBox) == 0 {
		return false
	}
	sels := make([]vertexSel, 0, len(inBox))
	for _, h := range inBox {
		sels = append(sels, h.vertexSel)
	}
	addVertexHandlesToSelection(sels)
	return true
}
