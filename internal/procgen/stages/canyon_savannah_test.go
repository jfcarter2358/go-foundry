package stages

// canyon_savannah_test.go — phase 6 (docs/regions/PHASE_6.md): the
// canyon_savannah template, the savannah preset's shape, and the knobs the
// phase added for the join — region_terrain's lead and flow_mask, the
// vegetation stage's fray, terrain_material's sand_tint.

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/material"
	"foundry/internal/procgen"
	"foundry/internal/procgen/regions"
	"foundry/internal/procgen/terrain"
)

func TestCanyonSavannahTemplate(t *testing.T) {
	d, err := procgen.NewFromTemplate(CanyonSavannahTemplate, "arena_world")
	if err != nil {
		t.Fatal(err)
	}
	if d.Level != "arena" {
		t.Errorf("level %q, want arena", d.Level)
	}
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	rs := d.StageByID("regions").Params
	items := rs.Items(regions.ParamRegions)
	if len(items) != 2 || items[0].Str("environment") != DesertCanyonEnvironment || items[1].Str("environment") != SavannahEnvironment {
		t.Fatalf("regions %v", items)
	}
	if !rs.Bool(regions.ParamSeedLevel) {
		t.Error("the template does not seed its level")
	}
	// Default transition: the table's desert_canyon|savannah width (200 m)
	// is the band.
	set, err := regionSet(procgen.Resolve(procgen.KindByName(RegionsKind).Params, rs), 1)
	if err != nil {
		t.Fatal(err)
	}
	if b := set.Regions[0].PairTransitions[SavannahEnvironment]; b != 200 {
		t.Errorf("canyon|savannah band %v, want 200 m from the adjacency table", b)
	}
	g := d.StageByID("ground").Params
	if g.Str("lead") != "canyon_west" || g.Str("flow_mask") != "mfd" {
		t.Errorf("ground %v", g)
	}
	// The spawn stands on the savannah side and looks west, into the canyon.
	air := d.StageByID("air").Params
	if !air.Bool("spawn") || air.Vec2("spawn_at")[0] <= 100 || math.Abs(air.Float("spawn_yaw")-180) > 40 {
		t.Errorf("spawn %v yaw %v", air.Vec2("spawn_at"), air.Float("spawn_yaw"))
	}
	// Every preset stage expands (or is skipped with a note) without error.
	if _, err := procgen.StageHashes(d); err != nil {
		t.Errorf("plan: %v", err)
	}
}

// The savannah's grass and acacia stand-ins are Poly Haven plants
// (docs/regions/PRESETS_2.md: no engine foliage biome): searsia_burchellii
// in clumps as scenery, and — since PRESETS_2B — the dry grass as a
// grass_field sward on the grass instancer, nothing grown by a vegetation
// stage. Until PRESETS_2 the grass was the engine's savannah biome as
// cross-faded vegetation volumes (PHASE_6); in PRESETS_2 it was scenery
// clumps in patches.
func TestSavannahPresetGrassIsPolyHavenGrassField(t *testing.T) {
	d := SavannahPreset()
	for _, st := range d.Stages {
		if st.Kind == VegetationKind {
			t.Errorf("stage %s grows an engine foliage biome", st.ID)
		}
	}
	cover := d.StageByID("cover")
	if cover == nil || cover.Disabled {
		t.Fatal("the savannah has no cover stage for its acacias")
	}
	var acacias procgen.Params
	for _, it := range cover.Params.Items("rules") {
		switch it.Str("name") {
		case "grass":
			t.Errorf("the grass is still a scenery rule")
		case "acacias":
			acacias = it
		}
	}
	if acacias == nil || acacias.Float("cluster") < 1 {
		t.Errorf("acacia clumps %v", acacias)
	}
	sward := d.StageByID("sward")
	if sward == nil || sward.Kind != GrassFieldKind {
		t.Fatalf("no grass_field stage: %+v", sward)
	}
	var grass procgen.Params
	for _, it := range sward.Params.Items("rules") {
		if it.Str("name") == "grass" {
			grass = it
		}
	}
	if grass == nil || grass.Str("plants") != "grass_clumps" || grass.Float("density") < 1 {
		t.Errorf("grass rule %v", grass)
	}
	for _, id := range []string{"grass_clumps", "acacia"} {
		if st := d.StageByID(id); st == nil || st.Kind != PolyHavenPlantsKind || !st.Params.Bool("thin_foliage") {
			t.Errorf("plants stage %s: %+v", id, st)
		}
	}
	// The desert preset in a region world is lighter than its template.
	pre := DesertCanyonPreset()
	for _, it := range pre.StageByID("scatter").Params.Items("rules") {
		if it.Str("name") == "wash_stones" && it.Int("max_instances") > 1500 {
			t.Errorf("wash stones %d", it.Int("max_instances"))
		}
	}
	tpl := DesertCanyon("x")
	for _, it := range tpl.StageByID("scatter").Params.Items("rules") {
		if it.Str("name") == "wash_stones" && it.Int("max_instances") != 3000 {
			t.Errorf("the template's wash stones changed: %d", it.Int("max_instances"))
		}
	}
}

// region_terrain's lead names the region whose knobs lead; an unknown one
// is refused.
func TestRegionTerrainLeadIsChosen(t *testing.T) {
	items := []procgen.Params{
		regionItem("big", SavannahEnvironment, "", -256, -128, 64, 128, 100),
		regionItem("small", DesertCanyonEnvironment, "", 64, -128, 256, 128, 100),
	}
	grid := procgen.Params{"resolution": procgen.Float(4), "tile_size": procgen.Float(64)}
	_, auto := runDoc(t, worldDoc("lead_auto", items, grid, nil))
	if auto.Meta.Biomes.Lead != "big" {
		t.Fatalf("auto lead %q, want the bigger region", auto.Meta.Biomes.Lead)
	}
	_, set := runDoc(t, worldDoc("lead_set", items, grid, procgen.Params{"lead": procgen.String("small")}))
	if set.Meta.Biomes.Lead != "small" {
		t.Errorf("lead %q, want small", set.Meta.Biomes.Lead)
	}
	d := worldDoc("lead_bad", items, grid, procgen.Params{"lead": procgen.String("nope")})
	if _, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: t.TempDir()}); err == nil {
		t.Error("an unknown lead region ran")
	}
}

// flow_mask mfd: the same heights; and across the band's broad flats no
// ruled lines — long straight runs of high flow on gentle ground, which
// single-receiver routing draws at 0° and 45° — where d8 has them.
func TestMFDFlowMaskHasNoRuledLines(t *testing.T) {
	items := []procgen.Params{
		regionItem("canyon", DesertCanyonEnvironment, "", -600, -300, 0, 300, 200),
		regionItem("plain", SavannahEnvironment, "", 0, -300, 600, 300, 200),
	}
	grid := procgen.Params{"resolution": procgen.Float(2), "tile_size": procgen.Float(128)}
	_, d8 := runDoc(t, worldDoc("flow_d8", items, grid, nil))
	_, mfd := runDoc(t, worldDoc("flow_mfd", items, grid, procgen.Params{"flow_mask": procgen.Enum("mfd")}))
	for k := range d8.Grid.H {
		if d8.Grid.H[k] != mfd.Grid.H[k] {
			t.Fatalf("heights differ at %d", k)
		}
	}
	ruled := func(tr *terrain.Terrain) int {
		fl, err := tr.Mask(terrain.MaskFlow)
		if err != nil {
			t.Fatal(err)
		}
		sl, _ := tr.Mask(terrain.MaskSlope)
		s := tr.Grid.S()
		// Samples on straight runs of ≥ 20 samples (40 m) of flow ≥ 0.5 on
		// ground under 3°, along rows, columns and both diagonals.
		on := func(i, j int) bool {
			if i < 0 || j < 0 || i >= s || j >= s {
				return false
			}
			k := j*s + i
			return fl[k] >= 0.5 && sl[k]*90 < 3
		}
		count := 0
		for _, dir := range [][2]int{{1, 0}, {0, 1}, {1, 1}, {1, -1}} {
			for j := 0; j < s; j++ {
				for i := 0; i < s; i++ {
					if !on(i, j) || on(i-dir[0], j-dir[1]) {
						continue
					}
					n := 0
					for on(i+n*dir[0], j+n*dir[1]) {
						n++
					}
					if n >= 20 {
						count += n
					}
				}
			}
		}
		return count
	}
	a, b := ruled(d8), ruled(mfd)
	t.Logf("samples on ruled flow lines: d8 %d, mfd %d", a, b)
	if a == 0 {
		t.Fatal("the fixture has no ruled lines to remove (d8)")
	}
	// Measured: d8 48,506, mfd 9,827 ruled samples (gently sloping floors
	// still route some flow in straight runs; under a few decimetres of
	// fill and on dead flats the mask is cut, masks.go).
	if b*4 > a {
		t.Errorf("mfd keeps %d of d8's %d ruled samples", b, a)
	}
}

// fray: each volume grows by 0.625 × fray a side and fades its edge over
// fray; 0 is the classic volume, untouched.
func TestVegetationFrayGrowsAndFadesVolumes(t *testing.T) {
	run := func(fray float64) []procgen.VolumeEntry {
		d := smallTerrainDoc()
		f, v2 := procgen.Float, procgen.Vec2
		params := procgen.Params{"terrain": procgen.StageRef("ground"), "cell": f(8), "max_size": f(16),
			"rules": procgen.List(procgen.Params{"biome": procgen.String("desert"), "slope": v2(0, 30), "threshold": f(0.3)})}
		if fray > 0 {
			params["fray"] = f(fray)
		}
		d.Stages = append(d.Stages, procgen.Stage{Kind: VegetationKind, ID: "plants", Params: params})
		res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		return res.Volumes()
	}
	half := func(v procgen.VolumeEntry, axis string) float64 {
		return v["half"].(map[interface{}]interface{})[axis].(float64)
	}
	plain, frayed := run(0), run(8)
	if len(plain) == 0 || len(plain) != len(frayed) {
		t.Fatalf("%d and %d volumes", len(plain), len(frayed))
	}
	for i := range plain {
		sp := plain[i]["scatter"].(map[interface{}]interface{})
		sf := frayed[i]["scatter"].(map[interface{}]interface{})
		if _, ok := sp["edge_fade_distance"]; ok {
			t.Fatal("a classic volume has an edge fade")
		}
		if sf["edge_fade_distance"] != 8.0 {
			t.Errorf("frayed volume fade %v", sf["edge_fade_distance"])
		}
		for _, ax := range []string{"x", "z"} {
			if g := half(frayed[i], ax) - half(plain[i], ax); math.Abs(g-5) > 0.01 {
				t.Errorf("volume %d grew %.2f on %s, want 5 (0.625 × 8)", i, g, ax)
			}
		}
	}
}

// sand_tint multiplies the sand layer's tint, in a region material too.
func TestSandTintTintsTheSandLayer(t *testing.T) {
	tintOf := func(tint *procgen.Value) [3]float32 {
		dir := t.TempDir()
		f, i := procgen.Float, procgen.Int
		a := groundPreset(t, dir, "hills", procgen.Params{"relief": f(20), "mesa_height": f(0), "mesa_tiers": i(0),
			"droplets": f(0), "talus_iterations": i(5), "canyons": procgen.List()},
			[4]float64{1, 2, 3, 4}, procgen.Params{"rock_scale": f(0)})
		extra := procgen.Params{"rock_scale": f(0)}
		if tint != nil {
			extra["sand_tint"] = *tint
		}
		b := groundPreset(t, dir, "plain", procgen.Params{"relief": f(6), "mesa_height": f(0), "mesa_tiers": i(0),
			"droplets": f(0), "talus_iterations": i(5), "canyons": procgen.List()}, [4]float64{9, 5, 6, 7}, extra)
		d := worldDoc("tinted", []procgen.Params{
			regionItem("hills", DesertCanyonEnvironment, a, -96, -64, 0, 64, 60),
			regionItem("plain", SavannahEnvironment, b, 0, -64, 96, 64, 60),
		}, procgen.Params{"resolution": f(2), "tile_size": f(64)}, nil)
		d.Stages = append(d.Stages, procgen.Stage{Kind: RegionMaterialKind, ID: "ground_mat",
			Params: procgen.Params{"terrain": procgen.StageRef("ground")}})
		_, art, _, res := runRegionMaterialDoc(t, d)
		md, err := material.ReadMaterial(filepath.Join(res, "materials", art.Material+".fmat"))
		if err != nil {
			t.Fatal(err)
		}
		fx := material.ResolveSurfaceDoc(md).Fx
		for k, l := range art.Layers {
			if l.Role == "sand" && len(l.Environments) == 1 && l.Environments[0] == SavannahEnvironment {
				c := fx.SplatLayers[k].Tint
				return [3]float32{c[0], c[1], c[2]}
			}
		}
		t.Fatal("no savannah sand layer")
		return [3]float32{}
	}
	prev := fileutil.ResourcesPath
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	base := tintOf(nil)
	half := procgen.Color(0.5, 0.5, 0.5)
	tinted := tintOf(&half)
	for c := 0; c < 3; c++ {
		if math.Abs(float64(tinted[c])-0.5*float64(base[c])) > 0.002 {
			t.Errorf("channel %d: tint %.3f, untinted %.3f", c, tinted[c], base[c])
		}
	}
}
