package stages

// terrain_landform.go — the `terrain` stage's landform, height-compatibility
// and water parameters (docs/regions/PRESETS.md, "Engine work", items 3–5),
// and their mapping onto terrain.Settings.
//
// They are what environment presets beyond canyon country set: an alpine
// preset's ridged ranges and glacial valleys, a dunes preset's dune field, a
// coast preset's shoreline and sea, a volcanic preset's cones and lava
// lobes, a tundra or wetland preset's hummocks and ponds (the generation is
// procgen/terrain/landform.go). Then two that only mean something in a
// world of several regions:
//
//   - base_elevation / edge_rise (terrain/edge.go): the level this region
//     meets its neighbours at, and how far inside its own ground its relief
//     takes to rise from it — so an alpine region's mountains climb out of
//     the plain inside its footprint instead of meeting a savannah as an
//     escarpment across the band;
//   - water / water_level / ponds (stages/region_terrain_water.go): standing
//     water the region terrain fills in over the finished ground — a sea at
//     a level, closed hollows as ponds — and places as water surfaces.
//
// The classic terrain stage reads the landforms (a preset is an ordinary
// generator: it can be run on its own, square, to see its ground) but not
// the last two, which need a region terrain: its notes say so.
//
// EVERY ONE IS `Added` AND OFF AT ITS DEFAULT, so an existing document's
// hash and output do not move (terrain_golden_test.go,
// TestDesertCanyonHashesUnchangedByRegions) and a preset that does not set
// them grows the classic ground.

import (
	"fmt"

	"foundry/internal/procgen"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/terrain"
)

// landformParams are the terrain stage's landform, edge and water
// parameters, all Added.
func landformParams() []procgen.ParamDef {
	f, i, b := procgen.Float, procgen.Int, procgen.Bool
	fl := func(name, label string, def, lo, hi float64, doc string) procgen.ParamDef {
		return procgen.ParamDef{Name: name, Added: true, Type: procgen.ParamFloat, Label: label, Default: f(def), Min: lo, Max: hi, Doc: doc}
	}
	return []procgen.ParamDef{
		// Ridged mountains.
		fl("ridge_height", "Ridge height (m)", 0, 0, 5000,
			"Ridged mountains: sharp crests and branching spurs rising this far above the lowland. 0: none."),
		fl("ridge_scale", "Ridge scale (m)", 1400, 50, 50000, "The ranges' typical spacing. Height over scale above about 0.25 makes walls too steep to stand."),
		{Name: "ridge_octaves", Added: true, Type: procgen.ParamInt, Label: "Ridge octaves", Default: i(5), Min: 1, Max: 10,
			Doc: "Detail in the ranges: more octaves, more spurs and gullies."},
		fl("ridge_valleys", "Glacial valleys", 0, 0, 1,
			"Flattens the valley floors between the ranges into broad, flat-floored troughs under steepened walls (a U profile). 0: the ridged noise's own V valleys."),
		fl("ridge_warp", "Ridge warp (m)", 150, 0, 5000, "Bends the ranges."),

		// Dunes.
		fl("dune_height", "Dune height (m)", 0, 0, 300, "A dune field: crests this high above the interdune flats. 0: none."),
		fl("dune_wavelength", "Dune spacing (m)", 120, 4, 5000, "Crest to crest."),
		fl("dune_wind", "Wind bearing (°)", 0, -360, 360,
			"The direction the wind blows TOWARD, degrees from +X toward +Z: the long gentle stoss slopes face into it, the steep slip faces away."),
		fl("dune_asymmetry", "Dune asymmetry", 0.8, 0, 1,
			"How much steeper the lee (slip) face is than the stoss slope; capped at the angle of repose (34°)."),
		fl("dune_sinuosity", "Crest sinuosity", 0.5, 0, 1, "How much the crests bend."),
		fl("dune_cover", "Dune cover", 0.75, 0, 1, "The share of the ground under dunes; the rest is flat interdune corridors."),
		fl("dune_ripple", "Ripple height (m)", 0, 0, 2, "Wind ripples on the stoss slopes. 0: none."),
		fl("dune_ripple_wavelength", "Ripple spacing (m)", 4, 0.5, 50, "Ripple crest to crest: keep it at least four samples."),

		// Coast.
		{Name: "coast", Added: true, Type: procgen.ParamEnum, Label: "Coast", Default: procgen.Enum("off"), Choices: terrain.CoastSides,
			Doc: "A shoreline: the sea lies toward this side of the region's footprint (+x, -x, +z, -z), or the right of the " +
				"drawn coast line. off: none."},
		{Name: "coast_line", Added: true, Type: procgen.ParamSpline, Label: "Coast line", Default: procgen.Spline(),
			Doc: "With coast: line, the shoreline (preset coordinates); walking it in order, the sea is on the right."},
		fl("sea_level", "Sea level (m)", 0, -2000, 2000, "The coast's sea level: the shore is at this height and the sea floor below it."),
		fl("coast_offset", "Shore offset (m)", 150, 0, 10000, "How far inside the footprint's side the shoreline runs."),
		fl("coast_meander", "Shore meander (m)", 40, 0, 2000, "How far the shoreline wanders off its line: bays and headlands."),
		fl("beach_width", "Beach width (m)", 30, 0, 1000, "The beach's width (with a cliff: the wave-cut platform at its foot)."),
		fl("beach_rise", "Beach rise (m)", 2, 0, 50, "How far the beach climbs above the sea."),
		fl("shelf_width", "Shelf width (m)", 150, 1, 10000, "How far offshore the sea floor reaches its depth."),
		fl("sea_depth", "Sea depth (m)", 12, 0, 2000, "The sea floor's depth below sea level past the shelf."),
		fl("cliff_height", "Sea cliff height (m)", 0, 0, 500, "A wave-cut cliff behind the beach. 0: none; the land rises gently."),
		fl("cliff_width", "Sea cliff run (m)", 10, 1, 500, "The cliff's horizontal run: smaller is sheerer."),
		fl("coast_inland", "Coast inland (m)", 120, 1, 10000, "How far inland the land's own relief takes to return."),

		// Volcanoes.
		{Name: "volcanoes", Added: true, Type: procgen.ParamInt, Label: "Volcanoes", Default: i(0), Min: 0, Max: 32,
			Doc: "Volcanic cones placed (seeded) inside the region's footprint. 0 and no vents: none."},
		{Name: "volcano_vents", Added: true, Type: procgen.ParamSpline, Label: "Vents", Default: procgen.Spline(),
			Doc: "Explicit vent positions (preset coordinates), instead of seeded ones. The points are vents, not a path."},
		fl("volcano_height", "Cone height (m)", 150, 0, 5000, "A cone's height above the ground round it."),
		fl("volcano_radius", "Cone radius (m)", 250, 10, 20000, "A cone's radius at its foot."),
		fl("crater", "Crater share", 0.18, 0, 0.6, "The summit crater's radius as a share of the cone's. 0: none."),
		fl("crater_depth", "Crater depth (m)", 35, 0, 2000, "How deep the crater bowl drops below its rim."),
		{Name: "lava_lobes", Added: true, Type: procgen.ParamInt, Label: "Lava lobes", Default: i(0), Min: 0, Max: 32,
			Doc: "Lava flow tongues per cone: flat-topped, steep lobate margins and fronts, running down the flank and out."},
		fl("lava_length", "Lobe length (m)", 300, 1, 20000, "A lobe's typical length."),
		fl("lava_width", "Lobe width (m)", 50, 2, 5000, "A lobe's typical width."),
		fl("lava_thickness", "Lobe thickness (m)", 5, 0, 200, "A lobe's thickness: the height of its front."),

		// Hummocks and ponds.
		fl("hummock_height", "Hummock height (m)", 0, 0, 20, "Frost-heave hummocks: low domes this high. 0: none."),
		fl("hummock_spacing", "Hummock spacing (m)", 6, 1, 500, "Their spacing."),
		fl("pond_cover", "Pond cover", 0, 0, 0.8, "The share of the ground in flat-floored hollows (thermokarst ponds); with ponds on, the region terrain fills them with water. 0: none."),
		fl("pond_depth", "Pond depth (m)", 1.5, 0, 100, "How deep the hollows are cut."),
		fl("pond_scale", "Pond size (m)", 70, 4, 10000, "The hollows' typical size."),

		// Height compatibility (region worlds only).
		fl("base_elevation", "Base elevation (m)", 0, -5000, 5000,
			"In a world of several regions: the ground level this region meets its neighbours at (with Edge rise)."),
		fl("edge_rise", "Edge rise (m)", 0, 0, 20000,
			"In a world of several regions: across the transition band this region stands at its base elevation, and its "+
				"relief rises back over this many metres inside its own footprint — mountains climbing out of the plain "+
				"rather than meeting it as a cliff. 0: off (the bands blend whatever heights meet there)."),

		// Water (region worlds only).
		{Name: "water", Added: true, Type: procgen.ParamBool, Label: "Water at a level", Default: b(false),
			Doc: "In a world of several regions: ground of this region below Water level stands under water — a sea, a flooded " +
				"wetland — placed as water surfaces by the region terrain."},
		fl("water_level", "Water level (m)", 0, -5000, 5000, "The water's surface height with Water at a level on (a coast's sea level, a wetland's water table)."),
		{Name: "ponds", Added: true, Type: procgen.ParamBool, Label: "Fill ponds", Default: b(false),
			Doc: "In a world of several regions: closed hollows of this region's ground fill with water to just below their rims."},
		fl("pond_min_depth", "Pond min depth (m)", 0.3, 0, 100, "A hollow shallower than this stays dry."),
		fl("pond_min_area", "Pond min area (m²)", 40, 0, 1e7, "A hollow smaller than this stays dry."),

		// Authored landforms (terrain/stamps.go).
		{Name: "stamps", Added: true, Type: procgen.ParamList, Label: "Stamps", Default: procgen.List(), MaxItems: 64,
			Doc: "Authored landforms placed where the layout needs them, added to the base shape before erosion: mountain " +
				"massifs, plateaus, flat platforms and ledges, ramps and cuts. Draw each one's points in the top-down view.",
			Fields: stampFields()},
	}
}

// stampFields is a stamp list item's schema.
func stampFields() []procgen.ParamDef {
	f := procgen.Float
	return []procgen.ParamDef{
		{Name: "kind", Type: procgen.ParamEnum, Default: procgen.Enum("mountain"), Choices: terrain.StampKindNames,
			Doc: "mountain: a massif whose crest runs through the points (height: rise, radius: flank reach). plateau: a " +
				"flat-topped rise (height: rise, radius: the top's reach from the points). platform: each point a flat disc at " +
				"the ABSOLUTE height (a ledge, a terrace). ramp: a flattened path at the absolute heights along it (radius: " +
				"half width). cut: a trench or pit (height: depth)."},
		{Name: "points", Type: procgen.ParamSpline, Label: "Points", Default: procgen.Spline(),
			Channels: []string{"radius", "height"}, ChannelDefaults: []float64{60, 80},
			Doc: "The crest, path or points, each with its radius and height (what height means depends on the kind)."},
		{Name: "falloff", Type: procgen.ParamFloat, Label: "Falloff (m)", Default: f(20), Min: 0, Max: 2000, Step: 0.5,
			Doc: "How far past the radius the stamp eases out: a plateau's rim, a platform's or ramp's edge, a cut's bank."},
		{Name: "rough", Type: procgen.ParamFloat, Label: "Roughness", Default: f(0.4), Min: 0, Max: 1, Step: 0.01,
			Doc: "Ragged outlines and, on a mountain, lumpy flanks."},
		{Name: "ridges", Type: procgen.ParamFloat, Label: "Ridges", Default: f(0.6), Min: 0, Max: 1, Step: 0.01,
			Doc: "A mountain's spurs and gullies."},
		{Name: "strength", Type: procgen.ParamFloat, Default: f(1), Min: 0, Max: 1, Step: 0.01,
			Doc: "How fully a platform or ramp flattens the ground to its height."},
		{Name: "hold", Type: procgen.ParamFloat, Default: f(0), Min: 0, Max: 1, Step: 0.01,
			Doc: "How much of the stamp's shape survives the erosion: 1 for a ledge or terrace that must stay flat, 0 to let " +
				"the drainage, droplets and talus carve it like any ground."},
		{Name: "seed", Type: procgen.ParamSeed, Default: procgen.Seed(0), Doc: "Varies this stamp's noise."},
	}
}

// stamps maps a terrain's resolved stamp items onto terrain.Stamps.
func stamps(p procgen.Params, seed uint64) []terrain.Stamp {
	var out []terrain.Stamp
	for si, it := range p.Items("stamps") {
		st := terrain.Stamp{Kind: terrain.StampKindFromName(it.Str("kind")), Falloff: it.Float("falloff"),
			Rough: it.Float("rough"), Ridges: it.Float("ridges"), Strength: it.Float("strength"), Hold: it.Float("hold"),
			Seed: noise.Hash64(seed, 0x5354_414D, uint64(si), uint64(int64(it.Int("seed"))))}
		for _, q := range it.Points("points") {
			st.Points = append(st.Points, terrain.StampPoint{X: q[0], Z: q[1], Radius: q[2], Height: q[3]})
		}
		out = append(out, st)
	}
	return out
}

func validateStamps(p procgen.Params) []string {
	var out []string
	for si, it := range p.Items("stamps") {
		k := it.Str("kind")
		if len(it.Points("points")) == 0 {
			out = append(out, fmt.Sprintf("stamp %d (%s): draw at least one point", si+1, k))
		}
		if k == "ramp" && len(it.Points("points")) < 2 {
			out = append(out, fmt.Sprintf("stamp %d: a ramp needs two points", si+1))
		}
	}
	return out
}

// applyLandforms fills s's landforms and edge from resolved terrain params
// p. A landform whose switch is off (its height, count or side at the
// default) is left at its zero value, so the schema's defaults map onto
// terrain.Defaults() exactly (TestTerrainSchemaDefaultsAreTheGeneratorsDefaults).
func applyLandforms(p procgen.Params, s *terrain.Settings) {
	lf := &s.Landform
	if h := p.Float("ridge_height"); h > 0 {
		lf.Ridge = terrain.Ridge{Height: h, Scale: p.Float("ridge_scale"), Octaves: p.Int("ridge_octaves"),
			Valleys: p.Float("ridge_valleys"), Warp: p.Float("ridge_warp")}
	}
	if h := p.Float("dune_height"); h > 0 {
		lf.Dunes = terrain.Dunes{Height: h, Wavelength: p.Float("dune_wavelength"), Wind: p.Float("dune_wind"),
			Asymmetry: p.Float("dune_asymmetry"), Sinuosity: p.Float("dune_sinuosity"), Cover: p.Float("dune_cover"),
			RippleHeight: p.Float("dune_ripple"), RippleWavelength: p.Float("dune_ripple_wavelength")}
	}
	if h, c := p.Float("hummock_height"), p.Float("pond_cover"); h > 0 || c > 0 {
		lf.Hummocks = terrain.Hummocks{Height: h, Spacing: p.Float("hummock_spacing"), Ponds: c,
			PondDepth: p.Float("pond_depth"), PondScale: p.Float("pond_scale")}
	}
	vents := p.Points("volcano_vents")
	if n := p.Int("volcanoes"); n > 0 || len(vents) > 0 {
		lf.Volcano = terrain.Volcano{Count: n, Height: p.Float("volcano_height"), Radius: p.Float("volcano_radius"),
			Crater: p.Float("crater"), CraterDepth: p.Float("crater_depth"), Lobes: p.Int("lava_lobes"),
			LobeLength: p.Float("lava_length"), LobeWidth: p.Float("lava_width"), LobeThickness: p.Float("lava_thickness")}
		for _, q := range vents {
			lf.Volcano.Vents = append(lf.Volcano.Vents, [2]float64{q[0], q[1]})
		}
	}
	if side := p.Str("coast"); side != "off" && side != "" {
		lf.Coast = terrain.Coast{Side: side, SeaLevel: p.Float("sea_level"), Offset: p.Float("coast_offset"),
			Meander: p.Float("coast_meander"), Beach: p.Float("beach_width"), BeachRise: p.Float("beach_rise"),
			Shelf: p.Float("shelf_width"), Depth: p.Float("sea_depth"), Cliff: p.Float("cliff_height"),
			CliffWidth: p.Float("cliff_width"), Inland: p.Float("coast_inland")}
		for _, q := range p.Points("coast_line") {
			lf.Coast.Line = append(lf.Coast.Line, [2]float64{q[0], q[1]})
		}
	}
	lf.Stamps = stamps(p, s.Seed)
	if r := p.Float("edge_rise"); r > 0 {
		s.Edge = terrain.Edge{Level: p.Float("base_elevation"), Rise: r}
	}
}

// waterRule is a preset terrain stage's water settings (resolved params p).
func waterRule(p procgen.Params) terrain.WaterRule {
	return terrain.WaterRule{HasLevel: p.Bool("water"), Level: p.Float("water_level"), Ponds: p.Bool("ponds"),
		MinDepth: p.Float("pond_min_depth"), MinArea: p.Float("pond_min_area")}
}
