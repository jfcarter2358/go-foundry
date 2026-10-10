package synth

// bowed.go — a bowed string as a digital waveguide (after STK's Bowed,
// itself after McIntyre, Schumacher and Woodhouse).
//
// A sawtooth through a filter gives you a string *pad*; it never gives you
// the scratch and grain of a bow, because those come from stick-slip
// friction: the bow drags the string until the string's tension snaps it
// back, once a period, a little differently every time. The model is the
// string as two delay lines either side of the bow point, a friction curve
// that decides how much bow velocity gets into the string, and a lossy
// reflection at the bridge. The irregularity that makes it sound real is
// also what makes it drift a few cents from the nominal length, so
// bowedVoice measures what it played and resamples it into tune.

import "math"

// fracDelay is a delay line read at a fractional length (linear).
type fracDelay struct {
	buf []float64
	w   int
	d   float64
}

func newFracDelay(max int) *fracDelay { return &fracDelay{buf: make([]float64, max)} }

func (f *fracDelay) out() float64 {
	n := len(f.buf)
	p := float64(f.w) - f.d
	i := int(math.Floor(p))
	t := p - float64(i)
	a := f.buf[((i%n)+n)%n]
	b := f.buf[(((i+1)%n)+n)%n]
	return a + (b-a)*t
}

func (f *fracDelay) push(v float64) {
	f.buf[f.w%len(f.buf)] = v
	f.w++
}

type bowed struct {
	neck, bridge *fracDelay
	sfY, sfP     float64
	slope        float64
	sr           float64
}

// newBowed is a string at f Hz under bow pressure 0..1.
func newBowed(sr, f, pressure float64) *bowed {
	size := int(sr/20) + 8
	b := &bowed{neck: newFracDelay(size), bridge: newFracDelay(size), sr: sr}
	b.sfP = 0.75 - 0.2*22050/sr
	b.slope = 5 - 4*clamp(pressure, 0, 1)
	b.setFreq(f)
	return b
}

const bowBeta = 0.127236 // bow position, as a fraction from the bridge

func (b *bowed) setFreq(f float64) {
	base := b.sr/f - 4
	b.bridge.d = math.Max(base*bowBeta, 1)
	b.neck.d = math.Max(base*(1-bowBeta), 1)
}

// tick advances with the bow moving at vel and returns the bridge signal.
func (b *bowed) tick(vel float64) float64 {
	bOut := b.bridge.out()
	b.sfY = 0.95*(1-b.sfP)*bOut + b.sfP*b.sfY
	bridgeRefl := -b.sfY
	nutRefl := -b.neck.out()
	dv := vel - (bridgeRefl + nutRefl)
	// The friction curve: sticking (all of it) at small slip, slipping
	// (little) at large.
	k := math.Pow(math.Abs(dv*b.slope)+0.75, -4)
	if k > 1 {
		k = 1
	}
	nv := dv * k
	b.neck.push(bridgeRefl + nv)
	b.bridge.push(nutRefl + nv)
	return bOut
}

// bowedVoice renders n frames of a bowed note at f, swelling in over attack
// seconds, with vibrato (cents, Hz), retuned to f by measurement.
func bowedVoice(c *ctx, r *rng, f float64, n int, pressure, attack, vibCents, vibRate float64) []float64 {
	b := newBowed(c.sr, f, pressure)
	var grain onePole
	grain.set(30, c.sr)
	vibPh := r.float()
	extra := n/20 + 64 // resampling may shorten the take a little
	x := render(n+extra, func(i int) float64 {
		t := c.t(i)
		b.setFreq(f * math.Pow(2, vibCents*math.Sin(2*math.Pi*(vibRate*t+vibPh))*smooth(t/0.5)/1200))
		// Bow speed wavers with the arm: a slow random drift on top of
		// the swell.
		vel := 0.19 * rise(t, attack) * (1 + 0.6*grain.lp(r.white()))
		return b.tick(vel)
	})
	hpBuf(x, 40, c.sr, 1)
	from := c.n(attack + 0.1)
	if from+c.n(0.6) < len(x) {
		got := measurePitch(x[from:], c.sr, f)
		x = resample(x, f/got)
	}
	if len(x) > n {
		x = x[:n]
	}
	return x
}
