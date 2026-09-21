package render

// live_shadow_gl_test.go — live shadows on baked surfaces, on a GPU.
//
// A baked surface carries the baked sun's share of its light (the 4th float of
// its baked light, compile.Face.SunShare), and fragment.glsl takes that much of
// the sun back off wherever the LIVE-ONLY cascades (LiveCascadeArray) say
// something shadows it. Wherever the full cascades say so is not enough: they
// hold the baked world too. So the two maps are filled with opposite answers
// and the pixels read.
//
//	FOUNDRY_GL_TEST=1 go test -run TestALiveShadow ./internal/render/

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// fillCascades clears every layer of a cascade array to depth.
func fillCascades(tex uint32, layers int, depth float64) {
	var prev int32
	gl.GetIntegerv(gl.FRAMEBUFFER_BINDING, &prev)
	gl.BindFramebuffer(gl.FRAMEBUFFER, Shadows.CascadeFBO)
	gl.ClearDepth(depth)
	for l := 0; l < layers; l++ {
		gl.FramebufferTextureLayer(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT, tex, 0, int32(l))
		gl.Clear(gl.DEPTH_BUFFER_BIT)
	}
	gl.ClearDepth(1)
	gl.BindFramebuffer(gl.FRAMEBUFFER, uint32(prev))
}

func TestALiveShadowTakesTheBakedSunOffABakedSurface(t *testing.T) {
	p := newNormalProbe(t)

	// Straight into the quad, so N.L = 1 and the sun's unshadowed term is its
	// brightness. No ambient: a baked surface's light is its bake alone.
	sun := world.DirectionalLight{
		Direction: mgl32.Vec3{0, 0, -1}, Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.5,
		CastShadows: true, ShadowStrength: 1,
	}
	ls := world.LightSet{Directional: []world.DirectionalLight{sun}}
	world.UploadLightsTo(gfx.Program, ls, false)

	// The full cascades say "lit" everywhere and the live-only ones "shadowed",
	// through an identity light matrix that puts the quad at depth 0.5.
	EnsureCascadeArray(ShadowCascades)
	ensureLiveCascadeArray(true)
	defer ensureLiveCascadeArray(false)
	fillCascades(Shadows.CascadeArray, ShadowCascades, 1)
	fillCascades(Shadows.LiveCascadeArray, ShadowCascades, 0)
	frame := newShadowFrame(1, 0.1)
	frame.Lights[0].BaseLayer = 0

	const baked, albedoRed = 0.6, 0.5
	upload := func(live bool) {
		frame.LiveDrawn, frame.LiveSun = live, 0
		UploadShadowUniformsTo(gfx.Program, ls, frame)
		gl.ActiveTexture(gl.TEXTURE0)
	}

	upload(true)
	nearlyByte(t, "a baked surface keeping the whole sun, under a live shadow",
		uint8(p.bakedQuadRed([]float32{baked, baked, baked, 1})*255), albedoRed*(baked-0.5))
	nearlyByte(t, "a baked surface the bake already shadowed, under a live shadow",
		uint8(p.bakedQuadRed([]float32{baked, baked, baked, 0})*255), albedoRed*baked)

	upload(false)
	nearlyByte(t, "a baked surface with no live cascades drawn",
		uint8(p.bakedQuadRed([]float32{baked, baked, baked, 1})*255), albedoRed*baked)
}
