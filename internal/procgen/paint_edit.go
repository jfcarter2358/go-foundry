package procgen

// paint_edit.go — renaming and deleting a document's paint layers (the
// Generator editor's Layer menu; docs/guide/36-terrain-paint.md).
//
// A layer is named in two places: its declaration under `paint:`, and every
// mask param that reads it (a scatter rule's paint band, a stage's exclude
// list, a material's paint list), anywhere in any stage's params, list
// items included. Renaming changes both; deleting drops the declaration and
// clears the masks that named it (a stage that read it then reads nothing,
// rather than failing validation over a layer that is gone).
//
// The pixels: a rename stores a copy-on-write clone under the new name
// (paint.Set.CopyAs) and a delete leaves them in Doc.PaintData undeclared,
// so an undo that restores the declaration finds them. An undeclared layer
// is neither read nor saved; its folder beside the .fgen stays on disk
// (as P1's "a layer dropped from paint: keeps its folder").

import (
	"fmt"

	"foundry/internal/procgen/paint"
)

// RenamePaintLayer renames layer from to to: its declaration, its pixels
// and every mask param naming it. It returns how many params it rewrote.
func (d *Doc) RenamePaintLayer(from, to string) (int, error) {
	def := d.PaintDef(from)
	if def == nil {
		return 0, fmt.Errorf("no paint layer %s", from)
	}
	if !paint.ValidName(to) {
		return 0, fmt.Errorf("%q: a layer name is lower-case letters, digits and underscores, starting with a letter", to)
	}
	if to != from && d.PaintDef(to) != nil {
		return 0, fmt.Errorf("there is already a layer %s", to)
	}
	nd := *def
	nd.Name = to
	*def = nd
	if d.PaintData != nil {
		d.PaintData.CopyAs(from, nd)
	}
	return d.rewriteMasks(from, to), nil
}

// DeletePaintLayer drops layer name's declaration and clears every mask
// param naming it; how many it cleared.
func (d *Doc) DeletePaintLayer(name string) (int, error) {
	for i := range d.Paint {
		if d.Paint[i].Name == name {
			d.Paint = append(d.Paint[:i:i], d.Paint[i+1:]...)
			return d.rewriteMasks(name, ""), nil
		}
	}
	return 0, fmt.Errorf("no paint layer %s", name)
}

// PaintLayerUsers is the IDs of the stages whose params name layer name.
func (d *Doc) PaintLayerUsers(name string) []string {
	var out []string
	for i := range d.Stages {
		s := &d.Stages[i]
		k := KindByName(s.Kind)
		if k == nil {
			continue
		}
		for _, n := range MaskRefs(k.Params, s.Params) {
			if n == name {
				out = append(out, s.ID)
				break
			}
		}
	}
	return out
}

// rewriteMasks sets every mask param equal to from to to, in every stage.
func (d *Doc) rewriteMasks(from, to string) int {
	n := 0
	var walk func(defs []ParamDef, p Params)
	walk = func(defs []ParamDef, p Params) {
		for i := range defs {
			def := &defs[i]
			v, ok := p[def.Name]
			if !ok {
				continue
			}
			switch def.Type {
			case ParamMask:
				if v.Str == from {
					v.Str = to
					p[def.Name] = v
					n++
				}
			case ParamList:
				for _, it := range v.Items {
					walk(def.Fields, it)
				}
			}
		}
	}
	for i := range d.Stages {
		if k := KindByName(d.Stages[i].Kind); k != nil && d.Stages[i].Params != nil {
			walk(k.Params, d.Stages[i].Params)
		}
	}
	return n
}
