package gfx

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/veandco/go-sdl2/sdl"

	"foundry/internal/platform"
)

// keysDown is a keyboard snapshot with only the named scancodes held.
func keysDown(sc ...sdl.Scancode) []uint8 {
	ks := make([]uint8, sdl.NUM_SCANCODES)
	for _, s := range sc {
		ks[s] = 1
	}
	return ks
}

func flyFrame(t *testing.T, sc ...sdl.Scancode) float32 {
	t.Helper()
	prev := Cam
	t.Cleanup(func() { Cam = prev })
	Cam.Position = mgl32.Vec3{}
	Cam.Yaw, Cam.Pitch = 0, 0
	UpdateCamDirection()
	before := Cam.Position.Y()
	HandleInputScaled(keysDown(sc...), 1)
	return Cam.Position.Y() - before
}

// The editor's fly camera goes up and down on Space and Shift whether or not
// the player's gravity happens to be on: the camera has nothing to stand on.
// Gravity is only turned off when a play session ends, so reading it here left
// both keys dead in a freshly opened editor.
func TestFreeFlyVerticalIgnoresGravity(t *testing.T) {
	prevG, prevDT := platform.GravityEnabled, platform.DeltaTime
	t.Cleanup(func() { platform.GravityEnabled, platform.DeltaTime = prevG, prevDT })
	platform.DeltaTime = 1.0 / 60

	for _, gravity := range []bool{true, false} {
		platform.GravityEnabled = gravity
		if up := flyFrame(t, sdl.SCANCODE_SPACE); up <= 0 {
			t.Errorf("gravity=%v: Space moved the fly camera by %v, want up", gravity, up)
		}
		if down := flyFrame(t, sdl.SCANCODE_LSHIFT); down >= 0 {
			t.Errorf("gravity=%v: Shift moved the fly camera by %v, want down", gravity, down)
		}
	}
}

// The player is a body, so its vertical keys wait for free-fly — and for
// free-fly specifically, not for gravity being off. The two were one switch
// once, which made "the player is weightless" and "the player is a noclip
// camera" impossible to ask for separately.
func TestMoveIntentVerticalNeedsFlyMode(t *testing.T) {
	prevG, prevF, prevDT, prevCam := platform.GravityEnabled, platform.FlyMode, platform.DeltaTime, Cam
	t.Cleanup(func() {
		platform.GravityEnabled, platform.FlyMode = prevG, prevF
		platform.DeltaTime, Cam = prevDT, prevCam
	})
	platform.DeltaTime = 1.0 / 60
	Cam.Yaw, Cam.Pitch = 0, 0
	UpdateCamDirection()

	platform.FlyMode = false
	for _, gravity := range []bool{true, false} {
		platform.GravityEnabled = gravity
		if up := MoveIntent(keysDown(sdl.SCANCODE_SPACE)).Y(); up != 0 {
			t.Errorf("gravity=%v: Space on foot asked for %v vertically, want 0 (it jumps instead)", gravity, up)
		}
		if down := MoveIntent(keysDown(sdl.SCANCODE_LSHIFT)).Y(); down != 0 {
			t.Errorf("gravity=%v: Shift on foot asked for %v vertically, want 0 (it sprints instead)", gravity, down)
		}
	}

	platform.FlyMode = true
	if up := MoveIntent(keysDown(sdl.SCANCODE_SPACE)).Y(); up <= 0 {
		t.Errorf("Space in free-fly asked for %v vertically, want up", up)
	}
	if down := MoveIntent(keysDown(sdl.SCANCODE_LSHIFT)).Y(); down >= 0 {
		t.Errorf("Shift in free-fly asked for %v vertically, want down", down)
	}
}
