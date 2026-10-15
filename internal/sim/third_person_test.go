package sim

import (
	"math"
	"testing"

	"foundry/internal/collision"
	"foundry/internal/gfx"
	"foundry/internal/leveltest"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// thirdPersonRoom is leveltest's 10x10 room (floor 0, ceiling 4) with a
// third-person camera, and the player standing in it at (x, z).
func thirdPersonRoom(t *testing.T, x, z float32) *world.Level {
	t.Helper()
	ch := leveltest.RoomChunk(t)
	leveltest.LoadGeometry(t, ch)
	lvl := &world.Level{Chunks: []world.Chunk{*ch}}
	cam := world.DefaultPlayerCamera()
	cam.Mode = world.CameraThirdPerson
	lvl.Player.Camera = cam

	prevP, prevB, prevCam, prevTP := world.PlayerEntity, world.PlayerBodyEntity, gfx.Cam, world.PlayerThirdPerson
	t.Cleanup(func() {
		world.PlayerEntity, world.PlayerBodyEntity, gfx.Cam, world.PlayerThirdPerson = prevP, prevB, prevCam, prevTP
		ResetThirdPerson()
	})
	world.PlayerEntity = world.NewPlayer(mgl32.Vec3{x, 0.9, z})
	ResetThirdPerson()
	stepDT = 1.0 / 60
	return lvl
}

func TestThirdPersonCameraSitsBehindAndAboveThePlayerInTheOpen(t *testing.T) {
	lvl := thirdPersonRoom(t, 7, 5)
	gfx.Cam.Yaw, gfx.Cam.Pitch = 0, 0 // looking down +X, toward the near wall
	PlacePlayerCamera(lvl)
	if !world.PlayerThirdPerson {
		t.Fatal("PlayerThirdPerson not set")
	}
	c := lvl.Player.Camera.Effective()
	p := gfx.Cam.Position
	if p.X() >= 7 {
		t.Errorf("camera at x=%v, want behind the player (x < 7)", p.X())
	}
	if want := 0.9 + c.Height; math.Abs(float64(p.Y()-want)) > 1e-3 {
		t.Errorf("camera height %v, want the pivot's %v at zero pitch", p.Y(), want)
	}
	// Over the shoulder: looking down +X, the camera's right is +Z, and the
	// default shoulder is the left one (negative).
	if c.Shoulder >= 0 {
		t.Fatalf("default shoulder %v: this test expects the left", c.Shoulder)
	}
	if p.Z() >= 5 {
		t.Errorf("camera at z=%v, want off over the left shoulder (z < 5)", p.Z())
	}
	if gfx.Cam.Direction.Sub(gfx.DirectionFromAngles(0, 0)).Len() > 1e-5 {
		t.Errorf("look direction %v changed; the look angles are the player's", gfx.Cam.Direction)
	}
}

func TestThirdPersonCameraIsPulledInByTheWallBehind(t *testing.T) {
	// Standing a metre from the wall behind, looking away from it: a 3.6
	// boom would put the camera through the wall.
	lvl := thirdPersonRoom(t, 5, 5)
	behind := collision.Raycast(lvl, mgl32.Vec3{5, 2, 5}, mgl32.Vec3{-1, 0, 0},
		collision.RayFilter{Range: 100, Geometry: true})
	wallX := behind.Stop.X()
	world.PlayerEntity.Position = mgl32.Vec3{wallX + 1, 0.9, 5}
	gfx.Cam.Yaw, gfx.Cam.Pitch = 0, 0
	PlacePlayerCamera(lvl)
	r := lvl.Player.Camera.Effective().Radius
	if gx := gfx.Cam.Position.X(); gx < wallX+r-1e-3 {
		t.Errorf("camera at x=%v is inside the wall's radius (wall at %v, radius %v)", gx, wallX, r)
	}

	// Turning so the boom has room again eases it back out, not in one jump.
	gfx.Cam.Yaw = math.Pi // look toward the wall: the boom now points into the room
	PlacePlayerCamera(lvl)
	first := boomState.boom
	for i := 0; i < 240; i++ {
		PlacePlayerCamera(lvl)
	}
	if first >= lvl.Player.Camera.Effective().Distance-1e-3 {
		t.Errorf("boom jumped straight out to %v", first)
	}
	if got, want := boomState.boom, lvl.Player.Camera.Effective().Distance; math.Abs(float64(got-want)) > 0.01 {
		t.Errorf("boom after 4 s = %v, want eased out to %v", got, want)
	}
}

func TestThirdPersonCameraNeverGoesUnderTheFloorOrThroughTheCeiling(t *testing.T) {
	lvl := thirdPersonRoom(t, 5, 5)
	for _, pitch := range []float32{-1.5, -0.8, 0.8, 1.5} {
		gfx.Cam.Yaw, gfx.Cam.Pitch = 0.3, pitch
		ResetThirdPerson()
		PlacePlayerCamera(lvl)
		y := gfx.Cam.Position.Y()
		if y < 0.2 || y > 3.8 {
			t.Errorf("pitch %v: camera at y=%v, outside the room's floor 0 / ceiling 4 less a radius", pitch, y)
		}
	}
}

func TestFirstPersonCameraIsUnchanged(t *testing.T) {
	lvl := thirdPersonRoom(t, 5, 5)
	lvl.Player.Camera = world.PlayerCamera{}
	gfx.Cam.Yaw, gfx.Cam.Pitch = 0.4, 0.1
	PlacePlayerCamera(lvl)
	want := world.PlayerEntity.Position.Add(mgl32.Vec3{0, world.PlayerEyeHeight(), 0})
	if gfx.Cam.Position != want || world.PlayerThirdPerson {
		t.Errorf("first person camera at %v (third person %v), want the eye %v", gfx.Cam.Position, world.PlayerThirdPerson, want)
	}
}

func TestEaseBoomSnapsInAndEasesOut(t *testing.T) {
	if got := easeBoom(3, 1, 5, 1.0/60, false); got != 1 {
		t.Errorf("pulling in eased: %v", got)
	}
	got := easeBoom(1, 3, 5, 1.0/60, false)
	if got <= 1 || got >= 1.5 {
		t.Errorf("easing out by one frame gave %v", got)
	}
	if got := easeBoom(1, 3, 5, 1.0/60, true); got != 3 {
		t.Errorf("snap did not snap: %v", got)
	}
}

func TestTurnTowardTakesTheShortWay(t *testing.T) {
	got := turnToward(3.0, -3.0, 0.1) // across the ±π seam
	if !(got > 3.0 || got < -3.1) {
		t.Errorf("turned the long way: %v", got)
	}
	if got := turnToward(0, 0.05, 0.1); got != 0.05 {
		t.Errorf("overshot: %v", got)
	}
}
