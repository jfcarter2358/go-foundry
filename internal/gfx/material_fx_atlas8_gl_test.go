package gfx_test

// material_fx_atlas8_gl_test.go — the eight-layer splat's 4×2 atlases
// (material_fx_textures.go, docs/regions/PHASE_3.md) on a real GL context:
// more than four distinct layers pack four tiles across and two down, each
// layer in the tile its index says, at most FxArray8MaxSize a side; the
// BC7 path takes the non-square atlas (its chain halves both sides) and
// samples back close to the RGBA8 one; and a splat naming only four layers
// in an eight-name call is the classic 2×2, under the classic key.
//
//	FOUNDRY_GL_TEST=1 go test -run TestSplatAtlasEight ./internal/gfx/

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-gl/gl/v4.3-core/gl"

	"foundry/internal/gfx"
)

// atlasLevel0WH is tex's level-0 size, internal format and pixels (any
// aspect).
func atlasLevel0WH(tex uint32) (w, h, format int32, pix []byte) {
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.GetTexLevelParameteriv(gl.TEXTURE_2D, 0, gl.TEXTURE_WIDTH, &w)
	gl.GetTexLevelParameteriv(gl.TEXTURE_2D, 0, gl.TEXTURE_HEIGHT, &h)
	gl.GetTexLevelParameteriv(gl.TEXTURE_2D, 0, gl.TEXTURE_INTERNAL_FORMAT, &format)
	pix = make([]byte, int(w)*int(h)*4)
	gl.GetTexImage(gl.TEXTURE_2D, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(pix))
	gl.BindTexture(gl.TEXTURE_2D, 0)
	return
}

func TestSplatAtlasEightLayers(t *testing.T) {
	splatAtlasEnv(t)
	dir := t.TempDir()
	const size = 256
	// Six distinct layers over eight names (layer 8 repeats layer 2, layer
	// 7 is empty): six tiles, the 4×2.
	var names [8]string
	for i := 0; i < 6; i++ {
		base := filepath.Join(dir, "l"+string(rune('a'+i)))
		variedPNG(t, base+".png", size, 3*i)
		variedPNG(t, base+"_height.png", size, 3*i+1)
		names[i] = base
	}
	names[7] = names[1]
	gfx.TextureCompression, gfx.TextureSkipMips = false, 0
	a := gfx.SplatArraysFor8(names)
	if a == nil {
		t.Fatal("no atlases")
	}
	g := size / 32
	tile := size + 2*g
	if a.Cols != 4 || a.Size != size || a.Gutter != g {
		t.Fatalf("cols %d size %d gutter %d, want 4, %d, %d", a.Cols, a.Size, a.Gutter, size, g)
	}
	want := [8]int{0, 1, 2, 3, 4, 5, -1, 1}
	if [8]int(a.Layer[:8]) != want {
		t.Fatalf("tiles %v, want %v", a.Layer, want)
	}
	w, h, f, pix := atlasLevel0WH(a.Albedo)
	if int(w) != 4*tile || int(h) != 2*tile || (f != gl.RGBA8 && f != gl.RGBA) {
		t.Fatalf("albedo atlas %d×%d format 0x%x, want %d×%d RGBA8", w, h, f, 4*tile, 2*tile)
	}
	// Each layer is in its tile: tile n's texel (g+x, g+y) is the layer's
	// (x, y), with its height in alpha.
	for n := 0; n < 6; n++ {
		src := readPNGRGBA(t, names[n]+".png")
		hgt := readPNGRGBA(t, names[n]+"_height.png")
		for _, q := range [][2]int{{0, 0}, {17, 101}, {size - 1, size - 1}} {
			tx, ty := (n%4)*tile+g+q[0], (n/4)*tile+g+q[1]
			o := 4 * (ty*int(w) + tx)
			s := src.Pix[4*(q[1]*size+q[0]):]
			if pix[o] != s[0] || pix[o+1] != s[1] || pix[o+2] != s[2] || pix[o+3] != hgt.Pix[4*(q[1]*size+q[0])] {
				t.Fatalf("layer %d texel %v: atlas %v, layer %v height %d", n, q, pix[o:o+4], s[:3], hgt.Pix[4*(q[1]*size+q[0])])
			}
		}
	}
	plain := pix

	// BC7: the same geometry, close to the same pixels.
	gfx.ForgetSplatArrays8(names)
	gfx.TextureCompression = true
	fb := gfx.TextureCompressionStats.Fallbacks.Load()
	c := gfx.SplatArraysFor8(names)
	if c == nil {
		t.Fatal("no compressed atlases")
	}
	if n := gfx.TextureCompressionStats.Fallbacks.Load() - fb; n != 0 {
		t.Fatalf("the driver refused the 4×2 BC7 atlas (%d fallbacks)", n)
	}
	cw, ch, cf, cpix := atlasLevel0WH(c.Albedo)
	if cw != w || ch != h || cf != gl.COMPRESSED_RGBA_BPTC_UNORM {
		t.Fatalf("compressed atlas %d×%d format 0x%x, want %d×%d BPTC", cw, ch, cf, w, h)
	}
	if r := gfx.TextureFormatOf(gfx.SplatAtlasVRAMKey8(names) + ":albedo"); r != gfx.TextureBC7 {
		t.Errorf("recorded as %v", r)
	}
	p := psnrBytes(plain, cpix)
	t.Logf("4×2 albedo atlas BC7 vs RGBA8: PSNR %.2f dB", p)
	if p < 35 {
		t.Errorf("PSNR %.2f dB, want >= 35", p)
	}

	// Four names in an eight-name call: the 2×2, under the four-layer key.
	gfx.TextureCompression = false
	var four [8]string
	copy(four[:4], names[:4])
	b := gfx.SplatArraysFor8(four)
	if b == nil || b.Cols != 2 {
		t.Fatalf("four layers: %+v", b)
	}
	if gfx.SplatArraysFor([4]string{names[0], names[1], names[2], names[3]}) != b {
		t.Error("the four-name and eight-name asks for the same four layers built two atlases")
	}
}

// readPNGRGBA decodes a PNG the test wrote (opaque RGBA).
func readPNGRGBA(t *testing.T, path string) *image.RGBA {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	if n, ok := img.(*image.NRGBA); ok {
		return &image.RGBA{Pix: n.Pix, Stride: n.Stride, Rect: n.Rect}
	}
	t.Fatalf("%s: %T", path, img)
	return nil
}
