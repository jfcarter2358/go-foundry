package genlevel

// splice_test.go — a splice replaces exactly the generator's entries.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/world"

	"gopkg.in/yaml.v2"
)

func handMade() world.RawLevel {
	return world.RawLevel{
		Scenery: []world.RawScenery{
			{ID: "barrel", Model: "models/barrel.fmdl"},
			{ID: "gen.canyon.old.r1", Model: "generated/canyon/old/x.fnm"},
			{ID: "gen.other.talus.r1", Model: "generated/other/talus/x.fnm"},
			{ID: "crate", Model: "models/crate.fmdl"},
		},
		Volumes: []world.RawVolume{{ID: "door"}, {ID: "gen.canyon.veg.v1"}},
	}
}

func TestSpliceReplacesOnlyThePrefix(t *testing.T) {
	raw := handMade()
	solid := false
	st, err := SpliceRaw(&raw, "canyon", []procgen.Placement{
		{ID: "gen.canyon.talus.r1", Model: "a.fnm", Position: procgen.Point3{X: 1, Y: 2, Z: 3},
			Scale: &procgen.Point3{X: 2, Y: 2, Z: 2}, Solid: &solid, Collision: "hulls",
			Materials: map[string]string{"stone": "sandstone"}},
		{ID: "gen.canyon.talus.r2", Model: "a.fnm", Chunk: procgen.ChunkAuto},
	}, []procgen.VolumeEntry{{"id": "gen.canyon.veg.v9", "shape": "sphere"}},
		Options{ChunkAt: func(x, y, z float32) string { return "ground" }})
	if err != nil {
		t.Fatal(err)
	}
	if st != (Stats{RemovedScenery: 1, AddedScenery: 2, RemovedVolumes: 1, AddedVolumes: 1}) {
		t.Errorf("stats %+v", st)
	}
	var ids []string
	for _, s := range raw.Scenery {
		ids = append(ids, s.ID)
	}
	if got := strings.Join(ids, ","); got != "barrel,gen.other.talus.r1,crate,gen.canyon.talus.r1,gen.canyon.talus.r2" {
		t.Errorf("scenery after the splice: %s", got)
	}
	r1 := raw.Scenery[3]
	if r1.Position != (world.RawVec3{X: 1, Y: 2, Z: 3}) || r1.Scale == nil || r1.Scale.X != 2 ||
		r1.Solid == nil || *r1.Solid || r1.Collision != "hulls" || r1.Materials["stone"] != "sandstone" {
		t.Errorf("a placement lost a field: %+v", r1)
	}
	if raw.Scenery[4].Chunk != "ground" {
		t.Errorf("chunk: auto resolved to %q", raw.Scenery[4].Chunk)
	}
	if len(raw.Volumes) != 2 || raw.Volumes[0].ID != "door" || raw.Volumes[1].Shape != "sphere" {
		t.Errorf("volumes %+v", raw.Volumes)
	}
	if sc, vo := Count(&raw, "canyon"); sc != 2 || vo != 1 {
		t.Errorf("Count %d %d", sc, vo)
	}

	// An empty run removes everything the generator placed.
	st, _ = SpliceRaw(&raw, "canyon", nil, nil, Options{})
	if st.RemovedScenery != 2 || st.RemovedVolumes != 1 || len(raw.Scenery) != 3 {
		t.Errorf("an empty splice: %+v, %d left", st, len(raw.Scenery))
	}
}

func TestSpliceRefusesUnprefixedAndBadVolumes(t *testing.T) {
	raw := handMade()
	if _, err := SpliceRaw(&raw, "canyon", []procgen.Placement{{ID: "rock"}}, nil, Options{}); err == nil {
		t.Error("an unprefixed placement was spliced")
	}
	if _, err := SpliceRaw(&raw, "canyon", nil, []procgen.VolumeEntry{{"id": "gen.canyon.x", "colour": "red"}}, Options{}); err == nil {
		t.Error("a volume with a key volumes do not have was spliced")
	}
	if len(raw.Scenery) != 4 {
		t.Error("a refused splice changed the level")
	}
}

func TestSpliceFileWritesOnlyOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lvl.yaml")
	raw := handMade()
	data, _ := yaml.Marshal(raw)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	pl := []procgen.Placement{{ID: "gen.canyon.t.r1", Model: "m.fnm"}}
	st, err := SpliceFile(path, "canyon", pl, nil, Options{})
	if err != nil || !st.Changed() {
		t.Fatalf("first splice: %v %+v", err, st)
	}
	back, err := ReadRawLevel(path)
	if err != nil {
		t.Fatal(err)
	}
	if sc, vo := Count(&back, "canyon"); sc != 1 || vo != 0 {
		t.Errorf("the file holds %d/%d of the generator's entries", sc, vo)
	}
	stat1, _ := os.Stat(path)
	st, err = SpliceFile(path, "canyon", pl, nil, Options{})
	if err != nil || st.Changed() {
		t.Errorf("an identical splice reported a change: %v %+v", err, st)
	}
	stat2, _ := os.Stat(path)
	if !stat2.ModTime().Equal(stat1.ModTime()) {
		t.Error("an identical splice rewrote the file")
	}
}
