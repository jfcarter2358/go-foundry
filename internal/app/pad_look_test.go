package app

import (
	"math"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/platform"

	"github.com/go-gl/mathgl/mgl32"
)

// withPadLook is withLookState for the stick: defaults, no assist, and the pad
// let go of afterwards.
func withPadLook(t *testing.T) {
	t.Helper()
	withLookState(t)
	withDefaultBindings(t)
	sx, sy, inv, curve, device := platform.PadLookSensitivityX, platform.PadLookSensitivityY,
		platform.PadInvertY, platform.PadLookCurve, platform.LastDevice
	t.Cleanup(func() {
		platform.PadLookSensitivityX, platform.PadLookSensitivityY = sx, sy
		platform.PadInvertY, platform.PadLookCurve, platform.LastDevice = inv, curve, device
		platform.BlankPad()
		padBoostHeld = 0
	})
	platform.PadLookSensitivityX, platform.PadLookSensitivityY = 1, 1
	platform.PadInvertY, platform.PadLookCurve = false, 2
	platform.LastDevice = platform.DeviceKeyboard
	padBoostHeld = 0
}

func pushPad(p platform.PadInput, v float32) {
	var vals [platform.PadInputCount]float32
	vals[p] = v
	platform.SetPadValues(vals)
}

// The stick is a rate: a second held all the way over turns padLookYawSpeed
// (before the boost comes in), and half-way turns a quarter of that on the
// default square curve.
func TestPadLookIsARateThroughTheCurve(t *testing.T) {
	withPadLook(t)
	pushPad(platform.PadRSRight, 1)
	applyPadLook(0.1)
	full := float64(gfx.Cam.Yaw)
	if math.Abs(full-padLookYawSpeed*0.1) > 1e-5 {
		t.Errorf("full stick turned %v in 0.1 s, want %v", full, padLookYawSpeed*0.1)
	}

	gfx.Cam.Yaw = 0
	padBoostHeld = 0
	pushPad(platform.PadRSRight, 0.5)
	applyPadLook(0.1)
	if got := float64(gfx.Cam.Yaw); math.Abs(got-full*0.25) > 1e-5 {
		t.Errorf("half stick turned %v, want a quarter of %v", got, full)
	}
}

// Held hard over, the yaw rate ramps up so turning round does not crawl.
func TestPadLookBoostsAHeldTurn(t *testing.T) {
	withPadLook(t)
	pushPad(platform.PadRSRight, 1)
	applyPadLook(1.0 / 60)
	first := gfx.Cam.Yaw
	for i := 0; i < 60; i++ {
		applyPadLook(1.0 / 60)
	}
	before := gfx.Cam.Yaw
	applyPadLook(1.0 / 60)
	if step := gfx.Cam.Yaw - before; step < first*1.5 {
		t.Errorf("after a second held over a frame turned %v, the first %v", step, first)
	}
}

func TestPadLookInvertAndSensitivity(t *testing.T) {
	withPadLook(t)
	pushPad(platform.PadRSUp, 1)
	applyPadLook(0.1)
	if gfx.Cam.Pitch <= 0 {
		t.Fatalf("pushing up gave pitch %v", gfx.Cam.Pitch)
	}
	up := gfx.Cam.Pitch
	gfx.Cam.Pitch = 0
	platform.PadInvertY = true
	platform.PadLookSensitivityY = 2
	applyPadLook(0.1)
	if math.Abs(float64(gfx.Cam.Pitch+2*up)) > 1e-5 {
		t.Errorf("inverted at double sensitivity gave %v, want %v", gfx.Cam.Pitch, -2*up)
	}
}

// The stick follows a rebind: put look right on the left stick and the right
// stick no longer turns.
func TestPadLookFollowsBindings(t *testing.T) {
	withPadLook(t)
	platform.Bindings.Bind(platform.ActLookRight, platform.PadSlot, platform.GamepadInput(platform.PadDPadRight))
	pushPad(platform.PadRSRight, 1)
	applyPadLook(0.1)
	if gfx.Cam.Yaw != 0 {
		t.Errorf("an unbound stick direction turned the view %v", gfx.Cam.Yaw)
	}
	pushPad(platform.PadDPadRight, 1)
	applyPadLook(0.1)
	if gfx.Cam.Yaw <= 0 {
		t.Error("the rebound d-pad did not turn")
	}
}

func TestAimAssistGeometry(t *testing.T) {
	fwd := gfx.DirectionFromAngles(0, 0) // +X
	dead := mgl32.Vec3{20, 0, 0}
	if c := assistCloseness(fwd, dead, 0.4); math.Abs(c-1) > 1e-6 {
		t.Errorf("dead on is %v, want 1", c)
	}
	if c := assistCloseness(fwd, mgl32.Vec3{20, 0, 5}, 0.4); c != 0 {
		t.Errorf("14° off at 20 m is %v, want outside the cone", c)
	}
	edge := assistCloseness(fwd, mgl32.Vec3{20, 0, 0.6}, 0.4)
	if edge <= 0 || edge >= 1 {
		t.Errorf("just off centre is %v, want between 0 and 1", edge)
	}

	// angleTo inverts DirectionFromAngles, the short way round.
	to := gfx.DirectionFromAngles(3.0, 0.2).Mul(10)
	dy, dp := angleTo(to, -3.0, 0)
	if math.Abs(dy-(6.0-2*math.Pi)) > 1e-4 || math.Abs(dp-0.2) > 1e-4 {
		t.Errorf("angleTo gave %v, %v", dy, dp)
	}
}

// Aim assist never runs for the mouse.
func TestAimAssistIsPadOnly(t *testing.T) {
	withPadLook(t)
	platform.LastDevice = platform.DeviceKeyboard
	if a := aimAssist(1.0/60, 1); a.slow != 1 || a.yaw != 0 || a.pitch != 0 {
		t.Errorf("keyboard player got %+v", a)
	}
}
