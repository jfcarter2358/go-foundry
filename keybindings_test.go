package main

import "testing"

// The overlay's whole value is being accurate, so guard the two things that
// silently rot: a section whose rows are malformed, and the two-column split
// dropping or reordering one.

func TestKeybindSectionsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range keybindSections {
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
	left, right := keybindSplitSections(keybindSections)
	if len(left)+len(right) != len(keybindSections) {
		t.Fatalf("split lost sections: %d + %d != %d", len(left), len(right), len(keybindSections))
	}
	if len(left) == 0 || len(right) == 0 {
		t.Fatalf("split left a column empty: %d / %d", len(left), len(right))
	}
	joined := append(append([]KeybindSection{}, left...), right...)
	for i := range joined {
		if joined[i].Title != keybindSections[i].Title {
			t.Fatalf("split reordered sections at %d: got %q want %q",
				i, joined[i].Title, keybindSections[i].Title)
		}
	}
}

func TestKeybindActiveSectionsFollowMode(t *testing.T) {
	saved := Editor
	savedWin := editor2DWindow
	defer func() { Editor, editor2DWindow = saved, savedWin }()

	activeTitles := func() map[string]bool {
		out := map[string]bool{}
		for _, s := range keybindSections {
			if s.Active == nil || s.Active() {
				out[s.Title] = true
			}
		}
		return out
	}

	Editor = EditorState{}
	Editor.Active = false
	if got := activeTitles(); !got["Gameplay"] || got["Editor — 3D view"] {
		t.Errorf("editor closed: expected gameplay only, got %v", got)
	}

	// The sector map lives in its own window, and Editor.Mode never says
	// Mode2D outside that window's draw brackets — so the map's sections key
	// off the window being open, not off the mode.
	Editor = EditorState{}
	Editor.Active = true
	editor2DWindow = &UIWindow{}
	got := activeTitles()
	if !got["Editor — 2D sector map (its own window)"] || !got["Editor — any mode"] {
		t.Errorf("2D window open: expected the 2D and shared editor sections, got %v", got)
	}
	if got["Gameplay"] || got["Editor — biome brush (2D)"] {
		t.Errorf("2D window open: gameplay/biome sections should be inactive, got %v", got)
	}

	Editor.BiomeLayerActive = true
	if !activeTitles()["Editor — biome brush (2D)"] {
		t.Error("biome layer active: expected the biome brush section to light up")
	}

	// Closing the window takes the map's sections with it.
	editor2DWindow = nil
	if activeTitles()["Editor — 2D sector map (its own window)"] {
		t.Error("2D window closed: the sector map section should be inactive")
	}

	// Global sections have no predicate and apply everywhere.
	for _, mode := range []EditorMode{Mode3D, Mode2D, ModeSurface2D} {
		Editor = EditorState{}
		Editor.Active = true
		Editor.Mode = mode
		if !activeTitles()["Global"] {
			t.Errorf("mode %v: Global section should always be active", mode)
		}
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
		if got := keybindHelpTarget(c.current, c.focused, mainID, c.valid); got != c.want {
			t.Errorf("%s: keybindHelpTarget(%d, %d) = %d, want %d", c.name, c.current, c.focused, got, c.want)
		}
	}
}
