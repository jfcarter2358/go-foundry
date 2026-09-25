package world

// gravity.go — how hard things fall, and who gets to say.
//
// Gravity used to be one number in two places: platform's per-frame constants
// for the player, and EntityGravityAccel for everything else, with a comment
// explaining that the second was the first converted out of its units. That is
// fine for one world at one scale and wrong the moment a level is set on a moon,
// a ship loses its deck plating, or a single grenade is meant to float.
//
// So gravity is now answered rather than assumed, and three things get a say,
// in this order:
//
//	the level     `gravity:` — the ambient pull everywhere in it, in units/s².
//	              Absent is DefaultGravity, which is exactly what every level
//	              authored before this had.
//	a volume      `gravity:` — replaces the level's inside the region. The
//	              smallest containing volume wins, so a low-gravity chamber
//	              inside a normal-gravity hangar behaves the way it reads.
//	the body      `gravity_scale:` — multiplies whichever of those applies.
//	              This is a scale rather than a number so that it composes: a
//	              feather is half as heavy as everything around it wherever it
//	              is, and it does not have to be told what "around it" is.
//
// Everything here is a scalar along -Y. A full gravity vector would let a level
// turn its own down, and it is a much larger change than it looks: landing,
// head-bonks, step-up, the ground flag, crouch, the AI's fall-distance estimate
// and every "is this floor walkable" test in the collision package are all
// written in terms of Y. A signed scalar still buys the interesting cases —
// moon, zero-g, and an upward pull for a ceiling walk — without any of them
// quietly breaking.

import "github.com/go-gl/mathgl/mgl32"

// DefaultGravity is the pull a level that says nothing gets, in world units per
// second squared. It is the number the engine has always used: the player's old
// per-frame 0.010, taken out of its 60fps units.
const DefaultGravity = float32(36.0)

// DefaultMaxFallSpeed is the terminal speed a body that names none falls at, in
// units per second — the old per-frame 0.50, converted the same way.
//
// It exists as much for the resolver as for the feel: a body that accelerated
// without limit would eventually move further in one tick than the sweep can
// subdivide sensibly, and would meet the floor on the far side of it.
const DefaultMaxFallSpeed = float32(30.0)

// PlayerJumpVelocity is the upward speed a jump starts with, in units per
// second — the old per-frame 0.20.
//
// Not scaled by gravity: how high the player jumps SHOULD change when a level
// changes its gravity, and leaving the launch speed alone is what makes it.
const PlayerJumpVelocity = float32(12.0)

// EntityGravityAccel and EntityMaxFallSpeed are the old names, kept because
// they read well at a call site that genuinely wants the engine's default
// rather than a particular level's answer.
const (
	EntityGravityAccel = DefaultGravity
	EntityMaxFallSpeed = DefaultMaxFallSpeed
)

// LevelGravity is the level's own ambient pull, before any volume or body has
// its say.
//
// nil — a level with no `gravity:` key, and every Level built as a struct
// literal by a test or the editor — is the engine default rather than zero,
// which is why the field is a pointer: zero gravity is a thing an author may
// legitimately ask for, so "absent" and "0" cannot be the same value.
func LevelGravity(lvl *Level) float32 {
	if lvl == nil || lvl.Gravity == nil {
		return DefaultGravity
	}
	return *lvl.Gravity
}

// GravityAt is the ambient pull at a world-space point: the level's, unless a
// volume overrides it there.
//
// The smallest containing volume wins. Overlapping regions are normal — a
// low-gravity chamber authored inside the room volume that dims the lights —
// and "the most specific region containing the point" is the only reading that
// does not depend on level order.
//
// Cheap when nothing uses it, which is every level so far: a volume with no
// gravity override is rejected on a nil check, before the containment test that
// is the expensive half. Only a level that actually has gravity volumes pays
// for them, and then only once per falling body per tick.
func GravityAt(lvl *Level, p mgl32.Vec3) float32 {
	if lvl == nil {
		return DefaultGravity
	}
	g := LevelGravity(lvl)
	smallest := float32(-1)
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		if v.Gravity == nil {
			continue
		}
		if !v.ContainsWorldPoint(lvl, p) {
			continue
		}
		// Comparable across shapes without a shape-by-shape volume formula:
		// every kind here is inscribed in the box its half-extents describe, so
		// the box is a monotone stand-in for the region's actual size.
		size := v.HalfX * v.HalfY * v.HalfZ
		if smallest < 0 || size < smallest {
			smallest, g = size, *v.Gravity
		}
	}
	return g
}

// GravityFor is what one entity actually falls under: the ambient pull where it
// is standing, times its own scale.
//
// The point asked about is the body's world-space position, so a crate riding a
// ship is answered against the level's volumes rather than the ship's local
// coordinates — the low-gravity chamber it is being carried through is a place
// in the world.
func GravityFor(lvl *Level, e *Entity) float32 {
	if e == nil {
		return LevelGravity(lvl)
	}
	var p mgl32.Vec3
	if lvl != nil {
		p = lvl.Frames.WorldPosition(e)
	} else {
		p = e.Position
	}
	return GravityAt(lvl, p) * e.Collision.EffectiveGravityScale()
}

// EffectiveGravityScale is the multiplier actually in force. Zero means the
// field was never set, which is 1 — the same "0 restores the default" the
// player's StepHeight uses, and for the same reason: a struct literal that
// predates the field must keep behaving as it did.
//
// Switching gravity off is `gravity: false`, not `gravity_scale: 0`. A scale of
// zero is spelled the same as saying nothing, and that is the trade the
// convention makes; the flag is the honest way to say it and is documented as
// such everywhere the scale is.
func (c EntityCollision) EffectiveGravityScale() float32 {
	if c.GravityScale == 0 {
		return 1
	}
	return c.GravityScale
}

// EffectiveMaxFallSpeed is the terminal speed in force: what the body asks for,
// or the engine's default when it asks for nothing. Non-positive is the
// default rather than "never fall" — a body with no terminal speed at all is
// one that eventually falls through the floor, which is never what was meant.
func (c EntityCollision) EffectiveMaxFallSpeed() float32 {
	if c.MaxFallSpeed <= 0 {
		return DefaultMaxFallSpeed
	}
	return c.MaxFallSpeed
}
