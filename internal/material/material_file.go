package material

// material_file.go — the .fmat document format.
//
// resources/materials/<name>.fmat, YAML, following the conventions
// particle_file.go and level_schema.go set: name tables rather than raw enum
// ints, omitempty wherever absent and zero mean the same thing, and
// WriteFileAtomic for the write so a crash mid-save cannot leave half a
// document behind.
//
//	kind: surface
//	texture: hull_glass
//	doc: The lit strip down the hangar wall.
//	effects:
//	  - effect: unlit
//	  - effect: uv_scroll
//	    rate: {x: 0, y: 0.25}
//	  - effect: water
//	    enabled: false
//	    wave_amplitude: 0.1
//	    reflect_color: {r: 0.5, g: 0.7, b: 0.9}
//
// An effect's parameters are written FLAT inside its own map, beside the
// `effect:` key that names its kind, rather than nested under a `params:`. It
// reads better, and the two reserved keys (`effect`, `enabled`) can never
// collide with a parameter because RegisterKind's table is the only source of
// parameter names and none of them is called either.
//
// The reader is schema-driven: a scalar is converted by its ParamDef's type,
// so the file carries no type annotations and a parameter whose type CHANGES
// in a later build still reads as something sane rather than as zero. A key no
// kind declares is not dropped — it is kept in Effect.Unknown and written back
// out, so opening a document from a newer build in an older editor and saving
// it does not quietly delete what that build understood.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/fileutil"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// MaterialsDirName is where materials live, beside models/, textures/ and
// particles/.
const MaterialsDirName = "materials"

// MaterialExt is the native document extension.
const MaterialExt = ".fmat"

// MaterialsDir is where the editor's open/save dialogs look and where the
// library loads from.
func MaterialsDir() string { return filepath.Join(fileutil.ResourcesPath, MaterialsDirName) }

// MaterialPath resolves a bare material name to a full path.
func MaterialPath(name string) string { return filepath.Join(MaterialsDir(), name+MaterialExt) }

// ScanMaterialFiles lists every material name on disk, sorted — what the
// pickers and the library iterate.
func ScanMaterialFiles() []string { return fileutil.ScanFilesByExt(MaterialsDir(), MaterialExt) }

// The two keys inside an effect map that are not parameters.
const (
	effectKindKey    = "effect"
	effectEnabledKey = "enabled"
)

// rawDoc is the file as YAML sees it. The effects are left as loose maps and
// converted against the kind table below, which is the whole point of the
// format: there is no Go struct per effect kind to keep in step.
type rawDoc struct {
	Kind    string                   `yaml:"kind"`
	Texture string                   `yaml:"texture,omitempty"`
	Doc     string                   `yaml:"doc,omitempty"`
	Effects []map[string]interface{} `yaml:"effects,omitempty"`
}

// rawDocOut is the same document on the way OUT. It differs from rawDoc in one
// respect — its effects are ordered MapSlices rather than maps — and that is
// the whole reason it exists: a Go map marshals in a shuffled order, which
// would turn every save of an unchanged material into a diff.
type rawDocOut struct {
	Kind    string          `yaml:"kind"`
	Texture string          `yaml:"texture,omitempty"`
	Doc     string          `yaml:"doc,omitempty"`
	Effects []yaml.MapSlice `yaml:"effects,omitempty"`
}

// ReadMaterial reads a .fmat from path. name is what the document is called —
// the caller's basename, not anything in the file, so a renamed file is a
// renamed material with nothing inside to keep in step.
func ReadMaterial(path string) (*Doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d, err := DecodeMaterial(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	d.Name = strings.TrimSuffix(filepath.Base(path), MaterialExt)
	return d, nil
}

// DecodeMaterial parses a .fmat's bytes. Split from ReadMaterial so the tests —
// and the editor's "revert" — need no file.
func DecodeMaterial(data []byte) (*Doc, error) {
	var raw rawDoc
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	d := &Doc{
		Domain:  DomainFromName(raw.Kind),
		Texture: raw.Texture,
		Doc:     raw.Doc,
	}
	for _, re := range raw.Effects {
		e, ok := decodeEffect(re)
		if ok {
			d.Effects = append(d.Effects, e)
		}
	}
	return d, nil
}

// decodeEffect converts one loose map into an Effect. An entry with no
// `effect:` key names no kind and is dropped — there is nothing to keep it as.
func decodeEffect(raw map[string]interface{}) (Effect, bool) {
	e := Effect{}
	for k, v := range raw {
		if k == effectKindKey {
			e.Kind, _ = v.(string)
		}
	}
	if e.Kind == "" {
		return e, false
	}
	kind := KindByName(e.Kind)
	for k, v := range raw {
		switch k {
		case effectKindKey:
			continue
		case effectEnabledKey:
			// Spelled positively in the file and negatively in the struct, so
			// that an effect written with no `enabled:` at all is on.
			if on, ok := v.(bool); ok {
				e.Disabled = !on
			}
			continue
		}
		var def *ParamDef
		if kind != nil {
			def = kind.Param(k)
		}
		if def == nil {
			// A parameter this build does not declare. Kept verbatim so a save
			// writes it back; see the file comment.
			if e.Unknown == nil {
				e.Unknown = map[string]interface{}{}
			}
			e.Unknown[k] = v
			continue
		}
		val, ok := decodeValue(def, v)
		if !ok {
			continue // unreadable: leave the parameter at its default
		}
		e.Set(k, def.Clamp(val))
	}
	return e, true
}

// decodeValue converts one YAML scalar (or small map/sequence) by a
// parameter's declared type.
func decodeValue(def *ParamDef, v interface{}) (Value, bool) {
	switch def.Type {
	case ParamFloat:
		f, ok := toFloat(v)
		return FloatValue(f), ok
	case ParamVec2:
		n, ok := toNumbers(v, [3]string{"x", "y", ""})
		return Value{Type: ParamVec2, Num: n}, ok
	case ParamVec3:
		n, ok := toNumbers(v, [3]string{"x", "y", "z"})
		return Value{Type: ParamVec3, Num: n}, ok
	case ParamColor:
		n, ok := toNumbers(v, [3]string{"r", "g", "b"})
		return Value{Type: ParamColor, Num: n}, ok
	case ParamBool:
		b, ok := v.(bool)
		return BoolValue(b), ok
	case ParamTexture:
		s, ok := v.(string)
		return TextureValue(s), ok
	case ParamEnum:
		s, ok := v.(string)
		if !ok {
			return Value{}, false
		}
		// An unknown choice loads as the first — the fallback rule
		// gfx.SpriteBlendFromName and its siblings all follow, so that a
		// document from a newer build opens rather than failing.
		if !containsStr(def.Choices, s) && len(def.Choices) > 0 {
			s = def.Choices[0]
		}
		return EnumValue(s), true
	}
	return Value{}, false
}

// toFloat accepts every shape YAML hands a number back in.
func toFloat(v interface{}) (float32, bool) {
	switch n := v.(type) {
	case float64:
		return float32(n), true
	case float32:
		return n, true
	case int:
		return float32(n), true
	case int64:
		return float32(n), true
	case uint64:
		return float32(n), true
	}
	return 0, false
}

// toNumbers reads a vector written either as a sequence ([0, 0.25]) or as a map
// keyed by the names for its axes ({x: 0, y: 0.25}, {r: …, g: …, b: …}). Both
// are accepted because both are natural to type.
//
// A missing component stays zero rather than failing, so {r: 1} is a red.
//
// The `y` key gets a second chance as the boolean true, and `n` as false.
// YAML 1.1 — which gopkg.in/yaml.v2 implements — resolves a bare y, Y, n or N
// as a boolean, so an author who writes the obvious {x: 1, y: 2} hands the
// decoder a map whose second key is `true`. Quoting it would work and nobody
// would ever remember to; this is cheaper than telling everyone the axis is
// spelled "y" except when it is not. The writer sidesteps the whole question
// by emitting vectors as sequences.
func toNumbers(v interface{}, keys [3]string) (mgl32.Vec3, bool) {
	var out mgl32.Vec3
	switch n := v.(type) {
	case []interface{}:
		for i := 0; i < len(n) && i < 3; i++ {
			f, ok := toFloat(n[i])
			if !ok {
				return out, false
			}
			out[i] = f
		}
		return out, true
	case map[interface{}]interface{}:
		for i, k := range keys {
			if k == "" {
				continue
			}
			raw, ok := n[k]
			if !ok {
				if alt, isAlt := yamlBoolKey(k); isAlt {
					raw, ok = n[alt]
				}
			}
			if !ok {
				continue
			}
			f, ok := toFloat(raw)
			if !ok {
				return out, false
			}
			out[i] = f
		}
		return out, true
	case map[string]interface{}:
		for i, k := range keys {
			if k == "" {
				continue
			}
			if raw, ok := n[k]; ok {
				f, ok := toFloat(raw)
				if !ok {
					return out, false
				}
				out[i] = f
			}
		}
		return out, true
	}
	// A bare number for a vector fills every component — how a uniform scale
	// is most naturally written.
	if f, ok := toFloat(v); ok {
		return mgl32.Vec3{f, f, f}, true
	}
	return out, false
}

// yamlBoolKey is the boolean a YAML 1.1 parser turns a bare axis name into, for
// the two axis names that are also booleans. See toNumbers.
func yamlBoolKey(name string) (bool, bool) {
	switch name {
	case "y":
		return true, true
	case "n":
		return false, true
	}
	return false, false
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ---------- writing ----------

// EncodeMaterial renders a document as .fmat bytes. A parameter still equal to
// its kind's default is left out, which is what keeps a file down to what was
// actually changed and what lets a default be revised later without rewriting
// every document that never disagreed with it.
func EncodeMaterial(d *Doc) ([]byte, error) {
	raw := rawDocOut{
		Kind:    DomainName(d.Domain),
		Texture: d.Texture,
		Doc:     d.Doc,
	}
	for i := range d.Effects {
		raw.Effects = append(raw.Effects, encodeEffect(&d.Effects[i]))
	}
	return yaml.Marshal(&raw)
}

// encodeEffect is one effect as the ordered map the format writes: `effect:`
// first, then the parameters in the SCHEMA's order rather than in any order
// the document happened to be built in, so that two materials with the same
// effects produce byte-identical blocks and a save with nothing changed
// produces no diff.
func encodeEffect(e *Effect) yaml.MapSlice {
	out := yaml.MapSlice{{Key: effectKindKey, Value: e.Kind}}
	if e.Disabled {
		out = append(out, yaml.MapItem{Key: effectEnabledKey, Value: false})
	}
	kind := KindByName(e.Kind)
	if kind != nil {
		for i := range kind.Params {
			p := &kind.Params[i]
			v, set := e.Params[p.Name]
			if !set || valuesEqual(v, p.Default) {
				continue
			}
			out = append(out, yaml.MapItem{Key: p.Name, Value: encodeValue(p, v)})
		}
	} else {
		// An effect kind this build does not know has no schema to encode by,
		// so its parameters go back out in name order — the one order that is
		// stable without one.
		for _, name := range sortedKeys(e.Params) {
			v := e.Params[name]
			var raw interface{} = v.Str
			if v.Type != ParamTexture && v.Type != ParamEnum {
				raw = v.Num[0]
			}
			out = append(out, yaml.MapItem{Key: name, Value: raw})
		}
	}
	for _, k := range sortedUnknownKeys(e.Unknown) {
		out = append(out, yaml.MapItem{Key: k, Value: e.Unknown[k]})
	}
	return out
}

func sortedKeys(m map[string]Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedUnknownKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func encodeValue(p *ParamDef, v Value) interface{} {
	switch p.Type {
	case ParamFloat:
		return v.Num[0]
	case ParamVec2:
		// A sequence rather than {x: …, y: …}: see toNumbers on what YAML 1.1
		// does to a bare `y` key. Both forms read; only one is written.
		return []interface{}{v.Num[0], v.Num[1]}
	case ParamVec3:
		return []interface{}{v.Num[0], v.Num[1], v.Num[2]}
	case ParamColor:
		return map[string]interface{}{"r": v.Num[0], "g": v.Num[1], "b": v.Num[2]}
	case ParamBool:
		return v.Bool
	}
	return v.Str
}

// valuesEqual compares only the payload the type actually uses, so that a
// float whose Str happens to be set still reads as its default.
func valuesEqual(a, b Value) bool {
	switch a.Type {
	case ParamBool:
		return a.Bool == b.Bool
	case ParamTexture, ParamEnum:
		return a.Str == b.Str
	case ParamFloat:
		return a.Num[0] == b.Num[0]
	case ParamVec2:
		return a.Num[0] == b.Num[0] && a.Num[1] == b.Num[1]
	}
	return a.Num == b.Num
}

// WriteMaterial writes d to the materials directory under its own name,
// creating the directory on first save so a fresh project needs no setup.
func WriteMaterial(d *Doc) error {
	if d.Name == "" {
		return fmt.Errorf("material: cannot save a document with no name")
	}
	return WriteMaterialTo(MaterialPath(d.Name), d)
}

// WriteMaterialTo writes d to an explicit path.
func WriteMaterialTo(path string, d *Doc) error {
	data, err := EncodeMaterial(d)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data)
}

// Fingerprint is a hash of the document as its file would store it — what the
// editor's Saved/Unsaved chip compares against the one taken at the last save.
//
// Over the ENCODED bytes rather than over the struct, for the reason
// editor_save_state.go gives: the question is whether the file on disk would
// differ, and two documents that encode identically do not. It also means a
// parameter set back to its default reads as saved again, because the encoder
// leaves it out.
func Fingerprint(d *Doc) uint64 {
	if d == nil {
		return 0
	}
	data, err := EncodeMaterial(d)
	if err != nil {
		return 0
	}
	return fileutil.Fingerprint(string(data))
}
