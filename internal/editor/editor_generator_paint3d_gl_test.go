package editor

// editor_generator_paint3d_gl_test.go — the 3D paint overlay against a real
// context: a painted tile, draped over a stubbed ground plane, draws its
// density tint where the stroke is and nothing elsewhere, with no GL error.
//
//	FOUNDRY_GL_TEST=1 go test -run TestGeneratorPaint3DGL ./internal/editor/

import (
	"testing"

	"foundry/internal/glctx"
	"foundry/internal/gltest"
	"foundry/internal/procgen/paint"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

func TestGeneratorPaint3DGLOverlayDraws(t *testing.T) {
	gltest.Context(t)
	liveTestSetup(t)
	// Real textures for this one (liveTestSetup's window stubs them).
	genUploadTexture, genDeleteTextures = genRealUpload, genRealDelete
	t.Cleanup(func() { genPaint3DDrop() })
	genPaint3DOverlay.prog, genPaint3DOverlay.failed = 0, false
	genPaint3DOverlay.lvl, genPaint3DOverlay.layer = nil, ""
	// A full-strength disc in the middle of tile (0, 0).
	genDoc.PaintLayer("lush").Apply(paint.Stroke{X: 32, Z: 32, Radius: 10, Strength: 1})
	genPaint3D.hover, genPaint3D.hoverValid = mgl32.Vec3{32, 2, 32}, true

	const size = 96
	target, err := glctx.NewTarget(size, size)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Delete()
	target.Bind()
	gl.Viewport(0, 0, size, size)
	gl.ClearColor(0, 0, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	gl.Enable(gl.DEPTH_TEST)
	for gl.GetError() != gl.NO_ERROR {
	}
	// Straight down over the tile's middle, the tile filling the view.
	proj := mgl32.Perspective(mgl32.DegToRad(60), 1, 1, 500)
	view := mgl32.LookAtV(mgl32.Vec3{32, 60, 32.01}, mgl32.Vec3{32, 2, 32}, mgl32.Vec3{0, 0, -1})
	drawGenPaintOverlay(proj.Mul4(view))
	img := target.Image()
	target.Unbind()
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Errorf("GL error 0x%x after the overlay", e)
	}
	if genPaint3DOverlay.failed || genPaint3DOverlay.prog == 0 {
		t.Fatal("the overlay program did not build")
	}
	c := img.NRGBAAt(size/2, size/2)
	if !(c.G > 60 && c.G > c.R+20 && c.G > c.B+20) {
		t.Fatalf("the centre pixel %v is not the density tint", c)
	}
	e := img.NRGBAAt(2, 2)
	if e.R+e.G+e.B > 10 {
		t.Errorf("the corner pixel %v, 26 m from the stroke, was drawn on", e)
	}
}

// genRealUpload and genRealDelete are the GL uploads the window tests stub.
var genRealUpload, genRealDelete = genUploadTexture, genDeleteTextures
