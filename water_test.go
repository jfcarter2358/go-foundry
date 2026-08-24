package main

import (
	"foundry/internal/gfx"
	"foundry/internal/world"
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
	raw := &world.RawWater{
		WaveAmplitude: want.WaveAmplitude,
		WaveLength:    want.WaveLength,
		WaveSpeed:     want.WaveSpeed,
		Reflectivity:  want.Reflectivity,
		ReflectColor: world.RawColor3{
			R: want.ReflectColor.X(), G: want.ReflectColor.Y(), B: want.ReflectColor.Z(),
		},
	}

	if got := world.WaterParamsFromRaw(raw); got != want {
		t.Fatalf("round trip changed params:\n got %+v\nwant %+v", got, want)
	}
}

// TestWaterParamsDefaultWhenAbsent covers sectors painted before these settings
// existed — they must load with a usable look rather than a flat, motionless
// surface with zero amplitude.
func TestWaterParamsDefaultWhenAbsent(t *testing.T) {
	got := world.WaterParamsFromRaw(nil)

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
	w := world.WaterSurface{
		Position:   mgl32.Vec3{10, 2, -4},
		BoundHalfX: 25,
		BoundHalfY: 0.1,
		BoundHalfZ: 40,
	}
	min, max := w.WorldAABB()
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
	w := world.WaterSurface{
		BoundHalfX: 5,
		BoundHalfY: 0.1,
		BoundHalfZ: 5,
		Water:      gfx.WaterParams{Enabled: true, WaveAmplitude: 0.4},
	}
	min, max := w.WorldAABB()
	if max.Y() < 0.5-1e-6 || min.Y() > -0.5+1e-6 {
		t.Errorf("Y bounds = [%g,%g], want at least ±0.5 (0.1 half + 0.4 crest)",
			min.Y(), max.Y())
	}

	w.Water.Enabled = false
	flatMin, flatMax := w.WorldAABB()
	if flatMax.Y() >= max.Y() || flatMin.Y() <= min.Y() {
		t.Error("disabling waves did not shrink the bounds back to the mesh")
	}
}
