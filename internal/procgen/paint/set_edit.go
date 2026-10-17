package paint

// set_edit.go — renaming a layer in a set (the editor's Rename layer…).
//
// A rename keeps the old layer where it is and adds a copy-on-write clone
// under the new name: the old one is no longer declared, so nothing reads
// it and a save does not write it, but a document undo that brings the old
// declaration back finds its pixels again (the editor's document undo keeps
// the pixels as they are: paint has its own undo).

// CopyAs is a clone of the layer called from (its tiles shared
// copy-on-write) stored under def's name with def's declaration; an empty
// layer when from holds nothing. A kind or resolution change empties it, as
// Ensure does.
func (s *Set) CopyAs(from string, def LayerDef) *Layer {
	src := s.layers[from]
	if src == nil || src.Def.Kind != def.Kind || src.Def.Res() != def.Res() {
		return s.Ensure(def)
	}
	l := src.Clone()
	l.Def = def
	s.layers[def.Name] = l
	return l
}
