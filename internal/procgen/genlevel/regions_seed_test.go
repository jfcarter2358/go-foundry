package genlevel

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/regions"
	"foundry/internal/procgen/stages"
	"foundry/internal/world"
)

// A template's layout is written into a level with no regions, as region
// volumes whose world footprints are the template's, and the switch turns
// itself off; syncing then reads them back unchanged.
func TestSeedWritesTheTemplatesRegionsIntoAnEmptyLevel(t *testing.T) {
	d := stages.CanyonSavannah("arena_world")
	if !WantsSeed(d) {
		t.Fatal("the canyon_savannah template does not ask to seed its level")
	}
	want := d.StageByID("regions").Params.Items(regions.ParamRegions)
	raw := &world.RawLevel{Volumes: []world.RawVolume{{ID: "canyon_west", Half: world.RawVec3{X: 1, Y: 1, Z: 1}}}}
	ids, err := SeedLevelRegions(d, raw)
	if err != nil {
		t.Fatal(err)
	}
	// The trigger already called canyon_west keeps its name.
	if len(ids) != 2 || ids[0] != "canyon_west_2" || ids[1] != "savannah_east" {
		t.Fatalf("ids %v", ids)
	}
	if WantsSeed(d) {
		t.Error("the switch is still on after seeding")
	}
	if !HasRegionVolumes(raw) || len(raw.Volumes) != 3 {
		t.Fatalf("level volumes %d", len(raw.Volumes))
	}
	// The footprint is stored relative to the volume, the box fitted to it.
	v, err := world.VolumeFromRaw(raw.Volumes[2])
	if err != nil {
		t.Fatal(err)
	}
	if v.Position.X() != 256 || v.Position.Z() != 0 || v.HalfX != 256 || v.HalfZ != 384 {
		t.Errorf("savannah_east at %v half %v %v", v.Position, v.HalfX, v.HalfZ)
	}
	if v.Region.Environment != stages.SavannahEnvironment || v.Region.Footprint[0] != [2]float32{-256, -384} {
		t.Errorf("region %+v", v.Region)
	}
	// Syncing reads back the template's world footprints.
	if _, err := SyncRegions(d, raw); err != nil {
		t.Fatal(err)
	}
	got := d.StageByID("regions").Params.Items(regions.ParamRegions)
	if len(got) != 2 {
		t.Fatalf("synced %d regions", len(got))
	}
	for i, id := range []string{"canyon_west_2", "savannah_east"} {
		if got[i].Str("id") != id {
			t.Errorf("synced id %d %q, want %q", i, got[i].Str("id"), id)
		}
	}
	if !got[1][regions.FieldFootprint].Equal(want[1][regions.FieldFootprint]) {
		t.Errorf("savannah_east footprint %v, want %v", got[1].Points("footprint"), want[1].Points("footprint"))
	}
}

// An author's layout always wins: a level that has regions keeps them, and
// the switch turns off without a write.
func TestSeedLeavesALevelWithRegionsAlone(t *testing.T) {
	d := stages.CanyonSavannah("arena_world")
	raw := &world.RawLevel{Volumes: []world.RawVolume{{ID: "mine", Half: world.RawVec3{X: 1, Y: 1, Z: 1},
		Region: &world.RawVolumeRegion{Environment: "savannah", Footprint: [][2]float32{{0, 0}, {1, 0}, {0, 1}}}}}}
	ids, err := SeedLevelRegions(d, raw)
	if err != nil || ids != nil {
		t.Fatalf("ids %v err %v", ids, err)
	}
	if WantsSeed(d) || len(raw.Volumes) != 1 {
		t.Errorf("switch %v, volumes %d", WantsSeed(d), len(raw.Volumes))
	}
	// A doc that never asked is never touched.
	plain := stages.LevelWorld("arena_world")
	if ids, err := SeedLevelRegions(plain, &world.RawLevel{}); ids != nil || err != nil {
		t.Errorf("level_world seeded %v %v", ids, err)
	}
}

// The switch is an Added parameter: cleared, the regions stage hashes as a
// level_world doc's with the same list does.
func TestClearedSeedHashesLikeAPlainRegionsStage(t *testing.T) {
	d := stages.CanyonSavannah("arena_world")
	plain := d.Clone()
	delete(plain.StageByID("regions").Params, regions.ParamSeedLevel)
	k := procgen.KindByName(regions.StageKind)
	h := func(doc *procgen.Doc) string {
		return procgen.StageHash(k, 0, procgen.Resolve(k.Params, doc.StageByID("regions").Params), nil)
	}
	if h(d) == h(plain) {
		t.Error("the switch, on, does not change the hash (it must: a doc that would seed is not the doc that ran)")
	}
	ClearSeed(d)
	if h(d) != h(plain) {
		t.Error("cleared, the switch still changes the regions stage's hash")
	}
}

// SeedLevelFile makes the level when it does not exist.
func TestSeedLevelFileMakesTheLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "levels", "arena.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	d := stages.CanyonSavannah("arena_world")
	ids, err := SeedLevelFile(d, path)
	if err != nil || len(ids) != 2 {
		t.Fatalf("ids %v err %v", ids, err)
	}
	raw, err := ReadRawLevel(path)
	if err != nil {
		t.Fatal(err)
	}
	if !HasRegionVolumes(&raw) || len(raw.Volumes) != 2 {
		t.Errorf("level holds %d volumes", len(raw.Volumes))
	}
	// Run again (as a second `gen run` would): nothing more is written.
	if ids, err := SeedLevelFile(d, path); ids != nil || err != nil {
		t.Errorf("second seed %v %v", ids, err)
	}
}
