package editor

// editor_ui_sample.go — what the preview's bindings read.
//
// A bound widget in the editor has nothing real to read: there is no player and
// usually no play session. Three answers, cycled from the toolbar, because each
// is right for a different question:
//
//   - Sample invents a plausible, *moving* value. The only way to see whether a
//     fill direction, a nine-slice or a format string actually work is to watch
//     one change; a bar frozen at its authored value looks correct whichever way
//     round it fills.
//   - Literal turns bindings off entirely, so every widget shows exactly what
//     the document says. This is the one to check an authored placeholder in.
//   - Live reads the open play session, when there is one.

import (
	"math"

	"foundry/internal/platform"
	"foundry/internal/script"
	"foundry/internal/ui"
)

type uiSampleMode int

const (
	uiSampleFake uiSampleMode = iota
	uiSampleLiteral
	uiSampleLive
)

var uiSampleModeNames = []string{"Sample", "Literal", "Live"}

var uiSampleData uiSampleMode

// uiSampleClock advances every frame so sampled values move.
var uiSampleClock float32

// uiSampleValue invents an answer from the shape of an expression.
//
// The guesses are deliberate rather than random: a binding whose name mentions
// health comes back part-full and slowly draining, one that mentions a name
// comes back as a name. An author checking a health bar should see a health bar,
// not the number 0.5 in every widget on the screen.
func uiSampleValue(expr string) (ui.Value, bool) {
	uiSampleClock += platform.DeltaTime
	sweep := 0.5 + 0.5*float32(math.Sin(float64(uiSampleClock)*0.6))

	switch {
	case containsAny(expr, "health", "hp"):
		if containsAny(expr, "max") {
			return ui.NumValue(100), true
		}
		return ui.NumValue(float64(20 + 60*sweep)), true
	case containsAny(expr, "progress", "load"):
		return ui.NumValue(float64(sweep)), true
	case containsAny(expr, "ammo", "count", "score", "coins"):
		return ui.NumValue(float64(int(sweep * 250))), true
	case containsAny(expr, "name", "title", "label"):
		return ui.StrValue("Sample"), true
	case containsAny(expr, "stage", "status", "state"):
		return ui.StrValue("Building geometry"), true
	case containsAny(expr, "enabled", "on", "active", "visible"):
		return ui.BoolValue(sweep > 0.5), true
	}
	return ui.NumValue(float64(sweep)), true
}

// uiLiveBindings reads the running play session.
//
// A variable rather than a direct call so a test can point it somewhere else,
// and so "there is no live data" is one answer rather than several: a level
// with no play session open and a build with no script runtime look the same
// from here, and should.
var uiLiveBindings = script.ResolveUIBindingForEditor

// uiLiveResolver reads the level the script runtime currently has, and declines
// when the expression names nothing in it — which puts that widget back on its
// authored placeholder rather than showing a zero that looks like a real
// reading.
//
// Deliberately not gated on a play session being open. With the editor's own
// level loaded, a binding to @player.attr.health shows the authored value,
// which is exactly what an author checking a HUD wants to see.
func uiLiveResolver(expr string) (ui.Value, bool) {
	if uiLiveBindings == nil {
		return ui.Value{}, false
	}
	return uiLiveBindings(expr)
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

// indexOf is strings.Index over an ASCII-lowercased haystack, so a binding
// spelled "Health" matches as readily as "health".
func indexOf(s, sub string) int {
	if len(sub) == 0 || len(sub) > len(s) {
		return -1
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if lowerASCII(s[i+j]) != lowerASCII(sub[j]) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 32
	}
	return b
}
