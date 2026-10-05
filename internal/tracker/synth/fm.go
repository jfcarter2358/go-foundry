package synth

// fm.go — phase-modulation operators, the DX7's building block.
//
// "FM" synths modulate phase, not frequency: the modulator's output is added
// to the carrier's phase, so a modulator at an integer ratio keeps the
// spectrum harmonic and the pitch exact however deep the index goes. The
// self-feedback path averages the last two outputs, as the DX7 does, which
// keeps high feedback a buzzy saw instead of a chaotic hiss.

import "math"

type op struct {
	ph       float64
	fb1, fb2 float64
}

// tick returns sin(2π·phase + pm) and advances by dt cycles.
func (o *op) tick(dt, pm float64) float64 {
	v := math.Sin(2*math.Pi*o.ph + pm)
	o.ph += dt
	o.ph -= math.Floor(o.ph)
	return v
}

// tickFB is tick with self-feedback beta (radians at full output).
func (o *op) tickFB(dt, beta, pm float64) float64 {
	v := o.tick(dt, pm+beta*(o.fb1+o.fb2)/2)
	o.fb2, o.fb1 = o.fb1, v
	return v
}
