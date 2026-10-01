package harness

// cmd_render_test.go — the pictures, drawn for real.
//
// The model half needs nothing but a CPU and runs everywhere. The screen half
// needs a display and a GL 4.3 driver, so it is opt-in the same way every other
// drawing test in the engine is (FOUNDRY_GL_TEST=1) — and it is worth having
// opt-in rather than not at all, because the mistake it catches is one that
// leaves a perfectly plausible image: a screen whose glyphs never reached the
// GPU draws its whole layout correctly with a black box in place of every
// letter.

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/testenv"
)

func TestRenderModelWritesAPicture(t *testing.T) {
	root := testenv.RepoRoot(t)
	dst := filepath.Join(t.TempDir(), "model.png")
	var out strings.Builder
	code := RenderMain(root, []string{
		"model", filepath.Join(root, "resources", "models", "dropship.fmdl"),
		"-view", "iso", "-untextured", "-size", "128x128", "-o", dst,
	}, &out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	img := readPNG(t, dst)
	if b := img.Bounds(); b.Dx() != 128 || b.Dy() != 128 {
		t.Errorf("image is %v, want 128x128", b)
	}
	if uniform(img) {
		t.Error("every pixel is the same colour: nothing was drawn")
	}
}

// Flags after the path, which is how anyone actually types it and which Go's
// own flag package does not handle.
func TestRenderModelTakesFlagsAfterThePath(t *testing.T) {
	root := testenv.RepoRoot(t)
	dst := filepath.Join(t.TempDir(), "model.png")
	var out strings.Builder
	code := RenderMain(root, []string{
		"model", filepath.Join(root, "resources", "models", "dropship.fmdl"),
		"-size", "64x64", "-o", dst,
	}, &out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if b := readPNG(t, dst).Bounds(); b.Dx() != 64 {
		t.Errorf("the -size after the path was ignored: image is %v", b)
	}
}

func TestRenderScreenDrawsItsText(t *testing.T) {
	if os.Getenv("FOUNDRY_GL_TEST") == "" {
		t.Skip("set FOUNDRY_GL_TEST=1 to run (needs a display and a GL 4.3 driver)")
	}
	root := testenv.RepoRoot(t)
	dst := filepath.Join(t.TempDir(), "menu.png")
	var out strings.Builder
	code := RenderMain(root, []string{"screen", "menu", "-size", "480x270", "-o", dst}, &out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	img := readPNG(t, dst)
	if uniform(img) {
		t.Fatal("every pixel is the same colour: the screen did not draw")
	}
	// Text is the brightest thing on this screen, and a glyph that never reached
	// the atlas draws as a filled box — dark, not bright. So: something near
	// white has to be in there.
	bright := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 > 0xd0 && g>>8 > 0xd0 && bl>>8 > 0xd0 {
				bright++
			}
		}
	}
	if bright < 20 {
		t.Errorf("only %d near-white pixels: the glyphs never reached the GPU, "+
			"so every letter is a box", bright)
	}
}

func readPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("the render wrote no file: %v", err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}

func uniform(img image.Image) bool {
	b := img.Bounds()
	first := img.At(b.Min.X, b.Min.Y)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.At(x, y) != first {
				return false
			}
		}
	}
	return true
}
