package sdf

// displace.go — noise displacement: a shape's surface pushed in and out by a
// scalar noise field, and the fields a rock is made of.
//
//   - FBM: lumps and swells at every scale — a boulder's overall irregularity
//     at one metre, its surface's undulation at ten centimetres.
//   - Ridged: sharp creases between soft swells (1 − |n|, squared and fed
//     forward octave to octave) — the grooves and flutes running water and
//     wind leave, and, inverted, sharp arêtes.
//   - Cellular: the distance to the nearest of a set of random points — pits
//     (tafoni, honeycomb weathering) at one scale, granular crystals at
//     another; or, from F2 − F1, a network of cracks.
//
// Every field is about [-1, 1] and carries a Lipschitz estimate in its units
// per metre, so Displace can say how steep the displaced shape is.

import (
	"math"

	"foundry/internal/procgen/noise"
)

// Field is a scalar noise field over space, about [-1, 1], with a bound on
// its gradient (per metre).
type Field struct {
	F   func(p Vec3) float64
	Lip float64
}

// At is F(p).
func (f Field) At(p Vec3) float64 { return f.F(p) }

// fractalLip is a fractal's gradient bound: each octave of gradient noise
// varies by about 2.5 per cycle, so octave o adds 2.5·freq·lac^o·gain^o, and
// the sum is normalised by the total amplitude.
func fractalLip(f noise.Fractal) float64 {
	if f.Octaves < 1 {
		f.Octaves = 1
	}
	lac, gain := f.Lacunarity, f.Gain
	if lac == 0 {
		lac = 2
	}
	if gain == 0 {
		gain = 0.5
	}
	freq, amp, total, l := f.Frequency, 1.0, 0.0, 0.0
	for o := 0; o < f.Octaves; o++ {
		l += 2.5 * freq * amp
		total += amp
		freq *= lac
		amp *= gain
	}
	if f.Shape != noise.ShapeFBM {
		l *= 4 // ridged and billow fold and double the octaves
	}
	return l / total
}

// FractalField is a noise.Fractal as a Field.
func FractalField(f noise.Fractal) Field {
	return Field{F: func(p Vec3) float64 { return f.At3(p[0], p[1], p[2]) }, Lip: fractalLip(f)}
}

// FBM is fractal gradient noise: seed, first-octave frequency (cycles per
// metre), octaves and gain.
func FBM(seed uint64, freq float64, octaves int, gain float64) Field {
	return FractalField(noise.Fractal{Seed: seed, Octaves: octaves, Frequency: freq, Gain: gain})
}

// Ridged is the ridged multifractal: high along sharp crests, low between.
func Ridged(seed uint64, freq float64, octaves int, gain float64) Field {
	return FractalField(noise.Fractal{Seed: seed, Octaves: octaves, Frequency: freq, Gain: gain, Shape: noise.ShapeRidged})
}

// Cellular is cellular noise at freq cells per metre: 1 at the cell points
// falling to -1 a cell away (rounded pits when subtracted), jitter 0..1.
func Cellular(seed uint64, freq, jitter float64) Field {
	return Field{
		F: func(p Vec3) float64 {
			c := noise.Cellular3(seed, p[0]*freq, p[1]*freq, p[2]*freq, jitter, noise.MetricEuclidean)
			return 1 - 2*math.Min(c.F1, 1)
		},
		Lip: 2 * freq,
	}
}

// CellEdges is a crack network at freq cells per metre: -1 on the cell
// borders rising to 1 a cell in (F2 − F1, which grows linearly off a border).
func CellEdges(seed uint64, freq, jitter float64) Field {
	return Field{
		F: func(p Vec3) float64 {
			c := noise.Cellular3(seed, p[0]*freq, p[1]*freq, p[2]*freq, jitter, noise.MetricEuclidean)
			return math.Min(c.Edge(), 1)*2 - 1
		},
		Lip: 2 * freq,
	}
}

// Sum is the fields added with weights.
func Sum(weights []float64, fs ...Field) Field {
	l := 0.0
	for i, f := range fs {
		l += math.Abs(weights[i]) * f.Lip
	}
	return Field{
		F: func(p Vec3) float64 {
			v := 0.0
			for i, f := range fs {
				v += weights[i] * f.F(p)
			}
			return v
		},
		Lip: l,
	}
}

// Warped is f read at a point displaced by a 3D warp of amount metres
// (noise.Warp3 over w): bends the features of f.
func Warped(f Field, w noise.Fractal, amount float64) Field {
	ff := f.F
	return Field{
		F: func(p Vec3) float64 {
			x, y, z := noise.Warp3(w, p[0], p[1], p[2], amount)
			return ff(Vec3{x, y, z})
		},
		Lip: f.Lip * (1 + amount*fractalLip(w)),
	}
}

// Displace pushes the surface out by amp·f (in by a negative value): the
// distance minus the field. The box grows by |amp|.
func Displace(s Shape, f Field, amp float64) Shape {
	sf, ff := s.F, f.F
	box := s.Box
	if !box.IsInfinite() {
		box = box.Grow(math.Abs(amp))
	}
	return Shape{
		F:   func(p Vec3) float64 { return sf(p) - amp*ff(p) },
		Box: box,
		Lip: s.Lip + math.Abs(amp)*f.Lip,
	}
}

// DisplaceWhere is Displace scaled by a mask field m in 0..1 (the
// displacement where m is 1, none where it is 0) — roughness only on the
// weathered faces, say.
func DisplaceWhere(s Shape, f Field, amp float64, m Field) Shape {
	sf, ff, mf := s.F, f.F, m.F
	box := s.Box
	if !box.IsInfinite() {
		box = box.Grow(math.Abs(amp))
	}
	return Shape{
		F:   func(p Vec3) float64 { return sf(p) - amp*ff(p)*Clamp(mf(p), 0, 1) },
		Box: box,
		Lip: s.Lip + math.Abs(amp)*(f.Lip+m.Lip),
	}
}
