package scriptdata

// attr_yaml.go — the on-disk shape of an attribute block.
//
//	health: {type: int, value: 25}
//
// Here rather than in internal/world with the level schema, because it is the
// same block in a .fui as in a level file and there is no reason for two of
// them. Nothing in it knows what a level is: it is a conversion between the
// four scalars and whatever yaml.v2 decoded, which is as true of a widget's
// attributes as of an entity's.

import (
	"fmt"
	"sort"
	"strconv"
)

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
func AttrFromRaw(name string, r RawAttribute) (AttrValue, error) {
	t := AttrType(r.Type)
	if r.Type == "" {
		var ok bool
		if t, ok = inferAttrType(r.Value); !ok {
			return AttrValue{}, fmt.Errorf("attribute %q: cannot infer a type from %T; give it an explicit `type`", name, r.Value)
		}
	}
	if !ValidAttrType(t) {
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
		return ZeroAttr(t), nil
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
func AttrsFromRaw(raw map[string]RawAttribute) (AttrSet, error) {
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
		v, err := AttrFromRaw(name, raw[name])
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
func AttrToRaw(a AttrValue) RawAttribute {
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
func AttrsToRaw(s AttrSet) map[string]RawAttribute {
	if len(s) == 0 {
		return nil
	}
	out := make(map[string]RawAttribute, len(s))
	for k, v := range s {
		out[k] = AttrToRaw(v)
	}
	return out
}
