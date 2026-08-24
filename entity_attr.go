package main

// entity_attr.go — user-defined entity attributes.
//
// An entity's built-in fields (position, bounds, models) are what the engine
// itself needs. Attributes are what the *game* needs: health, a door's locked
// flag, an NPC's display name — arbitrary named values the engine stores,
// round-trips through YAML, and otherwise leaves alone until a script reads
// them.
//
// The four types are deliberately the four scalar types flang has (see
// SCRIPTING.md): int, float, bool, str. Keeping the sets identical means an
// attribute never has to be converted on its way into a script, and there is no
// type an author can write here that the scripting engine will not later
// understand. The compound flang types (dict, list, atom) are not authored
// here — they are things scripts build at runtime, not level data.

import (
	"fmt"
	"sort"
	"strconv"
)

// AttrType names an attribute's type. The values are the type names flang uses,
// so they read the same in a level file and in a script.
type AttrType string

const (
	AttrInt    AttrType = "int"
	AttrFloat  AttrType = "float"
	AttrBool   AttrType = "bool"
	AttrString AttrType = "str"
)

// attrTypeNames is every valid AttrType, in the order the editor's type combo
// offers them. Adding a type means adding it here and to the switches below.
var attrTypeNames = []AttrType{AttrInt, AttrFloat, AttrBool, AttrString}

// validAttrType reports whether t is one of the four.
func validAttrType(t AttrType) bool {
	for _, n := range attrTypeNames {
		if n == t {
			return true
		}
	}
	return false
}

// AttrValue is one attribute's value: a type tag plus the storage for each of
// the four types.
//
// A tagged struct rather than an `any`, because an attribute's type is authored
// state that has to survive a round-trip even when the value is a zero. An
// `any` holding int64(0) and one holding float64(0) are indistinguishable once
// YAML has been through them, which would silently retype an attribute the
// moment a level was saved.
type AttrValue struct {
	Type AttrType
	I    int64
	F    float64
	B    bool
	S    string
}

// IntAttr, FloatAttr, BoolAttr and StringAttr build a value of each type.
func IntAttr(v int64) AttrValue     { return AttrValue{Type: AttrInt, I: v} }
func FloatAttr(v float64) AttrValue { return AttrValue{Type: AttrFloat, F: v} }
func BoolAttr(v bool) AttrValue     { return AttrValue{Type: AttrBool, B: v} }
func StringAttr(v string) AttrValue { return AttrValue{Type: AttrString, S: v} }

// zeroAttr returns the zero value of a type — what a newly added attribute
// starts as, and what a malformed one falls back to.
func zeroAttr(t AttrType) AttrValue {
	if !validAttrType(t) {
		t = AttrInt
	}
	return AttrValue{Type: t}
}

// Int returns the value as an integer. A float attribute truncates, which is
// what makes `health: 10.0` usable where an int is expected; anything else
// reports false rather than inventing a number.
func (a AttrValue) Int() (int64, bool) {
	switch a.Type {
	case AttrInt:
		return a.I, true
	case AttrFloat:
		return int64(a.F), true
	}
	return 0, false
}

// Float returns the value as a float. An int attribute widens, so an attribute
// authored as `5` still satisfies a script asking for a float.
func (a AttrValue) Float() (float64, bool) {
	switch a.Type {
	case AttrFloat:
		return a.F, true
	case AttrInt:
		return float64(a.I), true
	}
	return 0, false
}

// Bool returns the value when it is a boolean. Numbers deliberately do not
// coerce: "0 is false" is a C convention flang does not have, and silently
// honouring it here would make the two disagree.
func (a AttrValue) Bool() (bool, bool) {
	if a.Type == AttrBool {
		return a.B, true
	}
	return false, false
}

// Str returns the value when it is a string. Use String() for a printable form
// of any type.
func (a AttrValue) Str() (string, bool) {
	if a.Type == AttrString {
		return a.S, true
	}
	return "", false
}

// String renders any attribute for display — the panel's read-out and error
// messages. Floats use -1 precision so an authored 0.5 shows as "0.5" rather
// than "0.500000".
func (a AttrValue) String() string {
	switch a.Type {
	case AttrInt:
		return strconv.FormatInt(a.I, 10)
	case AttrFloat:
		return strconv.FormatFloat(a.F, 'g', -1, 64)
	case AttrBool:
		return strconv.FormatBool(a.B)
	case AttrString:
		return a.S
	}
	return ""
}

// convertAttr re-types a value, carrying as much of it across as the new type
// can hold. Used by the editor when the user changes an attribute's type: the
// alternative is resetting to zero, which loses a number the user just typed
// merely because they picked float after int.
func convertAttr(a AttrValue, to AttrType) AttrValue {
	if !validAttrType(to) {
		return a
	}
	if a.Type == to {
		return a
	}
	switch to {
	case AttrInt:
		if v, ok := a.Int(); ok {
			return IntAttr(v)
		}
		if a.Type == AttrBool && a.B {
			return IntAttr(1)
		}
		if a.Type == AttrString {
			if v, err := strconv.ParseInt(a.S, 10, 64); err == nil {
				return IntAttr(v)
			}
		}
	case AttrFloat:
		if v, ok := a.Float(); ok {
			return FloatAttr(v)
		}
		if a.Type == AttrBool && a.B {
			return FloatAttr(1)
		}
		if a.Type == AttrString {
			if v, err := strconv.ParseFloat(a.S, 64); err == nil {
				return FloatAttr(v)
			}
		}
	case AttrBool:
		if v, ok := a.Bool(); ok {
			return BoolAttr(v)
		}
		if a.Type == AttrString {
			if v, err := strconv.ParseBool(a.S); err == nil {
				return BoolAttr(v)
			}
		}
	case AttrString:
		// Every type has a printable form, so this one never loses anything.
		return StringAttr(a.String())
	}
	return zeroAttr(to)
}

// ---------- attribute sets ----------

// AttrSet is an entity's attributes, keyed by name.
//
// A map rather than a slice: attributes are addressed by name from scripts and
// overridden by name by a level instance, and both of those are lookups. Author
// order is not preserved, which is why everything that walks a set — saving,
// the panel — goes through AttrNames for a stable alphabetical order instead.
type AttrSet map[string]AttrValue

// Clone returns a copy that shares nothing with the original. Instances start
// from their definition's attributes and then override them, so the definition
// must not be edited by the instances made from it.
func (s AttrSet) Clone() AttrSet {
	if s == nil {
		return nil
	}
	c := make(AttrSet, len(s))
	for k, v := range s {
		c[k] = v
	}
	return c
}

// AttrNames returns the set's names in sorted order. Every walk of a set uses
// this: Go randomises map iteration, which would otherwise reshuffle the
// attribute block on each save and churn the file.
func (s AttrSet) AttrNames() []string {
	names := make([]string, 0, len(s))
	for k := range s {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// Attr looks up one attribute.
func (s AttrSet) Attr(name string) (AttrValue, bool) {
	v, ok := s[name]
	return v, ok
}

// mergeAttrs returns base overlaid with over: every name in over replaces the
// one in base, names only in base survive. This is what a level instance
// overriding its definition's attributes means.
func mergeAttrs(base, over AttrSet) AttrSet {
	if len(base) == 0 && len(over) == 0 {
		return nil
	}
	out := make(AttrSet, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

// ---------- YAML ----------

// RawAttribute is the YAML form of one attribute:
//
//	health: {type: int, value: 25}
//
// Type may be omitted, in which case it is inferred from the scalar YAML
// produced — handy for hand-authoring, and unambiguous for every case except
// a whole-number float, which infers as int. Write the type explicitly when
// that matters.
type RawAttribute struct {
	Type  string `yaml:"type,omitempty"`
	Value any    `yaml:"value"`
}

// attrFromRaw converts one YAML attribute. The name is only used to make the
// error say which attribute was wrong.
func attrFromRaw(name string, r RawAttribute) (AttrValue, error) {
	t := AttrType(r.Type)
	if r.Type == "" {
		var ok bool
		if t, ok = inferAttrType(r.Value); !ok {
			return AttrValue{}, fmt.Errorf("attribute %q: cannot infer a type from %T; give it an explicit `type`", name, r.Value)
		}
	}
	if !validAttrType(t) {
		return AttrValue{}, fmt.Errorf("attribute %q: unknown type %q (want int, float, bool or str)", name, r.Type)
	}
	v, err := coerceAttr(t, r.Value)
	if err != nil {
		return AttrValue{}, fmt.Errorf("attribute %q: %w", name, err)
	}
	return v, nil
}

// inferAttrType picks a type from the Go value yaml.v2 decoded. A nil value
// (`value:` with nothing after it) has nothing to infer from.
func inferAttrType(v any) (AttrType, bool) {
	switch v.(type) {
	case int, int64, uint64:
		return AttrInt, true
	case float32, float64:
		return AttrFloat, true
	case bool:
		return AttrBool, true
	case string:
		return AttrString, true
	}
	return "", false
}

// coerceAttr turns a decoded YAML scalar into a value of the declared type.
//
// It accepts the numeric cross-cases (an int where a float was declared, and
// the reverse) because YAML gives no way to write 1.0 and have it decode as a
// float — `value: 1` under `type: float` is what an author will write, and
// refusing it would be pedantry. A string is parsed rather than rejected for
// the same reason: quoted numbers are what a YAML author reaches for when a
// value starts with a zero.
func coerceAttr(t AttrType, v any) (AttrValue, error) {
	if v == nil {
		// An absent value is the type's zero, not an error: `{type: str}` is a
		// perfectly reasonable way to declare an empty string attribute.
		return zeroAttr(t), nil
	}
	switch t {
	case AttrInt:
		switch n := v.(type) {
		case int:
			return IntAttr(int64(n)), nil
		case int64:
			return IntAttr(n), nil
		case uint64:
			return IntAttr(int64(n)), nil
		case float64:
			return IntAttr(int64(n)), nil
		case float32:
			return IntAttr(int64(n)), nil
		case string:
			p, err := strconv.ParseInt(n, 10, 64)
			if err != nil {
				return AttrValue{}, fmt.Errorf("%q is not an int", n)
			}
			return IntAttr(p), nil
		}
	case AttrFloat:
		switch n := v.(type) {
		case float64:
			return FloatAttr(n), nil
		case float32:
			return FloatAttr(float64(n)), nil
		case int:
			return FloatAttr(float64(n)), nil
		case int64:
			return FloatAttr(float64(n)), nil
		case uint64:
			return FloatAttr(float64(n)), nil
		case string:
			p, err := strconv.ParseFloat(n, 64)
			if err != nil {
				return AttrValue{}, fmt.Errorf("%q is not a float", n)
			}
			return FloatAttr(p), nil
		}
	case AttrBool:
		switch n := v.(type) {
		case bool:
			return BoolAttr(n), nil
		case string:
			p, err := strconv.ParseBool(n)
			if err != nil {
				return AttrValue{}, fmt.Errorf("%q is not a bool", n)
			}
			return BoolAttr(p), nil
		}
	case AttrString:
		// Everything has a printable form, so a string attribute accepts any
		// scalar: `version: {type: str, value: 2}` means "2".
		switch n := v.(type) {
		case string:
			return StringAttr(n), nil
		case int:
			return StringAttr(strconv.Itoa(n)), nil
		case int64:
			return StringAttr(strconv.FormatInt(n, 10)), nil
		case uint64:
			return StringAttr(strconv.FormatUint(n, 10)), nil
		case float64:
			return StringAttr(strconv.FormatFloat(n, 'g', -1, 64)), nil
		case bool:
			return StringAttr(strconv.FormatBool(n)), nil
		}
	}
	return AttrValue{}, fmt.Errorf("cannot read %T as %s", v, t)
}

// attrsFromRaw converts a whole YAML attribute block. Errors name the offending
// attribute and stop the load: a mistyped attribute is an authoring mistake
// worth surfacing, not something to silently drop on the floor and have a
// script find missing later.
func attrsFromRaw(raw map[string]RawAttribute) (AttrSet, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// Sorted so a file with two bad attributes always reports the same one
	// first, rather than whichever the map handed over that run.
	names := make([]string, 0, len(raw))
	for k := range raw {
		names = append(names, k)
	}
	sort.Strings(names)

	out := make(AttrSet, len(raw))
	for _, name := range names {
		v, err := attrFromRaw(name, raw[name])
		if err != nil {
			return nil, err
		}
		out[name] = v
	}
	return out, nil
}

// attrToRaw returns the YAML form of one attribute. The type is always written,
// even where it could be inferred, so a saved file never depends on the
// inference rules to load back as what it was.
func attrToRaw(a AttrValue) RawAttribute {
	r := RawAttribute{Type: string(a.Type)}
	switch a.Type {
	case AttrInt:
		r.Value = a.I
	case AttrFloat:
		r.Value = a.F
	case AttrBool:
		r.Value = a.B
	case AttrString:
		r.Value = a.S
	}
	return r
}

// attrsToRaw returns the YAML form of a whole set, or nil when it is empty so
// the `attributes:` key is omitted entirely rather than written as `{}`.
func attrsToRaw(s AttrSet) map[string]RawAttribute {
	if len(s) == 0 {
		return nil
	}
	out := make(map[string]RawAttribute, len(s))
	for k, v := range s {
		out[k] = attrToRaw(v)
	}
	return out
}
