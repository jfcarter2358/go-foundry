package stages

// canyon_savannah.go — the `canyon_savannah` template (docs/regions/PHASE_6.md):
// a world of two environment regions, a desert canyon running into
// savannah, generated in one pass with a 200 m transition between them.
//
// It is a level generation doc (level_world.go) that already knows its
// layout:
//
//	regions     two region volumes' worth: canyon_west (desert_canyon,
//	            x −512..0) and savannah_east (savannah, x 0..512), both
//	            z ±384, transition left at the default so the adjacency
//	            table's desert_canyon|savannah width (200 m) is the band;
//	            1 m resolution, 128 m tiles, global erosion. `seed_level`
//	            is on: the first run (editor or CLI) writes these two
//	            regions INTO the target level as region volumes when it has
//	            none — making the level when it does not exist — and from
//	            then on the level leads (genlevel/regions_seed.go). So
//	            `foundry gen new <level>_world -template canyon_savannah`
//	            then `foundry gen run <level>_world` is the whole flow.
//	ground      region_terrain over both, a 2 km backdrop at 8 m; its tiles
//	            and backdrop wear ground_mat's materials (the auto material)
//	ground_mat  region_material: the canyon's red sand, cracked ground,
//	            pebbles and cliff face and the savannah's grass, baked
//	            earth, stony soil and boulder rock in one eight-layer splat
//	air         environment: the level's sky, fog, haze, sun, HDR and the
//	            spawn — one compromise between the two environments (below)
//
// plus every region's expanded placement stages (r_canyon_west_scatter,
// r_savannah_east_grass, ...) from the two presets.
//
// # The atmosphere
//
// The desert template's air is a hot, hazy canyon: warm height fog pooled in
// the floor, a strong sun at exposure 0.75. The savannah wants more distance
// (a plain is looked ACROSS) and less pooled fog (it has no floor to pool
// in). The compromise keeps the canyon's sky, sun and HDR, pushes the aerial
// distance out to 2.2 km and halves the height fog's density, with its base
// near the plain's level, so the canyon floor still hazes and the savannah
// reads clear to its far swells. The spawn stands on the savannah side,
// 300 m east of the seam, looking west-north-west (yaw 160°) at the mesas
// over the canyon's mouth, across grass and an acacia clump.
//
// # Size and cost
//
// The union is 1024 m square at 1 m (the footprints are 1024 × 768; the
// square pads the rest from the nearest regions). A first run downloads the
// two presets' Poly Haven scans and textures (~300 MB), cooks the terrain
// and the canyon's rock walls; PHASE_6.md has the times, memory and frame
// costs. Resolution 0.5 m is four times the samples and still fits.

import "foundry/internal/procgen"

// CanyonSavannahTemplate is the template's name.
const CanyonSavannahTemplate = "canyon_savannah"

func init() {
	procgen.RegisterTemplate(procgen.Template{
		Name:  CanyonSavannahTemplate,
		Label: "Canyon into savannah (regions)",
		Doc: "A two-region world: a desert canyon and a savannah, grown in one pass and blended across a 200 m band, with " +
			"the sky, fog, sun and a spawn on the savannah looking into the canyon. The first run writes the two region " +
			"volumes into the target level. Name it <level>_world.",
		New: CanyonSavannah,
	})
}

// CanyonSavannahRegions is the template's layout: id, environment and
// footprint (world XZ), in the regions stage's list form.
func CanyonSavannahRegions() procgen.Value {
	s, sp := procgen.String, procgen.Spline
	item := func(id, env string, x0, x1 float64) procgen.Params {
		const hz = 384.0
		return procgen.Params{
			"id": s(id), "environment": s(env), "generator": s(""), "priority": procgen.Int(0),
			// The default: the adjacency table's pair width (200 m) is used.
			"transition": procgen.Float(150), "anchored": procgen.Bool(false), "anchor": procgen.Vec2(0, 0),
			"footprint": sp([]float64{x0, -hz}, []float64{x1, -hz}, []float64{x1, hz}, []float64{x0, hz}),
		}
	}
	return procgen.List(
		item("canyon_west", DesertCanyonEnvironment, -512, 0),
		item("savannah_east", SavannahEnvironment, 0, 512),
	)
}

// CanyonSavannah is the template's document. A name ending _world targets
// the level it names (canyon_world → canyon), as level_world's do.
func CanyonSavannah(name string) *procgen.Doc {
	f, b, ref, c, v2 := procgen.Float, procgen.Bool, procgen.StageRef, procgen.Color, procgen.Vec2
	d := LevelWorld(name)
	d.Seed = 2026
	d.Doc = "World generation for level " + d.Level + ": a desert canyon (west) running into savannah (east), " +
		"one pass, a 200 m transition"
	d.Stages = []procgen.Stage{
		{Kind: RegionsKind, ID: "regions", Params: procgen.Params{
			"regions": CanyonSavannahRegions(), "seed_level": b(true),
			"resolution": f(1), "tile_size": f(128), "erosion": procgen.Enum("global")}},
		// Material and backdrop material empty: the tiles wear ground_mat's
		// (regionTerrainAutoMaterial).
		{Kind: RegionTerrainKind, ID: "ground", Params: procgen.Params{"regions": ref("regions"),
			// The canyon leads: its walls keep their own erosion numbers and
			// the material its varnish, bedding and strata banding, beside
			// a savannah that owns as many samples.
			"lead": procgen.String("canyon_west"),
			// The canyon's mouth and the band are broad flats: a d8 flow
			// mask ruled them with straight wash lines.
			"flow_mask": procgen.Enum("mfd"),
			"far":       f(2000), "far_cell": f(8), "far_blend": f(140), "far_tile": f(512), "cook_memory": procgen.Int(2000)}},
		{Kind: RegionMaterialKind, ID: "ground_mat", Params: procgen.Params{"terrain": ref("ground"),
			// Per-tile materials (region_material_tiles.go): only the band pays for eight layers.
			"per_tile": procgen.Bool(true)}},
		{Kind: EnvironmentKind, ID: "air", Params: procgen.Params{"terrain": ref("ground"),
			"view_distance": f(4000), "aerial_distance": f(2200), "hdr": b(true), "exposure": f(0.75), "sun_brightness": f(1.5),
			"height_density": f(0.003), "height_falloff": f(0.08), "height_color": c(0.86, 0.78, 0.64),
			"ambient_brightness": f(0.4), "ambient_color": c(0.62, 0.7, 0.85),
			"spawn": b(true), "spawn_at": v2(300, 40), "spawn_yaw": f(160)}},
	}
	return d
}
