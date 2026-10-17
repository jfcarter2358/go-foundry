package noise

// noise.go — lattice noise: value and gradient, in two and three dimensions.
//
// Both are the classic constructions over an integer lattice whose corners are
// hashed with the seed (rng.go's hash2/hash3), blended with the quintic fade
// 6t^5 - 15t^4 + 10t^3, which is C2-continuous so a heightfield built from it
// has no visible creases in its normals.
//
//   - Value noise interpolates a random value per corner. Cheap and blobby;
//     its range is exactly [-1, 1].
//   - Gradient (Perlin) noise interpolates a random gradient's dot product with
//     the offset to each corner. Fewer axis-aligned artefacts, zero at every
//     lattice point; scaled here so its range is about [-1, 1].
//
// The gradient sets are fixed tables rather than random unit vectors: twelve
// cube-edge directions in 3D (Perlin 2002) and sixteen evenly spaced ones in
// 2D. A fixed table is what keeps the result bit-identical across platforms —
// a gradient made with sin/cos would depend on the libm.

import "math"

// fade is the quintic smoothstep.
func fade(t float64) float64 { return t * t * t * (t*(t*6-15) + 10) }

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

// floor splits x into its lattice cell and the offset inside it.
func floor(x float64) (int64, float64) {
	f := math.Floor(x)
	return int64(f), x - f
}

// Value2 is 2D value noise in [-1, 1].
func Value2(seed uint64, x, y float64) float64 {
	ix, fx := floor(x)
	iy, fy := floor(y)
	v := func(dx, dy int64) float64 { return unit(hash2(seed, ix+dx, iy+dy))*2 - 1 }
	u, w := fade(fx), fade(fy)
	return lerp(lerp(v(0, 0), v(1, 0), u), lerp(v(0, 1), v(1, 1), u), w)
}

// Value3 is 3D value noise in [-1, 1].
func Value3(seed uint64, x, y, z float64) float64 {
	ix, fx := floor(x)
	iy, fy := floor(y)
	iz, fz := floor(z)
	v := func(dx, dy, dz int64) float64 { return unit(hash3(seed, ix+dx, iy+dy, iz+dz))*2 - 1 }
	u, w, t := fade(fx), fade(fy), fade(fz)
	a := lerp(lerp(v(0, 0, 0), v(1, 0, 0), u), lerp(v(0, 1, 0), v(1, 1, 0), u), w)
	b := lerp(lerp(v(0, 0, 1), v(1, 0, 1), u), lerp(v(0, 1, 1), v(1, 1, 1), u), w)
	return lerp(a, b, t)
}

// grad2 is sixteen unit directions, 22.5° apart.
var grad2 = func() [16][2]float64 {
	var g [16][2]float64
	// Computed once at init rather than at each call. math.Sincos is pure Go,
	// so the table is the same on every build of one architecture (see the
	// package comment's FMA caveat for the others).
	for i := range g {
		s, c := math.Sincos(float64(i) * math.Pi / 8)
		g[i] = [2]float64{c, s}
	}
	return g
}()

// grad3 is the twelve edge directions of a cube, plus four repeats so the
// table indexes with a 4-bit mask (Perlin's "improved noise" set).
var grad3 = [16][3]float64{
	{1, 1, 0}, {-1, 1, 0}, {1, -1, 0}, {-1, -1, 0},
	{1, 0, 1}, {-1, 0, 1}, {1, 0, -1}, {-1, 0, -1},
	{0, 1, 1}, {0, -1, 1}, {0, 1, -1}, {0, -1, -1},
	{1, 1, 0}, {0, -1, 1}, {-1, 1, 0}, {0, -1, -1},
}

// Gradient2 is 2D gradient (Perlin) noise, roughly in [-1, 1].
func Gradient2(seed uint64, x, y float64) float64 {
	ix, fx := floor(x)
	iy, fy := floor(y)
	d := func(dx, dy int64) float64 {
		g := grad2[hash2(seed, ix+dx, iy+dy)>>60]
		return g[0]*(fx-float64(dx)) + g[1]*(fy-float64(dy))
	}
	u, w := fade(fx), fade(fy)
	// The largest a unit-gradient 2D Perlin can reach is sqrt(1/2); scale so
	// the extreme is 1.
	return lerp(lerp(d(0, 0), d(1, 0), u), lerp(d(0, 1), d(1, 1), u), w) * math.Sqrt2
}

// Gradient3 is 3D gradient (Perlin) noise, roughly in [-1, 1].
func Gradient3(seed uint64, x, y, z float64) float64 {
	ix, fx := floor(x)
	iy, fy := floor(y)
	iz, fz := floor(z)
	d := func(dx, dy, dz int64) float64 {
		g := grad3[hash3(seed, ix+dx, iy+dy, iz+dz)>>60]
		return g[0]*(fx-float64(dx)) + g[1]*(fy-float64(dy)) + g[2]*(fz-float64(dz))
	}
	u, w, t := fade(fx), fade(fy), fade(fz)
	a := lerp(lerp(d(0, 0, 0), d(1, 0, 0), u), lerp(d(0, 1, 0), d(1, 1, 0), u), w)
	b := lerp(lerp(d(0, 0, 1), d(1, 0, 1), u), lerp(d(0, 1, 1), d(1, 1, 1), u), w)
	// The edge-direction gradients have length sqrt(2), and the 3D extreme is
	// about 1.04 of that; this lands close to [-1, 1].
	return lerp(a, b, t) * 0.96
}

// SmoothStep is the cubic Hermite step from e0 to e1.
func SmoothStep(e0, e1, x float64) float64 {
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

// Clamp bounds x to [lo, hi].
func Clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
