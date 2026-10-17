package render

// viewmodel.go — the player's first-person view: hands, and whatever they
// hold, drawn directly in front of the camera.
//
// world.PlayerEntity is not in Level.Entities (see entity.go's header on why:
// "the view is from inside its head"), so RenderEntities' walk never sees it —
// deliberately, since a viewmodel wants none of what that walk gives a normal
// entity: it should not be culled by the world's frustum, occluded by nearby
// geometry, or cast into either shadow pass. This is the one place that draws
// PlayerEntity.Models, and it does so by a completely different route from
// RenderEntities: instead of the entity's own ModelMatrix (which is its
// physical body, standing wherever collision left it), the base matrix here is
// the camera's — so the hands and the gun move exactly as the eye does, and
// never as the feet do.
//
// It is still the same EntityModel/AttachmentMatrix/socket chain a third-
// person weapon mount uses (docs/guide/11-entities.md's Sockets section): a
// first-person "hands" attachment carries hand_socket.R/.L bones, and a
// rifle or pistol mounts on one exactly the way it would on an NPC. Nothing
// about sockets, skinning or animation needed to change for that to work —
// only where the chain starts.

import (
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// EditorPreviewActive is set by the editor's Player panel while its Viewmodel
// section is open, and read by app.go's call to RenderViewmodel — the one
// override to "the editor never draws this" (see the header above): with the
// section closed a floating gun over the free-fly camera would be noise while
// placing brushes, but with it open it is the only way to see what is being
// edited at all, since the editor's own preview cameras belong to other
// windows (the entity editor's own 3D view, say) and RenderViewmodel always
// draws from gfx.Cam, not from one of those.
var EditorPreviewActive = false

// RenderViewmodel draws the player's viewmodel attachments, last of anything
// in the 3D world — after the sky, the transparent pass, volumetrics and
// sprites, and before the 2D UI. Nothing later in the frame touches the depth
// buffer until the next frame's clear, so this is the only pass whose depth
// clear will not erase something drawn after it.
//
// The clear is what makes a viewmodel a viewmodel rather than an oddly-placed
// prop: without it, standing close to a wall would let the wall's own depth,
// already in the buffer from the opaque pass, win the depth test against a
// gun that is genuinely nearer the eye than the wall is genuinely nearer the
// eye — the classic first-person clipping artifact. Clearing first means the
// comparison the depth test makes is only ever against the viewmodel's own
// geometry, so the gun always wins, and its parts still occlude each other
// correctly among themselves.
func RenderViewmodel() {
	e := world.PlayerEntity
	if e == nil || !e.Visible || len(e.Models) == 0 {
		return
	}
	// A third-person camera sees the body (world.PlayerBodyEntity) in the
	// world instead; hands floating in front of a boom camera would be wrong.
	if world.PlayerThirdPerson {
		return
	}

	// The camera's own view matrix, inverted, is the camera's placement IN
	// the world: translated to the eye, oriented so local -Z looks the way
	// the camera looks and local +Y is the way it is held — the same
	// convention Entity.ModelMatrix gives a normal entity's local axes, which
	// is what lets a "hands" attachment's Offset/Rotation be authored exactly
	// the way any other root attachment's would be. Built from gfx.Cam.Matrix
	// rather than re-derived from Yaw/Pitch, so this can never disagree with
	// where the world already believes the eye is looking.
	base := gfx.Cam.Matrix.Inv()

	gl.Disable(gl.BLEND)
	gl.DepthMask(true)
	gl.Enable(gl.DEPTH_TEST)
	gl.Clear(gl.DEPTH_BUFFER_BIT)

	// The viewmodel's own lens, for the pass and no longer: a wider, fixed
	// field of view so that aiming down the sights zooms the world without
	// swelling the arms across the screen, and a near plane ten times closer so
	// that hands held at arm's length are not sliced open by it. See
	// gfx.ViewmodelProjection. The world program is bound explicitly rather
	// than assumed, since the uniform is uploaded to whatever is in use — and
	// gfx.Projection goes back at the end, because every pass after this one
	// reads that uniform expecting the world's lens.
	gl.UseProgram(gfx.Program)
	gfx.UploadProjection(gfx.ViewmodelProjection())
	defer gfx.UploadProjection(gfx.Projection)

	list := viewmodelDraws[:0]
	for mi := range e.Models {
		m := &e.Models[mi]
		if !entityDrawable(e, m) {
			continue
		}
		model := base.Mul4(e.AttachmentMatrix(m))
		for si := range m.Submeshes {
			list = append(list, entityDraw{&m.Submeshes[si], model})
		}
	}
	viewmodelDraws = list

	// Its depth first, through the viewmodel's own lens (entity_prepass.go):
	// the hands, the gun in them and its sights cover each other a great deal
	// at a large share of the screen, and this way each pixel of them is
	// shaded once.
	if pp := &entityPrepassProg; pp.begin() {
		for i := range list {
			pp.draw(list[i].g, list[i].model)
		}
		pp.end()
	}
	for i := range list {
		// Single-sided, on RenderEntities' own reasoning: a viewmodel is a
		// closed solid too, and its back faces are never the ones toward
		// the eye.
		list[i].g.DrawWithTransform(list[i].model)
	}
}

// viewmodelDraws is RenderViewmodel's list, kept between frames for its
// storage.
var viewmodelDraws []entityDraw
