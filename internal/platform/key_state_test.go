package platform

import (
	"math"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestKeyStateEdgesAndHolds(t *testing.T) {
	ResetKeys()
	t.Cleanup(ResetKeys)
	space := KeyInput(sdl.SCANCODE_SPACE)

	PressKey(space)
	PressKey(space) // key repeat or a doubled report: not a second press
	edges := AdvanceKeys(0.1)
	if len(edges) != 1 || !edges[0].Down {
		t.Fatalf("edges = %+v, want one press", edges)
	}
	if !KeyDown(space) || !KeyPressed(space) || KeyHeldTime(space) != 0 {
		t.Fatalf("after the press: down %v pressed %v held %v", KeyDown(space), KeyPressed(space), KeyHeldTime(space))
	}
	AdvanceKeys(0.25)
	if KeyPressed(space) || !near(KeyHeldTime(space), 0.25) {
		t.Errorf("a tick later: pressed %v held %v, want false 0.25", KeyPressed(space), KeyHeldTime(space))
	}

	// The snapshot says it is up: the release the UI took still lets go.
	SettleKeys(make([]uint8, 512), 0)
	edges = AdvanceKeys(0.1)
	if len(edges) != 1 || edges[0].Down || !near(edges[0].Held, 0.35) {
		t.Fatalf("edges = %+v, want one release after 0.35 s", edges)
	}
	if KeyDown(space) || !KeyReleased(space) || KeyHeldTime(space) != 0 {
		t.Errorf("after the release: down %v released %v", KeyDown(space), KeyReleased(space))
	}
	AdvanceKeys(0.1)
	if KeyReleased(space) {
		t.Error("released lasted more than one tick")
	}

	// A tap inside one frame is pressed and released on the same tick.
	f := KeyInput(sdl.SCANCODE_F)
	PressKey(f)
	ReleaseKey(f)
	AdvanceKeys(0.1)
	if !KeyPressed(f) || !KeyReleased(f) || KeyDown(f) {
		t.Errorf("tap: pressed %v released %v down %v", KeyPressed(f), KeyReleased(f), KeyDown(f))
	}
}

func TestActionStateFollowsTheLayout(t *testing.T) {
	ResetKeys()
	prev := Bindings
	t.Cleanup(func() { ResetKeys(); Bindings = prev })
	Bindings = DefaultBindings

	c, ctrl := KeyInput(sdl.SCANCODE_C), KeyInput(sdl.SCANCODE_LCTRL)
	PressKey(c)
	AdvanceKeys(0.5)
	PressKey(ctrl)
	AdvanceKeys(0.5)
	if !ActionDown(ActCrouch) || !near(ActionHeldTime(ActCrouch), 0.5) {
		t.Fatalf("crouch: down %v held %v", ActionDown(ActCrouch), ActionHeldTime(ActCrouch))
	}
	ReleaseKey(c)
	AdvanceKeys(0.1)
	if !ActionDown(ActCrouch) || ActionReleased(ActCrouch) {
		t.Error("letting go of one of crouch's keys released the action with the other still held")
	}
	ReleaseKey(ctrl)
	AdvanceKeys(0.1)
	if ActionDown(ActCrouch) || !ActionReleased(ActCrouch) {
		t.Error("letting go of both did not release crouch")
	}

	// Rebound: the old key no longer answers for the action.
	Bindings.Bind(ActJump, 0, KeyInput(sdl.SCANCODE_J))
	PressKey(KeyInput(sdl.SCANCODE_SPACE))
	AdvanceKeys(0.1)
	if ActionPressed(ActJump) {
		t.Error("Space still jumps after jump was rebound to J")
	}
}
