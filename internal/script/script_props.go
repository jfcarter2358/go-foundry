package script

// script_props.go — the `.prop` surface.
//
// A declarative table rather than a switch, because three separate things need
// to *enumerate* it: the script editor's completion list, the reference panel,
// and the "no property called `visble`, did you mean `visible`" message. A switch
// cannot be enumerated. This codebase already makes exactly that argument at
// entity.go's BoundingTypeNames — "a per-file switch is what lets an unknown
// name quietly load as the wrong shape".
//
// The `dirty` flag is the other reason the table earns its place. Many property
// writes are geometry-mutating — a floor's height, a wall's texture — and
// applying one means ReloadSector, which means GL, which cannot happen inside
// the script tick. Declaring "this setter needs a rebuild" once per property is
// far safer than remembering it at each call site; DrainScriptWork (see
// script_runtime.go) does the rest.
//
// Three traps are worth naming, because each is a place a naive setter would be
// quietly wrong:
//
//   - Wall.Visible is stored inverted from YAML's `hidden`.
//   - DirectionalLight.Direction must never be written directly; only Yaw and
//     Pitch, followed by SyncDirection. A zero vector normalises to NaN and
//     poisons the shadow matrix permanently.
//   - Alpha and DoubleSided have no zero-value-safe default in the editor's own
//     constructors, so a script writing 0 to Alpha means transparent, not opaque.

import (
	"fmt"
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/ai"
	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/world"
)

// propAccess is one property: how to read it, how to write it, and whether
// writing it requires a geometry rebuild.
type propAccess struct {
	Get func(c *refCache, r flang.RefVal) (flang.Value, bool)
	// Set may be nil, which makes the property read-only.
	Set func(c *refCache, r flang.RefVal, v flang.Value) error
	// Dirty means a successful Set must queue a rebuild of the owning sector.
	Dirty bool
	// Tex means the written value names a texture, which must be resident
	// before anything draws with it. Loading is GL work and cannot happen in
	// the tick, so SetProp queues the name for DrainScriptWork.
	Tex bool
	// Warn explains, after a successful write, why that write changed nothing
	// visible — an entity reskinned when it has no models loaded, say. Empty
	// for the ordinary case, and nil for a property that cannot fail quietly.
	//
	// A write is not a call, so there is no position to report it against; the
	// linter catches the literal case at author time, where there is one. An
	// unpositioned warning is still worth more than silence — see warnFor.
	Warn func(c *refCache, r flang.RefVal) string
	Doc  string
}

// propTables maps each object kind to its properties.
var propTables map[flang.Kind_]map[string]propAccess

// PropertyKinds lists every kind that actually has properties, sorted.
//
// Exported so the F1 reference can assert it is showing all of them: it has
// silently missed three before — see refKindOrder — and a property table nobody
// can find is a feature nobody has.
//
// Kinds registered with an empty table are skipped. Sector has one so that
// `@room.prop.x` says "no property x" rather than "a sector has no properties",
// which is a different and less useful thing to be told; but there is nothing
// there to list.
func PropertyKinds() []flang.Kind_ {
	out := make([]flang.Kind_, 0, len(propTables))
	for k, tbl := range propTables {
		if len(tbl) > 0 {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// PropertyNames lists the properties of one kind, sorted. The editor's reference
// panel reads this rather than keeping its own copy.
func PropertyNames(kind flang.Kind_) []string {
	tbl, ok := propTables[kind]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(tbl))
	for n := range tbl {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// PropertyDoc is one property's reference entry: what it is, and whether a
// script may write it.
type PropertyDoc struct {
	Name     string
	Doc      string
	Writable bool
}

// PropertyDocs lists one kind's properties with their descriptions, sorted.
//
// The reference draws from this rather than from PropertyNames because a list
// of bare names answers neither of the two questions actually asked of it —
// what does `bounds` mean, and can I assign to it. Read-only is the interesting
// half: a script that writes one of those is reported, not obeyed.
func PropertyDocs(kind flang.Kind_) []PropertyDoc {
	tbl, ok := propTables[kind]
	if !ok {
		return nil
	}
	out := make([]PropertyDoc, 0, len(tbl))
	for name, acc := range tbl {
		out = append(out, PropertyDoc{Name: name, Doc: acc.Doc, Writable: acc.Set != nil})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ---------- conversion helpers ----------

func vecValue(v mgl32.Vec3) flang.Value {
	return flang.VecVal{float64(v.X()), float64(v.Y()), float64(v.Z())}
}

// toVec3 converts a script value to an mgl32.Vec3.
func toVec3(v flang.Value) (mgl32.Vec3, bool) {
	t, ok := v.(flang.VecVal)
	if !ok {
		return mgl32.Vec3{}, false
	}
	return mgl32.Vec3{float32(t[0]), float32(t[1]), float32(t[2])}, true
}

// toFloat32 accepts either numeric type, since a script writing `0` where a
// float is wanted is expressing an intent, not a type error.
func toFloat32(v flang.Value) (float32, bool) {
	switch t := v.(type) {
	case flang.IntVal:
		return float32(t), true
	case flang.FloatVal:
		return float32(t), true
	}
	return 0, false
}

func toBool(v flang.Value) (bool, bool) {
	t, ok := v.(flang.BoolVal)
	return bool(t), ok
}

func toStr(v flang.Value) (string, bool) {
	switch t := v.(type) {
	case flang.StrVal:
		return string(t), true
	case flang.AtomVal:
		return string(t), true
	}
	return "", false
}

// propErr builds the message a wrongly-typed property write produces.
func propErr(name, want string, got flang.Value) error {
	return &scriptPropError{name: name, want: want, got: flang.TypeOf(got)}
}

type scriptPropError struct{ name, want, got string }

func (e *scriptPropError) Error() string {
	return "property " + e.name + " takes a " + e.want + ", not a " + e.got
}

// ---------- entity ----------

func entityProps() map[string]propAccess {
	get := func(fn func(e *world.Entity) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			e := c.entityFor(r)
			if e == nil {
				return nil, false
			}
			return fn(e), true
		}
	}
	set := func(fn func(e *world.Entity, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(c *refCache, r flang.RefVal, v flang.Value) error {
			e := c.entityFor(r)
			if e == nil {
				return &scriptMissingError{r.String()}
			}
			return fn(e, v)
		}
	}
	vecSet := func(name string, apply func(e *world.Entity, v mgl32.Vec3)) func(*world.Entity, flang.Value) error {
		return func(e *world.Entity, v flang.Value) error {
			p, ok := toVec3(v)
			if !ok {
				return propErr(name, "vec", v)
			}
			apply(e, p)
			return nil
		}
	}
	boolSet := func(name string, apply func(e *world.Entity, b bool)) func(*world.Entity, flang.Value) error {
		return func(e *world.Entity, v flang.Value) error {
			b, ok := toBool(v)
			if !ok {
				return propErr(name, "bool", v)
			}
			apply(e, b)
			return nil
		}
	}

	// Entity.Position is stored in the entity's OWN frame — an entity riding a
	// vehicle moves with it without its stored position changing. The property
	// has always been documented world-space, and every other position a
	// script can see (volumes, lights, and now sectors and refPosition's
	// entity arm) genuinely is, so these two convert rather than the doc
	// being weakened: a script comparing @a.prop.position against
	// @b.prop.position must be comparing numbers that can be subtracted.
	// Both conversions are the identity for an entity in no frame, which is
	// every entity in a level with no vehicles.
	frameGet := func(c *refCache, e *world.Entity) mgl32.Vec3 {
		if c.lvl == nil {
			return e.Position
		}
		return c.lvl.Frames.WorldPosition(e)
	}
	frameSet := func(c *refCache, e *world.Entity, p mgl32.Vec3) {
		if c.lvl == nil {
			e.Position = p
			return
		}
		e.Position = c.lvl.Frames.FrameFor(e).FromWorld(p)
	}

	// Velocity is integrated straight onto Position, so it is stored in the
	// same frame — and the same argument applies twice over: a script setting
	// a velocity is almost always aiming it at something (a direction to a
	// target, a world axis), and a rider's Y is not the ship's Y once the ship
	// rolls. A direction is a rotation only, which is why these use the Dir
	// pair rather than the point one; identity outside a frame either way.
	frameDirGet := func(c *refCache, e *world.Entity, d mgl32.Vec3) mgl32.Vec3 {
		if c.lvl == nil {
			return d
		}
		return c.lvl.Frames.FrameFor(e).DirToWorld(d)
	}
	frameDirSet := func(c *refCache, e *world.Entity, d mgl32.Vec3) mgl32.Vec3 {
		if c.lvl == nil {
			return d
		}
		return c.lvl.Frames.FrameFor(e).DirFromWorld(d)
	}

	return map[string]propAccess{
		"position": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				e := c.entityFor(r)
				if e == nil {
					return nil, false
				}
				return vecValue(frameGet(c, e)), true
			},
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				e := c.entityFor(r)
				if e == nil {
					return &scriptMissingError{r.String()}
				}
				p, ok := toVec3(v)
				if !ok {
					return propErr("position", "vec", v)
				}
				frameSet(c, e, p)
				return nil
			},
			Doc: "world-space position; writing it places the entity outright, with no collision check",
		},
		"rotation": {
			Get: get(func(e *world.Entity) flang.Value { return vecValue(e.Rotation) }),
			Set: set(vecSet("rotation", func(e *world.Entity, p mgl32.Vec3) { e.Rotation = p })),
			Doc: "Euler angles in radians",
		},
		"scale": {
			Get: get(func(e *world.Entity) flang.Value { return vecValue(e.EffectiveScale()) }),
			Set: set(vecSet("scale", func(e *world.Entity, p mgl32.Vec3) {
				e.Scale = mgl32.Vec3{
					world.EntityScaleClamp(p.X()), world.EntityScaleClamp(p.Y()), world.EntityScaleClamp(p.Z()),
				}
			})),
			Doc: "per-axis scale; clamped away from zero",
		},
		"waypoints": waypointsProp(),
		"velocity": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				e := c.entityFor(r)
				if e == nil {
					return nil, false
				}
				return vecValue(frameDirGet(c, e, e.Velocity)), true
			},
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				e := c.entityFor(r)
				if e == nil {
					return &scriptMissingError{r.String()}
				}
				d, ok := toVec3(v)
				if !ok {
					return propErr("velocity", "vec", v)
				}
				e.Velocity = frameDirSet(c, e, d)
				return nil
			},
			Doc: "world-space velocity, in units per second",
		},
		"angular_velocity": {
			Get: get(func(e *world.Entity) flang.Value { return vecValue(e.AngularVelocity) }),
			Set: set(vecSet("angular_velocity", func(e *world.Entity, p mgl32.Vec3) { e.AngularVelocity = p })),
			Doc: "radians per second about each Euler axis",
		},
		"visible": {
			Get: get(func(e *world.Entity) flang.Value { return flang.BoolVal(e.Visible) }),
			Set: set(boolSet("visible", func(e *world.Entity, b bool) { e.Visible = b })),
			Doc: "whether the entity's models draw",
		},
		"texture": {
			// An entity is a list of attachments, so there is no single texture
			// to report. The first one is what the property means, and writing
			// reskins them all; a script that wants one attachment of several
			// writes @entity.model.<name>.prop.texture instead.
			Get: get(func(e *world.Entity) flang.Value { return flang.StrVal(entityTextureName(e)) }),
			Set: set(func(e *world.Entity, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("texture", "str", v)
				}
				e.SetTexture("", s)
				return nil
			}),
			// No rebuild: an entity's mesh is unchanged by a reskin, and every
			// draw resolves the name afresh.
			Tex: true,
			// Reskinning an entity with no attachments loaded is not an error —
			// the ref resolved and the write was legal — but it is almost
			// always a mistake, and silence is what made it hard to find.
			Warn: func(c *refCache, r flang.RefVal) string {
				e := c.entityFor(r)
				if e == nil {
					return ""
				}
				return e.TextureProblem("")
			},
			Doc: "texture every attachment draws with, resolved against textures/",
		},
		"bounding": {
			Get: get(func(e *world.Entity) flang.Value { return flang.AtomVal(world.BoundingTypeName(e.BoundingKind)) }),
			Set: set(func(e *world.Entity, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("bounding", "str or atom", v)
				}
				e.BoundingKind = world.BoundingKindFromName(s)
				return nil
			}),
			Doc: "aabb, sphere or cylinder",
		},
		"bound_half": {
			Get: get(func(e *world.Entity) flang.Value {
				return flang.VecVal{float64(e.BoundHalfX), float64(e.BoundHalfY), float64(e.BoundHalfZ)}
			}),
			Set: set(vecSet("bound_half", func(e *world.Entity, p mgl32.Vec3) {
				e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ = p.X(), p.Y(), p.Z()
			})),
			Doc: "half-extents of the bounding volume",
		},
		"bound_offset": {
			Get: get(func(e *world.Entity) flang.Value { return vecValue(e.BoundOffset) }),
			Set: set(vecSet("bound_offset", func(e *world.Entity, p mgl32.Vec3) { e.BoundOffset = p })),
			Doc: "offset of the volume's centre from the entity's origin",
		},
		"collision_world": {
			Get: get(func(e *world.Entity) flang.Value { return flang.BoolVal(e.Collision.World) }),
			Set: set(boolSet("collision_world", func(e *world.Entity, b bool) { e.Collision.World = b })),
			Doc: "resolve against walls, floors and slopes",
		},
		"collision_entities": {
			Get: get(func(e *world.Entity) flang.Value { return flang.BoolVal(e.Collision.Entities) }),
			Set: set(boolSet("collision_entities", func(e *world.Entity, b bool) { e.Collision.Entities = b })),
			Doc: "push apart from other entities that also set it",
		},
		"collision_gravity": {
			Get: get(func(e *world.Entity) flang.Value { return flang.BoolVal(e.Collision.Gravity) }),
			Set: set(boolSet("collision_gravity", func(e *world.Entity, b bool) { e.Collision.Gravity = b })),
			Doc: "fall, and stop when something is underneath",
		},
		"collision_movable": {
			Get: get(func(e *world.Entity) flang.Value { return flang.BoolVal(e.Collision.Movable) }),
			Set: set(boolSet("collision_movable", func(e *world.Entity, b bool) { e.Collision.Movable = b })),
			Doc: "whether a resolution may displace this entity",
		},

		// Derived state. Read-only because writing it would be overwritten by
		// EntityUpdate on the very next frame, which is worse than refusing.
		"on_ground": {
			Get: get(func(e *world.Entity) flang.Value { return flang.BoolVal(e.Collision.OnGround) }),
			Doc: "read-only: whether the entity is resting on something",
		},
		// Where the entity looks from, and which way, in world space. For the
		// player both are the camera — the reticle hitscan casts along — because
		// its look is the camera's yaw and pitch, which Entity.Rotation never
		// tracks. For anything else they are the eye an AI agent sees from and
		// the entity's own facing, its local −Z.
		"eye_position": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				e := c.entityFor(r)
				if e == nil {
					return nil, false
				}
				return vecValue(entityEye(c.lvl, e)), true
			},
			Doc: "read-only: where the entity looks from, in world space — the camera for the player",
		},
		"crouching": {
			Get: get(func(e *world.Entity) flang.Value {
				return flang.BoolVal(e == world.PlayerEntity && world.PlayerCrouched())
			}),
			Doc: "read-only: whether the player is crouched (C or left Ctrl); always false for any other entity",
		},
		"sprinting": {
			Get: get(func(e *world.Entity) flang.Value {
				return flang.BoolVal(e == world.PlayerEntity && world.PlayerSprinting())
			}),
			Set: set(func(e *world.Entity, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("sprinting", "bool", v)
				}
				if e != world.PlayerEntity {
					return fmt.Errorf("only the player sprints; @%s has no sprint to set", e.ID)
				}
				world.SetPlayerSprinting(b)
				return nil
			}),
			Doc: "whether the player is sprinting (held on Left Shift while pushing forward; crouching or sitting ends it). Writable on the player: false ends it and keeps it off while the key stays down (firing, aiming), true lets a still-held key sprint again",
		},
		"look_direction": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				e := c.entityFor(r)
				if e == nil {
					return nil, false
				}
				return vecValue(entityLook(c.lvl, e)), true
			},
			Doc: "read-only: the unit direction the entity looks, in world space — the camera's aim for the player",
		},
		"type": {
			Get: get(func(e *world.Entity) flang.Value { return flang.StrVal(e.Type) }),
			Doc: "read-only: the definition this instance came from",
		},
		"current_chunk": {
			Get: get(func(e *world.Entity) flang.Value { return flang.StrVal(e.CurrentChunk) }),
			Doc: "read-only: the chunk the entity is in, refreshed each frame",
		},

		// ---------- riding ----------

		"frame": {
			Get: get(func(e *world.Entity) flang.Value {
				if e.Frame == "" {
					return flang.NullVal{}
				}
				return flang.RefVal{Kind: flang.RefEntity, A: e.Frame}
			}),
			Doc: "read-only: the vehicle this entity is riding in, or null",
		},
		// The one position that does NOT convert to world space. A rider's own
		// movement is its change here; `position` also carries the vehicle's
		// motion, so a passenger standing still on a falling ship reads as
		// walking. Same as `position` for an entity in no frame.
		"local_position": {
			Get: get(func(e *world.Entity) flang.Value { return vecValue(e.Position) }),
			Doc: "read-only: position in the frame the entity rides in (world-space when not riding)",
		},
		"seat": {
			Get: get(func(e *world.Entity) flang.Value { return flang.StrVal(e.Seat) }),
			Doc: "read-only: which seat this entity occupies, empty when standing",
		},

		// ---------- driving ----------
		//
		// All of these read null or zero on an entity that is not a vehicle,
		// rather than erroring: `@thing.prop.vehicle_speed` is a reasonable
		// question to ask about something that might be one.

		"vehicle_speed": {
			Get: get(func(e *world.Entity) flang.Value {
				flat := mgl32.Vec3{e.Velocity.X(), 0, e.Velocity.Z()}
				return flang.FloatVal(float64(flat.Len()))
			}),
			Doc: "read-only: horizontal speed in units per second",
		},
		"vehicle_driver": {
			Get: get(func(e *world.Entity) flang.Value {
				id := e.Driver()
				if id == "" {
					return flang.NullVal{}
				}
				return flang.RefVal{Kind: flang.RefEntity, A: id}
			}),
			Doc: "read-only: whoever is in a seat that hands over the controls, or null",
		},
		"vehicle_throttle": {
			Get: vehicleGet(func(e *world.Entity) float32 { return e.VState.Controls.Throttle }),
			Set: vehicleSet("vehicle_throttle", func(e *world.Entity, f float32) {
				e.VState.Controls.Throttle = clamp11(f)
			}),
			Doc: "throttle, -1 to 1",
		},
		"vehicle_steer": {
			Get: vehicleGet(func(e *world.Entity) float32 { return e.VState.Controls.Steer }),
			Set: vehicleSet("vehicle_steer", func(e *world.Entity, f float32) {
				e.VState.Controls.Steer = clamp11(f)
			}),
			Doc: "steering, -1 to 1",
		},
		"vehicle_pitch": {
			Get: vehicleGet(func(e *world.Entity) float32 { return e.VState.Controls.Pitch }),
			Set: vehicleSet("vehicle_pitch", func(e *world.Entity, f float32) {
				e.VState.Controls.Pitch = clamp11(f)
			}),
			Doc: "pitch control, -1 to 1",
		},
		"vehicle_roll": {
			Get: vehicleGet(func(e *world.Entity) float32 { return e.VState.Controls.Roll }),
			Set: vehicleSet("vehicle_roll", func(e *world.Entity, f float32) {
				e.VState.Controls.Roll = clamp11(f)
			}),
			Doc: "roll control, -1 to 1",
		},

		// The drive tuning, writable while running. "Tuned by attributes" has to
		// mean tunable during play, or it is a hard-coded model with a config
		// file — a script that makes a car handle differently in the rain needs
		// these to move.
		"vehicle_max_speed":  driveProp("vehicle_max_speed", func(d *world.DriveSpec) *float32 { return &d.MaxSpeed }),
		"vehicle_accel":      driveProp("vehicle_accel", func(d *world.DriveSpec) *float32 { return &d.Accel }),
		"vehicle_brake":      driveProp("vehicle_brake", func(d *world.DriveSpec) *float32 { return &d.Brake }),
		"vehicle_turn_rate":  driveProp("vehicle_turn_rate", func(d *world.DriveSpec) *float32 { return &d.TurnRate }),
		"vehicle_pitch_rate": driveProp("vehicle_pitch_rate", func(d *world.DriveSpec) *float32 { return &d.PitchRate }),
		"vehicle_roll_rate":  driveProp("vehicle_roll_rate", func(d *world.DriveSpec) *float32 { return &d.RollRate }),
		"vehicle_drag":       driveProp("vehicle_drag", func(d *world.DriveSpec) *float32 { return &d.Drag }),
	}
}

// entityTextureName reports what the entity's first attachment draws with: its
// override if it has one, otherwise whatever the loaded mesh ended up wearing.
// Empty for an entity with no models, or one whose models have not loaded.
func entityTextureName(e *world.Entity) string {
	if len(e.Models) == 0 {
		return ""
	}
	m := &e.Models[0]
	if m.Texture != "" {
		return m.Texture
	}
	if len(m.Submeshes) > 0 {
		return m.Submeshes[0].Texture
	}
	return ""
}

// ---------- volume ----------

// volumeAccessors returns the pair of adapters every volume property is built
// from: a getter or setter written against a *world.Volume, lifted to one that
// resolves the ref first. A function rather than two closures inside
// volumeProps so the property builders further down (volumeCloudNumberProp)
// can build on the same pair.
func volumeAccessors() (
	get func(fn func(v *world.Volume) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool),
	set func(fn func(vol *world.Volume, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error,
) {
	get = func(fn func(v *world.Volume) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			vol := c.volumeFor(r)
			if vol == nil {
				return nil, false
			}
			return fn(vol), true
		}
	}
	set = func(fn func(vol *world.Volume, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(c *refCache, r flang.RefVal, v flang.Value) error {
			vol := c.volumeFor(r)
			if vol == nil {
				return &scriptMissingError{r.String()}
			}
			return fn(vol, v)
		}
	}
	return get, set
}

func volumeProps() map[string]propAccess {
	get, set := volumeAccessors()

	return map[string]propAccess{
		"position": {
			Get: get(func(v *world.Volume) flang.Value { return vecValue(v.Position) }),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("position", "vec", v)
				}
				// No rebuild: the cage is built in volume-local space, so moving
				// one only changes the matrix it is drawn with.
				vol.Position = p
				return nil
			}),
			Doc: "position — world-space, or an offset in the carrier's space while attached",
		},
		"attach": {
			Get: get(func(v *world.Volume) flang.Value { return flang.StrVal(v.Attach.Describe()) }),
			Doc: "what the volume rides, as the editor names it; empty when it is in world space (read-only)",
		},
		"carrier": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				vol := c.volumeFor(r)
				if vol == nil {
					return nil, false
				}
				return carrierValue(c, vol.Attach), true
			},
			Doc: "a ref to the entity the volume rides, or null (read-only)",
		},
		"rotation": {
			Get: get(func(v *world.Volume) flang.Value { return vecValue(v.Rotation) }),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("rotation", "vec", v)
				}
				vol.Rotation = p
				return nil
			}),
			Doc: "Euler angles in radians",
		},
		"waypoints": waypointsProp(),
		"half": {
			Get: get(func(v *world.Volume) flang.Value {
				return flang.VecVal{float64(v.HalfX), float64(v.HalfY), float64(v.HalfZ)}
			}),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("half", "vec", v)
				}
				vol.HalfX, vol.HalfY, vol.HalfZ = p.X(), p.Y(), p.Z()
				return nil
			}),
			// Resizing changes the cage's shape, which is the one volume edit
			// that needs a rebuild.
			Dirty: true,
			Doc:   "half-extents of the region",
		},
		"shape": {
			Get: get(func(v *world.Volume) flang.Value { return flang.AtomVal(world.VolumeShapeName(v.Shape)) }),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("shape", "str or atom", v)
				}
				vol.Shape = world.VolumeShapeFromName(s)
				return nil
			}),
			Dirty: true,
			Doc:   "box, sphere or cylinder",
		},
		"enabled": {
			Get: get(func(v *world.Volume) flang.Value { return flang.BoolVal(v.Enabled) }),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("enabled", "bool", v)
				}
				vol.Enabled = b
				return nil
			}),
			Doc: "a disabled volume contains nothing, so it fires no events",
		},
		"load_level": {
			Get: get(func(v *world.Volume) flang.Value { return flang.StrVal(v.LoadLevel) }),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("load_level", "str", v)
				}
				vol.LoadLevel = s
				return nil
			}),
			// Writable so a hub level can re-point the same door at whichever
			// area the player last unlocked, rather than needing one volume per
			// destination stacked in the same doorway.
			Doc: "the level the player walks into this volume to load; \"\" for an ordinary volume",
		},
		"checkpoint": {
			Get: get(func(v *world.Volume) flang.Value { return flang.BoolVal(v.Checkpoint) }),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("checkpoint", "bool", v)
				}
				vol.Checkpoint = b
				return nil
			}),
			Doc: "a checkpoint: the first time the player walks in, the game autosaves and retry() comes back here",
		},
		"fog": {
			Get: get(func(v *world.Volume) flang.Value { return flang.BoolVal(v.Fog != nil && v.Fog.Enabled) }),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("fog", "bool", v)
				}
				return volumeFogSet(vol, b, func(f *world.VolumeFog) { f.Enabled = b })
			}),
			Doc: "whether the region is filled with fog",
		},
		"fog_color": {
			Get: get(func(v *world.Volume) flang.Value { return vecValue(volumeFogOf(v).Color) }),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("fog_color", "vec", v)
				}
				return volumeFogSet(vol, true, func(f *world.VolumeFog) { f.Color = p })
			}),
			Doc: "the fog's colour as a vec of r, g, b",
		},
		"fog_density": {
			Get: get(func(v *world.Volume) flang.Value { return flang.FloatVal(volumeFogOf(v).Density) }),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				f32, ok := toFloat32(v)
				if !ok {
					return propErr("fog_density", "number", v)
				}
				return volumeFogSet(vol, true, func(f *world.VolumeFog) { f.Density = f32 })
			}),
			Doc: "fog thickness per world unit travelled inside the region",
		},
		"fog_max_opacity": {
			Get: get(func(v *world.Volume) flang.Value { return flang.FloatVal(volumeFogOf(v).MaxOpacity) }),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				f32, ok := toFloat32(v)
				if !ok {
					return propErr("fog_max_opacity", "number", v)
				}
				return volumeFogSet(vol, true, func(f *world.VolumeFog) { f.MaxOpacity = f32 })
			}),
			Doc: "ceiling on how much the fog can hide, 0 to 1",
		},
		"scatter": {
			Get: get(func(v *world.Volume) flang.Value {
				return flang.BoolVal(v.Scatter != nil && v.Scatter.Enabled)
			}),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("scatter", "bool", v)
				}
				return volumeScatterSet(vol, b, func(s *world.VolumeScatter) { s.Enabled = b })
			}),
			Doc: "whether the region grows instanced foliage",
		},
		"scatter_kind": {
			Get: get(func(v *world.Volume) flang.Value {
				return flang.StrVal(world.ScatterKindName(volumeScatterOf(v).Kind))
			}),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				name, ok := toStr(v)
				if !ok {
					return propErr("scatter_kind", "str", v)
				}
				return volumeScatterSet(vol, true, func(s *world.VolumeScatter) {
					s.Kind = world.ScatterKindFromName(name)
				})
			}),
			Doc: "what grows: grass, rock, wildflower, cattail or moss",
		},
		"scatter_density": {
			Get: get(func(v *world.Volume) flang.Value {
				return flang.FloatVal(volumeScatterOf(v).Density)
			}),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				f32, ok := toFloat32(v)
				if !ok {
					return propErr("scatter_density", "number", v)
				}
				return volumeScatterSet(vol, true, func(s *world.VolumeScatter) { s.Density = f32 })
			}),
			Doc: "how thickly it grows, 0 to 1",
		},
		"scatter_range": {
			Get: get(func(v *world.Volume) flang.Value {
				return flang.FloatVal(world.ScatterRange(volumeScatterRef(v)))
			}),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				f32, ok := toFloat32(v)
				if !ok {
					return propErr("scatter_range", "number", v)
				}
				return volumeScatterSet(vol, true, func(s *world.VolumeScatter) { s.Range = f32 })
			}),
			Doc: "how far from the camera the foliage is drawn, in world units",
		},
		"scatter_color": {
			Get: get(func(v *world.Volume) flang.Value { return vecValue(volumeScatterOf(v).Color) }),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("scatter_color", "vec", v)
				}
				return volumeScatterSet(vol, true, func(s *world.VolumeScatter) { s.Color = p })
			}),
			Doc: "the foliage's tint as a vec of r, g, b",
		},
		"cloud": {
			Get: get(func(v *world.Volume) flang.Value { return flang.BoolVal(v.Cloud != nil && v.Cloud.Enabled) }),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("cloud", "bool", v)
				}
				return volumeCloudSet(vol, b, func(c *world.VolumeCloud) { c.Enabled = b })
			}),
			Doc: "whether the region is filled with volumetric cloud",
		},
		"clears_clouds": {
			Get: get(func(v *world.Volume) flang.Value { return flang.BoolVal(v.ClearsClouds) }),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("clears_clouds", "bool", v)
				}
				vol.ClearsClouds = b
				return nil
			}),
			// Writable so a hold can fill with the bank the moment its ramp
			// drops, rather than needing a second volume to swap in.
			Doc: "whether no volumetric cloud is drawn inside the region",
		},
		"cloud_coverage": volumeCloudNumberProp("cloud_coverage",
			func(c *world.VolumeCloud) *float32 { return &c.Coverage },
			"how much of the region the cloud fills, 0 to 1"),
		"cloud_density": volumeCloudNumberProp("cloud_density",
			func(c *world.VolumeCloud) *float32 { return &c.Density },
			"the cloud's thickness per world unit at its densest"),
		"cloud_scale": volumeCloudNumberProp("cloud_scale",
			func(c *world.VolumeCloud) *float32 { return &c.Scale },
			"world units across the cloud's largest lumps"),
		"cloud_erosion": volumeCloudNumberProp("cloud_erosion",
			func(c *world.VolumeCloud) *float32 { return &c.Erosion },
			"how ragged the cloud's edges are, 0 to 1"),
		"cloud_softness": volumeCloudNumberProp("cloud_softness",
			func(c *world.VolumeCloud) *float32 { return &c.Softness },
			"how far in from the region's walls the cloud fades up, 0 to 1"),
		"cloud_color": volumeCloudVecProp("cloud_color",
			func(c *world.VolumeCloud) *mgl32.Vec3 { return &c.Color },
			"the cloud's colour as a vec of r, g, b"),
		"cloud_wind": volumeCloudVecProp("cloud_wind",
			func(c *world.VolumeCloud) *mgl32.Vec3 { return &c.Wind },
			"how fast the cloud's pattern drifts, in world units per second"),
		"cloud_shadows": {
			Get: get(func(v *world.Volume) flang.Value { return flang.BoolVal(volumeCloudOf(v).CastShadows) }),
			Set: set(func(vol *world.Volume, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("cloud_shadows", "bool", v)
				}
				return volumeCloudSet(vol, true, func(c *world.VolumeCloud) { c.CastShadows = b })
			}),
			Doc: "whether the cloud casts its shadow on what is under it",
		},
	}
}

// ---------- volume cloud property helpers ----------
//
// The fog helpers' shape, for the fog helpers' reasons: a volume's cloud is a
// pointer, a plain trigger has none, reading one returns the defaults, and
// writing any cloud_* property gives it one.

// volumeCloudOf is the volume's cloud, or a default one to read fields off —
// a COPY when the volume has none.
func volumeCloudOf(v *world.Volume) world.VolumeCloud {
	if v.Cloud == nil {
		return world.DefaultVolumeCloud()
	}
	return *v.Cloud
}

// volumeCloudSet applies fn to the volume's cloud, creating one at the defaults
// first unless create is false — the write that switches cloud OFF, which
// leaves a volume with none as it found it.
func volumeCloudSet(v *world.Volume, create bool, fn func(*world.VolumeCloud)) error {
	if v.Cloud == nil {
		if !create {
			return nil
		}
		c := world.DefaultVolumeCloud()
		v.Cloud = &c
	}
	fn(v.Cloud)
	v.Cloud.Clamp()
	return nil
}

// volumeCloudNumberProp builds one number-valued cloud_* property.
func volumeCloudNumberProp(name string, field func(*world.VolumeCloud) *float32, doc string) propAccess {
	get, set := volumeAccessors()
	return propAccess{
		Get: get(func(v *world.Volume) flang.Value {
			c := volumeCloudOf(v)
			return flang.FloatVal(float64(*field(&c)))
		}),
		Set: set(func(vol *world.Volume, v flang.Value) error {
			f, ok := toFloat32(v)
			if !ok {
				return propErr(name, "number", v)
			}
			return volumeCloudSet(vol, true, func(c *world.VolumeCloud) { *field(c) = f })
		}),
		Doc: doc,
	}
}

// volumeCloudVecProp builds one vec-valued cloud_* property.
func volumeCloudVecProp(name string, field func(*world.VolumeCloud) *mgl32.Vec3, doc string) propAccess {
	get, set := volumeAccessors()
	return propAccess{
		Get: get(func(v *world.Volume) flang.Value {
			c := volumeCloudOf(v)
			return vecValue(*field(&c))
		}),
		Set: set(func(vol *world.Volume, v flang.Value) error {
			p, ok := toVec3(v)
			if !ok {
				return propErr(name, "vec", v)
			}
			return volumeCloudSet(vol, true, func(c *world.VolumeCloud) { *field(c) = p })
		}),
		Doc: doc,
	}
}

// ---------- volume fog property helpers ----------
//
// A volume's fog is a pointer, and a volume authored as a plain trigger has
// none. Reading one of those is a fair question with a dull answer, so it
// returns the defaults rather than erroring — the same shape the vehicle
// helpers below use.

// volumeFogOf is the volume's fog, or a default one to read fields off. The
// returned value is a COPY when the volume has no fog, so nothing written
// through it reaches the level by accident.
func volumeFogOf(v *world.Volume) world.VolumeFog {
	if v.Fog == nil {
		return world.DefaultVolumeFog()
	}
	return *v.Fog
}

// volumeFogSet applies fn to the volume's fog, creating one at the defaults if
// there was none.
//
// Creating rather than erroring, because the alternative is that a level has to
// author an invisible fog on every volume a script might ever want to fill —
// and `@cellar.prop.fog = true` on a plain volume has exactly one sensible
// reading. create is false only for the write that turns fog OFF: switching off
// fog that was never there should leave the volume as it found it rather than
// giving it a switched-off block that the next save would then write out.
func volumeFogSet(v *world.Volume, create bool, fn func(*world.VolumeFog)) error {
	if v.Fog == nil {
		if !create {
			return nil
		}
		f := world.DefaultVolumeFog()
		v.Fog = &f
	}
	fn(v.Fog)
	v.Fog.Clamp()
	return nil
}

// ---------- volume scatter property helpers ----------
//
// The same shape the fog helpers above have, and for the same reasons: a
// volume's scatter is a pointer, a plain trigger has none, and reading one of
// those is a fair question with a dull answer.

// volumeScatterOf is the volume's scatter, or a default one to read fields off.
// The returned value is a COPY when the volume has none, so nothing written
// through it reaches the level by accident.
func volumeScatterOf(v *world.Volume) world.VolumeScatter {
	if v.Scatter == nil {
		return world.DefaultVolumeScatter()
	}
	return *v.Scatter
}

// volumeScatterRef is volumeScatterOf for the resolvers that take a pointer
// (world.ScatterRange and friends, which read Kind to pick a default). The
// value it points at is a copy on the volume-has-none path, so this is still a
// read-only view.
func volumeScatterRef(v *world.Volume) *world.VolumeScatter {
	s := volumeScatterOf(v)
	return &s
}

// volumeScatterSet applies fn to the volume's scatter, creating one at the
// defaults if there was none — see volumeFogSet on why a write should not have
// to be preceded by switching the feature on. create is false only for the
// write that turns scatter OFF, so switching off scatter that was never there
// leaves the volume as it found it rather than giving it a switched-off block
// the next save would write out.
func volumeScatterSet(v *world.Volume, create bool, fn func(*world.VolumeScatter)) error {
	if v.Scatter == nil {
		if !create {
			return nil
		}
		s := world.DefaultVolumeScatter()
		v.Scatter = &s
	}
	fn(v.Scatter)
	return nil
}

// scriptMissingError is what a write to a vanished object produces.
type scriptMissingError struct{ what string }

func (e *scriptMissingError) Error() string { return e.what + " no longer exists" }

// ---------- vehicle property helpers ----------
//
// These exist because every vehicle property has the same two guards in front
// of it: the ref has to resolve to an entity, and that entity has to actually be
// a vehicle. Reading one of something that is not returns zero rather than
// erroring — asking a crate how fast it is going is a fair question with a dull
// answer — while *writing* one is a mistake worth reporting, because a script
// steering a barrel is not doing what its author thinks.

func vehicleGet(fn func(e *world.Entity) float32) func(*refCache, flang.RefVal) (flang.Value, bool) {
	return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
		e := c.entityFor(r)
		if e == nil {
			return nil, false
		}
		if e.EnsureVehicleState() == nil {
			return flang.FloatVal(0), true
		}
		return flang.FloatVal(float64(fn(e))), true
	}
}

func vehicleSet(name string, fn func(e *world.Entity, f float32)) func(*refCache, flang.RefVal, flang.Value) error {
	return func(c *refCache, r flang.RefVal, v flang.Value) error {
		e := c.entityFor(r)
		if e == nil {
			return &scriptMissingError{r.String()}
		}
		f, ok := toFloat32(v)
		if !ok {
			return propErr(name, "number", v)
		}
		if e.EnsureVehicleState() == nil {
			return fmt.Errorf("%s: %s is not a vehicle", name, r.String())
		}
		fn(e, f)
		return nil
	}
}

// driveProp builds a get/set pair for one field of the drive tuning.
func driveProp(name string, field func(*world.DriveSpec) *float32) propAccess {
	return propAccess{
		Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			e := c.entityFor(r)
			if e == nil {
				return nil, false
			}
			if e.Vehicle == nil {
				return flang.FloatVal(0), true
			}
			return flang.FloatVal(float64(*field(&e.Vehicle.Drive))), true
		},
		Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
			e := c.entityFor(r)
			if e == nil {
				return &scriptMissingError{r.String()}
			}
			f, ok := toFloat32(v)
			if !ok {
				return propErr(name, "number", v)
			}
			if e.Vehicle == nil {
				return fmt.Errorf("%s: %s is not a vehicle", name, r.String())
			}
			*field(&e.Vehicle.Drive) = f
			return nil
		},
		Doc: "drive tuning, writable during play",
	}
}

func clamp11(f float32) float32 {
	if f < -1 {
		return -1
	}
	if f > 1 {
		return 1
	}
	return f
}

// entityEye is eye_position: the camera for the player, an agent's eye for
// anything else.
func entityEye(lvl *world.Level, e *world.Entity) mgl32.Vec3 {
	if e == world.PlayerEntity {
		return gfx.Cam.Position
	}
	if lvl == nil {
		return e.Position
	}
	return ai.EyeOf(lvl, e)
}

// entityLook is look_direction: the camera's aim for the player, the entity's
// own −Z carried into world space for anything else.
func entityLook(lvl *world.Level, e *world.Entity) mgl32.Vec3 {
	if e == world.PlayerEntity {
		if d := gfx.Cam.Direction; d.Len() > 1e-6 {
			return d.Normalize()
		}
		return mgl32.Vec3{0, 0, -1}
	}
	d := e.RotationMatrix().Mul4x1(mgl32.Vec4{0, 0, -1, 0}).Vec3()
	if lvl != nil {
		d = lvl.Frames.FrameFor(e).DirToWorld(d)
	}
	if d.Len() < 1e-6 {
		return mgl32.Vec3{0, 0, -1}
	}
	return d.Normalize()
}
