package stages

// environment_dunes.go — the `dunes` environment preset
// (docs/regions/PRESETS.md step 2; ENVIRONMENT.md priority 5).
//
// A sand sea: wind-shaped transverse dunes with ripples, interdune flats.
//
// LANDFORM. The terrain stage's dune field (procgen/terrain/landform.go):
// crests ~22 m high, 140 m apart, the wind blowing toward 30° (from +X
// toward +Z) so the long stoss slopes face into it and the slip faces
// stand at the 34° angle of repose on the lee; crests bent by noise
// (sinuosity 0.6), the field broken into dune trains with flat interdune
// corridors over a quarter of the ground; wind ripples 12 cm high every
// 5 m on the stoss slopes (five samples a ripple at 1 m). Under it a low
// 4 m base. Sand drinks rain: weak drainage, no droplets, no fill. The
// field fades to the shared 0 m base over 120 m inside its footprint
// (`edge_rise`), so a desert canyon's floor or a savannah meets the
// interdune level.
//
// GROUND. Three of the desert canyon's four layers (red_sand, cracked
// red ground on the interdune flats, cliff_side on the slip faces'
// steepest — almost never), so where the two presets meet the band costs
// no layer at all. The washes are sand too: the desert's river pebbles,
// tried first, paved every interdune corridor (the flow mask runs down
// them). The difference is the
// landform and the cover: no strata, no varnish, no wash bars.
//
// LIFE. Almost none — a dune field is bare. Poly Haven's dry grass
// clumps (grass_medium_02, shared with the savannah and the coast) in
// sparse tussocks on the interdune flats only, and a few of the savannah's
// granite boulders (namaqualand_boulder_04/05, shared, so no new import)
// where the corridors expose bedrock.
//
// BUDGET. Measured ~190 scenery placements per km².

import "foundry/internal/procgen"

// DunesEnvironment is the preset's name.
const DunesEnvironment = "dunes"

func init() {
	procgen.RegisterEnvironment(procgen.Environment{
		Name:  DunesEnvironment,
		Label: "Dunes",
		Doc:   "A sand sea of transverse dunes with slip faces and wind ripples, flat interdune corridors; the desert's sands, almost bare.",
		Color: [3]float64{0.95, 0.72, 0.42},
		New:   DunesPreset,
	})
}

// DunesPreset is the dune field's document.
func DunesPreset() *procgen.Doc {
	f, i, s, ref, v2 := procgen.Float, procgen.Int, procgen.String, procgen.StageRef, procgen.Vec2
	return &procgen.Doc{Name: DunesEnvironment, Seed: 1729,
		Doc: "Environment preset: dunes (transverse dune field, slip faces, ripples, interdune flats; the desert's sands)",
		Stages: []procgen.Stage{
			texturesStage(layerRedSand, layerCrackedRed, layerCliffSide),
			{Kind: TerrainKind, ID: "ground", Params: plainTerrain(procgen.Params{
				"relief": f(4), "frequency": f(0.002), "octaves": i(4), "warp": f(120),
				"dune_height": f(22), "dune_wavelength": f(140), "dune_wind": f(30), "dune_asymmetry": f(0.85),
				"dune_sinuosity": f(0.6), "dune_cover": f(0.75), "dune_ripple": f(0.12), "dune_ripple_wavelength": f(5),
				"strata": procgen.List(
					procgen.Params{"thickness": f(30), "hardness": f(0.1), "color": procgen.Color(0.8, 0.56, 0.38)}),
				"resistance": f(0.05),
				"drainage":   f(0.4), "drainage_fill": f(0), "drainage_diffusion": f(0.3),
				"droplets": f(0), "talus_angle": f(33), "detail": f(0.03),
				"sand_color":     procgen.Color(0.82, 0.58, 0.4),
				"base_elevation": f(0), "edge_rise": f(120)})},
			{Kind: TerrainMaterialKind, ID: "ground_mat", Params: procgen.Params{"terrain": ref("ground"), "textures": ref("tex"),
				"sand": s("sand"), "cracked": s("cracked"), "gravel": s("sand"), "rock": s("cliff"),
				// The desert's rock repeat: the same palette entry.
				"rock_scale": f(10), "rock_slope": v2(40, 55), "cliff": f(0.9),
				// Cracked ground only on the flattest interdune floors.
				"flat_slope": v2(1.5, 4), "cracked_cover": f(0.45), "sand_deposition": v2(0.6, 0.95),
				"wash_flow": v2(0.8, 0.95), "wash_width": f(2),
				"layer_match": f(0.1), "breakup": f(0.5), "breakup_scale": f(25),
				"strata": f(0), "streaks": f(0), "macro": f(0.18), "macro_scale": f(70),
				"anti_tile": procgen.Bool(true), "blend": f(0.3)}},
			rocksStage("boulders", 5000, "", presetAsset("namaqualand_boulder_04", 0), presetAsset("namaqualand_boulder_05", 0)),
			{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "ground": ref("ground_mat"),
				"rules": procgen.List(
					procgen.Params{"name": s("boulders"), "rocks": ref("boulders"), "density": f(0.015), "scale": v2(0.4, 1.2),
						"scale_bias": f(2.2), "slope": v2(0, 6), "cluster": f(0.9), "cluster_scale": f(40),
						"align": f(0.6), "sink": f(0.35), "match_ground": f(0.35)})}},
			plantsStage("grass", 0, presetAsset("grass_medium_02", 0)),
			coverStage(coverRule("tussocks", "grass", 0.03, [2]float64{1.2, 2.2}, 6, 40, 0.8, 0.15)),
		}}
}
