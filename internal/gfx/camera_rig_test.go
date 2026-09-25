package gfx

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// placePlayer puts the camera where PlacePlayerCamera would: on the eye, looking
// the player's way, with the world's up.
func placePlayer(pos mgl32.Vec3, yaw, pitch float32) {
	Cam.Yaw, Cam.Pitch = yaw, pitch
	Cam.Position = pos
	Cam.Direction = DirectionFromAngles(yaw, pitch)
	Cam.Up = WorldUp
	UpdateCamMatrix()
}

func resetRigTest(t *testing.T) {
	t.Helper()
	ResetCameraRig()
	FieldOfView = DefaultFieldOfView
	t.Cleanup(func() {
		ResetCameraRig()
		FieldOfView = DefaultFieldOfView
	})
}

func rigNear(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-3 }

func nearVec(a, b mgl32.Vec3) bool { return a.Sub(b).Len() < 1e-3 }

func TestDetachHoldsTheViewWhereItIs(t *testing.T) {
	resetRigTest(t)
	placePlayer(mgl32.Vec3{1, 2, 3}, 0.5, 0.2)
	CameraDetach()
	StepCameraRig(0.016, true, true)
	if !nearVec(Cam.Position, mgl32.Vec3{1, 2, 3}) {
		t.Fatalf("detaching moved the camera to %v", Cam.Position)
	}
	if !nearVec(Cam.Direction, DirectionFromAngles(0.5, 0.2)) {
		t.Fatalf("detaching turned the camera to %v", Cam.Direction)
	}
}

func TestMoveToEasesAndArrives(t *testing.T) {
	resetRigTest(t)
	placePlayer(mgl32.Vec3{}, 0, 0)
	CameraMoveTo(mgl32.Vec3{10, 0, 0}, 1, EaseInOut)

	StepCameraRig(0.25, true, true)
	quarter := Cam.Position.X()
	// Eased in and out: a quarter of the time covers well under a quarter of
	// the distance.
	if quarter <= 0 || quarter >= 2.5 {
		t.Fatalf("a quarter of the way through an ease_in_out move was at x=%g, want (0, 2.5)", quarter)
	}
	StepCameraRig(0.25, true, true)
	if !rigNear(Cam.Position.X(), 5) {
		t.Fatalf("halfway through a symmetric ease the camera is at x=%g, want 5", Cam.Position.X())
	}
	StepCameraRig(1, true, true)
	if !nearVec(Cam.Position, mgl32.Vec3{10, 0, 0}) {
		t.Fatalf("the move ended at %v, want <10,0,0>", Cam.Position)
	}
}

func TestPlayerLookSurvivesADetachedCamera(t *testing.T) {
	resetRigTest(t)
	placePlayer(mgl32.Vec3{}, 1.0, 0.1)
	CameraTurnTo(-2, -0.5, 0, EaseLinear)
	StepCameraRig(0.016, true, true)
	if Cam.Yaw != 1.0 || Cam.Pitch != 0.1 {
		t.Fatalf("the rig rewrote the player's look to %g, %g", Cam.Yaw, Cam.Pitch)
	}
	if !nearVec(Cam.Direction, DirectionFromAngles(-2, -0.5)) {
		t.Fatalf("the rig's turn did not reach the view: %v", Cam.Direction)
	}
}

func TestAttachBlendsBackToThePlayersEye(t *testing.T) {
	resetRigTest(t)
	eye := mgl32.Vec3{0, 1.7, 0}
	placePlayer(eye, 0, 0)
	SetCameraPosition(mgl32.Vec3{10, 1.7, 0})
	CameraAttach(1, EaseLinear)
	if CameraDetached() {
		t.Fatal("still detached after camera_attach")
	}

	// Every frame the player's eye is placed first, then the rig blends over it.
	placePlayer(eye, 0, 0)
	StepCameraRig(0.5, true, true)
	if !rigNear(Cam.Position.X(), 5) {
		t.Fatalf("halfway back the camera is at x=%g, want 5", Cam.Position.X())
	}
	placePlayer(eye, 0, 0)
	StepCameraRig(0.6, true, true)
	if !nearVec(Cam.Position, eye) || CameraReturning() {
		t.Fatalf("after the blend the camera is at %v (returning %v), want the eye", Cam.Position, CameraReturning())
	}
}

func TestAttachWithNoPlayerLeavesTheViewAlone(t *testing.T) {
	resetRigTest(t)
	placePlayer(mgl32.Vec3{}, 0, 0)
	SetCameraPosition(mgl32.Vec3{4, 0, 0})
	CameraAttach(1, EaseLinear)
	StepCameraRig(0.1, false, false)
	if CameraReturning() {
		t.Fatal("blending back to a player that does not exist")
	}
	if !nearVec(Cam.Position, mgl32.Vec3{4, 0, 0}) {
		t.Fatalf("the view moved to %v", Cam.Position)
	}
}

func TestOrbitStartsWhereTheCameraIs(t *testing.T) {
	resetRigTest(t)
	placePlayer(mgl32.Vec3{-5, 0, 0}, 0, 0)
	target := RigTarget{Point: mgl32.Vec3{0, 0, 0}}
	if !CameraOrbit(&target, OrbitParams{}, 0, EaseLinear) {
		t.Fatal("camera_orbit with a target refused")
	}
	StepCameraRig(0.016, true, true)
	if !nearVec(Cam.Position, mgl32.Vec3{-5, 0, 0}) {
		t.Fatalf("starting an orbit jumped the camera to %v", Cam.Position)
	}
	if on, d, _, _ := CameraOrbiting(); !on || !rigNear(d, 5) {
		t.Fatalf("orbit = %v at distance %g, want on at 5", on, d)
	}

	// Swing a quarter turn round: yaw 90 looks along +Z, so the camera sits on
	// the -Z side of the target.
	yaw := float32(math.Pi / 2)
	CameraOrbit(nil, OrbitParams{Yaw: &yaw}, 0, EaseLinear)
	StepCameraRig(0.016, true, true)
	if !nearVec(Cam.Position, mgl32.Vec3{0, 0, -5}) {
		t.Fatalf("orbit yaw 90 put the camera at %v, want <0,0,-5>", Cam.Position)
	}
	if !nearVec(Cam.Direction, mgl32.Vec3{0, 0, 1}) {
		t.Fatalf("the orbiting camera looks along %v, not at its target", Cam.Direction)
	}
}

func TestOrbitFollowsAMovingTarget(t *testing.T) {
	resetRigTest(t)
	placePlayer(mgl32.Vec3{-5, 0, 0}, 0, 0)
	CameraOrbit(&RigTarget{Point: mgl32.Vec3{}, Ref: "unit"}, OrbitParams{}, 0, EaseLinear)
	targets := CameraTargets()
	if len(targets) != 1 {
		t.Fatalf("a followed orbit reports %d targets to refresh, want 1", len(targets))
	}
	targets[0].Point = mgl32.Vec3{0, 0, 3}
	StepCameraRig(0.016, true, true)
	if !nearVec(Cam.Position, mgl32.Vec3{-5, 0, 3}) {
		t.Fatalf("the camera did not follow its target: %v", Cam.Position)
	}
}

func TestAdjustingAnOrbitThatIsNotRunningRefuses(t *testing.T) {
	resetRigTest(t)
	placePlayer(mgl32.Vec3{}, 0, 0)
	d := float32(3)
	if CameraOrbit(nil, OrbitParams{Distance: &d}, 0, EaseLinear) {
		t.Fatal("adjusted an orbit that was never started")
	}
	if CameraDetached() {
		t.Fatal("a refused orbit detached the camera")
	}
}

func TestLookAtTracksTheTarget(t *testing.T) {
	resetRigTest(t)
	placePlayer(mgl32.Vec3{}, 0, 0)
	CameraLookAt(RigTarget{Point: mgl32.Vec3{0, 0, 10}, Ref: "x"}, 1, EaseLinear)
	StepCameraRig(0.5, true, true)
	yaw, _ := AnglesFromDirection(Cam.Direction)
	if !rigNear(yaw, math.Pi/4) {
		t.Fatalf("halfway through the turn yaw is %g, want pi/4", yaw)
	}
	StepCameraRig(0.6, true, true)
	CameraTargets()[0].Point = mgl32.Vec3{-10, 0, 0}
	StepCameraRig(0.016, true, true)
	if !nearVec(Cam.Direction, mgl32.Vec3{-1, 0, 0}) {
		t.Fatalf("the look did not stay on its target as it moved: %v", Cam.Direction)
	}
}

func TestTurnGoesTheShortWayRound(t *testing.T) {
	resetRigTest(t)
	placePlayer(mgl32.Vec3{}, radiansF(170), 0)
	CameraTurnTo(radiansF(-170), 0, 1, EaseLinear)
	StepCameraRig(0.5, true, true)
	yaw, _ := AnglesFromDirection(Cam.Direction)
	// Halfway from 170 to -170 the short way is 180, not 0.
	if math.Abs(math.Abs(float64(yaw))-math.Pi) > 1e-3 {
		t.Fatalf("halfway through a 20-degree turn across the wrap yaw is %g degrees", yaw*180/math.Pi)
	}
}

func TestRollBanksTheUpVector(t *testing.T) {
	resetRigTest(t)
	placePlayer(mgl32.Vec3{}, 0, 0) // looking along +X; right is +Z
	CameraRollTo(math.Pi/2, 0, EaseLinear)
	StepCameraRig(0.016, true, true)
	if !nearVec(Cam.Up, mgl32.Vec3{0, 0, 1}) {
		t.Fatalf("a 90-degree roll right left up at %v, want the camera's right, <0,0,1>", Cam.Up)
	}
}

func TestZoomEasesTheFieldOfViewAttached(t *testing.T) {
	resetRigTest(t)
	placePlayer(mgl32.Vec3{}, 0, 0)
	CameraZoom(25, 1, EaseLinear)
	StepCameraRig(0.5, true, true)
	if !rigNear(FieldOfView, 35) {
		t.Fatalf("halfway through a zoom from 45 to 25 the fov is %g", FieldOfView)
	}
	if CameraDetached() {
		t.Fatal("zooming detached the camera")
	}
	StepCameraRig(1, true, true)
	if !rigNear(FieldOfView, 25) {
		t.Fatalf("the zoom ended at %g", FieldOfView)
	}
}

func TestEaseCurvesHitTheirEnds(t *testing.T) {
	for _, n := range EaseNames() {
		e, ok := ParseEase(":" + n + ":")
		if !ok {
			t.Fatalf("ParseEase(%q) failed", n)
		}
		if e.Apply(0) != 0 || e.Apply(1) != 1 {
			t.Errorf("%s: Apply(0)=%g Apply(1)=%g", n, e.Apply(0), e.Apply(1))
		}
		prev := float32(0)
		for i := 1; i <= 20; i++ {
			v := e.Apply(float32(i) / 20)
			if v < prev {
				t.Errorf("%s runs backwards at %d/20", n, i)
			}
			prev = v
		}
	}
	if _, ok := ParseEase("bouncy"); ok {
		t.Error("an unknown ease parsed")
	}
}

func TestScreenRayThroughTheMiddleIsTheView(t *testing.T) {
	resetRigTest(t)
	placePlayer(mgl32.Vec3{1, 2, 3}, 0.7, -0.3)
	o, d, ok := ScreenRay(800, 450) // the default 1600x900 window's middle
	if !ok {
		t.Fatal("no ray with a camera placed")
	}
	if !nearVec(o, mgl32.Vec3{1, 2, 3}) || !nearVec(d, Cam.Direction) {
		t.Fatalf("the middle ray is %v along %v, want the camera's own", o, d)
	}
}

func TestWorldToScreenInvertsScreenRay(t *testing.T) {
	resetRigTest(t)
	placePlayer(mgl32.Vec3{0, 1, 0}, 0.3, 0.1)
	for _, px := range [][2]float32{{100, 100}, {1500, 800}, {800, 450}, {10, 890}} {
		o, d, _ := ScreenRay(px[0], px[1])
		p := o.Add(d.Mul(12))
		x, y, depth, ok := WorldToScreen(p)
		if !ok || !rigNear(x, px[0]) || !rigNear(y, px[1]) || depth <= 0 {
			t.Errorf("pixel %v -> %v -> (%g, %g, depth %g, %v)", px, p, x, y, depth, ok)
		}
	}
	behind := Cam.Position.Sub(Cam.Direction)
	if _, _, _, ok := WorldToScreen(behind); ok {
		t.Error("a point behind the camera landed on screen")
	}
}

func radiansF(deg float32) float32 { return deg * math.Pi / 180 }

// TestHoldKeepsTheRigInTheCameraMidFrame is the frame's order: the player's
// eye is placed, the rig takes the camera straight back (so the script tick
// casts from what is on screen), and the step afterwards moves it on.
func TestHoldKeepsTheRigInTheCameraMidFrame(t *testing.T) {
	resetRigTest(t)
	eye := mgl32.Vec3{0, 1.7, 0}
	placePlayer(eye, 0, 0)
	SetCameraPosition(mgl32.Vec3{10, 1.7, 0})

	placePlayer(eye, 0, 0)
	HoldCameraRig()
	if !nearVec(Cam.Position, mgl32.Vec3{10, 1.7, 0}) {
		t.Fatalf("mid-frame the detached camera is at %v, want the rig's pose", Cam.Position)
	}

	// Blending back: mid-frame shows the blend so far, and the blend still
	// arrives at the eye it was handed.
	CameraAttach(1, EaseLinear)
	placePlayer(eye, 0, 0)
	HoldCameraRig()
	if !rigNear(Cam.Position.X(), 10) {
		t.Fatalf("before the return starts, mid-frame shows x=%g, want the rig's 10", Cam.Position.X())
	}
	StepCameraRig(0.5, true, true)
	placePlayer(eye, 0, 0)
	HoldCameraRig()
	if !rigNear(Cam.Position.X(), 5) {
		t.Fatalf("halfway back, mid-frame shows x=%g, want 5", Cam.Position.X())
	}
	StepCameraRig(0.6, true, true)
	if !nearVec(Cam.Position, eye) || CameraReturning() {
		t.Fatalf("the return ended at %v (returning %v), want the eye", Cam.Position, CameraReturning())
	}
	placePlayer(eye, 0, 0)
	HoldCameraRig()
	if !nearVec(Cam.Position, eye) {
		t.Fatalf("attached again, a hold moved the camera to %v", Cam.Position)
	}
}
