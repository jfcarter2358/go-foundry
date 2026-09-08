package editor

// editor_3d_pick.go — 3D-mode click picking.
//
// One ray cast resolves every clickable thing in the world — the spawn point,
// the entities, the volumes and every brush face — into a single closest hit,
// so a click always selects whatever is actually nearest the camera along the
// ray. Brush faces are tested against their real triangles rather than their
// bounding boxes so that displaced or thin geometry does not steal clicks from
// the surface behind it.
//
// On top of that, brush vertices get screen-space handle boxes that appear
// when the cursor is near them; clicking inside a box selects that vertex,
// repeated clicks cycle through every vertex whose box covers the cursor, and
// ctrl+click selects all of them at once.

import (
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/platform"
	"foundry/internal/world"
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
	panelFocusNone = ""
	// panelFocusFace is the one geometry section there is now. A sector's six
	// kinds of face each had their own — floor, ceiling, the two slab sides,
	// wall and portal — because each was a different set of fields; a brush
	// face is one kind of thing with one set.
	panelFocusFace    = "face"
	panelFocusBrush   = "brush"
	panelFocusVertex  = "vertex"
	panelFocusSpawn   = "spawn"
	panelFocusEntity  = "entity"
	panelFocusVolume  = "volume"
	panelFocusSprite  = "sprite"
	panelFocusEmitter = "emitter"
	panelFocusSound   = "sound"
	panelFocusChunk   = "chunk"
	panelFocusLight   = "light"
	panelFocusScenery = "scenery"
)

// ---------- pick result ----------

// pickKind identifies what a 3D click landed on.
type pickKind string

const (
	pickNone  pickKind = ""
	pickSpawn pickKind = "spawn"
	// pickBrushFace is any face of any brush — the level's geometry is one
	// kind of thing now, where a sector's was six (wall, portal gap, floor,
	// ceiling, and the two slab faces).
	pickBrushFace pickKind = "brush_face"
	pickEntity    pickKind = "entity"
	pickVolume    pickKind = "volume"
	pickSprite    pickKind = "sprite"
	pickEmitter   pickKind = "emitter"
	pickSound     pickKind = "sound"
	pickLight     pickKind = "light"
	pickScenery   pickKind = "scenery"
)

// pickHit is the closest thing found along a picking ray. Which index fields
// are meaningful depends on kind:
//
//	spawn      → none; the level has at most one spawn point
//	entity     → idx = index into level.Entities
//	volume     → idx = index into level.Volumes
//	light      → idx = index into level.Lights.Points
//	brush_face → chunkIdx + brushIdx + faceIdx
type pickHit struct {
	kind     pickKind
	t        float32
	chunkIdx int
	brushIdx int
	faceIdx  int
	idx      int
}

// consider replaces h with a hit at distance t when that hit is closer.
func (h *pickHit) consider(t float32, kind pickKind, chunkIdx, idx int) {
	if t >= h.t {
		return
	}
	h.kind = kind
	h.t = t
	h.chunkIdx = chunkIdx
	h.brushIdx = -1
	h.faceIdx = -1
	h.idx = idx
}

// considerFace is consider for brush geometry, which is addressed by brush and
// face rather than by a single index.
func (h *pickHit) considerFace(t float32, chunkIdx, brushIdx, faceIdx int) {
	if t >= h.t {
		return
	}
	h.kind = pickBrushFace
	h.t = t
	h.chunkIdx = chunkIdx
	h.brushIdx = brushIdx
	h.faceIdx = faceIdx
	h.idx = -1
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

// pickGeometriesModelT is pickGeometriesT with a model transform, for a slice
// of extrusion geometries belonging to a chunk that may be moved/rotated/scaled.
func pickGeometriesModelT(origin, dir mgl32.Vec3, geoms []gfx.Geometry, model mgl32.Mat4) float32 {
	best := float32(math.MaxFloat32)
	for i := range geoms {
		if t := pickModelT(origin, dir, geoms[i].Vertices, model); t < best {
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
func pickEntityT(origin, dir mgl32.Vec3, e *world.Entity) float32 {
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
	hit := pickHit{kind: pickNone, t: float32(math.MaxFloat32), chunkIdx: -1, brushIdx: -1, faceIdx: -1, idx: -1}
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
				hit.consider(t, pickSpawn, -1, -1)
			}
		} else {
			bmin, bmax := sp.WorldAABB()
			if boxHit, boxT := rayAABBIntersect(origin, dir, bmin, bmax); boxHit {
				hit.consider(boxT, pickSpawn, -1, -1)
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
			hit.consider(t, pickEntity, -1, ei)
		}
	}

	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !ch.IsEnabled() {
			// Not drawn and not loaded, so there is nothing under the cursor
			// to hit — picking one would select an invisible wall out of empty
			// space. See world/chunk_enable.go.
			continue
		}
		// Brush verts are chunk-local, same as the production renderer's own
		// vertex buffers (app.go) — carried into world space by this same
		// matrix there, so picking has to go through it too.
		model := ch.ModelMatrix()

		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			for fi := range b.Faces {
				f := &b.Faces[fi]
				// A NoDraw face has no geometry to click, the same way it has
				// none to draw or to cast a ray against (world/brush.go).
				if f.NoDraw {
					continue
				}
				// The face's own triangles plus any Detail texture-override
				// submeshes, so clicking a detailed patch still selects the
				// face it belongs to. Back faces are biased outward (see
				// effectivePickT) so coincident faces at a brush boundary
				// resolve to the one the user can see.
				t := pickModelT(origin, dir, f.Geometry.Vertices, model)
				if et := pickGeometriesModelT(origin, dir, f.SubmeshGeometries, model); et < t {
					t = et
				}
				hit.considerFace(t, ci, bi, fi)
			}
		}
	}

	// Volumes, tested last so that a tie goes to the world: hit.consider keeps
	// the first hit at a given distance, and a region drawn as a wireframe cage
	// should not win against a surface it happens to sit flush with.
	for vi := range lvl.Volumes {
		if t := pickVolumeRayT(origin, dir, &lvl.Volumes[vi]); t < float32(math.MaxFloat32) {
			hit.consider(t, pickVolume, -1, vi)
		}
	}

	// Sprites, against the quad as it is currently oriented — the only thing
	// the author can see to aim at. Tested here rather than with the world for
	// the reason volumes are: a cutout sprite standing flush against a wall
	// should not steal the click meant for the wall.
	for si := range lvl.Sprites {
		if t := pickSpriteRayT(origin, dir, lvl, &lvl.Sprites[si], gfx.Cam.Position); t < float32(math.MaxFloat32) {
			hit.consider(t, pickSprite, -1, si)
		}
	}

	// Scenery, tested with the world rather than after it: a prop is a solid
	// thing standing in the level, so a rock in front of a wall should win the
	// click on distance exactly as the wall in front of another wall does.
	// Volumes and sprites are tested afterwards precisely because they are not.
	for si := range lvl.Scenery {
		if t := pickSceneryRayT(origin, dir, &lvl.Scenery[si]); t < float32(math.MaxFloat32) {
			hit.consider(t, pickScenery, -1, si)
		}
	}

	// Emitters, against the marker disc drawn for them. An emitter has no
	// geometry of its own — what it produces is the whole of what you see — so
	// the marker is the only thing there is to click.
	for ei := range lvl.Emitters {
		if t := pickEmitterRayT(origin, dir, lvl, &lvl.Emitters[ei], gfx.Cam.Position); t < float32(math.MaxFloat32) {
			hit.consider(t, pickEmitter, -1, ei)
		}
	}

	// Sounds, against their marker disc, for the same reason: a sound has
	// nothing to see except the marker drawn for it.
	for si := range lvl.Sounds {
		if t := pickSoundRayT(origin, dir, lvl, &lvl.Sounds[si], gfx.Cam.Position); t < float32(math.MaxFloat32) {
			hit.consider(t, pickSound, -1, si)
		}
	}

	// Point lights, against the marker sphere Editor3DRenderGizmos draws for
	// them.
	//
	// A marker under the cursor wins outright rather than competing on distance
	// with the world, because that is how it is drawn: the gizmo buffer is
	// flushed with the depth test off, so a light is visible through whatever
	// it sits inside. Lights are hung under ceiling slabs and inside lamp
	// fittings as a matter of course, and one you can see but cannot click is
	// the whole complaint. The marker is small — eight pixels at its smallest
	// (pointLightMarkerRadius) — so a click that lands on one is not ambiguous
	// about what it meant.
	bestLight, bestLightT := -1, float32(math.MaxFloat32)
	for li := range lvl.Lights.Points {
		if t := pickPointLightT(origin, dir, &lvl.Lights.Points[li]); t < bestLightT {
			bestLight, bestLightT = li, t
		}
	}
	if bestLight >= 0 {
		return pickHit{kind: pickLight, t: bestLightT, chunkIdx: -1, brushIdx: -1, faceIdx: -1, idx: bestLight}
	}

	if hit.kind == pickNone {
		hit.t = 0
	}
	return hit
}

// pointLightMarkerRadius is the world radius of a point light's clickable
// marker: the sphere the 3D view draws for it (Editor3DRenderGizmos), widened
// to a minimum on-screen size so a light with a small radius, or one far across
// the level, is still something a click can land on.
func pointLightMarkerRadius(pl *world.PointLight) float32 {
	r := pl.Radius * 0.1
	if min := worldSizeForPixels(pl.Position, pointLightPickPx); r < min {
		r = min
	}
	return r
}

// pointLightPickPx is that minimum, in screen pixels — the same order as the
// gizmo's own grab boxes, so aiming at a light feels like aiming at a handle.
const pointLightPickPx float32 = 8

// pickPointLightT returns the distance at which the ray meets a point light's
// marker sphere, or +Inf when it misses.
func pickPointLightT(origin, dir mgl32.Vec3, pl *world.PointLight) float32 {
	oc := origin.Sub(pl.Position)
	r := pointLightMarkerRadius(pl)
	// dir is unit length, so the quadratic's leading coefficient is 1.
	b := oc.Dot(dir)
	c := oc.Dot(oc) - r*r
	disc := b*b - c
	if disc < 0 {
		return float32(math.MaxFloat32)
	}
	root := float32(math.Sqrt(float64(disc)))
	// The near intersection, unless the eye is inside the sphere, where the far
	// one is the only hit in front of it.
	if t := -b - root; t > 0 {
		return t
	}
	if t := -b + root; t > 0 {
		return t
	}
	return float32(math.MaxFloat32)
}

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
	// vertex boxes" toggle is on. It is a sentinel rather than a radius —
	// handleShowsAll turns the cursor-proximity filter off entirely when it is
	// passed, so every in-range vertex draws its box.
	vertexHandleShowAllPx float32 = 1e6
)

// handleShowsAll reports whether showPx is the show-all sentinel and so asks
// for every in-range handle rather than the ones near the cursor.
//
// This is a check rather than a comparison because the off-world draw path has
// no cursor to be near: it passes one far outside the window so nothing reads
// as hovered, and any *finite* radius — 1e6 included — then rejects every
// handle for being billions of pixels away. That is what used to empty the
// viewport the moment a camera drag carried the pointer over the side panel.
func handleShowsAll(showPx float32) bool { return showPx >= vertexHandleShowAllPx }

// vertexSel identifies one selected brush vertex. Selections are stored as
// indices rather than pointers so they survive geometry rebuilds.
type vertexSel struct {
	chunkIdx int
	brushIdx int
	vertIdx  int // index into Brush.Verts
}

// vertexHandle is one clickable brush vertex. A brush owns its verts outright,
// so unlike a sector's shared point pool there is exactly one handle per
// vertex.
type vertexHandle struct {
	vertexSel
	pos     mgl32.Vec3
	screen  mgl32.Vec2
	screenD float32 // pixel distance from the cursor
	depth   float32 // distance from the camera
}

// collectVertexHandles returns every brush vertex handle within
// vertexHandleMaxDist of the camera whose on-screen position is within maxPx of
// (mx, my), nearest first. maxPx of vertexHandleShowAllPx drops that second
// test altogether — see handleShowsAll.
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
		if !ch.IsEnabled() {
			continue // not drawn, so no handles; see world/chunk_enable.go
		}
		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			for vi, v := range b.Verts {
				// Brush verts are chunk-local; every consumer below (screen
				// projection, camera distance, the eventual selection) wants
				// world space.
				pos := ch.LocalToWorld(v)
				depth := pos.Sub(gfx.Cam.Position).Len()
				if depth > vertexHandleMaxDist {
					continue
				}
				screen, ok := projectToScreen(projView, pos)
				if !ok {
					continue
				}
				d := screen.Sub(cursor).Len()
				if d > maxPx && !handleShowsAll(maxPx) {
					continue
				}
				sel := vertexSel{chunkIdx: ci, brushIdx: bi, vertIdx: vi}
				// Solid shading hides the far side of the world, so a handle
				// behind a wall is neither drawn nor clickable; wireframe
				// answers true for everything.
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
		return a.vertIdx < b.vertIdx
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
// it names the chunk and brush the whole selection is read against.
func setVertexSelection(sels []vertexSel) {
	// Cleared first, then assigned: editorClearObjectSelections empties this
	// very list on its way past. sels is always a fresh slice by here — the
	// ctrl+click path copies before calling — so nothing is lost to it.
	editorClearObjectSelections()
	Editor.SelectedVertices = sels

	if len(sels) == 0 {
		Editor.GizmoKind = gizmoKindNone
		return
	}
	primary := sels[0]
	Editor.ActiveChunkIdx = primary.chunkIdx
	Editor.ActiveBrushIdx = primary.brushIdx
	Editor.GizmoKind = gizmoKindVertices
	Editor.PanelFocus = panelFocusVertex
}

// clearVertexSelection drops every selected vertex, and with it the vertex
// translate gizmo.
func clearVertexSelection() {
	Editor.SelectedVertices = nil
	if Editor.GizmoKind == gizmoKindVertices {
		Editor.GizmoKind = gizmoKindNone
	}
}

// vertexSelPos returns the world position of a selected brush vertex.
//
// This is the one place that reads a brush vertex as a position everything else
// (the gizmos, the selection centre) treats as world — see
// editor_sel_transform.go's own doc comment. ch.LocalToWorld is the whole of
// what makes a chunk's own Origin/Rotation/Scale transparent to all of that
// code: fix the read (and writeSelectedVertexWorld's matching write) here, and
// nothing downstream needs to know a chunk can move at all.
func vertexSelPos(sel vertexSel) (mgl32.Vec3, bool) {
	ch, b := vertexSelBrush(sel)
	if b == nil || sel.vertIdx < 0 || sel.vertIdx >= len(b.Verts) {
		return mgl32.Vec3{}, false
	}
	return ch.LocalToWorld(b.Verts[sel.vertIdx]), true
}

// vertexSelBrush resolves the chunk and brush a vertex selection names, or nil
// when the indices no longer point at live geometry.
func vertexSelBrush(sel vertexSel) (*world.Chunk, *world.Brush) {
	lvl := Editor.level
	if lvl == nil || sel.chunkIdx < 0 || sel.chunkIdx >= len(lvl.Chunks) {
		return nil, nil
	}
	ch := &lvl.Chunks[sel.chunkIdx]
	if sel.brushIdx < 0 || sel.brushIdx >= len(ch.Brushes) {
		return ch, nil
	}
	return ch, &ch.Brushes[sel.brushIdx]
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
	gizmo.GizmoAABB(p.Sub(hv), p.Add(hv), col)
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

	gizmo.GizmoFlush(projView)
	prevDepth := gl.IsEnabled(gl.DEPTH_TEST)
	gl.Disable(gl.DEPTH_TEST)
	defer func() {
		gizmo.GizmoFlush(projView)
		if prevDepth {
			gl.Enable(gl.DEPTH_TEST)
		}
	}()

	// Drawn whatever the cursor is doing: the selection does not stop existing
	// because the pointer moved onto the panel.
	drawSelectedFaceOutline()
	drawSelectedVolumeOutline()
	drawSelectedSpriteOutline(gfx.Cam.Position)
	drawSelectedSceneryOutline()
	// The brush's footprint ring, on the same terms: it says where the next
	// stamp lands, which is exactly the thing the panel cannot show.
	DrawBrushGhostOutline()
	RenderEmitterMarkers(Editor.level, gfx.Cam.Position)
	RenderSoundMarkers(Editor.level, gfx.Cam.Position)

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

	// A brush corner is one kind of thing, so there is one resting colour for
	// it — the floor/ceiling blue-and-red split belonged to the sector model,
	// where a handle was one of a stacked pair. Selected is yellow, and the
	// handle under the cursor is white.
	for _, h := range collectVertexHandles(mx, my, showPx) {
		col := mgl32.Vec3{0.35, 0.55, 1}
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
