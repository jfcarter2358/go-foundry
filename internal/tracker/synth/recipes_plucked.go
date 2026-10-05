package synth

// recipes_plucked.go — guitars, harp and other plucked strings (95-102).
//
// All Karplus-Strong strings, differentiated by what a player would change:
// where and with what the string is plucked (the excitation), how fast its
// highs die (the loop filter), and above all what body it sits on — a
// guitar's air and top resonances, a banjo's drumhead, a kalimba's box.
// Real strings vibrate in two planes at slightly different pitches; adding a
// quieter, slightly detuned second string gives the slow beating that keeps
// a long note alive.

import "math"

func pluckedRecipes() []Recipe {
	p := func(name, base, desc string, tags []string, ps []Param, body func(*ctx) *out) Recipe {
		return rec(name, "plucked", base, desc, tags, ps, body)
	}
	return []Recipe{
		p("guitar_nylon", "C-3", "Nylon-string guitar, fingerpicked", []string{"guitar", "nylon", "classical"}, []Param{
			par("decay", 4.5, 1, 6, "seconds"),
			par("brightness", 0.45, 0, 1, "string brightness"),
			par("body", 0.6, 0, 1, "body resonance"),
		}, guitarNylon),
		p("guitar_steel", "C-3", "Steel-string acoustic with sympathetic shimmer", []string{"guitar", "steel", "acoustic"}, []Param{
			par("decay", 5, 1, 6, "seconds"),
			par("brightness", 0.75, 0, 1, "string brightness"),
			par("sympathetic", 0.5, 0, 1, "sympathetic string ring"),
		}, guitarSteel),
		p("guitar_muted", "C-3", "Palm-muted guitar chug", []string{"guitar", "muted", "percussive"}, []Param{
			par("mute", 0.7, 0, 1, "palm pressure"),
			par("brightness", 0.5, 0, 1, "pick brightness"),
		}, guitarMuted),
		p("guitar_power", "C-3", "Distorted power chord: root and fifth", []string{"guitar", "distorted", "rock"}, []Param{
			par("drive", 0.7, 0, 1, "distortion"),
			par("tone", 4500, 1500, 9000, "cabinet rolloff in Hz"),
			par("octave", 0.4, 0, 1, "added octave"),
		}, guitarPower),
		p("harp", "C-4", "Concert harp: round, ringing pluck", []string{"harp", "orchestral"}, []Param{
			par("decay", 4, 1, 6, "seconds"),
			par("brightness", 0.5, 0, 1, "string brightness"),
		}, harp),
		p("koto", "C-4", "Koto: bright pluck that bends into pitch", []string{"koto", "japanese", "world"}, []Param{
			par("decay", 3, 0.8, 6, "seconds"),
			par("bend", 0.4, 0, 2, "initial bend in semitones"),
		}, koto),
		p("banjo", "C-4", "Banjo: twangy string on a drumhead", []string{"banjo", "folk", "bright"}, []Param{
			par("decay", 1.6, 0.4, 4, "seconds"),
			par("head", 0.6, 0, 1, "head resonance"),
		}, banjo),
		p("kalimba", "C-4", "Kalimba: plucked metal tine in a wooden box", []string{"kalimba", "mbira", "world"}, []Param{
			par("decay", 3, 0.8, 6, "seconds"),
			par("bright", 0.5, 0, 1, "overtone level"),
		}, kalimba),
	}
}

// twoPlane plucks a string in two planes (main plus a quieter, slightly
// sharp one) and returns their sum.
func twoPlane(c *ctx, f float64, n int, decay, bright, pick, exBright float64) []float64 {
	exc := pluck(c.rng, c.sr, f, exBright, pick)
	a := runKS(newKS(c.sr, f, decay, bright, 0), exc, n)
	b := runKS(newKS(c.sr, detune(f, 0.7), decay*0.75, bright, 0), exc, n)
	addTo(a, b, 0.3)
	return norm1(a)
}

var (
	nylonBody = []bodyMode{{98, 8, 1}, {204, 10, 0.7}, {390, 6, 0.5}, {550, 5, 0.35}, {900, 4, 0.2}, {1650, 3, 0.12}, {2900, 2, 0.08}}
	steelBody = []bodyMode{{100, 6, 0.8}, {190, 8, 0.6}, {380, 5, 0.4}, {1000, 3, 0.25}, {2500, 2, 0.2}}
)

func guitarNylon(c *ctx) *out {
	decay, bright, bd := c.v("decay"), c.v("brightness"), c.v("body")
	x := twoPlane(c, c.f0, c.n(decay), decay, bright, 0.15, 0.4)
	body(x, c.sr, 1-0.4*bd, scaleBody(nylonBody, bd))
	return mono(x)
}

func scaleBody(m []bodyMode, k float64) []bodyMode {
	out := make([]bodyMode, len(m))
	for i, b := range m {
		out[i] = bodyMode{b.f, b.q, b.gain * k}
	}
	return out
}

func guitarSteel(c *ctx) *out {
	decay, bright, sym := c.v("decay"), c.v("brightness"), c.v("sympathetic")
	n := c.n(decay)
	x := twoPlane(c, c.f0, n, decay, bright, 0.12, 0.8)
	pick := noiseHit(c.rng, c.n(0.005), c.sr, func(x []float64) { hpBuf(x, 2500, c.sr, 1) },
		func(t float64) float64 { return math.Exp(-t / 0.0008) })
	addTo(x, pick, 0.15)
	// The open strings answer whatever the played string excites at their
	// own pitches, through the bridge.
	if sym > 0 {
		symp := make([]float64, n)
		for _, f := range []float64{82.41, 110, 146.83, 196, 246.94, 329.63} {
			s := newKS(c.sr, f, 4, 0.6, 0)
			for i := range symp {
				symp[i] += s.tick(0.01 * x[i])
			}
		}
		addTo(x, norm1(symp), 0.12*sym)
	}
	body(x, c.sr, 0.7, steelBody)
	eq(x, bqHighShelf(5000, 2, c.sr))
	return mono(x)
}

func guitarMuted(c *ctx) *out {
	mute, bright := c.v("mute"), c.v("brightness")
	f := c.f0
	decay := 0.6 - 0.45*mute
	k := newKS(c.sr, f, decay, 0.4-0.3*mute, 2500-1800*mute)
	x := runKS(k, pluck(c.rng, c.sr, f, 0.4+0.5*bright, 0.12), c.n(decay+0.3))
	norm1(x)
	body(x, c.sr, 0.7, steelBody)
	return mono(x)
}

func guitarPower(c *ctx) *out {
	p := c.plan(0.5, 1.6)
	drive, tone, oct := c.v("drive"), c.v("tone"), c.v("octave")
	n := p.frames()
	x := make([]float64, n)
	for _, s := range []struct{ f, amp, pick float64 }{
		{p.f0, 1, 0.15}, {p.f0 * 1.5, 0.8, 0.13}, {p.f0 * 2, 0.5 * oct, 0.17},
	} {
		y := runKS(newKS(c.sr, s.f, 8, 0.6, 0), pluck(c.rng, c.sr, s.f, 0.7, s.pick), n)
		norm1(y)
		flatten(y, c.n(0.25), c.sr, s.f)
		addTo(x, y, s.amp)
	}
	hpBuf(x, 80, c.sr, 1)
	norm1(x)
	g := 4 + 30*drive
	driveBuf(x, func(v float64) float64 { return math.Tanh(g * v) })
	eq(x, bqPeak(120, 1, 3, c.sr), bqPeak(1500, 0.8, 3, c.sr), bqPeak(3500, 2, -3, c.sr),
		bqLowpass(tone, 0.7, c.sr), bqLowpass(tone*1.15, 0.6, c.sr))
	return mono(x).looped(p).fade(0.05)
}

func harp(c *ctx) *out {
	decay, bright := c.v("decay"), c.v("brightness")
	x := twoPlane(c, c.f0, c.n(decay+0.2), decay, bright, 0.33, 0.35)
	body(x, c.sr, 0.8, []bodyMode{{180, 3, 0.4}, {420, 3, 0.3}, {900, 2, 0.2}})
	return mono(x)
}

func koto(c *ctx) *out {
	decay, bend := c.v("decay"), c.v("bend")
	f := c.f0
	n := c.n(decay + 0.2)
	k := newKS(c.sr, f, decay, 0.8, 0)
	exc := pluck(c.rng, c.sr, f, 0.9, 0.1)
	x := make([]float64, n)
	for i := range x {
		// Plucking hard pulls the string sharp; it settles as it rings.
		k.setFreq(f * math.Pow(2, bend*math.Exp(-c.t(i)/0.06)/12))
		var e float64
		if i < len(exc) {
			e = exc[i]
		}
		x[i] = k.tick(e)
	}
	norm1(x)
	body(x, c.sr, 0.7, []bodyMode{{250, 4, 0.4}, {600, 3, 0.3}, {1400, 2, 0.25}, {3000, 2, 0.15}})
	return mono(x)
}

func banjo(c *ctx) *out {
	decay, head := c.v("decay"), c.v("head")
	x := twoPlane(c, c.f0, c.n(decay+0.3), decay, 0.9, 0.06, 1)
	body(x, c.sr, 1-0.4*head, scaleBody([]bodyMode{{300, 3, 0.5}, {650, 3, 0.4}, {1500, 2, 0.35}, {2800, 2, 0.3}}, head))
	return mono(x)
}

func kalimba(c *ctx) *out {
	decay, bright := c.v("decay"), c.v("bright")
	f := c.f0
	n := c.n(decay + 0.2)
	x := make([]float64, n)
	addPartials(x, c.sr, []partial{
		{f: f, amp: 1, t60: decay},
		{f: f * 5.96, amp: 0.1 + 0.3*bright, t60: 0.35},
		{f: f * 16.5, amp: 0.03 + 0.08*bright, t60: 0.06},
	})
	norm1(x)
	click := strike(n, c.sr, 0.0007)
	hpBuf(click, 500, c.sr, 1)
	addTo(x, click, 0.2)
	body(x, c.sr, 1, []bodyMode{{240, 4, 0.5}, {900, 3, 0.2}})
	return mono(x)
}
