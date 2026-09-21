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
	"fmt"

	"foundry/internal/flang"
	"foundry/internal/hooks"
	"foundry/internal/platform"
	"foundry/internal/prefs"
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

	// The game's own settings (internal/prefs): whatever a settings screen
	// offers that is not the mouse, a key or the volume. setting's default is
	// also its type — a stored value of another kind (an old file, a renamed
	// option) is ignored rather than handed to a script that cannot use it.
	"setting": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 2 {
			return nil, verbArgErr("setting", "a name and a default")
		}
		name, ok := verbStrAt(args, 0)
		if !ok {
			return nil, verbArgErr("setting", "a setting name as its first argument")
		}
		def := args[1]
		v, ok := prefs.Get(name)
		if !ok {
			return def, nil
		}
		switch d := def.(type) {
		case flang.StrVal:
			if s, ok := v.(string); ok {
				return flang.StrVal(s), nil
			}
		case flang.BoolVal:
			if b, ok := v.(bool); ok {
				return flang.BoolVal(b), nil
			}
		case flang.IntVal:
			if f, ok := v.(float64); ok {
				return flang.IntVal(int64(f)), nil
			}
		case flang.FloatVal:
			if f, ok := v.(float64); ok {
				return flang.FloatVal(f), nil
			}
		default:
			return nil, fmt.Errorf("setting's default is a str, int, float or bool, not a %s", flang.TypeOf(d))
		}
		rt.warnAt(pos, "setting(%q) is saved as a %T, not the kind its default is; using the default", name, v)
		return def, nil
	},

	"set_setting": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 2 {
			return nil, verbArgErr("set_setting", "a name and a value")
		}
		name, ok := verbStrAt(args, 0)
		if !ok {
			return nil, verbArgErr("set_setting", "a setting name as its first argument")
		}
		var v any
		switch x := args[1].(type) {
		case flang.StrVal:
			v = string(x)
		case flang.BoolVal:
			v = bool(x)
		case flang.IntVal:
			v = float64(x)
		case flang.FloatVal:
			v = float64(x)
		default:
			return nil, fmt.Errorf("set_setting takes a str, int, float or bool, not a %s", flang.TypeOf(args[1]))
		}
		if err := prefs.Set(name, v); err != nil {
			return nil, err
		}
		return flang.NullVal{}, nil
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
