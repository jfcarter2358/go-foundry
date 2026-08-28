package levelio

// layer_save_test.go — a chunk's authoring layers across the YAML round trip.
//
// As with frames, the important half is the negative one. Layers exist for the
// minority of chunks that stack geometry; every other chunk — which is nearly
// all of them, and all of the ones authored before layers existed — must
// serialise exactly as it always did. `layer:` and `layers:` are both
// omitempty, and there is a live decision behind the second one (see
// chunkLayersToRaw) about when a chunk has anything worth writing at all.

import (
	"foundry/internal/geom"
	"foundry/internal/testenv"
	"foundry/internal/world"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

func TestSectorLayersRoundTrip(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	raw.ID = "cliff"
	// A second sector on a layer above the first, sharing its footprint.
	ledge := raw.Sectors[0]
	ledge.ID = "ledge"
	ledge.Layer = 1
	ledge.Floor = world.RawFloor{Enabled: true, Height: 6, Texture: "concrete"}
	ledge.Ceiling = world.RawCeiling{Enabled: true, Height: 10, Texture: "concrete"}
	raw.Sectors = append(raw.Sectors, ledge)
	raw.Layers = []world.RawChunkLayer{
		{Name: "Ground", BaseY: 0, CeilY: 4},
		{Name: "Ledge", BaseY: 6, CeilY: 10},
	}

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	if got := ch.Sectors[1].Layer; got != 1 {
		t.Errorf("the ledge loaded onto layer %d, want 1", got)
	}
	if ch.LayerCount() != 2 {
		t.Errorf("LayerCount = %d, want 2", ch.LayerCount())
	}
	if l := ch.LayerAt(1); l.Name != "Ledge" || l.BaseY != 6 || l.CeilY != 10 {
		t.Errorf("LayerAt(1) = %+v, want the saved Ledge band", l)
	}

	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	if back.Sectors[1].Layer != 1 {
		t.Errorf("sector layer did not survive the round trip: %d", back.Sectors[1].Layer)
	}
	if len(back.Layers) != 2 || back.Layers[1].Name != "Ledge" || back.Layers[1].BaseY != 6 {
		t.Errorf("layer metadata did not survive the round trip: %+v", back.Layers)
	}
}

func TestASingleLayerChunkWritesNoLayerKeys(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	raw.ID = "room"

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	if back.Layers != nil {
		t.Errorf("a chunk with one layer wrote layer metadata: %+v", back.Layers)
	}

	out, err := yaml.Marshal(back)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"layers:", "layer:"} {
		if strings.Contains(string(out), key) {
			t.Errorf("a chunk that never used layers emitted %q:\n%s", key, out)
		}
	}
}

// A layer index with no metadata behind it is valid on disk — the sector's own
// `layer:` is the durable fact. Loading one must not lose the sector's band, and
// saving it back must materialize the metadata the chunk was missing.
func TestSectorLayerWithoutMetadataSurvives(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	ledge := raw.Sectors[0]
	ledge.ID = "ledge"
	ledge.Layer = 1
	ledge.Floor = world.RawFloor{Enabled: true, Height: 6, Texture: "concrete"}
	ledge.Ceiling = world.RawCeiling{Enabled: true, Height: 10, Texture: "concrete"}
	raw.Sectors = append(raw.Sectors, ledge)
	raw.Layers = nil // no metadata block at all

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	if ch.LayerCount() != 2 {
		t.Fatalf("LayerCount = %d, want 2 derived from the sectors", ch.LayerCount())
	}

	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	if back.Sectors[1].Layer != 1 {
		t.Errorf("sector layer lost: %d", back.Sectors[1].Layer)
	}
	// Now that the chunk genuinely stacks, its bands are worth recording — and
	// the synthesized ones must match what the sectors actually occupy.
	if len(back.Layers) != 2 {
		t.Fatalf("a stacked chunk wrote %d layers, want 2", len(back.Layers))
	}
	if back.Layers[1].BaseY != 6 || back.Layers[1].CeilY != 10 {
		t.Errorf("synthesized band = %+v, want the ledge's own 6..10", back.Layers[1])
	}
}

func TestBiomeSectorLayerRoundTrips(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := squareRawChunk(false)
	raw.BiomeSectors = []world.RawBiomeSector{{
		ID:     "grass",
		Points: []geom.Point{{X: 1, Y: 1}, {X: 5, Y: 1}, {X: 5, Y: 5}},
		Layer:  1,
	}}

	ch, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	if got := ch.BiomeSectors[0].Layer; got != 1 {
		t.Fatalf("biome sector loaded onto layer %d, want 1", got)
	}
	back, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	if back.BiomeSectors[0].Layer != 1 {
		t.Errorf("biome sector layer did not survive the round trip: %d",
			back.BiomeSectors[0].Layer)
	}
}
