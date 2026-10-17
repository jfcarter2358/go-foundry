package tracker

// dsp.go — the signal-processing pieces the player is built from.
//
// The filter, reverb, echo and compressor are internal/tracker/fx's
// kernels, which the Score's effects are built on too; this file adapts
// them to the song's specs. Each is a small struct with its state inline,
// sized at construction and never allocating afterwards: they run on the
// mixer goroutine, where a garbage-collector pause is a dropout.

import (
	"math"

	"foundry/internal/tracker/fx"
)

// hermite is 4-point, 3rd-order Hermite interpolation between y1 and y2.
func hermite(t, y0, y1, y2, y3 float32) float32 {
	c1 := 0.5 * (y2 - y0)
	c2 := y0 - 2.5*y1 + 2*y2 - 0.5*y3
	c3 := 0.5*(y3-y0) + 1.5*(y1-y2)
	return ((c3*t+c2)*t+c1)*t + y1
}

// svf is the voices' and strips' filter: fx.SVF, set from a FilterMode
// (whose numbers are fx.SVFMode's).
type svf = fx.SVF

// setFilter sets a filter for an instrument's (or a strip's) mode.
func setFilter(f *svf, mode FilterMode, cutoff, resonance float32, rate int) {
	f.Set(fx.SVFMode(mode), cutoff, resonance, rate)
}

// setReverb sets send bus A's reverb from a song's spec.
func setReverb(r *fx.Freeverb, s ReverbSpec) { r.Set(s.Size, s.Damping, s.Width) }

// setDelay sets send bus B's echo from a song's spec and tempo: the time
// in beats as whole samples, jumping at once when the tempo moves.
func setDelay(d *fx.Echo, s DelaySpec, bpm float32, rate int) {
	beats := s.Beats
	if beats <= 0 {
		beats = 0.75
	}
	if bpm <= 0 {
		bpm = 120
	}
	d.SetLength(int(float64(beats) * 60 / float64(bpm) * float64(rate)))
	d.SetVoice(s.Feedback, s.Tone, s.PingPong)
}

// setCompressor sets the master compressor from a song's spec.
func setCompressor(c *fx.Comp, s CompressorSpec, rate int) {
	c.Set(s.Enabled, s.Threshold, s.Ratio, s.Attack, s.Release, s.Makeup, rate)
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
