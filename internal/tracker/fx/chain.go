package fx

// chain.go — effects in series: a line's inserts, a send bus, the master.
//
// A chain is built off the mixer goroutine (its effects allocate) and then
// only processed and adjusted there. Bypassing a slot crossfades it out
// over a few milliseconds rather than cutting, and a bypassed effect is not
// run at all. A chain rebuilt with the same effects in the same order can
// take the new one's settings with Take, so editing a parameter or the file
// around a playing chain keeps its reverb tail and delay line.

// bypassFrames is how long a slot takes to fade in or out when bypassed.
const bypassFrames = 256

type slot struct {
	e      Effect
	bypass bool
	// wet is how much of the slot's output is in the signal: 1 in, 0 out,
	// between while a bypass fades.
	wet, step float32
}

// Chain is effects in series.
type Chain struct {
	slots []slot
	dry   [2 * ChunkFrames]float32
}

// NewChain is an empty chain with room for n effects.
func NewChain(n int) *Chain { return &Chain{slots: make([]slot, 0, n)} }

// Add appends an effect, bypassed or not. Allocates past the room NewChain
// gave; call it while building.
func (c *Chain) Add(e Effect, bypass bool) {
	s := slot{e: e, bypass: bypass, wet: 1}
	if bypass {
		s.wet = 0
	}
	c.slots = append(c.slots, s)
}

// Len is how many effects the chain has; a nil chain has none.
func (c *Chain) Len() int {
	if c == nil {
		return 0
	}
	return len(c.slots)
}

// Effect is slot i's effect.
func (c *Chain) Effect(i int) Effect { return c.slots[i].e }

// Bypassed reports whether slot i is out of the signal.
func (c *Chain) Bypassed(i int) bool { return c.slots[i].bypass }

// SetBypass takes slot i out of the signal (on) or puts it back, fading.
func (c *Chain) SetBypass(i int, on bool) {
	if c == nil || i < 0 || i >= len(c.slots) {
		return
	}
	s := &c.slots[i]
	if s.bypass == on {
		return
	}
	s.bypass = on
	if on {
		s.step = -1 / float32(bypassFrames)
	} else {
		if s.wet == 0 {
			// It has not run while bypassed: start it from silence.
			s.e.Reset()
		}
		s.step = 1 / float32(bypassFrames)
	}
}

// SetParam glides slot's parameter to v over frames.
func (c *Chain) SetParam(slot, param int, v float32, frames int) {
	if c == nil || slot < 0 || slot >= len(c.slots) {
		return
	}
	c.slots[slot].e.SetParam(param, v, frames)
}

// SetTempo passes the tempo to every effect.
func (c *Chain) SetTempo(bpm float32) {
	if c == nil {
		return
	}
	for i := range c.slots {
		c.slots[i].e.SetTempo(bpm)
	}
}

// Reset empties every effect's memory.
func (c *Chain) Reset() {
	if c == nil {
		return
	}
	for i := range c.slots {
		c.slots[i].e.Reset()
	}
}

// SameKinds reports whether two chains have the same effects in the same
// order (so one can Take the other's settings).
func (c *Chain) SameKinds(o *Chain) bool {
	if c.Len() != o.Len() {
		return false
	}
	for i := 0; i < c.Len(); i++ {
		if c.slots[i].e.Kind() != o.slots[i].e.Kind() {
			return false
		}
	}
	return true
}

// Take moves c to o's settings — every parameter, gliding over frames, and
// every bypass — keeping c's own state. The chains must be SameKinds.
func (c *Chain) Take(o *Chain, frames int) {
	if !c.SameKinds(o) {
		return
	}
	for i := range c.slots {
		e, src := c.slots[i].e, o.slots[i].e
		for p := range e.Kind().Spec().Params {
			if v := src.Param(p); v != e.Param(p) {
				e.SetParam(p, v, frames)
			}
		}
		c.SetBypass(i, o.slots[i].bypass)
	}
}

// Process runs interleaved stereo frames through the chain in place.
func (c *Chain) Process(buf []float32) {
	if c == nil || len(c.slots) == 0 {
		return
	}
	for len(buf) >= 2 {
		n := min(len(buf), 2*ChunkFrames) &^ 1
		c.chunk(buf[:n])
		buf = buf[n:]
	}
}

func (c *Chain) chunk(buf []float32) {
	frames := len(buf) / 2
	for i := range c.slots {
		s := &c.slots[i]
		if s.step == 0 {
			if s.wet == 0 {
				continue // bypassed
			}
			s.e.Process(buf)
			continue
		}
		// Fading in or out: the effect runs and is blended with its input.
		w0 := s.wet
		w1 := clamp32(w0+s.step*float32(frames), 0, 1)
		dry := c.dry[:len(buf)]
		copy(dry, buf)
		s.e.Process(buf)
		blend(buf, dry, w0, w1)
		s.wet = w1
		if w1 == 0 || w1 == 1 {
			s.step = 0
		}
	}
}
