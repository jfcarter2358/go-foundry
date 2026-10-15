package stages

// environment_tundra.go — the `tundra` environment preset
// (docs/regions/PRESETS.md step 2; ENVIRONMENT.md priority 7).
//
// Cold lowland: low rolling ground heaved into hummocks, thermokarst ponds,
// lichen and sedge, snow lingering in patches, erratic boulders.
//
// LANDFORM. Low relief (10 m swells every ~350 m) under the terrain
// stage's frost-heave hummocks (0.6 m domes every 7 m) and thermokarst
// ponds — flat-floored hollows over about a fifth of the ground, 1.2 m
// deep, ~60 m across (procgen/terrain/landform.go) — which the region
// terrain fills with water (`ponds`) wherever a hollow is closed, at least
// 0.3 m deep and 40 m² (stages/region_terrain_water.go). Drainage is weak
// and the basin fill nearly off, so the hollows survive erosion. The relief
// is low enough to meet any neighbour at the shared 0 m base without an
// edge rise.
//
// GROUND. Alpine turf — moss and lichen over stones — everywhere
// (rocks_ground_03, shared with alpine); bare frozen ground on the
// flattest ground in patches (ground_grey, the volcanic preset's ash too);
// lichen rock on the few steep banks (shared); and the SNOW cover in drifts
// above 1.5–4.5 m with a strong breakup, so snow lies in patches on the
// higher swells rather than as a line. The drainage lines stay turf. Four
// layers, so it meets any other preset within a tile's eight (the shared
// scree in its washes, tried first, made five and forced the adjacency
// table to keep it from every four-layer grassland).
//
// LIFE (Poly Haven only). Sedge tufts — grass_medium_02's dry clumps,
// shared with the savannah, short (1.5–2.6×) and sparse, on the grass
// instancer (environments_grass.go) — in patches where the ground is gentle;
// dwarf shrubs (shrub_04 at a third to a half of its size) in drifts;
// boulder_01 (shared with alpine) as scattered glacial erratics and the
// pine_forest collection's mossy rock sets in clusters. No lichen or
// cotton grass models exist on Poly Haven: the turf texture carries them.
//
// BUDGET. ~1,200 scenery placements per km² (rocks 0.05, shrubs 0.04 per
// 100 m²); sedge ~0.7 M grass-instancer clumps per km².

import "foundry/internal/procgen"

// TundraEnvironment is the preset's name.
const TundraEnvironment = "tundra"

// The tundra's own ground layer.
var layerFrozenGround = envLayer{"frozen", "ground_grey", 3}

func init() {
	procgen.RegisterEnvironment(procgen.Environment{
		Name:  TundraEnvironment,
		Label: "Tundra",
		Doc:   "Low cold country heaved into hummocks with thermokarst ponds: lichen turf, frozen ground, snow patches, sedge and dwarf shrubs, erratic boulders.",
		Color: [3]float64{0.62, 0.7, 0.62},
		New:   TundraPreset,
	})
}

// TundraPreset is the tundra's document.
func TundraPreset() *procgen.Doc {
	f, i, s, ref, v2, b := procgen.Float, procgen.Int, procgen.String, procgen.StageRef, procgen.Vec2, procgen.Bool
	rules := procgen.List(
		procgen.Params{"name": s("erratics"), "rocks": ref("erratics"), "density": f(0.02), "scale": v2(0.6, 2.2),
			"scale_bias": f(2.4), "slope": v2(0, 20), "cluster": f(0.4), "cluster_scale": f(60),
			"align": f(0.5), "sink": f(0.3), "match_ground": f(0.25)},
		procgen.Params{"name": s("mossy_rocks"), "rocks": ref("mossy"), "density": f(0.03), "scale": v2(0.4, 0.9),
			"slope": v2(0, 25), "cluster": f(0.85), "cluster_scale": f(25), "align": f(0.8), "sink": f(0.3)},
	)
	return &procgen.Doc{Name: TundraEnvironment, Seed: 1414,
		Doc: "Environment preset: tundra (hummocks, thermokarst ponds, lichen turf, frozen ground, snow patches, erratics)",
		Stages: []procgen.Stage{
			texturesStage(layerAlpineTurf, layerFrozenGround, layerLichenRock, layerSnow),
			{Kind: TerrainKind, ID: "ground", Params: plainTerrain(procgen.Params{
				"relief": f(10), "frequency": f(0.0028), "octaves": i(5), "warp": f(100),
				"hummock_height": f(0.6), "hummock_spacing": f(7),
				"pond_cover": f(0.2), "pond_depth": f(1.2), "pond_scale": f(60),
				"drainage": f(0.7), "drainage_fill": f(0.02), "drainage_diffusion": f(0.2),
				"droplets": f(0), "talus_angle": f(30), "detail": f(0.12),
				"sand_color": procgen.Color(0.52, 0.5, 0.42),
				"ponds":      b(true), "pond_min_depth": f(0.3), "pond_min_area": f(40)})},
			{Kind: TerrainMaterialKind, ID: "ground_mat", Params: procgen.Params{"terrain": ref("ground"), "textures": ref("tex"),
				"sand": s("turf"), "cracked": s("frozen"), "gravel": s("turf"), "rock": s("rock"),
				"rock_scale": f(sharedRockScale), "rock_slope": v2(28, 42), "cliff": f(0.6),
				"flat_slope": v2(1.5, 5), "cracked_cover": f(0.35), "sand_deposition": v2(0.5, 0.9),
				"wash_flow": v2(0.55, 0.85), "wash_width": f(2.5),
				"layer_match": f(0.1), "breakup": f(0.7), "breakup_scale": f(14),
				"strata": f(0), "streaks": f(0), "macro": f(0.25), "macro_scale": f(35),
				"anti_tile": procgen.Bool(true), "gravel_relief": f(0.03), "blend": f(0.3),
				// Snow patches on the tops of its own swells, measured on its
				// own height range (snow_reference region, PRESETS_6). That
				// range is a few metres (hummocks and ponds), so the line
				// runs from 0.9 of it to 2.2 times it: a band climbing toward
				// a range or a cone (heights past the tundra's own top) goes
				// white only once it has climbed well clear of the tundra —
				// in world metres (1.5–4.5), or at 0.66–0.97 of the range,
				// the foot of the showcase's volcanic cones was a snowfield.
				"snow": s("snow"), "snow_reference": procgen.Enum("region"), "snow_height": v2(0.9, 2.2), "snow_slope": v2(20, 34),
				// rocks_ground_03 is a sunlit olive-tan: darker and greener
				// reads as wet autumn tundra.
				"sand_tint":    procgen.Color(0.72, 0.78, 0.62),
				"snow_breakup": f(0.85), "snow_breakup_scale": f(22)}},
			rocksStage("erratics", 5000, "", presetAsset("boulder_01", 0)),
			rocksStage("mossy", 5000, "", presetAsset("rock_moss_set_02", 0)),
			{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "ground": ref("ground_mat"),
				"rules": rules}},
			plantsStage("sedge", 0, presetAsset("grass_medium_02", 0)),
			plantsStage("dwarf_shrubs", 0, presetAsset("shrub_04", 0)),
			coverStage(
				coverRule("dwarf_shrubs", "dwarf_shrubs", 0.04, [2]float64{0.35, 0.55}, 25, 15, 0.8, 0)),
			// Sedge and cotton-grass tufts on the grass instancer
			// (environments_grass.go): grass_medium_02 at 1.5–2.6× (0.25–0.6
			// m), short and sparse, in patches; no shadows (too low to matter).
			grassStage(
				grassRange(grassWindOf(grassTint(grassRule("sedge", "sedge", "", 0.9, [2]float64{1.5, 2.6}, 25, 0.6, 18, 700),
					[3]float64{0.95, 0.92, 0.7}, [3]float64{1.05, 0.9, 0.62}, 30), 0.25, 0.03), 0.8, false)),
		}}
}
