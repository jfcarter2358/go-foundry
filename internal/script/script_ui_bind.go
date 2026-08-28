package script

// script_ui_bind.go — evaluating a widget's binding against the level.
//
// A HUD is a *view* of state, so the relationship between a bar and the health
// it shows is expressed once, in the document, and polled. Under a push model
// every place that changes health — the collision resolver, a damage script, a
// pickup, the restore that runs when play ends — would have to remember to
// update the bar, and the one that forgot would produce a HUD that is wrong in
// a way no test catches.
//
// The expression grammar is deliberately tiny:
//
//	@player.attr.health      an attribute on an object
//	@door_1.prop.open        an engine property
//	@level.prop.load_progress
//	42        "Ready"        a literal, for a placeholder that never changes
//
// No arithmetic, no calls, no tag broadcasts. A binding that needed a
// computation is a script's job — it can write an attribute and bind to that —
// and keeping this to a lookup is what makes it safe to run for every bound
// widget every frame.

import (
	"strconv"
	"strings"

	"foundry/internal/flang"
	"foundry/internal/ui"
)

// InstallUIBindings points a screen stack's resolver at the running level.
//
// Called by ScriptRuntimeLoad and cleared by ScriptRuntimeReset, so nothing
// evaluates a binding against a level that has been put back after play.
func InstallUIBindings(st *ui.Stack) {
	if st == nil {
		return
	}
	st.Resolve = resolveUIBinding
}

// ResolveUIBindingForEditor is the resolver, exported for the UI editor's Live
// preview mode. It reads the same level a running HUD would, and declines the
// same way when there is nothing to read.
func ResolveUIBindingForEditor(expr string) (ui.Value, bool) { return resolveUIBinding(expr) }

// ClearUIBindings puts a stack back to showing its authored values.
func ClearUIBindings(st *ui.Stack) {
	if st != nil {
		st.Resolve = nil
	}
}

// resolveUIBinding is the resolver itself.
func resolveUIBinding(expr string) (ui.Value, bool) {
	b, ok := parseBinding(expr)
	if !ok {
		return ui.Value{}, false
	}
	if b.literal != nil {
		return *b.literal, true
	}

	rt := Scripts
	if rt == nil || rt.host == nil {
		return ui.Value{}, false
	}
	r, ok := rt.host.Resolve(b.id)
	if !ok {
		return ui.Value{}, false
	}

	var v flang.Value
	if b.attr {
		v, ok = rt.host.Attr(r, b.name)
	} else {
		v, ok = rt.host.Prop(r, b.name)
	}
	if !ok {
		return ui.Value{}, false
	}
	return flangToUIValue(v)
}

// binding is a parsed expression.
type binding struct {
	id   string
	name string
	attr bool // attr rather than prop
	// literal is set for an expression that is just a value, in which case
	// nothing above it is used.
	literal *ui.Value
}

// bindingCache memoises the parse. A HUD re-evaluates every binding every
// frame, and re-parsing the same dozen strings sixty times a second is work
// nobody asked for.
var bindingCache = map[string]binding{}

func parseBinding(expr string) (binding, bool) {
	if b, ok := bindingCache[expr]; ok {
		return b, b.id != "" || b.literal != nil
	}
	b, ok := parseBindingUncached(expr)
	bindingCache[expr] = b
	return b, ok
}

func parseBindingUncached(expr string) (binding, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return binding{}, false
	}

	if !strings.HasPrefix(expr, "@") {
		// A literal. Numbers first, so "42" is a number rather than the string
		// "42" — a bar bound to a literal has to fill.
		if f, err := strconv.ParseFloat(expr, 64); err == nil {
			v := ui.NumValue(f)
			return binding{literal: &v}, true
		}
		if bl, err := strconv.ParseBool(expr); err == nil {
			v := ui.BoolValue(bl)
			return binding{literal: &v}, true
		}
		v := ui.StrValue(strings.Trim(expr, `"`))
		return binding{literal: &v}, true
	}

	parts := strings.Split(expr[1:], ".")
	if len(parts) != 3 {
		return binding{}, false
	}
	id, mode, name := parts[0], parts[1], parts[2]
	if id == "" || name == "" {
		return binding{}, false
	}
	switch mode {
	case "attr":
		return binding{id: id, name: name, attr: true}, true
	case "prop":
		return binding{id: id, name: name}, true
	}
	return binding{}, false
}

// flangToUIValue narrows a script value to the four a widget can show. A list
// or a dict has no rendering a label could give it, so it declines rather than
// printing something that looks like a bug.
func flangToUIValue(v flang.Value) (ui.Value, bool) {
	switch t := v.(type) {
	case flang.IntVal:
		return ui.NumValue(float64(t)), true
	case flang.FloatVal:
		return ui.NumValue(float64(t)), true
	case flang.BoolVal:
		return ui.BoolValue(bool(t)), true
	case flang.StrVal:
		return ui.StrValue(string(t)), true
	case flang.AtomVal:
		return ui.StrValue(string(t)), true
	case flang.NullVal:
		return ui.Value{}, false
	}
	return ui.Value{}, false
}

// InvalidateBindingCache drops the parsed expressions, for a test or a reload.
func InvalidateBindingCache() { bindingCache = map[string]binding{} }
