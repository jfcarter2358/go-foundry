package texcompress

// measure_test.go — opt-in numbers: encode time at 2k and 4k, cache size
// against the PNG, and quality on the repo's own ground textures.
//
//	FOUNDRY_TEXCOMPRESS_MEASURE=1 go test -run TestMeasure -v ./internal/texcompress/
//
// FOUNDRY_TEXCOMPRESS_SRC may name a directory of PNGs to measure instead of
// resources/textures (a Poly Haven download, say).

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMeasureEncode(t *testing.T) {
	if os.Getenv("FOUNDRY_TEXCOMPRESS_MEASURE") == "" {
		t.Skip("set FOUNDRY_TEXCOMPRESS_MEASURE=1 to measure")
	}
	dir := os.Getenv("FOUNDRY_TEXCOMPRESS_SRC")
	if dir == "" {
		dir = filepath.Join("..", "..", "resources", "textures")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.png"))
	var big *image.RGBA
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		src, err := png.Decode(bytes.NewReader(data))
		if err != nil || src.Bounds().Dx() < 256 {
			continue
		}
		img := image.NewRGBA(src.Bounds())
		draw.Draw(img, img.Bounds(), src, image.Point{}, draw.Src)
		f7 := BC7
		if filepath.Ext(f) == ".png" && len(f) > 11 && f[len(f)-11:] == "_normal.png" {
			f7 = BC5
		}
		start := time.Now()
		tex, err := Encode(img, f7, 0)
		if err != nil {
			t.Fatal(err)
		}
		el := time.Since(start)
		dec, _ := DecodeLevel(f7, tex.Levels[0])
		ch := 4
		if f7 == BC5 {
			ch = 2
		}
		t.Logf("%-28s %4dx%-4d %v  %6.1f ms  PNG %7d B  chain %7d B  PSNR %.2f dB",
			filepath.Base(f), img.Rect.Dx(), img.Rect.Dy(), f7, el.Seconds()*1000, len(data), tex.Bytes(), psnr(img, dec, ch))
		if big == nil || img.Rect.Dx()*img.Rect.Dy() > big.Rect.Dx()*big.Rect.Dy() {
			big = img
		}
	}
	// 2k and 4k: the largest source tiled up (real texture statistics, not a
	// synthetic gradient), encoded twice so the second number is warm.
	if big == nil {
		big = noisyImage(512, 512, 12, 3)
	}
	for _, size := range []int{2048, 4096} {
		img := image.NewRGBA(image.Rect(0, 0, size, size))
		for y := 0; y < size; y += big.Rect.Dy() {
			for x := 0; x < size; x += big.Rect.Dx() {
				draw.Draw(img, image.Rect(x, y, x+big.Rect.Dx(), y+big.Rect.Dy()), big, image.Point{}, draw.Src)
			}
		}
		for _, f := range []Format{BC7, BC5} {
			start := time.Now()
			tex, err := Encode(img, f, 0)
			if err != nil {
				t.Fatal(err)
			}
			el := time.Since(start)
			var buf bytes.Buffer
			png.Encode(&buf, img)
			t.Logf("%dx%d %v: encode %.0f ms (chain of %d levels, %.2f MB; RGBA8+mips %.2f MB; PNG %.2f MB)",
				size, size, f, el.Seconds()*1000, len(tex.Levels), float64(tex.Bytes())/(1<<20),
				float64(size*size*4)*4/3/(1<<20), float64(buf.Len())/(1<<20))
		}
	}
}
