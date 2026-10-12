package gfx

import (
	"math"
	"testing"
)

// The curve's inverse is its inverse over the display range, and the curve
// is what taa_output_frag.glsl applies: monotonic, 0 at 0, under 1 at 1
// (it rolls off rather than clips), and lifting the mid-tones.
func TestACESFilmInverseRoundTrips(t *testing.T) {
	for y := 0.0; y <= HDRDisplayMax; y += 0.037 {
		if got := ACESFilm(ACESFilmInverse(y)); math.Abs(got-y) > 1e-9 {
			t.Fatalf("ACESFilm(ACESFilmInverse(%.3f)) = %.6f", y, got)
		}
	}
	prev := -1.0
	for x := 0.0; x < 20; x += 0.05 {
		v := ACESFilm(x)
		if v < prev {
			t.Fatalf("ACESFilm falls at %.2f", x)
		}
		prev = v
	}
	if ACESFilm(0) != 0 || ACESFilm(1) > 0.85 || ACESFilm(0.18) < 0.2 {
		t.Errorf("curve: f(0)=%.3f f(1)=%.3f f(0.18)=%.3f", ACESFilm(0), ACESFilm(1), ACESFilm(0.18))
	}
}
