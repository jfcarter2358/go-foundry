package genlevel

// splice_emitters_test.go — a generator's particle emitters (a waterfall's
// mist, docs/terrain-paint/PHASE_W4.md) are spliced like its scenery: the
// prefixed ones replaced, hand-placed ones kept, a foreign prefix refused.

import (
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/world"
)

func TestSpliceReplacesTheGeneratorsEmitters(t *testing.T) {
	raw := world.RawLevel{Emitters: []world.RawEmitter{
		{ID: "torch", Effect: "torch"},
		{ID: "gen.canyon.edit.mist_old_1", Effect: "waterfall_mist"},
		{ID: "gen.other.edit.mist_x_1", Effect: "waterfall_mist"},
	}}
	ems := []procgen.Emitter{{ID: "gen.canyon.edit.mist_brook_1_1", Effect: "waterfall_mist",
		Position: procgen.Point3{X: 1, Y: 2, Z: 3}, Rotation: procgen.Point3{Y: 0.5}, RateScale: 1.4}}
	st, err := SpliceRaw(&raw, "canyon", nil, nil, Options{Emitters: ems})
	if err != nil {
		t.Fatal(err)
	}
	if st.RemovedEmitters != 1 || st.AddedEmitters != 1 || !st.Changed() {
		t.Fatalf("stats %+v", st)
	}
	ids := map[string]world.RawEmitter{}
	for _, e := range raw.Emitters {
		ids[e.ID] = e
	}
	got, ok := ids["gen.canyon.edit.mist_brook_1_1"]
	if len(raw.Emitters) != 3 || ids["torch"].ID == "" || ids["gen.other.edit.mist_x_1"].ID == "" || !ok {
		t.Fatalf("emitters %+v", raw.Emitters)
	}
	if got.Position.Y != 2 || got.Rotation.Y != 0.5 || got.RateScale == nil || *got.RateScale != 1.4 {
		t.Fatalf("spliced %+v", got)
	}
	// A rerun with none takes the generator's away.
	if st, _ := SpliceRaw(&raw, "canyon", nil, nil, Options{}); st.RemovedEmitters != 1 || len(raw.Emitters) != 2 {
		t.Fatalf("second splice %+v, %d left", st, len(raw.Emitters))
	}
	if _, err := SpliceRaw(&raw, "canyon", nil, nil, Options{Emitters: []procgen.Emitter{{ID: "mist"}}}); err == nil {
		t.Fatal("an unprefixed emitter was accepted")
	}
}
