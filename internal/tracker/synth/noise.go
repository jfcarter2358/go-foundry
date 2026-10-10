package synth

// noise.go — the library's only source of randomness.
//
// Every recipe owns an rng seeded from its name. Nothing reads math/rand's
// global source or the clock, so a regenerated library is byte-identical to
// the one in the repo and a diff of resources/ means a recipe changed.

// rng is xorshift64*: tiny, fast, and statistically plenty for audio.
type rng struct{ s uint64 }

func newRNG(seed uint64) *rng {
	if seed == 0 {
		seed = 0x9E3779B97F4A7C15
	}
	r := &rng{s: seed}
	for i := 0; i < 8; i++ { // stir: nearby seeds must not start alike
		r.next()
	}
	return r
}

func (r *rng) next() uint64 {
	x := r.s
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	r.s = x
	return x * 0x2545F4914F6CDD1D
}

// float is uniform in [0, 1).
func (r *rng) float() float64 { return float64(r.next()>>11) / (1 << 53) }

// white is uniform in [-1, 1).
func (r *rng) white() float64 { return 2*r.float() - 1 }

// rangef is uniform in [lo, hi).
func (r *rng) rangef(lo, hi float64) float64 { return lo + (hi-lo)*r.float() }

// fork is an independent generator for a sub-part (one channel, one voice)
// whose draws must not shift when another part's count changes.
func (r *rng) fork() *rng { return newRNG(r.next()) }

// noiseBuf is n frames of white noise.
func (r *rng) noiseBuf(n int) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = r.white()
	}
	return x
}

// pink is Paul Kellet's refined pink filter: -3 dB/octave within half a dB
// across the audio band, which is what makes brushes and air sound soft
// rather than hissy.
type pink struct{ b0, b1, b2, b3, b4, b5, b6 float64 }

func (p *pink) next(w float64) float64 {
	p.b0 = 0.99886*p.b0 + w*0.0555179
	p.b1 = 0.99332*p.b1 + w*0.0750759
	p.b2 = 0.96900*p.b2 + w*0.1538520
	p.b3 = 0.86650*p.b3 + w*0.3104856
	p.b4 = 0.55000*p.b4 + w*0.5329522
	p.b5 = -0.7616*p.b5 - w*0.0168980
	out := p.b0 + p.b1 + p.b2 + p.b3 + p.b4 + p.b5 + p.b6 + w*0.5362
	p.b6 = w * 0.115926
	return out * 0.11
}

func (r *rng) pinkBuf(n int) []float64 {
	var p pink
	x := make([]float64, n)
	for i := range x {
		x[i] = p.next(r.white())
	}
	return x
}

// lfsr is the NES noise channel: a 15-bit shift register clocked at a rate,
// whose feedback tap picks a 32767-step hiss (long) or a 93-step metallic
// buzz (short). Each output change is band-limited like an oscillator edge.
type lfsr struct {
	reg   uint16
	short bool
	ph    float64
	val   float64
	b     bl
}

func newLFSR(short bool) *lfsr { return &lfsr{reg: 1, short: short, val: -1} }

func (l *lfsr) shift() {
	tap := uint16(1)
	if l.short {
		tap = 6
	}
	bit := (l.reg ^ (l.reg >> tap)) & 1
	l.reg = l.reg>>1 | bit<<14
}

// next advances by dt shifts per frame.
func (l *lfsr) next(dt float64) float64 {
	l.ph += dt
	var corr float64
	for l.ph >= 1 {
		l.ph--
		old := l.val
		l.shift()
		l.val = 1
		if l.reg&1 != 0 {
			l.val = -1
		}
		if l.val != old {
			l.b.step(l.ph/dt, l.val-old, &corr)
		}
	}
	return l.b.out(l.val + corr)
}

// nesNoiseRate is the NES (NTSC) noise channel's shift rate for each of its
// sixteen period settings.
func nesNoiseRate(idx int) float64 {
	periods := [16]float64{4, 8, 16, 32, 64, 96, 128, 160, 202, 254, 380, 508, 762, 1016, 2034, 4068}
	if idx < 0 {
		idx = 0
	} else if idx > 15 {
		idx = 15
	}
	return 1789773 / periods[idx]
}
