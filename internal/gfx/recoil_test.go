package gfx

import (
	"math"
	"testing"
)

func runRecoil(frames int) {
	for i := 0; i < frames; i++ {
		ApplyRecoil(1.0 / 60)
	}
}

// A kick arrives quickly, then gives back its recovering share and keeps the
// rest.
func TestRecoilKicksThenRecovers(t *testing.T) {
	prev := Cam
	t.Cleanup(func() { Cam = prev; ResetRecoil() })
	Cam.Pitch, Cam.Yaw = 0, 0
	ResetRecoil()

	AddRecoil(2, 1, 0.75)
	runRecoil(6) // 0.1 s
	peak := Cam.Pitch
	if peak < 1.2*math.Pi/180 {
		t.Fatalf("after 0.1 s the view had only climbed %v rad", peak)
	}
	runRecoil(240)
	wantP, wantY := float32(0.5*math.Pi/180), float32(0.25*math.Pi/180)
	if d := Cam.Pitch - wantP; d > 1e-4 || d < -1e-4 {
		t.Errorf("settled pitch %v, want %v (the unrecovered quarter)", Cam.Pitch, wantP)
	}
	if d := Cam.Yaw - wantY; d > 1e-4 || d < -1e-4 {
		t.Errorf("settled yaw %v, want %v", Cam.Yaw, wantY)
	}
	if ApplyRecoil(1.0 / 60) {
		t.Error("recoil still running after it settled")
	}
}

// A player pulling down by hand is not also pulled down by the recovery.
func TestRecoilAbsorbsPlayerPullDown(t *testing.T) {
	prev := Cam
	t.Cleanup(func() { Cam = prev; ResetRecoil() })
	Cam.Pitch, Cam.Yaw = 0, 0
	ResetRecoil()

	AddRecoil(2, 0, 1)
	runRecoil(10)
	pull := Cam.Pitch
	Cam.Pitch -= pull
	AbsorbRecoilPitch(-pull)
	runRecoil(240)
	if Cam.Pitch < -1e-4 {
		t.Errorf("recovery dragged the view below the start: pitch %v", Cam.Pitch)
	}
}
