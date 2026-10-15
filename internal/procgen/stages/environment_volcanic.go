package stages

// environment_volcanic.go — the `volcanic` environment preset
// (docs/regions/PRESETS.md step 2; ENVIRONMENT.md priority 6).
//
// A basalt field round a cinder cone: the cone and its crater, lava-flow
// lobes down its flanks and out over the plain, ash round the vent.
//
// LANDFORM. The terrain stage's volcanoes (procgen/terrain/landform.go): one
// seeded cone 160 m high (its crater rim ~110 m), 240 m in radius at its
// foot, a summit crater a fifth of that across and 30 m deep, and four lava
// lobes — flat-topped tongues with steep 5 m fronts, 45 m wide, ~260 m long
// — running out from it; a low 15 m roll in the field. Drainage and droplets
// gully the cone's flanks. The field fades to the shared 0 m base over 60 m
// inside its footprint (`edge_rise`), so a forest beside it meets the plain,
// not the cone.
//
// GROUND. Dark cinder sand everywhere (coast_sand_05 — a grey sand scan,
// tinted near-black); bare dark lava (dark_rock_02: the field's pahoehoe)
// ON THE LAVA LOBES (`lava_lobes`, the terrain's own lobe outlines) and in
// a few patches on the flats; basalt on the steep ground (dark_rock); and
// the ASH cover (terrain_material_cover.go) — fine grey ground
// (ground_grey, the tundra's frozen ground: one palette entry;
// burned_ground_01, tried first, read as straw over the whole cone) — as an
// APRON round each cone's foot (`ash_hole`: none within 0.8 cone radii,
// full 1.0–1.2, gone by 1.7, half covering), off the cliffs. PRESETS_6: with ash from
// the vent outwards the whole cone read pale grey with the basalt as dark
// bands; now the cone is cinders and basalt and the pale ground rings it.
// Three layers of its own and the tundra's grey ground, so with any
// four-layer neighbour a band tile holds at most eight.
//
// LIFE (Poly Haven only). Basalt boulders are Poly Haven's moon rocks
// (moon_rock_01/03/05: rough, dusted, the darkest broken rock it has),
// pulled hard toward the cinder ground's colour; until PRESETS_6 they came
// from the SDF rock generator, which the showcase's Poly-Haven-only rule
// excludes. Burnt deadwood — Poly Haven's dead tree trunk and a stump
// (dead_tree_trunk_02, tree_stump_02), darkened by the ground colour
// match — on the ash apron and the old flows, more of it than before. Dry grass clumps
// (grass_medium_02) in sparse patches on the old flows away from the cone.
// The woolly moss a lava field would carry has no Poly Haven model that
// works as scenery (moss_01 is 1–3 cm sprigs): the ground texture carries
// it.
//
// BUDGET. Measured ~1,300 scenery placements per km².

import "foundry/internal/procgen"

// VolcanicEnvironment is the preset's name.
const VolcanicEnvironment = "volcanic"

// The volcanic preset's own ground layers.
var (
	layerCinders  = envLayer{"cinders", "coast_sand_05", 6}
	layerBareLava = envLayer{"lava", "dark_rock_02", 3}
	layerBasalt   = envLayer{"basalt", "dark_rock", 6}
)

func init() {
	procgen.RegisterEnvironment(procgen.Environment{
		Name:  VolcanicEnvironment,
		Label: "Volcanic",
		Doc:   "A cinder cone with a summit crater and lava-flow lobes over a basalt field: dark cinders, bare lava, ash round the vent, moss on the old flows.",
		Color: [3]float64{0.42, 0.2, 0.2},
		New:   VolcanicPreset,
	})
}

// VolcanicPreset is the volcanic field's document.
func VolcanicPreset() *procgen.Doc {
	f, i, s, ref, v2 := procgen.Float, procgen.Int, procgen.String, procgen.StageRef, procgen.Vec2
	rules := procgen.List(
		procgen.Params{"name": s("basalt"), "rocks": ref("basalt"), "density": f(0.08), "scale": v2(0.5, 1.6),
			"scale_bias": f(2.2), "slope": v2(0, 40), "cluster": f(0.75), "cluster_scale": f(30),
			"align": f(0.7), "tilt": f(12), "sink": f(0.25), "match_ground": f(0.8)},
		procgen.Params{"name": s("deadwood"), "rocks": ref("deadwood"), "density": f(0.04), "scale": v2(0.7, 1.3),
			"slope": v2(0, 22), "cliff": v2(0, 0.2), "align": f(0.9), "tilt": f(5), "sink": f(0.08), "spacing": f(1.5),
			"cluster": f(0.6), "cluster_scale": f(40), "match_ground": f(0.75)},
	)
	return &procgen.Doc{Name: VolcanicEnvironment, Seed: 2236,
		Doc: "Environment preset: volcanic (cinder cone, crater, lava lobes, cinders, basalt, ash, moss)",
		Stages: []procgen.Stage{
			texturesStage(layerCinders, layerBareLava, layerBasalt, layerFrozenGround),
			{Kind: TerrainKind, ID: "ground", Params: plainTerrain(procgen.Params{
				"relief": f(15), "frequency": f(0.004), "octaves": i(5), "warp": f(80),
				// A cone field: two cones (seeded, 1.2 radii apart at least).
				"volcanoes": i(2), "volcano_height": f(150), "volcano_radius": f(220), "crater": f(0.2), "crater_depth": f(28),
				"lava_lobes": i(4), "lava_length": f(260), "lava_width": f(45), "lava_thickness": f(5),
				"strata": procgen.List(
					procgen.Params{"thickness": f(20), "hardness": f(0.75), "color": procgen.Color(0.2, 0.19, 0.19)},
					procgen.Params{"thickness": f(12), "hardness": f(0.4), "color": procgen.Color(0.3, 0.22, 0.2)}),
				"resistance": f(0.4),
				"drainage":   f(1.6), "drainage_fill": f(0.08), "drainage_diffusion": f(0.08),
				"droplets": f(0.8), "talus_angle": f(33), "detail": f(0.3),
				"sand_color":     procgen.Color(0.22, 0.21, 0.2),
				// 60 m (PRESETS_6): at 150 the fade cut the cones to a third
				// of their height in the showcase.
				"base_elevation": f(0), "edge_rise": f(60)})},
			{Kind: TerrainMaterialKind, ID: "ground_mat", Params: procgen.Params{"terrain": ref("ground"), "textures": ref("tex"),
				"sand": s("cinders"), "cracked": s("lava"), "gravel": s("cinders"), "rock": s("basalt"),
				"rock_scale": f(sharedRockScale), "rock_slope": v2(33, 46), "cliff": f(0.7),
				// Bare lava on the flats (the lobes' tops) in patches.
				"flat_slope": v2(5, 12), "cracked_cover": f(0.2), "sand_deposition": v2(0.4, 0.8),
				"wash_flow": v2(0.7, 0.95), "wash_width": f(2),
				"layer_match": f(0.05), "breakup": f(0.6), "breakup_scale": f(18),
				"strata": f(0), "streaks": f(0.2), "macro": f(0.2), "macro_scale": f(45), "macro_vertical": f(0.4),
				"anti_tile": procgen.Bool(true), "cracked_relief": f(0.02), "blend": f(0.3),
				// A dusting: ground_grey (the tundra's, one palette entry and
				// its tint) as a full cover read as snow round the cone.
				"ash": s("frozen"), "ash_radius": v2(1.2, 1.7), "ash_hole": f(1.0), "ash_cover": f(0.5),
				"lava_lobes": f(0.9),
				// coast_sand_05 is a grey beach sand in daylight: from the
				// field it read beige. Cinders dark brown-grey; the bare lava
				// (dark_rock_02, lighter than the cinders as scanned: the
				// lobes read as pale stripes down the cone) darker still, so
				// the tongues are the blackest ground in the field.
				"sand_tint": procgen.Color(0.55, 0.5, 0.48), "cracked_tint": procgen.Color(0.42, 0.42, 0.44),
				"rock_tint": procgen.Color(0.7, 0.66, 0.64)}},
			rocksStage("basalt", 5000, "", presetAsset("moon_rock_01", 0), presetAsset("moon_rock_03", 0),
				presetAsset("moon_rock_05", 0)),
			rocksStage("deadwood", 6000, "", presetAsset("dead_tree_trunk_02", 40000), presetAsset("tree_stump_02", 20000)),
			{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "ground": ref("ground_mat"),
				"rules": rules}},
			plantsStage("grass", 0, presetAsset("grass_medium_02", 0)),
			coverStage(withHeight(coverRule("flow_grass", "grass", 0.04, [2]float64{1.2, 2.2}, 25, 35, 0.8, 0.15), 0, 0.35)),
		}}
}
