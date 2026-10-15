package stages

// scatter.go — the `scatter` stage: rocks stages' variants scattered over a
// terrain stage by a list of rules, each filtered by the terrain's masks —
// talus at the feet of cliffs, boulder fields, pebbles in the washes.
//
// The placement itself is internal/procgen/place (scatter.go there has the
// whole method: mask weights, clumping, candidates by block, satellites,
// Poisson-disk keep biggest first, seating, collision by size). This file
// is the schema and its mapping onto place.Rule; scatter_run.go the run
// (placements, scatter.yaml, Load) and the preview.
//
// The rules run in order, and each keeps clear of what the earlier ones
// placed: list the big rocks first. A rule's rocks stage should make LIGHT
// variants for anything placed by the thousand (rubble, pebbles: a few
// thousand triangles) — every placement costs the Nanite cull its mesh's
// leaf tiles (docs/canyon/SPEC.md, "Engine facts"; PHASE_3.md has the
// measured cost).

import (
	"fmt"

	"foundry/internal/procgen"
	"foundry/internal/procgen/place"
)

// ScatterKind is the stage's name.
const ScatterKind = "scatter"

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     ScatterKind,
		Label:    "Scatter",
		Category: "Placement",
		// Per environment region of a level generation doc: weighted by the
		// region, over its band (place_region.go).
		Regional: procgen.RegionalPerRegion,
		Version:  1,
		Doc: "Rocks scattered over a terrain by a list of rules — talus at cliff feet, boulder fields, pebbles in washes. " +
			"Each rule weighs the ground by the terrain's masks (slope, height, flow, deposition, curvature, cliff, distance " +
			"below a cliff) and a clumping noise, throws candidates at its density, keeps them by a Poisson-disk test " +
			"biggest first with power-law sizes, aligns them to the ground, sinks them in, and gives them collision by size.",
		Params:   scatterParams(),
		Run:      runScatter,
		Preview:  previewScatter,
		Load:     loadScatter,
		Validate: validateScatter,
	})
}

func scatterRuleFields() []procgen.ParamDef {
	f := procgen.Float
	d := place.DefaultRule()
	fields := []procgen.ParamDef{
		{Name: "name", Type: procgen.ParamString, Default: procgen.String("rocks"),
			Doc: "The rule's name: its placements' IDs start with it."},
		{Name: "enabled", Type: procgen.ParamBool, Default: procgen.Bool(true), Doc: "Off skips the rule."},
		{Name: "rocks", Type: procgen.ParamStage, StageKinds: ScatterSources, Default: procgen.StageRef(""),
			Doc: "The rocks stage (generated rocks, Poly Haven rock or plant scans — any stage whose product is a rocks manifest) whose variants this rule places, picked at random per rock. " +
				"For plants keep Align and Tilt at 0 (trees grow upright) and raise Spacing: a plant's footprint is its trunk or stems."},
		{Name: "max_triangles", Type: procgen.ParamInt, Label: "Max triangles", Default: procgen.Int(0), Min: 0, Max: 100000000,
			Doc: "Place only the rocks stage's variants of at most this many triangles (0: all; none that light: its lightest). " +
				"Every placement costs the cull its mesh's leaf tiles, so a dense rule wants light variants — a scanned boulder can be millions."},
		{Name: "color", Type: procgen.ParamColor, Label: "Dot colour", Default: procgen.Color(1, 1, 1), Min: 0, Max: 1,
			Doc: "The rule's dots in the 2D view. White picks a colour per rule."},
		{Name: "density", Type: procgen.ParamFloat, Label: "Density (/100 m²)", Default: f(d.Density), Min: 0, Max: 10000, Step: 0.05,
			Doc: "Candidates per 100 m² of ground at full weight. Spacing then thins them where they crowd."},
		{Name: "spacing", Type: procgen.ParamFloat, Default: f(d.Spacing), Min: 0.2, Max: 4, Step: 0.01,
			Doc: "How far apart footprints keep: centres at least this times the sum of their radii (1: just touching)."},
		{Name: "scale", Type: procgen.ParamVec2, Default: procgen.Vec2(d.Scale[0], d.Scale[1]), Min: 0.01, Max: 100, Step: 0.01,
			Doc: "The scale range, on top of the variants' own sizes."},
		{Name: "scale_bias", Type: procgen.ParamFloat, Label: "Size power law", Default: f(d.ScaleBias), Min: 0, Max: 6, Step: 0.05,
			Doc: "The power law's exponent: 0 draws scales evenly, 2–3 many small and few big, as real rock fields are."},
	}
	fields = append(fields, bandParams()...)
	fields = append(fields,
		procgen.ParamDef{Name: "satellites", Type: procgen.ParamFloat, Default: f(0), Min: 0, Max: 100, Step: 0.05,
			Doc: "Extra candidates round each rock the EARLIER rules placed, per metre of its footprint radius: rubble skirting the boulders."},
		procgen.ParamDef{Name: "satellite_spread", Type: procgen.ParamFloat, Label: "Satellite spread", Default: f(d.SatelliteSpread), Min: 0, Max: 20, Step: 0.05,
			Doc: "How far out the satellites reach, in the host's footprint radii."},
		procgen.ParamDef{Name: "align", Type: procgen.ParamFloat, Label: "Align to ground", Default: f(d.Align), Min: 0, Max: 1, Step: 0.01,
			Doc: "How far each rock is turned toward the ground's normal (averaged over its footprint): 0 upright, 1 flush with the slope."},
		procgen.ParamDef{Name: "tilt", Type: procgen.ParamFloat, Label: "Tilt (°)", Default: f(d.Tilt), Min: 0, Max: 90, Step: 0.5,
			Doc: "A random tilt on top, up to this many degrees about a random horizontal axis (every rock is yawed at random too)."},
		procgen.ParamDef{Name: "sink", Type: procgen.ParamFloat, Default: f(d.Sink), Min: 0, Max: 1, Step: 0.01,
			Doc: "How far each rock sits in the ground, as a fraction of its height (±40% per rock; less for a flat-based variant). " +
				"Before the sink it is lowered until no part of its base floats."},
		procgen.ParamDef{Name: "max_bury", Type: procgen.ParamFloat, Label: "Max bury", Default: f(d.MaxBury), Min: 0, Max: 2, Step: 0.01,
			Doc: "A candidate whose ground rises more than this fraction of its height across its footprint is dropped (a ledge edge, a gully)."},
		procgen.ParamDef{Name: "collision", Type: procgen.ParamEnum, Default: procgen.Enum("auto"), Choices: []string{"auto", "hulls", "mesh", "box", "none"},
			Doc: "Auto by size: none under Collision min, hulls up to Hull max (when the variant was cooked with hulls), the mesh above."},
		procgen.ParamDef{Name: "collision_min", Type: procgen.ParamFloat, Label: "Collision min (m)", Default: f(d.CollisionMin), Min: 0, Max: 100, Step: 0.01,
			Doc: "Rocks smaller than this (largest extent, scaled) do not collide: a body would only snag on them."},
		procgen.ParamDef{Name: "hull_max", Type: procgen.ParamFloat, Label: "Hull max (m)", Default: f(d.HullMax), Min: 0, Max: 1000, Step: 0.1,
			Doc: "Rocks up to this size collide as their hulls; larger ones as their mesh."},
		procgen.ParamDef{Name: "max_instances", Type: procgen.ParamInt, Label: "Max rocks", Default: procgen.Int(d.MaxInstances), Min: 0, Max: 1000000,
			Doc: "The most this rule places (0: no cap). The biggest are kept."},
		// Added: 0 keeps every variant, so existing rules hash and place
		// as before (docs/regions/PRESETS_2.md).
		procgen.ParamDef{Name: "min_size", Added: true, Type: procgen.ParamFloat, Label: "Min piece size (m)", Default: f(0), Min: 0, Max: 1000, Step: 0.01,
			Doc: "Leave out the source's variants smaller than this (their largest side, before the rule's scale): a Poly Haven " +
				"grass or moss set splits into clumps from a few centimetres to a third of a metre, and a rule placing clumps by " +
				"the hundred wants only the big ones. 0: every variant. When none is this big, the largest is kept."},
	)
	// The rocks' material (materials_out.go): a ground blend and a colour
	// match toward the stage's ground, desert varnish. Plants take none.
	fields = append(fields, rockFxParams(0)...)
	// How far they are drawn, and the trees' impostors (scatter_draw.go).
	fields = append(fields, drawRuleFields()...)
	return fields
}

func scatterParams() []procgen.ParamDef {
	return []procgen.ParamDef{
		{Name: "terrain", Type: procgen.ParamStage, StageKinds: TerrainSources, Default: procgen.StageRef(""),
			Doc: "The terrain stage to scatter over: its heights and masks."},
		{Name: "seed", Type: procgen.ParamSeed, Default: procgen.Seed(0), Doc: "Changes every rule's rocks without changing anything else."},
		{Name: "rules", Type: procgen.ParamList, MinItems: 1, Fields: scatterRuleFields(), Default: procgen.List(),
			Doc: "The rules, run in order: each keeps clear of what the earlier ones placed, so list the big rocks first."},
		{Name: "ground", Added: true, Type: procgen.ParamStage, StageKinds: GroundSources, Default: procgen.StageRef(""),
			Doc: "A terrain_material (or region_material) stage: the rules' rocks blend into its ground colour at their feet (ground_band) and match toward it (match_ground)."},
		{Name: "avoid", Type: procgen.ParamStage, StageKinds: []string{CliffWallKind, ScatterKind, RuinsKind}, Default: procgen.StageRef(""),
			Doc: "A cliff_wall (or another scatter) stage whose footprints these rocks keep clear of — or a ruins stage, whose terraces, plinths and stairs they keep off."},
		{Name: "cliff_threshold", Type: procgen.ParamFloat, Label: "Cliff threshold", Default: procgen.Float(0.5), Min: 0.05, Max: 1, Step: 0.01,
			Doc: "The cliff mask level that counts as a cliff for the cliff-base weight."},
		{Name: "margin", Type: procgen.ParamFloat, Label: "Edge margin (m)", Default: procgen.Float(2), Min: 0, Max: 1000, Step: 0.1,
			Doc: "Footprints keep this far in from the terrain's edge."},
		{Name: "preview_max", Type: procgen.ParamInt, Label: "Preview max", Default: procgen.Int(20000), Min: 100, Max: 1000000, PreviewOnly: true,
			Doc: "The most rocks a rule places in the preview (the run uses Max rocks)."},
		{Name: "preview_triangles", Type: procgen.ParamInt, Label: "Proxy triangles", Default: procgen.Int(240), Min: 12, Max: 20000, PreviewOnly: true,
			Doc: "The 3D preview draws each rule's rocks as one proxy of its previewed variant, decimated to this many triangles."},
	}
}

// scatterRules maps the rules onto place rules, variants from vars(ref)
// (nil for a disabled or unset rule: it is skipped). idx[k] is place rule
// k's index in the list.
func scatterRules(p procgen.Params, vars func(ref string) ([]place.Variant, error)) ([]place.Rule, []int, error) {
	var out []place.Rule
	var idx []int
	for i, it := range p.Items("rules") {
		if !it.Bool("enabled") || it.Str("rocks") == "" {
			continue
		}
		r := place.DefaultRule()
		r.Name = safeName(it.Str("name"), fmt.Sprintf("rule%d", i+1))
		v, err := vars(it.Str("rocks"))
		if err != nil {
			return nil, nil, fmt.Errorf("rule %d (%s): %w", i+1, r.Name, err)
		}
		r.Variants = lightVariants(sizedVariants(v, it.Float("min_size")), it.Int("max_triangles"))
		r.Density, r.Spacing = it.Float("density"), it.Float("spacing")
		r.Scale, r.ScaleBias = it.Vec2("scale"), it.Float("scale_bias")
		if r.Scale[1] < r.Scale[0] {
			r.Scale[0], r.Scale[1] = r.Scale[1], r.Scale[0]
		}
		bandRule(it, &r)
		r.Satellites, r.SatelliteSpread = it.Float("satellites"), it.Float("satellite_spread")
		r.Align, r.Tilt, r.Sink, r.MaxBury = it.Float("align"), it.Float("tilt"), it.Float("sink"), it.Float("max_bury")
		r.Collision, r.CollisionMin, r.HullMax = it.Str("collision"), it.Float("collision_min"), it.Float("hull_max")
		r.MaxInstances = it.Int("max_instances")
		out = append(out, r)
		idx = append(idx, i)
	}
	return out, idx, nil
}

func validateScatter(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	var out []string
	if p.Str("terrain") == "" {
		out = append(out, "Choose a terrain stage to scatter over")
	}
	for i, it := range p.Items("rules") {
		if it.Bool("enabled") && it.Str("rocks") == "" {
			out = append(out, fmt.Sprintf("rule %d (%s) names no rocks stage", i+1, it.Str("name")))
		}
		if b := it.Vec2("slope"); b[0] > b[1] {
			out = append(out, fmt.Sprintf("rule %d: its slope band runs backwards (%g..%g°)", i+1, b[0], b[1]))
		}
	}
	return out
}
