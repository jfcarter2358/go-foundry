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
	"foundry/internal/ui"
	"foundry/internal/world"
)

// refCache indexes a level by the names refs are made of.
//
// It is kept across ticks and checked rather than rebuilt: re-hashing every ID
// in a level of thousands of objects sixty times a second was a millisecond a
// frame spent arriving at the same maps. What makes keeping it safe is that
// every kind's index remembers the ID list it was built from, and is compared
// against the level's — a string compare per object, nearly always of two
// identical pointers — so an add, a removal, a rename or a reorder anywhere is
// still seen, and the kind it touched is re-indexed.
//
// Each kind is checked the first time a tick asks it something (ix), not at
// the top of every tick: a level's three thousand sprites cost a walk only on
// the ticks that name a sprite. sync marks every kind unchecked — at the top of
// each ScriptTick, and after anything structural a verb does mid-tick (spawn,
// destroy, an effect placed).
type refCache struct {
	lvl *world.Level
	// ixs are the per-kind ID -> slice index maps, by ixKind. Reach them
	// through ix, which checks them against the level first.
	ixs [ixCount]idIndex

	// brush is brush ID -> {chunk, brush}. Checked on first use in a tick
	// (brushIndex) rather than in sync: a level has thousands, and most ticks
	// never name one. brushIDs is the flattened chunk-by-chunk ID list it was
	// built from, and brushChecked whether this tick has compared against it.
	brush        map[string][2]int
	brushIDs     []string
	brushChecked bool

	// tags is the tag index — tag -> every object carrying it, in refsWithTag's
	// order — built the first time a tick asks for a tag and dropped by every
	// sync. Not carried across ticks the way the ID indexes are: a save restore
	// rewrites tags in place, under IDs that do not move, so there is no list
	// to compare against that would notice.
	tags map[string][]flang.RefVal

	// missing are the names refFor has already failed to find among the
	// level's objects this tick, so a script asking every frame after one the
	// level does not have — `@gate2` in a level without a gate2 — pays for
	// the dozen lookups once. Dropped by every sync, like tags. Only the
	// level half: the screen stack can change under a tick, so a miss there
	// is asked again each time.
	missing map[string]bool

	// ui is the screen stack as it stood at the top of this tick. Not indexed
	// the way the level kinds are: a stack is a handful of screens with a
	// handful of widgets each, and a map rebuilt every tick over that would
	// cost more than the walk it saved.
	ui *ui.Stack

	// hidden are the screens that left the stack since the previous tick,
	// kept so the :hidden: and :cancel: recorded as they left can still be
	// delivered to them — see ScriptRuntime.hidden. Only a screen ref falls
	// back to these: a widget on a screen that has gone resolves to nothing,
	// as it always did, and nothing here is ticked, because scriptedUIRefs
	// walks the stack rather than this cache.
	hidden []*ui.Screen
}

// ixKind names one of the level's indexed kinds.
type ixKind uint8

const (
	ixEntity   ixKind = iota // lvl.Entities
	ixVolume                 // lvl.Volumes
	ixSprite                 // lvl.Sprites
	ixEmitter                // lvl.Emitters
	ixSound                  // lvl.Sounds
	ixEffect                 // lvl.Effects
	ixDecal                  // lvl.Decals
	ixChunk                  // lvl.Chunks
	ixDirLight               // lvl.Lights.Directional
	ixPtLight                // lvl.Lights.Points
	ixCount
)

// idIndex is one kind's ID -> slice index map, with the ID list it was built
// from so that it can tell whether the level still matches it, and whether it
// has been compared this tick.
type idIndex struct {
	m       map[string]int
	ids     []string
	checked bool
}

// ix returns one kind's index, checked against the level.
func (c *refCache) ix(k ixKind) *idIndex {
	x := &c.ixs[k]
	if !x.checked {
		x.checked = true
		n, id := c.ids(k)
		x.sync(n, id)
	}
	return x
}

// ids is how many objects of a kind the level has, and the ID of each.
func (c *refCache) ids(k ixKind) (int, func(int) string) {
	lvl := c.lvl
	if lvl == nil {
		return 0, nil
	}
	switch k {
	case ixEntity:
		return len(lvl.Entities), func(i int) string { return lvl.Entities[i].ID }
	case ixVolume:
		return len(lvl.Volumes), func(i int) string { return lvl.Volumes[i].ID }
	case ixSprite:
		return len(lvl.Sprites), func(i int) string { return lvl.Sprites[i].ID }
	case ixEmitter:
		return len(lvl.Emitters), func(i int) string { return lvl.Emitters[i].ID }
	case ixSound:
		return len(lvl.Sounds), func(i int) string { return lvl.Sounds[i].ID }
	case ixEffect:
		return len(lvl.Effects), func(i int) string { return lvl.Effects[i].ID }
	case ixDecal:
		return len(lvl.Decals), func(i int) string { return lvl.Decals[i].ID }
	case ixChunk:
		return len(lvl.Chunks), func(i int) string { return lvl.Chunks[i].ID }
	case ixDirLight:
		return len(lvl.Lights.Directional), func(i int) string { return lvl.Lights.Directional[i].ID }
	case ixPtLight:
		return len(lvl.Lights.Points), func(i int) string { return lvl.Lights.Points[i].ID }
	}
	return 0, nil
}

// get looks one ID up.
func (x *idIndex) get(id string) (int, bool) {
	i, ok := x.m[id]
	return i, ok
}

// has reports whether an ID is indexed.
func (x *idIndex) has(id string) bool {
	_, ok := x.m[id]
	return ok
}

// sync brings the index up to n objects whose IDs id(i) returns.
//
// Three cases. Nothing changed — the common one by far — costs a compare per
// object. Objects appended past the end, which is what spawning anything does,
// cost an insert each. Anything else — a removal, a rename, a reorder — starts
// the index over, because a removal shifts every index after it and finding
// which entries moved is the same work as re-inserting them.
//
// A duplicated ID maps to its last occurrence, exactly as filling the map in
// level order always did.
func (x *idIndex) sync(n int, id func(int) string) {
	if x.m == nil {
		x.m = make(map[string]int, n)
	}
	i := 0
	for i < n && i < len(x.ids) && x.ids[i] == id(i) {
		i++
	}
	if i == n && i == len(x.ids) {
		return
	}
	if i < len(x.ids) {
		clear(x.m)
		x.ids = x.ids[:0]
		i = 0
	}
	for ; i < n; i++ {
		s := id(i)
		x.m[s] = i
		x.ids = append(x.ids, s)
	}
}

// appended records that one object with this ID was just appended as the
// n-th. When the index was in step before the append it is one insert; when it
// was not, it is a sync.
func (x *idIndex) appended(n int, id func(int) string) {
	if x.m != nil && len(x.ids) == n-1 {
		s := id(n - 1)
		x.m[s] = n - 1
		x.ids = append(x.ids, s)
		return
	}
	x.sync(n, id)
}

// screenFor returns the screen a ref names, or nil.
func (c *refCache) screenFor(r flang.RefVal) *ui.Screen {
	if c.ui == nil || (r.Kind != flang.RefScreen && r.Kind != flang.RefWidget) {
		return nil
	}
	if s := c.ui.Find(r.A); s != nil {
		return s
	}
	if r.Kind != flang.RefScreen {
		return nil
	}
	// Newest first: a screen shown and hidden twice in one frame is the
	// later of the two instances.
	for i := len(c.hidden) - 1; i >= 0; i-- {
		if c.hidden[i].ID == r.A {
			return c.hidden[i]
		}
	}
	return nil
}

// widgetFor returns the widget a ref names, or nil.
func (c *refCache) widgetFor(r flang.RefVal) *ui.Node {
	if r.Kind != flang.RefWidget {
		return nil
	}
	s := c.screenFor(r)
	if s == nil {
		return nil
	}
	return s.Find(r.B)
}

// uiStack is the screen stack refs resolve against.
//
// A variable rather than a direct reference to ui.Game so a test can point it
// at a stack of its own without those screens leaking into the next test. The
// editor's preview stack is deliberately never this one: scripts do not run
// against a document being authored.
var uiStack = &ui.Game

// buildRefCache returns a fresh cache indexing lvl. The runtime keeps one and
// syncs it instead (ScriptRuntime.syncCache); this is for callers that look a
// level up once — the linter, completion, the console.
func buildRefCache(lvl *world.Level) *refCache {
	c := &refCache{}
	c.sync(lvl)
	return c
}

// sync brings the cache up to date with lvl and with the screen stack.
func (c *refCache) sync(lvl *world.Level) {
	c.lvl = lvl
	c.ui = uiStack
	// From the runtime rather than handed in by ScriptTick: a verb that
	// spawns or destroys something syncs the cache partway through a drain,
	// and a departed screen must survive that.
	c.hidden = Scripts.hidden
	c.tags = nil
	clear(c.missing)
	c.brushChecked = false
	for k := range c.ixs {
		c.ixs[k].checked = false
	}
}

// syncCache brings the runtime's cache up to date with lvl, making one the
// first time.
func (rt *ScriptRuntime) syncCache(lvl *world.Level) {
	if rt.cache == nil {
		rt.cache = &refCache{}
	}
	rt.cache.sync(lvl)
}

// brushIndex is the brush ID -> {chunk, brush} map, checked against the level
// the first time a tick needs it.
func (c *refCache) brushIndex() map[string][2]int {
	if c.lvl == nil {
		return c.brush
	}
	if c.brushChecked && c.brush != nil {
		return c.brush
	}
	c.brushChecked = true
	if c.brush != nil && c.brushesUnchanged() {
		return c.brush
	}
	if c.brush == nil {
		c.brush = map[string][2]int{}
	} else {
		clear(c.brush)
	}
	c.brushIDs = c.brushIDs[:0]
	for ci := range c.lvl.Chunks {
		ch := &c.lvl.Chunks[ci]
		for bi := range ch.Brushes {
			c.brush[ch.Brushes[bi].ID] = [2]int{ci, bi}
			c.brushIDs = append(c.brushIDs, ch.Brushes[bi].ID)
		}
	}
	return c.brush
}

// brushesUnchanged reports whether every chunk still holds the brushes the
// index was built from, in the same places.
//
// The flat ID list alone would not see a brush moving from the end of one chunk
// to the start of the next, so the chunk boundaries are compared too: c.brush
// holds each ID's {chunk, brush}, and the walk checks that it is where the map
// says.
func (c *refCache) brushesUnchanged() bool {
	k := 0
	for ci := range c.lvl.Chunks {
		bs := c.lvl.Chunks[ci].Brushes
		for bi := range bs {
			if k >= len(c.brushIDs) || c.brushIDs[k] != bs[bi].ID {
				return false
			}
			if at := c.brush[bs[bi].ID]; at[0] != ci || at[1] != bi {
				// A duplicated ID: the map holds its last place, so only the
				// last occurrence can agree. Rare enough to just rebuild.
				return false
			}
			k++
		}
	}
	return k == len(c.brushIDs)
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
	if c.missing[id] {
		return c.uiRefOnly(id)
	}
	if r, ok := c.levelRefFor(id); ok {
		return r, true
	}
	if c.missing == nil {
		c.missing = map[string]bool{}
	}
	c.missing[id] = true
	// Biome scatter has no addressable object at the moment — see biomeFor.
	// The UI last, so a level object always wins a name it shares with a
	// widget: the level is the thing an author names deliberately, and a widget
	// id is scoped to its screen in a way a level id is not.
	return c.uiRefOnly(id)
}

// uiRefOnly is uiRefFor with refFor's answer for a miss: the zero ref, whether
// nothing matched or too many widgets did.
func (c *refCache) uiRefOnly(id string) (flang.RefVal, bool) {
	if r, ok := c.uiRefFor(id); ok {
		return r, true
	}
	return flang.RefVal{}, false
}

// levelRefFor is refFor's search of the level's own objects.
func (c *refCache) levelRefFor(id string) (flang.RefVal, bool) {
	if c.ix(ixEntity).has(id) {
		return flang.RefVal{Kind: flang.RefEntity, A: id}, true
	}
	if c.ix(ixVolume).has(id) {
		return flang.RefVal{Kind: flang.RefVolume, A: id}, true
	}
	if c.ix(ixSprite).has(id) {
		return flang.RefVal{Kind: flang.RefSprite, A: id}, true
	}
	if c.ix(ixEmitter).has(id) {
		return flang.RefVal{Kind: flang.RefEmitter, A: id}, true
	}
	if c.ix(ixSound).has(id) {
		return flang.RefVal{Kind: flang.RefSound, A: id}, true
	}
	if c.ix(ixEffect).has(id) {
		return flang.RefVal{Kind: flang.RefEffect, A: id}, true
	}
	if c.ix(ixDecal).has(id) {
		return flang.RefVal{Kind: flang.RefDecal, A: id}, true
	}
	if _, ok := c.brushIndex()[id]; ok {
		// Brush IDs are level-unique (world/identity.go), so a brush ref is a
		// bare name like an entity's — not scoped by its chunk the way a
		// sector's used to be.
		return flang.RefVal{Kind: flang.RefBrush, A: id}, true
	}
	if c.ix(ixChunk).has(id) {
		return flang.RefVal{Kind: flang.RefChunk, A: id}, true
	}
	if c.ix(ixDirLight).has(id) {
		return flang.RefVal{Kind: flang.RefLight, A: id}, true
	}
	if c.ix(ixPtLight).has(id) {
		return flang.RefVal{Kind: flang.RefLight, A: id}, true
	}
	// A waypoint animation is found by walking the list rather than through an
	// index: a level holds a handful, and they are named only when a script
	// sends a brush along one.
	if world.FindWaypointAnimation(c.lvl, id) != nil {
		return flang.RefVal{Kind: flang.RefWaypoints, A: id}, true
	}
	return flang.RefVal{}, false
}

// uiRefFor resolves a bare name against the screen stack: screens first, then
// widgets across every screen that is up.
//
// A widget id that two visible screens both carry is refused rather than
// guessed at. Two prefabs of the same menu are the ordinary case, and picking
// whichever the walk reached first would make @ok mean a different button
// depending on the order the screens were shown in.
func (c *refCache) uiRefFor(id string) (flang.RefVal, bool) {
	if c.ui == nil {
		return flang.RefVal{}, false
	}
	for _, s := range c.ui.Screens() {
		if s.ID == id {
			return flang.RefVal{Kind: flang.RefScreen, A: id}, true
		}
	}

	var found flang.RefVal
	matches := 0
	for _, s := range c.ui.Screens() {
		if s.Find(id) != nil {
			matches++
			if matches == 1 {
				found = flang.RefVal{Kind: flang.RefWidget, A: s.ID, B: id}
			}
		}
	}
	if matches == 1 {
		return found, true
	}
	if matches > 1 {
		return flang.RefVal{Kind: flang.RefNone, A: id}, false
	}
	return flang.RefVal{}, false
}

// uiRefIsAmbiguous reports whether a name matched more than one widget, so the
// caller can say why nothing came back rather than just that nothing did.
func (c *refCache) uiRefIsAmbiguous(id string) bool {
	if c.ui == nil {
		return false
	}
	matches := 0
	for _, s := range c.ui.Screens() {
		if s.Find(id) != nil {
			matches++
		}
	}
	return matches > 1
}

// ---------- ref to object ----------

// entity returns the entity a ref names, or nil.
//
// The player is engine-owned and not in Level.Entities, so it needs the same
// case levelHost.entityFor carries. Both have to have it: the host's is what
// `@player.attr.x` and the movement verbs go through, and THIS one is what
// every property getter and setter goes through (entityProps builds them all
// over a *refCache). Without it `@player.prop.position` — a documented address
// and a documented property — reported "@player has no property", which reads
// as the property being wrong rather than the lookup.
func (c *refCache) entityFor(r flang.RefVal) *world.Entity {
	if r.Kind != flang.RefEntity {
		return nil
	}
	if r.A == "player" {
		return world.PlayerEntity
	}
	if c.lvl == nil {
		return nil
	}
	if i, ok := c.ix(ixEntity).get(r.A); ok && i < len(c.lvl.Entities) {
		return &c.lvl.Entities[i]
	}
	return nil
}

// volumeFor returns the volume a ref names, or nil.
func (c *refCache) volumeFor(r flang.RefVal) *world.Volume {
	if c.lvl == nil || r.Kind != flang.RefVolume {
		return nil
	}
	if i, ok := c.ix(ixVolume).get(r.A); ok && i < len(c.lvl.Volumes) {
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
	if i, ok := c.ix(ixSprite).get(r.A); ok && i < len(c.lvl.Sprites) {
		return &c.lvl.Sprites[i]
	}
	return nil
}

// emitterFor resolves an emitter ref to the emitter itself.
func (c *refCache) emitterFor(r flang.RefVal) *world.Emitter {
	if c.lvl == nil || r.Kind != flang.RefEmitter {
		return nil
	}
	if i, ok := c.ix(ixEmitter).get(r.A); ok && i < len(c.lvl.Emitters) {
		return &c.lvl.Emitters[i]
	}
	return nil
}

// soundFor resolves a sound ref to the sound itself.
func (c *refCache) soundFor(r flang.RefVal) *world.Sound {
	if c.lvl == nil || r.Kind != flang.RefSound {
		return nil
	}
	if i, ok := c.ix(ixSound).get(r.A); ok && i < len(c.lvl.Sounds) {
		return &c.lvl.Sounds[i]
	}
	return nil
}

// decalFor resolves a decal ref to the decal itself.
func (c *refCache) decalFor(r flang.RefVal) *world.Decal {
	if c.lvl == nil || r.Kind != flang.RefDecal {
		return nil
	}
	if i, ok := c.ix(ixDecal).get(r.A); ok && i < len(c.lvl.Decals) {
		return &c.lvl.Decals[i]
	}
	return nil
}

// effectFor resolves an effect ref to the effect itself.
func (c *refCache) effectFor(r flang.RefVal) *world.Effect {
	if c.lvl == nil || r.Kind != flang.RefEffect {
		return nil
	}
	if i, ok := c.ix(ixEffect).get(r.A); ok && i < len(c.lvl.Effects) {
		return &c.lvl.Effects[i]
	}
	return nil
}

// waypointAnimationFor resolves a waypoints ref to the animation itself.
func (c *refCache) waypointAnimationFor(r flang.RefVal) *world.WaypointAnimation {
	if c.lvl == nil || r.Kind != flang.RefWaypoints {
		return nil
	}
	return world.FindWaypointAnimation(c.lvl, r.A)
}

func (c *refCache) chunkFor(r flang.RefVal) *world.Chunk {
	if c.lvl == nil {
		return nil
	}
	if i, ok := c.ix(ixChunk).get(r.A); ok && i < len(c.lvl.Chunks) {
		return &c.lvl.Chunks[i]
	}
	return nil
}

// brushFor returns the brush a ref names.
func (c *refCache) brushFor(r flang.RefVal) *world.Brush {
	ch, b := c.brushOwnerFor(r)
	_ = ch
	return b
}

// brushOwnerFor is brushFor with the chunk the brush belongs to. A brush's
// verts are chunk-local, so anything answering in world space needs the
// placement too.
func (c *refCache) brushOwnerFor(r flang.RefVal) (*world.Chunk, *world.Brush) {
	if c.lvl == nil || r.Kind != flang.RefBrush {
		return nil, nil
	}
	idx, ok := c.brushIndex()[r.A]
	if !ok || idx[0] >= len(c.lvl.Chunks) {
		return nil, nil
	}
	ch := &c.lvl.Chunks[idx[0]]
	if idx[1] >= len(ch.Brushes) {
		return nil, nil
	}
	return ch, &ch.Brushes[idx[1]]
}

// lightFor returns the light a ref names. The two kinds are returned separately
// because a directional light and a point light share only some properties.
func (c *refCache) lightFor(r flang.RefVal) (*world.DirectionalLight, *world.PointLight) {
	if c.lvl == nil || r.Kind != flang.RefLight {
		return nil, nil
	}
	if i, ok := c.ix(ixDirLight).get(r.A); ok && i < len(c.lvl.Lights.Directional) {
		return &c.lvl.Lights.Directional[i], nil
	}
	if i, ok := c.ix(ixPtLight).get(r.A); ok && i < len(c.lvl.Lights.Points) {
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
	case flang.RefEffect:
		if fx := c.effectFor(r); fx != nil {
			return &fx.ScriptData
		}
	case flang.RefDecal:
		if d := c.decalFor(r); d != nil {
			return &d.ScriptData
		}
	case flang.RefBrush:
		if b := c.brushFor(r); b != nil {
			return &b.ScriptData
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
	case flang.RefScreen:
		if s := c.screenFor(r); s != nil {
			return &s.ScriptData
		}
	case flang.RefWidget:
		if n := c.widgetFor(r); n != nil {
			return &n.ScriptData
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
	case flang.RefEffect:
		return c.effectFor(r) != nil
	case flang.RefDecal:
		return c.decalFor(r) != nil
	case flang.RefChunk:
		return c.chunkFor(r) != nil
	case flang.RefBrush:
		return c.brushFor(r) != nil
	case flang.RefLight:
		d, p := c.lightFor(r)
		return d != nil || p != nil
	case flang.RefScreen:
		return c.screenFor(r) != nil
	case flang.RefWidget:
		return c.widgetFor(r) != nil
	case flang.RefModel:
		return c.modelFor(r) != nil
	case flang.RefWaypoints:
		return c.waypointAnimationFor(r) != nil
	}
	return false
}

// ---------- tags ----------

// refsWithTag returns every object carrying tag, in a deterministic order:
// entities, then volumes, then geometry in level order. Determinism matters
// because a broadcast's delivery order follows this one, and script behaviour
// that changed between runs would be untestable.
//
// The level's half comes from the tag index, built on the first call of a tick
// (tagIndex) — every `#tag`, find() and tag send used to walk every object in
// the level, brushes included. The UI's half is walked live, as it always was:
// the stack is small and changes under a tick in ways nothing re-syncs.
//
// The slice returned may be shared with the index, so callers must not write
// to it; its capacity is clipped, so an append makes a copy.
func (c *refCache) refsWithTag(tag string) []flang.RefVal {
	if c.lvl == nil || tag == "" {
		return nil
	}
	lvlRefs := c.tagIndex()[tag]
	uiRefs := c.uiRefsWithTag(tag)
	if len(uiRefs) == 0 {
		if len(lvlRefs) == 0 {
			return nil
		}
		return lvlRefs[:len(lvlRefs):len(lvlRefs)]
	}
	// The UI last, and in stack-then-tree order, so a broadcast reaches the
	// level before the screens over it and always in the same order.
	out := make([]flang.RefVal, 0, len(lvlRefs)+len(uiRefs))
	out = append(out, lvlRefs...)
	return append(out, uiRefs...)
}

// tagIndex returns the level's tag -> refs index, building it on first use.
//
// One walk in exactly the order refsWithTag always walked — entities, volumes,
// sprites, emitters, sounds, effects, decals, then each chunk followed by its
// brushes, then lights — appending each object to the list of every tag it
// carries. Per tag that is the same sequence a walk testing HasTag would have
// produced, which is what keeps a broadcast's delivery order unchanged.
func (c *refCache) tagIndex() map[string][]flang.RefVal {
	if c.tags != nil {
		return c.tags
	}
	c.tags = map[string][]flang.RefVal{}
	lvl := c.lvl
	if lvl == nil {
		return c.tags
	}
	for i := range lvl.Entities {
		c.indexTags(lvl.Entities[i].Tags, flang.RefVal{Kind: flang.RefEntity, A: lvl.Entities[i].ID})
	}
	for i := range lvl.Volumes {
		c.indexTags(lvl.Volumes[i].Tags, flang.RefVal{Kind: flang.RefVolume, A: lvl.Volumes[i].ID})
	}
	for i := range lvl.Sprites {
		c.indexTags(lvl.Sprites[i].Tags, flang.RefVal{Kind: flang.RefSprite, A: lvl.Sprites[i].ID})
	}
	for i := range lvl.Emitters {
		c.indexTags(lvl.Emitters[i].Tags, flang.RefVal{Kind: flang.RefEmitter, A: lvl.Emitters[i].ID})
	}
	for i := range lvl.Sounds {
		c.indexTags(lvl.Sounds[i].Tags, flang.RefVal{Kind: flang.RefSound, A: lvl.Sounds[i].ID})
	}
	for i := range lvl.Effects {
		c.indexTags(lvl.Effects[i].Tags, flang.RefVal{Kind: flang.RefEffect, A: lvl.Effects[i].ID})
	}
	for i := range lvl.Decals {
		c.indexTags(lvl.Decals[i].Tags, flang.RefVal{Kind: flang.RefDecal, A: lvl.Decals[i].ID})
	}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		c.indexTags(ch.Tags, flang.RefVal{Kind: flang.RefChunk, A: ch.ID})
		for bi := range ch.Brushes {
			c.indexTags(ch.Brushes[bi].Tags, flang.RefVal{Kind: flang.RefBrush, A: ch.Brushes[bi].ID})
		}
	}
	for i := range lvl.Lights.Directional {
		c.indexTags(lvl.Lights.Directional[i].Tags, flang.RefVal{Kind: flang.RefLight, A: lvl.Lights.Directional[i].ID})
	}
	for i := range lvl.Lights.Points {
		c.indexTags(lvl.Lights.Points[i].Tags, flang.RefVal{Kind: flang.RefLight, A: lvl.Lights.Points[i].ID})
	}
	return c.tags
}

// indexTags files one object under each of its tags.
//
// A tag listed twice on one object files it once, as HasTag would have: the
// objects arrive one at a time, so a repeat is always the last entry already
// in that tag's list.
func (c *refCache) indexTags(tags []string, r flang.RefVal) {
	for _, t := range tags {
		list := c.tags[t]
		if n := len(list); n > 0 && list[n-1] == r {
			continue
		}
		c.tags[t] = append(list, r)
	}
}

// uiRefsWithTag is the screen-stack half of refsWithTag.
func (c *refCache) uiRefsWithTag(tag string) []flang.RefVal {
	if c.ui == nil {
		return nil
	}
	var out []flang.RefVal
	for _, s := range c.ui.Screens() {
		if s.HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefScreen, A: s.ID})
		}
		screen := s
		s.Root.Walk(func(_, n *ui.Node) bool {
			if n.HasTag(tag) {
				out = append(out, flang.RefVal{Kind: flang.RefWidget, A: screen.ID, B: n.ID})
			}
			return true
		})
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
	for i := range c.lvl.Effects {
		add(c.lvl.Effects[i].Tags)
	}
	for i := range c.lvl.Decals {
		add(c.lvl.Decals[i].Tags)
	}
	for ci := range c.lvl.Chunks {
		ch := &c.lvl.Chunks[ci]
		add(ch.Tags)
		for bi := range ch.Brushes {
			add(ch.Brushes[bi].Tags)
		}
	}
	for i := range c.lvl.Lights.Directional {
		add(c.lvl.Lights.Directional[i].Tags)
	}
	for i := range c.lvl.Lights.Points {
		add(c.lvl.Lights.Points[i].Tags)
	}
	if c.ui != nil {
		for _, sc := range c.ui.Screens() {
			add(sc.Tags)
			sc.Root.Walk(func(_, n *ui.Node) bool { add(n.Tags); return true })
		}
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
		return flang.BoxInt(a.I)
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

// cacheAdded records that one object of this kind was just appended to the
// level — an effect placed, a sound started — so the tick's cache can find it.
// An append shifts nothing, so it is one entry rather than a re-index: a battle
// places a one-shot sound for every shot fired.
func (rt *ScriptRuntime) cacheAdded(kind flang.Kind_) {
	c, lvl := rt.cache, rt.lvl
	if c == nil || c.lvl != lvl || lvl == nil {
		rt.syncCache(lvl)
		return
	}
	// Whatever it was may carry tags, and may be a name refFor had missed.
	c.tags = nil
	clear(c.missing)
	var k ixKind
	switch kind {
	case flang.RefEmitter:
		k = ixEmitter
	case flang.RefSound:
		k = ixSound
	case flang.RefEffect:
		k = ixEffect
	case flang.RefDecal:
		k = ixDecal
	default:
		c.sync(lvl)
		return
	}
	// An index not yet checked this tick will see the new object when it is;
	// one that has been takes it as one more entry.
	if x := &c.ixs[k]; x.checked {
		n, id := c.ids(k)
		x.appended(n, id)
	}
}
