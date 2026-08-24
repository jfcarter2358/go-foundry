package editor

import (
	"foundry/internal/gfx"
	"foundry/internal/model"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestCameraModeSwitchDoesNotJumpTheView(t *testing.T) {
	// The whole point of deriving the orbit eye from the look direction: going
	// orbit -> fly -> orbit must land back where it started, or pressing C
	// would teleport the user.
	c := modelCamera{Target: mgl32.Vec3{1, 2, 3}, Dist: 5,
		Yaw: mgl32.DegToRad(35), Pitch: mgl32.DegToRad(-25)}

	eye := c.position()
	fwd := c.forward()

	c.setFly(true)
	if !vec3Close(c.position(), eye, 1e-5) {
		t.Errorf("switching to fly moved the eye: %v -> %v", eye, c.position())
	}
	if !vec3Close(c.forward(), fwd, 1e-6) {
		t.Error("switching to fly changed the look direction")
	}

	c.setFly(false)
	if !vec3Close(c.position(), eye, 1e-4) {
		t.Errorf("round trip moved the eye: %v -> %v", eye, c.position())
	}
	if !vec3Close(c.Target, mgl32.Vec3{1, 2, 3}, 1e-4) {
		t.Errorf("round trip moved the pivot to %v", c.Target)
	}
}

func TestOrbitEyeLooksAtTheTarget(t *testing.T) {
	c := modelCamera{Target: mgl32.Vec3{0, 1, 0}, Dist: 4,
		Yaw: mgl32.DegToRad(70), Pitch: mgl32.DegToRad(-30)}
	eye := c.position()
	toTarget := c.Target.Sub(eye)
	if math.Abs(float64(toTarget.Len()-4)) > 1e-4 {
		t.Errorf("eye is %v from the target, want Dist = 4", toTarget.Len())
	}
	if d := toTarget.Normalize().Dot(c.forward()); d < 0.9999 {
		t.Errorf("the camera does not look at its own pivot (dot = %v)", d)
	}
}

func TestCameraLookClampsPitch(t *testing.T) {
	// Straight up or down makes the look direction parallel to WorldUp, and
	// LookAtV has no defined right vector there.
	c := modelCamera{Dist: 4}
	c.look(0, -100000)
	if c.Pitch > gfx.MaxPitch+1e-6 {
		t.Errorf("pitch = %v, above MaxPitch %v", c.Pitch, gfx.MaxPitch)
	}
	c.look(0, 200000)
	if c.Pitch < -gfx.MaxPitch-1e-6 {
		t.Errorf("pitch = %v, below -MaxPitch %v", c.Pitch, -gfx.MaxPitch)
	}
	// The view must stay well-formed at the clamp.
	if v := c.view(); math.IsNaN(float64(v[0])) {
		t.Error("view matrix degenerated at the pitch limit")
	}
}

func TestFlyMoveIsHorizontalRegardlessOfPitch(t *testing.T) {
	// Forward is flattened to the ground plane, as MoveCamera does, so looking
	// down does not drive the camera into the floor.
	c := modelCamera{Fly: true, Pitch: mgl32.DegToRad(-80)}
	c.move(1, 0, 0)
	if math.Abs(float64(c.Pos.Y())) > 1e-5 {
		t.Errorf("flying forward while looking down changed height to %v", c.Pos.Y())
	}
	if c.Pos.Len() < 0.99 {
		t.Errorf("flying forward moved only %v", c.Pos.Len())
	}

	// Up is the one axis that does move vertically.
	c2 := modelCamera{Fly: true}
	c2.move(0, 2, 0)
	if !vec3Close(c2.Pos, mgl32.Vec3{0, 2, 0}, 1e-5) {
		t.Errorf("flying up gave %v, want (0,2,0)", c2.Pos)
	}
}

func TestFlyMoveIgnoredInOrbitMode(t *testing.T) {
	// WASD belongs to fly mode; in orbit it must not drag the eye off its pivot.
	c := modelCamera{Dist: 4}
	c.move(5, 5, 5)
	if c.Pos != (mgl32.Vec3{}) {
		t.Errorf("orbit mode moved the fly position to %v", c.Pos)
	}
}

func TestFrameFitsTheDocument(t *testing.T) {
	c := modelCamera{Fly: true, Pos: mgl32.Vec3{100, 100, 100}}
	m := model.NewCubeDoc()
	m.MoveVerts([]int{0, 1, 2, 3, 4, 5, 6, 7}, mgl32.Vec3{10, 0, 0})

	c.frame(m)

	if c.Fly {
		t.Error("framing left the camera in fly mode")
	}
	if !vec3Close(c.Target, mgl32.Vec3{10, 0, 0}, 1e-5) {
		t.Errorf("pivot = %v, want the model centre (10,0,0)", c.Target)
	}
	// Far enough out that the whole cube fits in a 60 degree vertical FOV.
	if c.Dist < 0.87 {
		t.Errorf("Dist = %v, too close to fit a unit cube", c.Dist)
	}

	// An empty document has no extent to frame; fall back rather than divide by
	// zero.
	c.frame(&model.ModelDoc{})
	if c.Dist <= 0 || c.Target != (mgl32.Vec3{}) {
		t.Errorf("framing an empty document gave Target %v Dist %v", c.Target, c.Dist)
	}
	c.frame(nil)
	if c.Dist <= 0 {
		t.Errorf("framing nil gave Dist %v", c.Dist)
	}
}

func TestCameraRayPointsThroughScreenCentre(t *testing.T) {
	c := modelCamera{Target: mgl32.Vec3{}, Dist: 5, Yaw: mgl32.DegToRad(20), Pitch: mgl32.DegToRad(-15)}
	origin, dir := c.ray(640, 400, 1280, 800)

	if !vec3Close(origin, c.position(), 1e-5) {
		t.Errorf("ray origin %v is not the eye %v", origin, c.position())
	}
	// A ray through the exact centre of the viewport is the look direction.
	if d := dir.Dot(c.forward()); d < 0.9999 {
		t.Errorf("centre ray diverges from the look direction (dot = %v)", d)
	}
	// And it must reach the pivot it is aimed at.
	hit := origin.Add(dir.Mul(c.Dist))
	if !vec3Close(hit, c.Target, 1e-3) {
		t.Errorf("centre ray at Dist lands at %v, want the pivot %v", hit, c.Target)
	}
}

func TestCameraRayHandlesDegenerateViewport(t *testing.T) {
	c := modelCamera{Dist: 4}
	if _, dir := c.ray(0, 0, 0, 0); dir.Len() < 0.5 {
		t.Error("a zero-sized viewport produced a degenerate ray")
	}
}
