package procgen

// fgen_file.go — reading and writing .fgen documents.
//
// Strict where it can afford to be: an unknown top-level or stage key, a
// parameter no kind declares, or a value of the wrong shape is an error, and
// every such problem in the file is reported at once with where it is
// ("stage ground: param size: want a number, got \"big\"") rather than the
// first one alone — a document is usually broken in several places by the
// same misunderstanding, and fixing them one load at a time is miserable.
//
// The one leniency is a stage of a kind this build does not know: its
// parameters are kept verbatim (Stage.Raw) and written back, Validate reports
// it, and Run refuses to run the document. A generator written by a newer
// build still opens, and saving it loses nothing.
//
// The forms each type takes in YAML:
//
//	float, int, seed     1.5, 4, 1234
//	bool                 true
//	enum, texture,
//	string, stage, mask  ridged
//	vec2, vec3           {x: 1, y: 2} / {x: 1, y: 2, z: 3}   (or [1, 2, 3])
//	color                {r: 1, g: 0.5, b: 0.2}              (or [1, 0.5, 0.2])
//	spline               [{x: 0, z: 0, width: 30}, ...]      (or [[0, 0, 30], ...])
//	curve                [{x: 0, y: 1}, {x: 1, y: 0}]        (or [[0, 1], [1, 0]])
//	list                 [{thickness: 4, hardness: 0.8}, ...]
//
// Writing uses the named forms and the schema's order, and omits parameters
// at their defaults (and list-item fields at theirs), so a saved file is
// stable — an unchanged document saves to the same bytes — and short.

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/fileutil"

	"gopkg.in/yaml.v2"
)

// rawDoc is the file as YAML sees it on the way in.
type rawDoc struct {
	Doc    string     `yaml:"doc,omitempty"`
	Seed   int64      `yaml:"seed"`
	Output string     `yaml:"output,omitempty"`
	Level  string     `yaml:"level,omitempty"`
	Paint  []rawPaint `yaml:"paint,omitempty"`
	Stages []rawStage `yaml:"stages"`
}

type rawStage struct {
	Stage   string                 `yaml:"stage"`
	ID      string                 `yaml:"id"`
	Enabled *bool                  `yaml:"enabled,omitempty"`
	Params  map[string]interface{} `yaml:"params,omitempty"`
}

// rawDocOut is the same on the way out, with ordered parameter maps.
type rawDocOut struct {
	Doc    string        `yaml:"doc,omitempty"`
	Seed   int64         `yaml:"seed"`
	Output string        `yaml:"output,omitempty"`
	Level  string        `yaml:"level,omitempty"`
	Paint  []rawPaint    `yaml:"paint,omitempty"`
	Stages []rawStageOut `yaml:"stages"`
}

type rawStageOut struct {
	Stage   string        `yaml:"stage"`
	ID      string        `yaml:"id"`
	Enabled *bool         `yaml:"enabled,omitempty"`
	Params  yaml.MapSlice `yaml:"params,omitempty"`
}

// DecodeError is every problem a document has, in file order.
type DecodeError struct {
	Problems []string
}

func (e *DecodeError) Error() string {
	if len(e.Problems) == 1 {
		return e.Problems[0]
	}
	return fmt.Sprintf("%d problems:\n  %s", len(e.Problems), strings.Join(e.Problems, "\n  "))
}

// GeneratorsDir is resources/generators.
func GeneratorsDir() string { return filepath.Join(fileutil.ResourcesPath, GeneratorsDirName) }

// DocPath is a generator's file by name.
func DocPath(name string) string { return filepath.Join(GeneratorsDir(), name+Ext) }

// ScanDocs lists every generator by name, sorted.
func ScanDocs() []string {
	files := fileutil.ScanFilesByExt(GeneratorsDir(), Ext)
	for i := range files {
		files[i] = strings.TrimSuffix(files[i], Ext)
	}
	return files
}

// NameFromPath is a document's name: its file's basename.
func NameFromPath(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// ReadDoc reads a .fgen file.
func ReadDoc(path string) (*Doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d, err := DecodeDoc(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	d.Name = NameFromPath(path)
	// Its paint layers' pixels, from <doc>.paint/ beside it (paint_doc.go).
	if err := d.LoadPaint(path); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return d, nil
}

// WriteDoc writes d to path (atomically, keeping a .bak like every document
// the editor saves), and its paint layers' changed tiles beside it when it
// holds any (paint_doc.go).
func WriteDoc(path string, d *Doc) error {
	data, err := EncodeDoc(d)
	if err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomic(path, data); err != nil {
		return err
	}
	return d.SavePaint(path)
}

// DecodeDoc parses a document's bytes. The name is left empty: it is the
// file's, which these bytes do not know.
func DecodeDoc(data []byte) (*Doc, error) {
	var raw rawDoc
	if err := yaml.UnmarshalStrict(data, &raw); err != nil {
		return nil, &DecodeError{Problems: []string{friendlyYAMLError(err)}}
	}
	d := &Doc{Doc: raw.Doc, Seed: raw.Seed, Output: raw.Output, Level: raw.Level, Paint: paintDefsIn(raw.Paint)}
	var problems []string
	for i, rs := range raw.Stages {
		where := fmt.Sprintf("stage %d", i+1)
		if rs.ID != "" {
			where = "stage " + rs.ID
		}
		if rs.Stage == "" {
			problems = append(problems, where+": has no `stage:` (which kind of stage it is)")
		}
		s := Stage{Kind: rs.Stage, ID: rs.ID, Disabled: rs.Enabled != nil && !*rs.Enabled}
		k := KindByName(rs.Stage)
		if k == nil {
			// Kept verbatim; Validate reports it.
			s.Raw = rs.Params
			d.Stages = append(d.Stages, s)
			continue
		}
		s.Params = Params{}
		keys := make([]string, 0, len(rs.Params))
		for key := range rs.Params {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			def := k.Param(key)
			if def == nil {
				problems = append(problems, fmt.Sprintf("%s: %s has no param %q%s", where, k.Name, key, suggest(key, k.Params)))
				continue
			}
			v, errs := decodeValue(def, rs.Params[key], where+": param "+key)
			problems = append(problems, errs...)
			if len(errs) == 0 {
				s.Params[key] = v
			}
		}
		d.Stages = append(d.Stages, s)
	}
	if len(problems) > 0 {
		return nil, &DecodeError{Problems: problems}
	}
	return d, nil
}

// friendlyYAMLError trims yaml.v2's wording to what an author needs: which
// line, and which key is not a thing.
func friendlyYAMLError(err error) string {
	s := strings.TrimPrefix(err.Error(), "yaml: ")
	s = strings.ReplaceAll(s, "unmarshal errors:\n  ", "")
	s = strings.ReplaceAll(s, " in type procgen.rawDoc", " (a document has doc, seed, output, level, paint and stages)")
	s = strings.ReplaceAll(s, " in type procgen.rawStage", " (a stage has stage, id, enabled and params)")
	return s
}

// suggest is a "did you mean" for a misspelt parameter, when one is close.
func suggest(key string, defs []ParamDef) string {
	best, bestD := "", 3
	for i := range defs {
		if d := editDistance(key, defs[i].Name); d < bestD {
			best, bestD = defs[i].Name, d
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf(" (did you mean %q?)", best)
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			c := 1
			if a[i-1] == b[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// ---------- values in ----------

func decodeValue(d *ParamDef, raw interface{}, where string) (Value, []string) {
	v := Value{Type: d.Type}
	bad := func(want string) (Value, []string) {
		return Value{}, []string{fmt.Sprintf("%s: want %s, got %s", where, want, describeRaw(raw))}
	}
	switch d.Type {
	case ParamFloat, ParamInt, ParamSeed:
		n, ok := asNumber(raw)
		if !ok {
			return bad("a number")
		}
		if d.Type != ParamFloat && n != math.Trunc(n) {
			return bad("a whole number")
		}
		v.Num = n
	case ParamBool:
		b, ok := raw.(bool)
		if !ok {
			return bad("true or false")
		}
		v.Bool = b
	case ParamEnum:
		s, ok := raw.(string)
		if !ok {
			return bad("one of " + strings.Join(d.Choices, ", "))
		}
		if !containsStr(d.Choices, s) {
			return Value{}, []string{fmt.Sprintf("%s: %q is not one of %s", where, s, strings.Join(d.Choices, ", "))}
		}
		v.Str = s
	case ParamTexture, ParamString, ParamStage, ParamMask:
		s, ok := raw.(string)
		if !ok {
			return bad("a name")
		}
		v.Str = s
	case ParamVec2, ParamVec3, ParamColor:
		keys := []string{"x", "y"}
		switch d.Type {
		case ParamVec3:
			keys = []string{"x", "y", "z"}
		case ParamColor:
			keys = []string{"r", "g", "b"}
		}
		nums, errs := decodeTuple(raw, keys, where, nil)
		if len(errs) > 0 {
			return Value{}, errs
		}
		copy(v.Vec[:], nums)
	case ParamSpline, ParamCurve:
		list, ok := raw.([]interface{})
		if !ok && raw != nil {
			return bad("a list of points")
		}
		var problems []string
		for i, item := range list {
			var init []float64
			if d.Type == ParamSpline {
				init = d.NewPoint()
			}
			nums, errs := decodeTuple(item, d.PointKeys(), fmt.Sprintf("%s: point %d", where, i+1), init)
			problems = append(problems, errs...)
			if len(errs) == 0 {
				v.Points = append(v.Points, nums)
			}
		}
		if len(problems) > 0 {
			return Value{}, problems
		}
	case ParamList:
		list, ok := raw.([]interface{})
		if !ok && raw != nil {
			return bad("a list")
		}
		var problems []string
		for i, item := range list {
			m, ok := asStringMap(item)
			if !ok {
				problems = append(problems, fmt.Sprintf("%s: item %d: want a map of %s, got %s", where, i+1, fieldNames(d.Fields), describeRaw(item)))
				continue
			}
			p := Params{}
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fd := d.Field(k)
				iw := fmt.Sprintf("%s: item %d: %s", where, i+1, k)
				if fd == nil {
					problems = append(problems, fmt.Sprintf("%s: not a field (fields are %s)", iw, fieldNames(d.Fields)))
					continue
				}
				fv, errs := decodeValue(fd, m[k], iw)
				problems = append(problems, errs...)
				if len(errs) == 0 {
					p[k] = fv
				}
			}
			v.Items = append(v.Items, p)
		}
		if len(problems) > 0 {
			return Value{}, problems
		}
	}
	return d.Clamp(v), nil
}

func fieldNames(defs []ParamDef) string {
	names := make([]string, len(defs))
	for i := range defs {
		names[i] = defs[i].Name
	}
	return strings.Join(names, ", ")
}

// decodeTuple reads a fixed set of numbers as a map with the given keys or a
// list in that order. A missing key reads init's entry (a spline point's
// channel defaults), or 0.
func decodeTuple(raw interface{}, keys []string, where string, init []float64) ([]float64, []string) {
	out := make([]float64, len(keys))
	copy(out, init)
	if list, ok := raw.([]interface{}); ok {
		if len(list) > len(keys) {
			return nil, []string{fmt.Sprintf("%s: %d numbers, want at most %d (%s)", where, len(list), len(keys), strings.Join(keys, ", "))}
		}
		for i, x := range list {
			n, ok := asNumber(x)
			if !ok {
				return nil, []string{fmt.Sprintf("%s: %s is %s, want a number", where, keys[i], describeRaw(x))}
			}
			out[i] = n
		}
		return out, nil
	}
	m, ok := asStringMap(raw)
	if !ok {
		return nil, []string{fmt.Sprintf("%s: want {%s}, got %s", where, strings.Join(keys, ", "), describeRaw(raw))}
	}
	var problems []string
	for k, x := range m {
		i := -1
		for j, key := range keys {
			if key == k {
				i = j
			}
		}
		if i < 0 {
			problems = append(problems, fmt.Sprintf("%s: no key %q (keys are %s)", where, k, strings.Join(keys, ", ")))
			continue
		}
		n, ok := asNumber(x)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: %s is %s, want a number", where, k, describeRaw(x)))
			continue
		}
		out[i] = n
	}
	sort.Strings(problems)
	return out, problems
}

func asNumber(raw interface{}) (float64, bool) {
	switch n := raw.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	}
	return 0, false
}

func asStringMap(raw interface{}) (map[string]interface{}, bool) {
	switch m := raw.(type) {
	case map[string]interface{}:
		return m, true
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(m))
		for k, v := range m {
			ks, ok := k.(string)
			if !ok {
				return nil, false
			}
			out[ks] = v
		}
		return out, true
	}
	return nil, false
}

func describeRaw(raw interface{}) string {
	switch t := raw.(type) {
	case nil:
		return "nothing"
	case string:
		return fmt.Sprintf("%q", t)
	case []interface{}:
		return "a list"
	case map[interface{}]interface{}, map[string]interface{}:
		return "a map"
	}
	return fmt.Sprintf("%v", raw)
}

// ---------- values out ----------

// EncodeDoc is the document's file bytes.
func EncodeDoc(d *Doc) ([]byte, error) {
	out := rawDocOut{Doc: d.Doc, Seed: d.Seed, Output: d.Output, Level: d.Level, Paint: paintDefsOut(d.Paint)}
	for i := range d.Stages {
		s := &d.Stages[i]
		rs := rawStageOut{Stage: s.Kind, ID: s.ID}
		if s.Disabled {
			f := false
			rs.Enabled = &f
		}
		if k := KindByName(s.Kind); k != nil {
			rs.Params = encodeParams(k.Params, s.Params, true)
		} else {
			rs.Params = rawToMapSlice(s.Raw)
		}
		out.Stages = append(out.Stages, rs)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodeParams is p in schema order. omitDefaults drops values equal to their
// def's default — what a file wants; the hash (hash.go) wants everything.
func encodeParams(defs []ParamDef, p Params, omitDefaults bool) yaml.MapSlice {
	var ms yaml.MapSlice
	for i := range defs {
		d := &defs[i]
		v, ok := p[d.Name]
		if !ok || v.Type != d.Type {
			if omitDefaults {
				continue
			}
			v = d.Default
		}
		if omitDefaults && v.Equal(d.Default) {
			continue
		}
		// The hash's encoding (omitDefaults false) leaves out an Added
		// parameter at its default, so the stages written before it existed
		// keep their hashes.
		if !omitDefaults && d.Added && v.Equal(d.Default) {
			continue
		}
		ms = append(ms, yaml.MapItem{Key: d.Name, Value: encodeValue(d, v, omitDefaults)})
	}
	return ms
}

func encodeValue(d *ParamDef, v Value, omitDefaults bool) interface{} {
	switch d.Type {
	case ParamFloat:
		return v.Num
	case ParamInt, ParamSeed:
		return int64(v.Num)
	case ParamBool:
		return v.Bool
	case ParamEnum, ParamTexture, ParamString, ParamStage, ParamMask:
		return v.Str
	case ParamVec2:
		return yaml.MapSlice{{Key: "x", Value: v.Vec[0]}, {Key: "y", Value: v.Vec[1]}}
	case ParamVec3:
		return yaml.MapSlice{{Key: "x", Value: v.Vec[0]}, {Key: "y", Value: v.Vec[1]}, {Key: "z", Value: v.Vec[2]}}
	case ParamColor:
		return yaml.MapSlice{{Key: "r", Value: v.Vec[0]}, {Key: "g", Value: v.Vec[1]}, {Key: "b", Value: v.Vec[2]}}
	case ParamSpline, ParamCurve:
		keys := d.PointKeys()
		pts := make([]yaml.MapSlice, 0, len(v.Points))
		for _, p := range v.Points {
			var ms yaml.MapSlice
			for i, k := range keys {
				if i < len(p) {
					ms = append(ms, yaml.MapItem{Key: k, Value: p[i]})
				}
			}
			pts = append(pts, ms)
		}
		return pts
	case ParamList:
		items := make([]yaml.MapSlice, 0, len(v.Items))
		for _, it := range v.Items {
			ms := encodeParams(d.Fields, it, omitDefaults)
			if ms == nil {
				ms = yaml.MapSlice{}
			}
			items = append(items, ms)
		}
		return items
	}
	return nil
}

// rawToMapSlice writes an unknown kind's params back in sorted key order, so
// even a document this build cannot read saves stably.
func rawToMapSlice(m map[string]interface{}) yaml.MapSlice {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ms := make(yaml.MapSlice, 0, len(keys))
	for _, k := range keys {
		ms = append(ms, yaml.MapItem{Key: k, Value: m[k]})
	}
	return ms
}

// Fingerprint is a hash of the document as its file stores it, for the
// editor's save chip.
func Fingerprint(d *Doc) uint64 {
	if d == nil {
		return 0
	}
	data, err := EncodeDoc(d)
	if err != nil {
		return 0
	}
	return fileutil.Fingerprint(string(data))
}
