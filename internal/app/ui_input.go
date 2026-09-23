package app

// ui_input.go — turning SDL events into the intents internal/ui understands.
//
// The translation lives here rather than in internal/ui so that package never
// learns which device asked. A gamepad's d-pad and a keyboard's arrows both
// become ui.DirUp; its south button and Enter both become Activate. Adding
// controller support later is a case in this file and no change at all in the
// UI itself.
//
// Ordering in the event loop matters and is stated at the call site: this runs
// after the secondary-window router, so an editor window still owns its own
// input, and before the gameplay handler, so a click on a menu button is not
// also a shot.

import (
	"foundry/internal/devui"
	"log"

	"foundry/internal/gfx"
	"foundry/internal/ui"

	"github.com/veandco/go-sdl2/sdl"
)

// pauseScreenName is the screen Escape puts up during play. A project without
// one gets no pause menu and the key does nothing, which is the same way the
// loading screen is optional (see level_load.go).
const pauseScreenName = "pause"

// uiOpenPauseMenu puts the pause screen up. Called from the gameplay key
// handler, which Escape only reaches when the stack did not consume it — and
// the stack consumes Escape exactly when a modal screen is up, closing the
// topmost one (Stack.Cancel).
//
// So the two halves are symmetric without either knowing about the other:
// Escape with nothing up opens the pause menu, and Escape with it up closes it
// again. Nothing here has to track whether the menu is showing.
func uiOpenPauseMenu() {
	if _, err := ui.Game.Show(pauseScreenName); err != nil {
		log.Printf("pause screen: %v", err)
	}
}

// uiHandleEvent offers one SDL event to the running game's screen stack, and
// reports whether the UI consumed it.
//
// Consuming is the whole of how gameplay input is gated: a consumed event
// simply never reaches HandleEvent or the fly camera.
func uiHandleEvent(event sdl.Event) bool {
	// The UI only exists outside the editor, for the same reason scripts only
	// run outside it: what is on screen in the editor is the level, not the
	// game. The UI editor's own preview is a separate stack in a separate
	// window.
	if devActive() {
		return false
	}

	// The cases below are VALUE types, not pointers. sdl.PollEvent hands back
	// what goEvent builds, and goEvent returns values — so a `case
	// *sdl.MouseButtonEvent` matches nothing at all, and the symptom is not a
	// compile error or a panic but a UI that silently never sees an event.
	// Every other event handler in the engine (editor.go, uiwin/window.go,
	// input.go) switches on values for the same reason.
	switch t := event.(type) {
	case sdl.MouseMotionEvent:
		return ui.Game.PointerMove(float32(t.X), float32(t.Y))

	case sdl.MouseButtonEvent:
		if t.Button != sdl.ButtonLeft {
			return ui.Game.CapturesInput()
		}
		if t.State == sdl.PRESSED {
			return ui.Game.PointerDown(float32(t.X), float32(t.Y))
		}
		return ui.Game.PointerUp(float32(t.X), float32(t.Y))

	case sdl.MouseWheelEvent:
		return ui.Game.Wheel(float32(t.Y))

	case sdl.TextInputEvent:
		return ui.Game.TypeText(t.GetText())

	case sdl.KeyboardEvent:
		if t.State != sdl.PRESSED {
			// Key-up is consumed alongside its key-down, so a key pressed over
			// a menu does not release into gameplay when the menu closes.
			return ui.Game.CapturesInput()
		}
		return uiHandleKey(t)
	}
	return false
}

func uiHandleKey(t sdl.KeyboardEvent) bool {
	if dir, ok := uiNavDir(t.Keysym.Scancode); ok {
		return ui.Game.Navigate(dir)
	}

	switch t.Keysym.Scancode {
	case sdl.SCANCODE_RETURN, sdl.SCANCODE_KP_ENTER:
		// Enter submits a text field and activates anything else, which is what
		// it means in both cases: finish with this control.
		if ui.Game.EditText(ui.EditSubmit) {
			return true
		}
		return ui.Game.Activate()
	case sdl.SCANCODE_SPACE:
		return ui.Game.Activate()
	case sdl.SCANCODE_ESCAPE:
		return ui.Game.Cancel()
	case sdl.SCANCODE_BACKSPACE:
		return ui.Game.EditText(ui.EditBackspace)
	case sdl.SCANCODE_DELETE:
		return ui.Game.EditText(ui.EditDelete)
	case sdl.SCANCODE_HOME:
		return ui.Game.EditText(ui.EditHome)
	case sdl.SCANCODE_END:
		return ui.Game.EditText(ui.EditEnd)
	}
	// Anything else is consumed only under a modal screen, so a key that means
	// something to gameplay does not also fire behind an open menu.
	return ui.Game.CapturesInput()
}

// uiNavDir maps the movement keys onto a direction.
//
// Left and right double as caret movement in a focused text field, which
// EditText resolves: it returns false when nothing is focused, and the arrow
// falls through to navigation.
func uiNavDir(sc sdl.Scancode) (ui.Dir, bool) {
	switch sc {
	case sdl.SCANCODE_UP:
		return ui.DirUp, true
	case sdl.SCANCODE_DOWN:
		return ui.DirDown, true
	case sdl.SCANCODE_LEFT:
		if ui.Game.EditText(ui.EditLeft) {
			return 0, false
		}
		return ui.DirLeft, true
	case sdl.SCANCODE_RIGHT:
		if ui.Game.EditText(ui.EditRight) {
			return 0, false
		}
		return ui.DirRight, true
	}
	return 0, false
}

// uiCursorReleased is the last cursor state the UI asked for.
//
// The request is edge-triggered rather than applied every frame because
// mouse-look capture has three claimants — the editor toggle, window focus, and
// now a screen that wants a pointer — and re-asserting it unconditionally every
// frame would have the UI fighting the other two rather than joining them.
var uiCursorReleased bool

// uiApplyCursor releases or re-takes mouse-look when what the stack wants
// changes.
func uiApplyCursor() {
	// The developer console is a third claimant, alongside the screen stack
	// and the editor: typing into it with the cursor captured means the view
	// spins while you look at the keyboard. Joining them here rather than
	// grabbing the mouse itself is what makes closing the console hand capture
	// back to whatever the game wants *now*, which may be a menu that came up
	// while it was open.
	want := !devActive() && (ui.Game.WantsCursor() || devui.Open())
	if want == uiCursorReleased {
		return
	}
	uiCursorReleased = want
	gfx.SetMouseLock(!want && !devActive())
}
