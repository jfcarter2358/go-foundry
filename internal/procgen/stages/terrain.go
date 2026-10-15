package stages

// terrain.go — the `terrain` stage: eroded desert canyon country as a
// heightfield, its masks, and closed Nanite tiles placed as scenery.
//
// The generation itself is internal/procgen/terrain (pure computation, see
// its package comment for the pipeline: mesas, canyons, drainage, strata,
// droplets, talus). This file is the stage's schema and its mapping onto
// terrain.Settings; terrain_run.go is the run (files, tiles, the cook, the
// placements); terrain_preview.go the fast preview and the Load.
//
// The parameters are grouped by name prefix the way the pipeline runs:
// the grid (size, resolution, tile_size), the base shape and mesas
// (mesa_*), the strata (strata, terrace, ...), the canyons (a list, each with
// a spline path whose points carry width and depth), the erosion
// (drainage_*, droplet_*, talus_*), the masks and the output.
//
// Every length is metres and every angle degrees, and the landforms are
// defined in metres rather than samples: changing the resolution changes the
// detail, not the shape — which is what lets the preview run the same
// document at a quarter of the resolution in a fraction of the time.

import (
	"fmt"
	"math"

	"foundry/internal/procgen"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/terrain"
)

// TerrainKind is the stage's name.
const TerrainKind = "terrain"

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     TerrainKind,
		Label:    "Terrain",
		Category: "Terrain",
		// Served by the level generation doc's shared stages for every
		// region; never expanded per region (docs/regions/SPEC.md).
		Regional: procgen.RegionalShared,
		Version:  1,
		Doc: "Eroded desert canyon country: mesas with stepped strata ledges, canyons carved along splines, " +
			"drainage and droplet erosion, talus slopes. Writes a 16-bit heightmap, masks (flow, slope, deposition, " +
			"cliff, strata, curvature), an albedo hint and metadata, and places closed Nanite tiles as mesh-collision scenery.",
		Params:   terrainParams(),
		Run:      runTerrain,
		Preview:  previewTerrain,
		Load:     loadTerrain,
		Validate: validateTerrain,
	})
	procgen.RegisterTemplate(procgen.Template{
		Name:  "terrain_demo",
		Label: "Canyon terrain",
		Doc:   "One terrain stage: a kilometre of mesa country with a meandering canyon and its tributaries.",
		New: func(name string) *procgen.Doc {
			return &procgen.Doc{Name: name, Seed: 1234, Doc: "Canyon terrain", Stages: []procgen.Stage{{
				Kind: TerrainKind, ID: "ground", Params: procgen.Params{"canyons": DemoCanyons()},
			}}}
		},
	})
}

// DemoCanyons is the template's canyon: across the map, deepening
// downstream, with five tributaries.
func DemoCanyons() procgen.Value {
	return procgen.List(procgen.Params{
		"path": procgen.Spline(
			[]float64{-560, -260, 90, 80}, []float64{-200, -120, 110, 95},
			[]float64{120, 60, 130, 110}, []float64{560, 240, 150, 120}),
		"profile": procgen.Enum("stepped"), "tributaries": procgen.Int(5),
	})
}

func strataItem(l terrain.Layer) procgen.Params {
	return procgen.Params{
		"thickness": procgen.Float(l.Thickness), "hardness": procgen.Float(l.Hardness),
		"color": procgen.Color(l.Color[0], l.Color[1], l.Color[2]),
	}
}

func terrainParams() []procgen.ParamDef {
	d := terrain.Defaults()
	var layers []procgen.Params
	for _, l := range d.Strata.Layers {
		layers = append(layers, strataItem(l))
	}
	f := procgen.Float
	i := procgen.Int
	return append([]procgen.ParamDef{
		// The grid.
		{Name: "size", Type: procgen.ParamFloat, Label: "Size (m)", Default: f(d.Size), Min: 64, Max: 8192,
			Doc: "The side of the square, centred on the origin. Must be a whole number of tiles."},
		{Name: "resolution", Type: procgen.ParamFloat, Label: "Resolution (m)", Default: f(d.Cell), Min: 0.25, Max: 16, Step: 0.05,
			Doc: "Metres between height samples. 0.5 is hero ground; 1 is a good default; the landforms are the same at any resolution."},
		{Name: "tile_size", Type: procgen.ParamFloat, Label: "Tile size (m)", Default: f(128), Min: 16, Max: 128,
			Doc: "The side of each cooked tile (at most 128 m, so the GI distance field stays fine). Must divide Size, and be a whole number of samples."},
		{Name: "seed", Type: procgen.ParamSeed, Default: procgen.Seed(0),
			Doc: "Changes the noise, the droplets and the tributaries without changing anything else."},

		// The base shape.
		{Name: "base_height", Type: procgen.ParamFloat, Label: "Base height (m)", Default: f(d.Base.Height), Min: -2000, Max: 2000,
			Doc: "The lowland's mean elevation."},
		{Name: "relief", Type: procgen.ParamFloat, Label: "Relief (m)", Default: f(d.Base.Relief), Min: 0, Max: 1000,
			Doc: "The lowland's rolling relief (FBM half range). Keep it modest: the cliffs carry canyon country's relief."},
		{Name: "frequency", Type: procgen.ParamFloat, Default: f(d.Base.Frequency), Min: 0.0001, Max: 0.1, Step: 0.0002,
			Doc: "The lowland noise's first octave, cycles per metre."},
		{Name: "octaves", Type: procgen.ParamInt, Default: i(d.Base.Octaves), Min: 1, Max: 10,
			Doc: "The lowland noise's octaves."},
		{Name: "warp", Type: procgen.ParamFloat, Label: "Warp (m)", Default: f(d.Base.Warp), Min: 0, Max: 1000,
			Doc: "Domain warp of the lowland noise: bends its features."},
		{Name: "mesa_height", Type: procgen.ParamFloat, Label: "Mesa height (m)", Default: f(d.Mesa.Height), Min: 0, Max: 1000,
			Doc: "Each plateau tier's rise above the one below. 0 turns the mesas off."},
		{Name: "mesa_coverage", Type: procgen.ParamFloat, Label: "Mesa coverage", Default: f(d.Mesa.Coverage), Min: 0, Max: 1,
			Doc: "The share of the map the first tier covers; each higher tier covers about 45% of the one below."},
		{Name: "mesa_scale", Type: procgen.ParamFloat, Label: "Mesa scale (m)", Default: f(d.Mesa.Scale), Min: 20, Max: 8000,
			Doc: "The plateaus' typical size."},
		{Name: "mesa_tiers", Type: procgen.ParamInt, Label: "Mesa tiers", Default: i(d.Mesa.Tiers), Min: 0, Max: 4,
			Doc: "Plateaus stacked on plateaus: 2 stands buttes on the mesas."},
		{Name: "mesa_rim", Type: procgen.ParamFloat, Label: "Rim width (m)", Default: f(d.Mesa.Rim), Min: 1, Max: 1000,
			Doc: "How much ground a tier's rise takes. The strata turn the rise into cliffs and ledges; narrower is a sheerer escarpment."},
		{Name: "mesa_roughness", Type: procgen.ParamFloat, Label: "Rim roughness", Default: f(d.Mesa.Roughness), Min: 0, Max: 1,
			Doc: "Ragged rims: alcoves, points and spurs."},
		{Name: "mesa_top_relief", Type: procgen.ParamFloat, Label: "Top relief (m)", Default: f(d.Mesa.TopRelief), Min: 0, Max: 100,
			Doc: "The undulation left on the flat tops."},
		{Name: "mesa_joints", Type: procgen.ParamFloat, Label: "Rim joints", Default: f(d.Mesa.Joints), Min: 0, Max: 1,
			Doc: "How strongly the rims follow a fracture network: straight, angular rim segments and slots. Strong values read as tiles."},
		{Name: "mesa_joint_spacing", Type: procgen.ParamFloat, Label: "Rim joint spacing (m)", Default: f(d.Mesa.JointSpacing), Min: 5, Max: 2000,
			Doc: "The rim fracture network's spacing."},

		// The strata.
		{Name: "strata", Type: procgen.ParamList, Label: "Strata", Default: procgen.List(layers...), MaxItems: 64,
			Doc: "The rock layers, bottom up, repeated upward (and downward) as far as the heights go. Hard layers stand as cliffs " +
				"with benches on top, soft ones weather to slopes; the colours feed the albedo hint.",
			Fields: []procgen.ParamDef{
				{Name: "thickness", Type: procgen.ParamFloat, Label: "Thickness (m)", Default: f(10), Min: 0.5, Max: 1000, Doc: "The layer's thickness."},
				{Name: "hardness", Type: procgen.ParamFloat, Default: f(0.5), Min: 0, Max: 1,
					Doc: "0 is shale (slope-former, erodes fast), 1 is sandstone (cliff-former, resists)."},
				{Name: "color", Type: procgen.ParamColor, Default: procgen.Color(0.72, 0.45, 0.3), Min: 0, Max: 1, Doc: "Rock colour for the albedo hint."},
			}},
		{Name: "strata_base", Type: procgen.ParamFloat, Label: "Strata base (m)", Default: f(d.Strata.Base), Min: -5000, Max: 5000,
			Doc: "The height the first layer starts at."},
		{Name: "terrace", Type: procgen.ParamFloat, Default: f(d.Strata.Terrace), Min: 0, Max: 1,
			Doc: "How strongly the surface steps through the layers: 0 smooth, 1 sheer cliffs and flat benches."},
		{Name: "terrace_slope_lo", Type: procgen.ParamFloat, Label: "Terrace from (°)", Default: f(d.Strata.SlopeLo), Min: 0, Max: 89,
			Doc: "Ground gentler than this is not stepped: mesa tops and lowlands stay smooth."},
		{Name: "terrace_slope_hi", Type: procgen.ParamFloat, Label: "Terrace full at (°)", Default: f(d.Strata.SlopeHi), Min: 1, Max: 90,
			Doc: "Ground this steep is stepped at full strength."},
		{Name: "strata_warp", Type: procgen.ParamFloat, Label: "Strata warp (m)", Default: f(d.Strata.Warp), Min: 0, Max: 200,
			Doc: "Noise displacement of the layer heights, so ledges wander."},
		{Name: "strata_dip", Type: procgen.ParamVec2, Label: "Strata dip", Default: procgen.Vec2(0, 0), Min: -50, Max: 50,
			Doc: "Tilts the layers: metres of rise per 100 m along X and Z."},
		{Name: "resistance", Type: procgen.ParamFloat, Default: f(d.Strata.Resistance), Min: 0, Max: 1,
			Doc: "How much hardness slows erosion: at 1 a layer of hardness 1 does not erode at all."},

		// The canyons.
		{Name: "canyons", Type: procgen.ParamList, Default: procgen.List(), MaxItems: 32,
			Doc: "Canyons carved along paths. A path runs downstream (its floor never rises along it); edit it in the top-down view.",
			Fields: []procgen.ParamDef{
				{Name: "path", Type: procgen.ParamSpline, Label: "Path",
					Default: procgen.Spline(), Channels: []string{"width", "depth"}, ChannelDefaults: []float64{100, 80},
					Doc: "The centreline, downstream, with the rim-to-rim width and the depth below the ground around it at each point."},
				{Name: "profile", Type: procgen.ParamEnum, Label: "Cross-section", Default: procgen.Enum("stepped"), Choices: terrain.ProfileNames,
					Doc: "v: a V gorge. u: steep walls, a wide floor. slot: sheer walls. stepped: benches down the walls. custom: the curve below."},
				{Name: "section", Type: procgen.ParamCurve, Label: "Custom section",
					Default: procgen.Curve([2]float64{0, 1}, [2]float64{0.5, 0.8}, [2]float64{1, 0}), Min: 0, Max: 1, MinItems: 2,
					Doc: "The custom cross-section: depth (1 floor, 0 rim) by distance from the centre (0) to the rim (1)."},
				{Name: "floor", Type: procgen.ParamFloat, Label: "Floor share", Default: f(0.15), Min: 0, Max: 0.95,
					Doc: "The flat floor's share of the width."},
				{Name: "steps", Type: procgen.ParamInt, Default: i(3), Min: 1, Max: 12, Doc: "The stepped profile's benches."},
				{Name: "meander", Type: procgen.ParamFloat, Label: "Meander (m)", Default: f(40), Min: 0, Max: 1000,
					Doc: "How far the canyon wanders off the drawn path."},
				{Name: "meander_length", Type: procgen.ParamFloat, Label: "Meander length (m)", Default: f(260), Min: 10, Max: 10000,
					Doc: "The meanders' wavelength."},
				{Name: "tributaries", Type: procgen.ParamInt, Default: i(4), Min: 0, Max: 64,
					Doc: "Side canyons branching off upstream, narrower and shallower, graded down to the main floor."},
				{Name: "tributary_length", Type: procgen.ParamFloat, Label: "Tributary length (m)", Default: f(220), Min: 10, Max: 5000,
					Doc: "How far a side canyon reaches."},
				{Name: "seed", Type: procgen.ParamSeed, Default: procgen.Seed(0), Doc: "Varies this canyon's meanders and tributaries."},
				{Name: "hold", Added: true, Type: procgen.ParamFloat, Label: "Hold", Default: f(0), Min: 0, Max: 1, Step: 0.01,
					Doc: "How much of the carved shape survives the erosion: 1 keeps a slot canyon's sheer walls and open floor, which " +
						"the talus would otherwise fill. 0: the canyon erodes like any ground."},
				{Name: "hold_margin", Added: true, Type: procgen.ParamFloat, Label: "Hold margin (m)", Default: f(6), Min: 0, Max: 200, Step: 0.5,
					Doc: "How far past the rims the hold reaches, easing out: the rims stay sharp."},
			}},

		// Drainage.
		{Name: "drainage", Type: procgen.ParamFloat, Label: "Drainage strength", Default: f(d.Drainage.Strength), Min: 0, Max: 10,
			Doc: "Stream-power erosion of the whole map: washes and dendritic gully networks cut back into the rims. 0 turns it off."},
		{Name: "drainage_iterations", Type: procgen.ParamInt, Default: i(d.Drainage.Iterations), Min: 1, Max: 1000,
			Doc: "Routing-and-incision rounds. More is a more mature network."},
		{Name: "drainage_cell", Type: procgen.ParamFloat, Label: "Drainage cell (m)", Default: f(d.Drainage.Cell), Min: 0.5, Max: 64,
			Doc: "The drainage grid's spacing: the narrowest wash it cuts is a couple of these."},
		{Name: "drainage_fill", Type: procgen.ParamFloat, Label: "Basin fill", Default: f(d.Drainage.Fill), Min: 0, Max: 1,
			Doc: "How fast closed basins fill with sediment: flat playas and sandy wash floors."},
		{Name: "drainage_diffusion", Type: procgen.ParamFloat, Label: "Hillslope creep", Default: f(d.Drainage.Diffusion), Min: 0, Max: 0.24,
			Doc: "Rounds the ground between channels, per round."},

		// Droplets.
		{Name: "droplets", Type: procgen.ParamFloat, Label: "Droplets per m²", Default: f(d.Droplets.Density), Min: 0, Max: 10, Step: 0.01,
			Doc: "Particle hydraulic erosion: rills, gullies and deposition at full resolution. 0 turns it off."},
		{Name: "droplet_lifetime", Type: procgen.ParamFloat, Label: "Droplet run (m)", Default: f(d.Droplets.Lifetime), Min: 2, Max: 500,
			Doc: "How far a droplet runs."},
		{Name: "droplet_inertia", Type: procgen.ParamFloat, Label: "Inertia", Default: f(d.Droplets.Inertia), Min: 0, Max: 1,
			Doc: "How much a droplet keeps its heading rather than turning downhill: higher is straighter, smoother channels."},
		{Name: "droplet_capacity", Type: procgen.ParamFloat, Label: "Capacity", Default: f(d.Droplets.Capacity), Min: 0, Max: 10, Step: 0.01,
			Doc: "Sediment (m³) a droplet carries per unit of slope, speed and water. The main strength control."},
		{Name: "droplet_erosion", Type: procgen.ParamFloat, Label: "Erosion rate", Default: f(d.Droplets.Erosion), Min: 0, Max: 1,
			Doc: "Pick-up rate per metre run."},
		{Name: "droplet_deposition", Type: procgen.ParamFloat, Label: "Deposition rate", Default: f(d.Droplets.Deposition), Min: 0, Max: 1,
			Doc: "Drop rate per metre run once over capacity."},
		{Name: "droplet_evaporation", Type: procgen.ParamFloat, Label: "Evaporation", Default: f(d.Droplets.Evaporation), Min: 0, Max: 1,
			Doc: "Water lost per metre run."},
		{Name: "droplet_radius", Type: procgen.ParamFloat, Label: "Erosion radius (m)", Default: f(d.Droplets.Radius), Min: 0.1, Max: 20,
			Doc: "The brush a droplet erodes with."},
		{Name: "droplet_gravity", Type: procgen.ParamFloat, Label: "Gravity", Default: f(d.Droplets.Gravity), Min: 0, Max: 50,
			Doc: "How fast a droplet speeds up downhill."},
		{Name: "droplet_passes", Type: procgen.ParamInt, Label: "Passes", Default: i(d.Droplets.Passes), Min: 1, Max: 64,
			Doc: "Rounds the droplets are split into, each over the whole map."},

		// Talus.
		{Name: "talus_angle", Type: procgen.ParamFloat, Label: "Talus angle (°)", Default: f(d.Thermal.TalusAngle), Min: 5, Max: 80,
			Doc: "The angle of repose of soft layers: scree slopes relax to it."},
		{Name: "talus_cliff_angle", Type: procgen.ParamFloat, Label: "Cliff angle (°)", Default: f(d.Thermal.CliffAngle), Min: 10, Max: 89,
			Doc: "The steepest a hard layer stands at."},
		{Name: "talus_iterations", Type: procgen.ParamInt, Label: "Talus iterations", Default: i(d.Thermal.Iterations), Min: 0, Max: 1000,
			Doc: "Rounds of sliding; more reaches further down the slope. 0 turns it off."},
		{Name: "talus_rate", Type: procgen.ParamFloat, Label: "Talus rate", Default: f(d.Thermal.Rate), Min: 0, Max: 1,
			Doc: "The share of the excess moved per round."},

		// Detail.
		{Name: "detail", Type: procgen.ParamFloat, Label: "Detail (m)", Default: f(d.Detail.Amplitude), Min: 0, Max: 10,
			Doc: "Fine texture after erosion: jointed slickrock swells and cracks on hard layers, crumbly bumps on soft ones, none on sand."},
		{Name: "detail_joint_spacing", Type: procgen.ParamFloat, Label: "Joint spacing (m)", Default: f(d.Detail.JointSpacing), Min: 1, Max: 200,
			Doc: "The slickrock crack network's spacing (the cracks run in a set along one bearing)."},

		// Masks.
		{Name: "cliff_angle", Type: procgen.ParamFloat, Label: "Cliff mask angle (°)", Default: f(d.CliffAngle), Min: 10, Max: 89,
			Doc: "The cliff mask's threshold."},
		{Name: "sand_color", Type: procgen.ParamColor, Label: "Sand colour",
			Default: procgen.Color(d.SandColor[0], d.SandColor[1], d.SandColor[2]), Min: 0, Max: 1,
			Doc: "The albedo hint's colour of washes and deposits."},

		// Output.
		{Name: "mesh_step", Type: procgen.ParamInt, Label: "Mesh step", Default: i(1), Min: 1, Max: 16,
			Doc: "Height samples per tile vertex: 2 meshes every other sample. Must divide a tile's samples."},
		{Name: "collision", Type: procgen.ParamEnum, Default: procgen.Enum("mesh"),
			Choices: []string{"mesh", "hulls", "box", "none", "auto"},
			Doc:     "How the tiles collide. mesh is the terrain's real surface; tiles are closed, so nav sees each as one solid."},
		{Name: "texture", Type: procgen.ParamTexture, Default: procgen.Texture(""),
			Doc: "The tiles' texture; none draws them in their normal colours."},
		{Name: "material", Added: true, Type: procgen.ParamString, Default: procgen.String(""),
			Doc: "The .fmat the tiles wear over their texture: a terrain_material stage's, gen_<generator>_<its id> (it prints the name). Empty: none."},
		{Name: "chunk", Added: true, Type: procgen.ParamEnum, Label: "Streaming", Default: procgen.Enum("auto"), Choices: []string{"auto", "none"},
			Doc: "Which level chunk the tiles stream with. auto: the chunk whose footprint holds each tile's centre (as every other " +
				"placement). none: always in — the ground under a level split into area chunks stays when an area is switched off."},
		{Name: "uv_mode", Type: procgen.ParamEnum, Label: "UV mode", Default: procgen.Enum("world"), Choices: []string{"world", "terrain"},
			Doc: "world: a top-down projection at UV scale, for a tiling texture. terrain: one 0..1 square over the whole terrain, " +
				"for a terrain-wide image such as the albedo hint."},
		{Name: "uv_scale", Type: procgen.ParamFloat, Label: "UV scale", Default: f(0.125), Min: 0.0001, Max: 16, Step: 0.005,
			Doc: "UVs per metre, in the world UV mode."},
		{Name: "cook", Type: procgen.ParamBool, Default: procgen.Bool(true),
			Doc: "Cook and place the tiles. Off writes them as .glb and places nothing."},
		{Name: "cook_memory", Type: procgen.ParamInt, Label: "Cook memory (MB)", Default: i(3000), Min: 256, Max: 262144,
			Doc: "How much memory the tile cooks may use together; fewer cook at once when tiles are big."},
		{Name: "preview_cells", Type: procgen.ParamInt, Label: "Preview cells", Default: i(256), Min: 32, Max: 1024,
			Doc: "The preview's samples a side, at most (a fast mode: the same landforms, coarser erosion)."},
	}, append(farParams(), landformParams()...)...)
}

// terrainSettings maps resolved params onto terrain.Settings.
func terrainSettings(env *procgen.Env) terrain.Settings {
	p := env.Params
	s := terrain.Defaults()
	s.Size, s.Cell, s.Seed = p.Float("size"), p.Float("resolution"), env.Seed
	s.Base = terrain.BaseShape{Height: p.Float("base_height"), Relief: p.Float("relief"), Frequency: p.Float("frequency"),
		Octaves: p.Int("octaves"), Gain: 0.5, Warp: p.Float("warp")}
	s.Mesa = terrain.Mesa{Height: p.Float("mesa_height"), Coverage: p.Float("mesa_coverage"), Scale: p.Float("mesa_scale"),
		Tiers: p.Int("mesa_tiers"), Rim: p.Float("mesa_rim"), Roughness: p.Float("mesa_roughness"), TopRelief: p.Float("mesa_top_relief"),
		Joints: p.Float("mesa_joints"), JointSpacing: p.Float("mesa_joint_spacing")}
	s.Strata.Layers = nil
	for _, it := range p.Items("strata") {
		c := it.Vec3("color")
		s.Strata.Layers = append(s.Strata.Layers, terrain.Layer{Thickness: it.Float("thickness"), Hardness: it.Float("hardness"), Color: c})
	}
	s.Strata.Base, s.Strata.Terrace, s.Strata.Warp = p.Float("strata_base"), p.Float("terrace"), p.Float("strata_warp")
	s.Strata.Dip, s.Strata.Resistance = p.Vec2("strata_dip"), p.Float("resistance")
	s.Strata.SlopeLo, s.Strata.SlopeHi = p.Float("terrace_slope_lo"), p.Float("terrace_slope_hi")
	s.Detail = terrain.Detail{Amplitude: p.Float("detail"), JointSpacing: p.Float("detail_joint_spacing")}
	for ci, it := range p.Items("canyons") {
		c := terrain.Canyon{
			Profile: terrain.ProfileFromName(it.Str("profile")), Floor: it.Float("floor"), Steps: it.Int("steps"),
			Meander: it.Float("meander"), MeanderLength: it.Float("meander_length"),
			Tributaries: it.Int("tributaries"), TributaryLength: it.Float("tributary_length"),
			Seed: noise.Hash64(env.Seed, 0x4341_4E59, uint64(ci), uint64(int64(it.Int("seed")))),
			Hold: it.Float("hold"), HoldMargin: it.Float("hold_margin"),
		}
		for _, q := range it.Points("path") {
			c.Path = append(c.Path, terrain.PathPoint{X: q[0], Z: q[1], Width: q[2], Depth: q[3]})
		}
		if c.Profile == terrain.ProfileCustom {
			curve := it.Curve("section")
			c.Custom = curve.CurveAt
		}
		s.Canyons = append(s.Canyons, c)
	}
	s.Drainage = terrain.Drainage{Strength: p.Float("drainage"), Iterations: p.Int("drainage_iterations"), Cell: p.Float("drainage_cell"),
		Exponent: 0.5, Fill: p.Float("drainage_fill"), Diffusion: p.Float("drainage_diffusion")}
	s.Droplets = terrain.Droplets{Density: p.Float("droplets"), Lifetime: p.Float("droplet_lifetime"), Inertia: p.Float("droplet_inertia"),
		Capacity: p.Float("droplet_capacity"), MinSlope: s.Droplets.MinSlope, Erosion: p.Float("droplet_erosion"),
		Deposition: p.Float("droplet_deposition"), Evaporation: p.Float("droplet_evaporation"), Radius: p.Float("droplet_radius"),
		Gravity: p.Float("droplet_gravity"), Passes: p.Int("droplet_passes")}
	s.Thermal = terrain.Thermal{Iterations: p.Int("talus_iterations"), TalusAngle: p.Float("talus_angle"),
		CliffAngle: p.Float("talus_cliff_angle"), Rate: p.Float("talus_rate")}
	s.CliffAngle = p.Float("cliff_angle")
	s.SandColor = p.Vec3("sand_color")
	// Ridges, dunes, coast, volcanoes, hummocks; the edge fade (all off by
	// default: terrain_landform.go).
	applyLandforms(p, &s)
	return s
}

// whole reports whether a/b is (very nearly) an integer, and it.
func whole(a, b float64) (int, bool) {
	q := a / b
	r := math.Round(q)
	return int(r), r >= 1 && math.Abs(q-r) < 1e-6*max(q, 1)
}

func validateTerrain(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	var out []string
	size, res, ts := p.Float("size"), p.Float("resolution"), p.Float("tile_size")
	if _, ok := whole(size, res); !ok {
		out = append(out, fmt.Sprintf("Size (%g m) must be a whole number of %g m samples", size, res))
	}
	if _, ok := whole(size, ts); !ok {
		out = append(out, fmt.Sprintf("Size (%g m) must be a whole number of %g m tiles", size, ts))
	}
	per, ok := whole(ts, res)
	if !ok {
		out = append(out, fmt.Sprintf("Tile size (%g m) must be a whole number of %g m samples", ts, res))
	} else if per%p.Int("mesh_step") != 0 {
		out = append(out, fmt.Sprintf("Mesh step (%d) must divide a tile's %d cells", p.Int("mesh_step"), per))
	}
	if n, ok := whole(size, res); ok && n > 16384 {
		out = append(out, fmt.Sprintf("%d samples a side is more than 16384", n))
	}
	for ci, it := range p.Items("canyons") {
		if n := len(it.Points("path")); n == 1 {
			out = append(out, fmt.Sprintf("canyon %d: a path needs at least two points", ci+1))
		}
	}
	out = append(out, validateStamps(p)...)
	return append(out, validateFar(p)...)
}
