package scriptdata

// scriptdata.go — what a script can address on an object.
//
// Every scriptable thing embeds this: a level's entities, volumes, sectors,
// walls, sprites and emitters, and a UI document's screens and widgets. That is
// why it is here rather than in internal/world — a screen must be able to carry
// tags and a script without importing the level model, and a helper that two
// packages share belongs below both of them.
//
// Entity is deliberately *not* converted to this. Its tags and attributes have
// merge semantics of their own — a definition's, unioned and overlaid by an
// instance's — that EntityToRaw depends on to write only the difference;
// folding them into a shared struct would tangle that.

import "sort"

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

// NormalizeTags sorts, de-duplicates and drops empties. Every path that sets a
// tag list goes through it — loading, merging a definition with an instance,
// the panel — so `#tag` membership never depends on authoring order and a saved
// file never churns.
func NormalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}
