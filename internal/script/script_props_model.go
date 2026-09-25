package script

// script_props_model.go — the properties of one attachment of an entity.
//
// An entity is a list of model attachments, not a single mesh, so its own
// `texture` property can only mean "all of them". Addressing one is what
// `@guard.model.body` is for, and this is what that ref answers.
//
// The kind exists because a reskin used to be a verb — `set_texture(e, model,
// name)` — purely because there was no way to *name* the attachment. Naming it
// is the better half of that verb: everything else an attachment has (its
// opacity, whether it draws, where it sits on the entity) becomes readable and
// writable at the same time, and none of it needed a verb of its own.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/world"
)

// modelFor resolves a model ref to the attachment it names. A ref with no
// attachment name is the pending half of `@entity.model.<name>` and resolves to
// nothing, the same way a bare edge ref does.
func (c *refCache) modelFor(r flang.RefVal) *world.EntityModel {
	if r.Kind != flang.RefModel || r.B == "" {
		return nil
	}
	e := c.entityFor(flang.RefVal{Kind: flang.RefEntity, A: r.A})
	if e == nil {
		return nil
	}
	return e.Model(r.B)
}

func modelProps() map[string]propAccess {
	get := func(fn func(m *world.EntityModel) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			m := c.modelFor(r)
			if m == nil {
				return nil, false
			}
			return fn(m), true
		}
	}
	set := func(fn func(m *world.EntityModel, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(c *refCache, r flang.RefVal, v flang.Value) error {
			m := c.modelFor(r)
			if m == nil {
				return &scriptMissingError{r.String()}
			}
			return fn(m, v)
		}
	}
	vecSet := func(name string, apply func(m *world.EntityModel, v mgl32.Vec3)) func(*world.EntityModel, flang.Value) error {
		return func(m *world.EntityModel, v flang.Value) error {
			p, ok := toVec3(v)
			if !ok {
				return propErr(name, "vec", v)
			}
			apply(m, p)
			return nil
		}
	}

	return map[string]propAccess{
		"texture": {
			// The override, not the mesh's own name — reading back what was
			// written is what makes this a property rather than a command.
			// Empty means the attachment is still wearing whatever it was
			// authored with, which is what the mesh reports.
			Get: get(func(m *world.EntityModel) flang.Value { return flang.StrVal(modelTextureName(m)) }),
			Set: set(func(m *world.EntityModel, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("texture", "str", v)
				}
				m.SetTexture(s)
				return nil
			}),
			// No rebuild: the mesh is unchanged by a reskin, and every draw
			// resolves the name afresh. Tex, because the name may be one
			// nothing in the level has loaded yet.
			Tex: true,
			Doc: "this attachment's texture, resolved against textures/",
		},
		"material": {
			// The .fmat this attachment wears, overlaid on its own settings —
			// so a script can make one panel glow by naming a material rather
			// than by setting unlit, alpha and a scroll rate one at a time.
			Get: get(func(m *world.EntityModel) flang.Value { return flang.StrVal(m.Material) }),
			Set: set(func(m *world.EntityModel, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("material", "str", v)
				}
				m.Material = s
				for i := range m.Submeshes {
					m.Submeshes[i].MaterialName = s
				}
				return nil
			}),
			// No rebuild: the mesh is unchanged, and the material is resolved
			// afresh on every draw. Tex, because it may name art nothing has
			// loaded yet.
			Tex: true,
			Doc: "surface material (.fmat) name for this attachment, or \"\" for none",
		},
		"visible": {
			Get: get(func(m *world.EntityModel) flang.Value { return flang.BoolVal(m.Visible) }),
			Set: set(func(m *world.EntityModel, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("visible", "bool", v)
				}
				m.Visible = b
				return nil
			}),
			Doc: "whether this attachment draws",
		},
		"alpha": {
			// EffectiveAlpha on the way out, because an attachment that never
			// mentioned alpha is opaque and reporting 0 would say the opposite.
			// A script writing 0 means it: see script_props.go's header.
			Get: get(func(m *world.EntityModel) flang.Value { return flang.FloatVal(m.EffectiveAlpha()) }),
			Set: set(func(m *world.EntityModel, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("alpha", "float", v)
				}
				m.Alpha = f
				return nil
			}),
			Doc: "0.0 fully transparent, 1.0 opaque",
		},
		"offset": {
			Get: get(func(m *world.EntityModel) flang.Value { return vecValue(m.Offset) }),
			Set: set(vecSet("offset", func(m *world.EntityModel, p mgl32.Vec3) { m.Offset = p })),
			Doc: "position relative to the entity, before the entity's own rotation and scale",
		},
		"rotation": {
			Get: get(func(m *world.EntityModel) flang.Value { return vecValue(m.Rotation) }),
			Set: set(vecSet("rotation", func(m *world.EntityModel, p mgl32.Vec3) { m.Rotation = p })),
			Doc: "Euler angles in radians, relative to the entity",
		},
		"scale": {
			Get: get(func(m *world.EntityModel) flang.Value { return vecValue(m.EffectiveScale()) }),
			Set: set(vecSet("scale", func(m *world.EntityModel, p mgl32.Vec3) {
				m.Scale = mgl32.Vec3{
					world.EntityScaleClamp(p.X()), world.EntityScaleClamp(p.Y()), world.EntityScaleClamp(p.Z()),
				}
			})),
			Doc: "per-axis scale relative to the entity; clamped away from zero",
		},
		"uv_scroll": {
			Get: get(func(m *world.EntityModel) flang.Value { return vecValue(mgl32.Vec3{m.UVScroll.X(), m.UVScroll.Y(), 0}) }),
			Set: set(vecSet("uv_scroll", func(m *world.EntityModel, p mgl32.Vec3) { m.UVScroll = mgl32.Vec2{p.X(), p.Y()} })),
			Doc: "how fast the texture slides across the attachment, in UV units a second (x, y; z unused): a tank's tracks. <0, 0, 0> holds it still",
		},
		"uv_offset": {
			Get: get(func(m *world.EntityModel) flang.Value { return vecValue(mgl32.Vec3{m.UVOffset.X(), m.UVOffset.Y(), 0}) }),
			Set: set(vecSet("uv_offset", func(m *world.EntityModel, p mgl32.Vec3) {
				m.UVOffset = mgl32.Vec2{p.X() - float32(math.Floor(float64(p.X()))), p.Y() - float32(math.Floor(float64(p.Y())))}
			})),
			Doc: "how far the texture has slid across the attachment (x, y; z unused), kept within one tile",
		},
		"path": {
			Get: get(func(m *world.EntityModel) flang.Value { return flang.StrVal(m.Path) }),
			Doc: "read-only: the model file this attachment draws",
		},
	}
}

// modelTextureName is what this attachment currently draws with: its override
// if it has one, otherwise the name the loaded mesh came wearing. Empty before
// the mesh has loaded, which is the same answer the entity property gives.
func modelTextureName(m *world.EntityModel) string {
	if m.Texture != "" {
		return m.Texture
	}
	if len(m.Submeshes) > 0 {
		return m.Submeshes[0].Texture
	}
	return ""
}
