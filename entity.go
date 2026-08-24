package main

// entity.go — Entity, the one dynamic thing a level can place.
//
// An entity is a transform, a bounding volume, and a bag of user data. Every
// part of it beyond the transform is optional: an entity with no models is a
// perfectly good trigger volume or script anchor, and one with no attributes is
// a prop. What it is *not* is geometry — a level's chunks, sectors and biome
// paint are still the only things that describe the world's shape. An entity
// sits in that world and moves through it.
//
// An entity is authored one of two ways. The quick one is a *blank* entity:
// the editor places it, and the level file carries its whole description —
// mesh, bounds, attributes and all. The other is an *instance* of a definition
// under resources/entities/*.yaml (entity_def.go), which is what makes "every
// barrel in the game gains 10 health" a one-file edit rather than a search
// across every level. A definition is what a blank entity becomes once a second
// level wants the same thing; nothing has to start as one.
//
// The player is the exception at the other end: it is created by the engine
// (NewPlayer) rather than read from anywhere, carries no models because the
// view is from inside its head, and is never saved.

import (
	"foundry/internal/geom"

	"github.com/go-gl/mathgl/mgl32"
)

// Player bounding half-extents — the box movement and step-up have always been
// tuned against.
const (
	playerHalfWidth  = float32(0.3)
	playerHalfHeight = float32(0.9)
)

// Entity is a placed, dynamic object.
type Entity struct {
	// ID is unique within a level; scripts address an entity by it (`@id`).
	ID string
	// Type is the definition this instance was made from, empty for the
	// engine-owned player. Kept so saving can write the instance back as a
	// reference rather than as an expansion of everything it inherited.
	Type string
	// Tags are the entity's script-visible labels; scripts address every
	// entity carrying one at once (`#tag`). Sorted and de-duplicated by
	// setTags, so membership tests and saved files are both stable.
	Tags []string

	Position mgl32.Vec3
	Rotation mgl32.Vec3 // Euler angles (X, Y, Z) in radians
	Scale    mgl32.Vec3 // per-axis; zero value treated as {1,1,1}

	// Velocity is world units per second, and AngularVelocity is radians per
	// second about each Euler axis. Both are plain state that EntityUpdate
	// integrates — nothing damps or clamps them, because what should slow a
	// thrown crate down is the game's rules rather than the engine's.
	Velocity        mgl32.Vec3
	AngularVelocity mgl32.Vec3

	// Bounds. BoundOffset moves the volume's centre off the entity's origin,
	// which is what lets a model stand on its feet — Position at the floor —
	// while its bounding box is centred at hip height.
	BoundingKind BoundingType
	BoundHalfX   float32
	BoundHalfY   float32
	BoundHalfZ   float32
	BoundOffset  mgl32.Vec3

	// Models are the entity's visual attachments, each with its own transform
	// relative to the entity and its own set of playable animations. Empty is
	// legitimate: an invisible entity is still a position with attributes on it.
	Models []EntityModel

	// Script is the .flang file driving this entity's behaviour, resolved from
	// the definition and optionally overridden per instance. Empty means the
	// entity has no behaviour of its own.
	Script string

	// Attributes are user-defined values the engine only stores — see
	// entity_attr.go.
	Attributes AttrSet

	// ScriptParams answers the script's top-level `param` declarations — see
	// script_params.go. Merged from the definition and overlaid per instance,
	// exactly like Attributes, which is what lets one entity type ship a
	// scripted behaviour whose wiring each placed copy fills in for itself.
	ScriptParams AttrSet

	// Collision is what this entity takes part in physically — see
	// entity_collision.go. All off by default: an entity that suddenly became
	// solid because it was reloaded by a newer build would change every level
	// that ever placed one.
	Collision EntityCollision

	// Visible hides the entity's models without removing them, for a prop that
	// a script reveals later. It does not affect bounds or attributes.
	Visible bool

	// Where the entity currently is, refreshed by EntityUpdate. Derived state,
	// never authored and never saved.
	CurrentSector string
	CurrentChunk  string
}

// NewPlayer creates the engine-owned player at pos, which is the player's feet
// (the camera sits eyeHeight above it).
//
// It has no Type and no models: nothing authored it and nothing draws it.
func NewPlayer(pos mgl32.Vec3) *Entity {
	return &Entity{
		ID:         "player",
		Position:   pos,
		Scale:      mgl32.Vec3{1, 1, 1},
		BoundHalfX: playerHalfWidth,
		BoundHalfY: playerHalfHeight,
		BoundHalfZ: playerHalfWidth,
		Visible:    true,
		// The player's world collision is the main loop's own resolve path, not
		// the flag — but it is movable, which is what lets a solid entity push
		// it back out. See entity_collision.go.
		Collision: EntityCollision{Movable: true},
	}
}

// blankEntityID is what a blank entity is called when nothing else names it.
const blankEntityID = "entity"

// blankEntityHalfExtent is the half-size of the box a blank entity arrives
// with, in world units. It exists so the thing can be seen and clicked the
// moment it is placed — an entity with no mesh *and* no volume is one the
// editor can place and then never find again.
const blankEntityHalfExtent = float32(0.5)

// NewBlankEntity returns an entity that belongs to no definition: what the
// editor places when its catalog is on "blank entity".
//
// It has no models on purpose. Which mesh a thing wears is the first decision
// an author makes about it, and a default would be a guess to undo rather than
// a head start — so the attachment list starts empty and the panel's model
// picker is where it fills. Everything else gets a value the entity is usable
// with: a half-metre box resting on its origin, visible, and inert until its
// collision flags are ticked.
func NewBlankEntity(id string) Entity {
	if id == "" {
		id = blankEntityID
	}
	return Entity{
		ID:           id,
		Scale:        mgl32.Vec3{1, 1, 1},
		BoundingKind: BoundingAABB,
		BoundHalfX:   blankEntityHalfExtent,
		BoundHalfY:   blankEntityHalfExtent,
		BoundHalfZ:   blankEntityHalfExtent,
		// Position is an entity's base rather than its centre, so the box is
		// lifted to rest on it — the same convention every shipped definition
		// and the spawn marker use.
		BoundOffset: mgl32.Vec3{0, blankEntityHalfExtent, 0},
		Visible:     true,
		Collision:   DefaultEntityCollision(),
	}
}

// effectiveScale treats the zero value as identity, so an entity that never
// mentioned scale behaves as one scaled {1,1,1} rather than collapsing.
func (e *Entity) effectiveScale() mgl32.Vec3 {
	if e.Scale == (mgl32.Vec3{}) {
		return mgl32.Vec3{1, 1, 1}
	}
	return e.Scale
}

// ModelMatrix builds the TRS matrix for this entity. Rotation composes Y, then
// X, then Z — the same order the spawn point and every other placed thing in
// the engine uses, so a yaw is a yaw everywhere.
func (e *Entity) ModelMatrix() mgl32.Mat4 {
	s := e.effectiveScale()
	T := mgl32.Translate3D(e.Position.X(), e.Position.Y(), e.Position.Z())
	S := mgl32.Scale3D(s.X(), s.Y(), s.Z())
	return T.Mul4(eulerMatrixYXZ(e.Rotation)).Mul4(S)
}

// eulerMatrixYXZ composes an Euler triple the one way everything here agrees
// on: Y, then X, then Z.
func eulerMatrixYXZ(rot mgl32.Vec3) mgl32.Mat4 {
	return mgl32.HomogRotate3DY(rot.Y()).
		Mul4(mgl32.HomogRotate3DX(rot.X())).
		Mul4(mgl32.HomogRotate3DZ(rot.Z()))
}

// BoundCenter is the world-space centre of the bounding volume: the entity's
// position plus its offset, with the offset carried through the entity's own
// rotation and scale so it stays put on the model it was authored against.
func (e *Entity) BoundCenter() mgl32.Vec3 {
	if e.BoundOffset == (mgl32.Vec3{}) {
		return e.Position
	}
	s := e.effectiveScale()
	off := mgl32.Vec3{
		e.BoundOffset.X() * s.X(),
		e.BoundOffset.Y() * s.Y(),
		e.BoundOffset.Z() * s.Z(),
	}
	if e.Rotation != (mgl32.Vec3{}) {
		r := eulerMatrixYXZ(e.Rotation).Mul4x1(off.Vec4(0))
		off = mgl32.Vec3{r.X(), r.Y(), r.Z()}
	}
	return e.Position.Add(off)
}

// BoundHalfExtents returns the scaled half-extents. Scale is absolute: a
// negative scale mirrors a model but cannot give it negative size.
func (e *Entity) BoundHalfExtents() mgl32.Vec3 {
	s := e.effectiveScale()
	return mgl32.Vec3{
		e.BoundHalfX * geom.Abs32(s.X()),
		e.BoundHalfY * geom.Abs32(s.Y()),
		e.BoundHalfZ * geom.Abs32(s.Z()),
	}
}

// HasBounds reports whether the entity has a usable bounding volume. All three
// extents must be positive: a zero on any axis is an unauthored volume rather
// than a flat one.
func (e *Entity) HasBounds() bool {
	return e.BoundHalfX > 0 && e.BoundHalfY > 0 && e.BoundHalfZ > 0
}

// WorldAABB returns the entity's world-space bounds. ok is false when no
// extents are authored, in which case the caller must not cull the entity — an
// unbounded entity is not a zero-sized one.
//
// A rotated entity uses the extents' length as a uniform radius so the box stays
// valid at any orientation, rather than shrink-wrapping a box that would clip
// the moment the entity turned.
func (e *Entity) WorldAABB() (min, max mgl32.Vec3, ok bool) {
	if !e.HasBounds() {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	h := e.BoundHalfExtents()
	if e.Rotation != (mgl32.Vec3{}) {
		r := h.Len()
		h = mgl32.Vec3{r, r, r}
	}
	c := e.BoundCenter()
	return c.Sub(h), c.Add(h), true
}

// CollisionAABB is the entity's world-space bounds for *collision*: the exact
// box its rotated volume spans, found by carrying the eight corners through the
// transform.
//
// WorldAABB deliberately does not do this. Culling has to be conservative, so it
// blows a rotated box up to a uniform radius and never under-estimates; a
// resolver wants the opposite, because that padding would push things apart
// across a gap of up to 73% of their size. The two callers want different
// boxes, so they get different boxes.
func (e *Entity) CollisionAABB() (min, max mgl32.Vec3, ok bool) {
	if !e.HasBounds() {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	h := e.BoundHalfExtents()
	c := e.BoundCenter()
	if e.Rotation == (mgl32.Vec3{}) {
		return c.Sub(h), c.Add(h), true
	}
	rot := eulerMatrixYXZ(e.Rotation)
	for i := 0; i < 8; i++ {
		corner := mgl32.Vec3{
			h.X() * cornerSign(i, 0),
			h.Y() * cornerSign(i, 1),
			h.Z() * cornerSign(i, 2),
		}
		r := rot.Mul4x1(corner.Vec4(0))
		p := c.Add(mgl32.Vec3{r.X(), r.Y(), r.Z()})
		if i == 0 {
			min, max = p, p
			continue
		}
		min, max = geom.VecMin(min, p), geom.VecMax(max, p)
	}
	return min, max, true
}

// cornerSign returns ±1 for one axis of box corner i, so the eight corners come
// out of the low three bits of a counter.
func cornerSign(i, axis int) float32 {
	if i&(1<<axis) != 0 {
		return 1
	}
	return -1
}

// ContainsPoint reports whether p is inside the entity's bounding volume — the
// test behind the scripting engine's `enter` and `exit` events, and the one
// place BoundingKind actually matters.
//
// The point is moved into the entity's own space rather than the volume being
// moved into the world's, so a rotated cylinder stays a cylinder instead of
// degenerating into the conservative sphere WorldAABB has to use.
func (e *Entity) ContainsPoint(p mgl32.Vec3) bool {
	if !e.HasBounds() {
		return false
	}
	return shapeContainsPoint(p, e.BoundCenter(), e.Rotation, e.BoundHalfExtents(), e.BoundingKind)
}

// ---------- tags ----------

// HasTag reports whether the entity carries tag.
func (e *Entity) HasTag(tag string) bool {
	for _, t := range e.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// AddTag adds a tag, keeping the list sorted and duplicate-free.
func (e *Entity) AddTag(tag string) {
	if tag == "" || e.HasTag(tag) {
		return
	}
	e.Tags = normalizeTags(append(e.Tags, tag))
}

// RemoveTag drops a tag, and reports whether it was there.
func (e *Entity) RemoveTag(tag string) bool {
	for i, t := range e.Tags {
		if t == tag {
			e.Tags = append(e.Tags[:i:i], e.Tags[i+1:]...)
			return true
		}
	}
	return false
}

// mergeTags unions two tag lists — how an instance's tags combine with its
// definition's. Union rather than replacement: a definition's tags are what the
// thing *is*, and an instance adding "quest_item" should not have to restate
// them.
func mergeTags(base, extra []string) []string {
	if len(base) == 0 {
		return normalizeTags(extra)
	}
	if len(extra) == 0 {
		return normalizeTags(base)
	}
	return normalizeTags(append(append([]string(nil), base...), extra...))
}

// ---------- attributes ----------

// Attr reads one attribute.
func (e *Entity) Attr(name string) (AttrValue, bool) { return e.Attributes.Attr(name) }

// SetAttr writes one attribute, creating the set if the entity had none.
func (e *Entity) SetAttr(name string, v AttrValue) {
	if name == "" {
		return
	}
	if e.Attributes == nil {
		e.Attributes = AttrSet{}
	}
	e.Attributes[name] = v
}

// DeleteAttr removes an attribute and reports whether it was there.
func (e *Entity) DeleteAttr(name string) bool {
	if _, ok := e.Attributes[name]; !ok {
		return false
	}
	delete(e.Attributes, name)
	return true
}

// ---------- models ----------

// Model returns the named attachment, or nil.
func (e *Entity) Model(name string) *EntityModel {
	for i := range e.Models {
		if e.Models[i].Name == name {
			return &e.Models[i]
		}
	}
	return nil
}

// SetTexture reskins the named attachment, or every attachment when model is
// "". It reports whether it found anything to reskin.
//
// The caller is responsible for the texture being resident — see
// ScriptRuntime.markTextureDirty. Nothing here can load it, because uploading
// is GL work and this is called from the script tick.
func (e *Entity) SetTexture(model, texture string) bool {
	if model != "" {
		m := e.Model(model)
		if m == nil {
			return false
		}
		m.SetTexture(texture)
		return true
	}
	for i := range e.Models {
		e.Models[i].SetTexture(texture)
	}
	return len(e.Models) > 0
}

// Load uploads every model attachment to the GPU. Attachments that fail are
// reported but do not stop the rest: one missing .fmdl should cost that
// attachment, not the whole entity.
func (e *Entity) Load() error {
	var firstErr error
	for i := range e.Models {
		if err := e.Models[i].Load(e.ID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Delete releases every attachment's GPU buffers.
func (e *Entity) Delete() {
	for i := range e.Models {
		e.Models[i].Delete()
	}
}

// HasGeometry reports whether anything on this entity would draw.
func (e *Entity) HasGeometry() bool {
	for i := range e.Models {
		if e.Models[i].HasGeometry() {
			return true
		}
	}
	return false
}

// ---------- level lookup ----------

// FindEntity returns the level entity with the given ID, or nil. This is what
// a script's `@id` resolves through.
func FindEntity(lvl *Level, id string) *Entity {
	if lvl == nil || id == "" {
		return nil
	}
	for i := range lvl.Entities {
		if lvl.Entities[i].ID == id {
			return &lvl.Entities[i]
		}
	}
	return nil
}

// FindEntitiesByTag returns every level entity carrying tag, in level order.
// This is what a script's `#tag` resolves through.
func FindEntitiesByTag(lvl *Level, tag string) []*Entity {
	if lvl == nil || tag == "" {
		return nil
	}
	var out []*Entity
	for i := range lvl.Entities {
		if lvl.Entities[i].HasTag(tag) {
			out = append(out, &lvl.Entities[i])
		}
	}
	return out
}

// uniqueEntityID returns base, or base with a numeric suffix that no entity in
// lvl is already using. IDs are how scripts address entities, so two entities
// sharing one would make `@id` ambiguous — every path that creates an entity
// (placement, duplication, loading a level with a collision) goes through here.
func uniqueEntityID(lvl *Level, base string) string {
	if base == "" {
		base = "entity"
	}
	// Checked against every addressable kind, not just entities: SCRIPTING.md
	// promises `@id` never needs qualifying, which only holds if one name means
	// one object across sectors, volumes, lights and the rest. See
	// uniqueLevelID in identity.go.
	return uniqueLevelID(lvl, base)
}

// A zero scale collapses the model to nothing and makes its matrix singular,
// which the picker inverts — so a drag can approach flat but not reach it.
const entityScaleMin = float32(0.001)

// entityScaleClamp keeps a scale factor off zero without forcing its sign: a
// negative scale legitimately mirrors a model.
func entityScaleClamp(v float32) float32 {
	if v > -entityScaleMin && v < entityScaleMin {
		if v < 0 {
			return -entityScaleMin
		}
		return entityScaleMin
	}
	return v
}

// PlayerEntity points to the entity with ID "player" in the current level.
// Global so that a level load can update it — the player outlives the level it
// is standing in, which is why it is not simply a field on Level.
var PlayerEntity *Entity

// eyeHeight is the camera offset above the player entity's Y position.
const eyeHeight = float32(1.7)

// ---------- collision participation ----------
//
// The flags an entity carries, as opposed to the resolver that acts on them
// (entity_collision.go). They are authored in a definition and saved with an
// instance, so they are part of what an entity *is*.

// EntityCollision is one entity's participation in all of the above.
type EntityCollision struct {
	// World resolves the entity against level geometry.
	World bool
	// Entities resolves it against other entities that also have this set, and
	// against the player. Both sides must opt in — solidity is mutual, and an
	// entity that could push something which could not push back would make
	// which of the two moves depend on iteration order.
	Entities bool
	// Gravity makes it fall. Independent of World, though the two are usually
	// set together: gravity with no world collision falls forever, which is a
	// legitimate thing to want for a projectile.
	Gravity bool
	// Movable is whether a resolution may displace *this* entity. False makes
	// it immovable scenery that pushes others and is never pushed itself —
	// the boulder other things bounce off. Defaults to true.
	Movable bool
	// OnGround is whether the entity is standing on something — a floor, or
	// another entity. Derived, never authored, never saved.
	//
	// It holds the state as of the end of the *previous* frame, and is what
	// canDisplace consults. That one frame of lag is deliberate: the flag is
	// both written and read during a frame's resolution, so reading the live
	// value would make a stack settle differently depending on the order the
	// level happens to list its boxes in. groundedNext is where this frame's
	// answer accumulates until EntityUpdate commits it.
	OnGround     bool
	groundedNext bool
}

// DefaultEntityCollision is what a definition that says nothing gets: inert,
// but movable, so turning a single flag on behaves the obvious way.
func DefaultEntityCollision() EntityCollision {
	return EntityCollision{Movable: true}
}
