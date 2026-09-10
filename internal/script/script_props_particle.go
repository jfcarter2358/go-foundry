package script

// script_props_particle.go — the `.prop` surface for sprites and emitters.
//
// Both are deliberately thin. A sprite's properties are the handful a script
// plausibly changes at runtime — swap the texture, hide it, retint it — not
// everything the editor can author; and an emitter's are the placement's, not
// the effect's, because the effect is a shared document and a script writing to
// it would change every other placement of the same one.
//
// Dirty is false throughout: neither type owns sector geometry, so nothing here
// needs a rebuild queued. Tex is set on the two texture writes, so the name is
// loaded during DrainScriptWork rather than mid-tick, where GL must not be
// touched.

import (
	"fmt"

	"foundry/internal/flang"
	"foundry/internal/world"
)

// spriteProps is the property table for flang.RefSprite.
func spriteProps() map[string]propAccess {
	get := func(fn func(s *world.Sprite) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			sp := c.spriteFor(r)
			if sp == nil {
				return nil, false
			}
			return fn(sp), true
		}
	}
	set := func(fn func(sp *world.Sprite, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(c *refCache, r flang.RefVal, v flang.Value) error {
			sp := c.spriteFor(r)
			if sp == nil {
				return &scriptMissingError{r.String()}
			}
			return fn(sp, v)
		}
	}

	return map[string]propAccess{
		"position": {
			Get: get(func(s *world.Sprite) flang.Value { return vecValue(s.Position) }),
			Set: set(func(sp *world.Sprite, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("position", "vec", v)
				}
				sp.Position = p
				return nil
			}),
			Doc: "position, world-space unless the sprite is attached",
		},
		"waypoints": waypointsProp(),
		"visible": {
			Get: get(func(s *world.Sprite) flang.Value { return flang.BoolVal(s.Visible) }),
			Set: set(func(sp *world.Sprite, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("visible", "bool", v)
				}
				sp.Visible = b
				return nil
			}),
			Doc: "whether the sprite is drawn",
		},
		"texture": {
			Get: get(func(s *world.Sprite) flang.Value { return flang.StrVal(s.Texture) }),
			Set: set(func(sp *world.Sprite, v flang.Value) error {
				name, ok := toStr(v)
				if !ok {
					return propErr("texture", "str", v)
				}
				sp.Texture = name
				return nil
			}),
			// The name may not be resident yet, and the tick must not touch GL.
			Tex: true,
			Doc: "texture name",
		},
		"alpha": {
			Get: get(func(s *world.Sprite) flang.Value { return flang.FloatVal(float64(s.Alpha)) }),
			Set: set(func(sp *world.Sprite, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("alpha", "float", v)
				}
				sp.Alpha = f
				return nil
			}),
			Doc: "opacity, 0 to 1",
		},
		"tint": {
			Get: get(func(s *world.Sprite) flang.Value { return vecValue(s.Tint) }),
			Set: set(func(sp *world.Sprite, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("tint", "vec", v)
				}
				sp.Tint = p
				return nil
			}),
			Doc: "colour multiplier as a vec of r, g, b",
		},
		"size": {
			Get: get(func(s *world.Sprite) flang.Value {
				return flang.VecVal{float64(s.Width), float64(s.Height), 0}
			}),
			Set: set(func(sp *world.Sprite, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("size", "vec", v)
				}
				sp.Width, sp.Height = p.X(), p.Y()
				return nil
			}),
			Doc: "width and height as a vec; the z component is unused",
		},
		"scale": {
			Get: get(func(s *world.Sprite) flang.Value { return vecValue(s.EffectiveScale()) }),
			Set: set(func(sp *world.Sprite, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("scale", "vec", v)
				}
				// Refused rather than clamped, as a brush's scale is: a zero
				// axis would hide the quad in a way that reads as a bug.
				if p.X() <= 0 || p.Y() <= 0 {
					return fmt.Errorf("scale: x and y must be greater than zero, got <%g, %g, %g>", p.X(), p.Y(), p.Z())
				}
				sp.Scale = world.ClampBrushScale(p)
				return nil
			}),
			Doc: "multiplies size as drawn — x the width, y the height; <1, 1, 1> is the authored size",
		},
		"attach": {
			Get: get(func(s *world.Sprite) flang.Value { return flang.StrVal(s.Attach.Describe()) }),
			Doc: "what the sprite rides, as the editor names it; empty when it is in world space (read-only)",
		},
		"carrier": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				sp := c.spriteFor(r)
				if sp == nil {
					return nil, false
				}
				return carrierValue(c, sp.Attach), true
			},
			Doc: "a ref to the entity the sprite rides, or null (read-only)",
		},
		"frame": {
			Get: get(func(s *world.Sprite) flang.Value { return flang.IntVal(s.Frame) }),
			Set: set(func(sp *world.Sprite, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("frame", "int", v)
				}
				sp.Frame = int(f)
				return nil
			}),
			Doc: "which cell of the sprite sheet to show",
		},
	}
}

// emitterProps is the property table for flang.RefEmitter.
func emitterProps() map[string]propAccess {
	get := func(fn func(e *world.Emitter) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			e := c.emitterFor(r)
			if e == nil {
				return nil, false
			}
			return fn(e), true
		}
	}
	set := func(fn func(e *world.Emitter, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(c *refCache, r flang.RefVal, v flang.Value) error {
			e := c.emitterFor(r)
			if e == nil {
				return &scriptMissingError{r.String()}
			}
			return fn(e, v)
		}
	}

	return map[string]propAccess{
		"position": {
			Get: get(func(e *world.Emitter) flang.Value { return vecValue(e.Position) }),
			Set: set(func(e *world.Emitter, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("position", "vec", v)
				}
				e.Position = p
				return nil
			}),
			Doc: "position, world-space unless the emitter is attached",
		},
		"rotation": {
			Get: get(func(e *world.Emitter) flang.Value { return vecValue(e.Rotation) }),
			Set: set(func(e *world.Emitter, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("rotation", "vec", v)
				}
				e.Rotation = p
				return nil
			}),
			Doc: "Euler angles in radians; aims the emission shape",
		},
		"waypoints": waypointsProp(),
		"enabled": {
			Get: get(func(e *world.Emitter) flang.Value { return flang.BoolVal(e.Enabled) }),
			Set: set(func(e *world.Emitter, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("enabled", "bool", v)
				}
				e.Enabled = b
				return nil
			}),
			Doc: "off stops the simulation dead; live particles vanish",
		},
		"emitting": {
			Get: get(func(e *world.Emitter) flang.Value { return flang.BoolVal(e.Emitting) }),
			Set: set(func(e *world.Emitter, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("emitting", "bool", v)
				}
				e.Emitting = b
				e.SyncSystems()
				return nil
			}),
			Doc: "off stops new particles but lets the ones in flight finish",
		},
		"rate_scale": {
			Get: get(func(e *world.Emitter) flang.Value { return flang.FloatVal(float64(e.RateScale)) }),
			Set: set(func(e *world.Emitter, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("rate_scale", "float", v)
				}
				e.RateScale = f
				e.SyncSystems()
				return nil
			}),
			Doc: "multiplies every layer's emission rate; 1 is the effect's own",
		},
		"tint": {
			Get: get(func(e *world.Emitter) flang.Value { return vecValue(e.Tint) }),
			Set: set(func(e *world.Emitter, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("tint", "vec", v)
				}
				e.Tint = p
				// Pushed into the running systems, as rate_scale above is:
				// the tint is folded into each layer on the way to it, so
				// without this a write would only reach the particles emitted
				// after the next reload.
				e.SyncSystems()
				return nil
			}),
			Doc: "colour multiplier over every layer's own tint",
		},
		"effect": {
			Get: get(func(e *world.Emitter) flang.Value { return flang.StrVal(e.Effect) }),
			// Read-only: swapping the document means reloading it from disk,
			// which is file I/O in the middle of a tick. A script that wants two
			// effects places two emitters and switches `enabled`.
			Doc: "the .fpar document this emitter plays (read-only)",
		},
		"live": {
			Get: get(func(e *world.Emitter) flang.Value { return flang.IntVal(e.LiveCount()) }),
			Doc: "how many particles are currently in flight (read-only)",
		},
		"attach": {
			Get: get(func(e *world.Emitter) flang.Value { return flang.StrVal(e.Attach.Describe()) }),
			Doc: "what the emitter rides, as the editor names it; empty when it is in world space (read-only)",
		},
		"carrier": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				e := c.emitterFor(r)
				if e == nil {
					return nil, false
				}
				return carrierValue(c, e.Attach), true
			},
			Doc: "a ref to the entity the emitter rides, or null (read-only)",
		},
	}
}

// ---------- the attach binding, read-only ----------

// carrierValue is the `carrier` property for both kinds: a ref to the entity a
// sprite or emitter rides, or null.
//
// Null rather than a dangling ref when the entity has gone, because a carrier
// that was destroyed reads as "riding nothing" everywhere else too — Attach
// resolves a missing name to the identity — and `if @flame.prop.carrier` is the
// line a script wants to be able to write. Both write forms live in the verbs
// (script_actions_attach.go): binding takes up to three names and a diagnosis
// for each of them, which is more than a property assignment can say.
func carrierValue(c *refCache, a world.Attach) flang.Value {
	if a.Entity == "" {
		return flang.NullVal{}
	}
	if _, ok := c.entity[a.Entity]; !ok {
		return flang.NullVal{}
	}
	return flang.RefVal{Kind: flang.RefEntity, A: a.Entity}
}
