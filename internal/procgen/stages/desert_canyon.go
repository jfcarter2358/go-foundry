package stages

// desert_canyon.go — the `desert_canyon` template (docs/canyon/PHASE_7.md):
// every stage of the canyon project wired into one document, from nothing
// to a level you can walk.
//
//	tex          polyhaven_textures   red sand, cracked ground, river pebbles, a layered red cliff face
//	ground       terrain              half a kilometre of mesa country, a stepped canyon through it,
//	                                  a 3 km backdrop of the same country round it
//	ground_mat   terrain_material     the splat over the terrain's masks (and its backdrop), with
//	                                  bedding relief on the walls
//	cliffs       polyhaven_rocks      scanned cliff faces, 5 to 40 m: the walls' cladding
//	boulders     polyhaven_rocks      scanned Namaqualand boulders, with lite copies for scatter
//	stones       polyhaven_rocks      scanned stone and rock sets, split into their pieces
//	trees        polyhaven_plants     quiver trees
//	deadwood     polyhaven_plants     a dead quiver trunk
//	bushes       polyhaven_plants     rooibos and shrubs
//	walls        cliff_wall           the scans stacked up the canyon's walls, nearest the spawn first
//	scatter      scatter              talus under the walls, boulder fields, stones in the washes,
//	                                  quiver trees, deadwood and bushes on the flats
//	plants       vegetation           a sparse dry-scrub biome under it all (off by default; see below)
//	air          environment          height fog, aerial haze, the canyon sky, the sun, HDR with a
//	                                  filmic curve, the view distance the backdrop needs, the spawn
//
// The defaults are the ones the phase-7 renders were tuned with; the
// document is the place to change them. Every asset is a Poly Haven CC0 scan
// (downloaded inside the run and cached), as the project's asset rule asks;
// only the terrain is generated.
//
// The first run downloads about 250 MB and takes a few minutes (PHASE_7.md
// has the numbers); a rerun with nothing changed is a second.

import "foundry/internal/procgen"

// DesertCanyonTemplate is the template's name.
const DesertCanyonTemplate = "desert_canyon"

func init() {
	procgen.RegisterTemplate(procgen.Template{
		Name:  DesertCanyonTemplate,
		Label: "Desert canyon",
		Doc: "A complete desert canyon: Poly Haven textures and scans, an eroded mesa terrain with a far backdrop, its splat " +
			"material, cliff walls, talus and boulder scatter, quiver trees and scrub, the atmosphere, HDR and a spawn point.",
		New: DesertCanyon,
	})
}

// DesertCanyon is the template's document.
func DesertCanyon(name string) *procgen.Doc {
	f, i, e, s, ref, b := procgen.Float, procgen.Int, procgen.Enum, procgen.String, procgen.StageRef, procgen.Bool
	v2, c := procgen.Vec2, procgen.Color
	mat := "gen_" + safeName(name, "gen") + "_ground_mat"
	asset := func(id string) procgen.Params { return procgen.Params{"id": s(id)} }
	assetT := func(id string, tris int) procgen.Params { return procgen.Params{"id": s(id), "max_triangles": i(tris)} }
	layer := func(n, id string, scale float64) procgen.Params {
		return procgen.Params{"name": s(n), "id": s(id), "scale": f(scale)}
	}
	canyon := procgen.List(procgen.Params{
		"path": procgen.Spline(
			[]float64{-300, -120, 70, 55}, []float64{-140, -40, 80, 60},
			[]float64{40, 10, 90, 66}, []float64{180, 90, 95, 70}, []float64{300, 150, 100, 72}),
		"profile": e("stepped"), "steps": i(3), "floor": f(0.22), "meander": f(22), "meander_length": f(220),
		"tributaries": i(0),
	})
	rules := procgen.List(
		procgen.Params{"name": s("talus"), "rocks": ref("boulders"), "max_triangles": i(6000),
			"density": f(5), "scale": v2(0.4, 1.5), "scale_bias": f(2.4), "slope": v2(0, 42), "cliff": v2(0, 0.55),
			"cliff_base": f(1), "cliff_base_distance": f(12), "cluster": f(0.4), "cluster_scale": f(16),
			"align": f(0.7), "tilt": f(18), "sink": f(0.14), "match_ground": f(0.6)},
		procgen.Params{"name": s("boulder_field"), "rocks": ref("boulders"), "max_triangles": i(6000),
			"density": f(0.9), "scale": v2(0.6, 2), "scale_bias": f(2.2), "slope": v2(0, 25), "deposition": v2(0.05, 1),
			"cluster": f(1), "cluster_scale": f(28), "align": f(0.5), "sink": f(0.16), "match_ground": f(0.6)},
		procgen.Params{"name": s("wash_stones"), "rocks": ref("stones"), "max_triangles": i(3000),
			"density": f(14), "scale": v2(0.7, 1.6), "scale_bias": f(2), "slope": v2(0, 16), "flow": v2(0.3, 1),
			"cluster": f(0.6), "cluster_scale": f(10), "align": f(0.9), "tilt": f(15), "sink": f(0.25), "spacing": f(0.9),
			"max_instances": i(3000), "match_ground": f(0.5)},
		procgen.Params{"name": s("trees"), "rocks": ref("trees"), "density": f(0.12), "scale": v2(0.8, 1.4),
			"slope": v2(0, 20), "cliff": v2(0, 0.3), "align": f(0), "tilt": f(0), "sink": f(0.02), "spacing": f(4),
			"cluster": f(0.5), "cluster_scale": f(40)},
		procgen.Params{"name": s("deadwood"), "rocks": ref("deadwood"), "density": f(0.06), "scale": v2(0.7, 1.3),
			"slope": v2(0, 22), "cliff": v2(0, 0.3), "align": f(0.6), "tilt": f(80), "sink": f(0.05), "spacing": f(3),
			"cluster": f(0.5), "cluster_scale": f(30)},
		procgen.Params{"name": s("bushes"), "rocks": ref("bushes"), "density": f(1.2), "scale": v2(0.7, 1.5),
			"slope": v2(0, 30), "cliff": v2(0, 0.4), "align": f(0.3), "tilt": f(0), "sink": f(0.05), "spacing": f(1.5),
			"cluster": f(0.6), "cluster_scale": f(20)},
	)
	spawn := [2]float64{-150, -45}
	return &procgen.Doc{Name: name, Seed: 1234, Level: safeName(name, "level"),
		Doc: "Desert canyon: red sandstone mesas, a stepped canyon, Poly Haven scans", Stages: []procgen.Stage{
			{Kind: PolyHavenTexturesKind, ID: "tex", Params: procgen.Params{"layers": procgen.List(
				layer("sand", "red_sand", 3), layer("cracked", "cracked_red_ground", 4.5),
				layer("gravel", "dry_river_pebbles", 2), layer("cliff", "cliff_side", 10))}},
			{Kind: TerrainKind, ID: "ground", Params: procgen.Params{
				"size": f(512), "resolution": f(0.5), "tile_size": f(128),
				"mesa_scale": f(340), "mesa_height": f(55), "mesa_rim": f(26), "mesa_coverage": f(0.75),
				"canyons": canyon, "texture": procgen.Texture("ph_red_sand"), "uv_scale": f(0.333), "material": s(mat),
				"far": f(3000), "far_cell": f(8), "far_blend": f(140), "far_tile": f(512), "far_material": s(mat + "_far"),
				"cook_memory": i(2000)}},
			{Kind: TerrainMaterialKind, ID: "ground_mat", Params: procgen.Params{"terrain": ref("ground"), "textures": ref("tex"),
				"rock": s("cliff"), "rock_scale": f(10), "strata": f(0.45), "strata_warp": f(2.5), "streaks": f(0.4),
				"bedding": f(0.22), "bedding_spacing": f(2.4), "cap_rock": f(0.75), "rock_tint": c(1.1, 0.94, 0.84), "cracked_cover": f(0.3), "wall_normal": f(1.8), "normal_strength": f(1.2)}},
			{Kind: PolyHavenRocksKind, ID: "cliffs", Params: procgen.Params{"assets": procgen.List(
				asset("namaqualand_cliff_01"), asset("namaqualand_cliff_02"), asset("rock_face_01"), asset("mountainside"), assetT("coastal_cliff_02", 400000)), "match_ground": f(0.7)}},
			{Kind: PolyHavenRocksKind, ID: "boulders", Params: procgen.Params{"assets": procgen.List(
				asset("namaqualand_boulder_02"), asset("namaqualand_boulder_03"), asset("namaqualand_boulder_04"),
				asset("namaqualand_boulder_05")), "back": s("none"), "lite_triangles": i(5000)}},
			{Kind: PolyHavenRocksKind, ID: "stones", Params: procgen.Params{"assets": procgen.List(
				asset("namaqualand_stones_01"), asset("namaqualand_rocks_01")), "back": s("none"), "lite_triangles": i(2000),
				"collision": e("none")}},
			{Kind: PolyHavenPlantsKind, ID: "trees", Params: procgen.Params{"assets": procgen.List(
				asset("quiver_tree_01"), asset("quiver_tree_02"))}},
			{Kind: PolyHavenPlantsKind, ID: "deadwood", Params: procgen.Params{"assets": procgen.List(asset("dead_quiver_trunk"))}},
			{Kind: PolyHavenPlantsKind, ID: "bushes", Params: procgen.Params{"assets": procgen.List(
				asset("wild_rooibos_bush"), asset("shrub_04"))}},
			{Kind: CliffWallKind, ID: "walls", Params: procgen.Params{"terrain": ref("ground"), "chunks": ref("cliffs"),
				"ground": ref("ground_mat"), "match_ground": f(0.9), "max_chunks": i(1500), "scale": v2(1, 3), "match_to": e("rock"), "match_brightness": f(0.8),
				"focus": v2(spawn[0], spawn[1]), "focus_range": f(220), "stretch": f(40), "spacing": f(0.45), "min_wall": f(6)}},
			{Kind: ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "ground": ref("ground_mat"),
				"avoid": ref("walls"), "rules": rules}},
			// Off by default: on WSLg's Mesa driver the biome volumes' Nanite
			// foliage stalled the GPU for good in some views down the canyon
			// (docs/canyon/PHASE_7.md, known gaps), and the scanned plants the
			// scatter places carry the vegetation. Switch it on to add the
			// desert biome's dry scrub under them.
			{Kind: VegetationKind, ID: "plants", Disabled: true, Params: procgen.Params{"terrain": ref("ground"), "rules": procgen.List(
				procgen.Params{"name": s("scrub"), "biome": s("desert"), "density": f(0.08),
					"amounts": s("rock=0 pebbles=0 cactus=0"), "slope": v2(0, 22), "cliff": v2(0, 0.3),
					"cluster": f(0.6), "cluster_scale": f(50)})}},
			{Kind: EnvironmentKind, ID: "air", Params: procgen.Params{"terrain": ref("ground"),
				"view_distance": f(4500), "aerial_distance": f(1700), "hdr": b(true), "exposure": f(0.75), "sun_brightness": f(1.5),
				"ambient_brightness": f(0.38), "ambient_color": c(0.62, 0.7, 0.85),
				"spawn": b(true), "spawn_at": v2(spawn[0], spawn[1]), "spawn_yaw": f(20)}},
		}}
}
