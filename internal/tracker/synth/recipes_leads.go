package synth

// recipes_leads.go — leads (recipes 55-68), based on C-4.
//
// Leads are the most exposed sounds in a mix and the ones most often played
// high, where aliasing is loudest; every discontinuous oscillator here is
// polyBLEP and every saturator is oversampled. Detuned copies, LFOs and
// beating partials are snapped to the loop grid so the loops are seamless.

import "math"

func leadRecipes() []Recipe {
	l := func(name, desc string, tags []string, ps []Param, body func(*ctx) *out) Recipe {
		return rec(name, "leads", "C-4", desc, tags, ps, body)
	}
	return []Recipe{
		l("lead_supersaw", "Seven detuned saws spread across the stereo field", []string{"supersaw", "trance", "stereo"}, []Param{
			par("spread", 0.2, 0, 1, "detune amount"),
			par("mix", 0.6, 0, 1, "side saws against the centre one"),
			par("width", 0.8, 0, 1, "stereo width"),
		}, leadSupersaw),
		l("lead_square", "Square wave, gently filtered", []string{"square", "classic"}, []Param{
			par("cutoff", 3000, 500, 12000, "lowpass in Hz"),
			par("width", 0.5, 0.1, 0.5, "pulse width"),
		}, leadSquare),
		l("lead_pwm", "Pulse-width-modulated lead", []string{"pwm", "pulse", "analog"}, []Param{
			par("rate", 1, 0.25, 6, "PWM rate in Hz"),
			par("depth", 0.7, 0, 1, "PWM depth"),
			par("cutoff", 4000, 800, 12000, "lowpass in Hz"),
		}, leadPWM),
		l("lead_sync", "Hard-sync saw with a sweeping slave", []string{"sync", "aggressive"}, []Param{
			par("ratio", 2.2, 1, 6, "slave pitch ratio"),
			par("sweep", 1, 0, 3, "sweep depth in ratio"),
			par("rate", 0.5, 0.25, 4, "sweep rate in Hz"),
		}, leadSync),
		l("lead_fm", "Two-operator FM lead, modulator at 3:1", []string{"fm", "digital", "bright"}, []Param{
			par("index", 1.6, 0, 5, "sustained modulation index"),
			par("punch", 3.5, 0, 8, "attack modulation index"),
			par("feedback", 0.2, 0, 1.5, "modulator feedback"),
		}, leadFM),
		l("lead_whistle", "Breathy whistled sine", []string{"whistle", "sine", "breathy"}, []Param{
			par("breath", 0.15, 0, 0.6, "breath noise"),
			par("attack", 0.06, 0.005, 0.3, "seconds"),
		}, leadWhistle),
		l("lead_pluck", "Plucky saw with a fast filter envelope", []string{"pluck", "saw", "short"}, []Param{
			par("fdecay", 0.25, 0.05, 1, "filter decay in seconds"),
			par("cutoff", 6000, 1000, 16000, "filter peak in Hz"),
			par("resonance", 0.3, 0, 0.9, "filter resonance"),
			par("decay", 1.2, 0.2, 3, "amplitude decay in seconds"),
		}, leadPluck),
		l("lead_hoover", "Rave hoover: detuned PWM saws with a pitch dip", []string{"hoover", "rave", "stereo"}, []Param{
			par("detune", 15, 0, 40, "voice detune in cents"),
			par("dip", 2, 0, 7, "pitch dip in semitones"),
			par("pwm", 0.3, 0, 0.45, "PWM depth"),
		}, leadHoover),
		l("lead_ocarina", "Ocarina: sine, soft third harmonic, breath", []string{"ocarina", "breathy", "soft"}, []Param{
			par("breath", 0.2, 0, 0.6, "breath noise"),
			par("harmonic", 0.06, 0, 0.3, "third harmonic level"),
		}, leadOcarina),
		l("lead_overdrive", "Overdriven saw through a guitar cabinet", []string{"overdrive", "rock", "distorted"}, []Param{
			par("drive", 0.6, 0, 1, "drive"),
			par("tone", 4500, 1500, 9000, "cabinet rolloff in Hz"),
			par("bias", 0.15, 0, 0.5, "tube asymmetry"),
		}, leadOverdrive),
		l("lead_acid", "Saw through a screaming resonant lowpass", []string{"acid", "303", "resonant"}, []Param{
			par("cutoff", 900, 200, 5000, "filter cutoff in Hz"),
			par("resonance", 0.85, 0, 0.97, "filter resonance"),
			par("env", 0.6, 0, 1, "filter envelope on the attack"),
		}, leadAcid),
		l("lead_vox", "Saw through an 'ah' formant", []string{"vox", "formant", "vocal"}, []Param{
			par("shift", 1, 0.7, 1.4, "formant shift"),
			par("bright", 0.15, 0, 1, "unfiltered saw mixed in"),
		}, leadVox),
		l("lead_glass", "Glassy odd harmonics, slowly beating", []string{"glass", "additive", "soft"}, []Param{
			par("beating", 0.5, 0, 1.5, "beat rate"),
			par("bright", 0.5, 0, 1, "upper harmonic level"),
		}, leadGlass),
		l("lead_crushed", "Saw crushed to 6 bits at 11 kHz", []string{"lofi", "bitcrush", "gritty"}, []Param{
			par("bits", 6, 3, 12, "bit depth"),
			par("rate", 11025, 2000, 24000, "sample-and-hold rate in Hz"),
		}, leadCrushed),
	}
}

func leadSupersaw(c *ctx) *out {
	p := c.plan(0.15, 3)
	spread, mix, width := c.v("spread"), c.v("mix"), c.v("width")
	// The JP-8000's detune offsets: uneven, so the beating never lines up.
	offs := []float64{-0.11002313, -0.06288439, -0.01952356, 0, 0.01991221, 0.06216538, 0.10745242}
	pans := []float64{-0.9, 0.6, -0.3, 0, 0.3, -0.6, 0.9}
	n := p.frames()
	l, r := make([]float64, n), make([]float64, n)
	for k, o := range offs {
		f := p.q(p.f0 * (1 + spread*0.5*o))
		amp := mix
		if o == 0 {
			amp, f = 1, p.f0
		}
		gl, gr := panGains(pans[k] * width)
		var s osc
		s.ph = c.rng.float()
		for i := 0; i < n; i++ {
			v := s.saw(f/c.sr) * amp
			l[i] += v * gl
			r[i] += v * gr
		}
	}
	hpBuf(l, p.f0*0.7, c.sr, 1)
	hpBuf(r, p.f0*0.7, c.sr, 1)
	for i := range l {
		g := fadeIn(c.t(i), 0.005)
		l[i] *= g
		r[i] *= g
	}
	return stereo(l, r).looped(p)
}

func leadSquare(c *ctx) *out {
	p := c.plan(0.1, 1.5)
	w := c.v("width")
	var o osc
	x := render(p.frames(), func(i int) float64 { return o.pulse(p.f0/c.sr, w) * fadeIn(c.t(i), 0.003) })
	lpBuf(x, c.v("cutoff"), c.sr, 2)
	return mono(x).looped(p)
}

func leadPWM(c *ctx) *out {
	p := c.plan(0.1, 2)
	rate, depth := p.q(c.v("rate")), c.v("depth")
	var o osc
	x := render(p.frames(), func(i int) float64 {
		t := c.t(i)
		return o.pulse(p.f0/c.sr, 0.5+0.42*depth*math.Sin(2*math.Pi*rate*t)) * fadeIn(t, 0.003)
	})
	lpBuf(x, c.v("cutoff"), c.sr, 2)
	return mono(x).looped(p)
}

func leadSync(c *ctx) *out {
	p := c.plan(0.1, 2)
	ratio, sw, rate := c.v("ratio"), c.v("sweep"), p.q(c.v("rate"))
	var s syncSaw
	x := render(p.frames(), func(i int) float64 {
		t := c.t(i)
		r := ratio + sw*(0.5-0.5*math.Cos(2*math.Pi*rate*t))
		return s.next(p.f0/c.sr, r*p.f0/c.sr) * fadeIn(t, 0.003)
	})
	lpBuf(x, 9000, c.sr, 1)
	return mono(x).looped(p)
}

func leadFM(c *ctx) *out {
	p := c.plan(0.4, 1.5)
	idx, punch, fb := c.v("index"), c.v("punch"), c.v("feedback")
	var car, mod op
	return loopMono(c, p, func(i int, t float64) float64 {
		k := idx
		if t < 0.3 {
			x := 1 - t/0.3
			k += (punch - idx) * x * x
		}
		return car.tick(p.f0/c.sr, k*mod.tickFB(3*p.f0/c.sr, fb, 0)) * ads(t, 0.004, 0.3, 0.85)
	})
}

// breathAt is noise focused around a frequency: at high Q onto the note,
// which makes a whistle or a flute sound blown rather than generated; at low
// Q broadband air.
func breathAt(r *rng, n int, sr, f, q float64) []float64 {
	var bp svf
	bp.set(f, q, sr)
	return norm1(render(n, func(i int) float64 {
		_, b, _ := bp.tick(r.white())
		return b
	}))
}

func leadWhistle(c *ctx) *out {
	p := c.plan(0.4, 1.5)
	breath, att := c.v("breath"), c.v("attack")
	n := p.frames()
	pitched := breathAt(c.rng, n, c.sr, p.f0, 12)
	air := breathAt(c.rng, n, c.sr, 2500, 0.7)
	var ph float64
	x := render(n, func(i int) float64 {
		t := c.t(i)
		// A whistle scoops up into the note.
		f := p.f0 * math.Pow(2, -0.3*(1-smooth(t/0.06))/12)
		a := 2 * math.Pi * ph
		ph += f / c.sr
		ph -= math.Floor(ph)
		v := math.Sin(a) + 0.04*math.Sin(2*a) + breath*(0.5*pitched[i]+0.15*air[i])
		return v * rise(t, att)
	})
	return mono(x).looped(p).fade(0.05)
}

func leadPluck(c *ctx) *out {
	fdecay, cut, res, decay := c.v("fdecay"), c.v("cutoff"), c.v("resonance"), c.v("decay")
	f := c.f0
	var a, b osc
	b.ph = 0.4
	var lp svf
	x := render(c.n(decay+0.1), func(i int) float64 {
		t := c.t(i)
		lp.set(250+(cut-250)*math.Exp(-t/fdecay), 0.7+4*res, c.sr)
		v := 0.5 * (a.saw(detune(f, 7)/c.sr) + b.saw(detune(f, -7)/c.sr))
		y, _, _ := lp.tick(v)
		return y * fadeIn(t, 0.002) * t60(t, decay)
	})
	return mono(x)
}

func leadHoover(c *ctx) *out {
	p := c.plan(0.45, 2)
	d, dip, pwm := c.v("detune"), c.v("dip"), c.v("pwm")
	n := p.frames()
	// The dip: down and back up in the first third of a second.
	bend := func(t float64) float64 {
		if t < 0.06 {
			return smooth(t / 0.06)
		}
		return 1 - smooth((t-0.06)/0.28)
	}
	side := func(sign float64, r *rng) []float64 {
		x := make([]float64, n)
		for _, cents := range []float64{-d, 0, d * 0.8} {
			f := p.q(detune(p.f0, sign*cents))
			if cents == 0 {
				f = p.f0
			}
			rate := p.q(r.rangef(0.4, 1.2))
			var s, q osc
			s.ph, q.ph = r.float(), r.float()
			lfoPh := r.float()
			for i := range x {
				t := c.t(i)
				fi := f * math.Pow(2, -dip*bend(t)/12)
				dt := fi / c.sr
				w := 0.5 + pwm*math.Sin(2*math.Pi*(rate*t+lfoPh))
				x[i] += s.saw(dt) + 0.7*q.pulse(dt, w)
			}
		}
		lpBuf(x, 6000, c.sr, 1)
		// PWM swings the pulses' DC at the LFO rate; take it out before the
		// saturator turns it into a lopsided clip.
		hpBuf(x, 20, c.sr, 1)
		for i := range x {
			x[i] = math.Tanh(0.6*x[i]) * fadeIn(c.t(i), 0.004)
		}
		return x
	}
	return stereo(side(1, c.rng.fork()), side(-1, c.rng.fork())).looped(p)
}

func leadOcarina(c *ctx) *out {
	p := c.plan(0.3, 1.5)
	breath, h3 := c.v("breath"), c.v("harmonic")
	n := p.frames()
	pitched := breathAt(c.rng, n, c.sr, p.f0, 20)
	air := breathAt(c.rng, n, c.sr, 1800, 0.5)
	chiff := noiseHit(c.rng, n, c.sr, func(x []float64) {
		var bp svf
		bp.set(3000, 1, c.sr)
		for i, v := range x {
			_, b, _ := bp.tick(v)
			x[i] = b
		}
	}, func(t float64) float64 { return math.Exp(-t / 0.025) })
	var ph float64
	x := render(n, func(i int) float64 {
		t := c.t(i)
		a := 2 * math.Pi * ph
		ph += p.f0 / c.sr
		ph -= math.Floor(ph)
		v := math.Sin(a) + 0.03*math.Sin(2*a) + h3*math.Sin(3*a)
		return (v+breath*(0.4*pitched[i]+0.1*air[i]))*rise(t, 0.04) + 0.25*chiff[i]
	})
	return mono(x).looped(p).fade(0.05)
}

func leadOverdrive(c *ctx) *out {
	p := c.plan(0.2, 1.5)
	drive, tone, bias := c.v("drive"), c.v("tone"), c.v("bias")
	var a, b osc
	f2 := p.q(detune(p.f0, 4))
	x := render(p.frames(), func(i int) float64 {
		return (a.saw(p.f0/c.sr) + 0.3*b.saw(f2/c.sr)) * 0.5 * fadeIn(c.t(i), 0.003)
	})
	g := 3 + 12*drive
	driveBuf(x, func(v float64) float64 { return tube(v, g, bias) })
	eq(x, bqHighpass(90, 0.7, c.sr), bqPeak(1600, 0.8, 4, c.sr), bqPeak(3500, 2, -4, c.sr),
		bqLowpass(tone, 0.7, c.sr), bqLowpass(tone*1.15, 0.6, c.sr))
	return mono(x).looped(p)
}

func leadAcid(c *ctx) *out {
	p := c.plan(0.45, 1.5)
	cut, res, env := c.v("cutoff"), c.v("resonance"), c.v("env")
	var o osc
	var lad ladder
	return loopMono(c, p, func(i int, t float64) float64 {
		fe := 0.0
		if t < 0.35 {
			x := 1 - t/0.35
			fe = x * x
		}
		y := lad.tick(0.5*o.saw(p.f0/c.sr), cut*math.Exp2(2.5*env*fe), res, c.sr)
		return y * fadeIn(t, 0.003)
	})
}

func leadVox(c *ctx) *out {
	p := c.plan(0.15, 1.5)
	shift, bright := c.v("shift"), c.v("bright")
	var o osc
	fm := newFormants(vowels["tenor_a"].shift(shift), c.sr)
	return loopMono(c, p, func(i int, t float64) float64 {
		v := o.saw(p.f0 / c.sr)
		return (3*fm.tick(v) + bright*0.3*v) * rise(t, 0.03)
	})
}

func leadGlass(c *ctx) *out {
	p := c.plan(0.4, 3)
	beat, bright := c.v("beating"), c.v("bright")
	n := p.frames()
	x := make([]float64, n)
	var ps []partial
	for h := 1; h <= 11; h += 2 {
		f := p.f0 * float64(h)
		a := 1 / math.Pow(float64(h), 2.2-1.2*bright)
		ps = append(ps,
			partial{f: f, amp: a * 0.6, ph: c.rng.float()},
			partial{f: f + p.q(beat*(0.3+0.15*float64(h))), amp: a * 0.4, ph: c.rng.float()})
	}
	addPartials(x, c.sr, ps)
	for i := range x {
		x[i] *= ads(c.t(i), 0.03, 0.3, 0.8)
	}
	return mono(x).looped(p)
}

func leadCrushed(c *ctx) *out {
	p := c.plan(0.1, 1.5)
	var o osc
	x := render(p.frames(), func(i int) float64 { return 0.95 * o.saw(p.f0/c.sr) * fadeIn(c.t(i), 0.003) })
	crush(x, c.sr, c.v("bits"), p.q(c.v("rate")))
	return mono(x).looped(p)
}
