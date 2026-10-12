package gfx_test

// texture_compress_measure_gl_test.go — opt-in numbers for the texture memory
// settings: VRAM for a set of 2048² textures (albedos with normal maps) as
// RGBA8, compressed, and with skip-mips 1, and the load time of the first
// (encoding) and second (cached) compressed load.
//
//	FOUNDRY_GL_TEST=1 FOUNDRY_TEXCOMPRESS_MEASURE=1 go test -run TestMeasureTextureVRAM -v ./internal/gfx/

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"foundry/internal/gfx"
	"foundry/internal/testenv"
)

func TestMeasureTextureVRAM(t *testing.T) {
	if os.Getenv("FOUNDRY_TEXCOMPRESS_MEASURE") == "" {
		t.Skip("set FOUNDRY_TEXCOMPRESS_MEASURE=1 to measure")
	}
	dir := compressedTestEnv(t, false, 0)
	cache := os.Getenv("FOUNDRY_TEXCACHE_DIR")

	// Six 2048² albedos tiled up from the repo's 512² ground textures (real
	// texture statistics), each with a synthetic 2048² normal map.
	srcs, _ := filepath.Glob(filepath.Join(testenv.RepoRoot(t), "resources", "textures", "ground_*.png"))
	if len(srcs) < 6 {
		t.Skip("needs the repo's ground textures")
	}
	const n, size = 6, 2048
	var pngBytes int64
	for i := 0; i < n; i++ {
		f, err := os.Open(srcs[i])
		if err != nil {
			t.Fatal(err)
		}
		small, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		big := image.NewRGBA(image.Rect(0, 0, size, size))
		for y := 0; y < size; y += small.Bounds().Dy() {
			for x := 0; x < size; x += small.Bounds().Dx() {
				draw.Draw(big, image.Rect(x, y, x+small.Bounds().Dx(), y+small.Bounds().Dy()), small, image.Point{}, draw.Src)
			}
		}
		pngBytes += writeMeasurePNG(t, filepath.Join(dir, fmt.Sprintf("m%d.png", i)), big)
		nm := image.NewRGBA(big.Rect)
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				dx := 0.5 * math.Sin(float64(x)/37+float64(i))
				dy := 0.5 * math.Cos(float64(y)/23)
				l := math.Sqrt(dx*dx + dy*dy + 1)
				o := y*nm.Stride + x*4
				nm.Pix[o] = uint8((-dx/l*0.5 + 0.5) * 255)
				nm.Pix[o+1] = uint8((-dy/l*0.5 + 0.5) * 255)
				nm.Pix[o+2] = uint8((1/l*0.5 + 0.5) * 255)
				nm.Pix[o+3] = 255
			}
		}
		pngBytes += writeMeasurePNG(t, filepath.Join(dir, fmt.Sprintf("m%d_normal.png", i)), nm)
	}
	t.Logf("%d albedos + %d normal maps at %d², PNG total %.1f MB", n, n, size, float64(pngBytes)/(1<<20))

	run := func(label, prefix string, compress bool, skip int) {
		gfx.TextureCompression, gfx.TextureSkipMips = compress, skip
		before := gfx.TextureVRAM()
		start := time.Now()
		names := make([]string, n)
		for i := 0; i < n; i++ {
			// A fresh name per run so each run's textures are counted on
			// their own; the files (and so the cache keys) are shared.
			names[i] = fmt.Sprintf("%s%d", prefix, i)
			src := filepath.Join(dir, fmt.Sprintf("m%d.png", i))
			dst := filepath.Join(dir, names[i]+".png")
			os.Link(src, dst)
			os.Link(filepath.Join(dir, fmt.Sprintf("m%d_normal.png", i)), filepath.Join(dir, names[i]+"_normal.png"))
		}
		gfx.TextureNames = gfx.ScanTextures(dir)
		gfx.PrefetchTextures(names)
		for _, name := range names {
			if err := gfx.EnsureTextureLoaded(name); err != nil {
				t.Fatal(err)
			}
		}
		el := time.Since(start)
		after := gfx.TextureVRAM()
		t.Logf("%-34s VRAM %7.1f MB  (%d BC7, %d BC5)  load %6.0f ms", label,
			float64(after.Bytes-before.Bytes)/(1<<20), after.BC7-before.BC7, after.BC5-before.BC5, el.Seconds()*1000)
	}
	run("RGBA8 (default)", "a", false, 0)
	run("RGBA8, skip 1 mip", "b", false, 1)
	hits, enc := gfx.TextureCompressionStats.CacheHits.Load(), gfx.TextureCompressionStats.Encodes.Load()
	run("compressed, first load (encode)", "c", true, 0)
	run("compressed, second load (cache)", "d", true, 0)
	run("compressed, skip 1 mip (cache)", "e", true, 1)
	t.Logf("encodes %d, cache hits %d", gfx.TextureCompressionStats.Encodes.Load()-enc, gfx.TextureCompressionStats.CacheHits.Load()-hits)
	var cacheBytes int64
	entries, _ := os.ReadDir(cache)
	for _, e := range entries {
		if fi, err := e.Info(); err == nil {
			cacheBytes += fi.Size()
		}
	}
	t.Logf("cache: %d files, %.1f MB (PNG sources %.1f MB)", len(entries), float64(cacheBytes)/(1<<20), float64(pngBytes)/(1<<20))
}

func writeMeasurePNG(t *testing.T, path string, img *image.RGBA) int64 {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return int64(buf.Len())
}
