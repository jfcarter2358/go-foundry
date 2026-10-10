package script

// script_props_decal.go — the `.prop` surface for projected decals.
//
// Thin for the sprites' reason: these are the properties a script plausibly
// changes while the game runs, not everything the editor can author.
//
// The split that matters here is between the properties that move a VERTEX and
// the ones that do not. A decal's mesh is clipped out of the level's geometry, so
// writing `position` or `size` throws that mesh away and re-clips on the next
// frame; writing `alpha`, `tint`, `texture` or `visible` does not, and costs
// nothing at all. That is why the transform properties are here at all but are
// not what a fade or a flash should be written through — see
// world/decal_sync.go, which decides between the two by fingerprint, so neither
// this table nor its callers have to remember which is which.

import (
	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/world"
)

// decalProps is the property table for flang.RefDecal.
func decalProps() map[string]propAccess {
	get := func(fn func(d *world.Decal) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			d := c.decalFor(r)
			if d == nil {
				return nil, false
			}
			return fn(d), true
		}
	}
	set := func(fn func(d *world.Decal, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(c *refCache, r flang.RefVal, v flang.Value) error {
			d := c.decalFor(r)
			if d == nil {
				return &scriptMissingError{r.String()}
			}
			return fn(d, v)
		}
	}

	return map[string]propAccess{
		"position": {
			Get: get(func(d *world.Decal) flang.Value { return vecValue(d.Position) }),
			Set: set(func(d *world.Decal, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("position", "vec", v)
				}
				d.Position = p
				return nil
			}),
			Doc: "centre of the projection box, world-space; moving it re-projects the decal",
		},
		"rotation": {
			Get: get(func(d *world.Decal) flang.Value { return vecValue(d.Rotation) }),
			Set: set(func(d *world.Decal, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("rotation", "vec", v)
				}
				d.Rotation = p
				return nil
			}),
			Doc: "Euler angles in radians; the decal's local +Z is the way it faces out of the surface",
		},
		"visible": {
			Get: get(func(d *world.Decal) flang.Value { return flang.BoolVal(d.Visible) }),
			Set: set(func(d *world.Decal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("visible", "bool", v)
				}
				d.Visible = b
				return nil
			}),
			Doc: "whether the decal is drawn; hiding one keeps its mesh, so showing it again is free",
		},
		"texture": {
			Get: get(func(d *world.Decal) flang.Value { return flang.StrVal(d.Texture) }),
			Set: set(func(d *world.Decal, v flang.Value) error {
				name, ok := toStr(v)
				if !ok {
					return propErr("texture", "str", v)
				}
				d.Texture = name
				return nil
			}),
			// The name may not be resident yet, and the tick must not touch GL.
			Tex: true,
			Doc: "texture name; swapping it does not re-project the mesh",
		},
		"material": {
			Get: get(func(d *world.Decal) flang.Value { return flang.StrVal(d.Material) }),
			Set: set(func(d *world.Decal, v flang.Value) error {
				name, ok := toStr(v)
				if !ok {
					return propErr("material", "str", v)
				}
				d.Material = name
				return nil
			}),
			Tex: true,
			Doc: "surface material (.fmat) name, or \"\" for none",
		},
		"alpha": {
			Get: get(func(d *world.Decal) flang.Value { return flang.FloatVal(float64(d.Alpha)) }),
			Set: set(func(d *world.Decal, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("alpha", "float", v)
				}
				d.Alpha = f
				return nil
			}),
			Doc: "opacity, 0 to 1; what a fade should be written through",
		},
		"tint": {
			Get: get(func(d *world.Decal) flang.Value { return vecValue(d.Tint) }),
			Set: set(func(d *world.Decal, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("tint", "vec", v)
				}
				d.Tint = p
				return nil
			}),
			Doc: "colour multiplier as a vec of r, g, b",
		},
		"size": {
			Get: get(func(d *world.Decal) flang.Value {
				return flang.VecVal{float64(d.Width), float64(d.Height), float64(d.Depth)}
			}),
			Set: set(func(d *world.Decal, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("size", "vec", v)
				}
				d.Width, d.Height = p.X(), p.Y()
				// A zero z leaves the depth alone rather than collapsing the box
				// to nothing, so `size = vec(1, 1, 0)` means what a script
				// resizing a poster obviously intends.
				if p.Z() > 0 {
					d.Depth = p.Z()
				}
				return nil
			}),
			Doc: "width, height and projection depth as a vec; a zero z leaves the depth as it was",
		},
		"order": {
			Get: get(func(d *world.Decal) flang.Value { return flang.FloatVal(float64(d.Order)) }),
			Set: set(func(d *world.Decal, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("order", "number", v)
				}
				d.Order = int(f)
				return nil
			}),
			Doc: "draw order among overlapping decals; higher lies on top",
		},
		"lifetime": {
			Get: get(func(d *world.Decal) flang.Value { return flang.FloatVal(float64(d.Lifetime)) }),
			Set: set(func(d *world.Decal, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("lifetime", "number", v)
				}
				if f < 0 {
					f = 0
				}
				d.Lifetime = f
				return nil
			}),
			Doc: "seconds left before the decal fades out and is removed; 0 means it never does",
		},
		"unlit": {
			Get: get(func(d *world.Decal) flang.Value { return flang.BoolVal(d.Unlit) }),
			Set: set(func(d *world.Decal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("unlit", "bool", v)
				}
				d.Unlit = b
				return nil
			}),
			Doc: "ignore the scene's lights and draw the texture's own colour",
		},
		"blend": {
			Get: get(func(d *world.Decal) flang.Value {
				return flang.StrVal(gfx.DecalBlendName(d.Blend))
			}),
			Set: set(func(d *world.Decal, v flang.Value) error {
				name, ok := toStr(v)
				if !ok {
					return propErr("blend", "str", v)
				}
				d.Blend = gfx.DecalBlendFromName(name)
				return nil
			}),
			Doc: `how it combines with the surface: "alpha", "multiply" or "additive"`,
		},
		"faces": {
			Get: get(func(d *world.Decal) flang.Value { return vecValue(d.Facing()) }),
			Doc: "the direction the decal faces out of its surface, as a unit vec (read-only)",
		},
	}
}
