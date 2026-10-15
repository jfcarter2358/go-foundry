package render

// taa_nan_gl_test.go — TAA's resolve keeps a NaN pixel a pixel
// (resources/shaders/taa_resolve_frag.glsl, "NOT A NUMBER";
// docs/regions/PRESETS_4.md, bug 1).
//
// The resolve reads each pixel's 3x3 neighbourhood for its clamp box, and
// writes into an RGBA16F history it reads back next frame. A NaN in the
// frame used to poison every neighbour's box, and the history kept it, so
// it grew a pixel each way every frame: the white blocks over the biome
// showcase's scanned scenery from 600 m up, from a few NaN-lit triangles.
//
// A 32² frame, mid grey, with ONE NaN pixel in the middle (and one +Inf),
// resolved for twelve frames with the history ping-ponging as sceneFrameEnd
// does: no pixel of the history may end up non-finite.
//
//	FOUNDRY_GL_TEST=1 go test -timeout 600s -run TAAResolveKeepsANaN ./internal/render/

import (
	"math"
	"testing"

	"foundry/internal/gltest"

	"github.com/go-gl/gl/v4.3-core/gl"
)

func TestTAAResolveKeepsANaNAPixel(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)
	prev := taa
	t.Cleanup(func() { taa = prev })
	taa = taaState{}
	if !taa.ready() {
		t.Fatal("the TAA programs did not build")
	}
	const n = 32
	frame := make([]float32, n*n*4)
	for i := range frame {
		frame[i] = 0.5
	}
	nan, inf := float32(math.NaN()), float32(math.Inf(1))
	for c := 0; c < 3; c++ {
		frame[(16*n+16)*4+c] = nan
		frame[(8*n+24)*4+c] = inf
	}
	var cur uint32
	gl.GenTextures(1, &cur)
	gl.BindTexture(gl.TEXTURE_2D, cur)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA16F, n, n, 0, gl.RGBA, gl.FLOAT, gl.Ptr(frame))
	setNearestClamp()
	motion, motionFBO := colorTarget(gl.RG16F, gl.RG, n, n)
	gl.BindFramebuffer(gl.FRAMEBUFFER, motionFBO)
	gl.ClearColor(0, 0, 0, 0)
	gl.Clear(gl.COLOR_BUFFER_BIT)
	var hist, histFBO [2]uint32
	for i := range hist {
		hist[i], histFBO[i] = colorTarget(gl.RGBA16F, gl.RGBA, n, n)
	}
	t.Cleanup(func() {
		gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
		gl.DeleteTextures(1, &cur)
		gl.DeleteTextures(1, &motion)
		gl.DeleteFramebuffers(1, &motionFBO)
		for i := range hist {
			gl.DeleteTextures(1, &hist[i])
			gl.DeleteFramebuffers(1, &histFBO[i])
		}
	})

	r := &taa.resolve
	gl.Disable(gl.DEPTH_TEST)
	gl.Disable(gl.BLEND)
	gl.BindVertexArray(taa.vao)
	gl.UseProgram(r.prog)
	cur0 := 0
	for f := 0; f < 12; f++ {
		next := 1 - cur0
		gl.BindFramebuffer(gl.FRAMEBUFFER, histFBO[next])
		gl.Viewport(0, 0, n, n)
		for unit, tex := range []uint32{cur, hist[cur0], motion} {
			gl.ActiveTexture(gl.TEXTURE0 + uint32(unit))
			gl.BindTexture(gl.TEXTURE_2D, tex)
		}
		gl.Uniform1i(r.U("current"), 0)
		gl.Uniform1i(r.U("history"), 1)
		gl.Uniform1i(r.U("motion"), 2)
		gl.Uniform1i(r.U("historyValid"), boolToInt32(f > 0))
		gl.Uniform2f(r.U("size"), n, n)
		gl.Uniform1f(r.U("blendCurrent"), taaBlendCurrent)
		gl.DrawArrays(gl.TRIANGLES, 0, 3)
		cur0 = next
	}
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindVertexArray(0)
	gl.Enable(gl.DEPTH_TEST)

	out := make([]float32, n*n*4)
	gl.BindFramebuffer(gl.FRAMEBUFFER, histFBO[cur0])
	gl.ReadPixels(0, 0, n, n, gl.RGBA, gl.FLOAT, gl.Ptr(out))
	bad := 0
	for i := 0; i < n*n; i++ {
		for c := 0; c < 3; c++ {
			v := float64(out[i*4+c])
			if math.IsNaN(v) || math.IsInf(v, 0) {
				bad++
				break
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d of %d history pixels are NaN or infinite after 12 frames from one NaN and one Inf pixel", bad, n*n)
	}
	// Away from the two, the grey is untouched.
	if v := out[(2*n+2)*4]; math.Abs(float64(v)-0.5) > 0.01 {
		t.Errorf("a far pixel resolved to %.3f, want 0.5", v)
	}
}
