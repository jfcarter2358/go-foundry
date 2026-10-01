package render

// live_shadow_gl_test.go — a baked surface's sun, re-shadowed by the cascades,
// on a GPU.
//
// A baked surface carries the baked sun's share of its light (the 4th float of
// its baked light, compile.Face.SunShare). Wherever the cascades reach,
// fragment.glsl takes that share back off and puts the sun back shadowed by
// the cascades instead — the bake's shadow is a per-vertex ramp, theirs a
// per-pixel edge. Past their reach the bake's answer stands. So the bake and
// the cascades are given every pair of opposite answers and the pixels read.
//
//	FOUNDRY_GL_TEST=1 go test -run TestABakedSurface ./internal/render/

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
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

func TestABakedSurfaceTakesItsSunShadowFromTheCascades(t *testing.T) {
	p := newNormalProbe(t)

	// Straight into the quad, so N.L = 1 and the sun's unshadowed term is its
	// brightness. No ambient: a baked surface's light is its bake alone.
	sun := world.DirectionalLight{
		Direction: mgl32.Vec3{0, 0, -1}, Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.3,
		CastShadows: true, ShadowStrength: 1,
	}
	ls := world.LightSet{Directional: []world.DirectionalLight{sun}}
	world.UploadLightsTo(gfx.Program, ls, false)

	// An identity light matrix puts the quad at depth 0.5: a cascade cleared
	// to 1 says "lit", one cleared to 0 "shadowed".
	EnsureCascadeArray(ShadowCascades)
	frame := newShadowFrame(1, 0.1)
	frame.Lights[0].BaseLayer = 0
	upload := func(cascades float64, baked bool, splits [ShadowCascades]float32) {
		fillCascades(Shadows.CascadeArray, ShadowCascades, cascades)
		f := frame
		f.Splits = splits
		f.BakedSunCast, f.BakedSun = baked, 0
		UploadShadowUniformsTo(gfx.Program, ls, f)
		gl.ActiveTexture(gl.TEXTURE0)
	}
	near := frame.Splits
	// Sums kept under 1, where the shader clamps.
	const baked, albedoRed, sunTerm = 0.6, 0.5, 0.3
	quad := func(share float32) float32 {
		return p.bakedQuadRed([]float32{baked, baked, baked, share})
	}
	check := func(what string, got float32, light float32) {
		t.Helper()
		nearlyByte(t, what, uint8(got*255), albedoRed*light)
	}

	upload(0, true, near)
	check("the bake kept the sun, the cascades shadow it: it comes off", quad(1), baked-sunTerm)
	check("the bake and the cascades both shadow it", quad(0), baked)

	upload(1, true, near)
	check("the bake shadowed the sun, the cascades light it: it goes on", quad(0), baked+sunTerm)
	check("the bake and the cascades both light it", quad(1), baked)
	check("the sun was never baked here (bake_exclude): left alone", quad(-1), baked)

	// Every split nearer than the quad: past the cascades' reach, where the
	// bake's shadow is the only one there is.
	upload(1, true, [ShadowCascades]float32{0.001, 0.002, 0.003})
	check("past the cascades the bake's shadow stands", quad(0), baked)

	upload(1, false, near)
	check("a level with no baked sun", quad(0), baked)
}
