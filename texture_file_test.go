package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/texdoc"
)

// withTempTextureDir points executablePath at a scratch dir so file-format
// tests never touch the real textures/ tree, matching withTempPresetDir's
// pattern in detail_presets_test.go.
func withTempTextureDir(t *testing.T) {
	t.Helper()
	prev := executablePath
	prevPal := texdoc.GlobalPalette
	executablePath = t.TempDir()
	t.Cleanup(func() {
		executablePath = prev
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
	data[16] = texdoc.TexMaxLayers + 1
	os.WriteFile(path, data, 0644)
	if _, err := ReadTexDoc(path); err == nil {
		t.Error("ReadTexDoc accepted layerCount > texdoc.TexMaxLayers")
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
