package sim

// vehicle_input.go — the keys (and the pad's sticks), when the player is driving.
//
// The same movement keys that walk a body steer a vehicle; which of the two it reaches
// depends on whether the player is sitting in a seat that hands over the
// controls. Routing rather than a second key set is deliberate: a player who has
// just sat down should not have to learn different keys to move. All of them go
// through the player's layout, platform.Bindings.

import (
	"math"

	"foundry/internal/collision"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/platform"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// DrivenVehicle is the vehicle the player is currently driving, or nil.
//
// Driving specifically, not merely riding: a passenger's keys still walk them
// around the cabin.
func DrivenVehicle(lvl *world.Level) *world.Entity {
	p := world.PlayerEntity
	if p == nil || p.Frame == "" || p.Seat == "" {
		return nil
	}
	v := world.FindEntity(lvl, p.Frame)
	if v == nil || !v.IsVehicle() {
		return nil
	}
	if s := v.Vehicle.Seat(p.Seat); s == nil || !s.Driver {
		return nil
	}
	return v
}

// ReadVehicleControls turns this frame's held keys into a vehicle's controls.
//
// Mouse look still moves the camera while driving — a pilot turning their head
// is not a control input — except in the modes where pitch and roll are how you
// fly, where the look angles *are* the stick.
func ReadVehicleControls(v *world.Entity, keys []uint8) {
	st := v.EnsureVehicleState()
	c := world.VehicleControls{}

	// Analog: a stick or trigger steers and throttles as far as it is pushed,
	// a key all the way.
	c.Throttle = platform.ActionValue(platform.ActMoveForward, keys) - platform.ActionValue(platform.ActMoveBack, keys)
	c.Steer = platform.ActionValue(platform.ActMoveRight, keys) - platform.ActionValue(platform.ActMoveLeft, keys)
	c.Brake = platform.ActionHeld(platform.ActJump, keys)

	switch v.Vehicle.Mode {
	case world.VehicleAir, world.VehicleSpace:
		// The roll keys (Q/E, or the bumpers, by default) roll, and the
		// view's own pitch — the mouse's or the look stick's — drives the
		// nose. Reading the camera's pitch rather than a key is what makes
		// flying feel like aiming rather than like nudging.
		c.Roll = platform.ActionValue(platform.ActRollRight, keys) - platform.ActionValue(platform.ActRollLeft, keys)
		c.Pitch = platform.ActionValue(platform.ActPitchUp, keys) - platform.ActionValue(platform.ActPitchDown, keys)
	}

	st.Controls = c
}

// SeatedCamera places the camera for an occupant riding in a frame, and reports
// whether it did.
//
// Two things differ from standing on the ground. The eye sits at the seat's own
// height rather than at the engine's standing one, because sitting is not
// standing. And the look angles are *frame-local*: yaw and pitch are measured
// against the vehicle's own axes and rotated into the world, so a barrel roll
// carries the view with it instead of leaving the horizon nailed to the sky.
func SeatedCamera(lvl *world.Level) bool {
	p := world.PlayerEntity
	if p == nil || p.Frame == "" {
		return false
	}
	f := lvl.Frames.Frame(p.Frame)
	if f == nil {
		return false
	}

	eye := world.PlayerEyeHeight()
	if v := world.FindEntity(lvl, p.Frame); v != nil && v.IsVehicle() && p.Seat != "" {
		if s := v.Vehicle.Seat(p.Seat); s != nil {
			eye = s.Eye
			clampLookToSeat(s)
		}
	}

	local := p.Position.Add(mgl32.Vec3{0, eye, 0})
	gfx.Cam.Position = f.ToWorld(local)
	gfx.Cam.Direction = f.DirToWorld(gfx.DirectionFromAngles(gfx.Cam.Yaw, gfx.Cam.Pitch))
	gfx.Cam.Up = f.DirToWorld(gfx.WorldUp)
	gfx.UpdateCamMatrix()
	return true
}

// PlacePlayerCamera puts the camera on the player, wherever the player is.
//
// It MUST run after world.RefreshFrames, and the frame loop calls it there for
// that reason. A rider's position is stored in coordinates that move, so the
// camera is only meaningful alongside the transform those coordinates are
// current for — and the geometry it is about to look at is drawn through
// exactly that transform. Placed any earlier, the camera is computed from the
// frame's position before the tick's last movers (EntityUpdate, and world
// collision on the vehicle itself) have had their say, while the hold around it
// is drawn from after: the eye and the room it is standing in disagree by
// however far the ship travelled this tick, every tick.
//
// On foot none of that applies and this is the world placement it always was.
func PlacePlayerCamera(lvl *world.Level) {
	if world.PlayerEntity == nil {
		return
	}
	// Riding in a frame puts the camera in that frame's space, with its own up
	// vector; on foot this falls through.
	if SeatedCamera(lvl) {
		return
	}
	gfx.Cam.Up = gfx.WorldUp
	gfx.Cam.Direction = gfx.DirectionFromAngles(gfx.Cam.Yaw, gfx.Cam.Pitch)
	gfx.Cam.Position = world.PlayerEntity.Position.Add(mgl32.Vec3{0, world.PlayerEyeHeight(), 0})
	gfx.UpdateCamMatrix()
}

// clampLookToSeat holds the view inside a seat's cone. A zero limit means
// unrestricted, which is what a passenger in an open vehicle wants.
func clampLookToSeat(s *world.VehicleSeat) {
	if s.YawLimit > 0 {
		gfx.Cam.Yaw = clampAngleAround(gfx.Cam.Yaw, s.Yaw, s.YawLimit)
	}
	if s.PitchLimit > 0 {
		gfx.Cam.Pitch = clampFloat(gfx.Cam.Pitch, -s.PitchLimit, s.PitchLimit)
	}
}

// clampAngleAround limits how far an angle may stray from a centre, measuring
// the difference the short way round so a cone spanning the ±π wrap works.
func clampAngleAround(a, centre, limit float32) float32 {
	d := wrapPi(a - centre)
	return centre + clampFloat(d, -limit, limit)
}

func wrapPi(a float32) float32 {
	const twoPi = float32(2 * 3.14159265358979)
	for a > twoPi/2 {
		a -= twoPi
	}
	for a < -twoPi/2 {
		a += twoPi
	}
	return a
}

func clampFloat(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// PlayerMoveIntent is the movement the player's keys ask for, in the player's
// own frame.
//
// gfx.MoveIntent builds the vector in the camera's yaw-relative basis with
// WorldUp as its vertical, which is the world's basis while the camera's own
// yaw is measured in the world. Riding in a frame, the yaw is measured in that
// frame — so the vector is already frame-local and needs no rotation. What it
// does need is to not exist at all while the controls are driving something.
//
// Nor while the player's control is switched off — and unlike driving, the look
// keys go quiet with it, so gfx.MoveIntent is not called at all. A driven
// vehicle gets its controls cleared rather than left alone, or it would keep
// doing whatever the keys last asked of it.
func PlayerMoveIntent(lvl *world.Level, keys []uint8) mgl32.Vec3 {
	if !platform.PlayerControlEnabled {
		if v := DrivenVehicle(lvl); v != nil {
			v.EnsureVehicleState().Controls = world.VehicleControls{}
		}
		return mgl32.Vec3{}
	}
	if v := DrivenVehicle(lvl); v != nil {
		ReadVehicleControls(v, keys)
		// The look keys still have to run, or a driver cannot turn their head.
		return gfx.MoveIntent(keys).Mul(0)
	}
	return cameraRelative(gfx.MoveIntent(keys))
}

// cameraRelative turns the move keys to the script camera's heading while one
// is detached: forward is into the screen, the way every third-person and
// fixed-angle game walks. gfx.MoveIntent built the vector on the player's own
// yaw, which the rig leaves alone and nobody can see while it is detached.
//
// On foot on the world only. Riding in a frame the intent is in the frame's
// coordinates, and the camera's heading is not.
func cameraRelative(intent mgl32.Vec3) mgl32.Vec3 {
	if !gfx.CameraDetached() || world.PlayerEntity == nil || world.PlayerEntity.Frame != "" {
		return intent
	}
	d := float64(gfx.Rig.Yaw - gfx.Cam.Yaw)
	s, c := float32(math.Sin(d)), float32(math.Cos(d))
	x, z := intent.X(), intent.Z()
	return mgl32.Vec3{x*c - z*s, intent.Y(), x*s + z*c}
}

// VehicleInteractRequest is the interact key's vehicle half, run before the
// script tick so a mount this frame is visible to it.
//
// It is a PRESS, taken from the edge the input handler recorded — not a state
// read once a frame. Without that gate this fires every tick for as long as the
// player is aboard with a seat in reach, and VehicleInteract's own precedence
// then alternates: seated with somewhere to stand means stand up, aboard and
// standing with a seat in reach means sit down. Sit, stand, sit, stand, sixty
// times a second. Nothing about the position moves — Sit and Stand both put the
// occupant at the seat — so it shows up only in the view, because a seated eye
// sits at the seat's own Eye and a standing one at world.EyeHeight: the camera
// hops a metre up and down every frame, in and out of the deck it is standing
// on, which reads as the interior strobing against whatever is outside the hull.
//
// A press that did nothing is handed back rather than swallowed, so `E` still
// reaches a scripted door when there is no seat within reach — the contract
// collision.VehicleInteract's own doc comment describes.
func VehicleInteractRequest(lvl *world.Level) bool {
	if world.PlayerEntity == nil {
		return false
	}
	if !hooks.ScriptInteractPressed() {
		return false
	}
	if collision.VehicleInteract(lvl, world.PlayerEntity) {
		return true
	}
	hooks.ScriptInteractRequest()
	return false
}

// crouchable reports whether the player is on foot and in control: the state
// crouching and sprinting both need. A driver's Ctrl and Shift are the
// vehicle's, and a seat has its own eye.
func crouchable(lvl *world.Level) bool {
	return platform.PlayerControlEnabled && world.PlayerEntity != nil &&
		world.PlayerEntity.Seat == "" && DrivenVehicle(lvl) == nil
}
