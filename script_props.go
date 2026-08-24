package main

// script_props.go — the `.prop` surface.
//
// A declarative table rather than a switch, because three separate things need
// to *enumerate* it: the script editor's completion list, the reference panel,
// and the "no property called `visble`, did you mean `visible`" message. A switch
// cannot be enumerated. This codebase already makes exactly that argument at
// entity.go's boundingTypeNames — "a per-file switch is what lets an unknown
// name quietly load as the wrong shape".
//
// The `dirty` flag is the other reason the table earns its place. Many property
// writes are geometry-mutating — a floor's height, a wall's texture — and
// applying one means reloadSector, which means GL, which cannot happen inside
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
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
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
	Doc string
}

// propTables maps each object kind to its properties.
var propTables map[flang.Kind_]map[string]propAccess

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
	get := func(fn func(e *Entity) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			e := c.entityFor(r)
			if e == nil {
				return nil, false
			}
			return fn(e), true
		}
	}
	set := func(fn func(e *Entity, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(c *refCache, r flang.RefVal, v flang.Value) error {
			e := c.entityFor(r)
			if e == nil {
				return &scriptMissingError{r.String()}
			}
			return fn(e, v)
		}
	}
	vecSet := func(name string, apply func(e *Entity, v mgl32.Vec3)) func(*Entity, flang.Value) error {
		return func(e *Entity, v flang.Value) error {
			p, ok := toVec3(v)
			if !ok {
				return propErr(name, "vec", v)
			}
			apply(e, p)
			return nil
		}
	}
	boolSet := func(name string, apply func(e *Entity, b bool)) func(*Entity, flang.Value) error {
		return func(e *Entity, v flang.Value) error {
			b, ok := toBool(v)
			if !ok {
				return propErr(name, "bool", v)
			}
			apply(e, b)
			return nil
		}
	}

	return map[string]propAccess{
		"position": {
			Get: get(func(e *Entity) flang.Value { return vecValue(e.Position) }),
			Set: set(vecSet("position", func(e *Entity, p mgl32.Vec3) { e.Position = p })),
			Doc: "world-space position",
		},
		"rotation": {
			Get: get(func(e *Entity) flang.Value { return vecValue(e.Rotation) }),
			Set: set(vecSet("rotation", func(e *Entity, p mgl32.Vec3) { e.Rotation = p })),
			Doc: "Euler angles in radians",
		},
		"scale": {
			Get: get(func(e *Entity) flang.Value { return vecValue(e.effectiveScale()) }),
			Set: set(vecSet("scale", func(e *Entity, p mgl32.Vec3) {
				e.Scale = mgl32.Vec3{
					entityScaleClamp(p.X()), entityScaleClamp(p.Y()), entityScaleClamp(p.Z()),
				}
			})),
			Doc: "per-axis scale; clamped away from zero",
		},
		"velocity": {
			Get: get(func(e *Entity) flang.Value { return vecValue(e.Velocity) }),
			Set: set(vecSet("velocity", func(e *Entity, p mgl32.Vec3) { e.Velocity = p })),
			Doc: "world units per second",
		},
		"angular_velocity": {
			Get: get(func(e *Entity) flang.Value { return vecValue(e.AngularVelocity) }),
			Set: set(vecSet("angular_velocity", func(e *Entity, p mgl32.Vec3) { e.AngularVelocity = p })),
			Doc: "radians per second about each Euler axis",
		},
		"visible": {
			Get: get(func(e *Entity) flang.Value { return flang.BoolVal(e.Visible) }),
			Set: set(boolSet("visible", func(e *Entity, b bool) { e.Visible = b })),
			Doc: "whether the entity's models draw",
		},
		"texture": {
			// An entity is a list of attachments, so there is no single texture
			// to report. The first one is what the property means, and writing
			// reskins them all; a script that wants one attachment of several
			// calls set_texture(ref, model, name).
			Get: get(func(e *Entity) flang.Value { return flang.StrVal(entityTextureName(e)) }),
			Set: set(func(e *Entity, v flang.Value) error {
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
			Doc: "texture every attachment draws with, resolved against textures/",
		},
		"bounding": {
			Get: get(func(e *Entity) flang.Value { return flang.AtomVal(boundingTypeName(e.BoundingKind)) }),
			Set: set(func(e *Entity, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("bounding", "str or atom", v)
				}
				e.BoundingKind = boundingKindFromName(s)
				return nil
			}),
			Doc: "aabb, sphere or cylinder",
		},
		"bound_half": {
			Get: get(func(e *Entity) flang.Value {
				return flang.VecVal{float64(e.BoundHalfX), float64(e.BoundHalfY), float64(e.BoundHalfZ)}
			}),
			Set: set(vecSet("bound_half", func(e *Entity, p mgl32.Vec3) {
				e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ = p.X(), p.Y(), p.Z()
			})),
			Doc: "half-extents of the bounding volume",
		},
		"bound_offset": {
			Get: get(func(e *Entity) flang.Value { return vecValue(e.BoundOffset) }),
			Set: set(vecSet("bound_offset", func(e *Entity, p mgl32.Vec3) { e.BoundOffset = p })),
			Doc: "offset of the volume's centre from the entity's origin",
		},
		"collision_world": {
			Get: get(func(e *Entity) flang.Value { return flang.BoolVal(e.Collision.World) }),
			Set: set(boolSet("collision_world", func(e *Entity, b bool) { e.Collision.World = b })),
			Doc: "resolve against walls, floors and slopes",
		},
		"collision_entities": {
			Get: get(func(e *Entity) flang.Value { return flang.BoolVal(e.Collision.Entities) }),
			Set: set(boolSet("collision_entities", func(e *Entity, b bool) { e.Collision.Entities = b })),
			Doc: "push apart from other entities that also set it",
		},
		"collision_gravity": {
			Get: get(func(e *Entity) flang.Value { return flang.BoolVal(e.Collision.Gravity) }),
			Set: set(boolSet("collision_gravity", func(e *Entity, b bool) { e.Collision.Gravity = b })),
			Doc: "fall, and stop when something is underneath",
		},
		"collision_movable": {
			Get: get(func(e *Entity) flang.Value { return flang.BoolVal(e.Collision.Movable) }),
			Set: set(boolSet("collision_movable", func(e *Entity, b bool) { e.Collision.Movable = b })),
			Doc: "whether a resolution may displace this entity",
		},

		// Derived state. Read-only because writing it would be overwritten by
		// EntityUpdate on the very next frame, which is worse than refusing.
		"on_ground": {
			Get: get(func(e *Entity) flang.Value { return flang.BoolVal(e.Collision.OnGround) }),
			Doc: "read-only: whether the entity is resting on something",
		},
		"type": {
			Get: get(func(e *Entity) flang.Value { return flang.StrVal(e.Type) }),
			Doc: "read-only: the definition this instance came from",
		},
		"current_sector": {
			Get: get(func(e *Entity) flang.Value { return flang.StrVal(e.CurrentSector) }),
			Doc: "read-only: the sector the entity is in, refreshed each frame",
		},
		"current_chunk": {
			Get: get(func(e *Entity) flang.Value { return flang.StrVal(e.CurrentChunk) }),
			Doc: "read-only: the chunk the entity is in, refreshed each frame",
		},
	}
}

// entityTextureName reports what the entity's first attachment draws with: its
// override if it has one, otherwise whatever the loaded mesh ended up wearing.
// Empty for an entity with no models, or one whose models have not loaded.
func entityTextureName(e *Entity) string {
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

func volumeProps() map[string]propAccess {
	get := func(fn func(v *Volume) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			vol := c.volumeFor(r)
			if vol == nil {
				return nil, false
			}
			return fn(vol), true
		}
	}
	set := func(fn func(vol *Volume, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(c *refCache, r flang.RefVal, v flang.Value) error {
			vol := c.volumeFor(r)
			if vol == nil {
				return &scriptMissingError{r.String()}
			}
			return fn(vol, v)
		}
	}

	return map[string]propAccess{
		"position": {
			Get: get(func(v *Volume) flang.Value { return vecValue(v.Position) }),
			Set: set(func(vol *Volume, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("position", "vec", v)
				}
				// No rebuild: the cage is built in volume-local space, so moving
				// one only changes the matrix it is drawn with.
				vol.Position = p
				return nil
			}),
			Doc: "world-space position",
		},
		"rotation": {
			Get: get(func(v *Volume) flang.Value { return vecValue(v.Rotation) }),
			Set: set(func(vol *Volume, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("rotation", "vec", v)
				}
				vol.Rotation = p
				return nil
			}),
			Doc: "Euler angles in radians",
		},
		"half": {
			Get: get(func(v *Volume) flang.Value {
				return flang.VecVal{float64(v.HalfX), float64(v.HalfY), float64(v.HalfZ)}
			}),
			Set: set(func(vol *Volume, v flang.Value) error {
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
			Get: get(func(v *Volume) flang.Value { return flang.AtomVal(volumeShapeName(v.Shape)) }),
			Set: set(func(vol *Volume, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("shape", "str or atom", v)
				}
				vol.Shape = volumeShapeFromName(s)
				return nil
			}),
			Dirty: true,
			Doc:   "box, sphere or cylinder",
		},
		"enabled": {
			Get: get(func(v *Volume) flang.Value { return flang.BoolVal(v.Enabled) }),
			Set: set(func(vol *Volume, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("enabled", "bool", v)
				}
				vol.Enabled = b
				return nil
			}),
			Doc: "a disabled volume contains nothing, so it fires no events",
		},
	}
}

// scriptMissingError is what a write to a vanished object produces.
type scriptMissingError struct{ what string }

func (e *scriptMissingError) Error() string { return e.what + " no longer exists" }
