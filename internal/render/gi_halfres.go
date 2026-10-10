package render

// gi_halfres.go — the probes' light at half resolution, for the visibility
// buffer's material pass.
//
// Every lit pixel samples the GI probes (light_model.glsl's giIrradiance):
// the eight probes of a cascade, sixteen where two cascades hand over, each a
// data fetch, a visibility fetch and an irradiance fetch with the weighting
// between them — most of what a pixel of the material pass costs, for a term
// that changes over metres, not pixels. In a scene-target frame the material
// pass (nanite_vis.go) shades once a pixel from a depth that is final before
// it starts, so the probes can be asked once for each 2x2 block instead:
//
//  1. giHalfResRun, before the material pass draws: a compute pass
//     (gi_halfres_comp.glsl) takes one pixel of each block, its position from
//     the frame's depth and its facing from the depths beside it, and stores
//     the probes' irradiance there with that surface (its normal and plane).
//     The pixel it takes turns with TAA's frame, so over four frames every
//     pixel of a block is sampled and TAA's history averages them.
//  2. The material pass reads it back (giHalfResIrradiance, compiled into its
//     program only — giHalfResSource): the four samples around the pixel,
//     each weighted by distance, facing and how near the pixel lies to its
//     plane. Where none of them is on the pixel's surface — an edge, a rail
//     thinner than a block, a normal map bent far off the facet — it asks the
//     probes itself, as before.
//
// Everything else that samples GI — the forward path (MSAA, the editor), the
// forward draws of a scene-target frame (entities, terrain, foliage, sprites)
// — keeps doing it a pixel at a time. FOUNDRY_GI_HALFRES=0 turns it off.
//
// What it gives up: the probes see the surface's facet normal from the depth,
// not its smoothed or normal-mapped one, so GI's own shading across a curved
// or bumped surface is a little softer; the direct light, which carries most
// of a normal map's look, is untouched.

import (
	"fmt"
	"log"
	"strings"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// GIHalfRes switches the material pass's half-resolution GI.
var GIHalfRes = gfx.EnvFlag("FOUNDRY_GI_HALFRES", true)

// The texture units the material program reads the half-resolution GI on,
// past GIProbePackedUnit.
const (
	giHalfResIrrUnit   = 45
	giHalfResGuideUnit = 46
)

// giHalfResOffsets is the pixel of each 2x2 block a TAA frame samples, in
// turn: the diagonals first, so two frames already cover the block's spread.
var giHalfResOffsets = [4][2]int32{{0, 0}, {1, 1}, {1, 0}, {0, 1}}

type giHalfResPass struct {
	tried, ok bool
	prog      progUniforms
	irr       uint32 // RGBA16F: irradiance, cover
	guide     uint32 // RGBA32F: the sample's normal (oct) and plane, valid
	w, h      int32  // the half-resolution size
	// The frame: whether giHalfResRun filled the textures, and with which
	// pixel of each block.
	active bool
	offset [2]int32
}

var giHalf giHalfResPass

// giHalfResSource is the material pass's fragment source with the
// half-resolution read compiled in: GI_HALF_RES defined after its #version
// line, ahead of light_model.glsl, which reads it.
func giHalfResSource(frag string) string {
	i := strings.Index(frag, "#version")
	if i < 0 {
		return frag
	}
	j := strings.IndexByte(frag[i:], '\n')
	if j < 0 {
		return frag
	}
	at := i + j + 1
	return frag[:at] + "#define GI_HALF_RES\n" + frag[at:]
}

func (g *giHalfResPass) ready() bool {
	if g.tried {
		return g.ok
	}
	g.tried = true
	if err := g.build(); err != nil {
		log.Printf("gi: half-resolution pass unavailable: %v", err)
		return false
	}
	g.ok = true
	return true
}

func (g *giHalfResPass) build() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	g.prog, err = giComputeProgram(giShaderSource("gi_halfres_comp.glsl", "", "light_model.glsl"))
	return err
}

// ensure sizes the textures for a frame of fw x fh pixels.
func (g *giHalfResPass) ensure(fw, fh int32) {
	w, h := (fw+1)/2, (fh+1)/2
	if g.irr != 0 && g.w == w && g.h == h {
		return
	}
	g.release()
	g.w, g.h = w, h
	g.irr = newGITexture2D(gl.RGBA16F, int(w), int(h), gl.NEAREST)
	g.guide = newGITexture2D(gl.RGBA32F, int(w), int(h), gl.NEAREST)
}

func (g *giHalfResPass) release() {
	for _, t := range []*uint32{&g.irr, &g.guide} {
		if *t != 0 {
			gl.DeleteTextures(1, t)
			*t = 0
		}
	}
	g.w, g.h = 0, 0
}

// giHalfResRun works the probes' light out at half resolution for the frame
// being drawn, from scene.depth: called by the material pass before it
// shades, when the depth it shades from is final. A frame without live GI,
// or with the switch off, leaves it off and the material pass samples the
// probes itself.
func giHalfResRun() {
	g := &giHalf
	g.active = false
	if !GIHalfRes || !gi.active || !sceneFrameActive() || scene.depth == 0 || !g.ready() {
		return
	}
	ProfileBegin("gi: half res")
	defer ProfileEnd()
	g.ensure(scene.w, scene.h)
	g.offset = [2]int32{}
	if taaActive() {
		g.offset = giHalfResOffsets[taa.frame%4]
	}
	p := &g.prog
	gi.uploadProbeUniformsTo(p)
	invVP := gfx.Projection.Mul4(gfx.Cam.Matrix).Inv()
	gl.UniformMatrix4fv(p.U("invViewProj"), 1, false, &invVP[0])
	gl.Uniform2i(p.U("fullSize"), scene.w, scene.h)
	gl.Uniform2i(p.U("halfSize"), g.w, g.h)
	gl.Uniform2i(p.U("offset"), g.offset[0], g.offset[1])
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, scene.depth)
	gl.Uniform1i(p.U("sceneDepth"), 0)
	gl.BindImageTexture(0, g.irr, 0, false, 0, gl.WRITE_ONLY, gl.RGBA16F)
	gl.BindImageTexture(1, g.guide, 0, false, 0, gl.WRITE_ONLY, gl.RGBA32F)
	gl.DispatchCompute(uint32((g.w+7)/8), uint32((g.h+7)/8), 1)
	gl.MemoryBarrier(gl.TEXTURE_FETCH_BARRIER_BIT)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	gl.UseProgram(gfx.Program)
	g.active = true
}

// giHalfResBindTo tells the material program prog whether this frame has
// the half-resolution light, and binds it. Called with the program's other
// frame uniforms, after giHalfResRun.
func giHalfResBindTo(prog uint32) {
	g := &giHalf
	loc := func(n string) int32 { return world.ProgUniformLoc(prog, n+"\x00") }
	gl.UseProgram(prog)
	irr, guide := giDummyTexture(), giDummyTexture()
	if g.active {
		irr, guide = g.irr, g.guide
	}
	gl.Uniform1i(loc("giHalfResOn"), boolToInt32(g.active))
	gl.Uniform2i(loc("giHalfResOffset"), g.offset[0], g.offset[1])
	gl.ActiveTexture(gl.TEXTURE0 + giHalfResIrrUnit)
	gl.BindTexture(gl.TEXTURE_2D, irr)
	gl.Uniform1i(loc("giHalfResIrr"), giHalfResIrrUnit)
	gl.ActiveTexture(gl.TEXTURE0 + giHalfResGuideUnit)
	gl.BindTexture(gl.TEXTURE_2D, guide)
	gl.Uniform1i(loc("giHalfResGuide"), giHalfResGuideUnit)
	gl.ActiveTexture(gl.TEXTURE0)
}
