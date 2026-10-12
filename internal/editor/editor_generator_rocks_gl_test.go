package editor

// editor_generator_rocks_gl_test.go — a rock variant's preview draws in the
// 3D view with its baked vertex colours, against a real context:
//
//	FOUNDRY_GL_TEST=1 go test -run TestGeneratorGLRock ./internal/editor/
//
// An untextured mesh with colours is the colour (white underneath), not the
// engine's normal-colour look: a red sandstone rock is warm, red over blue,
// where its normal colours would be every hue.

import (
	"context"
	"testing"

	"foundry/internal/glctx"
	"foundry/internal/procgen"
	"foundry/internal/uiwin"

	"github.com/go-gl/gl/v4.3-core/gl"
)

func TestGeneratorGLRockPreviewDrawsItsColours(t *testing.T) {
	generatorGLSetup(t)
	d := rocksEditorDoc()
	genSetDoc(d, "")
	p, err := procgen.Preview(context.Background(), d, "boulders", procgen.NewPreviewCache(2), procgen.PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	genPreview.data, genPreview.dataStage = p, "boulders"
	t.Cleanup(func() { genPreview.data, genPreview.dataStage = nil, "" })
	if !genEnsureView3D(p) {
		t.Fatal("the preview program did not compile")
	}
	if len(genV3.meshes) != 1 || !genV3.turntable || genV3.meshes[0].Cbo == 0 {
		t.Fatalf("%d meshes, turntable %v, colour buffer %d", len(genV3.meshes), genV3.turntable, genV3.meshes[0].Cbo)
	}
	for gl.GetError() != gl.NO_ERROR {
	}
	genV3.grid, genV3.spin = false, false
	const size = 96
	target, err := glctx.NewTarget(size, size)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Delete()
	target.Bind()
	gl.Viewport(0, 0, size, size)
	gl.ClearColor(0, 1, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	genDrawView3D(&uiwin.UIWindow{W: size, H: size})
	img := target.Image()
	target.Unbind()
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Errorf("GL error 0x%x after the draw", e)
	}
	covered, warm := 0, 0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			c := img.NRGBAAt(x, y)
			if c.R == 0 && c.G == 255 && c.B == 0 {
				continue
			}
			covered++
			if int(c.R) > int(c.B)+25 && c.R >= c.G {
				warm++
			}
		}
	}
	if covered < size*size/10 {
		t.Fatalf("the rock covers %d of %d pixels", covered, size*size)
	}
	if warm < covered*9/10 {
		t.Errorf("only %d of %d rock pixels are the sandstone's warm colour (normal colours?)", warm, covered)
	}
}
