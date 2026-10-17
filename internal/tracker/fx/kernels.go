package fx

// kernels.go — the signal-processing cores the Tracker was built from, and
// the effects here are built on.
//
// These are the Tracker's own filter, reverb, echo, compressor, drive and
// bit crusher, moved here unchanged so the Score's effects and the Tracker
// share one implementation: a Tracker song renders bit-identically through
// them (internal/tracker's golden tests). The Score's effects add what a
// mixer needs on top — parameter smoothing, wet/dry mix, a delay that
// glides when the tempo moves — without changing a kernel's arithmetic for
// the Tracker's calls.
//
// Each kernel keeps its state inline and is sized when it is made; nothing
// here allocates after that.

import "math"

// Denorm is the magnitude below which feedback state is flushed to zero.
// A float32 that decays into the subnormal range is up to a hundred times
// slower to compute with on x86, and a silent reverb tail would otherwise
// spend seconds there.
const Denorm = 1e-15

// flush zeroes a value too small to hear and dangerously close to the
// subnormal range.
func flush(v float32) float32 {
	if v > -Denorm && v < Denorm {
		return 0
	}
	return v
}

func flush64(v float64) float64 {
	if v > -1e-30 && v < 1e-30 {
		return 0
	}
	return v
}

// ---------- state-variable filter ----------

// SVFMode is a state-variable filter's output. The numbers are the
// Tracker's FilterMode values, so an instrument's mode converts directly.
type SVFMode uint8

const (
	SVFLowPass  SVFMode = 1
	SVFHighPass SVFMode = 2
	SVFBandPass SVFMode = 3
)

// SVF is a topology-preserving-transform state-variable filter (Zavalishin),
// stable under fast cutoff modulation, which is what a filter sweep is. Two
// channels of state.
type SVF struct {
	a1, a2, a3, k float32
	ic1, ic2      [2]float32
	mode          SVFMode
}

// Set recomputes the coefficients. Call it at control rate, not per sample:
// the tan is the expensive part. Resonance 0..1 runs from a gentle slope
// (Q 0.5) to near self-oscillation.
func (f *SVF) Set(mode SVFMode, cutoff, resonance float32, rate int) {
	f.mode = mode
	nyq := float32(rate) * 0.49
	cutoff = clamp32(cutoff, 16, nyq)
	g := float32(math.Tan(math.Pi * float64(cutoff) / float64(rate)))
	f.k = 2 - 1.95*clamp32(resonance, 0, 1)
	f.a1 = 1 / (1 + g*(g+f.k))
	f.a2 = g * f.a1
	f.a3 = g * f.a2
}

// Reset clears the filter's state.
func (f *SVF) Reset() { f.ic1, f.ic2 = [2]float32{}, [2]float32{} }

// Process filters one sample on channel ch (0 or 1).
func (f *SVF) Process(ch int, x float32) float32 {
	v3 := x - f.ic2[ch]
	v1 := f.a1*f.ic1[ch] + f.a2*v3
	v2 := f.ic2[ch] + f.a2*f.ic1[ch] + f.a3*v3
	f.ic1[ch] = 2*v1 - f.ic1[ch]
	f.ic2[ch] = 2*v2 - f.ic2[ch]
	if f.ic1[ch] > -Denorm && f.ic1[ch] < Denorm {
		f.ic1[ch] = 0
	}
	if f.ic2[ch] > -Denorm && f.ic2[ch] < Denorm {
		f.ic2[ch] = 0
	}
	switch f.mode {
	case SVFHighPass:
		return x - f.k*v1 - v2
	case SVFBandPass:
		return v1
	}
	return v2
}

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
	if c.store > -Denorm && c.store < Denorm {
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

// processFlush is process with the stored value flushed when it is
// inaudible: a silent tail halves in the line each pass and would run on
// subnormals for a while before reaching zero.
func (a *allpass) processFlush(x float32) float32 {
	b := a.buf[a.idx]
	y := -x + b
	a.buf[a.idx] = flush(x + b*0.5)
	a.idx++
	if a.idx >= len(a.buf) {
		a.idx = 0
	}
	return y
}

// Freeverb is a stereo Freeverb: eight damped combs and four allpasses a
// side, the right side's lines a little longer for width. It returns only
// the wet signal.
type Freeverb struct {
	combs     [2][8]comb
	allpasses [2][4]allpass
	wet1      float32
	wet2      float32
	// Flush keeps the allpasses off subnormal numbers as a tail dies. The
	// Score's reverb sets it; the Tracker's (golden) does not.
	Flush bool
}

// NewFreeverb sizes a reverb's lines for a sample rate. Allocates.
func NewFreeverb(rate int) *Freeverb {
	r := &Freeverb{}
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
	r.Set(0.6, 0.4, 1)
	return r
}

// Set takes the room size, the high-frequency damping and the stereo
// width, each 0..1.
func (r *Freeverb) Set(size, damping, width float32) {
	room := 0.7 + 0.28*clamp32(size, 0, 1)
	damp := 0.4 * clamp32(damping, 0, 1)
	for ch := 0; ch < 2; ch++ {
		for i := range r.combs[ch] {
			c := &r.combs[ch][i]
			c.feedback, c.damp1, c.damp2 = room, damp, 1-damp
		}
	}
	w := clamp32(width, 0, 1)
	r.wet1 = 3 * (w/2 + 0.5)
	r.wet2 = 3 * ((1 - w) / 2)
}

// Reset empties the reverb's lines.
func (r *Freeverb) Reset() {
	for ch := 0; ch < 2; ch++ {
		for i := range r.combs[ch] {
			clear(r.combs[ch][i].buf)
			r.combs[ch][i].store = 0
		}
		for i := range r.allpasses[ch] {
			clear(r.allpasses[ch][i].buf)
		}
	}
}

// Process takes one stereo input frame and returns the wet output.
func (r *Freeverb) Process(l, ri float32) (float32, float32) {
	in := (l + ri) * 0.015
	var ol, or float32
	for i := range r.combs[0] {
		ol += r.combs[0][i].process(in)
		or += r.combs[1][i].process(in)
	}
	if r.Flush {
		for i := range r.allpasses[0] {
			ol = r.allpasses[0][i].processFlush(ol)
			or = r.allpasses[1][i].processFlush(or)
		}
	} else {
		for i := range r.allpasses[0] {
			ol = r.allpasses[0][i].process(ol)
			or = r.allpasses[1][i].process(or)
		}
	}
	return ol*r.wet1 + or*r.wet2, or*r.wet1 + ol*r.wet2
}

// ---------- echo ----------

// MaxEchoSeconds is the longest an echo's line holds.
const MaxEchoSeconds = 4

// Echo is a stereo echo with a low-passed feedback path and an optional
// ping-pong. Its length either jumps (SetLength — the Tracker's delay, which
// moves the instant the tempo does) or glides (GlideTo — the Score's, which
// slews the read point so a tempo change bends the repeats instead of
// clicking). It returns only the wet signal.
type Echo struct {
	buf      [2][]float32
	idx      int
	length   int // current delay in samples, while not gliding
	feedback float32
	toneA    float32
	lp       [2]float32
	pingPong bool

	// The glide: pos is the read distance in samples, fractional while it
	// moves toward target at slew samples per sample.
	gliding     bool
	pos, target float64
	slew        float64
	everGlided  bool
	// Flush zeroes the feedback path's state once it is inaudible, so a
	// dying echo never runs on subnormal numbers. The Score's delay sets
	// it; the Tracker's (whose renders are golden) does not.
	Flush bool
}

// NewEcho sizes an echo line for a sample rate. Allocates.
func NewEcho(rate int) *Echo {
	n := rate * MaxEchoSeconds
	return &Echo{buf: [2][]float32{make([]float32, n), make([]float32, n)}, length: rate / 2}
}

// MaxLength is the longest delay the line holds, in samples.
func (d *Echo) MaxLength() int { return len(d.buf[0]) - 1 }

// SetLength sets the delay at once, in samples (clamped to the line).
func (d *Echo) SetLength(n int) {
	d.length = clampInt(n, 1, len(d.buf[0])-1)
	d.gliding, d.pos, d.target = false, float64(d.length), float64(d.length)
}

// GlideTo moves the delay toward n samples over about secs seconds (0 or a
// first call: at once). A glide reads between samples until it lands.
func (d *Echo) GlideTo(n float64, secs float64, rate int) {
	n = math.Max(1, math.Min(n, float64(len(d.buf[0])-2)))
	if !d.everGlided || secs <= 0 {
		d.everGlided = true
		d.target, d.pos = n, n
		d.length = int(n)
		d.gliding = n != math.Floor(n)
		return
	}
	if n == d.target && !d.gliding && float64(d.length) == n {
		return
	}
	d.target = n
	d.slew = math.Abs(n-d.pos) / math.Max(secs*float64(rate), 1)
	d.gliding = true
}

// SetVoice sets the feedback (0..0.95), the tone of the repeats (0..1, 1
// passes everything) and ping-pong.
func (d *Echo) SetVoice(feedback, tone float32, pingPong bool) {
	d.feedback = clamp32(feedback, 0, 0.95)
	// One-pole low-pass coefficient: tone 1 passes everything.
	d.toneA = 0.05 + 0.95*clamp32(tone, 0, 1)
	d.pingPong = pingPong
}

// Reset empties the line.
func (d *Echo) Reset() {
	clear(d.buf[0])
	clear(d.buf[1])
	d.lp = [2]float32{}
}

// Process takes one stereo frame and returns the echo.
func (d *Echo) Process(l, r float32) (float32, float32) {
	n := len(d.buf[0])
	var yl, yr float32
	if d.gliding {
		if d.pos != d.target {
			if math.Abs(d.target-d.pos) <= d.slew {
				d.pos = d.target
			} else if d.target > d.pos {
				d.pos += d.slew
			} else {
				d.pos -= d.slew
			}
		}
		ip := math.Floor(d.pos)
		t := float32(d.pos - ip)
		read := d.idx - int(ip)
		if read < 0 {
			read += n
		}
		prev := read - 1
		if prev < 0 {
			prev += n
		}
		// pos samples back lies between read (ip back) and prev (ip+1).
		yl = d.buf[0][read] + (d.buf[0][prev]-d.buf[0][read])*t
		yr = d.buf[1][read] + (d.buf[1][prev]-d.buf[1][read])*t
		if d.pos == d.target && d.pos == ip {
			d.length, d.gliding = int(ip), false
		}
	} else {
		read := d.idx - d.length
		if read < 0 {
			read += n
		}
		yl, yr = d.buf[0][read], d.buf[1][read]
	}
	d.lp[0] += d.toneA * (yl - d.lp[0])
	d.lp[1] += d.toneA * (yr - d.lp[1])
	if d.Flush {
		d.lp[0], d.lp[1] = flush(d.lp[0]), flush(d.lp[1])
	}
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

// Comp is a feed-forward peak compressor with a 6 dB soft knee, linked
// across the two channels.
type Comp struct {
	env       float32 // dB
	attack    float32
	release   float32
	threshold float32
	ratio     float32
	makeup    float32 // linear
	on        bool
}

// Set configures it: threshold in dBFS, ratio (1 and up), attack and
// release in seconds, makeup gain in dB. Off passes the signal unchanged.
func (c *Comp) Set(on bool, threshold, ratio, attack, release, makeup float32, rate int) {
	c.on = on
	coef := func(sec float32) float32 {
		if sec <= 0 {
			return 0
		}
		return float32(math.Exp(-1 / (float64(sec) * float64(rate))))
	}
	c.attack, c.release = coef(attack), coef(release)
	c.threshold = threshold
	c.ratio = ratio
	if c.ratio < 1 {
		c.ratio = 1
	}
	c.makeup = float32(math.Pow(10, float64(makeup)/20))
	if c.env == 0 {
		c.env = -120
	}
}

// Reset forgets the envelope.
func (c *Comp) Reset() { c.env = -120 }

// Process compresses one stereo frame.
func (c *Comp) Process(l, r float32) (float32, float32) {
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

// GainReduction is how far below unity the last frame was turned, in dB
// (makeup aside): for a meter.
func (c *Comp) GainReduction() float32 {
	over := c.env - c.threshold
	if !c.on || over <= -3 {
		return 0
	}
	if over >= 3 {
		return over * (1 - 1/c.ratio)
	}
	x := over + 3
	return (1 - 1/c.ratio) * x * x / 12
}

// ---------- drive and crush ----------

// DriveK is the input gain a drive amount (0..1) saturates at.
//
// The drive trap (the Tracker learned it): saturation must keep unity
// gain for small signals. tanh(x·k)/k does — quiet passages pass as they
// are and only peaks above about 1/k are squashed — where a curve
// normalised to its peak, tanh(x·k)/tanh(k), turns everything quiet up by
// k and makes "a little drive" a level jump.
func DriveK(amount float32) float32 { return 1 + 9*amount }

// Drive saturates one sample at input gain k (DriveK), with norm = 1/k.
func Drive(x, k, norm float32) float32 {
	return float32(math.Tanh(float64(x*k))) * norm
}

// CrushSteps is the quantiser's steps either side of zero for a bit depth.
func CrushSteps(bits int) float32 { return float32(int(1) << max(bits-1, 0)) }

// Crush rounds a sample to steps levels a side (CrushSteps).
func Crush(x, steps float32) float32 {
	return float32(math.Round(float64(x*steps))) / steps
}

// ---------- helpers ----------

func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func clamp32(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
