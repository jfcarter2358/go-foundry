package collision

// vehicle_drive.go — how a vehicle moves, and the ordering that keeps its
// riders on the deck.
//
// One integrator per mode, all reading the same DriveSpec, all writing the same
// two pieces of state: a world-space velocity and a quaternion orientation. The
// quaternion is what makes 6DOF work — Euler angles cannot express a tumbling
// body without gimbal lock, and integrating an angular velocity as Euler rates
// is wrong even before it locks.
//
// The ordering below is load-bearing rather than tidy. Vehicles are integrated
// parents-first and their frames republished *before* anything inside them
// resolves, because a rider that resolves against last frame's transform is
// shoved through the deck the moment the ship pitches.

import (
	"foundry/internal/world"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// VehicleUpdate advances every vehicle in the level by one frame and brings the
// frames they carry up to date.
//
// motion is the same play gate EntityUpdate takes: in the editor a vehicle stays
// exactly where it was placed, so an author can put one down and have it stay
// put, while its animations keep running.
func VehicleUpdate(lvl *world.Level, dt float32, motion bool) {
	if lvl == nil {
		return
	}
	// Parents before children, so a shuttle in a hangar integrates against a
	// carrier that has already moved this frame rather than one that has not.
	// The frame order is exactly that ordering, so it is reused rather than
	// re-derived.
	seen := map[string]bool{}
	if lvl.Frames != nil {
		for _, f := range lvl.Frames.Order() {
			if e := world.FindEntity(lvl, f.OwnerID); e != nil && e.IsVehicle() {
				driveOne(lvl, e, dt, motion)
				seen[e.ID] = true
			}
		}
	}
	// Vehicles that carry no interior own no frame, so they are not in that
	// order at all. They still drive.
	for _, v := range world.FindVehicles(lvl) {
		if !seen[v.ID] {
			driveOne(lvl, v, dt, motion)
		}
	}

	// Republish every frame transform from the positions just written, before
	// MovePlayer or EntityUpdate reads one.
	world.RefreshFrames(lvl, lvl.Frames)
}

// driveOne integrates a single vehicle.
func driveOne(lvl *world.Level, e *world.Entity, dt float32, motion bool) {
	st := e.EnsureVehicleState()
	if st == nil || !motion || dt <= 0 {
		return
	}
	spec := e.Vehicle
	d := spec.Drive

	// --- orientation ---
	//
	// Controls are rates, not torques. There is no inertia tensor here and no
	// intention of adding one: a vehicle turns as fast as its tuning says, which
	// is what makes an arcade drive model predictable to author against.
	want := mgl32.Vec3{
		st.Controls.Pitch * d.PitchRate,
		st.Controls.Steer * d.TurnRate,
		st.Controls.Roll * d.RollRate,
	}
	switch spec.Mode {
	case world.VehicleSpace:
		// Nothing to push against: a rate command is an acceleration, and what
		// you set spinning stays spinning.
		st.AngVel = st.AngVel.Add(want.Mul(dt))
	default:
		// Everything else is in a medium, so the commanded rate is approached
		// and released rather than accumulated.
		st.AngVel = approachVec(st.AngVel, want, d.AngularDrag*dt+1e-6)
	}

	// Compose the frame's rotation in the vehicle's *own* axes, which is what
	// makes a roll roll about the nose rather than about the world's Z.
	if st.AngVel.Len() > 1e-8 {
		step := mgl32.AnglesToQuat(
			st.AngVel.Y()*dt, st.AngVel.X()*dt, st.AngVel.Z()*dt, mgl32.YXZ)
		st.Orient = st.Orient.Mul(step).Normalize()
	}

	// Ground and water vehicles stay upright by construction: their body may be
	// tilted to the surface for looks, but the frame an occupant stands in
	// keeps its own up, or walking around a cabin would fight the terrain.
	if spec.Mode == world.VehicleGround || spec.Mode == world.VehicleWater {
		st.Orient = flattenToYaw(st.Orient)
		st.AngVel[0], st.AngVel[2] = 0, 0
	}

	// Keep the authored Euler triple in step, so scripts, the panel and the
	// saved file keep working on the field they already know.
	e.Rotation = quatToEulerYXZ(st.Orient)

	// --- velocity ---
	forward := st.Orient.Rotate(mgl32.Vec3{0, 0, -1})
	up := st.Orient.Rotate(mgl32.Vec3{0, 1, 0})

	thrust := st.Controls.Throttle
	accel := d.Accel
	if thrust < 0 {
		accel = d.Brake
	}
	if st.Controls.Brake {
		e.Velocity = approachVec(e.Velocity, mgl32.Vec3{}, d.Brake*dt)
	}
	e.Velocity = e.Velocity.Add(forward.Mul(thrust * accel * dt))

	switch spec.Mode {
	case world.VehicleSpace:
		// No drag, no gravity. Only the throttle changes anything.
	case world.VehicleAir:
		// Lift trades forward speed for holding altitude: fly fast enough and
		// gravity stops mattering, slow down and you sink.
		speed := e.Velocity.Len()
		hold := speed * d.Lift
		e.Velocity = e.Velocity.Add(up.Mul(hold * dt))
		e.Velocity[1] -= world.EntityGravityAccel * dt
		e.Velocity = applyDrag(e.Velocity, d.Drag, dt)
	case world.VehicleWater:
		e.Velocity = applyDrag(e.Velocity, d.Drag, dt)
		e.Velocity[1] -= world.EntityGravityAccel * dt
	default: // ground
		e.Velocity = applyDrag(e.Velocity, d.Drag, dt)
		e.Velocity[1] -= world.EntityGravityAccel * dt
	}

	// Clamp the driven speed rather than the fall: a vehicle dropped off a
	// cliff should reach terminal velocity, not creep down at its top speed.
	clampHorizontal(&e.Velocity, d.MaxSpeed, d.Reverse, forward)

	// --- move, and resolve against the world ---
	//
	// A hull is an entity like any other, so it goes through the same swept
	// world resolve everything else does. That is also why a fast vehicle does
	// not pass through walls: the sweep in resolveEntityWorld is speed-aware.
	oldPos := e.Position
	e.Position = e.Position.Add(e.Velocity.Mul(dt))
	if e.CollidesWithWorld() {
		if hitFloor := resolveEntityWorld(lvl, e, oldPos); hitFloor {
			e.Collision.GroundedNext = true
			if spec.Mode == world.VehicleGround || spec.Mode == world.VehicleWater {
				// Hold the wheels down rather than letting the vehicle bounce
				// off every rise it drives over.
				if e.Velocity.Y() < 0 {
					e.Velocity[1] = 0
				}
			}
		}
	}
	e.UpdateLocation(lvl)
}

// approachVec moves v toward want by at most step per call, per axis. Used for
// rates that should be reached and released rather than accumulated.
func approachVec(v, want mgl32.Vec3, step float32) mgl32.Vec3 {
	d := want.Sub(v)
	if l := d.Len(); l <= step || l < 1e-9 {
		return want
	}
	return v.Add(d.Normalize().Mul(step))
}

// applyDrag sheds a fraction of velocity per second, framerate-independently.
// An exponential rather than a linear subtraction, so halving the frame time
// does not halve the drag.
func applyDrag(v mgl32.Vec3, drag, dt float32) mgl32.Vec3 {
	if drag <= 0 {
		return v
	}
	k := float32(math.Exp(float64(-drag * dt)))
	return mgl32.Vec3{v.X() * k, v.Y(), v.Z() * k}
}

// clampHorizontal limits how fast a vehicle may be driven, forwards and
// backwards, without touching how fast it may fall.
func clampHorizontal(v *mgl32.Vec3, maxSpeed, reverse float32, forward mgl32.Vec3) {
	flat := mgl32.Vec3{v.X(), 0, v.Z()}
	speed := flat.Len()
	if speed < 1e-6 {
		return
	}
	limit := maxSpeed
	if flat.Dot(mgl32.Vec3{forward.X(), 0, forward.Z()}) < 0 {
		limit = reverse
	}
	if limit <= 0 || speed <= limit {
		return
	}
	scaled := flat.Mul(limit / speed)
	v[0], v[2] = scaled.X(), scaled.Z()
}

// flattenToYaw drops the pitch and roll from an orientation, keeping only the
// heading. What a wheeled vehicle's frame does, so its cabin floor stays level
// however lumpy the ground under it is.
func flattenToYaw(q mgl32.Quat) mgl32.Quat {
	f := q.Rotate(mgl32.Vec3{0, 0, -1})
	yaw := float32(math.Atan2(float64(-f.X()), float64(-f.Z())))
	return mgl32.QuatRotate(yaw, mgl32.Vec3{0, 1, 0})
}

// quatToEulerYXZ writes a quaternion back into the Euler triple the rest of the
// engine reads, in the Y-then-X-then-Z order everything here agrees on.
func quatToEulerYXZ(q mgl32.Quat) mgl32.Vec3 {
	m := q.Mat4()
	// For R = Ry·Rx·Rz, the second column's Y term isolates the X rotation.
	sx := -m.At(1, 2)
	if sx > 1 {
		sx = 1
	}
	if sx < -1 {
		sx = -1
	}
	x := float32(math.Asin(float64(sx)))
	var y, z float32
	if math.Abs(float64(sx)) < 0.9999 {
		y = float32(math.Atan2(float64(m.At(0, 2)), float64(m.At(2, 2))))
		z = float32(math.Atan2(float64(m.At(1, 0)), float64(m.At(1, 1))))
	} else {
		// Straight up or straight down: yaw and roll are the same rotation, so
		// the split between them is arbitrary. Put it all in yaw.
		y = float32(math.Atan2(float64(-m.At(2, 0)), float64(m.At(0, 0))))
		z = 0
	}
	return mgl32.Vec3{x, y, z}
}
