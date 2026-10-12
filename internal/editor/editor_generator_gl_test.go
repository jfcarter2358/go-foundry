package editor

// editor_generator_gl_test.go — the Generator editor's GL halves, against a
// real context: a preview's image layer becomes a texture of its size, and
// its mesh draws through the preview copy of the world program into pixels.
//
//	FOUNDRY_GL_TEST=1 go test -run TestGeneratorGL ./internal/editor/
//
// The draw test is the one that catches what no imgui test can: a preview
// mesh wound inside out, a program that failed to link (the 3D view would
// silently show nothing), or a shadow sampler left unbound (every draw
// GL_INVALID_OPERATION, also nothing).

import (
	"context"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/glctx"
	"foundry/internal/gltest"
	"foundry/internal/procgen"
	"foundry/internal/render"
	"foundry/internal/testenv"
	"foundry/internal/uiwin"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

func generatorGLSetup(t *testing.T) {
	t.Helper()
	gltest.Context(t)
	withGeneratorState(t)
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	sh := func(n string) string { return fileutil.ShaderPath(n) }
	if err := render.InitShadows(sh("shadow_vert.glsl"), sh("shadow_frag.glsl"),
		sh("shadow_cube_vert.glsl"), sh("shadow_cube_frag.glsl")); err != nil {
		t.Fatalf("shadow programs: %v", err)
	}
	savedV3 := genV3
	t.Cleanup(func() {
		genFreeView3D()
		genV3 = savedV3
	})
	genV3.prog, genV3.failed, genV3.framed, genV3.texture, genV3.builtTx = 0, false, false, "", ""
}

func TestGeneratorGLLayerTextureUpload(t *testing.T) {
	generatorGLSetup(t)
	rgba := make([]uint8, 7*5*4)
	for i := range rgba {
		rgba[i] = uint8(i)
	}
	tex := genUploadTexture(7, 5, rgba)
	if tex == 0 || !gl.IsTexture(tex) {
		t.Fatalf("upload made no texture (%d)", tex)
	}
	defer genDeleteTextures([]uint32{tex})
	var w, h int32
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.GetTexLevelParameteriv(gl.TEXTURE_2D, 0, gl.TEXTURE_WIDTH, &w)
	gl.GetTexLevelParameteriv(gl.TEXTURE_2D, 0, gl.TEXTURE_HEIGHT, &h)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	if w != 7 || h != 5 {
		t.Errorf("texture is %dx%d, want 7x5 (an odd width needs UNPACK_ALIGNMENT 1)", w, h)
	}
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Errorf("GL error 0x%x after the upload", e)
	}
}

func TestGeneratorGLPreviewMeshDraws(t *testing.T) {
	generatorGLSetup(t)
	d, err := procgen.NewFromTemplate("noise_demo", "gl")
	if err != nil {
		t.Fatal(err)
	}
	d.Stages[0].Params["size"] = procgen.Float(64)
	d.Stages[0].Params["resolution"] = procgen.Int(32)
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
	if len(genV3.meshes) != 1 {
		t.Fatalf("%d preview meshes built, want 1", len(genV3.meshes))
	}
	// gfx.Geometry.Load leaves a GL_INVALID_VALUE behind in this context for
	// any mesh, one triangle included — the engine's, not this view's (it is
	// the same upload the material editor's swatch makes). Cleared so the
	// check below is about the draw.
	for gl.GetError() != gl.NO_ERROR {
	}
	// Framed on the field, looking down at it.
	genView3DFrame()
	genV3.cam.Pitch = mgl32.DegToRad(-60)
	genV3.grid = false

	const size = 96
	target, err := glctx.NewTarget(size, size)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Delete()
	target.Bind()
	gl.Viewport(0, 0, size, size)
	gl.ClearColor(0, 1, 0, 1) // a colour the normal-coloured surface is not
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	genDrawView3D(&uiwin.UIWindow{W: size, H: size})
	img := target.Image()
	target.Unbind()
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Errorf("GL error 0x%x after the draw", e)
	}
	c := img.NRGBAAt(size/2, size/2)
	if c.R == 0 && c.G == 255 && c.B == 0 {
		t.Fatalf("the centre pixel is the clear colour: the preview mesh drew nothing")
	}
	covered := 0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if p := img.NRGBAAt(x, y); !(p.R == 0 && p.G == 255 && p.B == 0) {
				covered++
			}
		}
	}
	if covered < size*size/4 {
		t.Errorf("the framed field covers %d of %d pixels", covered, size*size)
	}
}
