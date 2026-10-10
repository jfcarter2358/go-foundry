package texture

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/texdoc"
)

func writeTestImage(t *testing.T, name string, img image.Image) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fileutil.TexturesDir(), name+".png"), buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeTestPNG(t *testing.T, name string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	writeTestImage(t, name, img)
}

// bumpsNormalMap is the normal map of a smooth, tiling field of bumps — one
// that is exactly the slope of a surface, so it should come back almost
// unchanged.
func bumpsNormalMap(w, h int) *image.RGBA {
	hf := make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			u, v := 2*math.Pi*float64(x)/float64(w), 2*math.Pi*float64(y)/float64(h)
			hf[y*w+x] = float32(0.5 + 0.25*math.Sin(2*u)*math.Cos(3*v))
		}
	}
	return texdoc.NormalMapFromHeight(w, h, hf, 6)
}

// noiseNormalMap has independent random slopes per texel: not the gradient of
// anything, so no height field can give it back.
func noiseNormalMap(w, h int) *image.RGBA {
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		nx, ny := rng.Float64()*1.2-0.6, rng.Float64()*1.2-0.6
		inv := 1 / math.Sqrt(nx*nx+ny*ny+1)
		img.Pix[i*4] = uint8((nx*inv*0.5 + 0.5) * 255)
		img.Pix[i*4+1] = uint8((ny*inv*0.5 + 0.5) * 255)
		img.Pix[i*4+2] = uint8((inv*0.5 + 0.5) * 255)
		img.Pix[i*4+3] = 255
	}
	return img
}

func setupConvertDir(t *testing.T) {
	t.Helper()
	withTempTextureDir(t)
	if err := os.MkdirAll(fileutil.TexturesDir(), 0755); err != nil {
		t.Fatal(err)
	}
	writeTestPNG(t, "plain", 16, 16)
	writeTestPNG(t, "bumped", 64, 64)
	writeTestImage(t, "bumped_normal", bumpsNormalMap(64, 64))
	writeTestPNG(t, "noisy", 64, 64)
	writeTestImage(t, "noisy_normal", noiseNormalMap(64, 64))
	writeTestPNG(t, "mismatched", 32, 32)
	writeTestImage(t, "mismatched_normal", bumpsNormalMap(16, 16))
	// Not a PNG at all: must fail without stopping the rest.
	if err := os.WriteFile(filepath.Join(fileutil.TexturesDir(), "odd.png"), []byte("not a png"), 0644); err != nil {
		t.Fatal(err)
	}
	// Already converted: must be left alone.
	writeTestPNG(t, "done", 16, 16)
	d, err := ImportPNG(filepath.Join(fileutil.TexturesDir(), "done.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveTexDoc(d, "done"); err != nil {
		t.Fatal(err)
	}
}

func TestConvertPNGsToFtex(t *testing.T) {
	setupConvertDir(t)
	noisyBefore, _ := os.ReadFile(texNormalPNGPath("noisy"))

	r := ConvertPNGsToFtex(ConvertOptions{MaxNormalError: DefaultMaxNormalError})

	if len(r.Converted) != 2 || r.Converted[0] != "bumped" || r.Converted[1] != "plain" {
		t.Errorf("Converted = %v, want [bumped plain]", r.Converted)
	}
	if _, ok := r.Failed["odd"]; !ok {
		t.Errorf("odd (undecodable) did not fail: %+v", r)
	}
	for _, name := range []string{"noisy", "mismatched"} {
		if _, ok := r.Skipped[name]; !ok {
			t.Errorf("%s not skipped: %+v", name, r)
		}
		if _, err := os.Stat(FtexPath(name)); err == nil {
			t.Errorf("%s.ftex written for a skipped texture", name)
		}
	}
	if after, _ := os.ReadFile(texNormalPNGPath("noisy")); !bytes.Equal(after, noisyBefore) {
		t.Error("a skipped texture's normal map was rewritten")
	}
	if _, err := os.Stat(FtexPath("bumped_normal")); err == nil {
		t.Error("a normal map was converted as a texture of its own")
	}

	// The bumped texture's map came in as a bump channel, and the map saved
	// from it matches the original.
	if e := r.NormalError["bumped"]; e > 2 {
		t.Errorf("bumped normal map rebuilt %.2f° off, want under 2°", e)
	}
	d, err := ReadTexDoc(FtexPath("bumped"))
	if err != nil {
		t.Fatal(err)
	}
	if !d.HasBump() {
		t.Fatal("bumped.ftex has no bump channel")
	}
	if _, err := os.Stat(texNormalPNGPath("bumped")); err != nil {
		t.Errorf("bumped's normal map was not written back: %v", err)
	}
}

func TestConvertPNGsToFtexLenient(t *testing.T) {
	setupConvertDir(t)
	r := ConvertPNGsToFtex(ConvertOptions{})
	if _, ok := r.NormalError["noisy"]; !ok {
		t.Errorf("noisy not converted with no error limit: %+v", r)
	}
	if _, ok := r.Skipped["mismatched"]; !ok {
		t.Errorf("a size mismatch must be skipped even with no error limit: %+v", r)
	}
}

func TestHeightFromNormalMapFlat(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := 0; i < 64; i++ {
		c := texdoc.FlatNormalTexel
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = c.R, c.G, c.B, c.A
	}
	if _, _, ok := texdoc.HeightFromNormalMap(img); ok {
		t.Error("a flat normal map reported relief")
	}
}
