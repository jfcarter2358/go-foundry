package flang

// value.go — what a flang expression evaluates to.
//
// Values are concrete types behind a small interface rather than one tagged
// struct. A tagged struct would avoid an allocation per scalar, but this is a
// language for gameplay events — a handful of handlers a frame, not a hot loop —
// and the type switch the interpreter is built on reads far better against real
// types than against a tag field. The AST is shaped the same way.
//
// The names carry a Val suffix because the token kinds already own Int, Float
// and Atom, and a language whose lexer and runtime disagree about what `Int`
// means is a language nobody can read.
//
// Which types are mutable is deliberate. Lists and dicts are reference values:
// passing one to a function and appending inside it is visible to the caller,
// which is what makes them useful for accumulating state. Everything else —
// scalars, atoms, refs, tuples, vecs — is a value, copied on assignment. A tuple
// being immutable is what lets a message be posted once and delivered many times
// without one recipient's edit reaching the others.

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Type names a value's runtime type. The names match the ones a script writes in
// a parameter's type position, so a mismatch message reads the way the author
// wrote the declaration.
type Type uint8

const (
	TypeNull Type = iota
	TypeInt
	TypeFloat
	TypeStr
	TypeBool
	TypeAtom
	TypeRef
	TypeList
	TypeDict
	TypeTuple
	TypeVec
)

var typeStrings = [...]string{
	TypeNull: "null", TypeInt: "int", TypeFloat: "float", TypeStr: "str",
	TypeBool: "bool", TypeAtom: "atom", TypeRef: "ref", TypeList: "list",
	TypeDict: "dict", TypeTuple: "tuple", TypeVec: "vec",
}

// String renders a type as a script would spell it.
func (t Type) String() string {
	if int(t) < len(typeStrings) {
		return typeStrings[t]
	}
	return "?"
}

// Value is anything a flang expression produces.
type Value interface {
	Type() Type
	// String renders the value the way `log` prints it.
	String() string
}

// ---------- scalars ----------

// NullVal is the absent value.
type NullVal struct{}

// IntVal is a 64-bit signed integer.
type IntVal int64

// FloatVal is a 64-bit float.
type FloatVal float64

// StrVal is a string.
type StrVal string

// BoolVal is true or false.
type BoolVal bool

// AtomVal is a literal name used as an enum or a message tag. Comparable, which
// is what lets the dispatcher index handlers by it.
type AtomVal string

func (NullVal) Type() Type  { return TypeNull }
func (IntVal) Type() Type   { return TypeInt }
func (FloatVal) Type() Type { return TypeFloat }
func (StrVal) Type() Type   { return TypeStr }
func (BoolVal) Type() Type  { return TypeBool }
func (AtomVal) Type() Type  { return TypeAtom }

func (NullVal) String() string    { return "null" }
func (v IntVal) String() string   { return strconv.FormatInt(int64(v), 10) }
func (v FloatVal) String() string { return formatFloat(float64(v)) }
func (v StrVal) String() string   { return string(v) }
func (v BoolVal) String() string {
	if v {
		return "true"
	}
	return "false"
}
func (v AtomVal) String() string { return ":" + string(v) + ":" }

// formatFloat renders a float the way a script author wrote it, keeping a
// trailing ".0" so a float never prints as though it were an int.
func formatFloat(f float64) string {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// ---------- refs ----------

// RefVal is a handle to a world object. It is a path of stable names rather than
// a pointer or an index, because the level is rebuilt wholesale by undo and by a
// reload — anything positional would dangle across either. See host.go.
type RefVal struct {
	Kind Kind_ // what sort of object
	A    string
	B    string
	C    string
}

// Kind_ identifies what a ref points at. The trailing underscore keeps it clear
// of the lexer's Kind, which is a different idea entirely.
type Kind_ uint8

const (
	RefNone Kind_ = iota
	RefLevel
	RefChunk
	// RefBrush is a piece of the level's solid geometry — the sectors/edges/
	// floors/ceilings a ref used to be able to name are gone with them. A
	// brush is addressed by its own (level-unique) ID, same as an entity or a
	// volume, not scoped by its chunk the way a sector's ID used to be.
	RefBrush
	RefEntity
	RefVolume
	RefLight
	RefSprite
	RefEmitter
	RefSound
	// A screen and one widget on it. Appended after the level kinds so that no
	// existing constant shifts — a reordered enum would silently reinterpret a
	// ref held anywhere it was written down.
	//
	// (RefBiome sat between RefLight and RefSprite and was removed rather than
	// left as a hole: biome scatter became a property of a Volume rather than
	// an object of its own, so nothing addresses one. Safe because a ref kind
	// is never serialised — it lives only in a running script's values.)
	RefScreen
	RefWidget
	// RefModel is one attachment of an entity — `@guard.model.body`. Appended
	// for the same reason the screen kinds were.
	RefModel
)

var refKindNames = [...]string{
	RefNone: "none", RefLevel: "level", RefChunk: "chunk", RefBrush: "brush",
	RefEntity: "entity", RefVolume: "volume", RefLight: "light",
	RefSprite: "sprite", RefEmitter: "emitter", RefSound: "sound",
	RefScreen: "screen", RefWidget: "widget",
	RefModel: "model",
}

// String renders a ref kind as the atom `ref.kind` yields.
func (k Kind_) String() string {
	if int(k) < len(refKindNames) {
		return refKindNames[k]
	}
	return "none"
}

func (RefVal) Type() Type { return TypeRef }

// String renders a ref as its address, which is what makes a logged ref
// something an author can search their level for.
func (v RefVal) String() string {
	switch v.Kind {
	case RefNone:
		return "@?"
	case RefLevel:
		return "@level"
	case RefWidget:
		// Screen and node, because a widget id is only unique within its
		// screen and "@ok" would not say which "ok".
		return "@" + v.A + "." + v.B
	case RefModel:
		// Entity and attachment, for the same reason: "body" names an
		// attachment of something, never a thing in the level.
		return "@" + v.A + ".model." + v.B
	default:
		return "@" + v.A
	}
}

// ID returns the name a script would address this object by.
func (v RefVal) ID() string {
	switch v.Kind {
	case RefWidget, RefModel:
		// Both are scoped by something that comes first in the path: a widget
		// by its screen, a model attachment by its entity. `ref.id` is the
		// thing's own name, which is what a handler comparing `src.id` against
		// a literal is asking for.
		return v.B
	default:
		return v.A
	}
}

// IsNone reports whether the ref points at nothing.
func (v RefVal) IsNone() bool { return v.Kind == RefNone }

// ---------- compounds ----------

// ListVal is an ordered, growable sequence. A pointer type: appending inside a
// function is visible to the caller, which is what makes a list usable as an
// accumulator.
type ListVal struct {
	Elems []Value
}

// NewList returns a list over elems, which it takes ownership of.
func NewList(elems ...Value) *ListVal { return &ListVal{Elems: elems} }

func (*ListVal) Type() Type { return TypeList }

func (v *ListVal) String() string {
	var b strings.Builder
	b.WriteByte('[')
	for i, e := range v.Elems {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(display(e))
	}
	b.WriteByte(']')
	return b.String()
}

// DictVal maps string keys to values. A pointer type, for the same reason a list
// is one.
type DictVal struct {
	M map[string]Value
}

// NewDict returns an empty dict.
func NewDict() *DictVal { return &DictVal{M: map[string]Value{}} }

func (*DictVal) Type() Type { return TypeDict }

// String renders the dict with its keys sorted, so logging one twice gives the
// same text twice — Go's map order would not.
func (v *DictVal) String() string {
	keys := make([]string, 0, len(v.M))
	for k := range v.M {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Quote(k))
		b.WriteString(": ")
		b.WriteString(display(v.M[k]))
	}
	b.WriteByte('}')
	return b.String()
}

// Get returns the value at key, or null. A missing key is not an error — see
// SCRIPTING.md.
func (v *DictVal) Get(key string) Value {
	if got, ok := v.M[key]; ok {
		return got
	}
	return NullVal{}
}

// TupleVal is a fixed-length sequence, copied by value. Messages are tuples, and
// their immutability is what lets one be posted to a hundred recipients without
// any of them seeing another's edit.
type TupleVal struct {
	Elems []Value
}

// NewTuple returns a tuple over elems.
func NewTuple(elems ...Value) TupleVal { return TupleVal{Elems: elems} }

func (TupleVal) Type() Type { return TypeTuple }

func (v TupleVal) String() string {
	var b strings.Builder
	b.WriteByte('(')
	for i, e := range v.Elems {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(display(e))
	}
	if len(v.Elems) == 1 {
		b.WriteByte(',')
	}
	b.WriteByte(')')
	return b.String()
}

// VecVal is three floats with vector operations.
type VecVal [3]float64

func (VecVal) Type() Type { return TypeVec }

func (v VecVal) String() string {
	return "<" + formatFloat(v[0]) + ", " + formatFloat(v[1]) + ", " + formatFloat(v[2]) + ">"
}

// Add, Sub, Mul, Dot, Cross, Len and Normalized are the built-in vector ops.
func (v VecVal) Add(o VecVal) VecVal { return VecVal{v[0] + o[0], v[1] + o[1], v[2] + o[2]} }
func (v VecVal) Sub(o VecVal) VecVal { return VecVal{v[0] - o[0], v[1] - o[1], v[2] - o[2]} }
func (v VecVal) Mul(s float64) VecVal {
	return VecVal{v[0] * s, v[1] * s, v[2] * s}
}
func (v VecVal) Dot(o VecVal) float64 { return v[0]*o[0] + v[1]*o[1] + v[2]*o[2] }
func (v VecVal) Cross(o VecVal) VecVal {
	return VecVal{
		v[1]*o[2] - v[2]*o[1],
		v[2]*o[0] - v[0]*o[2],
		v[0]*o[1] - v[1]*o[0],
	}
}
func (v VecVal) Len() float64 { return math.Sqrt(v.Dot(v)) }

// Normalized returns the unit vector, or the zero vector when there is no
// direction to normalise — dividing by zero here would produce NaN and poison
// every position it later touched.
func (v VecVal) Normalized() VecVal {
	l := v.Len()
	if l == 0 {
		return VecVal{}
	}
	return v.Mul(1 / l)
}

// ---------- helpers ----------

// display renders a value for inclusion inside a container, quoting strings so a
// list of names does not read as a list of bare words.
func display(v Value) string {
	if s, ok := v.(StrVal); ok {
		return strconv.Quote(string(s))
	}
	return v.String()
}

// Truthy is what `if`, `while`, `and`, `or` and a guard test.
//
// Only `false` and `null` are false. Zero and the empty string are true, because
// a language where `if @self.attr.count` silently means "is it non-zero" is one
// where forgetting a comparison is invisible.
func Truthy(v Value) bool {
	switch t := v.(type) {
	case BoolVal:
		return bool(t)
	case NullVal:
		return false
	case nil:
		return false
	}
	return true
}

// Equal implements `==`. Values of different types are never equal rather than
// an error, so a dict lookup that yielded null can be compared against anything.
func Equal(a, b Value) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	// int and float compare across the numeric tower, matching the way
	// arithmetic promotes them.
	if an, aok := numeric(a); aok {
		if bn, bok := numeric(b); bok {
			return an == bn
		}
		return false
	}
	if a.Type() != b.Type() {
		return false
	}
	switch x := a.(type) {
	case NullVal:
		return true
	case StrVal:
		return x == b.(StrVal)
	case BoolVal:
		return x == b.(BoolVal)
	case AtomVal:
		return x == b.(AtomVal)
	case RefVal:
		return x == b.(RefVal)
	case VecVal:
		return x == b.(VecVal)
	case *ListVal:
		y := b.(*ListVal)
		if len(x.Elems) != len(y.Elems) {
			return false
		}
		for i := range x.Elems {
			if !Equal(x.Elems[i], y.Elems[i]) {
				return false
			}
		}
		return true
	case TupleVal:
		y := b.(TupleVal)
		if len(x.Elems) != len(y.Elems) {
			return false
		}
		for i := range x.Elems {
			if !Equal(x.Elems[i], y.Elems[i]) {
				return false
			}
		}
		return true
	case *DictVal:
		y := b.(*DictVal)
		if len(x.M) != len(y.M) {
			return false
		}
		for k, xv := range x.M {
			yv, ok := y.M[k]
			if !ok || !Equal(xv, yv) {
				return false
			}
		}
		return true
	}
	return false
}

// numeric returns a value as a float64 when it is an int or a float.
func numeric(v Value) (float64, bool) {
	switch t := v.(type) {
	case IntVal:
		return float64(t), true
	case FloatVal:
		return float64(t), true
	}
	return 0, false
}

// TypeMatches reports whether v satisfies the type a parameter declared.
//
// An int does *not* match `float` and a float does not match `int`. Arithmetic
// promotes freely, but dispatch does not: two clauses distinguished only by
// int-versus-float would otherwise both match every number.
func TypeMatches(v Value, typeName string) bool {
	if v == nil {
		return typeName == "null"
	}
	return v.Type().String() == typeName
}

// typeError builds the message a mismatched operand produces.
func typeError(op string, vals ...Value) string {
	names := make([]string, len(vals))
	for i, v := range vals {
		if v == nil {
			names[i] = "null"
			continue
		}
		names[i] = v.Type().String()
	}
	return fmt.Sprintf("%s does not apply to %s", op, strings.Join(names, " and "))
}

// TypeOf names a value's type, tolerating nil. Used by hosts building error
// messages, which routinely have a value that may not be there.
func TypeOf(v Value) string {
	if v == nil {
		return "null"
	}
	return v.Type().String()
}
