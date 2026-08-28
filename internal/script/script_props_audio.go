package script

// script_props_audio.go — the `.prop` surface for placed sounds.
//
// Thin, on script_props_particle.go's rule: these are the *placement's*
// properties, not the clip's. `clip` is read-only for the reason `effect` is
// read-only on an emitter — swapping it means reading a file from disk, and a
// tick must not do file I/O.
//
// A write that changes how the sound carries takes effect on the next frame
// rather than immediately, because world.SoundUpdate republishes every live
// voice's parameters once a frame. That is one frame of latency on a gain
// change, which is inaudible, and it is what keeps the script tick from having
// to know anything about the mixer.

import (
	"foundry/internal/audio"
	"foundry/internal/flang"
	"foundry/internal/world"
)

// soundProps is the property table for flang.RefSound.
func soundProps() map[string]propAccess {
	get := func(fn func(s *world.Sound) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			s := c.soundFor(r)
			if s == nil {
				return nil, false
			}
			return fn(s), true
		}
	}
	set := func(fn func(s *world.Sound, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(c *refCache, r flang.RefVal, v flang.Value) error {
			s := c.soundFor(r)
			if s == nil {
				return &scriptMissingError{r.String()}
			}
			return fn(s, v)
		}
	}

	// numeric builds the common case: a float property with a clamp.
	numeric := func(name string, rd func(*world.Sound) float32, wr func(*world.Sound, float32), min, max float32, doc string) propAccess {
		return propAccess{
			Get: get(func(s *world.Sound) flang.Value { return flang.FloatVal(rd(s)) }),
			Set: set(func(s *world.Sound, v flang.Value) error {
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
				wr(s, f)
				return nil
			}),
			Doc: doc,
		}
	}

	boolean := func(name string, rd func(*world.Sound) bool, wr func(*world.Sound, bool), doc string) propAccess {
		return propAccess{
			Get: get(func(s *world.Sound) flang.Value { return flang.BoolVal(rd(s)) }),
			Set: set(func(s *world.Sound, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr(name, "bool", v)
				}
				wr(s, b)
				return nil
			}),
			Doc: doc,
		}
	}

	return map[string]propAccess{
		"position": {
			Get: get(func(s *world.Sound) flang.Value { return vecValue(s.Position) }),
			Set: set(func(s *world.Sound, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("position", "vec", v)
				}
				s.Position = p
				return nil
			}),
			Doc: "position, world-space unless the sound is attached",
		},
		"enabled": boolean("enabled",
			func(s *world.Sound) bool { return s.Enabled },
			func(s *world.Sound, b bool) { s.Enabled = b },
			"master switch; a disabled sound holds no voice at all"),
		"playing": boolean("playing",
			func(s *world.Sound) bool { return s.Playing },
			func(s *world.Sound, b bool) { s.Playing = b },
			"whether it is sounding now; the softer switch, which keeps the placement"),
		"loop": boolean("loop",
			func(s *world.Sound) bool { return s.Loop },
			func(s *world.Sound, b bool) { s.Loop = b },
			"true for ambience, false for a one-shot"),

		// Gain, not volume: `volume` is a level object type in this engine.
		"gain": numeric("gain",
			func(s *world.Sound) float32 { return s.Gain },
			func(s *world.Sound, f float32) { s.Gain = f },
			0, 4, "loudness multiplier; 1 is the clip as authored"),
		"pitch": numeric("pitch",
			func(s *world.Sound) float32 { return s.Pitch },
			func(s *world.Sound, f float32) { s.Pitch = f },
			// The mixer clamps the playback rate to this range anyway; doing
			// it here means a script reads back what it wrote.
			0.5, 2, "playback rate multiplier; 1 is the clip's own"),
		"ref_distance": numeric("ref_distance",
			func(s *world.Sound) float32 { return s.RefDistance },
			func(s *world.Sound, f float32) { s.RefDistance = f },
			0.01, 10000, "radius within which the sound plays at full gain"),
		"max_distance": numeric("max_distance",
			func(s *world.Sound) float32 { return s.MaxDistance },
			func(s *world.Sound, f float32) { s.MaxDistance = f },
			0.01, 100000, "radius past which the sound is silent"),
		"rolloff": numeric("rolloff",
			func(s *world.Sound) float32 { return s.Rolloff },
			func(s *world.Sound, f float32) { s.Rolloff = f },
			0, 100, "how sharply the gain falls between the two distances"),

		"bus": {
			Get: get(func(s *world.Sound) flang.Value { return flang.StrVal(s.AudioBus()) }),
			Set: set(func(s *world.Sound, v flang.Value) error {
				name, ok := toStr(v)
				if !ok {
					return propErr("bus", "string", v)
				}
				switch audio.Bus(name) {
				case audio.BusEffects, audio.BusMusic:
					s.Bus = name
					return nil
				}
				return propErr("bus", `"effects" or "music"`, v)
			}),
			Doc: `which slider it answers to: "effects" or "music"`,
		},

		// Read-only, for the reason an emitter's `effect` is: changing it
		// means reading a file from disk, which a tick must not do.
		"clip": {
			Get: get(func(s *world.Sound) flang.Value { return flang.StrVal(s.Clip) }),
			Doc: "the clip this placement plays (read-only)",
		},
		"sounding": {
			Get: get(func(s *world.Sound) flang.Value { return flang.BoolVal(!s.Voice.IsZero()) }),
			Doc: "whether the mixer is actually playing it right now (read-only)",
		},
	}
}
