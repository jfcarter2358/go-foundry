package uiwin

import "testing"

// keybind_help_test.go — the overlay has to be raised every time it arrives in
// a window, not only the first time imgui ever saw it.
//
// The bug this pins: F1 in the script editor worked once and then opened the
// reference *behind* the editor's full-window root, because NoFocusOnAppearing
// stops imgui raising a window that reappears, and only a never-before-seen
// window starts at the front of the stack.

func TestKeybindHelpArrivesOnEveryOpen(t *testing.T) {
	const scriptWin, mainWin = uint32(3), uint32(1)

	defer func() {
		KeybindHelpVisible = false
		keybindHelpDrawnIn = 0
	}()
	KeybindHelpVisible = false
	keybindHelpDrawnIn = 0

	KeybindHelpSetVisible(true)
	if !keybindHelpArriving(scriptWin) {
		t.Fatal("first open did not ask to be raised")
	}
	if keybindHelpArriving(scriptWin) {
		t.Fatal("a later frame in the same window asked to be raised again; " +
			"the overlay would steal focus every frame")
	}

	// Close and reopen in the same window: this is the case that regressed.
	KeybindHelpSetVisible(false)
	KeybindHelpSetVisible(true)
	if !keybindHelpArriving(scriptWin) {
		t.Error("reopening in the same window did not ask to be raised, so it " +
			"would appear behind whatever was clicked while it was closed")
	}

	// Carrying an open reference to another window is another arrival: each
	// window has its own imgui context and its own window stack.
	if !keybindHelpArriving(mainWin) {
		t.Error("crossing to another window did not ask to be raised")
	}
	if !keybindHelpArriving(scriptWin) {
		t.Error("crossing back did not ask to be raised")
	}
}
