package render

// entity_prepass.go — a depth prepass for entities and the viewmodel.
//
// Entities are forward-shaded: every fragment that passes the depth test runs
// the whole light model, the shadow lookups and the GI. The world in front of
// an entity is already in the depth buffer when it draws, so the world hides
// it for free — but nothing hides a model from itself. Its back faces are
// drawn too (the pass draws with culling off), a character's arm crosses its
// chest, a gun sits in a glove, and wherever a farther triangle of the model
// happens to be drawn before a nearer one, that pixel is shaded twice. The
// viewmodel, inches from the eye, is the worst of it: a large share of the
// screen, and nothing in front of it at all.
//
// So each is drawn twice, as nanite_prepass.go draws the cull's batches: once
// through a program that writes only depth — the world's own vertex stage,
// skinning and all, so the depth is the colour pass's to the bit — and then
// through the world program as before, which at the frame's LEQUAL now shades
// each covered pixel of a model once. The colour pass keeps writing depth: the
// prepass writes it only where the colour pass is certain to cover the pixel
// (entity_depth_frag.glsl), and leaves cutout edges to it.
//
// Transparent attachments are not in it: they draw later, sorted, and blend.
//
// FOUNDRY_ENTITY_PREPASS=0 turns it off.

import (
	"fmt"
	"log"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// EntityPrepass switches the entities' and the viewmodel's depth prepass.
var EntityPrepass = gfx.EnvFlag("FOUNDRY_ENTITY_PREPASS", true)

type entityPrepassProgram struct {
	tried, ok bool
	p         progUniforms
	skin      gfx.SkinUniforms
	// warmedFor is the level whose entities' vertex layouts have been drawn
	// through the program once (see warm).
	warmedFor *world.Level
}

var entityPrepassProg entityPrepassProgram

func (pp *entityPrepassProgram) ready() bool {
	if pp.tried {
		return pp.ok
	}
	pp.tried = true
	if err := pp.build(); err != nil {
		log.Printf("entities: depth prepass unavailable: %v", err)
		return false
	}
	pp.ok = true
	return true
}

func (pp *entityPrepassProgram) build() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	vert, _, err := gfx.WorldProgramSources(fileutil.ShaderPath("vertex.glsl"), fileutil.ShaderPath("fragment.glsl"))
	if err != nil {
		return err
	}
	frag := mustReadShader(fileutil.ShaderPath("entity_depth_frag.glsl"))
	prog, err := gfx.CompileProgramFromSources(vert, frag)
	if err != nil {
		return err
	}
	gfx.ForgetUniformLocations(prog)
	pp.p = progUniforms{prog: prog, loc: map[string]int32{}}
	return nil
}

// begin readies the program for a pass: the matrices the world program will
// draw the colour pass with, colour writes off. False when there is no
// prepass.
func (pp *entityPrepassProgram) begin() bool {
	if !EntityPrepass || !pp.ready() {
		return false
	}
	p := &pp.p
	// Read out of the world program rather than taken from gfx: the depth has
	// to be the colour pass's to the bit, and what the program holds is TAA's
	// jittered lens in a TAA frame, the viewmodel's own lens in its pass (see
	// the same read in nanite_prepass.go's begin).
	var projection, cam mgl32.Mat4
	gl.GetUniformfv(gfx.Program, gfx.ProjectionUniform, &projection[0])
	gl.GetUniformfv(gfx.Program, gfx.UniformLoc(gfx.Program, "camera\x00"), &cam[0])
	gl.UseProgram(p.prog)
	gl.UniformMatrix4fv(p.U("projection"), 1, false, &projection[0])
	gl.UniformMatrix4fv(p.U("camera"), 1, false, &cam[0])
	gl.Uniform1i(p.U("instanced"), 0)
	gl.Uniform1i(p.U("tex"), 0)
	gl.Uniform1i(p.U("blendTex"), gfx.BlendTexUnit)
	gl.Uniform1i(p.U("blendMask"), gfx.BlendMaskUnit)
	// Sharpened always: what it then keeps (a sharpened alpha of 1) is
	// covered by the colour pass whichever way that pass decides an edge.
	gl.Uniform1i(p.U("alphaSharpen"), 1)
	gl.ColorMask(false, false, false, false)
	return true
}

// end puts colour writes back and the world program up.
func (pp *entityPrepassProgram) end() {
	gl.ColorMask(true, true, true, true)
	gl.BindVertexArray(0)
	gl.UseProgram(gfx.Program)
}

// draw writes one opaque submesh's depth; a transparent one is skipped.
func (pp *entityPrepassProgram) draw(g *gfx.Geometry, model mgl32.Mat4) {
	if len(g.Vertices) == 0 || g.IsTransparent() {
		return
	}
	p := &pp.p
	st := g.SurfaceState()
	gl.UniformMatrix4fv(p.U("model"), 1, false, &model[0])
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
	gfx.ApplySkinUniforms(&pp.skin, p.prog, g.BonePalette)
	gl.BindVertexArray(g.Vao)
	g.DrawTriangles()
}

// warm draws every attachment of lvl's entities, and the player's, through
// the program once with nothing able to pass the scissor, the first time it
// meets the level. A driver that builds a pipeline at its first draw (Mesa's
// D3D12 translation does — see warmSpriteMode) builds one per vertex layout
// here, while the level is loading, and not in the middle of play the first
// time a skinned soldier walks into view. Called between begin and end.
func (pp *entityPrepassProgram) warm(lvl *world.Level) {
	if pp.warmedFor == lvl {
		return
	}
	pp.warmedFor = lvl
	gl.Enable(gl.SCISSOR_TEST)
	gl.Scissor(0, 0, 0, 0)
	ident := mgl32.Ident4()
	each := func(e *world.Entity) {
		if e == nil {
			return
		}
		for mi := range e.Models {
			m := &e.Models[mi]
			if !m.HasGeometry() {
				continue
			}
			for si := range m.Submeshes {
				pp.draw(&m.Submeshes[si], ident)
			}
		}
	}
	for ei := range lvl.Entities {
		each(&lvl.Entities[ei])
	}
	each(world.PlayerEntity)
	each(world.PlayerBodyEntity)
	gl.Disable(gl.SCISSOR_TEST)
}
