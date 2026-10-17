package stages

// paint_preset_river.go — RIVER BRUSHES: a brush preset whose stroke
// becomes a river spline rather than paint (docs/terrain-paint/PHASE_W3.md;
// the user's page is docs/guide/36-terrain-paint.md, "Rivers and streams").
//
//	name: River
//	river:
//	  depth: 1.4          # the thalweg below the water at every point
//	  width_scale: 1      # bank to bank = 2 × the brush radius × this
//	  material: water_river
//	  simplify: 0.35      # Douglas–Peucker tolerance, × the radius
//	  shape: {pools: 0.7, boulders: 1.2, floodplain: 4}   # any rivers field
//	recipe: {...}         # the dressing (gravel bed, sand bars, reeds, boulders)
//
// A STROKE (the editor's drag, or `foundry gen river`) is the brush
// centre's path. AddRiverStroke simplifies it (Douglas–Peucker at
// simplify × radius, so a wobbly hand gives a few clean points and a
// deliberate bend keeps its points), gives every point the width and depth,
// and appends a `rivers` item to the document's ground_edit — applying the
// preset's recipe first when there is no ground_edit, so the first stroke on
// a bare terrain sets everything up. The width is editable per point
// afterwards (the spline's width channel), the direction is not the
// author's concern (the carve turns it downhill), and a painted river never
// meanders unless its item says so.

import (
	"fmt"
	"math"

	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

// PresetRiver is a river brush's settings (file comment).
type PresetRiver struct {
	Depth      float64            `yaml:"depth"`
	WidthScale float64            `yaml:"width_scale,omitempty"`
	Material   string             `yaml:"material,omitempty"`
	Simplify   float64            `yaml:"simplify,omitempty"`
	Shape      map[string]float64 `yaml:"shape,omitempty"`
}

func (r *PresetRiver) validate() error {
	if r.Depth < 0 || r.WidthScale < 0 || r.Simplify < 0 {
		return fmt.Errorf("river: depth, width_scale and simplify must not be negative")
	}
	names := map[string]bool{}
	for _, f := range riverShapeFields() {
		names[f.Name] = true
	}
	for k := range r.Shape {
		if !names[k] {
			return fmt.Errorf("river: shape %q is not a river field", k)
		}
	}
	return nil
}

// NeedsRecipe reports whether applying p's recipe would add what its
// strokes need: a layer the document lacks, or — for a river or path brush
// — a ground_edit to hold the rivers (paths), or a stage of its dressing.
func NeedsRecipe(d *procgen.Doc, p *Preset) bool {
	if len(MissingLayers(d, p)) > 0 {
		return true
	}
	if p.River == nil && p.Path == nil {
		return false
	}
	if firstStageOf(d, GroundEditKind) == nil {
		return true
	}
	// The dressing: a stage of the recipe's the document lacks.
	stages, err := p.Recipe.stages()
	if err != nil {
		return false
	}
	for _, rs := range stages {
		if rs.Kind != GroundEditKind && d.StageByID(rs.ID) == nil {
			return true
		}
	}
	// A terrain-mask material entry the material stage lacks (phase P4:
	// each path style brings its own surface entry, so a second path
	// preset on a document the first set up still needs its recipe).
	if mat := firstStageOf(d, TerrainMaterialKind, RegionMaterialKind); mat != nil {
		for _, m := range p.Recipe.Material {
			if m.Mask != "" && !listHas(mat.Params["paint"].Items, "mask", m.Mask) {
				return true
			}
		}
	}
	return false
}

// RiverItem is the `rivers` item a stroke along pts (world x, z) with the
// brush at radius makes; ok false when the stroke is too short to be a
// river (under a radius long, or one point).
func (r *PresetRiver) RiverItem(pts [][2]float64, radius float64, name string) (procgen.Params, bool) {
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
		tol = 0.35
	}
	keep := simplifyStroke(pts, math.Max(tol*radius, 0.25))
	ws := r.WidthScale
	if ws <= 0 {
		ws = 1
	}
	depth := r.Depth
	if depth <= 0 {
		depth = 1
	}
	var path [][]float64
	for _, p := range keep {
		path = append(path, []float64{round2(p[0]), round2(p[1]), round2(2 * radius * ws), round2(depth)})
	}
	it := procgen.Params{"name": procgen.String(name), "path": procgen.Spline(path...)}
	if r.Material != "" {
		it["material"] = procgen.String(r.Material)
	}
	for k, v := range r.Shape {
		it[k] = procgen.Float(v)
	}
	return it, true
}

// simplifyStroke is the stroke's points kept by Douglas–Peucker at tol.
func simplifyStroke(pts [][2]float64, tol float64) [][2]float64 {
	idx := terrain.DouglasPeucker(pts, tol)
	out := make([][2]float64, len(idx))
	for i, k := range idx {
		out[i] = pts[k]
	}
	return out
}

// AddRiverStroke adds the river a stroke of river brush p made to d's
// ground_edit (file comment), applying p's recipe first when d has none.
// It returns the ground_edit's ID, the new item's index and the recipe's
// notes.
func AddRiverStroke(d *procgen.Doc, p *Preset, pts [][2]float64, radius float64) (string, int, []string, error) {
	if p.River == nil {
		return "", -1, nil, fmt.Errorf("preset %s is not a river brush", p.Label())
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
		return "", -1, notes, fmt.Errorf("no ground_edit stage to hold the river (the preset's recipe adds one over a terrain)")
	}
	cur, ok := ge.Params["rivers"]
	if !ok {
		cur = resolvedList(ge, "rivers")
	}
	taken := map[string]bool{}
	for _, it := range cur.Items {
		taken[it.Str("name")] = true
	}
	name := ""
	for n := len(cur.Items) + 1; ; n++ {
		name = fmt.Sprintf("%s_%d", safeName(p.ID, "river"), n)
		if !taken[name] {
			break
		}
	}
	it, ok := p.River.RiverItem(pts, radius, name)
	if !ok {
		return ge.ID, -1, notes, fmt.Errorf("the stroke is too short for a river (drag at least a brush radius)")
	}
	cur.Items = append(cur.Items, it)
	ge.Params["rivers"] = cur
	id, n := ge.ID, len(cur.Items)-1
	// The new item may name a stage the recipe appended below the
	// ground_edit (a path's rocks): keep the order consistent.
	OrderStagesByRefs(d)
	return id, n, notes, nil
}
