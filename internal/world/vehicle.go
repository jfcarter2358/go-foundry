package world

// vehicle.go — the thing a frame is usually carried by.
//
// A vehicle is an Entity with a `vehicle:` block, not a type of its own. An
// entity already carries an ID, tags, attributes, a script and its parameters, a
// transform, a velocity, bounds, collision flags and model attachments with
// animations — which is the whole of what a vehicle needs before anything
// vehicle-specific starts. This is the inverse of the argument for Volume: a
// volume is a *smaller* idea than an entity so it gets its own type, and a
// vehicle is a strictly larger one, so it extends rather than duplicates.
//
// Two things fall out of that. Scripting needs no new ref kind — `@id` and
// `#tag` already reach vehicles, and the runtime's ref cache already indexes
// them. And a lift becomes a vehicle: scripted movers raise a floor but nothing
// carries what is standing on it, while anything in a vehicle's frame is carried
// for free, because it never moved.

import (
	"github.com/go-gl/mathgl/mgl32"
)

// VehicleMode is how a vehicle moves, and therefore which integrator drives it.
type VehicleMode int

const (
	VehicleGround VehicleMode = iota
	VehicleAir
	VehicleWater
	VehicleSpace
)

// VehicleModeNames is the YAML/UI name of each mode, indexed by the mode — the
// same one-table arrangement BoundingTypeNames uses, and for the same reason: a
// per-file switch is what lets an unknown name quietly load as the wrong thing.
var VehicleModeNames = []string{"ground", "air", "water", "space"}

// VehicleModeName returns a mode's name, falling back to the default for
// anything out of range.
func VehicleModeName(m VehicleMode) string {
	if int(m) < 0 || int(m) >= len(VehicleModeNames) {
		return VehicleModeNames[VehicleGround]
	}
	return VehicleModeNames[m]
}

// VehicleModeFromName is the inverse. An unknown name loads as a ground vehicle
// rather than failing, so a level written by a newer build still opens.
func VehicleModeFromName(name string) VehicleMode {
	for i, n := range VehicleModeNames {
		if n == name {
			return VehicleMode(i)
		}
	}
	return VehicleGround
}

// VehicleSeat is a place aboard a vehicle that an occupant can be locked to.
//
// Everything here is in the vehicle's own coordinates, because that is where it
// stays put: a seat authored against the model has to remain under the pilot
// however the ship is oriented.
type VehicleSeat struct {
	Name string
	// Driver says whether sitting here hands the controls over. A vehicle with
	// no driver seat is furniture that happens to move.
	Driver bool

	Position mgl32.Vec3 // vehicle-local, the occupant's feet
	Yaw      float32    // rest facing, vehicle-local

	// YawLimit and PitchLimit are how far the occupant may look from that rest
	// facing, in radians. Zero means unrestricted, which is what a passenger in
	// an open vehicle wants.
	YawLimit   float32
	PitchLimit float32

	// Eye is how far above Position the view sits. Separate from the engine-wide
	// eye height because sitting is not standing.
	Eye float32

	// Exit is where an occupant is put down on leaving, in vehicle-local
	// coordinates. Authored rather than derived: the outside of a hull is not
	// something the engine can work out, and guessing puts people inside walls.
	Exit mgl32.Vec3
}

// DriveSpec is the tuning the built-in drive model reads.
//
// Every field is a plain number the level author sets, and every one is exposed
// to scripts as an entity property — "an engine model tuned by attributes" has
// to mean tunable while running, or it is just a hard-coded model with a
// configuration file.
type DriveSpec struct {
	MaxSpeed float32 // units per second
	Accel    float32 // units per second squared
	Brake    float32
	Reverse  float32 // max speed backwards; usually well under MaxSpeed

	TurnRate  float32 // radians per second, about the vehicle's own up
	PitchRate float32
	RollRate  float32

	Drag        float32 // fraction of speed shed per second with no throttle
	AngularDrag float32
	Lift        float32 // air: how much speed becomes altitude hold
	Buoyancy    float32 // water: how hard the surface pushes back

	// GroundStick is how firmly a ground vehicle is held to the surface, and
	// MaxClimbAngle how steep a slope it will take, in radians.
	GroundStick   float32
	MaxClimbAngle float32
}

// DefaultDriveSpec is a drivable vehicle of the given mode before any tuning.
//
// The defaults matter more than they look: an author who places a vehicle and
// presses play should get something that drives, not something inert that needs
// a dozen numbers before it will move at all.
func DefaultDriveSpec(mode VehicleMode) DriveSpec {
	d := DriveSpec{
		MaxSpeed: 14, Accel: 9, Brake: 18, Reverse: 5,
		TurnRate: 1.6, PitchRate: 1.2, RollRate: 2.0,
		Drag: 1.4, AngularDrag: 6,
		GroundStick: 12, MaxClimbAngle: 0.9,
	}
	switch mode {
	case VehicleAir:
		d.MaxSpeed, d.Accel, d.Drag = 40, 14, 0.5
		d.TurnRate, d.Lift = 1.0, 1.0
	case VehicleWater:
		d.MaxSpeed, d.Accel, d.Drag = 9, 4, 2.2
		d.TurnRate, d.Buoyancy = 0.9, 8
	case VehicleSpace:
		// Nothing to push against and nothing to slow you down: thrust and
		// rotation persist until something cancels them.
		d.MaxSpeed, d.Accel = 90, 20
		d.Drag, d.AngularDrag = 0, 0
		d.TurnRate, d.PitchRate, d.RollRate = 1.2, 1.2, 1.6
	}
	return d
}

// VehicleSpec is the authored half of a vehicle.
type VehicleSpec struct {
	Mode VehicleMode
	// Interiors are the chunks whose geometry is authored in this vehicle's
	// frame — the decks an occupant can stand up and walk around. A single
	// vehicle can carry several: a bridge and a cargo hold, or a floor and a
	// roof over the same footprint, are separate chunks sharing one
	// FrameOwner. Empty is a seat-only vehicle, which is a complete thing to
	// be.
	Interiors []string
	Seats     []VehicleSeat
	Drive     DriveSpec
}

// HasInterior reports whether this vehicle has anywhere to stand, as opposed
// to being seats only.
func (v *VehicleSpec) HasInterior() bool { return v != nil && len(v.Interiors) > 0 }

// CarriesChunk reports whether chunkID is one of this vehicle's interior
// chunks.
func (v *VehicleSpec) CarriesChunk(chunkID string) bool {
	if v == nil {
		return false
	}
	for _, id := range v.Interiors {
		if id == chunkID {
			return true
		}
	}
	return false
}

// AddInterior binds a chunk to this vehicle as one more deck, if it is not
// bound already.
func (v *VehicleSpec) AddInterior(chunkID string) {
	if v == nil || chunkID == "" || v.CarriesChunk(chunkID) {
		return
	}
	v.Interiors = append(v.Interiors, chunkID)
}

// RemoveInterior unbinds a chunk from this vehicle, if it was one of its
// decks.
func (v *VehicleSpec) RemoveInterior(chunkID string) {
	if v == nil {
		return
	}
	for i, id := range v.Interiors {
		if id == chunkID {
			v.Interiors = append(v.Interiors[:i], v.Interiors[i+1:]...)
			return
		}
	}
}

// Seat looks up a seat by name.
func (v *VehicleSpec) Seat(name string) *VehicleSeat {
	if v == nil {
		return nil
	}
	for i := range v.Seats {
		if v.Seats[i].Name == name {
			return &v.Seats[i]
		}
	}
	return nil
}

// DriverSeat is the first seat that hands over the controls, or nil.
func (v *VehicleSpec) DriverSeat() *VehicleSeat {
	if v == nil {
		return nil
	}
	for i := range v.Seats {
		if v.Seats[i].Driver {
			return &v.Seats[i]
		}
	}
	return nil
}

// VehicleControls is one frame of driving input, each axis in [-1, 1].
//
// Normalised rather than raw, so the same numbers arrive whether they came from
// a key that is held, a stick that is pushed, or a script that wrote them.
type VehicleControls struct {
	Throttle float32
	Steer    float32
	Pitch    float32
	Roll     float32
	Brake    bool
}

// Occupant is one entity aboard, and which seat it is in. An empty Seat means
// aboard but standing — walking the deck rather than strapped down.
type Occupant struct {
	EntityID string
	Seat     string
}

// VehicleState is a vehicle's runtime state. Never serialised: everything here
// is either derived from the authored transform or is this session's business.
type VehicleState struct {
	// Orient is authoritative. Entity.Rotation is Euler XYZ composed YXZ, which
	// is gimbal-lock-prone, and integrating an angular velocity as Euler rates
	// is simply wrong for a tumbling body. So the quaternion leads and
	// Entity.Rotation is written back from it each frame, which keeps scripts,
	// the editor panel and serialisation working on the field they already know.
	Orient mgl32.Quat

	// There is deliberately NO angular velocity here. It is Entity.AngularVelocity,
	// the same field every other entity spins on, read in the vehicle's own axes:
	// pitch about local X, yaw about local Y, roll about local Z. A second copy
	// here is what used to make `@ship.prop.angular_velocity = <...>` a write
	// nobody read — the drive model integrated its private one and overwrote
	// Entity.Rotation from the result — while the linear half worked, because
	// Entity.Velocity never had a duplicate to shadow it.

	Controls  VehicleControls
	Occupants []Occupant

	// Started is false until the first tick seeds Orient from Entity.Rotation.
	Started bool
}

// IsVehicle reports whether an entity carries a vehicle block.
func (e *Entity) IsVehicle() bool { return e != nil && e.Vehicle != nil }

// EnsureVehicleState creates the runtime half on first use and seeds the
// orientation from whatever the level authored.
func (e *Entity) EnsureVehicleState() *VehicleState {
	if e.Vehicle == nil {
		return nil
	}
	if e.VState == nil {
		e.VState = &VehicleState{}
	}
	if !e.VState.Started {
		e.VState.Orient = mgl32.Mat4ToQuat(EulerMatrixYXZ(e.Rotation))
		e.VState.Started = true
	}
	return e.VState
}

// Occupant returns the seat an entity is in aboard this vehicle, and whether it
// is aboard at all.
func (v *VehicleState) Occupant(entityID string) (string, bool) {
	if v == nil {
		return "", false
	}
	for _, o := range v.Occupants {
		if o.EntityID == entityID {
			return o.Seat, true
		}
	}
	return "", false
}

// AddOccupant records an entity as aboard, replacing any seat it already held.
func (v *VehicleState) AddOccupant(entityID, seat string) {
	if v == nil {
		return
	}
	for i := range v.Occupants {
		if v.Occupants[i].EntityID == entityID {
			v.Occupants[i].Seat = seat
			return
		}
	}
	v.Occupants = append(v.Occupants, Occupant{EntityID: entityID, Seat: seat})
}

// RemoveOccupant forgets an entity, and reports whether it was aboard.
func (v *VehicleState) RemoveOccupant(entityID string) bool {
	if v == nil {
		return false
	}
	for i := range v.Occupants {
		if v.Occupants[i].EntityID == entityID {
			v.Occupants = append(v.Occupants[:i], v.Occupants[i+1:]...)
			return true
		}
	}
	return false
}

// Driver is the entity currently in a seat that hands over the controls.
func (e *Entity) Driver() string {
	if e.Vehicle == nil || e.VState == nil {
		return ""
	}
	for _, o := range e.VState.Occupants {
		if s := e.Vehicle.Seat(o.Seat); s != nil && s.Driver {
			return o.EntityID
		}
	}
	return ""
}

// SeatMatrix is a seat's transform in the vehicle's own coordinates.
func SeatMatrix(s *VehicleSeat) mgl32.Mat4 {
	return mgl32.Translate3D(s.Position.X(), s.Position.Y(), s.Position.Z()).
		Mul4(mgl32.HomogRotate3DY(s.Yaw))
}

// FindVehicles returns every entity in the level that is a vehicle.
func FindVehicles(lvl *Level) []*Entity {
	if lvl == nil {
		return nil
	}
	var out []*Entity
	for i := range lvl.Entities {
		if lvl.Entities[i].IsVehicle() {
			out = append(out, &lvl.Entities[i])
		}
	}
	return out
}
