package script

// script_complete.go — the level-side half of the script editor's completion.
//
// The editor knows what a script *says*; only the engine knows what the level
// *holds*. `@` is worth completing precisely because the answer cannot be read
// off the buffer — the ids, the tags and the attributes are all authored
// somewhere else — so the three lists live here, beside the refCache that
// already resolves them, rather than in the editor that shows them.
//
// This is the same argument builtin.go makes for BuiltinNames and
// script_props.go for PropertyNames: the thing that owns a set exports it, and
// the editor keeps no copy that could fall behind.
//
// All three are snapshots. They are read when a completion list opens and not
// held, because a level being edited beside the script editor changes under
// them constantly.

import (
	"sort"

	"foundry/internal/flang"
	"foundry/internal/ui"
	"foundry/internal/world"
)

// CompletionRef is one object a script can address by name: the id `@name`
// would use, and what kind of thing it turns out to be.
//
// The kind is carried because it is what makes the *next* keystroke completable:
// knowing `@door` is an edge is what lets `.prop.` offer an edge's properties
// rather than the union of everything.
type CompletionRef struct {
	ID   string
	Kind flang.Kind_
}

// CompletionRefs lists every object in lvl that `@name` reaches, sorted by id.
//
// The set is deliberately the same one refFor searches, in the same order, so
// that a name offered here always resolves to the kind offered with it. A
// widget id that two visible screens share is left out for the reason refFor
// refuses it: it does not name one object.
func CompletionRefs(lvl *world.Level) []CompletionRef {
	c := buildRefCache(lvl)
	seen := map[string]bool{}
	var out []CompletionRef
	add := func(id string, kind flang.Kind_) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, CompletionRef{ID: id, Kind: kind})
	}

	if lvl != nil {
		for i := range lvl.Entities {
			add(lvl.Entities[i].ID, flang.RefEntity)
		}
		for i := range lvl.Volumes {
			add(lvl.Volumes[i].ID, flang.RefVolume)
		}
		for i := range lvl.Sprites {
			add(lvl.Sprites[i].ID, flang.RefSprite)
		}
		for i := range lvl.Emitters {
			add(lvl.Emitters[i].ID, flang.RefEmitter)
		}
		for i := range lvl.Sounds {
			add(lvl.Sounds[i].ID, flang.RefSound)
		}
		for i := range lvl.Effects {
			add(lvl.Effects[i].ID, flang.RefEffect)
		}
		for i := range lvl.Decals {
			add(lvl.Decals[i].ID, flang.RefDecal)
		}
		for i := range lvl.WaypointAnimations {
			add(lvl.WaypointAnimations[i].ID, flang.RefWaypoints)
		}
		for ci := range lvl.Chunks {
			ch := &lvl.Chunks[ci]
			add(ch.ID, flang.RefChunk)
			for bi := range ch.Brushes {
				add(ch.Brushes[bi].ID, flang.RefBrush)
			}
		}
		for i := range lvl.Lights.Directional {
			add(lvl.Lights.Directional[i].ID, flang.RefLight)
		}
		for i := range lvl.Lights.Points {
			add(lvl.Lights.Points[i].ID, flang.RefLight)
		}
	}

	if c.ui != nil {
		for _, s := range c.ui.Screens() {
			add(s.ID, flang.RefScreen)
		}
		for _, s := range c.ui.Screens() {
			s.Root.Walk(func(_, n *ui.Node) bool {
				// Ambiguous widget ids are skipped rather than offered: `@ok`
				// with two screens up is refused at runtime, and completing to
				// it would be offering a null.
				if n.ID != "" && !c.uiRefIsAmbiguous(n.ID) {
					add(n.ID, flang.RefWidget)
				}
				return true
			})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CompletionTags lists every tag anything in lvl carries, sorted — the set
// `#tag` can usefully name.
func CompletionTags(lvl *world.Level) []string {
	return buildRefCache(lvl).allTags()
}

// CompletionAttrs lists the attribute names one object carries, sorted.
//
// Attributes are not declared anywhere a script can see, so this is the only
// way `@barrel.attr.` can offer anything at all. An object with none, or a name
// that resolves to nothing, yields nothing — which is correct rather than
// unhelpful: writing an attribute creates it, so an empty list is not a claim
// that no attribute may be written.
func CompletionAttrs(lvl *world.Level, id string) []string {
	c := buildRefCache(lvl)
	r, ok := c.refFor(id)
	if !ok {
		return nil
	}
	if e := c.entityFor(r); e != nil {
		return e.Attributes.AttrNames()
	}
	if d := c.scriptDataFor(r); d != nil {
		return d.Attributes.AttrNames()
	}
	return nil
}

// CompletionKindOf reports what kind of object a name reaches, so a `.prop.`
// list can be the one kind's rather than every kind's.
func CompletionKindOf(lvl *world.Level, id string) (flang.Kind_, bool) {
	r, ok := buildRefCache(lvl).refFor(id)
	if !ok {
		return flang.RefNone, false
	}
	return r.Kind, true
}
