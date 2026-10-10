package synth

// recipes_bass.go — basses (recipes 41-54), all based on C-2.
//
// Most basses loop: a tracker bass is held for as long as the pattern says,
// and its instrument envelope, not the sample, decides when it stops. The
// sample supplies the attack and a steady body. Plucked basses get there by
// holding the string's level after the attack (flatten); synth basses by
// letting every envelope finish before the loop starts.

import "math"

func bassRecipes() []Recipe {
	b := func(name, desc string, tags []string, ps []Param, body func(*ctx) *out) Recipe {
		return rec(name, "bass", "C-2", desc, tags, ps, body)
	}
	return []Recipe{
		b("bass_sub", "Pure sub sine with a hint of second harmonic", []string{"sub", "sine", "clean"}, []Param{
			par("harmonic", 0.1, 0, 0.5, "second harmonic level"),
			par("warmth", 0.03, 0, 0.3, "third harmonic level"),
		}, bassSub),
		b("bass_moog", "Fat analog bass: two detuned saws into a ladder filter", []string{"analog", "moog", "fat"}, []Param{
			par("cutoff", 650, 150, 4000, "filter cutoff in Hz"),
			par("resonance", 0.35, 0, 0.9, "filter resonance"),
			par("detune", 6, 0, 25, "saw detune in cents"),
			par("env", 0.5, 0, 1, "filter envelope on the attack"),
			par("sub", 0.3, 0, 1, "square sub-octave level"),
		}, bassMoog),
		b("bass_acid", "Raw saw for a resonant instrument filter to chew on", []string{"acid", "303", "saw"}, []Param{
			par("shape", 0, 0, 1, "saw to square"),
			par("drive", 0.2, 0, 1, "saturation"),
		}, bassAcid),
		b("bass_fm", "Two-operator FM bass with a plucky index envelope", []string{"fm", "digital", "punchy"}, []Param{
			par("index", 1.2, 0, 5, "sustained modulation index"),
			par("punch", 4, 0, 10, "attack modulation index"),
			par("ratio", 1, 0.5, 4, "modulator ratio (snapped to halves)"),
			par("feedback", 0.1, 0, 1.5, "modulator self-feedback"),
		}, bassFM),
		b("bass_reese", "Reese: detuned saws drifting slowly in and out of phase", []string{"reese", "dnb", "detuned"}, []Param{
			par("detune", 8, 2, 30, "detune in cents"),
			par("cutoff", 1200, 300, 6000, "lowpass in Hz"),
			par("drive", 0.3, 0, 1, "saturation"),
		}, bassReese),
		b("bass_finger", "Fingered electric bass", []string{"electric", "finger", "plucked"}, []Param{
			par("brightness", 0.35, 0, 1, "string brightness"),
			par("decay", 3, 0.8, 8, "string decay in seconds"),
			par("pick", 0.2, 0.05, 0.5, "pluck position from the bridge"),
		}, bassKS(false)),
		b("bass_slap", "Slap bass: thumb pop and bright string", []string{"electric", "slap", "funk"}, []Param{
			par("brightness", 0.85, 0.3, 1, "string brightness"),
			par("decay", 1.2, 0.3, 4, "seconds"),
			par("thump", 0.6, 0, 1, "thumb transient"),
		}, bassSlap),
		b("bass_upright", "Upright bass: round pluck through a wooden body", []string{"acoustic", "upright", "jazz"}, []Param{
			par("body", 0.6, 0, 1, "body resonance"),
			par("brightness", 0.25, 0, 1, "string brightness"),
			par("decay", 2.2, 0.6, 5, "seconds"),
		}, bassUpright),
		b("bass_pick", "Picked electric bass, near the bridge", []string{"electric", "pick", "plucked"}, []Param{
			par("brightness", 0.6, 0, 1, "string brightness"),
			par("decay", 2.5, 0.8, 8, "string decay in seconds"),
			par("pick", 0.08, 0.03, 0.3, "pick position from the bridge"),
		}, bassKS(true)),
		b("bass_wobble", "Saw stack and square, unfiltered: wobble it with the filter", []string{"wobble", "dubstep", "saw"}, []Param{
			par("detune", 10, 0, 30, "saw detune in cents"),
			par("square", 0.5, 0, 1, "square level"),
			par("sub", 0.3, 0, 1, "sine sub level"),
		}, bassWobble),
		b("bass_rubber", "Rubbery sine that drops a semitone into each note", []string{"sine", "rubber", "glide"}, []Param{
			par("drop", 1, 0, 12, "semitones above the note it starts"),
			par("glide", 0.12, 0.02, 0.5, "seconds to land"),
			par("decay", 1, 0.3, 3, "seconds"),
			par("drive", 0.3, 0, 1, "saturation"),
		}, bassRubber),
		b("bass_pulse", "25% pulse with slow pulse-width modulation", []string{"pulse", "pwm", "analog"}, []Param{
			par("width", 0.25, 0.05, 0.5, "pulse width"),
			par("depth", 0.1, 0, 0.2, "PWM depth"),
			par("rate", 0.5, 0.25, 4, "PWM rate in Hz"),
		}, bassPulse),
		b("bass_growl", "Growling FM feedback bass through a moving vowel", []string{"growl", "fm", "vowel"}, []Param{
			par("feedback", 1.3, 0, 2, "operator feedback"),
			par("index", 2.5, 0, 6, "modulation index"),
			par("vowel", 0.5, 0, 1, "o to a"),
			par("motion", 0.3, 0, 1, "vowel movement"),
		}, bassGrowl),
		b("bass_dist", "Distorted saw-and-square through a speaker cabinet", []string{"distorted", "rock", "gritty"}, []Param{
			par("drive", 0.6, 0, 1, "distortion"),
			par("tone", 4000, 1500, 9000, "cabinet rolloff in Hz"),
			par("square", 0.5, 0, 1, "square level"),
		}, bassDist),
	}
}

// loopMono renders a looped mono voice frame by frame.
func loopMono(c *ctx, p plan, f func(i int, t float64) float64) *out {
	return mono(render(p.frames(), func(i int) float64 { return f(i, c.t(i)) })).looped(p)
}

// detune is f moved by cents.
func detune(f, cents float64) float64 { return f * math.Pow(2, cents/1200) }

func bassSub(c *ctx) *out {
	p := c.plan(0.1, 1.5)
	h2, h3 := c.v("harmonic"), c.v("warmth")
	var ph float64
	return loopMono(c, p, func(i int, t float64) float64 {
		a := 2 * math.Pi * ph
		ph += p.f0 / c.sr
		ph -= math.Floor(ph)
		return (math.Sin(a) + h2*math.Sin(2*a) + h3*math.Sin(3*a)) * fadeIn(t, 0.005)
	})
}

func bassMoog(c *ctx) *out {
	p := c.plan(0.45, 1.6)
	d := c.v("detune")
	f1, f2, fs := p.q(detune(p.f0, d)), p.q(detune(p.f0, -d)), p.f0/2
	cut, res, env, sub := c.v("cutoff"), c.v("resonance"), c.v("env"), c.v("sub")
	var a, b, s osc
	b.ph = 0.3
	var lad ladder
	return loopMono(c, p, func(i int, t float64) float64 {
		v := 0.5*(a.saw(f1/c.sr)+b.saw(f2/c.sr)) + 0.5*sub*s.square(fs/c.sr)
		fe := 0.0
		if t < 0.3 {
			x := 1 - t/0.3
			fe = x * x * x
		}
		y := lad.tick(0.6*v, cut*math.Exp2(3.5*env*fe), res, c.sr)
		return y * ads(t, 0.004, 0.3, 0.85)
	})
}

func bassAcid(c *ctx) *out {
	p := c.plan(0.1, 1.2)
	shape, drive := c.v("shape"), c.v("drive")
	var o osc
	var q osc
	g := 1 + 4*drive
	return loopMono(c, p, func(i int, t float64) float64 {
		v := (1-shape)*o.saw(p.f0/c.sr) + shape*q.square(p.f0/c.sr)
		return math.Tanh(g*v) / math.Tanh(g) * fadeIn(t, 0.003)
	})
}

func bassFM(c *ctx) *out {
	p := c.plan(0.45, 1.5)
	idx, punch, fb := c.v("index"), c.v("punch"), c.v("feedback")
	ratio := math.Max(0.5, math.Round(c.v("ratio")*2)/2)
	fm := p.q(p.f0 * ratio)
	var car, mod op
	return loopMono(c, p, func(i int, t float64) float64 {
		k := idx
		if t < 0.25 {
			x := 1 - t/0.25
			k += (punch - idx) * x * x
		}
		m := mod.tickFB(fm/c.sr, fb, 0)
		return car.tick(p.f0/c.sr, k*m) * ads(t, 0.002, 0.4, 0.8)
	})
}

func bassReese(c *ctx) *out {
	p := c.plan(0.2, 3.2)
	d, cut, drive := c.v("detune"), c.v("cutoff"), c.v("drive")
	fs := []float64{p.f0, p.q(detune(p.f0, d)), p.q(detune(p.f0, -d*1.6))}
	oscs := make([]osc, 3)
	for k := range oscs {
		oscs[k].ph = c.rng.float()
	}
	var lp svf
	lp.set(cut, 0.8, c.sr)
	var sub osc
	g := 1 + 3*drive
	return loopMono(c, p, func(i int, t float64) float64 {
		var v float64
		for k, f := range fs {
			v += oscs[k].saw(f / c.sr)
		}
		y, _, _ := lp.tick(v / 3)
		y += 0.3 * sub.sine(p.f0/c.sr)
		return math.Tanh(g*y) * fadeIn(t, 0.008)
	})
}

// bassKS is a plucked bass that sustains: the string is plucked, then its
// level is held so the loop has a body to repeat.
func bassKS(picked bool) func(c *ctx) *out {
	return func(c *ctx) *out {
		p := c.plan(0.6, 1.5)
		bright, decay, pick := c.v("brightness"), c.v("decay"), c.v("pick")
		k := newKS(c.sr, p.f0, decay, bright, 0)
		exBright := bright * 0.6
		if picked {
			exBright = 0.6 + 0.4*bright
		}
		x := runKS(k, pluck(c.rng, c.sr, p.f0, exBright, pick), p.frames())
		norm1(x)
		var att []float64
		if picked {
			att = noiseHit(c.rng, c.n(0.006), c.sr, func(x []float64) { hpBuf(x, 2000, c.sr, 1) },
				func(t float64) float64 { return math.Exp(-t / 0.001) })
		} else {
			att = noiseHit(c.rng, c.n(0.01), c.sr, func(x []float64) { lpBuf(x, 900, c.sr, 2) },
				func(t float64) float64 { return math.Exp(-t / 0.002) })
		}
		addTo(x, att, 0.12)
		flatten(x, c.n(0.4), c.sr, p.f0)
		return mono(x).looped(p).fade(0.05)
	}
}

func bassSlap(c *ctx) *out {
	bright, decay, thump := c.v("brightness"), c.v("decay"), c.v("thump")
	f := c.f0
	n := c.n(decay + 0.2)
	k := newKS(c.sr, f, decay, bright, 0)
	x := runKS(k, pluck(c.rng, c.sr, f, 1, 0.1), n)
	norm1(x)
	// The thumb hits the string against the last fret: a pop plus a bright
	// metallic click, and the string buzzes a little (soft clip).
	pop := strike(n, c.sr, 0.0015)
	addTo(x, pop, 0.5*thump)
	click := noiseHit(c.rng, c.n(0.01), c.sr, func(x []float64) {
		var bp svf
		bp.set(3000, 1.2, c.sr)
		for i, v := range x {
			_, b, _ := bp.tick(v)
			x[i] = b
		}
	}, func(t float64) float64 { return math.Exp(-t / 0.0015) })
	addTo(x, click, 0.3*thump)
	for i, v := range x {
		x[i] = math.Tanh(1.6 * v)
	}
	eq(x, bqPeak(2500, 0.8, 4, c.sr), bqLowShelf(120, 3, c.sr))
	return mono(x)
}

func bassUpright(c *ctx) *out {
	bodyAmt, bright, decay := c.v("body"), c.v("brightness"), c.v("decay")
	f := c.f0
	n := c.n(decay + 0.3)
	k := newKS(c.sr, f, decay, bright, 1800)
	exc := pluck(c.rng, c.sr, f, 0.3, 0.25)
	x := make([]float64, n)
	for i := range x {
		// Plucking stretches the string: pitch starts a touch sharp.
		k.setFreq(f * (1 + 0.012*math.Exp(-c.t(i)/0.03)))
		var e float64
		if i < len(exc) {
			e = exc[i]
		}
		x[i] = k.tick(e)
	}
	norm1(x)
	body(x, c.sr, 1-0.4*bodyAmt, []bodyMode{
		{95, 4, 0.9 * bodyAmt}, {180, 5, 0.6 * bodyAmt}, {270, 6, 0.4 * bodyAmt},
		{420, 4, 0.3 * bodyAmt}, {1100, 2, 0.15 * bodyAmt},
	})
	return mono(x)
}

func bassWobble(c *ctx) *out {
	p := c.plan(0.1, 1.5)
	d, sq, sub := c.v("detune"), c.v("square"), c.v("sub")
	fs := []float64{p.f0, p.q(detune(p.f0, d)), p.q(detune(p.f0, -d))}
	saws := make([]osc, 3)
	for k := range saws {
		saws[k].ph = c.rng.float()
	}
	var s, so osc
	return loopMono(c, p, func(i int, t float64) float64 {
		var v float64
		for k, f := range fs {
			v += saws[k].saw(f / c.sr)
		}
		v = v/3 + sq*0.6*s.square(p.f0/c.sr) + sub*so.sine(p.f0/c.sr)
		return v * fadeIn(t, 0.003)
	})
}

func bassRubber(c *ctx) *out {
	drop, glide, decay, drive := c.v("drop"), c.v("glide"), c.v("decay"), c.v("drive")
	f0 := c.f0
	g := 1 + 3*drive
	x := sineSweep(c, c.n(decay+0.2),
		func(t float64) float64 { return f0 * math.Pow(2, drop*(1-smooth(t/glide))/12) },
		func(t float64) float64 { return fadeIn(t, 0.003) * t60(t, decay) })
	for i, v := range x {
		x[i] = math.Tanh(g*v + 0.1*v*v)
	}
	return mono(x)
}

func bassPulse(c *ctx) *out {
	p := c.plan(0.1, 2)
	w, depth, rate := c.v("width"), c.v("depth"), p.q(c.v("rate"))
	var o osc
	var lp onePole
	lp.set(6000, c.sr)
	return loopMono(c, p, func(i int, t float64) float64 {
		pw := w + depth*math.Sin(2*math.Pi*rate*t)
		return lp.lp(o.pulse(p.f0/c.sr, pw)) * fadeIn(t, 0.003)
	})
}

func bassGrowl(c *ctx) *out {
	p := c.plan(0.2, 2)
	fb, idx, vow, motion := c.v("feedback"), c.v("index"), c.v("vowel"), c.v("motion")
	rate := p.q(1)
	var car, mod op
	var sub osc
	fm := newFormants(vowels["bass_o"], c.sr)
	vo, va := vowels["bass_o"], vowels["bass_a"]
	return loopMono(c, p, func(i int, t float64) float64 {
		if i%32 == 0 {
			m := clamp(vow+0.5*motion*math.Sin(2*math.Pi*rate*t), 0, 1)
			fm.set(mixVowel(vo, va, m).shift(0.9))
		}
		v := car.tick(p.f0/c.sr, idx*mod.tickFB(p.f0/c.sr, fb, 0))
		y := 2.5*fm.tick(v) + 0.25*v + 0.4*sub.sine(p.f0/c.sr)
		return math.Tanh(1.5*y) * fadeIn(t, 0.004)
	})
}

func bassDist(c *ctx) *out {
	p := c.plan(0.2, 1.5)
	drive, tone, sq := c.v("drive"), c.v("tone"), c.v("square")
	var a, b osc
	x := render(p.frames(), func(i int) float64 {
		return (a.saw(p.f0/c.sr) + sq*b.square(p.f0/c.sr)) * 0.5 * fadeIn(c.t(i), 0.003)
	})
	g := 2 + 20*drive
	driveBuf(x, func(v float64) float64 { return math.Tanh(g * v) })
	eq(x, bqHighpass(60, 0.7, c.sr), bqPeak(1100, 0.9, 3, c.sr),
		bqLowpass(tone, 0.7, c.sr), bqLowpass(tone*1.2, 0.6, c.sr))
	return mono(x).looped(p)
}
