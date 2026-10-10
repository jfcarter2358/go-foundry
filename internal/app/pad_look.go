package app

// pad_look.go — turning the player's view with a stick, and aim assist.
//
// A mouse reports how far it moved; a stick reports how far it is pushed, which
// is a turn RATE. So the look stick is not the arrow keys' fixed rate and not
// the mouse's banked motion, but its own path into the same turnView:
//
//   - The four look actions' pad travel (so the stick follows a rebind) make
//     one vector, already through the radial dead zone.
//   - A response curve, travel^PadLookCurve, keeps the top speed and spends
//     the first half of the stick on fine aim.
//   - Held hard over for a moment, yaw ramps up by padTurnBoost, so turning
//     round does not take a second and a half.
//   - Sensitivity per axis, invert-Y, and lookZoomScale so aiming down sights
//     slows the stick the way it slows the mouse.
//
// Aim assist runs only for a pad player (platform.LastDevice) with it switched
// on, on foot, with the player's own camera. Two parts, both faded by how close
// the crosshair is to the target's centre within its cone:
//
//   - friction: the stick turns slower over a target, so a fast sweep does
//     not overshoot it;
//   - pull: while the player is steering — stick or walking — the view eases
//     toward the target, which is what tracks a strafing enemy. Standing still
//     with the sticks at rest it does nothing, so it never aims for anyone.
//
// The cone is the target's width plus a margin, shrinking with distance, and a
// target behind cover (ai.LineOfSight) gets nothing.

import (
	"math"

	"foundry/internal/ai"
	"foundry/internal/gfx"
	"foundry/internal/platform"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	// The turn rates at full deflection and sensitivity 1, radians a second.
	padLookYawSpeed   = 3.2 // about 185°/s
	padLookPitchSpeed = 2.2
	// padTurnBoost is the extra yaw rate reached after the stick has been
	// held over for padBoostDelay, ramping in over padBoostRamp.
	padTurnBoost  = 0.7
	padBoostDelay = float32(0.25)
	padBoostRamp  = float32(0.35)

	// aimAssistRange is how far a target can be and still be helped onto.
	aimAssistRange = 60
	// The cone is the target's half-width times aimAssistConeScale plus
	// aimAssistConePad metres, seen from the eye, and never wider than
	// aimAssistMaxCone radians.
	aimAssistConeScale = 2.5
	aimAssistConePad   = 0.3
	aimAssistMaxCone   = 8 * math.Pi / 180
	// aimAssistFriction is how much of the stick's rate is taken away dead
	// on a target.
	aimAssistFriction = 0.5
	// aimAssistPull is the rate, per second, at which the view closes on the
	// target while the player steers at full effort, dead on it.
	aimAssistPull = 5.0
)

// padBoostHeld is how long the look stick has been held hard over sideways.
var padBoostHeld float32

// aimTargets is reused across frames.
var aimTargets []ai.AimTarget

// applyPadLook spends this frame's look-stick travel on the camera.
func applyPadLook(dt float32) {
	b := &platform.Bindings
	x := float64(b.PadValue(platform.ActLookRight) - b.PadValue(platform.ActLookLeft))
	y := float64(b.PadValue(platform.ActLookUp) - b.PadValue(platform.ActLookDown))
	m := math.Hypot(x, y)
	if m > 1 {
		x, y, m = x/m, y/m, 1
	}
	if m > 0 {
		k := math.Pow(m, float64(platform.PadLookCurve)) / m
		x, y = x*k, y*k
	}

	if math.Abs(x) > 0.9 {
		padBoostHeld += dt
	} else {
		padBoostHeld = 0
	}
	boost := 1 + padTurnBoost*float64(clamp01((padBoostHeld-padBoostDelay)/padBoostRamp))

	assist := aimAssist(dt, m)

	zoom := lookZoomScale()
	dt64 := float64(dt)
	yaw := x * padLookYawSpeed * float64(platform.PadLookSensitivityX) * zoom * boost * assist.slow * dt64
	pitch := y * padLookPitchSpeed * float64(platform.PadLookSensitivityY) * zoom * assist.slow * dt64
	if platform.PadInvertY {
		pitch = -pitch
	}
	yaw += assist.yaw
	pitch += assist.pitch
	if yaw == 0 && pitch == 0 {
		return
	}
	turnView(yaw, pitch)
}

// assistTurn is aim assist's say in one frame's turn: a factor on the stick's
// rate, and a turn of its own in radians.
type assistTurn struct {
	slow       float64
	yaw, pitch float64
}

// aimAssist finds the target nearest the crosshair and returns what it does to
// this frame's turn. look is the look stick's travel, 0 to 1.
func aimAssist(dt float32, look float64) assistTurn {
	none := assistTurn{slow: 1}
	if !platform.AimAssist || platform.LastDevice != platform.DevicePad || gfx.CameraDetached() || !onFoot() {
		return none
	}
	lvl := world.CurrentLevel()
	if lvl == nil || world.PlayerEntity == nil {
		return none
	}
	eye := gfx.Cam.Position
	fwd := gfx.DirectionFromAngles(gfx.Cam.Yaw, gfx.Cam.Pitch)
	aimTargets = ai.AimTargets(lvl, world.PlayerEntity, aimTargets[:0])

	best, bestCloseness := -1, 0.0
	var bestTo mgl32.Vec3
	for i, t := range aimTargets {
		to := t.Point.Sub(eye)
		d := float64(to.Len())
		if d < 0.5 || d > aimAssistRange {
			continue
		}
		if c := assistCloseness(fwd, to, t.Radius); c > bestCloseness {
			best, bestCloseness, bestTo = i, c, to
		}
	}
	if best < 0 || !ai.LineOfSight(lvl, eye, aimTargets[best].Point, aimTargets[best].ID, "player") {
		return none
	}

	out := assistTurn{slow: 1 - aimAssistFriction*bestCloseness}
	b := &platform.Bindings
	move := math.Hypot(
		float64(b.PadValue(platform.ActMoveRight)-b.PadValue(platform.ActMoveLeft)),
		float64(b.PadValue(platform.ActMoveForward)-b.PadValue(platform.ActMoveBack)))
	effort := math.Min(look+move, 1)
	if effort <= 0 {
		return out
	}
	yawErr, pitchErr := angleTo(bestTo, gfx.Cam.Yaw, gfx.Cam.Pitch)
	pull := math.Min(aimAssistPull*float64(dt), 1) * effort * bestCloseness
	out.yaw, out.pitch = yawErr*pull, pitchErr*pull
	return out
}

// assistCloseness is how near the view's forward is to a target's centre
// within its cone: 1 dead on, falling to 0 at the cone's edge and outside it.
func assistCloseness(fwd, to mgl32.Vec3, radius float32) float64 {
	d := float64(to.Len())
	if d <= 0 {
		return 0
	}
	cos := float64(fwd.Dot(to)) / d
	ang := math.Acos(math.Max(-1, math.Min(1, cos)))
	cone := math.Min(math.Atan((float64(radius)*aimAssistConeScale+aimAssistConePad)/d), aimAssistMaxCone)
	if ang >= cone {
		return 0
	}
	return 1 - ang/cone
}

// angleTo is the yaw and pitch, in radians, that turn a view at (yaw, pitch)
// to face along to — the inverse of gfx.DirectionFromAngles, with the yaw
// difference taken the short way round.
func angleTo(to mgl32.Vec3, yaw, pitch float32) (float64, float64) {
	d := float64(to.Len())
	if d <= 0 {
		return 0, 0
	}
	ty := math.Atan2(float64(to.Z()), float64(to.X()))
	tp := math.Asin(float64(to.Y()) / d)
	dy := math.Remainder(ty-float64(yaw), 2*math.Pi)
	return dy, tp - float64(pitch)
}
