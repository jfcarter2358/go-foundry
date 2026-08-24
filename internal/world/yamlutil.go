package world

// yamlutil.go — the two-way pointer idiom the Raw* schema is built on.
//
// Every optional field in level_schema.go is a pointer, because "absent" and
// "present and false" are different things in a level file: a wall with no
// `collide:` key takes the default, and one with `collide: false` does not.
// These convert between that representation and the plain values the runtime
// types hold.
//
// They live below both the loader and the saver because the types that use
// them — volume.go, entity_def.go, detail_presets.go — are not part of either.

// BoolDefault returns the value of a *bool, defaulting to def when nil.
func BoolDefault(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}

// BoolPtrIfFalse returns a *bool pointer only when v is false (non-default).
// When v is true (the default for DoubleSided), returns nil so the field is
// omitted from YAML via omitempty.
func BoolPtrIfFalse(v bool) *bool {
	if !v {
		return &v
	}
	return nil
}
