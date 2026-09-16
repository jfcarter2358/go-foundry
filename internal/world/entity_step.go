package world

// entity_step.go — easing a walker's body over a step it was teleported up.
//
// collision.StepUp does not climb a riser, it lifts the body straight onto it:
// one tick the feet are at the foot of a 0.5 m sill, the next they are on top.
// That is the right answer for collision — a swept climb would have to know
// the riser's shape, and every probe it made would be another place to leak
// through geometry — and the wrong one for the eye. The player never sees it,
// because the camera eases its own height; an AI squadmate walking into a
// dropship hold visibly pops.
//
// There are no step clips to play, so the fix is procedural and lives entirely
// on the drawing side. The resolve reports how far it lifted the body, that
// lift is subtracted from StepOffset so the drawn body starts the tick exactly
// where it ended the last one, and StepOffset then closes on zero over the next
// few frames. Position, bounds, sockets a script reads (bone_position) and the
// AI's own idea of where it stands are all untouched: nothing but the render's
// DrawMatrix knows the offset exists.
//
// Only a discrete snap is taken back out. Falling off a ledge is ordinary
// gravity — already continuous — and walking up a slope moves the body up a
// little every tick for the same reason, so neither goes near this.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	// StepEaseTimeConstant is how quickly the drawn body closes on the real
	// one: the gap shrinks by a factor of e every this many seconds. 0.12 s
	// puts a half-metre sill under two centimetres in about 0.4 s, which reads
	// as a stride rather than a slide.
	StepEaseTimeConstant = float32(0.12)

	// StepEaseMinSpeed is the slowest the gap closes, in metres per second.
	// A pure exponential never quite arrives, and a body left a few
	// millimetres off its feet for a second is a body the shadow gives away.
	StepEaseMinSpeed = float32(0.4)

	// StepEaseMaxFactor caps the offset at this multiple of the step height
	// that produced it. Anything larger was not a stride — a lift that tall is
	// a teleport the resolve happened to be part of — and animating it would
	// show the body rising through the floor.
	StepEaseMaxFactor = float32(1.5)

	// StepEaseTeleport is how far Position may move between the end of one
	// update and the start of the next before the offset is thrown away. The
	// controller only writes velocities, so anything that moves a body between
	// updates is a script placing it or a chunk carrying it; a lift carrying a
	// walker a few centimetres a tick keeps the ease, a script dropping one
	// across the map does not bring half a stride's lag with it.
	StepEaseTeleport = float32(1.0)
)

// NoteStepLift records that the resolve teleported the body up by lift metres
// (negative for down) under a step allowance of stepHeight, so the drawn body
// is left where it was and eases over instead.
func (e *Entity) NoteStepLift(lift, stepHeight float32) {
	if lift == 0 {
		return
	}
	off := e.StepOffset - lift
	limit := StepEaseMaxFactor * stepHeight
	if off > limit {
		off = limit
	} else if off < -limit {
		off = -limit
	}
	e.StepOffset = off
}

// EaseStepOffset closes StepOffset on zero over dt seconds, the same amount at
// any frame rate: the exponential part is exact for any dt, and the minimum
// speed is a distance per second rather than per tick.
func (e *Entity) EaseStepOffset(dt float32) {
	if e.StepOffset == 0 || dt <= 0 {
		return
	}
	gap := e.StepOffset
	if gap < 0 {
		gap = -gap
	}
	closeBy := gap * (1 - float32(math.Exp(float64(-dt/StepEaseTimeConstant))))
	if floor := StepEaseMinSpeed * dt; closeBy < floor {
		closeBy = floor
	}
	if closeBy >= gap {
		e.StepOffset = 0
		return
	}
	if e.StepOffset < 0 {
		e.StepOffset += closeBy
	} else {
		e.StepOffset -= closeBy
	}
}

// ClearStepOffset drops any ease in progress, so the body is drawn exactly
// where it is.
func (e *Entity) ClearStepOffset() {
	e.StepOffset = 0
	e.StepAnchor = e.Position
}

// DrawMatrix is ModelMatrix with StepOffset applied: where the entity's
// attachments are drawn, in its own frame. The offset goes on the outside of
// the whole TRS, along the frame's up axis, so it is metres whatever the
// entity's scale or facing.
//
// Only the colour and shadow passes use it. Everything that has to agree with
// collision — picking, bounds, bone_position, the lights and emitters attached
// to a bone (Attach) — keeps ModelMatrix, and is at most a stride's height out
// for the fraction of a second the ease lasts.
func (e *Entity) DrawMatrix() mgl32.Mat4 {
	m := e.ModelMatrix()
	if e.StepOffset != 0 {
		m[13] += e.StepOffset
	}
	return m
}
