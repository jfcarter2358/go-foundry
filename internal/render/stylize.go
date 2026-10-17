package render

// stylize.go — a painted level's paint pass (world.Stylize; experimental;
// docs/guide/38-stylized-lighting.md).
//
// The look has two halves. The light ramp — cool shadow, warm light — is in
// every lit shader already (light_model.glsl's stylizeLight, fed by the
// scene block), on either frame path. This file is the other half: edge
// highlights, a wash in the creases and dry-brushed raised detail, painted
// over the frame from the surfaces' own normals.
//
// # The normal buffer
//
// In a painted scene-target frame the scene target carries a fifth
// attachment (scene_target.go's COLOR4, RGBA32UI). While the opaque world
// draws — the compiled surfaces, the chunks' brushes, terrain, entities,
// scenery and the visibility buffer's material pass, between
// stylizeBeginWrites and stylizeEndWrites in DrawWorld — the colour draw set
// is the colour AND that buffer (drawBuffersColour), and the programs drawing
// then write their surface's record at location 1: its geometric and shading
// normals and its view depth (light_model.glsl's stylizePack). Everything
// drawn after (foliage, grass, sprites, decals, glass) leaves the buffer
// alone; the pass tells a record such a thing has since covered by its depth
// against the depth buffer's.
//
// # The pass
//
// stylize_frag.glsl, once a frame (stylizePaint): the frame's colour with
// the paint on it, into a target of its own, copied back over the colour.
// After the world's last depth-writing pass and TAA's motion, and before the
// viewmodel — which clears the depth the pass checks every record against,
// and whose hands, drawn after, take no paint. So also before the TAA
// resolve, on purpose: the buffer is this frame's, jittered, at the render
// size, and the history is what anti-aliases its edges.
//
// The forward path (MSAA mode, wireframe, FOUNDRY_EDITOR_SCENE_FRAME=0) has no
// scene target and so no paint; it still takes the light ramp.

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// StylizeDebug is FOUNDRY_STYLIZE_DEBUG: 0 the paint, 1 the normal buffer's
// records (black none, red covered since, green good), 2 the paint's masks
// (red edge, green dry-brush, blue wash). The levers to tune a look by.
var StylizeDebug = func() int32 {
	n, _ := strconv.Atoi(os.Getenv("FOUNDRY_STYLIZE_DEBUG"))
	return int32(n)
}()

// stylizeState is the paint pass's program, its target and the frame's
// switches.
type stylizeState struct {
	tried, ok bool
	prog      progUniforms

	out, outFBO uint32
	w, h        int32

	// frame: this frame is painted and has the normal buffer; look is the
	// level's.
	frame bool
	look  *world.Stylize
	// writing: the opaque world is drawing its records (drawBuffersColour).
	writing bool
}

var stylizer stylizeState

func (s *stylizeState) ready() bool {
	if s.tried {
		return s.ok
	}
	s.tried = true
	if err := s.build(); err != nil {
		log.Printf("stylize: unavailable: %v — painted levels take the light ramp alone", err)
		return false
	}
	s.ok = true
	return true
}

func (s *stylizeState) build() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	prog, err := gfx.CompileProgramFromSources(mustReadShader(biomeShaderPath("taa_fullscreen_vert.glsl")),
		mustReadShader(biomeShaderPath("stylize_frag.glsl")))
	if err != nil {
		return fmt.Errorf("stylize_frag.glsl: %v", err)
	}
	s.prog = progUniforms{prog: prog, loc: map[string]int32{}}
	return nil
}

// ensure sizes the pass's target to the render size.
func (s *stylizeState) ensure(w, h int32) bool {
	if s.out != 0 && s.w == w && s.h == h {
		return true
	}
	s.release()
	s.out, s.outFBO = colorTarget(gl.RGBA16F, gl.RGBA, w, h)
	if s.out == 0 {
		return false
	}
	// Linear, as the scene's colour is: the resolve reads between texels.
	gl.BindTexture(gl.TEXTURE_2D, s.out)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	s.w, s.h = w, h
	return true
}

func (s *stylizeState) release() {
	if s.out != 0 {
		gl.DeleteTextures(1, &s.out)
		gl.DeleteFramebuffers(1, &s.outFBO)
		s.out, s.outFBO = 0, 0
	}
	s.w, s.h = 0, 0
}

// stylizeFrameBegin decides whether this frame is painted: a scene-target
// frame of a level whose look is on, with the normal buffer and the program
// to hand. After sceneFrameBegin, before the frame's clear (which clears the
// buffer).
func stylizeFrameBegin(lvl *world.Level) {
	s := &stylizer
	s.frame, s.writing, s.look = false, false, nil
	look := world.LevelStylize(lvl)
	if look == nil || !taa.active || !s.ready() {
		return
	}
	if !scene.ensureStylize() {
		return
	}
	// ensureStylize bound the scene target to attach the buffer; it is the
	// frame's target anyway.
	gl.BindFramebuffer(gl.FRAMEBUFFER, scene.fbo)
	s.frame, s.look = true, look
}

// stylizeBeginWrites makes the opaque world's draws write their records.
func stylizeBeginWrites() {
	if !stylizer.frame {
		return
	}
	stylizer.writing = true
	drawBuffersColour()
}

// stylizeEndWrites puts the colour draw set back to the colour alone, on the
// scene target, for everything drawn after the opaque world.
func stylizeEndWrites() {
	if !stylizer.writing {
		return
	}
	stylizer.writing = false
	gl.BindFramebuffer(gl.FRAMEBUFFER, scene.fbo)
	drawBuffersColour()
}

// stylizeMaterialDrawBuffers sets the material pass's framebuffer's draw set
// (nanite_vis.go): the colour and, while the world writes its records, the
// normal buffer it carries at COLOR1.
func stylizeMaterialDrawBuffers() {
	if stylizer.writing && scene.stylize != 0 {
		gl.DrawBuffers(2, &[]uint32{gl.COLOR_ATTACHMENT0, gl.COLOR_ATTACHMENT1}[0])
		return
	}
	gl.DrawBuffer(gl.COLOR_ATTACHMENT0)
}

// stylizePaint paints the frame: the scene's colour with the paint on it,
// drawn into the pass's own target and copied back over the colour. Nothing
// when the frame is not painted. Leaves the scene target bound at the render
// size, the world program in use and blending off, as the viewmodel after it
// expects.
func stylizePaint() {
	s := &stylizer
	if !s.frame || s.look == nil || !taa.ready() {
		return
	}
	look := s.look
	w, h := scene.w, scene.h
	if !s.ensure(w, h) {
		return
	}
	ProfileBegin("stylize")
	defer ProfileEnd()
	// The target has no depth or stencil, so neither test can refuse a pixel.
	gl.Disable(gl.BLEND)
	gl.BindFramebuffer(gl.FRAMEBUFFER, s.outFBO)
	gl.Viewport(0, 0, w, h)
	// The frame's own projection, jittered: the depth was drawn through it.
	s.draw(scene.colour, scene.stylize, scene.depth, w, h, gfx.Cam.Matrix, gfx.Projection, look)
	// Back over the colour, which the viewmodel and the resolve go on with.
	gl.CopyImageSubData(s.out, gl.TEXTURE_2D, 0, 0, 0, 0, scene.colour, gl.TEXTURE_2D, 0, 0, 0, 0, w, h, 1)
	gl.BindFramebuffer(gl.FRAMEBUFFER, scene.fbo)
	gl.Viewport(0, 0, w, h)
	gl.UseProgram(gfx.Program)
}

// draw runs the pass over the bound framebuffer's viewport: colour, gbuf
// (the normal buffer) and depth the frame's, w by h, drawn through view and
// proj, painted with look.
func (s *stylizeState) draw(colour, gbuf, depth uint32, w, h int32, view, proj mgl32.Mat4, look *world.Stylize) {
	p := &s.prog
	gl.UseProgram(p.prog)
	gl.BindVertexArray(taa.vao)
	for unit, tex := range []uint32{colour, gbuf, depth} {
		gl.ActiveTexture(gl.TEXTURE0 + uint32(unit))
		gl.BindTexture(gl.TEXTURE_2D, tex)
	}
	gl.Uniform1i(p.U("colourSrc"), 0)
	gl.Uniform1i(p.U("gbuf"), 1)
	gl.Uniform1i(p.U("depthSrc"), 2)
	gl.Uniform2i(p.U("size"), w, h)
	inv := proj.Mul4(view).Inv()
	gl.UniformMatrix4fv(p.U("invViewProj"), 1, false, &inv[0])
	rot := view.Mat3()
	gl.UniformMatrix3fv(p.U("viewRot"), 1, false, &rot[0])
	gl.Uniform2f(p.U("depthAB"), proj[10], proj[14])
	gl.Uniform1f(p.U("pxScale"), float32(h)/1080)
	paint := func(name string, c mgl32.Vec3, a float32) {
		gl.Uniform4f(p.U(name), c[0], c[1], c[2], a)
	}
	paint("edgePaint", look.EdgeColor, look.Edge)
	gl.Uniform1f(p.U("edgeWidth"), look.EdgeWidth)
	paint("washPaint", look.WashColor, look.Wash)
	paint("dryPaint", look.DrybrushColor, look.Drybrush)
	gl.Uniform2f(p.U("dryParams"), look.DrybrushScale, look.DrybrushThreshold)
	gl.Uniform1f(p.U("fadeDistance"), look.FadeDistance)
	gl.Uniform1i(p.U("debugView"), StylizeDebug)
	gl.DrawArrays(gl.TRIANGLES, 0, 3)
	for u := uint32(0); u < 3; u++ {
		gl.ActiveTexture(gl.TEXTURE0 + u)
		gl.BindTexture(gl.TEXTURE_2D, 0)
	}
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindVertexArray(0)
}
