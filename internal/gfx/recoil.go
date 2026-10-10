package gfx

// recoil.go — a shot kicking the player's view.
//
// A kick is not a jolt: it is banked and delivered over a few hundredths of a
// second, the way banked mouse motion is. Part of it stays — a held trigger
// climbs, and the player pulls it back down — and the rest eases back by
// itself once delivered. Pulling the view down while it recovers is spent
// against what is still owed, so a player compensating by hand is not
// dragged below where they were aiming.
//
// It turns Cam.Yaw and Cam.Pitch themselves, so the next shot's aim
// (Cam.Direction) is the kicked one.

import "math"

const (
	// recoilKickSeconds is how quickly a kick is delivered (time constant).
	recoilKickSeconds = 0.025
	// recoilRecoverRate is the share of the owed recovery given back a second
	// (exponential).
	recoilRecoverRate = 7.0
)

// Kick not yet delivered, and the part of that kick that will recover, in
// radians; then what has been delivered and is still to come back.
var (
	recoilPendPitch, recoilPendYaw   float32
	recoilPendBackP, recoilPendBackY float32
	recoilOwedPitch, recoilOwedYaw   float32
)

// AddRecoil kicks the view up by pitch and right by yaw, in degrees. recover
// (0–1) is the share of it that eases back on its own.
func AddRecoil(pitch, yaw, recover float32) {
	if recover < 0 {
		recover = 0
	}
	if recover > 1 {
		recover = 1
	}
	p, y := pitch*math.Pi/180, yaw*math.Pi/180
	recoilPendPitch += p
	recoilPendYaw += y
	recoilPendBackP += p * recover
	recoilPendBackY += y * recover
}

// ResetRecoil forgets every kick, delivered or not: the view could not move,
// or the session that fired is gone.
func ResetRecoil() {
	recoilPendPitch, recoilPendYaw = 0, 0
	recoilPendBackP, recoilPendBackY = 0, 0
	recoilOwedPitch, recoilOwedYaw = 0, 0
}

// AbsorbRecoilPitch is the player's own pitch change this frame (radians,
// positive up). A pull downward is taken off what the recovery still owes, so
// the two don't add up to more than the kick.
func AbsorbRecoilPitch(d float32) {
	if d < 0 && recoilOwedPitch > 0 {
		recoilOwedPitch += d
		if recoilOwedPitch < 0 {
			recoilOwedPitch = 0
		}
	}
}

// ApplyRecoil moves the camera by this frame's share of the kick and the
// recovery, and reports whether it moved it. The caller rebuilds the view.
func ApplyRecoil(dt float32) bool {
	if dt <= 0 || recoilPendPitch == 0 && recoilPendYaw == 0 && recoilOwedPitch == 0 && recoilOwedYaw == 0 {
		return false
	}
	kick := float32(1 - math.Exp(-float64(dt)/recoilKickSeconds))
	back := float32(1 - math.Exp(-float64(dt)*recoilRecoverRate))

	dp, dy := recoilPendPitch*kick, recoilPendYaw*kick
	bp, by := recoilPendBackP*kick, recoilPendBackY*kick
	recoilPendPitch -= dp
	recoilPendYaw -= dy
	recoilPendBackP -= bp
	recoilPendBackY -= by

	rp, ry := recoilOwedPitch*back, recoilOwedYaw*back
	recoilOwedPitch += bp - rp
	recoilOwedYaw += by - ry

	Cam.Pitch += dp - rp
	Cam.Yaw += dy - ry
	// A tail too small to see would otherwise run forever.
	const tiny = 1e-5
	if abs32(recoilPendPitch) < tiny && abs32(recoilPendYaw) < tiny {
		Cam.Pitch += recoilPendPitch
		Cam.Yaw += recoilPendYaw
		recoilOwedPitch += recoilPendBackP
		recoilOwedYaw += recoilPendBackY
		recoilPendPitch, recoilPendYaw, recoilPendBackP, recoilPendBackY = 0, 0, 0, 0
	}
	if abs32(recoilOwedPitch) < tiny && abs32(recoilOwedYaw) < tiny {
		Cam.Pitch -= recoilOwedPitch
		Cam.Yaw -= recoilOwedYaw
		recoilOwedPitch, recoilOwedYaw = 0, 0
	}
	if Cam.Pitch > MaxPitch {
		Cam.Pitch = MaxPitch
	}
	if Cam.Pitch < -MaxPitch {
		Cam.Pitch = -MaxPitch
	}
	return true
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
