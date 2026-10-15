package stages

// environments_builtin.go — the built-in environment presets
// (docs/regions/SPEC.md, "Environment presets").
//
// A preset is the document a region of that environment grows: its terrain
// stage is the region's field provider (size ignored — the region supplies
// bounds), its terrain_material and polyhaven_textures its ground material,
// and its placement stages are expanded once per region of a level
// generation doc (procgen/regional.go). Level-wide stages — the atmosphere,
// the sky, the spawn — belong to the level doc, not to any one environment,
// so a preset leaves them out.
//
// Registered here and written to a project's resources/environments/ only
// when no file of that name is there (procgen.WriteEnvironment), so an
// author's edits to a preset are never overwritten by a newer build.
//
//	desert_canyon   the desert_canyon template (PHASE_7/8) without its
//	                environment stage and target level
//	savannah        wide rolling plains — long swells, a dendritic net of
//	                shallow dry washes, no mesas, soft strata, no canyons —
//	                and its ground: Poly Haven dry grass (tinted gold),
//	                baked earth, stony soil and a pale boulder rock; scanned
//	                searsia and quiver trees in clumps, sparse shrubs,
//	                granite boulders and wash stones; and Poly Haven dry
//	                grass clumps in patches with searsia_burchellii as the
//	                acacia stand-in in clumps (since PRESETS_2: no engine
//	                foliage biome; it was the savannah biome's grass and
//	                acacias as vegetation volumes in phase 6)
//
// In a region world the desert preset carries fewer wash stones and bushes
// than its template (regionBudget): the CPU frame pays per placement.

import "foundry/internal/procgen"

// Built-in environment names (procgen/regions names the same strings for the
// adjacency table).
const (
	DesertCanyonEnvironment = "desert_canyon"
	SavannahEnvironment     = "savannah"
)

func init() {
	procgen.RegisterEnvironment(procgen.Environment{
		Name:  DesertCanyonEnvironment,
		Label: "Desert canyon",
		Doc:   "Red sandstone mesas cut by a stepped canyon, Poly Haven scans: the desert_canyon template as a region preset.",
		Color: [3]float64{0.86, 0.46, 0.24},
		New:   DesertCanyonPreset,
	})
	procgen.RegisterEnvironment(procgen.Environment{
		Name:  SavannahEnvironment,
		Label: "Savannah",
		Doc:   "Wide rolling plains, soft strata, gentle drainage, dry grass over baked red-brown earth, clumps of searsia and quiver trees, shrubs and granite boulders.",
		Color: [3]float64{0.78, 0.76, 0.3},
		New:   SavannahPreset,
	})
}

// DesertCanyonPreset is the desert canyon as an environment preset: the
// template's document less the stages that set the whole level.
func DesertCanyonPreset() *procgen.Doc {
	d := DesertCanyon(DesertCanyonEnvironment)
	d.Level = ""
	d.Doc = "Environment preset: desert canyon (red sandstone mesas, a stepped canyon, Poly Haven scans)"
	stages := d.Stages[:0]
	for _, s := range d.Stages {
		if s.Kind == EnvironmentKind {
			continue
		}
		stages = append(stages, s)
	}
	d.Stages = stages
	regionBudget(d, map[string]procgen.Params{
		"wash_stones": {"max_instances": procgen.Int(1200)},
		"bushes":      {"density": procgen.Float(0.6)},
	})
	return d
}

// regionBudget sets some of d's scatter rules' params (by rule name).
//
// A region shares the frame with its neighbours, and on the GTX 1070 the
// CPU frame grows by about 1 ms per thousand placements in view
// (docs/regions/PHASE_6.md: the canyon_savannah world at 16.6k placements
// ran 19–21 ms of CPU, at 7.9k 11–12 ms). So the presets carry fewer of the
// smallest, most numerous pieces than the standalone desert_canyon template
// does — wash stones and bushes, which the vegetation and the larger rocks
// read past from a few metres — and the template itself is unchanged.
func regionBudget(d *procgen.Doc, set map[string]procgen.Params) {
	for i := range d.Stages {
		if d.Stages[i].Kind != ScatterKind {
			continue
		}
		rules := d.Stages[i].Params["rules"]
		for k := range rules.Items {
			if over, ok := set[rules.Items[k].Str("name")]; ok {
				it := rules.Items[k].Clone()
				for name, v := range over {
					it[name] = v
				}
				rules.Items[k] = it
			}
		}
		d.Stages[i].Params["rules"] = rules
	}
}

// SavannahPreset is the savannah: its terrain, a rolling plain, and its
// ground material.
//
// The ground (docs/regions/PHASE_3.md; Poly Haven ids checked against the
// API's catalogue): withered_grass — sun-bleached dry grass — is the
// everywhere layer (terrain_material's `sand` key); dry_ground_01, baked
// cracked earth, takes the flats in patches (`cracked`); dry_ground_rocks,
// stony parched soil, the washes (`gravel`); rock_boulder_dry, a pale
// weathered boulder face, the steep ground (`rock`). Layer match is low:
// the grass is meant to read straw-yellow against red-brown earth. No
// strata bands or bedding (the canyon's are the lead's anyway), no
// varnish; macro variation for the scale of a plain. Phase 6 tinted the
// grass gold (sand_tint) so a plain does not read pale straw from 100 m,
// and cut the baked-earth cover.
//
// The placement (docs/regions/PHASE_4.md; ids checked against the live
// Poly Haven catalogue with `foundry polyhaven search -c plants` and
// `-c rocks`): trees are searsia_lucida (the karee, a southern-African
// savannah tree) and quiver_tree_01, decimated for scattering, in sparse
// clumps on gentle ground; shrubs are wild_rooibos_bush, shrub_04 and
// didelta_spinosa; boulders and stones are the namaqualand granite set
// the desert uses too (one import serves both). The grass (PRESETS_2:
// Poly Haven plants only) is grass_medium_02's dry clumps as scenery in
// patches — ~3,000 a km² at full weight — and the acacia stand-in is
// searsia_burchellii in clumps; until PRESETS_2 both were the engine's
// savannah foliage biome as vegetation volumes (phase 6).
func SavannahPreset() *procgen.Doc {
	f, i, s, ref, v2 := procgen.Float, procgen.Int, procgen.String, procgen.StageRef, procgen.Vec2
	asset := func(id string) procgen.Params { return procgen.Params{"id": s(id)} }
	assetT := func(id string, tris int) procgen.Params { return procgen.Params{"id": s(id), "max_triangles": i(tris)} }
	rules := procgen.List(
		// Big pieces first: each later rule keeps clear of them.
		procgen.Params{"name": s("tree_clumps"), "rocks": ref("trees"), "density": f(0.05), "scale": v2(0.8, 1.3),
			"slope": v2(0, 14), "cliff": v2(0, 0.2), "align": f(0), "tilt": f(0), "sink": f(0.02), "spacing": f(3),
			"cluster": f(1), "cluster_scale": f(70)},
		procgen.Params{"name": s("boulders"), "rocks": ref("boulders"), "max_triangles": i(6000), "density": f(0.12),
			"scale": v2(0.5, 1.6), "scale_bias": f(2.2), "slope": v2(4, 32), "cluster": f(0.9), "cluster_scale": f(40),
			"align": f(0.6), "sink": f(0.2), "match_ground": f(0.25)},
		// Shrubs and stones are the most numerous pieces; the grass
		// vegetation carries the ground cover between them, and the CPU
		// frame pays per placement (regionBudget), so both are kept sparse.
		procgen.Params{"name": s("shrubs"), "rocks": ref("shrubs"), "density": f(0.25), "scale": v2(0.6, 1.3),
			"slope": v2(0, 24), "cliff": v2(0, 0.3), "align": f(0.3), "tilt": f(0), "sink": f(0.04), "spacing": f(1.4),
			"cluster": f(0.7), "cluster_scale": f(30), "satellites": f(0.5), "satellite_spread": f(3)},
		procgen.Params{"name": s("stones"), "rocks": ref("stones"), "max_triangles": i(3000), "density": f(4),
			"scale": v2(0.6, 1.4), "scale_bias": f(2), "slope": v2(0, 18), "flow": v2(0.45, 1), "cluster": f(0.6),
			"cluster_scale": f(12), "align": f(0.9), "tilt": f(15), "sink": f(0.25), "spacing": f(0.9),
			"max_instances": i(900), "match_ground": f(0.3)},
	)
	soft := func(thick, hard, r, g, b float64) procgen.Params {
		return procgen.Params{"thickness": f(thick), "hardness": f(hard), "color": procgen.Color(r, g, b)}
	}
	layer := func(n, id string, scale float64) procgen.Params {
		return procgen.Params{"name": s(n), "id": s(id), "scale": f(scale)}
	}
	return &procgen.Doc{Name: SavannahEnvironment, Seed: 2718,
		Doc: "Environment preset: savannah (rolling plains with dry washes, gold grass, acacia and searsia clumps, shrubs, granite)",
		Stages: []procgen.Stage{
			{Kind: PolyHavenTexturesKind, ID: "tex", Params: procgen.Params{"layers": procgen.List(
				layer("grass", "withered_grass", 2.5), layer("baked", "dry_ground_01", 4),
				layer("stony", "dry_ground_rocks", 3), layer("rock", "rock_boulder_dry", 6))}},
			{Kind: TerrainKind, ID: "ground", Params: procgen.Params{
				"size": f(512), "resolution": f(0.5), "tile_size": f(128),
				// Long, low swells rather than mesa country: one swell every
				// ~900 m, a few octaves of smaller rolls on it.
				"relief": f(45), "frequency": f(0.0013), "octaves": i(5), "warp": f(140),
				"mesa_height": f(0), "mesa_coverage": f(0), "mesa_tiers": i(0),
				// Thick, soft, earthy beds: no ledges for the terrace to find.
				"strata": procgen.List(
					soft(18, 0.2, 0.62, 0.5, 0.34), soft(24, 0.25, 0.68, 0.55, 0.38), soft(30, 0.3, 0.58, 0.47, 0.33)),
				"terrace": f(0), "resistance": f(0.15),
				"canyons": procgen.List(),
				// Gentle drainage: a dendritic net of shallow dry washes down
				// the swells, soft-sided (hillslope creep), little basin
				// fill. No droplets: on slopes this low their trails pile
				// into parallel streaks a heightfield shows as a comb.
				"drainage": f(2.2), "drainage_fill": f(0.12), "drainage_diffusion": f(0.15),
				"droplets":    f(0),
				"talus_angle": f(26), "detail": f(0.1),
				"sand_color":  procgen.Color(0.7, 0.62, 0.4),
				"cook_memory": i(2000)}},
			{Kind: TerrainMaterialKind, ID: "ground_mat", Params: procgen.Params{"terrain": ref("ground"), "textures": ref("tex"),
				"sand": s("grass"), "cracked": s("baked"), "gravel": s("stony"), "rock": s("rock"),
				// Rock only where the plain breaks: banks and gully walls.
				"rock_slope": v2(30, 44), "cliff": f(0.6), "rock_scale": f(6),
				// Baked earth on the flats where nothing has settled, in
				// patches; stony soil only down the main drainage lines (a
				// plain's flow mask is broad: the canyon's wash band would
				// pave a quarter of it).
				// PRESETS_6: 0.08, not 0.3 — on a plain this flat the baked
				// earth (dry_ground_01, near white from 40 m) was most of
				// the ground, not the gold grass.
				"flat_slope": v2(4, 10), "cracked_cover": f(0.08), "sand_deposition": v2(0.3, 0.7),
				"wash_flow": v2(0.55, 0.85), "wash_width": f(2.5),
				"layer_match": f(0.15), "breakup": f(0.6), "breakup_scale": f(18),
				"strata": f(0), "streaks": f(0), "macro": f(0.22), "macro_scale": f(60),
				"anti_tile": procgen.Bool(true), "gravel_relief": f(0.03), "blend": f(0.3),
				// withered_grass is a sun-bleached scan: from 100 m it reads
				// as pale straw. Less blue deepens it to the gold of a dry
				// season savannah.
				// PRESETS_6: a deeper gold, the prairie presets' dry-grass
				// tint (environment_prairie.go: in a world with an oak
				// savannah its tint is the one the shared layer wears).
				"sand_tint": procgen.Color(0.85, 0.7, 0.45)}},
			{Kind: PolyHavenPlantsKind, ID: "trees", Params: procgen.Params{"assets": procgen.List(
				assetT("searsia_lucida", 250000), assetT("quiver_tree_01", 150000))}},
			{Kind: PolyHavenPlantsKind, ID: "shrubs", Params: procgen.Params{"assets": procgen.List(
				asset("wild_rooibos_bush"), asset("shrub_04"), assetT("didelta_spinosa", 120000))}},
			{Kind: PolyHavenRocksKind, ID: "boulders", Params: procgen.Params{"assets": procgen.List(
				asset("namaqualand_boulder_04"), asset("namaqualand_boulder_05")), "back": s("none"), "lite_triangles": i(5000)}},
			{Kind: PolyHavenRocksKind, ID: "stones", Params: procgen.Params{"assets": procgen.List(
				asset("namaqualand_stones_01")), "back": s("none"), "lite_triangles": i(2000), "collision": procgen.Enum("none")}},
			{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "ground": ref("ground_mat"),
				"rules": rules}},
			// The grass between the scanned plants (docs/regions/
			// PRESETS_2.md: Poly Haven plants only, no engine foliage
			// biome): grass_medium_02's dry clumps scaled 2.5–4.5× in
			// overlapping patches; and the acacia stand-in — Poly Haven has
			// no acacia — searsia_burchellii, a flat-crowned namaqualand
			// tree, only where a slow cluster noise is high, so it stands
			// in clumps (the engine acacias' rule, now scenery).
			plantsStage("grass_clumps", 0, presetAsset("grass_medium_02", 0)),
			plantsStage("acacia", 0, presetAsset("searsia_burchellii", 200000)),
			{Kind: ScatterKind, ID: "cover", Params: procgen.Params{"terrain": ref("ground"), "ground": ref("ground_mat"),
				"rules": procgen.List(
					// searsia_burchellii's parts are 2–3.4 m bushes as
					// scanned: grown 1.6–2.4× to a savannah tree's 5–8 m.
					procgen.Params{"name": s("acacias"), "rocks": ref("acacia"), "density": f(0.05), "scale": v2(1.6, 2.4),
						"slope": v2(0, 16), "cliff": v2(0, 0.2), "align": f(0), "tilt": f(0), "sink": f(0.03), "spacing": f(2),
						"cluster": f(1), "cluster_scale": f(70)},
				)}},
			// The grass itself on the grass instancer (environments_grass.go,
			// docs/regions/PRESETS_2B.md): grass_medium_02's dry clumps grown
			// to 0.5–1.3 m, a near-continuous dry-season sward thinning into
			// patches where the clumping noise is low, straw to pale gold.
			grassStage(
				grassWindOf(grassTint(grassRule("grass", "grass_clumps", "", 1.6, [2]float64{3, 5}, 22, 0.45, 40, 500),
					[3]float64{1.12, 0.98, 0.7}, [3]float64{1.0, 0.92, 0.62}, 50), 0.3, 0.04),
				grassTint(grassRule("tall_grass", "grass_clumps", "_e _d", 0.25, [2]float64{3.5, 5}, 18, 0.7, 25, 500),
					[3]float64{1.15, 1.0, 0.72}, [3]float64{1.05, 0.95, 0.66}, 50)),
		}}
}
