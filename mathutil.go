package main

// mathutil.go — small scalar-math helpers shared across the renderer/geometry
// code that Go's builtin generic min/max don't cover.
//
// (min32/max32/min2/max2/max0 used to live here too, scattered one-per-file;
// they were deleted in favor of the language's builtin min/max — see
// RESTRUCTURE_PROPOSAL.md §2.)

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

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

// clampValue clamps v to [min, max] when the range is non-zero.
//
// The zero-means-unbounded convention comes from the attribute/property editors,
// where an unset limit is the zero value rather than a sentinel.
func clampValue(v, min, max float32) float32 {
	if min != 0 && v < min {
		v = min
	}
	if max != 0 && v > max {
		v = max
	}
	return v
}

// approxEq2 reports whether two 2D points are within a millimetre of each other.
// The tolerance is the one point-snapping and sector welding have always used.
func approxEq2(a, b mgl32.Vec2) bool {
	const eps = 0.001
	return math.Abs(float64(a.X()-b.X())) < eps &&
		math.Abs(float64(a.Y()-b.Y())) < eps
}

// vecMin and vecMax are the component-wise pair used to grow an AABB.
func vecMin(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{
		float32(math.Min(float64(a.X()), float64(b.X()))),
		float32(math.Min(float64(a.Y()), float64(b.Y()))),
		float32(math.Min(float64(a.Z()), float64(b.Z()))),
	}
}

func vecMax(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{
		float32(math.Max(float64(a.X()), float64(b.X()))),
		float32(math.Max(float64(a.Y()), float64(b.Y()))),
		float32(math.Max(float64(a.Z()), float64(b.Z()))),
	}
}

// perpAxis builds an arbitrary unit vector perpendicular to n. Used wherever a
// frame has to be completed from a single direction — face detail, UV
// projection, bone rings.
func perpAxis(n mgl32.Vec3) mgl32.Vec3 {
	if math.Abs(float64(n.Y())) < 0.9 {
		return mgl32.Vec3{0, 1, 0}.Cross(n)
	}
	return mgl32.Vec3{1, 0, 0}.Cross(n)
}
