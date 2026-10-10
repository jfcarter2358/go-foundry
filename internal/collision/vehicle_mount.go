package collision

// vehicle_mount.go — getting in, standing up, and getting out.
//
// Mounting is a change of *frame*, not a teleport. The occupant's position is
// converted out of whatever space it was in and into the vehicle's, so it ends
// up at the same place in the world it started — and from then on it is carried,
// because it stores its position in coordinates that move.
//
// The same conversion is what makes the two authored modes one system rather
// than two. Aboard-and-seated pins the occupant to a seat; aboard-and-standing
// lets it walk the deck. Sitting down and standing up are the same entity
// changing one string.

import (
	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// MountRange is how close an occupant must be to a seat to take it, in world
// units. Generous enough to reach a cockpit from beside the hull, tight enough
// that two vehicles parked together do not fight over the same key press.
const MountRange = float32(2.5)

// Mount puts an entity into a vehicle's seat.
//
// The position conversion is the whole of it: out of the entity's current frame
// into the vehicle's, so nothing jumps. Returns false when the seat does not
// exist, when the vehicle is not one, or when mounting would nest a frame inside
// itself — a ship cannot be driven into its own hangar.
func Mount(lvl *world.Level, e *world.Entity, vehicle *world.Entity, seat string) bool {
	if lvl == nil || e == nil || vehicle == nil || !vehicle.IsVehicle() {
		return false
	}
	if e.ID == vehicle.ID {
		return false
	}
	s := vehicle.Vehicle.Seat(seat)
	if seat != "" && s == nil {
		return false
	}
	if wouldCycle(lvl, e.ID, vehicle.ID) {
		return false
	}

	st := vehicle.EnsureVehicleState()

	// Carry the occupant's position through both frames so it stays where it
	// was in the world. Only meaningful when it is not being pinned to a seat
	// anyway, but doing it unconditionally keeps standing and sitting the same
	// operation.
	from := lvl.Frames.FrameFor(e)
	to := lvl.Frames.Frame(vehicle.ID)
	e.Position = to.FromWorld(from.ToWorld(e.Position))
	e.Velocity = to.DirFromWorld(from.DirToWorld(e.Velocity))

	e.Frame = vehicle.ID
	e.Seat = seat
	st.AddOccupant(e.ID, seat)

	if s != nil {
		PinToSeat(e, s)
	}
	hooks.RecordVehicleEvent(hooks.VehicleEvent{
		Atom: "mount", Vehicle: vehicle.ID, Occupant: e.ID, Seat: seat,
	})
	return true
}

// Dismount takes an entity out of whatever vehicle it is in, putting it down at
// its seat's exit point, and returns the vehicle it left.
func Dismount(lvl *world.Level, e *world.Entity) *world.Entity {
	if lvl == nil || e == nil || e.Frame == "" {
		return nil
	}
	vehicle := world.FindEntity(lvl, e.Frame)
	if vehicle == nil {
		// The vehicle is gone. Leave the entity in world space rather than in a
		// frame nothing drives.
		e.Frame, e.Seat = "", ""
		return nil
	}

	from := lvl.Frames.Frame(vehicle.ID)
	if s := vehicle.Vehicle.Seat(e.Seat); s != nil {
		e.Position = s.Exit
	}
	// Out of the vehicle's frame and into whatever frame the vehicle itself is
	// in — which is the world for an ordinary vehicle, and the carrier for a
	// shuttle leaving a hangar.
	outer := lvl.Frames.FrameFor(vehicle)
	e.Position = outer.FromWorld(from.ToWorld(e.Position))
	e.Velocity = outer.DirFromWorld(from.DirToWorld(e.Velocity))

	leftSeat := e.Seat
	e.Frame = vehicle.Frame
	e.Seat = ""
	if vehicle.VState != nil {
		vehicle.VState.RemoveOccupant(e.ID)
		vehicle.VState.Controls = world.VehicleControls{}
	}
	hooks.RecordVehicleEvent(hooks.VehicleEvent{
		Atom: "dismount", Vehicle: vehicle.ID, Occupant: e.ID, Seat: leftSeat,
	})
	return vehicle
}

// Sit moves an occupant already aboard into one of its vehicle's seats, and
// Stand takes it out of the seat without leaving the vehicle.
//
// Both are one field, which is the point: standing up on a moving ship is not a
// different kind of being-aboard, it is the same rider with no seat.
func Sit(lvl *world.Level, e *world.Entity, seat string) bool {
	if e == nil || e.Frame == "" {
		return false
	}
	v := world.FindEntity(lvl, e.Frame)
	if v == nil || !v.IsVehicle() {
		return false
	}
	s := v.Vehicle.Seat(seat)
	if s == nil {
		return false
	}
	e.Seat = seat
	v.EnsureVehicleState().AddOccupant(e.ID, seat)
	PinToSeat(e, s)
	return true
}

func Stand(lvl *world.Level, e *world.Entity) bool {
	if e == nil || e.Frame == "" || e.Seat == "" {
		return false
	}
	v := world.FindEntity(lvl, e.Frame)
	if v == nil || !v.IsVehicle() {
		return false
	}
	// Stand up where the seat is. Its Position is the occupant's feet, so this
	// puts them on the deck rather than inside the chair.
	if s := v.Vehicle.Seat(e.Seat); s != nil {
		e.Position = s.Position
	}
	e.Seat = ""
	e.Velocity = mgl32.Vec3{}
	// A body walked by keys carries a record of what they contributed, and
	// standing up is not a frame anything walked — see Entity.WalkVelocity.
	e.WalkVelocity = mgl32.Vec3{}
	v.EnsureVehicleState().AddOccupant(e.ID, "")
	if v.Driver() == "" {
		v.VState.Controls = world.VehicleControls{}
	}
	return true
}

// PinToSeat puts an entity exactly at its seat and stops it moving of its own
// accord. Called every frame for a seated occupant: a seat is a constraint, not
// a starting position.
func PinToSeat(e *world.Entity, s *world.VehicleSeat) {
	e.Position = s.Position
	e.Velocity = mgl32.Vec3{}
	e.WalkVelocity = mgl32.Vec3{}
	e.Collision.OnGround = true
	e.Collision.GroundedNext = true
}

// NearestSeat finds the closest seat on any vehicle within MountRange of a
// world-space point, and the vehicle carrying it.
//
// Nearest rather than a ray cast, for the same reason the interact event
// resolves by containment: there is no ray here, and "the seat you are standing
// next to" is what a player means by the key press.
func NearestSeat(lvl *world.Level, p mgl32.Vec3) (*world.Entity, *world.VehicleSeat, float32) {
	var bestV *world.Entity
	var bestS *world.VehicleSeat
	best := float32(-1)

	for _, v := range world.FindVehicles(lvl) {
		f := lvl.Frames.Frame(v.ID)
		for i := range v.Vehicle.Seats {
			s := &v.Vehicle.Seats[i]
			var wp mgl32.Vec3
			if f != nil {
				wp = f.ToWorld(s.Position)
			} else {
				// A seat-only vehicle owns no frame, so its seat is placed by
				// the vehicle's own transform.
				r := v.ModelMatrix().Mul4x1(s.Position.Vec4(1))
				wp = mgl32.Vec3{r.X(), r.Y(), r.Z()}
			}
			d := wp.Sub(p).Len()
			if d > MountRange {
				continue
			}
			if best < 0 || d < best {
				bestV, bestS, best = v, s, d
			}
		}
	}
	return bestV, bestS, best
}

// wouldCycle reports whether putting rider into host's frame would make a frame
// contain itself, directly or through any number of steps.
func wouldCycle(lvl *world.Level, riderID, hostID string) bool {
	seen := map[string]bool{}
	for id := hostID; id != ""; {
		if id == riderID {
			return true
		}
		if seen[id] {
			return true // already-broken graph; refuse rather than loop
		}
		seen[id] = true
		e := world.FindEntity(lvl, id)
		if e == nil {
			return false
		}
		id = e.Frame
	}
	return false
}

// VehicleInteract is the interact key's vehicle half: sit, stand, or get out.
//
// The precedence is what makes one key enough. A seat within reach always wins,
// because reaching for a seat is unambiguous. Failing that, someone aboard and
// standing gets out, and someone seated in a vehicle with somewhere to stand
// gets up rather than being ejected — a ship with a deck should let you leave
// the controls without leaving the ship.
func VehicleInteract(lvl *world.Level, e *world.Entity) bool {
	if lvl == nil || e == nil {
		return false
	}
	worldPos := lvl.Frames.WorldPosition(e)

	if v, s, _ := NearestSeat(lvl, worldPos); v != nil && s != nil {
		if e.Frame == v.ID && e.Seat == s.Name {
			// Already in this seat: the press means "get up", or "get out" if
			// there is nowhere to stand.
			if v.Vehicle.HasInterior() {
				return Stand(lvl, e)
			}
			return Dismount(lvl, e) != nil
		}
		if e.Frame == v.ID {
			return Sit(lvl, e, s.Name)
		}
		return Mount(lvl, e, v, s.Name)
	}

	if e.Frame != "" {
		if e.Seat != "" {
			v := world.FindEntity(lvl, e.Frame)
			if v != nil && v.IsVehicle() && v.Vehicle.HasInterior() {
				return Stand(lvl, e)
			}
		}
		return Dismount(lvl, e) != nil
	}
	return false
}
