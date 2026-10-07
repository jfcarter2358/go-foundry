package render

// scatter_bake_gl_test.go — a patched height texture holds what a whole
// upload of the patched data would.
//
//	FOUNDRY_GL_TEST=1 go test -run TestScatterHeightPatch ./internal/render/

import (
	"math"
	"testing"

	"foundry/internal/glext"
	"foundry/internal/gltest"

	"github.com/go-gl/gl/v4.3-core/gl"
)

func TestScatterHeightPatchUploadsTheRect(t *testing.T) {
	gltest.Context(t)
	const w, h = 37, 29
	data := make([]float32, w*h*2)
	for i := range data {
		data[i] = float32(i) * 0.25
	}
	var tex uint32
	gl.GenTextures(1, &tex)
	defer gl.DeleteTextures(1, &tex)
	uploadRG32F(tex, w, h, data, true)

	// A rect off every edge, as an edit in the middle of a field makes.
	x0, y0, x1, y1 := 5, 7, 23, 19
	rect := make([]float32, (x1-x0)*(y1-y0)*2)
	for i := range rect {
		rect[i] = -1 - float32(i)
	}
	uploadRG32FRect(tex, x0, y0, x1-x0, y1-y0, rect)
	for y := y0; y < y1; y++ {
		copy(data[(y*w+x0)*2:(y*w+x1)*2], rect[(y-y0)*(x1-x0)*2:(y-y0+1)*(x1-x0)*2])
	}

	got := make([]float32, w*h*2)
	gl.BindTexture(gl.TEXTURE_2D, tex)
	glext.GetTexImage(gl.TEXTURE_2D, 0, gl.RG, gl.FLOAT, gl.Ptr(got))
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Fatalf("GL error 0x%x", e)
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(data[i]) {
			t.Fatalf("texel (%d,%d) channel %d: %v, want %v", (i/2)%w, (i/2)/w, i%2, got[i], data[i])
		}
	}
}
