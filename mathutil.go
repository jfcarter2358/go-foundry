package main

// mathutil.go — small scalar-math helpers shared across the renderer/geometry
// code that Go's builtin generic min/max don't cover.
//
// (min32/max32/min2/max2/max0 used to live here too, scattered one-per-file;
// they were deleted in favor of the language's builtin min/max — see
// RESTRUCTURE_PROPOSAL.md §2.)

import "math"

// abs32 returns the absolute value of a float32.
func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

// clamp01 clamps v to [0,1].
func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// floor32 returns the floor of a float32 as a float32.
func floor32(x float32) float32 {
	i := int(x)
	if float32(i) > x {
		i--
	}
	return float32(i)
}

// circlePoint returns the cosine and sine of step i of n around a full circle.
// Shared by the wireframe cage builders, which each need a ring of points and
// would otherwise repeat the same two lines of trigonometry.
func circlePoint(i, n int) (cos, sin float32) {
	a := 2 * math.Pi * float64(i) / float64(n)
	return float32(math.Cos(a)), float32(math.Sin(a))
}
