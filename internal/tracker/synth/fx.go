package synth

// fx.go — the processors between a source and a finished sample: drive,
// bit-crush, chorus, oversampling, retuning and the small buffer utilities
// the recipes share.
//
// Drive is the one that needs care. A saturator turns a 2 kHz harmonic into
// 6, 10, 14 kHz ones; at 48 kHz those above 24 kHz fold back as inharmonic
// grit that no filter after the fact can remove. driveBuf runs the curve at
// four times the rate between two steep FIR lowpasses, so only harmonics
// that exist come out.

import (
	"math"
	"sync"
)

// render fills n frames from a per-frame function.
func render(n int, f func(i int) float64) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = f(i)
	}
	return x
}

// softClip is a cubic saturator: linear near zero, flat at ±1.
func softClip(x float64) float64 {
	if x >= 1 {
		return 1
	}
	if x <= -1 {
		return -1
	}
	return 1.5*x - 0.5*x*x*x
}

// tube is an asymmetric tanh: the bias makes it clip one side first, which
// adds the even harmonics that read as "warm" rather than "fizzy".
func tube(x, drive, bias float64) float64 {
	return math.Tanh(drive*(x+bias)) - math.Tanh(drive*bias)
}

// driveBuf applies a memoryless curve to x in place, 4x oversampled.
func driveBuf(x []float64, curve func(float64) float64) {
	const m = 4
	up := upsample(x, m)
	for i, v := range up {
		up[i] = curve(v)
	}
	copy(x, decimate(up, m))
}

var (
	firMu    sync.Mutex
	firCache = map[int][]float64{}
)

// halfbandFIR is a Blackman-windowed sinc lowpass for an m-times rate change.
func halfbandFIR(m int) []float64 {
	firMu.Lock()
	defer firMu.Unlock()
	if h, ok := firCache[m]; ok {
		return h
	}
	taps := 24*m + 1
	h := make([]float64, taps)
	fc := 0.46 / float64(m) // of the high rate; just under the low Nyquist
	mid := float64(taps-1) / 2
	var sum float64
	for i := range h {
		x := float64(i) - mid
		s := 2 * fc
		if x != 0 {
			s = math.Sin(2*math.Pi*fc*x) / (math.Pi * x)
		}
		w := 0.42 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(taps-1)) + 0.08*math.Cos(4*math.Pi*float64(i)/float64(taps-1))
		h[i] = s * w
		sum += h[i]
	}
	for i := range h {
		h[i] /= sum
	}
	firCache[m] = h
	return h
}

// upsample raises x's rate by m (zero-stuff and filter), aligned so frame i
// of x lands on frame i*m of the result.
func upsample(x []float64, m int) []float64 {
	h := halfbandFIR(m)
	half := (len(h) - 1) / 2
	y := make([]float64, len(x)*m)
	for j := range y {
		// y[j] = m * sum_k h[k] * z[j + half - k], z nonzero at multiples of m.
		var acc float64
		k0 := (j + half) % m
		for k := k0; k < len(h); k += m {
			idx := (j + half - k) / m
			if idx >= 0 && idx < len(x) {
				acc += h[k] * x[idx]
			}
		}
		y[j] = acc * float64(m)
	}
	return y
}

// decimate lowers x's rate by m, the inverse alignment of upsample.
func decimate(x []float64, m int) []float64 {
	h := halfbandFIR(m)
	half := (len(h) - 1) / 2
	y := make([]float64, len(x)/m)
	for i := range y {
		c := i * m
		var acc float64
		for k := range h {
			if idx := c + half - k; idx >= 0 && idx < len(x) {
				acc += h[k] * x[idx]
			}
		}
		y[i] = acc
	}
	return y
}

// crush quantizes to `bits` and holds each value for 1/holdHz seconds: the
// sound of an early sampler, aliasing and all, which is the point.
func crush(x []float64, sr, bits, holdHz float64) {
	steps := math.Pow(2, bits-1)
	var ph, held float64
	first := true
	for i, v := range x {
		ph += holdHz / sr
		if ph >= 1 || first {
			ph -= math.Floor(ph)
			held = math.Round(v*steps) / steps
			first = false
		}
		x[i] = held
	}
}

// quantize rounds to a bit depth without the hold.
func quantize(x []float64, bits float64) {
	steps := math.Pow(2, bits-1)
	for i, v := range x {
		x[i] = math.Round(v*steps) / steps
	}
}

// readFrac reads x at a fractional index with Hermite interpolation.
func readFrac(x []float64, p float64) float64 {
	i := int(math.Floor(p))
	t := p - float64(i)
	at := func(k int) float64 {
		if k < 0 || k >= len(x) {
			return 0
		}
		return x[k]
	}
	return hermite(t, at(i-1), at(i), at(i+1), at(i+2))
}

// chorus is an ensemble of modulated delays, alternating left and right —
// the string machine's and the rotary's motion. rates are each voice's LFO
// in Hz (snap them to the loop's grid); base and depth are seconds.
func chorus(x []float64, sr, base, depth float64, rates []float64, wet float64) (l, r []float64) {
	l, r = make([]float64, len(x)), make([]float64, len(x))
	n := len(rates)
	for i := range x {
		t := float64(i) / sr
		var sl, sr2 float64
		for k, rate := range rates {
			ph := float64(k) / float64(n)
			d := (base + depth*(0.5+0.5*math.Sin(2*math.Pi*(rate*t+ph)))) * sr
			v := readFrac(x, float64(i)-d)
			if k&1 == 0 {
				sl += v
			} else {
				sr2 += v
			}
		}
		half := float64((n + 1) / 2)
		l[i] = x[i]*(1-wet) + wet*sl/half
		r[i] = x[i]*(1-wet) + wet*sr2/math.Max(1, float64(n/2))
	}
	return l, r
}

// resample reads x at `speed` times its rate: speed > 1 raises the pitch.
func resample(x []float64, speed float64) []float64 {
	n := int(float64(len(x)) / speed)
	y := make([]float64, n)
	for i := range y {
		y[i] = readFrac(x, float64(i)*speed)
	}
	return y
}

// measurePitch estimates the fundamental of x near guess Hz by normalized
// autocorrelation with parabolic refinement. Physical models (bowed
// strings) drift a few cents from their nominal length; this is how they
// are put back in tune.
func measurePitch(x []float64, sr, guess float64) float64 {
	P := sr / guess
	lo, hi := int(P*0.8), int(P*1.25)+2
	if hi*2 >= len(x) {
		return guess
	}
	n := len(x) - hi - 2
	if n > int(sr*0.5) {
		n = int(sr * 0.5)
	}
	corr := func(lag int) float64 {
		var a, b, c float64
		for i := 0; i < n; i++ {
			a += x[i] * x[i+lag]
			b += x[i] * x[i]
			c += x[i+lag] * x[i+lag]
		}
		return a / math.Sqrt(b*c+1e-30)
	}
	best, bv := lo, -2.0
	vals := make([]float64, hi+2)
	for lag := lo - 1; lag <= hi+1; lag++ {
		vals[lag] = corr(lag)
	}
	for lag := lo; lag <= hi; lag++ {
		if vals[lag] > bv {
			best, bv = lag, vals[lag]
		}
	}
	a, b, c := vals[best-1], vals[best], vals[best+1]
	off := 0.0
	if d := a - 2*b + c; d != 0 {
		off = 0.5 * (a - c) / d
	}
	return sr / (float64(best) + off)
}

// panGains is equal-power panning, p in [-1, 1].
func panGains(p float64) (gl, gr float64) {
	a := (clamp(p, -1, 1) + 1) * math.Pi / 4
	return math.Cos(a) * math.Sqrt2, math.Sin(a) * math.Sqrt2
}

// addTo mixes src into dst at gain g.
func addTo(dst, src []float64, g float64) {
	for i := range dst {
		if i >= len(src) {
			break
		}
		dst[i] += g * src[i]
	}
}

// scale multiplies x by g in place.
func scale(x []float64, g float64) {
	for i := range x {
		x[i] *= g
	}
}

// reverse returns x backwards.
func reverse(x []float64) []float64 {
	y := make([]float64, len(x))
	for i, v := range x {
		y[len(x)-1-i] = v
	}
	return y
}

// peak1 is the peak of one buffer.
func peak1(x []float64) float64 { return peakOf([][]float64{x}) }

// norm1 normalizes one buffer to peak 1 (no-op on silence).
func norm1(x []float64) []float64 {
	normalize([][]float64{x}, 1)
	return x
}
