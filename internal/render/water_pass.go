package render

// water_pass.go — drawing the frame's water (docs/terrain-paint/SPEC.md,
// "Water > Look"; docs/terrain-paint/PHASE_W1.md).
//
// Water used to be an ordinary surface: drawn in the opaque pass (a generated
// lake's tiles by the GPU cull, a brush face in its chunk's batch) with a
// fresnel mix toward one flat colour. To look like water it needs what is
// BEHIND it — how deep the bed is, what the bed looks like, what the scene
// above it would reflect — so it is now drawn in a pass of its own, after the
// sky and before the sorted transparent pass:
//
//  1. Every water surface is routed out of the opaque world:
//     SurfaceState.IsTransparent is true for water, so brushes and compiled
//     surfaces queue it like glass, a cooked placement wearing water stays on
//     the classic path (world.Scenery.wearsWater), and DrawWorld moves the
//     water entries out of the transparent queue into this pass
//     (splitWaterDraws).
//  2. "water: copy" — the scene's depth (and, from medium quality, its
//     colour) is copied out of the frame's framebuffer, and the sky is drawn
//     into the water's octahedral sky map (water_sky.go). Copies, because
//     the water draws into that same framebuffer and sampling an attachment
//     of it would be a feedback loop.
//  3. "water" — the surfaces, back to front, blended and WRITING DEPTH: what
//     the transparent pass, the volumetrics and the sprites draw afterwards
//     sorts against the water rather than against the bed under it, and
//     TAA's motion is the surface's.
//
// The quality tiers (gfx.WaterQuality) decide what step 2 does: low copies
// depth only (depth colour, sky reflection, blended over the bed), medium
// adds the colour copy (refraction, the bed tinted in the shader), high adds
// the screen-space reflection and the caustics the surfaces throw on the bed
// they show (water_caustics.go).
//
// Water draws through the world program only; nothing the pass prepares is
// read by any other program, and the frame uniforms are cleared after it, so
// a water surface drawn anywhere else (the material editor's preview) takes
// the shader's fallbacks.

import (
	"log"
	"sort"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// water.glsl's waterFrameFlags bits.
const (
	waterHaveDepth  = 1
	waterHaveColour = 2
	waterHaveSky    = 4
	waterHaveSSR    = 8
	// waterHaveCaustics: the surfaces throw caustics on the bed they show
	// (water.glsl's waterBedCaustics; phase W4, water_caustics.go).
	waterHaveCaustics = 16
)

// isWaterDraw reports whether a queued transparent draw is a water surface.
func isWaterDraw(td *TransparentDraw) bool {
	if td.Batch.Batch != nil {
		return frameSurface(&td.Batch.Batch.Material).st.Water.Enabled
	}
	return td.Geom != nil && td.Geom.SurfaceState().Water.Enabled
}

// waterQueue is the frame's water draws, kept from frame to frame.
var waterQueue []TransparentDraw

// splitWaterDraws moves the water surfaces out of draws, returning what is
// left (in draws' own storage) and the water (in waterQueue's).
func splitWaterDraws(draws []TransparentDraw) (rest, water []TransparentDraw) {
	water = waterQueue[:0]
	rest = draws[:0]
	for i := range draws {
		if isWaterDraw(&draws[i]) {
			water = append(water, draws[i])
		} else {
			rest = append(rest, draws[i])
		}
	}
	// What was moved out is left in the tail; clear it so nothing in it is
	// kept reachable.
	clear(draws[len(rest):])
	waterQueue = water
	return rest, water
}

// batchCastsShadow is gfx.Geometry.CastsShadow for a batch's material, off
// the frame's resolved surface state: the shadow passes ask it of every
// group, every light.
func batchCastsShadow(m *gfx.Material) bool {
	return m.Alpha >= 0.05 && !frameSurface(m).st.Water.Enabled
}

// waterTargets is the pass's copies of the frame's colour and depth.
type waterTargets struct {
	depthTex, depthFBO   uint32
	colourTex, colourFBO uint32
	w, h                 int32
	src                  uint32
	srcKnown             bool
	depthInternal        int32
	hasDepth             bool
	colourInternal       int32
	hasColour            bool
	// The blits from this source have been seen to work (or not) once:
	// asking GL stalls the frame (frame_target.go), and the answer cannot
	// change until the source or the size does.
	depthChecked, depthFailed   bool
	colourChecked, colourFailed bool
}

var waterCopies waterTargets

// sourceColourFormat is the internal format a texture needs for a colour
// blit from src's read buffer to be legal — a multisampled source must be
// resolved into exactly its own format. ok is false when src has no colour.
func sourceColourFormat(src uint32) (internal int32, ok bool) {
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, src)
	att := uint32(gl.COLOR_ATTACHMENT0)
	if src == 0 {
		att = gl.BACK_LEFT
	}
	param := func(pname uint32) int32 {
		var v int32
		gl.GetFramebufferAttachmentParameteriv(gl.READ_FRAMEBUFFER, att, pname, &v)
		return v
	}
	if param(gl.FRAMEBUFFER_ATTACHMENT_OBJECT_TYPE) == gl.NONE {
		return gl.RGBA8, false
	}
	bits := param(gl.FRAMEBUFFER_ATTACHMENT_RED_SIZE)
	alpha := param(gl.FRAMEBUFFER_ATTACHMENT_ALPHA_SIZE)
	float := param(gl.FRAMEBUFFER_ATTACHMENT_COMPONENT_TYPE) == gl.FLOAT
	switch {
	case float && bits > 16:
		return gl.RGBA32F, true
	case float:
		return gl.RGBA16F, true
	case bits == 10:
		return gl.RGB10_A2, true
	case alpha == 0:
		return gl.RGB8, true
	default:
		return gl.RGBA8, true
	}
}

// ensure (re)builds the copies for a w × h viewport of src.
func (t *waterTargets) ensure(src uint32, w, h int32, colour bool) {
	if !t.srcKnown || t.src != src {
		t.src, t.srcKnown = src, true
		t.depthInternal, t.hasDepth = sourceDepthFormat(src)
		t.colourInternal, t.hasColour = sourceColourFormat(src)
		t.depthChecked, t.depthFailed, t.colourChecked, t.colourFailed = false, false, false, false
		t.release()
	}
	if t.w != w || t.h != h {
		t.release()
		t.w, t.h = w, h
		t.depthChecked, t.colourChecked = false, false
	}
	if t.depthTex == 0 && t.hasDepth {
		format, xtype, attach := depthUpload(t.depthInternal)
		gl.GenTextures(1, &t.depthTex)
		gl.BindTexture(gl.TEXTURE_2D, t.depthTex)
		gl.TexImage2D(gl.TEXTURE_2D, 0, t.depthInternal, w, h, 0, format, xtype, nil)
		setNearestClamp()
		gl.GenFramebuffers(1, &t.depthFBO)
		gl.BindFramebuffer(gl.FRAMEBUFFER, t.depthFBO)
		gl.FramebufferTexture2D(gl.FRAMEBUFFER, attach, gl.TEXTURE_2D, t.depthTex, 0)
		gl.DrawBuffer(gl.NONE)
		gl.ReadBuffer(gl.NONE)
		if s := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); s != gl.FRAMEBUFFER_COMPLETE {
			log.Printf("water: depth copy framebuffer incomplete (0x%x) — water drawn without depth", s)
			t.depthFailed = true
		}
	}
	if colour && t.colourTex == 0 && t.hasColour {
		gl.GenTextures(1, &t.colourTex)
		gl.BindTexture(gl.TEXTURE_2D, t.colourTex)
		gl.TexStorage2D(gl.TEXTURE_2D, 1, uint32(t.colourInternal), w, h)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
		gl.GenFramebuffers(1, &t.colourFBO)
		gl.BindFramebuffer(gl.FRAMEBUFFER, t.colourFBO)
		gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, t.colourTex, 0)
		gl.DrawBuffer(gl.COLOR_ATTACHMENT0)
		if s := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); s != gl.FRAMEBUFFER_COMPLETE {
			log.Printf("water: colour copy framebuffer incomplete (0x%x) — no refraction", s)
			t.colourFailed = true
		}
	}
	gl.BindTexture(gl.TEXTURE_2D, 0)
}

func (t *waterTargets) release() {
	for _, f := range []*uint32{&t.depthFBO, &t.colourFBO} {
		if *f != 0 {
			gl.DeleteFramebuffers(1, f)
			*f = 0
		}
	}
	for _, x := range []*uint32{&t.depthTex, &t.colourTex} {
		if *x != 0 {
			gl.DeleteTextures(1, x)
			*x = 0
		}
	}
	t.w, t.h = 0, 0
}

// blit copies mask from src's viewport rect into fbo, checking the first
// time that GL took it. ok is false if it refused.
func (t *waterTargets) blit(src, fbo uint32, vp [4]int32, mask uint32, checked, failed *bool, what string) bool {
	if *failed {
		return false
	}
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, src)
	if mask == gl.COLOR_BUFFER_BIT {
		if src == 0 {
			gl.ReadBuffer(gl.BACK)
		} else {
			gl.ReadBuffer(gl.COLOR_ATTACHMENT0)
		}
	}
	gl.BindFramebuffer(gl.DRAW_FRAMEBUFFER, fbo)
	if !*checked {
		for i := 0; i < 16 && gl.GetError() != gl.NO_ERROR; i++ {
		}
	}
	gl.BlitFramebuffer(vp[0], vp[1], vp[0]+vp[2], vp[1]+vp[3], 0, 0, vp[2], vp[3], mask, gl.NEAREST)
	if !*checked {
		*checked = true
		if err := gl.GetError(); err != gl.NO_ERROR {
			*failed = true
			log.Printf("water: the driver refused the %s copy (GL error 0x%x) — water drawn without it", what, err)
			return false
		}
	}
	return true
}

// copyScene fills the copies from the bound framebuffer's viewport and
// returns the water.glsl flags they make true.
func (t *waterTargets) copyScene(src uint32, vp [4]int32, colour bool) int32 {
	t.ensure(src, vp[2], vp[3], colour)
	var flags int32
	if t.hasDepth && t.depthTex != 0 &&
		t.blit(src, t.depthFBO, vp, gl.DEPTH_BUFFER_BIT, &t.depthChecked, &t.depthFailed, "depth") {
		flags |= waterHaveDepth
	}
	if colour && flags&waterHaveDepth != 0 && t.hasColour && t.colourTex != 0 &&
		t.blit(src, t.colourFBO, vp, gl.COLOR_BUFFER_BIT, &t.colourChecked, &t.colourFailed, "colour") {
		flags |= waterHaveColour
	}
	return flags
}

// WaterDrawStats is the last frame's water pass, for the debug overlay and
// the tests.
var WaterDrawStats struct {
	Surfaces int
	Flags    int32
}

// renderWater draws the frame's water surfaces; see the file comment.
func renderWater(lvl *world.Level, water []TransparentDraw) {
	WaterDrawStats.Surfaces, WaterDrawStats.Flags = len(water), 0
	if len(water) == 0 {
		return
	}
	q := gfx.WaterQuality
	vp := boundViewport()
	src := boundFramebuffer()
	state := currentGLState()

	ProfileBegin("water: copy")
	flags := waterCopies.copyScene(src, vp, q >= gfx.WaterMedium)
	if waterSky.draw(lvl) {
		flags |= waterHaveSky
	}
	if q >= gfx.WaterHigh {
		flags |= waterHaveSSR | waterHaveCaustics
	}
	state.restore(src, vp)
	ProfileEnd()

	ProfileBegin("water")
	gfx.WaterFrame.SceneColour = waterCopies.colourTex
	gfx.WaterFrame.SceneDepth = waterCopies.depthTex
	gfx.WaterFrame.Sky = waterSky.tex
	uploadWaterFrame(gfx.Program, flags, vp)
	WaterDrawStats.Flags = flags

	sort.Slice(water, func(a, b int) bool { return water[a].Distance > water[b].Distance })
	gl.Enable(gl.BLEND)
	gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
	gl.DepthMask(true)
	for i := range water {
		td := &water[i]
		if td.DoubleSided {
			gl.Enable(gl.CULL_FACE)
			gl.CullFace(gl.FRONT)
			td.draw()
			gl.CullFace(gl.BACK)
			td.draw()
			gl.Disable(gl.CULL_FACE)
		} else {
			td.draw()
		}
	}
	gl.Disable(gl.BLEND)
	// The flags off again: a water draw outside this pass must not read
	// copies of a frame that has moved on.
	gl.UseProgram(gfx.Program)
	gl.Uniform1i(gfx.UniformLoc(gfx.Program, "waterFrameFlags\x00"), 0)
	gfx.WaterFrame.SceneColour, gfx.WaterFrame.SceneDepth, gfx.WaterFrame.Sky = 0, 0, 0
	clear(water)
	ProfileEnd()
}

// uploadWaterFrame sends prog the frame uniforms water.glsl reads.
func uploadWaterFrame(prog uint32, flags int32, vp [4]int32) {
	gl.UseProgram(prog)
	loc := func(name string) int32 { return gfx.UniformLoc(prog, name+"\x00") }
	gl.Uniform1i(loc("waterFrameFlags"), flags)
	gl.Uniform4f(loc("waterViewport"), float32(vp[0]), float32(vp[1]), float32(vp[2]), float32(vp[3]))
	p := gfx.Projection
	gl.Uniform2f(loc("waterDepthParams"), p[10], p[14])
	pv := gfx.Projection.Mul4(gfx.Cam.Matrix)
	gl.UniformMatrix4fv(loc("waterViewProj"), 1, false, &pv[0])
	gl.Uniform1f(loc("waterSkyMips"), float32(waterSky.mips))
	// The rings and the rain (phase W5, water_interact.go).
	uploadWaterInteract(prog)
}
