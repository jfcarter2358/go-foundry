package editor

// editor_generator_terrain_gl_test.go — the terrain preview's tiles draw in
// the 3D view painted with the albedo hint (PreviewMesh.Image), against a
// real context:
//
//	FOUNDRY_GL_TEST=1 go test -run TestGeneratorGLTerrain ./internal/editor/

import (
	"context"
	"testing"

	"foundry/internal/glctx"
	"foundry/internal/procgen"
	"foundry/internal/uiwin"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

func TestGeneratorGLTerrainTilesDrawWithTheirImage(t *testing.T) {
	generatorGLSetup(t)
	d := terrainEditorDoc()
	genSetDoc(d, "")
	p, err := procgen.Preview(context.Background(), d, "ground", procgen.NewPreviewCache(2), procgen.PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	genPreview.data, genPreview.dataStage = p, "ground"
	t.Cleanup(func() { genPreview.data, genPreview.dataStage = nil, "" })
	if !genEnsureView3D(p) {
		t.Fatal("the preview program did not compile")
	}
	if len(genV3.meshes) != 4 || genV3.image != p {
		t.Fatalf("%d meshes, image uploaded: %v — want four tiles painted with the albedo", len(genV3.meshes), genV3.image == p)
	}
	for gl.GetError() != gl.NO_ERROR {
	}
	genView3DFrame()
	genV3.cam.Pitch = mgl32.DegToRad(-70)
	genV3.grid = false
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
	// Red rock and sand: warm pixels, red over blue, across most of the view.
	warm := 0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			c := img.NRGBAAt(x, y)
			if !(c.R == 0 && c.G == 255 && c.B == 0) && int(c.R) > int(c.B)+10 {
				warm++
			}
		}
	}
	if warm < size*size/3 {
		t.Errorf("only %d of %d pixels are the albedo's warm colours", warm, size*size)
	}
}
