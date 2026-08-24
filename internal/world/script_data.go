package world

// script_data.go — the script-facing state every addressable world object
// carries, and its YAML counterpart.
//
// An entity has had tags and attributes since entities existed, because an
// entity was the only thing a script was ever going to talk to. Scripting
// widens that: a door is a wall, a trigger is a volume, a room's lighting cue
// belongs to the sector, and each of those needs somewhere to put a name, a
// classification, and the handful of values its behaviour turns on.
//
// Rather than adding three fields to eight structs, geometry types embed
// ScriptData and the Raw types embed RawScriptData inline, so the YAML shape is
// identical everywhere — `tags:`, `attributes:` and `script:` mean the same
// thing on a sector as on a wall.
//
// Entity is deliberately *not* converted to this. Its tags and attributes have
// merge semantics of their own (a definition's, unioned and overlaid by an
// instance's) that EntityToRaw depends on to write only the difference; folding
// them into a shared struct would tangle that. An entity gains `Script` and
// nothing else.

// ScriptData is the runtime half: what a script can address on an object.
type ScriptData struct {
	// Tags classify the object. `#tag` addresses every object carrying one, and
	// unlike entity tags these are authored directly rather than inherited.
	Tags []string
	// Attributes are user-defined values the engine only stores. Same four types
	// as an entity's, so one accessor serves everything.
	Attributes AttrSet
	// Script is the .flang file driving this object's behaviour, relative to the
	// scripts root. Empty means the object has no behaviour of its own — which is
	// the case for nearly every wall in a level.
	Script string
	// ScriptParams answers the script's top-level `param` declarations — see
	// script_params.go. Separate from Attributes because the two are different
	// things: an attribute is state the script owns and writes, a param is
	// configuration the *level* owns and the script only reads. Sharing one map
	// would mean a script could overwrite its own wiring.
	//
	// A `ref` param is stored as the ID of the object it names, for the same
	// reason script_ref.go stores names rather than pointers: an index or a
	// pointer does not survive an undo.
	ScriptParams AttrSet
}

// RawScriptData is the YAML half, embedded inline so it contributes its three
// keys directly to whatever object contains it.
type RawScriptData struct {
	Tags         []string                `yaml:"tags,omitempty"`
	Attributes   map[string]RawAttribute `yaml:"attributes,omitempty"`
	Script       string                  `yaml:"script,omitempty"`
	ScriptParams map[string]RawAttribute `yaml:"params,omitempty"`
}

// ScriptDataFromRaw converts the YAML form, normalising tags the same way an
// entity's are so membership tests and saved files are both stable.
//
// A malformed attribute is reported rather than dropped: silently losing a value
// a level author wrote is worse than refusing to load the level.
func ScriptDataFromRaw(r RawScriptData, what string) (ScriptData, error) {
	attrs, err := attrsFromRaw(r.Attributes)
	if err != nil {
		return ScriptData{}, wrapScriptDataErr(what, err)
	}
	params, err := attrsFromRaw(r.ScriptParams)
	if err != nil {
		return ScriptData{}, wrapScriptDataErr(what, err)
	}
	return ScriptData{
		Tags:         NormalizeTags(r.Tags),
		Attributes:   attrs,
		Script:       r.Script,
		ScriptParams: params,
	}, nil
}

// ScriptDataToRaw is the inverse. Everything is omitempty, so an object nobody
// has scripted or tagged adds nothing to the file.
func ScriptDataToRaw(d ScriptData) RawScriptData {
	return RawScriptData{
		Tags:         append([]string(nil), d.Tags...),
		Attributes:   attrsToRaw(d.Attributes),
		Script:       d.Script,
		ScriptParams: attrsToRaw(d.ScriptParams),
	}
}

// CloneScriptData returns a deep copy, so a cloned chunk's tags and attributes
// are not shared with the original's.
func CloneScriptData(d ScriptData) ScriptData {
	return ScriptData{
		Tags:         append([]string(nil), d.Tags...),
		Attributes:   d.Attributes.Clone(),
		Script:       d.Script,
		ScriptParams: d.ScriptParams.Clone(),
	}
}

// HasTag reports whether the object carries tag. Tags are sorted by
// NormalizeTags, but the sets are small enough that a scan beats a search.
func (d *ScriptData) HasTag(tag string) bool {
	for _, t := range d.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// AddTag adds tag if it is not already present, keeping the list normalised.
func (d *ScriptData) AddTag(tag string) {
	if tag == "" || d.HasTag(tag) {
		return
	}
	d.Tags = NormalizeTags(append(d.Tags, tag))
}

// RemoveTag drops tag, reporting whether it was there.
func (d *ScriptData) RemoveTag(tag string) bool {
	for i, t := range d.Tags {
		if t == tag {
			d.Tags = append(d.Tags[:i], d.Tags[i+1:]...)
			return true
		}
	}
	return false
}

// Attr reads one attribute.
func (d *ScriptData) Attr(name string) (AttrValue, bool) {
	return d.Attributes.Attr(name)
}

// SetAttr writes one attribute, creating the set if the object had none.
func (d *ScriptData) SetAttr(name string, v AttrValue) {
	if name == "" {
		return
	}
	if d.Attributes == nil {
		d.Attributes = AttrSet{}
	}
	d.Attributes[name] = v
}

// DeleteAttr removes one attribute, reporting whether it was there.
func (d *ScriptData) DeleteAttr(name string) bool {
	if _, ok := d.Attributes[name]; !ok {
		return false
	}
	delete(d.Attributes, name)
	return true
}

// ScriptParam reads one script parameter's bound value.
func (d *ScriptData) ScriptParam(name string) (AttrValue, bool) {
	return d.ScriptParams.Attr(name)
}

// SetScriptParam binds one script parameter, creating the set if the object had
// none.
func (d *ScriptData) SetScriptParam(name string, v AttrValue) {
	if name == "" {
		return
	}
	if d.ScriptParams == nil {
		d.ScriptParams = AttrSet{}
	}
	d.ScriptParams[name] = v
}

// wrapScriptDataErr adds the object's description to an attribute error, so a
// bad value names what carried it rather than just itself.
func wrapScriptDataErr(what string, err error) error {
	if what == "" {
		return err
	}
	return &scriptDataError{what: what, err: err}
}

type scriptDataError struct {
	what string
	err  error
}

func (e *scriptDataError) Error() string { return e.what + ": " + e.err.Error() }
func (e *scriptDataError) Unwrap() error { return e.err }
