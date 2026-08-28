package script

// script_actions_ui.go — the verbs for putting screens up and finding widgets
// on them.
//
// Five, and no more. Everything else a script wants to say to the UI is a
// property, which is the shape spriteProps and emitterProps deliberately chose:
// a verb is for something that *happens*, and showing a screen, hiding one and
// moving focus are the only three of those. Setting a label's text is a fact
// about the label, not an event.
//
// Every failure here is one a script may legitimately ignore, so each returns
// false or null *and* warns with the line and a sentence naming the fix —
// the convention script_actions.go's header sets. A menu that silently does not
// open is the worst possible outcome.

import (
	"fmt"

	"foundry/internal/flang"
	"foundry/internal/hooks"
	"foundry/internal/ui"
)

// verbArgErr is the error for a verb called with the wrong shape of arguments.
// A real error rather than a warning, because unlike a lookup that matched
// nothing this is a mistake in the script itself.
func verbArgErr(verb, want string) error {
	return fmt.Errorf("%s needs %s", verb, want)
}

var uiVerbs = map[string]scriptVerb{
	"show_screen": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		name, err := verbStr(args, 0, "show_screen")
		if err != nil {
			return nil, err
		}
		if uiStack == nil {
			rt.warnAt(pos, "show_screen(%q) did nothing: there is no screen stack, which is the case in the editor", name)
			return flang.NullVal{}, nil
		}
		s, err := uiStack.Show(name)
		if err != nil {
			rt.warnAt(pos, "show_screen(%q) did nothing: %v. Check that resources/ui/%s.fui exists and loads", name, err, name)
			return flang.NullVal{}, nil
		}
		return flang.RefVal{Kind: flang.RefScreen, A: s.ID}, nil
	},

	"hide_screen": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		name, ok := uiScreenArg(args, 0)
		if !ok {
			return nil, verbArgErr("hide_screen", "a screen ref or a name")
		}
		if uiStack == nil || !uiStack.Hide(name) {
			rt.warnAt(pos, "hide_screen(%q) did nothing: that screen is not up", name)
			return flang.BoolVal(false), nil
		}
		return flang.BoolVal(true), nil
	},

	"screen": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		name, err := verbStr(args, 0, "screen")
		if err != nil {
			return nil, err
		}
		if uiStack == nil || uiStack.Find(name) == nil {
			rt.warnAt(pos, "screen(%q) matched nothing: that screen is not up. show_screen puts it up", name)
			return flang.NullVal{}, nil
		}
		return flang.RefVal{Kind: flang.RefScreen, A: name}, nil
	},

	"widget": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 2 {
			return nil, verbArgErr("widget", "a screen and an id")
		}
		screen, ok := uiScreenArg(args, 0)
		if !ok {
			return nil, verbArgErr("widget", "a screen ref or a name as its first argument")
		}
		id, err := verbStr(args, 1, "widget")
		if err != nil {
			return nil, err
		}
		if uiStack == nil {
			rt.warnAt(pos, "widget(%q, %q) matched nothing: there is no screen stack", screen, id)
			return flang.NullVal{}, nil
		}
		s := uiStack.Find(screen)
		if s == nil {
			rt.warnAt(pos, "widget(%q, %q) matched nothing: screen %q is not up", screen, id, screen)
			return flang.NullVal{}, nil
		}
		if s.Find(id) == nil {
			rt.warnAt(pos, "widget(%q, %q) matched nothing: screen %q has no widget called %q", screen, id, screen, id)
			return flang.NullVal{}, nil
		}
		return flang.RefVal{Kind: flang.RefWidget, A: screen, B: id}, nil
	},

	// load_level is here rather than in a file of its own because it exists for
	// the loading screen: it is the only thing that starts a load the screen
	// can cover, and the two would be meaningless apart.
	"load_level": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		name, err := verbStr(args, 0, "load_level")
		if err != nil {
			return nil, err
		}
		// A request rather than a call. Loading tears down the runtime that is
		// running this very script, so the frame loop does it between frames —
		// the same reason a sector rebuild waits for DrainScriptWork.
		hooks.RequestLevelLoad(name)
		return flang.BoolVal(true), nil
	},

	"focus": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 1 {
			return nil, verbArgErr("focus", "one widget")
		}
		r, ok := args[0].(flang.RefVal)
		if !ok || r.Kind != flang.RefWidget {
			return nil, verbArgErr("focus", "a widget ref")
		}
		if uiStack == nil || !uiStack.Focus(r.A, r.B) {
			rt.warnAt(pos, "focus(%s) did nothing: that widget is hidden, disabled or gone", r.String())
			return flang.BoolVal(false), nil
		}
		return flang.BoolVal(true), nil
	},
}

// uiScreenArg reads an argument that may be a screen ref or a bare name, which
// is the difference between `hide_screen(@pause)` and `hide_screen("pause")`.
// Both read naturally and there is no reason to insist on one.
func uiScreenArg(args []flang.Value, i int) (string, bool) {
	if i >= len(args) {
		return "", false
	}
	switch v := args[i].(type) {
	case flang.RefVal:
		if v.Kind == flang.RefScreen || v.Kind == flang.RefWidget {
			return v.A, true
		}
	case flang.StrVal:
		return string(v), true
	}
	return "", false
}

// uiWidgetRef is the ref a UI event is delivered to, and the screen's when the
// widget itself carries no script — one level of bubbling.
//
// One level, not the whole ancestor chain. A widget's owner is its screen; a
// full routing chain through every intermediate panel is a feature nobody has
// asked for, and it would make where a handler runs depend on the layout.
func uiWidgetRef(screen, widget string) flang.RefVal {
	if widget == "" {
		return flang.RefVal{Kind: flang.RefScreen, A: screen}
	}
	return flang.RefVal{Kind: flang.RefWidget, A: screen, B: widget}
}

// InstallUIStack points the runtime's ref resolution at a stack. Used by tests;
// the engine uses the game's, which is the default.
func InstallUIStack(st *ui.Stack) { uiStack = st }
