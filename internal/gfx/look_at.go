package gfx

// look_at.go — a script turning the player's view toward a point (`look_at`).
//
// A cutscene that takes the controls away still has to point the camera at
// what it is about — a flag coming down, a door blowing in — and with control
// off the view otherwise stays wherever the player last left it. This eases
// Cam.Yaw and Cam.Pitch from where they were to the point over a set time,
// the way `recoil` turns them for a shot: the same two angles, so the next
// shot's aim and the listener follow without being told.
//
// The point is re-aimed at every frame rather than turned to once, so a view
// that moves while it turns (a body still settling, a ride) ends looking at
// the point rather than at where the point used to be from where it started.
//
// While player control is off the view then stays on the point until a
// script says otherwise. With control on the turn still runs, and the player
// gets the view back as soon as it arrives: holding it there against the
// mouse would be a fight the player cannot see the reason for.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

var (
	lookActive               bool
	lookTarget               mgl32.Vec3
	lookFromYaw, lookFromPit float32
	lookElapsed, lookSeconds float32
)

// StartLookAt turns the view toward target over seconds; 0 or less is at once.
func StartLookAt(target mgl32.Vec3, seconds float32) {
	lookActive = true
	lookTarget = target
	lookFromYaw, lookFromPit = Cam.Yaw, Cam.Pitch
	lookElapsed = 0
	if seconds < 0 {
		seconds = 0
	}
	lookSeconds = seconds
}

// StopLookAt lets go of the view where it is.
func StopLookAt() {
	lookActive = false
}

// LookingAt reports whether a look is holding the view.
func LookingAt() bool {
	return lookActive
}

// ApplyLookAt moves the view by this frame's share of the turn, and reports
// whether it moved it; the caller rebuilds the view. hold is whether the look
// may keep the view once it has arrived — true while player control is off.
func ApplyLookAt(dt float32, hold bool) bool {
	if !lookActive {
		return false
	}
	d := lookTarget.Sub(Cam.Position)
	flat := float32(math.Hypot(float64(d.X()), float64(d.Z())))
	if flat < 1e-4 && math.Abs(float64(d.Y())) < 1e-4 {
		// Standing on the point: nowhere to look.
		lookActive = false
		return false
	}
	wantYaw := float32(math.Atan2(float64(d.Z()), float64(d.X())))
	wantPitch := float32(math.Atan2(float64(d.Y()), float64(flat)))
	if wantPitch > MaxPitch {
		wantPitch = MaxPitch
	}
	if wantPitch < -MaxPitch {
		wantPitch = -MaxPitch
	}

	lookElapsed += dt
	t := float32(1)
	if lookSeconds > 0 && lookElapsed < lookSeconds {
		t = lookElapsed / lookSeconds
		t = t * t * (3 - 2*t) // smoothstep: starts and settles gently
	}
	// The short way round: a yaw from 170 degrees to -170 is twenty, not 340.
	dy := float64(wantYaw - lookFromYaw)
	dy = math.Mod(dy+math.Pi, 2*math.Pi)
	if dy < 0 {
		dy += 2 * math.Pi
	}
	dy -= math.Pi
	Cam.Yaw = lookFromYaw + float32(dy)*t
	Cam.Pitch = lookFromPit + (wantPitch-lookFromPit)*t

	if t >= 1 && !hold {
		lookActive = false
	}
	return true
}
