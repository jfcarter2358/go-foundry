package fx

// modulation.go — the chorus and the phaser: effects whose sound is a
// slow LFO moving something — a short delay's length, an allpass chain's
// break frequency — and the moving copy mixed back with the original.

import "math"

// ---------- chorus ----------

const chorusBufSeconds = 0.064

type chorus struct {
	base
	buf   [2][]float32
	idx   int
	phase float64
	fb    [2]float32
	dry   [2 * ChunkFrames]float32
}

func newChorus(rate int) *chorus {
	n := int(float64(rate)*chorusBufSeconds) + 4
	return &chorus{base: newBase(KindChorus, rate), buf: [2][]float32{make([]float32, n), make([]float32, n)}}
}

const (
	chRate = iota
	chDepth
	chDelay
	chFeedback
	chSpread
	chMix
)

func (c *chorus) Reset() {
	clear(c.buf[0])
	clear(c.buf[1])
	c.fb = [2]float32{}
}

func (c *chorus) Process(buf []float32) { runChunks(c, buf) }

// read is the line d samples back (fractional, at least 1).
func (c *chorus) read(ch int, d float64) float32 {
	b := c.buf[ch]
	n := len(b)
	rp := float64(c.idx) - d
	for rp < 0 {
		rp += float64(n)
	}
	i := int(rp)
	t := float32(rp - float64(i))
	j := i + 1
	if j >= n {
		j = 0
	}
	if i >= n {
		i -= n
	}
	return b[i] + (b[j]-b[i])*t
}

func (c *chorus) chunk(buf []float32) {
	n := len(buf) / 2
	c.advance(n)
	copy(c.dry[:len(buf)], buf)
	ms := float64(c.rate) / 1000
	dly := newRamp(c.p[chDelay].prev, c.val(chDelay), n)
	dep := newRamp(c.p[chDepth].prev, c.val(chDepth), n)
	inc := float64(c.val(chRate)) / float64(c.rate)
	spread := float64(c.val(chSpread)) * 0.25
	fbk := c.val(chFeedback)
	maxD := float64(len(c.buf[0]) - 3)
	// The LFO is a phasor turned by a rotation each frame, set from the
	// phase at the chunk's start so it never drifts: sin and cos of the
	// left voice, and the right's from them by the spread's angle. The
	// second voice of each side is half a cycle on, the negative.
	w := 2 * math.Pi * inc
	rs, rc := math.Sin(w), math.Cos(w)
	sn, cs := math.Sincos(2 * math.Pi * c.phase)
	os, oc := math.Sincos(2 * math.Pi * spread)
	for f := 0; f < n; f++ {
		base, depth := float64(dly.next())*ms, float64(dep.next())*ms
		half := depth * 0.5
		lfo := [2]float64{sn, sn*oc + cs*os}
		var out [2]float32
		for ch := 0; ch < 2; ch++ {
			d1 := math.Max(1, math.Min(base+half*(1+lfo[ch]), maxD))
			d2 := math.Max(1, math.Min(base+half*(1-lfo[ch]), maxD))
			out[ch] = (c.read(ch, d1) + c.read(ch, d2)) * 0.5
		}
		c.buf[0][c.idx] = flush(buf[f*2] + c.fb[0]*fbk)
		c.buf[1][c.idx] = flush(buf[f*2+1] + c.fb[1]*fbk)
		c.fb = out
		c.idx++
		if c.idx >= len(c.buf[0]) {
			c.idx = 0
		}
		sn, cs = sn*rc+cs*rs, cs*rc-sn*rs
		buf[f*2], buf[f*2+1] = out[0], out[1]
	}
	c.phase += inc * float64(n)
	c.phase -= math.Floor(c.phase)
	blend(buf, c.dry[:len(buf)], c.p[chMix].prev, c.val(chMix))
}

// ---------- phaser ----------

const maxPhaserStages = 12

// phaserStep is how often, in frames, the sweep moves the allpasses.
const phaserStep = 16

type phaser struct {
	base
	s     [2][maxPhaserStages]float32
	a     [2]float32
	last  [2]float32
	phase float64
	dry   [2 * ChunkFrames]float32
}

func newPhaser(rate int) *phaser { return &phaser{base: newBase(KindPhaser, rate)} }

const (
	phRate = iota
	phDepth
	phFreq
	phFeedback
	phStages
	phSpread
	phMix
)

func (p *phaser) Reset() {
	p.s = [2][maxPhaserStages]float32{}
	p.last = [2]float32{}
}

func (p *phaser) Process(buf []float32) { runChunks(p, buf) }

// coef is the first-order allpass coefficient for a break frequency.
func (p *phaser) coef(f float64) float32 {
	f = math.Max(20, math.Min(f, 0.45*float64(p.rate)))
	t := math.Tan(math.Pi * f / float64(p.rate))
	return float32((t - 1) / (t + 1))
}

func (p *phaser) chunk(buf []float32) {
	n := len(buf) / 2
	p.advance(n)
	copy(p.dry[:len(buf)], buf)
	inc := float64(p.val(phRate)) / float64(p.rate)
	depth := float64(p.val(phDepth)) * 2 // octaves either side
	centre := float64(p.val(phFreq))
	fb := p.val(phFeedback)
	stages := clampInt(int(p.val(phStages)), 1, maxPhaserStages)
	spread := float64(p.val(phSpread)) * 0.5
	for f := 0; f < n; f++ {
		if f%phaserStep == 0 {
			for ch := 0; ch < 2; ch++ {
				ph := p.phase
				if ch == 1 {
					ph += spread
				}
				p.a[ch] = p.coef(centre * math.Exp2(depth*math.Sin(2*math.Pi*ph)))
			}
		}
		for ch := 0; ch < 2; ch++ {
			x := buf[f*2+ch] + p.last[ch]*fb
			a := p.a[ch]
			st := &p.s[ch]
			for k := 0; k < stages; k++ {
				v := x - a*st[k]
				x = a*v + st[k]
				st[k] = flush(v)
			}
			p.last[ch] = flush(x)
			buf[f*2+ch] = x
		}
		p.phase += inc
		if p.phase >= 1 {
			p.phase--
		}
	}
	blend(buf, p.dry[:len(buf)], p.p[phMix].prev, p.val(phMix))
}
