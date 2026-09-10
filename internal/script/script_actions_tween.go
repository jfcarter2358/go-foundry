package script

// script_actions_tween.go — easing a brush, chunk or entity to a placement over
// a set time.
//
// `velocity` says "keep going"; these say "be there in this long". Verbs rather
// than properties for play_waypoints' reason: a blend starts from wherever the
// object is NOW, and a property the next tick re-reads has no such moment. The
// blend itself is the engine's (world/tween.go), stepped on the gameplay tick
// beside every other mover, so a script sends a door and never writes a
// position again.
//
// The spaces are the properties' own, so a script can read one and write the
// other: `move_to` takes what `position` reads (`origin`, for a chunk), and
// `rotate_to` and `scale_to` what `rotation` and `scale` read.

import (
	"fmt"

	"foundry/internal/flang"
	"foundry/internal/world"
)

// tweenVerbs is merged into scriptVerbs at init.
var tweenVerbs = map[string]scriptVerb{
	"move_to":   tweenVerb("move_to", world.TweenMove),
	"rotate_to": tweenVerb("rotate_to", world.TweenRotate),
	"scale_to":  tweenVerb("scale_to", world.TweenScale),
}

// tweenVerb builds the verb that eases one channel. The three differ only in
// the channel and in what each object kind allows of it.
func tweenVerb(name string, channel world.TweenChannel) scriptVerb {
	return func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("%s takes an object, a vec and a number of seconds", name)
		}
		to, ok := toVec3(args[1])
		if !ok {
			return nil, fmt.Errorf("%s takes a vec as its second argument, not a %s", name, flang.TypeOf(args[1]))
		}
		seconds, ok := verbNum(args, 2)
		if !ok {
			return nil, fmt.Errorf("%s takes a number of seconds as its third argument, not a %s", name, flang.TypeOf(args[2]))
		}
		r, ok := args[0].(flang.RefVal)
		if !ok {
			if _, isNull := args[0].(flang.NullVal); isNull {
				return nil, fmt.Errorf("%s was given null — an `@name` that matches nothing in the level resolves to null, so check the object's ID", name)
			}
			return nil, fmt.Errorf("%s takes a brush, a chunk or an entity, not a %s", name, flang.TypeOf(args[0]))
		}
		if rt.lvl == nil {
			return flang.NullVal{}, nil
		}

		switch r.Kind {
		case flang.RefBrush:
			ch, b, err := rt.verbDynamicBrush(args, 0, name)
			if err != nil {
				return nil, err
			}
			// Refused rather than clamped, as writing `scale` is: a zero axis
			// collapses the solid and a negative one turns it inside out.
			if channel == world.TweenScale && (to.X() <= 0 || to.Y() <= 0 || to.Z() <= 0) {
				return nil, fmt.Errorf("%s: every axis must be greater than zero, got <%g, %g, %g>", name, to.X(), to.Y(), to.Z())
			}
			rt.markDirty()
			b.Ease(rt.lvl, ch, channel, to, float32(seconds))

		case flang.RefChunk:
			if channel != world.TweenMove {
				return nil, fmt.Errorf("%s turns or scales a brush or an entity; a chunk's rotation and scale are read-only, so only move_to works on %s", name, r)
			}
			ch := rt.cache.chunkFor(r)
			if ch == nil {
				return nil, &scriptMissingError{r.String()}
			}
			rt.markDirty()
			ch.EaseOrigin(rt.lvl, to, float32(seconds))

		case flang.RefEntity:
			e, err := rt.verbEntity(args, 0, name)
			if err != nil {
				return nil, err
			}
			// Both have a mover of their own that would fight the blend every
			// frame: the player's input, and a vehicle's drive model, whose
			// orientation is a quaternion `rotation` does not feed.
			if e == world.PlayerEntity {
				return nil, fmt.Errorf("%s cannot move the player: its movement belongs to the player's input", name)
			}
			if e.IsVehicle() {
				return nil, fmt.Errorf("%s: %s is a vehicle, and its drive model owns its motion; write its velocity instead", name, r)
			}
			rt.markDirty()
			e.Ease(rt.lvl.Frames, channel, to, float32(seconds))

		default:
			what := "a brush, a chunk or an entity"
			if channel != world.TweenMove {
				what = "a brush or an entity"
			}
			return nil, fmt.Errorf("%s takes %s; %s is a %s", name, what, r, r.Kind)
		}
		return flang.NullVal{}, nil
	}
}
