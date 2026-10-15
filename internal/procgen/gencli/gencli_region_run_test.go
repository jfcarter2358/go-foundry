package gencli

// gencli_region_run_test.go — `foundry gen run <doc> -region <id>`
// (docs/regions/PHASE_4.md): runs one environment region's expanded stages
// and splices only their placements; the other region's placements stay in
// the level file as they were.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/world"

	"gopkg.in/yaml.v2"
)

// regionRunPreset writes a small preset: a flat terrain, two uncooked
// generated pebbles and a scatter of them at density.
func regionRunPreset(t *testing.T, path string, density float64) {
	t.Helper()
	f, i, s, ref, v2, b := procgen.Float, procgen.Int, procgen.String, procgen.StageRef, procgen.Vec2, procgen.Bool
	d := &procgen.Doc{Name: strings.TrimSuffix(filepath.Base(path), procgen.Ext), Seed: 3, Stages: []procgen.Stage{
		{Kind: "terrain", ID: "ground", Params: procgen.Params{"relief": f(1.5), "mesa_height": f(0), "mesa_tiers": i(0),
			"terrace": f(0), "droplets": f(0), "canyons": procgen.List()}},
		{Kind: "rocks", ID: "pebbles", Params: procgen.Params{"preset": procgen.Enum("pebble"), "variants": i(2),
			"triangles": i(200), "cook": b(false)}},
		{Kind: "scatter", ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "rules": procgen.List(
			procgen.Params{"name": s("stones"), "rocks": ref("pebbles"), "density": f(density), "slope": v2(0, 60), "cluster": f(0)})}},
	}}
	if err := procgen.WriteDoc(path, d); err != nil {
		t.Fatal(err)
	}
}

func TestGenRunOneRegionSplicesOnlyIt(t *testing.T) {
	root := t.TempDir()
	prev := fileutil.ResourcesPath
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	res := filepath.Join(root, "resources")
	os.MkdirAll(filepath.Join(res, "levels"), 0o755)
	os.MkdirAll(filepath.Join(res, "environments"), 0o755)
	regionRunPreset(t, filepath.Join(res, "environments", "west_p.fgen"), 3)
	regionRunPreset(t, filepath.Join(res, "environments", "east_p.fgen"), 3)
	vol := func(id, env, gen string, x float32) world.RawVolume {
		return world.RawVolume{ID: id, Position: world.RawVec3{X: x}, Half: world.RawVec3{X: 64, Y: 10, Z: 64},
			Region: &world.RawVolumeRegion{Environment: env, Generator: gen, Transition: 60,
				Footprint: [][2]float32{{-64, -64}, {64, -64}, {64, 64}, {-64, 64}}}}
	}
	lvl := world.RawLevel{Volumes: []world.RawVolume{
		vol("west", "desert_canyon", "environments/west_p.fgen", -64),
		vol("east", "savannah", "environments/east_p.fgen", 64)}}
	data, _ := yaml.Marshal(lvl)
	levelPath := filepath.Join(res, "levels", "arena.yaml")
	os.WriteFile(levelPath, data, 0o644)

	var out bytes.Buffer
	if code := Main(root, []string{"world", "arena"}, &out); code != 0 {
		t.Fatalf("world: %d\n%s", code, out.String())
	}
	docPath := filepath.Join(res, "generators", "arena_world.fgen")
	d, err := procgen.ReadDoc(docPath)
	if err != nil {
		t.Fatal(err)
	}
	// A small, uncooked world without the region material (its textures
	// would download).
	d.Stages[0].Params["resolution"], d.Stages[0].Params["tile_size"] = procgen.Float(2), procgen.Float(64)
	d.Stages[1].Params["cook"] = procgen.Bool(false)
	d.Stages = d.Stages[:2]
	if err := procgen.WriteDoc(docPath, d); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		out.Reset()
		if code := Main(root, append([]string{"run", "arena_world", "-q", "-workers", "4"}, args...), &out); code != 0 {
			t.Fatalf("run %v: %d\n%s", args, code, out.String())
		}
	}
	run()
	level := func() world.RawLevel {
		t.Helper()
		raw, err := genlevel.ReadRawLevel(levelPath)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	byPrefix := func(raw world.RawLevel, pre string) map[string]world.RawScenery {
		m := map[string]world.RawScenery{}
		for _, s := range raw.Scenery {
			if strings.HasPrefix(s.ID, pre) {
				m[s.ID] = s
			}
		}
		return m
	}
	west, east := procgen.RegionStagePrefix("arena_world", "west"), procgen.RegionStagePrefix("arena_world", "east")
	l1 := level()
	w1, e1 := byPrefix(l1, west), byPrefix(l1, east)
	if len(w1) < 20 || len(e1) < 20 {
		t.Fatalf("west %d, east %d placements after the first run", len(w1), len(e1))
	}

	// East's preset doubles its density; run east alone.
	regionRunPreset(t, filepath.Join(res, "environments", "east_p.fgen"), 6)
	run("-region", "east")
	if !strings.Contains(out.String(), "spliced") {
		t.Errorf("the region run did not splice:\n%s", out.String())
	}
	l2 := level()
	w2, e2 := byPrefix(l2, west), byPrefix(l2, east)
	if len(w2) != len(w1) {
		t.Fatalf("west has %d placements, had %d", len(w2), len(w1))
	}
	for id, s := range w1 {
		if o, ok := w2[id]; !ok || o.Position != s.Position || o.Model != s.Model {
			t.Errorf("west placement %s changed", id)
		}
	}
	if len(e2) <= len(e1) {
		t.Errorf("east has %d placements after doubling its density, had %d", len(e2), len(e1))
	}
	out.Reset()
	if code := Main(root, []string{"run", "arena_world", "-q", "-region", "nowhere"}, &out); code == 0 {
		t.Errorf("an unknown region ran: %s", out.String())
	}
}
