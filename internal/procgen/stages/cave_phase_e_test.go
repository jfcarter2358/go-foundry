package stages

import (
	"testing"

	"foundry/internal/procgen"
)

// TestCaveChunkerKeepsMouthsByPlace: with a chunk named, the cave's
// placements stream with it except within reach of a mouth (seen from
// outside), which stay chunk: auto; with none named, every one is auto.
func TestCaveChunkerKeepsMouthsByPlace(t *testing.T) {
	mouths := []CaveMouth{{Position: [3]float64{0, 10, 0}}, {Position: [3]float64{200, 90, 0}}}
	at := caveChunker("cave", 28, mouths)
	if got := at(100, 50, 0); got != "cave" {
		t.Errorf("deep inside: %q, want cave", got)
	}
	if got := at(10, 12, 5); got != procgen.ChunkAuto {
		t.Errorf("by the first mouth: %q, want auto", got)
	}
	if got := at(190, 88, 0); got != procgen.ChunkAuto {
		t.Errorf("by the second mouth: %q, want auto", got)
	}
	if got := caveChunker("", 28, mouths)(100, 50, 0); got != procgen.ChunkAuto {
		t.Errorf("no chunk named: %q, want auto", got)
	}
}

// TestCaveAmbientVolumesEncloseTheInterior: one ambient volume per
// interior box, grown by the margin, keeping more of the ambient where its
// samples see sky.
func TestCaveAmbientVolumesEncloseTheInterior(t *testing.T) {
	a := &CaveArtifact{
		Interior: []CaveBox{
			{Min: [3]float64{0, 0, 0}, Max: [3]float64{16, 8, 6}},
			{Min: [3]float64{16, 0, 0}, Max: [3]float64{32, 8, 6}},
		},
		Samples: []CaveSample{
			{Position: [3]float64{8, 2, 3}, Sky: 0},
			{Position: [3]float64{24, 2, 3}, Sky: 0.09},
		},
	}
	p := procgen.Params{
		"ambient_margin": {Num: 4}, "ambient_sky": {Num: 0.1}, "ambient_ground": {Num: 0.15},
		"ambient_fade": {Num: 3}, "ambient_dust": {Num: 0.02},
	}
	vols := caveAmbientVolumes(a, p)
	if len(vols) != 2 {
		t.Fatalf("%d volumes, want 2", len(vols))
	}
	half := vols[0]["half"].(map[string]interface{})
	if half["x"].(float64) != 12 || half["y"].(float64) != 8 {
		t.Errorf("first volume's half %v, want the box grown by 4 (12, 8, 7)", half)
	}
	dark := vols[0]["ambient"].(map[string]interface{})["sky"].(float64)
	lit := vols[1]["ambient"].(map[string]interface{})["sky"].(float64)
	if dark != 0.1 || lit <= dark {
		t.Errorf("sky kept: deep %g (want 0.1), by the sky %g (want more)", dark, lit)
	}
}
