package main

// editor_3d_pick.go — 3D-mode click picking.
//
// One ray cast resolves every clickable thing in the world — the spawn point,
// the entities, the volumes and the sector faces (walls, floors, ceilings) —
// into a single closest hit, so a click always selects whatever is actually
// nearest the camera along the ray. Sector faces and primitives are tested
// against their real triangles rather than their bounding boxes so that curved
// or thin geometry does not steal clicks from the surface behind it.
//
// On top of that, sector vertices get screen-space handle boxes that appear
// when the cursor is near them; clicking inside a box selects that vertex,
// repeated clicks cycle through every vertex whose box covers the cursor, and
// ctrl+click selects all of them at once.

import (
	"foundry/internal/gfx"
	"foundry/internal/platform"
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
	panelFocusNone    = ""
	panelFocusSpawn   = "spawn"
	panelFocusWall    = "wall"
	panelFocusFloor   = "floor"
	panelFocusCeiling = "ceiling"
	panelFocusVertex  = "vertex"
	panelFocusWater   = "water"
	panelFocusEntity  = "entity"
	panelFocusVolume  = "volume"
)

// ---------- pick result ----------

// pickKind identifies what a 3D click landed on.
type pickKind string

const (
	pickNone    pickKind = ""
	pickSpawn   pickKind = "spawn"
	pickWall    pickKind = "wall"
	pickFloor   pickKind = "floor"
	pickCeiling pickKind = "ceiling"
	pickWater   pickKind = "water"
	pickEntity  pickKind = "entity"
	pickVolume  pickKind = "volume"
)

// pickHit is the closest thing found along a picking ray. Which index fields
// are meaningful depends on kind:
//
//	spawn      → none; the level has at most one spawn point
//	entity     → idx = index into level.Entities
//	volume     → idx = index into level.Volumes
//	wall       → chunkIdx + sectorIdx + idx = wall index within that sector
//	floor/ceil → chunkIdx + sectorIdx
//	water      → chunkIdx + idx = surface index within that chunk
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
func pickGeometriesT(origin, dir mgl32.Vec3, geoms []gfx.Geometry) float32 {
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

// pickEntityT returns the closest hit on an entity: its attachments' triangles
// when it has any uploaded, its bounding volume otherwise.
//
// The fallback matters more here than it does for the spawn marker. An entity
// is legitimately model-less — a trigger volume, a script anchor — and one that
// could not be clicked would be one the editor could place but never select
// again.
func pickEntityT(origin, dir mgl32.Vec3, e *Entity) float32 {
	best := float32(math.MaxFloat32)
	world := e.ModelMatrix()
	for mi := range e.Models {
		m := &e.Models[mi]
		if !m.Visible {
			continue
		}
		model := world.Mul4(m.LocalMatrix())
		for si := range m.Submeshes {
			if t := pickModelT(origin, dir, m.Submeshes[si].Vertices, model); t < best {
				best = t
			}
		}
	}
	if best < float32(math.MaxFloat32) {
		return best
	}
	if bmin, bmax, ok := e.WorldAABB(); ok {
		if boxHit, boxT := rayAABBIntersect(origin, dir, bmin, bmax); boxHit {
			return boxT
		}
	}
	return float32(math.MaxFloat32)
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

	// The spawn marker — exact mesh when it has been uploaded, its AABB
	// otherwise. It is the only non-geometry thing in the level a click can
	// land on.
	if sp := lvl.Spawn; sp != nil {
		if len(sp.Geometry.Vertices) > 0 {
			if t := pickModelT(origin, dir, sp.Geometry.Vertices, sp.ModelMatrix()); t < float32(math.MaxFloat32) {
				hit.consider(t, pickSpawn, -1, -1, -1)
			}
		} else {
			bmin, bmax := sp.WorldAABB()
			if boxHit, boxT := rayAABBIntersect(origin, dir, bmin, bmax); boxHit {
				hit.consider(boxT, pickSpawn, -1, -1, -1)
			}
		}
	}

	// Placed entities — every attachment's real triangles, so a click lands on
	// the model the user can see rather than on the box around it. An entity
	// with nothing uploaded (no models, or a model that failed to load) falls
	// back to its bounding volume, which is the only thing left to aim at.
	for ei := range lvl.Entities {
		e := &lvl.Entities[ei]
		if !e.Visible {
			continue
		}
		if t := pickEntityT(origin, dir, e); t < float32(math.MaxFloat32) {
			hit.consider(t, pickEntity, -1, -1, ei)
		}
	}

	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]

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

		// Water planes. They are derived from a Water-kind biome sector rather
		// than placed, but they are still a surface the user points at and
		// wants the settings for, so a click resolves to the sector that owns
		// one (see editorWaterSectorFor). Tested last only because the loop
		// reads better that way — hit.consider sorts by distance, not order.
		for wi := range ch.WaterSurfaces {
			w := &ch.WaterSurfaces[wi]
			if len(w.Geometry.Vertices) == 0 {
				continue
			}
			if t := pickModelT(origin, dir, w.Geometry.Vertices, w.ModelMatrix()); t < float32(math.MaxFloat32) {
				hit.consider(t, pickWater, ci, -1, wi)
			}
		}
	}

	// Volumes, tested last so that a tie goes to the world: hit.consider keeps
	// the first hit at a given distance, and a region drawn as a wireframe cage
	// should not win against a surface it happens to sit flush with.
	for vi := range lvl.Volumes {
		if t := pickVolumeRayT(origin, dir, &lvl.Volumes[vi]); t < float32(math.MaxFloat32) {
			hit.consider(t, pickVolume, -1, -1, vi)
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
func pickSurfaceT(origin, dir mgl32.Vec3, ch *Chunk, s *Sector, geo *gfx.Geometry,
	extrusions []gfx.Geometry, heightAt func(int) float32, reversed bool) float32 {

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
	return projectToScreenIn(projView, p, platform.WinWidth, platform.WinHeight)
}

// projectToScreenIn is projectToScreen against an explicit viewport size, for
// callers that do not draw into the main window — the model editor pop-out has
// its own dimensions and its own projection (editor_model_pick.go).
func projectToScreenIn(projView mgl32.Mat4, p mgl32.Vec3, viewW, viewH int32) (mgl32.Vec2, bool) {
	clip := projView.Mul4x1(mgl32.Vec4{p.X(), p.Y(), p.Z(), 1})
	if clip.W() <= 1e-6 {
		return mgl32.Vec2{}, false
	}
	ndcX := clip.X() / clip.W()
	ndcY := clip.Y() / clip.W()
	return mgl32.Vec2{
		(ndcX*0.5 + 0.5) * float32(viewW),
		(1 - (ndcY*0.5 + 0.5)) * float32(viewH),
	}, true
}

// worldSizeForPixels returns the world-space half-extent that covers halfPx
// pixels vertically at point p, so a gizmo drawn with it keeps a constant
// on-screen size regardless of distance.
func worldSizeForPixels(p mgl32.Vec3, halfPx float32) float32 {
	return worldSizeForPixelsIn(p, gfx.Cam.Position, halfPx, gfx.Projection.At(1, 1), platform.WinHeight)
}

// worldSizeForPixelsIn is worldSizeForPixels against an explicit camera,
// projection and viewport height, so a secondary window can size its handles
// from its own view rather than the main window's. projYY is the projection
// matrix's [1][1] term (the vertical focal scale).
func worldSizeForPixelsIn(p, camPos mgl32.Vec3, halfPx, projYY float32, viewH int32) float32 {
	if projYY <= 0 || viewH == 0 {
		return 0.05
	}
	d := p.Sub(camPos).Len()
	if d < 0.01 {
		d = 0.01
	}
	return d * halfPx * 2 / (float32(viewH) * projYY)
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
	// vertexHandleShowAllPx replaces vertexHandleShowPx when the "show all
	// vertex boxes" toggle is on — wider than any screen, so cursor proximity
	// stops filtering and every in-range vertex draws its box.
	vertexHandleShowAllPx float32 = 1e6
)

// vertexSel identifies one selected point. Selections are stored as indices
// rather than pointers so they survive geometry rebuilds.
//
// Almost always that is a sector vertex handle — one corner of one sector, at
// its floor or its ceiling height. The exception is a *free* point: a chunk
// point that belongs to no sector yet, which the 2D map can select so it can be
// joined into one (editor_2d_join.go). A free point has no sector and so no
// vertIdx and no height of its own; see freePointSel.
type vertexSel struct {
	chunkIdx  int
	sectorIdx int
	vertIdx   int // index into Sector.PointIndices; -1 for a free point
	pointIdx  int // index into Chunk.Points
	isFloor   bool
}

// freePointSel names a chunk point that is in no sector.
//
// isFloor is fixed rather than following the map's floor/ceiling switch: a free
// point has one position and no heights, so letting the switch vary it would
// make the same point two different selections depending on which way a toggle
// happened to be set.
func freePointSel(chunkIdx, pointIdx int) vertexSel {
	return vertexSel{chunkIdx: chunkIdx, sectorIdx: -1, vertIdx: -1, pointIdx: pointIdx, isFloor: true}
}

// isFreePoint reports whether this selection is a chunk point with no sector
// behind it, which is what every "…and a sector" step has to skip.
func (s vertexSel) isFreePoint() bool { return s.sectorIdx < 0 }

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
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)

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
					depth := pos.Sub(gfx.Cam.Position).Len()
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
					sel := vertexSel{
						chunkIdx:  ci,
						sectorIdx: seci,
						vertIdx:   vi,
						pointIdx:  pIdx,
						isFloor:   isFloor,
					}
					// Solid shading hides the far side of the world, so a
					// handle behind a wall is neither drawn nor clickable;
					// wireframe answers true for everything.
					if !editorVertexVisible(sel, pos) {
						continue
					}
					out = append(out, vertexHandle{
						vertexSel: sel,
						pos:       pos,
						screen:    screen,
						screenD:   d,
						depth:     depth,
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
	editorClearSpawnSelection()
	editorClearEntitySelection()
	editorClearVolumeSelection()
	clearFaceSelection()

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
	// A free point has no height of its own, so it must not drag the map's
	// floor/ceiling switch with it — that switch decides which handles the next
	// click reaches, and a point with neither should leave it alone.
	if !primary.isFreePoint() {
		Editor.EditFloorVerts = primary.isFloor
	}
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

// vertexSelPos returns the world position of a selected vertex handle. A free
// point sits at y=0, which is where the 2D map draws it — it has no floor or
// ceiling to take a height from until it is part of a sector.
func vertexSelPos(sel vertexSel) (mgl32.Vec3, bool) {
	lvl := Editor.level
	if lvl == nil || sel.chunkIdx < 0 || sel.chunkIdx >= len(lvl.Chunks) {
		return mgl32.Vec3{}, false
	}
	ch := &lvl.Chunks[sel.chunkIdx]
	if sel.isFreePoint() {
		if sel.pointIdx < 0 || sel.pointIdx >= len(ch.Points) {
			return mgl32.Vec3{}, false
		}
		pt := ch.Points[sel.pointIdx]
		return mgl32.Vec3{pt.X(), 0, pt.Y()}, true
	}
	if sel.sectorIdx >= len(ch.Sectors) {
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

// editorRenderInteractiveOverlays draws everything that goes on top of the
// world: the selected face's outline, the handles the current selection mode
// can act on, and whichever gizmo the current tool shows.
//
// All of it is drawn with depth testing off, so a mark is visible wherever it
// is meaningful — a handle at the corner of a sector on the far side of a wall,
// or the outline of the floor you just picked from across the room. (What is
// *clickable* is a separate question, and solid shading answers it by culling
// occluded handles before they get here — see editor_3d_visibility.go.)
//
// The cursor counts as off the world when it is over the side panel or an ImGui
// window, or when a placement mode has already spoken for the next click. An
// in-progress axis drag overrides that, so the gizmo does not vanish when the
// cursor crosses the panel mid-drag.
func editorRenderInteractiveOverlays(projView mgl32.Mat4, withVertexHandles bool) {
	mx, my, _ := sdl.GetMouseState()
	offWorld := !editorGizmoDragging() &&
		(gizmoSuppressed() || imgui.CurrentIO().WantCaptureMouse() || mx >= panelLeftEdgeX())

	GizmoFlush(projView)
	prevDepth := gl.IsEnabled(gl.DEPTH_TEST)
	gl.Disable(gl.DEPTH_TEST)
	defer func() {
		GizmoFlush(projView)
		if prevDepth {
			gl.Enable(gl.DEPTH_TEST)
		}
	}()

	// Drawn whatever the cursor is doing: the selection does not stop existing
	// because the pointer moved onto the panel.
	drawSelectedFaceOutline()
	drawSelectedVolumeOutline()

	if offWorld {
		// Nothing is under the cursor, so no handle reads as hovered and the
		// gizmo stays hidden — but a view that draws the whole cage (wireframe,
		// or the show-all toggle) still wants every handle drawn, not least
		// while the user is over the panel holding those switches.
		if withVertexHandles && editorShowsAllHandles() {
			drawSelModeHandles(math.MinInt32/2, math.MinInt32/2, vertexHandleShowAllPx)
		}
		return
	}

	if withVertexHandles {
		drawSelModeHandles(mx, my, editorHandleShowPx())
	}
	renderEditorGizmo(mx, my)
}

// drawSelModeHandles queues the handles the current selection mode can act on,
// and only those: point mode draws vertex boxes, edge mode draws edge prisms,
// and face mode draws neither, because a click there goes to the surface or the
// object behind them. Drawing a handle no click can reach was the confusing
// part of the pre-mode viewport, where every box was always up.
func drawSelModeHandles(mx, my int32, showPx float32) {
	switch Editor.SelMode {
	case editorSelPoint:
		drawVertexHandleBoxes(mx, my, showPx)
	case editorSelEdge:
		drawEdgeHandlePrisms(mx, my, showPx)
	}
}

// drawVertexHandleBoxes queues the selection marks and the handle boxes for
// every vertex within showPx of (mx, my). Caller sets up the depth state.
func drawVertexHandleBoxes(mx, my int32, showPx float32) {
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
	for _, h := range collectVertexHandles(mx, my, showPx) {
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
