package editor

// editor_surface_keys_test.go — surface mode's keys must leave the camera alone.
//
// The fly controls read the keyboard by polling, not from the event queue, so a
// surface-mode binding that sits on a movement key cannot be fixed by consuming
// the event: both things happen. And key repeat means holding one to fly would
// re-run the operation once per repeat. This is the test that keeps a future
// shortcut off them.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// flyKeys are the scancodes editorCamMove polls (editor_3d_camera.go).
var flyKeys = map[sdl.Scancode]string{
	sdl.SCANCODE_W:      "W (forward)",
	sdl.SCANCODE_A:      "A (strafe left)",
	sdl.SCANCODE_S:      "S (back)",
	sdl.SCANCODE_D:      "D (strafe right)",
	sdl.SCANCODE_SPACE:  "Space (up)",
	sdl.SCANCODE_LSHIFT: "Shift (down)",
	sdl.SCANCODE_LEFT:   "Left (yaw)",
	sdl.SCANCODE_RIGHT:  "Right (yaw)",
	sdl.SCANCODE_UP:     "Up (pitch)",
	sdl.SCANCODE_DOWN:   "Down (pitch)",
}

// surfaceBareKeys is every scancode surfaceEditHandleKey acts on without a
// modifier. Kept as data so the check below is a list to read rather than a
// switch to re-derive.
var surfaceBareKeys = []sdl.Scancode{
	sdl.SCANCODE_ESCAPE,
	sdl.SCANCODE_1, sdl.SCANCODE_2, sdl.SCANCODE_3,
	sdl.SCANCODE_E, sdl.SCANCODE_I, sdl.SCANCODE_C, sdl.SCANCODE_H, sdl.SCANCODE_B,
	sdl.SCANCODE_DELETE, sdl.SCANCODE_BACKSPACE,
}

func TestSurfaceShortcutsAvoidTheFlyControls(t *testing.T) {
	for _, sc := range surfaceBareKeys {
		if name, clash := flyKeys[sc]; clash {
			t.Errorf("surface mode binds %s, which flies the camera — "+
				"the camera polls that key, so consuming the event will not stop it moving", name)
		}
	}
}

// surfaceEditHandleKey is gated on a live target, so with no level loaded it
// must decline everything rather than acting on a nil sector.
func TestSurfaceKeysAreInertWhenNotShaping(t *testing.T) {
	Editor = EditorState{}
	for _, sc := range surfaceBareKeys {
		ev := sdl.KeyboardEvent{Type: sdl.KEYDOWN}
		ev.Keysym.Scancode = sc
		if surfaceEditHandleKey(ev) {
			t.Errorf("scancode %v was consumed while no surface is being shaped", sc)
		}
	}
}
