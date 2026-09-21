package gfx

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func near(a, b, eps float32) bool { return float32(math.Abs(float64(a-b))) <= eps }

// A look turns the view onto the point by the time it runs out, halfway round
// at the halfway mark, and the short way round.
func TestLookAtEasesOntoThePointTheShortWay(t *testing.T) {
	saved := Cam
	defer func() { Cam = saved; StopLookAt() }()

	Cam.Position = mgl32.Vec3{0, 0, 0}
	Cam.Yaw, Cam.Pitch = float32(math.Pi)*0.9, 0 // facing almost -X
	// A point up and just past -X on the other side of the seam: the short way
	// is through +-pi, not back through zero.
	target := mgl32.Vec3{-10, 10, -2}
	StartLookAt(target, 2)

	ApplyLookAt(1, true)
	if !LookingAt() {
		t.Fatal("the look let go halfway")
	}
	ApplyLookAt(1, true)
	wantYaw := float32(math.Atan2(-2, -10))
	d := math.Mod(float64(Cam.Yaw-wantYaw), 2*math.Pi)
	if d > math.Pi {
		d -= 2 * math.Pi
	}
	if d < -math.Pi {
		d += 2 * math.Pi
	}
	if math.Abs(d) > 1e-4 {
		t.Fatalf("yaw %v after the look, want %v (mod 2pi)", Cam.Yaw, wantYaw)
	}
	if !near(Cam.Pitch, float32(math.Atan2(10, math.Hypot(10, 2))), 1e-4) {
		t.Fatalf("pitch %v after the look", Cam.Pitch)
	}
	if Cam.Yaw < float32(math.Pi)*0.9-0.001 && Cam.Yaw > 0 {
		t.Fatalf("yaw %v went the long way round through zero", Cam.Yaw)
	}
}

// Holding keeps the look, and with it the view on the point; not holding lets
// go the moment it arrives.
func TestLookAtHoldsOnlyWhenAsked(t *testing.T) {
	saved := Cam
	defer func() { Cam = saved; StopLookAt() }()
	Cam.Position = mgl32.Vec3{}
	Cam.Yaw, Cam.Pitch = 0, 0

	StartLookAt(mgl32.Vec3{0, 0, 5}, 0.5)
	ApplyLookAt(1, true)
	if !LookingAt() {
		t.Fatal("a held look let go once it arrived")
	}
	Cam.Position = mgl32.Vec3{5, 0, 0} // the body moved: the view follows the point
	ApplyLookAt(0.1, true)
	if !near(Cam.Yaw, float32(math.Atan2(5, -5)), 1e-4) {
		t.Fatalf("a held look did not re-aim from the new position: yaw %v", Cam.Yaw)
	}

	StartLookAt(mgl32.Vec3{0, 0, -5}, 0.5)
	ApplyLookAt(1, false)
	if LookingAt() {
		t.Fatal("an unheld look kept the view after it arrived")
	}
}
