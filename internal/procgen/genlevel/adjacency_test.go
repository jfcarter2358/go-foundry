package genlevel

// adjacency_test.go — the adjacency check and "Insert intermediate region"
// on a level's region volumes.

import (
	"testing"

	"foundry/internal/procgen/regions"
	"foundry/internal/world"
)

// forbiddenLevel is a desert beside a tundra, sharing x = 0, the tundra a
// rotated volume (its footprint goes through its rotation).
func forbiddenLevel() *world.RawLevel {
	sq := [][2]float32{{-400, -300}, {400, -300}, {400, 300}, {-400, 300}}
	return &world.RawLevel{Volumes: []world.RawVolume{
		{ID: "dry", Position: world.RawVec3{X: -400, Y: 10}, Half: world.RawVec3{X: 400, Y: 25, Z: 300},
			Region: &world.RawVolumeRegion{Environment: regions.EnvDesertCanyon, Transition: 150, Footprint: sq}},
		{ID: "cold", Position: world.RawVec3{X: 300, Y: 30}, Half: world.RawVec3{X: 300, Y: 25, Z: 400},
			Rotation: world.RawVec3{Y: 1.5707963},
			Region: &world.RawVolumeRegion{Environment: regions.EnvTundra, Priority: 2, Transition: 150,
				Footprint: [][2]float32{{-300, -300}, {300, -300}, {300, 300}, {-300, 300}}}},
		{ID: "plain_trigger", Half: world.RawVec3{X: 1, Y: 1, Z: 1}},
	}}
}

func TestInsertIntermediateResolvesAForbiddenPairOnALevel(t *testing.T) {
	raw := forbiddenLevel()
	adj := regions.DefaultAdjacency()
	warp := regions.Warp{Amount: 40, Scale: 300, Seed: 99}
	_, pairs, err := LevelAdjacency(nil, raw, adj, warp)
	if err != nil {
		t.Fatal(err)
	}
	forb := regions.ForbiddenPairs(pairs)
	if len(forb) != 1 || forb[0].Suggestion() != regions.EnvSavannah {
		t.Fatalf("forbidden pairs %+v", forb)
	}
	ids, err := InsertIntermediate(raw, adj, forb[0].IDA, forb[0].IDB)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "cold_dry_savannah" {
		t.Fatalf("inserted %v", ids)
	}
	add := raw.Volumes[len(raw.Volumes)-1]
	v, err := world.VolumeFromRaw(add)
	if err != nil {
		t.Fatal(err)
	}
	if v.Region == nil || v.Region.Environment != regions.EnvSavannah || v.Region.Priority != 3 ||
		v.Region.Transition != world.DefaultRegionTransition || v.Position[1] != 20 {
		t.Errorf("the new volume %+v / %+v", v, v.Region)
	}
	if fp := v.RegionWorldFootprint(); len(fp) < 3 {
		t.Fatalf("footprint %v", fp)
	}
	// The gate: re-checked with the same warp, nothing is forbidden.
	_, pairs, err = LevelAdjacency(nil, raw, adj, warp)
	if err != nil {
		t.Fatal(err)
	}
	if f := regions.ForbiddenPairs(pairs); len(f) != 0 {
		t.Fatalf("still forbidden: %s", f[0].Warning())
	}
	// The savannah touches both (that is what it is for).
	touches := map[string]bool{}
	for _, p := range pairs {
		touches[p.IDA+"|"+p.IDB] = true
	}
	if !touches["cold|cold_dry_savannah"] || !touches["cold_dry_savannah|dry"] {
		t.Errorf("touching pairs after: %v", touches)
	}
	// A second insert for a pair that no longer touches refuses.
	if _, err := InsertIntermediate(raw, adj, "dry", "cold"); err == nil {
		t.Error("a pair that no longer overlaps got another intermediate")
	}
}
