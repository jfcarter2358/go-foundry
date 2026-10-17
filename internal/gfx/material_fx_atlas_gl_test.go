package gfx_test

// material_fx_atlas_gl_test.go — opt-in checks that a splat's layer atlases
// (material_fx_textures.go) go up as BC7 when texture compression is on,
// sample back close to the RGBA8 atlases they replace, are counted right in
// the F3 VRAM record, come off the disk cache the second time, and — under
// skip-mips — drop their top level to the same atlas the uncompressed path
// packs small, from the same cache file. Plus a measurement on real 2k
// layers when pointed at a folder of them.
//
//	FOUNDRY_GL_TEST=1 go test -run TestSplatAtlas ./internal/gfx/
//	FOUNDRY_GL_TEST=1 FOUNDRY_SPLAT_MEASURE_DIR=<textures dir> go test -run TestMeasureSplatAtlas -v ./internal/gfx/

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-gl/gl/v4.3-core/gl"

	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/texcompress"
)

// splatAtlasEnv sets up GL, a private texture cache and the two settings,
// restored afterwards.
func splatAtlasEnv(t *testing.T) {
	t.Helper()
	gltest.Context(t)
	t.Setenv(texcompress.CacheDirEnv, t.TempDir())
	prevC, prevS, prevU := gfx.TextureCompression, gfx.TextureSkipMips, gfx.GeometryUploadsEnabled
	gfx.GeometryUploadsEnabled = true
	t.Cleanup(func() {
		gfx.TextureCompression, gfx.TextureSkipMips, gfx.GeometryUploadsEnabled = prevC, prevS, prevU
	})
}

// variedPNG writes a size² smooth pattern that differs with seed, so no two
// of a test's maps — and no two atlases — are the same bytes.
func variedPNG(t *testing.T, path string, size, seed int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fs := 1 + float64(seed%5)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			o := y*img.Stride + x*4
			img.Pix[o] = uint8(127 + 120*math.Sin(fs*float64(x)/40+float64(seed)))
			img.Pix[o+1] = uint8(127 + 100*math.Cos(float64(y)/(15+float64(seed))))
			img.Pix[o+2] = uint8((x*(seed+1) + y) * 255 / ((seed + 2) * size))
			img.Pix[o+3] = 255
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// atlasLevel0 is tex's level-0 width, internal format and pixels.
func atlasLevel0(tex uint32) (w, format int32, pix []byte) {
	w, format = levelInfo(tex, 0)
	pix = make([]byte, int(w)*int(w)*4)
	gl.GetTexImage(gl.TEXTURE_2D, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(pix))
	gl.BindTexture(gl.TEXTURE_2D, 0)
	return
}

// psnrBytes is the PSNR of b against a over all four channels.
func psnrBytes(a, b []byte) float64 {
	var se float64
	for i := range a {
		d := float64(a[i]) - float64(b[i])
		se += d * d
	}
	if se == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(255*255/(se/float64(len(a))))
}

// chainBytes is a BC7 chain's bytes from an edge² level 0 down to 1x1.
func chainBytes(edge int) int64 {
	var n int64
	for w := edge; ; w /= 2 {
		n += int64(texcompress.LevelBytes(w, w))
		if w == 1 {
			return n
		}
	}
}

func rgba8Chain(edge int) int64 {
	var n int64
	for w := edge; ; w /= 2 {
		n += int64(w * w * 4)
		if w == 1 {
			return n
		}
	}
}

func TestSplatAtlasCompressedUploadsAsBC7(t *testing.T) {
	splatAtlasEnv(t)
	dir := t.TempDir()
	// Four 512² layers, each with a height, a normal and an AO map: smooth
	// content, so BC7's error is small and any packing mistake is not.
	var names [4]string
	for i := range names {
		base := filepath.Join(dir, "layer"+string(rune('a'+i)))
		for k, suffix := range []string{"", "_height", "_normal", "_ao"} {
			variedPNG(t, base+suffix+".png", 512, 4*i+k)
		}
		names[i] = base
	}
	key := gfx.SplatAtlasVRAMKey(names)
	const edge = 2 * (512 + 2*16)

	// The RGBA8 atlases, as they always were.
	gfx.TextureCompression, gfx.TextureSkipMips = false, 0
	plain := gfx.SplatArraysFor(names)
	if plain == nil {
		t.Fatal("no atlases")
	}
	w, f, refAlbedo := atlasLevel0(plain.Albedo)
	if w != edge || (f != gl.RGBA8 && f != gl.RGBA) {
		t.Fatalf("plain albedo atlas: %d wide, format 0x%x", w, f)
	}
	_, _, refNormal := atlasLevel0(plain.Normal)
	if gfx.TextureFormatOf(key+":albedo") != gfx.TextureRGBA8 {
		t.Errorf("plain atlas recorded as %v", gfx.TextureFormatOf(key+":albedo"))
	}
	plainBytes := gfx.TextureVRAMBytes()

	// Compressed: an encode each, BC7 on the GPU and in the record.
	gfx.ForgetSplatArrays(names)
	gfx.TextureCompression = true
	enc, fb := gfx.TextureCompressionStats.Encodes.Load(), gfx.TextureCompressionStats.Fallbacks.Load()
	comp := gfx.SplatArraysFor(names)
	if comp == nil {
		t.Fatal("no compressed atlases")
	}
	if n := gfx.TextureCompressionStats.Fallbacks.Load() - fb; n != 0 {
		t.Fatalf("the driver refused the compressed atlases (%d fallbacks)", n)
	}
	if n := gfx.TextureCompressionStats.Encodes.Load() - enc; n != 2 {
		t.Errorf("%d encodes, want 2", n)
	}
	if comp.Size != plain.Size || comp.Gutter != plain.Gutter {
		t.Errorf("compressed size %d gutter %d, plain %d %d", comp.Size, comp.Gutter, plain.Size, plain.Gutter)
	}
	for _, c := range []struct {
		part string
		tex  uint32
		ref  []byte
	}{{"albedo", comp.Albedo, refAlbedo}, {"normal", comp.Normal, refNormal}} {
		w, f, got := atlasLevel0(c.tex)
		if w != edge || f != gl.COMPRESSED_RGBA_BPTC_UNORM {
			t.Errorf("%s atlas: %d wide, format 0x%x; want %d, BPTC", c.part, w, f, edge)
			continue
		}
		if r := gfx.TextureFormatOf(key + ":" + c.part); r != gfx.TextureBC7 {
			t.Errorf("%s atlas recorded as %v", c.part, r)
		}
		p := psnrBytes(c.ref, got)
		t.Logf("%s atlas BC7 vs RGBA8: PSNR %.2f dB", c.part, p)
		if p < 35 {
			t.Errorf("%s atlas PSNR %.2f dB, want >= 35", c.part, p)
		}
	}
	if d := plainBytes - gfx.TextureVRAMBytes(); d != 2*(rgba8Chain(edge)-chainBytes(edge)) {
		t.Errorf("the record shrank by %d bytes, want %d", d, 2*(rgba8Chain(edge)-chainBytes(edge)))
	}

	// Again: both from the disk cache.
	gfx.ForgetSplatArrays(names)
	hits := gfx.TextureCompressionStats.CacheHits.Load()
	if gfx.SplatArraysFor(names) == nil {
		t.Fatal("no atlases on the cached build")
	}
	if n := gfx.TextureCompressionStats.CacheHits.Load() - hits; n != 2 {
		t.Errorf("%d cache hits on the second build, want 2", n)
	}

	// Skip one mip: the uncompressed path packs at 256; the compressed one
	// packs at 512 (so the same cache files serve) and drops level 0 — the
	// same geometry, and close to the same pixels.
	gfx.TextureSkipMips = 1
	gfx.TextureCompression = false
	gfx.ForgetSplatArrays(names)
	small := gfx.SplatArraysFor(names)
	_, _, smallRef := atlasLevel0(small.Albedo)
	gfx.TextureCompression = true
	gfx.ForgetSplatArrays(names)
	hits = gfx.TextureCompressionStats.CacheHits.Load()
	skipped := gfx.SplatArraysFor(names)
	if n := gfx.TextureCompressionStats.CacheHits.Load() - hits; n != 2 {
		t.Errorf("skip-mips build: %d cache hits, want 2 (the full-size chains)", n)
	}
	if skipped.Size != small.Size || skipped.Gutter != small.Gutter || skipped.Size != 256 {
		t.Errorf("skipped size %d gutter %d, plain skipped %d %d", skipped.Size, skipped.Gutter, small.Size, small.Gutter)
	}
	w, f, got := atlasLevel0(skipped.Albedo)
	if w != edge/2 || f != gl.COMPRESSED_RGBA_BPTC_UNORM {
		t.Fatalf("skipped atlas: %d wide, format 0x%x; want %d, BPTC", w, f, edge/2)
	}
	if p := psnrBytes(smallRef, got); p < 35 {
		t.Errorf("skipped atlas PSNR %.2f dB against the small RGBA8 one, want >= 35", p)
	}
	if b := gfx.TextureVRAM(); b.Bytes <= 0 {
		t.Error("no VRAM counted")
	}
}

// A small atlas stays RGBA8 with compression on: below the world textures'
// minimum side, as a small world texture does.
func TestSplatAtlasSmallStaysRGBA8(t *testing.T) {
	splatAtlasEnv(t)
	dir := t.TempDir()
	base := filepath.Join(dir, "tiny")
	smoothPNG(t, base+".png", 64, 64)
	names := [4]string{base}
	gfx.TextureCompression = true
	a := gfx.SplatArraysFor(names)
	if a == nil {
		t.Fatal("no atlases")
	}
	if _, f := levelInfo(a.Albedo, 0); f == gl.COMPRESSED_RGBA_BPTC_UNORM {
		t.Error("a 136² atlas was compressed")
	}
	if r := gfx.TextureFormatOf(gfx.SplatAtlasVRAMKey(names) + ":albedo"); r != gfx.TextureRGBA8 {
		t.Errorf("recorded as %v", r)
	}
}

// The numbers for a real four-layer 2k set: build time and VRAM as RGBA8,
// the first (encoding) and second (cached) compressed builds.
func TestMeasureSplatAtlas(t *testing.T) {
	src := os.Getenv("FOUNDRY_SPLAT_MEASURE_DIR")
	if src == "" {
		t.Skip("set FOUNDRY_SPLAT_MEASURE_DIR to a textures folder holding the ph_ layers")
	}
	splatAtlasEnv(t)
	var names [4]string
	for i, n := range []string{"ph_red_sand", "ph_cracked_red_ground", "ph_red_laterite_soil_stones", "ph_rock_face"} {
		names[i] = filepath.Join(src, n)
		if _, err := os.Stat(names[i] + ".png"); err != nil {
			t.Skip(err)
		}
	}
	base := gfx.TextureVRAMBytes()
	build := func(label string) *gfx.SplatArrays {
		gfx.ForgetSplatArrays(names)
		t0 := time.Now()
		a := gfx.SplatArraysFor(names)
		gl.Finish()
		if a == nil {
			t.Fatalf("%s: no atlases", label)
		}
		t.Logf("%-22s %7s  VRAM %6.1f MB  (layer %d², gutter %d)", label, time.Since(t0).Round(time.Millisecond),
			float64(gfx.TextureVRAMBytes()-base)/(1<<20), a.Size, a.Gutter)
		return a
	}
	gfx.TextureCompression = false
	build("RGBA8")
	gfx.TextureCompression = true
	build("BC7, encoding")
	build("BC7, cached")
	gfx.TextureSkipMips = 1
	build("BC7 skip 1, cached")
}
