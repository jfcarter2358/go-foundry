package synth

// osc.go — oscillators that do not alias.
//
// A naive sawtooth's jump puts energy far above Nyquist that folds back as
// inharmonic whine, loudest exactly where leads live. PolyBLEP replaces each
// jump with a two-frame polynomial step, which removes most of it for the
// price of a multiply. Every discontinuous waveform here — saw, pulse, hard
// sync, the chip machines' stepped tables and noise — goes through one
// corrector, bl, so a step anywhere inside a frame is band-limited the same
// way. bl runs one frame late so it can correct the frame before a step too.
//
// Oscillators take dt (frequency / sample rate) per call, so any of them can
// be swept, modulated or synced without a separate API.

import "math"

// bl band-limits steps with a two-point polyBLEP, emitting one frame late.
type bl struct{ held float64 }

// step registers a jump of height h that happened t frames (0 <= t < 1)
// before the current frame, whose value cur already includes the jump.
func (b *bl) step(t, h float64, cur *float64) {
	if t < 0 {
		t = 0
	} else if t >= 1 {
		t = 0.999999
	}
	*cur -= h / 2 * (1 - t) * (1 - t)
	b.held += h / 2 * t * t
}

// out emits the previous frame and holds the current one.
func (b *bl) out(cur float64) float64 {
	o := b.held
	b.held = cur
	return o
}

// osc is a phase accumulator with every basic waveform.
type osc struct {
	ph float64
	b  bl
}

func (o *osc) advance(dt float64) bool {
	o.ph += dt
	if o.ph >= 1 {
		o.ph -= math.Floor(o.ph)
		return true
	}
	return false
}

// sine needs no correction and is not delayed.
func (o *osc) sine(dt float64) float64 {
	v := math.Sin(2 * math.Pi * o.ph)
	o.advance(dt)
	return v
}

func (o *osc) saw(dt float64) float64 {
	var corr float64
	if o.advance(dt) {
		o.b.step(o.ph/dt, -2, &corr)
	}
	return o.b.out(2*o.ph - 1 + corr)
}

// pulse is high for the first w of each cycle.
func (o *osc) pulse(dt, w float64) float64 {
	prev := o.ph
	wrapped := o.advance(dt)
	var corr float64
	if wrapped {
		o.b.step(o.ph/dt, 2, &corr)
		if prev < w {
			o.b.step((o.ph+1-w)/dt, -2, &corr)
		}
		if o.ph >= w {
			o.b.step((o.ph-w)/dt, -2, &corr)
		}
	} else if prev < w && o.ph >= w {
		o.b.step((o.ph-w)/dt, -2, &corr)
	}
	v := -1.0
	if o.ph < w {
		v = 1
	}
	return o.b.out(v + corr)
}

func (o *osc) square(dt float64) float64 { return o.pulse(dt, 0.5) }

// tri is naive: its harmonics fall at 12 dB/octave, so what folds back is
// already buried. It is delayed a frame to stay aligned with saw and pulse.
func (o *osc) tri(dt float64) float64 {
	v := 2*math.Abs(2*o.ph-1) - 1
	o.advance(dt)
	return o.b.out(v)
}

// syncSaw is a saw hard-synced to a master: each master cycle restarts it,
// and the restart's jump is band-limited like any other.
type syncSaw struct {
	m, s float64
	b    bl
}

func (o *syncSaw) next(dm, ds float64) float64 {
	o.m += dm
	o.s += ds
	var corr float64
	if o.m >= 1 {
		o.m -= math.Floor(o.m)
		t := o.m / dm
		before := o.s - ds*t
		if before >= 1 {
			o.b.step((o.s-1)/ds, -2, &corr)
			before -= math.Floor(before)
		}
		o.b.step(t, -2*before, &corr)
		o.s = ds * t
	} else if o.s >= 1 {
		o.s -= math.Floor(o.s)
		o.b.step(o.s/ds, -2, &corr)
	}
	return o.b.out(2*o.s - 1 + corr)
}

// stepTable plays a table of levels as a staircase — the chip machines'
// wavetables and stepped triangles — band-limiting each step.
type stepTable struct {
	tab []float64
	ph  float64 // in table steps
	b   bl
}

func (o *stepTable) next(dt float64) float64 {
	n := float64(len(o.tab))
	adv := dt * n // steps per frame
	i0 := int(o.ph)
	o.ph += adv
	var corr float64
	for k := i0 + 1; float64(k) <= o.ph; k++ {
		h := o.tab[k%len(o.tab)] - o.tab[(k-1)%len(o.tab)]
		if h != 0 {
			o.b.step((o.ph-float64(k))/adv, h, &corr)
		}
	}
	if o.ph >= n {
		o.ph -= n * math.Floor(o.ph/n)
	}
	return o.b.out(o.tab[int(o.ph)%len(o.tab)] + corr)
}

// rot is a constant-frequency sinusoid by complex rotation: one complex
// multiply a frame instead of a sine, which is what lets the cymbals and
// glottal sources afford hundreds of partials.
type rot struct{ re, im, cr, ci float64 }

func newRot(f, sr, phase float64) rot {
	w := 2 * math.Pi * f / sr
	return rot{re: math.Cos(2 * math.Pi * phase), im: math.Sin(2 * math.Pi * phase), cr: math.Cos(w), ci: math.Sin(w)}
}

// next returns the current sine value and advances.
func (r *rot) next() float64 {
	v := r.im
	r.re, r.im = r.re*r.cr-r.im*r.ci, r.re*r.ci+r.im*r.cr
	return v
}

// partial is one sinusoid in an additive or modal sound.
type partial struct {
	f, amp float64
	t60    float64 // seconds to fall 60 dB; 0 = sustained
	ph     float64 // starting phase in cycles
	attack float64 // seconds of raised-cosine onset; 0 = instant
}

// addPartials sums partials into dst, skipping any above 0.45 * sr. Fixed
// frequencies run on rotators; decays are a per-frame multiplier.
func addPartials(dst []float64, sr float64, ps []partial) {
	for _, p := range ps {
		if p.f <= 0 || p.f > 0.45*sr || p.amp == 0 {
			continue
		}
		r := newRot(p.f, sr, p.ph)
		g, dec := p.amp, 1.0
		if p.t60 > 0 {
			dec = math.Exp(-6.907755 / (p.t60 * sr))
		}
		na := int(p.attack * sr)
		for i := range dst {
			v := r.next() * g
			if i < na {
				v *= 0.5 - 0.5*math.Cos(math.Pi*float64(i)/float64(na))
			}
			dst[i] += v
			g *= dec
			if g < 1e-9*p.amp && p.t60 > 0 {
				break
			}
		}
	}
}
