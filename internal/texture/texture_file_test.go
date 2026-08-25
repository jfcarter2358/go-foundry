package texture

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/texdoc"
)

// withTempTextureDir points ExecutablePath at a scratch dir so file-format
// tests never touch the real textures/ tree, matching withTempPresetDir's
// pattern in detail_presets_test.go.
func withTempTextureDir(t *testing.T) {
	t.Helper()
	prev := fileutil.ExecutablePath
	prevPal := texdoc.GlobalPalette
	fileutil.ExecutablePath = t.TempDir()
	t.Cleanup(func() {
		fileutil.ExecutablePath = prev
		texdoc.GlobalPalette = prevPal
	})
}

func roundTripPalette() texdoc.TexPalette {
	var pal texdoc.TexPalette
	for i := range pal {
		pal[i] = color.RGBA{
			R: uint8(i), G: uint8(255 - i), B: uint8(i * 3), A: uint8(200 + i%56),
		}
	}
	pal[0] = color.RGBA{0, 0, 0, 0}
	return pal
}

func TestPaletteRoundTrip(t *testing.T) {
	withTempTextureDir(t)
	pal := roundTripPalette()
	path := filepath.Join(t.TempDir(), "test.pal")
	if err := WritePalette(path, &pal); err != nil {
		t.Fatalf("WritePalette: %v", err)
	}
	got, err := ReadPalette(path)
	if err != nil {
		t.Fatalf("ReadPalette: %v", err)
	}
	if *got != pal {
		t.Error("palette round-trip mismatch")
	}
}

func TestReadPaletteRejectsBadMagic(t *testing.T) {
	withTempTextureDir(t)
	path := filepath.Join(t.TempDir(), "bad.pal")
	data := make([]byte, fpalFileLen)
	copy(data, "NOPE")
	os.WriteFile(path, data, 0644)
	if _, err := ReadPalette(path); err == nil {
		t.Error("ReadPalette accepted a bad magic")
	}
}

func TestReadPaletteRejectsShortFile(t *testing.T) {
	withTempTextureDir(t)
	path := filepath.Join(t.TempDir(), "short.pal")
	os.WriteFile(path, []byte("FPAL\x01\x00truncated"), 0644)
	if _, err := ReadPalette(path); err == nil {
		t.Error("ReadPalette accepted a truncated file")
	}
}

func roundTripDoc() *texdoc.TexDoc {
	pal := roundTripPalette()
	d, _ := texdoc.NewTexDoc("mixed", 4, 3, pal)
	names := []string{"base", "détail ✓", "", "over 8000", "e", "f", "g", "h"}
	opacities := []float32{1, 0.5, 0.0, 0.999, 0.1, 0.75, 0.33, 1}
	blends := []texdoc.TexBlend{texdoc.BlendNormal, texdoc.BlendMultiply, texdoc.BlendAdd, texdoc.BlendScreen, texdoc.BlendNormal, texdoc.BlendMultiply, texdoc.BlendAdd, texdoc.BlendScreen}
	for i := 1; i < 8; i++ {
		if err := d.AddLayer(names[i]); err != nil {
			panic(err)
		}
	}
	for i := range d.Layers {
		d.Layers[i].Name = names[i]
		d.Layers[i].Opacity = opacities[i]
		d.Layers[i].Blend = blends[i]
		d.Layers[i].Visible = i%2 == 0
		for j := range d.Layers[i].Idx {
			d.Layers[i].Idx[j] = uint8((j*7 + i*13) % texdoc.TexPaletteSz)
		}
	}
	return d
}

func TestFtexRoundTrip8Layers(t *testing.T) {
	withTempTextureDir(t)
	d := roundTripDoc()
	path := filepath.Join(t.TempDir(), "mixed.ftex")
	if err := WriteTexDoc(path, d); err != nil {
		t.Fatalf("WriteTexDoc: %v", err)
	}
	got, err := ReadTexDoc(path)
	if err != nil {
		t.Fatalf("ReadTexDoc: %v", err)
	}
	if got.W != d.W || got.H != d.H {
		t.Fatalf("dims = %dx%d, want %dx%d", got.W, got.H, d.W, d.H)
	}
	if got.Pal != d.Pal {
		t.Error("embedded palette mismatch")
	}
	if len(got.Layers) != len(d.Layers) {
		t.Fatalf("layer count = %d, want %d", len(got.Layers), len(d.Layers))
	}
	for i := range d.Layers {
		w, g := d.Layers[i], got.Layers[i]
		if g.Name != w.Name {
			t.Errorf("layer %d name = %q, want %q", i, g.Name, w.Name)
		}
		if g.Blend != w.Blend {
			t.Errorf("layer %d blend = %v, want %v", i, g.Blend, w.Blend)
		}
		if g.Visible != w.Visible {
			t.Errorf("layer %d visible = %v, want %v", i, g.Visible, w.Visible)
		}
		if g.Opacity != w.Opacity {
			t.Errorf("layer %d opacity = %v, want %v", i, g.Opacity, w.Opacity)
		}
		if !bytes.Equal(g.Idx, w.Idx) {
			t.Errorf("layer %d indices mismatch", i)
		}
	}
}

// TestFtexRoundTripMultipleFrames covers the version-2 format's actual new
// content: several frames, each with its own layer stack, surviving a
// write/read round trip independently of one another.
func TestFtexRoundTripMultipleFrames(t *testing.T) {
	withTempTextureDir(t)
	pal := roundTripPalette()
	d, err := texdoc.NewTexDoc("anim", 2, 2, pal)
	if err != nil {
		t.Fatal(err)
	}
	for i := range d.Layers[0].Idx {
		d.Layers[0].Idx[i] = 1
	}
	if err := d.AddFrame(); err != nil {
		t.Fatal(err)
	}
	if err := d.SetActiveFrame(1); err != nil {
		t.Fatal(err)
	}
	for i := range d.Layers[0].Idx {
		d.Layers[0].Idx[i] = 2
	}
	if err := d.AddLayer("extra"); err != nil {
		t.Fatal(err)
	}
	if err := d.AddFrame(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "anim.ftex")
	if err := WriteTexDoc(path, d); err != nil {
		t.Fatalf("WriteTexDoc: %v", err)
	}
	got, err := ReadTexDoc(path)
	if err != nil {
		t.Fatalf("ReadTexDoc: %v", err)
	}
	if got.FrameCount() != 3 {
		t.Fatalf("frame count = %d, want 3", got.FrameCount())
	}
	frames := got.Frames()
	if len(frames[0].Layers) != 1 || frames[0].Layers[0].Idx[0] != 1 {
		t.Errorf("frame 0 = %+v, want a single layer of index 1", frames[0])
	}
	if len(frames[1].Layers) != 2 || frames[1].Layers[0].Idx[0] != 2 {
		t.Errorf("frame 1 = %+v, want two layers, bottom index 2", frames[1])
	}
	if len(frames[2].Layers) != 1 {
		t.Errorf("frame 2 has %d layers, want 1 (the fresh empty frame)", len(frames[2].Layers))
	}
	if got.FPS() != 3 {
		t.Errorf("FPS = %v, want 3 (one frame count per second)", got.FPS())
	}
}

// TestFtexReadsVersion1AsOneFrame pins the migration path: an old
// single-frame file (no frameCount field at all) still opens today, as a
// one-frame — i.e. not animated — document.
func TestFtexReadsVersion1AsOneFrame(t *testing.T) {
	withTempTextureDir(t)
	d := roundTripDoc() // 8 layers, single (implicit) frame
	path := filepath.Join(t.TempDir(), "old.ftex")
	if err := WriteTexDoc(path, d); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Rewrite the version-2 header (frameCount=1, since d only ever had one
	// frame) back into the version-1 shape: a version number of 1 and the
	// frame's layerCount sitting directly at offset 16, no frame wrapper.
	binary.LittleEndian.PutUint16(data[4:6], 1)
	frameHeader := data[ftexHeaderLen : ftexHeaderLen+4] // layerCount + 3 padding
	copy(data[16:20], frameHeader)
	v1 := append(append([]byte{}, data[:ftexHeaderLen]...), data[ftexHeaderLen+4:]...)
	path = filepath.Join(t.TempDir(), "old_v1.ftex")
	if err := os.WriteFile(path, v1, 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadTexDoc(path)
	if err != nil {
		t.Fatalf("ReadTexDoc of a version-1 file: %v", err)
	}
	if got.FrameCount() != 1 {
		t.Errorf("frame count = %d, want 1", got.FrameCount())
	}
	if len(got.Layers) != len(d.Layers) {
		t.Fatalf("layer count = %d, want %d", len(got.Layers), len(d.Layers))
	}
	for i := range d.Layers {
		if !bytes.Equal(got.Layers[i].Idx, d.Layers[i].Idx) {
			t.Errorf("layer %d indices mismatch after version-1 read", i)
		}
	}
}

func TestFtexRejectsBadMagic(t *testing.T) {
	withTempTextureDir(t)
	path := filepath.Join(t.TempDir(), "bad.ftex")
	data := make([]byte, ftexHeaderLen)
	copy(data, "NOPE")
	os.WriteFile(path, data, 0644)
	if _, err := ReadTexDoc(path); err == nil {
		t.Error("ReadTexDoc accepted a bad magic")
	}
}

func TestFtexRejectsWrongVersion(t *testing.T) {
	withTempTextureDir(t)
	d := roundTripDoc()
	path := filepath.Join(t.TempDir(), "v.ftex")
	if err := WriteTexDoc(path, d); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	binary.LittleEndian.PutUint16(data[4:6], 99)
	os.WriteFile(path, data, 0644)
	if _, err := ReadTexDoc(path); err == nil {
		t.Error("ReadTexDoc accepted an unsupported version")
	}
}

func TestFtexRejectsLayerCountAboveMax(t *testing.T) {
	withTempTextureDir(t)
	d := roundTripDoc()
	path := filepath.Join(t.TempDir(), "l.ftex")
	if err := WriteTexDoc(path, d); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	data[ftexHeaderLen] = texdoc.TexMaxLayers + 1 // frame 0's layerCount byte
	os.WriteFile(path, data, 0644)
	if _, err := ReadTexDoc(path); err == nil {
		t.Error("ReadTexDoc accepted layerCount > texdoc.TexMaxLayers")
	}
}

func TestFtexRejectsFrameCountAboveMax(t *testing.T) {
	withTempTextureDir(t)
	d := roundTripDoc()
	path := filepath.Join(t.TempDir(), "f.ftex")
	if err := WriteTexDoc(path, d); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	binary.LittleEndian.PutUint16(data[16:18], texdoc.TexMaxFrames+1)
	os.WriteFile(path, data, 0644)
	if _, err := ReadTexDoc(path); err == nil {
		t.Error("ReadTexDoc accepted frameCount > texdoc.TexMaxFrames")
	}
}

func TestFtexRejectsOversizedDimensions(t *testing.T) {
	withTempTextureDir(t)
	d := roundTripDoc()
	path := filepath.Join(t.TempDir(), "d.ftex")
	if err := WriteTexDoc(path, d); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	binary.LittleEndian.PutUint32(data[8:12], texdoc.TexMaxDim+1)
	os.WriteFile(path, data, 0644)
	if _, err := ReadTexDoc(path); err == nil {
		t.Error("ReadTexDoc accepted width > texdoc.TexMaxDim")
	}
}

func TestFtexRejectsZeroDimensions(t *testing.T) {
	withTempTextureDir(t)
	d := roundTripDoc()
	path := filepath.Join(t.TempDir(), "z.ftex")
	if err := WriteTexDoc(path, d); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	binary.LittleEndian.PutUint32(data[8:12], 0)
	os.WriteFile(path, data, 0644)
	if _, err := ReadTexDoc(path); err == nil {
		t.Error("ReadTexDoc accepted zero width")
	}
}

func TestFtexRejectsTruncatedPayload(t *testing.T) {
	withTempTextureDir(t)
	d := roundTripDoc()
	path := filepath.Join(t.TempDir(), "t.ftex")
	if err := WriteTexDoc(path, d); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	os.WriteFile(path, data[:len(data)-10], 0644)
	if _, err := ReadTexDoc(path); err == nil {
		t.Error("ReadTexDoc accepted a truncated payload")
	}
}

func TestFtexRejectsWrongDecompressedLength(t *testing.T) {
	withTempTextureDir(t)
	// One layer whose compressed payload decompresses to fewer bytes than W*H.
	pal := roundTripPalette()
	d, _ := texdoc.NewTexDoc("small", 4, 4, pal) // 16 indices
	d.Layers[0].Idx = d.Layers[0].Idx[:8]        // WriteTexDoc should refuse this directly
	path := filepath.Join(t.TempDir(), "wl.ftex")
	if err := WriteTexDoc(path, d); err == nil {
		t.Error("WriteTexDoc accepted a layer whose index count doesn't match W*H")
	}
}

func TestFtexRejectsShortPaletteBlock(t *testing.T) {
	withTempTextureDir(t)
	d := roundTripDoc()
	path := filepath.Join(t.TempDir(), "p.ftex")
	if err := WriteTexDoc(path, d); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	// Truncate right in the middle of the embedded palette block.
	os.WriteFile(path, data[:500], 0644)
	if _, err := ReadTexDoc(path); err == nil {
		t.Error("ReadTexDoc accepted a short palette block")
	}
}

func TestImportPNGRejectsNonexistentFile(t *testing.T) {
	withTempTextureDir(t)
	if _, err := ImportPNG(filepath.Join(t.TempDir(), "nope.png")); err == nil {
		t.Error("ImportPNG accepted a nonexistent file")
	}
}

func TestImportPNGRejectsOversizedImage(t *testing.T) {
	withTempTextureDir(t)
	img := image.NewRGBA(image.Rect(0, 0, texdoc.TexMaxDim+1, 1))
	path := filepath.Join(t.TempDir(), "big.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, err := ImportPNG(path); err == nil {
		t.Error("ImportPNG accepted an image wider than texdoc.TexMaxDim")
	}
}

func TestImportPNGQuantisesToGlobalPalette(t *testing.T) {
	withTempTextureDir(t)
	texdoc.GlobalPalette = flattenPalette() // 0 transparent, 1 red, 2 green, 3 blue, 4 grey
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255}) // exact red
	img.Set(1, 0, color.RGBA{0, 0, 0, 0})     // exact transparent
	path := filepath.Join(t.TempDir(), "rg.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()

	doc, err := ImportPNG(path)
	if err != nil {
		t.Fatalf("ImportPNG: %v", err)
	}
	if got := doc.Layers[0].Idx[0]; got != 1 {
		t.Errorf("imported red texel index = %d, want 1", got)
	}
	if got := doc.Layers[0].Idx[1]; got != 0 {
		t.Errorf("imported transparent texel index = %d, want 0", got)
	}
}
