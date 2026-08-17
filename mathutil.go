package main

// mathutil.go — small scalar-math helpers shared across the renderer/geometry
// code that Go's builtin generic min/max don't cover.
//
// (min32/max32/min2/max2/max0 used to live here too, scattered one-per-file;
// they were deleted in favor of the language's builtin min/max — see
// RESTRUCTURE_PROPOSAL.md §2.)

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
