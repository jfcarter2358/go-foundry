package synth

// loop.go — loop points that do not click.
//
// The cheap way to a click-free loop is to not need a crossfade at all: if
// every oscillator in a sound completes a whole number of cycles over the
// loop, the waveform at the loop end is the waveform at the loop start. A
// plan picks a loop length that holds a whole number of base periods and
// snaps every other frequency a recipe uses (detuned copies, LFO rates,
// beating partials) to a multiple of 1/length — a nudge of a fraction of a
// cent that buys an exact seam.
//
// What cannot be made periodic (noise, a plucked string's slow evolution, a
// bowed string's friction) is handled by loopify's crossfade, which blends
// the frames before the end with the frames before the start so the seam
// lands where the waveform was always going to be.

import "math"

type plan struct {
	start int     // earliest loop start
	L     int     // loop length in frames
	unit  float64 // sr/L: any multiple of this repeats exactly over the loop
	f0    float64 // the base frequency snapped onto the grid
	win   int     // frames loopify may search past start for a quiet crossing
}

// plan lays out a loop of about loopSec seconds starting no earlier than
// startSec, for a sound whose fundamental is the recipe's base note.
func (c *ctx) plan(startSec, loopSec float64) plan {
	return c.planAt(c.f0, startSec, loopSec)
}

func (c *ctx) planAt(f0, startSec, loopSec float64) plan {
	// An even cycle count keeps f0/2 on the grid too, for sub-octaves and
	// the fifth of a power chord.
	k := math.Max(2, 2*math.Round(loopSec*f0/2))
	L := int(math.Round(k * c.sr / f0))
	unit := c.sr / float64(L)
	win := c.n(1 / f0)
	if win < c.n(0.01) {
		win = c.n(0.01)
	}
	if win > c.n(0.05) {
		win = c.n(0.05)
	}
	return plan{start: c.n(startSec), L: L, unit: unit, f0: k * unit, win: win}
}

// q snaps a frequency onto the loop's grid. A positive rate never snaps to
// zero: an LFO that stopped would silently change the sound.
func (p plan) q(f float64) float64 {
	m := math.Round(f / p.unit)
	if m < 1 && f > 0 {
		m = 1
	}
	return m * p.unit
}

// frames is how much a body must render for loopify to have room.
func (p plan) frames() int { return p.start + p.win + p.L + 16 }

// loopify chooses the loop and crossfades its seam in place.
func loopify(c *ctx, o *out) (start, end int) {
	x := o.xfade
	if x == 0 {
		x = 0.03
	}
	X := c.n(x)
	n := len(o.ch[0])
	L := o.length
	if L <= 0 || L >= n {
		panic("synth: " + c.r.Name + ": bad loop length")
	}
	s0 := o.start
	if s0 < X+1 {
		s0 = X + 1
	}
	win := o.searchWindow
	if win <= 0 {
		win = c.n(0.02)
	}
	mix := make([]float64, n)
	for _, ch := range o.ch {
		for i, v := range ch {
			mix[i] += v
		}
	}
	// The flattest, quietest point in the window. For a periodic loop every
	// candidate is seamless and this only keeps the seam off a steep edge
	// (a pulse wave crosses zero only on its edges); for noise it keeps a
	// crackle off the seam.
	best, bestScore := s0, math.Inf(1)
	for i := s0; i < s0+win && i+L <= n; i++ {
		score := math.Abs(mix[i]-mix[i-1]) + 0.25*math.Abs(mix[i])
		if score < bestScore {
			best, bestScore = i, score
		}
	}
	start, end = best, best+L
	if end > n {
		panic("synth: " + c.r.Name + ": loop runs past the render")
	}
	for _, ch := range o.ch {
		for j := 0; j < X; j++ {
			w := 0.5 - 0.5*math.Cos(math.Pi*float64(j+1)/float64(X+1))
			a, b := w, 1-w
			if o.equalPower {
				a, b = math.Sin(w*math.Pi/2), math.Cos(w*math.Pi/2)
			}
			ch[end-X+j] = ch[end-X+j]*b + ch[start-X+j]*a
		}
	}
	return start, end
}

// flatten holds a decaying sound's level steady from frame `from` on, so a
// plucked string can sustain under a loop. Its timbre keeps evolving — the
// highs still die away — which is what keeps it sounding plucked.
func flatten(x []float64, from int, sr, f0 float64) {
	w := int(4 * sr / f0)
	if w < 64 {
		w = 64
	}
	if from+w >= len(x) {
		return
	}
	// RMS over a sliding window a few periods long, centred.
	sq := make([]float64, len(x)+1)
	for i, v := range x {
		sq[i+1] = sq[i] + v*v
	}
	rms := func(i int) float64 {
		a, b := i-w/2, i+w/2
		if a < 0 {
			a = 0
		}
		if b > len(x) {
			b = len(x)
		}
		return math.Sqrt((sq[b]-sq[a])/float64(b-a)) + 1e-12
	}
	ref := rms(from)
	for i := from; i < len(x); i++ {
		x[i] *= ref / rms(i)
	}
}
