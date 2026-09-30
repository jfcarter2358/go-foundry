package world

// terrain_noise.go — the deterministic hashes and value noise terrain
// generation and foliage placement are made of.
//
// Everything here is a pure function of its inputs: a section of foliage
// rebuilt after an edit elsewhere must come back exactly as it was, down to
// which way each fern faces, or painting one hillside would reshuffle the
// grass on every other.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// Hash32 mixes a into a well-distributed 32-bit value (lowbias32).
func Hash32(a uint32) uint32 {
	a ^= a >> 16
	a *= 0x7feb352d
	a ^= a >> 15
	a *= 0x846ca68b
	a ^= a >> 16
	return a
}

// HashInts hashes several integers into one value.
func HashInts(seed uint32, v ...int) uint32 {
	h := Hash32(seed ^ 0x9e3779b9)
	for _, x := range v {
		h = Hash32(h ^ uint32(int32(x)) + 0x9e3779b9 + (h << 6) + (h >> 2))
	}
	return h
}

// Unit maps a hash onto [0, 1).
func Unit(h uint32) float32 { return float32(h>>8) / float32(1<<24) }

func smooth01(t float32) float32 { return t * t * (3 - 2*t) }

func lerp32(a, b, t float32) float32 { return a + (b-a)*t }

// ValueNoise2 is smooth value noise over the plane, in [0, 1].
func ValueNoise2(p mgl32.Vec2, seed uint32) float32 {
	fx, fz := math.Floor(float64(p[0])), math.Floor(float64(p[1]))
	ix, iz := int(fx), int(fz)
	tx, tz := smooth01(p[0]-float32(fx)), smooth01(p[1]-float32(fz))
	c := func(dx, dz int) float32 { return Unit(HashInts(seed, ix+dx, iz+dz)) }
	return lerp32(lerp32(c(0, 0), c(1, 0), tx), lerp32(c(0, 1), c(1, 1), tx), tz)
}

// ValueNoise3 is smooth value noise over space, in [0, 1].
func ValueNoise3(p mgl32.Vec3, seed uint32) float32 {
	f := [3]float64{math.Floor(float64(p[0])), math.Floor(float64(p[1])), math.Floor(float64(p[2]))}
	i := [3]int{int(f[0]), int(f[1]), int(f[2])}
	t := [3]float32{smooth01(p[0] - float32(f[0])), smooth01(p[1] - float32(f[1])), smooth01(p[2] - float32(f[2]))}
	c := func(dx, dy, dz int) float32 { return Unit(HashInts(seed, i[0]+dx, i[1]+dy, i[2]+dz)) }
	x00 := lerp32(c(0, 0, 0), c(1, 0, 0), t[0])
	x10 := lerp32(c(0, 1, 0), c(1, 1, 0), t[0])
	x01 := lerp32(c(0, 0, 1), c(1, 0, 1), t[0])
	x11 := lerp32(c(0, 1, 1), c(1, 1, 1), t[0])
	return lerp32(lerp32(x00, x10, t[1]), lerp32(x01, x11, t[1]), t[2])
}

// FBM2 is octaves of ValueNoise2, each twice as fine and half as strong,
// normalised back into [0, 1].
func FBM2(p mgl32.Vec2, seed uint32, octaves int) float32 {
	sum, amp, norm := float32(0), float32(1), float32(0)
	for o := 0; o < octaves; o++ {
		sum += ValueNoise2(p, seed+uint32(o)*1013) * amp
		norm += amp
		amp *= 0.5
		// Rotate a little between octaves so their grids never line up.
		p = mgl32.Vec2{p[0]*1.6 - p[1]*1.2, p[0]*1.2 + p[1]*1.6}
	}
	return sum / norm
}

// FBM3 is FBM2 in three dimensions.
func FBM3(p mgl32.Vec3, seed uint32, octaves int) float32 {
	sum, amp, norm := float32(0), float32(1), float32(0)
	for o := 0; o < octaves; o++ {
		sum += ValueNoise3(p, seed+uint32(o)*1013) * amp
		norm += amp
		amp *= 0.5
		p = mgl32.Vec3{p[0]*1.6 - p[2]*1.2, p[1] * 2, p[0]*1.2 + p[2]*1.6}
	}
	return sum / norm
}

// SmoothStep is GLSL's smoothstep.
func SmoothStep(e0, e1, x float32) float32 {
	if e1 == e0 {
		if x < e0 {
			return 0
		}
		return 1
	}
	t := (x - e0) / (e1 - e0)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return t * t * (3 - 2*t)
}
