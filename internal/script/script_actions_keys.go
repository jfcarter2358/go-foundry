package script

// script_actions_keys.go — asking what the keyboard and mouse buttons are doing.
//
// The fixed input events (`:interact:`, `:primary_fire:`, `:reload:`…) cover
// the actions the engine's own player uses. These are for everything else: a
// key a game gives its own meaning, a charge-up that needs how long a button
// has been held, a debug toggle on a key no action owns.
//
// Two ways to name an input, as two sets of verbs. key_* takes a key or button
// by name — "W", "Space", "Left Shift", "Mouse Left", as binding_name spells
// them, in any case — and means that physical key whatever the player has
// rebound. action_* takes an action — "jump", "interact" — and means whatever
// the player has bound to it, which is what a game should prefer for anything a
// player might want on another key.
//
// Pressed and released are measured between script ticks, so a handler for
// `:tick:` sees each press exactly once. The state is kept by
// platform/key_state.go; this file only reads it, and ScriptEventsTick is what
// moves it on and posts `:key_down:` and `:key_up:`.

import (
	"foundry/internal/flang"
	"foundry/internal/platform"
	"foundry/internal/world"
)

var keyVerbs = map[string]scriptVerb{
	"key_down":      keyQuery("key_down", func(in platform.Input) flang.Value { return flang.BoolVal(platform.KeyDown(in)) }),
	"key_pressed":   keyQuery("key_pressed", func(in platform.Input) flang.Value { return flang.BoolVal(platform.KeyPressed(in)) }),
	"key_released":  keyQuery("key_released", func(in platform.Input) flang.Value { return flang.BoolVal(platform.KeyReleased(in)) }),
	"key_held_time": keyQuery("key_held_time", func(in platform.Input) flang.Value { return flang.FloatVal(platform.KeyHeldTime(in)) }),

	"keys_down": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 0 {
			return nil, verbArgErr("keys_down", "no arguments")
		}
		down := platform.KeysDown()
		out := make([]flang.Value, len(down))
		for i, in := range down {
			out[i] = flang.StrVal(in.Name())
		}
		return flang.NewList(out...), nil
	},

	"action_down":      actionQuery("action_down", flang.BoolVal(false), func(a platform.Action) flang.Value { return flang.BoolVal(platform.ActionDown(a)) }),
	"action_pressed":   actionQuery("action_pressed", flang.BoolVal(false), func(a platform.Action) flang.Value { return flang.BoolVal(platform.ActionPressed(a)) }),
	"action_released":  actionQuery("action_released", flang.BoolVal(false), func(a platform.Action) flang.Value { return flang.BoolVal(platform.ActionReleased(a)) }),
	"action_held_time": actionQuery("action_held_time", flang.FloatVal(0), func(a platform.Action) flang.Value { return flang.FloatVal(platform.ActionHeldTime(a)) }),
}

// keyQuery builds a verb that takes one key name. A name that is no key is a
// warning and the answer for a key that is up — false, or 0 — rather than an
// error, the way an unknown action is for binding_name.
func keyQuery(verb string, answer func(platform.Input) flang.Value) scriptVerb {
	return func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 1 {
			return nil, verbArgErr(verb, "a key name, such as \"Space\" or \"Mouse Left\"")
		}
		name, ok := verbStrAt(args, 0)
		if !ok {
			return nil, verbArgErr(verb, "a key name as a str")
		}
		in, ok := platform.ParseInput(name)
		if !ok || !in.Bound() {
			rt.warnAt(pos, "%s(%q) matched nothing: there is no key or mouse button by that name", verb, name)
			return answer(platform.Input{}), nil
		}
		return answer(in), nil
	}
}

// actionQuery is keyQuery for an action's name.
// unknown is the answer for a name that is no action.
func actionQuery(verb string, unknown flang.Value, answer func(platform.Action) flang.Value) scriptVerb {
	return func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 1 {
			return nil, verbArgErr(verb, "an action name, such as \"jump\"")
		}
		name, ok := verbStrAt(args, 0)
		if !ok {
			return nil, verbArgErr(verb, "an action name as a str")
		}
		a, ok := platform.ActionByName(name)
		if !ok {
			rt.warnAt(pos, "%s(%q) matched nothing: there is no action by that name", verb, name)
			return unknown, nil
		}
		return answer(a), nil
	}
}

// postKeyEvents moves the key state on one tick and posts its edges as
// `:key_down:` and `:key_up:`, in the order they happened, to the level's
// scripts and the player's — the same listeners the pointer events have.
// Under a script's pause the state still moves, so a menu can ask, but the
// events are the level's and are dropped with the rest of its input.
func postKeyEvents(rt *ScriptRuntime, lvl *world.Level, dt float32, paused bool) {
	edges := platform.AdvanceKeys(float64(dt))
	if paused {
		return
	}
	for _, e := range edges {
		name := flang.StrVal(e.In.Name())
		if e.Down {
			postPointer(rt, lvl, "key_down", flang.RefVal{}, []flang.Value{name})
		} else {
			postPointer(rt, lvl, "key_up", flang.RefVal{}, []flang.Value{name, flang.FloatVal(e.Held)})
		}
	}
}
