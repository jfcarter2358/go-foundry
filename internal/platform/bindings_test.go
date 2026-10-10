package platform

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

func withBindings(t *testing.T) {
	t.Helper()
	prev, prevMouse, prevChanged := Bindings, MouseButtons, ControlSettingsChanged
	t.Cleanup(func() {
		Bindings, MouseButtons, ControlSettingsChanged = prev, prevMouse, prevChanged
		capture = bindCapture{}
	})
	Bindings = DefaultBindings
	MouseButtons = 0
	capture = bindCapture{}
}

// Every action needs a unique name, or controls.yaml and bind_key could not
// tell two of them apart.
func TestEveryActionHasAUniqueName(t *testing.T) {
	seen := map[string]bool{}
	for i, a := range Actions {
		if a.Name == "" || a.Label == "" {
			t.Errorf("action %d has no name or label", i)
		}
		if seen[a.Name] {
			t.Errorf("two actions are named %q", a.Name)
		}
		seen[a.Name] = true
		if got, ok := ActionByName(a.Name); !ok || got != Action(i) {
			t.Errorf("ActionByName(%q) = %v, %v", a.Name, got, ok)
		}
	}
}

func TestInputNamesRoundTrip(t *testing.T) {
	for _, in := range []Input{
		{}, KeyInput(sdl.SCANCODE_W), KeyInput(sdl.SCANCODE_LSHIFT), KeyInput(sdl.SCANCODE_SPACE),
		MouseInput(sdl.ButtonLeft), MouseInput(sdl.ButtonX2), MouseInput(9),
	} {
		got, ok := ParseInput(in.Name())
		if !ok || got != in {
			t.Errorf("%q parsed back as %+v (ok %v), want %+v", in.Name(), got, ok, in)
		}
	}
	if _, ok := ParseInput("Not A Key"); ok {
		t.Error("a name matching nothing parsed")
	}
}

func TestHeldReadsKeysAndMouseButtons(t *testing.T) {
	withBindings(t)
	keys := make([]uint8, sdl.NUM_SCANCODES)
	if ActionHeld(ActCrouch, keys) {
		t.Fatal("crouch held with nothing down")
	}
	keys[sdl.SCANCODE_LCTRL] = 1
	if !ActionHeld(ActCrouch, keys) {
		t.Error("crouch's second slot (Left Ctrl) did not count")
	}
	MouseButtons = 1 << (sdl.ButtonRight - 1)
	if !ActionHeld(ActSecondaryFire, keys) || ActionHeld(ActPrimaryFire, keys) {
		t.Error("the right button reads as the wrong action")
	}
}

// Binding a key takes it off whatever else could be live at the same moment,
// and only that: Q stays "roll left" in a cockpit when it becomes "reload".
func TestBindStealsOnlyFromOverlappingContexts(t *testing.T) {
	withBindings(t)
	q := KeyInput(sdl.SCANCODE_Q)
	Bindings.Bind(ActReload, 0, q)
	if Bindings[ActNextWeapon][0] == q {
		t.Error("next weapon kept Q after reload took it on foot")
	}
	if Bindings[ActRollLeft][0] != q {
		t.Error("roll left lost Q, though a vehicle's keys never clash with on-foot ones")
	}
	// Movement is live in both, so it takes from both.
	Bindings.Bind(ActMoveForward, 1, q)
	if Bindings[ActReload][0] == q || Bindings[ActRollLeft][0] == q {
		t.Error("an always-live action did not take Q from the others")
	}
	if got := Bindings.ActionsFor(q); len(got) != 1 || got[0] != ActMoveForward {
		t.Errorf("Q now triggers %v", got)
	}
}

func TestCaptureBindsClearsCancelsAndRefusesReservedKeys(t *testing.T) {
	withBindings(t)
	ends := 0
	done := func() { ends++ }

	BeginBindingCapture(ActJump, 1, done)
	if !CaptureBindingInput(KeyInput(sdl.SCANCODE_F5)) || ends != 0 || !BindingCaptureActive() {
		t.Fatal("a reserved key must be swallowed and leave the capture listening")
	}
	if !CaptureBindingInput(MouseInput(sdl.ButtonMiddle)) || ends != 1 || BindingCaptureActive() {
		t.Fatal("a mouse button did not finish the capture")
	}
	if Bindings[ActJump][1] != MouseInput(sdl.ButtonMiddle) || !ControlSettingsChanged {
		t.Errorf("jump slot 2 is %+v, changed %v", Bindings[ActJump][1], ControlSettingsChanged)
	}

	BeginBindingCapture(ActJump, 0, done)
	CaptureBindingInput(KeyInput(sdl.SCANCODE_ESCAPE))
	if Bindings[ActJump][0] != KeyInput(sdl.SCANCODE_SPACE) || ends != 2 {
		t.Error("Escape changed the binding instead of cancelling")
	}

	BeginBindingCapture(ActJump, 0, done)
	CaptureBindingInput(KeyInput(sdl.SCANCODE_BACKSPACE))
	if Bindings[ActJump][0].Bound() || ends != 3 {
		t.Error("Backspace did not clear the slot")
	}

	if CaptureBindingInput(KeyInput(sdl.SCANCODE_W)) {
		t.Error("a press with no capture running was taken")
	}
}
