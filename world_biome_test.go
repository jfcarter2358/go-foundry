package main

import (
	"foundry/internal/geom"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// TestBiomeSectorBrushIsOwnedBySector checks that mutating one sector's brush
// never affects another sector — there's no shared/frozen-layer indirection
// in the new model, so this is structurally guaranteed, but worth pinning.
func TestBiomeSectorBrushIsOwnedBySector(t *testing.T) {
	a := BiomeSector{ID: "a", Brush: BiomeBrush{Kind: BiomeGrass, Density: 0.5}}
	b := BiomeSector{ID: "b", Brush: BiomeBrush{Kind: BiomeGrass, Density: 0.5}}

	a.Brush.Density = 0.9
	a.Brush.Kind = BiomeRock

	if b.Brush.Density != 0.5 || b.Brush.Kind != BiomeGrass {
		t.Fatalf("editing sector a's brush leaked into sector b: %+v", b.Brush)
	}
}

// TestBiomeSeedDeterministicAndDecorrelated covers the "different config ->
// different placement" requirement at the unit level: biomeSeed must be
// stable for a given (ID, Brush) and differ when either changes, since the
// shader mixes it into the scatter hash.
func TestBiomeSeedDeterministicAndDecorrelated(t *testing.T) {
	base := BiomeSector{ID: "s0", Brush: BiomeBrush{Kind: BiomeGrass, Density: 0.8, GrassHeight: 0.5}}

	s1 := base
	s2 := base
	if biomeSeed(&s1) != biomeSeed(&s2) {
		t.Fatal("identical (ID, Brush) should produce the same seed")
	}

	tall := base
	tall.Brush.GrassHeight = 2.5
	if biomeSeed(&base) == biomeSeed(&tall) {
		t.Fatal("different brush config (grass height) should produce a different seed")
	}

	other := base
	other.ID = "s1"
	if biomeSeed(&base) == biomeSeed(&other) {
		t.Fatal("different sector ID should produce a different seed")
	}
}

// TestBiomeSectorPointInPolygon checks winding/point-in-polygon sanity for a
// hand-built polygon, using the same helpers the editor relies on for
// selection (biomeSectorAt) and the shader's CPU-side counterpart.
func TestBiomeSectorPointInPolygon(t *testing.T) {
	pts := []mgl32.Vec2{{-2, -2}, {2, -2}, {2, 2}, {-2, 2}}
	if signedArea2D(pts) < 0 {
		t.Fatal("test polygon should already be CCW")
	}
	if !geom.PointInPolygon(mgl32.Vec2{0, 0}, pts) {
		t.Fatal("origin should be inside the square")
	}
	if geom.PointInPolygon(mgl32.Vec2{10, 10}, pts) {
		t.Fatal("far point should be outside the square")
	}

	ch := &Chunk{BiomeSectors: []BiomeSector{{ID: "sq", Points: pts}}}
	if idx := biomeSectorAt(ch, 0, 0); idx != 0 {
		t.Fatalf("expected sector 0 under the cursor, got %d", idx)
	}
	if idx := biomeSectorAt(ch, 10, 10); idx != -1 {
		t.Fatalf("expected no sector under a far point, got %d", idx)
	}
}

// TestFloorHeightUnderLevel checks that biome scatter height-sampling follows
// the structural floor (including elevation) and falls back to 0 over open
// ground with no sector at all.
func TestFloorHeightUnderLevel(t *testing.T) {
	ch := Chunk{
		Points: []mgl32.Vec2{{-4, -4}, {4, -4}, {4, 4}, {-4, 4}},
		Sectors: []Sector{{
			ID:           "s0",
			PointIndices: []int{0, 1, 2, 3},
			Floor:        Floor{Enabled: true, Height: 3},
		}},
	}
	lvl := &Level{Chunks: []Chunk{ch}}

	if h := floorHeightUnderLevel(lvl, 0, 0); h != 3 {
		t.Fatalf("expected floor height 3 inside the sector, got %v", h)
	}
	if h := floorHeightUnderLevel(lvl, 100, 100); h != 0 {
		t.Fatalf("expected 0 over open ground, got %v", h)
	}
}

// TestChunkCloneDeepCopiesBiomeSectors ensures a cloned chunk's biome sectors
// are independent of the source's.
func TestChunkCloneDeepCopiesBiomeSectors(t *testing.T) {
	src := Chunk{
		ID: "orig",
		BiomeSectors: []BiomeSector{{
			ID:     "b0",
			Points: []mgl32.Vec2{{0, 0}, {1, 0}, {1, 1}},
			Brush:  BiomeBrush{Kind: BiomeGrass, Density: 0.5},
		}},
	}

	clone := src.Clone("copy", "_c")
	if len(clone.BiomeSectors) != 1 {
		t.Fatalf("expected 1 cloned biome sector, got %d", len(clone.BiomeSectors))
	}

	clone.BiomeSectors[0].Points[0] = mgl32.Vec2{99, 99}
	clone.BiomeSectors[0].Brush.Density = 0.1

	if src.BiomeSectors[0].Points[0] != (mgl32.Vec2{0, 0}) {
		t.Fatal("mutating the clone's points affected the source")
	}
	if src.BiomeSectors[0].Brush.Density != 0.5 {
		t.Fatal("mutating the clone's brush affected the source")
	}
	if clone.BiomeSectors[0].ID == src.BiomeSectors[0].ID {
		t.Fatal("cloned sector ID should be suffixed to stay unique")
	}
}
