package main

import (
	"math"
	"testing"
)

// TestBiomeLODTierCount pins which brush kinds render three concentric LOD
// tiers (Near/Mid/Far — see grass.go's package doc comment) versus a single
// tier: only the dense-field foliage kinds get three; Rock (sparse,
// individually-read instances) and every non-scattering kind stay at one,
// unchanged from before LOD existed.
func TestBiomeLODTierCount(t *testing.T) {
	three := []int32{BiomeGrass, BiomeWildflower, BiomeCattail, BiomeMoss}
	for _, k := range three {
		if got := biomeLODTierCount(k); got != 3 {
			t.Errorf("biomeLODTierCount(%d) = %d, want 3", k, got)
		}
	}
	one := []int32{BiomeRock, BiomeSand, BiomeWater}
	for _, k := range one {
		if got := biomeLODTierCount(k); got != 1 {
			t.Errorf("biomeLODTierCount(%d) = %d, want 1", k, got)
		}
	}
}

// TestResolvedLODDefaults checks the "0 means the kind's default" resolution
// — defaultLOD1Fraction/defaultLOD2Fraction of the brush's own Range — the
// same convention every other zero-value brush field uses.
func TestResolvedLODDefaults(t *testing.T) {
	b := &BiomeBrush{Kind: BiomeGrass, Range: 100}
	if got, want := resolvedLOD1(b), float32(100)*defaultLOD1Fraction; got != want {
		t.Errorf("resolvedLOD1 = %v, want %v", got, want)
	}
	if got, want := resolvedLOD2(b), float32(100)*defaultLOD2Fraction; got != want {
		t.Errorf("resolvedLOD2 = %v, want %v", got, want)
	}
}

// TestResolvedLODOrdering asserts the invariant grass.go's per-tier draw
// calls rely on unconditionally: 0 < LOD1 <= LOD2 <= Range, even when a
// hand-edited save sets the fields out of order, past Range, or to a
// NaN/negative value.
func TestResolvedLODOrdering(t *testing.T) {
	cases := []struct {
		name       string
		lod1, lod2 float32
		rng        float32
	}{
		{"defaults", 0, 0, 60},
		{"lod1 past lod2's default", 55, 0, 60},  // lod2's default (0.7*60=42) is below lod1
		{"lod1 beyond range", 500, 0, 60},        // clamp to range
		{"lod2 beyond range", 10, 500, 60},       // clamp to range
		{"lod1 negative", -5, 0, 60},             // falls back to default fraction
		{"lod1 NaN", float32(math.NaN()), 0, 60}, // falls back to default fraction
		{"lod2 below lod1", 40, 10, 60},          // lod2 pulled up to lod1
		{"tiny range", 0, 0, 0.5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := &BiomeBrush{Kind: BiomeGrass, Range: c.rng, LOD1Distance: c.lod1, LOD2Distance: c.lod2}
			lod1 := resolvedLOD1(b)
			lod2 := resolvedLOD2(b)
			if !(lod1 > 0) {
				t.Fatalf("resolvedLOD1 = %v, want > 0", lod1)
			}
			if lod1 > c.rng {
				t.Errorf("resolvedLOD1 = %v, want <= range %v", lod1, c.rng)
			}
			if lod2 < lod1 {
				t.Errorf("resolvedLOD2 = %v, want >= resolvedLOD1 %v", lod2, lod1)
			}
			if lod2 > c.rng {
				t.Errorf("resolvedLOD2 = %v, want <= range %v", lod2, c.rng)
			}
		})
	}
}
