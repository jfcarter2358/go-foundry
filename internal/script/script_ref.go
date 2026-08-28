package script

// script_ref.go — turning a flang ref into something in the level, and back.
//
// A ref is a path of stable names, never a pointer and never an index. That is
// not a stylistic choice: `Level.Entities` is a slice of values, so every
// `*Entity` dies on the next append, and `restoreLevelSnapshot` rebuilds the
// entire level from RawLevel — so one Ctrl+Z invalidates every pointer, index
// and generation counter in the world at once. A handle scheme that cannot
// survive undo is a handle scheme that breaks in the editor.
//
// Names cost a lookup. That is what refCache is for: a per-tick index rebuilt at
// the top of every ScriptTick and thrown away at the end, never carried across
// frames where it could go stale.

import (
	"foundry/internal/flang"
	"foundry/internal/world"
)

// refCache indexes a level by the names refs are made of. Valid for exactly one
// tick — anything structural invalidates it, and rebuilding is cheaper than
// reasoning about when it did not.
type refCache struct {
	lvl      *world.Level
	entity   map[string]int    // entity ID -> index into lvl.Entities
	volume   map[string]int    // volume ID -> index into lvl.Volumes
	sprite   map[string]int    // sprite ID -> index into lvl.Sprites
	emitter  map[string]int    // emitter ID -> index into lvl.Emitters
	sound    map[string]int    // sound ID -> index into lvl.Sounds
	chunk    map[string]int    // chunk ID -> index into lvl.Chunks
	sector   map[string][2]int // sector ID -> {chunk, sector}
	biome    map[string][2]int // biome sector ID -> {chunk, biome}
	dirLight map[string]int
	ptLight  map[string]int
}

// buildRefCache indexes lvl. Cheap enough to do every tick at the entity counts
// a level holds.
func buildRefCache(lvl *world.Level) *refCache {
	c := &refCache{
		lvl:      lvl,
		entity:   map[string]int{},
		volume:   map[string]int{},
		sprite:   map[string]int{},
		emitter:  map[string]int{},
		sound:    map[string]int{},
		chunk:    map[string]int{},
		sector:   map[string][2]int{},
		biome:    map[string][2]int{},
		dirLight: map[string]int{},
		ptLight:  map[string]int{},
	}
	if lvl == nil {
		return c
	}
	for i := range lvl.Entities {
		c.entity[lvl.Entities[i].ID] = i
	}
	for i := range lvl.Volumes {
		c.volume[lvl.Volumes[i].ID] = i
	}
	for i := range lvl.Sprites {
		c.sprite[lvl.Sprites[i].ID] = i
	}
	for i := range lvl.Emitters {
		c.emitter[lvl.Emitters[i].ID] = i
	}
	for i := range lvl.Sounds {
		c.sound[lvl.Sounds[i].ID] = i
	}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		c.chunk[ch.ID] = ci
		for si := range ch.Sectors {
			c.sector[ch.Sectors[si].ID] = [2]int{ci, si}
		}
		for bi := range ch.BiomeSectors {
			c.biome[ch.BiomeSectors[bi].ID] = [2]int{ci, bi}
		}
	}
	for i := range lvl.Lights.Directional {
		c.dirLight[lvl.Lights.Directional[i].ID] = i
	}
	for i := range lvl.Lights.Points {
		c.ptLight[lvl.Lights.Points[i].ID] = i
	}
	return c
}

// ---------- name to ref ----------

// refFor resolves an ID to a ref, searching every addressable kind.
//
// One name means one object across all of them — uniqueLevelID (identity.go)
// enforces that — so the search order only decides which lookup runs first, not
// which object wins.
func (c *refCache) refFor(id string) (flang.RefVal, bool) {
	if id == "" {
		return flang.RefVal{}, false
	}
	if _, ok := c.entity[id]; ok {
		return flang.RefVal{Kind: flang.RefEntity, A: id}, true
	}
	if _, ok := c.volume[id]; ok {
		return flang.RefVal{Kind: flang.RefVolume, A: id}, true
	}
	if _, ok := c.sprite[id]; ok {
		return flang.RefVal{Kind: flang.RefSprite, A: id}, true
	}
	if _, ok := c.emitter[id]; ok {
		return flang.RefVal{Kind: flang.RefEmitter, A: id}, true
	}
	if _, ok := c.sound[id]; ok {
		return flang.RefVal{Kind: flang.RefSound, A: id}, true
	}
	if idx, ok := c.sector[id]; ok {
		return flang.RefVal{Kind: flang.RefSector, A: c.lvl.Chunks[idx[0]].ID, B: id}, true
	}
	if _, ok := c.chunk[id]; ok {
		return flang.RefVal{Kind: flang.RefChunk, A: id}, true
	}
	if _, ok := c.dirLight[id]; ok {
		return flang.RefVal{Kind: flang.RefLight, A: id}, true
	}
	if _, ok := c.ptLight[id]; ok {
		return flang.RefVal{Kind: flang.RefLight, A: id}, true
	}
	if idx, ok := c.biome[id]; ok {
		return flang.RefVal{Kind: flang.RefBiome, A: c.lvl.Chunks[idx[0]].ID, B: id}, true
	}
	return flang.RefVal{}, false
}

// ---------- ref to object ----------

// entity returns the entity a ref names, or nil.
func (c *refCache) entityFor(r flang.RefVal) *world.Entity {
	if c.lvl == nil || r.Kind != flang.RefEntity {
		return nil
	}
	if i, ok := c.entity[r.A]; ok && i < len(c.lvl.Entities) {
		return &c.lvl.Entities[i]
	}
	return nil
}

// volumeFor returns the volume a ref names, or nil.
func (c *refCache) volumeFor(r flang.RefVal) *world.Volume {
	if c.lvl == nil || r.Kind != flang.RefVolume {
		return nil
	}
	if i, ok := c.volume[r.A]; ok && i < len(c.lvl.Volumes) {
		return &c.lvl.Volumes[i]
	}
	return nil
}

// chunkFor returns the chunk a ref names, or nil. Sector, edge, floor, ceiling
// and biome refs all carry their chunk in A, so this serves them too.
// spriteFor resolves a sprite ref to the sprite itself.
func (c *refCache) spriteFor(r flang.RefVal) *world.Sprite {
	if c.lvl == nil || r.Kind != flang.RefSprite {
		return nil
	}
	if i, ok := c.sprite[r.A]; ok && i < len(c.lvl.Sprites) {
		return &c.lvl.Sprites[i]
	}
	return nil
}

// emitterFor resolves an emitter ref to the emitter itself.
func (c *refCache) emitterFor(r flang.RefVal) *world.Emitter {
	if c.lvl == nil || r.Kind != flang.RefEmitter {
		return nil
	}
	if i, ok := c.emitter[r.A]; ok && i < len(c.lvl.Emitters) {
		return &c.lvl.Emitters[i]
	}
	return nil
}

// soundFor resolves a sound ref to the sound itself.
func (c *refCache) soundFor(r flang.RefVal) *world.Sound {
	if c.lvl == nil || r.Kind != flang.RefSound {
		return nil
	}
	if i, ok := c.sound[r.A]; ok && i < len(c.lvl.Sounds) {
		return &c.lvl.Sounds[i]
	}
	return nil
}

func (c *refCache) chunkFor(r flang.RefVal) *world.Chunk {
	if c.lvl == nil {
		return nil
	}
	if i, ok := c.chunk[r.A]; ok && i < len(c.lvl.Chunks) {
		return &c.lvl.Chunks[i]
	}
	return nil
}

// sectorFor returns the sector a ref names, along with its chunk.
func (c *refCache) sectorFor(r flang.RefVal) (*world.Chunk, *world.Sector) {
	if c.lvl == nil {
		return nil, nil
	}
	id := r.B
	if r.Kind == flang.RefSector && id == "" {
		id = r.A
	}
	idx, ok := c.sector[id]
	if !ok || idx[0] >= len(c.lvl.Chunks) {
		return nil, nil
	}
	ch := &c.lvl.Chunks[idx[0]]
	if idx[1] >= len(ch.Sectors) {
		return nil, nil
	}
	return ch, &ch.Sectors[idx[1]]
}

// biomeFor returns the biome sector a ref names.
func (c *refCache) biomeFor(r flang.RefVal) *world.BiomeSector {
	if c.lvl == nil || r.Kind != flang.RefBiome {
		return nil
	}
	idx, ok := c.biome[r.B]
	if !ok || idx[0] >= len(c.lvl.Chunks) {
		return nil
	}
	ch := &c.lvl.Chunks[idx[0]]
	if idx[1] >= len(ch.BiomeSectors) {
		return nil
	}
	return &ch.BiomeSectors[idx[1]]
}

// lightFor returns the light a ref names. The two kinds are returned separately
// because a directional light and a point light share only some properties.
func (c *refCache) lightFor(r flang.RefVal) (*world.DirectionalLight, *world.PointLight) {
	if c.lvl == nil || r.Kind != flang.RefLight {
		return nil, nil
	}
	if i, ok := c.dirLight[r.A]; ok && i < len(c.lvl.Lights.Directional) {
		return &c.lvl.Lights.Directional[i], nil
	}
	if i, ok := c.ptLight[r.A]; ok && i < len(c.lvl.Lights.Points) {
		return nil, &c.lvl.Lights.Points[i]
	}
	return nil, nil
}

// scriptDataFor returns the tags/attributes/script an object carries, or nil
// when the ref names something that has none.
//
// This is the one place the eight scriptable kinds are enumerated against their
// storage, so adding a ninth means adding it here and to refFor.
func (c *refCache) scriptDataFor(r flang.RefVal) *world.ScriptData {
	switch r.Kind {
	case flang.RefVolume:
		if v := c.volumeFor(r); v != nil {
			return &v.ScriptData
		}
	case flang.RefSprite:
		if sp := c.spriteFor(r); sp != nil {
			return &sp.ScriptData
		}
	case flang.RefEmitter:
		if e := c.emitterFor(r); e != nil {
			return &e.ScriptData
		}
	case flang.RefSound:
		if sd := c.soundFor(r); sd != nil {
			return &sd.ScriptData
		}
	case flang.RefSector:
		if _, s := c.sectorFor(r); s != nil {
			return &s.ScriptData
		}
	case flang.RefFloor:
		if _, s := c.sectorFor(r); s != nil {
			return &s.Floor.ScriptData
		}
	case flang.RefCeiling:
		if _, s := c.sectorFor(r); s != nil {
			return &s.Ceiling.ScriptData
		}
	case flang.RefEdge:
		if _, s := c.sectorFor(r); s != nil {
			if w := world.WallByEdgeID(s, r.C); w != nil {
				return &w.ScriptData
			}
			if p := world.PortalByEdgeID(s, r.C); p != nil {
				return &p.ScriptData
			}
		}
	case flang.RefChunk:
		if ch := c.chunkFor(r); ch != nil {
			return &ch.ScriptData
		}
	case flang.RefLight:
		if d, p := c.lightFor(r); d != nil {
			return &d.ScriptData
		} else if p != nil {
			return &p.ScriptData
		}
	}
	return nil
}

// alive reports whether a ref still names something.
func (c *refCache) alive(r flang.RefVal) bool {
	switch r.Kind {
	case flang.RefLevel:
		return c.lvl != nil
	case flang.RefEntity:
		return c.entityFor(r) != nil
	case flang.RefVolume:
		return c.volumeFor(r) != nil
	case flang.RefSprite:
		return c.spriteFor(r) != nil
	case flang.RefEmitter:
		return c.emitterFor(r) != nil
	case flang.RefSound:
		return c.soundFor(r) != nil
	case flang.RefChunk:
		return c.chunkFor(r) != nil
	case flang.RefSector, flang.RefFloor, flang.RefCeiling:
		_, s := c.sectorFor(r)
		return s != nil
	case flang.RefEdge:
		_, s := c.sectorFor(r)
		return s != nil && world.PolyEdgeIdxByID(s, r.C) >= 0
	case flang.RefBiome:
		return c.biomeFor(r) != nil
	case flang.RefLight:
		d, p := c.lightFor(r)
		return d != nil || p != nil
	}
	return false
}

// ---------- tags ----------

// refsWithTag returns every object carrying tag, in a deterministic order:
// entities, then volumes, then geometry in level order. Determinism matters
// because a broadcast's delivery order follows this one, and script behaviour
// that changed between runs would be untestable.
func (c *refCache) refsWithTag(tag string) []flang.RefVal {
	if c.lvl == nil || tag == "" {
		return nil
	}
	var out []flang.RefVal
	for i := range c.lvl.Entities {
		if c.lvl.Entities[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefEntity, A: c.lvl.Entities[i].ID})
		}
	}
	for i := range c.lvl.Volumes {
		if c.lvl.Volumes[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefVolume, A: c.lvl.Volumes[i].ID})
		}
	}
	for i := range c.lvl.Sprites {
		if c.lvl.Sprites[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefSprite, A: c.lvl.Sprites[i].ID})
		}
	}
	for i := range c.lvl.Emitters {
		if c.lvl.Emitters[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefEmitter, A: c.lvl.Emitters[i].ID})
		}
	}
	for i := range c.lvl.Sounds {
		if c.lvl.Sounds[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefSound, A: c.lvl.Sounds[i].ID})
		}
	}
	for ci := range c.lvl.Chunks {
		ch := &c.lvl.Chunks[ci]
		if ch.HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefChunk, A: ch.ID})
		}
		for si := range ch.Sectors {
			s := &ch.Sectors[si]
			if s.HasTag(tag) {
				out = append(out, flang.RefVal{Kind: flang.RefSector, A: ch.ID, B: s.ID})
			}
			if s.Floor.HasTag(tag) {
				out = append(out, flang.RefVal{Kind: flang.RefFloor, A: ch.ID, B: s.ID})
			}
			if s.Ceiling.HasTag(tag) {
				out = append(out, flang.RefVal{Kind: flang.RefCeiling, A: ch.ID, B: s.ID})
			}
			world.EnsureEdgeIDs(s)
			for ei, id := range s.EdgeIDs {
				kind, idx := world.EdgeKindAndIndex(s, ei)
				var d *world.ScriptData
				if kind == "wall" && idx < len(s.Walls) {
					d = &s.Walls[idx].ScriptData
				} else if kind == "portal" && idx < len(s.Portals) {
					d = &s.Portals[idx].ScriptData
				}
				if d != nil && d.HasTag(tag) {
					out = append(out, flang.RefVal{Kind: flang.RefEdge, A: ch.ID, B: s.ID, C: id})
				}
			}
		}
	}
	for i := range c.lvl.Lights.Directional {
		if c.lvl.Lights.Directional[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefLight, A: c.lvl.Lights.Directional[i].ID})
		}
	}
	for i := range c.lvl.Lights.Points {
		if c.lvl.Lights.Points[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefLight, A: c.lvl.Lights.Points[i].ID})
		}
	}
	return out
}

// allTags returns every tag any object in the level carries, sorted and without
// repeats. Used only for "did you mean" on a `find` that matched nothing, so it
// walks the level rather than keeping an index nothing else would read.
func (c *refCache) allTags() []string {
	if c.lvl == nil {
		return nil
	}
	seen := map[string]bool{}
	add := func(tags []string) {
		for _, t := range tags {
			seen[t] = true
		}
	}
	for i := range c.lvl.Entities {
		add(c.lvl.Entities[i].Tags)
	}
	for i := range c.lvl.Volumes {
		add(c.lvl.Volumes[i].Tags)
	}
	for i := range c.lvl.Sprites {
		add(c.lvl.Sprites[i].Tags)
	}
	for i := range c.lvl.Emitters {
		add(c.lvl.Emitters[i].Tags)
	}
	for i := range c.lvl.Sounds {
		add(c.lvl.Sounds[i].Tags)
	}
	for ci := range c.lvl.Chunks {
		ch := &c.lvl.Chunks[ci]
		add(ch.Tags)
		for si := range ch.Sectors {
			s := &ch.Sectors[si]
			add(s.Tags)
			add(s.Floor.Tags)
			add(s.Ceiling.Tags)
			for i := range s.Walls {
				add(s.Walls[i].Tags)
			}
			for i := range s.Portals {
				add(s.Portals[i].Tags)
			}
		}
	}
	for i := range c.lvl.Lights.Directional {
		add(c.lvl.Lights.Directional[i].Tags)
	}
	for i := range c.lvl.Lights.Points {
		add(c.lvl.Lights.Points[i].Tags)
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sortStringsAsc(out)
	return out
}

// ---------- attribute conversion ----------

// attrToValue converts a stored attribute into a script value. The four types
// line up exactly, which is why entity_attr.go chose them.
func attrToValue(a world.AttrValue) flang.Value {
	switch a.Type {
	case world.AttrInt:
		return flang.IntVal(a.I)
	case world.AttrFloat:
		return flang.FloatVal(a.F)
	case world.AttrBool:
		return flang.BoolVal(a.B)
	default:
		return flang.StrVal(a.S)
	}
}

// valueToAttr converts a script value into a stored attribute.
//
// Only the four scalars can be stored: a level file has no way to write a list
// or a ref, so accepting one here would produce an attribute that could not be
// saved. A script wanting compound state uses `@self.state`, which is transient
// by design.
func valueToAttr(v flang.Value) (world.AttrValue, bool) {
	switch t := v.(type) {
	case flang.IntVal:
		return world.IntAttr(int64(t)), true
	case flang.FloatVal:
		return world.FloatAttr(float64(t)), true
	case flang.BoolVal:
		return world.BoolAttr(bool(t)), true
	case flang.StrVal:
		return world.StringAttr(string(t)), true
	case flang.AtomVal:
		// An atom stores as its name, so `:open:` round-trips as "open" and a
		// level author can see what it was.
		return world.StringAttr(string(t)), true
	}
	return world.AttrValue{}, false
}
