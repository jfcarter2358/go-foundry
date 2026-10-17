package fx

// distortion.go — drive and the bit crusher: effects that change the
// waveform itself. Drive is the Tracker's channel drive (kernels.go: unity
// gain for quiet signals) with a tone control after it; the crusher is the
// Tracker's crush with sample-rate reduction added.

import "math"

// ---------- drive ----------

const (
	drDrive = iota
	drTone
	drMix
	drOutput
)

type drive struct {
	base
	lp  [2]float32
	dry [2 * ChunkFrames]float32
}

func newDrive(rate int) *drive { return &drive{base: newBase(KindDrive, rate)} }

func (d *drive) Reset() { d.lp = [2]float32{} }

func (d *drive) Process(buf []float32) { runChunks(d, buf) }

func (d *drive) chunk(buf []float32) {
	n := len(buf) / 2
	d.advance(n)
	copy(d.dry[:len(buf)], buf)
	amt := newRamp(d.p[drDrive].prev, d.val(drDrive), n)
	// Tone 1 is open; below, a one-pole low-pass from 20 kHz down to 1 kHz
	// takes the fizz off the saturated top.
	tone := d.val(drTone)
	toneOn := tone < 0.999
	var a float32
	if toneOn {
		fc := 1000 * math.Pow(20, float64(tone))
		a = float32(1 - math.Exp(-2*math.Pi*fc/float64(d.rate)))
	}
	for f := 0; f < n; f++ {
		k := DriveK(amt.next())
		norm := 1 / k
		l, r := Drive(buf[f*2], k, norm), Drive(buf[f*2+1], k, norm)
		if toneOn {
			d.lp[0] = flush(d.lp[0] + a*(l-d.lp[0]))
			d.lp[1] = flush(d.lp[1] + a*(r-d.lp[1]))
			l, r = d.lp[0], d.lp[1]
		}
		buf[f*2], buf[f*2+1] = l, r
	}
	blend(buf, d.dry[:len(buf)], d.p[drMix].prev, d.val(drMix))
	if o := d.p[drOutput]; o.prev != 0 || o.cur != 0 {
		r := newRamp(dbGain(o.prev), dbGain(d.val(drOutput)), n)
		for f := 0; f < n; f++ {
			g := r.next()
			buf[f*2] *= g
			buf[f*2+1] *= g
		}
	}
}

// ---------- bit crusher ----------

const (
	crBits = iota
	crDownsample
	crMix
)

type crusher struct {
	base
	hold  [2]float32
	phase float32
	dry   [2 * ChunkFrames]float32
}

func newCrusher(rate int) *crusher { return &crusher{base: newBase(KindCrusher, rate), phase: 1} }

func (c *crusher) Reset() { c.hold, c.phase = [2]float32{}, 1 }

func (c *crusher) Process(buf []float32) { runChunks(c, buf) }

func (c *crusher) chunk(buf []float32) {
	n := len(buf) / 2
	c.advance(n)
	copy(c.dry[:len(buf)], buf)
	steps := CrushSteps(int(c.val(crBits)))
	ds := c.val(crDownsample)
	inc := 1 / max(ds, 1)
	for f := 0; f < n; f++ {
		// Sample and hold: a new sample every ds frames (fractional ds
		// alternates between the two nearest holds).
		if c.phase >= 1 || ds <= 1 {
			c.phase -= float32(math.Floor(float64(c.phase)))
			c.hold[0] = Crush(buf[f*2], steps)
			c.hold[1] = Crush(buf[f*2+1], steps)
		}
		c.phase += inc
		buf[f*2], buf[f*2+1] = c.hold[0], c.hold[1]
	}
	blend(buf, c.dry[:len(buf)], c.p[crMix].prev, c.val(crMix))
}
