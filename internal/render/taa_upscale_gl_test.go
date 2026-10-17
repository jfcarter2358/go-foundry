package render

// taa_upscale_gl_test.go — the upsampling resolve (taa_resolve_frag.glsl's
// resolveUpscaled; render/dynres.go; docs/adventure/PHASE_F.md, Phase F2).
//
// A 16² frame resolved into a 32² history, over the jitter sequence a 0.5
// render scale uses: a flat grey stays that grey at every output pixel (no
// pixel left without a sample), and a vertical step edge at the frame's
// middle lands at the output's middle — the reconstruction places each
// sample where the jitter put it, not shifted by half a render pixel.
//
//	FOUNDRY_GL_TEST=1 go test -timeout 600s -run TAAUpscale ./internal/render/

import (
	"math"
	"testing"

	"foundry/internal/gltest"

	"github.com/go-gl/gl/v4.3-core/gl"
)

func TestTAAUpscaleResolveReconstructsTheOutput(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)
	prev := taa
	t.Cleanup(func() { taa = prev })
	taa = taaState{}
	if !taa.ready() {
		t.Fatal("the TAA programs did not build")
	}
	const rn, n = 16, 32
	run := func(name string, at func(x, y float64) float32) []float32 {
		var cur uint32
		gl.GenTextures(1, &cur)
		motion, motionFBO := colorTarget(gl.RG16F, gl.RG, rn, rn)
		gl.BindFramebuffer(gl.FRAMEBUFFER, motionFBO)
		gl.ClearColor(0, 0, 0, 0)
		gl.Clear(gl.COLOR_BUFFER_BIT)
		var hist, histFBO [2]uint32
		for i := range hist {
			hist[i], histFBO[i] = colorTarget(gl.RGBA16F, gl.RGBA, n, n)
		}
		defer func() {
			gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
			gl.DeleteTextures(1, &cur)
			gl.DeleteTextures(1, &motion)
			gl.DeleteFramebuffers(1, &motionFBO)
			for i := range hist {
				gl.DeleteTextures(1, &hist[i])
				gl.DeleteFramebuffers(1, &histFBO[i])
			}
		}()
		r := &taa.resolve
		gl.Disable(gl.DEPTH_TEST)
		gl.Disable(gl.BLEND)
		gl.BindVertexArray(taa.vao)
		phases := taaJitterPhases(rn, n)
		cur0 := 0
		frame := make([]float32, rn*rn*4)
		for f := 0; f < 3*phases; f++ {
			j := taaJitterN(uint32(f+1), rn, rn, phases)
			jp := [2]float64{float64(j[0]) * rn * 0.5, float64(j[1]) * rn * 0.5}
			// Render pixel i saw the scene at i + 0.5 - jitter (render px).
			for y := 0; y < rn; y++ {
				for x := 0; x < rn; x++ {
					v := at((float64(x)+0.5-jp[0])/rn, (float64(y)+0.5-jp[1])/rn)
					for c := 0; c < 3; c++ {
						frame[(y*rn+x)*4+c] = v
					}
					frame[(y*rn+x)*4+3] = 1
				}
			}
			gl.BindTexture(gl.TEXTURE_2D, cur)
			gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA16F, rn, rn, 0, gl.RGBA, gl.FLOAT, gl.Ptr(frame))
			setNearestClamp()
			next := 1 - cur0
			gl.BindFramebuffer(gl.FRAMEBUFFER, histFBO[next])
			gl.Viewport(0, 0, n, n)
			gl.UseProgram(r.prog)
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
			gl.Uniform1i(r.U("upscale"), 1)
			gl.Uniform2f(r.U("renderSize"), rn, rn)
			gl.Uniform2f(r.U("jitterPx"), float32(jp[0]), float32(jp[1]))
			gl.DrawArrays(gl.TRIANGLES, 0, 3)
			// Each frame done before the next re-specifies the texture:
			// Mesa's D3D12 driver hangs on a TexImage2D over one a queued
			// draw still reads.
			gl.Finish()
			cur0 = next
		}
		gl.ActiveTexture(gl.TEXTURE0)
		gl.BindVertexArray(0)
		gl.Enable(gl.DEPTH_TEST)
		out := make([]float32, n*n*4)
		gl.BindFramebuffer(gl.FRAMEBUFFER, histFBO[cur0])
		gl.ReadPixels(0, 0, n, n, gl.RGBA, gl.FLOAT, gl.Ptr(out))
		return out
	}

	flat := run("flat", func(x, y float64) float32 { return 0.5 })
	for i := 0; i < n*n; i++ {
		if v := flat[i*4]; math.Abs(float64(v)-0.5) > 0.01 {
			t.Fatalf("flat grey: output pixel (%d, %d) is %.3f, want 0.5", i%n, i/n, v)
		}
	}

	edge := run("edge", func(x, y float64) float32 {
		if x < 0.5 {
			return 0
		}
		return 1
	})
	// Row 16: dark well left of the middle, bright well right of it, and
	// the half-way crossing between output columns 14 and 18.
	row := 16
	at := func(x int) float32 { return edge[(row*n+x)*4] }
	if at(4) > 0.05 || at(27) < 0.95 {
		t.Errorf("edge: far left %.3f, far right %.3f; want 0 and 1", at(4), at(27))
	}
	cross := -1
	for x := 1; x < n; x++ {
		if at(x-1) < 0.5 && at(x) >= 0.5 {
			cross = x
			break
		}
	}
	if cross < 14 || cross > 18 {
		var vals []float32
		for x := 0; x < n; x++ {
			vals = append(vals, at(x))
		}
		t.Errorf("edge: crosses 0.5 at output column %d, want 14..18 (row: %.2f)", cross, vals)
	}
}
