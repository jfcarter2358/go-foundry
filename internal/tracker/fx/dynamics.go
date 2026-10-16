package fx

// dynamics.go — the compressor and the limiter. The compressor is the
// Tracker's master compressor (kernels.go); the limiter is a look-ahead
// brick wall that no sample gets past.

import "math"

// ---------- compressor ----------

const (
	cpThreshold = iota
	cpRatio
	cpAttack
	cpRelease
	cpMakeup
	cpEnabled
)

type compressor struct {
	base
	c Comp
}

func newCompressor(rate int) *compressor { return &compressor{base: newBase(KindCompressor, rate)} }

func (c *compressor) Reset() { c.c.Reset() }

func (c *compressor) Process(buf []float32) { runChunks(c, buf) }

func (c *compressor) chunk(buf []float32) {
	c.advance(len(buf) / 2)
	if c.dirty {
		c.dirty = false
		c.c.Set(c.val(cpEnabled) >= 0.5, c.val(cpThreshold), c.val(cpRatio), c.val(cpAttack), c.val(cpRelease), c.val(cpMakeup), c.rate)
	}
	for f := 0; f+1 < len(buf); f += 2 {
		buf[f], buf[f+1] = c.c.Process(buf[f], buf[f+1])
	}
}

// GainReduction is the compressor's present gain reduction in dB.
func (c *compressor) GainReduction() float32 { return c.c.GainReduction() }

// ---------- limiter ----------

const (
	lmCeiling = iota
	lmGain
	lmRelease
)

// limiterLookahead is how far ahead the limiter looks, in seconds: the
// time its gain takes to come down before a peak, and the delay it adds.
const limiterLookahead = 0.0015

// limiter is a look-ahead peak limiter. Each frame asks for the gain that
// would bring it to the ceiling; the gain applied is the smallest asked
// for over the look-ahead window (a running minimum), averaged over the
// window (so it comes down in a straight line rather than a step), and
// released slowly. Every value averaged is at most what the frame leaving
// the delay line asked for, so the output never passes the ceiling.
type limiter struct {
	base
	look int // window, frames
	// The audio delay line, look frames long.
	dl  []float32
	pos int
	// The running minimum: a monotonic queue of (gain asked, frame).
	qv   []float32
	qi   []int64
	head int
	size int
	t    int64
	// The window average of the minima.
	box    []float32
	boxPos int
	sum    float64
	gain   float32 // released gain, applied
	relK   float32
}

func newLimiter(rate int) *limiter {
	look := max(int(math.Round(limiterLookahead*float64(rate))), 1)
	l := &limiter{base: newBase(KindLimiter, rate), look: look,
		dl: make([]float32, 2*look), qv: make([]float32, look+2), qi: make([]int64, look+2), box: make([]float32, look)}
	l.Reset()
	return l
}

// Latency is the frames the limiter delays its input by.
func (l *limiter) Latency() int { return l.look }

func (l *limiter) Reset() {
	clear(l.dl)
	l.head, l.size, l.t, l.pos = 0, 0, 0, 0
	for i := range l.box {
		l.box[i] = 1
	}
	l.boxPos, l.sum, l.gain = 0, float64(l.look), 1
}

func (l *limiter) Process(buf []float32) { runChunks(l, buf) }

// push adds a gain asked for at frame t to the running minimum.
func (l *limiter) push(g float32) {
	c := len(l.qv)
	for l.size > 0 {
		back := (l.head + l.size - 1) % c
		if l.qv[back] < g {
			break
		}
		l.size--
	}
	at := (l.head + l.size) % c
	l.qv[at], l.qi[at] = g, l.t
	l.size++
	// Forget minima older than the window (look+1 frames: the frame
	// leaving the delay line and the look after it).
	for l.size > 0 && l.qi[l.head] <= l.t-int64(l.look)-1 {
		l.head = (l.head + 1) % c
		l.size--
	}
}

func (l *limiter) chunk(buf []float32) {
	n := len(buf) / 2
	l.advance(n)
	ceil := dbGain(l.val(lmCeiling))
	if l.dirty {
		l.dirty = false
		l.relK = float32(1 - math.Exp(-1/(float64(l.val(lmRelease))*float64(l.rate))))
	}
	in := newRamp(dbGain(l.p[lmGain].prev), dbGain(l.val(lmGain)), n)
	for f := 0; f < n; f++ {
		g := in.next()
		xl, xr := buf[f*2]*g, buf[f*2+1]*g
		peak := max(absf(xl), absf(xr))
		ask := float32(1)
		if peak > ceil {
			ask = ceil / peak
		}
		l.push(ask)
		l.t++
		hold := l.qv[l.head]
		// The window's average of the minima.
		l.sum += float64(hold) - float64(l.box[l.boxPos])
		l.box[l.boxPos] = hold
		l.boxPos++
		if l.boxPos == l.look {
			l.boxPos = 0
			// Re-add from scratch once a window so rounding never builds.
			s := 0.0
			for _, v := range l.box {
				s += float64(v)
			}
			l.sum = s
		}
		target := min(float32(l.sum/float64(l.look)), 1)
		if target < l.gain {
			l.gain = target
		} else {
			l.gain += (target - l.gain) * l.relK
		}
		// The delay line: the frame look frames ago leaves now.
		ol, or := l.dl[l.pos*2], l.dl[l.pos*2+1]
		l.dl[l.pos*2], l.dl[l.pos*2+1] = xl, xr
		l.pos++
		if l.pos == l.look {
			l.pos = 0
		}
		gl := l.gain
		buf[f*2], buf[f*2+1] = ol*gl, or*gl
	}
}

// ---------- filter ----------

const (
	ftMode = iota
	ftCutoff
	ftResonance
	ftSlope
	ftMix
)

type filter struct {
	base
	s   [2]SVF
	dry [2 * ChunkFrames]float32
}

func newFilter(rate int) *filter { return &filter{base: newBase(KindFilter, rate)} }

func (f *filter) Reset() { f.s[0].Reset(); f.s[1].Reset() }

func (f *filter) Process(buf []float32) { runChunks(f, buf) }

func (f *filter) chunk(buf []float32) {
	n := len(buf) / 2
	f.advance(n)
	if f.dirty {
		f.dirty = false
		mode := SVFMode(clampInt(int(f.val(ftMode)), 0, 2) + 1)
		// Two stages for 24 dB; a stage's resonance is shared so the
		// peak stays where one stage puts it.
		for k := range f.s {
			f.s[k].Set(mode, f.val(ftCutoff), f.val(ftResonance), f.rate)
		}
	}
	m0, m1 := f.p[ftMix].prev, f.val(ftMix)
	if !(m0 == 1 && m1 == 1) {
		copy(f.dry[:len(buf)], buf)
	}
	two := f.val(ftSlope) >= 0.5
	for i := 0; i < n; i++ {
		l, r := f.s[0].Process(0, buf[i*2]), f.s[0].Process(1, buf[i*2+1])
		if two {
			l, r = f.s[1].Process(0, l), f.s[1].Process(1, r)
		}
		buf[i*2], buf[i*2+1] = l, r
	}
	blend(buf, f.dry[:len(buf)], m0, m1)
}
