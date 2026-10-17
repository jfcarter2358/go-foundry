package render

// hdr_gl_test.go — HDR shading's colour round trip (gfx/hdr.go), on a GPU.
//
// In an HDR frame, display-referred colour (the sky, the fog's colours, an
// HDR-unaware pass's output) is carried into the scene's units by
// hdrFromDisplay, and the frame's output pass maps it back through exposure
// and the ACES fit. The two must cancel, or every level that opts in would
// shift its sky and its haze. A probe pushes a colour through applyFog (with
// and without an atmosphere) and the output pass's curve, and reads it back.
//
//	FOUNDRY_GL_TEST=1 go test -run TestHDR ./internal/render/

import (
	"math"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

const hdrProbeFrag = `#version 430
uniform vec3 probePos;
uniform vec3 probeColor;
uniform int  probeCurve;
out vec4 outColor;
void main() {
    vec3 c = applyFog(probeColor, probePos);
    if (probeCurve != 0) {
        // taa_output_frag.glsl's curve.
        vec3 x = max(c * hdrExposure, vec3(0.0));
        c = clamp((x * (2.51 * x + 0.03)) / (x * (2.43 * x + 0.59) + 0.14), 0.0, 1.0);
    }
    outColor = vec4(c, 1.0);
}
`

func hdrProbe(t *testing.T) uint32 {
	t.Helper()
	fogProbeSetup(t)
	vs, err := gfx.CompileShader(fogProbeVert, gl.VERTEX_SHADER)
	if err != nil {
		t.Fatalf("probe vertex: %v", err)
	}
	fs, err := gfx.CompileShader(gfx.MustInjectFog(hdrProbeFrag), gl.FRAGMENT_SHADER)
	if err != nil {
		t.Fatalf("probe fragment: %v", err)
	}
	prog, err := linkStages(vs, fs)
	if err != nil {
		t.Fatalf("probe program: %v", err)
	}
	return prog
}

func hdrShade(prog, vao uint32, f *world.Fog, lvl *world.Level, col, pos mgl32.Vec3, curve bool) mgl32.Vec3 {
	world.UploadFogTo(prog, f, nil, lvl)
	gl.Uniform3f(world.ProgUniformLoc(prog, "probePos\x00"), pos.X(), pos.Y(), pos.Z())
	gl.Uniform3f(world.ProgUniformLoc(prog, "probeColor\x00"), col.X(), col.Y(), col.Z())
	c := int32(0)
	if curve {
		c = 1
	}
	gl.Uniform1i(world.ProgUniformLoc(prog, "probeCurve\x00"), c)
	gl.Disable(gl.DEPTH_TEST)
	gl.ClearColor(0, 1, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT)
	gl.BindVertexArray(vao)
	gl.DrawArrays(gl.TRIANGLES, 0, 3)
	gl.BindVertexArray(0)
	var px [4]uint8
	glext.ReadPixels(skyTestSize/2, skyTestSize/2, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return mgl32.Vec3{float32(px[0]) / 255, float32(px[1]) / 255, float32(px[2]) / 255}
}

// TestHDRDisplayColourSurvivesTheCurve: with a level that asks for HDR in a
// scene-target frame, a screen colour through applyFog and the curve comes
// back as it went in, at two exposures; with no atmosphere and with a far
// point in haze (which must come out the haze's own colour, as the sky's
// horizon does). Without HDR (no block, or not a scene frame) applyFog is
// untouched.
func TestHDRDisplayColourSurvivesTheCurve(t *testing.T) {
	prog := hdrProbe(t)
	vao := newFogProbe(t).vao
	defer func() { gfx.HDRFrame = false }()

	none := world.DefaultFog()
	none.Mode = world.FogNone
	haze := atmosOnly()
	haze.Aerial = world.DefaultFogAerial(mgl32.Vec3{0.8, 0.6, 0.4})
	haze.Aerial.Distance = 10
	haze.Aerial.Tint = mgl32.Vec3{1, 1, 1}
	haze.Aerial.ScaleHeight = 1e6
	cols := []mgl32.Vec3{{0.1, 0.2, 0.3}, {0.5, 0.5, 0.5}, {0.85, 0.7, 0.55}}
	near := mgl32.Vec3{0, 0, 1}

	for _, exp := range []float32{1, 1.6} {
		lvl := &world.Level{Tonemap: &world.Tonemap{Enabled: true, Exposure: exp, Curve: world.TonemapACES}}
		gfx.HDRFrame = true
		for _, c := range cols {
			nearlyRGB(t, "display colour through the curve", hdrShade(prog, vao, &none, lvl, c, near, true), c)
		}
		// Deep in the haze, the haze's own colour, whatever the surface.
		nearlyRGB(t, "far in the haze", hdrShade(prog, vao, &haze, lvl, mgl32.Vec3{0.1, 0.9, 0.1}, mgl32.Vec3{0, 0, 2000}, true),
			mgl32.Vec3{0.8, 0.6, 0.4})
		// And the carried value really is in scene units: brighter than
		// the display value for a bright colour at exposure 1.
		if exp == 1 {
			got := hdrShade(prog, vao, &none, lvl, mgl32.Vec3{0.6, 0.6, 0.6}, near, false)
			want := float32(gfx.ACESFilmInverse(0.6))
			if math.Abs(float64(got[0]-min(want, 1))) > 0.03 {
				t.Errorf("carried 0.6: got %v, want %.3f (the curve's inverse, clamped by the 8-bit target)", got, want)
			}
		}
	}

	// Not a scene frame, or no block: nothing changes.
	lvl := &world.Level{Tonemap: &world.Tonemap{Enabled: true, Exposure: 1}}
	gfx.HDRFrame = false
	for _, c := range cols {
		nearlyRGB(t, "HDR level outside a scene frame", hdrShade(prog, vao, &none, lvl, c, near, false), c)
	}
	gfx.HDRFrame = true
	for _, c := range cols {
		nearlyRGB(t, "scene frame, LDR level", hdrShade(prog, vao, &none, &world.Level{}, c, near, false), c)
	}
}
