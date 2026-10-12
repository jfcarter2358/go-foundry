package noise

// fractal.go — sums of octaves, and domain warping.
//
// A fractal here is a description, not a function: Fractal holds the octave
// count, the base frequency, the lacunarity (frequency multiplier per octave),
// the gain (amplitude multiplier per octave), which basis it sums and how it
// shapes each octave. The generators' parameter panels map straight onto these
// fields, so a stage's schema can say "octaves, frequency, gain" and hand the
// numbers over without a translation table.
//
// Three shapes:
//
//   - FBM: the plain sum. Rolling hills.
//   - Ridged: 1 - |n| per octave, squared, with each octave weighted by the
//     previous one's value (Musgrave's ridged multifractal), so the ridges stay
//     sharp and the valleys between them smooth. Mountain crests, eroded rims.
//   - Billow: |n| per octave. Puffy, rounded lumps — boulders, dunes.
//
// Every octave gets its own seed (Hash64(seed, octave)), not just its own
// frequency: summing the same lattice at doubled frequency lines the octaves
// up at the origin and is visible as a cross of artefacts there.
//
// The sums are normalised by the total amplitude so every shape stays in
// about [-1, 1] (Ridged and Billow are remapped to that range from their
// natural [0, 1]) whatever the octave count — changing "octaves" in a panel
// then adds detail without also changing the overall height.

import "math"

// Basis picks the noise a fractal sums.
type Basis int

const (
	BasisGradient Basis = iota
	BasisValue
)

// BasisNames are the UI spellings of each Basis, indexed by it.
var BasisNames = []string{"gradient", "value"}

// Shape picks how a fractal shapes its octaves.
type Shape int

const (
	ShapeFBM Shape = iota
	ShapeRidged
	ShapeBillow
)

// ShapeNames are the UI spellings of each Shape, indexed by it.
var ShapeNames = []string{"fbm", "ridged", "billow"}

// ShapeFromName parses a ShapeNames entry; an unknown name is FBM.
func ShapeFromName(s string) Shape {
	for i, n := range ShapeNames {
		if n == s {
			return Shape(i)
		}
	}
	return ShapeFBM
}

// BasisFromName parses a BasisNames entry; an unknown name is gradient.
func BasisFromName(s string) Basis {
	for i, n := range BasisNames {
		if n == s {
			return Basis(i)
		}
	}
	return BasisGradient
}

// Fractal is a fractal noise description.
type Fractal struct {
	Seed       uint64
	Octaves    int     // at least 1
	Frequency  float64 // of the first octave, in cycles per unit
	Lacunarity float64 // frequency multiplier per octave; 0 means 2
	Gain       float64 // amplitude multiplier per octave; 0 means 0.5
	Basis      Basis
	Shape      Shape
}

func (f Fractal) resolved() Fractal {
	if f.Octaves < 1 {
		f.Octaves = 1
	}
	if f.Frequency == 0 {
		f.Frequency = 1
	}
	if f.Lacunarity == 0 {
		f.Lacunarity = 2
	}
	if f.Gain == 0 {
		f.Gain = 0.5
	}
	return f
}

func (f *Fractal) basis2(seed uint64, x, y float64) float64 {
	if f.Basis == BasisValue {
		return Value2(seed, x, y)
	}
	return Gradient2(seed, x, y)
}

func (f *Fractal) basis3(seed uint64, x, y, z float64) float64 {
	if f.Basis == BasisValue {
		return Value3(seed, x, y, z)
	}
	return Gradient3(seed, x, y, z)
}

// At2 samples the fractal at (x, y). About [-1, 1].
func (f Fractal) At2(x, y float64) float64 {
	f = f.resolved()
	freq, amp, total, sum := f.Frequency, 1.0, 0.0, 0.0
	weight := 1.0
	for o := 0; o < f.Octaves; o++ {
		n := f.basis2(Hash64(f.Seed, uint64(o)), x*freq, y*freq)
		sum += shapeOctave(f.Shape, n, &weight) * amp
		total += amp
		freq *= f.Lacunarity
		amp *= f.Gain
	}
	return finish(f.Shape, sum/total)
}

// At3 samples the fractal at (x, y, z). About [-1, 1].
func (f Fractal) At3(x, y, z float64) float64 {
	f = f.resolved()
	freq, amp, total, sum := f.Frequency, 1.0, 0.0, 0.0
	weight := 1.0
	for o := 0; o < f.Octaves; o++ {
		n := f.basis3(Hash64(f.Seed, uint64(o)), x*freq, y*freq, z*freq)
		sum += shapeOctave(f.Shape, n, &weight) * amp
		total += amp
		freq *= f.Lacunarity
		amp *= f.Gain
	}
	return finish(f.Shape, sum/total)
}

// shapeOctave turns one octave's raw noise into its contribution. weight is
// the ridged multifractal's running weight, carried between octaves.
func shapeOctave(s Shape, n float64, weight *float64) float64 {
	switch s {
	case ShapeRidged:
		r := 1 - math.Abs(n)
		r *= r
		v := r * *weight
		*weight = Clamp(r*2, 0, 1)
		return v
	case ShapeBillow:
		return math.Abs(n)
	}
	return n
}

// finish maps a normalised sum back to about [-1, 1].
func finish(s Shape, v float64) float64 {
	switch s {
	case ShapeRidged, ShapeBillow:
		return v*2 - 1
	}
	return v
}

// FBM2 is the plain 2D fractal sum of gradient noise — the common case,
// spelled short.
func FBM2(seed uint64, x, y float64, octaves int, gain float64) float64 {
	return Fractal{Seed: seed, Octaves: octaves, Frequency: 1, Gain: gain}.At2(x, y)
}

// FBM3 is FBM2 in three dimensions.
func FBM3(seed uint64, x, y, z float64, octaves int, gain float64) float64 {
	return Fractal{Seed: seed, Octaves: octaves, Frequency: 1, Gain: gain}.At3(x, y, z)
}

// Ridged2 is the ridged multifractal in 2D, about [-1, 1].
func Ridged2(seed uint64, x, y float64, octaves int, gain float64) float64 {
	return Fractal{Seed: seed, Octaves: octaves, Frequency: 1, Gain: gain, Shape: ShapeRidged}.At2(x, y)
}

// Billow2 is billow noise in 2D, about [-1, 1].
func Billow2(seed uint64, x, y float64, octaves int, gain float64) float64 {
	return Fractal{Seed: seed, Octaves: octaves, Frequency: 1, Gain: gain, Shape: ShapeBillow}.At2(x, y)
}

// Warp2 is a 2D domain warp: it returns (x, y) displaced by two independent
// fractals scaled by amount. Feed the result to another noise and its features
// bend and swirl — the "Lumen in the Land of Nanite" mesa outlines come from
// exactly this, applied to a thresholded low-frequency field.
func Warp2(f Fractal, x, y, amount float64) (float64, float64) {
	fx, fy := f, f
	fx.Seed = Hash64(f.Seed, 0x5741_5250_58) // "WARPX"
	fy.Seed = Hash64(f.Seed, 0x5741_5250_59) // "WARPY"
	// The second sample is offset as well as reseeded, so even a basis whose
	// seed does not matter (none here, but a future one) would not warp along
	// the diagonal.
	return x + amount*fx.At2(x, y), y + amount*fy.At2(x+5.2, y+1.3)
}

// Warp3 is Warp2 in three dimensions.
func Warp3(f Fractal, x, y, z, amount float64) (float64, float64, float64) {
	fx, fy, fz := f, f, f
	fx.Seed = Hash64(f.Seed, 1)
	fy.Seed = Hash64(f.Seed, 2)
	fz.Seed = Hash64(f.Seed, 3)
	return x + amount*fx.At3(x, y, z),
		y + amount*fy.At3(x+5.2, y+1.3, z+2.8),
		z + amount*fz.At3(x+9.7, y+3.1, z+7.4)
}
