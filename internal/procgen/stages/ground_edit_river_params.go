package stages

// ground_edit_river_params.go — ground_edit's river parameters
// (docs/terrain-paint/PHASE_W3.md; the run is ground_edit_river.go).
//
// WHY RIVERS LIVE IN ground_edit, not in a stage of their own. A river is
// an edit of the finished ground — a channel cut, banks trimmed, water
// standing in it — exactly what ground_edit already is: it reads the
// terrain's files, owns its tiles, quantises the result onto the
// heightmap, rewrites the masks, recooks only the tiles that moved,
// supersedes the source's placements and answers the rect keys downstream
// pieces are reused by. A separate `river` stage would have to be another
// terrain source doing all of that again, and a river running into a pond
// would cross two stages' grids. Inside ground_edit it is one more input to
// the same grid, carved after the height layers and the basins (so a river
// sees the ponds it runs into), and every downstream reader sees one edited
// terrain.
//
// THE PARAMETERS:
//
//   - `rivers`: the hand rivers — each a spline with a width and a depth
//     per point (the river brush writes one per stroke; the 2D view edits
//     it), a water material and the shape numbers (terrain.RiverShape);
//   - `drainage` and `drainage_*`: rivers traced from the ground's own
//     drainage network (terrain.TraceDrainage) above a flow threshold, a
//     width and depth from the flow, a mild meander; trimmed where a hand
//     river runs (an adopted river replaces its traced one);
//   - `river_rocks`: a rocks (or polyhaven_rocks) stage whose variants are
//     dropped as boulders in the riffles — they make foam.
//
// All are Added (a ground_edit without them hashes as before) and the
// river ones RectCovered (procgen.ParamDef.RectCovered): editing a river
// reruns the stage, recooks the tiles it moved, and downstream incremental
// pieces rebuild only where the ground or the river masks changed (the
// rect keys, terrain.Terrain.RectKey).

import (
	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

// River styles' default water presets, and the waterfalls' sheet material
// and mist effect (phase W4).
const (
	defaultRiverMaterial = "water_river"
	defaultFallsMaterial = "water_falls"
	defaultFallsMist     = "waterfall_mist"
)

// riverShapeFields are a river's shape params (the hand river's list
// fields; the drainage rivers take the defaults but for a few).
func riverShapeFields() []procgen.ParamDef {
	f := procgen.Float
	d := terrain.DefaultRiverShape()
	return []procgen.ParamDef{
		{Name: "bank_height", Type: procgen.ParamFloat, Label: "Bank height (m)", Default: f(d.BankHeight), Min: 0, Max: 10, Step: 0.01,
			Doc: "How far below the ground along the channel's middle the water stands: the bank's lip."},
		{Name: "floodplain", Type: procgen.ParamFloat, Label: "Floodplain (m)", Default: f(d.Floodplain), Min: 0, Max: 200, Step: 0.1,
			Doc: "A gentle shelf beyond each bank before the ground rises at the bank slope."},
		{Name: "bank_slope", Type: procgen.ParamFloat, Label: "Bank slope", Default: f(d.BankSlope), Min: 0.02, Max: 3, Step: 0.01,
			Doc: "The steepest the ground beyond the floodplain rises from the river (rise per metre). Higher ground is cut down to it."},
		{Name: "reach", Type: procgen.ParamFloat, Label: "Bank reach (m)", Default: f(d.Reach), Min: 0, Max: 200, Step: 0.5,
			Doc: "How far past the floodplain the bank cut may reach."},
		{Name: "meander", Type: procgen.ParamFloat, Label: "Meander (m)", Default: f(0), Min: 0, Max: 200, Step: 0.1,
			Doc: "How far the river may wander off the drawn path. 0 (the default for a painted river): exactly the bends drawn."},
		{Name: "meander_length", Type: procgen.ParamFloat, Label: "Meander length (m)", Default: f(d.MeanderLength), Min: 4, Max: 5000, Step: 1,
			Doc: "The meanders' wavelength."},
		{Name: "pools", Type: procgen.ParamFloat, Label: "Pools and riffles", Default: f(d.Pools), Min: 0, Max: 1, Step: 0.01,
			Doc: "How strongly deep slow pools alternate with shallow fast riffles (foam, boulders) along the river."},
		{Name: "riffle_spacing", Type: procgen.ParamFloat, Label: "Riffle spacing (m)", Default: f(0), Min: 0, Max: 1000, Step: 0.5,
			Doc: "Distance between riffles. 0: six widths, as rivers tend to."},
		{Name: "max_slope", Type: procgen.ParamFloat, Label: "Waterfall slope", Default: f(d.MaxSlope), Min: 0, Max: 10, Step: 0.01,
			Doc: "A stretch steeper than this (rise per metre) becomes a waterfall: a step in the surface and a plunge pool. 0: never."},
		{Name: "min_fall", Type: procgen.ParamFloat, Label: "Min fall (m)", Default: f(d.MinFall), Min: 0, Max: 100, Step: 0.05,
			Doc: "The least drop a waterfall is worth; a smaller steep stretch stays a rapid."},
		{Name: "speed", Type: procgen.ParamFloat, Label: "Current (m/s)", Default: f(d.Speed), Min: 0, Max: 4, Step: 0.01,
			Doc: "The current through a typical stretch: faster in narrows and riffles, slower in pools."},
		{Name: "boulders", Type: procgen.ParamFloat, Label: "Boulders", Default: f(0), Min: 0, Max: 20, Step: 0.1,
			Doc: "Boulders per riffle per 4 m of width (needs the stage's River rocks); each throws foam."},
		// Phase W4 (terrain/river_falls.go).
		{Name: "cascade_step", Added: true, Type: procgen.ParamFloat, Label: "Cascade step (m)", Default: f(0), Min: 0, Max: 20, Step: 0.05,
			Doc: "A long steep stretch (a stream down a valley side) becomes a staircase of small waterfalls about this high, " +
				"each with its own pool, falling sheet and mist. 0: it stays a sloping, foamy rapid."},
	}
}

// riverParams are ground_edit's river params (appended to its list).
func riverParams() []procgen.ParamDef {
	f, s, v2 := procgen.Float, procgen.String, procgen.Vec2
	dd := terrain.DefaultDrainage()
	fields := []procgen.ParamDef{
		{Name: "name", Type: procgen.ParamString, Default: s(""),
			Doc: "The river's name (rivers.yaml, confluences, its water meshes). Empty: river_<n>."},
		{Name: "path", Type: procgen.ParamSpline, Label: "Path", Default: procgen.Spline(), MinItems: 0,
			Channels: []string{"width", "depth"}, ChannelDefaults: []float64{4, 1},
			Doc: "The centreline, either way round (it is turned to run downhill), with the bank-to-bank width and the " +
				"thalweg's depth below the water at each point. The river brush draws one; edit it in the 2D view."},
		{Name: "material", Type: procgen.ParamString, Default: s(""),
			Doc: "The water material (any .fmat with a water effect; water_river has flow mapping). Empty: water_river."},
		{Name: "from", Type: procgen.ParamEnum, Default: procgen.Enum("painted"), Choices: []string{"painted", "adopted"},
			Doc: "Where it came from: painted with the brush, or adopted from a drainage river (it then replaces it)."},
	}
	fields = append(fields, riverShapeFields()...)
	cov := func(p procgen.ParamDef) procgen.ParamDef {
		p.Added, p.RectCovered = true, true
		return p
	}
	return []procgen.ParamDef{
		cov(procgen.ParamDef{Name: "rivers", Type: procgen.ParamList, Label: "Rivers", Default: procgen.List(), Fields: fields,
			Doc: "Hand rivers: each a spline with a width and depth per point, carved as a channel whose water surface never " +
				"rises downstream (pools, riffles, waterfalls where it is steep, confluences where one ends on another) and " +
				"meshed as a flowing ribbon. The river and stream brushes add them."}),
		cov(procgen.ParamDef{Name: "drainage", Type: procgen.ParamBool, Label: "Rivers from drainage", Default: procgen.Bool(false),
			Doc: "Also trace rivers down the ground's own drainage network wherever the flow is above the threshold."}),
		cov(procgen.ParamDef{Name: "drainage_threshold", Type: procgen.ParamFloat, Label: "Drainage threshold", Default: f(dd.Threshold),
			Min: 0.05, Max: 1, Step: 0.01,
			Doc: "The flow value (the flow mask's scale: 0 at 4 m² drained, 1 at a quarter of a km²) from which ground carries a river."}),
		cov(procgen.ParamDef{Name: "drainage_min_length", Type: procgen.ParamFloat, Label: "Min length (m)", Default: f(dd.MinLength),
			Min: 0, Max: 100000, Step: 1, Doc: "Traced rivers shorter than this are dropped."}),
		cov(procgen.ParamDef{Name: "drainage_width", Type: procgen.ParamVec2, Label: "Width from flow (m)", Default: v2(dd.WidthMin, dd.WidthMax),
			Min: 0.2, Max: 500, Step: 0.1,
			Doc: "A traced river's width at the threshold and at full flow: it widens downstream as its catchment grows."}),
		cov(procgen.ParamDef{Name: "drainage_depth", Type: procgen.ParamVec2, Label: "Depth from flow (m)", Default: v2(dd.DepthMin, dd.DepthMax),
			Min: 0.05, Max: 50, Step: 0.05, Doc: "Its depth at the threshold and at full flow."}),
		cov(procgen.ParamDef{Name: "drainage_meander", Type: procgen.ParamFloat, Label: "Drainage meander (m)", Default: f(1),
			Min: 0, Max: 100, Step: 0.1, Doc: "A mild wander given to traced rivers (whose paths follow the grid otherwise)."}),
		cov(procgen.ParamDef{Name: "drainage_boulders", Type: procgen.ParamFloat, Label: "Drainage boulders", Default: f(0),
			Min: 0, Max: 20, Step: 0.1, Doc: "Boulders per riffle per 4 m of width in traced rivers."}),
		cov(procgen.ParamDef{Name: "drainage_material", Type: procgen.ParamString, Label: "Drainage material", Default: s(""),
			Doc: "Traced rivers' water material. Empty: water_river."}),
		cov(procgen.ParamDef{Name: "drainage_max", Type: procgen.ParamInt, Label: "Max traced rivers", Default: procgen.Int(0),
			Min: 0, Max: 10000, Doc: "Keep at most this many traced rivers (largest first). 0: all."}),
		{Name: "river_rocks", Added: true, Type: procgen.ParamStage, StageKinds: RockSources, Default: procgen.StageRef(""),
			Label: "River rocks", Doc: "A rocks or polyhaven_rocks stage whose rocks are dropped as boulders in the riffles."},
		// Phase W4's waterfalls (ground_edit_falls.go).
		cov(procgen.ParamDef{Name: "falls_material", Type: procgen.ParamString, Label: "Falls material", Default: s(defaultFallsMaterial),
			Doc: "The water material every waterfall's falling sheet wears (a water effect with `falls` set; water_falls ships)."}),
		cov(procgen.ParamDef{Name: "falls_mist", Type: procgen.ParamString, Label: "Falls mist", Default: s(defaultFallsMist),
			Doc: "The particle effect (resources/particles, no extension) placed as spray at the foot of every waterfall. Empty: none."}),
		// Phase W5's water sounds (ground_edit_audio.go).
		waterSoundParamDefs()[0], waterSoundParamDefs()[1],
	}
}

// riverShape is a hand river item's shape.
func riverShape(it procgen.Params) terrain.RiverShape {
	return terrain.RiverShape{BankHeight: it.Float("bank_height"), Floodplain: it.Float("floodplain"), BankSlope: it.Float("bank_slope"),
		Reach: it.Float("reach"), Meander: it.Float("meander"), MeanderLength: it.Float("meander_length"), Pools: it.Float("pools"),
		RiffleSpacing: it.Float("riffle_spacing"), MaxSlope: it.Float("max_slope"), MinFall: it.Float("min_fall"),
		Speed: it.Float("speed"), Boulders: it.Float("boulders"), CascadeStep: it.Float("cascade_step")}
}
