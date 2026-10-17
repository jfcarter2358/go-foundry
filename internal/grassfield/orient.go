package grassfield

// orient.go — an oriented clump's up axis: how it is packed into the fourth
// instance word, and the rotation that stands a clump on it.
//
// A grass clump stands up the world's +Y and turns about it by its yaw byte.
// Ground cover lies along the ground instead (`orient: ground`: a twig on a
// bank lies down the bank, a leaf card is flat on it) or leans at random
// (`tilt_jitter`): the stage works the axis out at placement time — the
// terrain's normal under the clump, tilted — and packs it here, and the
// vertex stage (grassfield_common.glsl's grassUpRotate) stands the clump on
// it. The yaw byte is still the turn about the clump's own axis, so an
// oriented clump keeps its random roll.
//
// THE WORD
//
//	bits  0..11   octahedral u   (0..4094 over −1..1: an even range, so 0
//	              — level ground's axis — is a code of its own, 2047)
//	bits 12..23   octahedral v
//	bits 24..31   reserved, 0
//
// The octahedral map (Cigolle et al., "A Survey of Efficient
// Representations for Independent Unit Vectors", 2014) folds the sphere onto
// a square, so 12 bits a side keep any direction to well under a tenth of a
// degree — finer than any terrain normal is known.
//
// THE ROTATION is the shortest one taking +Y to the axis n = (x, y, z):
//
//	| 1 − x²/(1+y)   x   −xz/(1+y) |
//	|     −x         y      −z     |
//	| −xz/(1+y)      z   1 − z²/(1+y) |
//
// (Rodrigues' formula about +Y × n). It has no twist of its own, so the yaw
// applied before it is the roll, and a level axis (n = +Y) is exactly the
// upright clump. It degenerates only for an axis pointing straight down,
// which ground cover never has (clamped to y ≥ 0.05).

import "math"

// orientBits is one octahedral component's bits.
const orientBits = 12

// orientMax is a component's largest code (even, so the centre is exact).
const orientMax = 1<<orientBits - 2

// orientMask masks one component's bits.
const orientMask = 1<<orientBits - 1

// octEncode maps unit n onto the octahedral square, (−1..1)².
func octEncode(n [3]float32) (u, v float32) {
	l := float32(math.Abs(float64(n[0])) + math.Abs(float64(n[1])) + math.Abs(float64(n[2])))
	if l <= 0 {
		return 0, 0 // straight up: the square's centre
	}
	// The square is the octahedron's XZ plane, so +Y (the common case)
	// sits at its centre where the code is densest.
	x, y, z := n[0]/l, n[1]/l, n[2]/l
	if y < 0 {
		sx, sz := float32(1), float32(1)
		if x < 0 {
			sx = -1
		}
		if z < 0 {
			sz = -1
		}
		x, z = (1-float32(math.Abs(float64(z))))*sx, (1-float32(math.Abs(float64(x))))*sz
	}
	return x, z
}

// octDecode is octEncode's inverse, normalised.
func octDecode(u, v float32) [3]float32 {
	x, z := u, v
	y := 1 - float32(math.Abs(float64(u))) - float32(math.Abs(float64(v)))
	if y < 0 {
		sx, sz := float32(1), float32(1)
		if x < 0 {
			sx = -1
		}
		if z < 0 {
			sz = -1
		}
		x, z = (1-float32(math.Abs(float64(v))))*sx, (1-float32(math.Abs(float64(u))))*sz
	}
	return normalize3([3]float32{x, y, z})
}

func normalize3(v [3]float32) [3]float32 {
	l := float32(math.Sqrt(float64(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])))
	if l <= 0 {
		return [3]float32{0, 1, 0}
	}
	return [3]float32{v[0] / l, v[1] / l, v[2] / l}
}

// PackUp is the fourth word of an instance standing on up (a zero vector:
// straight up). The axis is normalised and kept at least 0.05 above the
// horizon.
func PackUp(up [3]float32) uint32 {
	if up == ([3]float32{}) {
		up = [3]float32{0, 1, 0}
	}
	up = ClampUp(up)
	u, v := octEncode(up)
	q := func(t float32) uint32 {
		return uint32(math.Round(math.Min(math.Max(float64(t)*0.5+0.5, 0), 1) * orientMax))
	}
	return q(u) | q(v)<<orientBits
}

// UnpackUp is the up axis a fourth word holds.
func UnpackUp(w uint32) [3]float32 {
	d := func(c uint32) float32 { return float32(c&orientMask)/orientMax*2 - 1 }
	return octDecode(d(w), d(w>>orientBits))
}

// ClampUp is up normalised with its Y at least 0.05: ground cover never
// hangs upside down.
func ClampUp(up [3]float32) [3]float32 {
	up = normalize3(up)
	if up[1] < 0.05 {
		h := float32(math.Hypot(float64(up[0]), float64(up[2])))
		if h <= 0 {
			return [3]float32{0, 1, 0}
		}
		s := float32(math.Sqrt(1-0.05*0.05)) / h
		up = [3]float32{up[0] * s, 0.05, up[2] * s}
	}
	return up
}

// UpRotate turns v (in a clump's upright frame) onto axis n: the vertex
// stage's grassUpRotate, line for line, for the tests and tools.
func UpRotate(n, v [3]float32) [3]float32 {
	x, y, z := n[0], n[1], n[2]
	k := 1 / (1 + y)
	return [3]float32{
		(1-x*x*k)*v[0] + x*v[1] - x*z*k*v[2],
		-x*v[0] + y*v[1] - z*v[2],
		-x*z*k*v[0] + z*v[1] + (1-z*z*k)*v[2],
	}
}
