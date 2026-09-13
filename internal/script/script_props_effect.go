package script

// script_props_effect.go — the `.prop` surface for placed effects.
//
// The placement's switches and the explosion's parameters. A write takes
// effect on the frame it lands: the renderer reads an explosion's parameters
// straight off the level every frame, so there is nothing to sync.

import (
	"foundry/internal/flang"
	"foundry/internal/world"
)

// effectProps is the property table for flang.RefEffect.
func effectProps() map[string]propAccess {
	get := func(fn func(e *world.Effect) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			e := c.effectFor(r)
			if e == nil {
				return nil, false
			}
			return fn(e), true
		}
	}
	set := func(fn func(e *world.Effect, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(c *refCache, r flang.RefVal, v flang.Value) error {
			e := c.effectFor(r)
			if e == nil {
				return &scriptMissingError{r.String()}
			}
			return fn(e, v)
		}
	}

	numeric := func(name string, rd func(*world.Effect) float32, wr func(*world.Effect, float32), min, max float32, doc string) propAccess {
		return propAccess{
			Get: get(func(e *world.Effect) flang.Value { return flang.FloatVal(rd(e)) }),
			Set: set(func(e *world.Effect, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr(name, "number", v)
				}
				if f < min {
					f = min
				}
				if f > max {
					f = max
				}
				wr(e, f)
				return nil
			}),
			Doc: doc,
		}
	}

	boolean := func(name string, rd func(*world.Effect) bool, wr func(*world.Effect, bool), doc string) propAccess {
		return propAccess{
			Get: get(func(e *world.Effect) flang.Value { return flang.BoolVal(rd(e)) }),
			Set: set(func(e *world.Effect, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr(name, "bool", v)
				}
				wr(e, b)
				return nil
			}),
			Doc: doc,
		}
	}

	return map[string]propAccess{
		"position": {
			Get: get(func(e *world.Effect) flang.Value { return vecValue(e.Position) }),
			Set: set(func(e *world.Effect, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("position", "vec", v)
				}
				e.Position = p
				return nil
			}),
			Doc: "position, world-space unless the effect is attached",
		},
		"enabled": boolean("enabled",
			func(e *world.Effect) bool { return e.Enabled },
			func(e *world.Effect, b bool) { e.Enabled = b },
			"master switch; a disabled effect draws and lights nothing"),
		"playing": boolean("playing",
			func(e *world.Effect) bool { return e.Playing },
			func(e *world.Effect, b bool) { e.Playing = b },
			"whether its run is going; play(@id) restarts it from the beginning"),
		"loop": boolean("loop",
			func(e *world.Effect) bool { return e.Loop },
			func(e *world.Effect, b bool) { e.Loop = b },
			"go off again loop_delay seconds after each run ends"),
		"loop_delay": numeric("loop_delay",
			func(e *world.Effect) float32 { return e.LoopDelay },
			func(e *world.Effect, f float32) { e.LoopDelay = f },
			0, 3600, "seconds between one run ending and the next going off"),

		"radius": numeric("radius",
			func(e *world.Effect) float32 { return e.Explosion.Radius },
			func(e *world.Effect, f float32) { e.Explosion.Radius = f },
			0.01, 10000, "how far the fireball reaches once grown"),
		"duration": numeric("duration",
			func(e *world.Effect) float32 { return e.Explosion.Duration },
			func(e *world.Effect, f float32) { e.Explosion.Duration = f },
			0.05, 3600, "seconds from detonation to the last of the smoke"),
		"brightness": numeric("brightness",
			func(e *world.Effect) float32 { return e.Explosion.Brightness },
			func(e *world.Effect, f float32) { e.Explosion.Brightness = f },
			0, 100, "how strongly the fire glows"),
		"density": numeric("density",
			func(e *world.Effect) float32 { return e.Explosion.Density },
			func(e *world.Effect, f float32) { e.Explosion.Density = f },
			0, 100, "how thick the fire and smoke are"),
		"turbulence": numeric("turbulence",
			func(e *world.Effect) float32 { return e.Explosion.Turbulence },
			func(e *world.Effect, f float32) { e.Explosion.Turbulence = f },
			0, 1.5, "how far the billows push the surface in and out"),
		"rise": numeric("rise",
			func(e *world.Effect) float32 { return e.Explosion.Rise },
			func(e *world.Effect, f float32) { e.Explosion.Rise = f },
			-10000, 10000, "how far the fireball climbs over its life"),
		"light_brightness": numeric("light_brightness",
			func(e *world.Effect) float32 { return e.Explosion.LightBrightness },
			func(e *world.Effect, f float32) { e.Explosion.LightBrightness = f },
			0, 1000, "the flash's brightness at its peak; 0 lights nothing"),
		"light_radius": numeric("light_radius",
			func(e *world.Effect) float32 { return e.Explosion.LightRadius },
			func(e *world.Effect, f float32) { e.Explosion.LightRadius = f },
			0, 100000, "how far the flash reaches"),

		"kind": {
			Get: get(func(e *world.Effect) flang.Value { return flang.StrVal(e.Kind) }),
			Doc: `what the effect is, e.g. "explosion" (read-only)`,
		},
		"progress": {
			Get: get(func(e *world.Effect) flang.Value { return flang.FloatVal(e.Progress()) }),
			Doc: "how far through its run it is: 0 at detonation, 1 when it ends (read-only)",
		},
		"live": {
			Get: get(func(e *world.Effect) flang.Value { return flang.BoolVal(e.Live()) }),
			Doc: "whether it is drawing and lighting right now (read-only)",
		},
	}
}
