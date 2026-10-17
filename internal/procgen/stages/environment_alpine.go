package stages

// environment_alpine.go — the `alpine` environment preset
// (docs/regions/PRESETS.md step 2; ENVIRONMENT.md priority 4).
//
// High mountains: ridged ranges, glacial valleys, scree aprons, snow.
//
// LANDFORM. The terrain stage's ridged mountains (procgen/terrain/
// landform.go): ranges ~180 m high on a ~750 m spacing (height over scale
// 0.24, under the 0.25 past which walls are too steep to stand), five
// octaves of spurs and gullies, the valley floors flattened toward a U
// (`ridge_valleys`). Drainage and droplets cut the flanks; a steep talus
// angle (37°) lets thermal erosion pile scree aprons under the walls. The
// range rises from the shared 0 m base over 160 m inside the footprint
// (`edge_rise`), so a forest or a plain beside it meets its foothills, not
// a cliff.
//
// GROUND. Scree is the everywhere layer (rocks_ground_01); alpine turf —
// olive moss and lichen over small stones (rocks_ground_03) — takes the
// gentle ground in patches (`cracked` with a wide flat band, so the valley
// floors and benches go green); grey rock (rock_surface at a 16 m repeat,
// tinted darker and cool) holds the walls — lichen_rock at 9 m, the step-2
// choice, read orange-brown and showed its repeat as a lattice across a
// 200 m face (docs/regions/PRESETS_2B.md); and the SNOW cover (terrain_material_cover.go) lies above 70–110 m
// on slopes gentler than 40–56°, bent into drifts and bare patches by noise,
// so the ridges are white with dark rock ribs through them. The snow line
// is measured on the massif's OWN relief (`snow_reference: region`,
// PRESETS_6): snow from 36 to 52 % of the way from its core's foot to its
// summits, whatever its footprint's size, base or neighbours — in world
// metres it was 70–110 m, which a narrow region never reached and a broad
// one buried to the foot. Four layers;
// turf and snow shared with tundra, scree its own, the rock its own
// palette entry. PRESETS_6 darkened the rock (granite grey, not the scan's
// pale buff) and calmed the valley floors' scree/turf swirl: a wider,
// weaker breakup and the scree wash only down the main channels.
//
// LIFE (Poly Haven only). Rock scans: boulder_01 on the aprons, the
// verdant-trail rock faces (rock_face_01, rock_face_02 — light, ~25k
// triangles) and mountainside at the foot of the walls, and the
// namaqualand_rocks_01 set as scree debris on the aprons (deposition);
// grass_medium_01's small and mid clumps as short sparse meadow tufts on
// the valley floors and benches (1.6–2.4×), on the grass instancer
// (environments_grass.go). No alpine flowers: Poly Haven has none.
//
// BUDGET. Measured ~360 scenery placements per km² (the bands are
// narrow: boulders 0.08, faces 0.04, mountainside 0.01, debris 0.06 per
// 100 m² where they allow); the meadow tufts are ~0.25 M grass clumps per
// km² on the instancer.

import "foundry/internal/procgen"

// AlpineEnvironment is the preset's name.
const AlpineEnvironment = "alpine"

func init() {
	procgen.RegisterEnvironment(procgen.Environment{
		Name:  AlpineEnvironment,
		Label: "Alpine",
		Doc:   "Ridged mountains with glacial valleys and scree aprons: snow on the heights, grey rock walls, alpine turf below.",
		Color: [3]float64{0.82, 0.86, 0.92},
		New:   AlpinePreset,
	})
}

// AlpinePreset is the alpine mountains' document.
func AlpinePreset() *procgen.Doc {
	f, i, s, ref, v2 := procgen.Float, procgen.Int, procgen.String, procgen.StageRef, procgen.Vec2
	rules := procgen.List(
		procgen.Params{"name": s("cliff_faces"), "rocks": ref("faces"), "density": f(0.04), "scale": v2(0.8, 1.8),
			"slope": v2(25, 70), "cliff_base": f(0.8), "cliff_base_distance": f(20), "cluster": f(0.7), "cluster_scale": f(40),
			"align": f(0.8), "sink": f(0.3), "match_ground": f(0.35)},
		procgen.Params{"name": s("mountainside"), "rocks": ref("crags"), "density": f(0.01), "scale": v2(0.8, 1.5),
			"slope": v2(30, 75), "cliff": v2(0.2, 1), "cluster": f(0.6), "cluster_scale": f(60),
			"align": f(0.9), "sink": f(0.35), "match_ground": f(0.35)},
		procgen.Params{"name": s("boulders"), "rocks": ref("boulders"), "density": f(0.08), "scale": v2(0.4, 1.8),
			"scale_bias": f(2.4), "slope": v2(10, 38), "deposition": v2(0.15, 1), "cluster": f(0.7), "cluster_scale": f(30),
			"align": f(0.6), "sink": f(0.2), "match_ground": f(0.3)},
	)
	return &procgen.Doc{Name: AlpineEnvironment, Seed: 4807,
		Doc: "Environment preset: alpine (ridged ranges, glacial valleys, scree, snow above the line, grey rock)",
		Stages: []procgen.Stage{
			texturesStage(layerScree, layerAlpineTurf, layerAlpineRock, layerSnow),
			{Kind: TerrainKind, ID: "ground", Params: plainTerrain(procgen.Params{
				// The massif stands 80 m above the shared base (base_height)
				// and edge_rise brings its rim down to it (PRESETS_6).
				"base_height": f(80), "relief": f(12), "frequency": f(0.004), "octaves": i(5), "warp": f(80),
				// PRESETS_6: a massif, not a ridge or two over a plain — 0.45
				// valleys flattened ~80 % of a region to the floor (its
				// median stood at 0.4 m in the showcase); shallower troughs,
				// closer and higher ridges.
				"ridge_height": f(220), "ridge_scale": f(620), "ridge_octaves": i(5), "ridge_valleys": f(0.2), "ridge_warp": f(150),
				"strata": procgen.List(
					procgen.Params{"thickness": f(40), "hardness": f(0.7), "color": procgen.Color(0.46, 0.46, 0.45)},
					procgen.Params{"thickness": f(30), "hardness": f(0.6), "color": procgen.Color(0.52, 0.51, 0.49)}),
				"resistance": f(0.5),
				"drainage":   f(2.5), "drainage_fill": f(0.1), "drainage_diffusion": f(0.08),
				"droplets": f(1), "talus_angle": f(37), "detail": f(0.4),
				"sand_color":     procgen.Color(0.55, 0.55, 0.53),
				// 160 m, not 220: the rise is measured from the nearest
				// sample the region does not own OUTRIGHT, and with a 200 m
				// band plus the warp that left a 0.55 km² massif rising
				// fully on a few hectares (PRESETS_6: median 4 m).
				"base_elevation": f(0), "edge_rise": f(160)})},
			{Kind: TerrainMaterialKind, ID: "ground_mat", Params: procgen.Params{"terrain": ref("ground"), "textures": ref("tex"),
				"sand": s("scree"), "cracked": s("turf"), "gravel": s("scree"), "rock": s("rock"),
				"rock_scale": f(layerAlpineRock.scale), "rock_slope": v2(33, 46), "cliff": f(0.7),
				// Turf on the gentle ground: valley floors, benches, cols.
				"flat_slope": v2(14, 24), "cracked_cover": f(0.8), "sand_deposition": v2(0.75, 1),
				"wash_flow": v2(0.78, 0.96), "wash_width": f(2),
				"layer_match": f(0.1), "breakup": f(0.3), "breakup_scale": f(40),
				"strata": f(0), "streaks": f(0.2), "macro": f(0.1), "macro_scale": f(80), "macro_vertical": f(0.4),
				"anti_tile": procgen.Bool(true), "gravel_relief": f(0.04), "blend": f(0.3),
				"snow": s("snow"), "snow_reference": procgen.Enum("region"), "snow_height": v2(0.36, 0.52), "snow_slope": v2(40, 56),
				// rock_surface is pale in the haze: darker and a little cool, so the
				// faces read as granite against the scree and the snow.
				"rock_tint":    procgen.Color(0.5, 0.52, 0.56),
				// The scree a cool grey too, not the scan's warm buff.
				"sand_tint": procgen.Color(0.78, 0.79, 0.8),
				"snow_breakup": f(0.6), "snow_breakup_scale": f(45)}},
			rocksStage("boulders", 5000, "", presetAsset("boulder_01", 0)),
			rocksStage("faces", 0, "", presetAsset("rock_face_01", 0), presetAsset("rock_face_02", 0)),
			rocksStage("crags", 20000, "", presetAsset("mountainside", 150000)),
			{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "ground": ref("ground_mat"),
				"rules": rules}},
			rocksStage("debris", 3000, "none", presetAsset("namaqualand_rocks_01", 20000)),
			plantsStage("grass", 0, presetAsset("grass_medium_01", 0)),
			coverStage(
				procgen.Params{"name": s("scree_debris"), "rocks": ref("debris"), "density": f(0.06), "scale": v2(0.6, 1.4),
					"slope": v2(15, 40), "deposition": v2(0.1, 1), "cliff": v2(0, 0.4), "cluster": f(0.6), "cluster_scale": f(25),
					"align": f(0.9), "tilt": f(10), "sink": f(0.2), "spacing": f(0.8), "max_triangles": i(3000),
					"collision": procgen.Enum("none"), "match_ground": f(0.3)}),
			// Short alpine meadow tufts on the grass instancer
			// (environments_grass.go) on the benches and valley floors:
			// grass_medium_01's small and mid clumps at 1.6–2.4×.
			grassStage(
				grassRange(grassWindOf(withHeight(grassTint(grassRule("meadow_grass", "grass", "small mid", 0.8, [2]float64{1.6, 2.4},
					24, 0.6, 25, 700), [3]float64{0.95, 0.98, 0.72}, [3]float64{1.05, 0.95, 0.66}, 40), 0, 0.55), 0.25, 0.03), 0.8, false)),
		}}
}
