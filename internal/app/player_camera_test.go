package app

// player_camera_test.go — the camera is placed from CURRENT frame transforms.
//
// A rider stores its position in coordinates that move, so where the camera
// goes depends on the transform those coordinates are current for. The frame
// loop's last movers — EntityUpdate, and a vehicle's own world collision —
// run after the player has moved, and RefreshFrames is what publishes their
// result. Place the camera before that and the eye is computed from where the
// ship WAS while the hold around it draws from where it IS, so the interior
// jumps by however far the ship travelled, every single tick. On a ship being
// dragged by a drive model that is metres a frame, and it reads on screen as
// the interior strobing against whatever is outside it.

import (
	"strings"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// ridingLevel is a ship that owns a chunk, with the player riding in it.
func ridingLevel(t *testing.T, shipAt mgl32.Vec3) *world.Level {
	t.Helper()
	lvl := &world.Level{
		Chunks:   []world.Chunk{{ID: "hold", FrameOwner: "ship", Scale: 1}},
		Entities: []world.Entity{{ID: "ship", Position: shipAt, Scale: mgl32.Vec3{1, 1, 1}}},
	}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	prev := world.PlayerEntity
	t.Cleanup(func() { world.PlayerEntity = prev })
	world.PlayerEntity = world.NewPlayer(mgl32.Vec3{1, 0, 2}) // hold-local
	world.PlayerEntity.Frame = "ship"
	return lvl
}

func TestTheRidersCameraFollowsTheShipItIsPublishedWith(t *testing.T) {
	lvl := ridingLevel(t, mgl32.Vec3{0, 100, 0})
	local := world.PlayerEntity.Position.Add(mgl32.Vec3{0, world.EyeHeight, 0})

	// Where the eye is before anything moves.
	PlacePlayerCamera(lvl)
	if got, want := gfx.Cam.Position, lvl.Frames.Frame("ship").ToWorld(local); got != want {
		t.Fatalf("camera at %v, want %v", got, want)
	}
	stale := gfx.Cam.Position

	// Now the tick's last mover shoves the ship — the ship's own world
	// collision, or an entity push. This is the motion the old ordering missed.
	const shove = float32(1.5)
	world.FindEntity(lvl, "ship").Position = mgl32.Vec3{0, 100 - shove, 0}

	// Placing the camera without republishing leaves it where the ship was,
	// which is the bug this file is about. Asserted so the test has teeth: if
	// the fixture ever stops moving the ship, this catches it.
	PlacePlayerCamera(lvl)
	if gfx.Cam.Position != stale {
		t.Fatalf("the fixture did not exercise a stale transform: %v moved to %v", stale, gfx.Cam.Position)
	}

	// Republished, the camera lands on the ship's new position — and agrees
	// with the matrix the hold's own geometry is drawn through.
	world.RefreshFrames(lvl, lvl.Frames)
	PlacePlayerCamera(lvl)
	want := lvl.Frames.Frame("ship").ToWorld(local)
	if got := gfx.Cam.Position; got != want {
		t.Errorf("camera at %v, want %v", got, want)
	}
	if got := gfx.Cam.Position.Y(); got != stale.Y()-shove {
		t.Errorf("camera Y %v; want the stale %v less the ship's %v of travel", got, stale.Y(), shove)
	}
	drawnAt := lvl.Chunks[0].LocalToWorld(mgl32.Vec3{})
	if eye := gfx.Cam.Position.Sub(local); !nearVec(eye, drawnAt, 1e-4) {
		t.Errorf("the eye sits at %v relative to the hold, which draws at %v", eye, drawnAt)
	}
}

// On foot the frame is the world and none of this applies, but the same call
// still has to put the camera on the player's head.
func TestOnFootTheCameraIsStillTheWorldPlacement(t *testing.T) {
	lvl := ridingLevel(t, mgl32.Vec3{0, 100, 0})
	world.PlayerEntity.Frame = ""
	world.PlayerEntity.Position = mgl32.Vec3{4, 1, -6}

	PlacePlayerCamera(lvl)
	if want := (mgl32.Vec3{4, 1 + world.EyeHeight, -6}); gfx.Cam.Position != want {
		t.Errorf("camera at %v, want %v", gfx.Cam.Position, want)
	}
	if gfx.Cam.Up != gfx.WorldUp {
		t.Errorf("on foot the up vector is %v, want the world's %v", gfx.Cam.Up, gfx.WorldUp)
	}
}

// The ordering itself, checked in the source the way world_passes_test.go
// checks its own: there is no runtime symptom to assert on from here — the loop
// is one long function that cannot be stepped — and the invariant is entirely
// about which line comes first.
func TestTheFrameLoopPlacesTheCameraAfterRefreshingFrames(t *testing.T) {
	src := testenv.RepoFile(t, "internal", "app", "app.go")

	refresh := strings.Index(src, "world.RefreshFrames(&level, level.Frames)")
	if refresh < 0 {
		t.Fatal("the frame loop no longer calls world.RefreshFrames; this test needs rewriting")
	}
	place := strings.Index(src, "PlacePlayerCamera(&level)")
	if place < 0 {
		t.Fatal("the frame loop no longer calls PlacePlayerCamera")
	}
	if place < refresh {
		t.Error("PlacePlayerCamera runs before RefreshFrames: a rider's camera would be " +
			"computed from last tick's transform while the geometry around it draws from this tick's")
	}
	if n := strings.Count(src, "PlacePlayerCamera(&level)"); n != 1 {
		t.Errorf("the loop places the player camera %d times; one, after RefreshFrames, is the point", n)
	}
	// And nothing else in the loop places it: SeatedCamera is PlacePlayerCamera's
	// own business now, and a second caller would be a second ordering to keep.
	if strings.Contains(src, "SeatedCamera(") {
		t.Error("app.go calls SeatedCamera directly again; camera placement belongs in PlacePlayerCamera")
	}
}

func nearVec(a, b mgl32.Vec3, eps float32) bool {
	d := a.Sub(b)
	return d.Len() <= eps
}
