package stages

// environment_presets_test.go — the nine environment presets of
// docs/regions/PRESETS.md step 2 (environment_*.go): each is registered,
// validates, expands into a level generation doc's per-region stages, and
// grows its landform when its terrain stage runs as a region at low
// resolution; their ground layers come from the shared catalogue so that
// presets the adjacency table lets meet fit a tile's eight layers.
//
// FOUNDRY_PRESETS_DUMP=<dir> writes each preset's relief picture.

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/regions"
	"foundry/internal/procgen/terrain"
)

// newPresets are the step-2 presets with what their landform must show.
var newPresets = []struct {
	name  string
	check func(t *testing.T, tr *terrain.Terrain, s presetStats)
}{
	{PineForestEnvironment, func(t *testing.T, tr *terrain.Terrain, s presetStats) {
		if s.span < 30 || s.p95 < 8 {
			t.Errorf("pine forest hills too flat: span %.1f m, p95 slope %.1f°", s.span, s.p95)
		}
	}},
	{AlpineEnvironment, func(t *testing.T, tr *terrain.Terrain, s presetStats) {
		if s.span < 100 || s.p95 < 25 {
			t.Errorf("alpine relief too low: span %.1f m, p95 slope %.1f°", s.span, s.p95)
		}
	}},
	{CoastEnvironment, func(t *testing.T, tr *terrain.Terrain, s presetStats) {
		if tr.Meta.Water == nil || len(tr.Meta.Water.Bodies) == 0 {
			t.Fatal("the coast has no sea")
		}
		if s.min > -5 {
			t.Errorf("no sea floor: lowest %.1f m", s.min)
		}
	}},
	{DunesEnvironment, func(t *testing.T, tr *terrain.Terrain, s presetStats) {
		if s.span < 10 || s.max > 60 {
			t.Errorf("dune field span %.1f m (max %.1f)", s.span, s.max)
		}
	}},
	{VolcanicEnvironment, func(t *testing.T, tr *terrain.Terrain, s presetStats) {
		if len(tr.Meta.Vents) == 0 {
			t.Fatal("no vents in terrain.yaml")
		}
		if s.max < 70 {
			t.Errorf("no cone: highest %.1f m", s.max)
		}
	}},
	{TundraEnvironment, func(t *testing.T, tr *terrain.Terrain, s presetStats) {
		if s.span > 40 {
			t.Errorf("tundra too rugged: span %.1f m", s.span)
		}
	}},
	{WetlandEnvironment, func(t *testing.T, tr *terrain.Terrain, s presetStats) {
		if tr.Meta.Water == nil || len(tr.Meta.Water.Bodies) == 0 {
			t.Fatal("the wetland has no water")
		}
		if s.p95 > 10 {
			t.Errorf("wetland not flat: p95 slope %.1f°", s.p95)
		}
	}},
	{TallgrassPrairieEnvironment, func(t *testing.T, tr *terrain.Terrain, s presetStats) {
		if s.p95 > 15 {
			t.Errorf("prairie not gentle: p95 slope %.1f°", s.p95)
		}
	}},
	{OakSavannahEnvironment, func(t *testing.T, tr *terrain.Terrain, s presetStats) {
		if s.p95 > 15 {
			t.Errorf("oak savannah not gentle: p95 slope %.1f°", s.p95)
		}
	}},
}

// presetStats is a terrain's height range and slope percentiles.
type presetStats struct{ min, max, span, p50, p95 float64 }

func statsOf(tr *terrain.Terrain) presetStats {
	g := tr.Grid
	s := presetStats{min: math.Inf(1), max: math.Inf(-1)}
	var slopes []float64
	for j := 1; j < g.N; j++ {
		for i := 1; i < g.N; i++ {
			h := float64(g.At(i, j))
			s.min, s.max = math.Min(s.min, h), math.Max(s.max, h)
			dx := float64(g.At(i+1, j)-g.At(i-1, j)) / (2 * g.Cell)
			dz := float64(g.At(i, j+1)-g.At(i, j-1)) / (2 * g.Cell)
			slopes = append(slopes, math.Atan(math.Hypot(dx, dz))*180/math.Pi)
		}
	}
	sort.Float64s(slopes)
	s.span = s.max - s.min
	s.p50, s.p95 = slopes[len(slopes)/2], slopes[len(slopes)*95/100]
	return s
}

func TestNewEnvironmentPresets(t *testing.T) {
	for _, pc := range newPresets {
		pc := pc
		t.Run(pc.name, func(t *testing.T) {
			e := procgen.EnvironmentByName(pc.name)
			if e == nil {
				t.Fatalf("%s is not registered", pc.name)
			}
			d := e.New()
			if d.Name != pc.name || d.Level != "" {
				t.Errorf("name %q level %q", d.Name, d.Level)
			}
			if probs := d.Validate(); len(probs) > 0 {
				t.Fatalf("problems: %v", probs)
			}
			for _, st := range d.Stages {
				if st.Kind == EnvironmentKind {
					t.Errorf("a preset carries the level-wide %s stage", st.ID)
				}
				// Plants are Poly Haven scenery, never the engine's foliage
				// biomes (docs/regions/PRESETS_2.md), and their needles and
				// blades are thinned, not edge-collapsed.
				if st.Kind == VegetationKind {
					t.Errorf("stage %s grows an engine foliage biome", st.ID)
				}
				if st.Kind == PolyHavenPlantsKind && !st.Params.Bool("thin_foliage") {
					t.Errorf("plants stage %s does not thin its foliage", st.ID)
				}
			}
			// Deterministic: two builds are the same document.
			if a, b := fmt.Sprint(e.New().Stages), fmt.Sprint(d.Stages); a != b {
				t.Error("two builds of the preset differ")
			}

			// Expansion: a level generation doc with one region of it has
			// every per-region stage of the preset as r_<region>_<id>.
			f := procgen.Float
			item := regionItem("only", pc.name, "", -192, -192, 192, 192, 100)
			w := worldDoc("w_"+pc.name, []procgen.Params{item},
				procgen.Params{"resolution": f(4), "tile_size": f(128), "warp_amount": f(10)}, nil)
			w.Stages = append(w.Stages, procgen.Stage{Kind: RegionMaterialKind, ID: "ground_mat",
				Params: procgen.Params{"terrain": procgen.StageRef("ground")}})
			hashes, err := procgen.StageHashes(w)
			if err != nil {
				t.Fatal(err)
			}
			for _, st := range d.Stages {
				k := procgen.KindByName(st.Kind)
				// The textures stage is read by the level's region_material,
				// not expanded.
				if k == nil || k.Regional != procgen.RegionalPerRegion || st.Kind == PolyHavenTexturesKind {
					continue
				}
				found := false
				for id := range hashes {
					if strings.HasPrefix(id, "r_") && strings.HasSuffix(id, "_"+st.ID) {
						found = true
					}
				}
				if !found {
					t.Errorf("stage %s (%s) was not expanded: %v", st.ID, st.Kind, hashes)
				}
			}

			// The landform: run the region terrain alone at 4 m.
			res, err := procgen.Run(context.Background(), w, procgen.Options{OutDir: t.TempDir(), Workers: 4, Only: []string{"ground"}})
			if err != nil {
				t.Fatal(err)
			}
			st := res.Stage("ground")
			tr := st.Artifact.(*terrain.Terrain)
			presetsDump(t, st, "preset_"+pc.name)
			s := statsOf(tr)
			t.Logf("%s: heights %.1f..%.1f m, slope p50 %.1f° p95 %.1f°", pc.name, s.min, s.max, s.p50, s.p95)
			pc.check(t, tr, s)
		})
	}
}

// presetLayerKeys is a preset's ground layers as palette keys (id, repeat,
// projection — regionLayer.key), from its textures and terrain_material
// stages.
func presetLayerKeys(t *testing.T, name string) map[string]bool {
	t.Helper()
	d, _, err := procgen.LoadEnvironment(name)
	if err != nil {
		t.Fatal(err)
	}
	var tex, mat procgen.Params
	for _, st := range d.Stages {
		k := procgen.KindByName(st.Kind)
		switch st.Kind {
		case PolyHavenTexturesKind:
			tex = procgen.Resolve(k.Params, st.Params)
		case TerrainMaterialKind:
			mat = procgen.Resolve(k.Params, st.Params)
		}
	}
	out := map[string]bool{}
	for r, key := range groundRoleKeys {
		layer := mat.Str(key)
		if layer == "" {
			continue
		}
		id, scale, ok := textureItem(tex, layer)
		if !ok {
			t.Fatalf("%s: %s names layer %q, which its textures stage lacks", name, key, layer)
		}
		tri := false
		if r == 3 {
			if mat.Float("rock_scale") > 0 {
				scale = mat.Float("rock_scale")
			}
			tri = mat.Bool("rock_triplanar")
		}
		if scale <= 0 {
			scale = 3
		}
		out[fmt.Sprintf("%s|%.4g|%v", id, round3(scale), tri)] = true
	}
	return out
}

// Every pair of presets the adjacency table does not forbid fits a band
// tile's eight layers; every preset keeps to four; all eleven fit one
// world's palette.
func TestEnvironmentPresetsShareLayers(t *testing.T) {
	names := []string{DesertCanyonEnvironment, SavannahEnvironment}
	for _, pc := range newPresets {
		names = append(names, pc.name)
	}
	keys := map[string]map[string]bool{}
	all := map[string]bool{}
	for _, n := range names {
		keys[n] = presetLayerKeys(t, n)
		for k := range keys[n] {
			all[k] = true
		}
		limit := 4
		if len(keys[n]) > limit {
			t.Errorf("%s has %d ground layers (want ≤ %d): %v", n, len(keys[n]), limit, keys[n])
		}
	}
	t.Logf("%d distinct layers over %d presets", len(all), len(names))
	if len(all) > 32 {
		t.Errorf("%d distinct layers: more than one world's palette holds", len(all))
	}
	adj := regions.DefaultAdjacency()
	for i, a := range names {
		for _, b := range names[i+1:] {
			union := map[string]bool{}
			for k := range keys[a] {
				union[k] = true
			}
			for k := range keys[b] {
				union[k] = true
			}
			r, _ := adj.Lookup(a, b)
			if len(union) > 8 && !r.Forbidden {
				t.Errorf("%s and %s may meet but need %d layers in a band tile", a, b, len(union))
			}
		}
	}
}

// The new presets meet their neighbours at the shared 0 m base: water lies
// below it, and every preset with relief far above it rises from it.
func TestEnvironmentPresetsShareTheBaseLevel(t *testing.T) {
	for _, pc := range newPresets {
		d, _, err := procgen.LoadEnvironment(pc.name)
		if err != nil {
			t.Fatal(err)
		}
		g := d.StageByID("ground")
		k := procgen.KindByName(TerrainKind)
		p := procgen.Resolve(k.Params, g.Params)
		if p.Bool("water") && p.Float("water_level") >= 0 {
			t.Errorf("%s: water at %.2f m, not below the shared base", pc.name, p.Float("water_level"))
		}
		high := p.Float("ridge_height") > 0 || p.Int("volcanoes") > 0 || p.Float("relief") > 40 || p.Float("dune_height") > 0
		if high && (p.Float("edge_rise") <= 0 || p.Float("base_elevation") != 0) {
			t.Errorf("%s: high ground without an edge rise from 0 m (rise %.0f, base %.1f)", pc.name,
				p.Float("edge_rise"), p.Float("base_elevation"))
		}
	}
}
