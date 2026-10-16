package script

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

	"foundry/internal/collision"
	"foundry/internal/flang"
	"foundry/internal/world"
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
			if world.PlayerEntity == nil {
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

		// impulse is the one verb that speaks to the physics rather than around
		// it. Everything else that moves something either places it outright
		// (`position`), eases it (`move_to`) or drives it along a path — all of
		// which take the body OUT of the simulation for as long as they run.
		// An impulse hands it a change in momentum and lets the simulation
		// carry on: gravity still pulls, walls still stop it, the ground still
		// bleeds it off. That is what a blast, a launch pad, a recoil shove and
		// a jump pad all are, and writing `velocity` for them is wrong in the
		// one way that matters — it discards whatever the body was already
		// doing, so a grenade going off under a running player stops them dead
		// and then throws them.
		"impulse": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			if len(args) != 2 {
				return nil, fmt.Errorf("impulse takes (object, vec): the change in velocity, in units a second")
			}
			e, err := rt.verbEntity(args, 0, "impulse")
			if err != nil {
				return nil, err
			}
			d, ok := toVec3(args[1])
			if !ok {
				return nil, fmt.Errorf("impulse takes a vec, not a %s", flang.TypeOf(args[1]))
			}
			// World space in, the body's own frame stored — `velocity`'s own
			// bargain, for its reason: an impulse is almost always aimed at
			// something, and a rider's Y is not the ship's Y once the ship
			// rolls. A rotation only, so the length is untouched.
			if rt.lvl != nil {
				d = rt.lvl.Frames.FrameFor(e).DirFromWorld(d)
			}
			e.Velocity = e.Velocity.Add(d)
			rt.markDirty()
			// A body being eased or walked along a path has its position
			// written for it every frame, so the momentum sits there unspent
			// until whatever is carrying it lets go — and then arrives all at
			// once. Worth saying out loud: it is a real effect, and it never
			// looks like the one that was wanted.
			if e.Tweens.Easing(world.TweenMove) || e.Waypoints != nil {
				rt.warnAt(pos, "impulse on @%s does nothing yet: a move_to or a waypoint animation is writing its position, and the momentum is only spent once that lets go", e.ID)
			}
			return flang.BoolVal(true), nil
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
			def, err := world.LoadEntityDef(typeName)
			if err != nil {
				return nil, fmt.Errorf("spawn %q: %w", typeName, err)
			}
			rt.markDirty()
			id := world.UniqueEntityID(rt.lvl, typeName)
			// Whatever an object of this name left behind is not this object's.
			//
			// Everything about an object is keyed by its ref, and a ref is a
			// name: the ninth grenade a player throws is called `grenade`
			// again, because the eighth blew itself up and gave the name back.
			// Without this it comes into the world holding the eighth's
			// scratch — its bounce count, its "I have already gone off" flag —
			// and, worse, holding the eighth's place in the engine's own
			// bookkeeping: see forgetNamesake, which is the rest of this and
			// says what each piece of it costs.
			ref := flang.RefVal{Kind: flang.RefEntity, A: id}
			delete(rt.state, ref)
			forgetNamesake(ref)
			e := def.Instantiate(id)
			e.Position = p
			// A model that will not load costs the entity its mesh, not its
			// existence — the bounds, the attributes and the ID still mean
			// something, and a script can act on all three.
			if err := e.Load(); err != nil {
				rt.logLine(fmt.Sprintf("spawn %s: %v", id, err))
			}
			rt.lvl.Entities = append(rt.lvl.Entities, e)
			// Its script, if nothing already in the level named it. The runtime
			// loads what the level references when play starts, and a type that
			// only ever arrives by spawn — a thrown grenade — is referenced by
			// nothing placed, so without this its `:spawn:` and every message
			// after went to a script that was never parsed. File I/O, which a
			// tick may do; loadUnit is a no-op for a script already loaded.
			rt.loadUnit(e.Script)
			// The effects the type carries, materialised as level emitters
			// riding the new entity (world/entity_emitter.go). Their textures
			// are queued rather than loaded, because the tick must not touch
			// GL — the rule spawn_effect follows.
			tex := map[string]bool{}
			if err := world.MaterializeEntityEmitters(rt.lvl, &rt.lvl.Entities[len(rt.lvl.Entities)-1], tex); err != nil {
				rt.logLine(fmt.Sprintf("spawn %s: %v", id, err))
			}
			for t := range tex {
				rt.markTextureDirty(t)
			}
			// And the volumes it carries (world/entity_volume.go). No cage: that
			// is GL work and an editor aid, and play draws no cages.
			world.MaterializeEntityVolumes(rt.lvl, &rt.lvl.Entities[len(rt.lvl.Entities)-1])
			// And its paths (world/entity_waypoints.go), so the new ship's door
			// has somewhere to close to.
			world.MaterializeEntityWaypointAnimations(rt.lvl, &rt.lvl.Entities[len(rt.lvl.Entities)-1])
			// The cache is indexed by name and the new entity is not in it yet.
			rt.syncCache(rt.lvl)
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
			rt.syncCache(rt.lvl)
			return flang.BoolVal(removed), nil
		},

		"mount": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			// mount(rider, vehicle, "seat"). The seat is optional: without one
			// the rider comes aboard standing, which is what a cargo hold wants.
			e, err := rt.verbEntity(args, 0, "mount")
			if err != nil {
				return nil, err
			}
			v, err := rt.verbEntity(args, 1, "mount")
			if err != nil {
				return nil, err
			}
			seat, _ := verbStrAt(args, 2)
			if !v.IsVehicle() {
				rt.warnAt(pos, "mount: %s is not a vehicle", v.ID)
				return flang.BoolVal(false), nil
			}
			if seat != "" && v.Vehicle.Seat(seat) == nil {
				rt.warnAt(pos, "mount: %s has no seat called %q", v.ID, seat)
				return flang.BoolVal(false), nil
			}
			rt.markDirty()
			ok := collision.Mount(rt.lvl, e, v, seat)
			if ok {
				rt.post(flang.RefVal{Kind: flang.RefEntity, A: v.ID}, "mount",
					[]flang.Value{flang.RefVal{Kind: flang.RefEntity, A: e.ID}, flang.StrVal(seat)}, pos)
			}
			return flang.BoolVal(ok), nil
		},

		"dismount": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			e, err := rt.verbEntity(args, 0, "dismount")
			if err != nil {
				return nil, err
			}
			if e.Frame == "" {
				return flang.BoolVal(false), nil
			}
			seat := e.Seat
			rt.markDirty()
			v := collision.Dismount(rt.lvl, e)
			if v != nil {
				rt.post(flang.RefVal{Kind: flang.RefEntity, A: v.ID}, "dismount",
					[]flang.Value{flang.RefVal{Kind: flang.RefEntity, A: e.ID}, flang.StrVal(seat)}, pos)
			}
			return flang.BoolVal(v != nil), nil
		},

		// ease_sky is play_music's own trick applied to the sky: it does not
		// itself change a single field, it arms a blend that whatever `sky_*`
		// properties get written next will be eased INTO rather than snapped
		// to, over the given number of seconds. See world.Sky.BeginTransition
		// and SCRIPTING.md's "Sky" section for the two-line pattern this is
		// meant to be called in.
		"ease_sky": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			seconds, ok := verbNum(args, 0)
			if !ok {
				return nil, fmt.Errorf("ease_sky takes a number of seconds")
			}
			if rt.lvl == nil {
				return flang.BoolVal(false), nil
			}
			rt.markDirty()
			if rt.lvl.Sky == nil {
				// Nothing to ease FROM but the level's default look, which is
				// exactly what a script adding a sky for the first time and
				// easing it in wants — the same auto-vivify the sky_* props
				// give a level that has none yet.
				sk := world.DefaultSky()
				rt.lvl.Sky = &sk
			}
			rt.lvl.Sky.BeginTransition(float32(seconds))
			// True only when this call actually armed a blend: `seconds <= 0`
			// is BeginTransition's cancel spelling, and a script checking the
			// result should be able to tell "eased" from "snapped".
			return flang.BoolVal(seconds > 0), nil
		},
	}

	// The particle, attach and audio verbs live in their own files and their
	// own maps, so each surface can be read in one place; they are merged in
	// here so there is still exactly one table the runtime looks in.
	for _, group := range []map[string]scriptVerb{particleVerbs, attachVerbs, audioVerbs, songVerbs, effectVerbs, decalVerbs, uiVerbs, bindingVerbs, keyVerbs, padVerbs, saveVerbs, hitscanVerbs, waypointVerbs, tweenVerbs, aiVerbs, boneVerbs, cameraVerbs, waterVerbs} {
		for name, fn := range group {
			if _, clash := scriptVerbs[name]; clash {
				panic("script: two implementations of the verb " + name)
			}
			scriptVerbs[name] = fn
		}
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
				// The effects its type hung on it go with it, or the level goes
				// on emitting from where the thing used to be — the emitters
				// ride the entity through Attach, and an Attach naming nothing
				// resolves to the world origin rather than to nowhere.
				world.DropEntityEmitters(rt.lvl, rt.lvl.Entities[i].ID)
				// Its volumes likewise, for the same reason and more urgently: a
				// sight cone left behind would go on firing `enter` at the origin.
				world.DropEntityVolumes(rt.lvl, rt.lvl.Entities[i].ID)
				// And its paths: anything still on one stops where it has got to.
				world.DropEntityWaypointAnimations(rt.lvl, rt.lvl.Entities[i].ID)
				// Dropping volumes compacts that slice, not this one, so i still
				// names the entity.
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
func (rt *ScriptRuntime) verbEntity(args []flang.Value, i int, name string) (*world.Entity, error) {
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
func entityCanAnimate(e *world.Entity) bool {
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
	if best := world.NearestName(tag, rt.cache.allTags()); best != "" {
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
