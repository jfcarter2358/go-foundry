package collision

// collide_sweep.go — not going through things when moving quickly.
//
// Every resolver in this package is *positional*: it looks at where a body has
// ended up and pushes it back out. That works while a frame's movement is small
// against the body, and fails completely when it is not — a step that clears a
// wall and leaves the sector in one go lands somewhere the sector lookup returns
// nothing for, so nothing resolves and the body keeps going. At the shipped
// walking speed of 0.1 against a 0.3 radius that never happens; at a vehicle's
// speed it happens constantly.
//
// Two mechanisms, for the two shapes of the problem.
//
// SweepSteps splits a motion so no single step can outrun the body's own size,
// and the caller re-runs its ordinary resolve chain on each piece. That is
// deliberately not a second collision routine: the resolvers stay the only
// description of what a wall does, and a slow frame produces exactly one step
// and so exactly the arithmetic it produced before any of this existed.
//
// SweepHit is the general shape-against-shape version, for pairs where there is
// no sector to march through — it advances conservatively, never stepping
// farther than the gap that is left, so it cannot step over a contact.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	// sweepSafeFraction is how much of the body's smallest half-extent a single
	// step may cover. At a half it takes two steps to cross the body, so an
	// obstacle cannot fall entirely between two sampled positions.
	sweepSafeFraction = float32(0.5)

	// maxSweepSteps bounds the work a single absurd velocity can demand. It is
	// high because the cost is only paid by whatever is actually moving that
	// fast, and low enough that a runaway value cannot stall a frame. Something
	// that needs more than this is a projectile and wants a ray, not a body.
	maxSweepSteps = 256

	// minSweepExtent floors the divisor so a body with a near-zero extent asks
	// for a bounded number of steps rather than an infinite one.
	minSweepExtent = float32(1e-3)
)

// SweepSteps is how many equal pieces a motion must be split into before a body
// of the given half-extents can be moved through it safely.
//
// It returns 1 whenever the motion is already small enough, which is the case
// for everything walking around at ordinary speed — so the substepping costs
// nothing until something is actually moving fast enough to need it.
func SweepSteps(half, delta mgl32.Vec3) int {
	n, _ := SweepPlan(half, delta)
	return n
}

// SweepPlan splits a motion into safe steps, and returns the motion it is
// willing to perform.
//
// Beyond maxSweepSteps the displacement itself is shortened rather than the
// step count being quietly clamped. Clamping the count is the tempting thing to
// do and it is wrong: it hands back steps larger than the body, which is
// precisely the situation this exists to prevent, and it does so silently at
// exactly the speeds where going through a wall is most likely. Shortening
// instead imposes a speed ceiling — for the player, some 38 units per frame —
// and fails in the direction that is recoverable. Anything that genuinely needs
// to travel farther than that in one frame is a projectile and wants a ray cast,
// not a body being pushed out of things.
func SweepPlan(half, delta mgl32.Vec3) (steps int, safe mgl32.Vec3) {
	d := delta.Len()
	if d <= 0 || math.IsNaN(float64(d)) || math.IsInf(float64(d), 0) {
		return 1, mgl32.Vec3{}
	}
	e := half.X()
	if half.Y() < e {
		e = half.Y()
	}
	if half.Z() < e {
		e = half.Z()
	}
	if e < minSweepExtent {
		e = minSweepExtent
	}
	stride := sweepSafeFraction * e

	n := int(math.Ceil(float64(d / stride)))
	if n < 1 {
		return 1, delta
	}
	if n > maxSweepSteps {
		return maxSweepSteps, delta.Mul(float32(maxSweepSteps) * stride / d)
	}
	return n, delta
}

// SweepHit finds the fraction of motion along which a can travel before it
// touches b, by conservative advancement: measure the gap between the two,
// advance by no more than that gap, repeat. Because a step is never longer than
// the clearance, there is no step size an obstacle can hide between — a 200-unit
// move still finds a wall two hundredths of a unit thick.
//
// t is in [0,1] and is 0 when the two already overlap. hit is false when a
// completes the whole motion without touching b.
func SweepHit(a, b Shape, motion mgl32.Vec3) (t float32, hit bool) {
	dist := motion.Len()
	if dist < degenerate {
		return 0, Overlap(a, b)
	}
	dir := motion.Mul(1 / dist)

	travelled := float32(0)
	for i := 0; i < sweepMaxIter; i++ {
		gap := Distance(Translate(a, dir.Mul(travelled)), b)
		if gap <= sweepEpsilon {
			return travelled / dist, true
		}
		// The gap is a straight-line distance, so it is a lower bound on how far
		// a may go in *any* direction — advancing by it can never overshoot the
		// contact, whatever angle the approach is at.
		travelled += gap
		if travelled >= dist {
			return 1, false
		}
	}
	// Still closing after this many steps means a grazing approach along a
	// near-parallel surface. Report the contact: a false stop costs a frame, a
	// false pass costs the wall.
	return travelled / dist, true
}

const (
	sweepMaxIter = 64
	sweepEpsilon = float32(1e-4)
)
