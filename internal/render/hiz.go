package render

// hiz.go — a Hi-Z pyramid: the depth drawn so far this frame, copied out of
// the (multisampled) framebuffer and reduced level by level to the FURTHEST
// depth under each texel. A sphere whose nearest point is nearer than every
// texel under its screen rectangle is hidden behind what is already drawn.
//
// Written for the foliage cull (foliage_gpu.go) and shared since Phase 1 of
// the Nanite plan with the GPU-driven cluster cull (nanite_cull.go), whose
// second pass tests against it. Each owner keeps its own pyramid — they are
// built at different points of the frame, from different depth.

import (
	"fmt"
	"log"

	"foundry/internal/gfx"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// hiZPyramid is the depth drawn so far this frame, reduced to a pyramid of
// furthest depths. Level 0 is half the viewport.
type hiZPyramid struct {
	// name is who owns it, for the log; unit the texture unit the reduction
	// binds its source on.
	name     string
	unit     int32
	prog     progUniforms
	vao      uint32
	depthTex uint32
	depthFBO uint32
	depthFmt int32
	w, h     int32
	src      uint32
	srcKnown bool
	srcFmt   int32
	srcDepth bool
	tex, fbo uint32
	size     [2]int32
	levels   int32
	failed   bool
	// checked: the depth copy from src into depthTex has been seen to work
	// once (volumetric.go's copyChecked, for the same reason).
	checked bool
}

func (h *hiZPyramid) init(name string, unit int32) error {
	h.name, h.unit = name, unit
	p, err := gfx.CompileProgramFromFiles(biomeShaderPath("foliage_hiz_vert.glsl"), biomeShaderPath("foliage_hiz_frag.glsl"))
	if err != nil {
		return fmt.Errorf("hi-z program: %v", err)
	}
	h.prog = progUniforms{prog: p, loc: map[string]int32{}}
	gl.GenVertexArrays(1, &h.vao)
	gl.GenFramebuffers(1, &h.fbo)
	return nil
}

// build fills the pyramid from the bound framebuffer's depth. False when
// there is no depth to read (the cull then tests only the frustum).
func (h *hiZPyramid) build() bool {
	if h.failed {
		return false
	}
	// Known, not asked, inside a frame: see frame_target.go. There the
	// read framebuffer is the target too.
	vp := boundViewport()
	src := boundFramebuffer()
	readFB := int32(src)
	if !drawTarget.set {
		gl.GetIntegerv(gl.READ_FRAMEBUFFER_BINDING, &readFB)
	}
	if !h.srcKnown || h.src != src {
		h.src, h.srcKnown = src, true
		h.srcFmt, h.srcDepth = sourceDepthFormat(src)
		h.checked = false
	}
	if !h.srcDepth {
		gl.BindFramebuffer(gl.READ_FRAMEBUFFER, uint32(readFB))
		return false
	}
	w, hh := vp[2], vp[3]
	if w < 2 || hh < 2 {
		return false
	}
	if h.depthTex == 0 || h.w != w || h.h != hh || h.depthFmt != h.srcFmt {
		h.resize(w, hh)
		h.checked = false
		if h.failed {
			return false
		}
	}
	check := !h.checked
	if check {
		for i := 0; i < 16 && gl.GetError() != gl.NO_ERROR; i++ {
		}
	}
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, src)
	gl.BindFramebuffer(gl.DRAW_FRAMEBUFFER, h.depthFBO)
	gl.BlitFramebuffer(vp[0], vp[1], vp[0]+w, vp[1]+hh, 0, 0, w, hh, gl.DEPTH_BUFFER_BIT, gl.NEAREST)
	h.checked = true
	if check {
		if e := gl.GetError(); e != gl.NO_ERROR {
			log.Printf("%s: the driver refused the depth copy (GL error 0x%x) — culling by the frustum only", h.name, e)
			h.failed = true
			gl.BindFramebuffer(gl.DRAW_FRAMEBUFFER, src)
			gl.BindFramebuffer(gl.READ_FRAMEBUFFER, uint32(readFB))
			return false
		}
	}

	was := currentGLState()
	depthTest, blend, depthMask := was.depthTest, was.blend, was.depthMask
	gl.Disable(gl.DEPTH_TEST)
	gl.Disable(gl.BLEND)
	gl.DepthMask(false)
	gl.UseProgram(h.prog.prog)
	gl.Uniform1i(h.prog.U("src"), h.unit)
	gl.BindVertexArray(h.vao)
	gl.BindFramebuffer(gl.DRAW_FRAMEBUFFER, h.fbo)
	gl.ActiveTexture(gl.TEXTURE0 + uint32(h.unit))
	sw, sh := w, hh
	for l := int32(0); l < h.levels; l++ {
		if l == 0 {
			gl.BindTexture(gl.TEXTURE_2D, h.depthTex)
		} else {
			gl.BindTexture(gl.TEXTURE_2D, h.tex)
			gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_BASE_LEVEL, l-1)
			gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAX_LEVEL, l-1)
		}
		dw, dh := max(sw/2, 1), max(sh/2, 1)
		gl.FramebufferTexture2D(gl.DRAW_FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, h.tex, l)
		gl.Uniform2i(h.prog.U("srcSize"), sw, sh)
		gl.Viewport(0, 0, dw, dh)
		gl.DrawArrays(gl.TRIANGLES, 0, 3)
		sw, sh = dw, dh
	}
	gl.BindTexture(gl.TEXTURE_2D, h.tex)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_BASE_LEVEL, 0)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAX_LEVEL, h.levels-1)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	gl.ActiveTexture(gl.TEXTURE0)

	gl.BindFramebuffer(gl.DRAW_FRAMEBUFFER, src)
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, uint32(readFB))
	gl.Viewport(vp[0], vp[1], vp[2], vp[3])
	if depthTest {
		gl.Enable(gl.DEPTH_TEST)
	}
	if blend {
		gl.Enable(gl.BLEND)
	}
	gl.DepthMask(depthMask)
	return true
}

func (h *hiZPyramid) resize(w, hh int32) {
	if h.depthTex != 0 {
		gl.DeleteTextures(1, &h.depthTex)
		gl.DeleteFramebuffers(1, &h.depthFBO)
	}
	if h.tex != 0 {
		gl.DeleteTextures(1, &h.tex)
	}
	h.w, h.h, h.depthFmt = w, hh, h.srcFmt
	format, xtype, attach := depthUpload(h.srcFmt)
	gl.GenTextures(1, &h.depthTex)
	gl.BindTexture(gl.TEXTURE_2D, h.depthTex)
	gl.TexImage2D(gl.TEXTURE_2D, 0, h.srcFmt, w, hh, 0, format, xtype, nil)
	setNearestClamp()
	gl.GenFramebuffers(1, &h.depthFBO)
	gl.BindFramebuffer(gl.DRAW_FRAMEBUFFER, h.depthFBO)
	gl.FramebufferTexture2D(gl.DRAW_FRAMEBUFFER, attach, gl.TEXTURE_2D, h.depthTex, 0)
	gl.DrawBuffer(gl.NONE)
	if s := gl.CheckFramebufferStatus(gl.DRAW_FRAMEBUFFER); s != gl.FRAMEBUFFER_COMPLETE {
		log.Printf("%s: depth copy framebuffer incomplete (0x%x) — culling by the frustum only", h.name, s)
		h.failed = true
		return
	}
	h.size = [2]int32{max(w/2, 1), max(hh/2, 1)}
	h.levels = 1
	for s := max(h.size[0], h.size[1]); s > 1; s /= 2 {
		h.levels++
	}
	gl.GenTextures(1, &h.tex)
	gl.BindTexture(gl.TEXTURE_2D, h.tex)
	lw, lh := h.size[0], h.size[1]
	for l := int32(0); l < h.levels; l++ {
		gl.TexImage2D(gl.TEXTURE_2D, l, gl.R32F, lw, lh, 0, gl.RED, gl.FLOAT, nil)
		lw, lh = max(lw/2, 1), max(lh/2, 1)
	}
	setNearestClamp()
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST_MIPMAP_NEAREST)
	gl.BindTexture(gl.TEXTURE_2D, 0)
}
