package render

// nanite_prepass.go — a depth prepass for what the GPU-driven cull draws.
// Phase 1 of the Nanite plan (docs/nanite/PHASE_1.md).
//
// The world is forward-shaded: every fragment that passes the depth test runs
// the whole light model, the shadow lookups and the GI. So a pixel costs as
// many shadings as there are surfaces that reach it in DRAW ORDER before the
// nearest one — and the cluster cull changes the draw order. Measured on
// level1's ship hold (GTX 1070, 1600x900, 8x MSAA): the occlusion pass removed
// 226 of 264 clusters, every one of them truly hidden, and the hold got 2 ms
// SLOWER to draw, because some of those hidden clusters had been drawn early
// enough to stop the visible walls behind them being shaded twice.
//
// The answer a forward renderer has always had is a depth prepass, and the GPU
// cull makes it nearly free: the indirect commands are already built, so each
// pass draws them twice — once through a program that writes only depth (the
// world's own vertex stage, so the depth is identical, and a fragment stage
// that computes nothing but cutout coverage), then through the world program
// at LEQUAL, which shades each covered sample exactly once. Phase 4's
// visibility buffer replaces this; until then it is what makes the cull's
// savings land on the GPU.
//
// Skipped for a group that is unlit, water, or blended by a material: those
// are not the opaque, depth-honest surfaces a prepass assumes, and they draw
// as before. FOUNDRY_NANITE_PREPASS=0 turns it off; cooked Nanite meshes take
// it only with FOUNDRY_NANITE_PREPASS_MESHES=1 (see NanitePrepassMeshes).

import (
	"fmt"
	"log"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// NanitePrepass switches the depth prepass; see the file comment.
var NanitePrepass = gfx.EnvFlag("FOUNDRY_NANITE_PREPASS", true)

// NanitePrepassMeshes extends it to cooked Nanite meshes. Off by default, on
// measurement: a dense mesh is bound by its vertices, not its pixels, and a
// prepass draws every vertex twice — 256 rocks of 82k triangles took 2 to 4
// ms longer with it (docs/nanite/PHASE_1.md). A level's brushes, few
// triangles and many lit pixels, are the other way round.
var NanitePrepassMeshes = gfx.EnvFlag("FOUNDRY_NANITE_PREPASS_MESHES", false)

type nanitePrepassProgram struct {
	tried, ok bool
	p         progUniforms
}

var nanitePrepassProg nanitePrepassProgram

func (pp *nanitePrepassProgram) ready() bool {
	if pp.tried {
		return pp.ok
	}
	pp.tried = true
	if err := pp.build(); err != nil {
		log.Printf("nanite: depth prepass unavailable: %v", err)
		return false
	}
	pp.ok = true
	return true
}

func (pp *nanitePrepassProgram) build() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	vert, _, err := gfx.WorldProgramSources(fileutil.ShaderPath("vertex.glsl"), fileutil.ShaderPath("fragment.glsl"))
	if err != nil {
		return err
	}
	frag := mustReadShader(biomeShaderPath("nanite_depth_frag.glsl"))
	prog, err := gfx.CompileProgramFromSources(vert, frag)
	if err != nil {
		return err
	}
	gfx.ForgetUniformLocations(prog)
	pp.p = progUniforms{prog: prog, loc: map[string]int32{}}
	return nil
}

// prepassable reports whether a group in material m can take part.
func prepassable(m *gfx.Material) bool {
	st := m.SurfaceState()
	return !st.Unlit && !st.Water.Enabled && st.Blend == 0 && !st.IsTransparent()
}

// begin readies the prepass program for a pass: camera, colour writes off.
// False when there is no prepass this frame.
func (pp *nanitePrepassProgram) begin() bool {
	if !NanitePrepass || !pp.ready() {
		return false
	}
	p := &pp.p
	// The matrices the world program will draw the colour pass with, read
	// out of it rather than taken from gfx: the depth has to be the colour
	// pass's to the bit, and a frame whose projection was rebuilt while
	// another program was bound leaves the two apart (a headless session's
	// first frame does).
	var proj, cam mgl32.Mat4
	gl.GetUniformfv(gfx.Program, gfx.ProjectionUniform, &proj[0])
	gl.GetUniformfv(gfx.Program, gfx.UniformLoc(gfx.Program, "camera\x00"), &cam[0])
	gl.UseProgram(p.prog)
	gl.UniformMatrix4fv(p.U("projection"), 1, false, &proj[0])
	gl.UniformMatrix4fv(p.U("camera"), 1, false, &cam[0])
	gl.Uniform1i(p.U("tex"), 0)
	gl.Uniform1i(p.U("blendTex"), gfx.BlendTexUnit)
	gl.Uniform1i(p.U("blendMask"), gfx.BlendMaskUnit)
	gl.Uniform1i(p.U("alphaSharpen"), boolToInt32(alphaToCoverage()))
	gl.ColorMask(false, false, false, false)
	return true
}

// end puts colour writes back and the world program up.
func (pp *nanitePrepassProgram) end() {
	gl.ColorMask(true, true, true, true)
	gl.UseProgram(gfx.Program)
}

// material sets one group's coverage state: what decides which samples its
// fragments cover, and nothing else.
func (pp *nanitePrepassProgram) material(m *gfx.Material, model mgl32.Mat4, instanced bool) {
	p := &pp.p
	st := m.SurfaceState()
	gl.UniformMatrix4fv(p.U("model"), 1, false, &model[0])
	gl.Uniform1i(p.U("instanced"), boolToInt32(instanced))
	gl.Uniform1f(p.U("alpha"), st.Alpha)
	gl.Uniform1f(p.U("alphaCutoffBias"), st.CutoffBias)
	gl.Uniform1i(p.U("normalColor"), st.NormalColorFlag())
	gl.Uniform2f(p.U("uvOffset"), st.UVOffset.X(), st.UVOffset.Y())
	gl.Uniform2f(p.U("uvScaleBias"), st.UVScale.X()-1, st.UVScale.Y()-1)
	gl.Uniform1f(p.U("uvRotation"), st.UVRotation)
	hasBlend := st.BlendTexture != "" && st.BlendMask != ""
	gl.Uniform1i(p.U("hasBlend"), boolToInt32(hasBlend))
	if hasBlend {
		gl.ActiveTexture(gl.TEXTURE0 + gfx.BlendTexUnit)
		gl.BindTexture(gl.TEXTURE_2D, gfx.Textures[st.BlendTexture])
		gl.ActiveTexture(gl.TEXTURE0 + gfx.BlendMaskUnit)
		gl.BindTexture(gl.TEXTURE_2D, gfx.Textures[st.BlendMask])
	}
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, gfx.Textures[st.Texture])
}

// prepassBatch draws one pass's groups depth-only: for each group i whose
// material mat(i) can take part, model(i) and the VAO vao(i), then batch's
// commands. Reports whether it ran — the colour pass after it is the same
// either way, so this is purely the saving.
func prepassBatch(b *cullBatch, n int, mat func(i int) *gfx.Material, model func(i int) mgl32.Mat4,
	vao func(i int) uint32, instanced bool) bool {
	pp := &nanitePrepassProg
	if instanced && !NanitePrepassMeshes {
		return false
	}
	if !pp.begin() {
		return false
	}
	ProfileBegin("nanite: prepass")
	for i := 0; i < n; i++ {
		if !b.groupLive(i) {
			continue
		}
		m := mat(i)
		if !prepassable(m) {
			continue
		}
		pp.material(m, model(i), instanced)
		gl.BindVertexArray(vao(i))
		b.drawGroup(i)
	}
	gl.BindVertexArray(0)
	ProfileEnd()
	pp.end()
	return true
}
