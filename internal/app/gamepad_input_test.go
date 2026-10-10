package app

import (
	"testing"

	"foundry/internal/platform"
)

// A pad press goes where a key press would: to a rebind first, and otherwise
// to gameplay, where scripts see it by name.
func TestPadEdgesRouteLikeKeys(t *testing.T) {
	withDefaultBindings(t)
	device := platform.LastDevice
	t.Cleanup(func() {
		platform.LastDevice = device
		platform.EndBindingCapture()
		platform.ResetKeys()
	})
	platform.ResetKeys()

	platform.BeginBindingCapture(platform.ActReload, platform.PadSlot, nil)
	routePadEdge(platform.PadEdge{In: platform.PadY, Down: true})
	routePadEdge(platform.PadEdge{In: platform.PadY, Down: false})
	if platform.BindingCaptureActive() || platform.Bindings[platform.ActReload][platform.PadSlot] != platform.GamepadInput(platform.PadY) {
		t.Fatalf("the rebind did not take Pad Y: %+v", platform.Bindings[platform.ActReload])
	}
	platform.AdvanceKeys(1.0 / 60)
	if platform.KeyPressed(platform.GamepadInput(platform.PadY)) || platform.KeyReleased(platform.GamepadInput(platform.PadY)) {
		t.Error("a press the rebind took also reached gameplay")
	}

	routePadEdge(platform.PadEdge{In: platform.PadRT, Down: true})
	platform.AdvanceKeys(1.0 / 60)
	if !platform.KeyPressed(platform.GamepadInput(platform.PadRT)) || platform.LastDevice != platform.DevicePad {
		t.Error("a trigger pull did not reach scripts or mark the pad in use")
	}
	routePadEdge(platform.PadEdge{In: platform.PadRT, Down: false})
	platform.AdvanceKeys(1.0 / 60)
	if !platform.KeyReleased(platform.GamepadInput(platform.PadRT)) {
		t.Error("the trigger's release did not reach scripts")
	}
}

// With no modal screen up the pad belongs to gameplay: the left stick walks
// even if something somewhere has focus.
func TestPadMenusOnlyUnderAModalScreen(t *testing.T) {
	if uiHandlePad(platform.PadLSUp) || uiHandlePad(platform.PadA) {
		t.Error("the UI took a pad press with no modal screen up")
	}
}
