package gfx

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// The detail map is generated, not shipped: it must be the same bytes every
// time, tile seamlessly (the ripples are sampled with REPEAT at three scales),
// and carry slopes centred on flat.
func TestWaterDetailIsDeterministicAndTiles(t *testing.T) {
	a, b := WaterDetailPixels(), WaterDetailPixels()
	if !bytes.Equal(a, b) {
		t.Fatal("two generations differ")
	}
	n := waterDetailSize
	if len(a) != n*n*4 {
		t.Fatalf("%d bytes, want %d", len(a), n*n*4)
	}
	at := func(x, y, c int) float64 { return float64(a[((y%n)*n+x%n)*4+c]) }
	// Across the wrap the step is no bigger than an ordinary step inside.
	for c := 0; c < 4; c++ {
		var inside, wrap float64
		for y := 0; y < n; y++ {
			inside = math.Max(inside, math.Abs(at(n/2, y, c)-at(n/2-1, y, c)))
			wrap = math.Max(wrap, math.Abs(at(0, y, c)-at(n-1, y, c)))
		}
		if wrap > inside*1.6+4 {
			t.Errorf("channel %d: a step of %v across the wrap, %v inside", c, wrap, inside)
		}
	}
	// The slopes average to flat (128).
	for c := 0; c < 2; c++ {
		var sum float64
		for i := 0; i < n*n; i++ {
			sum += float64(a[i*4+c])
		}
		if m := sum / float64(n*n); math.Abs(m-127.5) > 4 {
			t.Errorf("slope channel %d averages %v, want ~127.5", c, m)
		}
	}
	if dir := os.Getenv("FOUNDRY_TEST_DUMP"); dir != "" {
		img := image.NewNRGBA(image.Rect(0, 0, n, n))
		for i := 0; i < n*n; i++ {
			img.Set(i%n, i/n, color.NRGBA{a[i*4], a[i*4+1], a[i*4+2], 255})
		}
		f, _ := os.Create(filepath.Join(dir, "water_detail.png"))
		png.Encode(f, img)
		f.Close()
	}
}

func TestWaterQualityNamesRoundTrip(t *testing.T) {
	for _, n := range WaterQualityNames() {
		q, ok := ParseWaterQuality(n)
		if !ok || q.String() != n {
			t.Errorf("%q -> %v %v", n, q, ok)
		}
	}
	if _, ok := ParseWaterQuality("ultra"); ok {
		t.Error("ultra parsed")
	}
}
