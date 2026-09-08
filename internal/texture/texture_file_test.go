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
	"foundry/internal/gfx"
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
	// Rewrite the version-3 header (frameCount=1, since d only ever had one
	// frame) back into the version-1 shape: a version number of 1, the frame's
	// layerCount sitting directly at offset 16 with no frame wrapper, and
	// neither the bumpStrength word nor the frame header in the body.
	binary.LittleEndian.PutUint16(data[4:6], 1)
	frameHeader := data[ftexV3BodyOff : ftexV3BodyOff+4] // layerCount + 3 padding
	copy(data[16:20], frameHeader)
	v1 := append(append([]byte{}, data[:ftexHeaderLen]...), data[ftexV3BodyOff+4:]...)
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
	data[ftexV3BodyOff] = texdoc.TexMaxLayers + 1 // frame 0's layerCount byte
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

// ---------- bump channel / normal maps ----------

// bumpRoundTripDoc is a two-frame document with a painted height field in
// each frame, which is what exercises every part of the version-3 body:
// bumpStrength, and a height block per frame block.
func bumpRoundTripDoc(t *testing.T) *texdoc.TexDoc {
	t.Helper()
	d, err := texdoc.NewTexDoc("bumpy", 4, 3, roundTripPalette())
	if err != nil {
		t.Fatal(err)
	}
	for i := range d.Layers[0].Idx {
		d.Layers[0].Idx[i] = uint8(i + 1)
	}
	if err := d.AddFrame(); err != nil {
		t.Fatal(err)
	}
	d.SetChannel(texdoc.ChanBump)
	d.SetBumpStrength(6.5)
	for f := 0; f < 2; f++ {
		if err := d.SetActiveFrame(f); err != nil {
			t.Fatal(err)
		}
		for i := range d.Layers[0].Idx {
			d.Layers[0].Idx[i] = uint8(200 - i - f)
		}
	}
	return d
}

func TestFtexRoundTripsTheBumpChannel(t *testing.T) {
	withTempTextureDir(t)
	d := bumpRoundTripDoc(t)
	path := filepath.Join(t.TempDir(), "bumpy.ftex")
	if err := WriteTexDoc(path, d); err != nil {
		t.Fatalf("WriteTexDoc: %v", err)
	}
	got, err := ReadTexDoc(path)
	if err != nil {
		t.Fatalf("ReadTexDoc: %v", err)
	}
	if !got.HasBump() {
		t.Fatal("bump channel lost on the round trip")
	}
	if got.BumpStrength() != d.BumpStrength() {
		t.Errorf("bump strength = %v, want %v", got.BumpStrength(), d.BumpStrength())
	}
	want, have := d.Frames(), got.Frames()
	if len(have) != len(want) {
		t.Fatalf("frame count = %d, want %d", len(have), len(want))
	}
	for i := range want {
		if len(have[i].Bump) != len(want[i].Bump) {
			t.Fatalf("frame %d height layer count = %d, want %d", i, len(have[i].Bump), len(want[i].Bump))
		}
		for li := range want[i].Bump {
			if !bytes.Equal(have[i].Bump[li].Idx, want[i].Bump[li].Idx) {
				t.Errorf("frame %d height layer %d mismatch after round trip", i, li)
			}
		}
	}
}

// TestFtexWithoutBumpWritesNoBumpBlock: the flag has to actually gate the
// body, or every existing texture's file grows a height stack it never asked
// for.
func TestFtexWithoutBumpWritesNoBumpBlock(t *testing.T) {
	withTempTextureDir(t)
	plain := roundTripDoc()
	plainPath := filepath.Join(t.TempDir(), "plain.ftex")
	if err := WriteTexDoc(plainPath, plain); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(plainPath)
	if err != nil {
		t.Fatal(err)
	}
	if flags := binary.LittleEndian.Uint16(data[6:8]); flags&ftexFlagBump != 0 {
		t.Errorf("flags = %#x on a document with no bump channel, want the bump bit clear", flags)
	}
	got, err := ReadTexDoc(plainPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.HasBump() {
		t.Error("a document with no bump channel read back as having one")
	}
}

// TestFtexVersion2ReadsWithoutABumpChannel pins the migration: a file written
// before the bump channel existed still opens, flat.
func TestFtexVersion2ReadsWithoutABumpChannel(t *testing.T) {
	withTempTextureDir(t)
	d := roundTripDoc()
	path := filepath.Join(t.TempDir(), "v3.ftex")
	if err := WriteTexDoc(path, d); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Version 2 is version 3 minus the bumpStrength word.
	binary.LittleEndian.PutUint16(data[4:6], 2)
	v2 := append(append([]byte{}, data[:ftexHeaderLen]...), data[ftexV3BodyOff:]...)
	v2Path := filepath.Join(t.TempDir(), "v2.ftex")
	if err := os.WriteFile(v2Path, v2, 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadTexDoc(v2Path)
	if err != nil {
		t.Fatalf("ReadTexDoc of a version-2 file: %v", err)
	}
	if got.HasBump() {
		t.Error("a version-2 file read back as having a bump channel")
	}
	if len(got.Layers) != len(d.Layers) {
		t.Fatalf("layer count = %d, want %d", len(got.Layers), len(d.Layers))
	}
	for i := range d.Layers {
		if !bytes.Equal(got.Layers[i].Idx, d.Layers[i].Idx) {
			t.Errorf("layer %d indices mismatch after a version-2 read", i)
		}
	}
}

// TestSaveWritesTheNormalMapPNG: the engine samples the PNG, not the .ftex,
// so a bump channel that never reaches disk as a normal map does nothing.
func TestSaveWritesTheNormalMapPNG(t *testing.T) {
	withTempTextureDir(t)
	d := bumpRoundTripDoc(t)
	if err := SaveTexDoc(d, "bumpy"); err != nil {
		t.Fatalf("SaveTexDoc: %v", err)
	}
	normal := filepath.Join(fileutil.TexturesDir(), "bumpy_normal.png")
	f, err := os.Open(normal)
	if err != nil {
		t.Fatalf("normal map not written: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode normal map: %v", err)
	}
	if got := img.Bounds().Size(); got.X != d.W || got.Y != d.H {
		t.Errorf("normal map is %v, want %dx%d", got, d.W, d.H)
	}
}

// TestSaveRemovesAStaleNormalMap: the renderer decides a texture is
// normal-mapped purely by whether the file loaded, so deleting a bump channel
// has to take the file with it.
func TestSaveRemovesAStaleNormalMap(t *testing.T) {
	withTempTextureDir(t)
	d := bumpRoundTripDoc(t)
	if err := SaveTexDoc(d, "bumpy"); err != nil {
		t.Fatal(err)
	}
	normal := filepath.Join(fileutil.TexturesDir(), "bumpy_normal.png")
	if _, err := os.Stat(normal); err != nil {
		t.Fatalf("precondition: %v", err)
	}

	d.DisableBump()
	if err := SaveTexDoc(d, "bumpy"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(normal); !os.IsNotExist(err) {
		t.Errorf("normal map still on disk after the bump channel was deleted (err = %v)", err)
	}
}

// TestSaveWithoutBumpWritesNoNormalMap keeps the common case free of stray
// files: most textures have no relief and should ship nothing extra.
func TestSaveWithoutBumpWritesNoNormalMap(t *testing.T) {
	withTempTextureDir(t)
	if err := SaveTexDoc(roundTripDoc(), "plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(fileutil.TexturesDir(), "plain_normal.png")); !os.IsNotExist(err) {
		t.Errorf("a document with no bump channel wrote a normal map (err = %v)", err)
	}
}

// TestScanFtexIgnoresNormalMaps guards the naming convention from the other
// side: a normal map is a .png, so it must never be mistaken for a texture in
// its own right.
func TestScanNamesExcludeNormalMaps(t *testing.T) {
	withTempTextureDir(t)
	d := bumpRoundTripDoc(t)
	if err := SaveTexDoc(d, "bumpy"); err != nil {
		t.Fatal(err)
	}
	names := gfx.ScanTextures(fileutil.TexturesDir())
	if _, ok := names["bumpy"]; !ok {
		t.Error("the albedo is missing from the scanned textures")
	}
	if _, ok := names["bumpy_normal"]; ok {
		t.Error("the normal map was scanned as a texture of its own")
	}
}

// TestImportedPNGIsAWholeDocument: an imported document has to come out of the
// constructor, not a struct literal. TexDoc carries unexported frame/channel
// bookkeeping, and one assembled by hand has an empty animation stack that
// every whole-document operation — Flatten among them — indexes into.
func TestImportedPNGIsAWholeDocument(t *testing.T) {
	withTempTextureDir(t)
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for i := range src.Pix {
		src.Pix[i] = 255
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "imported.png")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	d, err := ImportPNG(path)
	if err != nil {
		t.Fatalf("ImportPNG: %v", err)
	}
	if d.Name != "imported" {
		t.Errorf("name = %q, want %q", d.Name, "imported")
	}
	if got := d.FrameCount(); got != 1 {
		t.Errorf("frame count = %d, want 1", got)
	}
	if d.HasBump() {
		t.Error("an imported PNG came in with a bump channel")
	}
	// The operations a freshly imported document is immediately put through.
	if got := d.Flatten().Bounds().Size(); got.X != 3 || got.Y != 2 {
		t.Errorf("flattened size = %v, want 3x2", got)
	}
	if err := SaveTexDoc(d, "imported"); err != nil {
		t.Fatalf("SaveTexDoc on an imported document: %v", err)
	}
}
