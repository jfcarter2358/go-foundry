package render

// surface_fx_noise_gl_test.go — surface_fx.glsl's value noise is continuous
// on the GPU (docs/regions/PRESETS_2B.md, "rectangular patches").
//
// fxHash amplifies its input about two thousand times, so the corner a
// value-noise cell shares with its neighbour must be hashed from the same
// bits on both sides. A driver free to fold (i + 1)·123.34 into an FMA in
// one cell and not the other made the noise jump at cell edges: the
// prairie's macro variation (60 m patches) drew sharp-edged rectangles from
// 40 m up. The `precise` qualifiers forbid that; this draws fxFbm over a
// stretch of world far from the origin, at the macro variation's scale,
// and checks no two neighbouring pixels differ by more than the noise's own
// slope allows.
//
//	FOUNDRY_GL_TEST=1 go test -run 'SurfaceFxNoise' ./internal/render/

import (
	"math"
	"strings"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/gltest"

	"github.com/go-gl/gl/v4.3-core/gl"
)

func TestSurfaceFxNoiseIsContinuous(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)
	src := mustReadShader(biomeShaderPath("surface_fx.glsl"))
	a := strings.Index(src, "float fxHash(")
	b := strings.Index(src, "// ---------- projections")
	if a < 0 || b < a {
		t.Fatal("surface_fx.glsl's noise section moved")
	}
	noise := src[a:b]
	if !strings.Contains(noise, "precise") {
		t.Error("fxHash/fxNoise lost their `precise` qualifiers")
	}
	const size = 512
	vs := "#version 430\nvoid main() { vec2 p = vec2(float((gl_VertexID << 1) & 2), float(gl_VertexID & 2));" +
		" gl_Position = vec4(p * 2.0 - 1.0, 0.0, 1.0); }\n"
	fs := "#version 430\nout vec4 o;\nuniform vec2 origin;\nuniform float step_;\nuniform float scale;\n" + noise +
		"\nvoid main() { vec2 q = origin + gl_FragCoord.xy * step_; o = vec4(fxFbm(q * scale), 0.0, 0.0, 1.0); }\n"
	v, err := gfx.CompileShader(vs, gl.VERTEX_SHADER)
	if err != nil {
		t.Fatal(err)
	}
	f, err := gfx.CompileShader(fs, gl.FRAGMENT_SHADER)
	if err != nil {
		t.Fatal(err)
	}
	prog, err := linkStages(v, f)
	if err != nil {
		t.Fatal(err)
	}
	defer gl.DeleteProgram(prog)
	var tex, fbo, vao uint32
	gl.GenTextures(1, &tex)
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.R32F, size, size, 0, gl.RED, gl.FLOAT, nil)
	gl.GenFramebuffers(1, &fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, tex, 0)
	gl.GenVertexArrays(1, &vao)
	defer func() {
		gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
		gl.DeleteFramebuffers(1, &fbo)
		gl.DeleteTextures(1, &tex)
		gl.DeleteVertexArrays(1, &vao)
	}()
	gl.Viewport(0, 0, size, size)
	gl.Disable(gl.DEPTH_TEST)
	gl.Disable(gl.BLEND)
	gl.UseProgram(prog)
	// A 512 m square of world 200–700 m out, one pixel a metre, at the
	// prairie's macro scale (60 m).
	gl.Uniform2f(gl.GetUniformLocation(prog, gl.Str("origin\x00")), -700, 213)
	gl.Uniform1f(gl.GetUniformLocation(prog, gl.Str("step_\x00")), 1)
	gl.Uniform1f(gl.GetUniformLocation(prog, gl.Str("scale\x00")), 1.0/60)
	gl.BindVertexArray(vao)
	gl.DrawArrays(gl.TRIANGLES, 0, 3)
	px := make([]float32, size*size)
	gl.ReadPixels(0, 0, size, size, gl.RED, gl.FLOAT, gl.Ptr(&px[0]))
	gl.UseProgram(gfx.Program)
	// fxFbm's steepest slope: three octaves of a smoothstepped value noise
	// (slope ≤ 1.5 a cell), at 1, 2.03 and 4.12 cells a unit, weighted
	// ½, ¼, ⅛, over 0.875; one pixel is 1/60 of a unit.
	bound := 1.5 * (0.5 + 0.25*2.03 + 0.125*4.12) / 0.875 / 60 * 1.5
	worst := 0.0
	for y := 0; y < size; y++ {
		for x := 0; x+1 < size; x++ {
			worst = math.Max(worst, math.Abs(float64(px[y*size+x+1]-px[y*size+x])))
			if y+1 < size {
				worst = math.Max(worst, math.Abs(float64(px[(y+1)*size+x]-px[y*size+x])))
			}
		}
	}
	t.Logf("largest step between neighbouring metres %.4f (smooth bound %.4f)", worst, bound)
	if worst > bound {
		t.Errorf("the value noise jumps by %.4f between neighbouring pixels (a smooth one moves at most %.4f): cell edges show", worst, bound)
	}
}
