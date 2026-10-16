package genlevel

// splice_sounds_test.go — a generator's positional sounds (a river's flow,
// a waterfall's roar, docs/terrain-paint/PHASE_W5.md) are spliced like its
// emitters: the prefixed ones replaced, hand-placed ones kept, a foreign
// prefix refused, and each lands as a looping level sound.

import (
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/world"
)

func TestSpliceReplacesTheGeneratorsSounds(t *testing.T) {
	raw := world.RawLevel{Sounds: []world.RawSound{
		{ID: "hum", Clip: "hum"},
		{ID: "gen.valley.edit.flow_old_1", Clip: "water_river"},
	}}
	ss := []procgen.Sound{{ID: "gen.valley.edit.roar_brook_1", Clip: "water_falls",
		Position: procgen.Point3{X: 1, Y: 2, Z: 3}, Gain: 1.2, Pitch: 0.95, RefDistance: 4, MaxDistance: 60}}
	st, err := SpliceRaw(&raw, "valley", nil, nil, Options{Sounds: ss})
	if err != nil {
		t.Fatal(err)
	}
	if st.RemovedSounds != 1 || st.AddedSounds != 1 || !st.Changed() {
		t.Fatalf("stats %+v", st)
	}
	if len(raw.Sounds) != 2 || raw.Sounds[0].ID != "hum" {
		t.Fatalf("sounds %+v", raw.Sounds)
	}
	s, err := world.SoundFromRaw(raw.Sounds[1])
	if err != nil {
		t.Fatal(err)
	}
	if !s.Loop || !s.Enabled || !s.Playing || s.Gain != 1.2 || s.Pitch != 0.95 || s.RefDistance != 4 || s.MaxDistance != 60 || s.Position.Y() != 2 {
		t.Fatalf("spliced %+v", s)
	}
	if st, _ := SpliceRaw(&raw, "valley", nil, nil, Options{}); st.RemovedSounds != 1 || len(raw.Sounds) != 1 {
		t.Fatalf("second splice %+v, %d left", st, len(raw.Sounds))
	}
	if _, err := SpliceRaw(&raw, "valley", nil, nil, Options{Sounds: []procgen.Sound{{ID: "flow"}}}); err == nil {
		t.Fatal("an unprefixed sound was accepted")
	}
}
