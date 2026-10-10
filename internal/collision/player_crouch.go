package collision

// player_crouch.go — changing the player's body between standing and crouched.
//
// Going down is always allowed. Coming back up is only allowed when the space
// the taller body would take is free, so letting go of crouch under a low
// ceiling or a ship's hatch keeps the player crouched until they are out from
// under it, rather than pushing them through the geometry above.

import (
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// UpdatePlayerCrouch applies one frame of the crouch key: want is whether it is
// held. It changes p's half-height and position (keeping the feet in place),
// and eases the eye world.PlayerEyeHeight reports toward the stance's height.
func UpdatePlayerCrouch(p *world.Entity, lvl *world.Level, want bool, dt float32) {
	if p == nil || lvl == nil {
		return
	}
	eye := world.PlayerEyeHeight()

	switch {
	case want && p.BoundHalfY > world.CrouchHalfHeight:
		drop := p.BoundHalfY - world.CrouchHalfHeight
		p.BoundHalfY = world.CrouchHalfHeight
		p.Position[1] -= drop
		// The body's centre just dropped; the eye is measured from it, so it
		// gains what the centre lost and the view does not jump.
		eye += drop
	case !want && p.BoundHalfY < world.StandHalfHeight && roomToStand(p, lvl):
		rise := world.StandHalfHeight - p.BoundHalfY
		p.BoundHalfY = world.StandHalfHeight
		p.Position[1] += rise
		eye -= rise
	}

	target := world.EyeHeight
	if p.BoundHalfY < world.StandHalfHeight {
		target = world.CrouchEyeHeight
	}
	k := world.StanceEyeRate * dt
	if k > 1 {
		k = 1
	}
	eye += (target - eye) * k
	if d := target - eye; d < 1e-3 && d > -1e-3 {
		eye = target
	}
	world.SetPlayerEyeHeight(eye)
}

// roomToStand reports whether the slice of space between the crouched body's
// top and the standing body's top is free of anything solid.
func roomToStand(p *world.Entity, lvl *world.Level) bool {
	const gap = float32(0.02) // clear of the crouched body's own resting contacts
	top := p.Position[1] + p.BoundHalfY + gap
	standTop := p.Position[1] - p.BoundHalfY + 2*world.StandHalfHeight
	if standTop <= top {
		return true
	}
	half := (standTop - top) / 2
	probe := mgl32.Vec3{p.Position[0], top + half, p.Position[2]}

	radius := p.BoundHalfX
	if p.BoundHalfZ > radius {
		radius = p.BoundHalfZ
	}
	frame := lvl.Frames.FrameFor(p)
	chunks := solidChunksFor(lvl, frame, p.Position, probe, mgl32.Vec3{radius, half, radius}, 0, nil)
	if local, _, _ := pushOutOfChunksBrushes(chunks, probe, radius, half); local.Sub(probe).Len() > 1e-4 {
		return false
	}
	out, _, _ := resolveAgainstEntityBrushes(lvl, p, frame, probe, radius, half)
	return out.Sub(probe).Len() <= 1e-4
}
