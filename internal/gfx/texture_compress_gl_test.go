package gfx_test

// texture_compress_gl_test.go — opt-in checks that a compressed texture
// really reaches the GPU in its block format and samples back close to its
// source, that skip-mips allocates the smaller level only, and that the VRAM
// accounting follows each upload.
//
//	FOUNDRY_GL_TEST=1 go test -run TestCompressed ./internal/gfx/

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-gl/gl/v4.3-core/gl"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/texcompress"
)

// smoothPNG writes a w x h smooth RGBA gradient and returns it.
func smoothPNG(t *testing.T, path string, w, h int) *image.RGBA {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := y*img.Stride + x*4
			img.Pix[o] = uint8(x * 255 / w)
			img.Pix[o+1] = uint8(127 + 100*math.Sin(float64(y)/20))
			img.Pix[o+2] = uint8((x + y) * 255 / (w + h))
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
	return img
}

// compressedTestEnv points the resources and the cache at temp directories
// and sets the two settings for the test.
func compressedTestEnv(t *testing.T, compress bool, skip int) string {
	t.Helper()
	gltest.Context(t)
	prevRes, prevC, prevS := fileutil.ResourcesPath, gfx.TextureCompression, gfx.TextureSkipMips
	fileutil.ResourcesPath = t.TempDir()
	t.Setenv(texcompress.CacheDirEnv, t.TempDir())
	gfx.TextureCompression, gfx.TextureSkipMips = compress, skip
	t.Cleanup(func() {
		fileutil.ResourcesPath, gfx.TextureCompression, gfx.TextureSkipMips = prevRes, prevC, prevS
	})
	dir := fileutil.TexturesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// levelInfo is the bound texture's level-0 width and internal format.
func levelInfo(tex uint32, level int32) (w, format int32) {
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.GetTexLevelParameteriv(gl.TEXTURE_2D, level, gl.TEXTURE_WIDTH, &w)
	gl.GetTexLevelParameteriv(gl.TEXTURE_2D, level, gl.TEXTURE_INTERNAL_FORMAT, &format)
	return
}

func TestCompressedTextureUploadsAndReadsBack(t *testing.T) {
	dir := compressedTestEnv(t, true, 0)
	src := smoothPNG(t, filepath.Join(dir, "ctest.png"), 256, 256)
	norm := smoothPNG(t, filepath.Join(dir, "ctest_normal.png"), 256, 256)

	before := gfx.TextureVRAM()
	if err := gfx.LoadTexture("ctest", filepath.Join(dir, "ctest.png")); err != nil {
		t.Fatal(err)
	}
	if n := gfx.TextureCompressionStats.Fallbacks.Load(); n != 0 {
		t.Fatalf("the driver refused a compressed upload (%d fallbacks)", n)
	}
	if f := gfx.TextureFormatOf("ctest"); f != gfx.TextureBC7 {
		t.Fatalf("the albedo uploaded as %v, want BC7", f)
	}
	if f := gfx.TextureFormatOf(gfx.NormalMapName("ctest")); f != gfx.TextureBC5 {
		t.Fatalf("the normal map uploaded as %v, want BC5", f)
	}
	tex := gfx.Textures["ctest"]
	if w, f := levelInfo(tex, 0); w != 256 || f != gl.COMPRESSED_RGBA_BPTC_UNORM {
		t.Errorf("level 0 is %d wide, format 0x%x", w, f)
	}

	// The driver decodes it back; it should be the source to within BC7's
	// error.
	got := make([]byte, 256*256*4)
	gl.GetTexImage(gl.TEXTURE_2D, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(got))
	var se float64
	for i := range got {
		d := float64(got[i]) - float64(src.Pix[i])
		se += d * d
	}
	psnr := 10 * math.Log10(255*255/(se/float64(len(got))))
	t.Logf("BC7 readback PSNR %.2f dB", psnr)
	if psnr < 35 {
		t.Errorf("BC7 readback PSNR %.2f dB, want >= 35", psnr)
	}
	// The normal map's red and green come back; blue is not stored.
	nrm := make([]byte, 256*256*4)
	gl.BindTexture(gl.TEXTURE_2D, gfx.Textures[gfx.NormalMapName("ctest")])
	gl.GetTexImage(gl.TEXTURE_2D, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(nrm))
	for _, i := range []int{0, 4 * (128*256 + 77), len(nrm) - 4} {
		for c := 0; c < 2; c++ {
			if d := int(nrm[i+c]) - int(norm.Pix[i+c]); d < -6 || d > 6 {
				t.Errorf("BC5 texel %d channel %d: %d, source %d", i/4, c, nrm[i+c], norm.Pix[i+c])
			}
		}
	}

	// The accounting: two 256² chains of a byte a texel, plus padding at the
	// 2x2 and 1x1 levels.
	var chain int64
	for w := 256; ; w /= 2 {
		chain += int64(texcompress.LevelBytes(w, w))
		if w == 1 {
			break
		}
	}
	after := gfx.TextureVRAM()
	if d := after.Bytes - before.Bytes; d != 2*chain {
		t.Errorf("VRAM grew by %d bytes, want %d", d, 2*chain)
	}
	if after.BC7-before.BC7 != 1 || after.BC5-before.BC5 != 1 {
		t.Errorf("format counts moved by BC7 %d, BC5 %d", after.BC7-before.BC7, after.BC5-before.BC5)
	}

	// A live edit replaces it with RGBA8, and the record follows.
	if err := gfx.UpdateTextureFromRGBA("ctest", src); err != nil {
		t.Fatal(err)
	}
	if f := gfx.TextureFormatOf("ctest"); f != gfx.TextureRGBA8 {
		t.Errorf("after a live edit the texture is recorded as %v", f)
	}
	if _, f := levelInfo(tex, 0); f == gl.COMPRESSED_RGBA_BPTC_UNORM {
		t.Error("a live edit left level 0 compressed")
	}
	var plain int64
	for w := 256; ; w /= 2 {
		plain += int64(w * w * 4)
		if w == 1 {
			break
		}
	}
	if d := gfx.TextureVRAM().Bytes - after.Bytes; d != plain-chain {
		t.Errorf("the live edit moved the count by %d, want %d", d, plain-chain)
	}
}

// Skip-mips on both paths: the top level is never allocated, and the count
// is of what is.
func TestCompressedAndPlainSkipMips(t *testing.T) {
	for _, compress := range []bool{false, true} {
		dir := compressedTestEnv(t, compress, 1)
		name := "skip_plain"
		if compress {
			name = "skip_compressed"
		}
		smoothPNG(t, filepath.Join(dir, name+".png"), 512, 512)
		before := gfx.TextureVRAMBytes()
		if err := gfx.LoadTexture(name, filepath.Join(dir, name+".png")); err != nil {
			t.Fatal(err)
		}
		w, f := levelInfo(gfx.Textures[name], 0)
		if w != 256 {
			t.Errorf("compress=%v: level 0 is %d wide, want 256", compress, w)
		}
		if compress != (f == gl.COMPRESSED_RGBA_BPTC_UNORM) {
			t.Errorf("compress=%v: level 0 format 0x%x", compress, f)
		}
		if gfx.TextureSizes[name] != [2]float32{512, 512} {
			t.Errorf("compress=%v: the recorded size is %v; it should stay the source's", compress, gfx.TextureSizes[name])
		}
		want := int64(256 * 256 * 4 * 4 / 3)
		if compress {
			want /= 4
		}
		if d := gfx.TextureVRAMBytes() - before; d < want*9/10 || d > want*11/10 {
			t.Errorf("compress=%v: counted %d bytes, want about %d", compress, d, want)
		}
	}
}

// Off, a large texture uploads exactly as before: RGBA8, full size.
func TestCompressedOffLeavesUploadsAlone(t *testing.T) {
	dir := compressedTestEnv(t, false, 0)
	smoothPNG(t, filepath.Join(dir, "plain.png"), 512, 512)
	if err := gfx.LoadTexture("plain", filepath.Join(dir, "plain.png")); err != nil {
		t.Fatal(err)
	}
	w, f := levelInfo(gfx.Textures["plain"], 0)
	if w != 512 || (f != gl.RGBA8 && f != gl.RGBA) {
		t.Errorf("level 0 is %d wide, format 0x%x; want 512, RGBA8", w, f)
	}
	if gfx.TextureFormatOf("plain") != gfx.TextureRGBA8 {
		t.Error("recorded as compressed")
	}
}
