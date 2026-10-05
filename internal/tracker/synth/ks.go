package synth

// ks.go — plucked strings (Karplus-Strong, as extended by Jaffe and Smith).
//
// A string is a delay line one period long that feeds back through a gentle
// lowpass: a burst of noise goes in, the loop keeps what repeats every
// period — the harmonics — and the lowpass makes the highs die first, which
// is most of what makes a pluck sound like a pluck.
//
// The extensions that matter here:
//   - the delay is read with Hermite interpolation at a fractional length, and
//     the loop filter's own phase delay is subtracted, so the string is in tune
//     to a cent instead of to the nearest whole sample (at C-5 that is ±10
//     cents), and the length can glide for a koto's bend;
//   - the loop filter is a symmetric three-tap FIR (exactly one sample of
//     delay at every frequency, so damping never detunes it) plus an optional
//     one-pole for the heavily damped strings — palm mutes, uprights;
//   - the excitation is shaped before it goes in: lowpassed for a soft finger
//     or bright for a pick, and comb-filtered for where along the string it
//     was plucked (near the bridge is thin and nasal, mid-string is round).

import "math"

type ks struct {
	buf    []float64
	mask   int
	w      int
	sr     float64
	a      float64 // FIR side tap: 0 = lossless, 0.25 = darkest
	y1, y2 float64
	damp   onePole
	damped bool
	g      float64
	d      float64 // read delay in frames
	t60    float64
}

// newKS is a string at f Hz whose fundamental falls 60 dB in decay seconds.
// bright 0..1 sets how long the highs last; damp, when > 0, is an extra
// one-pole cutoff in Hz inside the loop.
func newKS(sr, f, decay, bright, damp float64) *ks {
	size := 1
	for size < int(sr/20)+8 {
		size <<= 1
	}
	k := &ks{buf: make([]float64, size), mask: size - 1, sr: sr, t60: decay}
	k.a = 0.25 * (1 - clamp(bright, 0, 1))
	if damp > 0 {
		k.damp, k.damped = newOnePole(damp, sr), true
	}
	k.setFreq(f)
	return k
}

// setFreq retunes the string; safe to call every frame for a bend.
func (k *ks) setFreq(f float64) {
	D := k.sr / f
	tau := 1.0 // the FIR's delay
	if k.damped {
		w := 2 * math.Pi * f / k.sr
		b := 1 - k.damp.a
		tau += math.Atan2(b*math.Sin(w), 1-b*math.Cos(w)) / w
	}
	k.d = math.Max(D-tau, 3)
	k.g = math.Pow(10, -3*D/(k.sr*k.t60))
}

// tick feeds exc into the string and returns the string's output.
func (k *ks) tick(exc float64) float64 {
	p := float64(k.w) - k.d
	i := int(math.Floor(p))
	t := p - float64(i)
	m := k.mask
	y := hermite(t, k.buf[(i-1)&m], k.buf[i&m], k.buf[(i+1)&m], k.buf[(i+2)&m])
	v := k.a*y + (1-2*k.a)*k.y1 + k.a*k.y2
	k.y2, k.y1 = k.y1, y
	if k.damped {
		v = k.damp.lp(v)
	}
	v *= k.g
	if math.Abs(v) < denorm {
		v = 0
	}
	k.buf[k.w&m] = v + exc
	k.w++
	return y
}

func hermite(t, y0, y1, y2, y3 float64) float64 {
	c1 := 0.5 * (y2 - y0)
	c2 := y0 - 2.5*y1 + 2*y2 - 0.5*y3
	c3 := 0.5*(y3-y0) + 1.5*(y1-y2)
	return ((c3*t+c2)*t+c1)*t + y1
}

// pluck is the excitation for a string at f: one period of noise, its
// brightness and pick position shaped, mean removed and peak-normalized.
// pick is the fraction of the string from the bridge (0.5 = middle).
func pluck(r *rng, sr, f, bright, pick float64) []float64 {
	n := int(sr / f)
	if n < 4 {
		n = 4
	}
	e := r.noiseBuf(n)
	fc := 300 + bright*bright*16000
	lpBuf(e, fc, sr, 2)
	if pick > 0 {
		off := int(math.Round(pick * float64(n)))
		c := make([]float64, n)
		for i := range e {
			c[i] = e[i]
			if i >= off {
				c[i] -= e[i-off]
			}
		}
		e = c
	}
	var m float64
	for _, v := range e {
		m += v
	}
	m /= float64(n)
	for i := range e {
		e[i] -= m
	}
	normalize([][]float64{e}, 1)
	return e
}

// runKS plucks a string and renders n frames of it.
func runKS(k *ks, exc []float64, n int) []float64 {
	x := make([]float64, n)
	for i := range x {
		var e float64
		if i < len(exc) {
			e = exc[i]
		}
		x[i] = k.tick(e)
	}
	return x
}

// bodyMode is one resonance of an instrument body.
type bodyMode struct{ f, q, gain float64 }

// body adds an instrument body's resonances to a string: the dry signal at
// `dry` plus each mode's bandpass at its gain. Strings alone sound like wire;
// the body is where a guitar, an upright or a cello gets its identity.
func body(x []float64, sr, dry float64, modes []bodyMode) {
	src := append([]float64(nil), x...)
	for i := range x {
		x[i] *= dry
	}
	for _, m := range modes {
		bq := bqBandpass(m.f, m.q, sr)
		for i, v := range src {
			x[i] += m.gain * bq.tick(v)
		}
	}
}
