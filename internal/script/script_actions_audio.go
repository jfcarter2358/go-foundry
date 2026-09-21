package script

// script_actions_audio.go — the audio verbs.
//
// Six of them, in two groups. `play` and `stop` drive a sound a level already
// placed; `play_sound`/`play_sound_at` create one on the spot for gameplay
// that no author could have placed ahead of time — an impact, a footstep, a
// door. `play_music` and `stop_music` drive the level's global track.
//
// play_sound_at is modelled on spawn_effect and shares its one important
// property: the sound it places is a one-shot that removes itself when it
// finishes. A script firing one per impact would otherwise fill the level with
// spent placements, and they would be written to the level file.

import (
	"fmt"

	"foundry/internal/flang"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// verbSound resolves argument i to a placed sound, with the error a script
// author can act on. play and stop take an effect too; they ask verbEffectArg
// first.
func (rt *ScriptRuntime) verbSound(args []flang.Value, i int, name string) (*world.Sound, error) {
	if i >= len(args) {
		return nil, fmt.Errorf("%s needs a sound", name)
	}
	r, ok := args[i].(flang.RefVal)
	if !ok {
		return nil, fmt.Errorf("%s takes a sound, not a %s", name, flang.TypeOf(args[i]))
	}
	if r.Kind != flang.RefSound {
		return nil, fmt.Errorf("%s takes a sound; %s is a %s", name, r.String(), r.Kind)
	}
	s := rt.cache.soundFor(r)
	if s == nil {
		return nil, &scriptMissingError{r.String()}
	}
	return s, nil
}

// placeOneShot appends a self-removing sound and returns a ref to it.
func (rt *ScriptRuntime) placeOneShot(name string, at mgl32.Vec3, gain float32, verb string) (flang.Value, error) {
	if rt.lvl == nil {
		return flang.NullVal{}, nil
	}
	rt.markDirty()

	s := world.NewSound(world.UniqueSoundID(rt.lvl, name), at)
	s.Clip = name
	s.Gain = gain
	// A one-shot, not ambience: it plays once and then removes itself. See
	// world.SoundUpdate, which reaps it.
	s.Loop = false
	s.OneShot = true

	rt.lvl.Sounds = append(rt.lvl.Sounds, s)
	// The cache is indexed by name and the new sound is not in it yet. An
	// append shifts nothing, so it is the one entry, not a rebuild: a battle
	// places a one-shot for every shot fired.
	rt.cacheAdded(func(c *refCache) { c.sound[s.ID] = len(rt.lvl.Sounds) - 1 })
	return flang.RefVal{Kind: flang.RefSound, A: s.ID}, nil
}

// optionalGain reads a trailing gain argument, defaulting to 1.
func optionalGain(args []flang.Value, i int) float32 {
	if g, ok := verbNum(args, i); ok {
		return float32(g)
	}
	return 1
}

// audioVerbs is merged into scriptVerbs at init. Kept in its own file and its
// own map so the audio surface can be read in one place.
var audioVerbs = map[string]scriptVerb{
	"play": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if fx, ok, err := rt.verbEffectArg(args, 0, "play"); ok {
			if err != nil {
				return nil, err
			}
			rt.markDirty()
			fx.Detonate()
			return flang.NullVal{}, nil
		}
		s, err := rt.verbSound(args, 0, "play")
		if err != nil {
			return nil, err
		}
		rt.markDirty()
		// Enabled as well as Playing, on `emit`'s rule: play on a switched-off
		// sound should start it rather than silently do half the job.
		s.Enabled = true
		s.Playing = true
		return flang.NullVal{}, nil
	},

	"stop": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if fx, ok, err := rt.verbEffectArg(args, 0, "stop"); ok {
			if err != nil {
				return nil, err
			}
			rt.markDirty()
			fx.Playing = false
			return flang.NullVal{}, nil
		}
		s, err := rt.verbSound(args, 0, "stop")
		if err != nil {
			return nil, err
		}
		rt.markDirty()
		// Only Playing: the placement stays, and the mixer ramps the voice
		// down rather than cutting it. Setting Enabled false is the harder
		// switch and is available as a property.
		s.Playing = false
		return flang.NullVal{}, nil
	},

	"play_sound": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		name, err := verbStr(args, 0, "play_sound")
		if err != nil {
			return nil, err
		}
		if len(args) < 2 {
			return nil, fmt.Errorf("play_sound takes a clip name and something to play it on")
		}
		at, ok := rt.positionOf(args[1])
		if !ok {
			return nil, fmt.Errorf("play_sound takes a clip name and an object or a vec")
		}
		return rt.placeOneShot(name, at, optionalGain(args, 2), "play_sound")
	},

	"play_sound_at": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		name, err := verbStr(args, 0, "play_sound_at")
		if err != nil {
			return nil, err
		}
		at, ok := verbVec(args, 1)
		if !ok {
			return nil, fmt.Errorf("play_sound_at takes a clip name and a vec")
		}
		return rt.placeOneShot(name, at, optionalGain(args, 2), "play_sound_at")
	},

	"play_music": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		name, err := verbStr(args, 0, "play_music")
		if err != nil {
			return nil, err
		}
		if rt.lvl == nil {
			return flang.BoolVal(false), nil
		}
		fade := float32(1)
		if f, ok := verbNum(args, 1); ok {
			fade = float32(f)
		}
		rt.markDirty()
		// False when the named track is already playing, so a handler that
		// sets the track every tick does not restart it every tick — which
		// would be an audible stutter for what reads as a no-op.
		return flang.BoolVal(world.SetMusic(rt.lvl, name, fade)), nil
	},

	"stop_music": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if rt.lvl == nil {
			return flang.NullVal{}, nil
		}
		fade := float32(1)
		if f, ok := verbNum(args, 0); ok {
			fade = float32(f)
		}
		rt.markDirty()
		world.StopMusic(rt.lvl, fade)
		rt.lvl.Music = nil
		return flang.NullVal{}, nil
	},
}
