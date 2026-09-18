package world

// player_stance.go — standing and crouching.
//
// The player's body is a cylinder centred on PlayerEntity.Position, and the eye
// sits EyeHeight above that centre (see spawnBodyLift). Crouching is a shorter
// cylinder whose centre drops by exactly what its half-height lost, so the feet
// stay where they were, and a lower eye above it. collision.UpdatePlayerCrouch
// decides when the body may change shape; this file only holds the numbers and
// the eased eye the camera reads.

// Stance dimensions, in the same units as the standing body (half-height 0.9,
// eye 1.7 above its centre).
const (
	// CrouchHalfHeight is the crouched body's half-height.
	CrouchHalfHeight = float32(0.5)
	// CrouchEyeHeight is how far above the crouched body's centre the eye is.
	// With the centre 0.4 lower, the eye comes down 1.0 in all.
	CrouchEyeHeight = float32(1.1)
	// CrouchSpeedScale multiplies walking speed while crouched.
	CrouchSpeedScale = float32(0.45)
	// StanceEyeRate is how quickly the eye eases to its stance's height, as the
	// fraction of the remaining gap closed per second (exponential).
	StanceEyeRate = float32(12)
)

// SprintSpeedScale multiplies walking speed while sprinting.
const SprintSpeedScale = float32(1.65)

// playerSprinting is whether the player is sprinting right now. The sprint key
// is held, not toggled: the frame loop hands UpdatePlayerSprint whether the
// player is asking to sprint (the key down, pushing forward, on foot) every
// frame, and the sprint lasts exactly as long as that ask does.
var playerSprinting bool

// sprintWanted is the ask as of the last UpdatePlayerSprint.
var sprintWanted bool

// sprintHeldOff is a script's "not now" (@player.prop.sprinting = false —
// firing, aiming). It keeps the sprint off while the key stays down, and lifts
// when the key comes up or the script writes true again.
var sprintHeldOff bool

// PlayerSprinting reports whether the player is sprinting.
func PlayerSprinting() bool { return playerSprinting }

// UpdatePlayerSprint is the frame loop's once-a-frame say: want is whether the
// player is holding the sprint key down while able to sprint.
func UpdatePlayerSprint(want bool) {
	sprintWanted = want
	if !want {
		sprintHeldOff = false
	}
	playerSprinting = want && !sprintHeldOff && !PlayerCrouched()
}

// SetPlayerSprinting is a script's hand on the sprint. false ends it and holds
// it off for as long as the key stays down; true lifts that hold, so a player
// still holding the key is sprinting again at once. Neither can start a sprint
// the player is not asking for, and a crouched player never sprints: the two
// are different speeds for the same legs.
func SetPlayerSprinting(on bool) {
	sprintHeldOff = !on
	playerSprinting = on && sprintWanted && !PlayerCrouched()
}

// StandHalfHeight is the standing body's half-height.
const StandHalfHeight = playerHalfHeight

// playerEye is the eased height of the eye above the player's body centre.
// Zero means "not yet set", which PlayerEyeHeight reads as standing.
var playerEye float32

// PlayerCrouched reports whether the player's body is currently the crouched
// one.
func PlayerCrouched() bool {
	return PlayerEntity != nil && PlayerEntity.BoundHalfY < StandHalfHeight-1e-4
}

// PlayerEyeHeight is where the camera goes above PlayerEntity.Position: eased
// between the standing and crouched eyes, so a crouch lowers the view rather
// than cutting it.
func PlayerEyeHeight() float32 {
	if playerEye == 0 {
		return EyeHeight
	}
	return playerEye
}

// SetPlayerEyeHeight is how the crouch resolver moves the eased eye.
func SetPlayerEyeHeight(h float32) { playerEye = h }

// ResetPlayerStance stands the player straight up, instantly: a spawn, a level
// change, the start of play. Keeps the feet where they are.
func ResetPlayerStance() {
	playerEye = 0
	playerSprinting, sprintWanted, sprintHeldOff = false, false, false
	if PlayerEntity == nil {
		return
	}
	if d := StandHalfHeight - PlayerEntity.BoundHalfY; d > 0 {
		PlayerEntity.Position[1] += d
	}
	PlayerEntity.BoundHalfY = StandHalfHeight
}
