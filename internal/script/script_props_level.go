package script

// script_props_level.go — the `.prop` surface on `@level`.
//
// The two load properties are there because a loading screen needs them: a bar
// bound to `@level.prop.load_progress` is the whole reason the load reports
// itself at all. Both are read-only — a script that could write the load
// progress would be lying to the bar rather than telling it anything.
//
// `step_height` is the level's own gameplay tuning rather than a report, and is
// the one thing here a script may write. It lives on `@level` and not on
// `@player` for the same reason it lives on world.Level and not on world.Entity:
// there is one player, the setting is saved in the level file, and putting it in
// the entity table would advertise it on every barrel and door in the level as
// if each carried its own.
//
// The `sky_*` properties are the same idea applied to world.Sky: a level-scoped
// singleton, edited today from the Environment group of the properties panel
// (editor_environment_panel.go), and now writable at runtime for the same reason
// fog volumes are — a script fading from day to dusk, or swapping a cubemap when
// the player steps through a portal, is authoring the same field the editor's
// checkbox and combo do, just on a schedule the level file cannot express. None
// of it is saved: like step_height, a script's writes act on the level in memory
// and the editor's snapshot restores what the level file said once play ends.

import (
	"strings"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/hooks"
	"foundry/internal/world"
)

func levelProps() map[string]propAccess {
	return map[string]propAccess{
		"load_progress": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(float64(hooks.LoadProgress())), true
			},
			Doc: "how far a level load has got, from 0 to 1. 1 when nothing is loading",
		},
		"load_stage": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.StrVal(hooks.LoadStage()), true
			},
			Doc: "a one-line description of what the load is doing, or empty when nothing is",
		},

		// Writable, and it takes effect on the very next frame: MovePlayer asks
		// the level for this once per frame rather than caching it, so a script
		// lowering it for a crawlspace or raising it for a climb needs nothing
		// more than the assignment.
		"step_height": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil || c.lvl == nil {
					return nil, false
				}
				// The number in force, not the raw field: a script reading this
				// on a level that never set one wants the height it is actually
				// walking with, and 0 would be an answer that is never true.
				return flang.FloatVal(float64(
					c.lvl.Player.EffectiveStepHeight(world.PlayerEntity))), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("step_height", "num", v)
				}
				if c == nil || c.lvl == nil {
					return nil
				}
				// Anything not positive means the default, the same as an absent
				// `player:` block — see world.PlayerSettings. So `= 0` is how a
				// script hands the setting back rather than a way to freeze the
				// player against every floor seam in the level.
				if f < 0 {
					f = 0
				}
				c.lvl.Player.StepHeight = f
				return nil
			},
			Doc: "how tall a riser the player walks up rather than being stopped by, in world units; 0 restores the default (the player's own height)",
		},

		"sky": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil || c.lvl == nil {
					return nil, false
				}
				return flang.BoolVal(c.lvl.Sky != nil), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("sky", "bool", v)
				}
				if c == nil || c.lvl == nil {
					return nil
				}
				if b {
					if c.lvl.Sky == nil {
						sk := world.DefaultSky()
						c.lvl.Sky = &sk
					}
				} else {
					// Same gesture as the editor's own checkbox: gone, not a
					// switched-off block the next save would carry around. A
					// sky has no field of its own for "off" the way VolumeFog
					// has Enabled — existing IS the on/off switch.
					c.lvl.Sky = nil
				}
				return nil
			},
			Doc: "whether the level has a sky at all; off clears it, on restores the defaults if it had none",
		},
		"sky_kind": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil {
					return nil, false
				}
				return flang.AtomVal(world.SkyKindName(skyOf(c.lvl).Kind)), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("sky_kind", "str or atom", v)
				}
				if c == nil || c.lvl == nil {
					return nil
				}
				skySet(c.lvl, func(sk *world.Sky) { sk.Kind = world.SkyKindFromName(s) })
				return nil
			},
			Doc: "gradient or cubemap; a cubemap with any face unset falls back to the gradient rather than five faces and a hole",
		},
		"sky_cubemap": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil {
					return nil, false
				}
				return flang.StrVal(skyCubemapBase(skyOf(c.lvl).Faces)), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("sky_cubemap", "str", v)
				}
				if c == nil || c.lvl == nil {
					return nil
				}
				// The same shortcut the editor's "Fill from base name" button
				// is: "dusk" fills all six slots as dusk_px, dusk_nx, ... and
				// switches the kind to cubemap. Writing "" clears them without
				// switching the kind back, matching SetFacesFromBase itself.
				skySet(c.lvl, func(sk *world.Sky) {
					sk.SetFacesFromBase(s)
					if s != "" {
						sk.Kind = world.SkyCubemap
					}
				})
				return nil
			},
			Doc: "sets all six cubemap faces from one base name (\"dusk\" -> dusk_px, dusk_nx, ...) and switches kind to cubemap; \"\" clears them",
		},
		"sky_zenith": skyColorProp("sky_zenith", func(sk *world.Sky) *mgl32.Vec3 { return &sk.Zenith },
			"the gradient's straight-up stop"),
		"sky_horizon": skyColorProp("sky_horizon", func(sk *world.Sky) *mgl32.Vec3 { return &sk.Horizon },
			"the gradient's stop at the ring around the viewer"),
		"sky_ground": skyColorProp("sky_ground", func(sk *world.Sky) *mgl32.Vec3 { return &sk.Ground },
			"the gradient's straight-down stop, seen where the level has no floor under the camera"),
		"sky_tint": skyColorProp("sky_tint", func(sk *world.Sky) *mgl32.Vec3 { return &sk.Tint },
			"multiplies whichever mode produced the colour, gradient or cubemap alike"),
		"sky_intensity": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil {
					return nil, false
				}
				return flang.FloatVal(float64(skyOf(c.lvl).Intensity)), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("sky_intensity", "number", v)
				}
				if c == nil || c.lvl == nil {
					return nil
				}
				skySet(c.lvl, func(sk *world.Sky) { sk.Intensity = f })
				return nil
			},
			Doc: "brightness, on top of sky_tint",
		},
		"sky_rotation": {
			Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
				if c == nil {
					return nil, false
				}
				return flang.FloatVal(float64(skyOf(c.lvl).Rotation)), true
			},
			Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("sky_rotation", "number", v)
				}
				if c == nil || c.lvl == nil {
					return nil
				}
				skySet(c.lvl, func(sk *world.Sky) { sk.Rotation = f })
				return nil
			},
			Doc: "degrees about +Y; lines a cubemap's art up with the level's own sun without repainting it",
		},
	}
}

// skyColorProp builds one of the four vec3 sky properties above. field picks
// out which one of a *world.Sky it reads and writes; the property table entry
// otherwise looks the same for all four, so it is built once here rather than
// four times with the one line that differs buried in the middle of each.
func skyColorProp(name string, field func(*world.Sky) *mgl32.Vec3, doc string) propAccess {
	return propAccess{
		Get: func(c *refCache, _ flang.RefVal) (flang.Value, bool) {
			if c == nil {
				return nil, false
			}
			sk := skyOf(c.lvl)
			return vecValue(*field(&sk)), true
		},
		Set: func(c *refCache, _ flang.RefVal, v flang.Value) error {
			p, ok := toVec3(v)
			if !ok {
				return propErr(name, "vec", v)
			}
			if c == nil || c.lvl == nil {
				return nil
			}
			skySet(c.lvl, func(sk *world.Sky) { *field(sk) = p })
			return nil
		},
		Doc: doc,
	}
}

// ---------- level sky property helpers ----------
//
// A level's sky is a pointer, and a level authored before skies existed — or
// one that simply does not want one — has none. Reading one of those is a
// fair question with a dull answer, so it returns the defaults rather than
// erroring, the same shape volumeFogOf gives a plain trigger's fog.

// skyOf is the level's sky, or a default one to read fields off. The returned
// value is a COPY, so nothing written through it reaches the level by
// accident.
func skyOf(lvl *world.Level) world.Sky {
	if lvl == nil || lvl.Sky == nil {
		return world.DefaultSky()
	}
	return *lvl.Sky
}

// skySet applies fn to the level's sky, creating one at the defaults first if
// there was none — see volumeFogSet's note on why a write should not have to
// be preceded by switching the feature on: `@level.prop.sky_tint = ...` on a
// level with no sky yet has exactly one sensible reading, the same as
// `@cellar.prop.fog_density = ...` on a volume with no fog block.
func skySet(lvl *world.Level, fn func(*world.Sky)) {
	if lvl == nil {
		return
	}
	if lvl.Sky == nil {
		sk := world.DefaultSky()
		lvl.Sky = &sk
	}
	fn(lvl.Sky)
	lvl.Sky.Clamp()
}

// skyCubemapBase reconstructs the base name SetFacesFromBase would have taken
// to produce these six faces, or "" when they do not agree with one — hand
// picked one face at a time, say, or never set at all. Best-effort rather than
// stored, because the base name is a convenience SetFacesFromBase offers and
// not something a Sky actually keeps; what it keeps is six names.
func skyCubemapBase(faces [world.SkyFaces]string) string {
	suffix := "_" + world.SkyFaceSuffix[0]
	if faces[0] == "" || !strings.HasSuffix(faces[0], suffix) {
		return ""
	}
	base := strings.TrimSuffix(faces[0], suffix)
	var probe world.Sky
	probe.SetFacesFromBase(base)
	if probe.Faces == faces {
		return base
	}
	return ""
}
