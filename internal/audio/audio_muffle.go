package audio

// audio_muffle.go — the world heard from under water
// (docs/terrain-paint/PHASE_W5.md, item 4).
//
// With the camera below a water surface everything in the world should sound
// dull and far: water carries the low end and swallows the highs. SetMuffle
// asks the mixer for that, 0 (dry, the default) to 1 (fully under): the
// effects bus — every placed and played sound — is summed into a buffer of
// its own, run through a two-pole low-pass (two one-pole stages, the
// cut-off sliding from muffleOpen down to muffleCutoff Hz as the amount
// rises, cross-faded in from the dry sound) and ducked by up to muffleDuck, then added to the mix. The music bus is left
// alone: the score is not in the water.
//
// The amount glides (a time constant of muffleGlide seconds), so ducking
// under and breaking the surface are a quick sweep, not a click. At 0 —
// every frame above water — the effects voices mix straight into the output
// as before and the filter costs nothing.

import (
	"math"
	"sync/atomic"
)

const (
	// muffleCutoff is the low-pass's corner fully under, Hz.
	muffleCutoff = 500.0
	// muffleOpen is its corner at a trace of muffle, Hz; the filtered
	// sound is cross-faded in over the first third of the amount, so the
	// sweep starts from the dry sound, not from a filter.
	muffleOpen = 4000.0
	// muffleDuck is how much quieter the world is fully under.
	muffleDuck = 0.55
	// muffleGlide is the amount's time constant, seconds.
	muffleGlide = 0.12
)

var muffleTarget atomic.Uint32

// SetMuffle sets how far under water the listener is, 0..1.
func SetMuffle(amount float32) {
	muffleTarget.Store(math.Float32bits(clamp01(amount)))
}

// Muffle reads the amount back.
func Muffle() float32 { return math.Float32frombits(muffleTarget.Load()) }

// muffleState is the mixer's filter: the glided amount, the effects bus's
// buffer and the two stages' memories per ear.
type muffleState struct {
	cur    float32
	buf    []float32
	s1, s2 [2]float32
}

// begin returns the buffer this block's effects voices should mix into, or
// nil for straight into the output (no muffle, and none gliding out).
func (f *muffleState) begin(frames int, rate int) []float32 {
	target := Muffle()
	k := float32(1 - math.Exp(-float64(frames)/float64(rate)/muffleGlide))
	f.cur += (target - f.cur) * k
	if target == 0 && f.cur < 0.02 {
		f.cur = 0
		f.s1, f.s2 = [2]float32{}, [2]float32{}
		return nil
	}
	if cap(f.buf) < frames*2 {
		f.buf = make([]float32, frames*2)
	}
	f.buf = f.buf[:frames*2]
	clear(f.buf)
	return f.buf
}

// end filters the effects buffer into out. A no-op after a nil begin.
func (f *muffleState) end(out []float32, rate int) {
	if f.cur == 0 || len(f.buf) == 0 {
		return
	}
	// The corner, swept on a log scale.
	fc := muffleOpen * math.Pow(muffleCutoff/muffleOpen, float64(f.cur))
	a := float32(1 - math.Exp(-2*math.Pi*fc/float64(rate)))
	gain := 1 - muffleDuck*f.cur
	wet := min(f.cur*3, 1)
	n := len(f.buf) / 2
	for i := 0; i < n; i++ {
		for c := 0; c < 2; c++ {
			x := f.buf[i*2+c]
			f.s1[c] += (x - f.s1[c]) * a
			f.s2[c] += (f.s1[c] - f.s2[c]) * a
			out[i*2+c] += (x*(1-wet) + f.s2[c]*wet) * gain
		}
	}
	for c := 0; c < 2; c++ {
		if absF(f.s1[c]) < denormalFloor {
			f.s1[c] = 0
		}
		if absF(f.s2[c]) < denormalFloor {
			f.s2[c] = 0
		}
	}
}
