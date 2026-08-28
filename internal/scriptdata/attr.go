package scriptdata

// Package scriptdata is the tags/attributes/script/params triple that both a
// level object and a UI widget carry, and the four scalar values an attribute
// can hold.
//
// It sits at the bottom of the tree because both halves of the engine share it:
// internal/world puts it on entities, volumes, sectors and walls, and
// internal/ui puts it on screens and widgets. A shared helper belongs below
// everything that shares it, and this one was in internal/world only because
// that was the only thing that needed it at the time.
//
// attr.go holds the values and the sets of them; attr_yaml.go holds the block
// they are written as, which came along for the same reason — a .fui writes the
// same block a level file does, and two implementations of it would drift.

import (
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

// AttrTypeNames is every valid AttrType, in the order the editor's type combo
// offers them. Adding a type means adding it here and to the switches below.
var AttrTypeNames = []AttrType{AttrInt, AttrFloat, AttrBool, AttrString}

// validAttrType reports whether t is one of the four.
// ValidAttrType reports whether a type name is one of the four.
func ValidAttrType(t AttrType) bool {
	for _, n := range AttrTypeNames {
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

// ZeroAttr returns the zero value of a type — what a newly added attribute
// starts as, and what a malformed one falls back to.
func ZeroAttr(t AttrType) AttrValue {
	if !ValidAttrType(t) {
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

// ConvertAttr re-types a value, carrying as much of it across as the new type
// can hold. Used by the editor when the user changes an attribute's type: the
// alternative is resetting to zero, which loses a number the user just typed
// merely because they picked float after int.
func ConvertAttr(a AttrValue, to AttrType) AttrValue {
	if !ValidAttrType(to) {
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
	return ZeroAttr(to)
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
// MergeAttrs overlays one set on another, leaving both untouched.
func MergeAttrs(base, over AttrSet) AttrSet {
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
