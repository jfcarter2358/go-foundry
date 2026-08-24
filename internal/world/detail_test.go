package world

import (
	"fmt"
	"math"
	"testing"
)

// TestDetailRawRoundTrip verifies a DetailSpec survives save→load unchanged.
func TestDetailRawRoundTrip(t *testing.T) {
	in := DetailSpec{
		Enabled: true, Preset: "rock", Seed: 42,
		Displace: true,
		Waves: [DetailMaxWaves]DetailWave{
			{Enabled: true, Amplitude: 0.35, Frequency: 0.6, Rotation: 0, TessLevel: 24},
			{Enabled: false, Amplitude: 0.1, Frequency: 1.5, Rotation: 47.5, TessLevel: 32},
			{Enabled: true, Amplitude: 0.05, Frequency: 3, Rotation: -120, TessLevel: 8},
		},
		Noise: true, NoiseAmplitude: 0.12, NoiseFrequency: 1.4, NoiseOctaves: 4, NoiseTessLevel: 20,
		EdgeTaper: 0.4,
		Scatter:   true, ScatterMesh: "rock", ScatterTex: "rock",
		ScatterDensity: 0.5, ScatterSpacing: 1.4, ScatterScale: 0.5,
		ScatterJitter: 0.8, ScatterRange: 60,
	}
	raw := DetailToRaw(in)
	if raw == nil {
		t.Fatal("DetailToRaw returned nil for an enabled spec")
	}
	out := DetailFromRaw(raw)
	if !out.Enabled || out.Preset != in.Preset || out.Seed != in.Seed ||
		out.Displace != in.Displace || out.Waves != in.Waves ||
		out.Noise != in.Noise || out.NoiseAmplitude != in.NoiseAmplitude ||
		out.NoiseFrequency != in.NoiseFrequency || out.NoiseOctaves != in.NoiseOctaves ||
		out.NoiseTessLevel != in.NoiseTessLevel || out.EdgeTaper != in.EdgeTaper ||
		out.Scatter != in.Scatter ||
		out.ScatterTex != in.ScatterTex || out.ScatterDensity != in.ScatterDensity ||
		out.ScatterSpacing != in.ScatterSpacing || out.ScatterScale != in.ScatterScale ||
		out.ScatterJitter != in.ScatterJitter || out.ScatterRange != in.ScatterRange {
		t.Fatalf("round-trip mismatch:\n in=%+v\nout=%+v", in, out)
	}
}

// TestDetailLegacyDisplaceMigrates verifies a pre-waves level's single noise
// layer (amplitude/frequency/octaves/tess_level) loads as the noise layer.
func TestDetailLegacyDisplaceMigrates(t *testing.T) {
	out := DetailFromRaw(&RawDetail{
		Preset: "rock", Seed: 7, Displace: true,
		Amplitude: 0.35, Frequency: 0.6, Octaves: 4, TessLevel: 24,
	})
	if !out.Noise || out.NoiseAmplitude != 0.35 || out.NoiseFrequency != 0.6 ||
		out.NoiseOctaves != 4 || out.NoiseTessLevel != 24 {
		t.Fatalf("legacy displace params did not migrate to the noise layer: %+v", out)
	}
	if out.TessLevelFor() != 24 {
		t.Fatalf("expected effective tessellation 24, got %.0f", out.TessLevelFor())
	}
	for i, w := range out.Waves {
		if w.Enabled {
			t.Fatalf("legacy load should leave wave %d disabled: %+v", i, w)
		}
	}
}

// ridgeSeparation returns how far apart two wave rotations are as ridge families,
// in degrees (0..90). A plane wave looks the same travelling forwards or
// backwards, so rotations 180 apart are the *same* pattern, not opposite ones.
func ridgeSeparation(a, b float32) float64 {
	d := math.Mod(math.Abs(float64(a-b)), 180)
	if d > 90 {
		d = 180 - d
	}
	return d
}

// TestDetailWaveRotationsDiffer verifies no two active waves land on nearly the
// same ridge direction. Near-parallel waves stack into one ridge family instead
// of reading as separate layers — which is what the old seed-hashed directions
// did (with the default seed, waves 1 and 2 came out 8 degrees apart and almost
// in phase). Covers both fresh slots and every preset.
func TestDetailWaveRotationsDiffer(t *testing.T) {
	const minSep = 15.0

	var defaults []float32
	for i := 0; i < DetailMaxWaves; i++ {
		defaults = append(defaults, DetailDefaultWave(i).Rotation)
	}
	checkSpread := func(what string, rots []float32) {
		for i := 0; i < len(rots); i++ {
			for j := i + 1; j < len(rots); j++ {
				if sep := ridgeSeparation(rots[i], rots[j]); sep < minSep {
					t.Fatalf("%s: waves %d and %d are only %.1f degrees apart (rotations %.1f, %.1f)",
						what, i+1, j+1, sep, rots[i], rots[j])
				}
			}
		}
	}
	checkSpread("default wave slots", defaults)

	for name, p := range DetailPresets {
		var rots []float32
		for _, w := range p.Waves {
			if w.Enabled {
				rots = append(rots, w.Rotation)
			}
		}
		checkSpread(fmt.Sprintf("preset %q", name), rots)
	}
}

// TestDetailTessLevelIsMaxOfActiveLayers verifies disabled layers do not drive
// the shared per-patch subdivision level.
func TestDetailTessLevelIsMaxOfActiveLayers(t *testing.T) {
	d := DetailSpec{
		Waves: [DetailMaxWaves]DetailWave{
			{Enabled: true, Amplitude: 0.2, Frequency: 0.5, TessLevel: 12},
			{Enabled: false, Amplitude: 0.2, Frequency: 0.5, TessLevel: 64},
			{Enabled: true, Amplitude: 0.2, Frequency: 0.5, TessLevel: 30},
		},
		Noise: false, NoiseTessLevel: 48,
	}
	if got := d.TessLevelFor(); got != 30 {
		t.Fatalf("expected 30 (max of enabled waves), got %.0f", got)
	}
	d.Noise = true
	if got := d.TessLevelFor(); got != 48 {
		t.Fatalf("expected 48 (noise layer active), got %.0f", got)
	}
}

// TestDetailDisabledOmitted verifies a disabled spec serializes to nil (omitted).
func TestDetailDisabledOmitted(t *testing.T) {
	if DetailToRaw(DetailSpec{}) != nil {
		t.Fatal("disabled DetailSpec should serialize to nil")
	}
	if DetailFromRaw(nil).Enabled {
		t.Fatal("nil RawDetail should load as disabled")
	}
}
