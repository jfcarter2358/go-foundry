package app

// vehicle_input.go — the keys, when the player is driving.
//
// The same WASD that walks a body steers a vehicle; which of the two it reaches
// depends on whether the player is sitting in a seat that hands over the
// controls. Routing rather than a second key set is deliberate: a player who has
// just sat down should not have to learn different keys to move.

import (
	"foundry/internal/collision"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
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

	if keys[sdl.SCANCODE_W] != 0 {
		c.Throttle += 1
	}
	if keys[sdl.SCANCODE_S] != 0 {
		c.Throttle -= 1
	}
	if keys[sdl.SCANCODE_A] != 0 {
		c.Steer -= 1
	}
	if keys[sdl.SCANCODE_D] != 0 {
		c.Steer += 1
	}
	c.Brake = keys[sdl.SCANCODE_SPACE] != 0

	switch v.Vehicle.Mode {
	case world.VehicleAir, world.VehicleSpace:
		// Q/E roll, and the mouse's own pitch drives the nose. Reading the
		// camera's pitch rather than a key is what makes flying feel like
		// aiming rather than like nudging.
		if keys[sdl.SCANCODE_Q] != 0 {
			c.Roll -= 1
		}
		if keys[sdl.SCANCODE_E] != 0 {
			c.Roll += 1
		}
		if keys[sdl.SCANCODE_LSHIFT] != 0 {
			c.Pitch -= 1
		}
		if keys[sdl.SCANCODE_LCTRL] != 0 {
			c.Pitch += 1
		}
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

	eye := world.EyeHeight
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
func PlayerMoveIntent(lvl *world.Level, keys []uint8) mgl32.Vec3 {
	if v := DrivenVehicle(lvl); v != nil {
		ReadVehicleControls(v, keys)
		// The look keys still have to run, or a driver cannot turn their head.
		return gfx.MoveIntent(keys).Mul(0)
	}
	return gfx.MoveIntent(keys)
}

// VehicleInteractRequest is the interact key's vehicle half, run before the
// script tick so a mount this frame is visible to it.
func VehicleInteractRequest(lvl *world.Level) bool {
	if world.PlayerEntity == nil {
		return false
	}
	return collision.VehicleInteract(lvl, world.PlayerEntity)
}
