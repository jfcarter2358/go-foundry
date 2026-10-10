package scoredraw

// scoredraw_gl_test.go — the atlas page as a real GL texture.
//
//	FOUNDRY_GL_TEST=1 go test -run GL ./internal/score/scoredraw/

import (
	"testing"

	"github.com/go-gl/gl/v4.3-core/gl"

	"foundry/internal/glext"
	"foundry/internal/gltest"
)

func TestGLAtlasUpload(t *testing.T) {
	gltest.Context(t)
	r := New()
	r.FontSize = func() float32 { return 13 }
	list := demo(t)
	r.Draw(&rec{}, list, View{Scale: 12})
	id := r.Texture(12)
	if id == 0 || !gl.IsTexture(id) {
		t.Fatalf("no texture (%d)", id)
	}
	defer r.Release()
	gl.BindTexture(gl.TEXTURE_2D, id)
	var w, h int32
	gl.GetTexLevelParameteriv(gl.TEXTURE_2D, 0, gl.TEXTURE_WIDTH, &w)
	gl.GetTexLevelParameteriv(gl.TEXTURE_2D, 0, gl.TEXTURE_HEIGHT, &h)
	pix := make([]uint8, w*h*4)
	glext.GetTexImage(gl.TEXTURE_2D, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(pix))
	gl.BindTexture(gl.TEXTURE_2D, 0)
	a := r.atlases.For(12)
	if int(w) != a.W || int(h) != a.H {
		t.Fatalf("texture %dx%d, page %dx%d", w, h, a.W, a.H)
	}
	inked := 0
	for i := 3; i < len(pix); i += 4 {
		if pix[i] > 128 {
			inked++
		}
	}
	if inked < 200 {
		t.Errorf("only %d inked texels on the GPU", inked)
	}
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Errorf("GL error %#x", e)
	}
}
