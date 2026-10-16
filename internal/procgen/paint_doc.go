package procgen

// paint_doc.go — a document's paint layers (docs/terrain-paint/SPEC.md,
// docs/guide/36-terrain-paint.md).
//
//	paint:
//	  - {name: meadow, kind: density}
//	  - {name: keep_clear, kind: exclude, resolution: 1}
//	stages:
//	  - stage: grass_field
//	    params:
//	      exclude: [{layer: keep_clear}]
//	      rules:
//	        - {name: dense, paint: meadow, paint_band: {x: 0.1, y: 0.6}}
//
// The `paint:` list DECLARES layers: a name, a kind (procgen/paint's
// Kinds) and a resolution in metres per texel (0.5 when omitted). The
// pixels live beside the file in <doc>.paint/<layer>/<i>_<j>.png, sparse
// and in fixed 64 m world tiles (the paint package's file comment has the
// format); ReadDoc loads them into Doc.PaintData and WriteDoc saves what
// changed. Paint is an INPUT: a stage reads a layer through a mask
// parameter (ParamMask), anywhere in its schema, list items included.
//
// HASHING. A stage whose resolved parameters name any layer folds every
// named layer's content hash into its stage hash (after its refs' hashes,
// like HashExtra), so a stroke reruns exactly the stages that read the
// painted layer. A stage that names none — every stage of every document
// written before paint existed — hashes byte-identically to before, which
// the golden tests pin. The stages that read paint then rerun per tile, each
// piece keyed by the paint it can reach (paint_incremental.go).
//
// World level: a level generation doc's expanded region stages read the
// LEVEL doc's layers (the Env's Doc), never their preset's; a preset that
// names a layer the level doc does not declare reads zeros.

import (
	"fmt"
	"sort"
	"strings"

	"foundry/internal/procgen/paint"
)

// rawPaint is one `paint:` entry as YAML sees it.
type rawPaint struct {
	Name       string  `yaml:"name"`
	Kind       string  `yaml:"kind"`
	Resolution float64 `yaml:"resolution,omitempty"`
}

func paintDefsIn(raw []rawPaint) []paint.LayerDef {
	var out []paint.LayerDef
	for _, r := range raw {
		out = append(out, paint.LayerDef{Name: r.Name, Kind: r.Kind, Resolution: r.Resolution})
	}
	return out
}

func paintDefsOut(defs []paint.LayerDef) []rawPaint {
	var out []rawPaint
	for _, d := range defs {
		r := rawPaint{Name: d.Name, Kind: d.Kind, Resolution: d.Resolution}
		if r.Resolution == paint.DefaultResolution {
			r.Resolution = 0 // the default is not written
		}
		out = append(out, r)
	}
	return out
}

// PaintDef is the declared layer called name, or nil.
func (d *Doc) PaintDef(name string) *paint.LayerDef {
	for i := range d.Paint {
		if d.Paint[i].Name == name {
			return &d.Paint[i]
		}
	}
	return nil
}

// PaintLayer is the declared layer called name with its pixels, or nil (an
// undeclared name, an empty one, or no pixels loaded: every sample of a
// nil layer is 0).
func (d *Doc) PaintLayer(name string) *paint.Layer {
	if d == nil || name == "" || d.PaintDef(name) == nil {
		return nil
	}
	return d.PaintData.Layer(name)
}

// EnsurePaintLayer declares def (replacing a declaration of the same name)
// and returns its layer, creating PaintData as needed: what the editor's
// New layer and `foundry gen paint` do.
func (d *Doc) EnsurePaintLayer(def paint.LayerDef) *paint.Layer {
	if p := d.PaintDef(def.Name); p != nil {
		*p = def
	} else {
		d.Paint = append(d.Paint, def)
	}
	if d.PaintData == nil {
		d.PaintData = paint.NewSet()
	}
	return d.PaintData.Ensure(def)
}

// LoadPaint reads the declared layers' pixels from the paint folder beside
// path. A document declaring none gets no PaintData.
func (d *Doc) LoadPaint(path string) error {
	if len(d.Paint) == 0 {
		d.PaintData = nil
		return nil
	}
	for _, def := range d.Paint {
		if err := def.Validate(); err != nil {
			return err
		}
	}
	s, err := paint.Load(paint.Dir(path), d.Paint)
	if err != nil {
		return err
	}
	d.PaintData = s
	return nil
}

// SavePaint writes the declared layers' changed tiles into the paint folder
// beside path; nothing when the document holds no pixels.
func (d *Doc) SavePaint(path string) error {
	if d.PaintData == nil {
		return nil
	}
	return d.PaintData.Save(paint.Dir(path), d.Paint)
}

// MaskRefs is every paint layer p names through mask params, top level and
// inside list items, sorted, without repeats or blanks (a mask param's
// MaskWords name no layer and are left out).
func MaskRefs(defs []ParamDef, p Params) []string {
	seen := map[string]bool{}
	var walk func(defs []ParamDef, p Params)
	walk = func(defs []ParamDef, p Params) {
		for i := range defs {
			def := &defs[i]
			v, ok := p[def.Name]
			if !ok {
				v = def.Default
			}
			switch def.Type {
			case ParamMask:
				if v.Str != "" && !def.IsMaskWord(v.Str) {
					seen[v.Str] = true
				}
			case ParamList:
				for _, it := range v.Items {
					walk(def.Fields, it)
				}
			}
		}
	}
	walk(defs, p)
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// paintHash is what a stage whose params are p folds into its hash for the
// layers it reads: "" when it names none (so it hashes as before paint
// existed), else each named layer's content hash.
func paintHash(d *Doc, defs []ParamDef, p Params) string {
	refs := MaskRefs(defs, p)
	if len(refs) == 0 {
		return ""
	}
	parts := make([]string, len(refs))
	for i, n := range refs {
		h := "none"
		if def := d.PaintDef(n); def != nil {
			if l := d.PaintData.Layer(n); l != nil {
				h = l.Hash()
			} else {
				h = paint.NewLayer(*def).Hash()
			}
		}
		parts[i] = n + "=" + h
	}
	return strings.Join(parts, ",")
}

// foldPaint appends a stage's paint hash to its upstream hashes when it has
// one.
func foldPaint(d *Doc, defs []ParamDef, p Params, up []string) []string {
	if h := paintHash(d, defs, p); h != "" {
		up = append(up, "paint:"+h)
	}
	return up
}

// validatePaintDefs checks the `paint:` declarations.
func validatePaintDefs(d *Doc) []Problem {
	var out []Problem
	seen := map[string]bool{}
	for _, def := range d.Paint {
		if err := def.Validate(); err != nil {
			out = append(out, Problem{Msg: err.Error()})
		}
		if seen[def.Name] {
			out = append(out, Problem{Msg: fmt.Sprintf("paint layer %s is declared twice", def.Name)})
		}
		seen[def.Name] = true
	}
	return out
}

// validateMask is a mask param's problem, or "".
func validateMask(d *Doc, name string) string {
	if name == "" || d.PaintDef(name) != nil {
		return ""
	}
	var have []string
	for _, def := range d.Paint {
		have = append(have, def.Name)
	}
	if len(have) == 0 {
		return fmt.Sprintf("names paint layer %q, and the document declares none (add it under paint:, or paint it in the 2D view)", name)
	}
	return fmt.Sprintf("names paint layer %q, which is not declared (layers: %s)", name, strings.Join(have, ", "))
}

// PaintLayer is the document's paint layer called name, or nil (whose every
// sample is 0): what a stage reads a mask param through.
func (e *Env) PaintLayer(name string) *paint.Layer { return e.Doc.PaintLayer(name) }
