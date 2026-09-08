// Development-build only, alongside the table it checks (keybindings.go).

//go:build !release

package app

import (
	"foundry/internal/editor"
	"foundry/internal/uiwin"
	"testing"
)

// The overlay's whole value is being accurate, so guard the two things that
// silently rot: a section whose rows are malformed, and the two-column split
// dropping or reordering one.

func TestKeybindSectionsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range uiwin.KeybindSections {
		if s.Title == "" {
			t.Error("section with empty title")
		}
		if seen[s.Title] {
			t.Errorf("duplicate section title %q", s.Title)
		}
		seen[s.Title] = true
		if len(s.Binds) == 0 {
			t.Errorf("section %q has no bindings", s.Title)
		}
		for i, b := range s.Binds {
			if b.Keys == "" || b.Desc == "" {
				t.Errorf("section %q binding %d is incomplete: %+v", s.Title, i, b)
			}
		}
	}
}

func TestKeybindSplitSectionsCoversEverything(t *testing.T) {
	left, right := uiwin.KeybindSplitSections(uiwin.KeybindSections)
	if len(left)+len(right) != len(uiwin.KeybindSections) {
		t.Fatalf("split lost sections: %d + %d != %d", len(left), len(right), len(uiwin.KeybindSections))
	}
	if len(left) == 0 || len(right) == 0 {
		t.Fatalf("split left a column empty: %d / %d", len(left), len(right))
	}
	joined := append(append([]uiwin.KeybindSection{}, left...), right...)
	for i := range joined {
		if joined[i].Title != uiwin.KeybindSections[i].Title {
			t.Fatalf("split reordered sections at %d: got %q want %q",
				i, joined[i].Title, uiwin.KeybindSections[i].Title)
		}
	}
}

func TestKeybindActiveSectionsFollowMode(t *testing.T) {
	saved := editor.Editor
	defer func() { editor.Editor = saved }()

	activeTitles := func() map[string]bool {
		out := map[string]bool{}
		for _, s := range uiwin.KeybindSections {
			if s.Active == nil || s.Active() {
				out[s.Title] = true
			}
		}
		return out
	}

	editor.Editor = editor.EditorState{}
	editor.Editor.Active = false
	if got := activeTitles(); !got["Gameplay"] || got["Editor — 3D view"] {
		t.Errorf("editor closed: expected gameplay only, got %v", got)
	}

	editor.Editor = editor.EditorState{}
	editor.Editor.Active = true
	editor.Editor.Mode = editor.Mode3D
	got := activeTitles()
	for _, want := range []string{"Editor — 3D view", "Editor — any mode", "Editor — shaping a brush"} {
		if !got[want] {
			t.Errorf("3D view: expected the %q section, got %v", want, got)
		}
	}
	if got["Gameplay"] {
		t.Errorf("3D view: the gameplay section should be inactive, got %v", got)
	}

	// Global sections have no predicate and apply everywhere.
	editor.Editor = editor.EditorState{}
	editor.Editor.Active = true
	editor.Editor.Mode = editor.Mode3D
	if !activeTitles()["Global"] {
		t.Error("the Global section should always be active")
	}
}

// TestKeybindHelpTarget covers the window the overlay follows: the focused
// one, stickiness while the app is unfocused, and the fallback when the window
// it was in has closed.
func TestKeybindHelpTarget(t *testing.T) {
	const mainID, texID, modelID = uint32(1), uint32(2), uint32(3)
	open := func(ids ...uint32) func(uint32) bool {
		return func(id uint32) bool {
			if id == mainID {
				return true
			}
			for _, o := range ids {
				if id == o {
					return true
				}
			}
			return false
		}
	}

	cases := []struct {
		name             string
		current, focused uint32
		valid            func(uint32) bool
		want             uint32
	}{
		{"first frame follows focus", 0, texID, open(texID), texID},
		{"first frame, nothing focused", 0, 0, open(), mainID},
		{"moves to the newly focused window", mainID, modelID, open(modelID), modelID},
		{"moves back to the main window", texID, mainID, open(texID), mainID},
		{"app unfocused keeps the current window", texID, 0, open(texID), texID},
		{"current window closed falls back to main", texID, 0, open(), mainID},
		{"unknown focused window is ignored", texID, 99, open(texID), texID},
	}
	for _, c := range cases {
		if got := uiwin.KeybindHelpTarget(c.current, c.focused, mainID, c.valid); got != c.want {
			t.Errorf("%s: uiwin.KeybindHelpTarget(%d, %d) = %d, want %d", c.name, c.current, c.focused, got, c.want)
		}
	}
}
