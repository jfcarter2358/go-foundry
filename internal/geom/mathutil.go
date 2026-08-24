package geom

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

// Abs32 returns the absolute value of a float32.
func Abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

// Clamp01 clamps v to [0,1].
func Clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Floor32 returns the floor of a float32 as a float32.
func Floor32(x float32) float32 {
	i := int(x)
	if float32(i) > x {
		i--
	}
	return float32(i)
}

// CirclePoint returns the cosine and sine of step i of n around a full circle.
// Shared by the wireframe cage builders, which each need a ring of points and
// would otherwise repeat the same two lines of trigonometry.
func CirclePoint(i, n int) (cos, sin float32) {
	a := 2 * math.Pi * float64(i) / float64(n)
	return float32(math.Cos(a)), float32(math.Sin(a))
}

// ClampValue clamps v to [min, max] when the range is non-zero.
//
// The zero-means-unbounded convention comes from the attribute/property editors,
// where an unset limit is the zero value rather than a sentinel.
func ClampValue(v, min, max float32) float32 {
	if min != 0 && v < min {
		v = min
	}
	if max != 0 && v > max {
		v = max
	}
	return v
}

// ApproxEq2 reports whether two 2D points are within a millimetre of each other.
// The tolerance is the one point-snapping and sector welding have always used.
func ApproxEq2(a, b mgl32.Vec2) bool {
	const eps = 0.001
	return math.Abs(float64(a.X()-b.X())) < eps &&
		math.Abs(float64(a.Y()-b.Y())) < eps
}

// VecMin and VecMax are the component-wise pair used to grow an AABB.
func VecMin(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{
		float32(math.Min(float64(a.X()), float64(b.X()))),
		float32(math.Min(float64(a.Y()), float64(b.Y()))),
		float32(math.Min(float64(a.Z()), float64(b.Z()))),
	}
}

func VecMax(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{
		float32(math.Max(float64(a.X()), float64(b.X()))),
		float32(math.Max(float64(a.Y()), float64(b.Y()))),
		float32(math.Max(float64(a.Z()), float64(b.Z()))),
	}
}

// PerpAxis builds an arbitrary unit vector perpendicular to n. Used wherever a
// frame has to be completed from a single direction — face detail, UV
// projection, bone rings.
func PerpAxis(n mgl32.Vec3) mgl32.Vec3 {
	if math.Abs(float64(n.Y())) < 0.9 {
		return mgl32.Vec3{0, 1, 0}.Cross(n)
	}
	return mgl32.Vec3{1, 0, 0}.Cross(n)
}

// WrapAngle folds a difference of two atan2 results into (-pi, pi].
//
// Every rotate gizmo needs it — the map, the 3D view and the model editor —
// which is why it is here rather than beside any one of them.
// Without it, dragging across the ±pi seam would read as a near-full turn in
// the opposite direction and the selection would snap right round.
func WrapAngle(a float32) float32 {
	const twoPi = 2 * math.Pi
	for a > math.Pi {
		a -= twoPi
	}
	for a <= -math.Pi {
		a += twoPi
	}
	return a
}
