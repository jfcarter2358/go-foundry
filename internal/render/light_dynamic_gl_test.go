package render

// light_dynamic_gl_test.go — static and dynamic lights, on a GPU.
//
// The bake leaving a dynamic light out (compile's TestADynamicLightIsNotBaked)
// is only half of the contract; the other half is fragment.glsl summing that
// light back in live on a baked surface, and NOT summing a static one there a
// second time. Both halves are uniforms looked up by name, so either could be
// silently missing with every program still linking. So a baked quad and an
// unbaked one are drawn under each kind of light and the pixels read.
//
//	FOUNDRY_GL_TEST=1 go test -run TestBakedSurface ./internal/render/

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// bakedQuadRed draws the normal probe's quad — untextured, so its albedo red is
// 0.5 — with every vertex baked to baked (nil for an unbaked quad), and returns
// the centre pixel's red channel as 0..1.
func (p *normalProbe) bakedQuadRed(baked []float32) float32 {
	p.t.Helper()
	const h = 1.5
	verts := [][]float32{{-h, -h, 0, 0, 0}, {h, -h, 0, 1, 0}, {h, h, 0, 1, 1}, {-h, h, 0, 0, 1}}
	var g gfx.Geometry
	g.Alpha = 1
	if baked != nil {
		g.VertexLight = [][]float32{baked, baked, baked, baked}
	}
	if err := g.Load(verts, []int{0, 1, 2, 0, 2, 3}, "", mgl32.Vec3{}); err != nil {
		p.t.Fatalf("upload probe quad: %v", err)
	}
	defer g.Delete()
	if (g.BakedLightFlag() != 0) != (baked != nil) {
		p.t.Fatalf("baked light flag is %d for a quad baked=%v", g.BakedLightFlag(), baked != nil)
	}

	gl.Disable(gl.CULL_FACE)
	gl.ClearColor(0, 1, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	g.DrawWithTransform(mgl32.Ident4())

	var px [4]uint8
	gl.ReadPixels(skyTestSize/2, skyTestSize/2, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return float32(px[0]) / 255
}

func TestBakedSurfaceAddsOnlyDynamicLightsLive(t *testing.T) {
	p := newNormalProbe(t)

	// One unit in front of the quad's centre, facing it squarely: t = 0.9,
	// attenuation 0.81, N.L = 1, so brightness 0.5 contributes 0.405. No
	// ambient, so an unbaked surface's light is that term alone.
	lamp := world.PointLight{
		Position: mgl32.Vec3{0, 0, 1}, Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.5, Radius: 10,
	}
	const baked, lampTerm, albedoRed = 0.2, 0.405, 0.5
	use := func(dynamic bool) {
		lamp.Dynamic = dynamic
		ls := world.LightSet{Points: []world.PointLight{lamp}}
		world.UploadLightsTo(gfx.Program, ls, false)
		UploadPointShadowUniformsTo(gfx.Program, ls)
		gl.ActiveTexture(gl.TEXTURE0)
	}

	use(false)
	nearlyByte(t, "baked surface under a static light (already in the bake)",
		uint8(p.bakedQuadRed([]float32{baked, baked, baked})*255), albedoRed*baked)
	nearlyByte(t, "unbaked surface under a static light (lit live)",
		uint8(p.bakedQuadRed(nil)*255), albedoRed*lampTerm)

	use(true)
	nearlyByte(t, "baked surface under a dynamic light (bake plus the live term)",
		uint8(p.bakedQuadRed([]float32{baked, baked, baked})*255), albedoRed*(baked+lampTerm))
	nearlyByte(t, "unbaked surface under a dynamic light (lit live)",
		uint8(p.bakedQuadRed(nil)*255), albedoRed*lampTerm)
}
