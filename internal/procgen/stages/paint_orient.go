package stages

// paint_orient.go — ORIENTED PLACEMENTS: a scatter or grass_field rule's
// `orient_to` (docs/terrain-paint/PHASE_P4.md; the user's page is
// docs/guide/36-terrain-paint.md, "Direction").
//
// A rule normally turns every rock or clump to a random yaw. With
// `orient_to` it turns them along a DIRECTION FIELD instead — fallen
// branches lying one way, stones along a path, logs that slid down a slope
// — jittered by up to `orient_jitter` degrees and turned by `orient_offset`
// (for a model whose length is not where the rule expects). The field is
// one of:
//
//   - a DIRECTION LAYER (a paint layer of kind direction, painted by hand:
//     the drag's direction is the stamp's direction), with the PATHS laid
//     over it — where a ground_edit's path lies, its tangent wins in
//     proportion to the path's strength there (path_dir_x / path_dir_z: 1
//     on the path, easing out over its wear band). So paths "write" the
//     direction a rule reads without the stage writing anyone's paint:
//     paint stays an input;
//   - `paths`: the paths' tangents alone (no layer needed);
//   - `downhill`: down the terrain's slope, full strength from a 6% grade
//     (nothing on the flat).
//
// The field's strength blends the yaw back toward the random one where it
// is weak, so an oriented patch fades into a random one. Both senses of a
// direction are the same thing (a log has no head), and half the
// placements are turned end for end. A scatter rock's long side follows the
// field (whichever of its footprint's X and Z is longer); a grass clump's
// length is taken to lie along its model Z, and a kind whose X is clearly
// longer than its Z is given a quarter turn (KindYaw).
//
// The randomness is each placement's own random yaw, so positions, kinds,
// scales and tints are unchanged, and a rule without `orient_to` (the
// default, Added) places byte for byte as before. A layer named by
// `orient_to` is a mask param, so its paint keys the stage's pieces like
// any paint band; `downhill` and `paths` are mask words (no layer).

import (
	"math"

	"foundry/internal/procgen"
	"foundry/internal/procgen/place"
	"foundry/internal/procgen/terrain"
)

// The orient_to words that name no layer.
const (
	OrientDownhill = "downhill"
	OrientPaths    = "paths"
)

// orientRuleFields are a scatter or grass rule's orientation params.
func orientRuleFields() []procgen.ParamDef {
	f := procgen.Float
	return []procgen.ParamDef{
		{Name: "orient_to", Added: true, Type: procgen.ParamMask, Label: "Orient to", Default: procgen.Mask(""),
			MaskWords: []string{OrientDownhill, OrientPaths},
			Doc: "Turn each placement along a direction field instead of at random: a direction paint layer (with the " +
				"ground_edit's paths laid over it), `paths` (the paths' tangents alone) or `downhill`. Empty: random yaws."},
		{Name: "orient_jitter", Added: true, Type: procgen.ParamFloat, Label: "Orient jitter (°)", Default: f(12), Min: 0, Max: 180, Step: 0.5,
			Doc: "How far each placement may turn off the field, degrees either way."},
		{Name: "orient_offset", Added: true, Type: procgen.ParamFloat, Label: "Orient offset (°)", Default: f(0), Min: -180, Max: 180, Step: 1,
			Doc: "A fixed turn on top: 90 lays a piece across the field rather than along it."},
	}
}

// orientField is rule item it's direction field, or nil without
// orient_to (file comment). t is the terrain the stage stands on (its
// path masks and slope); g the placement ground (its gradient).
func orientField(env *procgen.Env, t *terrain.Terrain, g *place.Ground, it procgen.Params) func(x, z float64) (float64, float64, float64) {
	to := it.Str("orient_to")
	if to == "" {
		return nil
	}
	if to == OrientDownhill {
		if g == nil {
			return nil
		}
		return func(x, z float64) (float64, float64, float64) {
			gx, gz := g.Gradient(x, z)
			l := math.Hypot(gx, gz)
			if l < 1e-6 {
				return 0, 0, 0
			}
			return -gx / l, -gz / l, math.Min(l/0.06, 1)
		}
	}
	px := terrainMaskSampler(t, terrain.MaskPathDirX)
	pz := terrainMaskSampler(t, terrain.MaskPathDirZ)
	path := func(x, z float64) (float64, float64) {
		if px == nil || pz == nil {
			return 0, 0
		}
		return 2*px(x, z) - 1, 2*pz(x, z) - 1
	}
	var layer func(x, z float64) [2]float64
	if to != OrientPaths {
		if l := env.PaintLayer(to); l != nil {
			layer = l.Direction
		}
	}
	return func(x, z float64) (float64, float64, float64) {
		vx, vz := path(x, z)
		if layer != nil {
			// The path wins in proportion to its strength there.
			s := math.Min(math.Hypot(vx, vz), 1)
			d := layer(x, z)
			// A painted vector's sign is arbitrary against the path's (both
			// senses are the same): align it before blending.
			if vx*d[0]+vz*d[1] < 0 {
				d = [2]float64{-d[0], -d[1]}
			}
			vx, vz = vx+(1-s)*d[0], vz+(1-s)*d[1]
		}
		l := math.Hypot(vx, vz)
		if l < 1e-6 {
			return 0, 0, 0
		}
		return vx / l, vz / l, math.Min(l, 1)
	}
}

// orientScatterRules sets each scatter rule's direction field from its
// item (idx maps rule k to its item, as scatterRules returns them).
func orientScatterRules(env *procgen.Env, t *terrain.Terrain, g *place.Ground, p procgen.Params, rules []place.Rule, idx []int) {
	items := p.Items("rules")
	for k := range rules {
		it := items[idx[k]]
		if f := orientField(env, t, g, it); f != nil {
			rules[k].Orient = f
			rules[k].OrientJitter = it.Float("orient_jitter") * math.Pi / 180
			rules[k].OrientOffset = it.Float("orient_offset") * math.Pi / 180
		}
	}
}

// orientGrassRules is orientScatterRules for grass rules; kindLong, when
// set, reports whether a kit kind's length lies along its X (a quarter
// turn: KindYaw).
func orientGrassRules(env *procgen.Env, t *terrain.Terrain, g *place.Ground, p procgen.Params, rules []place.GrassRule, idx []int,
	kindLong func(kind int) bool) {
	items := p.Items("rules")
	for k := range rules {
		it := items[idx[k]]
		f := orientField(env, t, g, it)
		if f == nil {
			continue
		}
		rules[k].Orient = f
		rules[k].OrientJitter = it.Float("orient_jitter") * math.Pi / 180
		rules[k].OrientOffset = it.Float("orient_offset") * math.Pi / 180
		if kindLong != nil {
			ky := make([]float64, len(rules[k].Kinds))
			any := false
			for i, kd := range rules[k].Kinds {
				if kindLong(kd) {
					ky[i], any = math.Pi/2, true
				}
			}
			if any {
				rules[k].KindYaw = ky
			}
		}
	}
}
