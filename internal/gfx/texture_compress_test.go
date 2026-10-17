package gfx

// texture_compress_test.go — the CPU half of texture compression as gfx
// drives it: the defaults, which textures qualify, how many levels skip-mips
// drops, and that a second preparation of the same file is a cache read.
// The GL half is texture_compress_gl_test.go.

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/texcompress"
)

// Nothing changes for existing content unless someone opts in.
func TestTextureMemorySettingsDefaultOff(t *testing.T) {
	if os.Getenv("FOUNDRY_TEXTURE_COMPRESSION") != "" || os.Getenv("FOUNDRY_TEXTURE_SKIP_MIPS") != "" {
		t.Skip("the environment overrides the defaults")
	}
	if TextureCompression {
		t.Error("texture compression is on by default")
	}
	if TextureSkipMips != 0 {
		t.Errorf("texture skip-mips defaults to %d, want 0", TextureSkipMips)
	}
}

func TestTextureSkipLevels(t *testing.T) {
	prev := TextureSkipMips
	t.Cleanup(func() { TextureSkipMips = prev })
	cases := []struct {
		skip, w, h, want int
	}{
		{0, 4096, 4096, 0},
		{1, 2048, 2048, 1},
		{3, 2048, 2048, 3},
		{3, 512, 512, 1},   // stops at 256
		{2, 1024, 512, 1},  // the short side decides
		{3, 256, 256, 0},   // under the 512 threshold
		{2, 4096, 300, 0},  // short side under 512
		{3, 4096, 1024, 2}, // 1024 -> 512 -> 256
	}
	for _, c := range cases {
		TextureSkipMips = c.skip
		if got := textureSkipLevels(c.w, c.h); got != c.want {
			t.Errorf("skip %d on %dx%d drops %d levels, want %d", c.skip, c.w, c.h, got, c.want)
		}
	}
}

func TestCompressionQualification(t *testing.T) {
	big := image.NewRGBA(image.Rect(0, 0, 256, 512))
	if _, ok := compressionFormatFor("textures/rock.png", big, false); ok {
		t.Error("qualified with compression off")
	}
	if f, ok := compressionFormatFor("textures/rock.png", big, true); !ok || f != texcompress.BC7 {
		t.Errorf("an albedo got %v, %v; want BC7", f, ok)
	}
	if f, ok := compressionFormatFor("textures/rock_normal.png", big, true); !ok || f != texcompress.BC5 {
		t.Errorf("a normal map got %v, %v; want BC5", f, ok)
	}
	if _, ok := compressionFormatFor("textures/icon.png", image.NewRGBA(image.Rect(0, 0, 128, 1024)), true); ok {
		t.Error("a texture under 256 on a side qualified")
	}
	if _, ok := compressionFormatFor("textures/odd.png", image.NewRGBA(image.Rect(0, 0, 258, 256)), true); ok {
		t.Error("a texture whose size is not a multiple of four qualified")
	}
}

// The second preparation of a file reads the cache instead of encoding, and an
// edited file encodes again.
func TestPrepareCompressedUsesTheCache(t *testing.T) {
	t.Setenv(texcompress.CacheDirEnv, t.TempDir())
	dir := t.TempDir()
	file := filepath.Join(dir, "rock.png")
	if err := os.WriteFile(file, []byte("version one"), 0o644); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7)
	}
	enc, hits := TextureCompressionStats.Encodes.Load(), TextureCompressionStats.CacheHits.Load()

	if prepareCompressed(file, img, true) == nil {
		t.Fatal("no chain")
	}
	if prepareCompressed(file, img, true) == nil {
		t.Fatal("no chain the second time")
	}
	if d := TextureCompressionStats.Encodes.Load() - enc; d != 1 {
		t.Errorf("%d encodes for two loads of one file, want 1", d)
	}
	if d := TextureCompressionStats.CacheHits.Load() - hits; d != 1 {
		t.Errorf("%d cache hits, want 1", d)
	}
	if err := os.WriteFile(file, []byte("version two"), 0o644); err != nil {
		t.Fatal(err)
	}
	prepareCompressed(file, img, true)
	if d := TextureCompressionStats.Encodes.Load() - enc; d != 2 {
		t.Errorf("an edited file did not encode again (%d encodes)", d)
	}
}

func TestRGBA8ChainBytes(t *testing.T) {
	// 4x2: 32 + 2x1 (8) + 1x1 (4).
	if got := rgba8ChainBytes(4, 2); got != 44 {
		t.Errorf("rgba8ChainBytes(4, 2) = %d, want 44", got)
	}
}
