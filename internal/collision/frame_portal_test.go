package collision

// frame_portal_test.go — walking out of a moving thing.
//
// Three things to hold apart: whether two frames may currently be walked
// between (alignment), which side of a doorway an occupant is on (containment,
// in both directions), and what crossing does to their position, velocity and
// facing (the handoff).

import (
	"math"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// shipVehicleLevel is a ship entity carrying one frame-owned chunk (its cabin),
// alongside the world.
//
// The cabin holds a hollow room spanning x,z ∈ [-4,4] so that "inside the
// ship's chunks" is a question with an answer: Chunk.ChunkAt tests the chunk's
// bounds, and a chunk with no brushes has none.
func shipVehicleLevel(t *testing.T) *world.Level {
	t.Helper()
	cabin := world.Chunk{ID: "cabin", FrameOwner: "ship"}
	cabin.Brushes = world.NewHollowRoom("cabin_room",
		mgl32.Vec3{-4, 0, -4}, mgl32.Vec3{4, 3, 4}, 0.5, "concrete")
	cabin.RecomputeBounds()
	lvl := &world.Level{
		Chunks: []world.Chunk{cabin},
		Entities: []world.Entity{{
			ID: "ship", Scale: mgl32.Vec3{1, 1, 1},
			Vehicle: &world.VehicleSpec{Mode: world.VehicleSpace, Interiors: []string{"cabin"},
				Drive: world.DefaultDriveSpec(world.VehicleSpace)},
		}},
	}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	return lvl
}

// addRampDoorway gives the ship a doorway volume at its own local (0, 1, 4) —
// the hatch in its +Z wall — opening onto the world. Two units deep along Z,
// which is what makes it survive a step; see volume_doorway.go on why depth is
// the trade containment makes.
func addRampDoorway(lvl *world.Level) *world.Volume {
	lvl.Volumes = append(lvl.Volumes, world.Volume{
		ID:       "ramp",
		Frame:    "ship",
		Shape:    world.VolumeBox,
		Position: mgl32.Vec3{0, 1, 4},
		HalfX:    1.5,
		HalfY:    1,
		HalfZ:    1,
		Enabled:  true,
		Doorway:  &world.FrameDoorway{},
	})
	return &lvl.Volumes[len(lvl.Volumes)-1]
}

// aPlayerIn adds a player to lvl standing at pos in the named frame.
func aPlayerIn(lvl *world.Level, frame string, pos mgl32.Vec3) *world.Entity {
	p := world.NewPlayer(pos)
	p.Frame = frame
	lvl.Entities = append(lvl.Entities, *p)
	pl := world.FindEntity(lvl, "player")
	pl.UpdateLocation(lvl)
	return pl
}

// ---------- which side of the doorway ----------

// Standing in a ship's doorway volume from outside is boarding it. This is the
// whole of the "in" direction: containment, with no proximity band and no
// hysteresis, because inside and outside cannot both be true.
func TestStandingInADoorwayBoardsTheShip(t *testing.T) {
	lvl := shipVehicleLevel(t)
	addRampDoorway(lvl)
	ship := world.FindEntity(lvl, "ship")
	ship.Position = mgl32.Vec3{50, 0, 20}
	ship.EnsureVehicleState()
	world.RefreshFrames(lvl, lvl.Frames)

	// The ramp is at the ship's local (0,1,4), so its world position is the
	// ship's own plus that.
	pl := aPlayerIn(lvl, "", mgl32.Vec3{50, 1, 24})
	c := FindFramePortalCrossing(lvl, pl)
	if c == nil {
		t.Fatal("standing in the ramp did not find a crossing")
	}
	if c.FromID != "" || c.ToID != "ship" {
		t.Errorf("crossing goes %q -> %q, want world -> ship", c.FromID, c.ToID)
	}

	// A step to one side of it is not standing in it.
	pl.Position = mgl32.Vec3{50, 1, 30}
	pl.UpdateLocation(lvl)
	if c := FindFramePortalCrossing(lvl, pl); c != nil {
		t.Errorf("standing well clear of the ramp found a crossing to %q", c.ToID)
	}
}

// A doorway pointing at the sky is not a way in. Nothing is sealed on this side
// — the occupant is outside the ship and its hull is what stops them — so the
// boundary simply does not admit.
func TestAMisalignedShipCannotBeBoarded(t *testing.T) {
	lvl := shipVehicleLevel(t)
	addRampDoorway(lvl)
	ship := world.FindEntity(lvl, "ship")
	st := ship.EnsureVehicleState()
	st.Orient = mgl32.QuatRotate(math.Pi/2, mgl32.Vec3{0, 0, 1})
	world.RefreshFrames(lvl, lvl.Frames)

	// Wherever the rolled ship's ramp has ended up, standing in it admits
	// nobody.
	wp := lvl.Frames.Frame("ship").ToWorld(mgl32.Vec3{0, 1, 4})
	pl := aPlayerIn(lvl, "", wp)
	if c := FindFramePortalCrossing(lvl, pl); c != nil {
		t.Errorf("a rolled ship admitted a boarder to %q", c.ToID)
	}
}

// The "out" direction: aboard, and inside none of the ship's chunks. That is
// antisymmetric with the rule above by construction, which is what makes the
// two safe to run in the same tick without an occupant being traded back and
// forth forever.
func TestSteppingOffTheEndOfAShipLeavesIt(t *testing.T) {
	lvl := shipVehicleLevel(t)
	addRampDoorway(lvl)
	ship := world.FindEntity(lvl, "ship")
	ship.EnsureVehicleState()
	world.RefreshFrames(lvl, lvl.Frames)

	// Inside the cabin: still aboard.
	pl := aPlayerIn(lvl, "ship", mgl32.Vec3{0, 1, 0})
	if c := FindFramePortalCrossing(lvl, pl); c != nil {
		t.Fatalf("standing in the cabin found a crossing to %q", c.ToID)
	}

	// Off the end of it: out into the world.
	pl.Position = mgl32.Vec3{0, 1, 40}
	pl.UpdateLocation(lvl)
	c := FindFramePortalCrossing(lvl, pl)
	if c == nil {
		t.Fatal("walking off the ship found no crossing")
	}
	if c.FromID != "ship" || c.ToID != "" {
		t.Errorf("crossing goes %q -> %q, want ship -> world", c.FromID, c.ToID)
	}
}

// A ship with no doorway is sealed: its contents stay in it however far they
// wander off its floors, rather than being dropped into the world by walking
// into a wall.
func TestAShipWithNoDoorwayKeepsItsOccupants(t *testing.T) {
	lvl := shipVehicleLevel(t)
	ship := world.FindEntity(lvl, "ship")
	ship.EnsureVehicleState()
	world.RefreshFrames(lvl, lvl.Frames)

	pl := aPlayerIn(lvl, "ship", mgl32.Vec3{0, 1, 40})
	if c := FindFramePortalCrossing(lvl, pl); c != nil {
		t.Errorf("a doorless ship let its occupant out to %q", c.ToID)
	}
}

// Strapped in is not walking. A seat is a constraint rather than a place, and
// one authored outside the interior's own chunks — a cockpit on a nose — would
// otherwise read as its occupant stepping off the deck every tick.
func TestASeatedOccupantNeverCrosses(t *testing.T) {
	lvl := shipVehicleLevel(t)
	addRampDoorway(lvl)
	ship := world.FindEntity(lvl, "ship")
	ship.EnsureVehicleState()
	world.RefreshFrames(lvl, lvl.Frames)

	pl := aPlayerIn(lvl, "ship", mgl32.Vec3{0, 1, 40})
	pl.Seat = "pilot"
	if c := FindFramePortalCrossing(lvl, pl); c != nil {
		t.Errorf("a seated occupant crossed to %q", c.ToID)
	}
}

// A disabled doorway is a locked ramp: the volume is still there, and nobody
// goes through it either way.
func TestADisabledDoorwayAdmitsNobody(t *testing.T) {
	lvl := shipVehicleLevel(t)
	door := addRampDoorway(lvl)
	door.Enabled = false
	ship := world.FindEntity(lvl, "ship")
	ship.EnsureVehicleState()
	world.RefreshFrames(lvl, lvl.Frames)

	pl := aPlayerIn(lvl, "", mgl32.Vec3{0, 1, 4})
	if c := FindFramePortalCrossing(lvl, pl); c != nil {
		t.Errorf("a locked ramp admitted a boarder to %q", c.ToID)
	}
	// And a locked ramp is not a way out either — the ship is sealed while it
	// is shut.
	pl.Frame = "ship"
	pl.Position = mgl32.Vec3{0, 1, 40}
	pl.UpdateLocation(lvl)
	if c := FindFramePortalCrossing(lvl, pl); c != nil {
		t.Errorf("a locked ramp let an occupant out to %q", c.ToID)
	}
}

// ---------- sealing ----------

// While the ship is rolled, the doorway is solid: a body standing in it is
// pushed out rather than being allowed to walk through a hatch pointing at the
// sky. The doorway volume IS the opening, so its boundary is where the wall is.
func TestASealedDoorwayPushesABodyOut(t *testing.T) {
	lvl := shipVehicleLevel(t)
	addRampDoorway(lvl)
	ship := world.FindEntity(lvl, "ship")
	st := ship.EnsureVehicleState()
	world.RefreshFrames(lvl, lvl.Frames)

	pl := aPlayerIn(lvl, "ship", mgl32.Vec3{0, 1, 4})
	inside := pl.Position

	// Level: the doorway is a hole, and nothing pushes.
	if got := ResolveSealedPortals(lvl, pl, inside); got != inside {
		t.Errorf("an open doorway pushed the body from %v to %v", inside, got)
	}

	// Rolled: the doorway is a wall.
	st.Orient = mgl32.QuatRotate(math.Pi/2, mgl32.Vec3{0, 0, 1})
	world.RefreshFrames(lvl, lvl.Frames)
	got := ResolveSealedPortals(lvl, pl, inside)
	if got == inside {
		t.Fatal("a sealed doorway left the body standing in it")
	}
	// Out through the nearest side. The body stands at the volume's centre and
	// the box is 3 across in X and 2 in Z, so Z is the shallower overlap and
	// the way out — which is the right answer for a hatch: through the opening
	// rather than sideways into the hull beside it.
	if got.Y() != inside.Y() {
		t.Errorf("the push moved the body vertically: %v -> %v", inside, got)
	}
	if got.X() != inside.X() {
		t.Errorf("the push moved the body along the wider axis: %v -> %v", inside, got)
	}
	// Clear of the doorway's own Z extent, plus the body's radius.
	if got.Z() > inside.Z()-1 {
		t.Errorf("the body is still inside the doorway's Z extent: %v", got)
	}
}

// ---------- alignment and the handoff ----------

func TestFramesAlignedFollowsTheUpVectors(t *testing.T) {
	lvl := shipVehicleLevel(t)
	ship := world.FindEntity(lvl, "ship")
	st := ship.EnsureVehicleState()
	worldFrame := lvl.Frames.World()

	// Level, and yawed: both fine.
	for _, q := range []mgl32.Quat{mgl32.QuatIdent(), mgl32.QuatRotate(2.0, mgl32.Vec3{0, 1, 0})} {
		st.Orient = q
		world.RefreshFrames(lvl, lvl.Frames)
		if !FramesAligned(lvl.Frames.Frame("ship"), worldFrame) {
			t.Errorf("a yaw broke alignment: %v", q)
		}
	}
	// Rolled onto its side, and inverted: not fine.
	for _, q := range []mgl32.Quat{
		mgl32.QuatRotate(math.Pi/2, mgl32.Vec3{0, 0, 1}),
		mgl32.QuatRotate(math.Pi, mgl32.Vec3{1, 0, 0}),
	} {
		st.Orient = q
		world.RefreshFrames(lvl, lvl.Frames)
		if FramesAligned(lvl.Frames.Frame("ship"), worldFrame) {
			t.Errorf("a roll of %v was still considered aligned", q)
		}
	}
}

func TestCrossingAFramePortalPreservesWorldPositionAndFacing(t *testing.T) {
	lvl := shipVehicleLevel(t)
	ship := world.FindEntity(lvl, "ship")
	st := ship.EnsureVehicleState()
	// Docked somewhere well away from the origin and turned, but level.
	ship.Position = mgl32.Vec3{300, 25, -140}
	st.Orient = mgl32.QuatRotate(1.3, mgl32.Vec3{0, 1, 0})
	world.RefreshFrames(lvl, lvl.Frames)

	p := world.NewPlayer(mgl32.Vec3{2, 0.9, 1})
	p.Frame = "ship"
	lvl.Entities = append(lvl.Entities, *p)
	pl := world.FindEntity(lvl, "player")
	pl.Velocity = mgl32.Vec3{2, 0, 0}
	pl.UpdateLocation(lvl)

	beforePos := lvl.Frames.WorldPosition(pl)
	beforeVel := lvl.Frames.FrameFor(pl).DirToWorld(pl.Velocity)
	const yaw = float32(0.4)
	beforeDir := lvl.Frames.FrameFor(pl).DirToWorld(mgl32.Vec3{
		float32(math.Cos(float64(yaw))), 0, float32(math.Sin(float64(yaw)))})

	c := &FramePortalCrossing{FromID: "ship", ToID: ""}
	newYaw := CrossFramePortal(lvl, pl, c, yaw)

	if pl.Frame != "" {
		t.Errorf("after crossing to the world the player is in frame %q", pl.Frame)
	}
	if got := lvl.Frames.WorldPosition(pl); !vecNear(got, beforePos, 1e-3) {
		t.Errorf("the crossing moved the player: %v -> %v", beforePos, got)
	}
	if got := lvl.Frames.FrameFor(pl).DirToWorld(pl.Velocity); !vecNear(got, beforeVel, 1e-3) {
		t.Errorf("the crossing changed world velocity: %v -> %v", beforeVel, got)
	}
	afterDir := mgl32.Vec3{float32(math.Cos(float64(newYaw))), 0, float32(math.Sin(float64(newYaw)))}
	if !vecNear(afterDir, beforeDir, 1e-3) {
		t.Errorf("the crossing turned the player: facing %v -> %v", beforeDir, afterDir)
	}
}
