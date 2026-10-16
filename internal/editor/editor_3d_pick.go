package editor

// editor_3d_pick.go — 3D-mode click picking.
//
// One ray cast resolves every clickable thing in the world — the spawn point,
// the entities, the volumes and every brush face — into a list ordered from the
// camera outward, so a click selects whatever is actually nearest along the ray
// and a second click on the same pixel can carry on to what is behind it (that
// half is editor_3d_pick_cycle.go). Brush faces are tested against their real
// triangles rather than their bounding boxes so that displaced or thin geometry
// does not steal clicks from the surface behind it.
//
// On top of that, brush vertices get screen-space handle boxes that appear
// when the cursor is near them; clicking inside a box selects that vertex,
// repeated clicks cycle through every vertex whose box covers the cursor, and
// ctrl+click selects all of them at once.

import (
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/world"
	"math"
	"sort"

	"github.com/go-gl/gl/v4.3-core/gl"
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
	panelFocusFace     = "face"
	panelFocusBrush    = "brush"
	panelFocusVertex   = "vertex"
	panelFocusSpawn    = "spawn"
	panelFocusSeal     = "seal"
	panelFocusEntity   = "entity"
	panelFocusVolume   = "volume"
	panelFocusSprite   = "sprite"
	panelFocusDecal    = "decal"
	panelFocusEmitter  = "emitter"
	panelFocusSound    = "sound"
	panelFocusEffect   = "effect"
	panelFocusTree     = "tree"
	panelFocusChunk    = "chunk"
	panelFocusLight    = "light"
	panelFocusScenery  = "scenery"
	panelFocusPrefab   = "prefab"
	panelFocusWaypoint = "waypoint"
)

// ---------- pick result ----------

// pickKind identifies what a 3D click landed on.
type pickKind string

const (
	pickNone  pickKind = ""
	pickSpawn pickKind = "spawn"
	pickSeal  pickKind = "seal"
	// pickBrushFace is any face of any brush — the level's geometry is one
	// kind of thing now, where a sector's was six (wall, portal gap, floor,
	// ceiling, and the two slab faces).
	pickBrushFace pickKind = "brush_face"
	pickEntity    pickKind = "entity"
	pickVolume    pickKind = "volume"
	pickSprite    pickKind = "sprite"
	pickDecal     pickKind = "decal"
	pickEmitter   pickKind = "emitter"
	pickSound     pickKind = "sound"
	pickEffect    pickKind = "effect"
	pickTree      pickKind = "tree"
	pickLight     pickKind = "light"
	pickScenery   pickKind = "scenery"
	pickWaypoint  pickKind = "waypoint"
	// pickTerrain is a chunk's block terrain: chunkIdx names the chunk.
	pickTerrain pickKind = "terrain"
)

// pickHit is one thing found along a picking ray. Which index fields are
// meaningful depends on kind:
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
	// sub is the point inside the animation idx names, for a waypoint hit.
	sub int
	// detail says which of the brush's two meshes faceIdx indexes. A click in
	// detail mode lands on the detail mesh, one in edit mode on the hull, and
	// the two are different vertex pools — see vertexSel.detail.
	detail bool
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

// pickModelGeometryT is pickModelT for a whole Geometry, which reads either
// shape it may hold: a de-indexed soup goes straight to pickModelT, an indexed
// model (gfx.IndexedModels) is walked triangle by triangle through its index
// list rather than copied out into a soup on every pick.
func pickModelGeometryT(origin, dir mgl32.Vec3, g *gfx.Geometry, model mgl32.Mat4) float32 {
	if !g.IsIndexed() {
		return pickModelT(origin, dir, g.Vertices, model)
	}
	inv := model.Inv()
	if inv == (mgl32.Mat4{}) {
		return float32(math.MaxFloat32)
	}
	o4 := inv.Mul4x1(mgl32.Vec4{origin.X(), origin.Y(), origin.Z(), 1})
	d4 := inv.Mul4x1(mgl32.Vec4{dir.X(), dir.Y(), dir.Z(), 0})
	o, d := mgl32.Vec3{o4.X(), o4.Y(), o4.Z()}, mgl32.Vec3{d4.X(), d4.Y(), d4.Z()}
	best := float32(math.MaxFloat32)
	g.ForEachTriangle(func(v0, v1, v2 mgl32.Vec3) bool {
		if h, t, front := rayTriangleIntersect(o, d, v0, v1, v2); h {
			if eff := effectivePickT(t, front); eff < best {
				best = eff
			}
		}
		return true
	})
	return best
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
		model := world.Mul4(e.AttachmentMatrix(m))
		for si := range m.Submeshes {
			if t := pickModelGeometryT(origin, dir, &m.Submeshes[si], model); t < best {
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

// pickList collects the hits along a picking ray — every one of them, not just
// the closest, because a click cycles: the second click on the same pixel takes
// the thing behind the one the first click took (editor_3d_pick_cycle.go).
//
// Order is load-bearing. Hits are added in the order the picker tests them and
// sorted with a STABLE sort, so two things at the same distance keep the
// precedence the test order gave them — which is how a volume's wireframe cage
// still loses to the surface it sits flush with.
type pickList struct{ hits []pickHit }

// add records a hit at distance t, ignoring the miss value.
func (l *pickList) add(t float32, kind pickKind, chunkIdx, idx int) {
	if t >= float32(math.MaxFloat32) {
		return
	}
	l.hits = append(l.hits, pickHit{kind: kind, t: t, chunkIdx: chunkIdx, brushIdx: -1, faceIdx: -1, idx: idx})
}

// addFace is add for brush geometry, which is addressed by brush and face
// rather than by a single index.
func (l *pickList) addFace(t float32, chunkIdx, brushIdx, faceIdx int, detail bool) {
	if t >= float32(math.MaxFloat32) {
		return
	}
	l.hits = append(l.hits, pickHit{kind: pickBrushFace, t: t, chunkIdx: chunkIdx,
		brushIdx: brushIdx, faceIdx: faceIdx, detail: detail, idx: -1})
}

// sortByDistance orders the hits near to far, keeping the insertion order of
// anything tied.
func (l *pickList) sortByDistance() {
	sort.SliceStable(l.hits, func(i, j int) bool { return l.hits[i].t < l.hits[j].t })
}

// pickNoHit is what a ray that reached nothing resolves to.
func pickNoHit() pickHit {
	return pickHit{kind: pickNone, t: 0, chunkIdx: -1, brushIdx: -1, faceIdx: -1, idx: -1}
}

// pick3D casts a ray through screen pixel (sx, sy) and returns the closest
// clickable thing in the level.
func pick3D(sx, sy int32) pickHit {
	cands := pick3DCandidates(sx, sy)
	if len(cands) == 0 {
		return pickNoHit()
	}
	return cands[0]
}

// pick3DCandidates casts a ray through screen pixel (sx, sy) and returns
// EVERY clickable thing along it, nearest first — pick3D's answer is its first
// entry, and the rest are what repeated clicks cycle into.
//
// Brush faces are tested against their real triangles rather than their
// bounding boxes so that displaced or thin geometry does not steal clicks from
// the surface behind it.
func pick3DCandidates(sx, sy int32) []pickHit {
	lvl := ws().level
	if lvl == nil {
		return nil
	}
	origin, dir := ws().ray(sx, sy)

	// The markers drawn WITHOUT the depth test — point lights and waypoint
	// arrows — come first, ahead of the world and regardless of distance,
	// because that is how they are drawn: the gizmo buffer is flushed with the
	// depth test off, so a light is visible through whatever it sits inside and
	// an arrow through the very brush its path moves. Lights are hung under
	// ceiling slabs as a matter of course and the first point of a door's
	// animation is usually the door's own centre; a marker you can see but
	// cannot click is the whole complaint. They are small — eight pixels at
	// their smallest (pointLightMarkerRadius) — so a click that lands on one is
	// not ambiguous about what it meant, and a second click now carries on into
	// the world behind it.
	var markers pickList
	for li := range lvl.Lights.Points {
		markers.add(pickPointLightT(origin, dir, lvl, &lvl.Lights.Points[li]), pickLight, -1, li)
	}
	for _, w := range pickWaypointMarkers(origin, dir, ws().camPos(), lvl) {
		markers.hits = append(markers.hits, pickHit{kind: pickWaypoint, t: w.t,
			chunkIdx: -1, brushIdx: -1, faceIdx: -1, idx: w.anim, sub: w.point})
	}
	// Lights added before waypoints, so a tie between the two kinds goes to the
	// light — the order this has always resolved in.
	markers.sortByDistance()

	var scene pickList

	// The spawn marker — exact mesh when it has been uploaded, its AABB
	// otherwise. It is the only non-geometry thing in the level a click can
	// land on.
	if sp := lvl.Spawn; sp != nil {
		if len(sp.Geometry.Vertices) > 0 {
			scene.add(pickModelT(origin, dir, sp.Geometry.Vertices, sp.ModelMatrix()), pickSpawn, -1, -1)
		} else {
			bmin, bmax := sp.WorldAABB()
			if boxHit, boxT := rayAABBIntersect(origin, dir, bmin, bmax); boxHit {
				scene.add(boxT, pickSpawn, -1, -1)
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
		scene.add(pickEntityT(origin, dir, e), pickEntity, -1, ei)
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

		// The chunk's ground, through the same walk collision's rays take. Added as a plain hit, not a face: nothing that acts on a
		// brush face has a face to act on here.
		if ch.Terrain != nil {
			inv := model.Inv()
			lo := inv.Mul4x1(origin.Vec4(1)).Vec3()
			ld := inv.Mul4x1(dir.Vec4(0)).Vec3()
			if h, hit := ch.Terrain.Raycast(lo, ld, math.MaxFloat32); hit {
				scene.add(h.T, pickTerrain, ci, -1)
			}
		}

		for bi := range ch.Brushes {
			m, detail, ok := pickMeshFor(ci, bi, &ch.Brushes[bi])
			if !ok {
				continue
			}
			for fi := range m.Faces {
				f := &m.Faces[fi]
				// A NoDraw face has no geometry to click, the same way it has
				// none to draw or to cast a ray against (world/brush.go).
				if f.NoDraw {
					continue
				}
				// Back faces are biased outward (see effectivePickT) so
				// coincident faces at a brush boundary resolve to the one the
				// user can see — and so the far side of a solid sorts behind
				// its near side rather than tying with it.
				scene.addFace(pickBrushFaceT(origin, dir, m, f, model), ci, bi, fi, detail)
			}
		}
	}

	// Volumes, tested after the world so that a tie goes to the world: the
	// stable sort keeps the earlier hit at a given distance, and a region drawn
	// as a wireframe cage should not win against a surface it happens to sit
	// flush with.
	for vi := range lvl.Volumes {
		scene.add(pickVolumeRayT(lvl, origin, dir, &lvl.Volumes[vi]), pickVolume, -1, vi)
	}

	// Sprites, against the quad as it is currently oriented — the only thing
	// the author can see to aim at. Tested here rather than with the world for
	// the reason volumes are: a cutout sprite standing flush against a wall
	// should not steal the click meant for the wall.
	for si := range lvl.Sprites {
		scene.add(pickSpriteRayT(origin, dir, lvl, &lvl.Sprites[si], ws().camPos()), pickSprite, -1, si)
	}

	// Decals, after the world for the volumes' and sprites' reason, and with one
	// more of its own: a decal lies FLUSH with a surface by definition, so a tie
	// at that distance must go to the surface — clicking a wall with a poster on
	// it a metre away should select the wall.
	for di := range lvl.Decals {
		scene.add(pickDecalRayT(origin, dir, &lvl.Decals[di]), pickDecal, -1, di)
	}

	// Scenery, tested with the world rather than after it: a prop is a solid
	// thing standing in the level, so a rock in front of a wall should win the
	// click on distance exactly as the wall in front of another wall does.
	// Volumes and sprites are tested afterwards precisely because they are not.
	for si := range lvl.Scenery {
		if !lvl.Scenery[si].InWorld() {
			continue // its chunk is off, so it is not drawn: the chunks' reason above
		}
		scene.add(pickSceneryRayT(origin, dir, &lvl.Scenery[si]), pickScenery, -1, si)
	}
	// Trees on the same terms: solid things standing in the level, tested
	// against their crown's footprint (editor_tree.go).
	for ti := range lvl.Trees {
		if !lvl.Trees[ti].InWorld() {
			continue // its chunk is off, so it is not drawn
		}
		scene.add(pickTreeRayT(origin, dir, &lvl.Trees[ti]), pickTree, -1, ti)
	}

	// Emitters, against the marker disc drawn for them. An emitter has no
	// geometry of its own — what it produces is the whole of what you see — so
	// the marker is the only thing there is to click.
	for ei := range lvl.Emitters {
		scene.add(pickEmitterRayT(origin, dir, lvl, &lvl.Emitters[ei], ws().camPos()), pickEmitter, -1, ei)
	}

	// Sounds, against their marker disc, for the same reason: a sound has
	// nothing to see except the marker drawn for it.
	for si := range lvl.Sounds {
		scene.add(pickSoundRayT(origin, dir, lvl, &lvl.Sounds[si], ws().camPos()), pickSound, -1, si)
	}

	// Effects, against their marker disc: the fireball is there for a second
	// at a time, and is not what an author means to grab anyway.
	for fi := range lvl.Effects {
		scene.add(pickEffectRayT(origin, dir, lvl, &lvl.Effects[fi], ws().camPos()), pickEffect, -1, fi)
	}

	scene.sortByDistance()
	out := append(markers.hits, scene.hits...)

	// The seal box, which only gets a click that has hit nothing else at all:
	// from outside, the box is in front of the whole level it holds, so it is
	// not a thing to cycle past on the way in. See pickSealRayT.
	if len(out) == 0 {
		var seal pickList
		seal.add(pickSealRayT(origin, dir, lvl.Seal), pickSeal, -1, -1)
		return seal.hits
	}
	return out
}

// pointLightMarkerRadius is the world radius of a point light's clickable
// marker: the sphere the 3D view draws for it (Editor3DRenderGizmos), widened
// to a minimum on-screen size so a light with a small radius, or one far across
// the level, is still something a click can land on.
func pointLightMarkerRadius(pl *world.PointLight, at mgl32.Vec3) float32 {
	r := pl.Radius * 0.1
	if min := worldSizeForPixels(at, pointLightPickPx); r < min {
		r = min
	}
	return r
}

// pointLightPickPx is that minimum, in screen pixels — the same order as the
// gizmo's own grab boxes, so aiming at a light feels like aiming at a handle.
const pointLightPickPx float32 = 8

// pickPointLightT returns the distance at which the ray meets a point light's
// marker sphere, or +Inf when it misses.
func pickPointLightT(origin, dir mgl32.Vec3, lvl *world.Level, pl *world.PointLight) float32 {
	// Where the light is drawn, which for an attached one is where its carrier
	// has taken it.
	at := pl.WorldPosition(lvl)
	oc := origin.Sub(at)
	r := pointLightMarkerRadius(pl, at)
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
	w, h := ws().viewSize()
	return projectToScreenIn(projView, p, w, h)
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
	_, h := ws().viewSize()
	return worldSizeForPixelsIn(p, ws().camPos(), halfPx, ws().projYY(), h)
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
	vertIdx  int // index into the mesh selMesh resolves
	// detail says WHICH of the brush's two meshes vertIdx indexes: its hull,
	// or the detail mesh drawn over it (world/brush_detail.go). The two are
	// separate vertex pools, so an index means nothing without it.
	//
	// It is a property of the selection rather than of the editor's mode
	// because object mode drags BOTH at once: moving a brush has to carry the
	// detail carved into it, and that is one gizmo over one list of handles
	// naming two meshes.
	detail bool
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
	lvl := ws().level
	if lvl == nil {
		return nil
	}
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	projView := ws().projView()

	var out []vertexHandle
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
			for vi, v := range m.Verts {
				// Brush verts are chunk-local; every consumer below (screen
				// projection, camera distance, the eventual selection) wants
				// world space.
				pos := ch.LocalToWorld(v)
				depth := pos.Sub(ws().camPos()).Len()
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
				sel := vertexSel{chunkIdx: ci, brushIdx: bi, vertIdx: vi, detail: detail}
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
	for _, s := range ws().SelectedVertices {
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
	merged := append([]vertexSel{}, ws().SelectedVertices...)
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
	ws().SelectedVertices = sels

	if len(sels) == 0 {
		ws().GizmoKind = gizmoKindNone
		return
	}
	primary := sels[0]
	ws().ActiveChunkIdx = primary.chunkIdx
	ws().ActiveBrushIdx = primary.brushIdx
	ws().GizmoKind = gizmoKindVertices
	ws().PanelFocus = panelFocusVertex
}

// clearVertexSelection drops every selected vertex, and with it the vertex
// translate gizmo.
func clearVertexSelection() {
	ws().SelectedVertices = nil
	if ws().GizmoKind == gizmoKindVertices {
		ws().GizmoKind = gizmoKindNone
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
	m := selMesh(b, sel.detail)
	if m == nil || sel.vertIdx < 0 || sel.vertIdx >= len(m.Verts) {
		return mgl32.Vec3{}, false
	}
	return ch.LocalToWorld(m.Verts[sel.vertIdx]), true
}

// vertexSelBrush resolves the chunk and brush a vertex selection names, or nil
// when the indices no longer point at live geometry.
func vertexSelBrush(sel vertexSel) (*world.Chunk, *world.Brush) {
	lvl := ws().level
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
	for _, sel := range ws().SelectedVertices {
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

	// The detail mesh's own edges, and the one overlay here drawn WITH depth
	// testing: they are seams on a surface, so the far side of the mesh
	// showing through the near side would turn it into a hairball. Enabled
	// explicitly rather than relying on the state this was called in, since the
	// two windows that call it arrive with different ones.
	gl.Enable(gl.DEPTH_TEST)
	drawDetailMeshWires()
	// The navigation grid lies on the floor it describes, so it is
	// depth-tested for the detail wires' reason: through a wall, the grid of
	// the room behind would read as belonging to this one.
	drawNavigationOverlay()
	gizmo.GizmoFlush(projView)
	// The generator paint layer being painted, on the terrain
	// (editor_generator_paint3d.go): its own pass, depth-tested.
	drawGenPaintOverlay(projView)

	gl.Disable(gl.DEPTH_TEST)
	defer func() {
		gizmo.GizmoFlush(projView)
		if prevDepth {
			gl.Enable(gl.DEPTH_TEST)
		}
	}()

	// Drawn whatever the cursor is doing: the selection does not stop existing
	// because the pointer moved onto the panel. Only one of the two can be
	// non-empty — they are the two contexts' selections — so this is not two
	// cages fighting over the same brush.
	drawSelectedFaceOutline()
	drawSelectedBrushOutline()
	// The hull of the brush being detailed, which is otherwise not on screen
	// at all: its buffers are freed while its detail mesh is doing the drawing,
	// and where the player will actually stop is the one thing an author
	// carving a surface has to be able to see.
	drawDetailHullCage()
	// The prefab boxes are drawn whatever is selected: an instance is a unit
	// that moves and deletes together, and without a box round it a lamp made
	// of five brushes is indistinguishable from five loose brushes.
	DrawPrefabOutlines()
	drawSelectedVolumeOutline()
	// Every environment region's footprint in its environment's colour, and
	// the selected one's point handles (editor_volume_region.go).
	drawRegionVolumeOutlines()
	drawSelectedSpriteOutline(ws().camPos())
	drawSelectedDecalOutline()
	drawSelectedSceneryOutline()
	drawSelectedTreeOutline()
	drawObjectSetOutlines()
	// What the selected prop collides as, when Show collision is on.
	drawSelectedSceneryCollision()
	// The brush's footprint ring, on the same terms: it says where the next
	// stamp lands, which is exactly the thing the panel cannot show. The brush
	// stamp's box says the same thing about the next solid.
	DrawBrushGhostOutline()
	DrawBrushPlaceGhost()
	// The terrain brush's ring, on the ground it is about to reshape.
	drawTerrainBrushRing()
	// The generator paint brush's ring, on the ground it paints.
	drawGenPaintRing()
	RenderEmitterMarkers(ws().level, ws().camPos())
	RenderSoundMarkers(ws().level, ws().camPos())
	RenderEffectMarkers(ws().level, ws().camPos())
	// The level's own paths only: the entity editor's scratch level has none,
	// and the waypoint selection indexes the main window's level.
	if ws().level == Editor.level {
		RenderWaypointMarkers(Editor.level)
	}

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
	if !editorInMeshContext() {
		// Object mode has no sub-brush unit, so there is nothing here a click
		// could reach — and a viewport full of corner boxes that do not respond
		// is exactly the confusion the modes exist to remove. Detail mode does
		// have one — the detail mesh's own corners and edges — so it draws them.
		return
	}
	switch ws().SelMode {
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
	for _, sel := range ws().SelectedVertices {
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
