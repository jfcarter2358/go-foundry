package procgen

// params.go — the parameter schema a stage kind declares, and the values a
// document gives it.
//
// The same idea as material.ParamDef, carried further: a ParamDef is the one
// source of truth for a parameter's type, default, range, label and tooltip,
// and everything downstream is driven off it —
//
//   - the .fgen reader converts each YAML value by its ParamDef (fgen_file.go),
//     so a document carries no type annotations;
//   - the writer omits any parameter still at its default, so a file says what
//     an author changed rather than what the engine thinks today;
//   - Resolve fills the defaults in and clamps, so a stage's Run reads a
//     complete, in-range set and never checks for missing keys;
//   - the Generator editor builds its whole parameter panel from the schema,
//     the spline editor in the top-down view included;
//   - `foundry gen list`, the guide's stage table and the Claude reference are
//     printed from it.
//
// The types beyond material's scalars are what procedural content needs:
//
//   - seed: an int with a dice button in the editor;
//   - stage: the ID of an UPSTREAM stage (StageKinds filters which kinds), the
//     edge of the run graph — a stage's hash folds in its refs' hashes;
//   - spline: a list of points, 2D (x, z) or 3D (x, y, z), each optionally
//     carrying named per-point channels (a canyon's width and depth along its
//     path). Edited in the top-down preview.
//   - curve: a 1D profile as (x, y) points sorted by x, evaluated with
//     Value.CurveAt — a cross-section, a falloff, a remap.
//   - list: a repeated group of sub-parameters (Fields), e.g. strata layers or
//     scatter rules. Items are Params of their own, resolved recursively.

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// ParamType is a parameter's type.
type ParamType int

const (
	ParamFloat ParamType = iota
	ParamInt
	ParamBool
	ParamEnum
	ParamVec2
	ParamVec3
	ParamColor
	ParamTexture
	ParamString
	ParamSeed
	ParamStage
	ParamSpline
	ParamCurve
	ParamList
)

var paramTypeNames = []string{
	"float", "int", "bool", "enum", "vec2", "vec3", "color", "texture", "string",
	"seed", "stage", "spline", "curve", "list",
}

// ParamTypeName is the type's spelling in docs and error messages.
func ParamTypeName(t ParamType) string {
	if int(t) >= 0 && int(t) < len(paramTypeNames) {
		return paramTypeNames[t]
	}
	return fmt.Sprintf("type(%d)", int(t))
}

// ParamDef declares one parameter.
type ParamDef struct {
	// Name is the YAML key under the stage's params:.
	Name string
	Type ParamType
	// Label is the editor's widget label (Name when empty); Doc is its tooltip
	// and what the docs print. People read both; nothing resolves off them.
	Label string
	Doc   string
	// Default is what a document that does not set the parameter reads. Its
	// Type must equal Type (RegisterStage checks). A list's default is usually
	// empty; a spline's may hold a starter path.
	Default Value
	// Min and Max bound numeric values — every component of a vector, every
	// coordinate of a curve's y. Equal means unbounded.
	Min, Max float64
	// Step is the editor's drag speed per pixel; 0 picks one from the range.
	Step float64
	// Choices are an enum's legal values in menu order. The first is what an
	// unknown value falls back to.
	Choices []string
	// StageKinds limits a stage ref to upstream stages of these kinds; empty
	// accepts any.
	StageKinds []string
	// Fields is a list item's schema.
	Fields []ParamDef
	// Spline3D makes a spline's points (x, y, z) rather than (x, z).
	Spline3D bool
	// Channels are a spline's extra per-point values, written after the
	// position under these names; ChannelDefaults is what a new point gets
	// (missing entries are 0).
	Channels        []string
	ChannelDefaults []float64
	// CurveX is a curve's domain, [lo, hi]. Zero means [0, 1].
	CurveX [2]float64
	// MinItems and MaxItems bound a list's items or a spline's or curve's
	// points. MaxItems 0 is unbounded.
	MinItems, MaxItems int
	// PreviewOnly marks a parameter that changes only what the editor's
	// preview shows (which variant, at what budget): it is left out of the
	// stage's hash (StageHash), so changing it never makes a run stale, and
	// kept in the preview's (PreviewHash), so the preview follows it. Top
	// level only.
	PreviewOnly bool
	// Added marks a parameter added to a kind after documents using it
	// existed: while it is at its default it is left out of the stage's
	// hash, so adding it does not make every existing stage of the kind
	// stale (and rerun a kilometre of terrain for nothing). Set to anything
	// else it hashes like any parameter. Top level and list fields alike.
	Added bool
	// AssetList marks a list whose items name Poly Haven assets of this type
	// ("models" or "textures") by their `id` field: the editor draws it with
	// each asset's thumbnail and a Browse… button that opens the Poly Haven
	// browser (docs/canyon/PHASE_4.md). It changes nothing else — the hash,
	// the YAML and validation treat it as the plain list it is.
	AssetList string
	// Closed marks a spline whose last point joins its first: a polygon (a
	// region's footprint) rather than a path. The editor draws the closing
	// segment; nothing else changes.
	Closed bool
	// ReadOnly marks a parameter the editor shows but does not let an
	// author change, because something else writes it (the regions stage's
	// region list, which genlevel.SyncRegions copies from the level before
	// every run). Its splines are not drawn as editable in the 2D view. It
	// changes nothing else: the file, the hash and validation treat it as
	// any parameter.
	ReadOnly bool
}

// DisplayLabel is Label, or Name when there is none.
func (p *ParamDef) DisplayLabel() string {
	if p.Label != "" {
		return p.Label
	}
	return p.Name
}

// Stride is how many numbers one of a spline's or curve's points holds.
func (p *ParamDef) Stride() int {
	switch p.Type {
	case ParamCurve:
		return 2
	case ParamSpline:
		n := 2
		if p.Spline3D {
			n = 3
		}
		return n + len(p.Channels)
	}
	return 0
}

// PointKeys are the YAML keys of one point, in order: x, z (or x, y, z) and
// then the channels for a spline; x, y for a curve.
func (p *ParamDef) PointKeys() []string {
	switch p.Type {
	case ParamCurve:
		return []string{"x", "y"}
	case ParamSpline:
		keys := []string{"x", "z"}
		if p.Spline3D {
			keys = []string{"x", "y", "z"}
		}
		return append(keys, p.Channels...)
	}
	return nil
}

// CurveDomain is the curve's x range.
func (p *ParamDef) CurveDomain() (lo, hi float64) {
	if p.CurveX == ([2]float64{}) {
		return 0, 1
	}
	return p.CurveX[0], p.CurveX[1]
}

// Field is a list item's sub-parameter by name, or nil.
func (p *ParamDef) Field(name string) *ParamDef { return findDef(p.Fields, name) }

func findDef(defs []ParamDef, name string) *ParamDef {
	for i := range defs {
		if defs[i].Name == name {
			return &defs[i]
		}
	}
	return nil
}

// NewPoint is a spline point at (x, z) — or (x, y, z) — with its channels at
// their defaults.
func (p *ParamDef) NewPoint(pos ...float64) []float64 {
	pt := make([]float64, p.Stride())
	copy(pt, pos)
	off := 2
	if p.Spline3D {
		off = 3
	}
	for i := range p.Channels {
		if i < len(p.ChannelDefaults) {
			pt[off+i] = p.ChannelDefaults[i]
		}
	}
	return pt
}

// NewItem is a list item with every field at its default.
func (p *ParamDef) NewItem() Params { return Resolve(p.Fields, nil) }

// ---------- values ----------

// Value is one parameter's value. Which fields mean anything depends on Type.
type Value struct {
	Type ParamType
	// Num is a float, int or seed.
	Num  float64
	Bool bool
	// Str is an enum, texture, string or stage ref.
	Str string
	// Vec is a vec2 (x, y), vec3 (x, y, z) or colour (r, g, b).
	Vec [3]float64
	// Points is a spline's or curve's points, each ParamDef.Stride long.
	Points [][]float64
	// Items is a list's items.
	Items []Params
}

// Params is a stage's (or a list item's) parameter values by name.
type Params map[string]Value

// Constructors, one per type, so a schema reads as a table.
func Float(f float64) Value            { return Value{Type: ParamFloat, Num: f} }
func Int(i int) Value                  { return Value{Type: ParamInt, Num: float64(i)} }
func Bool(b bool) Value                { return Value{Type: ParamBool, Bool: b} }
func Enum(s string) Value              { return Value{Type: ParamEnum, Str: s} }
func Texture(s string) Value           { return Value{Type: ParamTexture, Str: s} }
func String(s string) Value            { return Value{Type: ParamString, Str: s} }
func Seed(n int64) Value               { return Value{Type: ParamSeed, Num: float64(n)} }
func StageRef(id string) Value         { return Value{Type: ParamStage, Str: id} }
func Vec2(x, y float64) Value          { return Value{Type: ParamVec2, Vec: [3]float64{x, y}} }
func Vec3(x, y, z float64) Value       { return Value{Type: ParamVec3, Vec: [3]float64{x, y, z}} }
func Color(r, g, b float64) Value      { return Value{Type: ParamColor, Vec: [3]float64{r, g, b}} }
func Spline(points ...[]float64) Value { return Value{Type: ParamSpline, Points: points} }
func Curve(points ...[2]float64) Value {
	v := Value{Type: ParamCurve}
	for _, p := range points {
		v.Points = append(v.Points, []float64{p[0], p[1]})
	}
	return v
}
func List(items ...Params) Value { return Value{Type: ParamList, Items: items} }

// Clone is a deep copy: the points and the items are the value's own.
func (v Value) Clone() Value {
	if v.Points != nil {
		pts := make([][]float64, len(v.Points))
		for i, p := range v.Points {
			pts[i] = append([]float64(nil), p...)
		}
		v.Points = pts
	}
	if v.Items != nil {
		items := make([]Params, len(v.Items))
		for i, it := range v.Items {
			items[i] = it.Clone()
		}
		v.Items = items
	}
	return v
}

// Clone is a deep copy.
func (p Params) Clone() Params {
	if p == nil {
		return nil
	}
	out := make(Params, len(p))
	for k, v := range p {
		out[k] = v.Clone()
	}
	return out
}

// Equal reports whether two values are the same, deeply.
func (v Value) Equal(o Value) bool {
	if v.Type != o.Type || v.Num != o.Num || v.Bool != o.Bool || v.Str != o.Str || v.Vec != o.Vec {
		return false
	}
	if len(v.Points) != len(o.Points) || len(v.Items) != len(o.Items) {
		return false
	}
	for i := range v.Points {
		if len(v.Points[i]) != len(o.Points[i]) {
			return false
		}
		for j := range v.Points[i] {
			if v.Points[i][j] != o.Points[i][j] {
				return false
			}
		}
	}
	for i := range v.Items {
		if !v.Items[i].Equal(o.Items[i]) {
			return false
		}
	}
	return true
}

// Equal reports whether two parameter sets hold the same values.
func (p Params) Equal(o Params) bool {
	if len(p) != len(o) {
		return false
	}
	for k, v := range p {
		ov, ok := o[k]
		if !ok || !v.Equal(ov) {
			return false
		}
	}
	return true
}

// CurveAt evaluates a curve at x: linear between its points, flat beyond the
// ends. A curve with no points is 0; one point is a constant.
func (v Value) CurveAt(x float64) float64 {
	pts := v.Points
	switch len(pts) {
	case 0:
		return 0
	case 1:
		return pts[0][1]
	}
	if x <= pts[0][0] {
		return pts[0][1]
	}
	last := pts[len(pts)-1]
	if x >= last[0] {
		return last[1]
	}
	i := sort.Search(len(pts), func(i int) bool { return pts[i][0] > x })
	a, b := pts[i-1], pts[i]
	if b[0] == a[0] {
		return b[1]
	}
	t := (x - a[0]) / (b[0] - a[0])
	return a[1] + (b[1]-a[1])*t
}

// ---------- typed reads, for a stage's Run ----------
//
// Params handed to Run and Preview are Resolved, so every declared key is
// present; these return the zero value for a key that is not, which is a
// programming error in the stage (a name that is not in its schema) and is
// caught by the stage's own test rather than by a panic in a user's run.

func (p Params) Float(name string) float64   { return p[name].Num }
func (p Params) Int(name string) int         { return int(math.Round(p[name].Num)) }
func (p Params) Bool(name string) bool       { return p[name].Bool }
func (p Params) Str(name string) string      { return p[name].Str }
func (p Params) Vec2(name string) [2]float64 { v := p[name].Vec; return [2]float64{v[0], v[1]} }
func (p Params) Vec3(name string) [3]float64 { return p[name].Vec }
func (p Params) Points(name string) [][]float64 {
	return p[name].Points
}
func (p Params) Items(name string) []Params { return p[name].Items }
func (p Params) Curve(name string) Value    { return p[name] }

// ---------- resolution ----------

// Resolve returns a complete parameter set: every def present, missing ones
// at their defaults, every value clamped and of its def's type, list items
// resolved against their Fields. Keys no def declares are dropped (the reader
// reports them; Resolve is for running). in is not modified.
func Resolve(defs []ParamDef, in Params) Params {
	out := make(Params, len(defs))
	for i := range defs {
		d := &defs[i]
		v, ok := in[d.Name]
		if !ok || v.Type != d.Type {
			v = d.Default.Clone()
		} else {
			v = v.Clone()
		}
		out[d.Name] = d.Clamp(v)
	}
	return out
}

// Clamp puts v back inside the def's range and shape: numbers within
// [Min, Max], ints rounded, an unknown enum value to the first choice, points
// to the def's stride, curve points sorted and within the domain, list items
// resolved.
func (d *ParamDef) Clamp(v Value) Value {
	clamp := func(x float64) float64 {
		if d.Min == d.Max {
			return x
		}
		return math.Max(d.Min, math.Min(d.Max, x))
	}
	switch d.Type {
	case ParamFloat:
		v.Num = clamp(v.Num)
	case ParamInt, ParamSeed:
		v.Num = math.Round(clamp(v.Num))
	case ParamVec2, ParamVec3, ParamColor:
		for i := range v.Vec {
			v.Vec[i] = clamp(v.Vec[i])
		}
		if d.Type == ParamVec2 {
			v.Vec[2] = 0
		}
	case ParamEnum:
		if len(d.Choices) > 0 && !containsStr(d.Choices, v.Str) {
			v.Str = d.Choices[0]
		}
	case ParamSpline:
		n := d.Stride()
		for i, p := range v.Points {
			if len(p) != n {
				np := d.NewPoint()
				copy(np, p)
				v.Points[i] = np
			}
		}
	case ParamCurve:
		lo, hi := d.CurveDomain()
		for i, p := range v.Points {
			if len(p) != 2 {
				np := make([]float64, 2)
				copy(np, p)
				p = np
			}
			p[0] = math.Max(lo, math.Min(hi, p[0]))
			p[1] = clamp(p[1])
			v.Points[i] = p
		}
		sort.SliceStable(v.Points, func(a, b int) bool { return v.Points[a][0] < v.Points[b][0] })
	case ParamList:
		for i := range v.Items {
			v.Items[i] = Resolve(d.Fields, v.Items[i])
		}
	}
	return v
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// checkDefs is RegisterStage's consistency check over a schema: every default
// of its declared type, every name unique, an enum's default among its
// choices, a list's Fields checked in turn. A broken schema is a programming
// error and panics at init, where it cannot be missed.
func checkDefs(owner string, defs []ParamDef) {
	seen := map[string]bool{}
	for i := range defs {
		d := &defs[i]
		where := owner + " param " + d.Name
		if d.Name == "" || strings.ContainsAny(d.Name, " .:") {
			panic("procgen: " + where + ": a param name must be a plain word")
		}
		if seen[d.Name] {
			panic("procgen: " + where + ": declared twice")
		}
		seen[d.Name] = true
		if d.Default.Type != d.Type {
			panic(fmt.Sprintf("procgen: %s: default is a %s, declared %s", where,
				ParamTypeName(d.Default.Type), ParamTypeName(d.Type)))
		}
		if d.Type == ParamEnum && (len(d.Choices) == 0 || !containsStr(d.Choices, d.Default.Str)) {
			panic("procgen: " + where + ": enum default must be one of its choices")
		}
		if d.Type == ParamList {
			if len(d.Fields) == 0 {
				panic("procgen: " + where + ": a list needs Fields")
			}
			checkDefs(where, d.Fields)
		}
	}
}
