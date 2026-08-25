package collision

// vehicle_test.go — mounting, seats, driving, and walking a moving deck.
//
// The last test is the one the whole design exists for: the same walk must
// produce the same result whether the ship is parked at the origin or flying
// upside down half a mile away.

import (
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// shuttle builds a vehicle whose interior is collisionTestLevel's chunk, with a
// pilot's seat and a passenger's.
func shuttle(t *testing.T, mode world.VehicleMode) *world.Level {
	t.Helper()
	lvl := collisionTestLevel()
	lvl.Chunks[0].FrameOwner = "shuttle"
	lvl.Chunks[0].InferPortals()

	lvl.Entities = []world.Entity{{
		ID: "shuttle", Position: mgl32.Vec3{0, 0, 0}, Scale: mgl32.Vec3{1, 1, 1},
		BoundHalfX: 12, BoundHalfY: 5, BoundHalfZ: 6, Visible: true,
		Collision: world.EntityCollision{Movable: true},
		Vehicle: &world.VehicleSpec{
			Mode:     mode,
			Interior: "chunk",
			Drive:    world.DefaultDriveSpec(mode),
			Seats: []world.VehicleSeat{
				{Name: "pilot", Driver: true, Position: mgl32.Vec3{5, 0, 5}, Eye: 0.7,
					Exit: mgl32.Vec3{7, 0, 5}},
				{Name: "rear", Position: mgl32.Vec3{9, 0, 5}, Eye: 0.7,
					Exit: mgl32.Vec3{11, 0, 5}},
			},
		},
	}}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	return lvl
}

func flyTo(lvl *world.Level, pos mgl32.Vec3, q mgl32.Quat) {
	v := world.FindEntity(lvl, "shuttle")
	st := v.EnsureVehicleState()
	v.Position = pos
	st.Orient = q.Normalize()
	world.RefreshFrames(lvl, lvl.Frames)
}

func TestMountConvertsPositionAndLeavesTheWorldPointAlone(t *testing.T) {
	lvl := shuttle(t, world.VehicleSpace)
	flyTo(lvl, mgl32.Vec3{400, 90, -200}, mgl32.QuatRotate(1.1, mgl32.Vec3{0, 1, 0}))

	p := world.NewPlayer(mgl32.Vec3{0, 0, 0})
	lvl.Entities = append(lvl.Entities, *p)
	pl := world.FindEntity(lvl, "player")
	// Standing in the world, right where the shuttle's pilot seat happens to be.
	seatWorld := lvl.Frames.Frame("shuttle").ToWorld(mgl32.Vec3{5, 0, 5})
	pl.Position = seatWorld

	v := world.FindEntity(lvl, "shuttle")
	if !Mount(lvl, pl, v, "pilot") {
		t.Fatal("Mount refused a seat that exists")
	}
	if pl.Frame != "shuttle" || pl.Seat != "pilot" {
		t.Errorf("aboard state is frame=%q seat=%q", pl.Frame, pl.Seat)
	}
	// Pinned to the seat, in the shuttle's coordinates.
	if !vecNear(pl.Position, mgl32.Vec3{5, 0, 5}, 1e-3) {
		t.Errorf("pinned to %v, want the seat at [5 0 5]", pl.Position)
	}
	// And in world terms it did not jump.
	if got := lvl.Frames.WorldPosition(pl); !vecNear(got, seatWorld, 1e-2) {
		t.Errorf("mounting teleported the player from %v to %v", seatWorld, got)
	}
	if v.Driver() != "player" {
		t.Errorf("driver is %q, want player", v.Driver())
	}
}

func TestDismountReturnsToTheWorldAtTheExitPoint(t *testing.T) {
	lvl := shuttle(t, world.VehicleSpace)
	flyTo(lvl, mgl32.Vec3{100, 0, 0}, mgl32.QuatIdent())

	lvl.Entities = append(lvl.Entities, *world.NewPlayer(mgl32.Vec3{}))
	pl := world.FindEntity(lvl, "player")
	v := world.FindEntity(lvl, "shuttle")
	Mount(lvl, pl, v, "pilot")

	if got := Dismount(lvl, pl); got == nil || got.ID != "shuttle" {
		t.Fatal("Dismount did not report the vehicle left")
	}
	if pl.Frame != "" || pl.Seat != "" {
		t.Errorf("still aboard after dismounting: frame=%q seat=%q", pl.Frame, pl.Seat)
	}
	// The pilot's exit is local [7 0 5]; the shuttle is 100 out along X.
	if !vecNear(pl.Position, mgl32.Vec3{107, 0, 5}, 1e-3) {
		t.Errorf("stepped out at %v, want [107 0 5]", pl.Position)
	}
	if v.Driver() != "" {
		t.Error("the vehicle still has a driver after they got out")
	}
}

func TestSeatPinningHoldsThroughAFullRotation(t *testing.T) {
	lvl := shuttle(t, world.VehicleSpace)
	lvl.Entities = append(lvl.Entities, *world.NewPlayer(mgl32.Vec3{}))
	pl := world.FindEntity(lvl, "player")
	Mount(lvl, pl, world.FindEntity(lvl, "shuttle"), "pilot")

	for i := 0; i < 64; i++ {
		a := float32(i) / 64 * 2 * math.Pi
		flyTo(lvl, mgl32.Vec3{float32(i) * 3, 0, 0},
			mgl32.QuatRotate(a, mgl32.Vec3{0.3, 1, 0.5}))
		MovePlayer(pl, lvl, walkMove(mgl32.Vec3{0.4, 0, 0.4}))

		if !vecNear(pl.Position, mgl32.Vec3{5, 0, 5}, 1e-4) {
			t.Fatalf("step %d: a seated occupant drifted to %v", i, pl.Position)
		}
	}
}

func TestStandingUpAndSittingBackDown(t *testing.T) {
	lvl := shuttle(t, world.VehicleSpace)
	lvl.Entities = append(lvl.Entities, *world.NewPlayer(mgl32.Vec3{}))
	pl := world.FindEntity(lvl, "player")
	v := world.FindEntity(lvl, "shuttle")
	Mount(lvl, pl, v, "pilot")

	if !Stand(lvl, pl) {
		t.Fatal("could not stand up from a seat")
	}
	if pl.Frame != "shuttle" {
		t.Error("standing up left the vehicle entirely")
	}
	if pl.Seat != "" {
		t.Errorf("still seated after standing: %q", pl.Seat)
	}
	if v.Driver() != "" {
		t.Error("someone who stood up is still driving")
	}

	// Walk about the cabin, then sit back down.
	for i := 0; i < 20; i++ {
		MovePlayer(pl, lvl, walkMove(mgl32.Vec3{0.1, 0, 0}))
	}
	if vecNear(pl.Position, mgl32.Vec3{5, 0, 5}, 1e-3) {
		t.Error("walking after standing up did not move the player")
	}
	if !Sit(lvl, pl, "rear") {
		t.Fatal("could not sit in the rear seat")
	}
	if !vecNear(pl.Position, mgl32.Vec3{9, 0, 5}, 1e-4) {
		t.Errorf("sat down at %v, want the rear seat at [9 0 5]", pl.Position)
	}
}

func TestAVehicleCannotBeDrivenIntoItsOwnHangar(t *testing.T) {
	lvl := shuttle(t, world.VehicleSpace)
	// Make the shuttle itself a carrier's rider, then try to put the carrier
	// inside the shuttle.
	carrier := world.Entity{
		ID: "carrier", Scale: mgl32.Vec3{1, 1, 1},
		Vehicle: &world.VehicleSpec{
			Mode:  world.VehicleSpace,
			Drive: world.DefaultDriveSpec(world.VehicleSpace),
			Seats: []world.VehicleSeat{{Name: "bay", Position: mgl32.Vec3{}}},
		},
	}
	lvl.Entities = append(lvl.Entities, carrier)
	sh := world.FindEntity(lvl, "shuttle")
	sh.Frame = "carrier"
	_ = world.RebuildIndexes(lvl)

	if Mount(lvl, world.FindEntity(lvl, "carrier"), sh, "pilot") {
		t.Error("a carrier was mounted inside the shuttle it is carrying")
	}
}

func TestGroundVehicleDrivesAndStops(t *testing.T) {
	withCollisionMasters(t, true, true)
	lvl := shuttle(t, world.VehicleGround)
	v := world.FindEntity(lvl, "shuttle")
	v.Vehicle.Interior = ""
	v.Collision.World = false // no hull collision in this bare test level
	st := v.EnsureVehicleState()

	st.Controls.Throttle = 1
	for i := 0; i < 120; i++ {
		VehicleUpdate(lvl, 1.0/60.0, true)
	}
	flat := mgl32.Vec3{v.Velocity.X(), 0, v.Velocity.Z()}
	speed := flat.Len()
	if speed < 1 {
		t.Fatalf("full throttle for two seconds reached only %v", speed)
	}
	if speed > v.Vehicle.Drive.MaxSpeed*1.05 {
		t.Errorf("speed %v exceeded the %v limit", speed, v.Vehicle.Drive.MaxSpeed)
	}

	// Let go and drag brings it down.
	st.Controls.Throttle = 0
	for i := 0; i < 600; i++ {
		VehicleUpdate(lvl, 1.0/60.0, true)
	}
	rest := mgl32.Vec3{v.Velocity.X(), 0, v.Velocity.Z()}
	if got := rest.Len(); got > 0.2 {
		t.Errorf("still moving at %v ten seconds after the throttle was released", got)
	}
}

func TestGroundVehicleStaysUpright(t *testing.T) {
	lvl := shuttle(t, world.VehicleGround)
	v := world.FindEntity(lvl, "shuttle")
	v.Collision.World = false
	st := v.EnsureVehicleState()

	// Ask it to pitch and roll hard. A wheeled vehicle's frame must not take it,
	// or the cabin floor tilts out from under anyone standing on it.
	st.Controls = world.VehicleControls{Throttle: 1, Pitch: 1, Roll: 1, Steer: 1}
	for i := 0; i < 300; i++ {
		VehicleUpdate(lvl, 1.0/60.0, true)
	}
	up := st.Orient.Rotate(mgl32.Vec3{0, 1, 0})
	if up.Y() < 0.999 {
		t.Errorf("a ground vehicle tilted: its up is %v", up)
	}
	// But it did turn.
	if math.Abs(float64(v.Rotation.Y())) < 0.1 {
		t.Errorf("steering hard for five seconds turned it only %v radians", v.Rotation.Y())
	}
}

func TestSpaceVehicleRotatesFreelyWithoutGimbalLock(t *testing.T) {
	lvl := shuttle(t, world.VehicleSpace)
	v := world.FindEntity(lvl, "shuttle")
	v.Collision.World = false
	st := v.EnsureVehicleState()

	// Pitch straight up through the pole and keep going. Euler angles would
	// lock here; the quaternion does not.
	st.Controls.Pitch = 1
	for i := 0; i < 600; i++ {
		VehicleUpdate(lvl, 1.0/60.0, true)
		if q := st.Orient; math.IsNaN(float64(q.W)) || math.Abs(float64(q.Len()-1)) > 1e-3 {
			t.Fatalf("step %d: orientation went bad: %v", i, q)
		}
	}
	// It should have gone right over the top and come back round.
	up := st.Orient.Rotate(mgl32.Vec3{0, 1, 0})
	if math.Abs(float64(up.Len()-1)) > 1e-3 {
		t.Errorf("up vector is not a unit vector: %v", up)
	}
}

func TestQuaternionDoesNotDriftOverManySteps(t *testing.T) {
	lvl := shuttle(t, world.VehicleSpace)
	v := world.FindEntity(lvl, "shuttle")
	v.Collision.World = false
	st := v.EnsureVehicleState()
	st.Controls = world.VehicleControls{Pitch: 1, Steer: 1, Roll: 1}

	for i := 0; i < 10000; i++ {
		VehicleUpdate(lvl, 1.0/60.0, true)
	}
	if l := st.Orient.Len(); math.Abs(float64(l-1)) > 1e-3 {
		t.Errorf("after 10000 steps the orientation has length %v", l)
	}
	// And the Euler write-back still describes the same rotation.
	back := world.EulerMatrixYXZ(v.Rotation)
	fromQuat := st.Orient.Mat4()
	for i := 0; i < 16; i++ {
		if math.Abs(float64(back[i]-fromQuat[i])) > 1e-2 {
			t.Fatalf("Euler write-back diverged from the quaternion:\n%v\nvs\n%v", back, fromQuat)
		}
	}
}

// TestWalkingADeckIsTheSameWhereverTheShipIs is what the whole design is for.
func TestWalkingADeckIsTheSameWhereverTheShipIs(t *testing.T) {
	steps := []mgl32.Vec3{
		{0.1, 0, 0}, {0.1, 0, 0.05}, {0, 0, 0.1}, {-0.1, 0, 0.05}, {0, 0, 0},
	}

	run := func(pos mgl32.Vec3, q mgl32.Quat) mgl32.Vec3 {
		lvl := shuttle(t, world.VehicleSpace)
		lvl.Entities = append(lvl.Entities, *world.NewPlayer(mgl32.Vec3{}))
		pl := world.FindEntity(lvl, "player")
		Mount(lvl, pl, world.FindEntity(lvl, "shuttle"), "pilot")
		Stand(lvl, pl) // on the deck, free to walk
		flyTo(lvl, pos, q)

		for i := 0; i < 200; i++ {
			MovePlayer(pl, lvl, walkMove(steps[i%len(steps)]))
		}
		return pl.Position
	}

	parked := run(mgl32.Vec3{}, mgl32.QuatIdent())
	cases := []struct {
		name string
		pos  mgl32.Vec3
		q    mgl32.Quat
	}{
		{"far away", mgl32.Vec3{9000, -400, 2500}, mgl32.QuatIdent()},
		{"yawed", mgl32.Vec3{100, 0, 0}, mgl32.QuatRotate(2.1, mgl32.Vec3{0, 1, 0})},
		{"pitched and rolled", mgl32.Vec3{-50, 700, 30},
			mgl32.QuatRotate(0.9, mgl32.Vec3{1, 0, 0}).Mul(mgl32.QuatRotate(0.6, mgl32.Vec3{0, 0, 1}))},
		{"upside down", mgl32.Vec3{0, 300, 0}, mgl32.QuatRotate(math.Pi, mgl32.Vec3{0, 0, 1})},
	}
	for _, tc := range cases {
		if got := run(tc.pos, tc.q); !vecNear(got, parked, 1e-3) {
			t.Errorf("%s: the same walk ended at local %v, want %v", tc.name, got, parked)
		}
	}
}
