package genlevel

// region_splice_test.go — a scoped splice replaces only the entries under
// its prefixes (region_splice.go).

import (
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/world"
)

func TestScopedSpliceKeepsTheOtherRegions(t *testing.T) {
	raw := world.RawLevel{
		Scenery: []world.RawScenery{
			{ID: "barrel"},
			{ID: "gen.w.r_a_scatter.r1", Model: "old_a"},
			{ID: "gen.w.r_a_b_scatter.r1", Model: "old_ab"}, // region a_b: not region a's
			{ID: "gen.w.r_b_scatter.r1", Model: "old_b"},
			{ID: "gen.w.ground.tile_0_0", Model: "old_tile"},
		},
		Volumes: []world.RawVolume{{ID: "gen.w.r_a_plants.p1"}, {ID: "gen.w.r_b_plants.p1"}},
	}
	scope := []string{procgen.StagePrefix("w", "r_a_scatter"), procgen.StagePrefix("w", "r_a_plants")}
	st, err := SpliceRaw(&raw, "w", []procgen.Placement{
		{ID: "gen.w.r_a_scatter.r1", Model: "new_a"}, {ID: "gen.w.r_a_scatter.r2", Model: "new_a"},
		{ID: "gen.w.r_b_scatter.r1", Model: "new_b"}, {ID: "gen.w.ground.tile_0_0", Model: "new_tile"},
	}, []procgen.VolumeEntry{{"id": "gen.w.r_a_plants.p2"}, {"id": "gen.w.r_b_plants.p9"}}, Options{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if st != (Stats{RemovedScenery: 1, AddedScenery: 2, RemovedVolumes: 1, AddedVolumes: 1}) {
		t.Errorf("stats %+v", st)
	}
	models := map[string]string{}
	for _, s := range raw.Scenery {
		models[s.ID] = s.Model
	}
	want := map[string]string{"barrel": "", "gen.w.r_a_scatter.r1": "new_a", "gen.w.r_a_scatter.r2": "new_a",
		"gen.w.r_a_b_scatter.r1": "old_ab", "gen.w.r_b_scatter.r1": "old_b", "gen.w.ground.tile_0_0": "old_tile"}
	if len(models) != len(want) {
		t.Errorf("scenery %v", models)
	}
	for id, m := range want {
		if models[id] != m {
			t.Errorf("%s: model %q, want %q", id, models[id], m)
		}
	}
	if len(raw.Volumes) != 2 || raw.Volumes[0].ID != "gen.w.r_b_plants.p1" || raw.Volumes[1].ID != "gen.w.r_a_plants.p2" {
		t.Errorf("volumes %+v", raw.Volumes)
	}
	// An empty scope (not nil) touches nothing of the generator's.
	before := len(raw.Scenery)
	if st, err := SpliceRaw(&raw, "w", nil, nil, Options{Scope: []string{}}); err != nil || st.Changed() || len(raw.Scenery) != before {
		t.Errorf("an empty scope changed the level: %+v %v", st, err)
	}
}
