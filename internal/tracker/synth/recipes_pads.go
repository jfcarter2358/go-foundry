package synth

// recipes_pads.go — pads, strings and drones (recipes 69-82).
//
// Pads are held for bars at a time, so their loops are long (about three
// seconds) and must not pulse at the seam: every detuned voice and every
// slow LFO is snapped to the loop grid, which makes the synthetic pads
// exactly periodic. The ensembles that cannot be — bowed strings, breathy
// choirs, friction-driven metal — get a longer crossfade instead.

import "math"

func padRecipes() []Recipe {
	p := func(name, base, desc string, tags []string, ps []Param, body func(*ctx) *out) Recipe {
		return rec(name, "pads", base, desc, tags, ps, body)
	}
	return []Recipe{
		p("pad_warm", "C-4", "Warm detuned saws, softly filtered, slow attack", []string{"warm", "analog", "stereo"}, []Param{
			par("attack", 0.6, 0.05, 1.5, "seconds"),
			par("cutoff", 1500, 300, 6000, "lowpass in Hz"),
			par("detune", 12, 0, 30, "spread in cents"),
		}, padWarm),
		p("pad_glass", "C-4", "Glassy bell partials, slowly beating", []string{"glass", "bell", "stereo"}, []Param{
			par("attack", 0.4, 0.02, 1.5, "seconds"),
			par("bright", 0.5, 0, 1, "upper partial level"),
			par("beating", 0.5, 0, 1.5, "beat rate"),
		}, padGlass),
		p("pad_choir", "C-4", "Choir 'ooh': stacked formant voices", []string{"choir", "vocal", "stereo"}, []Param{
			par("vowel", 0.2, 0, 1, "ooh to oh"),
			par("detune", 10, 0, 25, "voice spread in cents"),
			par("breath", 0.3, 0, 1, "breathiness"),
		}, padChoir),
		p("pad_string_machine", "C-4", "70s string machine: twelve saws and an ensemble chorus", []string{"strings", "solina", "stereo"}, []Param{
			par("detune", 14, 0, 30, "spread in cents"),
			par("chorus", 0.7, 0, 1, "ensemble depth"),
			par("cutoff", 3500, 1000, 9000, "lowpass in Hz"),
		}, padStringMachine),
		p("strings_section", "C-4", "String section: six bowed strings with chorus", []string{"strings", "orchestral", "bowed", "stereo"}, []Param{
			par("pressure", 0.45, 0.1, 0.9, "bow pressure"),
			par("attack", 0.3, 0.05, 1, "seconds"),
			par("vibrato", 0.4, 0, 1, "vibrato depth"),
		}, stringsSection),
		p("strings_pizz", "C-4", "Pizzicato violin: short pluck through a wooden body", []string{"strings", "pizzicato", "orchestral"}, []Param{
			par("decay", 0.6, 0.2, 2, "seconds"),
			par("brightness", 0.5, 0, 1, "string brightness"),
		}, stringsPizz),
		p("cello", "C-3", "Solo cello, bowed, with gentle vibrato", []string{"strings", "cello", "bowed", "orchestral"}, []Param{
			par("pressure", 0.5, 0.1, 0.9, "bow pressure"),
			par("attack", 0.25, 0.05, 1, "seconds"),
			par("vibrato", 0.5, 0, 1, "vibrato depth"),
		}, cello),
		p("pad_dark", "C-3", "Dark drone: saws and low noise under a slowly moving filter", []string{"dark", "drone", "stereo"}, []Param{
			par("cutoff", 500, 150, 2000, "filter centre in Hz"),
			par("motion", 0.6, 0, 1, "filter movement"),
			par("noise", 0.3, 0, 1, "low noise level"),
		}, padDark),
		p("pad_shimmer", "C-4", "Shimmering octaves that sparkle as they move", []string{"shimmer", "airy", "stereo"}, []Param{
			par("attack", 0.8, 0.05, 2, "seconds"),
			par("shimmer", 0.6, 0, 1, "upper octave level"),
			par("motion", 0.5, 0, 1, "sparkle depth"),
		}, padShimmer),
		p("pad_pwm", "C-4", "Two pulses with opposed PWM", []string{"pwm", "analog"}, []Param{
			par("depth", 0.35, 0, 0.45, "PWM depth"),
			par("rate", 0.5, 0.25, 3, "PWM rate in Hz"),
			par("cutoff", 2500, 600, 8000, "lowpass in Hz"),
		}, padPWM),
		p("pad_air", "C-4", "Air: noise tuned to the note", []string{"air", "noise", "stereo"}, []Param{
			par("q", 40, 8, 120, "resonance sharpness"),
			par("air", 0.25, 0, 1, "untuned hiss"),
		}, padAir),
		p("pad_sweep", "C-4", "Saws with a slow filter sweep baked in", []string{"sweep", "analog", "stereo"}, []Param{
			par("low", 400, 100, 2000, "sweep bottom in Hz"),
			par("high", 4000, 1000, 12000, "sweep top in Hz"),
			par("resonance", 0.4, 0, 0.9, "filter resonance"),
		}, padSweep),
		p("drone_scifi", "C-2", "Evolving inharmonic FM drone", []string{"drone", "scifi", "fm", "stereo"}, []Param{
			par("index", 1.5, 0, 4, "modulation depth"),
			par("motion", 0.6, 0, 1, "how much it evolves"),
			par("shimmer", 0.3, 0, 1, "high cluster level"),
		}, droneScifi),
		p("pad_bowed_metal", "C-4", "Bowed metal: resonant modes rubbed into singing", []string{"metal", "bowed", "eerie", "stereo"}, []Param{
			par("ring", 4, 1, 8, "mode ring time in seconds"),
			par("friction", 0.5, 0, 1, "friction grain"),
		}, padBowedMetal),
	}
}

// sawStack is detuned saws summed, each a frequency on the grid with its own
// starting phase.
func sawStack(c *ctx, r *rng, n int, fs []float64) []float64 {
	x := make([]float64, n)
	for _, f := range fs {
		var o osc
		o.ph = r.float()
		for i := range x {
			x[i] += o.saw(f / c.sr)
		}
	}
	scale(x, 1/math.Sqrt(float64(len(fs))))
	return x
}

// spreadFreqs is count frequencies evenly spread over ±cents, snapped.
func spreadFreqs(p plan, count int, cents, skew float64) []float64 {
	fs := make([]float64, count)
	for k := range fs {
		x := -1 + 2*float64(k)/float64(count-1)
		fs[k] = p.q(detune(p.f0, cents*(x+skew*x*x*x)))
	}
	return fs
}

func padWarm(c *ctx) *out {
	att := c.v("attack")
	p := c.plan(att+0.2, 3)
	cut, d := c.v("cutoff"), c.v("detune")
	n := p.frames()
	side := func(r *rng, skew float64) []float64 {
		x := sawStack(c, r, n, spreadFreqs(p, 5, d, skew))
		var lp svf
		lp.set(cut, 0.9, c.sr)
		for i := range x {
			y, _, _ := lp.tick(x[i])
			x[i] = y * rise(c.t(i), att)
		}
		return x
	}
	return stereo(side(c.rng.fork(), 0.3), side(c.rng.fork(), -0.3)).looped(p).fade(0.05)
}

func padGlass(c *ctx) *out {
	att := c.v("attack")
	p := c.plan(att+0.2, 3)
	bright, beat := c.v("bright"), c.v("beating")
	n := p.frames()
	ratios := []float64{1, 2, 3, 4.16, 5.43, 6.79, 8.1}
	side := func(r *rng) []float64 {
		x := make([]float64, n)
		var ps []partial
		for i, rt := range ratios {
			a := math.Pow(0.62-0.25*(1-bright), float64(i))
			f := p.q(p.f0 * rt)
			if i == 0 {
				f = p.f0
			}
			ps = append(ps,
				partial{f: f, amp: a * 0.6, ph: r.float()},
				partial{f: f + p.q(beat*r.rangef(0.2, 0.8)*(1+0.3*float64(i))), amp: a * 0.4, ph: r.float()})
		}
		addPartials(x, c.sr, ps)
		for i := range x {
			x[i] *= rise(c.t(i), att)
		}
		return x
	}
	return stereo(side(c.rng.fork()), side(c.rng.fork())).looped(p)
}

func padChoir(c *ctx) *out {
	p := c.plan(0.8, 3)
	vow, d, breath := c.v("vowel"), c.v("detune"), c.v("breath")
	n := p.frames()
	v := mixVowel(vowels["alto_u"], vowels["alto_o"], vow)
	side := func(r *rng, skew float64) []float64 {
		src := make([]float64, n)
		for _, f := range spreadFreqs(p, 5, d, skew) {
			g := newGlottal(r, f, breath, c.sr)
			g.o.ph = r.float()
			rate := p.q(r.rangef(4.6, 5.6))
			vph := r.float()
			for i := range src {
				t := c.t(i)
				fi := f * (1 + 0.004*math.Sin(2*math.Pi*(rate*t+vph)))
				src[i] += g.tick(fi / c.sr)
			}
		}
		fm := newFormants(v, c.sr)
		x := render(n, func(i int) float64 { return fm.tick(src[i]) * rise(c.t(i), 0.5) })
		return x
	}
	return stereo(side(c.rng.fork(), 0.2), side(c.rng.fork(), -0.2)).looped(p).fade(0.06)
}

func padStringMachine(c *ctx) *out {
	p := c.plan(0.4, 3)
	d, depth, cut := c.v("detune"), c.v("chorus"), c.v("cutoff")
	n := p.frames()
	x := sawStack(c, c.rng, n, spreadFreqs(p, 12, d, 0.2))
	// The ensemble: three delay taps, each swept by a slow and a fast LFO —
	// the BBD chorus every string machine had, and most of their sound.
	rates := []float64{p.q(0.63), p.q(0.95), p.q(0.71), p.q(1.13), p.q(5.9), p.q(6.6)}
	l, r := chorus(x, c.sr, 0.006, 0.004*depth, rates, 0.5*depth+0.2)
	for _, ch := range [][]float64{l, r} {
		lpBuf(ch, cut, c.sr, 2)
		hpBuf(ch, 160, c.sr, 1)
		for i := range ch {
			ch[i] *= rise(c.t(i), 0.25)
		}
	}
	return stereo(l, r).looped(p)
}

// violinBody and celloBody are the main wood and air resonances.
var (
	violinBody = []bodyMode{{280, 4, 0.5}, {460, 5, 0.6}, {550, 5, 0.5}, {700, 4, 0.3}, {1200, 3, 0.25}, {2800, 2, 0.35}, {4500, 2, 0.15}}
	celloBody  = []bodyMode{{110, 3, 0.7}, {210, 4, 0.6}, {290, 4, 0.5}, {420, 3, 0.4}, {700, 3, 0.3}, {1400, 2, 0.2}, {2600, 2, 0.15}}
)

func stringsSection(c *ctx) *out {
	att := c.v("attack")
	pr, vib := c.v("pressure"), c.v("vibrato")
	p := c.plan(att+0.4, 2.5)
	n := p.frames()
	cents := []float64{-9, -5, -2, 2, 5, 9}
	l, r := make([]float64, n), make([]float64, n)
	for k, cn := range cents {
		vr := c.rng.fork()
		v := bowedVoice(c, vr, detune(p.f0, cn), n, pr*vr.rangef(0.85, 1.15), att*vr.rangef(0.8, 1.2),
			12*vib, vr.rangef(4.6, 5.8))
		norm1(v)
		main, other := l, r
		if k%2 == 1 {
			main, other = r, l
		}
		addTo(main, v, 1)
		addTo(other, v, 0.4)
	}
	for _, ch := range [][]float64{l, r} {
		body(ch, c.sr, 0.6, violinBody)
	}
	cl, _ := chorus(l, c.sr, 0.008, 0.003, []float64{0.5, 0.8}, 0.3)
	_, cr := chorus(r, c.sr, 0.008, 0.003, []float64{0.6, 0.9}, 0.3)
	return stereo(cl, cr).looped(p).fade(0.08)
}

func stringsPizz(c *ctx) *out {
	decay, bright := c.v("decay"), c.v("brightness")
	f := c.f0
	k := newKS(c.sr, f, decay, bright, 0)
	x := runKS(k, pluck(c.rng, c.sr, f, 0.35+0.4*bright, 0.3), c.n(decay+0.2))
	norm1(x)
	body(x, c.sr, 0.5, violinBody)
	return mono(x)
}

func cello(c *ctx) *out {
	att := c.v("attack")
	pr, vib := c.v("pressure"), c.v("vibrato")
	p := c.plan(att+0.5, 2.5)
	x := bowedVoice(c, c.rng, p.f0, p.frames(), pr, att, 18*vib, 5.2)
	norm1(x)
	body(x, c.sr, 0.5, celloBody)
	return mono(x).looped(p).fade(0.06)
}

func padDark(c *ctx) *out {
	p := c.plan(1, 4)
	cut, motion, noise := c.v("cutoff"), c.v("motion"), c.v("noise")
	n := p.frames()
	rate := p.q(0.25)
	side := func(r *rng, ph float64) []float64 {
		x := sawStack(c, r, n, spreadFreqs(p, 3, 8, 0))
		nz := r.pinkBuf(n)
		lpBuf(nz, 300, c.sr, 2)
		norm1(nz)
		var lp svf
		for i := range x {
			t := c.t(i)
			lp.set(cut*math.Exp2(1.5*motion*math.Sin(2*math.Pi*(rate*t+ph))), 1.2, c.sr)
			y, _, _ := lp.tick(x[i] + noise*0.5*nz[i])
			x[i] = y * rise(t, 0.8)
		}
		return x
	}
	return stereo(side(c.rng.fork(), 0), side(c.rng.fork(), 0.15)).looped(p).fade(0.05)
}

func padShimmer(c *ctx) *out {
	att := c.v("attack")
	p := c.plan(att+0.2, 3)
	shim, motion := c.v("shimmer"), c.v("motion")
	n := p.frames()
	type comp struct{ mul, amp, rate float64 }
	comps := []comp{{1, 1, 0.33}, {2, 0.6, 0.5}, {3, 0.25, 0.66}, {4, 0.5 * shim, 1}, {6, 0.2 * shim, 1.33}, {8, 0.35 * shim, 1.66}, {16, 0.15 * shim, 2.33}}
	side := func(r *rng) []float64 {
		x := make([]float64, n)
		for k, cp := range comps {
			for j := 0; j < 2; j++ {
				f := p.q(p.f0*cp.mul + float64(j)*p.unit*float64(1+k%3))
				if k == 0 && j == 0 {
					f = p.f0
				}
				car := newRot(f, c.sr, r.float())
				// Upper octaves sparkle: their level swings faster and
				// further than the fundamental's.
				lfo := newRot(p.q(cp.rate*r.rangef(0.8, 1.2)), c.sr, r.float())
				depth := motion * math.Min(1, 0.2+0.2*float64(k))
				for i := range x {
					x[i] += car.next() * cp.amp * 0.5 * (1 - depth*0.5*(1+lfo.next()))
				}
			}
		}
		for i := range x {
			x[i] *= rise(c.t(i), att)
		}
		return x
	}
	return stereo(side(c.rng.fork()), side(c.rng.fork())).looped(p)
}

func padPWM(c *ctx) *out {
	p := c.plan(0.6, 3)
	depth, rate := c.v("depth"), p.q(c.v("rate"))
	f2 := p.q(detune(p.f0, 6))
	var a, b osc
	b.ph = 0.37
	x := render(p.frames(), func(i int) float64 {
		t := c.t(i)
		m := depth * math.Sin(2*math.Pi*rate*t)
		return (a.pulse(p.f0/c.sr, 0.5+m) + b.pulse(f2/c.sr, 0.5-m)) * 0.5 * rise(t, 0.4)
	})
	var lp svf
	lp.set(c.v("cutoff"), 0.7, c.sr)
	for i, v := range x {
		x[i], _, _ = lp.tick(v)
	}
	return mono(x).looped(p)
}

func padAir(c *ctx) *out {
	p := c.plan(0.6, 3)
	q, air := c.v("q"), c.v("air")
	n := p.frames()
	side := func(r *rng) []float64 {
		var bps [3]svf
		for k := range bps {
			bps[k].set(p.f0*float64(k+1), q, c.sr)
		}
		hiss := r.noiseBuf(n)
		hpBuf(hiss, 4000, c.sr, 1)
		lpBuf(hiss, 12000, c.sr, 1)
		norm1(hiss)
		flutter := newRot(p.q(0.7), c.sr, r.float())
		x := render(n, func(i int) float64 {
			w := r.white()
			var y float64
			for k := range bps {
				_, b, _ := bps[k].tick(w)
				y += b / float64(k+1)
			}
			return (y*math.Sqrt(q)*0.15 + air*0.3*hiss[i]) * (0.85 + 0.15*flutter.next()) * rise(c.t(i), 0.5)
		})
		return x
	}
	return stereo(side(c.rng.fork()), side(c.rng.fork())).looped(p).fade(0.06).noisy()
}

func padSweep(c *ctx) *out {
	p := c.plan(0.3, 4)
	lo, hi, res := c.v("low"), c.v("high"), c.v("resonance")
	n := p.frames()
	rate := p.q(1 / 4.0) // one full sweep per loop
	side := func(r *rng, ph float64) []float64 {
		x := sawStack(c, r, n, spreadFreqs(p, 4, 10, 0.2))
		var lp svf
		for i := range x {
			t := c.t(i)
			s := 0.5 - 0.5*math.Cos(2*math.Pi*(rate*t+ph))
			lp.set(lo*math.Pow(hi/lo, s), 0.7+4*res, c.sr)
			y, _, _ := lp.tick(x[i])
			x[i] = y * rise(t, 0.2)
		}
		return x
	}
	return stereo(side(c.rng.fork(), 0), side(c.rng.fork(), 0.08)).looped(p)
}

func droneScifi(c *ctx) *out {
	p := c.plan(0.8, 4)
	idx, motion, shim := c.v("index"), c.v("motion"), c.v("shimmer")
	n := p.frames()
	m1, m2 := p.q(p.f0*1.414), p.q(p.f0*2.718)
	side := func(r *rng) []float64 {
		var car, mod1, mod2 op
		var anchor osc
		l1 := newRot(p.q(0.25), c.sr, r.float())
		l2 := newRot(p.q(0.5), c.sr, r.float())
		l3 := newRot(p.q(0.75), c.sr, r.float())
		x := render(n, func(i int) float64 {
			a, b, d := l1.next(), l2.next(), l3.next()
			k1 := idx * (1 + 0.8*motion*a)
			k2 := idx * 0.6 * (1 + motion*b)
			pm := k1*mod1.tick(m1/c.sr, 0) + k2*mod2.tick(m2/c.sr, 0)
			// A plain sine under the clangour anchors the drone in key.
			return car.tick(p.f0/c.sr, pm)*(0.8+0.2*motion*d) + 0.4*anchor.sine(p.f0/c.sr)
		})
		cl := make([]float64, n)
		var ps []partial
		for k := 0; k < 6; k++ {
			ps = append(ps, partial{f: p.q(p.f0 * r.rangef(14, 40)), amp: r.rangef(0.3, 1), ph: r.float()})
		}
		addPartials(cl, c.sr, ps)
		trem := newRot(p.q(1.25), c.sr, r.float())
		for i := range cl {
			cl[i] *= 0.5 + 0.5*trem.next()
		}
		norm1(cl)
		addTo(x, cl, 0.15*shim)
		lpBuf(x, 5000, c.sr, 1)
		for i := range x {
			x[i] *= rise(c.t(i), 0.6)
		}
		return x
	}
	return stereo(side(c.rng.fork()), side(c.rng.fork())).looped(p)
}

func padBowedMetal(c *ctx) *out {
	p := c.plan(1, 3)
	ring, fric := c.v("ring"), c.v("friction")
	n := p.frames()
	ratios := []float64{1, 2.32, 3.94, 5.61, 7.09, 9.38, 11.4}
	side := func(r *rng) []float64 {
		var ps []partial
		for i, rt := range ratios {
			ps = append(ps, partial{f: p.f0 * rt * (1 + r.rangef(-0.002, 0.002)*float64(min(i, 1))), amp: 1 / (1 + 0.6*float64(i)), t60: ring / (1 + 0.15*float64(i))})
		}
		bank := newModeBank(ps, c.sr)
		var o osc
		var grain onePole
		grain.set(40, c.sr)
		var nlp onePole
		nlp.set(3000, c.sr)
		x := render(n, func(i int) float64 {
			// Stick-slip: the rosin's noise released in a burst once per
			// fundamental cycle, its strength stuttering with the bow.
			// Broadband, so every mode sings, not just the first.
			o.advance(p.f0 / c.sr)
			slip := 0.4
			if o.ph < 0.3 {
				slip = 1
			}
			stutter := 1 + 3*fric*grain.lp(r.white())
			e := 0.04 * nlp.lp(r.white()) * slip * stutter
			return bank.tick(e) * rise(c.t(i), 0.8)
		})
		return x
	}
	return stereo(side(c.rng.fork()), side(c.rng.fork())).looped(p).fade(0.08)
}
