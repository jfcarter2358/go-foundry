package synth

// recipes_drums.go — kicks, snares, claps, hats, cymbals, toms and hand
// percussion (recipes 1-40).
//
// Three families of technique, picked per sound by what the real thing is:
// analog drum machines are sine sweeps and filtered noise, because that is
// literally their circuit; acoustic drums are modal banks (a membrane's or
// bar's mode table) with a contact transient; chip drums are the NES's
// triangle and LFSR channels stepped at its 60 Hz frame rate.

import "math"

func drumRecipes() []Recipe {
	d := func(name, desc string, tags []string, ps []Param, body func(*ctx) *out) Recipe {
		return rec(name, "drums", "", desc, tags, ps, body)
	}
	dt := func(name, base, desc string, tags []string, ps []Param, body func(*ctx) *out) Recipe {
		return rec(name, "drums", base, desc, append(tags, "tuned"), ps, body)
	}
	return []Recipe{
		// Kicks
		d("kick_808", "Long sub boom with a deep pitch drop", []string{"kick", "808", "sub"}, []Param{
			par("decay", 1.2, 0.3, 3, "seconds to fade out"),
			par("pitch", 150, 60, 300, "starting pitch in Hz"),
			par("tune", 45, 30, 80, "resting pitch in Hz"),
			par("drive", 0.3, 0, 1, "soft-clip saturation"),
		}, kick808),
		d("kick_909", "Punchy, clicky kick with a short body", []string{"kick", "909", "punchy"}, []Param{
			par("decay", 0.5, 0.15, 1.5, "body length in seconds"),
			par("tune", 55, 40, 90, "resting pitch in Hz"),
			par("click", 0.5, 0, 1, "beater click level"),
			par("drive", 0.5, 0, 1, "saturation"),
		}, kick909),
		d("kick_acoustic", "Felt beater on a kick drum with a little shell ring", []string{"kick", "acoustic", "natural"}, []Param{
			par("tune", 58, 40, 90, "head fundamental in Hz"),
			par("decay", 0.55, 0.2, 1.5, "head ring in seconds"),
			par("beater", 0.5, 0, 1, "beater attack brightness"),
			par("shell", 0.4, 0, 1, "shell resonance"),
		}, kickAcoustic),
		d("kick_chip", "8-bit kick from a stepped triangle pitch drop", []string{"kick", "chip", "8bit"}, []Param{
			par("start", 220, 80, 600, "starting pitch in Hz"),
			par("end", 50, 30, 120, "final pitch in Hz"),
			par("length", 0.22, 0.08, 0.6, "seconds"),
		}, kickChip),
		d("kick_industrial", "Hard distorted kick with a rumbling tail", []string{"kick", "industrial", "distorted"}, []Param{
			par("drive", 0.7, 0, 1, "distortion"),
			par("rumble", 0.6, 0, 1, "rumble tail level"),
			par("decay", 1.4, 0.4, 3, "tail length in seconds"),
			par("tune", 45, 30, 80, "resting pitch in Hz"),
		}, kickIndustrial),
		d("kick_sub", "Pure 55 Hz thump, no click", []string{"kick", "sub", "clean"}, []Param{
			par("tune", 55, 35, 90, "pitch in Hz"),
			par("decay", 0.6, 0.2, 2, "seconds"),
		}, kickSub),
		dt("kick_trap", "C-1", "Tuned long 808 that plays as a bass line", []string{"kick", "808", "bass"}, []Param{
			par("decay", 2.5, 0.8, 5, "tail length in seconds"),
			par("sweep", 4, 1, 8, "starting pitch as a multiple of the note"),
			par("saturation", 0.45, 0, 1, "harmonics so it reads on small speakers"),
		}, kickTrap),
		d("kick_lofi", "Dull, tape-saturated kick", []string{"kick", "lofi", "tape"}, []Param{
			par("tone", 2000, 500, 8000, "lowpass cutoff in Hz"),
			par("bits", 12, 6, 16, "bit depth"),
			par("wow", 0.4, 0, 1, "tape speed wobble"),
		}, kickLofi),

		// Snares and claps
		d("snare_808", "Classic 808 snare: two tones and hissy snap", []string{"snare", "808"}, []Param{
			par("tune", 185, 120, 300, "tone pitch in Hz"),
			par("tone", 0.6, 0, 1, "tone level"),
			par("snappy", 0.7, 0, 1, "noise level"),
			par("decay", 0.25, 0.08, 0.8, "noise decay in seconds"),
		}, snare808),
		d("snare_909", "909 snare: tuned body with bright noise", []string{"snare", "909"}, []Param{
			par("tune", 190, 120, 300, "tone pitch in Hz"),
			par("tone", 0.55, 0, 1, "tone level"),
			par("snappy", 0.8, 0, 1, "noise level"),
			par("decay", 0.28, 0.08, 0.8, "noise tail in seconds"),
		}, snare909),
		d("snare_acoustic", "Stick on a snare: shell modes and rattling wires", []string{"snare", "acoustic"}, []Param{
			par("tune", 195, 140, 320, "head fundamental in Hz"),
			par("wires", 0.7, 0, 1, "snare wire level"),
			par("decay", 0.3, 0.1, 0.8, "wire decay in seconds"),
			par("ring", 0.4, 0, 1, "head ring"),
		}, snareAcoustic),
		d("snare_rim", "Rim click: short woody tones", []string{"snare", "rim", "click"}, []Param{
			par("tone", 1700, 900, 3000, "main tone in Hz"),
			par("decay", 0.05, 0.02, 0.2, "seconds"),
		}, snareRim),
		d("snare_brush", "Brush swish on a snare", []string{"snare", "brush", "soft"}, []Param{
			par("attack", 0.03, 0.005, 0.12, "swish onset in seconds"),
			par("decay", 0.35, 0.1, 1, "seconds"),
			par("tone", 3000, 1000, 8000, "band centre in Hz"),
		}, snareBrush),
		d("snare_chip", "NES noise-channel snare burst", []string{"snare", "chip", "8bit"}, []Param{
			par("decay", 0.18, 0.05, 0.5, "seconds"),
			par("pitch", 3, 0, 10, "noise period setting (higher is lower)"),
			par("body", 0.4, 0, 1, "triangle thump under the noise"),
		}, snareChip),
		d("snare_gated", "80s snare through a plate, hard-gated", []string{"snare", "80s", "reverb", "stereo"}, []Param{
			par("gate", 0.25, 0.1, 0.6, "gate time in seconds"),
			par("size", 0.7, 0.2, 1, "plate size"),
			par("wet", 0.65, 0, 1, "reverb level"),
		}, snareGated),
		d("clap_808", "808 clap: stacked noise bursts and a short tail", []string{"clap", "808"}, []Param{
			par("spread", 0.010, 0.004, 0.025, "seconds between bursts"),
			par("tail", 0.2, 0.05, 0.6, "tail decay in seconds"),
			par("tone", 1200, 600, 3000, "band centre in Hz"),
		}, clap808),
		d("clap_wide", "Wide stereo clap with offset, panned hands", []string{"clap", "stereo", "wide"}, []Param{
			par("spread", 0.012, 0.004, 0.03, "seconds between bursts"),
			par("tail", 0.22, 0.05, 0.6, "tail decay in seconds"),
			par("width", 0.8, 0, 1, "stereo width"),
		}, clapWide),
		d("snap", "Finger snap", []string{"snap", "click"}, []Param{
			par("tone", 2500, 1500, 4000, "resonance in Hz"),
			par("decay", 0.05, 0.02, 0.15, "seconds"),
		}, snap),

		// Hats and cymbals
		d("hat_closed_808", "808 closed hat: six metallic squares, 50 ms", []string{"hat", "808", "closed"}, []Param{
			par("decay", 0.12, 0.03, 0.4, "seconds to fade out"),
			par("tone", 7000, 4000, 12000, "highpass in Hz"),
		}, hat808()),
		d("hat_closed_tight", "Very short, bright closed hat, 20 ms", []string{"hat", "closed", "tight"}, []Param{
			par("decay", 0.05, 0.02, 0.2, "seconds to fade out"),
			par("tone", 9000, 5000, 14000, "highpass in Hz"),
		}, hat808()),
		d("hat_open_808", "808 open hat, 450 ms", []string{"hat", "808", "open"}, []Param{
			par("decay", 0.9, 0.2, 2, "seconds to fade out"),
			par("tone", 7000, 4000, 12000, "highpass in Hz"),
		}, hat808()),
		d("hat_pedal", "Foot-closed hat: soft attack, band-limited chick", []string{"hat", "pedal", "soft"}, []Param{
			par("decay", 0.12, 0.04, 0.4, "seconds to fade out"),
			par("tone", 6500, 3000, 10000, "band centre in Hz"),
		}, hatPedal),
		d("hat_chip", "NES short-mode noise hat", []string{"hat", "chip", "8bit"}, []Param{
			par("decay", 0.07, 0.02, 0.3, "seconds"),
			par("pitch", 1, 0, 6, "noise period setting"),
		}, hatChip),
		d("shaker", "Shaker: bright noise in two pushes", []string{"shaker", "percussion"}, []Param{
			par("length", 0.14, 0.05, 0.4, "seconds"),
			par("tone", 6000, 3000, 12000, "band centre in Hz"),
		}, shaker),
		d("ride", "Ride cymbal: stick ping over a shimmering wash", []string{"cymbal", "ride"}, []Param{
			par("decay", 3, 1, 6, "seconds"),
			par("ping", 0.5, 0, 1, "stick ping level"),
			par("wash", 0.4, 0, 1, "noise wash level"),
		}, ride),
		d("ride_bell", "Ride bell: few strong, bell-like partials", []string{"cymbal", "ride", "bell"}, []Param{
			par("decay", 2.5, 0.8, 6, "seconds"),
			par("tone", 650, 400, 1000, "lowest partial in Hz"),
		}, rideBell),
		d("crash", "Crash cymbal: dense partials and a noise bloom", []string{"cymbal", "crash", "stereo"}, []Param{
			par("decay", 4, 1.5, 6, "seconds"),
			par("tone", 0.6, 0, 1, "brightness"),
			par("width", 0.8, 0, 1, "stereo width"),
		}, crash),
		d("cymbal_reverse", "Reversed crash swelling into the beat", []string{"cymbal", "reverse", "stereo"}, []Param{
			par("length", 2, 0.5, 4, "seconds"),
			par("tone", 0.6, 0, 1, "brightness"),
		}, cymbalReverse),

		// Toms and percussion
		dt("tom_808", "C-3", "808 tom: tuned sine with a pitch bend", []string{"tom", "808"}, []Param{
			par("decay", 0.6, 0.15, 2, "seconds"),
			par("bend", 0.35, 0, 1, "pitch drop amount"),
		}, tom808),
		dt("tom_mid", "C-3", "Mid tom: membrane modes and stick attack", []string{"tom", "acoustic"}, []Param{
			par("decay", 0.55, 0.15, 1.5, "seconds"),
			par("stick", 0.5, 0, 1, "stick attack level"),
		}, tomAcoustic),
		dt("tom_floor", "C-2", "Floor tom: lower and longer", []string{"tom", "acoustic", "floor"}, []Param{
			par("decay", 0.9, 0.2, 2.5, "seconds"),
			par("stick", 0.45, 0, 1, "stick attack level"),
		}, tomAcoustic),
		d("conga", "Open conga tone with a slap", []string{"conga", "hand", "percussion"}, []Param{
			par("tune", 330, 180, 500, "pitch in Hz"),
			par("decay", 0.35, 0.1, 1, "seconds"),
			par("slap", 0.4, 0, 1, "slap transient"),
		}, handDrum),
		d("bongo", "Bongo: small, high, short", []string{"bongo", "hand", "percussion"}, []Param{
			par("tune", 520, 300, 900, "pitch in Hz"),
			par("decay", 0.18, 0.05, 0.6, "seconds"),
			par("slap", 0.5, 0, 1, "slap transient"),
		}, handDrum),
		d("cowbell_808", "808 cowbell: two squares through a bandpass", []string{"cowbell", "808"}, []Param{
			par("decay", 0.35, 0.1, 1, "seconds"),
			par("tone", 1000, 500, 3000, "band centre in Hz"),
		}, cowbell808),
		d("woodblock", "Woodblock knock", []string{"woodblock", "wood", "percussion"}, []Param{
			par("tune", 900, 400, 2000, "pitch in Hz"),
			par("decay", 0.08, 0.03, 0.3, "seconds"),
		}, woodblock),
		d("clave", "Clave: bright, pitched click", []string{"clave", "wood", "percussion"}, []Param{
			par("tune", 2500, 1500, 4000, "pitch in Hz"),
			par("decay", 0.1, 0.03, 0.3, "seconds"),
		}, clave),
		d("tambourine", "Tambourine: jingles and skin", []string{"tambourine", "metal", "percussion"}, []Param{
			par("decay", 0.3, 0.1, 1, "seconds"),
			par("jingles", 0.6, 0.1, 1, "jingle density"),
			par("spread", 0.04, 0.005, 0.1, "seconds the jingles scatter over"),
		}, tambourine),
		d("triangle", "Orchestral triangle: long metallic ring", []string{"triangle", "metal", "orchestral"}, []Param{
			par("decay", 4, 1, 6, "seconds"),
			par("tone", 1100, 600, 2000, "lowest mode in Hz"),
		}, triangleInst),
		dt("timpani", "C-3", "Timpani: tuned membrane that settles into pitch", []string{"timpani", "orchestral"}, []Param{
			par("decay", 2.5, 0.8, 4, "seconds"),
			par("mallet", 0.4, 0, 1, "mallet hardness"),
			par("room", 0.2, 0, 1, "room ambience"),
		}, timpani),
		d("taiko", "Taiko: huge low drum in a room", []string{"taiko", "drum", "stereo", "cinematic"}, []Param{
			par("tune", 70, 45, 110, "head fundamental in Hz"),
			par("decay", 1.2, 0.4, 2.5, "head ring in seconds"),
			par("room", 0.4, 0, 1, "reverb level"),
		}, taiko),
	}
}

// sineSweep is the analog drum voice: a sine whose pitch and level follow
// two curves.
func sineSweep(c *ctx, n int, f func(t float64) float64, amp func(t float64) float64) []float64 {
	var ph float64
	return render(n, func(i int) float64 {
		t := c.t(i)
		v := math.Sin(2*math.Pi*ph) * amp(t)
		ph += f(t) / c.sr
		if ph >= 1 {
			ph--
		}
		return v
	})
}

// satNorm is tanh saturation scaled so full scale stays full scale.
func satNorm(x []float64, amount float64) {
	g := 1 + amount
	k := 1 / math.Tanh(g)
	for i, v := range x {
		x[i] = math.Tanh(g*v) * k
	}
}

func kick808(c *ctx) *out {
	decay, f1, f2 := c.v("decay"), c.v("pitch"), c.v("tune")
	x := sineSweep(c, c.n(decay+0.05),
		func(t float64) float64 { return sweep(t, f1, f2, 0.045) },
		func(t float64) float64 { return t60(t, decay) })
	satNorm(x, 4*c.v("drive"))
	return mono(x)
}

func kick909(c *ctx) *out {
	decay, tune := c.v("decay"), c.v("tune")
	x := sineSweep(c, c.n(decay+0.05),
		func(t float64) float64 { return tune + 180*math.Exp(-t/0.03) + 400*math.Exp(-t/0.004) },
		func(t float64) float64 { return math.Min(1, 1.15*t60(t, decay)) })
	// The click: a few milliseconds of highpassed noise and a pulse, the
	// beater's contact the body sweep cannot supply.
	click := c.rng.noiseBuf(c.n(0.006))
	hpBuf(click, 1200, c.sr, 2)
	for i := range click {
		click[i] *= math.Exp(-c.t(i) / 0.0015)
	}
	addTo(x, norm1(click), 0.5*c.v("click"))
	pulse := strike(c.n(0.002), c.sr, 0.0006)
	addTo(x, pulse, 0.5*c.v("click"))
	satNorm(x, 3*c.v("drive"))
	return mono(x)
}

func kickAcoustic(c *ctx) *out {
	f, decay, beater, shell := c.v("tune"), c.v("decay"), c.v("beater"), c.v("shell")
	n := c.n(decay*1.4 + 0.1)
	x := make([]float64, n)
	amps := []float64{1, 0.45, 0.3, 0.22, 0.16, 0.12}
	ps := modes(f, membraneRatios[:6],
		func(i int) float64 { return amps[i] * (0.5 + 0.5*beater) },
		func(i int) float64 { return decay / (1 + 1.2*float64(i)) })
	ps[0].amp = 1
	ringGlide(x, c.sr, ps, func(t float64) float64 { return 1 + 0.15*math.Exp(-t/0.025) })
	// The felt: a soft thump and a little slap, brighter as beater rises.
	thump := strike(n, c.sr, 0.004)
	addTo(x, thump, 0.4)
	slap := c.rng.noiseBuf(c.n(0.02))
	lpBuf(slap, 1500+5000*beater, c.sr, 2)
	for i := range slap {
		slap[i] *= math.Exp(-c.t(i) / 0.004)
	}
	addTo(x, norm1(slap), 0.25*beater+0.05)
	// The shell rings quietly at a few unrelated frequencies.
	addPartials(x, c.sr, []partial{
		{f: f * 3.1, amp: 0.05 * shell, t60: 0.35, attack: 0.002},
		{f: f * 5.3, amp: 0.035 * shell, t60: 0.3, attack: 0.002},
		{f: f * 8.1, amp: 0.02 * shell, t60: 0.2, attack: 0.002},
	})
	return mono(x)
}

// nesTriangle is the NES triangle channel's 32-step, 4-bit sequence.
func nesTriangle() []float64 {
	t := make([]float64, 32)
	for i := range t {
		v := 15 - i
		if i >= 16 {
			v = i - 16
		}
		t[i] = float64(v)/7.5 - 1
	}
	return t
}

func kickChip(c *ctx) *out {
	f1, f2, length := c.v("start"), c.v("end"), c.v("length")
	tri := &stepTable{tab: nesTriangle()}
	noise := newLFSR(false)
	x := render(c.n(length), func(i int) float64 {
		t := c.t(i)
		frame := math.Floor(t * 60) // the NES updates registers once a frame
		f := f1 * math.Pow(f2/f1, math.Min(frame/6, 1))
		v := tri.next(f / c.sr)
		if frame < 1 {
			v += 0.5 * noise.next(nesNoiseRate(6)/c.sr)
		}
		return v
	})
	return mono(x)
}

func kickIndustrial(c *ctx) *out {
	decay, tune, drive, rumble := c.v("decay"), c.v("tune"), c.v("drive"), c.v("rumble")
	n := c.n(decay + 0.4)
	x := sineSweep(c, n,
		func(t float64) float64 { return sweep(t, 230, tune, 0.05) },
		func(t float64) float64 { return t60(t, 0.5) })
	g := 2 + 18*drive
	driveBuf(x, func(v float64) float64 { return math.Tanh(g * v) })
	// The rumble is the classic trick: the kick into a long dark reverb,
	// distorted again and lowpassed until only the grumble is left.
	wl, wr := reverb(x, nil, c.sr, verb{t60: decay * 1.3, size: 0.9, damp: 2000, predelay: 0.01})
	rb := make([]float64, n)
	for i := range rb {
		rb[i] = (wl[i] + wr[i]) * 2
	}
	driveBuf(rb, func(v float64) float64 { return math.Tanh(4 * v) })
	lpBuf(rb, 180, c.sr, 2)
	hpBuf(rb, 30, c.sr, 1)
	for i := range rb {
		rb[i] *= t60(c.t(i), decay) * smooth(c.t(i)/0.12)
	}
	norm1(rb)
	addTo(x, rb, 0.6*rumble)
	return mono(x)
}

func kickSub(c *ctx) *out {
	f, decay := c.v("tune"), c.v("decay")
	x := sineSweep(c, c.n(decay+0.05),
		func(t float64) float64 { return f * (1 + 0.1*math.Exp(-t/0.04)) },
		func(t float64) float64 { return fadeIn(t, 0.006) * t60(t, decay) })
	return mono(x)
}

func kickTrap(c *ctx) *out {
	f0, decay, sw := c.f0, c.v("decay"), c.v("sweep")
	x := sineSweep(c, c.n(decay+0.1),
		func(t float64) float64 { return f0 * (1 + (sw-1)*math.Exp(-t/0.035)) },
		func(t float64) float64 {
			if t < 0.08 {
				return 1
			}
			return t60(t-0.08, decay)
		})
	satNorm(x, 6*c.v("saturation"))
	return mono(x)
}

func kickLofi(c *ctx) *out {
	wow := c.v("wow")
	n := c.n(0.6)
	x := sineSweep(c, n,
		func(t float64) float64 {
			return sweep(t, 120, 48, 0.05) * (1 + 0.012*wow*math.Sin(2*math.Pi*3.1*t))
		},
		func(t float64) float64 { return t60(t, 0.5) })
	hiss := c.rng.noiseBuf(n)
	lpBuf(hiss, 5000, c.sr, 1)
	for i := range x {
		x[i] += 0.006 * hiss[i] * t60(c.t(i), 0.5)
	}
	satNorm(x, 1.5)
	lpBuf(x, c.v("tone"), c.sr, 2)
	norm1(x)
	quantize(x, c.v("bits"))
	return mono(x)
}

// noiseHit is filtered noise under an envelope: the snare and clap staple.
func noiseHit(r *rng, n int, sr float64, filt func(x []float64), env func(t float64) float64) []float64 {
	x := r.noiseBuf(n)
	filt(x)
	for i := range x {
		x[i] *= env(float64(i) / sr)
	}
	return norm1(x)
}

func snare808(c *ctx) *out {
	f, decay := c.v("tune"), c.v("decay")
	n := c.n(decay*1.2 + 0.1)
	tone := sineSweep(c, n,
		func(t float64) float64 { return f * (1 + 0.08*math.Exp(-t/0.01)) },
		func(t float64) float64 { return t60(t, 0.16) })
	addTo(tone, sineSweep(c, n,
		func(t float64) float64 { return f * 1.78 * (1 + 0.05*math.Exp(-t/0.01)) },
		func(t float64) float64 { return t60(t, 0.1) }), 0.55)
	noise := noiseHit(c.rng, n, c.sr, func(x []float64) {
		hpBuf(x, 1800, c.sr, 2)
		lpBuf(x, 8000, c.sr, 1)
	}, func(t float64) float64 { return t60(t, decay) })
	x := make([]float64, n)
	addTo(x, norm1(tone), c.v("tone"))
	addTo(x, noise, 0.8*c.v("snappy"))
	return mono(x)
}

func snare909(c *ctx) *out {
	f, decay := c.v("tune"), c.v("decay")
	n := c.n(decay*1.2 + 0.1)
	tone := sineSweep(c, n,
		func(t float64) float64 { return f * (1 + 0.3*math.Exp(-t/0.012)) },
		func(t float64) float64 { return t60(t, 0.14) })
	addTo(tone, sineSweep(c, n,
		func(t float64) float64 { return f * 1.75 * (1 + 0.25*math.Exp(-t/0.012)) },
		func(t float64) float64 { return t60(t, 0.09) }), 0.6)
	// Two envelopes on the noise: a hard initial crack, then the sizzle.
	noise := noiseHit(c.rng, n, c.sr, func(x []float64) {
		hpBuf(x, 1500, c.sr, 1)
		lpBuf(x, 12000, c.sr, 1)
	}, func(t float64) float64 { return 0.6*t60(t, 0.05) + 0.4*t60(t, decay) })
	x := make([]float64, n)
	addTo(x, norm1(tone), c.v("tone"))
	addTo(x, noise, 0.85*c.v("snappy"))
	satNorm(x, 0.8)
	return mono(x)
}

func snareAcoustic(c *ctx) *out {
	f, decay, ring := c.v("tune"), c.v("decay"), c.v("ring")
	n := c.n(decay*1.3 + 0.1)
	head := make([]float64, n)
	amps := []float64{1, 0.8, 0.55, 0.5, 0.35, 0.3, 0.2}
	ringGlide(head, c.sr, modes(f, membraneRatios[:7],
		func(i int) float64 { return amps[i] },
		func(i int) float64 { return (0.12 + 0.35*ring) / (1 + 0.4*float64(i)) }),
		func(t float64) float64 { return 1 + 0.08*math.Exp(-t/0.015) })
	norm1(head)
	// The wires buzz as long as the bottom head moves: noise gated by the
	// head's own envelope, with the wires' own slower decay.
	var follow float64
	fr := math.Exp(-1 / (0.03 * c.sr))
	var bp svf
	bp.set(5000, 0.6, c.sr)
	wires := make([]float64, n)
	for i := range wires {
		t := c.t(i)
		follow = math.Max(math.Abs(head[i]), follow*fr)
		_, b, _ := bp.tick(c.rng.white())
		wires[i] = b * (0.35 + 0.65*follow) * t60(t, decay) * smooth((t-0.001)/0.003)
	}
	hpBuf(wires, 900, c.sr, 1)
	norm1(wires)
	stick := c.rng.noiseBuf(c.n(0.004))
	hpBuf(stick, 2500, c.sr, 1)
	for i := range stick {
		stick[i] *= math.Exp(-c.t(i) / 0.0008)
	}
	x := make([]float64, n)
	addTo(x, head, 0.7)
	addTo(x, wires, 0.8*c.v("wires"))
	addTo(x, norm1(stick), 0.35)
	return mono(x)
}

func snareRim(c *ctx) *out {
	f, decay := c.v("tone"), c.v("decay")
	n := c.n(decay*2 + 0.05)
	x := make([]float64, n)
	addPartials(x, c.sr, []partial{
		{f: f, amp: 1, t60: decay},
		{f: f * 1.41, amp: 0.7, t60: decay * 0.7},
		{f: f * 0.3, amp: 0.4, t60: decay * 1.2},
	})
	click := c.rng.noiseBuf(c.n(0.003))
	hpBuf(click, 3000, c.sr, 1)
	for i := range click {
		click[i] *= math.Exp(-c.t(i) / 0.0006)
	}
	addTo(x, norm1(click), 0.6)
	return mono(x)
}

func snareBrush(c *ctx) *out {
	att, decay, tone := c.v("attack"), c.v("decay"), c.v("tone")
	n := c.n(att + decay*1.3)
	src := c.rng.pinkBuf(n)
	var bp svf
	x := render(n, func(i int) float64 {
		t := c.t(i)
		// The swish rises in pitch as the bristles speed up.
		bp.set(tone*(0.7+0.3*rise(t, att)), 0.5, c.sr)
		_, b, _ := bp.tick(src[i])
		env := rise(t, att)
		if t > att {
			env = t60(t-att, decay)
		}
		return b * env
	})
	return mono(x)
}

func snareChip(c *ctx) *out {
	decay, pitch, bodyAmt := c.v("decay"), c.v("pitch"), c.v("body")
	noise := newLFSR(false)
	tri := &stepTable{tab: nesTriangle()}
	frames := math.Max(1, decay*60)
	x := render(c.n(decay), func(i int) float64 {
		t := c.t(i)
		frame := math.Floor(t * 60)
		idx := int(pitch + frame/2)
		vol := math.Round(15*(1-frame/frames)) / 15 // 4-bit volume
		v := vol * noise.next(nesNoiseRate(idx)/c.sr)
		if frame < 2 {
			v += bodyAmt * tri.next(180*math.Pow(0.6, frame)/c.sr)
		}
		return v
	})
	return mono(x)
}

func snareGated(c *ctx) *out {
	gt, size, wet := c.v("gate"), c.v("size"), c.v("wet")
	n := c.n(gt + 0.06)
	tone := sineSweep(c, n,
		func(t float64) float64 { return 180 * (1 + 0.25*math.Exp(-t/0.01)) },
		func(t float64) float64 { return t60(t, 0.2) })
	addTo(tone, sineSweep(c, n,
		func(t float64) float64 { return 330 * (1 + 0.2*math.Exp(-t/0.01)) },
		func(t float64) float64 { return t60(t, 0.12) }), 0.5)
	noise := noiseHit(c.rng, n, c.sr, func(x []float64) {
		hpBuf(x, 1200, c.sr, 1)
		lpBuf(x, 10000, c.sr, 1)
	}, func(t float64) float64 { return t60(t, 0.25) })
	dry := make([]float64, n)
	addTo(dry, norm1(tone), 0.8)
	addTo(dry, noise, 0.8)
	wl, wr := reverb(dry, nil, c.sr, verb{t60: 1.2 + 1.5*size, size: 0.4 + 0.5*size, damp: 9000, predelay: 0.004})
	norm := 1 / math.Max(peak1(wl), peak1(wr))
	l, r := make([]float64, n), make([]float64, n)
	for i := range l {
		g := gate(c.t(i), gt, 0.012)
		l[i] = (dry[i] + wet*wl[i]*norm) * g
		r[i] = (dry[i] + wet*wr[i]*norm) * g
	}
	return stereo(l, r)
}

// clapEnv is the 808's clap envelope: a sawtooth retriggered per burst,
// then a tail.
func clapEnv(t float64, offs []float64, tail float64) float64 {
	var e float64
	for _, o := range offs {
		if t >= o {
			e = math.Max(e, math.Exp(-(t-o)/0.006))
		}
	}
	last := offs[len(offs)-1]
	if t >= last {
		e = math.Max(e, 0.7*t60(t-last, tail))
	}
	return e
}

func clap808(c *ctx) *out {
	sp, tail, tone := c.v("spread"), c.v("tail"), c.v("tone")
	offs := []float64{0, sp, 2 * sp, 3 * sp}
	n := c.n(3*sp + tail + 0.05)
	x := c.rng.noiseBuf(n)
	for i := range x {
		x[i] *= clapEnv(c.t(i), offs, tail)
	}
	src := append([]float64(nil), x...)
	var bp svf
	bp.set(tone, 1.4, c.sr)
	for i := range x {
		_, b, _ := bp.tick(src[i])
		x[i] = b + 0.15*src[i]
	}
	hpBuf(x, 400, c.sr, 1)
	return mono(x)
}

func clapWide(c *ctx) *out {
	sp, tail, width := c.v("spread"), c.v("tail"), c.v("width")
	n := c.n(4*sp + tail + 0.06)
	ch := func(r *rng, pan float64) []float64 {
		offs := []float64{0}
		for k := 1; k < 4; k++ {
			offs = append(offs, offs[k-1]+sp*r.rangef(0.6, 1.4))
		}
		x := r.noiseBuf(n)
		var bp svf
		bp.set(1100+r.rangef(-150, 150), 1.2, c.sr)
		for i := range x {
			t := c.t(i)
			// Alternate bursts lean to alternate sides: four hands, not two.
			var e float64
			for k, o := range offs {
				if t >= o {
					g := 1.0
					if (k%2 == 0) != (pan < 0) {
						g = 1 - 0.7*width
					}
					e = math.Max(e, g*math.Exp(-(t-o)/0.006))
				}
			}
			if t >= offs[3] {
				e = math.Max(e, 0.7*t60(t-offs[3], tail))
			}
			_, b, _ := bp.tick(x[i] * e)
			x[i] = b
		}
		return x
	}
	l := ch(c.rng.fork(), -1)
	r := ch(c.rng.fork(), 1)
	return stereo(l, r)
}

func snap(c *ctx) *out {
	f, decay := c.v("tone"), c.v("decay")
	n := c.n(decay*2 + 0.03)
	x := noiseHit(c.rng, n, c.sr, func(x []float64) {
		var bp svf
		bp.set(3200, 1.8, c.sr)
		for i, v := range x {
			_, b, _ := bp.tick(v)
			x[i] = b
		}
	}, func(t float64) float64 { return math.Exp(-t / 0.005) })
	addPartials(x, c.sr, []partial{{f: f, amp: 0.6, t60: decay}, {f: f * 1.5, amp: 0.2, t60: decay * 0.5}})
	return mono(x)
}

// metal808 is the 808's cymbal source: six square oscillators at
// deliberately unrelated frequencies.
func metal808(c *ctx, n int) []float64 {
	fs := []float64{205.3, 304.4, 369.6, 522.7, 540, 800}
	oscs := make([]osc, len(fs))
	for i := range oscs {
		oscs[i].ph = c.rng.float()
	}
	return render(n, func(i int) float64 {
		var v float64
		for k, f := range fs {
			v += oscs[k].square(f / c.sr)
		}
		return v / 6
	})
}

func hat808() func(c *ctx) *out {
	return func(c *ctx) *out {
		decay, tone := c.v("decay"), c.v("tone")
		n := c.n(decay + 0.02)
		src := metal808(c, n)
		var b1, b2 svf
		b1.set(3440, 2, c.sr)
		b2.set(7100, 2, c.sr)
		x := render(n, func(i int) float64 {
			_, a, _ := b1.tick(src[i])
			_, b, _ := b2.tick(src[i])
			return a + b
		})
		hpBuf(x, tone, c.sr, 2)
		for i := range x {
			t := c.t(i)
			x[i] *= t60(t, decay) * fadeIn(t, 0.0004)
		}
		return mono(x)
	}
}

func hatPedal(c *ctx) *out {
	decay, tone := c.v("decay"), c.v("tone")
	n := c.n(decay + 0.03)
	src := metal808(c, n)
	var bp svf
	bp.set(tone, 1.5, c.sr)
	x := render(n, func(i int) float64 {
		_, b, _ := bp.tick(src[i])
		t := c.t(i)
		return b * rise(t, 0.005) * t60(t, decay)
	})
	chick := noiseHit(c.rng, n, c.sr, func(x []float64) {
		var b svf
		b.set(1300, 1.5, c.sr)
		for i, v := range x {
			_, y, _ := b.tick(v)
			x[i] = y
		}
	}, func(t float64) float64 { return rise(t, 0.003) * math.Exp(-t/0.008) })
	norm1(x)
	addTo(x, chick, 0.25)
	return mono(x)
}

func hatChip(c *ctx) *out {
	decay, pitch := c.v("decay"), int(c.v("pitch"))
	noise := newLFSR(true)
	n := c.n(decay + 0.017)
	// The short sequence is lopsided; centre it before the envelope, or the
	// envelope turns its offset into a thump.
	src := render(n, func(int) float64 { return noise.next(nesNoiseRate(pitch) / c.sr) })
	hpBuf(src, 300, c.sr, 1)
	frames := math.Max(1, decay*60)
	x := render(n, func(i int) float64 {
		frame := math.Floor(c.t(i) * 60)
		vol := math.Max(0, math.Round(15*(1-frame/frames))) / 15
		return vol * src[i]
	})
	return mono(x)
}

func shaker(c *ctx) *out {
	length, tone := c.v("length"), c.v("tone")
	n := c.n(length + 0.02)
	x := c.rng.noiseBuf(n)
	var bp svf
	bp.set(tone, 0.7, c.sr)
	for i, v := range x {
		_, x[i], _ = bp.tick(v)
	}
	hpBuf(x, 2000, c.sr, 1)
	// Beads land in clumps: a grainy amplitude on top of two pushes.
	grain := c.rng.noiseBuf(n)
	lpBuf(grain, 400, c.sr, 1)
	norm1(grain)
	for i := range x {
		t := c.t(i)
		push := rise(t, length*0.25)
		if t > length*0.35 {
			push = t60(t-length*0.35, length*0.6)
		}
		second := 0.5 * math.Exp(-math.Pow((t-length*0.55)/(length*0.08), 2))
		x[i] *= (push + second) * (0.6 + 0.4*math.Abs(grain[i]))
	}
	return mono(x)
}

// cymbalPartials is a random cloud of inharmonic modes between lo and hi,
// amplitude falling with frequency, higher modes dying sooner.
func cymbalPartials(r *rng, count int, lo, hi, decay, bright float64) []partial {
	ps := make([]partial, count)
	for i := range ps {
		f := lo * math.Exp(r.float()*math.Log(hi/lo))
		ps[i] = partial{
			f:   f,
			amp: r.rangef(0.4, 1) / math.Pow(1+f/(2000+6000*bright), 0.8),
			t60: decay * r.rangef(0.4, 1) / (1 + f/9000),
			ph:  r.float(),
		}
	}
	return ps
}

func ride(c *ctx) *out {
	decay, ping, wash := c.v("decay"), c.v("ping"), c.v("wash")
	n := c.n(decay + 0.1)
	x := make([]float64, n)
	addPartials(x, c.sr, cymbalPartials(c.rng, 40, 350, 14000, decay, 0.5))
	norm1(x)
	// The ping is the stick: a few strong partials around 3-5 kHz that
	// speak first and die in a quarter second.
	pg := make([]float64, n)
	for k := 0; k < 6; k++ {
		addPartials(pg, c.sr, []partial{{f: c.rng.rangef(2500, 5200), amp: c.rng.rangef(0.5, 1), t60: 0.3, ph: c.rng.float()}})
	}
	norm1(pg)
	w := noiseHit(c.rng, n, c.sr, func(x []float64) {
		hpBuf(x, 5000, c.sr, 2)
	}, func(t float64) float64 { return t60(t, decay*0.7) * rise(t, 0.01) })
	addTo(x, pg, 0.6*ping)
	addTo(x, w, 0.25*wash)
	tick := strike(c.n(0.002), c.sr, 0.0004)
	hpBuf(tick, 2000, c.sr, 1)
	addTo(x, tick, 0.3)
	return mono(x)
}

func rideBell(c *ctx) *out {
	decay, f := c.v("decay"), c.v("tone")
	n := c.n(decay + 0.1)
	x := make([]float64, n)
	ratios := []float64{1, 1.51, 2.02, 2.47, 2.98, 3.6, 4.1, 4.9, 5.7, 6.6, 7.4, 8.3}
	var ps []partial
	for i, r := range ratios {
		ps = append(ps, partial{
			f: f * r * c.rng.rangef(0.995, 1.005), amp: 1 / (1 + 0.25*float64(i)),
			t60: decay * (1.2 - 0.06*float64(i)), ph: c.rng.float(),
		})
	}
	addPartials(x, c.sr, ps)
	norm1(x)
	cloud := make([]float64, n)
	addPartials(cloud, c.sr, cymbalPartials(c.rng, 25, 600, 12000, decay*0.6, 0.6))
	addTo(x, norm1(cloud), 0.3)
	return mono(x)
}

// crashChannel is one side of a crash: its own partial cloud and noise.
func crashChannel(c *ctx, r *rng, n int, decay, tone float64) []float64 {
	x := make([]float64, n)
	addPartials(x, c.sr, cymbalPartials(r, 150, 250, 16000, decay, tone))
	norm1(x)
	for i := range x { // the crash blooms: partials swell as energy spreads
		x[i] *= 0.6 + 0.4*rise(c.t(i), 0.05)
	}
	w := noiseHit(r, n, c.sr, func(x []float64) {
		hpBuf(x, 2500+2500*tone, c.sr, 2)
	}, func(t float64) float64 { return 0.4*math.Exp(-t/0.02) + 0.6*t60(t, decay*0.8)*rise(t, 0.015) })
	addTo(x, w, 0.6)
	return x
}

func crashStereo(c *ctx, decay, tone, width float64) (l, r []float64) {
	n := c.n(decay + 0.05)
	a := crashChannel(c, c.rng.fork(), n, decay, tone)
	b := crashChannel(c, c.rng.fork(), n, decay, tone)
	l, r = make([]float64, n), make([]float64, n)
	for i := range a {
		m, s := (a[i]+b[i])/2, (a[i]-b[i])/2
		l[i], r[i] = m+width*s, m-width*s
	}
	return l, r
}

func crash(c *ctx) *out {
	l, r := crashStereo(c, c.v("decay"), c.v("tone"), c.v("width"))
	return stereo(l, r)
}

func cymbalReverse(c *ctx) *out {
	length := c.v("length")
	l, r := crashStereo(c, length*1.2, c.v("tone"), 0.8)
	n := c.n(length)
	l, r = reverse(l[:n]), reverse(r[:n])
	for i := range l {
		g := fadeIn(c.t(i), length*0.3)
		l[i] *= g
		r[i] *= g
	}
	return stereo(l, r)
}

func tom808(c *ctx) *out {
	decay, bend := c.v("decay"), c.v("bend")
	f := c.f0
	x := sineSweep(c, c.n(decay+0.05),
		func(t float64) float64 { return f * (1 + bend*math.Exp(-t/0.04)) },
		func(t float64) float64 { return t60(t, decay) })
	click := noiseHit(c.rng, c.n(0.01), c.sr, func(x []float64) { lpBuf(x, 4000, c.sr, 1) },
		func(t float64) float64 { return math.Exp(-t / 0.0015) })
	addTo(x, click, 0.15)
	return mono(x)
}

func tomAcoustic(c *ctx) *out {
	decay, stick := c.v("decay"), c.v("stick")
	f := c.f0
	n := c.n(decay*1.3 + 0.1)
	x := make([]float64, n)
	amps := []float64{1, 0.5, 0.35, 0.3, 0.2, 0.15}
	ringGlide(x, c.sr, modes(f, membraneRatios[:6],
		func(i int) float64 { return amps[i] },
		func(i int) float64 { return decay / (1 + 0.8*float64(i)) }),
		func(t float64) float64 { return 1 + 0.1*math.Exp(-t/0.05) })
	norm1(x)
	hit := noiseHit(c.rng, c.n(0.02), c.sr, func(x []float64) { lpBuf(x, 5000, c.sr, 2) },
		func(t float64) float64 { return math.Exp(-t / 0.003) })
	addTo(x, hit, 0.3*stick)
	return mono(x)
}

func handDrum(c *ctx) *out {
	f, decay, slap := c.v("tune"), c.v("decay"), c.v("slap")
	n := c.n(decay*1.3 + 0.05)
	x := make([]float64, n)
	// Hand drums are tuned close to harmonic: the head is tensioned until
	// the fundamental dominates.
	ringGlide(x, c.sr, []partial{
		{f: f, amp: 1, t60: decay},
		{f: f * 1.5, amp: 0.3, t60: decay * 0.5},
		{f: f * 2.04, amp: 0.25, t60: decay * 0.35},
		{f: f * 2.9, amp: 0.15 + 0.2*slap, t60: decay * 0.2},
	}, func(t float64) float64 { return 1 + 0.04*math.Exp(-t/0.02) })
	norm1(x)
	s := noiseHit(c.rng, c.n(0.03), c.sr, func(x []float64) {
		var bp svf
		bp.set(2600, 1.2, c.sr)
		for i, v := range x {
			_, b, _ := bp.tick(v)
			x[i] = b
		}
	}, func(t float64) float64 { return math.Exp(-t / 0.006) })
	addTo(x, s, 0.5*slap)
	return mono(x)
}

func cowbell808(c *ctx) *out {
	decay, tone := c.v("decay"), c.v("tone")
	n := c.n(decay + 0.05)
	var a, b osc
	var bp svf
	bp.set(tone, 1.6, c.sr)
	x := render(n, func(i int) float64 {
		v := a.square(540/c.sr) + 0.8*b.square(800/c.sr)
		_, y, _ := bp.tick(v)
		t := c.t(i)
		return (0.4*y + 0.1*v) * (0.65*math.Exp(-t/0.015) + 0.35*t60(t, decay))
	})
	return mono(x)
}

func woodblock(c *ctx) *out {
	f, decay := c.v("tune"), c.v("decay")
	n := c.n(decay*1.5 + 0.03)
	x := make([]float64, n)
	addPartials(x, c.sr, []partial{
		{f: f, amp: 1, t60: decay},
		{f: f * 2.57, amp: 0.4, t60: decay * 0.5},
		{f: f * 4.3, amp: 0.15, t60: decay * 0.3},
	})
	k := noiseHit(c.rng, c.n(0.005), c.sr, func(x []float64) { hpBuf(x, 2000, c.sr, 1) },
		func(t float64) float64 { return math.Exp(-t / 0.0007) })
	addTo(x, k, 0.35)
	return mono(x)
}

func clave(c *ctx) *out {
	f, decay := c.v("tune"), c.v("decay")
	x := make([]float64, c.n(decay*1.2+0.02))
	addPartials(x, c.sr, []partial{{f: f, amp: 1, t60: decay}, {f: f * 2.7, amp: 0.05, t60: decay * 0.3}})
	k := noiseHit(c.rng, c.n(0.003), c.sr, func(x []float64) { hpBuf(x, 3000, c.sr, 1) },
		func(t float64) float64 { return math.Exp(-t / 0.0005) })
	addTo(x, k, 0.2)
	return mono(x)
}

func tambourine(c *ctx) *out {
	decay, density, spread := c.v("decay"), c.v("jingles"), c.v("spread")
	n := c.n(spread + decay*1.2 + 0.05)
	x := make([]float64, n)
	count := int(6 + 24*density)
	for k := 0; k < count; k++ {
		// Jingles cluster early: they are all struck by the same hit and
		// keep colliding a little after.
		off := c.n(spread * math.Pow(c.rng.float(), 2))
		amp := c.rng.rangef(0.3, 1)
		var ps []partial
		for j := 0; j < 4; j++ {
			ps = append(ps, partial{f: c.rng.rangef(5000, 12500), amp: amp * c.rng.rangef(0.4, 1), t60: decay * c.rng.rangef(0.3, 0.7), ph: c.rng.float()})
		}
		addPartials(x[off:], c.sr, ps)
	}
	norm1(x)
	w := noiseHit(c.rng, n, c.sr, func(x []float64) { hpBuf(x, 6000, c.sr, 2) },
		func(t float64) float64 { return t60(t, decay*0.5) })
	addTo(x, w, 0.35)
	skin := make([]float64, n)
	addPartials(skin, c.sr, []partial{{f: 240, amp: 1, t60: 0.08}, {f: 410, amp: 0.4, t60: 0.05}})
	addTo(x, skin, 0.25)
	return mono(x)
}

func triangleInst(c *ctx) *out {
	decay, f := c.v("decay"), c.v("tone")
	n := c.n(math.Min(decay+0.1, 6))
	x := make([]float64, n)
	ratios := []float64{1, 2.32, 2.94, 3.67, 4.51, 5.4, 6.31, 7.28, 8.93, 10.2, 11.8, 13.3}
	var ps []partial
	for i, r := range ratios {
		amp := 1 / (1 + 0.15*float64(i))
		t := decay / (1 + 0.08*float64(i))
		// A bent rod's modes come in near-degenerate pairs; their slow
		// beating is the triangle's shimmer.
		ps = append(ps,
			partial{f: f * r, amp: amp, t60: t, ph: c.rng.float()},
			partial{f: f * r * (1 + c.rng.rangef(0.0004, 0.0015)), amp: amp * 0.6, t60: t * 0.9, ph: c.rng.float()})
	}
	addPartials(x, c.sr, ps)
	norm1(x)
	ping := noiseHit(c.rng, c.n(0.01), c.sr, func(x []float64) { hpBuf(x, 6000, c.sr, 1) },
		func(t float64) float64 { return math.Exp(-t / 0.001) })
	addTo(x, ping, 0.3)
	return mono(x)
}

func timpani(c *ctx) *out {
	decay, mallet, room := c.v("decay"), c.v("mallet"), c.v("room")
	f := c.f0
	n := c.n(decay + 0.8)
	x := make([]float64, n)
	ratios := []float64{1, 1.504, 1.742, 2.0, 2.245, 2.494, 2.8, 2.98}
	amps := []float64{1, 0.6, 0.3, 0.45, 0.25, 0.2, 0.12, 0.1}
	decs := []float64{1, 0.8, 0.5, 0.6, 0.4, 0.35, 0.3, 0.25}
	ps := make([]partial, 0, 9)
	for i, r := range ratios {
		a := amps[i]
		if i > 0 {
			a *= 0.5 + 0.7*mallet
		}
		ps = append(ps, partial{f: f * r, amp: a, t60: decay * decs[i]})
	}
	ps = append(ps, partial{f: f * 0.62, amp: 0.5, t60: 0.18}) // the (0,1) thud
	// A hard hit stretches the head: pitch starts sharp and settles.
	ringGlide(x, c.sr, ps, func(t float64) float64 { return 1 + 0.015*math.Exp(-t/0.12) })
	norm1(x)
	hit := noiseHit(c.rng, c.n(0.03), c.sr, func(x []float64) { lpBuf(x, 800+3000*mallet, c.sr, 2) },
		func(t float64) float64 { return math.Exp(-t / 0.006) })
	addTo(x, hit, 0.2)
	if room > 0 {
		wl, wr := reverb(x, nil, c.sr, verb{t60: 1.6, size: 1, damp: 4000, predelay: 0.02})
		for i := range x {
			x[i] += room * 0.6 * (wl[i] + wr[i])
		}
	}
	return mono(x)
}

func taiko(c *ctx) *out {
	f, decay, room := c.v("tune"), c.v("decay"), c.v("room")
	n := c.n(3)
	x := make([]float64, n)
	amps := []float64{1, 0.55, 0.4, 0.3, 0.2, 0.15}
	ringGlide(x, c.sr, modes(f, membraneRatios[:6],
		func(i int) float64 { return amps[i] },
		func(i int) float64 { return decay / (1 + 0.7*float64(i)) }),
		func(t float64) float64 { return 1 + 0.15*math.Exp(-t/0.05) })
	norm1(x)
	thump := strike(n, c.sr, 0.006)
	addTo(x, thump, 0.5)
	slap := noiseHit(c.rng, c.n(0.03), c.sr, func(x []float64) {
		var bp svf
		bp.set(1500, 1, c.sr)
		for i, v := range x {
			_, b, _ := bp.tick(v)
			x[i] = b
		}
	}, func(t float64) float64 { return math.Exp(-t / 0.006) })
	addTo(x, slap, 0.2)
	wl, wr := reverb(x, nil, c.sr, verb{t60: 2.2, size: 1.2, damp: 3500, predelay: 0.015})
	l, r := make([]float64, n), make([]float64, n)
	for i := range x {
		l[i] = x[i] + room*1.5*wl[i]
		r[i] = x[i] + room*1.5*wr[i]
	}
	return stereo(l, r)
}
