package script

// script_actions_effect.go — the effect verbs.
//
//	play @boom                 set a placed effect off from the start
//	stop @boom                 stop it where it is; it vanishes
//	explode <pos>              a detached one-shot explosion at a point
//	explode @crate             ... at an object's position
//	explode <pos> 6            ... with a radius of its own
//
// play and stop are the sound verbs, taught one more kind (see
// script_actions_audio.go): to a script an explosion is set off the way a
// sound is started, and a second pair of names for the same idea would be one
// more thing to remember.
//
// explode is spawn_effect's twin for the explosion kind and shares its one
// important property: the effect it places removes itself when the smoke has
// cleared, so a script firing one per rocket does not fill the level with
// spent ones. Nothing here touches GL — an explosion is uniforms and a light,
// both uploaded by the frame loop.

import (
	"fmt"

	"foundry/internal/flang"
	"foundry/internal/world"
)

// verbEffectArg resolves argument i to an effect when it is one. isEffect is
// false for anything else, so a verb shared with another kind can fall through
// to that kind's resolver and its errors.
func (rt *ScriptRuntime) verbEffectArg(args []flang.Value, i int, name string) (fx *world.Effect, isEffect bool, err error) {
	if i >= len(args) {
		return nil, false, nil
	}
	r, ok := args[i].(flang.RefVal)
	if !ok || r.Kind != flang.RefEffect {
		return nil, false, nil
	}
	fx = rt.cache.effectFor(r)
	if fx == nil {
		return nil, true, &scriptMissingError{r.String()}
	}
	return fx, true, nil
}

// effectVerbs is merged into scriptVerbs at init.
var effectVerbs = map[string]scriptVerb{
	"explode": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("explode takes a vec or an object to explode at")
		}
		at, ok := rt.positionOf(args[0])
		if !ok {
			return nil, fmt.Errorf("explode takes a vec or an object, not a %s", flang.TypeOf(args[0]))
		}
		if rt.lvl == nil {
			return flang.NullVal{}, nil
		}
		rt.markDirty()

		fx := world.NewEffect(world.UniqueEffectID(rt.lvl, "explosion"), world.EffectExplosion, at)
		if r, ok := verbNum(args, 1); ok {
			if r <= 0 {
				return nil, fmt.Errorf("explode needs a radius above zero, not %v", r)
			}
			// The flash scales with the fireball, so a small pop does not light
			// the whole level and a big one reaches past its own smoke.
			scale := float32(r) / fx.Explosion.Radius
			fx.Explosion.Radius = float32(r)
			fx.Explosion.LightRadius *= scale
			fx.Explosion.Rise *= scale
		}
		// Once, then gone: see world.EffectUpdate, which reaps it.
		fx.Loop = false
		fx.OneShot = true

		rt.lvl.Effects = append(rt.lvl.Effects, fx)
		// The cache is indexed by name and the new effect is not in it yet.
		rt.cache = buildRefCache(rt.lvl)
		return flang.RefVal{Kind: flang.RefEffect, A: fx.ID}, nil
	},
}
