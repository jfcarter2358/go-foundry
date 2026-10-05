package tracker

// dsp.go — the signal-processing pieces the player is built from.
//
// Each is a small struct with its state inline and a per-sample method, sized
// at construction and never allocating afterwards: they run on the mixer
// goroutine, where a garbage-collector pause is a dropout.

import "math"

// hermite is 4-point, 3rd-order Hermite interpolation between y1 and y2.
func hermite(t, y0, y1, y2, y3 float32) float32 {
	c1 := 0.5 * (y2 - y0)
	c2 := y0 - 2.5*y1 + 2*y2 - 0.5*y3
	c3 := 0.5*(y3-y0) + 1.5*(y1-y2)
	return ((c3*t+c2)*t+c1)*t + y1
}

// svf is a topology-preserving-transform state-variable filter (Zavalishin),
// stable under fast cutoff modulation, which is what a filter sweep is.
type svf struct {
	a1, a2, a3, k float32
	ic1, ic2      [2]float32
	mode          FilterMode
}

// set recomputes the coefficients. Called at control rate, not per sample:
// the tan is the expensive part.
func (f *svf) set(mode FilterMode, cutoff, resonance float32, rate int) {
	f.mode = mode
	nyq := float32(rate) * 0.49
	cutoff = clamp32(cutoff, 16, nyq)
	g := float32(math.Tan(math.Pi * float64(cutoff) / float64(rate)))
	f.k = 2 - 1.95*clamp32(resonance, 0, 1)
	f.a1 = 1 / (1 + g*(g+f.k))
	f.a2 = g * f.a1
	f.a3 = g * f.a2
}

func (f *svf) reset() { f.ic1, f.ic2 = [2]float32{}, [2]float32{} }

// process filters one sample on channel ch (0 or 1).
func (f *svf) process(ch int, x float32) float32 {
	v3 := x - f.ic2[ch]
	v1 := f.a1*f.ic1[ch] + f.a2*v3
	v2 := f.ic2[ch] + f.a2*f.ic1[ch] + f.a3*v3
	f.ic1[ch] = 2*v1 - f.ic1[ch]
	f.ic2[ch] = 2*v2 - f.ic2[ch]
	if f.ic1[ch] > -denorm && f.ic1[ch] < denorm {
		f.ic1[ch] = 0
	}
	if f.ic2[ch] > -denorm && f.ic2[ch] < denorm {
		f.ic2[ch] = 0
	}
	switch f.mode {
	case FilterHighPass:
		return x - f.k*v1 - v2
	case FilterBandPass:
		return v1
	}
	return v2
}

const denorm = 1e-15

// ---------- reverb ----------

// Freeverb's tunings, in samples at 44.1 kHz.
var (
	combTuning    = [8]int{1116, 1188, 1277, 1356, 1422, 1491, 1557, 1617}
	allpassTuning = [4]int{556, 441, 341, 225}
)

const reverbSpread = 23

type comb struct {
	buf          []float32
	idx          int
	store        float32
	feedback     float32
	damp1, damp2 float32
}

func (c *comb) process(x float32) float32 {
	y := c.buf[c.idx]
	c.store = y*c.damp2 + c.store*c.damp1
	if c.store > -denorm && c.store < denorm {
		c.store = 0
	}
	c.buf[c.idx] = x + c.store*c.feedback
	c.idx++
	if c.idx >= len(c.buf) {
		c.idx = 0
	}
	return y
}

type allpass struct {
	buf []float32
	idx int
}

func (a *allpass) process(x float32) float32 {
	b := a.buf[a.idx]
	y := -x + b
	a.buf[a.idx] = x + b*0.5
	a.idx++
	if a.idx >= len(a.buf) {
		a.idx = 0
	}
	return y
}

// reverb is a stereo Freeverb.
type reverb struct {
	combs     [2][8]comb
	allpasses [2][4]allpass
	wet1      float32
	wet2      float32
}

func newReverb(rate int) *reverb {
	r := &reverb{}
	scale := float64(rate) / 44100
	for ch := 0; ch < 2; ch++ {
		spread := 0
		if ch == 1 {
			spread = reverbSpread
		}
		for i, t := range combTuning {
			r.combs[ch][i].buf = make([]float32, int(float64(t+spread)*scale)+1)
		}
		for i, t := range allpassTuning {
			r.allpasses[ch][i].buf = make([]float32, int(float64(t+spread)*scale)+1)
		}
	}
	r.set(ReverbSpec{Size: 0.6, Damping: 0.4, Width: 1})
	return r
}

func (r *reverb) set(s ReverbSpec) {
	room := 0.7 + 0.28*clamp32(s.Size, 0, 1)
	damp := 0.4 * clamp32(s.Damping, 0, 1)
	for ch := 0; ch < 2; ch++ {
		for i := range r.combs[ch] {
			c := &r.combs[ch][i]
			c.feedback, c.damp1, c.damp2 = room, damp, 1-damp
		}
	}
	w := clamp32(s.Width, 0, 1)
	r.wet1 = 3 * (w/2 + 0.5)
	r.wet2 = 3 * ((1 - w) / 2)
}

// process takes one stereo input frame and returns the wet output.
func (r *reverb) process(l, ri float32) (float32, float32) {
	in := (l + ri) * 0.015
	var ol, or float32
	for i := range r.combs[0] {
		ol += r.combs[0][i].process(in)
		or += r.combs[1][i].process(in)
	}
	for i := range r.allpasses[0] {
		ol = r.allpasses[0][i].process(ol)
		or = r.allpasses[1][i].process(or)
	}
	return ol*r.wet1 + or*r.wet2, or*r.wet1 + ol*r.wet2
}

// ---------- delay ----------

const maxDelaySeconds = 4

// delay is a stereo, tempo-synced echo with a low-passed feedback path.
type delay struct {
	buf      [2][]float32
	idx      int
	length   int // current delay in samples
	feedback float32
	toneA    float32
	lp       [2]float32
	pingPong bool
}

func newDelay(rate int) *delay {
	n := rate * maxDelaySeconds
	return &delay{buf: [2][]float32{make([]float32, n), make([]float32, n)}, length: rate / 2}
}

func (d *delay) set(s DelaySpec, bpm float32, rate int) {
	beats := s.Beats
	if beats <= 0 {
		beats = 0.75
	}
	if bpm <= 0 {
		bpm = 120
	}
	n := int(float64(beats) * 60 / float64(bpm) * float64(rate))
	d.length = clampInt(n, 1, len(d.buf[0])-1)
	d.feedback = clamp32(s.Feedback, 0, 0.95)
	// One-pole low-pass coefficient: tone 1 passes everything.
	d.toneA = 0.05 + 0.95*clamp32(s.Tone, 0, 1)
	d.pingPong = s.PingPong
}

func (d *delay) process(l, r float32) (float32, float32) {
	n := len(d.buf[0])
	read := d.idx - d.length
	if read < 0 {
		read += n
	}
	yl, yr := d.buf[0][read], d.buf[1][read]
	d.lp[0] += d.toneA * (yl - d.lp[0])
	d.lp[1] += d.toneA * (yr - d.lp[1])
	fl, fr := d.lp[0]*d.feedback, d.lp[1]*d.feedback
	if d.pingPong {
		// Mono in on the left; each repeat crosses sides.
		d.buf[0][d.idx] = (l+r)*0.5 + fr
		d.buf[1][d.idx] = fl
	} else {
		d.buf[0][d.idx] = l + fl
		d.buf[1][d.idx] = r + fr
	}
	d.idx++
	if d.idx >= n {
		d.idx = 0
	}
	return yl, yr
}

// ---------- compressor ----------

// compressor is a feed-forward peak compressor with a soft knee.
type compressor struct {
	env       float32 // dB
	attack    float32
	release   float32
	threshold float32
	ratio     float32
	makeup    float32 // linear
	on        bool
}

func (c *compressor) set(s CompressorSpec, rate int) {
	c.on = s.Enabled
	coef := func(sec float32) float32 {
		if sec <= 0 {
			return 0
		}
		return float32(math.Exp(-1 / (float64(sec) * float64(rate))))
	}
	c.attack, c.release = coef(s.Attack), coef(s.Release)
	c.threshold = s.Threshold
	c.ratio = s.Ratio
	if c.ratio < 1 {
		c.ratio = 1
	}
	c.makeup = float32(math.Pow(10, float64(s.Makeup)/20))
	if c.env == 0 {
		c.env = -120
	}
}

func (c *compressor) process(l, r float32) (float32, float32) {
	if !c.on {
		return l, r
	}
	peak := absf(l)
	if a := absf(r); a > peak {
		peak = a
	}
	lvl := float32(-120)
	if peak > 1e-6 {
		lvl = 20 * float32(math.Log10(float64(peak)))
	}
	if lvl > c.env {
		c.env = lvl + c.attack*(c.env-lvl)
	} else {
		c.env = lvl + c.release*(c.env-lvl)
	}
	over := c.env - c.threshold
	const knee = 6
	var gr float32
	switch {
	case over <= -knee/2:
	case over >= knee/2:
		gr = over * (1 - 1/c.ratio)
	default:
		x := over + knee/2
		gr = (1 - 1/c.ratio) * x * x / (2 * knee)
	}
	g := float32(math.Pow(10, float64(-gr)/20)) * c.makeup
	return l * g, r * g
}

func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// equalPower returns left/right gains for a pan in -1..1.
func equalPower(pan float32) (float32, float32) {
	pan = clamp32(pan, -1, 1)
	a := float64(pan+1) * math.Pi / 4
	return float32(math.Cos(a)), float32(math.Sin(a))
}

// semitoneRatio is 2^(s/12).
func semitoneRatio(s float32) float64 { return math.Exp2(float64(s) / 12) }
