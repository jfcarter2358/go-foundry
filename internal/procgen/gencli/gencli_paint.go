package gencli

// gencli_paint.go — `foundry gen paint`: brush strokes from the command
// line, for tests, scripts and a headless check of a painted generator
// (docs/guide/36-terrain-paint.md).
//
//	foundry gen paint meadow -layer lush -disc 10,-4,12
//	foundry gen paint meadow -layer path -kind exclude -line -40,0,40,0,3
//	foundry gen paint meadow -layer lush -mode erase -strength 0.5 -disc 10,-4,6
//	foundry gen paint meadow -layer lush -clear
//	foundry gen paint meadow -list
//	foundry gen paint meadow -preset forest_floor -disc 0,0,15
//	foundry gen paint -presets
//
// Each -disc x,z,r stamps once; each -line x0,z0,x1,z1,r stamps along the
// segment as the editor's drag does (a quarter radius apart). They run in
// the order given, all with the same -mode, -strength, -falloff, -value
// and -dir. A layer the document does not declare is declared with -kind
// (density by default) and -res. The document and its changed tiles are
// written back, exactly as the editor's Save writes them; a run then
// reruns the stages that read the layer.
//
// A RIVER brush (-preset river, -preset stream: phase W3) paints no layer:
// the -disc centres (x,z,r) and -line ends (x0,z0,x1,z1,r), in order, are
// the stroke's path, the last radius its brush radius, and the river is
// added to the document's ground_edit (stages.AddRiverStroke, the recipe
// applied first when the document lacks its ground_edit or dressing):
//
//	foundry gen paint valley -preset river -line -100,20,-20,0,3 -line -20,0,80,-10,3
//
// A PATH brush (-preset rock_path, stepping_stones, dirt_trail,
// gravel_path, game_trail: phase P4) is the same: the stroke becomes a
// `paths` item in the ground_edit (stages.AddPathStroke), its width the
// brush's diameter times the preset's width_scale.
//
// -preset paints a brush preset (resources/paint/<name>.fpb; procgen/stages
// paint_preset.go) instead of one layer: every stamp writes each of the
// preset's layers at -strength times the layer's own. When the document
// lacks the preset's layers its recipe is applied first (the layers, the
// stages and rules that read them, the splat entries) and what it added is
// printed; -no-recipe refuses instead. -presets lists the presets.

import (
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
	"foundry/internal/procgen/stages"
)

// numList is a repeatable flag of comma-separated numbers.
type numList [][]float64

func (n *numList) String() string { return fmt.Sprint(*n) }
func (n *numList) Set(v string) error {
	var out []float64
	for _, f := range strings.Split(v, ",") {
		x, err := strconv.ParseFloat(strings.TrimSpace(f), 64)
		if err != nil {
			return fmt.Errorf("%q: %w", v, err)
		}
		out = append(out, x)
	}
	*n = append(*n, out)
	return nil
}

func paintMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("gen paint", flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	layer := fs.String("layer", "", "the paint layer to paint")
	kind := fs.String("kind", paint.KindDensity, "a new layer's kind: "+strings.Join(paint.Kinds, ", "))
	res := fs.Float64("res", paint.DefaultResolution, "a new layer's resolution, metres per texel")
	mode := fs.String("mode", "add", "add, erase, smooth, set or flatten (a height layer toward -value metres of ground)")
	strength := fs.Float64("strength", 1, "0..1 (metres for a height layer's add)")
	falloff := fs.Float64("falloff", 0, "the outer fraction of the radius the brush eases out over")
	curve := fs.String("curve", "smooth", "the falloff's curve: "+strings.Join(paint.CurveNames, ", "))
	value := fs.Float64("value", 1, "what -mode set paints toward; -mode flatten's ground height (m)")
	dir := fs.String("dir", "1,0", "a direction layer's vector, x,z")
	clear := fs.Bool("clear", false, "erase the whole layer first")
	list := fs.Bool("list", false, "list the document's layers and their tiles, and paint nothing")
	presetName := fs.String("preset", "", "paint a brush preset (resources/paint/<name>.fpb) instead of -layer")
	noRecipe := fs.Bool("no-recipe", false, "with -preset: fail rather than apply the preset's recipe to a document lacking its layers")
	listPresets := fs.Bool("presets", false, "list the brush presets and paint nothing")
	var discs, lines numList
	fs.Var(&discs, "disc", "stamp a disc: x,z,radius (repeatable)")
	fs.Var(&lines, "line", "stamp along a segment: x0,z0,x1,z1,radius (repeatable)")
	rest, code, ok := parse(fs, args)
	if !ok {
		return code
	}
	if *listPresets {
		fileutil.ResourcesPath = fileutil.ResourcesDir(*project)
		ps, errs := stages.LoadPresets(stages.PresetsDir())
		for _, e := range errs {
			fmt.Fprintln(out, e)
		}
		for _, pr := range ps {
			var ls []string
			for _, l := range pr.Layers {
				ls = append(ls, fmt.Sprintf("%s %.2g", l.Layer, l.Strength))
			}
			fmt.Fprintf(out, "%-18s %-20s %s\n", pr.ID, pr.Label(), strings.Join(ls, ", "))
		}
		return 0
	}
	if len(rest) != 1 {
		usage(out)
		return 2
	}
	fileutil.ResourcesPath = fileutil.ResourcesDir(*project)
	path := resolveDoc(rest[0])
	d, err := procgen.ReadDoc(path)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if *list {
		for _, def := range d.Paint {
			l := d.PaintLayer(def.Name)
			fmt.Fprintf(out, "%s  %s  %g m/texel  %d tiles\n", def.Name, def.Kind, def.Res(), len(l.Keys()))
		}
		return 0
	}
	m, ok := paint.ParseMode(*mode)
	if !ok {
		fmt.Fprintf(out, "-mode %q: want one of %s\n", *mode, strings.Join(paint.ModeNames, ", "))
		return 2
	}
	var dv numList
	if err := dv.Set(*dir); err != nil || len(dv[0]) != 2 {
		fmt.Fprintf(out, "-dir %q: want x,z\n", *dir)
		return 2
	}
	st := paint.Stroke{Strength: *strength, Falloff: *falloff, Mode: m, Value: *value, Dir: [2]float64{dv[0][0], dv[0][1]}}
	if *presetName != "" {
		return paintPreset(out, path, d, *presetName, *noRecipe, st, *curve, *clear, discs, lines)
	}
	def := paint.LayerDef{Name: *layer, Kind: *kind, Resolution: *res}
	if have := d.PaintDef(*layer); have != nil {
		def = *have
	}
	if err := def.Validate(); err != nil {
		fmt.Fprintln(out, err)
		return 2
	}
	l := d.EnsurePaintLayer(def)
	if *clear {
		for _, k := range l.Keys() {
			l.SetTile(k, nil)
		}
	}
	cv, ok := paint.ParseCurve(*curve)
	if !ok {
		fmt.Fprintf(out, "-curve %q: want one of %s\n", *curve, strings.Join(paint.CurveNames, ", "))
		return 2
	}
	st.Curve = cv
	if m == paint.ModeFlatten && def.Kind == paint.KindHeight {
		// Flatten levels the GROUND: the ground_edit's source terrain (and
		// its other height layers) from the last run.
		base, _, err := stages.HeightLayerBase(d, procgen.OutDirFor(d, procgen.Options{}), def.Name)
		if err != nil {
			fmt.Fprintf(out, "flatten: %v; painting the layer toward %g m instead\n", err, *value)
		} else {
			st.Base = base
		}
	}
	for _, c := range discs {
		if len(c) != 3 {
			fmt.Fprintf(out, "-disc %v: want x,z,radius\n", c)
			return 2
		}
		s := st
		s.X, s.Z, s.Radius = c[0], c[1], c[2]
		l.Apply(s)
	}
	for _, c := range lines {
		if len(c) != 5 {
			fmt.Fprintf(out, "-line %v: want x0,z0,x1,z1,radius\n", c)
			return 2
		}
		s := st
		s.Radius = c[4]
		l.ApplyLine(s, c[0], c[1], c[2], c[3])
	}
	if err := procgen.WriteDoc(path, d); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	fmt.Fprintf(out, "%s: painted %s (%s): %d tiles -> %s\n", d.Name, def.Name, def.Kind, len(l.Keys()),
		fileutil.RelAssetPath(paint.Dir(path)))
	return 0
}

// paintPreset is -preset: the recipe when the document lacks the preset's
// layers, then every -disc and -line stamped into each of its layers.
func paintPreset(out io.Writer, path string, d *procgen.Doc, name string, noRecipe bool, st paint.Stroke, curve string,
	clear bool, discs, lines numList) int {
	ps, errs := stages.LoadPresets(stages.PresetsDir())
	for _, e := range errs {
		fmt.Fprintln(out, e)
	}
	pr := stages.FindPreset(ps, name)
	if pr == nil {
		fmt.Fprintf(out, "no brush preset %q in %s\n", name, fileutil.RelAssetPath(stages.PresetsDir()))
		return 2
	}
	if pr.River != nil || pr.Path != nil {
		return paintRiver(out, path, d, pr, discs, lines)
	}
	cv, ok := paint.ParseCurve(curve)
	if !ok {
		fmt.Fprintf(out, "-curve %q: want one of %s\n", curve, strings.Join(paint.CurveNames, ", "))
		return 2
	}
	st.Curve = cv
	if miss := stages.MissingLayers(d, pr); len(miss) > 0 {
		if noRecipe {
			fmt.Fprintf(out, "%s lacks the layers %s of preset %s\n", d.Name, strings.Join(miss, ", "), pr.Label())
			return 1
		}
		notes, err := stages.ApplyRecipe(d, pr)
		for _, n := range notes {
			fmt.Fprintf(out, "recipe %s: %s\n", pr.ID, n)
		}
		if err != nil {
			fmt.Fprintln(out, err)
			return 1
		}
	}
	for _, c := range discs {
		if len(c) != 3 {
			fmt.Fprintf(out, "-disc %v: want x,z,radius\n", c)
			return 2
		}
	}
	for _, c := range lines {
		if len(c) != 5 {
			fmt.Fprintf(out, "-line %v: want x0,z0,x1,z1,radius\n", c)
			return 2
		}
	}
	for _, pl := range pr.Layers {
		def := d.PaintDef(pl.Layer)
		if def == nil {
			fmt.Fprintf(out, "preset %s paints %s, which the document does not declare\n", pr.ID, pl.Layer)
			return 1
		}
		l := d.EnsurePaintLayer(*def)
		if clear {
			for _, k := range l.Keys() {
				l.SetTile(k, nil)
			}
		}
	}
	stamp := func(s paint.Stroke, line []float64) {
		for _, ps := range pr.Stamps(s) {
			l := d.PaintLayer(ps.Layer)
			if line != nil {
				l.ApplyLine(ps.Stroke, line[0], line[1], line[2], line[3])
			} else {
				l.Apply(ps.Stroke)
			}
		}
	}
	for _, c := range discs {
		s := st
		s.X, s.Z, s.Radius = c[0], c[1], c[2]
		stamp(s, nil)
	}
	for _, c := range lines {
		s := st
		s.Radius = c[4]
		stamp(s, c[:4])
	}
	if err := procgen.WriteDoc(path, d); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	for _, pl := range pr.Layers {
		fmt.Fprintf(out, "%s: painted %s (%s, x%.2g): %d tiles\n", d.Name, pl.Layer, d.PaintDef(pl.Layer).Kind, pl.Strength,
			len(d.PaintLayer(pl.Layer).Keys()))
	}
	return 0
}

// paintRiver is -preset with a river brush, or a path brush (phase P4:
// the same stroke, a `paths` item instead) (file comment).
func paintRiver(out io.Writer, path string, d *procgen.Doc, pr *stages.Preset, discs, lines numList) int {
	var pts [][2]float64
	radius := 0.0
	for _, c := range discs {
		if len(c) != 3 {
			fmt.Fprintf(out, "-disc %v: want x,z,radius\n", c)
			return 2
		}
		pts = append(pts, [2]float64{c[0], c[1]})
		radius = c[2]
	}
	for _, c := range lines {
		if len(c) != 5 {
			fmt.Fprintf(out, "-line %v: want x0,z0,x1,z1,radius\n", c)
			return 2
		}
		if n := len(pts); n == 0 || pts[n-1] != [2]float64{c[0], c[1]} {
			pts = append(pts, [2]float64{c[0], c[1]})
		}
		pts = append(pts, [2]float64{c[2], c[3]})
		radius = c[4]
	}
	// The editor's drag samples the path densely; so does this, so the
	// simplification sees the same stroke.
	var dense [][2]float64
	for i := range pts {
		if i == 0 {
			dense = append(dense, pts[0])
			continue
		}
		a, b := pts[i-1], pts[i]
		n := max(int(math.Ceil(math.Hypot(b[0]-a[0], b[1]-a[1])/math.Max(radius*0.25, 0.25))), 1)
		for k := 1; k <= n; k++ {
			t := float64(k) / float64(n)
			dense = append(dense, [2]float64{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t})
		}
	}
	add, list, what := stages.AddRiverStroke, "rivers", "river"
	if pr.Path != nil {
		add, list, what = stages.AddPathStroke, "paths", "path"
	}
	ge, idx, notes, err := add(d, pr, dense, radius)
	for _, n := range notes {
		fmt.Fprintln(out, "recipe:", n)
	}
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if err := procgen.WriteDoc(path, d); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	it := d.StageByID(ge).Params[list].Items[idx]
	fmt.Fprintf(out, "%s: %s %s (%d points) in %s\n", d.Name, what, it.Str("name"), len(it.Points("path")), ge)
	return 0
}
