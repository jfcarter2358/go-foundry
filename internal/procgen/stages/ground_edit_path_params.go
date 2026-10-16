package stages

// ground_edit_path_params.go — ground_edit's path parameters
// (docs/terrain-paint/PHASE_P4.md; the run is ground_edit_path.go, the
// laying itself procgen/terrain path*.go).
//
// WHY PATHS LIVE IN ground_edit, as rivers do (ground_edit_river_params.go
// says why for rivers, and the same holds): a path is an edit of the
// finished ground — its bed flattened, its surface and wear written as
// masks — and every stage downstream (the material's splat, the grass's
// wear, the scatters keeping clear, `orient_to` reading its direction)
// must see it. Inside ground_edit it is one more input to the same grid,
// laid after the rivers (so it knows where the water is and fords it rather
// than flattening into it), quantised, masked, recooked and rect-keyed with
// everything else, and its stones are placed next to the river boulders. A
// sibling `paths` stage would have been a second terrain source redoing all
// of that, and a path could not see a river's channel without reading
// another stage's grid.
//
// THE PARAMETERS:
//
//   - `paths`: each a spline with a width per point (the path brush writes
//     one per stroke, width = the brush's diameter; the 2D view edits it),
//     a style (rock_path, stepping_stones, dirt_trail, gravel_path,
//     game_trail — terrain.PathStyles: which stones, how strong the surface,
//     wear and keep-clear are, whether it fords water), flatten on or off,
//     and the shape numbers (terrain.PathShape);
//   - `path_rocks`: a rocks or polyhaven_rocks stage whose variants become
//     the flagstones, stepping stones and fords, each scaled to its stone's
//     size; a path's own `rocks` overrides it for its flagstones or
//     stepping stones (a rock path wants a flat stone, a ford a boulder).
//
// All are Added (a ground_edit without paths hashes as before); `paths` is
// RectCovered like `rivers`: editing a path reruns the stage, recooks the
// tiles its flatten moved, and downstream pieces rebuild only where the
// ground or the path masks changed (terrain.Terrain.RectKey).

import (
	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

// pathParams are ground_edit's path params (appended to its list).
func pathParams() []procgen.ParamDef {
	f, s, b := procgen.Float, procgen.String, procgen.Bool
	d := terrain.DefaultPathShape()
	fields := []procgen.ParamDef{
		{Name: "name", Type: procgen.ParamString, Default: s(""),
			Doc: "The path's name (paths.yaml, its stones' IDs). Empty: path_<n>."},
		{Name: "style", Type: procgen.ParamEnum, Default: procgen.Enum(terrain.PathDirt), Choices: terrain.PathStyleNames,
			Doc: "rock_path: flagstones filling the width on a stony splat. stepping_stones: a line of stones a stride apart, " +
				"the grass worn between them. dirt_trail and gravel_path: worn earth or gravel. game_trail: a faint, narrow " +
				"wear line, no stones. Every style but game_trail fords water with stepping stones."},
		{Name: "path", Type: procgen.ParamSpline, Label: "Path", Default: procgen.Spline(), MinItems: 0,
			Channels: []string{"width"}, ChannelDefaults: []float64{1.5},
			Doc: "The centreline, with the path's width at each point. The path brush draws one; edit it in the 2D view."},
		{Name: "flatten", Type: procgen.ParamBool, Default: b(d.Flatten),
			Doc: "Level the ground across the path to its smoothed centreline (never in water or a river's channel)."},
		{Name: "blend", Type: procgen.ParamFloat, Label: "Blend (m)", Default: f(d.Blend), Min: 0, Max: 20, Step: 0.1,
			Doc: "How far past the edge the flatten eases out."},
		{Name: "smooth", Type: procgen.ParamFloat, Label: "Smooth (m)", Default: f(d.Smooth), Min: 0, Max: 100, Step: 0.5,
			Doc: "The length the centreline's height is smoothed over: bumps shorter than this are levelled out."},
		{Name: "steps", Type: procgen.ParamBool, Default: b(false),
			Doc: "Cut treads where the path is steeper than the max grade."},
		{Name: "step_rise", Type: procgen.ParamFloat, Label: "Step rise (m)", Default: f(d.StepRise), Min: 0.05, Max: 1, Step: 0.01,
			Doc: "The height between treads."},
		{Name: "max_grade", Type: procgen.ParamFloat, Label: "Max grade", Default: f(d.MaxGrade), Min: 0.02, Max: 3, Step: 0.01,
			Doc: "Rise per metre above which a stepped path gets treads."},
		{Name: "edge", Type: procgen.ParamFloat, Label: "Worn edge", Default: f(d.Edge), Min: 0, Max: 1, Step: 0.01,
			Doc: "How much the edge wanders in and out, as a fraction of the half width: 0 is a ruled line."},
		{Name: "edge_size", Type: procgen.ParamFloat, Label: "Edge size (m)", Default: f(d.EdgeSize), Min: 0.1, Max: 50, Step: 0.1,
			Doc: "The worn edge's feature size."},
		{Name: "wear", Type: procgen.ParamFloat, Label: "Wear band (m)", Default: f(d.Wear), Min: 0, Max: 20, Step: 0.1,
			Doc: "How far past the edge the grass is still thinned and shortened (the path_wear mask)."},
		{Name: "stones", Type: procgen.ParamFloat, Label: "Stones", Default: f(d.Stones), Min: 0, Max: 1, Step: 0.01,
			Doc: "The share of the style's stones laid (flagstones, stepping stones, a ford's): 1 all, 0 none."},
		{Name: "stone_size", Type: procgen.ParamFloat, Label: "Stone size", Default: f(d.StoneSize), Min: 0.2, Max: 4, Step: 0.01,
			Doc: "Scales the stones (flagstones are about 0.7 m across at 1, stepping stones half a metre)."},
		{Name: "stride", Type: procgen.ParamFloat, Label: "Stride (m)", Default: f(d.Stride), Min: 0.3, Max: 3, Step: 0.01,
			Doc: "Stepping stones' spacing along the path (and a ford's)."},
		{Name: "rocks", Added: true, Type: procgen.ParamStage, StageKinds: RockSources, Default: procgen.StageRef(""),
			Label: "Rocks", Doc: "This path's flagstones or stepping stones from another rocks stage than the stage's Path rocks " +
				"(a flat stone for a rock path). Its ford stones still come from Path rocks. Empty: Path rocks."},
	}
	return []procgen.ParamDef{
		{Name: "paths", Added: true, RectCovered: true, Type: procgen.ParamList, Label: "Paths", Default: procgen.List(), Fields: fields,
			Doc: "Footpaths: each a spline with a width per point and a style. The ground is flattened along it, its style's " +
				"surface, wear, keep-clear and direction masks are written (path_<style>, path_wear, path_clear, path_dir_x/z) " +
				"for the material, grass and scatter stages to read, and stones are laid along it from Path rocks. " +
				"Where a path crosses water it fords it: stepping stones, no flatten. The path brushes add them."},
		{Name: "path_rocks", Added: true, Type: procgen.ParamStage, StageKinds: RockSources, Default: procgen.StageRef(""),
			Label: "Path rocks", Doc: "A rocks or polyhaven_rocks stage whose rocks become the paths' flagstones and stepping stones."},
	}
}

// pathShape is a paths item's shape.
func pathShape(it procgen.Params) terrain.PathShape {
	return terrain.PathShape{Flatten: it.Bool("flatten"), Blend: it.Float("blend"), Smooth: it.Float("smooth"),
		Steps: it.Bool("steps"), StepRise: it.Float("step_rise"), MaxGrade: it.Float("max_grade"), Edge: it.Float("edge"),
		EdgeSize: it.Float("edge_size"), Wear: it.Float("wear"), Stones: it.Float("stones"), StoneSize: it.Float("stone_size"),
		Stride: it.Float("stride")}
}
