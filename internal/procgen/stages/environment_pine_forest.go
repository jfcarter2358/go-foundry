package stages

// environment_pine_forest.go — the `pine_forest` environment preset
// (docs/regions/PRESETS.md step 2; ENVIRONMENT.md priority 2).
//
// Temperate conifer forest on rolling hills.
//
// LANDFORM. Moderate relief: swells every ~300 m about 50 m high, gullied by
// drainage and a light droplet pass, soft-sided (talus 33°). Its relief
// rises from the shared 0 m base over 80 m inside its footprint
// (`edge_rise`), so it meets a plain or a savannah at their level and the
// hills climb within the forest.
//
// GROUND (environments_shared.go's catalogue). Pine needles everywhere
// (forest_leaves_04, Poly Haven's pine_forest collection), tinted darker —
// the scan is sunlit and from 50 m reads as sand — and lichen-crusted rock
// on the steep banks (lichen_rock, shared with alpine, tundra, the coast
// and the wetland). Two layers. Clearings of other ground were tried and
// dropped: forrest_ground_01 rendered as pale grey-white blotches,
// forest_ground_04 as beige ones, and the shared scree in the gullies
// pooled into pale stony pans wherever the flow mask converged — every
// second layer read as a hole in the forest floor.
//
// LIFE (Poly Haven only, no engine foliage — docs/regions/PRESETS_2.md). The
// canopy is fir_tree_01 (Poly Haven's pine_forest collection: three 14–19 m
// firs, each split into its own part, decimated from 7 M triangles to 300k
// with a 30k lite variant) grown 1.2–1.7× (20–30 m) in a close stand
// (PRESETS_2B: 0.32 per 100 m², spacing 0.55, open only in the clumping
// noise's clearings), with fir_sapling_medium (three 6–9 m firs, ×1–1.8) in
// the gaps (pine_sapling_small was tried and dropped: it rendered as bare
// black poles); fallen trunks and stumps; mossy boulder sets. The
// understory is on the grass instancer (environments_grass.go): fern_02's
// fronds (1.6–2.6×) over most of the floor in drifts, grass_medium_01 in
// the clearings — it also hides the needle texture's repeat. moss_01 was tried and dropped: its
// pieces are 1–3 cm sprigs, invisible as scenery. The needle cards are
// thinned, not edge-collapsed, and their texture keeps its alpha coverage
// down the mip chain (`thin_foliage`, phimport/decimate_cards.go,
// texcompress/mip_coverage.go): without both a fir is a bare skeleton. Poly
// Haven's pine_tree_01 (17 M triangles) was left out: decimating it needs
// ~18 GB.
//
// BUDGET. ~5,000 scenery placements per km² (1,309 in a 512 m region;
// firs 0.32 + saplings 0.08 + logs and stumps 0.02 + boulders 0.03 per
// 100 m²) — over the ~3,000 target, the price of a denser stand; the
// understory is ~1.5 M grass-instancer clumps per km². Still not a closed
// canopy — real forest is 400–1,500 stems per hectare and fir_tree_01's
// thinned crowns are sparse — but a stand of 20–30 m firs a few metres
// apart over a fern floor.

import "foundry/internal/procgen"

// PineForestEnvironment is the preset's name.
const PineForestEnvironment = "pine_forest"

func init() {
	procgen.RegisterEnvironment(procgen.Environment{
		Name:  PineForestEnvironment,
		Label: "Pine forest",
		Doc:   "Rolling forested hills: a conifer canopy over pine needles, scanned firs, logs, ferns and mossy boulders, grey scree in the gullies.",
		Color: [3]float64{0.16, 0.42, 0.24},
		New:   PineForestPreset,
	})
}

// PineForestPreset is the pine forest's document.
func PineForestPreset() *procgen.Doc {
	f, i, s, ref, v2 := procgen.Float, procgen.Int, procgen.String, procgen.StageRef, procgen.Vec2
	rules := procgen.List(
		// A closed stand: big firs (1.2–1.7×, 20–30 m) close together, so
		// their crowns meet overhead, open only in the clumping noise's
		// clearings.
		//
		// PRESETS_6: 0.3 a 100 m² at 50k triangles (PRESETS_3 had cut it to
		// 0.12 for frame time), 1.1–2.0× with a bias to the small end so the
		// canopy is layered rather than a field of equal poles, and handed
		// to the impostors from 50 m: the 50k mesh's thinned crowns read as
		// bare poles, the cards keep the full crown's coverage, so the stand
		// reads closed past the nearest trees.
		procgen.Params{"name": s("firs"), "rocks": ref("trees"), "density": f(0.3), "scale": v2(1.1, 2),
			"scale_bias": f(1.2), "slope": v2(0, 34), "cliff": v2(0, 0.25), "align": f(0), "tilt": f(0), "sink": f(0.03),
			"spacing": f(0.5), "cluster": f(0.2), "cluster_scale": f(60), "impostor_distance": f(50)},
		procgen.Params{"name": s("young_firs"), "rocks": ref("young"), "density": f(0.16), "scale": v2(0.9, 1.8),
			"slope": v2(0, 30), "cliff": v2(0, 0.2), "align": f(0.05), "tilt": f(0), "sink": f(0.03), "spacing": f(0.8),
			"cluster": f(0.5), "cluster_scale": f(25)},
		procgen.Params{"name": s("deadwood"), "rocks": ref("deadwood"), "density": f(0.01), "scale": v2(0.7, 1.2),
			"slope": v2(0, 25), "cliff": v2(0, 0.2), "align": f(0.9), "tilt": f(4), "sink": f(0.08), "spacing": f(1.5),
			"cluster": f(0.5), "cluster_scale": f(30)},
		procgen.Params{"name": s("boulders"), "rocks": ref("boulders"), "density": f(0.03), "scale": v2(0.5, 1.2),
			"scale_bias": f(2), "slope": v2(6, 40), "cluster": f(0.8), "cluster_scale": f(35), "align": f(0.6),
			"sink": f(0.2), "match_ground": f(0.2)},
	)
	// Ferns over most of the floor, thickest where the clumping noise is
	// high: the understory that hides the needle texture's repeat.
	ferns := grassRule("ferns", "ferns", "", 1.1, [2]float64{1.6, 2.6}, 35, 0.5, 16, 600)
	return &procgen.Doc{Name: PineForestEnvironment, Seed: 3141,
		Doc: "Environment preset: pine forest (rolling hills, conifer canopy, needles, scanned firs, logs, ferns, mossy boulders)",
		Stages: []procgen.Stage{
			texturesStage(layerNeedles, layerLichenRock),
			{Kind: TerrainKind, ID: "ground", Params: plainTerrain(procgen.Params{
				"relief": f(55), "frequency": f(0.0032), "octaves": i(6), "warp": f(110),
				"drainage": f(2.2), "drainage_fill": f(0.05), "drainage_diffusion": f(0.12),
				"droplets": f(0.5), "talus_angle": f(33), "detail": f(0.25),
				"sand_color":     procgen.Color(0.36, 0.3, 0.2),
				// 80 m (PRESETS_6): 160 flattened a belt of forest 500 m
				// wide to within 5 m of the base.
				"base_elevation": f(0), "edge_rise": f(80)})},
			{Kind: TerrainMaterialKind, ID: "ground_mat", Params: procgen.Params{"terrain": ref("ground"), "textures": ref("tex"),
				"sand": s("needles"), "cracked": s("needles"), "gravel": s("needles"), "rock": s("rock"),
				"rock_scale": f(sharedRockScale), "rock_slope": v2(34, 48), "cliff": f(0.6),
				"flat_slope": v2(3, 9), "cracked_cover": f(0.3), "sand_deposition": v2(0.4, 0.8),
				"wash_flow": v2(0.8, 0.95), "wash_width": f(1.5),
				"layer_match": f(0.1), "breakup": f(0.6), "breakup_scale": f(16),
				"strata": f(0), "streaks": f(0), "macro": f(0.25), "macro_scale": f(40),
				"anti_tile": procgen.Bool(true), "gravel_relief": f(0.03), "blend": f(0.3),
				// forest_leaves_04 is a sunlit scan: from 50 m it reads as
				// sand. Darker and less red, the shade of a needle floor.
				"sand_tint": procgen.Color(0.38, 0.36, 0.31)}},
			// 50k / 20k triangles (PRESETS_3): the 300k / 120k cut cost the
			// stand its frame rate for detail only the nearest trees show.
			plantsStage("trees", 0, presetAsset("fir_tree_01", 50000)),
			plantsStage("young", 0, presetAsset("fir_sapling_medium", 20000)),
			plantsStage("ferns", 0, presetAsset("fern_02", 0)),
			plantsStage("grass", 12000, presetAsset("grass_medium_01", 60000)),
			rocksStage("deadwood", 6000, "", presetAsset("dead_tree_trunk", 40000), presetAsset("tree_stump_01", 20000),
				presetAsset("tree_stump_02", 20000)),
			rocksStage("boulders", 5000, "", presetAsset("rock_moss_set_01", 0), presetAsset("rock_moss_set_02", 0)),
			{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "ground": ref("ground_mat"),
				"rules": rules}},
			// The understory on the grass instancer (environments_grass.go):
			// fern_02's fronds at 1.6–2.6× in drifts along the moist draws
			// and under the canopy, grass in the clearings.
			grassStage(
				grassWindOf(grassTint(ferns, [3]float64{0.85, 0.95, 0.75}, [3]float64{1.0, 1.0, 0.8}, 30), 0.15, 0.03),
				grassWindOf(grassTint(grassRule("clearing_grass", "grass", "", 0.7, [2]float64{2.5, 3.5}, 25, 0.85, 30, 600),
					[3]float64{0.9, 0.95, 0.7}, [3]float64{1.0, 0.95, 0.65}, 30), 0.3, 0.04)),
		}}
}
