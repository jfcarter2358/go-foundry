package editor

// editor_entity_editor_render.go — drawing the type being edited.
//
// GL context ownership, which follows the model editor's rule rather than the
// 2D pop-out's: everything built here is drawn *only* in the entity editor's
// window, so it is built, drawn and released entirely in that window's context
// and nothing here may be wrapped in withMainGLContext. VAOs are the one class
// of GL object a shared context does not share, and a VAO minted against the
// main context is a name this window cannot draw.
//
// What the preview is, and why it is an Entity rather than a bespoke draw list:
//
// The definition is a template with no GPU state. Stamping it out through
// EntityDef.Instantiate and loading that is the same path the level loader
// takes, which is the whole value — the preview cannot disagree with the game
// about what a definition means, because it is not a second implementation of
// it. An attachment that will not load looks here exactly as wrong as it will
// look in the level.
//
// The type's BRUSHES are not previewed at all in that sense: they are the live,
// editable geometry of the scratch level this window edits
// (editor_entity_editor_brushes.go), uploaded once and reloaded per edit by the
// same tools that reload a level's. Drawing a second copy of them would be
// drawing something an author could not click on. This window *is* the entity's
// local space, so the frame transform a level would apply is the identity here
// and the brushes are drawn where they are authored.
//
// Both halves are drawn through modelProgram, the model editor's headlight
// shader, which carries the shared skinning path so a rigged attachment
// previews in motion rather than in its rest pose.

import (
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/world"
	"math"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// entityPreviewDirty says the document has changed shape since the preview was
// built. Set by every structural edit, acted on once a frame in
// entityPreviewSync — a panel drag runs its widget many times, and re-uploading
// a mesh per frame of one is the difference between a live slider and a stalled
// one.
var entityPreviewDirty = true

// entityPreviewErr is what went wrong building the preview, shown in the
// toolbar. Kept rather than logged and forgotten: a missing chunk file is a
// thing the author has to see, and the viewport's answer to it is an absence.
var entityPreviewErr string

// entityPreviewInvalidate marks the preview for rebuild on the next frame.
func entityPreviewInvalidate() { entityPreviewDirty = true }

// entityPreviewSync rebuilds the preview if the document has changed shape.
// Called once per frame from Draw, with this window's GL context current.
func entityPreviewSync() {
	if !entityPreviewDirty {
		return
	}
	entityPreviewDirty = false
	entityPreviewRelease()
	if entityDef == nil {
		return
	}

	e := entityDef.Instantiate(entityPreviewID)
	entityPreview = &e
	entityPreviewErr = ""
	if err := e.Load(); err != nil {
		entityPreviewErr = err.Error()
	}

	// The brushes are seeded here too, but only when there is no live chunk yet
	// or the document has been replaced under it — an ordinary edit mutates the
	// live brushes in place, and re-seeding would throw away the selection and
	// re-upload every buffer mid-drag. See entityGeomDirty.
	if entityGeomDirty || entityGeomChunk() == nil {
		entitySeedGeometry()
	}
}

// entityPreviewApplyTransforms moves the preview to match the document without
// rebuilding it.
//
// Everything a gizmo drag writes — an attachment's offset, rotation and scale,
// a chunk's placement — is a transform the draw reads per frame, not something
// baked into a buffer. Copying the four fields across is all it takes, and it
// is the difference between a live drag and one that re-reads every chunk file
// off disk on every motion event.
//
// A structural change is still entityPreviewInvalidate's job: this cannot add
// an attachment, swap a mesh or notice that a chunk file has changed.
func entityPreviewApplyTransforms() {
	if entityDef == nil || entityPreviewDirty {
		// Dirty already: the rebuild coming this frame supersedes anything
		// written here, and the lists may not line up until it has run.
		return
	}
	if entityPreview != nil && len(entityPreview.Models) == len(entityDef.Models) {
		// Instantiate copies the attachments in order, so the two lists are
		// index-aligned — the length check is what says the preview has not
		// fallen behind a structural edit.
		for i := range entityDef.Models {
			src, dst := &entityDef.Models[i], &entityPreview.Models[i]
			dst.Offset, dst.Rotation, dst.Scale = src.Offset, src.Rotation, src.Scale
		}
	}
}

// entityPreviewID is what the preview entity calls itself. It is never in a
// level and never addressed by a script; the name only ever appears in a load
// error, where it reads better than an empty string.
const entityPreviewID = "(preview)"

// entityPreviewRelease frees the preview's GPU buffers. Must run with the
// entity editor window's context current — see the file comment.
func entityPreviewRelease() {
	if entityPreview != nil {
		entityPreview.Delete()
		entityPreview = nil
	}
	entityReleaseGeometry()
}

// entityPreviewAnimate advances every attachment's animation clock.
//
// EntityAnim.Update directly rather than EntityModel.Update, which is the
// call a level makes: that one also records `:animation_end:` and
// `:animation_start:` events for the script system, and a preview looping in a
// window nobody is playing has no business filling that queue.
func entityPreviewAnimate(dt float32) {
	if entityPreview == nil || dt <= 0 {
		return
	}
	for i := range entityPreview.Models {
		if a := entityPreview.Models[i].Anim; a != nil {
			a.Update(dt)
		}
	}
}

// entityPreviewBounds is the box everything in the preview occupies, for the
// camera to frame. Reports false when there is nothing with extent — a type
// with no models, no chunks and no bounds is a point, and framing a point at
// some arbitrary distance is worse than leaving the camera alone.
func entityPreviewBounds() (mgl32.Vec3, mgl32.Vec3, bool) {
	min := mgl32.Vec3{math.MaxFloat32, math.MaxFloat32, math.MaxFloat32}
	max := mgl32.Vec3{-math.MaxFloat32, -math.MaxFloat32, -math.MaxFloat32}
	any := false
	grow := func(p mgl32.Vec3) {
		any = true
		for i := 0; i < 3; i++ {
			if p[i] < min[i] {
				min[i] = p[i]
			}
			if p[i] > max[i] {
				max[i] = p[i]
			}
		}
	}

	if entityPreview != nil {
		for i := range entityPreview.Models {
			m := &entityPreview.Models[i]
			mm := m.LocalMatrix()
			for si := range m.Submeshes {
				g := &m.Submeshes[si]
				for v := 0; v+2 < len(g.Vertices); v += 5 {
					p := mm.Mul4x1(mgl32.Vec4{g.Vertices[v], g.Vertices[v+1], g.Vertices[v+2], 1})
					grow(mgl32.Vec3{p.X(), p.Y(), p.Z()})
				}
			}
		}
	}
	if ch := entityGeomChunk(); ch != nil {
		m := ch.ModelMatrix()
		lo, hi, ok := entityChunkBox(ch)
		if !ok {
			goto boundsDone
		}
		// Every corner, not just the two: the chunk's placement may rotate it,
		// and transforming the min and max alone would give a box that is not
		// the one the geometry is in.
		for c := 0; c < 8; c++ {
			p := mgl32.Vec3{
				pick(c&1 != 0, hi.X(), lo.X()),
				pick(c&2 != 0, hi.Y(), lo.Y()),
				pick(c&4 != 0, hi.Z(), lo.Z()),
			}
			w := m.Mul4x1(p.Vec4(1))
			grow(mgl32.Vec3{w.X(), w.Y(), w.Z()})
		}
	}
boundsDone:
	if entityDef != nil && entityDef.BoundHalfX > 0 && entityDef.BoundHalfY > 0 && entityDef.BoundHalfZ > 0 {
		half := mgl32.Vec3{entityDef.BoundHalfX, entityDef.BoundHalfY, entityDef.BoundHalfZ}
		grow(entityDef.BoundOffset.Sub(half))
		grow(entityDef.BoundOffset.Add(half))
	}
	return min, max, any
}

// pick is a ternary for the corner walk above.
func pick(cond bool, a, b float32) float32 {
	if cond {
		return a
	}
	return b
}

// ---------- the mesh pass ----------

// entityDrawPreview draws the attachments and the chunk geometry.
//
// Geometry.Draw is not reusable here for the reason drawModelMesh gives: it
// hardcodes the world program and resolves its uniforms against it. This is the
// same sequence against modelProgram, plus the per-draw skinning upload that
// program now needs.
func entityDrawPreview(view, proj mgl32.Mat4, camPos mgl32.Vec3) {
	if err := modelEditorEnsureProgram(); err != nil {
		entityPreviewErr = err.Error()
		return
	}
	gl.UseProgram(modelProgram)
	gl.UniformMatrix4fv(modelUniforms.projection, 1, false, &proj[0])
	gl.UniformMatrix4fv(modelUniforms.view, 1, false, &view[0])
	gl.Uniform1i(modelUniforms.tex, 0)
	gl.Uniform3f(modelUniforms.camPos, camPos.X(), camPos.Y(), camPos.Z())

	if entityWireOverlay {
		gl.PolygonMode(gl.FRONT_AND_BACK, gl.LINE)
		defer gl.PolygonMode(gl.FRONT_AND_BACK, gl.FILL)
	}

	if entityShowChunks {
		if ch := entityGeomChunk(); ch != nil {
			m := ch.ModelMatrix()
			for _, g := range world.ChunkDrawGeometries(ch) {
				entityDrawGeometry(g, m, entityChunkTint)
			}
		}
	}

	if entityShowModels && entityPreview != nil {
		for i := range entityPreview.Models {
			mdl := &entityPreview.Models[i]
			if !mdl.Visible {
				continue
			}
			tint := entityModelTint
			if entitySel.Kind == entitySelModel && entitySel.Idx == i {
				tint = entitySelectedTint
			}
			m := mdl.LocalMatrix()
			for si := range mdl.Submeshes {
				entityDrawGeometry(&mdl.Submeshes[si], m, tint)
			}
		}
	}
	gl.BindVertexArray(0)
}

// entityDrawGeometry issues one draw under modelProgram.
func entityDrawGeometry(g *gfx.Geometry, model mgl32.Mat4, tint mgl32.Vec3) {
	if g == nil || len(g.Vertices) == 0 {
		return
	}
	gl.UniformMatrix4fv(modelUniforms.model, 1, false, &model[0])
	gl.Uniform3f(modelUniforms.tint, tint.X(), tint.Y(), tint.Z())
	gl.Uniform1i(modelUniforms.smoothShading, g.SmoothFlag())
	// Not optional even for the unskinned chunk geometry: `skinned` is program
	// state, so a wall drawn after an animated attachment would otherwise bend
	// around that attachment's bones. See modelSkinUniforms.
	gfx.ApplySkinUniforms(&modelSkinUniforms, modelProgram, g.BonePalette)
	gl.BindVertexArray(g.Vao)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, gfx.Textures[g.Texture])
	gl.DrawArrays(gl.TRIANGLES, 0, int32(len(g.Vertices)/5))
}

// ---------- overlays ----------

var (
	entityModelTint    = mgl32.Vec3{1, 1, 1}
	entityChunkTint    = mgl32.Vec3{0.92, 0.94, 1}
	entitySelectedTint = mgl32.Vec3{1.25, 1.15, 0.8}

	entityGridColor      = mgl32.Vec3{0.26, 0.26, 0.31}
	entityAxisX          = mgl32.Vec3{0.62, 0.24, 0.24}
	entityAxisZ          = mgl32.Vec3{0.24, 0.34, 0.68}
	entityBoundsColor    = mgl32.Vec3{0.35, 0.85, 0.45}
	entityChunkBoxColor  = mgl32.Vec3{0.35, 0.55, 0.9}
	entityChunkOffColor  = mgl32.Vec3{0.45, 0.45, 0.5}
	entitySeatColor      = mgl32.Vec3{0.95, 0.75, 0.25}
	entitySeatExitColor  = mgl32.Vec3{0.55, 0.75, 0.95}
	entitySelectionColor = mgl32.Vec3{1, 0.85, 0.35}
)

// drawEntityGrid lays a unit grid on the XZ plane with the two ground axes
// picked out, so the type has a sense of scale and of which way it faces.
//
// An entity's forward is +X (the spawn point's yaw convention), so the red axis
// is the one a vehicle drives along — worth knowing before laying out seats.
func drawEntityGrid() {
	const half = 10
	for i := -half; i <= half; i++ {
		f := float32(i)
		xCol, zCol := entityGridColor, entityGridColor
		if i == 0 {
			xCol, zCol = entityAxisZ, entityAxisX
		}
		gizmo.GizmoLine(mgl32.Vec3{f, 0, -half}, mgl32.Vec3{f, 0, half}, xCol)
		gizmo.GizmoLine(mgl32.Vec3{-half, 0, f}, mgl32.Vec3{half, 0, f}, zCol)
	}
}

// drawEntityBoundsCage outlines the collision volume the type carries — the box
// the player will actually bump into, which is routinely nothing like the
// silhouette of the mesh inside it.
func drawEntityBoundsCage() {
	if entityDef == nil {
		return
	}
	half := mgl32.Vec3{entityDef.BoundHalfX, entityDef.BoundHalfY, entityDef.BoundHalfZ}
	if half.X() <= 0 || half.Y() <= 0 || half.Z() <= 0 {
		return // no volume: nothing to outline, and the panel says so
	}
	c := entityDef.BoundOffset
	switch entityDef.BoundingKind {
	case world.BoundingSphere:
		// One radius, so the cage is drawn at the largest half-extent — the
		// same reading the collision code takes.
		r := half.X()
		if half.Y() > r {
			r = half.Y()
		}
		if half.Z() > r {
			r = half.Z()
		}
		gizmo.GizmoSphere(c, r, entityBoundsColor)
	default:
		gizmo.GizmoAABB(c.Sub(half), c.Add(half), entityBoundsColor)
	}
}

// drawEntityGeometryOutline boxes the extent of the type's brushes.
//
// That box is not decoration: it is the region a body walking into the placed
// entity is handed into the entity's frame at, so what it outlines is exactly
// where "inside this thing" begins. See collision/entity_brushes.go.
func drawEntityGeometryOutline() {
	ch := entityGeomChunk()
	if entityDef == nil || ch == nil {
		return
	}
	lo, hi, ok := entityChunkBox(ch)
	if !ok {
		return
	}
	drawTransformedBox(ch.ModelMatrix(), lo, hi, entityChunkBoxColor)
}

// entityChunkBox is a chunk's local bounds, or false when it has none worth
// drawing.
//
// A chunk with no brushes in it never gets any extent, and is left holding the
// sentinels RecomputeBounds starts with — a box from +MaxFloat32 down to
// -MaxFloat32, which as an outline is twelve lines to the horizon. Rejecting an
// inverted range is cheaper than teaching every caller to care about a chunk
// that has nothing in it.
func entityChunkBox(ch *world.Chunk) (mgl32.Vec3, mgl32.Vec3, bool) {
	lo, hi := ch.MinBound, ch.MaxBound
	for i := 0; i < 3; i++ {
		if lo[i] > hi[i] {
			return mgl32.Vec3{}, mgl32.Vec3{}, false
		}
	}
	if lo == hi {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	return lo, hi, true
}

// drawTransformedBox outlines a local-space AABB carried through a matrix, so a
// rotated chunk gets a rotated box rather than an axis-aligned one around it.
func drawTransformedBox(m mgl32.Mat4, lo, hi mgl32.Vec3, col mgl32.Vec3) {
	var c [8]mgl32.Vec3
	for i := 0; i < 8; i++ {
		p := mgl32.Vec3{
			pick(i&1 != 0, hi.X(), lo.X()),
			pick(i&2 != 0, hi.Y(), lo.Y()),
			pick(i&4 != 0, hi.Z(), lo.Z()),
		}
		w := m.Mul4x1(p.Vec4(1))
		c[i] = mgl32.Vec3{w.X(), w.Y(), w.Z()}
	}
	edges := [12][2]int{
		{0, 1}, {1, 3}, {3, 2}, {2, 0},
		{4, 5}, {5, 7}, {7, 6}, {6, 4},
		{0, 4}, {1, 5}, {2, 6}, {3, 7},
	}
	for _, e := range edges {
		gizmo.GizmoLine(c[e[0]], c[e[1]], col)
	}
}

// drawEntitySeatMarkers shows where a vehicle's occupants sit and where they
// step out to.
//
// A seat is one point and one facing, which on its own is a dot in space. What
// is drawn is a stick figure's worth of it — the seat, the eye height above it,
// and a short arrow along the seat's yaw — because "is this seat facing the
// windscreen" is the question an author is actually asking.
func drawEntitySeatMarkers() {
	if entityDef == nil || entityDef.Vehicle == nil {
		return
	}
	for i := range entityDef.Vehicle.Seats {
		s := &entityDef.Vehicle.Seats[i]
		col := entitySeatColor
		if entitySel.Kind == entitySelSeat && entitySel.Idx == i {
			col = entitySelectionColor
		}
		p := s.Position
		eye := p.Add(mgl32.Vec3{0, s.Eye, 0})
		gizmo.GizmoAABB(p.Sub(mgl32.Vec3{0.2, 0, 0.2}), p.Add(mgl32.Vec3{0.2, 0.05, 0.2}), col)
		gizmo.GizmoLine(p, eye, col)
		// The facing, from the eye rather than the floor — the direction the
		// occupant looks, not the direction the chair points.
		// Through SeatMatrix rather than a cosine and a sine written out
		// here: the seat's facing is that matrix carrying local +X, and a
		// hand-rolled pair turns out to spin the opposite way to it.
		dir := world.SeatMatrix(s).Mul4x1(mgl32.Vec4{1, 0, 0, 0}).Vec3()
		gizmo.GizmoLine(eye, eye.Add(dir.Mul(0.6)), col)
		if s.Exit != (mgl32.Vec3{}) {
			gizmo.GizmoLine(p, s.Exit, entitySeatExitColor)
			gizmo.GizmoAABB(s.Exit.Sub(mgl32.Vec3{0.15, 0, 0.15}),
				s.Exit.Add(mgl32.Vec3{0.15, 0.05, 0.15}), entitySeatExitColor)
		}
	}
}
