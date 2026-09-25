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

// CopyFloatPtr duplicates an optional float, so a round trip through the Raw
// form never leaves the runtime value and the file's value sharing one word.
//
// Aliasing them would be a quiet bug rather than a loud one: a level's
// `gravity:` written back out as the same pointer the runtime is holding means
// a script writing @level.prop.gravity edits the saved file's copy too, and
// leaving play would put the level back with the value play had left behind.
// nil stays nil, which is what keeps "said nothing" distinct from "said 0".
func CopyFloatPtr(p *float32) *float32 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// FloatPtrIfSet returns a pointer to v, or nil when v is zero — the shape an
// optional YAML key wants, for a field whose zero means "not set".
func FloatPtrIfSet(v float32) *float32 {
	if v == 0 {
		return nil
	}
	return &v
}
