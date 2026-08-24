package main

// script_actions.go — the world verbs.
//
// These are the builtins that do something rather than compute something:
// move an entity, play a clip, spawn, destroy. They live here rather than in
// internal/flang because what they mean is the engine's business — the language
// knows only that a host may or may not provide a verb by that name.
//
// Every verb marks the play session dirty, because every one of them changes
// authored state.
//
// Several of them report failure as a `false` return rather than as an error,
// because "the entity has no attachment by that name" is a thing a script may
// legitimately not care about — `if not play_anim(...)` is a reasonable line to
// write. But a script that ignores the result, which is nearly all of them, then
// gets silence: the call did nothing and nothing said so. Every such path now
// also calls rt.warnAt, so the no-op reaches the log window with the line that
// caused it and a sentence naming the fix. The reasons themselves come from
// entity_diagnose.go, shared with the script editor's linter so that the message
// before the run and the message during it are the same message.

import (
	"fmt"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
)

// action dispatches a world verb. The bool reports whether this engine provides
// it at all, which is what lets flang say "this engine does not provide X"
// rather than "no function named X".
func (rt *ScriptRuntime) action(name string, args []flang.Value, pos flang.Pos) (flang.Value, bool, error) {
	fn, ok := scriptVerbs[name]
	if !ok {
		return nil, false, nil
	}
	v, err := fn(rt, args, pos)
	return v, true, err
}

// scriptVerb is one world verb.
type scriptVerb func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error)

var scriptVerbs map[string]scriptVerb

func init() {
	scriptVerbs = map[string]scriptVerb{
		"player": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			if PlayerEntity == nil {
				rt.warnAt(pos, "player() is null: there is no player yet, which is the case in the editor and before the level finishes loading")
				return flang.NullVal{}, nil
			}
			return flang.RefVal{Kind: flang.RefEntity, A: "player"}, nil
		},

		"time": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			return flang.FloatVal(scriptElapsed()), nil
		},

		"find": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			tag, err := verbStr(args, 0, "find")
			if err != nil {
				return nil, err
			}
			refs := rt.cache.refsWithTag(tag)
			if len(refs) == 0 {
				rt.warnAt(pos, "find(%q) matched nothing: no object in the level carries that tag%s",
					tag, rt.nearestTag(tag))
			}
			elems := make([]flang.Value, len(refs))
			for i, r := range refs {
				elems[i] = r
			}
			return flang.NewList(elems...), nil
		},

		"distance": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			if len(args) != 2 {
				return nil, fmt.Errorf("distance takes two objects or two vecs")
			}
			a, aok := rt.positionOf(args[0])
			b, bok := rt.positionOf(args[1])
			if !aok || !bok {
				return nil, fmt.Errorf("distance needs two things with a position")
			}
			return flang.FloatVal(float64(a.Sub(b).Len())), nil
		},

		"move_to": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			e, err := rt.verbEntity(args, 0, "move_to")
			if err != nil {
				return nil, err
			}
			p, ok := verbVec(args, 1)
			if !ok {
				return nil, fmt.Errorf("move_to takes an object and a vec")
			}
			rt.markDirty()
			e.Position = p
			return flang.NullVal{}, nil
		},

		"set_velocity": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			e, err := rt.verbEntity(args, 0, "set_velocity")
			if err != nil {
				return nil, err
			}
			v, ok := verbVec(args, 1)
			if !ok {
				return nil, fmt.Errorf("set_velocity takes an object and a vec")
			}
			rt.markDirty()
			e.Velocity = v
			return flang.NullVal{}, nil
		},

		"play_anim": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			e, err := rt.verbEntity(args, 0, "play_anim")
			if err != nil {
				return nil, err
			}
			// Two spellings: (ref, animation) plays on every attachment, and
			// (ref, model, animation) targets one. The three-argument form is
			// what a rigged entity with several attachments needs.
			model, anim := "", ""
			switch len(args) {
			case 2:
				anim, _ = verbStrAt(args, 1)
			case 3:
				model, _ = verbStrAt(args, 1)
				anim, _ = verbStrAt(args, 2)
			default:
				return nil, fmt.Errorf("play_anim takes (object, animation) or (object, model, animation)")
			}
			if anim == "" {
				return nil, fmt.Errorf("play_anim needs an animation name")
			}
			if ok := e.PlayAnimation(model, anim); !ok {
				rt.warnAt(pos, "play_anim did nothing: %s", e.AnimationProblem(model, anim))
				return flang.BoolVal(false), nil
			}
			return flang.BoolVal(true), nil
		},

		"stop_anim": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			e, err := rt.verbEntity(args, 0, "stop_anim")
			if err != nil {
				return nil, err
			}
			if !entityCanAnimate(e) {
				rt.warnAt(pos, "stop_anim did nothing: no attachment of entity %q can animate", e.ID)
			}
			e.StopAnimations()
			return flang.NullVal{}, nil
		},

		// set_texture reskins whatever a ref names. Two spellings, mirroring
		// play_anim's: (ref, texture) covers a wall, a floor, a ceiling or every
		// attachment of an entity, and (ref, model, texture) targets one
		// attachment of an entity with several.
		//
		// A wall's texture is also `@w.prop.texture`; the verb exists because
		// an entity's models are not one property, and because "change this
		// thing's texture" should read the same whatever the thing is.
		"set_texture": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			if len(args) < 2 || len(args) > 3 {
				return nil, fmt.Errorf("set_texture takes (object, texture) or (object, model, texture)")
			}
			r, ok := args[0].(flang.RefVal)
			if !ok {
				return nil, fmt.Errorf("set_texture takes an object, not a %s", flang.TypeOf(args[0]))
			}
			name, err := verbStr(args, len(args)-1, "set_texture")
			if err != nil {
				return nil, err
			}
			if len(args) == 3 {
				if r.Kind != flang.RefEntity {
					return nil, fmt.Errorf("only an entity has models; set_texture on a %s takes two arguments", r.Kind)
				}
				model, _ := verbStrAt(args, 1)
				e := rt.host.entityFor(r)
				if e == nil {
					return nil, fmt.Errorf("set_texture: %s is not an entity that exists", r)
				}
				rt.markDirty()
				rt.markTextureDirty(name)
				if ok := e.SetTexture(model, name); !ok {
					rt.warnAt(pos, "set_texture did nothing: %s", e.TextureProblem(model))
					return flang.BoolVal(false), nil
				}
				return flang.BoolVal(true), nil
			}
			switch r.Kind {
			case flang.RefEntity, flang.RefEdge, flang.RefFloor, flang.RefCeiling:
				if e := rt.host.entityFor(r); e != nil {
					// The property setter reskins every attachment and cannot
					// report "there were none", so the check happens here. A ref
					// that resolves to no entity is left to SetProp, which
					// errors — that is not a silent no-op and needs no warning.
					if why := e.TextureProblem(""); why != "" {
						rt.warnAt(pos, "set_texture did nothing: %s", why)
						return flang.BoolVal(false), nil
					}
				}
				// Through the property table, so the reskin queues the same
				// texture load and the same deferred rebuild a write to
				// `.prop.texture` would.
				if err := rt.host.SetProp(r, "texture", flang.StrVal(name)); err != nil {
					return nil, err
				}
				return flang.BoolVal(true), nil
			}
			return nil, fmt.Errorf("a %s has no texture; name an entity, an edge, a floor or a ceiling", r.Kind)
		},

		"spawn": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			typeName, err := verbStr(args, 0, "spawn")
			if err != nil {
				return nil, err
			}
			p, ok := verbVec(args, 1)
			if !ok {
				return nil, fmt.Errorf("spawn takes a type name and a vec")
			}
			if rt.lvl == nil {
				return flang.NullVal{}, nil
			}
			def, err := LoadEntityDef(typeName)
			if err != nil {
				return nil, fmt.Errorf("spawn %q: %w", typeName, err)
			}
			rt.markDirty()
			id := uniqueEntityID(rt.lvl, typeName)
			e := def.Instantiate(id)
			e.Position = p
			// A model that will not load costs the entity its mesh, not its
			// existence — the bounds, the attributes and the ID still mean
			// something, and a script can act on all three.
			if err := e.Load(); err != nil {
				rt.logLine(fmt.Sprintf("spawn %s: %v", id, err))
			}
			rt.lvl.Entities = append(rt.lvl.Entities, e)
			// The cache is indexed by name and the new entity is not in it yet.
			rt.cache = buildRefCache(rt.lvl)
			return flang.RefVal{Kind: flang.RefEntity, A: id}, nil
		},

		"destroy": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("destroy takes one object")
			}
			r, ok := args[0].(flang.RefVal)
			if !ok {
				return nil, fmt.Errorf("destroy takes an object, not a %s", flang.TypeOf(args[0]))
			}
			if r.A == "player" {
				// The engine owns the player and re-creates it; letting a script
				// delete it would leave the camera riding on nothing.
				return nil, fmt.Errorf("the player cannot be destroyed")
			}
			if rt.lvl == nil {
				return flang.BoolVal(false), nil
			}
			rt.markDirty()
			// `:destroy:` is posted before the object goes, so its own handler
			// can still read it. Marked as an engine event rather than a script
			// send even though a script asked for it: most objects have no
			// `:destroy:` clause and do not need one, so "nothing handled it"
			// would be a warning about nothing.
			_ = rt.postFrom(r, "destroy", nil, pos, false)
			removed := rt.removeObject(r)
			if !removed {
				rt.warnAt(pos, "destroy did nothing: %s is %s",
					r, destroyRefusal(r))
			}
			rt.cache = buildRefCache(rt.lvl)
			return flang.BoolVal(removed), nil
		},

		"after": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("after takes a delay in milliseconds and an atom")
			}
			ms, ok := verbNum(args, 0)
			if !ok {
				return nil, fmt.Errorf("after's delay is a number of milliseconds")
			}
			atom, ok := args[1].(flang.AtomVal)
			if !ok {
				return nil, fmt.Errorf("after takes an atom, not a %s", flang.TypeOf(args[1]))
			}
			// Addressed to whoever is running, which is what makes `after` read
			// as "remind me" rather than "tell someone".
			scriptAddTimer(ms, rt.self, string(atom), args[2:], pos)
			return flang.NullVal{}, nil
		},
	}
}

// removeObject deletes whatever a ref names, reporting whether it found it.
// Only entities and volumes can be destroyed: geometry is the level's shape, and
// a script deleting a wall would leave a sector whose ring no longer closes.
func (rt *ScriptRuntime) removeObject(r flang.RefVal) bool {
	switch r.Kind {
	case flang.RefEntity:
		for i := range rt.lvl.Entities {
			if rt.lvl.Entities[i].ID == r.A {
				rt.lvl.Entities[i].Delete()
				rt.lvl.Entities = append(rt.lvl.Entities[:i], rt.lvl.Entities[i+1:]...)
				return true
			}
		}
	case flang.RefVolume:
		for i := range rt.lvl.Volumes {
			if rt.lvl.Volumes[i].ID == r.A {
				rt.lvl.Volumes[i].Geometry.Delete()
				rt.lvl.Volumes = append(rt.lvl.Volumes[:i], rt.lvl.Volumes[i+1:]...)
				return true
			}
		}
	}
	return false
}

// ---------- argument helpers ----------

// positionOf accepts either a vec or an object, so `distance(@a, @b)` and
// `distance(p, q)` both work.
func (rt *ScriptRuntime) positionOf(v flang.Value) (mgl32.Vec3, bool) {
	if vec, ok := toVec3(v); ok {
		return vec, true
	}
	if r, ok := v.(flang.RefVal); ok {
		return rt.host.refPosition(r)
	}
	return mgl32.Vec3{}, false
}

// verbEntity resolves an argument to an entity, including the player.
func (rt *ScriptRuntime) verbEntity(args []flang.Value, i int, name string) (*Entity, error) {
	if i >= len(args) {
		return nil, fmt.Errorf("%s needs an object", name)
	}
	r, ok := args[i].(flang.RefVal)
	if !ok {
		if _, isNull := args[i].(flang.NullVal); isNull {
			// The overwhelmingly common way to get here: `@name` for a name the
			// level does not have resolves to null by design (see evalAddr), so
			// the mistake is one line further up than the error would otherwise
			// suggest.
			return nil, fmt.Errorf("%s was given null — an `@name` that matches nothing in the level resolves to null, so check the object's ID", name)
		}
		return nil, fmt.Errorf("%s takes an object, not a %s", name, flang.TypeOf(args[i]))
	}
	e := rt.host.entityFor(r)
	if e == nil {
		return nil, fmt.Errorf("%s: %s is not an entity that exists", name, r)
	}
	return e, nil
}

// entityCanAnimate reports whether any attachment has playback state at all.
func entityCanAnimate(e *Entity) bool {
	for i := range e.Models {
		if e.Models[i].Anim != nil {
			return true
		}
	}
	return false
}

// destroyRefusal says why a ref could not be destroyed. Only entities and
// volumes can be, so "it is a wall" and "there is no such entity" are the two
// answers and they want different fixes.
func destroyRefusal(r flang.RefVal) string {
	switch r.Kind {
	case flang.RefEntity, flang.RefVolume:
		return "not in the level (it may already have been destroyed)"
	case flang.RefNone:
		return "nothing — an `@name` that matches nothing resolves to null"
	}
	return "a " + r.Kind.String() + ", and only entities and volumes can be destroyed"
}

// nearestTag renders a "did you mean" for a tag that matched nothing, or "" when
// nothing in the level is close enough to suggest.
func (rt *ScriptRuntime) nearestTag(tag string) string {
	if best := nearestName(tag, rt.cache.allTags()); best != "" {
		return fmt.Sprintf("; did you mean #%s?", best)
	}
	return ""
}

func verbStr(args []flang.Value, i int, name string) (string, error) {
	s, ok := verbStrAt(args, i)
	if !ok {
		return "", fmt.Errorf("%s needs a name as argument %d", name, i+1)
	}
	return s, nil
}

func verbStrAt(args []flang.Value, i int) (string, bool) {
	if i >= len(args) {
		return "", false
	}
	return toStr(args[i])
}

func verbVec(args []flang.Value, i int) (mgl32.Vec3, bool) {
	if i >= len(args) {
		return mgl32.Vec3{}, false
	}
	return toVec3(args[i])
}

func verbNum(args []flang.Value, i int) (float64, bool) {
	if i >= len(args) {
		return 0, false
	}
	switch t := args[i].(type) {
	case flang.IntVal:
		return float64(t), true
	case flang.FloatVal:
		return float64(t), true
	}
	return 0, false
}
