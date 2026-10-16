package stages

// paint_preset.go — BRUSH PRESETS: one stroke painting several layers at
// once (docs/terrain-paint/SPEC.md "Brush presets", PHASE_P3.md,
// docs/guide/36-terrain-paint.md).
//
// Ground cover is several layers: a forest floor is the litter splat, twigs,
// a few ferns and less grass. Painting them one layer at a time, each at its
// own strength, is tedious and never quite lines up. A preset is a named
// list of (layer, strength) pairs that one stroke writes together — "Forest
// floor": forest_litter 1.0, forest_twigs 0.6, forest_ferns 0.3, grass_clear
// 0.5. A pair's strength is the LEVEL the stroke paints that layer to: each
// stamp raises it by the brush's strength (paint.ModeRaise) but never past
// the pair's, and never lowers a layer already painted higher — so a drag
// with overlapping stamps leaves twigs at 0.6, not at 1.
//
// A preset names layers, and a layer is only useful if a stage reads it, so
// a preset also carries a RECIPE (paint_preset_recipe.go): the layer
// declarations, the Poly Haven textures and models, the grass_field and
// scatter rules and the material paint entries that make its layers grow
// something. Picking a preset whose layers the document lacks offers to
// apply the recipe, so "Forest floor" on a fresh terrain just works.
//
// FILES. resources/paint/<name>.fpb, YAML:
//
//	name: Forest floor                 # shown in the picker (default: the file name)
//	doc: Leaf litter, twigs and a few ferns; thins the grass.
//	order: 10                          # the picker's order (then the name)
//	layers:                            # what one stroke paints
//	  - {layer: forest_litter, strength: 1}
//	  - {layer: forest_twigs, strength: 0.6}
//	  - {layer: grass_thin, strength: 0.5}
//	recipe: {...}                      # paint_preset_recipe.go
//
// Strength is 0..1. The brush's mode chooses: add raises each layer toward
// its level; erase and smooth act on every layer as the brush would on one
// (erasing with a preset takes all of its layers away).
//
// RIVER BRUSHES (phase W3, paint_preset_river.go). A preset with a `river:`
// block paints no layer: a stroke of it becomes a river spline in the
// document's ground_edit (simplified from the drag, its width from the
// brush's radius), and its recipe dresses the rivers — the river and stream
// presets.
//
// PATH BRUSHES (phase P4, paint_preset_path.go). A preset with a `path:`
// block likewise paints no layer: a stroke becomes a footpath spline in the
// ground_edit, in the preset's style — rock path, stepping stones, dirt
// trail, gravel path, game trail.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/procgen/paint"

	"gopkg.in/yaml.v2"
)

// PresetExt is a brush preset file's extension; PresetDir its folder under
// resources/.
const (
	PresetExt = ".fpb"
	PresetDir = "paint"
)

// PresetLayer is one layer a preset's stroke paints.
type PresetLayer struct {
	Layer string `yaml:"layer"`
	// Strength is the level a stroke paints the layer to, 0..1.
	Strength float64 `yaml:"strength"`
}

// Preset is one brush preset.
type Preset struct {
	// ID is the file's name without the extension (not in the file).
	ID     string        `yaml:"-"`
	Name   string        `yaml:"name"`
	Doc    string        `yaml:"doc"`
	Order  int           `yaml:"order,omitempty"`
	Layers []PresetLayer `yaml:"layers"`
	Recipe Recipe        `yaml:"recipe"`
	// River makes the preset a river brush (paint_preset_river.go).
	River *PresetRiver `yaml:"river,omitempty"`
	// Path makes the preset a path brush (paint_preset_path.go).
	Path *PresetPath `yaml:"path,omitempty"`
}

// Label is the name the picker shows.
func (p *Preset) Label() string {
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}

// ParsePreset reads a preset file's bytes; id names it.
func ParsePreset(id string, data []byte) (*Preset, error) {
	p := &Preset{}
	if err := yaml.UnmarshalStrict(data, p); err != nil {
		return nil, fmt.Errorf("%s%s: %w", id, PresetExt, err)
	}
	p.ID = id
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("%s%s: %w", id, PresetExt, err)
	}
	return p, nil
}

func (p *Preset) validate() error {
	if len(p.Layers) == 0 && p.River == nil && p.Path == nil {
		return fmt.Errorf("paints no layer")
	}
	if p.River != nil && p.Path != nil {
		return fmt.Errorf("a preset is a river brush or a path brush, not both")
	}
	if p.River != nil {
		if err := p.River.validate(); err != nil {
			return err
		}
	}
	if p.Path != nil {
		if err := p.Path.validate(); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, l := range p.Layers {
		if !paint.ValidName(l.Layer) {
			return fmt.Errorf("%q is not a layer name", l.Layer)
		}
		if seen[l.Layer] {
			return fmt.Errorf("layer %s listed twice", l.Layer)
		}
		seen[l.Layer] = true
		if l.Strength < 0 || l.Strength > 1 {
			return fmt.Errorf("layer %s: strength %g is outside 0..1", l.Layer, l.Strength)
		}
	}
	return p.Recipe.validate(p.Layers)
}

// PresetsDir is the folder presets are read from.
func PresetsDir() string { return filepath.Join(fileutil.ResourcesPath, PresetDir) }

// LoadPresets reads every preset in dir, in picker order (Order, then
// label). A file that does not parse is reported in errs and left out.
func LoadPresets(dir string) (out []*Preset, errs []error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			errs = append(errs, err)
		}
		return nil, errs
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), PresetExt) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		p, err := ParsePreset(strings.TrimSuffix(e.Name(), PresetExt), data)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Label() < out[j].Label()
	})
	return out, errs
}

// FindPreset is the preset with this ID (or label, ignoring case) in ps.
func FindPreset(ps []*Preset, name string) *Preset {
	for _, p := range ps {
		if p.ID == name || strings.EqualFold(p.Label(), name) {
			return p
		}
	}
	return nil
}

// PresetStamp is one layer's stamp of a preset stroke.
type PresetStamp struct {
	Layer  string
	Stroke paint.Stroke
}

// Stamps is what one stamp of the brush (s, at the brush's strength and
// mode) writes with this preset: one stroke per layer. An add raises the
// layer toward its level (paint.ModeRaise, at the brush's strength times
// the level, so every layer arrives in the same number of stamps); erase,
// smooth and set act as the brush does on one layer.
func (p *Preset) Stamps(s paint.Stroke) []PresetStamp {
	out := make([]PresetStamp, 0, len(p.Layers))
	for _, l := range p.Layers {
		ls := s
		if s.Mode == paint.ModeAdd {
			ls.Mode = paint.ModeRaise
			ls.Value = l.Strength
			ls.Strength = s.Strength * l.Strength
		}
		out = append(out, PresetStamp{Layer: l.Layer, Stroke: ls})
	}
	return out
}
