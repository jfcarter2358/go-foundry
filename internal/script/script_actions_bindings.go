package script

// script_actions_bindings.go — the verbs a settings screen rebinds keys with.
//
// The layout itself is platform.Bindings, which gameplay input reads and the
// app saves to controls.yaml. Binding is a verb rather than a property because
// it *happens*: bind_key starts listening, and the player's next press — which
// arrives between frames, ahead of the UI (app/input.go's
// captureBindingEvent) — finishes it. The screen hears back through `:bound:`.
//
// Slots are 1 and 2, the way weapon slots count from 1.

import (
	"foundry/internal/flang"
	"foundry/internal/hooks"
	"foundry/internal/platform"
)

var bindingVerbs = map[string]scriptVerb{
	"bind_key": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		a, slot, err := bindingArgs(rt, args, pos, "bind_key")
		if err != nil || slot < 0 {
			return flang.BoolVal(false), err
		}
		// The event goes back to whichever screen or widget asked, routed the
		// way every UI event is. Anything else has nowhere to hear it.
		var screen, widget string
		switch rt.self.Kind {
		case flang.RefScreen:
			screen = rt.self.A
		case flang.RefWidget:
			screen, widget = rt.self.A, rt.self.B
		default:
			rt.warnAt(pos, "bind_key did nothing: it listens on behalf of a screen, so call it from a screen's or a widget's script")
			return flang.BoolVal(false), nil
		}
		name := platform.Actions[a].Name
		platform.BeginBindingCapture(a, slot, func() {
			hooks.RecordUIEvent(hooks.UIEvent{Atom: "bound", Screen: screen, Widget: widget,
				Str: name, Num: float64(slot + 1)})
		})
		return flang.BoolVal(true), nil
	},

	"binding_name": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		a, slot, err := bindingArgs(rt, args, pos, "binding_name")
		if err != nil || slot < 0 {
			return flang.StrVal(""), err
		}
		return flang.StrVal(platform.Bindings[a][slot].Name()), nil
	},

	"reset_bindings": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 0 {
			return nil, verbArgErr("reset_bindings", "no arguments")
		}
		platform.EndBindingCapture()
		platform.Bindings = platform.DefaultBindings
		platform.ControlSettingsChanged = true
		return flang.NullVal{}, nil
	},
}

// bindingArgs reads (action, slot). An unknown action or a slot out of range is
// a warning and a slot of -1, since a settings screen that names an action a
// newer engine renamed should grey out one row, not stop.
func bindingArgs(rt *ScriptRuntime, args []flang.Value, pos flang.Pos, verb string) (platform.Action, int, error) {
	if len(args) != 2 {
		return 0, -1, verbArgErr(verb, "an action name and a slot (1 or 2)")
	}
	name, ok := verbStrAt(args, 0)
	if !ok {
		return 0, -1, verbArgErr(verb, "an action name as its first argument")
	}
	n, ok := numericValue(args[1])
	if !ok {
		return 0, -1, verbArgErr(verb, "a slot number (1 or 2) as its second argument")
	}
	a, ok := platform.ActionByName(name)
	if !ok {
		rt.warnAt(pos, "%s(%q) matched nothing: there is no action by that name", verb, name)
		return 0, -1, nil
	}
	slot := int(n) - 1
	if slot < 0 || slot >= platform.BindingSlots {
		rt.warnAt(pos, "%s(%q, %v): an action has slots 1 to %d", verb, name, n, platform.BindingSlots)
		return 0, -1, nil
	}
	return a, slot, nil
}
