package procgen

// regional_test.go — plan-time expansion of environment regions, on fake
// kinds registered here (regional.go):
//
//   - "tregions" defines regions: a list of (key, env), each growing the
//     preset named env from testPresets, with a weight of 1 inside x < 0 for
//     key "a" and x >= 0 for anything else;
//   - "tterrain" is a preset's terrain (Shared), "tunion" the level doc's
//     stand-in for it (Shared, SharedFor tterrain);
//   - "tplace" is a placement kind (PerRegion) that refers to a terrain and,
//     optionally, to another tplace, and records what it was handed;
//   - "tdrift" is a kind that cannot run per region (None).

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

var (
	testPresetsMu sync.Mutex
	testPresets   = map[string]*Doc{}
	placeSeen     sync.Map // stage ID -> string
)

func init() {
	RegisterStage(StageKind{
		Name: "tregions", Category: "Test", Version: 1, Regional: RegionalShared,
		Params: []ParamDef{
			{Name: "regions", Type: ParamList, Default: List(), Fields: []ParamDef{
				{Name: "key", Type: ParamString, Default: String("a")},
				{Name: "env", Type: ParamString, Default: String("p1")},
			}},
		},
		Run: func(rc *RunContext) error { return rc.WriteFile("regions.txt", []byte("ok")) },
		Expand: func(d *Doc, s *Stage, p Params) ([]RegionExpansion, error) {
			var out []RegionExpansion
			for _, it := range p.Items("regions") {
				key, env := it.Str("key"), it.Str("env")
				testPresetsMu.Lock()
				pre := testPresets[env]
				testPresetsMu.Unlock()
				if pre == nil {
					return nil, fmt.Errorf("no preset %q", env)
				}
				west := key == "a"
				out = append(out, RegionExpansion{Preset: pre.Clone(), Region: RegionContext{
					Key: key, ID: key, Environment: env,
					Weight: func(x, z float64) float64 {
						if (x < 0) == west {
							return 1
						}
						return 0
					},
				}})
			}
			return out, nil
		},
	})
	RegisterStage(StageKind{
		Name: "tterrain", Category: "Test", Version: 1, Regional: RegionalShared,
		Params: []ParamDef{{Name: "height", Type: ParamFloat, Default: Float(1)}},
		Run:    func(rc *RunContext) error { return rc.WriteFile("t.txt", []byte("t")) },
	})
	RegisterStage(StageKind{
		Name: "tunion", Category: "Test", Version: 1, Regional: RegionalShared, SharedFor: []string{"tterrain"},
		Params: []ParamDef{{Name: "regions", Type: ParamStage, Default: StageRef("")}},
		Run:    func(rc *RunContext) error { return rc.WriteFile("u.txt", []byte("u")) },
	})
	RegisterStage(StageKind{
		Name: "tplace", Category: "Test", Version: 1, Regional: RegionalPerRegion,
		Params: []ParamDef{
			{Name: "terrain", Type: ParamStage, Default: StageRef("")},
			{Name: "after", Type: ParamStage, Default: StageRef("")},
			{Name: "density", Type: ParamFloat, Default: Float(1)},
		},
		Run: func(rc *RunContext) error {
			r := rc.Region
			if r == nil {
				return fmt.Errorf("no region")
			}
			line := fmt.Sprintf("region=%s env=%s terrain=%s after=%s west=%v east=%v seed=%d",
				r.Key, r.Environment, rc.Params.Str("terrain"), rc.Params.Str("after"),
				r.Weight(-10, 0), r.Weight(10, 0), rc.Seed)
			placeSeen.Store(rc.Stage.ID, line)
			rc.Place(Placement{ID: "p0", Model: "m.fnm"})
			return rc.WriteFile("p.txt", []byte(line))
		},
	})
	RegisterStage(StageKind{
		Name: "tdrift", Category: "Test", Version: 1,
		Params: []ParamDef{{Name: "terrain", Type: ParamStage, Default: StageRef("")}},
		Run:    func(rc *RunContext) error { return nil },
	})
}

func setPreset(name string, d *Doc) {
	testPresetsMu.Lock()
	testPresets[name] = d
	testPresetsMu.Unlock()
}

func preset(density float64) *Doc {
	return &Doc{Name: "pre", Seed: 9, Stages: []Stage{
		{Kind: "tterrain", ID: "ground"},
		{Kind: "tplace", ID: "rocks", Params: Params{"terrain": StageRef("ground"), "density": Float(density)}},
		{Kind: "tplace", ID: "bushes", Params: Params{"terrain": StageRef("ground"), "after": StageRef("rocks")}},
		{Kind: "tdrift", ID: "drift", Params: Params{"terrain": StageRef("ground")}},
		{Kind: "tplace", ID: "off", Disabled: true, Params: Params{"terrain": StageRef("ground")}},
	}}
}

func worldDoc() *Doc {
	item := func(k, e string) Params { return Params{"key": String(k), "env": String(e)} }
	return &Doc{Name: "w", Seed: 3, Stages: []Stage{
		{Kind: "tregions", ID: "regions", Params: Params{"regions": List(item("a", "p1"), item("b", "p2"))}},
		{Kind: "tunion", ID: "union", Params: Params{"regions": StageRef("regions")}},
	}}
}

func TestRegionsExpandPerRegionStages(t *testing.T) {
	setPreset("p1", preset(1))
	setPreset("p2", preset(1))
	d := worldDoc()
	hashes, err := StageHashes(d)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"regions", "union", "r_a_rocks", "r_a_bushes", "r_b_rocks", "r_b_bushes"}
	if len(hashes) != len(want) {
		t.Fatalf("planned %v, want %v", hashes, want)
	}
	for _, id := range want {
		if hashes[id] == "" {
			t.Errorf("no %s in the plan", id)
		}
	}
	// Same preset, same params, different regions: different seeds and so
	// different hashes.
	if hashes["r_a_rocks"] == hashes["r_b_rocks"] {
		t.Error("two regions growing the same preset hash the same")
	}

	var logs []string
	var mu sync.Mutex
	dir := t.TempDir()
	res, err := Run(context.Background(), d, Options{OutDir: dir, Log: func(l string) { mu.Lock(); logs = append(logs, l); mu.Unlock() }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Ran) != 6 {
		t.Fatalf("ran %v", res.Ran)
	}
	get := func(id string) string { v, _ := placeSeen.Load(id); s, _ := v.(string); return s }
	a, b := get("r_a_bushes"), get("r_b_bushes")
	if !strings.Contains(a, "region=a env=p1 terrain=union after=r_a_rocks west=1 east=0") {
		t.Errorf("r_a_bushes saw %q", a)
	}
	if !strings.Contains(b, "region=b env=p2 terrain=union after=r_b_rocks west=0 east=1") {
		t.Errorf("r_b_bushes saw %q", b)
	}
	if seedOf := func(s string) string { return s[strings.Index(s, "seed="):] }; seedOf(get("r_a_rocks")) == seedOf(get("r_b_rocks")) {
		t.Error("two regions' rocks ran with one seed")
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "preset stage drift (tdrift) cannot run per region yet; skipped") {
		t.Errorf("no skip note for the None kind in:\n%s", joined)
	}
	if strings.Contains(joined, "preset stage off") {
		t.Error("a disabled preset stage was mentioned")
	}
	if ids := placementIDs(res); !ids["gen.w.r_a_rocks.p0"] || !ids["gen.w.r_b_bushes.p0"] {
		t.Errorf("placements %v lack the region stage prefixes", ids)
	}
	if RegionStagePrefix("w", "a") != "gen.w.r_a_" {
		t.Error("RegionStagePrefix")
	}

	// A rerun skips everything and prunes nothing: the expanded stages are
	// in the plan, though not in the document.
	res, err = Run(context.Background(), d, Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Ran) != 0 || len(res.Skipped) != 6 {
		t.Fatalf("rerun ran %v skipped %v", res.Ran, res.Skipped)
	}
	if _, err := os.Stat(res.Stage("r_a_rocks").File("p.txt")); err != nil {
		t.Errorf("the rerun pruned an expanded stage's output: %v", err)
	}

	// Editing region b's preset reruns b's stages only.
	setPreset("p2", preset(2))
	res, err = Run(context.Background(), d, Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Ran, ",") != "r_b_rocks,r_b_bushes" {
		t.Errorf("after editing b's preset ran %v, want r_b_rocks,r_b_bushes", res.Ran)
	}
	// -stage on an expanded stage works.
	if _, err := Run(context.Background(), d, Options{OutDir: dir, Only: []string{"r_a_rocks"}}); err != nil {
		t.Errorf("Only an expanded stage: %v", err)
	}

	// Dropping a region prunes its stages' outputs.
	d.Stages[0].Params["regions"] = List(Params{"key": String("a"), "env": String("p1")})
	res, err = Run(context.Background(), d, Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir + "/r_b_rocks/p.txt"); err == nil {
		t.Error("a removed region's outputs survived")
	}
}

func placementIDs(res *Result) map[string]bool {
	out := map[string]bool{}
	for _, p := range res.Placements() {
		out[p.ID] = true
	}
	return out
}

// Without a stand-in for the preset's terrain the preset's stages that
// need it are skipped, with a note naming it (phase 1 refused the plan;
// phase 4 made placement kinds PerRegion, and a level doc of regions
// alone must still plan).
func TestRegionsNeedASharedStandIn(t *testing.T) {
	setPreset("p1", preset(1))
	d := worldDoc()
	d.Stages = d.Stages[:1]
	d.Stages[0].Params["regions"] = List(Params{"key": String("a"), "env": String("p1")})
	ps, notes, err := planWithNotes(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Errorf("planned %d stages, want the regions stage alone", len(ps))
	}
	if !strings.Contains(strings.Join(notes, "\n"), "no enabled stage standing in for tterrain") {
		t.Fatalf("notes = %v", notes)
	}
}

// A document without an Expand stage plans exactly as before: nothing added.
func TestNoRegionsNoExpansion(t *testing.T) {
	d := pipeline()
	ps, notes, err := planWithNotes(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || len(notes) != 0 {
		t.Fatalf("planned %d stages with notes %v", len(ps), notes)
	}
	for _, p := range ps {
		if p.region != nil {
			t.Error("a listed stage got a region")
		}
		if p.seed != StageSeed(d.Seed, p.stage.ID, p.kind.Params, p.params) {
			t.Error("seed changed")
		}
	}
}

func TestRewriteStageRefsInsideLists(t *testing.T) {
	defs := []ParamDef{
		{Name: "a", Type: ParamStage, Default: StageRef("")},
		{Name: "l", Type: ParamList, Default: List(), Fields: []ParamDef{{Name: "r", Type: ParamStage, Default: StageRef("")}}},
	}
	in := Params{"a": StageRef("x"), "l": List(Params{"r": StageRef("y")}, Params{"r": StageRef("z")})}
	out := RewriteStageRefs(defs, in, func(r string) string {
		if r == "z" {
			return ""
		}
		return "n_" + r
	})
	if out.Str("a") != "n_x" || out.Items("l")[0].Str("r") != "n_y" || out.Items("l")[1].Str("r") != "z" {
		t.Errorf("rewrote to %v", out)
	}
	if in.Str("a") != "x" || in.Items("l")[0].Str("r") != "y" {
		t.Error("the input was modified")
	}
}
