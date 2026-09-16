package world

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
	"foundry/internal/hooks"
	"math"

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
	// FromPrefab is runtime-only, never serialized: the prefab instance whose
	// file put this entity here (prefab.go). Empty for one the level file
	// authored. It is what LevelToRaw checks before writing — a level that
	// saved a copy of a prefab's content would freeze it and stop tracking the
	// file.
	FromPrefab string
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
	//
	// A VEHICLE is the one exception, and it is the same exception for both:
	// its drive model owns these two fields rather than EntityUpdate, so a
	// throttle adds to Velocity and drag sheds it, and the commanded turn rates
	// add to AngularVelocity while AngularDrag sheds that. Writing either still
	// works and still means what it says — it is state the drive model then
	// acts on, not a suggestion it ignores. The one difference is the axes:
	// a vehicle's AngularVelocity is read in its OWN frame (pitch about local
	// X, yaw about local Y, roll about local Z), because that is the only
	// reading under which a roll rolls about the nose. See
	// collision/vehicle_drive.go.
	Velocity        mgl32.Vec3
	AngularVelocity mgl32.Vec3

	// Tweens are the channels of Position, Rotation and Scale a script is
	// easing toward a target — `move_to`, `rotate_to`, `scale_to`. A channel
	// being eased is written by its tween, and the rate that would otherwise
	// move it waits. Runtime-only; see tween.go.
	Tweens Tweens

	// Waypoints is the waypoint animation this entity is travelling, or nil.
	// It writes Position, Rotation and Scale, and the rates wait while it
	// does, as they do for a tween. Runtime-only; see waypoint_follow.go.
	Waypoints *WaypointPlayback

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

	// Brushes are the solid the entity is built out of: convex polyhedra in the
	// entity's own local space, the same ones a level is built from (see
	// entity_brush.go). They are materialised into the level as one frame-owned
	// chunk when the entity is placed or the level is loaded, which is what
	// makes a ship with a walkable hold a *type* rather than a level's
	// hand-wiring — and what lets a body stand inside one and be carried.
	Brushes []Brush

	// Emitters are the particle effects the entity carries, in its own local
	// space (entity_emitter.go): a thruster plume, a torch flame, smoke off a
	// wreck. Like the brushes they are a description rather than a simulation —
	// the level materialises one live Emitter per attachment, attached to this
	// entity, so an effect follows the thing it belongs to for free.
	Emitters []EntityEmitter

	// Volumes are the regions the entity carries, in its own local space
	// (entity_volume.go): a sight cone, a pickup radius. Materialised the way
	// the emitters are — one live level Volume per template, attached to this
	// entity — so `enter` and `exit` on it follow the thing it belongs to.
	Volumes []EntityVolume

	// WaypointAnimations are the paths the entity carries, written in its own
	// local space (entity_waypoints.go): a blast door's slide, a hatch's swing.
	// Materialised like the volumes — one live level animation per template,
	// riding this entity — so a path goes wherever the thing it moves goes.
	WaypointAnimations []WaypointAnimation

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

	// Vehicle is the authored vehicle block, nil for everything that is not one
	// — see vehicle.go. VState is its runtime half, created on first tick and
	// never serialised.
	Vehicle *VehicleSpec
	VState  *VehicleState

	// AI is the authored controller block, nil for everything that decides
	// nothing for itself — see ai.go. AIState is its runtime half, created on
	// the controller's first tick and never serialised.
	AI      *AISpec
	AIState *AIState

	// Seat is which seat of Frame's vehicle this entity occupies. Empty while
	// aboard means standing on the deck rather than strapped into anything, so
	// the two fields together say aboard-and-seated, aboard-and-walking, or not
	// aboard at all.
	Seat string

	// Frame names the entity whose local space Position, Rotation and Velocity
	// are expressed in — empty for the ordinary case of world space. An entity
	// aboard a vehicle stores its position in that vehicle's coordinates, which
	// is what makes it carried perfectly rather than dragged along: it does not
	// move at all while the thing under it does.
	//
	// Anything comparing this entity's position against one in another frame has
	// to go through FrameSet.WorldPosition first. A local position and a world
	// one are not numbers that can be subtracted.
	Frame string

	// AttachedChunk names a chunk this entity rides: whenever that chunk's
	// Origin moves — a lift rising, a barge drifting, someone dragging it in
	// the editor — this entity's Position moves the same distance. See
	// CarryAttachedEntities.
	//
	// This is a plain translation, not a change of coordinate system the way
	// Frame is: Position stays in whatever space it already was, and nothing
	// here gives the entity a socket, a seat, or a way to cross a frame
	// portal. A chunk that only rotates or scales leaves an attached entity
	// exactly where it stood — Chunk.Velocity itself only ever moves an
	// Origin, and this follows the same scope.
	//
	// The entity and the chunk have to agree on which space they are placed
	// in: a world chunk carries a world-space entity (Frame == ""), and a
	// frame-owned chunk carries an entity riding that same frame (Frame ==
	// the chunk's FrameOwner). Attached across that boundary, it silently
	// does nothing — the same "unresolvable name reads as not attached"
	// convention every other named binding in this engine follows (Attach).
	AttachedChunk string

	// Where the entity currently is, refreshed by EntityUpdate. Derived state,
	// never authored and never saved.
	//
	// There is no CurrentBrush counterpart to the old CurrentSector: a brush
	// is a solid, not a room, so "which brush is the entity in" is not a
	// question that makes sense the way "which sector" did. "What region is
	// this" becomes a Volume question (see volume.go) once Volumes take over
	// the trigger-region role sectors used to play.
	CurrentChunk string

	// StepOffset is how far the DRAWN body sits from Position, along the up
	// axis of the entity's own frame, in metres — and StepAnchor is the
	// Position it was last carried at. Both runtime-only: never authored,
	// never saved, and plain values so the zero state is the only state a gob
	// or a fresh Instantiate ever sees.
	//
	// A walker climbs a riser by being teleported up it (collision.StepUp), and
	// a body that jumps half a metre in one tick is the thing a player's eye
	// catches first. The collision has to stay exact, so the lift is taken
	// back out of the drawing instead and eased away over a few frames. See
	// entity_step.go.
	StepOffset float32
	StepAnchor mgl32.Vec3
}

// NewPlayer creates the engine-owned player at pos, which is the player's feet
// (the camera sits EyeHeight above it).
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

// BlankEntityID is what a blank entity is called when nothing else names it.
const BlankEntityID = "entity"

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
		id = BlankEntityID
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
func (e *Entity) EffectiveScale() mgl32.Vec3 {
	if e.Scale == (mgl32.Vec3{}) {
		return mgl32.Vec3{1, 1, 1}
	}
	return e.Scale
}

// ModelMatrix builds the TRS matrix for this entity. Rotation composes Y, then
// X, then Z — the same order the spawn point and every other placed thing in
// the engine uses, so a yaw is a yaw everywhere.
func (e *Entity) ModelMatrix() mgl32.Mat4 {
	s := e.EffectiveScale()
	T := mgl32.Translate3D(e.Position.X(), e.Position.Y(), e.Position.Z())
	S := mgl32.Scale3D(s.X(), s.Y(), s.Z())
	return T.Mul4(e.RotationMatrix()).Mul4(S)
}

// RotationMatrix is the entity's orientation.
//
// A vehicle's quaternion is authoritative when it has one: Euler angles cannot
// express a tumbling body without gimbal lock, and Entity.Rotation is only kept
// in step with it so scripts, the panel and the saved file keep working on the
// field they already know. Everything else has no quaternion and reads its
// Euler triple, exactly as before.
func (e *Entity) RotationMatrix() mgl32.Mat4 {
	if e.VState != nil && e.VState.Started {
		return e.VState.Orient.Mat4()
	}
	return EulerMatrixYXZ(e.Rotation)
}

// EulerMatrixYXZ composes an Euler triple the one way everything here agrees
// on: Y, then X, then Z.
func EulerMatrixYXZ(rot mgl32.Vec3) mgl32.Mat4 {
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
	s := e.EffectiveScale()
	off := mgl32.Vec3{
		e.BoundOffset.X() * s.X(),
		e.BoundOffset.Y() * s.Y(),
		e.BoundOffset.Z() * s.Z(),
	}
	if e.Rotation != (mgl32.Vec3{}) || e.VState != nil {
		r := e.RotationMatrix().Mul4x1(off.Vec4(0))
		off = mgl32.Vec3{r.X(), r.Y(), r.Z()}
	}
	return e.Position.Add(off)
}

// BoundHalfExtents returns the scaled half-extents. Scale is absolute: a
// negative scale mirrors a model but cannot give it negative size.
func (e *Entity) BoundHalfExtents() mgl32.Vec3 {
	s := e.EffectiveScale()
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
	rot := EulerMatrixYXZ(e.Rotation)
	for i := 0; i < 8; i++ {
		corner := mgl32.Vec3{
			h.X() * CornerSign(i, 0),
			h.Y() * CornerSign(i, 1),
			h.Z() * CornerSign(i, 2),
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

// CornerSign returns ±1 for one axis of box corner i, so the eight corners come
// out of the low three bits of a counter.
func CornerSign(i, axis int) float32 {
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
	return ShapeContainsPoint(p, e.BoundCenter(), e.Rotation, e.BoundHalfExtents(), e.BoundingKind)
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
	e.Tags = NormalizeTags(append(e.Tags, tag))
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
		return NormalizeTags(extra)
	}
	if len(extra) == 0 {
		return NormalizeTags(base)
	}
	return NormalizeTags(append(append([]string(nil), base...), extra...))
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

// UniqueEntityID returns base, or base with a numeric suffix that no entity in
// lvl is already using. IDs are how scripts address entities, so two entities
// sharing one would make `@id` ambiguous — every path that creates an entity
// (placement, duplication, loading a level with a collision) goes through here.
func UniqueEntityID(lvl *Level, base string) string {
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
const EntityScaleMin = float32(0.001)

// EntityScaleClamp keeps a scale factor off zero without forcing its sign: a
// negative scale legitimately mirrors a model.
func EntityScaleClamp(v float32) float32 {
	if v > -EntityScaleMin && v < EntityScaleMin {
		if v < 0 {
			return -EntityScaleMin
		}
		return EntityScaleMin
	}
	return v
}

// EntityFrameScale is the single factor the *geometry* an entity carries —
// its chunk attachments, and any level chunk hand-bound to it as an interior —
// follows when the entity is scaled: the mean of its three axis magnitudes.
//
// One factor rather than three because a chunk's scale has only ever been
// uniform (Chunk.Scale), and a good deal is built on that: a walkable slope
// survives a uniform scale unchanged, the 2D map projects a chunk's plane with
// one ruler, and a click radius converts into local units by one division.
// Stretching a chunk per-axis would skew all three at once and say nothing
// about it. The mean is exact for the uniform case, which is what "make this
// ship bigger" actually is, and a reasonable middle for the rest.
//
// Magnitudes because a negative scale mirrors a model but cannot mirror a
// place: a flipped chunk would invert its winding and read its floors as
// ceilings.
func EntityFrameScale(e *Entity) float32 {
	if e == nil {
		return 1
	}
	s := e.EffectiveScale()
	k := (geom.Abs32(s.X()) + geom.Abs32(s.Y()) + geom.Abs32(s.Z())) / 3
	if k < EntityScaleMin {
		return EntityScaleMin
	}
	return k
}

// PlayerEntity points to the entity with ID "player" in the current level.
// Global so that a level load can update it — the player outlives the level it
// is standing in, which is why it is not simply a field on Level.
var PlayerEntity *Entity

// EyeHeight is the camera offset above the player entity's Y position.
const EyeHeight = float32(1.7)

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
	// level happens to list its boxes in. GroundedNext is where this frame's
	// answer accumulates until EntityUpdate commits it.
	OnGround     bool
	GroundedNext bool
}

// DefaultEntityCollision is what a definition that says nothing gets: inert,
// but movable, so turning a single flag on behaves the obvious way.
func DefaultEntityCollision() EntityCollision {
	return EntityCollision{Movable: true}
}

// ---------- per-frame motion and animation ----------
//
// What an entity does to itself each frame. The driver that walks the level
// calling these, and the collision resolution between them, is EntityUpdate in
// the root package — it has to run after the resolver, which is layered above
// this package.

// Integrate advances position by velocity and rotation by angular velocity.
//
// Rotation wraps into [-π, π] rather than growing without bound: an entity
// spinning at 1 rad/s for an hour would otherwise reach an angle whose float32
// resolution is coarser than a degree, and the spin would visibly quantise.
func (e *Entity) Integrate(dt float32) {
	if e.Velocity != (mgl32.Vec3{}) {
		e.Position = e.Position.Add(e.Velocity.Mul(dt))
	}
	if e.AngularVelocity != (mgl32.Vec3{}) {
		r := e.Rotation.Add(e.AngularVelocity.Mul(dt))
		e.Rotation = mgl32.Vec3{WrapAngle(r.X()), WrapAngle(r.Y()), WrapAngle(r.Z())}
	}
}

// UpdateLocation refreshes which chunk the entity is standing in.
//
// This is derived state that scripts read, and that a future entity-aware
// collision pass would start from. It narrows the same way the player
// already does, through the same spatial hash.
func (e *Entity) UpdateLocation(lvl *Level) {
	// Searched in the entity's own frame: its position is expressed there, and
	// a frame's chunks are not in the world index at all.
	chunk := lvl.Frames.FrameFor(e).ChunkAt(lvl, e.Position)
	if chunk != nil {
		e.CurrentChunk = chunk.ID
	} else {
		e.CurrentChunk = ""
	}
}

// UpdateAnimations advances every attachment's clip.
func (e *Entity) UpdateAnimations(dt float32) {
	for i := range e.Models {
		e.Models[i].Update(dt, e.ID)
	}
}

// PlayAnimation starts a named animation on one attachment and reports whether
// it resolved. An empty model name plays on every attachment that has an
// animation by that name, which is what a script asking a whole entity to
// "walk" means when the walk is split across a body and a pair of legs.
func (e *Entity) PlayAnimation(model, animation string) bool {
	if model != "" {
		m := e.Model(model)
		if m == nil {
			return false
		}
		if !m.Play(animation) {
			return false
		}
		// The clip has begun. Emitted here rather than in EntityModel.Play
		// because an attachment has no way back to its entity, and a clip
		// restarted from its own end has no clock change for Update to notice.
		hooks.RecordAnimEvent(hooks.AnimEvent{Entity: e.ID, Model: m.Name, Animation: animation})
		return true
	}
	any := false
	for i := range e.Models {
		if e.Models[i].Play(animation) {
			hooks.RecordAnimEvent(hooks.AnimEvent{Entity: e.ID, Model: e.Models[i].Name, Animation: animation})
			any = true
		}
	}
	return any
}

// StopAnimations returns every attachment to its rest pose.
func (e *Entity) StopAnimations() {
	for i := range e.Models {
		e.Models[i].Stop()
	}
}

// ---------- collision participation ----------
//
// Whether an entity takes part, and the gravity that follows from it. The
// resolution itself is entity_collision.go's; these are questions about the
// entity, and every one of them is answered from its own fields.

// CollidesWithWorld / CollidesWithEntities fold the master switch into the
// per-entity flag, so no caller has to remember to check both.
func (e *Entity) CollidesWithWorld() bool {
	return EntityWorldCollisionEnabled && e.Collision.World
}
func (e *Entity) CollidesWithEntities() bool {
	return EntityEntityCollisionEnabled && e.Collision.Entities && e.HasBounds()
}

// HasGravity reports whether the entity should fall. An immovable entity never
// does, whatever its flag says — nothing can displace it, gravity included.
func (e *Entity) HasGravity() bool {
	return e.Collision.Gravity && e.Collision.Movable
}

// ApplyGravity accelerates the entity downward, capped at terminal velocity so
// a long fall cannot tunnel through a floor in one step.
func (e *Entity) ApplyGravity(dt float32) {
	if !e.HasGravity() {
		return
	}
	v := e.Velocity.Y() - EntityGravityAccel*dt
	if v < -EntityMaxFallSpeed {
		v = -EntityMaxFallSpeed
	}
	e.Velocity[1] = v
}

// CommitGrounded promotes this frame's accumulated answer to the readable flag.
// Called once per entity at the end of EntityUpdate, after every push and every
// settle has had its say.
func (e *Entity) CommitGrounded() {
	e.Collision.OnGround = e.Collision.GroundedNext
	e.Collision.GroundedNext = false
}

// WrapAngle folds an angle into [-pi, pi].
//
// Near-identical to geom.WrapAngle, which folds into the half-open (-pi, pi]
// instead. They differ only at exactly -pi, and are kept apart rather than
// merged because that boundary is the one place a rotation and a gizmo drag
// would disagree.

// WrapAngle folds an angle into [-π, π].
func WrapAngle(a float32) float32 {
	if a >= -math.Pi && a <= math.Pi {
		return a
	}
	a = float32(math.Mod(float64(a), float64(twoPi)))
	if a > math.Pi {
		a -= twoPi
	} else if a < -math.Pi {
		a += twoPi
	}
	return a
}

// twoPi is the wrap period for Euler angles under angular velocity.
const twoPi = float32(2 * math.Pi)

// Engine-wide master switches. Per-entity flags say what *wants* to collide;
// these say whether the engine is listening. Both default on, so turning a
// flag on in a definition is all it takes.
var (
	EntityWorldCollisionEnabled  = true
	EntityEntityCollisionEnabled = true
)

// Entity gravity, in world units per second and per second squared.
//
// These are the player's own GravityAccel/MaxFallSpeed (input.go) converted out
// of its per-frame-at-60fps units, so a dropped crate falls at the rate a
// player does. The player's cannot simply be reused: its velocity is measured
// per frame, an entity's per second.
const (
	EntityGravityAccel  = float32(36.0)
	EntityMaxFallSpeed  = float32(30.0)
	EntityGroundEpsilon = float32(0.0001)
)

// EntitiesContaining is a spatial query, not a physics step — it is what a
// script means by "who is standing in this volume".
// EntitiesContaining returns every entity whose bounding volume holds the
// world-space point p — the query behind the scripting engine's `enter`/`exit`
// events, and what an "interact" key press resolves through.
func EntitiesContaining(lvl *Level, p mgl32.Vec3) []*Entity {
	if lvl == nil {
		return nil
	}
	var out []*Entity
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		// p is world-space; an entity in a frame describes its volume in that
		// frame's coordinates, so the point is carried in rather than the volume
		// carried out — a rotated volume stays the shape it was authored as.
		if e.ContainsPoint(lvl.Frames.FrameFor(e).FromWorld(p)) {
			out = append(out, e)
		}
	}
	return out
}
