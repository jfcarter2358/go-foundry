package synth

// recipes_keys.go — keyboards and tuned percussion (recipes 83-94).
//
// Struck keys are one-shots: a piano or a marimba decays on its own, and a
// loop would freeze that decay into an organ. The organs loop. The piano is
// additive rather than modal because its identity is in details a mode
// table does not carry: stretched partials (stiff strings run sharp up the
// series), three slightly mistuned strings per note beating against each
// other, and the two-stage decay those strings' coupling produces — a fast
// "prompt" sound and a long "aftersound".

import "math"

func keyRecipes() []Recipe {
	k := func(name, base, desc string, tags []string, ps []Param, body func(*ctx) *out) Recipe {
		return rec(name, "keys", base, desc, tags, ps, body)
	}
	return []Recipe{
		k("piano", "C-4", "Acoustic piano: stretched partials, hammer, two-stage decay", []string{"piano", "acoustic"}, []Param{
			par("decay", 5, 1.5, 6, "seconds"),
			par("hardness", 0.5, 0, 1, "hammer hardness (brightness)"),
			par("detune", 0.4, 0, 1, "unison string mistuning"),
			par("hammer", 0.3, 0, 1, "hammer thump level"),
		}, piano),
		k("epiano_tine", "C-4", "Tine electric piano: bell attack, warm body", []string{"epiano", "rhodes", "fm"}, []Param{
			par("decay", 4, 1, 6, "seconds"),
			par("bark", 0.5, 0, 1, "attack bite"),
			par("bell", 0.4, 0, 1, "tine bell level"),
		}, epianoTine),
		k("epiano_reed", "C-4", "Reed electric piano: nasal, barky when pushed", []string{"epiano", "wurlitzer", "fm"}, []Param{
			par("decay", 3, 0.8, 6, "seconds"),
			par("bark", 0.5, 0, 1, "reed growl"),
		}, epianoReed),
		k("organ_drawbar", "C-4", "Tonewheel organ with rotary speaker", []string{"organ", "hammond", "rotary"}, []Param{
			par("sub", 0, 0, 1, "16' drawbar"),
			par("upper", 0.7, 0, 1, "4', 2 2/3', 2' drawbars"),
			par("percussion", 0.5, 0, 1, "third-harmonic percussion"),
			par("rotary", 0.5, 0, 1, "rotary speaker depth"),
			par("speed", 5.8, 0.6, 7, "rotor speed in Hz"),
		}, organDrawbar),
		k("organ_pipe", "C-4", "Pipe organ: principal plus mixture, with chiff", []string{"organ", "pipe", "church", "stereo"}, []Param{
			par("mixture", 0.5, 0, 1, "mixture ranks"),
			par("octave", 0.5, 0, 1, "4' rank"),
			par("chiff", 0.4, 0, 1, "speech transient"),
		}, organPipe),
		k("organ_combo", "C-4", "Combo organ: filtered squares, reedy and bright", []string{"organ", "combo", "farfisa"}, []Param{
			par("bright", 0.6, 0, 1, "brightness"),
			par("octave", 0.5, 0, 1, "4' square level"),
		}, organCombo),
		k("clavinet", "C-4", "Clavinet: bright, funky, percussive", []string{"clavinet", "funk"}, []Param{
			par("decay", 1.4, 0.3, 4, "seconds"),
			par("bright", 0.85, 0.3, 1, "string brightness"),
			par("pulse", 0.3, 0, 1, "pulse layer"),
		}, clavinet),
		k("harpsichord", "C-4", "Harpsichord: plucked, bright, with jack noise", []string{"harpsichord", "baroque"}, []Param{
			par("decay", 3.5, 1, 6, "seconds"),
			par("jack", 0.4, 0, 1, "mechanism noise"),
		}, harpsichord),
		k("marimba", "C-4", "Marimba: tuned wooden bar over a resonator", []string{"marimba", "mallet", "wood"}, []Param{
			par("decay", 1.2, 0.3, 3, "seconds"),
			par("hardness", 0.4, 0, 1, "mallet hardness"),
		}, marimba),
		k("vibraphone", "C-4", "Vibraphone: long-ringing metal bar", []string{"vibraphone", "mallet", "metal"}, []Param{
			par("decay", 5, 1.5, 6, "seconds"),
			par("tremolo", 0.2, 0, 1, "motor tremolo depth"),
			par("hardness", 0.4, 0, 1, "mallet hardness"),
		}, vibraphone),
		k("glockenspiel", "C-5", "Glockenspiel: bright steel bar", []string{"glockenspiel", "mallet", "bell"}, []Param{
			par("decay", 3, 1, 6, "seconds"),
			par("hardness", 0.7, 0, 1, "mallet hardness"),
		}, glockenspiel),
		k("music_box", "C-5", "Music box: plucked comb tine and a little click", []string{"musicbox", "bell", "toy"}, []Param{
			par("decay", 2.5, 0.8, 5, "seconds"),
			par("click", 0.4, 0, 1, "pin click level"),
		}, musicBox),
	}
}

func piano(c *ctx) *out {
	decay, hard, det, ham := c.v("decay"), c.v("hardness"), c.v("detune"), c.v("hammer")
	f0 := c.f0
	n := c.n(decay)
	B := 0.00035    // inharmonicity of a mid-range string
	beta := 1.0 / 7 // hammer strike point along the string
	var ps []partial
	cents := []float64{-0.8 * det, 0.15 * det, 0.9 * det}
	for h := 1; ; h++ {
		fh := float64(h) * f0 * math.Sqrt((1+B*float64(h*h))/(1+B))
		if fh > 12000 {
			break
		}
		amp := math.Abs(math.Sin(math.Pi*float64(h)*beta)) / math.Pow(float64(h), 0.9)
		amp *= 1 / (1 + math.Pow(fh/(1200+5000*hard), 2))
		prompt := decay * 0.3 / (1 + 0.12*float64(h))
		after := decay * 1.6 / (1 + 0.07*float64(h))
		for _, cn := range cents {
			f := detune(fh, cn)
			ph := c.rng.float() * 0.1
			ps = append(ps,
				partial{f: f, amp: amp * 0.7 / 3, t60: prompt, ph: ph},
				partial{f: f, amp: amp * 0.3 / 3, t60: after, ph: ph})
		}
	}
	x := make([]float64, n)
	addPartials(x, c.sr, ps)
	norm1(x)
	thump := strike(n, c.sr, 0.004)
	lpBuf(thump, 600, c.sr, 1)
	addTo(x, norm1(thump), 0.25*ham)
	knock := noiseHit(c.rng, c.n(0.03), c.sr, func(x []float64) {
		var bp svf
		bp.set(1800, 1, c.sr)
		for i, v := range x {
			_, b, _ := bp.tick(v)
			x[i] = b
		}
	}, func(t float64) float64 { return math.Exp(-t / 0.005) })
	addTo(x, knock, 0.08*ham)
	eq(x, bqPeak(250, 1, 2, c.sr), bqPeak(2500, 1, 1.5, c.sr))
	return mono(x)
}

func epianoTine(c *ctx) *out {
	decay, bark, bell := c.v("decay"), c.v("bark"), c.v("bell")
	f := c.f0
	var car, mod, tc, tm op
	x := render(c.n(decay), func(i int) float64 {
		t := c.t(i)
		idx := bark*1.8*math.Exp(-t/0.15) + 0.5*math.Exp(-t/1.5)
		body := car.tick(f/c.sr, idx*mod.tick(f/c.sr, 0))
		// The tine's bell: a high ratio pair that rings for a moment.
		ding := tc.tick(f/c.sr, 1.2*math.Exp(-t/0.03)*tm.tick(14*f/c.sr, 0)) * math.Exp(-t/0.25)
		v := body*(0.75*t60(t, decay*0.35)+0.25*t60(t, decay)) + bell*0.4*ding
		return (v + 0.25*v*v) * fadeIn(t, 0.001)
	})
	return mono(x)
}

func epianoReed(c *ctx) *out {
	decay, bark := c.v("decay"), c.v("bark")
	f := c.f0
	var car, mod, mod2 op
	g := 1 + 2*bark
	x := render(c.n(decay), func(i int) float64 {
		t := c.t(i)
		idx := 1.1 + bark*2.5*math.Exp(-t/0.12)
		v := car.tick(f/c.sr, idx*mod.tick(f/c.sr, 0)+0.3*mod2.tick(2*f/c.sr, 0))
		v *= 0.6*t60(t, decay*0.4) + 0.4*t60(t, decay)
		return softClip(math.Tanh(g*v)/math.Tanh(g)*0.95) * fadeIn(t, 0.001)
	})
	eq(x, bqPeak(1200, 1, 3, c.sr))
	return mono(x)
}

// rotary is a Leslie: the treble horn and bass drum spin at different
// speeds, each swinging level (the horn's mouth passing you) and pitch (its
// motion toward and away). Rates come from the loop grid.
func rotary(c *ctx, p plan, x []float64, depth, speed float64) []float64 {
	lo := append([]float64(nil), x...)
	lpBuf(lo, 800, c.sr, 2)
	hi := make([]float64, len(x))
	for i := range x {
		hi[i] = x[i] - lo[i]
	}
	horn, drum := p.q(speed), p.q(speed*0.85)
	y := make([]float64, len(x))
	for i := range y {
		t := c.t(i)
		sh := math.Sin(2 * math.Pi * horn * t)
		sd := math.Sin(2 * math.Pi * drum * t)
		h := readFrac(hi, float64(i)-(0.0006+0.0004*depth*sh)*c.sr) * (1 + 0.35*depth*sh)
		d := readFrac(lo, float64(i)-(0.0006+0.0002*depth*sd)*c.sr) * (1 + 0.15*depth*sd)
		y[i] = h + d
	}
	return y
}

func organDrawbar(c *ctx) *out {
	p := c.plan(0.3, 2)
	sub, up, perc, rot, speed := c.v("sub"), c.v("upper"), c.v("percussion"), c.v("rotary"), c.v("speed")
	n := p.frames()
	x := make([]float64, n)
	bars := []struct{ ratio, amp float64 }{
		{0.5, sub}, {1, 1}, {2, 0.8 * up}, {3, 0.6 * up}, {4, 0.5 * up},
	}
	var ps []partial
	for _, b := range bars {
		ps = append(ps, partial{f: p.f0 * b.ratio, amp: b.amp})
	}
	addPartials(x, c.sr, ps)
	pc := make([]float64, n)
	addPartials(pc, c.sr, []partial{{f: 3 * p.f0, amp: 0.8 * perc, t60: 0.6}})
	addTo(x, pc, 1)
	click := noiseHit(c.rng, c.n(0.006), c.sr, func(x []float64) { lpBuf(x, 3000, c.sr, 1) },
		func(t float64) float64 { return math.Exp(-t / 0.0015) })
	for i := range x {
		// Tonewheels leak a little: the faint grit of a real one.
		x[i] = math.Tanh(0.5*x[i]) * fadeIn(c.t(i), 0.002)
	}
	addTo(x, click, 0.15)
	return mono(rotary(c, p, x, rot, speed)).looped(p)
}

func organPipe(c *ctx) *out {
	p := c.plan(0.4, 2.5)
	mix, oct, chiff := c.v("mixture"), c.v("octave"), c.v("chiff")
	n := p.frames()
	// pipeRank is one rank's flue tone at a multiple of the base pitch:
	// strong low harmonics, even ones a little weaker, the upper partials
	// speaking first and the fundamental last.
	pipeRank := func(dst []float64, mul, amp float64, r *rng) {
		f := p.q(p.f0*mul + r.rangef(-1, 1)*p.unit)
		if mul == 1 {
			f = p.f0
		}
		var ps []partial
		for h := 1; float64(h)*f < 9000; h++ {
			a := amp / math.Pow(float64(h), 1.3)
			if h%2 == 0 {
				a *= 0.7
			}
			ps = append(ps, partial{f: float64(h) * f, amp: a, ph: r.float(), attack: 0.09 / (1 + 0.3*float64(h))})
		}
		addPartials(dst, c.sr, ps)
	}
	l, r := make([]float64, n), make([]float64, n)
	rr := c.rng.fork()
	pipeRank(l, 1, 1, rr)
	copy(r, l)
	o := make([]float64, n)
	pipeRank(o, 2, 0.5*oct, rr)
	addTo(l, o, 0.7)
	addTo(r, o, 1)
	for k, mul := range []float64{4, 6, 8} {
		m := make([]float64, n)
		pipeRank(m, mul, 0.3*mix, rr)
		if k%2 == 0 {
			addTo(l, m, 1)
			addTo(r, m, 0.6)
		} else {
			addTo(l, m, 0.6)
			addTo(r, m, 1)
		}
	}
	for side, ch := range [][]float64{l, r} {
		ck := noiseHit(c.rng, n, c.sr, func(x []float64) {
			var bp svf
			bp.set(p.f0*(4+float64(side)), 3, c.sr)
			for i, v := range x {
				_, b, _ := bp.tick(v)
				x[i] = b
			}
		}, func(t float64) float64 { return rise(t, 0.01) * math.Exp(-t/0.04) })
		addTo(ch, ck, 0.4*chiff)
	}
	return stereo(l, r).looped(p)
}

func organCombo(c *ctx) *out {
	p := c.plan(0.1, 1.5)
	bright, oct := c.v("bright"), c.v("octave")
	var a, b osc
	x := render(p.frames(), func(i int) float64 {
		return (a.square(p.f0/c.sr) + oct*0.6*b.square(2*p.f0/c.sr)) * fadeIn(c.t(i), 0.003)
	})
	lpBuf(x, 1500+6000*bright, c.sr, 1)
	eq(x, bqPeak(1400, 1.2, 5, c.sr), bqHighpass(120, 0.7, c.sr))
	return mono(x).looped(p)
}

func clavinet(c *ctx) *out {
	decay, bright, pls := c.v("decay"), c.v("bright"), c.v("pulse")
	f := c.f0
	n := c.n(decay + 0.1)
	k := newKS(c.sr, f, decay, bright, 0)
	x := runKS(k, pluck(c.rng, c.sr, f, 1, 0.07), n)
	norm1(x)
	// Two pickups under the string, wired against each other: a comb that
	// carves the nasal "quack".
	off := int(c.sr / f * 0.18)
	y := make([]float64, n)
	for i := range x {
		y[i] = x[i]
		if i >= off {
			y[i] -= 0.8 * x[i-off]
		}
	}
	norm1(y)
	var o osc
	pulse := render(n, func(i int) float64 {
		t := c.t(i)
		return o.pulse(f/c.sr, 0.25) * t60(t, decay*0.3)
	})
	lpBuf(pulse, 4000, c.sr, 1)
	addTo(y, pulse, 0.4*pls)
	return mono(y)
}

func harpsichord(c *ctx) *out {
	decay, jack := c.v("decay"), c.v("jack")
	f := c.f0
	n := c.n(decay + 0.1)
	x := make([]float64, n)
	for k, cents := range []float64{0, 1.5} {
		s := newKS(c.sr, detune(f, cents), decay*(1-0.2*float64(k)), 0.95, 0)
		addTo(x, runKS(s, pluck(c.rng, c.sr, f, 1, 0.08), n), 1-0.35*float64(k))
	}
	norm1(x)
	thunk := strike(n, c.sr, 0.002)
	lpBuf(thunk, 1000, c.sr, 1)
	addTo(x, norm1(thunk), 0.2*jack)
	quill := noiseHit(c.rng, c.n(0.006), c.sr, func(x []float64) { hpBuf(x, 3000, c.sr, 1) },
		func(t float64) float64 { return math.Exp(-t / 0.001) })
	addTo(x, quill, 0.15*jack)
	body(x, c.sr, 0.8, []bodyMode{{220, 3, 0.3}, {500, 3, 0.3}, {1200, 2, 0.2}, {3000, 2, 0.15}})
	return mono(x)
}

// malletBar renders struck bar modes plus a mallet contact.
func malletBar(c *ctx, n int, ps []partial, hard float64) []float64 {
	x := make([]float64, n)
	addPartials(x, c.sr, ps)
	norm1(x)
	click := noiseHit(c.rng, c.n(0.01), c.sr, func(x []float64) { lpBuf(x, 1500+8000*hard, c.sr, 2) },
		func(t float64) float64 { return math.Exp(-t / (0.0025 - 0.0018*hard)) })
	addTo(x, click, 0.05+0.15*hard)
	return x
}

func marimba(c *ctx) *out {
	decay, hard := c.v("decay"), c.v("hardness")
	f := c.f0
	n := c.n(decay + 0.4)
	x := malletBar(c, n, []partial{
		{f: f, amp: 1, t60: decay},
		{f: f * 3.99, amp: 0.15 + 0.35*hard, t60: decay * 0.35},
		{f: f * 9.95, amp: 0.04 + 0.12*hard, t60: decay * 0.1},
	}, hard)
	// The tube under the bar: a resonator that blooms the fundamental.
	res := newReson(f, decay*0.5, c.sr)
	tube := render(n, func(i int) float64 { return res.tick(x[i]) })
	addTo(x, norm1(tube), 0.35)
	return mono(x)
}

func vibraphone(c *ctx) *out {
	decay, trem, hard := c.v("decay"), c.v("tremolo"), c.v("hardness")
	f := c.f0
	n := c.n(math.Min(decay+0.3, 6))
	x := malletBar(c, n, []partial{
		{f: f, amp: 1, t60: decay},
		{f: f * 3.98, amp: 0.15 + 0.2*hard, t60: decay * 0.4},
		{f: f * 9.0, amp: 0.03 + 0.08*hard, t60: decay * 0.12},
	}, hard)
	for i := range x {
		x[i] *= 1 - trem*0.5*(1-math.Cos(2*math.Pi*5.5*c.t(i)))
	}
	return mono(x)
}

func glockenspiel(c *ctx) *out {
	decay, hard := c.v("decay"), c.v("hardness")
	f := c.f0
	amps := []float64{1, 0.4, 0.2, 0.1}
	decs := []float64{1, 0.4, 0.2, 0.1}
	ps := modes(f, freeBarRatios[:4], func(i int) float64 {
		if i == 0 {
			return 1
		}
		return amps[i] * (0.4 + 0.8*hard)
	}, func(i int) float64 { return decay * decs[i] })
	return mono(malletBar(c, c.n(decay+0.2), ps, hard))
}

func musicBox(c *ctx) *out {
	decay, click := c.v("decay"), c.v("click")
	f := c.f0
	n := c.n(decay + 0.2)
	x := make([]float64, n)
	addPartials(x, c.sr, []partial{
		{f: f, amp: 1, t60: decay},
		{f: f * 1.0012, amp: 0.15, t60: decay * 0.8},
		{f: f * tineRatios[1], amp: 0.25, t60: decay * 0.2},
		{f: f * tineRatios[2], amp: 0.08, t60: decay * 0.05},
	})
	norm1(x)
	pin := noiseHit(c.rng, c.n(0.006), c.sr, func(x []float64) {
		var bp svf
		bp.set(5000, 1.5, c.sr)
		for i, v := range x {
			_, b, _ := bp.tick(v)
			x[i] = b
		}
	}, func(t float64) float64 { return math.Exp(-t / 0.0012) })
	addTo(x, pin, 0.3*click)
	body(x, c.sr, 1, []bodyMode{{650, 3, 0.3}, {1500, 3, 0.2}})
	return mono(x)
}
