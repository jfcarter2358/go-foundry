package render

// unlit_gl_test.go — an unlit surface comes out at its albedo in the dark.
//
//	FOUNDRY_GL_TEST=1 go test -run TestUnlit ./internal/render/

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// In a scene lit at a tenth, a lit face is dim and an unlit one is not. The
// untextured normal colour stands in for the albedo, so no texture is needed:
// facing the camera it is (0.5, 0.5, 1).
func TestUnlitSurfaceIgnoresTheLight(t *testing.T) {
	p := newNormalProbe(t)
	var ls world.LightSet
	ls.Global.Color = mgl32.Vec3{1, 1, 1}
	ls.Global.Brightness = 0.1
	world.UploadLightsTo(gfx.Program, ls, false)

	lit := p.shadeQuadWith(gfx.Geometry{Alpha: 1}, "", mgl32.Ident4())
	nearlyByte(t, "lit at a tenth, blue", lit[2], 0.1)

	unlit := p.shadeQuadWith(gfx.Geometry{Alpha: 1, Unlit: true}, "", mgl32.Ident4())
	nearlyByte(t, "unlit, red", unlit[0], 0.5)
	nearlyByte(t, "unlit, green", unlit[1], 0.5)
	nearlyByte(t, "unlit, blue", unlit[2], 1.0)

	// And it does not stick: the next lit draw is lit again.
	again := p.shadeQuadWith(gfx.Geometry{Alpha: 1}, "", mgl32.Ident4())
	nearlyByte(t, "lit after unlit, blue", again[2], 0.1)
}
