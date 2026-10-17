package gfx_test

// material_fx_atlas_palette_gl_test.go — a region terrain's ground PALETTE
// atlas (material_fx_textures.go, docs/regions/PRESETS_1.md) on a real GL
// context: more than eight distinct layers pack eight across and as many
// rows as they need (20 layers: the 8×3), each layer in the tile its index
// says; the BC7 path takes it with the same geometry; and eight names or
// fewer are the eight-layer 4×2 they always were.
//
//	FOUNDRY_GL_TEST=1 go test -run TestSplatAtlasPalette ./internal/gfx/

import (
	"path/filepath"
	"testing"

	"github.com/go-gl/gl/v4.3-core/gl"

	"foundry/internal/gfx"
	"foundry/internal/material"
)

func TestSplatAtlasPalette(t *testing.T) {
	splatAtlasEnv(t)
	dir := t.TempDir()
	const size = 128
	var names [material.SplatAtlasMax]string
	for i := 0; i < 20; i++ {
		base := filepath.Join(dir, "p"+string(rune('a'+i)))
		variedPNG(t, base+".png", size, 3*i)
		variedPNG(t, base+"_height.png", size, 3*i+1)
		names[i] = base
	}
	a := gfx.SplatArraysForAtlas(names)
	if a == nil {
		t.Fatal("no atlases")
	}
	g := size / 32
	tile := size + 2*g
	if a.Cols != 8 || a.Rows != 3 || a.Size != size || a.Gutter != g {
		t.Fatalf("cols %d rows %d size %d gutter %d, want 8, 3, %d, %d", a.Cols, a.Rows, a.Size, a.Gutter, size, g)
	}
	for i := 0; i < 20; i++ {
		if a.Layer[i] != i {
			t.Fatalf("tiles %v", a.Layer)
		}
	}
	if a.Layer[20] != -1 {
		t.Fatalf("an unnamed palette entry has tile %d", a.Layer[20])
	}
	w, h, f, pix := atlasLevel0WH(a.Albedo)
	if int(w) != 8*tile || int(h) != 3*tile || (f != gl.RGBA8 && f != gl.RGBA) {
		t.Fatalf("albedo atlas %d×%d format 0x%x, want %d×%d RGBA8", w, h, f, 8*tile, 3*tile)
	}
	for _, n := range []int{0, 7, 9, 17, 19} {
		src := readPNGRGBA(t, names[n]+".png")
		hgt := readPNGRGBA(t, names[n]+"_height.png")
		for _, q := range [][2]int{{0, 0}, {17, 101}, {size - 1, size - 1}} {
			tx, ty := (n%8)*tile+g+q[0], (n/8)*tile+g+q[1]
			o := 4 * (ty*int(w) + tx)
			s := src.Pix[4*(q[1]*size+q[0]):]
			if pix[o] != s[0] || pix[o+1] != s[1] || pix[o+2] != s[2] || pix[o+3] != hgt.Pix[4*(q[1]*size+q[0])] {
				t.Fatalf("layer %d texel %v: atlas %v, layer %v", n, q, pix[o:o+4], s[:3])
			}
		}
	}
	plain := pix

	// BC7: the same geometry, close to the same pixels.
	gfx.ForgetSplatArraysPalette(names)
	gfx.TextureCompression = true
	fb := gfx.TextureCompressionStats.Fallbacks.Load()
	c := gfx.SplatArraysForAtlas(names)
	if c == nil {
		t.Fatal("no compressed atlases")
	}
	if n := gfx.TextureCompressionStats.Fallbacks.Load() - fb; n != 0 {
		t.Fatalf("the driver refused the 8×3 BC7 atlas (%d fallbacks)", n)
	}
	cw, ch, cf, cpix := atlasLevel0WH(c.Albedo)
	if cw != w || ch != h || cf != gl.COMPRESSED_RGBA_BPTC_UNORM {
		t.Fatalf("compressed atlas %d×%d format 0x%x, want %d×%d BPTC", cw, ch, cf, w, h)
	}
	if r := gfx.TextureFormatOf(gfx.SplatAtlasVRAMKeyPalette(names) + ":albedo"); r != gfx.TextureBC7 {
		t.Errorf("recorded as %v", r)
	}
	p := psnrBytes(plain, cpix)
	t.Logf("8×3 palette albedo atlas BC7 vs RGBA8: PSNR %.2f dB", p)
	if p < 35 {
		t.Errorf("PSNR %.2f dB, want >= 35", p)
	}

	// Eight names: the eight-layer 4×2, as before palettes.
	gfx.TextureCompression = false
	var eight [8]string
	copy(eight[:], names[:8])
	if e := gfx.SplatArraysFor8(eight); e == nil || e.Cols != 4 || e.Rows != 2 {
		t.Fatalf("eight layers: %+v", e)
	}
}
