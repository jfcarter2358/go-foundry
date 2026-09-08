package gfx_test

// texture_normal_gl_test.go — opt-in check that a texture picks its normal map
// up off disk on the strength of the filename alone.
//
// This is the seam the whole feature hangs from and the one Go cannot see: the
// companion is found by ensureNormalMapLoaded inside LoadTexture, which needs a
// real context to upload anything at all. Everything downstream — the
// hasNormalMap uniform, the binding, the shading — is a no-op if this does not
// fire.
//
//	FOUNDRY_GL_TEST=1 go test -run TestNormalMap ./internal/gfx/

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/gltest"
)

// writeTestPNG writes a 2x2 solid-colour PNG, which is all either half of this
// needs to be.
func writeTestPNG(t *testing.T, path string, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for i := 0; i < 4; i++ {
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = c.R, c.G, c.B, c.A
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestNormalMapLoadsWithItsAlbedo(t *testing.T) {
	gltest.Context(t)

	prev := fileutil.ExecutablePath
	fileutil.ExecutablePath = t.TempDir()
	t.Cleanup(func() { fileutil.ExecutablePath = prev })
	dir := fileutil.TexturesDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	writeTestPNG(t, filepath.Join(dir, "withmap.png"), color.RGBA{200, 100, 50, 255})
	writeTestPNG(t, filepath.Join(dir, "withmap_normal.png"), gfx.FlatNormalRGBA())
	writeTestPNG(t, filepath.Join(dir, "nomap.png"), color.RGBA{10, 20, 30, 255})

	if err := gfx.LoadTexture("withmap", filepath.Join(dir, "withmap.png")); err != nil {
		t.Fatalf("LoadTexture: %v", err)
	}
	if !gfx.HasNormalMap("withmap") {
		t.Error("a texture with <name>_normal.png beside it did not pick it up")
	}
	if _, ok := gfx.Textures[gfx.NormalMapName("withmap")]; !ok {
		t.Error("the normal map is not in the texture registry")
	}

	if err := gfx.LoadTexture("nomap", filepath.Join(dir, "nomap.png")); err != nil {
		t.Fatalf("LoadTexture: %v", err)
	}
	if gfx.HasNormalMap("nomap") {
		t.Error("a texture with no companion file reported a normal map")
	}

	// And the scan keeps the map itself out of the pickers.
	names := gfx.ScanTextures(dir)
	if _, ok := names["withmap_normal"]; ok {
		t.Error("the normal map was scanned as a texture of its own")
	}
	if _, ok := names["withmap"]; !ok {
		t.Error("the albedo is missing from the scanned textures")
	}
}
