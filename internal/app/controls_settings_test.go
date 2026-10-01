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

func TestPadSettingsAndColumnRoundTrip(t *testing.T) {
	withPadLook(t)
	vib, dead, assist := platform.PadVibration, platform.PadDeadZone, platform.AimAssist
	t.Cleanup(func() { platform.PadVibration, platform.PadDeadZone, platform.AimAssist = vib, dead, assist })

	platform.PadLookSensitivityX, platform.PadLookSensitivityY, platform.PadInvertY = 1.7, 0.6, true
	platform.PadDeadZone, platform.PadLookCurve, platform.PadVibration, platform.AimAssist = 0.22, 1.4, 0.3, false
	platform.Bindings.Bind(platform.ActJump, platform.PadSlot, platform.GamepadInput(platform.PadLB))
	want := platform.Bindings

	data, err := marshalControlSettings()
	if err != nil {
		t.Fatal(err)
	}
	platform.Bindings = platform.DefaultBindings
	platform.PadLookSensitivityX, platform.PadLookSensitivityY, platform.PadInvertY = 1, 1, false
	platform.PadDeadZone, platform.PadLookCurve, platform.PadVibration, platform.AimAssist = 0.15, 2, 1, true
	if err := applyControlSettings(data); err != nil {
		t.Fatal(err)
	}
	if platform.Bindings != want {
		t.Errorf("pad bindings did not survive the file:\n%s", data)
	}
	if platform.PadLookSensitivityX != 1.7 || platform.PadLookSensitivityY != 0.6 || !platform.PadInvertY ||
		platform.PadDeadZone != 0.22 || platform.PadLookCurve != 1.4 || platform.PadVibration != 0.3 || platform.AimAssist {
		t.Errorf("pad settings did not survive the file:\n%s", data)
	}
}

// A file from before the pad column keeps the default pad layout, and a pad
// input written into a key slot (or a key into the pad's) is refused.
func TestOldControlsFileKeepsPadDefaults(t *testing.T) {
	withLookState(t)
	withDefaultBindings(t)
	file := "bindings:\n  jump: [J, \"\"]\n  reload: [Pad X, \"\", T]\n"
	if err := applyControlSettings([]byte(file)); err != nil {
		t.Fatal(err)
	}
	if got := platform.Bindings[platform.ActJump][platform.PadSlot]; got != platform.GamepadInput(platform.PadA) {
		t.Errorf("jump's pad slot is %+v, want the default kept", got)
	}
	r := platform.Bindings[platform.ActReload]
	if r[0].Bound() || r[platform.PadSlot].Bound() {
		t.Errorf("inputs in the wrong column were kept: %+v", r)
	}
}
