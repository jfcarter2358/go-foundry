package genlevel

import (
	"math"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/regions"
	_ "foundry/internal/procgen/stages" // the regions stage kind
	"foundry/internal/world"
)

func TestSyncRegionsCopiesSortedWorldFootprints(t *testing.T) {
	d := &procgen.Doc{Name: "arena_world", Seed: 1, Stages: []procgen.Stage{{Kind: regions.StageKind, ID: "regions"}}}
	anchor := [2]float32{5, 0}
	raw := &world.RawLevel{Volumes: []world.RawVolume{
		{ID: "zeta", Position: world.RawVec3{X: 100, Y: 3, Z: 50}, Rotation: world.RawVec3{Y: float32(math.Pi / 2)},
			Half: world.RawVec3{X: 10, Y: 5, Z: 20},
			Region: &world.RawVolumeRegion{Environment: "savannah", Priority: 3, Transition: 80, Anchor: &anchor,
				Footprint: [][2]float32{{-10, -20}, {10, -20}, {10, 20}, {-10, 20}}}},
		{ID: "trigger", Half: world.RawVec3{X: 1, Y: 1, Z: 1}},
		{ID: "alpha", Position: world.RawVec3{X: -0.1}, Half: world.RawVec3{X: 1, Y: 1, Z: 1},
			Region: &world.RawVolumeRegion{Environment: "desert_canyon", Generator: "environments/x.fgen",
				Footprint: [][2]float32{{0, 0}, {1, 0}, {0, 1}}}},
	}}
	changed, err := SyncRegions(d, raw)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	items := d.Stages[0].Params.Items(regions.ParamRegions)
	if len(items) != 2 || items[0].Str("id") != "alpha" || items[1].Str("id") != "zeta" {
		t.Fatalf("items %v, want alpha then zeta", items)
	}
	a := items[0]
	if a.Str("generator") != "environments/x.fgen" || a.Float("transition") != 150 || a.Bool("anchored") {
		t.Errorf("alpha %v", a)
	}
	// -0.1 in float32 arrives as -0.1, not -0.10000000149.
	if p := a.Points("footprint")[0]; p[0] != -0.1 || p[1] != 0 {
		t.Errorf("alpha's first point %v, want [-0.1 0]", p)
	}
	z := items[1]
	// A quarter turn about Y maps local (x, z) to world (z, -x) about the
	// position: (-10, -20) -> (100-20, 50+10).
	if p := z.Points("footprint")[0]; math.Abs(p[0]-80) > 1e-3 || math.Abs(p[1]-60) > 1e-3 {
		t.Errorf("zeta's first point %v, want [80 60]", p)
	}
	if !z.Bool("anchored") || z.Int("priority") != 3 {
		t.Errorf("zeta %v", z)
	}
	if again, _ := SyncRegions(d, raw); again {
		t.Error("resyncing the same level changed the doc")
	}
	// A doc without a regions stage is untouched.
	plain := &procgen.Doc{Name: "p", Stages: []procgen.Stage{{Kind: "terrain", ID: "ground"}}}
	if ch, err := SyncRegions(plain, raw); ch || err != nil {
		t.Errorf("plain doc: %v %v", ch, err)
	}
	// Removing the regions from the level empties the list.
	if ch, _ := SyncRegions(d, &world.RawLevel{}); !ch || len(d.Stages[0].Params.Items(regions.ParamRegions)) != 0 {
		t.Error("an emptied level did not empty the list")
	}
}
