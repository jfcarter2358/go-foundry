package script

// script_host.go — flang.Host, implemented over a Level.
//
// This is the whole of what a script can do to a Foundry world. Everything above
// it is the language, which knows nothing about entities or sectors; everything
// below is the engine, which knows nothing about atoms or clauses.
//
// Two rules the methods here all follow:
//
//   - A ref that no longer resolves is not a crash. The level is rebuilt
//     wholesale by undo and by a reload, so a script holding a ref across a
//     frame is ordinary, and a stale one has to fail softly.
//   - A mutating call marks the play session dirty and, when the property says
//     so, queues a geometry rebuild. Neither happens inline: ReloadSector is GL
//     work, and the script tick is not where GL can happen.

import (
	"fmt"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/strutil"
	"foundry/internal/world"
)

// levelHost is the engine side of the language boundary.
type levelHost struct {
	rt *ScriptRuntime
}

// lvl is the level the host is reading, or nil.
func (h *levelHost) lvl() *world.Level {
	if h.rt == nil {
		return nil
	}
	return h.rt.lvl
}

func (h *levelHost) cache() *refCache {
	if h.rt == nil || h.rt.cache == nil {
		return buildRefCache(nil)
	}
	return h.rt.cache
}

// ---------- addressing ----------

func (h *levelHost) Resolve(id string) (flang.RefVal, bool) {
	switch id {
	case "level":
		return flang.RefVal{Kind: flang.RefLevel}, true
	case "player":
		// The player is engine-owned and not in Level.Entities, so it needs its
		// own case — but it answers to `@player` like anything else.
		if world.PlayerEntity != nil {
			return flang.RefVal{Kind: flang.RefEntity, A: "player"}, true
		}
		return flang.RefVal{}, false
	}
	return h.cache().refFor(id)
}

func (h *levelHost) ResolveTag(tag string) []flang.RefVal {
	return h.cache().refsWithTag(tag)
}

func (h *levelHost) Self() flang.RefVal {
	if h.rt == nil {
		return flang.RefVal{}
	}
	return h.rt.self
}

func (h *levelHost) Alive(r flang.RefVal) bool {
	if r.Kind == flang.RefEntity && r.A == "player" {
		return world.PlayerEntity != nil
	}
	return h.cache().alive(r)
}

// entityFor resolves an entity ref, including the engine-owned player.
func (h *levelHost) entityFor(r flang.RefVal) *world.Entity {
	if r.Kind == flang.RefEntity && r.A == "player" {
		return world.PlayerEntity
	}
	return h.cache().entityFor(r)
}

// ---------- attributes ----------

// scriptData returns the tags/attributes an object carries. Entities keep theirs
// outside a ScriptData — their tags and attributes are merged from a definition,
// which EntityToRaw depends on — so they are handled separately.
func (h *levelHost) scriptData(r flang.RefVal) *world.ScriptData {
	return h.cache().scriptDataFor(r)
}

func (h *levelHost) Attr(r flang.RefVal, name string) (flang.Value, bool) {
	if e := h.entityFor(r); e != nil {
		a, ok := e.Attr(name)
		if !ok {
			return nil, false
		}
		return attrToValue(a), true
	}
	if d := h.scriptData(r); d != nil {
		a, ok := d.Attr(name)
		if !ok {
			return nil, false
		}
		return attrToValue(a), true
	}
	return nil, false
}

func (h *levelHost) SetAttr(r flang.RefVal, name string, v flang.Value) error {
	av, ok := valueToAttr(v)
	if !ok {
		// A level file has no way to write a list or a ref, so storing one here
		// would produce an attribute that could not be saved. Compound state
		// belongs in @self.state, which is transient by design.
		return fmt.Errorf("an attribute holds an int, float, bool or str, not a %s — use @self.state for anything else",
			flang.TypeOf(v))
	}
	if e := h.entityFor(r); e != nil {
		h.rt.markDirty()
		e.SetAttr(name, av)
		return nil
	}
	if d := h.scriptData(r); d != nil {
		h.rt.markDirty()
		d.SetAttr(name, av)
		return nil
	}
	return fmt.Errorf("%s no longer exists", r)
}

// ---------- properties ----------

func (h *levelHost) Prop(r flang.RefVal, name string) (flang.Value, bool) {
	tbl, ok := propTables[r.Kind]
	if !ok {
		return nil, false
	}
	acc, ok := tbl[name]
	if !ok || acc.Get == nil {
		return nil, false
	}
	return acc.Get(h.cache(), r)
}

func (h *levelHost) SetProp(r flang.RefVal, name string, v flang.Value) error {
	tbl, ok := propTables[r.Kind]
	if !ok {
		return fmt.Errorf("a %s has no properties", r.Kind)
	}
	acc, ok := tbl[name]
	if !ok {
		return fmt.Errorf("a %s has no property %q%s", r.Kind, name, didYouMean(name, tbl))
	}
	return h.setProp(&acc, r, name, v)
}

// setProp is the write half of SetProp once the property has been found — the
// part a cached handle shares with it, side effects and all.
func (h *levelHost) setProp(acc *propAccess, r flang.RefVal, name string, v flang.Value) error {
	if acc.Set == nil {
		return fmt.Errorf("%s.%s is read-only", r.Kind, name)
	}
	if err := acc.Set(h.cache(), r, v); err != nil {
		return err
	}
	h.rt.markDirty()
	if acc.Tex {
		// The name may be one no wall in the level uses, so nothing has loaded
		// it; drawing an unloaded name binds texture 0 and samples black.
		if s, ok := toStr(v); ok {
			h.rt.markTextureDirty(s)
		}
	}
	if acc.Dirty {
		// Geometry work cannot happen here — ReloadSector is GL, and the tick
		// runs before the frame's GL work. DrainScriptWork applies it, coalesced,
		// with the main context current.
		h.rt.markGeometryDirty(r)
	}
	if acc.Warn != nil {
		if why := acc.Warn(h.cache(), r); why != "" {
			h.rt.warnAt(flang.Pos{}, "writing %s.prop.%s did nothing: %s", r, name, why)
		}
	}
	return nil
}

// PropHandle implements flang.PropResolver: the property table entry for one
// kind and name, found once for a member expression rather than on every read
// and write through it. Nil for a kind or name the tables do not have, which
// sends the interpreter through Prop and SetProp and their diagnostics.
//
// The tables are built at init and never change, so a handle stays valid for
// as long as the host does.
func (h *levelHost) PropHandle(kind flang.Kind_, name string) flang.PropHandle {
	tbl, ok := propTables[kind]
	if !ok {
		return nil
	}
	acc, ok := tbl[name]
	if !ok {
		return nil
	}
	return &propHandle{h: h, acc: acc, name: name}
}

// propHandle is one property of one kind, for one host.
type propHandle struct {
	h    *levelHost
	acc  propAccess
	name string
}

// GetProp is Prop, for this property.
func (p *propHandle) GetProp(r flang.RefVal) (flang.Value, bool) {
	if p.acc.Get == nil {
		return nil, false
	}
	return p.acc.Get(p.h.cache(), r)
}

// SetProp is SetProp, for this property.
func (p *propHandle) SetProp(r flang.RefVal, v flang.Value) error {
	return p.h.setProp(&p.acc, r, p.name, v)
}

// didYouMean suggests the closest property name, so a typo names its own fix.
// Only offered when something is genuinely close: a wrong guess is worse than
// no guess.
func didYouMean(name string, tbl map[string]propAccess) string {
	best, bestDist := "", 3
	for cand := range tbl {
		if d := strutil.EditDistance(name, cand); d < bestDist {
			best, bestDist = cand, d
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf("; did you mean %q?", best)
}

// ---------- state ----------

func (h *levelHost) State(r flang.RefVal) *flang.DictVal {
	if h.rt == nil {
		return flang.NewDict()
	}
	return h.rt.stateFor(r)
}

// ---------- structural members ----------

// Member resolves the steps that are part of an object's shape: a brush's
// chunk, and a chunk's brushes.
func (h *levelHost) Member(r flang.RefVal, name string) (flang.Value, bool) {
	c := h.cache()
	switch r.Kind {
	case flang.RefBrush:
		// A brush's faces are not addressable yet — they have no IDs of their
		// own. See script_props_geom.go's brushProps.
		if name == "chunk" {
			ch, _ := c.brushOwnerFor(r)
			if ch == nil {
				return nil, false
			}
			return flang.RefVal{Kind: flang.RefChunk, A: ch.ID}, true
		}

	case flang.RefChunk:
		if name == "brushes" {
			ch := c.chunkFor(r)
			if ch == nil {
				return nil, false
			}
			elems := make([]flang.Value, 0, len(ch.Brushes))
			for bi := range ch.Brushes {
				elems = append(elems, flang.RefVal{Kind: flang.RefBrush, A: ch.Brushes[bi].ID})
			}
			return flang.NewList(elems...), true
		}

	case flang.RefEntity:
		e := h.entityFor(r)
		if e == nil {
			return nil, false
		}
		switch name {
		case "model":
			// The pending half of `@guard.model.<name>`, the same shape
			// `@sec.edge.<id>` uses: the accessor word is this step and the
			// attachment name is the next.
			return flang.RefVal{Kind: flang.RefModel, A: r.A}, true
		case "models":
			elems := make([]flang.Value, 0, len(e.Models))
			for i := range e.Models {
				elems = append(elems, flang.RefVal{Kind: flang.RefModel, A: r.A, B: e.Models[i].Name})
			}
			return flang.NewList(elems...), true
		case "geometry":
			// The one chunk an entity's brushes materialise into
			// (levelio/entity_geometry.go). Named rather than derived so
			// `@ship.geometry.prop.enabled` switches a whole hull off.
			id := world.EntityGeometryChunkID(r.A)
			if c.chunkFor(flang.RefVal{Kind: flang.RefChunk, A: id}) == nil {
				return nil, false
			}
			return flang.RefVal{Kind: flang.RefChunk, A: id}, true
		case "brushes":
			ch := c.chunkFor(flang.RefVal{Kind: flang.RefChunk, A: world.EntityGeometryChunkID(r.A)})
			if ch == nil {
				// An entity with no brushes has no chunk, and an empty list is
				// what a loop over it wants — not an error.
				return flang.NewList(), true
			}
			elems := make([]flang.Value, 0, len(ch.Brushes))
			for bi := range ch.Brushes {
				elems = append(elems, flang.RefVal{Kind: flang.RefBrush, A: ch.Brushes[bi].ID})
			}
			return flang.NewList(elems...), true
		case "emitters":
			// The effects the entity's type hung on it, materialised into the
			// level (world/entity_emitter.go). Resolved through the cache
			// rather than off e.Emitters, because those are the descriptions
			// and a ref has to name the live emitter.
			elems := make([]flang.Value, 0, len(e.Emitters))
			for i := range e.Emitters {
				id := world.EntityEmitterID(r.A, e.Emitters[i].Name)
				if c.emitterFor(flang.RefVal{Kind: flang.RefEmitter, A: id}) != nil {
					elems = append(elems, flang.RefVal{Kind: flang.RefEmitter, A: id})
				}
			}
			return flang.NewList(elems...), true
		case "volumes":
			// The regions the entity's type hung on it (world/entity_volume.go),
			// resolved to the live level volumes for the reason emitters are.
			elems := make([]flang.Value, 0, len(e.Volumes))
			for i := range e.Volumes {
				id := world.EntityVolumeID(r.A, e.Volumes[i].Name)
				if c.volumeFor(flang.RefVal{Kind: flang.RefVolume, A: id}) != nil {
					elems = append(elems, flang.RefVal{Kind: flang.RefVolume, A: id})
				}
			}
			return flang.NewList(elems...), true
		default:
			// One of the entity's own brushes, addressed by the name it has in
			// the *definition* — `@ship.hatch`, not `@ship.hatch` spelled out
			// as the level-unique "ship.hatch" the placement gave it. A script
			// names the placement and the part; qualifying the two is the
			// engine's business, and has to be, because the definition-local
			// name is the only one an author of the type ever saw.
			id := world.EntityBrushID(r.A, name)
			if _, b := c.brushOwnerFor(flang.RefVal{Kind: flang.RefBrush, A: id}); b != nil {
				return flang.RefVal{Kind: flang.RefBrush, A: id}, true
			}
			// Or one of its emitters, under exactly the same rule and for the
			// same reason: `@ship.thruster` is the name the author of the type
			// gave it, and the level-unique "ship.thruster" is the engine's
			// business. Brushes are tried first only because they came first;
			// a level cannot hold both under one ID.
			emID := world.EntityEmitterID(r.A, name)
			if c.emitterFor(flang.RefVal{Kind: flang.RefEmitter, A: emID}) != nil {
				return flang.RefVal{Kind: flang.RefEmitter, A: emID}, true
			}
			// Or one of its volumes: `@guard.sight`.
			volID := world.EntityVolumeID(r.A, name)
			if c.volumeFor(flang.RefVal{Kind: flang.RefVolume, A: volID}) != nil {
				return flang.RefVal{Kind: flang.RefVolume, A: volID}, true
			}
			// Or one of its paths: `@self.door_close`, the animation a type's
			// script sends its own hatch along (world/entity_waypoints.go).
			animID := world.EntityWaypointAnimationID(r.A, name)
			if c.waypointAnimationFor(flang.RefVal{Kind: flang.RefWaypoints, A: animID}) != nil {
				return flang.RefVal{Kind: flang.RefWaypoints, A: animID}, true
			}
		}

	case flang.RefModel:
		// A model ref with no name is that pending half; this step is the name.
		if r.B == "" {
			e := h.entityFor(flang.RefVal{Kind: flang.RefEntity, A: r.A})
			if e == nil || e.Model(name) == nil {
				return nil, false
			}
			return flang.RefVal{Kind: flang.RefModel, A: r.A, B: name}, true
		}
		if name == "entity" {
			return flang.RefVal{Kind: flang.RefEntity, A: r.A}, true
		}

	case flang.RefLevel:
		switch name {
		case "entities":
			lvl := h.lvl()
			if lvl == nil {
				return nil, false
			}
			elems := make([]flang.Value, 0, len(lvl.Entities))
			for i := range lvl.Entities {
				elems = append(elems, flang.RefVal{Kind: flang.RefEntity, A: lvl.Entities[i].ID})
			}
			return flang.NewList(elems...), true
		case "volumes":
			lvl := h.lvl()
			if lvl == nil {
				return nil, false
			}
			elems := make([]flang.Value, 0, len(lvl.Volumes))
			for i := range lvl.Volumes {
				elems = append(elems, flang.RefVal{Kind: flang.RefVolume, A: lvl.Volumes[i].ID})
			}
			return flang.NewList(elems...), true
		}
	}
	return nil, false
}

// ---------- messages and verbs ----------

func (h *levelHost) Post(to flang.RefVal, atom string, args []flang.Value, pos flang.Pos) error {
	if h.rt == nil {
		return fmt.Errorf("no script runtime")
	}
	return h.rt.post(to, atom, args, pos)
}

// PostAfter parks the message on the timer queue instead of the message queue.
// The ref is kept rather than resolved: the object may be destroyed before the
// timer comes due, and a message to something that has gone is dropped.
func (h *levelHost) PostAfter(to flang.RefVal, atom string, args []flang.Value, delayMS float64, pos flang.Pos) error {
	if h.rt == nil {
		return fmt.Errorf("no script runtime")
	}
	scriptAddTimer(delayMS, to, atom, args, pos)
	return nil
}

func (h *levelHost) Action(name string, args []flang.Value, pos flang.Pos) (flang.Value, bool, error) {
	if h.rt == nil {
		return nil, false, fmt.Errorf("no script runtime")
	}
	return h.rt.action(name, args, pos)
}

func (h *levelHost) Log(msg string) {
	if h.rt != nil {
		h.rt.logLine(msg)
	}
}

// ---------- shared conversions ----------

// refPosition returns an object's world position, for distance and movement
// verbs. Not every kind has one — a chunk's origin is not where it is — so the
// bool says whether the answer is meaningful.
func (h *levelHost) refPosition(r flang.RefVal) (mgl32.Vec3, bool) {
	c := h.cache()
	switch r.Kind {
	case flang.RefEntity:
		if e := h.entityFor(r); e != nil {
			// Entity.Position is stored in the entity's own frame (a vehicle
			// interior rides its ship), so it needs the same treatment the
			// sector arm below gets: FrameSet.WorldPosition's own doc spells
			// out that a local position and a world one are not numbers that
			// can be subtracted, which is exactly what the distance verbs
			// calling this do. script_events.go's moverPosition already went
			// through it; this did not.
			if lvl := h.lvl(); lvl != nil {
				return lvl.Frames.WorldPosition(e), true
			}
			return e.Position, true
		}
	case flang.RefVolume:
		if v := c.volumeFor(r); v != nil {
			// Through what it rides, on the sprites' rule below.
			return v.WorldPosition(h.lvl()), true
		}
	case flang.RefLight:
		if _, p := c.lightFor(r); p != nil {
			return p.Position, true
		}
	// Sprites, emitters and sounds all carry an Attach and so all have a
	// world position that is not their stored one. None of the three was
	// here, which meant every verb taking "an object or a vec" quietly
	// refused them; `play_sound("clank", @torch)` is the first place that
	// became visible, and adding only sounds would have left the same hole
	// with one more kind in it.
	case flang.RefSprite:
		if sp := c.spriteFor(r); sp != nil {
			return sp.WorldPosition(h.lvl()), true
		}
	case flang.RefEmitter:
		if e := c.emitterFor(r); e != nil {
			return e.WorldPosition(h.lvl()), true
		}
	case flang.RefSound:
		if sd := c.soundFor(r); sd != nil {
			return sd.WorldPosition(h.lvl()), true
		}
	case flang.RefEffect:
		if fx := c.effectFor(r); fx != nil {
			return fx.WorldPosition(h.lvl()), true
		}
	case flang.RefDecal:
		if d := c.decalFor(r); d != nil {
			// Already world-space: a decal has no carrier to resolve through —
			// what rides a moving surface is its MESH, piece by piece, and the
			// box itself stays where it was projected. See world/decal.go.
			return d.Position, true
		}
	case flang.RefBrush:
		if ch, b := c.brushOwnerFor(r); b != nil {
			// A brush's verts are chunk-local; every other arm of this switch
			// returns a genuine world position, and the distance/movement
			// verbs that call this compare them against each other. Without
			// the conversion a script measuring to a brush on a moved chunk
			// gets the distance to where that chunk would be if it had never
			// been placed.
			lo, hi := b.LocalAABB()
			return ch.LocalToWorld(lo.Add(hi).Mul(0.5)), true
		}
	}
	return mgl32.Vec3{}, false
}
