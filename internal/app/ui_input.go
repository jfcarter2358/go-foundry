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
	"foundry/internal/editor"
	"foundry/internal/gfx"
	"foundry/internal/ui"

	"github.com/veandco/go-sdl2/sdl"
)

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
	if editor.Editor.Active {
		return false
	}

	switch t := event.(type) {
	case *sdl.MouseMotionEvent:
		return ui.Game.PointerMove(float32(t.X), float32(t.Y))

	case *sdl.MouseButtonEvent:
		if t.Button != sdl.ButtonLeft {
			return ui.Game.CapturesInput()
		}
		if t.State == sdl.PRESSED {
			return ui.Game.PointerDown(float32(t.X), float32(t.Y))
		}
		return ui.Game.PointerUp(float32(t.X), float32(t.Y))

	case *sdl.MouseWheelEvent:
		return ui.Game.Wheel(float32(t.Y))

	case *sdl.TextInputEvent:
		return ui.Game.TypeText(t.GetText())

	case *sdl.KeyboardEvent:
		if t.State != sdl.PRESSED {
			// Key-up is consumed alongside its key-down, so a key pressed over
			// a menu does not release into gameplay when the menu closes.
			return ui.Game.CapturesInput()
		}
		return uiHandleKey(t)
	}
	return false
}

func uiHandleKey(t *sdl.KeyboardEvent) bool {
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
	want := !editor.Editor.Active && ui.Game.WantsCursor()
	if want == uiCursorReleased {
		return
	}
	uiCursorReleased = want
	gfx.SetMouseLock(!want && !editor.Editor.Active)
}
