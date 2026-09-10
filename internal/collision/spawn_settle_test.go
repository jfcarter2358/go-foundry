package collision

// spawn_settle_test.go — the player starts exactly where the spawn marker says.
//
// The marker's Position is the player's feet, and that is where the editor
// draws the arrow. Collision places the body by its centre. A spawn that put
// the body's centre on the marker started it half-buried in the floor, and the
// first tick shoved it out — so the camera the player actually got was not the
// one ApplySpawnPoint set, nor anywhere the arrow suggested.

import (
	"foundry/internal/gfx"
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestSpawnedPlayerDoesNotMoveWhenItSettles(t *testing.T) {
	lvl := collisionTestLevel() // floors at y=0
	lvl.Spawn = &world.SpawnPoint{Position: mgl32.Vec3{5, 0, 5}}

	prevPlayer, prevCam := world.PlayerEntity, gfx.Cam
	t.Cleanup(func() { world.PlayerEntity, gfx.Cam = prevPlayer, prevCam })
	world.PlayerEntity = world.NewPlayer(mgl32.Vec3{})

	if !world.ApplySpawnPoint(lvl) {
		t.Fatal("ApplySpawnPoint found no spawn")
	}
	spawnedBody := world.PlayerEntity.Position
	spawnedEye := gfx.Cam.Position

	// The eye ApplySpawnPoint sets has to be the one the frame loop will place
	// from the body (PlacePlayerCamera), or the view jumps on the first frame.
	if want := spawnedBody.Add(mgl32.Vec3{0, world.EyeHeight, 0}); !nearVec3(spawnedEye, want, 1e-5) {
		t.Errorf("spawn camera %v, but the body it spawned puts the eye at %v", spawnedEye, want)
	}

	if !settle(world.PlayerEntity, lvl, 60) {
		t.Fatalf("never stood on the floor; ended at %v", world.PlayerEntity.Position)
	}
	// The separation margin the push-apart leaves (separationEpsilon) is the
	// only movement allowed.
	if got := world.PlayerEntity.Position; !nearVec3(got, spawnedBody, 0.02) {
		t.Errorf("player settled at %v, spawned at %v — the spawn did not put it where it stands", got, spawnedBody)
	}
	// And the feet are on the marker, which is what the arrow shows.
	feet := world.PlayerEntity.Position.Y() - world.PlayerEntity.BoundHalfY
	if feet < lvl.Spawn.Position.Y()-1e-4 || feet > lvl.Spawn.Position.Y()+0.02 {
		t.Errorf("feet at y=%v, marker at y=%v", feet, lvl.Spawn.Position.Y())
	}
}

func nearVec3(a, b mgl32.Vec3, tol float32) bool {
	d := a.Sub(b)
	return d.X() <= tol && d.X() >= -tol && d.Y() <= tol && d.Y() >= -tol && d.Z() <= tol && d.Z() >= -tol
}
