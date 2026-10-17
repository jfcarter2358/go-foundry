package render

// entity_walk_test.go — the third-person body is one of the entities the
// world passes walk, and the virtual shadow map treats it as a mover.

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// withBody installs a visible body with one small mesh at pos.
func withBody(t *testing.T, pos mgl32.Vec3) *world.Entity {
	t.Helper()
	prev := world.PlayerBodyEntity
	t.Cleanup(func() { world.PlayerBodyEntity = prev })
	b := world.NewPlayerBody()
	b.Visible = true
	b.Position = pos
	b.Models = []world.EntityModel{{Name: "body", Visible: true, Submeshes: []gfx.Geometry{{
		Vertices: []float32{-0.3, 0, -0.3, 0, 0, 0.3, 2, 0.3, 0, 0, 0.3, 0, -0.3, 0, 0},
	}}}}
	world.PlayerBodyEntity = b
	return b
}

func TestTheBodyIsWalkedOnlyWhileItIsShown(t *testing.T) {
	lvl := &world.Level{Entities: []world.Entity{{ID: "crate", Visible: true}}}
	if n := drawnEntityCount(lvl); n != 1 {
		t.Fatalf("no body: %d entities walked, want 1", n)
	}
	b := withBody(t, mgl32.Vec3{})
	if n := drawnEntityCount(lvl); n != 2 || drawnEntity(lvl, 1) != b {
		t.Fatalf("shown body: %d entities walked, want the level's 1 and the body", n)
	}
	b.Visible = false
	if n := drawnEntityCount(lvl); n != 1 {
		t.Errorf("hidden body still walked (%d)", n)
	}
}

func TestVSMTrackerHandsInTheBodysOldAndNewPlaceAsItMoves(t *testing.T) {
	lvl := &world.Level{}
	b := withBody(t, mgl32.Vec3{0, 0, 0})
	var tr vsmTracker
	tr.collect(lvl, mgl32.Vec3{}, 100)
	if len(tr.draws) != 1 {
		t.Fatalf("movers drawn = %d, want the body", len(tr.draws))
	}
	before := tr.draws[0].box

	b.Position = mgl32.Vec3{10, 0, 0}
	_, boxes := tr.collect(lvl, mgl32.Vec3{}, 100)
	after := tr.draws[0].box
	if !boxIn(boxes, before) || !boxIn(boxes, after) {
		t.Errorf("moving the body handed in %v; want both where it was (%v) and where it is (%v)", boxes, before, after)
	}

	b.Visible = false
	_, boxes = tr.collect(lvl, mgl32.Vec3{}, 100)
	if !boxIn(boxes, after) {
		t.Error("hiding the body did not hand in the place it was last drawn")
	}
}
