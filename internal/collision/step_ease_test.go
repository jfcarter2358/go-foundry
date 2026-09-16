package collision

// step_ease_test.go — a walker's body eased over a step collision teleported it
// up (world/entity_step.go).
//
// Two halves that have to agree: the resolve snaps Position exactly as it
// always did, and StepOffset takes that snap back out of the drawing so the
// body is drawn where the last tick left it. And two ways the offset must NOT
// appear: a ramp, which is continuous motion already, and a change of frame or
// a teleport, where Position changes by any amount without a stride in it.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

const easeDT = float32(1.0 / 60)

// easeWalker is an AI-driven, gravity-on body with its feet at feet, the shape
// the ai package's own fixtures use, with the friendly rifleman's step height.
func easeWalker(id string, feet mgl32.Vec3) world.Entity {
	spec := world.DefaultAISpec()
	spec.StepHeight = 0.55
	return world.Entity{
		ID:           id,
		Position:     feet,
		StepAnchor:   feet,
		Scale:        mgl32.Vec3{1, 1, 1},
		BoundingKind: world.BoundingCylinder,
		BoundHalfX:   0.3, BoundHalfY: 0.9, BoundHalfZ: 0.3,
		BoundOffset: mgl32.Vec3{0, 0.9, 0},
		Visible:     true,
		Collision:   world.EntityCollision{World: true, Gravity: true, Movable: true},
		AI:          &spec,
	}
}

// Walking into a 0.5 sill: Position snaps up in one tick, the drawn body does
// not, and the gap is gone half a second after the walker stops.
func TestAWalkerSteppingOntoASillIsEasedInTheDrawing(t *testing.T) {
	lvl := stepRoom(4, block(0.5))
	lvl.Entities = []world.Entity{easeWalker("w", mgl32.Vec3{0, 0.1, 0})}
	w := &lvl.Entities[0]

	for i := 0; i < 30; i++ {
		EntityUpdate(lvl, easeDT, true, nil)
	}
	if w.StepOffset != 0 {
		t.Fatalf("settling on a flat floor left an offset of %v", w.StepOffset)
	}

	stepped := false
	for i := 0; i < 120 && !stepped; i++ {
		w.Velocity[0] = 2 // what the controller writes; the Y is gravity's
		before := w.Position.Y() + w.StepOffset
		prevY := w.Position.Y()
		EntityUpdate(lvl, easeDT, true, nil)
		jump := w.Position.Y() - prevY
		if jump < 0.3 {
			if w.StepOffset != 0 {
				t.Fatalf("tick %d: an offset of %v with no step (rose %v)", i, w.StepOffset, jump)
			}
			continue
		}
		stepped = true
		if w.StepOffset > -0.3 || w.StepOffset < -0.55 {
			t.Errorf("stepped up %v but StepOffset is %v, want about -%v", jump, w.StepOffset, jump)
		}
		if drawn := w.Position.Y() + w.StepOffset; drawn-before > 0.05 {
			t.Errorf("the drawn body still rose %v on the stepping tick", drawn-before)
		}
	}
	if !stepped {
		t.Fatalf("the walker never stepped onto the sill; feet at %v", w.Position)
	}
	if feetY := w.Position.Y(); feetY < 0.45 {
		t.Errorf("collision no longer snaps: feet at y=%v, want on the 0.5 sill", feetY)
	}

	w.Velocity[0] = 0
	for i := 0; i < 30; i++ { // half a second
		EntityUpdate(lvl, easeDT, true, nil)
	}
	if w.StepOffset != 0 {
		t.Errorf("StepOffset still %v half a second after the step", w.StepOffset)
	}
}

// A ramp is climbed a little every tick, not snapped, and must never read as a
// stride — a body walking uphill drawn sunk into the slope is the failure.
func TestAWalkerClimbingARampHasNoStepOffset(t *testing.T) {
	ramp := world.NewBoxBrush("ramp", mgl32.Vec3{0, 0.1, 0}, mgl32.Vec3{4, 0.1, 2}, "concrete")
	ramp.Verts[3][1] += 1.2
	ramp.Verts[7][1] += 1.2
	lvl := stepRoom(6, ramp)
	lvl.Entities = []world.Entity{easeWalker("w", mgl32.Vec3{-3, 0.5, 0})}
	w := &lvl.Entities[0]
	for i := 0; i < 60; i++ {
		EntityUpdate(lvl, easeDT, true, nil)
	}
	startY := w.Position.Y()
	var worst float32
	for i := 0; i < 180; i++ {
		w.Velocity[0] = 2
		EntityUpdate(lvl, easeDT, true, nil)
		if -w.StepOffset > worst {
			worst = -w.StepOffset
		}
	}
	if w.Position.Y()-startY < 0.5 {
		t.Fatalf("the walker never climbed the ramp: y %v -> %v", startY, w.Position.Y())
	}
	if worst > 0.03 {
		t.Errorf("walking up a ramp built a step offset of %v", worst)
	}
}

// Boarding a ship moves Position into a different coordinate system — tens of
// units, here — without the body going anywhere. That must neither make an
// offset nor throw away one a stride is still easing.
func TestBoardingAFrameNeitherMakesNorDropsAStepOffset(t *testing.T) {
	for _, start := range []float32{0, -0.3} {
		lvl := shipVehicleLevel(t)
		addRampDoorway(lvl)
		ship := world.FindEntity(lvl, "ship")
		ship.Position = mgl32.Vec3{50, 0, 20}
		ship.EnsureVehicleState()
		world.RefreshFrames(lvl, lvl.Frames)

		// Standing in the ramp from outside, clear of the cabin's walls.
		lvl.Entities = append(lvl.Entities, easeWalker("w", mgl32.Vec3{50, 0.5, 24.95}))
		w := world.FindEntity(lvl, "w")
		w.StepOffset = start
		w.UpdateLocation(lvl)

		EntityUpdate(lvl, easeDT, true, nil)
		w = world.FindEntity(lvl, "w")
		if w.Frame != "ship" {
			t.Fatalf("start %v: the walker did not board (frame %q, pos %v)", start, w.Frame, w.Position)
		}
		switch {
		case start == 0 && w.StepOffset != 0:
			t.Errorf("boarding made an offset of %v", w.StepOffset)
		case start != 0 && (w.StepOffset == 0 || w.StepOffset < start):
			t.Errorf("boarding mid-stride took StepOffset %v -> %v, want eased toward 0", start, w.StepOffset)
		}
		if w.StepAnchor != w.Position {
			t.Errorf("start %v: StepAnchor %v not refreshed to the boarded position %v", start, w.StepAnchor, w.Position)
		}
	}
}

// A script placing a body somewhere else mid-stride takes the stride with it:
// the body arrives drawn where it is.
func TestATeleportDropsTheStepOffset(t *testing.T) {
	lvl := &world.Level{Entities: []world.Entity{easeWalker("w", mgl32.Vec3{0, 0, 0})}}
	w := &lvl.Entities[0]
	w.Collision.Gravity = false // nothing under it; only the teleport is under test
	w.StepOffset = -0.4

	EntityUpdate(lvl, easeDT, true, nil)
	if w.StepOffset == 0 {
		t.Fatal("an ordinary tick dropped the offset rather than easing it")
	}

	w.Position = w.Position.Add(mgl32.Vec3{12, 0, 3}) // a `position` write
	EntityUpdate(lvl, easeDT, true, nil)
	if w.StepOffset != 0 {
		t.Errorf("a teleport kept a step offset of %v", w.StepOffset)
	}
}
