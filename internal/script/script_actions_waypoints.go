package script

// script_actions_waypoints.go — sending an object along a waypoint animation.
//
// Two verbs rather than a writable property, because starting an animation is
// an instruction with a moment attached: the object sets off from where it is
// NOW, and a property the next tick re-reads has no such moment. What an object
// is currently following is still readable, as `@door.prop.waypoints`.
//
// Anything a path can carry takes them — a dynamic brush, a chunk, an entity, a
// sprite, an emitter, a sound, a light or a volume — each reading the channels
// of a point it has (world/waypoint_follow.go). The movement itself is the
// engine's, on the same tick as the object's other movers, so a script sends
// it and never has to write a position again.

import (
	"fmt"

	"foundry/internal/flang"
	"foundry/internal/world"
)

// waypointFollowerKinds is what the waypoint verbs take, in the words their
// errors use.
const waypointFollowerKinds = "a dynamic brush, a chunk, an entity, a sprite, an emitter, a sound, a dynamic light or a volume"

// verbDynamicBrush resolves argument i to a brush a script may move, with the
// error an author can act on — the same refusal, in the same words, that
// writing `position` on a static brush gets.
func (rt *ScriptRuntime) verbDynamicBrush(args []flang.Value, i int, name string) (*world.Chunk, *world.Brush, error) {
	if i >= len(args) {
		return nil, nil, fmt.Errorf("%s needs a brush", name)
	}
	r, ok := args[i].(flang.RefVal)
	if !ok {
		if _, isNull := args[i].(flang.NullVal); isNull {
			return nil, nil, fmt.Errorf("%s was given null — an `@name` that matches nothing in the level resolves to null, so check the brush's ID", name)
		}
		return nil, nil, fmt.Errorf("%s takes a brush, not a %s", name, flang.TypeOf(args[i]))
	}
	if r.Kind != flang.RefBrush {
		return nil, nil, fmt.Errorf("%s takes a brush; %s is a %s", name, r.String(), r.Kind)
	}
	ch, b := rt.cache.brushOwnerFor(r)
	if b == nil {
		return nil, nil, &scriptMissingError{r.String()}
	}
	if !b.Dynamic {
		return nil, nil, fmt.Errorf("%s: %s is not dynamic; tick Dynamic in its properties panel first", name, r.String())
	}
	return ch, b, nil
}

// waypointFollowerFor resolves a ref to the object it names, as something a
// waypoint animation can carry — or nil, for a kind no path can move as well as
// for a name nothing has. Only the lookup: whether a script may send that
// object is verbWaypointFollower's business.
func (c *refCache) waypointFollowerFor(r flang.RefVal) world.WaypointFollower {
	switch r.Kind {
	case flang.RefBrush:
		if ch, b := c.brushOwnerFor(r); b != nil {
			return world.BrushWaypointFollower(ch, b)
		}
	case flang.RefChunk:
		if ch := c.chunkFor(r); ch != nil {
			return ch
		}
	case flang.RefEntity:
		if e := c.entityFor(r); e != nil {
			return e
		}
	case flang.RefSprite:
		if s := c.spriteFor(r); s != nil {
			return s
		}
	case flang.RefEmitter:
		if e := c.emitterFor(r); e != nil {
			return e
		}
	case flang.RefSound:
		if s := c.soundFor(r); s != nil {
			return s
		}
	case flang.RefVolume:
		if v := c.volumeFor(r); v != nil {
			return v
		}
	case flang.RefLight:
		switch d, p := c.lightFor(r); {
		case d != nil:
			return d
		case p != nil:
			return p
		}
	}
	return nil
}

// verbWaypointFollower resolves argument i to an object a script may send along
// a waypoint animation.
//
// A brush must be dynamic, for the reason writing its `position` must be. The
// player and vehicles are refused on move_to's grounds: each has a mover of its
// own — the player's input, a vehicle's drive model — that would fight the path
// every frame.
func (rt *ScriptRuntime) verbWaypointFollower(args []flang.Value, i int, name string) (world.WaypointFollower, error) {
	if i >= len(args) {
		return nil, fmt.Errorf("%s needs an object", name)
	}
	r, ok := args[i].(flang.RefVal)
	if !ok {
		if _, isNull := args[i].(flang.NullVal); isNull {
			return nil, fmt.Errorf("%s was given null — an `@name` that matches nothing in the level resolves to null, so check the object's ID", name)
		}
		return nil, fmt.Errorf("%s takes %s, not a %s", name, waypointFollowerKinds, flang.TypeOf(args[i]))
	}
	switch r.Kind {
	case flang.RefBrush:
		ch, b, err := rt.verbDynamicBrush(args, i, name)
		if err != nil {
			return nil, err
		}
		return world.BrushWaypointFollower(ch, b), nil
	case flang.RefChunk:
		ch := rt.cache.chunkFor(r)
		if ch == nil {
			return nil, &scriptMissingError{r.String()}
		}
		return ch, nil
	case flang.RefEntity:
		e, err := rt.verbEntity(args, i, name)
		if err != nil {
			return nil, err
		}
		if e == world.PlayerEntity {
			return nil, fmt.Errorf("%s cannot move the player: its movement belongs to the player's input", name)
		}
		if e.IsVehicle() {
			return nil, fmt.Errorf("%s: %s is a vehicle, and its drive model owns its motion; write its velocity instead", name, r)
		}
		return e, nil
	case flang.RefSprite, flang.RefEmitter, flang.RefSound, flang.RefVolume, flang.RefLight:
		if f := rt.cache.waypointFollowerFor(r); f != nil {
			// A light must be dynamic, for the reason writing its position
			// must be: a static one's light is baked where it was authored.
			if d, p := rt.cache.lightFor(r); (d != nil && !d.Dynamic) || (p != nil && !p.Dynamic) {
				return nil, staticLightError(r.String(), name+" cannot move")
			}
			return f, nil
		}
		return nil, &scriptMissingError{r.String()}
	}
	return nil, fmt.Errorf("%s takes %s; %s is a %s", name, waypointFollowerKinds, r, r.Kind)
}

// verbWaypointAnimation resolves argument i to a waypoint animation. Either
// spelling is accepted — `@door_open` or "door_open" — since an animation's
// name is as likely to arrive in a string attribute as to be written inline.
//
// A name that is not a level ID is also tried as a path an entity carries,
// qualified by the entity f belongs to and then by the entity whose script is
// running: so `play_waypoints(@self.hatch, "door_close")` in a type's script
// finds this placement's door_close, and every other ship's is left alone.
func (rt *ScriptRuntime) verbWaypointAnimation(f world.WaypointFollower, args []flang.Value, i int, name string) (*world.WaypointAnimation, error) {
	if i >= len(args) {
		return nil, fmt.Errorf("%s needs a waypoint animation", name)
	}
	switch v := args[i].(type) {
	case flang.RefVal:
		if v.Kind != flang.RefWaypoints {
			return nil, fmt.Errorf("%s takes a waypoint animation; %s is a %s", name, v.String(), v.Kind)
		}
		if a := rt.cache.waypointAnimationFor(v); a != nil {
			return a, nil
		}
		return nil, &scriptMissingError{v.String()}
	case flang.StrVal:
		if a := world.FindWaypointAnimation(rt.lvl, string(v)); a != nil {
			return a, nil
		}
		carriers := []string{world.WaypointFollowerCarrier(f)}
		if rt.self.Kind == flang.RefEntity {
			carriers = append(carriers, rt.self.A)
		}
		for _, carrier := range carriers {
			if carrier == "" {
				continue
			}
			if a := world.FindWaypointAnimation(rt.lvl, world.EntityWaypointAnimationID(carrier, string(v))); a != nil {
				return a, nil
			}
		}
		return nil, fmt.Errorf("%s: this level has no waypoint animation called %q", name, string(v))
	case flang.NullVal:
		return nil, fmt.Errorf("%s was given null for its animation — an `@name` that matches nothing in the level resolves to null, so check the animation's ID", name)
	}
	return nil, fmt.Errorf("%s takes a waypoint animation or its name, not a %s", name, flang.TypeOf(args[i]))
}

// waypointsProp is the read-only `waypoints` property of every kind a path can
// carry: the animation the object is travelling, or null.
func waypointsProp() propAccess {
	return propAccess{
		Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			f := c.waypointFollowerFor(r)
			if f == nil {
				return nil, false
			}
			if id := world.FollowedWaypointAnimation(f); id != "" {
				return flang.RefVal{Kind: flang.RefWaypoints, A: id}, true
			}
			return flang.NullVal{}, true
		},
		Doc: "read-only: the waypoint animation this is travelling, or null — see play_waypoints",
	}
}

// waypointVerbs is merged into scriptVerbs at init.
var waypointVerbs = map[string]scriptVerb{
	"play_waypoints": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("play_waypoints takes an object and a waypoint animation")
		}
		f, err := rt.verbWaypointFollower(args, 0, "play_waypoints")
		if err != nil {
			return nil, err
		}
		anim, err := rt.verbWaypointAnimation(f, args, 1, "play_waypoints")
		if err != nil {
			return nil, err
		}
		if world.WaypointFollowerCarriesAnimation(f, anim) {
			return nil, fmt.Errorf("play_waypoints: %s carries %q, and cannot follow a path that moves with it; send one of its parts along it instead", args[0], anim.ID)
		}
		if len(anim.Points) == 0 {
			// A false rather than an error, on play_anim's rule: nothing is
			// wrong with the call, there is simply nowhere to go. But said, so
			// a door that never opens is not a mystery.
			rt.warnAt(pos, "play_waypoints did nothing: waypoint animation %q has no points", anim.ID)
			return flang.BoolVal(false), nil
		}
		rt.markDirty()
		world.PlayWaypoints(rt.lvl, f, anim)
		return flang.BoolVal(true), nil
	},

	"stop_waypoints": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("stop_waypoints takes an object")
		}
		f, err := rt.verbWaypointFollower(args, 0, "stop_waypoints")
		if err != nil {
			return nil, err
		}
		rt.markDirty()
		return flang.BoolVal(world.StopWaypoints(f)), nil
	},
}
