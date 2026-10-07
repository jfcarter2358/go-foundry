package world

// entity_index.go — FindEntity's ID → index map.
//
// FindEntity used to be a linear scan of Level.Entities, and it is called per
// frame from everywhere a body refers to another by ID: frame refresh, rest
// tracking, vehicle mounts, attachments, waypoint followers, every AI agent's
// target and line-of-sight ignores. With a few hundred entities that is tens of
// thousands of string compares a frame.
//
// The map is derived data with no owner to keep it in step: entities are
// appended and removed by the loader, the prefab expander, script spawn and
// destroy, save-game restore and the editor, each writing Level.Entities
// directly. So rather than ask every one of those sites to invalidate it, the
// index validates itself on every lookup, and is only ever trusted to say
// "here", never "nowhere":
//
//   - it records the slice it was built from (address of the first element and
//     the length). Any append or removal changes the length; a reallocation or a
//     wholesale replacement changes the address. Either makes it stale, and the
//     lookup rebuilds it first.
//   - a hit is spot-checked: the entity at the recorded index must carry the ID
//     asked for. A reorder or in-place rename that kept the slice header fails
//     that check and falls back to the scan.
//   - a miss always falls back to the scan, so an entity renamed in place since
//     the build is still found.
//
// The one thing a spot-checked hit cannot see is a *second* entity with the
// same ID earlier in the array — FindEntity's definition is "the first match".
// IDs are unique by construction (every insertion goes through UniqueEntityID),
// and an ID that occurs twice when the index is built is left out of the map,
// so it always takes the scan.
//
// Lookups come from parallel phases (AI sensing, the entity update), so the
// index is immutable once built and published through an atomic pointer; a
// stale one is replaced, never edited. Building only reads Entities, which
// nothing writes while those phases run.

import (
	"sync/atomic"
	"unsafe"
)

// entityIndex is one immutable build of the map.
type entityIndex struct {
	// base is &Entities[0] at build time, nil for an empty slice. Holding it
	// also keeps that array alive, so a later slice can never be allocated at
	// the same address and pass for the one this was built from.
	base *Entity
	n    int
	ids  map[string]int32
}

// entityIndexSlot is the Level field holding the current build. A raw pointer
// read and written through sync/atomic rather than an atomic.Pointer, because
// Level is copied by value (the editor installs levels that way, harness check
// too) and atomic.Pointer forbids copies. A copy is harmless: it shares the
// Entities slice the build was validated against, and either copy rebuilds its
// own as soon as its slice diverges.
type entityIndexSlot struct{ p unsafe.Pointer }

func (s *entityIndexSlot) load() *entityIndex {
	return (*entityIndex)(atomic.LoadPointer(&s.p))
}

// sliceBase returns the address of the slice's first element, or nil.
func sliceBase(es []Entity) *Entity {
	if len(es) == 0 {
		return nil
	}
	return unsafe.SliceData(es)
}

// find answers id from the index when the index is valid and has it. ok false
// means "ask the scan" — never "absent".
func (s *entityIndexSlot) find(lvl *Level, id string) (*Entity, bool) {
	idx := s.load()
	if idx == nil || idx.n != len(lvl.Entities) || idx.base != sliceBase(lvl.Entities) {
		idx = s.rebuild(lvl)
	}
	i, ok := idx.ids[id]
	if !ok || int(i) >= len(lvl.Entities) || lvl.Entities[i].ID != id {
		return nil, false
	}
	return &lvl.Entities[i], true
}

// rebuild indexes Entities as they stand and publishes the result.
func (s *entityIndexSlot) rebuild(lvl *Level) *entityIndex {
	idx := &entityIndex{
		base: sliceBase(lvl.Entities),
		n:    len(lvl.Entities),
		ids:  make(map[string]int32, len(lvl.Entities)),
	}
	var dup map[string]bool
	for i := range lvl.Entities {
		id := lvl.Entities[i].ID
		if _, seen := idx.ids[id]; seen {
			if dup == nil {
				dup = map[string]bool{}
			}
			dup[id] = true
			continue
		}
		idx.ids[id] = int32(i)
	}
	for id := range dup {
		delete(idx.ids, id)
	}
	atomic.StorePointer(&s.p, unsafe.Pointer(idx))
	return idx
}
