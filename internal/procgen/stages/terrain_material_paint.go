package stages

// terrain_material_paint.go — paint in the ground materials: the `paint`
// list of terrain_material and region_material
// (docs/guide/36-terrain-paint.md).
//
// Each item names a paint layer and a TARGET — what it acts on:
//
//   - in terrain_material, a role: sand, cracked, gravel or rock (and wet,
//     ash or snow when the stage has covers);
//   - in region_material, a role (every environment's layer of that role)
//     or one layer by its Poly Haven id or its albedo texture's name.
//
// and a MODE:
//
//   - override (the default, for a layer of kind material): the painted
//     weight a = strength × band(paint) moves the sample's splat toward the
//     target, w ← w·(1 − a) + a·target. A trail painted at full strength is
//     all gravel; at half it is half gravel over whatever the land said.
//     A role target shares a among its layers by their weights there (the
//     savannah's gravel on savannah ground, the desert's on desert), or
//     gives it all to the first when none has weight;
//   - band: the target's weight is multiplied by 1 − strength·(1 −
//     band(paint)) and the sample renormalised, so the target grows only
//     where painted (outside, its share goes to the other layers by their
//     weights). A sample the band would leave with no weight at all keeps
//     what it had.
//
// band(v) is the placement stages' paint ramp (paint_bands.go): (lo, hi),
// optionally inverted. Items apply in list order, after the stage's own
// rules and covers and before anything is quantised or averaged, so the
// ground colour map, the coverage and every per-tile material agree with
// the painted splat. The far backdrop is not painted: paint is for the
// ground a player walks on.
//
// TERRAIN MASKS (phase W3). An item may name a terrain mask (`mask`)
// instead of a paint layer: the value is then the terrain's own mask at the
// sample — a ground_edit's river_bed under a river (gravel on the bed),
// river_bar on the inside of its bends (sand bars), or any other mask the
// terrain writes. Everything else (band, invert, strength, mode) is the
// same. Empty (the default, Added) changes nothing.
//
// An empty list (the default, Added) changes nothing and hashes as before.

import (
	"fmt"
	"strings"

	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
	"foundry/internal/procgen/terrain"
)

// splatPaintParam is the material stages' paint list. targets says what a
// target may name, for the doc line.
func splatPaintParam(targets string) procgen.ParamDef {
	return procgen.ParamDef{Name: "paint", Added: true, Type: procgen.ParamList, Label: "Paint", Default: procgen.List(),
		Fields: []procgen.ParamDef{
			{Name: "layer", Type: procgen.ParamMask, Default: procgen.Mask(""),
				Doc: "The paint layer: usually of kind material for an override, density for a band."},
			{Name: "target", Type: procgen.ParamString, Default: procgen.String("gravel"),
				Doc: "What it paints: " + targets + "."},
			{Name: "mode", Type: procgen.ParamEnum, Default: procgen.Enum("override"), Choices: []string{"override", "band"},
				Doc: "override: painted weight forces the target (a trail). band: the target grows only where painted."},
			{Name: "band", Type: procgen.ParamVec2, Default: procgen.Vec2(0, 1), Min: 0, Max: 1, Step: 0.01,
				Doc: "How the painted value becomes weight: 0 at or below the first, 1 at or above the second, linear between."},
			{Name: "invert", Type: procgen.ParamBool, Default: procgen.Bool(false),
				Doc: "Use 1 − the band: everywhere except where painted."},
			{Name: "strength", Type: procgen.ParamFloat, Default: procgen.Float(1), Min: 0, Max: 1, Step: 0.01,
				Doc: "How far it goes: 1 is all the way to the target where fully painted."},
			{Name: "mask", Added: true, Type: procgen.ParamString, Label: "Terrain mask", Default: procgen.String(""),
				Doc: "A terrain mask to read instead of a paint layer: river_bed (a ground_edit's river channels), river_bar " +
					"(the sand bars on the inside of their bends), flow, deposition... Empty: the paint layer."},
		},
		Doc: "Paint layers that change the splat: an override forces a target layer where painted (a gravel trail), a band keeps a target to the painted ground."}
}

// splatPaintRule is one paint item resolved against a stage's weight
// layout.
type splatPaintRule struct {
	layer *paint.Layer
	// mask is a terrain mask read instead of the layer (grid-aligned).
	mask     []float32
	slots    []int
	override bool
	band     [2]float64
	invert   bool
	strength float64
}

// splatPaintRules resolves p's paint items; slots maps a target name to
// the weight slots it names (nil: unknown). Items naming no layer, an
// empty layer or no strength are dropped; an unknown target is an error.
func splatPaintRules(env *procgen.Env, p procgen.Params, slots func(target string) []int, tr []*terrain.Terrain) ([]splatPaintRule, error) {
	var out []splatPaintRule
	for i, it := range p.Items("paint") {
		name := it.Str("layer")
		if mname := strings.TrimSpace(it.Str("mask")); mname != "" && it.Float("strength") > 0 {
			// A terrain mask (file comment): a terrain without it is
			// skipped, as an empty layer is.
			target := strings.TrimSpace(it.Str("target"))
			s := slots(target)
			if len(s) == 0 {
				return nil, fmt.Errorf("paint %d (mask %s): target %q is not one of this stage's layers", i+1, mname, target)
			}
			if len(tr) == 0 || tr[0] == nil {
				continue
			}
			m, err := tr[0].Mask(mname)
			if err != nil || m == nil {
				continue
			}
			out = append(out, splatPaintRule{mask: m, slots: s, override: it.Str("mode") == "override",
				band: it.Vec2("band"), invert: it.Bool("invert"), strength: it.Float("strength")})
			continue
		}
		if name == "" || it.Float("strength") <= 0 {
			continue
		}
		target := strings.TrimSpace(it.Str("target"))
		s := slots(target)
		if len(s) == 0 {
			return nil, fmt.Errorf("paint %d (%s): target %q is not one of this stage's layers", i+1, name, target)
		}
		l := env.PaintLayer(name)
		if l.Empty() {
			continue
		}
		out = append(out, splatPaintRule{layer: l, slots: s, override: it.Str("mode") == "override",
			band: it.Vec2("band"), invert: it.Bool("invert"), strength: it.Float("strength")})
	}
	return out, nil
}

// applySplatPaint applies the rules to w, nc weights per sample of grid g
// (row-major, as the material stages lay them out).
func applySplatPaint(g *terrain.Grid, w []float32, nc int, rules []splatPaintRule, pool terrain.Pool) error {
	if len(rules) == 0 {
		return nil
	}
	sz := g.S()
	return pool.Parallel(sz, func(j int) error {
		old := make([]float32, nc)
		z := g.Z(j)
		for i := 0; i < sz; i++ {
			x := g.X(i)
			ws := w[(j*sz+i)*nc : (j*sz+i+1)*nc]
			for r := range rules {
				rule := &rules[r]
				var v float64
				if rule.mask != nil {
					if k := j*sz + i; k < len(rule.mask) {
						v = float64(rule.mask[k])
					}
				} else {
					v = rule.layer.Sample(x, z)
				}
				f := paintRamp(v, rule.band, rule.invert)
				if rule.override {
					a := float32(rule.strength * f)
					if a <= 0 {
						continue
					}
					// The target's share of a, by its slots' weights before
					// the scale (all to the first when none has any).
					var sum float32
					for _, s := range rule.slots {
						sum += ws[s]
					}
					copy(old, ws)
					for c := range ws {
						ws[c] *= 1 - a
					}
					for k, s := range rule.slots {
						switch {
						case sum > 1e-6:
							ws[s] += a * old[s] / sum
						case k == 0:
							ws[s] += a
						}
					}
					continue
				}
				m := float32(1 - rule.strength*(1-f))
				if m >= 1 {
					continue
				}
				copy(old, ws)
				for _, s := range rule.slots {
					ws[s] *= m
				}
				var total float32
				for _, v := range ws {
					total += v
				}
				var before float32
				for _, v := range old {
					before += v
				}
				if total <= 1e-6 {
					copy(ws, old)
					continue
				}
				for c := range ws {
					ws[c] *= before / total
				}
			}
		}
		return nil
	})
}

// terrainMaterialPaint applies terrain_material's paint list to its
// weights (roles in groundRoleKeys order, nc of them a sample).
func terrainMaterialPaint(env *procgen.Env, p procgen.Params, g *terrain.Grid, w []float32, nc int, tr ...*terrain.Terrain) error {
	rules, err := splatPaintRules(env, p, func(target string) []int {
		for i, k := range groundRoleKeys[:nc] {
			if k == target {
				return []int{i}
			}
		}
		return nil
	}, tr)
	if err != nil {
		return err
	}
	return applySplatPaint(g, w, nc, rules, env)
}

// regionMaterialPaint applies region_material's paint list to the union
// weights (one slot per global layer).
func regionMaterialPaint(env *procgen.Env, p procgen.Params, g *terrain.Grid, w []float32, layers []*regionLayer, tr ...*terrain.Terrain) error {
	rules, err := splatPaintRules(env, p, func(target string) []int {
		var out []int
		for i, l := range layers {
			if l.layer != nil && (l.layer.ID == target || l.layer.Albedo == target) {
				return []int{i}
			}
			if splatRoleNames[l.role] == target {
				out = append(out, i)
			}
		}
		return out
	}, tr)
	if err != nil {
		return err
	}
	return applySplatPaint(g, w, len(layers), rules, env)
}
