package synth

// recipes_vocal.go — sung vowels (recipes 119-121).
//
// A glottal source through five formants (formant.go). What keeps these from
// sounding like a vocoder is small: vibrato that fades in rather than
// starting at full depth, a breath component that puffs once per cycle, and
// for the chant, several voices that never quite agree on pitch.

import "math"

func vocalRecipes() []Recipe {
	v := func(name, base, desc string, tags []string, ps []Param, body func(*ctx) *out) Recipe {
		return rec(name, "vocal", base, desc, tags, ps, body)
	}
	return []Recipe{
		v("vox_ah", "C-4", "Sung 'ah'", []string{"vox", "vowel", "choir"}, []Param{
			par("vibrato", 0.4, 0, 1, "vibrato depth"),
			par("breath", 0.25, 0, 1, "breathiness"),
			par("shift", 1, 0.8, 1.25, "formant shift (tract size)"),
		}, voxVowel("tenor_a")),
		v("vox_ooh", "C-4", "Sung 'ooh'", []string{"vox", "vowel", "choir"}, []Param{
			par("vibrato", 0.4, 0, 1, "vibrato depth"),
			par("breath", 0.25, 0, 1, "breathiness"),
			par("shift", 1, 0.8, 1.25, "formant shift (tract size)"),
		}, voxVowel("alto_u")),
		v("vox_chant", "C-2", "Low chanted 'om', the vowel slowly morphing", []string{"vox", "chant", "drone", "stereo"}, []Param{
			par("voices", 3, 1, 5, "voices per side"),
			par("detune", 8, 0, 25, "voice spread in cents"),
			par("morph", 0.7, 0, 1, "vowel movement"),
		}, voxChant),
	}
}

func voxVowel(name string) func(c *ctx) *out {
	return func(c *ctx) *out {
		vib, breath, shift := c.v("vibrato"), c.v("breath"), c.v("shift")
		p := c.plan(0.6, 2.5)
		rate := p.q(5.2)
		g := newGlottal(c.rng, p.f0, breath, c.sr)
		fm := newFormants(vowels[name].shift(shift), c.sr)
		return loopMono(c, p, func(i int, t float64) float64 {
			// Vibrato arrives after the note settles, as a singer's does;
			// by the loop it is at full, steady depth.
			depth := vib * 25 * smooth((t-0.15)/0.4)
			f := p.f0 * math.Pow(2, depth*math.Sin(2*math.Pi*rate*t)/1200)
			return fm.tick(g.tick(f/c.sr)) * rise(t, 0.08)
		}).fade(0.05)
	}
}

func voxChant(c *ctx) *out {
	voices, d, morph := int(math.Round(c.v("voices"))), c.v("detune"), c.v("morph")
	p := c.plan(0.8, 4)
	n := p.frames()
	vo, vu, va := vowels["bass_o"], vowels["bass_u"], vowels["bass_a"]
	rate := p.q(0.25) // one o-u-o breath per loop
	side := func(r *rng, phOff float64) []float64 {
		src := make([]float64, n)
		var fs []float64
		if voices == 1 {
			fs = []float64{p.f0}
		} else {
			fs = spreadFreqs(p, voices, d, 0.1)
		}
		for _, f := range fs {
			g := newGlottal(r, f, 0.2, c.sr)
			g.o.ph = r.float()
			for i := range src {
				src[i] += g.tick(f / c.sr)
			}
		}
		fm := newFormants(vo, c.sr)
		return render(n, func(i int) float64 {
			t := c.t(i)
			if i%32 == 0 {
				m := 0.5 - 0.5*math.Cos(2*math.Pi*(rate*t+phOff))
				v := mixVowel(vo, vu, m*morph)
				v = mixVowel(v, va, 0.15*(1-m)*morph)
				fm.set(v)
			}
			return fm.tick(src[i]) * rise(t, 0.6)
		})
	}
	return stereo(side(c.rng.fork(), 0), side(c.rng.fork(), 0.04)).looped(p).fade(0.06)
}
