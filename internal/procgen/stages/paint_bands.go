package stages

// paint_bands.go — what the placement stages read from the document's paint
// layers (docs/guide/36-terrain-paint.md; the layers themselves are
// procgen/paint and procgen's paint_doc.go).
//
// Two inputs, both OFF by default and Added, so a rule or a stage that does
// not use them hashes and places exactly as before:
//
//   - a PAINT BAND on a scatter or grass_field rule: `paint` names a layer,
//     `paint_band` (lo, hi) ramps what is painted into the rule's weight
//     (paint at or below lo counts 0, at or above hi counts 1, linear
//     between; the default (0, 1) is the painted value itself), and
//     `paint_invert` flips it (grow everywhere except where painted). It
//     multiplies the rule's other bands, so a painted meadow still keeps
//     off cliffs and out of water;
//   - a stage-level EXCLUDE list on scatter and grass_field: each listed
//     layer multiplies every rule of the stage by (1 − painted value) — a
//     path kept clear of rocks and grass alike. Phase P4: an entry may
//     name a terrain `mask` instead (a ground_edit's path_clear or
//     path_wear), with a `strength` (1 − strength × value) and, for grass,
//     a `shrink` (shorter clumps by shrink × value: a path's wear band).
//
// Both become place.Rule.Mask, one function per rule; nil when the rule
// reads no paint. The material stages' paint (overrides and role bands)
// is terrain_material_paint.go.

import (
	"math"

	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
	"foundry/internal/procgen/place"
	"foundry/internal/procgen/terrain"
)

// paintRuleFields are a rule's paint band params.
func paintRuleFields() []procgen.ParamDef {
	return []procgen.ParamDef{
		{Name: "paint", Added: true, Type: procgen.ParamMask, Label: "Paint layer", Default: procgen.Mask(""),
			Doc: "A paint layer (the document's `paint:` list, painted in the 2D view) whose painted value multiplies the rule's weight: " +
				"a meadow painted where it should be dense. Empty: no paint band."},
		{Name: "paint_band", Added: true, Type: procgen.ParamVec2, Label: "Paint band", Default: procgen.Vec2(0, 1), Min: 0, Max: 1, Step: 0.01,
			Doc: "How the painted value becomes weight: at or below the first it counts 0, at or above the second 1, linear between. " +
				"(0, 1) is the painted value itself; (0.1, 0.4) makes a light stroke already full density."},
		{Name: "paint_invert", Added: true, Type: procgen.ParamBool, Label: "Paint invert", Default: procgen.Bool(false),
			Doc: "Grow everywhere EXCEPT where painted (weight × (1 − band))."},
	}
}

// paintExcludeParam is a stage's exclude list.
func paintExcludeParam() procgen.ParamDef {
	return procgen.ParamDef{Name: "exclude", Added: true, Type: procgen.ParamList, Label: "Exclude paint", Default: procgen.List(),
		Fields: []procgen.ParamDef{
			{Name: "layer", Type: procgen.ParamMask, Default: procgen.Mask(""),
				Doc: "A paint layer (usually of kind exclude) that keeps every rule of this stage clear where it is painted."},
			// Phase P4 (docs/terrain-paint/PHASE_P4.md): a terrain mask
			// instead of a layer, a strength, and a shrink.
			{Name: "mask", Added: true, Type: procgen.ParamString, Label: "Terrain mask", Default: procgen.String(""),
				Doc: "A terrain mask to read instead of a paint layer: a ground_edit's path_clear (keep large rocks off its " +
					"paths), path_wear (thin grass toward a path's centreline), river_bed... A mask the terrain lacks reads 0."},
			{Name: "strength", Added: true, Type: procgen.ParamFloat, Default: procgen.Float(1), Min: 0, Max: 1, Step: 0.01,
				Doc: "How strongly it keeps clear: the weight is multiplied by 1 − strength × value."},
			{Name: "shrink", Added: true, Type: procgen.ParamFloat, Default: procgen.Float(0), Min: 0, Max: 1, Step: 0.01,
				Doc: "grass_field only: clumps are also shorter by this share of the value (a path's wear band: thinner and shorter grass)."},
		},
		Doc: "Paint layers (or terrain masks) that keep this stage's every rule clear: each multiplies the weight by " +
			"(1 − strength × value)."}
}

// terrainMaskSampler is terrain t's mask name sampled bilinearly at
// (x, z), or nil when t is nil or has no such mask.
func terrainMaskSampler(t *terrain.Terrain, name string) func(x, z float64) float64 {
	if t == nil || t.Grid == nil || name == "" {
		return nil
	}
	m, err := t.Mask(name)
	if err != nil || len(m) != len(t.Grid.H) {
		return nil
	}
	mg := *t.Grid
	mg.H = m
	return func(x, z float64) float64 { return float64(mg.Sample(x, z)) }
}

// firstTerrain is ts[0], or nil (the variadic terrain the mask helpers
// take, so the P1 test calls are unchanged).
func firstTerrain(ts []*terrain.Terrain) *terrain.Terrain {
	if len(ts) == 0 {
		return nil
	}
	return ts[0]
}

// paintRamp is the band's weight for painted value v.
func paintRamp(v float64, band [2]float64, invert bool) float64 {
	lo, hi := band[0], band[1]
	var f float64
	switch {
	case hi > lo:
		f = math.Max(0, math.Min(1, (v-lo)/(hi-lo)))
	case v > lo: // a band of no width is a step at lo
		f = 1
	}
	if invert {
		return 1 - f
	}
	return f
}

// paintRuleMask is the paint mask of rule item it in a stage with params p
// (its exclude list: paint layers, and — phase P4 — the terrain's masks
// when ts names it), or nil when it reads no paint.
func paintRuleMask(env *procgen.Env, p, it procgen.Params, ts ...*terrain.Terrain) func(x, z float64) float64 {
	type excluder struct {
		at       func(x, z float64) float64
		strength float64
	}
	var excl []excluder
	for _, e := range p.Items("exclude") {
		st := e.Float("strength")
		if n := e.Str("layer"); n != "" {
			excl = append(excl, excluder{env.PaintLayer(n).Sample, st})
		} else if f := terrainMaskSampler(firstTerrain(ts), e.Str("mask")); f != nil {
			excl = append(excl, excluder{f, st})
		}
	}
	name := it.Str("paint")
	if name == "" && len(excl) == 0 {
		return nil
	}
	var band *paint.Layer
	if name != "" {
		band = env.PaintLayer(name)
	}
	rng, inv := it.Vec2("paint_band"), it.Bool("paint_invert")
	return func(x, z float64) float64 {
		w := 1.0
		if name != "" {
			if w = paintRamp(band.Sample(x, z), rng, inv); w <= 0 {
				return 0
			}
		}
		for _, e := range excl {
			if w *= 1 - e.strength*math.Max(0, math.Min(1, e.at(x, z))); w <= 0 {
				return 0
			}
		}
		return w
	}
}

// paintRuleShrink is a grass stage's exclude shrink (the product of
// 1 − shrink × value over its entries that set one), or nil when none does.
func paintRuleShrink(env *procgen.Env, p procgen.Params, ts ...*terrain.Terrain) func(x, z float64) float64 {
	type shrinker struct {
		at func(x, z float64) float64
		k  float64
	}
	var sh []shrinker
	for _, e := range p.Items("exclude") {
		k := e.Float("shrink")
		if k <= 0 {
			continue
		}
		if n := e.Str("layer"); n != "" {
			sh = append(sh, shrinker{env.PaintLayer(n).Sample, k})
		} else if f := terrainMaskSampler(firstTerrain(ts), e.Str("mask")); f != nil {
			sh = append(sh, shrinker{f, k})
		}
	}
	if len(sh) == 0 {
		return nil
	}
	return func(x, z float64) float64 {
		f := 1.0
		for _, s := range sh {
			f *= 1 - s.k*math.Max(0, math.Min(1, s.at(x, z)))
		}
		return math.Max(f, 0.05)
	}
}

// maskScatterRules sets each place rule's Mask from its item (idx maps
// rule k to its item, as scatterRules returns them).
func maskScatterRules(env *procgen.Env, p procgen.Params, rules []place.Rule, idx []int, ts ...*terrain.Terrain) {
	items := p.Items("rules")
	for k := range rules {
		rules[k].Mask = paintRuleMask(env, p, items[idx[k]], ts...)
	}
}

// maskGrassRules is maskScatterRules for grass rules (their band rule).
func maskGrassRules(env *procgen.Env, p procgen.Params, rules []place.GrassRule, idx []int, ts ...*terrain.Terrain) {
	items := p.Items("rules")
	shrink := paintRuleShrink(env, p, ts...)
	for k := range rules {
		rules[k].Band.Mask = paintRuleMask(env, p, items[idx[k]], ts...)
		rules[k].Shrink = shrink
	}
}
