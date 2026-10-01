package platform

import (
	"math"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

func withPad(t *testing.T) {
	t.Helper()
	withBindings(t)
	prevPad, prevDead := pad, PadDeadZone
	t.Cleanup(func() { pad, PadDeadZone = prevPad, prevDead })
	pad = padState{}
	PadDeadZone = 0.15
}

func near32(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-4 }

// Inside the dead zone is nothing; the edge of it is 0 rather than a jump to
// 0.15; full travel is 1; and a diagonal stays on the unit circle.
func TestRadialDeadZone(t *testing.T) {
	if x, y := radialDeadZone(0.1, 0.05, 0.15); x != 0 || y != 0 {
		t.Errorf("inside the zone read %v, %v", x, y)
	}
	if x, _ := radialDeadZone(0.16, 0, 0.15); x <= 0 || x > 0.02 {
		t.Errorf("just past the zone read %v, want a little above 0", x)
	}
	if x, _ := radialDeadZone(1, 0, 0.15); !near32(x, 1) {
		t.Errorf("full travel read %v", x)
	}
	x, y := radialDeadZone(1, 1, 0.15)
	if m := math.Hypot(float64(x), float64(y)); m > 1+1e-5 {
		t.Errorf("a corner read magnitude %v", m)
	}
	if !near32(x, y) {
		t.Errorf("a diagonal bent: %v, %v", x, y)
	}
}

// SDL's sticks have +Y down; up is PadLSUp.
func TestPadValuesSplitSticksIntoDirections(t *testing.T) {
	var buttons [padButtonCount]bool
	buttons[PadA-1] = true
	v := padValues(PadAxes{LY: -1, RX: 0.5, RT: 1}, &buttons, 0)
	if v[PadLSUp] != 1 || v[PadLSDown] != 0 {
		t.Errorf("left stick up read up %v, down %v", v[PadLSUp], v[PadLSDown])
	}
	if !near32(v[PadRSRight], 0.5) || v[PadRSLeft] != 0 {
		t.Errorf("right stick right read %v / %v", v[PadRSRight], v[PadRSLeft])
	}
	if v[PadRT] != 1 || v[PadLT] != 0 || v[PadA] != 1 || v[PadB] != 0 {
		t.Errorf("triggers or buttons misread: %v", v)
	}
}

// A press needs PadPressThreshold and a release needs to fall below the lower
// line, so a stick resting on the threshold does not chatter.
func TestPadEdgesHaveHysteresis(t *testing.T) {
	withPad(t)
	var v [PadInputCount]float32
	v[PadRT] = 0.45
	if e := SetPadValues(v); len(e) != 0 {
		t.Fatalf("below the threshold gave %v", e)
	}
	v[PadRT] = 0.6
	if e := SetPadValues(v); len(e) != 1 || e[0] != (PadEdge{PadRT, true}) {
		t.Fatalf("crossing gave %v", e)
	}
	v[PadRT] = 0.45
	if e := SetPadValues(v); len(e) != 0 || !PadHeld(PadRT) {
		t.Fatalf("dropping just under the press line let go: %v", e)
	}
	v[PadRT] = 0.2
	if e := SetPadValues(v); len(e) != 1 || e[0] != (PadEdge{PadRT, false}) {
		t.Fatalf("letting go gave %v", e)
	}
	v[PadA] = 1
	SetPadValues(v)
	if e := BlankPad(); len(e) != 1 || e[0].Down {
		t.Errorf("blanking gave %v", e)
	}
}

func TestPadNamesRoundTripAndLabelByFamily(t *testing.T) {
	for p := PadA; p < PadInputCount; p++ {
		in := GamepadInput(p)
		got, ok := ParseInput(in.Name())
		if !ok || got != in {
			t.Errorf("%q parsed back as %+v", in.Name(), got)
		}
	}
	if got, ok := ParseInput("pad right stick up"); !ok || got.Pad != PadRSUp {
		t.Errorf("a pad name in lower case parsed as %+v, %v", got, ok)
	}
	for _, c := range []struct {
		f    PadFamily
		p    PadInput
		want string
	}{
		{PadFamilyXbox, PadA, "A"},
		{PadFamilyPlayStation, PadA, "Cross"},
		{PadFamilyNintendo, PadA, "B"}, // positional: Nintendo prints B on the south button
		{PadFamilyPlayStation, PadRT, "R2"},
		{PadFamilyNone, PadLB, "LB"},
		{PadFamilyNintendo, PadRSUp, "RS Up"},
	} {
		if got := PadLabel(c.p, c.f); got != c.want {
			t.Errorf("PadLabel(%s, %s) = %q, want %q", PadName(c.p), c.f, got, c.want)
		}
	}
}

func TestPadSlotReadsHeldAndValue(t *testing.T) {
	withPad(t)
	keys := make([]uint8, sdl.NUM_SCANCODES)
	var v [PadInputCount]float32
	v[PadLSUp] = 0.4
	SetPadValues(v)
	if ActionHeld(ActMoveForward, keys) {
		t.Error("a stick under halfway counts as held")
	}
	if got := ActionValue(ActMoveForward, keys); !near32(got, 0.4) {
		t.Errorf("forward's value is %v, want the stick's 0.4", got)
	}
	keys[sdl.SCANCODE_W] = 1
	if got := ActionValue(ActMoveForward, keys); got != 1 {
		t.Errorf("a held key gives %v, want 1", got)
	}
	v[PadRSRight] = 1
	SetPadValues(v)
	if !Bindings.Held(ActLookRight, keys) || Bindings.KeyHeld(ActLookRight, keys) {
		t.Error("the look stick must be Held but not KeyHeld")
	}
}

// The pad has its own column: a key cannot go in it and a pad input cannot go
// anywhere else.
func TestBindKeepsPadInItsColumn(t *testing.T) {
	withPad(t)
	Bindings.Bind(ActJump, PadSlot, KeyInput(sdl.SCANCODE_J))
	if Bindings[ActJump][PadSlot] != GamepadInput(PadA) {
		t.Error("a key went into the pad column")
	}
	Bindings.Bind(ActJump, 0, GamepadInput(PadY))
	if Bindings[ActJump][0] != KeyInput(sdl.SCANCODE_SPACE) {
		t.Error("a pad input went into a key column")
	}
	// Binding Y to jump takes it off next weapon, live at the same time.
	Bindings.Bind(ActJump, PadSlot, GamepadInput(PadY))
	if Bindings[ActNextWeapon][PadSlot].Bound() {
		t.Error("next weapon kept Pad Y")
	}
}

// No pad input in the default layout does two things at once.
func TestDefaultPadLayoutHasNoClashes(t *testing.T) {
	for a := range DefaultBindings {
		for b := a + 1; b < int(ActionCount); b++ {
			pa, pb := DefaultBindings[a][PadSlot], DefaultBindings[b][PadSlot]
			if pa.Bound() && pa == pb && contextsOverlap(Actions[a].Context, Actions[b].Context) {
				t.Errorf("%s and %s are both on %s", Actions[a].Name, Actions[b].Name, pa.Name())
			}
		}
		if p := DefaultBindings[a][PadSlot].Pad; ReservedPad(p) {
			t.Errorf("%s defaults to the reserved %s", Actions[a].Name, PadName(p))
		}
	}
}

func TestCaptureIntoThePadColumn(t *testing.T) {
	withPad(t)
	BeginBindingCapture(ActReload, PadSlot, nil)
	if !CaptureBindingInput(KeyInput(sdl.SCANCODE_T)) || !BindingCaptureActive() {
		t.Fatal("a key must be swallowed while the pad slot listens")
	}
	if !CaptureBindingInput(GamepadInput(PadGuide)) || !BindingCaptureActive() {
		t.Fatal("Guide must be swallowed and leave the capture listening")
	}
	if !CaptureBindingInput(GamepadInput(PadLT)) || !BindingCaptureActive() {
		t.Fatal("a pad press must wait to see whether it is a press or a hold")
	}
	if CapturePadReleased(PadRT) || !CapturePadReleased(PadLT) || BindingCaptureActive() {
		t.Fatal("letting go of the trigger did not finish the capture")
	}
	if Bindings[ActReload][PadSlot] != GamepadInput(PadLT) {
		t.Errorf("reload's pad slot is %+v", Bindings[ActReload][PadSlot])
	}

	BeginBindingCapture(ActReload, PadSlot, nil)
	CaptureBindingInput(GamepadInput(PadStart))
	if Bindings[ActReload][PadSlot] != GamepadInput(PadLT) || BindingCaptureActive() {
		t.Error("Start did not cancel")
	}
	BeginBindingCapture(ActReload, PadSlot, nil)
	CaptureBindingInput(GamepadInput(PadBack))
	if Bindings[ActReload][PadSlot].Bound() {
		t.Error("Back did not clear the pad slot")
	}

	// A key slot ignores pad presses but lets Start back out.
	BeginBindingCapture(ActReload, 0, nil)
	if !CaptureBindingInput(GamepadInput(PadA)) || !BindingCaptureActive() {
		t.Fatal("a pad button must be swallowed while a key slot listens")
	}
	CaptureBindingInput(GamepadInput(PadStart))
	if BindingCaptureActive() || Bindings[ActReload][0] != KeyInput(sdl.SCANCODE_R) {
		t.Error("Start did not cancel a key slot's capture")
	}
}

// Held through the hold time, the pad input binds as a hold; and a hold takes
// nothing off the press of the same button.
func TestCaptureBindsAHoldWhenHeld(t *testing.T) {
	withPad(t)
	t.Cleanup(EndBindingCapture)
	Bindings.Bind(ActReload, PadSlot, GamepadInput(PadX))
	BeginBindingCapture(ActInteract, PadSlot, nil)
	CaptureBindingInput(GamepadInput(PadX))
	if !CapturePadHeld(PadX) || BindingCaptureActive() {
		t.Fatal("holding the button did not finish the capture")
	}
	if Bindings[ActInteract][PadSlot] != HoldInput(PadX) {
		t.Errorf("interact's pad slot is %+v, want a hold of X", Bindings[ActInteract][PadSlot])
	}
	if Bindings[ActReload][PadSlot] != GamepadInput(PadX) {
		t.Errorf("binding the hold took reload's press of X: %+v", Bindings[ActReload][PadSlot])
	}
	if CapturePadReleased(PadX) {
		t.Error("the release after a hold was bound bound again")
	}
}

func TestHoldInputNamesRoundTrip(t *testing.T) {
	h := HoldInput(PadX)
	if h.Name() != "Hold Pad X" {
		t.Errorf("name %q", h.Name())
	}
	for _, n := range []string{"Hold Pad X", "hold pad x"} {
		if in, ok := ParseInput(n); !ok || in != h {
			t.Errorf("ParseInput(%q) = %+v, %v", n, in, ok)
		}
	}
	if _, ok := ParseInput("Hold Space"); ok {
		t.Error("a key parsed as a hold")
	}
	if h.Plain() != GamepadInput(PadX) {
		t.Error("Plain kept the hold")
	}
}

// A hold reads as held only after PadHoldTime, and a press sharing its button
// with a hold never reads as held — it is a tap.
func TestHoldAndSharedPressLevels(t *testing.T) {
	withPad(t)
	prev := PadHoldTime
	t.Cleanup(func() { PadHoldTime = prev })
	PadHoldTime = 0.4
	Bindings.Bind(ActReload, PadSlot, GamepadInput(PadX))
	Bindings.Bind(ActInteract, PadSlot, HoldInput(PadX))
	Bindings.Bind(ActJump, PadSlot, GamepadInput(PadA))

	var v [PadInputCount]float32
	v[PadX], v[PadA] = 1, 1
	SetPadValues(v)
	if got := AdvancePadHolds(0.2); len(got) != 0 {
		t.Fatalf("holds reached early: %v", got)
	}
	if Bindings.Held(ActInteract, nil) || Bindings.Held(ActReload, nil) {
		t.Error("X read as held before the hold time, as a hold or a shared press")
	}
	if !Bindings.Held(ActJump, nil) {
		t.Error("a press with no hold on its button stopped reading as held")
	}
	if got := AdvancePadHolds(0.25); len(got) != 2 || got[0] != PadA || got[1] != PadX {
		t.Fatalf("holds reached = %v, want A and X", got)
	}
	if !Bindings.Held(ActInteract, nil) || Bindings.PadValue(ActInteract) != 1 {
		t.Error("the hold did not read as held once held long enough")
	}
	if Bindings.Held(ActReload, nil) {
		t.Error("the shared press read as held")
	}
	if got := AdvancePadHolds(0.5); len(got) != 0 {
		t.Errorf("a hold was reported twice: %v", got)
	}
	SetPadValues([PadInputCount]float32{})
	if PadHeldFor(PadX) != 0 || Bindings.Held(ActInteract, nil) {
		t.Error("letting go did not end the hold")
	}
}

// Scripts see a hold become down after the hold time, and a shared press as a
// tap on the release.
func TestActionStateForHoldsAndTaps(t *testing.T) {
	withPad(t)
	ResetKeys()
	t.Cleanup(ResetKeys)
	prev := PadHoldTime
	t.Cleanup(func() { PadHoldTime = prev })
	PadHoldTime = 0.4
	Bindings.Bind(ActReload, PadSlot, GamepadInput(PadX))
	Bindings.Bind(ActInteract, PadSlot, HoldInput(PadX))
	x := GamepadInput(PadX)

	// A tap.
	PressKey(x)
	AdvanceKeys(0.1)
	if ActionPressed(ActReload) || ActionDown(ActReload) || ActionPressed(ActInteract) {
		t.Error("the press acted before it was let go of or held")
	}
	ReleaseKey(x)
	AdvanceKeys(0.1)
	if !ActionPressed(ActReload) || !ActionReleased(ActReload) || ActionReleased(ActInteract) {
		t.Errorf("a tap: reload pressed %v released %v, interact released %v",
			ActionPressed(ActReload), ActionReleased(ActReload), ActionReleased(ActInteract))
	}

	// A hold.
	PressKey(x)
	AdvanceKeys(0.1)
	AdvanceKeys(0.2)
	if ActionDown(ActInteract) {
		t.Error("the hold was down early")
	}
	AdvanceKeys(0.25)
	if !ActionDown(ActInteract) || !ActionPressed(ActInteract) || math.Abs(ActionHeldTime(ActInteract)-0.05) > 1e-6 {
		t.Errorf("at the hold time: down %v pressed %v held %v",
			ActionDown(ActInteract), ActionPressed(ActInteract), ActionHeldTime(ActInteract))
	}
	AdvanceKeys(0.1)
	if ActionPressed(ActInteract) {
		t.Error("the hold was pressed on two ticks")
	}
	ReleaseKey(x)
	AdvanceKeys(0.1)
	if !ActionReleased(ActInteract) || ActionPressed(ActReload) {
		t.Errorf("the hold's release: interact released %v, reload pressed %v",
			ActionReleased(ActInteract), ActionPressed(ActReload))
	}
}
