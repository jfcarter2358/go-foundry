package script

// script_actions_ai.go — AI controllers, as a script reaches them.
//
// A controller runs itself: a type with an `ai:` block hunts, patrols and
// wanders with no script at all. What a script adds is reacting — the events
// below arrive at the entity's own script — and overruling: sending an agent
// somewhere, choosing its target, handing it an objective.
//
// Verbs rather than properties for the instructions, on play_waypoints' reason:
// "walk there" has a moment attached. What the controller is doing is readable
// as properties (`ai_state`, `ai_target`, `ai_destination`), and its tuning is
// writable as properties, because a guard that runs once it is alerted needs
// its speed to change during play.

import (
	"fmt"
	"strings"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/ai"
	"foundry/internal/flang"
	"foundry/internal/hooks"
	"foundry/internal/world"
)

// aiEventAtoms is every atom a controller posts by itself, for the linter's
// near-miss check.
var aiEventAtoms = []string{
	"target_acquired", "target_lost", "target_in_range",
	"ai_arrived", "ai_path_failed",
	"objective_reached", "objective_complete", "objectives_done",
	"cover_taken", "cover_lost",
}

// postAIEvents turns what the controllers did this frame into messages.
//
// Every one goes to the agent with the agent first, as `src`, the way the
// waypoint events do. A target event carries the target after it; an objective
// event carries the volume, and goes to the volume as well — so a console's
// script hears it was hacked without knowing who might hack it.
func postAIEvents(rt *ScriptRuntime) {
	for _, ev := range hooks.TakeAIEvents() {
		agent := flang.RefVal{Kind: flang.RefEntity, A: ev.Agent}
		switch {
		case ev.Target != "":
			target := flang.RefVal{Kind: flang.RefEntity, A: ev.Target}
			_ = rt.post(agent, ev.Atom, []flang.Value{agent, target}, flang.Pos{})
		case ev.Objective != "":
			vol := flang.RefVal{Kind: flang.RefVolume, A: ev.Objective}
			_ = rt.post(agent, ev.Atom, []flang.Value{agent, vol}, flang.Pos{})
			if ev.ToObjective {
				_ = rt.post(vol, ev.Atom, []flang.Value{agent, vol}, flang.Pos{})
			}
		default:
			_ = rt.post(agent, ev.Atom, []flang.Value{agent}, flang.Pos{})
		}
	}
}

// verbAgent resolves argument i to an entity carrying a controller.
func (rt *ScriptRuntime) verbAgent(args []flang.Value, i int, name string) (*world.Entity, error) {
	e, err := rt.verbEntity(args, i, name)
	if err != nil {
		return nil, err
	}
	if e.AI == nil {
		return nil, fmt.Errorf("%s: @%s has no AI controller; give it one in the AI group of its properties, or on its type", name, e.ID)
	}
	return e, nil
}

// aiVerbs is merged into scriptVerbs at init.
var aiVerbs = map[string]scriptVerb{
	"ai_move_to": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("ai_move_to takes an agent and a place: a vec or an object")
		}
		e, err := rt.verbAgent(args, 0, "ai_move_to")
		if err != nil {
			return nil, err
		}
		goal, ok := rt.aiPlace(args[1])
		if !ok {
			return nil, fmt.Errorf("ai_move_to takes a vec or an object with a position as its second argument, not a %s", flang.TypeOf(args[1]))
		}
		if rt.lvl == nil {
			return flang.BoolVal(false), nil
		}
		rt.markDirty()
		reachable := ai.MoveTo(rt.lvl, e, goal)
		if !reachable {
			rt.warnAt(pos, "ai_move_to: @%s cannot reach <%g, %g, %g> and will walk as near as it can", e.ID, goal.X(), goal.Y(), goal.Z())
		}
		return flang.BoolVal(reachable), nil
	},

	"ai_stop": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("ai_stop takes an agent")
		}
		e, err := rt.verbAgent(args, 0, "ai_stop")
		if err != nil {
			return nil, err
		}
		rt.markDirty()
		return flang.BoolVal(ai.Stop(rt.lvl, e)), nil
	},

	"ai_target": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("ai_target takes an agent and an entity to hunt, or null to let it choose")
		}
		e, err := rt.verbAgent(args, 0, "ai_target")
		if err != nil {
			return nil, err
		}
		if _, isNull := args[1].(flang.NullVal); isNull {
			return flang.BoolVal(ai.SetTarget(rt.lvl, e, nil)), nil
		}
		t, err := rt.verbEntity(args, 1, "ai_target")
		if err != nil {
			return nil, err
		}
		if t == e {
			return nil, fmt.Errorf("ai_target: @%s cannot hunt itself", e.ID)
		}
		return flang.BoolVal(ai.SetTarget(rt.lvl, e, t)), nil
	},

	"ai_objective": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("ai_objective takes an agent and an objective volume, or null to drop its current one")
		}
		e, err := rt.verbAgent(args, 0, "ai_objective")
		if err != nil {
			return nil, err
		}
		if _, isNull := args[1].(flang.NullVal); isNull {
			return flang.BoolVal(ai.SetObjective(rt.lvl, e, nil)), nil
		}
		vol, err := rt.verbVolume(args, 1, "ai_objective")
		if err != nil {
			return nil, err
		}
		if vol.Objective == nil {
			return nil, fmt.Errorf("ai_objective: @%s is not an objective; open its AI Objective group and tick the box", vol.ID)
		}
		if !ai.SetObjective(rt.lvl, e, vol) {
			rt.warnAt(pos, "ai_objective did nothing: %s", objectiveRefusal(vol, e))
			return flang.BoolVal(false), nil
		}
		return flang.BoolVal(true), nil
	},

	"ai_follow_waypoints": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 2 && len(args) != 3 {
			return nil, fmt.Errorf("ai_follow_waypoints takes an agent, a waypoint animation (or its name, or null to stop), and optionally whether to loop")
		}
		e, err := rt.verbAgent(args, 0, "ai_follow_waypoints")
		if err != nil {
			return nil, err
		}
		if rt.lvl == nil {
			return flang.BoolVal(false), nil
		}
		if _, isNull := args[1].(flang.NullVal); isNull {
			rt.markDirty()
			return flang.BoolVal(ai.FollowWaypoints(rt.lvl, e, nil, false)), nil
		}
		var anim *world.WaypointAnimation
		switch v := args[1].(type) {
		case flang.RefVal:
			if v.Kind != flang.RefWaypoints {
				return nil, fmt.Errorf("ai_follow_waypoints takes a waypoint animation; %s is a %s", v, v.Kind)
			}
			if anim = rt.cache.waypointAnimationFor(v); anim == nil {
				return nil, &scriptMissingError{v.String()}
			}
		case flang.StrVal:
			if anim = ai.ResolveWaypoints(rt.lvl, e, string(v)); anim == nil {
				return nil, fmt.Errorf("ai_follow_waypoints: this level has no waypoint animation called %q, and @%s's type carries none by that name", string(v), e.ID)
			}
		default:
			return nil, fmt.Errorf("ai_follow_waypoints takes a waypoint animation or its name, not a %s", flang.TypeOf(args[1]))
		}
		loop := false
		if len(args) == 3 {
			b, ok := toBool(args[2])
			if !ok {
				return nil, fmt.Errorf("ai_follow_waypoints takes a bool as its third argument, not a %s", flang.TypeOf(args[2]))
			}
			loop = b
		}
		if len(anim.Points) == 0 {
			rt.warnAt(pos, "ai_follow_waypoints did nothing: waypoint animation %q has no points", anim.ID)
			return flang.BoolVal(false), nil
		}
		rt.markDirty()
		return flang.BoolVal(ai.FollowWaypoints(rt.lvl, e, anim, loop)), nil
	},

	"ai_continue": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("ai_continue takes an agent")
		}
		e, err := rt.verbAgent(args, 0, "ai_continue")
		if err != nil {
			return nil, err
		}
		return flang.BoolVal(ai.ContinueWaypoints(rt.lvl, e)), nil
	},

	"ai_follow": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("ai_follow takes an agent and the entity to keep up with, or null to follow nobody")
		}
		e, err := rt.verbAgent(args, 0, "ai_follow")
		if err != nil {
			return nil, err
		}
		if rt.lvl == nil {
			return flang.BoolVal(false), nil
		}
		var leader *world.Entity
		if _, isNull := args[1].(flang.NullVal); !isNull {
			if leader, err = rt.verbEntity(args, 1, "ai_follow"); err != nil {
				return nil, err
			}
			if leader == e {
				return nil, fmt.Errorf("ai_follow: @%s cannot follow itself", e.ID)
			}
		}
		rt.markDirty()
		return flang.BoolVal(ai.SetFollow(rt.lvl, e, leader)), nil
	},

	"find_path": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 2 && len(args) != 3 {
			return nil, fmt.Errorf("find_path takes two places — vecs or objects — and optionally the entity whose size to plan for")
		}
		from, ok := rt.aiPlace(args[0])
		if !ok {
			return nil, fmt.Errorf("find_path takes a vec or an object with a position, not a %s", flang.TypeOf(args[0]))
		}
		to, ok := rt.aiPlace(args[1])
		if !ok {
			return nil, fmt.Errorf("find_path takes a vec or an object with a position, not a %s", flang.TypeOf(args[1]))
		}
		var sized *world.Entity
		if len(args) == 3 {
			e, err := rt.verbEntity(args, 2, "find_path")
			if err != nil {
				return nil, err
			}
			sized = e
		}
		if rt.lvl == nil {
			return flang.NullVal{}, nil
		}
		p, ok := ai.FindPath(rt.lvl, sized, from, to)
		if !ok || p.Partial {
			return flang.NullVal{}, nil
		}
		elems := make([]flang.Value, len(p.Points))
		for i, pt := range p.Points {
			elems[i] = vecValue(pt)
		}
		return flang.NewList(elems...), nil
	},

	"can_see": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("can_see takes a looker and what it is looking at: an object or a vec")
		}
		from, err := rt.verbEntity(args, 0, "can_see")
		if err != nil {
			return nil, err
		}
		if rt.lvl == nil {
			return flang.BoolVal(false), nil
		}
		ignore := []string{from.ID}
		var to mgl32.Vec3
		if r, ok := args[1].(flang.RefVal); ok && r.Kind == flang.RefEntity {
			t := rt.host.entityFor(r)
			if t == nil {
				return nil, &scriptMissingError{r.String()}
			}
			to = ai.Eye(rt.lvl, t)
			ignore = append(ignore, t.ID)
		} else if p, ok := rt.positionOf(args[1]); ok {
			to = p
		} else {
			return nil, fmt.Errorf("can_see takes an object or a vec as its second argument, not a %s", flang.TypeOf(args[1]))
		}
		return flang.BoolVal(ai.LineOfSight(rt.lvl, ai.Eye(rt.lvl, from), to, ignore...)), nil
	},
}

// aiPlace reads a place: a vec as it is, an entity at its feet, anything else
// with a position at that position.
func (rt *ScriptRuntime) aiPlace(v flang.Value) (mgl32.Vec3, bool) {
	if r, ok := v.(flang.RefVal); ok && r.Kind == flang.RefEntity && rt.lvl != nil {
		if e := rt.host.entityFor(r); e != nil {
			return ai.Feet(rt.lvl, e), true
		}
	}
	return rt.positionOf(v)
}

// verbVolume resolves argument i to a volume.
func (rt *ScriptRuntime) verbVolume(args []flang.Value, i int, name string) (*world.Volume, error) {
	if i >= len(args) {
		return nil, fmt.Errorf("%s needs a volume", name)
	}
	r, ok := args[i].(flang.RefVal)
	if !ok {
		if _, isNull := args[i].(flang.NullVal); isNull {
			return nil, fmt.Errorf("%s was given null — an `@name` that matches nothing in the level resolves to null, so check the volume's ID", name)
		}
		return nil, fmt.Errorf("%s takes a volume, not a %s", name, flang.TypeOf(args[i]))
	}
	if r.Kind != flang.RefVolume {
		return nil, fmt.Errorf("%s takes a volume; %s is a %s", name, r, r.Kind)
	}
	v := rt.cache.volumeFor(r)
	if v == nil {
		return nil, &scriptMissingError{r.String()}
	}
	return v, nil
}

// objectiveRefusal says why an agent may not take an objective.
func objectiveRefusal(v *world.Volume, e *world.Entity) string {
	switch {
	case v.Objective == nil:
		return fmt.Sprintf("@%s is not an objective", v.ID)
	case !v.Enabled:
		return fmt.Sprintf("volume @%s is disabled", v.ID)
	case !v.Objective.Enabled:
		return fmt.Sprintf("@%s's objective is switched off (a once-only objective switches itself off when done)", v.ID)
	case !v.HasBounds():
		return fmt.Sprintf("@%s has no size", v.ID)
	case v.Objective.AgentTag != "" && !e.HasTag(v.Objective.AgentTag):
		return fmt.Sprintf("@%s is only for agents tagged #%s, and @%s is not", v.ID, v.Objective.AgentTag, e.ID)
	}
	return "it refused"
}

// ---------- properties ----------

// aiEntityProps is the controller's half of an entity's property table.
//
// Reading any of them on an entity with no controller is a dull answer rather
// than an error — `@crate.prop.ai_state` is `:none:` — on the vehicle
// properties' rule. Writing one is reported: a script tuning a controller that
// is not there is not doing what its author thinks.
func aiEntityProps() map[string]propAccess {
	withAI := func(name string, c *refCache, r flang.RefVal) (*world.Entity, error) {
		e := c.entityFor(r)
		if e == nil {
			return nil, &scriptMissingError{r.String()}
		}
		if e.AI == nil {
			return nil, fmt.Errorf("%s: %s has no AI controller", name, r.String())
		}
		return e, nil
	}
	get := func(fn func(c *refCache, e *world.Entity) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			e := c.entityFor(r)
			if e == nil {
				return nil, false
			}
			return fn(c, e), true
		}
	}
	number := func(name, doc string, field func(*world.AISpec) *float32, lo float32) propAccess {
		return propAccess{
			Get: get(func(_ *refCache, e *world.Entity) flang.Value {
				if e.AI == nil {
					return flang.FloatVal(0)
				}
				return flang.FloatVal(float64(*field(e.AI)))
			}),
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				e, err := withAI(name, c, r)
				if err != nil {
					return err
				}
				f, ok := toFloat32(v)
				if !ok {
					return propErr(name, "number", v)
				}
				if f < lo {
					f = lo
				}
				*field(e.AI) = f
				return nil
			},
			Doc: doc,
		}
	}
	names := func(name, doc string, field func(*world.AISpec) *[]string, reset func(*world.AIState)) propAccess {
		return propAccess{
			Get: get(func(_ *refCache, e *world.Entity) flang.Value {
				if e.AI == nil {
					return flang.NewList()
				}
				list := *field(e.AI)
				elems := make([]flang.Value, len(list))
				for i, s := range list {
					elems[i] = flang.StrVal(s)
				}
				return flang.NewList(elems...)
			}),
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				e, err := withAI(name, c, r)
				if err != nil {
					return err
				}
				l, ok := v.(*flang.ListVal)
				if !ok {
					return propErr(name, "list of str", v)
				}
				out := make([]string, 0, len(l.Elems))
				for _, el := range l.Elems {
					switch t := el.(type) {
					case flang.StrVal:
						out = append(out, string(t))
					case flang.RefVal:
						// A ref is its ID: `[@player, @console]` reads as written.
						out = append(out, t.A)
					default:
						return fmt.Errorf("%s takes names — `player`, an ID, or `#tag` — not a %s", name, flang.TypeOf(el))
					}
				}
				*field(e.AI) = out
				if e.AIState != nil && reset != nil {
					reset(e.AIState)
				}
				return nil
			},
			Doc: doc,
		}
	}

	return map[string]propAccess{
		"ai_enabled": {
			Get: get(func(_ *refCache, e *world.Entity) flang.Value { return flang.BoolVal(e.AI != nil && e.AI.Enabled) }),
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				e, err := withAI("ai_enabled", c, r)
				if err != nil {
					return err
				}
				b, ok := toBool(v)
				if !ok {
					return propErr("ai_enabled", "bool", v)
				}
				e.AI.Enabled = b
				return nil
			},
			Doc: "whether the AI controller runs; off stops the agent where it is",
		},
		"ai_state": {
			Get: get(func(_ *refCache, e *world.Entity) flang.Value {
				if e.AI == nil {
					return flang.AtomVal("none")
				}
				return flang.AtomVal(world.AIModeName(ai.Mode(e)))
			}),
			Doc: "read-only: :idle:, :moving:, :wandering:, :travelling:, :performing:, :chasing:, :engaging:, :watching:, :following:, :taking_cover:, :in_cover:, :waiting:, :escorting:, :regrouping:, :disabled:, or :none: without a controller",
		},
		"ai_target": {
			Get: get(func(c *refCache, e *world.Entity) flang.Value {
				if t := ai.Target(c.lvl, e); t != nil {
					return flang.RefVal{Kind: flang.RefEntity, A: t.ID}
				}
				return flang.NullVal{}
			}),
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				e, err := withAI("ai_target", c, r)
				if err != nil {
					return err
				}
				if _, isNull := v.(flang.NullVal); isNull {
					ai.SetTarget(c.lvl, e, nil)
					return nil
				}
				tr, ok := v.(flang.RefVal)
				if !ok || tr.Kind != flang.RefEntity {
					return propErr("ai_target", "entity or null", v)
				}
				t := c.entityFor(tr)
				if t == nil {
					return &scriptMissingError{tr.String()}
				}
				if t == e {
					return fmt.Errorf("ai_target: %s cannot hunt itself", r.String())
				}
				ai.SetTarget(c.lvl, e, t)
				return nil
			},
			Doc: "the entity the agent is hunting, or null; writing one makes it hunt that and nothing else until cleared",
		},
		"ai_objective": {
			Get: get(func(c *refCache, e *world.Entity) flang.Value {
				if v := ai.Objective(c.lvl, e); v != nil {
					return flang.RefVal{Kind: flang.RefVolume, A: v.ID}
				}
				return flang.NullVal{}
			}),
			Doc: "read-only: the objective volume the agent is working, or null; ai_objective() hands it one",
		},
		"ai_destination": {
			Get: get(func(_ *refCache, e *world.Entity) flang.Value {
				if p, ok := ai.Destination(e); ok {
					return vecValue(p)
				}
				return flang.NullVal{}
			}),
			Doc: "read-only: where the agent is walking to, or null when it is standing still",
		},
		"ai_waypoints": {
			Get: get(func(_ *refCache, e *world.Entity) flang.Value {
				if id, _ := ai.Route(e); id != "" {
					return flang.RefVal{Kind: flang.RefWaypoints, A: id}
				}
				return flang.NullVal{}
			}),
			Doc: "read-only: the waypoint animation the agent is walking, or null; ai_follow_waypoints sends it along one",
		},
		"ai_waypoint_index": {
			Get: get(func(_ *refCache, e *world.Entity) flang.Value {
				_, idx := ai.Route(e)
				return flang.IntVal(idx)
			}),
			Doc: "read-only: the point of its path the agent is walking to or waiting at, counting from 0; -1 when it walks none",
		},
		"ai_follow": {
			Get: get(func(c *refCache, e *world.Entity) flang.Value {
				if l := ai.Leader(c.lvl, e); l != nil {
					return flang.RefVal{Kind: flang.RefEntity, A: l.ID}
				}
				return flang.NullVal{}
			}),
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				e, err := withAI("ai_follow", c, r)
				if err != nil {
					return err
				}
				if _, isNull := v.(flang.NullVal); isNull {
					ai.SetFollow(c.lvl, e, nil)
					return nil
				}
				lr, ok := v.(flang.RefVal)
				if !ok || lr.Kind != flang.RefEntity {
					return propErr("ai_follow", "entity or null", v)
				}
				l := c.entityFor(lr)
				if l == nil {
					return &scriptMissingError{lr.String()}
				}
				if l == e {
					return fmt.Errorf("ai_follow: %s cannot follow itself", r.String())
				}
				ai.SetFollow(c.lvl, e, l)
				return nil
			},
			Doc: "the entity the agent keeps up with as a squadmate, or null; null too while that leader is gone or down",
		},
		"ai_in_cover": {
			Get: get(func(_ *refCache, e *world.Entity) flang.Value { return flang.BoolVal(ai.InCover(e)) }),
			Doc: "read-only: whether the agent is hidden at a point of its path that takes cover",
		},
		"ai_waiting": {
			Get: get(func(_ *refCache, e *world.Entity) flang.Value { return flang.BoolVal(ai.Waiting(e)) }),
			Doc: "read-only: whether the agent is waiting at a point of its path; ai_continue sends it on",
		},
		"ai_on_target": {
			Get: get(func(_ *refCache, e *world.Entity) flang.Value {
				if e.AI == nil {
					return flang.AtomVal("none")
				}
				return flang.AtomVal(world.AITargetModeName(e.AI.OnTarget))
			}),
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				e, err := withAI("ai_on_target", c, r)
				if err != nil {
					return err
				}
				s, ok := toStr(v)
				if !ok {
					return propErr("ai_on_target", "atom", v)
				}
				s = strings.Trim(s, ":")
				for i, n := range world.AITargetModeNames {
					if n == s {
						e.AI.OnTarget = world.AITargetMode(i)
						return nil
					}
				}
				return fmt.Errorf("ai_on_target is :chase:, :watch: or :ignore:, not :%s:", s)
			},
			Doc: ":chase:, :watch: or :ignore: — what the agent does about a target",
		},
		"ai_targets": names("ai_targets",
			"what the agent hunts: a list of `player`, entity IDs and `#tag`s",
			func(s *world.AISpec) *[]string { return &s.Targets }, nil),
		"ai_objectives": names("ai_objectives",
			"the objective volumes the agent works through, as IDs and `#tag`s; writing it starts the list over",
			func(s *world.AISpec) *[]string { return &s.Objectives },
			func(st *world.AIState) {
				st.SeqIndex, st.Done, st.ObjectivesFinished = 0, nil, false
				if !st.ObjectiveForced {
					st.Objective, st.Performing = "", false
				}
			}),
		"ai_speed":           number("ai_speed", "walking speed, in units per second", func(s *world.AISpec) *float32 { return &s.Speed }, 0),
		"ai_accel":           number("ai_accel", "how quickly the agent reaches its speed and stops, in units per second squared", func(s *world.AISpec) *float32 { return &s.Accel }, 0),
		"ai_turn_rate":       number("ai_turn_rate", "how fast the agent turns to face its way, in radians per second; 0 turns at once", func(s *world.AISpec) *float32 { return &s.TurnRate }, 0),
		"ai_sight_range":     number("ai_sight_range", "how far the agent sees", func(s *world.AISpec) *float32 { return &s.SightRange }, 0),
		"ai_fov":             number("ai_fov", "how wide the agent sees, in degrees across the whole cone; 360 is all round", func(s *world.AISpec) *float32 { return &s.FOV }, 0),
		"ai_hearing_range":   number("ai_hearing_range", "how close a target is noticed whatever the agent faces and whatever is in between", func(s *world.AISpec) *float32 { return &s.HearingRange }, 0),
		"ai_lose_time":       number("ai_lose_time", "seconds a target may be out of sight before the agent gives up on it", func(s *world.AISpec) *float32 { return &s.LoseTime }, 0),
		"ai_attack_range":    number("ai_attack_range", "how close the agent closes on a target before :target_in_range:", func(s *world.AISpec) *float32 { return &s.AttackRange }, 0),
		"ai_attack_interval": number("ai_attack_interval", "seconds between repeats of :target_in_range: while the target stays close", func(s *world.AISpec) *float32 { return &s.AttackInterval }, 0),
		"ai_follow_distance": number("ai_follow_distance", "how far behind their leader followers gather, and roughly how far apart", func(s *world.AISpec) *float32 { return &s.FollowDistance }, 0),
		"ai_follow_leash":    number("ai_follow_leash", "how far from its leader a fight may take a follower before it regroups; 0 never breaks off", func(s *world.AISpec) *float32 { return &s.FollowLeash }, 0),
		"ai_run_speed":       number("ai_run_speed", "how fast a follower runs to catch up once it has fallen well behind; no faster than ai_speed never runs", func(s *world.AISpec) *float32 { return &s.RunSpeed }, 0),
		"ai_wander_radius":   number("ai_wander_radius", "how far from where it started an idle agent strolls; 0 stands still", func(s *world.AISpec) *float32 { return &s.WanderRadius }, 0),
	}
}

// objectiveVolumeProps is the objective block's half of a volume's table.
func objectiveVolumeProps() map[string]propAccess {
	withObjective := func(name string, c *refCache, r flang.RefVal) (*world.Volume, error) {
		v := c.volumeFor(r)
		if v == nil {
			return nil, &scriptMissingError{r.String()}
		}
		if v.Objective == nil {
			return nil, fmt.Errorf("%s: %s is not an objective", name, r.String())
		}
		return v, nil
	}
	get := func(fn func(c *refCache, v *world.Volume) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			v := c.volumeFor(r)
			if v == nil {
				return nil, false
			}
			return fn(c, v), true
		}
	}
	return map[string]propAccess{
		"objective_enabled": {
			Get: get(func(_ *refCache, v *world.Volume) flang.Value {
				return flang.BoolVal(v.Objective != nil && v.Objective.Enabled)
			}),
			Set: func(c *refCache, r flang.RefVal, val flang.Value) error {
				v, err := withObjective("objective_enabled", c, r)
				if err != nil {
					return err
				}
				b, ok := toBool(val)
				if !ok {
					return propErr("objective_enabled", "bool", val)
				}
				v.Objective.Enabled = b
				return nil
			},
			Doc: "whether agents may take this objective; false for a volume that is not one",
		},
		"objective_action": {
			Get: get(func(_ *refCache, v *world.Volume) flang.Value {
				return flang.StrVal(v.Objective.ActionAtom())
			}),
			Set: func(c *refCache, r flang.RefVal, val flang.Value) error {
				v, err := withObjective("objective_action", c, r)
				if err != nil {
					return err
				}
				s, ok := toStr(val)
				if !ok {
					return propErr("objective_action", "str or atom", val)
				}
				v.Objective.Action = s
				return nil
			},
			Doc: "the atom posted to the agent and the volume when the objective is done",
		},
		"objective_duration": {
			Get: get(func(_ *refCache, v *world.Volume) flang.Value {
				if v.Objective == nil {
					return flang.FloatVal(0)
				}
				return flang.FloatVal(float64(v.Objective.Duration))
			}),
			Set: func(c *refCache, r flang.RefVal, val flang.Value) error {
				v, err := withObjective("objective_duration", c, r)
				if err != nil {
					return err
				}
				f, ok := toFloat32(val)
				if !ok {
					return propErr("objective_duration", "number", val)
				}
				if f < 0 {
					f = 0
				}
				v.Objective.Duration = f
				return nil
			},
			Doc: "seconds an agent stays inside to perform the objective",
		},
		"objective_agents": {
			Get: get(func(c *refCache, v *world.Volume) flang.Value {
				return flang.IntVal(ai.ObjectiveAgents(c.lvl, v))
			}),
			Doc: "read-only: how many agents are working this objective, walking to it included",
		},
	}
}

// mergeProps adds extra's properties to base, which it returns.
func mergeProps(base, extra map[string]propAccess) map[string]propAccess {
	for k, v := range extra {
		base[k] = v
	}
	return base
}
