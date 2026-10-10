package script

// script_actions_pad.go — what only a game controller has: travel and rumble.
//
// Its buttons and stick directions are inputs like keys, so key_down("Pad A")
// and action_down("jump") already answer for a pad (script_actions_keys.go).
// These are for the rest: how FAR an action or a stick is pushed, and shaking
// the pad. Rumble goes to the pad the player last touched, scaled by their
// @level.prop.pad_vibration, and does nothing with no pad plugged in — so a
// script calls it for every shot without asking first.

import (
	"strings"

	"foundry/internal/flang"
	"foundry/internal/platform"
)

// padAxes are pad_axis's names: sticks from -1 to 1, right and up positive,
// and triggers from 0 to 1.
var padAxes = map[string]func() float32{
	"left_x":        func() float32 { return platform.PadValue(platform.PadLSRight) - platform.PadValue(platform.PadLSLeft) },
	"left_y":        func() float32 { return platform.PadValue(platform.PadLSUp) - platform.PadValue(platform.PadLSDown) },
	"right_x":       func() float32 { return platform.PadValue(platform.PadRSRight) - platform.PadValue(platform.PadRSLeft) },
	"right_y":       func() float32 { return platform.PadValue(platform.PadRSUp) - platform.PadValue(platform.PadRSDown) },
	"left_trigger":  func() float32 { return platform.PadValue(platform.PadLT) },
	"right_trigger": func() float32 { return platform.PadValue(platform.PadRT) },
}

var padVerbs = map[string]scriptVerb{
	"action_value": actionQuery("action_value", flang.FloatVal(0), func(a platform.Action) flang.Value {
		return flang.FloatVal(platform.ActionValue(a, platform.KeyStates))
	}),

	"pad_axis": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 1 {
			return nil, verbArgErr("pad_axis", "an axis name, such as \"left_x\" or \"right_trigger\"")
		}
		name, ok := verbStrAt(args, 0)
		if !ok {
			return nil, verbArgErr("pad_axis", "an axis name as a str")
		}
		f, ok := padAxes[strings.ToLower(name)]
		if !ok {
			rt.warnAt(pos, "pad_axis(%q) matched nothing: the axes are left_x, left_y, right_x, right_y, left_trigger and right_trigger", name)
			return flang.FloatVal(0), nil
		}
		return flang.FloatVal(f()), nil
	},

	"rumble": rumbleVerb("rumble", "a heavy and a light motor's strength (0–1) and seconds", platform.Rumble),

	"rumble_triggers": rumbleVerb("rumble_triggers", "the left and right trigger's strength (0–1) and seconds", platform.RumbleTriggers),
}

// rumbleVerb builds a verb taking two strengths and a duration.
func rumbleVerb(verb, want string, shake func(a, b, seconds float32)) scriptVerb {
	return func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 3 {
			return nil, verbArgErr(verb, want)
		}
		var v [3]float32
		for i := range v {
			n, ok := verbNum(args, i)
			if !ok {
				return nil, verbArgErr(verb, want)
			}
			v[i] = float32(n)
		}
		shake(v[0], v[1], v[2])
		return flang.NullVal{}, nil
	}
}
