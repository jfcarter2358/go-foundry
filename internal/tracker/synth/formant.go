package synth

// formant.go — voices: a glottal buzz shaped by the vocal tract.
//
// A sung vowel is a harmonic-rich source (the vocal folds) filtered by a
// handful of resonances (the throat and mouth) whose frequencies do not move
// with pitch. That last part is why a formant filter beats a fixed lowpass:
// transpose a recorded "ah" and it turns into a chipmunk, but the same tract
// over a higher buzz still says "ah". The five-formant tables are the
// classic per-voice-type measurements (as in Csound's formant tables).

import "math"

type vowel struct {
	f, bw, dB [5]float64
}

// vowels by voice type and vowel.
var vowels = map[string]vowel{
	"bass_a":  {[5]float64{600, 1040, 2250, 2450, 2750}, [5]float64{60, 70, 110, 120, 130}, [5]float64{0, -7, -9, -9, -20}},
	"bass_o":  {[5]float64{400, 750, 2400, 2600, 2900}, [5]float64{40, 80, 100, 120, 120}, [5]float64{0, -11, -21, -20, -40}},
	"bass_u":  {[5]float64{350, 600, 2400, 2675, 2950}, [5]float64{40, 80, 100, 120, 120}, [5]float64{0, -20, -32, -28, -36}},
	"tenor_a": {[5]float64{650, 1080, 2650, 2900, 3250}, [5]float64{80, 90, 120, 130, 140}, [5]float64{0, -6, -7, -8, -22}},
	"tenor_o": {[5]float64{400, 800, 2600, 2800, 3000}, [5]float64{40, 80, 100, 120, 120}, [5]float64{0, -10, -12, -12, -26}},
	"tenor_u": {[5]float64{350, 600, 2700, 2900, 3300}, [5]float64{40, 60, 100, 120, 120}, [5]float64{0, -20, -17, -14, -26}},
	"alto_a":  {[5]float64{800, 1150, 2800, 3500, 4950}, [5]float64{80, 90, 120, 130, 140}, [5]float64{0, -4, -20, -36, -60}},
	"alto_o":  {[5]float64{450, 800, 2830, 3500, 4950}, [5]float64{70, 80, 100, 130, 135}, [5]float64{0, -9, -16, -28, -55}},
	"alto_u":  {[5]float64{325, 700, 2530, 3500, 4950}, [5]float64{50, 60, 170, 180, 200}, [5]float64{0, -12, -30, -40, -64}},
}

// mixVowel interpolates two vowels: frequencies geometrically, the rest
// linearly — the way a tract actually slides between shapes.
func mixVowel(a, b vowel, x float64) vowel {
	var v vowel
	for i := 0; i < 5; i++ {
		v.f[i] = a.f[i] * math.Pow(b.f[i]/a.f[i], x)
		v.bw[i] = a.bw[i] + (b.bw[i]-a.bw[i])*x
		v.dB[i] = a.dB[i] + (b.dB[i]-a.dB[i])*x
	}
	return v
}

// shift scales a vowel's formants: >1 is a smaller tract (brighter, younger).
func (v vowel) shift(k float64) vowel {
	for i := range v.f {
		v.f[i] *= k
	}
	return v
}

// formants is five parallel bandpasses weighted by the vowel's levels.
type formants struct {
	bp   [5]svf
	gain [5]float64
	sr   float64
}

func newFormants(v vowel, sr float64) *formants {
	f := &formants{sr: sr}
	f.set(v)
	return f
}

// set retunes the bank; cheap enough to call every few frames for a morph.
func (f *formants) set(v vowel) {
	for i := 0; i < 5; i++ {
		q := v.f[i] / v.bw[i]
		f.bp[i].set(v.f[i], q, f.sr)
		// An SVF bandpass peaks at 1/k = q; normalize so dB is the peak.
		f.gain[i] = math.Pow(10, v.dB[i]/20) / q
	}
}

func (f *formants) tick(x float64) float64 {
	var y float64
	for i := range f.bp {
		_, bp, _ := f.bp[i].tick(x)
		y += f.gain[i] * bp
	}
	return y
}

// glottal is the voice source: a band-limited saw, tilted down another
// 6 dB/octave above 1 kHz. The glottal pulse itself falls at 12 dB/octave,
// but the lips radiate +6, so what reaches a listener is close to a saw
// with a softened top. Aspiration noise puffs once a cycle as the folds
// open.
type glottal struct {
	o    osc
	tilt onePole
	r    *rng
	air  float64
}

func newGlottal(r *rng, f, air, sr float64) *glottal {
	return &glottal{tilt: newOnePole(math.Max(1000, 3*f), sr), r: r, air: air}
}

func (g *glottal) tick(dt float64) float64 {
	v := g.tilt.lp(-g.o.saw(dt))
	if g.air > 0 {
		open := 0.3
		if g.o.ph < 0.4 {
			open = 1
		}
		v += g.air * open * g.r.white() * 0.3
	}
	return v
}
