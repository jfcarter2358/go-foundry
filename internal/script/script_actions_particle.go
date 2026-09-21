package script

// script_actions_particle.go — the particle verbs.
//
//	emit @fountain              start (or resume) continuous emission
//	stop_emit @fountain         stop it; live particles finish, bursts still fire
//	burst @fountain 40          release n particles now
//	burst @fountain 40 "sparks" the same, from one named layer
//	spawn_effect "explosion" <pos>   a detached one-shot at a point
//
// spawn_effect is the one gameplay actually reaches for: an explosion at an
// impact point that no level authored an emitter for. It places a real emitter
// so everything else — attachment, culling, the debug counts — applies to it
// unchanged, and marks it self-destructing so nothing has to remember to clean
// it up.
//
// None of these touch GL. Loading an effect document is file I/O, which is
// allowed in a tick; loading the TEXTURE it names is not, so it is deferred
// through markTextureDirty to DrainScriptWork like every other texture write.

import (
	"fmt"
	"foundry/internal/flang"
	"foundry/internal/world"
)

// verbEmitter resolves argument i to an emitter, with the error a script author
// can act on.
func (rt *ScriptRuntime) verbEmitter(args []flang.Value, i int, name string) (*world.Emitter, error) {
	if i >= len(args) {
		return nil, fmt.Errorf("%s needs an emitter", name)
	}
	r, ok := args[i].(flang.RefVal)
	if !ok {
		return nil, fmt.Errorf("%s takes an emitter, not a %s", name, flang.TypeOf(args[i]))
	}
	if r.Kind != flang.RefEmitter {
		return nil, fmt.Errorf("%s takes an emitter; %s is a %s", name, r.String(), r.Kind)
	}
	e := rt.cache.emitterFor(r)
	if e == nil {
		return nil, &scriptMissingError{r.String()}
	}
	return e, nil
}

// particleVerbs is merged into scriptVerbs at init. Kept in its own file and
// its own map so the particle surface can be read in one place.
var particleVerbs = map[string]scriptVerb{
	"emit": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		e, err := rt.verbEmitter(args, 0, "emit")
		if err != nil {
			return nil, err
		}
		rt.markDirty()
		// Enabled as well as Emitting: `emit` on a switched-off emitter should
		// start it, not silently do half the job.
		e.Enabled = true
		e.Emitting = true
		e.SyncSystems()
		return flang.NullVal{}, nil
	},

	"stop_emit": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		e, err := rt.verbEmitter(args, 0, "stop_emit")
		if err != nil {
			return nil, err
		}
		rt.markDirty()
		// Emitting only. Clearing Enabled would make the whole plume vanish
		// mid-air, which is `@id.prop.enabled = false` and a different request.
		e.Emitting = false
		e.SyncSystems()
		return flang.NullVal{}, nil
	},

	"burst": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		e, err := rt.verbEmitter(args, 0, "burst")
		if err != nil {
			return nil, err
		}
		n, ok := verbNum(args, 1)
		if !ok {
			return nil, fmt.Errorf("burst takes an emitter and a count")
		}
		if n < 0 {
			return nil, fmt.Errorf("burst needs a count of zero or more, not %v", n)
		}
		// The optional third argument names one layer, so an effect with a
		// flash and a plume can have just the flash fired.
		layer, _ := verbStrAt(args, 2)
		if !e.Loaded() {
			return nil, fmt.Errorf("emitter %s has no effect loaded", e.ID)
		}
		if layer != "" && e.Doc.LayerIndexByName(layer) < 0 {
			return nil, fmt.Errorf("effect %q has no layer called %q", e.Effect, layer)
		}
		rt.markDirty()
		e.Burst(int(n), layer, e.OriginMatrix(rt.lvl))
		return flang.NullVal{}, nil
	},

	"spawn_effect": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		name, err := verbStr(args, 0, "spawn_effect")
		if err != nil {
			return nil, err
		}
		p, ok := verbVec(args, 1)
		if !ok {
			return nil, fmt.Errorf("spawn_effect takes an effect name and a vec")
		}
		if rt.lvl == nil {
			return flang.NullVal{}, nil
		}
		rt.markDirty()

		e := world.NewEmitter(world.UniqueEmitterID(rt.lvl, name), p)
		e.Effect = name
		// Self-destructing: it is removed once its last particle dies, so a
		// script firing one per impact does not fill the level with spent
		// emitters. See world.EmitterUpdate.
		e.OneShot = true
		if err := e.Load(); err != nil {
			return nil, fmt.Errorf("spawn_effect %q: %w", name, err)
		}
		// Every texture the effect reaches for, queued rather than loaded: the
		// tick must not touch GL.
		for i := range e.Doc.Layers {
			rt.markTextureDirty(e.Doc.Layers[i].Texture)
		}
		rt.lvl.Emitters = append(rt.lvl.Emitters, e)
		// The cache is indexed by name and the new emitter is not in it yet.
		rt.cacheAdded(func(c *refCache) { c.emitter[e.ID] = len(rt.lvl.Emitters) - 1 })
		return flang.RefVal{Kind: flang.RefEmitter, A: e.ID}, nil
	},
}
