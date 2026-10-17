package render

// scene_target.go — the frame's own framebuffer. Phase 4 of the Nanite plan
// (docs/nanite/PHASE_4.md).
//
// Until Phase 4 the world drew straight into whatever the caller handed
// DrawWorld — the window's multisampled backbuffer, or a headless target —
// and anti-aliasing was the window's MSAA (antialias.go). Two things need a
// framebuffer the renderer owns instead:
//
//   - TAA (taa.go) resolves each frame into a history of its own, so the
//     frame it resolves has to be somewhere it can sample: a texture, one
//     sample a pixel, with the depth and the motion beside it.
//   - The visibility buffer (nanite_vis.go) draws cooked meshes and compiled
//     surfaces as a depth and a 32-bit triangle ID per pixel and shades them
//     afterwards, once a pixel, so it needs an integer target beside the
//     colour and a stencil saying which pixels are still its own.
//
// So in a TAA frame (gfx.AntiAliasMode) the scene target stands in for the
// caller's: DrawWorld declares it as the frame target (frame_target.go), so
// every pass that draws into a shadow map or a copy of the depth and then
// puts "the frame's framebuffer" back puts this one back, and none of them
// needs to know. At the end the TAA resolve writes the finished picture into
// the caller's target.
//
// Its attachments:
//
//	COLOR0  RGBA16F  the colour every pass draws (drawBuffersColour: the
//	                 only buffer drawn to, outside the passes below)
//	COLOR1  R32UI    the visibility buffer's triangle IDs (nanite_vis.go)
//	COLOR2  RG16F    moving objects' motion vectors (taa.go)
//	COLOR3  R32UI    the material pass's bin per pixel (nanite_vis.go)
//	COLOR4  RGBA32UI a painted level's normal buffer (stylize.go), made
//	                 the first frame one is drawn and kept from then on
//	DEPTH_STENCIL    DEPTH24_STENCIL8, the window's own format, so a decal's
//	                 polygon offset and the volumetrics' depth copy behave as
//	                 they always did; the stencil says which pixels the
//	                 visibility buffer still owns
//
// It is sized to the frame target's viewport and remade when that changes.

import (
	"log"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// The scene target's colour attachments.
const (
	sceneAttachColour   = gl.COLOR_ATTACHMENT0
	sceneAttachVisID    = gl.COLOR_ATTACHMENT1
	sceneAttachVelocity = gl.COLOR_ATTACHMENT2
	sceneAttachBin      = gl.COLOR_ATTACHMENT3
	sceneAttachStylize  = gl.COLOR_ATTACHMENT4
)

// sceneTarget is the frame's own framebuffer; see the file comment.
type sceneTarget struct {
	w, h     int32
	fbo      uint32
	colour   uint32
	depth    uint32
	visID    uint32
	velocity uint32
	bin      uint32
	// stylize is the painted look's normal buffer (stylize.go), 0 until a
	// painted level is drawn.
	stylize uint32
	// failed: the driver would not make it, once; the frame stays on the
	// forward path for the rest of the run.
	failed bool
	// gen counts the times it was made, for whoever keeps framebuffers of
	// its attachments: a remade texture may come back under the same name,
	// and a framebuffer holding the old one would draw into a texture
	// nothing reads.
	gen uint64
}

var scene sceneTarget

// ensure makes the target w by h, remaking it if its size changed. False
// when it cannot be made.
func (s *sceneTarget) ensure(w, h int32) bool {
	if s.failed || w < 1 || h < 1 {
		return false
	}
	if s.fbo != 0 && s.w == w && s.h == h {
		return true
	}
	s.release()
	s.w, s.h = w, h
	s.gen++
	tex := func(internal int32, format, xtype uint32) uint32 {
		var t uint32
		gl.GenTextures(1, &t)
		gl.BindTexture(gl.TEXTURE_2D, t)
		gl.TexImage2D(gl.TEXTURE_2D, 0, internal, w, h, 0, format, xtype, nil)
		setNearestClamp()
		return t
	}
	s.colour = tex(gl.RGBA16F, gl.RGBA, gl.FLOAT)
	// Linear, for the TAA resolve's reads between texels.
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	s.depth = tex(gl.DEPTH24_STENCIL8, gl.DEPTH_STENCIL, gl.UNSIGNED_INT_24_8)
	s.visID = tex(gl.R32UI, gl.RED_INTEGER, gl.UNSIGNED_INT)
	s.velocity = tex(gl.RG16F, gl.RG, gl.FLOAT)
	s.bin = tex(gl.R32UI, gl.RED_INTEGER, gl.UNSIGNED_INT)
	gl.BindTexture(gl.TEXTURE_2D, 0)

	gl.GenFramebuffers(1, &s.fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, s.fbo)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, sceneAttachColour, gl.TEXTURE_2D, s.colour, 0)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, sceneAttachVisID, gl.TEXTURE_2D, s.visID, 0)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, sceneAttachVelocity, gl.TEXTURE_2D, s.velocity, 0)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, sceneAttachBin, gl.TEXTURE_2D, s.bin, 0)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_STENCIL_ATTACHMENT, gl.TEXTURE_2D, s.depth, 0)
	drawBuffersColour()
	st := gl.CheckFramebufferStatus(gl.FRAMEBUFFER)
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
	if st != gl.FRAMEBUFFER_COMPLETE {
		log.Printf("scene target incomplete (0x%x) — drawing the forward way, without TAA", st)
		s.release()
		s.failed = true
		return false
	}
	return true
}

func (s *sceneTarget) release() {
	if s.fbo != 0 {
		gl.DeleteFramebuffers(1, &s.fbo)
		s.fbo = 0
	}
	for _, t := range []*uint32{&s.colour, &s.depth, &s.visID, &s.velocity, &s.bin, &s.stylize} {
		if *t != 0 {
			gl.DeleteTextures(1, t)
			*t = 0
		}
	}
	s.w, s.h = 0, 0
}

// clear readies the target for a frame, after the colour and depth clear
// the frame does itself: no triangle IDs, no bins, every pixel's motion "the
// camera's" (taaNoObjectMotion), and the stencil zero. Leaves the colour
// attachment the one drawn to.
func (s *sceneTarget) clear() {
	// glClearBuffer names a DRAW BUFFER, not an attachment, so every
	// attachment is made one for the clear.
	all := []uint32{sceneAttachColour, sceneAttachVisID, sceneAttachVelocity, sceneAttachBin}
	gl.DrawBuffers(int32(len(all)), &all[0])
	zero := [4]uint32{}
	gl.ClearBufferuiv(gl.COLOR, 1, &zero[0])
	gl.ClearBufferuiv(gl.COLOR, 3, &zero[0])
	noMotion := [4]float32{taaNoObjectMotion, taaNoObjectMotion, 0, 0}
	gl.ClearBufferfv(gl.COLOR, 2, &noMotion[0])
	gl.StencilMask(0xff)
	gl.ClearBufferiv(gl.STENCIL, 0, &[]int32{0}[0])
	if s.stylize != 0 && stylizer.frame {
		// No surface anywhere yet (stylizePack's bit 8 clear).
		drawBuffersOnly(sceneAttachStylize)
		gl.ClearBufferuiv(gl.COLOR, 0, &zero[0])
	}
	drawBuffersColour()
}

// ensureStylize gives the target its normal buffer, once. The material
// pass's framebuffer holds the attachments by name, so gen moves on and it
// takes the new one (nanite_vis.go's ensureFBOs). False when the driver will
// not have it: the frame is painted with the light ramp alone.
func (s *sceneTarget) ensureStylize() bool {
	if s.stylize != 0 {
		return true
	}
	if s.fbo == 0 {
		return false
	}
	gl.GenTextures(1, &s.stylize)
	gl.BindTexture(gl.TEXTURE_2D, s.stylize)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA32UI, s.w, s.h, 0, gl.RGBA_INTEGER, gl.UNSIGNED_INT, nil)
	setNearestClamp()
	gl.BindTexture(gl.TEXTURE_2D, 0)
	gl.BindFramebuffer(gl.FRAMEBUFFER, s.fbo)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, sceneAttachStylize, gl.TEXTURE_2D, s.stylize, 0)
	if st := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); st != gl.FRAMEBUFFER_COMPLETE {
		log.Printf("stylize: normal buffer incomplete (0x%x) — painting with the light ramp alone", st)
		gl.FramebufferTexture2D(gl.FRAMEBUFFER, sceneAttachStylize, gl.TEXTURE_2D, 0, 0)
		gl.DeleteTextures(1, &s.stylize)
		s.stylize = 0
		return false
	}
	s.gen++
	return true
}

// The draw-buffer sets the frame switches between. Framebuffer state, so
// each pass that changes it puts drawBuffersColour back.
//
// While a painted level's opaque world draws (stylizeBeginWrites) the colour
// set is the colour and the normal buffer, as draw buffers 0 and 1: the
// programs drawing then — the world program, terrain — write their surface's
// record at location 1 (fragment.glsl's outputStylize).
func drawBuffersColour() {
	if stylizer.writing {
		gl.DrawBuffers(2, &[]uint32{sceneAttachColour, sceneAttachStylize}[0])
		return
	}
	gl.DrawBuffers(1, &[]uint32{sceneAttachColour}[0])
}

// drawBuffersOnly makes attach the one buffer drawn to, as draw buffer 0: a
// pass that draws into it writes its fragment output at location 0.
func drawBuffersOnly(attach uint32) {
	gl.DrawBuffers(1, &[]uint32{attach}[0])
}
