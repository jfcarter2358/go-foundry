package app

import (
	"testing"

	"foundry/internal/platform"

	"github.com/veandco/go-sdl2/sdl"
)

func withDefaultBindings(t *testing.T) {
	t.Helper()
	prev := platform.Bindings
	t.Cleanup(func() { platform.Bindings = prev })
	platform.Bindings = platform.DefaultBindings
}

func TestBindingsRoundTripThroughControlsFile(t *testing.T) {
	withLookState(t)
	withDefaultBindings(t)
	platform.Bindings.Bind(platform.ActJump, 1, platform.MouseInput(sdl.ButtonX1))
	platform.Bindings.Bind(platform.ActCrouch, 1, platform.Input{})
	platform.Bindings.Bind(platform.ActMoveForward, 0, platform.KeyInput(sdl.SCANCODE_UP))
	want := platform.Bindings

	data, err := marshalControlSettings()
	if err != nil {
		t.Fatal(err)
	}
	platform.Bindings = platform.DefaultBindings
	if err := applyControlSettings(data); err != nil {
		t.Fatal(err)
	}
	if platform.Bindings != want {
		t.Errorf("bindings did not survive the file:\n%s", data)
	}
}

// A file naming only some actions leaves the rest on their defaults, and a name
// the engine does not know is skipped rather than failing the whole file.
func TestPartialBindingsKeepDefaults(t *testing.T) {
	withLookState(t)
	withDefaultBindings(t)
	file := "bindings:\n  reload: [T, \"\"]\n  no_such_action: [X]\n  throw: [Not A Key]\n"
	if err := applyControlSettings([]byte(file)); err != nil {
		t.Fatal(err)
	}
	if platform.Bindings[platform.ActReload][0] != platform.KeyInput(sdl.SCANCODE_T) {
		t.Errorf("reload is %+v", platform.Bindings[platform.ActReload])
	}
	if platform.Bindings[platform.ActMoveForward] != platform.DefaultBindings[platform.ActMoveForward] {
		t.Error("an action the file did not name lost its default")
	}
	if platform.Bindings[platform.ActThrow][0].Bound() {
		t.Error("an unparseable key name should leave its slot unbound")
	}
}

// A captured press never reaches the UI or gameplay, and the release that
// follows the click which started it is not what gets bound.
func TestCaptureTakesPressesOnly(t *testing.T) {
	withDefaultBindings(t)
	t.Cleanup(platform.EndBindingCapture)
	platform.BeginBindingCapture(platform.ActReload, 0, nil)

	up := sdl.MouseButtonEvent{Type: sdl.MOUSEBUTTONUP, Button: sdl.ButtonLeft}
	if captureBindingEvent(up) || !platform.BindingCaptureActive() {
		t.Fatal("a button release was captured")
	}
	down := sdl.KeyboardEvent{Type: sdl.KEYDOWN, Keysym: sdl.Keysym{Scancode: sdl.SCANCODE_T}}
	if !captureBindingEvent(down) || platform.BindingCaptureActive() {
		t.Fatal("a key press was not captured")
	}
	if platform.Bindings[platform.ActReload][0] != platform.KeyInput(sdl.SCANCODE_T) {
		t.Errorf("reload is %+v", platform.Bindings[platform.ActReload])
	}
	if captureBindingEvent(down) {
		t.Error("a press with no capture running was taken")
	}
}
