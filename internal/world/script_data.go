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
//
// The runtime half moved to internal/scriptdata when UI screens and widgets
// started carrying one; what is left here is the YAML shape, which is a
// level-file concern rather than a shared one.

import "foundry/internal/scriptdata"

// ScriptData is re-exported from internal/scriptdata, where the runtime half
// moved when UI screens and widgets started carrying one too. An alias rather
// than a defined type, so world.ScriptData and scriptdata.ScriptData are the
// same type and no call site anywhere needs a conversion.
type ScriptData = scriptdata.ScriptData

// CloneScriptData returns a deep copy of the script-facing state.
var CloneScriptData = scriptdata.CloneScriptData

// NormalizeTags moved with the tags it normalises. Re-exported because every
// path that sets a tag list already calls it by this name.
var NormalizeTags = scriptdata.NormalizeTags

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
