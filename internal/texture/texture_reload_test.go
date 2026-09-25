package texture

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
	"foundry/internal/texdoc"
)

// reloadFixture is withTempTextureDir plus the gfx state a reload reads and
// writes, with uploads off: there is no GL context in a test.
func reloadFixture(t *testing.T) string {
	t.Helper()
	withTempTextureDir(t)
	dir := fileutil.TexturesDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	prevNames, prevUploads := gfx.TextureNames, gfx.TextureUploadsEnabled
	gfx.TextureNames = map[string]string{}
	gfx.TextureUploadsEnabled = false
	ftexStamps = map[string]ftexStamp{}
	t.Cleanup(func() {
		gfx.TextureNames, gfx.TextureUploadsEnabled = prevNames, prevUploads
		ftexStamps = map[string]ftexStamp{}
	})
	return dir
}

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 200, A: 255})
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

// The case this file exists for: a PNG written into textures/ by something that
// is not the texture editor — a generator, or Claude — while the editor is
// running. Before ReloadTexture there was no way to be told about it.
func TestReloadTextureRegistersANameTheEditorHasNeverSeen(t *testing.T) {
	dir := reloadFixture(t)
	writePNG(t, filepath.Join(dir, "reload_new.png"), 2, 2)

	if err := ReloadTexture("reload_new"); err != nil {
		t.Fatal(err)
	}
	if _, ok := gfx.TextureNames["reload_new"]; !ok {
		t.Error("the texture is still not a name the editor knows, so no picker offers it")
	}
}

// The other half of the same gap: a texture already drawing in the level, whose
// file has been rewritten. Nothing re-reads a resident texture on its own.
func TestReloadTextureRereadsOneThatIsAlreadyDrawing(t *testing.T) {
	dir := reloadFixture(t)
	path := filepath.Join(dir, "reload_resident.png")
	writePNG(t, path, 2, 2)
	if err := ReloadTexture("reload_resident"); err != nil {
		t.Fatal(err)
	}
	if err := gfx.EnsureTextureLoaded("reload_resident"); err != nil {
		t.Fatal(err)
	}

	writePNG(t, path, 8, 8)
	if err := ReloadTexture("reload_resident"); err != nil {
		t.Fatal(err)
	}
	if w, _, ok := gfx.TextureSize("reload_resident"); !ok || w != 8 {
		t.Errorf("TextureSize width = %g (known %v), want 8 — the rewritten file was not read", w, ok)
	}
}

func TestReloadTextureForgetsAPNGThatHasGone(t *testing.T) {
	dir := reloadFixture(t)
	path := filepath.Join(dir, "reload_gone.png")
	writePNG(t, path, 2, 2)
	if err := ReloadTexture("reload_gone"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := ReloadTexture("reload_gone"); err != nil {
		t.Fatal(err)
	}
	if _, ok := gfx.TextureNames["reload_gone"]; ok {
		t.Error("a deleted texture is still offered in the pickers")
	}
}

func TestReloadTexturesPicksUpAFileWrittenOutsideTheEditor(t *testing.T) {
	reloadFixture(t)
	d, err := texdoc.NewTexDoc("flame", 2, 2, texdoc.GlobalPalette)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddFrame(); err != nil {
		t.Fatal(err)
	}
	if err := SaveTexDoc(d, "flame"); err != nil {
		t.Fatal(err)
	}

	r := ReloadTextures()
	if len(r.Added) != 1 || r.Added[0] != "flame" {
		t.Fatalf("Added = %v, want [flame]", r.Added)
	}
	if _, ok := gfx.TextureNames["flame"]; !ok {
		t.Error("flame is still not a name the editor knows")
	}
}

// A second reload with nothing changed must not re-read the .ftex files: that
// walk is the expensive half, and a reload is offered as something safe to
// press twice.
func TestRescanAnimatedTexturesSkipsUnchangedFiles(t *testing.T) {
	reloadFixture(t)
	d, err := texdoc.NewTexDoc("flame", 2, 2, texdoc.GlobalPalette)
	if err != nil {
		t.Fatal(err)
	}
	d.AddFrame()
	if err := SaveTexDoc(d, "flame"); err != nil {
		t.Fatal(err)
	}

	if n := ScanAnimatedTextures(); n != 1 {
		t.Fatalf("first scan registered %d animated textures, want 1", n)
	}
	if n := RescanAnimatedTextures(); n != 0 {
		t.Errorf("rescan re-read %d unchanged .ftex files, want 0", n)
	}
}
