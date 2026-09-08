package levelio

// water_sector_save_test.go — the two Water-sector settings that decide what a
// lake looks like across the YAML round trip.
//
// Both have a trap in the serialised form. `water_depth` is omitempty and
// signed, so a raised surface has to survive a writer that drops zero values
// and a reader that treats 0 as "use the default". `hide_lakebed` is an
// omitempty bool whose zero value is the pre-existing look, so a lake painted
// before it existed must load back with its lakebed still on.

import (
	"foundry/internal/geom"
	"foundry/internal/testenv"
	"foundry/internal/world"
	"testing"
)

// waterRawChunk is squareRawChunk with one Water sector painted in it.
func waterRawChunk(depth float32, hideLakebed bool) world.RawChunk {
	raw := squareRawChunk(false)
	raw.BiomeSectors = []world.RawBiomeSector{{
		ID:     "lake",
		Points: []geom.Point{{X: 2, Y: 2}, {X: 8, Y: 2}, {X: 8, Y: 8}, {X: 2, Y: 8}},
		Brush: world.RawBiomeBrush{
			Kind:        world.BiomeWater,
			WaterDepth:  depth,
			HideLakebed: hideLakebed,
		},
	}}
	return raw
}

func loadWaterBrush(t *testing.T, raw world.RawChunk) world.BiomeBrush {
	t.Helper()
	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	if len(ch.BiomeSectors) != 1 {
		t.Fatalf("loaded %d biome sectors, want 1", len(ch.BiomeSectors))
	}
	return ch.BiomeSectors[0].Brush
}

// TestRaisedWaterSurvivesTheRoundTrip is the one the omitempty writer could
// break: a negative depth is a deliberately raised surface, not an unset field.
func TestRaisedWaterSurvivesTheRoundTrip(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	if got := loadWaterBrush(t, waterRawChunk(-2.5, false)).WaterDepth; got != -2.5 {
		t.Fatalf("loaded depth %g, want -2.5", got)
	}

	ch, _, err := loadRawChunk(waterRawChunk(-2.5, false))
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	if got := back.BiomeSectors[0].Brush.WaterDepth; got != -2.5 {
		t.Errorf("saved depth %g, want -2.5", got)
	}
}

// TestLakebedToggleRoundTrips covers both directions, since only one of them
// is written at all: hidden must persist, and shown must stay the default that
// costs no YAML.
func TestLakebedToggleRoundTrips(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	if loadWaterBrush(t, waterRawChunk(1, false)).HideLakebed {
		t.Error("a sector with no hide_lakebed key loaded with the lakebed hidden")
	}
	if !loadWaterBrush(t, waterRawChunk(1, true)).HideLakebed {
		t.Error("hide_lakebed: true did not survive the load")
	}

	ch, _, err := loadRawChunk(waterRawChunk(1, true))
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	if !back.BiomeSectors[0].Brush.HideLakebed {
		t.Error("hide_lakebed was dropped on save")
	}
}
