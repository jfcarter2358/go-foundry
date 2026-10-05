package synth

// recipes_fx.go — risers, impacts, sweeps and noise beds (recipes 122-128).
//
// Effects are built to sit on a musical grid: the riser lasts two bars at
// 120 BPM so it lands on a downbeat when triggered a bar early. The beds
// (radio, vinyl) are loops with equal-power seams, since they are noise and
// a linear crossfade would dip.

import "math"

func fxRecipes() []Recipe {
	f := func(name, desc string, tags []string, ps []Param, body func(*ctx) *out) Recipe {
		return rec(name, "fx", "", desc, tags, ps, body)
	}
	return []Recipe{
		f("fx_riser", "Noise and saws rising for two bars at 120 BPM", []string{"riser", "build", "stereo"}, []Param{
			par("length", 4, 1, 8, "seconds"),
			par("noise", 0.6, 0, 1, "noise level"),
			par("range", 4, 1, 6, "pitch rise in octaves"),
		}, fxRiser),
		f("fx_downlifter", "Falling sweep that drains the energy out", []string{"downlifter", "sweep", "stereo"}, []Param{
			par("length", 3, 1, 8, "seconds"),
			par("noise", 0.6, 0, 1, "noise level"),
		}, fxDownlifter),
		f("fx_impact", "Cinematic hit: sub drop, burst, metal and a long tail", []string{"impact", "hit", "cinematic", "stereo"}, []Param{
			par("sub", 0.8, 0, 1, "sub drop level"),
			par("metal", 0.5, 0, 1, "metallic layer"),
			par("tail", 4, 1, 6, "reverb tail in seconds"),
		}, fxImpact),
		f("fx_whoosh", "Swept noise passing left to right", []string{"whoosh", "transition", "stereo"}, []Param{
			par("length", 1.6, 0.4, 4, "seconds"),
			par("sweep", 0.7, 0, 1, "filter sweep range"),
			par("travel", 0.9, 0, 1, "stereo travel"),
		}, fxWhoosh),
		f("fx_laser", "Laser zap: square with a fast pitch fall", []string{"laser", "zap", "retro"}, []Param{
			par("start", 2500, 500, 8000, "starting pitch in Hz"),
			par("end", 120, 40, 800, "final pitch in Hz"),
			par("time", 0.25, 0.05, 1, "seconds"),
		}, fxLaser),
		f("fx_radio", "Radio static bed: band-limited hiss, crackle, a drifting whine", []string{"radio", "static", "bed", "noise"}, []Param{
			par("crackle", 0.5, 0, 1, "crackle density"),
			par("whine", 0.3, 0, 1, "heterodyne whine level"),
		}, fxRadio),
		f("fx_vinyl", "Lo-fi vinyl surface: crackle, pops and hiss", []string{"vinyl", "lofi", "bed", "noise", "stereo"}, []Param{
			par("crackle", 0.5, 0, 1, "crackle density"),
			par("pops", 0.4, 0, 1, "pop level"),
			par("hiss", 0.3, 0, 1, "surface hiss"),
		}, fxVinyl),
	}
}

func fxRiser(c *ctx) *out {
	T, nz, rng8 := c.v("length"), c.v("noise"), c.v("range")
	n := c.n(T)
	side := func(r *rng, cents float64, pan float64) []float64 {
		var bp svf
		var a, b osc
		a.ph, b.ph = r.float(), r.float()
		lp := newOnePole(500, c.sr)
		return render(n, func(i int) float64 {
			t := c.t(i)
			x := t / T
			bp.set(expGlide(t, 200, 9000, T), 2, c.sr)
			_, nv, _ := bp.tick(r.white())
			f := expGlide(t, 110, 110*math.Pow(2, rng8), T)
			s := 0.5 * (a.saw(detune(f, cents)/c.sr) + b.saw(detune(f, -cents*0.6)/c.sr))
			lp.set(400+12000*x*x, c.sr)
			s = lp.lp(s)
			// Widen as it rises: the saws start centred and spread out.
			g := 1 + pan*0.6*x
			return (nz*1.5*nv*x*x + 0.6*s*math.Pow(x, 1.5)*g) * fadeIn(t, 0.05)
		})
	}
	return stereo(side(c.rng.fork(), 12, -1), side(c.rng.fork(), -12, 1))
}

func fxDownlifter(c *ctx) *out {
	T, nz := c.v("length"), c.v("noise")
	n := c.n(T)
	side := func(r *rng, cents float64) []float64 {
		var bp svf
		var a osc
		var ph float64
		return render(n, func(i int) float64 {
			t := c.t(i)
			x := t / T
			bp.set(expGlide(t, 8000, 150, T), 1.5, c.sr)
			_, nv, _ := bp.tick(r.white())
			f := expGlide(t, 1500, 50, T*0.9)
			v := 0.5*math.Sin(2*math.Pi*ph) + 0.15*a.saw(detune(f, cents)/c.sr)
			ph += f / c.sr
			ph -= math.Floor(ph)
			env := fadeIn(t, 0.01) * (1 - x) * (1 - x)
			return (nz*1.5*nv + v) * env
		})
	}
	return stereo(side(c.rng.fork(), 8), side(c.rng.fork(), -8))
}

func fxImpact(c *ctx) *out {
	sub, metal, tail := c.v("sub"), c.v("metal"), c.v("tail")
	n := c.n(math.Min(tail+1.5, 7.5))
	x := sineSweep(c, n,
		func(t float64) float64 { return sweep(t, 90, 32, 0.4) },
		func(t float64) float64 { return t60(t, 2.5) })
	scale(x, sub)
	burst := noiseHit(c.rng, n, c.sr, func(y []float64) { lpBuf(y, 6000, c.sr, 1) },
		func(t float64) float64 { return math.Exp(-t / 0.08) })
	addTo(x, burst, 0.7)
	mt := make([]float64, n)
	var ps []partial
	for k := 0; k < 20; k++ {
		ps = append(ps, partial{f: c.rng.rangef(180, 3000), amp: c.rng.rangef(0.3, 1), t60: c.rng.rangef(1, 2.5), ph: c.rng.float()})
	}
	addPartials(mt, c.sr, ps)
	addTo(x, norm1(mt), 0.4*metal)
	for i, v := range x {
		x[i] = math.Tanh(1.2 * v)
	}
	wl, wr := reverb(x, nil, c.sr, verb{t60: tail, size: 1.4, damp: 3500, predelay: 0.02})
	l, r := make([]float64, n), make([]float64, n)
	for i := range x {
		l[i] = x[i] + 1.2*wl[i]
		r[i] = x[i] + 1.2*wr[i]
	}
	return stereo(l, r)
}

func fxWhoosh(c *ctx) *out {
	T, sw, travel := c.v("length"), c.v("sweep"), c.v("travel")
	n := c.n(T)
	src := c.rng.noiseBuf(n)
	var b1, b2 svf
	l, r := make([]float64, n), make([]float64, n)
	for i := range src {
		t := c.t(i)
		x := t / T
		// Peak just past the middle: it approaches faster than it leaves.
		bell := math.Pow(math.Sin(math.Pi*math.Pow(x, 0.8)), 2)
		lo, hi := 300.0, 300+6000*sw
		b1.set(lo*math.Pow(hi/lo, bell), 1.5, c.sr)
		b2.set(lo*2*math.Pow(hi/lo, bell), 3, c.sr)
		_, v1, _ := b1.tick(src[i])
		_, v2, _ := b2.tick(src[i])
		v := (v1 + 0.4*v2) * bell
		gl, gr := panGains(travel * (2*smooth(x) - 1))
		l[i], r[i] = v*gl, v*gr
	}
	return stereo(l, r)
}

func fxLaser(c *ctx) *out {
	f1, f2, T := c.v("start"), c.v("end"), c.v("time")
	var o osc
	x := render(c.n(T*1.6+0.02), func(i int) float64 {
		t := c.t(i)
		f := f2 + (f1-f2)*math.Exp(-t/(T/4))
		return o.square(f/c.sr) * t60(t, T*1.5) * fadeIn(t, 0.001)
	})
	lpBuf(x, 9000, c.sr, 1)
	return mono(x)
}

// impulses scatters clicks: a Poisson process at rate per second, each a
// tiny decaying burst with an exponentially distributed size (many small,
// few large — how dust and static actually sound).
func impulses(r *rng, n int, sr, rate, decay float64) []float64 {
	x := make([]float64, n)
	p := rate / sr
	for i := range x {
		if r.float() < p {
			a := -math.Log(1-r.float()) * 0.3
			if r.float() < 0.5 {
				a = -a
			}
			k := int(decay * sr * 6)
			for j := 0; j < k && i+j < n; j++ {
				x[i+j] += a * math.Exp(-float64(j)/(decay*sr)) * (1 - 2*float64(j%2)*0.3)
			}
		}
	}
	return x
}

func fxRadio(c *ctx) *out {
	cr, wh := c.v("crackle"), c.v("whine")
	p := c.plan(0.1, 3)
	n := p.frames()
	x := c.rng.noiseBuf(n)
	eq(x, bqHighpass(300, 0.7, c.sr), bqLowpass(3200, 0.7, c.sr), bqPeak(1800, 1, 3, c.sr))
	norm1(x)
	l1, l2 := newRot(p.q(0.37), c.sr, 0.1), newRot(p.q(1.1), c.sr, 0.6)
	for i := range x {
		x[i] *= 0.7 + 0.2*l1.next() + 0.1*l2.next()
	}
	cl := impulses(c.rng, n, c.sr, 5+60*cr, 0.0006)
	hpBuf(cl, 500, c.sr, 1)
	addTo(x, cl, 1)
	// The whine of a neighbouring carrier, drifting.
	drift := newRot(p.q(0.25), c.sr, 0)
	var ph float64
	for i := range x {
		f := 2700 + 150*drift.next()
		x[i] += 0.08 * wh * math.Sin(2*math.Pi*ph)
		ph += f / c.sr
		ph -= math.Floor(ph)
	}
	return mono(x).looped(p).fade(0.05).noisy()
}

func fxVinyl(c *ctx) *out {
	cr, pops, hiss := c.v("crackle"), c.v("pops"), c.v("hiss")
	p := c.plan(0.1, 4)
	n := p.frames()
	// Pops and rumble are the record's, so both channels share them; the
	// fine crackle is each groove wall's own.
	shared := impulses(c.rng, n, c.sr, 1.5+3*pops, 0.0015)
	lpBuf(shared, 3000, c.sr, 1)
	scale(shared, 1.5*pops)
	rumble := c.rng.pinkBuf(n)
	lpBuf(rumble, 40, c.sr, 2)
	norm1(rumble)
	side := func(r *rng) []float64 {
		x := impulses(r, n, c.sr, 40+400*cr, 0.0002)
		hpBuf(x, 1000, c.sr, 1)
		scale(x, 0.6)
		h := r.pinkBuf(n)
		hpBuf(h, 2000, c.sr, 1)
		norm1(h)
		addTo(x, h, 0.08*hiss)
		addTo(x, shared, 1)
		addTo(x, rumble, 0.05)
		lpBuf(x, 9000, c.sr, 1)
		return x
	}
	return stereo(side(c.rng.fork()), side(c.rng.fork())).looped(p).fade(0.05).noisy()
}
