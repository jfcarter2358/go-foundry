package collision

import (
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// entity_update_test.go — the per-frame driver.
//
// EntityUpdate lives in the root package because it runs the collision
// resolver, which sits above internal/world; the entity behaviour it drives is
// tested in that package.

func TestEntityUpdateGatesMotionButNotAnimation(t *testing.T) {
	// The editor passes motion=false so entities stay where they were placed;
	// animations advance either way, since a clip you cannot see while placing
	// is most of the way to useless.
	lvl := &world.Level{Entities: []world.Entity{{ID: "a", Velocity: mgl32.Vec3{1, 0, 0}}}}

	EntityUpdate(lvl, 1, false, nil)
	if lvl.Entities[0].Position != (mgl32.Vec3{}) {
		t.Errorf("entity moved with motion off: %v", lvl.Entities[0].Position)
	}
	EntityUpdate(lvl, 1, true, nil)
	if !vecNear(lvl.Entities[0].Position, mgl32.Vec3{1, 0, 0}, 1e-5) {
		t.Errorf("entity did not move with motion on: %v", lvl.Entities[0].Position)
	}
}
