package synth

// filter.go — the filters recipes shape sound with.
//
// Four kinds cover the library: one-poles for gentle tilts and smoothing,
// the TPT state-variable filter for anything swept (it stays stable however
// fast its cutoff moves), RBJ biquads for fixed EQ shapes — body resonances,
// formants, cabinets — and a zero-delay-feedback ladder for the 24 dB
// resonant lowpass a Moog or a 303 is remembered for. All run in float64:
// at a 30 Hz cutoff a float32 biquad's coefficients lose the filter.

import "math"

const denorm = 1e-18

// onePole is a 6 dB/octave lowpass; hp is its complement.
type onePole struct{ a, y float64 }

func newOnePole(fc, sr float64) onePole {
	var f onePole
	f.set(fc, sr)
	return f
}

func (f *onePole) set(fc, sr float64) {
	f.a = 1 - math.Exp(-2*math.Pi*clamp(fc, 1, 0.49*sr)/sr)
}

func (f *onePole) lp(x float64) float64 {
	f.y += f.a * (x - f.y)
	if f.y > -denorm && f.y < denorm {
		f.y = 0
	}
	return f.y
}

func (f *onePole) hp(x float64) float64 { return x - f.lp(x) }

// lpBuf runs `poles` one-pole lowpasses over x in place.
func lpBuf(x []float64, fc, sr float64, poles int) {
	for p := 0; p < poles; p++ {
		f := newOnePole(fc, sr)
		for i, v := range x {
			x[i] = f.lp(v)
		}
	}
}

// hpBuf runs `poles` one-pole highpasses over x in place.
func hpBuf(x []float64, fc, sr float64, poles int) {
	for p := 0; p < poles; p++ {
		f := newOnePole(fc, sr)
		for i, v := range x {
			x[i] = f.hp(v)
		}
	}
}

// svf is Zavalishin's topology-preserving state-variable filter.
type svf struct{ ic1, ic2, a1, a2, a3, k float64 }

func (f *svf) set(fc, q, sr float64) {
	g := math.Tan(math.Pi * clamp(fc, 5, 0.49*sr) / sr)
	f.k = 1 / math.Max(q, 0.05)
	f.a1 = 1 / (1 + g*(g+f.k))
	f.a2 = g * f.a1
	f.a3 = g * f.a2
}

// tick returns lowpass, bandpass and highpass outputs.
func (f *svf) tick(x float64) (lp, bp, hp float64) {
	v3 := x - f.ic2
	v1 := f.a1*f.ic1 + f.a2*v3
	v2 := f.ic2 + f.a2*f.ic1 + f.a3*v3
	f.ic1 = 2*v1 - f.ic1
	f.ic2 = 2*v2 - f.ic2
	if math.Abs(f.ic1) < denorm {
		f.ic1 = 0
	}
	if math.Abs(f.ic2) < denorm {
		f.ic2 = 0
	}
	return v2, v1, x - f.k*v1 - v2
}

// biquad is a direct-form-II-transposed second-order section.
type biquad struct{ b0, b1, b2, a1, a2, z1, z2 float64 }

func (b *biquad) tick(x float64) float64 {
	y := b.b0*x + b.z1
	b.z1 = b.b1*x - b.a1*y + b.z2
	b.z2 = b.b2*x - b.a2*y
	if math.Abs(b.z1) < denorm {
		b.z1 = 0
	}
	if math.Abs(b.z2) < denorm {
		b.z2 = 0
	}
	return y
}

func (b *biquad) run(x []float64) {
	for i, v := range x {
		x[i] = b.tick(v)
	}
}

func rbj(b0, b1, b2, a0, a1, a2 float64) biquad {
	return biquad{b0: b0 / a0, b1: b1 / a0, b2: b2 / a0, a1: a1 / a0, a2: a2 / a0}
}

func rbjW(fc, q, sr float64) (cw, alpha float64) {
	w := 2 * math.Pi * clamp(fc, 5, 0.49*sr) / sr
	return math.Cos(w), math.Sin(w) / (2 * q)
}

func bqLowpass(fc, q, sr float64) biquad {
	cw, al := rbjW(fc, q, sr)
	return rbj((1-cw)/2, 1-cw, (1-cw)/2, 1+al, -2*cw, 1-al)
}

func bqHighpass(fc, q, sr float64) biquad {
	cw, al := rbjW(fc, q, sr)
	return rbj((1+cw)/2, -(1 + cw), (1+cw)/2, 1+al, -2*cw, 1-al)
}

// bqBandpass has 0 dB at its peak, so a bank of them can be weighted by
// level directly.
func bqBandpass(fc, q, sr float64) biquad {
	cw, al := rbjW(fc, q, sr)
	return rbj(al, 0, -al, 1+al, -2*cw, 1-al)
}

func bqPeak(fc, q, dB, sr float64) biquad {
	cw, al := rbjW(fc, q, sr)
	A := math.Pow(10, dB/40)
	return rbj(1+al*A, -2*cw, 1-al*A, 1+al/A, -2*cw, 1-al/A)
}

func bqLowShelf(fc, dB, sr float64) biquad {
	cw, al := rbjW(fc, 0.707, sr)
	A := math.Pow(10, dB/40)
	sa := 2 * math.Sqrt(A) * al
	return rbj(A*((A+1)-(A-1)*cw+sa), 2*A*((A-1)-(A+1)*cw), A*((A+1)-(A-1)*cw-sa),
		(A+1)+(A-1)*cw+sa, -2*((A-1)+(A+1)*cw), (A+1)+(A-1)*cw-sa)
}

func bqHighShelf(fc, dB, sr float64) biquad {
	cw, al := rbjW(fc, 0.707, sr)
	A := math.Pow(10, dB/40)
	sa := 2 * math.Sqrt(A) * al
	return rbj(A*((A+1)+(A-1)*cw+sa), -2*A*((A-1)+(A+1)*cw), A*((A+1)+(A-1)*cw-sa),
		(A+1)-(A-1)*cw+sa, 2*((A-1)-(A+1)*cw), (A+1)-(A-1)*cw-sa)
}

// ladder is a four-pole resonant lowpass. The linear part of the feedback
// loop is solved exactly each frame (zero-delay feedback), so resonance
// sits at the cutoff at any setting; a tanh on the stage input supplies the
// compression a transistor ladder adds as resonance rises.
type ladder struct{ s [4]float64 }

// tick filters x; res 0..1 (self-oscillation near 1).
func (l *ladder) tick(x, fc, res, sr float64) float64 {
	g := math.Tan(math.Pi * clamp(fc, 10, 0.45*sr) / sr)
	G := g / (1 + g)
	k := 4 * clamp(res, 0, 0.995)
	S := G*G*G*(1-G)*l.s[0] + G*G*(1-G)*l.s[1] + G*(1-G)*l.s[2] + (1-G)*l.s[3]
	G4 := G * G * G * G
	x *= 1 + 0.6*k // give back the passband the feedback takes
	y4 := (G4*x + S) / (1 + k*G4)
	u := math.Tanh(x - k*y4)
	for i := range l.s {
		v := (u - l.s[i]) * G
		y := v + l.s[i]
		l.s[i] = y + v
		if math.Abs(l.s[i]) < denorm {
			l.s[i] = 0
		}
		u = y
	}
	return u
}

// reson is a two-pole resonator: one mode of a struck or bowed object. An
// impulse in rings out as a unit-amplitude sine decaying over t60.
type reson struct{ c1, c2, b0, y1, y2 float64 }

func newReson(f, t60s, sr float64) reson {
	w := 2 * math.Pi * f / sr
	r := math.Exp(-6.907755 / (t60s * sr))
	return reson{c1: 2 * r * math.Cos(w), c2: -r * r, b0: math.Sin(w)}
}

func (r *reson) tick(x float64) float64 {
	y := r.b0*x + r.c1*r.y1 + r.c2*r.y2
	r.y2, r.y1 = r.y1, y
	if math.Abs(y) < denorm {
		r.y1 = 0
	}
	return y
}

// eq is a chain of biquads run over a buffer: a cabinet, a body, a formant
// set.
func eq(x []float64, chain ...biquad) {
	for i := range chain {
		chain[i].run(x)
	}
}
