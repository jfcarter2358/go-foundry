package app

// ui_input_test.go — the runtime UI actually receives input.
//
// This exists because it did not, and nothing noticed. uiHandleEvent's type
// switch listed POINTER types (*sdl.MouseButtonEvent), while sdl.PollEvent
// returns values — goEvent in go-sdl2 builds them that way, which is why every
// other handler in the engine (editor.go, uiwin/window.go, input.go) switches
// on values. A type switch that matches nothing is not a compile error and not
// a panic: it is a menu whose buttons do nothing, and a level with no symptom
// anywhere but the screen.
//
// So the assertion is deliberately about the delivered types rather than about
// any particular widget: feed uiHandleEvent exactly what the frame loop feeds
// it, and require that a modal screen swallows it.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/hooks"
	"foundry/internal/ui"

	"github.com/veandco/go-sdl2/sdl"
)

// withModalScreen puts a blocking, modal screen on the game's stack.
func withModalScreen(t *testing.T) {
	t.Helper()
	s := ui.NewScreen("test_modal")
	s.Canvas = ui.Canvas{Mode: ui.ScaleConstantPixel}
	s.Modal = true
	ui.Game.Push(s)
	ui.Game.Tick(0, 800, 600)
	t.Cleanup(func() { ui.Game.Reset(); hooks.TakeUIEvents() })
}

func TestUIConsumesTheEventTypesSDLDelivers(t *testing.T) {
	withModalScreen(t)

	for _, ev := range []sdl.Event{
		sdl.MouseMotionEvent{Type: sdl.MOUSEMOTION, X: 10, Y: 10},
		sdl.MouseButtonEvent{Type: sdl.MOUSEBUTTONDOWN, Button: sdl.ButtonLeft, State: sdl.PRESSED, X: 10, Y: 10},
		sdl.MouseButtonEvent{Type: sdl.MOUSEBUTTONUP, Button: sdl.ButtonLeft, State: sdl.RELEASED, X: 10, Y: 10},
		sdl.MouseWheelEvent{Type: sdl.MOUSEWHEEL, Y: 1},
		sdl.KeyboardEvent{Type: sdl.KEYDOWN, State: sdl.PRESSED,
			Keysym: sdl.Keysym{Scancode: sdl.SCANCODE_DOWN}},
	} {
		if !uiHandleEvent(ev) {
			t.Errorf("%T reached gameplay: uiHandleEvent's type switch does not match "+
				"what sdl.PollEvent delivers", ev)
		}
	}
}

// The other direction, which the same switch decides: with nothing modal up,
// gameplay keeps its input. A UI that swallows everything is as broken as one
// that swallows nothing, and the two are one edit apart.
func TestGameplayKeepsItsInputWithNoMenuUp(t *testing.T) {
	ui.Game.Reset()
	t.Cleanup(func() { hooks.TakeUIEvents() })

	for _, ev := range []sdl.Event{
		sdl.MouseMotionEvent{Type: sdl.MOUSEMOTION, X: 10, Y: 10},
		sdl.KeyboardEvent{Type: sdl.KEYDOWN, State: sdl.PRESSED,
			Keysym: sdl.Keysym{Scancode: sdl.SCANCODE_SPACE}},
		sdl.KeyboardEvent{Type: sdl.KEYDOWN, State: sdl.PRESSED,
			Keysym: sdl.Keysym{Scancode: sdl.SCANCODE_ESCAPE}},
	} {
		if uiHandleEvent(ev) {
			t.Errorf("%T was swallowed by an empty screen stack", ev)
		}
	}
}

// Escape's two halves are in different packages and neither knows about the
// other: internal/ui closes the topmost modal screen, and this package opens
// the pause menu with the key the stack did NOT take. That is only symmetric
// while both halves see the key, which is the property this asserts — through
// the shipped pause.fui, so a rename of the screen breaks it here rather than
// in front of a player.
func TestEscapeOpensAndClosesThePauseMenu(t *testing.T) {
	prev := fileutil.ExecutablePath
	fileutil.ExecutablePath = filepath.Join(repoRootDir(t), "resources")
	t.Cleanup(func() {
		fileutil.ExecutablePath = prev
		ui.Game.Reset()
		hooks.TakeUIEvents()
	})
	ui.Game.Reset()

	esc := sdl.KeyboardEvent{Type: sdl.KEYDOWN, State: sdl.PRESSED,
		Keysym: sdl.Keysym{Scancode: sdl.SCANCODE_ESCAPE}}

	// Nothing up: the stack declines the key and gameplay opens the menu.
	if uiHandleEvent(esc) {
		t.Fatal("the stack took Escape with nothing up; the pause menu can never open")
	}
	HandleEvent(esc)
	if ui.Game.Find(pauseScreenName) == nil {
		t.Fatalf("Escape did not open %q", pauseScreenName)
	}

	// Up: the stack takes the key and closes it, and gameplay never sees it.
	if !uiHandleEvent(esc) {
		t.Fatal("the stack let Escape through while the pause menu was up")
	}
	if ui.Game.Find(pauseScreenName) != nil {
		t.Error("Escape did not close the pause menu")
	}
}

// repoRootDir walks up to the directory holding go.mod.
func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}
