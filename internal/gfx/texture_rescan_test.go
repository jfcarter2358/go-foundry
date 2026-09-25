package gfx

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"foundry/internal/fileutil"
)

// rescanFixture stands up a resources/textures directory the package's globals
// point at, with uploads off — there is no GL context in a test, and the decode
// half is the half this file is about.
func rescanFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	texDir := filepath.Join(dir, "textures")
	if err := os.MkdirAll(texDir, 0755); err != nil {
		t.Fatal(err)
	}

	prevRes, prevNames, prevUploads := fileutil.ResourcesPath, TextureNames, TextureUploadsEnabled
	fileutil.ResourcesPath = dir
	TextureNames = map[string]string{}
	TextureUploadsEnabled = false
	Textures = map[string]uint32{}
	textureStamps = map[string]textureStamp{}
	missingTextures = map[string]bool{}
	pendingTextureUploads = nil
	t.Cleanup(func() {
		fileutil.ResourcesPath, TextureNames, TextureUploadsEnabled = prevRes, prevNames, prevUploads
		Textures = map[string]uint32{}
		textureStamps = map[string]textureStamp{}
		missingTextures = map[string]bool{}
		pendingTextureUploads = nil
	})
	return texDir
}

// writeTestPNG writes a w×h PNG of one colour, and backdates nothing: callers
// that need a changed file write a different size, so the stamp differs whether
// or not the filesystem's timestamps are coarse.
func writeTestPNG(t *testing.T, path string, w, h int, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestRescanTexturesFindsANewFile(t *testing.T) {
	dir := rescanFixture(t)
	writeTestPNG(t, filepath.Join(dir, "rust.png"), 2, 2, color.RGBA{R: 255, A: 255})

	r := RescanTextures()
	if len(r.Added) != 1 || r.Added[0] != "rust" {
		t.Fatalf("Added = %v, want [rust]", r.Added)
	}
	if _, ok := TextureNames["rust"]; !ok {
		t.Error("a texture written after startup did not reach TextureNames, so no picker would offer it")
	}
	// Nothing draws it yet, so it has no business on the GPU.
	if len(r.Reloaded) != 0 {
		t.Errorf("Reloaded = %v, want none — nothing was resident", r.Reloaded)
	}
}

func TestRescanTexturesRereadsAChangedFile(t *testing.T) {
	dir := rescanFixture(t)
	path := filepath.Join(dir, "rust.png")
	writeTestPNG(t, path, 2, 2, color.RGBA{R: 255, A: 255})
	RescanTextures()
	if err := EnsureTextureLoaded("rust"); err != nil {
		t.Fatal(err)
	}

	if r := RescanTextures(); len(r.Reloaded) != 0 {
		t.Fatalf("an untouched file was re-read: %v", r.Reloaded)
	}

	writeTestPNG(t, path, 4, 4, color.RGBA{B: 255, A: 255})
	// Some filesystems have one-second timestamps; the size differs too, which
	// is the other half of the stamp.
	os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second))

	r := RescanTextures()
	if len(r.Reloaded) != 1 || r.Reloaded[0] != "rust" {
		t.Fatalf("Reloaded = %v, want [rust] — a rewritten texture kept its startup image", r.Reloaded)
	}
	if w, _, ok := TextureSize("rust"); !ok || w != 4 {
		t.Errorf("TextureSize width = %g (known %v), want 4 — the new image was not the one read", w, ok)
	}
}

func TestRescanTexturesDropsADeletedName(t *testing.T) {
	dir := rescanFixture(t)
	path := filepath.Join(dir, "rust.png")
	writeTestPNG(t, path, 2, 2, color.RGBA{R: 255, A: 255})
	RescanTextures()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	r := RescanTextures()
	if len(r.Removed) != 1 || r.Removed[0] != "rust" {
		t.Fatalf("Removed = %v, want [rust]", r.Removed)
	}
	if _, ok := TextureNames["rust"]; ok {
		t.Error("a deleted texture is still offered in the pickers")
	}
}

func TestRescanTexturesLoadsWhatWasMissing(t *testing.T) {
	dir := rescanFixture(t)
	// Something asked for it before it existed — a sprite, a decal, a model.
	if err := EnsureTextureLoaded("rust"); err == nil {
		t.Fatal("EnsureTextureLoaded accepted a texture that is not on disk")
	}
	writeTestPNG(t, filepath.Join(dir, "rust.png"), 2, 2, color.RGBA{R: 255, A: 255})

	r := RescanTextures()
	if len(r.Filled) != 1 || r.Filled[0] != "rust" {
		t.Fatalf("Filled = %v, want [rust] — what went without is still going without", r.Filled)
	}
	if _, ok := Textures["rust"]; !ok {
		t.Error("the texture that was missing was not loaded once its file appeared")
	}
}

func TestRescanTexturesKeepsNormalMapsOutOfThePickers(t *testing.T) {
	dir := rescanFixture(t)
	writeTestPNG(t, filepath.Join(dir, "rust.png"), 2, 2, color.RGBA{R: 255, A: 255})
	writeTestPNG(t, filepath.Join(dir, "rust_normal.png"), 2, 2, color.RGBA{B: 255, A: 255})

	r := RescanTextures()
	if len(r.Added) != 1 || r.Added[0] != "rust" {
		t.Fatalf("Added = %v, want [rust] alone — a normal map is not a texture in its own right", r.Added)
	}
}

func TestRescanTexturesSummaryIsEmptyWhenNothingChanged(t *testing.T) {
	rescanFixture(t)
	r := RescanTextures()
	if r.Changed() || r.Summary() != "" {
		t.Errorf("an empty rescan reported %q", r.Summary())
	}
}
