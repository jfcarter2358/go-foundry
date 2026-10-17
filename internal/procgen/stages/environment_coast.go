package stages

// environment_coast.go — the `coast` environment preset
// (docs/regions/PRESETS.md step 2; ENVIRONMENT.md priority 3).
//
// A rocky coast: sea, a narrow sand beach under a wave-cut cliff, dune grass
// on the land behind it.
//
// LANDFORM. The terrain stage's coastline (procgen/terrain/landform.go):
// the sea lies toward the footprint's +x side, the shoreline 170 m inside
// it, meandering ±45 m into bays and headlands; the sea floor shelves to
// 10 m below sea level over 120 m; a 35 m beach rises 2.5 m to the foot of
// a 24 m wave-cut cliff over a 6 m run; inland the land's own low relief
// (20 m swells) returns over 120 m. Erosion is kept light (drainage 0.6,
// little diffusion, no droplets, 55° talus): with step 2's (1.6, 0.12,
// 0.3, 32°) a 12 m cliff wore down to a 24° sand slope that never read as
// rock (docs/regions/PRESETS_2B.md); now ~23 m stand at ~43°, steep enough
// for the rock layer. Sea level is −1.5 m — below the shared 0 m base the
// presets meet at — and the region's `water` fills everything of its own
// below it as the sea's surface (stages/region_terrain_water.go). The side
// is a property of the preset: a coast along another edge of a world is
// the same preset with `coast` set to that side (or a drawn `coast_line`).
//
// GROUND. Beach sand everywhere (coast_sand_01, Poly Haven's smugglers_cove
// collection, an aerial scan, so a 8 m repeat); the WET cover — damp,
// darker sand (damp_beach_sand) — within 14 m of the water and less than
// 0.9 m above it, the swash zone and the sea floor; lichen rock (shared)
// on the cliff. Three layers. (The shared scree in the gullies was tried
// and dropped: the MFD flow mask paved most of the land with it.) The land
// behind the beach is sand too — a dune coast; terrain_material has no
// rule that tells a beach flat from an inland one, so a grassy headland
// would need one.
//
// LIFE (Poly Haven only). Smugglers_cove rock scans: coast_land_rocks
// (10 m blocks of the cliff fallen to its foot), coast_rocks_05 (3 m) and
// sand_rocks_small_01 (flat sandy reef rock) along the beach and the cliff
// foot; island_tree_02, a small wind-bent coastal tree, in a few clumps on
// the land. Dune grass is grass_medium_02 — Poly Haven's dry, brown-green
// clumps, grown 3–4.5×, pale — on the grass instancer
// (environments_grass.go) thick on the land behind the beach and in sparse
// tufts down onto it; shrub_sorrel_01 (smugglers_cove's sorrel) in
// small drifts on the land.
//
// BUDGET. ~600 scenery placements per km² (rocks 0.09, trees 0.02, sorrel
// 0.03 per 100 m² on their bands); dune grass ~0.75 M grass-instancer
// clumps per km².

import "foundry/internal/procgen"

// CoastEnvironment is the preset's name.
const CoastEnvironment = "coast"

// The coast's own ground layers (environments_shared.go has the shared
// ones).
var (
	layerCoastSand = envLayer{"beach", "coast_sand_01", 8}
	layerDampSand  = envLayer{"damp_sand", "damp_beach_sand", 3}
)

func init() {
	procgen.RegisterEnvironment(procgen.Environment{
		Name:  CoastEnvironment,
		Label: "Rocky coast",
		Doc:   "A shoreline at sea level: the sea, a sand beach with wet sand at the water, a wave-cut cliff, fallen coast rocks and dune grass behind.",
		Color: [3]float64{0.24, 0.6, 0.78},
		New:   CoastPreset,
	})
}

// CoastPreset is the coast's document.
func CoastPreset() *procgen.Doc {
	f, i, s, ref, v2, b := procgen.Float, procgen.Int, procgen.String, procgen.StageRef, procgen.Vec2, procgen.Bool
	rules := procgen.List(
		procgen.Params{"name": s("cliff_rocks"), "rocks": ref("blocks"), "density": f(0.03), "scale": v2(0.4, 0.9),
			"scale_bias": f(1.6), "slope": v2(0, 50), "cliff_base": f(0.9), "cliff_base_distance": f(18),
			"cluster": f(0.7), "cluster_scale": f(40), "align": f(0.7), "sink": f(0.25), "match_ground": f(0.2)},
		procgen.Params{"name": s("beach_rocks"), "rocks": ref("rocks"), "density": f(0.06), "scale": v2(0.4, 1.3),
			"scale_bias": f(2), "slope": v2(0, 30), "height": v2(0, 0.35), "cluster": f(0.85), "cluster_scale": f(25),
			"align": f(0.8), "sink": f(0.3), "match_ground": f(0.2)},
		procgen.Params{"name": s("trees"), "rocks": ref("trees"), "density": f(0.02), "scale": v2(0.7, 1.2),
			"slope": v2(0, 18), "height": v2(0.45, 1), "cliff": v2(0, 0.2), "align": f(0), "tilt": f(0), "sink": f(0.03),
			"spacing": f(2), "cluster": f(1), "cluster_scale": f(50)},
	)
	return &procgen.Doc{Name: CoastEnvironment, Seed: 1618,
		Doc: "Environment preset: rocky coast (sea at −1.5 m, beach, wave-cut cliff, coast rocks, dune grass)",
		Stages: []procgen.Stage{
			texturesStage(layerCoastSand, layerDampSand, layerLichenRock),
			{Kind: TerrainKind, ID: "ground", Params: plainTerrain(procgen.Params{
				"relief": f(20), "frequency": f(0.003), "octaves": i(5), "warp": f(90),
				"coast": procgen.Enum("+x"), "sea_level": f(-1.5), "coast_offset": f(170), "coast_meander": f(45),
				"beach_width": f(35), "beach_rise": f(2.5), "shelf_width": f(120), "sea_depth": f(10),
				"cliff_height": f(24), "cliff_width": f(6), "coast_inland": f(120),
				"drainage": f(0.6), "drainage_fill": f(0.1), "drainage_diffusion": f(0.02),
				"droplets": f(0), "talus_angle": f(55), "detail": f(0.2),
				"sand_color": procgen.Color(0.7, 0.64, 0.52),
				"water":      b(true), "water_level": f(-1.5)})},
			{Kind: TerrainMaterialKind, ID: "ground_mat", Params: procgen.Params{"terrain": ref("ground"), "textures": ref("tex"),
				"sand": s("beach"), "cracked": s("beach"), "gravel": s("beach"), "rock": s("rock"),
				"rock_scale": f(sharedRockScale), "rock_slope": v2(30, 44), "cliff": f(0.7),
				"flat_slope": v2(4, 10), "cracked_cover": f(0), "sand_deposition": v2(0.3, 0.7),
				"wash_flow": v2(0.6, 0.9), "wash_width": f(2.5),
				"layer_match": f(0.1), "breakup": f(0.5), "breakup_scale": f(18),
				"strata": f(0), "streaks": f(0.15), "macro": f(0.2), "macro_scale": f(50), "macro_vertical": f(0.3),
				"anti_tile": procgen.Bool(true), "gravel_relief": f(0.03), "blend": f(0.3),
				"wet": s("damp_sand"), "wet_distance": f(14), "wet_height": f(0.9)}},
			rocksStage("blocks", 20000, "", presetAsset("coast_land_rocks_02", 150000), presetAsset("coast_land_rocks_03", 150000)),
			rocksStage("rocks", 6000, "", presetAsset("coast_rocks_05", 60000), presetAsset("sand_rocks_small_01", 60000)),
			plantsStage("trees", 15000, presetAsset("island_tree_02", 150000)),
			{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "ground": ref("ground_mat"),
				"rules": rules}},
			plantsStage("grass", 0, presetAsset("grass_medium_02", 0)),
			plantsStage("sorrel", 0, presetAsset("shrub_sorrel_01", 0)),
			coverStage(
				withHeight(coverRule("sorrel", "sorrel", 0.03, [2]float64{1, 1.6}, 20, 12, 0.9, 0), 0.4, 1)),
			// Marram-like dune grass on the grass instancer
			// (environments_grass.go): grass_medium_02's clumps, pale and
			// wind-tossed, thick on the land behind the beach and in sparse
			// tufts down onto it.
			grassStage(
				grassWindOf(withHeight(grassTint(grassRule("dune_grass", "grass", "", 1.4, [2]float64{3, 4.5}, 30, 0.55, 22, 500),
					[3]float64{1.12, 1.08, 0.82}, [3]float64{1.0, 1.02, 0.76}, 30), 0.4, 1), 0.45, 0.06),
				withHeight(grassTint(grassRule("beach_grass", "grass", "", 0.15, [2]float64{2.5, 3.5}, 14, 0.8, 15, 500),
					[3]float64{1.12, 1.08, 0.82}, [3]float64{1.0, 1.02, 0.76}, 30), 0.25, 0.4)),
		}}
}
