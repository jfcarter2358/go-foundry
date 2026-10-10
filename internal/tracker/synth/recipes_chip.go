package synth

// recipes_chip.go — game-console voices (recipes 111-118).
//
// These are the one place the library imitates hardware limits on purpose:
// 4-bit staircases, an LFSR's fixed noise patterns, a SID's bitwise-ANDed
// waveforms. The steps are still band-limited, though — the console's own
// DAC and output filter rounded them, and an aliased staircase at 48 kHz
// sounds like a broken plugin, not a NES.
//
// Chip waves are exactly periodic, so their loops are short: a whole number
// of cycles the plan picks, with the loop seam identical by construction.

import "math"

func chipRecipes() []Recipe {
	ch := func(name, base, desc string, tags []string, ps []Param, body func(*ctx) *out) Recipe {
		return rec(name, "chip", base, desc, tags, ps, body)
	}
	pulseParams := func(duty float32) []Param {
		return []Param{
			par("duty", duty, 0.05, 0.5, "pulse width"),
			par("tone", 14000, 2000, 20000, "output lowpass in Hz"),
			par("attack", 0, 0, 0.05, "seconds"),
		}
	}
	return []Recipe{
		ch("chip_pulse_12", "C-4", "NES pulse at 12.5% duty: thin and nasal", []string{"pulse", "nes", "8bit"}, pulseParams(0.125), chipPulse),
		ch("chip_pulse_25", "C-4", "NES pulse at 25% duty: the classic lead", []string{"pulse", "nes", "8bit"}, pulseParams(0.25), chipPulse),
		ch("chip_pulse_50", "C-4", "NES pulse at 50% duty: hollow square", []string{"pulse", "nes", "8bit"}, pulseParams(0.5), chipPulse),
		ch("chip_triangle", "C-3", "NES triangle: 4-bit staircase bass", []string{"triangle", "nes", "8bit"}, []Param{
			par("tone", 14000, 2000, 20000, "output lowpass in Hz"),
			par("attack", 0, 0, 0.05, "seconds"),
		}, chipTriangle),
		ch("chip_noise", "C-4", "NES long-mode noise (unpitched hiss)", []string{"noise", "nes", "8bit"}, []Param{
			par("period", 3, 0, 15, "noise period setting (higher is lower)"),
			par("tone", 14000, 2000, 20000, "output lowpass in Hz"),
		}, chipNoise),
		ch("chip_noise_metal", "C-4", "NES short-mode noise: pitched metallic buzz", []string{"noise", "nes", "8bit", "metallic"}, []Param{
			par("tone", 14000, 2000, 20000, "output lowpass in Hz"),
			par("attack", 0, 0, 0.05, "seconds"),
		}, chipNoiseMetal),
		ch("chip_wave", "C-4", "Game Boy 32-step 4-bit wavetable", []string{"wavetable", "gameboy", "8bit"}, []Param{
			par("wave", 1, 0, 3, "0 saw, 1 organ, 2 half-square, 3 sine"),
			par("tone", 12000, 2000, 20000, "output lowpass in Hz"),
		}, chipWave),
		ch("chip_sid", "C-4", "SID: pulse and saw ANDed through a 12 dB filter", []string{"sid", "c64", "8bit"}, []Param{
			par("width", 0.5, 0.1, 0.9, "pulse width"),
			par("cutoff", 2200, 300, 12000, "filter cutoff in Hz"),
			par("resonance", 0.4, 0, 0.9, "filter resonance"),
		}, chipSID),
	}
}

// chipFinish is the console's output stage: the NES's highpass (which is
// why its pulses droop) and a lowpass, then a declick on the first frames.
func chipFinish(c *ctx, x []float64, tone, attack float64) {
	hpBuf(x, 40, c.sr, 1)
	lpBuf(x, tone, c.sr, 1)
	for i := range x {
		x[i] *= fadeIn(c.t(i), math.Max(attack, 0.001))
	}
}

func chipPlan(c *ctx) plan { return c.plan(0.08, 0.3) }

func chipPulse(c *ctx) *out {
	p := chipPlan(c)
	duty := c.v("duty")
	var o osc
	x := render(p.frames(), func(int) float64 { return o.pulse(p.f0/c.sr, duty) })
	chipFinish(c, x, c.v("tone"), c.v("attack"))
	return mono(x).looped(p).fade(0.002)
}

func chipTriangle(c *ctx) *out {
	p := chipPlan(c)
	tri := &stepTable{tab: nesTriangle()}
	x := render(p.frames(), func(int) float64 { return tri.next(p.f0 / c.sr) })
	chipFinish(c, x, c.v("tone"), c.v("attack"))
	return mono(x).looped(p).fade(0.002)
}

func chipNoise(c *ctx) *out {
	// The long sequence repeats every 32767 shifts; nudge the clock so that
	// is a whole number of frames and loop exactly one repeat (or several).
	rate := nesNoiseRate(int(c.v("period")))
	M := int(math.Round(32767 * c.sr / rate))
	reps := int(math.Max(1, math.Ceil(c.sr*0.5/float64(M))))
	fade := 0.002
	if M > c.n(2) {
		// The slowest settings repeat only every minute or so: too long to
		// store, and nobody hears that it repeats. Loop two seconds and
		// crossfade it like any other noise.
		M, reps, fade = c.n(2), 1, 0.05
	} else {
		rate = 32767 * c.sr / float64(M)
	}
	p := plan{start: c.n(0.08), L: M * reps, unit: c.sr / float64(M*reps), win: c.n(0.01)}
	l := newLFSR(false)
	x := render(p.frames(), func(int) float64 { return l.next(rate / c.sr) })
	chipFinish(c, x, c.v("tone"), 0)
	o := mono(x).looped(p).fade(fade)
	if fade > 0.002 {
		o.noisy()
	}
	return o
}

func chipNoiseMetal(c *ctx) *out {
	p := chipPlan(c)
	// Short mode cycles every 93 shifts: clocking it at 93x the note makes
	// the buzz's period the note's.
	l := newLFSR(true)
	x := render(p.frames(), func(int) float64 { return l.next(93 * p.f0 / c.sr) })
	chipFinish(c, x, c.v("tone"), c.v("attack"))
	return mono(x).looped(p).fade(0.002)
}

// gbWave is one of the Game Boy wave channel's 32-sample, 4-bit tables.
func gbWave(kind int) []float64 {
	t := make([]float64, 32)
	for i := range t {
		ph := float64(i) / 32
		var v float64
		switch kind {
		case 0: // saw
			v = float64(i / 2)
		case 2: // half-square: high a quarter, a step, low
			switch {
			case i < 8:
				v = 15
			case i < 16:
				v = 10
			default:
				v = 0
			}
		case 3: // sine
			v = math.Round(7.5 + 7.5*math.Sin(2*math.Pi*ph))
		default: // organ: fundamental plus octave and twelfth
			s := math.Sin(2*math.Pi*ph) + 0.5*math.Sin(4*math.Pi*ph) + 0.3*math.Sin(6*math.Pi*ph)
			v = math.Round(7.5 + 7.5*s/1.45)
		}
		t[i] = clamp(v, 0, 15)/7.5 - 1
	}
	return t
}

func chipWave(c *ctx) *out {
	p := chipPlan(c)
	st := &stepTable{tab: gbWave(int(math.Round(c.v("wave"))))}
	x := render(p.frames(), func(int) float64 { return st.next(p.f0 / c.sr) })
	chipFinish(c, x, c.v("tone"), 0)
	return mono(x).looped(p).fade(0.002)
}

func chipSID(c *ctx) *out {
	p := chipPlan(c)
	w, cut, res := c.v("width"), c.v("cutoff"), c.v("resonance")
	// The SID combines waveforms by ANDing their 12-bit values. That is not
	// band-limitable analytically, so it runs 8x oversampled and is
	// decimated through a steep lowpass.
	const os = 8
	n := p.frames()
	hi := make([]float64, n*os)
	var ph float64
	dt := p.f0 / (c.sr * os)
	for i := range hi {
		saw := uint32(ph * 4095)
		pulse := uint32(0)
		if ph < w {
			pulse = 4095
		}
		hi[i] = float64(saw&pulse)/2047.5 - 1
		ph += dt
		ph -= math.Floor(ph)
	}
	x := decimate(hi, os)
	var f svf
	f.set(cut, 0.7+5*res, c.sr)
	for i, v := range x {
		x[i], _, _ = f.tick(v)
	}
	for i := range x {
		x[i] *= fadeIn(c.t(i), 0.001)
	}
	return mono(x).looped(p).fade(0.002)
}
