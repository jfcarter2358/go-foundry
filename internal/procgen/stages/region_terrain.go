package stages

// region_terrain.go — the `region_terrain` stage: ONE terrain grown from
// every environment region of a level generation doc (docs/regions/SPEC.md,
// phase 2; the generator is internal/procgen/terrain/union.go).
//
// The level doc's `regions` stage lists the level's region volumes; each
// names an environment preset whose `terrain` stage is that region's FIELD
// PROVIDER. This stage:
//
//  1. reads the regions artifact (the *regions.Set: footprints, priorities,
//     bands, the boundary warp) and the grid settings on the regions stage
//     (resolution, tile_size, margin, erosion);
//  2. loads each region's preset (its `generator` override, else
//     resources/environments/<env>.fgen, else the built-in) and maps the
//     preset's first enabled terrain stage's params onto terrain.Settings
//     exactly as the classic stage does (terrainSettings). The preset's
//     size is ignored; its resolution is replaced by the union's; its seed
//     is the region's own (regionProviderSeed: doc seed, region key, preset
//     stage ID, the stage's seed param — so two regions growing one preset
//     differ, and editing one never reseeds the other);
//  3. fits the UNION GRID: world-aligned, origin and extent whole tiles from
//     the world origin, covering every region's footprint (`extent:
//     footprints`, the default) or footprint plus band (`bands`), padded to a
//     square (the extra tiles split evenly, the odd one on the max side);
//  4. runs terrain.GenerateUnion: providers over their own supports,
//     blended by the regions' weights (the SAME function the regions
//     preview paints), then drainage, terrace, droplets, talus and detail
//     once over the union — globally, or tiled with the margin when the
//     regions stage says `erosion: tiled` — and the masks;
//  5. writes everything the classic stage writes (heightmap, masks,
//     albedo, relief, terrain.yaml, tiles on one world grid with one
//     quantisation step, the far backdrop) plus a weight mask per
//     ENVIRONMENT (mask_biome_<env>.png, listed in terrain.yaml so
//     Terrain.Mask("biome_<env>") reads it) and biomes.png, and a
//     `biomes:` section in terrain.yaml.
//
// Its artifact is a *terrain.Terrain, so every stage that reads a terrain
// (terrain_material, scatter, vegetation, cliff_wall, wall_sheet, drift,
// environment) reads this one unchanged. Kind Shared, standing in for the
// presets' `terrain` stages (SharedFor), so a preset stage expanded per
// region that refers to its preset's terrain is rewritten to this stage.
//
// It is an INCREMENTAL kind (region_terrain_incremental.go): a rerun keeps
// last run's tiles and tiled-erosion windows and reuses every one whose
// inputs hash the same, so editing a far region with `erosion: tiled`
// re-erodes and recooks only the tiles it reaches.
//
// With one region whose footprint is a classic terrain's square, anchored
// at the origin, the output is the classic stage's to the byte (heights,
// masks, tiles): region_terrain_test.go's gate.

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"foundry/internal/procgen"
	"foundry/internal/procgen/regions"
	"foundry/internal/procgen/terrain"
)

// RegionTerrainKind is the stage's name.
const RegionTerrainKind = "region_terrain"

// TerrainSources are the kinds whose artifact is a *terrain.Terrain: what a
// stage reading a terrain may refer to. ground_edit (ground_edit.go) is one:
// a terrain with painted edits.
var TerrainSources = []string{TerrainKind, RegionTerrainKind, GroundEditKind}

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     RegionTerrainKind,
		Label:    "Region terrain",
		Category: "Level",
		Regional: procgen.RegionalShared,
		// The presets' terrain stages are this stage, per region.
		SharedFor: []string{TerrainKind},
		Version:   1,
		Doc: "One eroded terrain over every environment region: each region's preset terrain shapes its own ground, " +
			"the shapes blend across the transition bands, and drainage, droplets and talus run once over the whole " +
			"union so rivers and scree cross the seams. Writes what the terrain stage writes (heightmap, masks, albedo, " +
			"tiles on one world grid, the far backdrop) plus a weight mask per environment (mask_biome_<env>) and a biome overlay.",
		Params:    regionTerrainParams(),
		Run:       runRegionTerrain,
		Preview:   previewRegionTerrain,
		Load:      loadTerrain,
		Validate:  validateRegionTerrain,
		HashExtra: regionTerrainPresetsHash,
		// Keeps its tiles and erosion windows across runs and reuses the
		// ones whose inputs did not change (region_terrain_incremental.go).
		Incremental: true,
	})
}

func regionTerrainParams() []procgen.ParamDef {
	i := procgen.Int
	out := []procgen.ParamDef{
		{Name: "regions", Type: procgen.ParamStage, StageKinds: []string{RegionsKind}, Default: procgen.StageRef(""),
			Doc: "The regions stage whose regions (and resolution, tile size, erosion and margin) this terrain is grown from."},
		{Name: "seed", Type: procgen.ParamSeed, Default: procgen.Seed(0),
			Doc: "Changes the tiled erosion's window seeds. Each region's own shape is seeded by the region and its preset."},
		{Name: "extent", Type: procgen.ParamEnum, Default: procgen.Enum("footprints"), Choices: []string{"footprints", "bands"},
			Doc: "What the terrain covers: every region's footprint (footprints), or footprint plus half its transition band " +
				"and the edge meander (bands). Either way it is whole tiles from the world origin, padded to a square."},
		{Name: "window", Type: procgen.ParamInt, Label: "Erosion window (tiles)", Default: i(4), Min: 1, Max: 64,
			Doc: "With erosion: tiled (the regions stage), each window of this many tiles a side is eroded on its own, " +
				"with the regions stage's margin round it, and cross-faded into its neighbours."},
		{Name: "flow_mask", Added: true, Type: procgen.ParamEnum, Label: "Flow mask", Default: procgen.Enum("d8"), Choices: []string{"d8", "mfd"},
			Doc: "How the flow mask (washes, wash stones) is drawn: d8, one downhill neighbour per sample (as the terrain stage), " +
				"or mfd, flow shared among every lower neighbour. A blend band and a canyon's mouth leave broad, nearly flat ground " +
				"that d8 crosses in ruled 0°/45° lines; mfd spreads it and gathers it in the hollows. Heights are the same either way."},
		// Added: unset, no existing stage's hash moves (docs/regions/PHASE_6.md).
		{Name: "lead", Added: true, Type: procgen.ParamString, Label: "Lead region", Default: procgen.String(""),
			Doc: "The region (volume ID) that leads: global erosion runs with its preset's drainage, droplet and talus " +
				"numbers, the strata mask indexes its strata, and the region material takes its material-wide look (varnish, " +
				"bedding, strata). Empty: the region owning the most samples. Set it to the region with cliffs, so its walls " +
				"keep their own erosion and banding beside a larger, softer neighbour."},
	}
	// The classic stage's output params, verbatim.
	for _, d := range terrainParams() {
		switch d.Name {
		case "mesh_step", "collision", "texture", "material", "uv_mode", "uv_scale", "cook", "cook_memory":
			out = append(out, d)
		}
	}
	// Standing water (region_terrain_water.go), Added.
	out = append(out, regionWaterParams()...)
	out = append(out, procgen.ParamDef{Name: "preview_cells", Type: procgen.ParamInt, Label: "Preview cells", Default: i(256),
		Min: 32, Max: 1024, PreviewOnly: true,
		Doc: "The preview's samples a side, at most (a fast mode: the same landforms, coarser erosion, always global)."})
	return append(out, farParams()...)
}

// regionProviderSeed is a region's provider seed. A variable so the gate
// test can hand one region the classic stage's seed and compare bytes.
var regionProviderSeed = func(docSeed int64, regionKey, presetStageID string, defs []procgen.ParamDef, p procgen.Params) uint64 {
	return procgen.RegionStageSeed(docSeed, regionKey, presetStageID, defs, p)
}

// regionsStageParams is the referenced regions stage's resolved params.
func regionsStageParams(d *procgen.Doc, ref string) (*procgen.Stage, procgen.Params, error) {
	s := d.StageByID(ref)
	if s == nil || s.Kind != RegionsKind {
		return nil, nil, fmt.Errorf("Regions must name a regions stage (got %q)", ref)
	}
	return s, procgen.Resolve(procgen.KindByName(RegionsKind).Params, s.Params), nil
}

// presetTerrain is a region's preset and its terrain stage.
type presetTerrain struct {
	from   string
	stage  *procgen.Stage
	params procgen.Params
}

// loadPresetTerrain loads region r's preset and finds its terrain stage.
func loadPresetTerrain(r *regions.Region) (*presetTerrain, error) {
	pre, from, err := loadRegionPreset(r)
	if err != nil {
		return nil, err
	}
	for i := range pre.Stages {
		s := &pre.Stages[i]
		if s.Kind == TerrainKind && !s.Disabled {
			return &presetTerrain{from: from, stage: s, params: procgen.Resolve(procgen.KindByName(TerrainKind).Params, s.Params)}, nil
		}
	}
	return nil, fmt.Errorf("region %s: preset %s has no enabled terrain stage to grow its ground from", r.ID, from)
}

// regionTerrainPresetsHash folds every region's preset terrain params into
// the stage's hash: editing a preset reruns the terrain.
func regionTerrainPresetsHash(d *procgen.Doc, s *procgen.Stage, p procgen.Params) (string, error) {
	_, gp, err := regionsStageParams(d, p.Str("regions"))
	if err != nil {
		return "", err
	}
	tk := procgen.KindByName(TerrainKind)
	var parts []string
	rs := regionsFromParams(gp)
	sort.SliceStable(rs, func(a, b int) bool { return rs[a].Key < rs[b].Key })
	for i := range rs {
		pt, err := loadPresetTerrain(&rs[i])
		if err != nil {
			return "", err
		}
		parts = append(parts, rs[i].Key+"="+pt.stage.ID+":"+procgen.StageHash(tk, 0, pt.params, nil))
	}
	// The material its tiles wear by default (only when it has one: a doc
	// without a region_material hashes as it always did).
	if m, far := regionTerrainAutoMaterial(d, s, p); m != "" {
		parts = append(parts, "material="+m+","+far)
	}
	return strings.Join(parts, ","), nil
}

// regionTerrainAutoMaterial is the material (and backdrop material) a
// region terrain's tiles wear when its own `material` (`far_material`) is
// empty: the first enabled region_material stage of d that names it,
// gen_<doc>_<that stage>[_far] — "region_terrain's default material is the
// region_material's" (docs/regions/PHASE_3.md). "" when its params name
// one already, or no region_material reads it.
func regionTerrainAutoMaterial(d *procgen.Doc, s *procgen.Stage, p procgen.Params) (mat, far string) {
	if d == nil || s == nil || p.Str("material") != "" {
		return "", ""
	}
	for i := range d.Stages {
		m := &d.Stages[i]
		// Directly, or through a ground_edit of this terrain.
		if m.Kind != RegionMaterialKind || m.Disabled || !refersToTerrain(d, m.Params.Str("terrain"), s.ID) {
			continue
		}
		mat = "gen_" + safeName(d.Name, "gen") + "_" + safeName(m.ID, "stage")
		if p.Float("far") > 0 && p.Str("far_material") == "" {
			far = mat + "_far"
		}
		return mat, far
	}
	return "", ""
}

func validateRegionTerrain(d *procgen.Doc, s *procgen.Stage, p procgen.Params) []string {
	ref := p.Str("regions")
	if ref == "" {
		return []string{"Regions: name the level doc's regions stage"}
	}
	_, gp, err := regionsStageParams(d, ref)
	if err != nil {
		return nil // the generic stage-ref check says it
	}
	var out []string
	res, ts := gp.Float("resolution"), gp.Float("tile_size")
	per, ok := whole(ts, res)
	if !ok {
		out = append(out, fmt.Sprintf("the regions stage's tile size (%g m) must be a whole number of its %g m samples", ts, res))
	} else if per%p.Int("mesh_step") != 0 {
		out = append(out, fmt.Sprintf("Mesh step (%d) must divide a tile's %d cells", p.Int("mesh_step"), per))
	}
	if p.Float("far") > 0 {
		if _, ok := whole(ts, p.Float("far_cell")); !ok {
			out = append(out, fmt.Sprintf("the regions stage's tile size (%g m) must be a whole number of far cells (%g m)", ts, p.Float("far_cell")))
		}
	}
	return out
}

// unionPlan is a region terrain ready to generate.
type unionPlan struct {
	u    *terrain.Union
	set  *regions.Set
	pres []*presetTerrain
	// tile is the tile size; rect the union's square (world).
	tile float64
	rect terrain.Rect
}

// unionRect is the union's square: whole tiles from the world origin
// covering every region's footprint (or band), padded to a square with the
// extra tiles split evenly (the odd one on the max side).
func unionRect(set *regions.Set, tile float64, bands bool) terrain.Rect {
	var r regions.Rect
	for i := range set.Regions {
		b := set.RegionBounds(i)
		if bands {
			b = set.BandBounds(i)
		}
		r = r.Union(b)
	}
	snap := func(lo, hi float64) (int, int) {
		a := int(math.Floor(lo/tile + 1e-9))
		b := int(math.Ceil(hi/tile - 1e-9))
		return a, max(b, a+1)
	}
	x0, x1 := snap(r.MinX, r.MaxX)
	z0, z1 := snap(r.MinZ, r.MaxZ)
	side := max(x1-x0, z1-z0)
	pad := func(a, b int) (int, int) {
		extra := side - (b - a)
		return a - extra/2, b + extra - extra/2
	}
	x0, x1 = pad(x0, x1)
	z0, z1 = pad(z0, z1)
	return terrain.Rect{MinX: float64(x0) * tile, MinZ: float64(z0) * tile, MaxX: float64(x1) * tile, MaxZ: float64(z1) * tile}
}

// planUnion builds the union for set under the regions stage's params gp
// and this stage's params p.
func planUnion(d *procgen.Doc, set *regions.Set, gp, p procgen.Params, seed uint64) (*unionPlan, error) {
	if set == nil || set.Len() == 0 {
		return nil, fmt.Errorf("the regions stage has no regions: add region volumes to the level")
	}
	res, tile := gp.Float("resolution"), gp.Float("tile_size")
	if _, ok := whole(tile, res); !ok {
		return nil, fmt.Errorf("tile size %g m is not a whole number of %g m samples", tile, res)
	}
	rect := unionRect(set, tile, p.Str("extent") == "bands")
	n, ok := whole(rect.MaxX-rect.MinX, res)
	if !ok || n > 16384 {
		return nil, fmt.Errorf("the union is %.0f m at %g m: %d samples a side (at most 16384)", rect.MaxX-rect.MinX, res, n)
	}
	plan := &unionPlan{set: set, tile: tile, rect: rect}
	u := &terrain.Union{N: n, Cell: res, MinX: rect.MinX, MinZ: rect.MinZ, Seed: seed,
		Erosion: gp.Str("erosion"), Margin: int(math.Round(gp.Float("margin") / res))}
	per, _ := whole(tile, res)
	u.Window = p.Int("window") * per
	u.FlowMFD = p.Str("flow_mask") == "mfd"
	if lead := p.Str("lead"); lead != "" {
		li := set.Index(regions.Key(lead))
		if li < 0 {
			return nil, fmt.Errorf("lead region %q: no such region", lead)
		}
		u.Lead = li + 1
	}
	tk := procgen.KindByName(TerrainKind)
	for i := range set.Regions {
		r := &set.Regions[i]
		pt, err := loadPresetTerrain(r)
		if err != nil {
			return nil, err
		}
		env := procgen.Env{Params: pt.params, Seed: regionProviderSeed(d.Seed, r.Key, pt.stage.ID, tk.Params, pt.params)}
		s := terrainSettings(&env)
		s.Cell, s.Size = res, rect.MaxX-rect.MinX
		b := set.RegionBounds(i)
		// Canyons drawn to the region's edge run on through its band (and
		// the edge meander): as far as its own blend reaches.
		ext := b.MinX - set.BandBounds(i).MinX
		pv := terrain.Provider{Name: r.Key, Settings: s, Anchor: r.AnchorPoint(),
			Measure: terrain.Rect{MinX: b.MinX, MinZ: b.MinZ, MaxX: b.MaxX, MaxZ: b.MaxZ}, Extend: ext}
		pv.Key = providerKey(tk, s.Seed, pt, &pv)
		u.Providers = append(u.Providers, pv)
		plan.pres = append(plan.pres, pt)
	}
	if set.Len() > 1 {
		u.Weights = setWeights(set)
	}
	plan.u = u
	return plan, nil
}

// setWeights is the regions set's weight function in the terrain package's
// terms.
func setWeights(set *regions.Set) terrain.WeightFunc {
	return func(x, z float64, out []terrain.RegionWeight) []terrain.RegionWeight {
		var buf [8]regions.W
		ws := set.Weights(x, z, buf[:0])
		out = out[:0]
		for _, w := range ws {
			out = append(out, terrain.RegionWeight{Region: w.Region, Weight: w.Weight})
		}
		return out
	}
}

// environments is the set's environments, sorted, and each region's index
// into that list.
func environments(set *regions.Set) ([]string, []int) {
	seen := map[string]bool{}
	var envs []string
	for _, r := range set.Regions {
		if !seen[r.Environment] {
			seen[r.Environment] = true
			envs = append(envs, r.Environment)
		}
	}
	sort.Strings(envs)
	idx := make([]int, set.Len())
	for i, r := range set.Regions {
		idx[i] = sort.SearchStrings(envs, r.Environment)
	}
	return envs, idx
}

// biomeLayers are the per-environment weight planes of a union result and
// the overlay picture's RGB (colours mixed by weight).
func biomeLayers(set *regions.Set, res *terrain.Result) (envs []string, planes [][]float32, rgb []uint8) {
	envs, idx := environments(set)
	n := len(res.Grid.H)
	planes = make([][]float32, len(envs))
	for e := range planes {
		planes[e] = make([]float32, n)
	}
	for r := range set.Regions {
		w := res.RegionWeights(r)
		pl := planes[idx[r]]
		for k, v := range w {
			pl[k] += v
		}
	}
	cols := make([][3]float64, len(envs))
	for e, name := range envs {
		cols[e] = environmentColor(name)
	}
	rgb = make([]uint8, 3*n)
	for k := 0; k < n; k++ {
		var c [3]float64
		for e := range envs {
			w := float64(planes[e][k])
			c[0] += w * cols[e][0]
			c[1] += w * cols[e][1]
			c[2] += w * cols[e][2]
		}
		for ch := 0; ch < 3; ch++ {
			rgb[3*k+ch] = uint8(math.Round(math.Max(0, math.Min(1, c[ch])) * 255))
		}
	}
	return envs, planes, rgb
}

// biomesMeta is terrain.yaml's biomes section.
func biomesMeta(plan *unionPlan, res *terrain.Result, envs []string, p procgen.Params) *terrain.BiomesMeta {
	u := plan.u
	bm := &terrain.BiomesMeta{Overlay: terrain.BiomesFile, Erosion: u.Erosion, Lead: plan.set.Regions[res.Lead].Key,
		Extent: p.Str("extent")}
	if bm.Erosion == "" {
		bm.Erosion = terrain.ErosionGlobal
	}
	if bm.Erosion == terrain.ErosionTiled {
		bm.Window = float64(u.Window) * u.Cell
		bm.Margin = float64(u.Margin) * u.Cell
	}
	for _, e := range envs {
		bm.Environments = append(bm.Environments, terrain.BiomeEnvMeta{Name: e, Color: environmentColor(e), Mask: terrain.BiomeMask(e)})
	}
	for i := range plan.set.Regions {
		r := &plan.set.Regions[i]
		pv := &u.Providers[i]
		b := plan.set.RegionBounds(i)
		rm := terrain.BiomeRegionMeta{Key: r.Key, Environment: r.Environment, Preset: plan.pres[i].from, Stage: plan.pres[i].stage.ID,
			Seed: pv.Settings.Seed, Anchor: pv.Anchor, Bounds: [4]float64{b.MinX, b.MinZ, b.MaxX, b.MaxZ},
			Priority: r.Priority, Transition: r.Band()}
		for _, l := range pv.Settings.Strata.Layers {
			rm.Strata = append(rm.Strata, terrain.LayerMeta{Thickness: l.Thickness, Hardness: l.Hardness, Color: l.Color})
		}
		bm.Regions = append(bm.Regions, rm)
	}
	return bm
}
