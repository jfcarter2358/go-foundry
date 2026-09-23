package sim

// vehicle_interact_test.go — the interact key is a press, not a state.
//
// The failure this guards is not subtle once you know it, and invisible until
// then. VehicleInteract's precedence says a seated occupant with somewhere to
// stand gets up, and one aboard-and-standing with a seat in reach sits down.
// Run that once per frame instead of once per press and it simply alternates,
// sixty times a second — and because Sit and Stand both put the occupant AT the
// seat, nothing moves. It shows up only in the view, where a seated eye sits at
// the seat's own Eye and a standing one at world.EyeHeight.

import (
	"testing"

	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// seatedShipLevel is a ship with a walkable deck and one seat, the player
// aboard it and standing on that deck within reach of the seat.
func seatedShipLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := &world.Level{
		Chunks: []world.Chunk{{ID: "hold", FrameOwner: "ship", Scale: 1}},
		Entities: []world.Entity{{
			ID: "ship", Position: mgl32.Vec3{0, 0, 0}, Scale: mgl32.Vec3{1, 1, 1},
			Vehicle: &world.VehicleSpec{
				Mode:      world.VehicleSpace,
				Interiors: []string{"hold"},
				Drive:     world.DefaultDriveSpec(world.VehicleSpace),
				Seats:     []world.VehicleSeat{{Name: "driver", Eye: 0.7}},
			},
		}},
	}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	prev := world.PlayerEntity
	t.Cleanup(func() {
		world.PlayerEntity = prev
		hooks.ScriptInteractPressed() // drain anything this test left pending
	})
	world.PlayerEntity = world.NewPlayer(mgl32.Vec3{0, 0, 0}) // on the deck, at the seat
	world.PlayerEntity.Frame = "ship"
	world.PlayerEntity.Seat = ""
	lvl.Entities[0].EnsureVehicleState().AddOccupant("player", "")
	return lvl
}

// The bug, stated as the property it broke: no press, no change. Before the
// gate this alternated seat/no-seat on every single call.
func TestTheInteractKeyDoesNothingWhenNobodyPressedIt(t *testing.T) {
	lvl := seatedShipLevel(t)
	hooks.ScriptInteractPressed() // ensure nothing is pending

	for i := 0; i < 30; i++ {
		if VehicleInteractRequest(lvl) {
			t.Fatalf("tick %d: acted on a key nobody pressed", i)
		}
		if world.PlayerEntity.Seat != "" {
			t.Fatalf("tick %d: sat down on its own (seat=%q)", i, world.PlayerEntity.Seat)
		}
	}
}

// And a press acts exactly once, however many frames it is held across.
func TestOneInteractPressSitsDownOnce(t *testing.T) {
	lvl := seatedShipLevel(t)

	hooks.ScriptInteractRequest()
	if !VehicleInteractRequest(lvl) {
		t.Fatal("a press with a seat in reach did nothing")
	}
	if world.PlayerEntity.Seat != "driver" {
		t.Fatalf("after one press, seat=%q, want the driver's", world.PlayerEntity.Seat)
	}
	// Held down, or simply more frames: the press is spent.
	for i := 0; i < 30; i++ {
		if VehicleInteractRequest(lvl) {
			t.Fatalf("tick %d: the same press acted twice", i)
		}
	}
	if world.PlayerEntity.Seat != "driver" {
		t.Errorf("stood back up without a second press (seat=%q)", world.PlayerEntity.Seat)
	}

	// A second press stands up again — the toggle still works, on presses.
	hooks.ScriptInteractRequest()
	if !VehicleInteractRequest(lvl) {
		t.Fatal("a second press did nothing")
	}
	if world.PlayerEntity.Seat != "" {
		t.Errorf("a second press left seat=%q, want standing", world.PlayerEntity.Seat)
	}
}

// A press the vehicle half had no use for has to reach the script tick, or `E`
// stops opening doors the moment the level contains a vehicle.
func TestAnUnusedInteractPressIsHandedBack(t *testing.T) {
	lvl := seatedShipLevel(t)
	// Out on foot, nowhere near a seat.
	world.PlayerEntity.Frame = ""
	world.PlayerEntity.Position = mgl32.Vec3{100, 0, 100}

	hooks.ScriptInteractRequest()
	if VehicleInteractRequest(lvl) {
		t.Fatal("claimed a press with no seat in reach")
	}
	if !hooks.ScriptInteractPressed() {
		t.Error("the press was swallowed; a scripted door would never see it")
	}
}
