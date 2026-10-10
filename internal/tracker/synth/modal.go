package synth

// modal.go — struck and rubbed objects as banks of modes.
//
// A drumhead, a bar or a cymbal rings at a set of resonant frequencies whose
// ratios are fixed by its shape: an ideal membrane's are the zeros of Bessel
// functions (1, 1.59, 2.14, ...), a free bar's go 1, 2.76, 5.40, and a
// marimba bar is carved until its first three sit at 1:4:10. Each mode is a
// decaying sinusoid; the higher ones die faster. That table of ratios,
// amplitudes and decay times is most of an instrument's identity, so the
// recipes are mostly tables.
//
// Struck modes render as free decaying sinusoids (exact and cheap); modes
// that must be driven by a continuous excitation — friction, breath — are
// two-pole resonators the excitation is fed through.

import "math"

// Mode-frequency ratios for the shapes the library uses.
var (
	// membraneRatios: ideal circular membrane, (0,1) first.
	membraneRatios = []float64{1, 1.594, 2.136, 2.296, 2.653, 2.918, 3.156, 3.501, 3.600, 3.652, 4.060}
	// freeBarRatios: a uniform bar free at both ends (glockenspiel, triangle).
	freeBarRatios = []float64{1, 2.756, 5.404, 8.933, 13.344, 18.638}
	// tineRatios: a bar clamped at one end (music box comb, kalimba).
	tineRatios = []float64{1, 6.267, 17.547, 34.386}
)

// ringGlide renders struck modes whose frequencies all scale by glide(t): a
// drumhead's tension relaxing after the hit pulls every mode flat together.
func ringGlide(dst []float64, sr float64, ps []partial, glide func(t float64) float64) {
	for _, p := range ps {
		if p.amp == 0 {
			continue
		}
		ph := p.ph
		g, dec := p.amp, math.Exp(-6.907755/(p.t60*sr))
		na := int(p.attack * sr)
		for i := range dst {
			t := float64(i) / sr
			f := p.f * glide(t)
			if f > 0.45*sr {
				ph += f / sr
				g *= dec
				continue
			}
			v := math.Sin(2*math.Pi*ph) * g
			if i < na {
				v *= 0.5 - 0.5*math.Cos(math.Pi*float64(i)/float64(na))
			}
			dst[i] += v
			ph += f / sr
			if ph >= 1 {
				ph -= math.Floor(ph)
			}
			g *= dec
			if g < 1e-9*p.amp {
				break
			}
		}
	}
}

// modes scales a ratio table to a fundamental, with an amplitude and a decay
// per mode computed from its index.
func modes(f float64, ratios []float64, amp func(i int) float64, decay func(i int) float64) []partial {
	ps := make([]partial, len(ratios))
	for i, r := range ratios {
		ps[i] = partial{f: f * r, amp: amp(i), t60: decay(i)}
	}
	return ps
}

// modeBank drives resonators with a continuous excitation.
type modeBank struct {
	rs    []reson
	gains []float64
}

func newModeBank(ps []partial, sr float64) *modeBank {
	b := &modeBank{}
	for _, p := range ps {
		if p.f <= 0 || p.f > 0.45*sr {
			continue
		}
		b.rs = append(b.rs, newReson(p.f, p.t60, sr))
		b.gains = append(b.gains, p.amp)
	}
	return b
}

func (b *modeBank) tick(x float64) float64 {
	var y float64
	for i := range b.rs {
		y += b.gains[i] * b.rs[i].tick(x)
	}
	return y
}

// strike is a mallet or stick contact: a raised-cosine force pulse of `width`
// seconds. A soft mallet is a wide pulse, which is a lowpass on everything it
// excites — that one number is the difference between felt and plastic.
func strike(n int, sr, width float64) []float64 {
	w := int(width * sr)
	if w < 1 {
		w = 1
	}
	x := make([]float64, n)
	for i := 0; i < w && i < n; i++ {
		x[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(w))
	}
	return x
}
