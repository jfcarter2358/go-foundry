package stages

// paint_preset_path.go — PATH BRUSHES: a brush preset whose stroke becomes
// a footpath spline rather than paint (docs/terrain-paint/PHASE_P4.md; the
// user's page is docs/guide/36-terrain-paint.md, "Paths").
//
//	name: Rock path
//	path:
//	  style: rock_path     # terrain.PathStyleNames
//	  width_scale: 1       # width = 2 × the brush radius × this
//	  simplify: 0.3        # Douglas–Peucker tolerance, × the radius
//	  flatten: true        # the item's flatten (default true)
//	  rocks: flag_rocks_src  # the item's own rocks stage (from the recipe)
//	  shape: {blend: 1.2, wear: 1.4}   # any other number field
//	recipe: {...}          # the dressing: stones, textures, wear, keep-clear
//
// A STROKE (the editor's drag, or `foundry gen paint -preset rock_path
// -line …`) is the brush centre's path, simplified as a river brush's is
// (paint_preset_river.go), every point as wide as the brush's diameter,
// appended as a `paths` item to the document's ground_edit — the preset's
// recipe applied first when the document lacks the ground_edit or the
// dressing, so the first stroke on a bare terrain sets everything up. The
// width is editable per point afterwards (the spline's width channel), as
// are the style and flatten in the paths panel.

import (
	"fmt"
	"math"

	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

// PresetPath is a path brush's settings (file comment).
type PresetPath struct {
	Style      string             `yaml:"style"`
	WidthScale float64            `yaml:"width_scale,omitempty"`
	Simplify   float64            `yaml:"simplify,omitempty"`
	Flatten    *bool              `yaml:"flatten,omitempty"`
	// Rocks is the item's own rocks stage (a recipe stage's ID): a rock
	// path's flat flagstones.
	Rocks string             `yaml:"rocks,omitempty"`
	Shape map[string]float64 `yaml:"shape,omitempty"`
}

func (r *PresetPath) validate() error {
	if _, ok := terrain.PathStyles[r.Style]; !ok {
		return fmt.Errorf("path: style %q is not one of %v", r.Style, terrain.PathStyleNames)
	}
	if r.WidthScale < 0 || r.Simplify < 0 {
		return fmt.Errorf("path: width_scale and simplify must not be negative")
	}
	names := map[string]bool{}
	for _, f := range pathParams()[0].Fields {
		if f.Type == procgen.ParamFloat {
			names[f.Name] = true
		}
	}
	for k := range r.Shape {
		if !names[k] {
			return fmt.Errorf("path: shape %q is not a number field of a path", k)
		}
	}
	return nil
}

// PathItem is the `paths` item a stroke along pts (world x, z) with the
// brush at radius makes; ok false when the stroke is too short (under a
// radius long, or one point).
func (r *PresetPath) PathItem(pts [][2]float64, radius float64, name string) (procgen.Params, bool) {
	if len(pts) < 2 || radius <= 0 {
		return nil, false
	}
	l := 0.0
	for i := 1; i < len(pts); i++ {
		l += math.Hypot(pts[i][0]-pts[i-1][0], pts[i][1]-pts[i-1][1])
	}
	if l < radius {
		return nil, false
	}
	tol := r.Simplify
	if tol <= 0 {
		tol = 0.3
	}
	keep := simplifyStroke(pts, math.Max(tol*radius, 0.2))
	ws := r.WidthScale
	if ws <= 0 {
		ws = 1
	}
	var path [][]float64
	for _, p := range keep {
		path = append(path, []float64{round2(p[0]), round2(p[1]), round2(2 * radius * ws)})
	}
	it := procgen.Params{"name": procgen.String(name), "style": procgen.Enum(r.Style), "path": procgen.Spline(path...)}
	if r.Flatten != nil {
		it["flatten"] = procgen.Bool(*r.Flatten)
	}
	if r.Rocks != "" {
		it["rocks"] = procgen.StageRef(r.Rocks)
	}
	for k, v := range r.Shape {
		it[k] = procgen.Float(v)
	}
	return it, true
}

// AddPathStroke adds the path a stroke of path brush p made to d's
// ground_edit (file comment), applying p's recipe first when needed. It
// returns the ground_edit's ID, the new item's index and the recipe's
// notes.
func AddPathStroke(d *procgen.Doc, p *Preset, pts [][2]float64, radius float64) (string, int, []string, error) {
	if p.Path == nil {
		return "", -1, nil, fmt.Errorf("preset %s is not a path brush", p.Label())
	}
	var notes []string
	if NeedsRecipe(d, p) {
		n, err := ApplyRecipe(d, p)
		if err != nil {
			return "", -1, n, err
		}
		notes = n
	}
	ge := firstStageOf(d, GroundEditKind)
	if ge == nil {
		return "", -1, notes, fmt.Errorf("no ground_edit stage to hold the path (the preset's recipe adds one over a terrain)")
	}
	cur, ok := ge.Params["paths"]
	if !ok {
		cur = resolvedList(ge, "paths")
	}
	taken := map[string]bool{}
	for _, it := range cur.Items {
		taken[it.Str("name")] = true
	}
	name := ""
	for n := len(cur.Items) + 1; ; n++ {
		name = fmt.Sprintf("%s_%d", safeName(p.ID, "path"), n)
		if !taken[name] {
			break
		}
	}
	it, ok := p.Path.PathItem(pts, radius, name)
	if !ok {
		return ge.ID, -1, notes, fmt.Errorf("the stroke is too short for a path (drag at least a brush radius)")
	}
	cur.Items = append(cur.Items, it)
	ge.Params["paths"] = cur
	id, n := ge.ID, len(cur.Items)-1
	// The new item may name a stage the recipe appended below the
	// ground_edit (a path's rocks): keep the order consistent.
	OrderStagesByRefs(d)
	return id, n, notes, nil
}
