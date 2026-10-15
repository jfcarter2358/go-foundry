package stages

// environment_wetland.go — the `wetland` environment preset
// (docs/regions/PRESETS.md step 2; ENVIRONMENT.md priority 8).
//
// A marsh: flat, low ground just above a water table, standing water in
// its hollows and channels, reeds, mud at the water's edge.
//
// LANDFORM. Nearly flat: a base 0.1 m above the shared 0 m level with
// 2.2 m of slow relief and low hummocks (0.35 m every 5 m), and shallow
// pond hollows over a quarter of the ground (1 m deep, ~45 m across). The
// region's `water` stands at −0.25 m — BELOW the shared base — so every
// patch of its own ground below that is open water: the low lines of the
// relief read as channels between drier tussock ground, the hollows as
// pools (stages/region_terrain_water.go; closed hollows above the table
// fill to their spill as well, `ponds`). No droplets, almost no drainage
// or fill: water does not carve a marsh. Across a band the region rises to
// the base over 60 m (`edge_rise`), so its neighbours meet dry ground at
// 0 m, 25 cm above the water.
//
// GROUND. Temperate grass everywhere (leafy_grass, shared with the prairie
// and the oak savannah), mud under fallen leaves and moss on the flattest
// patches (brown_mud_leaves_01), lichen rock (shared; a marsh has almost no
// steep ground) and the WET cover — brown mud (brown_mud) — under the water
// and within 6 m of it, up to 0.35 m above it. Two of its four layers are
// shared.
//
// LIFE (Poly Haven only). Poly Haven has NO REEDS or rushes: the stand-in
// is grass_medium_01's tallest clumps grown 6–9× (1.5–2.8 m) in dense beds
// along every shore and out into the shallows (up to 0.45 m deep), its mid
// clumps 3–4.5× as tussock sedge everywhere else — both on the grass
// instancer (environments_grass.go, PRESETS_2B); nettle_plant and weed_plant_02 (the_shed
// collection) in drifts; Poly Haven fallen trunks (dead_tree_trunk, shared
// with the pine forest), the small coastal island_tree_02 (shared with the
// coast) as the odd stunted tree on the tussocks, verdant_trail shrubs
// (shrub_02, shrub_03) in clumps.
//
// BUDGET. ~1,200 scenery placements per km² (trees 0.012, shrubs 0.04,
// logs 0.012, weeds 0.03 per 100 m²); reeds and sedge ~3 M grass-instancer
// clumps per km².

import "foundry/internal/procgen"

// WetlandEnvironment is the preset's name.
const WetlandEnvironment = "wetland"

// The wetland's own ground layer.
var (
	layerMud = envLayer{"mud", "brown_mud", 3}
	// Mud under fallen leaves and moss (brown_mud_leaves_01): the flattest
	// tussock ground in patches.
	layerLeafMud = envLayer{"leaf_mud", "brown_mud_leaves_01", 3}
)

func init() {
	procgen.RegisterEnvironment(procgen.Environment{
		Name:  WetlandEnvironment,
		Label: "Wetland",
		Doc:   "A flat marsh just above a water table: open water in its hollows and channels, mud at the edges, reeds, lush grass, the odd stunted tree.",
		Color: [3]float64{0.3, 0.5, 0.42},
		New:   WetlandPreset,
	})
}

// WetlandPreset is the marsh's document.
func WetlandPreset() *procgen.Doc {
	f, i, s, ref, v2, b := procgen.Float, procgen.Int, procgen.String, procgen.StageRef, procgen.Vec2, procgen.Bool
	rules := procgen.List(
		procgen.Params{"name": s("trees"), "rocks": ref("trees"), "density": f(0.012), "scale": v2(0.6, 1.1),
			"slope": v2(0, 12), "height": v2(0.5, 1), "align": f(0), "tilt": f(4), "sink": f(0.05), "spacing": f(2),
			"cluster": f(0.9), "cluster_scale": f(40)},
		procgen.Params{"name": s("shrubs"), "rocks": ref("shrubs"), "density": f(0.04), "scale": v2(0.7, 1.3),
			"slope": v2(0, 15), "height": v2(0.4, 1), "align": f(0.3), "tilt": f(0), "sink": f(0.04), "spacing": f(1.4),
			"cluster": f(0.8), "cluster_scale": f(20), "collision": procgen.Enum("none")},
		procgen.Params{"name": s("logs"), "rocks": ref("logs"), "density": f(0.012), "scale": v2(0.7, 1.2),
			"slope": v2(0, 12), "align": f(0.95), "tilt": f(3), "sink": f(0.15), "spacing": f(1.5),
			"cluster": f(0.6), "cluster_scale": f(30)},
	)
	return &procgen.Doc{Name: WetlandEnvironment, Seed: 1732,
		Doc: "Environment preset: wetland (flat marsh at a water table, pools and channels, mud, reeds, grass)",
		Stages: []procgen.Stage{
			texturesStage(layerLeafyGrass, layerLeafMud, layerLichenRock, layerMud),
			{Kind: TerrainKind, ID: "ground", Params: plainTerrain(procgen.Params{
				"base_height": f(0.1), "relief": f(2.2), "frequency": f(0.006), "octaves": i(5), "warp": f(60),
				"hummock_height": f(0.35), "hummock_spacing": f(5),
				"pond_cover": f(0.25), "pond_depth": f(1), "pond_scale": f(45),
				"drainage": f(0.3), "drainage_fill": f(0.02), "drainage_diffusion": f(0.3),
				"droplets": f(0), "talus_angle": f(25), "talus_iterations": i(20), "detail": f(0.08),
				"sand_color": procgen.Color(0.36, 0.38, 0.24),
				"water":      b(true), "water_level": f(-0.25),
				"ponds": b(true), "pond_min_depth": f(0.2), "pond_min_area": f(30),
				"base_elevation": f(0), "edge_rise": f(60)})},
			{Kind: TerrainMaterialKind, ID: "ground_mat", Params: procgen.Params{"terrain": ref("ground"), "textures": ref("tex"),
				"sand": s("grass"), "cracked": s("leaf_mud"), "gravel": s("grass"), "rock": s("rock"),
				"rock_scale": f(sharedRockScale), "rock_slope": v2(35, 50), "cliff": f(0.8),
				"flat_slope": v2(1, 3), "cracked_cover": f(0.3), "sand_deposition": v2(0.5, 0.9),
				"wash_flow": v2(0.8, 0.95), "wash_width": f(2),
				"layer_match": f(0.1), "breakup": f(0.6), "breakup_scale": f(12),
				"strata": f(0), "streaks": f(0), "macro": f(0.25), "macro_scale": f(30),
				"anti_tile": procgen.Bool(true), "blend": f(0.3),
				"sand_tint": procgen.Color(0.92, 0.98, 0.86),
				"wet":       s("mud"), "wet_distance": f(6), "wet_height": f(0.35)}},
			plantsStage("trees", 15000, presetAsset("island_tree_02", 150000)),
			plantsStage("shrubs", 6000, presetAsset("shrub_02", 0), presetAsset("shrub_03", 0)),
			rocksStage("logs", 6000, "", presetAsset("dead_tree_trunk", 40000)),
			{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "ground": ref("ground_mat"),
				"rules": rules}},
			plantsStage("grass", 0, presetAsset("grass_medium_01", 0)),
			plantsStage("weeds", 0, presetAsset("nettle_plant", 0), presetAsset("weed_plant_02", 0)),
			coverStage(
				withHeight(coverRule("weeds", "weeds", 0.03, [2]float64{0.8, 1.3}, 15, 12, 0.85, 0), 0.4, 1)),
			// Reed beds and the wet meadow on the grass instancer
			// (environments_grass.go). Poly Haven has no reeds or rushes:
			// grass_medium_01's tall clumps grown 6–9× (1.5–2.8 m) stand in,
			// dense along every shore and out into the shallows (0.45 m), the
			// mid clumps as tussock sedge over the rest.
			grassStage(
				grassWindOf(grassShore(grassTint(grassRule("reeds", "grass", "tall", 4, [2]float64{6, 9}, 12, 0.3, 18, 400),
					[3]float64{0.95, 1.0, 0.7}, [3]float64{1.1, 1.0, 0.68}, 30), 0, 7, 0.45), 0.4, 0.06),
				grassTint(grassRule("tussocks", "grass", "mid", 1.2, [2]float64{3, 4.5}, 20, 0.45, 20, 500),
					[3]float64{0.9, 1.0, 0.72}, [3]float64{1.0, 0.98, 0.66}, 40)),
		}}
}
