package world

import (
	"foundry/internal/gfx"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// TestWaterParamsRoundTrip checks a configured water sector survives save/load.
// The save side is inline in editor_save.go, so this mirrors it rather than
// calling it — the point is that WaterParamsFromRaw reads back every field the
// writer puts out.
func TestWaterParamsRoundTrip(t *testing.T) {
	want := gfx.WaterParams{
		Enabled:       true,
		WaveAmplitude: 0.25,
		WaveLength:    7.5,
		WaveSpeed:     1.25,
		Reflectivity:  0.8,
		ReflectColor:  mgl32.Vec3{0.1, 0.2, 0.3},
	}
	raw := &RawWater{
		WaveAmplitude: want.WaveAmplitude,
		WaveLength:    want.WaveLength,
		WaveSpeed:     want.WaveSpeed,
		Reflectivity:  want.Reflectivity,
		ReflectColor: RawColor3{
			R: want.ReflectColor.X(), G: want.ReflectColor.Y(), B: want.ReflectColor.Z(),
		},
	}

	if got := WaterParamsFromRaw(raw); got != want {
		t.Fatalf("round trip changed params:\n got %+v\nwant %+v", got, want)
	}
}

// TestWaterParamsDefaultWhenAbsent covers sectors painted before these settings
// existed — they must load with a usable look rather than a flat, motionless
// surface with zero amplitude.
func TestWaterParamsDefaultWhenAbsent(t *testing.T) {
	got := WaterParamsFromRaw(nil)

	if !got.Enabled {
		t.Fatal("water params from absent YAML block are disabled")
	}
	if got.WaveAmplitude <= 0 || got.WaveLength <= 0 {
		t.Fatalf("defaults leave the surface flat: %+v", got)
	}
}

// TestWaterSurfaceAABBCoversFullExtent is the regression test for the bug where
// a water plane vanished once its centre left the view: culling used the owning
// sector's bounds, so the plane's own extent never entered the decision. The
// AABB must span the whole surface, not just a region around the centre.
func TestWaterSurfaceAABBCoversFullExtent(t *testing.T) {
	w := WaterSurface{
		Position:   mgl32.Vec3{10, 2, -4},
		BoundHalfX: 25,
		BoundHalfY: 0.1,
		BoundHalfZ: 40,
	}
	min, max := w.LocalAABB()
	if min.X() != -15 || max.X() != 35 {
		t.Errorf("X bounds = [%g,%g], want [-15,35]", min.X(), max.X())
	}
	if min.Z() != -44 || max.Z() != 36 {
		t.Errorf("Z bounds = [%g,%g], want [-44,36]", min.Z(), max.Z())
	}
}

// TestWaterSurfaceAABBIncludesWaveCrests keeps a wave from poking out of the
// box it is culled against: displacement happens in the shader, so the CPU-side
// bounds have to allow for it explicitly.
func TestWaterSurfaceAABBIncludesWaveCrests(t *testing.T) {
	w := WaterSurface{
		BoundHalfX: 5,
		BoundHalfY: 0.1,
		BoundHalfZ: 5,
		Water:      gfx.WaterParams{Enabled: true, WaveAmplitude: 0.4},
	}
	min, max := w.LocalAABB()
	if max.Y() < 0.5-1e-6 || min.Y() > -0.5+1e-6 {
		t.Errorf("Y bounds = [%g,%g], want at least ±0.5 (0.1 half + 0.4 crest)",
			min.Y(), max.Y())
	}

	w.Water.Enabled = false
	flatMin, flatMax := w.LocalAABB()
	if flatMax.Y() >= max.Y() || flatMin.Y() <= min.Y() {
		t.Error("disabling waves did not shrink the bounds back to the mesh")
	}
}

// TestResolvedWaterDepthKeepsNegative is the whole point of the signed depth:
// only exactly 0 means "use the default", so a surface deliberately raised
// above the floor it was sampled against survives the resolver instead of
// being snapped back under it.
func TestResolvedWaterDepthKeepsNegative(t *testing.T) {
	cases := []struct {
		in, want float32
	}{
		{0, DefaultWaterDepth},
		{0.6, 0.6},
		{-1.5, -1.5},
		{-0.0001, -0.0001},
	}
	for _, c := range cases {
		if got := ResolvedWaterDepth(c.in); got != c.want {
			t.Errorf("ResolvedWaterDepth(%g) = %g, want %g", c.in, got, c.want)
		}
	}
}

// TestNormalizeWaterDepthAvoidsTheSentinel covers the one value a drag or a
// slider must never come to rest on: storing an exact 0 would be read back as
// "use the default", so the surface would jump to DefaultWaterDepth the moment
// it was dragged level with the floor.
func TestNormalizeWaterDepthAvoidsTheSentinel(t *testing.T) {
	got := NormalizeWaterDepth(0)
	if got == 0 {
		t.Fatal("NormalizeWaterDepth(0) stored the default sentinel")
	}
	if ResolvedWaterDepth(got) != got {
		t.Errorf("normalized 0 to %g, which resolves to %g", got, ResolvedWaterDepth(got))
	}
	// Far below what the "%.2f" readouts can show, so the nudge is invisible.
	if got > 0.005 || got < -0.005 {
		t.Errorf("normalized 0 to %g, which is large enough to see", got)
	}
}

// TestNormalizeWaterDepthClamps keeps a runaway drag or a script typo from
// parking the plane somewhere it can never be found again.
func TestNormalizeWaterDepthClamps(t *testing.T) {
	if got := NormalizeWaterDepth(1e9); got != WaterDepthLimit {
		t.Errorf("NormalizeWaterDepth(1e9) = %g, want %g", got, WaterDepthLimit)
	}
	if got := NormalizeWaterDepth(-1e9); got != -WaterDepthLimit {
		t.Errorf("NormalizeWaterDepth(-1e9) = %g, want %g", got, -WaterDepthLimit)
	}
	if got := NormalizeWaterDepth(2.5); got != 2.5 {
		t.Errorf("NormalizeWaterDepth(2.5) = %g, want it untouched", got)
	}
}
