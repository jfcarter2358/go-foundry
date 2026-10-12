package render

// normal_map_bc5_gl_test.go — a BC5 normal map shades the same as the RGB
// one it was made from.
//
// BC5 stores only a normal's x and y, so fragment.glsl rebuilds z when
// BindNormalMap passes hasNormalMap = 2 (gfx/texture_compress.go). Neither
// half can be checked alone: the flag can be set with the shader branch
// missing (the blue channel then reads 0, z = -1, and the surface lights as
// if it faced away) or the branch can be there with the flag never set. So
// the same tilted normal map is drawn twice through the real world program —
// once uploaded RGB, once BC5 — under a light that the tilt matters to, and
// the two pixels have to agree, and differ from the unmapped surface.
//
//	FOUNDRY_GL_TEST=1 go test -run TestBC5NormalMap ./internal/render/

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/texcompress"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func writeSolidPNG(t *testing.T, path string, w, h int, c [4]uint8) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:], c[:])
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBC5NormalMapShadesLikeTheRGBOne(t *testing.T) {
	p := newNormalProbe(t)

	// A light the tilt matters to: dim ambient, a strong sun from the side.
	var ls world.LightSet
	ls.Global.Color = mgl32.Vec3{1, 1, 1}
	ls.Global.Brightness = 0.05
	ls.Directional = []world.DirectionalLight{{
		Direction:  mgl32.Vec3{-0.6, 0, -0.8}.Normalize(),
		Color:      mgl32.Vec3{1, 1, 1},
		Brightness: 0.5,
	}}
	world.UploadLightsTo(gfx.Program, ls, false)

	// Textures from a scratch directory (the shaders were read already).
	prevRes, prevC := fileutil.ResourcesPath, gfx.TextureCompression
	fileutil.ResourcesPath = t.TempDir()
	t.Setenv(texcompress.CacheDirEnv, t.TempDir())
	t.Cleanup(func() { fileutil.ResourcesPath, gfx.TextureCompression = prevRes, prevC })
	dir := fileutil.TexturesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	grey := [4]uint8{128, 128, 128, 255}
	// n = (0.6, 0, 0.8), encoded n * 0.5 + 0.5.
	tilted := [4]uint8{204, 128, 230, 255}
	for _, name := range []string{"bump_rgb", "bump_bc5", "bump_flat"} {
		writeSolidPNG(t, filepath.Join(dir, name+".png"), 256, 256, grey)
		if name != "bump_flat" {
			writeSolidPNG(t, filepath.Join(dir, name+"_normal.png"), 256, 256, tilted)
		}
	}
	gfx.TextureNames = gfx.ScanTextures(dir)

	load := func(name string, compress bool) {
		gfx.TextureCompression = compress
		if err := gfx.EnsureTextureLoaded(name); err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
	}
	load("bump_rgb", false)
	load("bump_bc5", true)
	load("bump_flat", false)
	if f := gfx.TextureFormatOf(gfx.NormalMapName("bump_bc5")); f != gfx.TextureBC5 {
		t.Fatalf("the compressed normal map uploaded as %v", f)
	}
	if f := gfx.TextureFormatOf(gfx.NormalMapName("bump_rgb")); f != gfx.TextureRGBA8 {
		t.Fatalf("the plain normal map uploaded as %v", f)
	}

	rgb := p.shadeQuad("bump_rgb", mgl32.Ident4())
	bc5 := p.shadeQuad("bump_bc5", mgl32.Ident4())
	flat := p.shadeQuad("bump_flat", mgl32.Ident4())
	t.Logf("rgb %v  bc5 %v  flat %v", rgb, bc5, flat)
	for c := 0; c < 3; c++ {
		if d := int(rgb[c]) - int(bc5[c]); d < -4 || d > 4 {
			t.Errorf("channel %d: BC5 %d, RGB %d — the rebuilt z does not match", c, bc5[c], rgb[c])
		}
	}
	if d := int(rgb[0]) - int(flat[0]); d > -8 && d < 8 {
		t.Errorf("the tilted map shades %v, the flat surface %v: the light does not test the normal", rgb, flat)
	}
}
