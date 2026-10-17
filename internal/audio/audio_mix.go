package audio

// audio_mix.go — voices in, one interleaved stereo block out.
//
// Nothing in this file touches SDL, a clock, or a goroutine. render() is an
// ordinary method that fills a slice, which is what lets the voice pool, the
// stealing policy, the declick ramp and the limiter all be tested by rendering
// ten seconds of audio in about a millisecond with no device attached.
//
// Two rules run through the whole file:
//
//   - Everything is float32 end to end, converted once at the sink. Mixing in
//     int16 would clip at every pairwise sum — which is exactly what
//     sdl.MixAudioFormat does, and why it is not used here.
//   - Gains interpolate per sample across the block, never per block. A source
//     moving quickly changes its pan a lot between two blocks, and applying
//     that as a step is zipper noise.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// cullGain is the level below which a voice is inaudible. A one-shot below it
// is freed; a loop is skipped but kept, because it will come back when the
// listener does.
const cullGain = 0.0005

// denormalFloor snaps a decaying envelope to zero. Denormal floats cost
// something like 100x on some CPUs, and an envelope tailing off is the one
// place in a game that reliably produces them.
const denormalFloor = 1e-6

// maxLoopingVoices caps sustained sounds, leaving the rest of the pool for
// one-shots. A script bug that starts a loop every frame would otherwise eat
// the whole pool in half a second and silence everything else — a failure that
// is much easier to design against than to debug.
const maxLoopingVoices = 8

// render fills dst with `frames` interleaved stereo frames.
//
// dst is assumed to be at least frames*2 long. It is overwritten, not added
// to, so the caller need not clear it.
func (m *mixer) render(dst []float32, frames int) {
	out := dst[:frames*2]
	for i := range out {
		out[i] = 0
	}

	st := m.state.Load()
	if st != nil {
		m.applyState(st)
	}
	effectsGain, musicGain := busGains()

	var listener ListenerBasis
	var listenerVel mgl32.Vec3
	if st != nil {
		listener = st.listener
		listenerVel = st.listenerVel
	} else {
		// No frame has been published yet. A world-aligned listener at the
		// origin is wrong, but it is only wrong for the first block, and it
		// beats skipping the mix and starting with a gap.
		listener = BasisFrom(mgl32.Vec3{}, mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 1, 0})
	}

	// Under water the effects bus mixes into the muffle's own buffer, to be
	// low-passed into out after the loop (audio_muffle.go); the music bus
	// always goes straight to out.
	fx := out
	if b := m.muffle.begin(frames, m.rate); b != nil {
		fx = b
	}
	for i := range m.voices {
		v := &m.voices[i]
		if !v.active {
			continue
		}
		bus, dst := effectsGain, fx
		if v.bus == BusMusic {
			bus, dst = musicGain, out
		}
		var finished bool
		if v.gen != nil {
			finished = m.mixGenerator(v, dst, frames, bus)
		} else {
			finished = m.mixVoice(v, dst, frames, listener, listenerVel, bus)
		}
		if finished {
			m.freeVoice(i)
		}
	}
	m.muffle.end(out, m.rate)

	m.limit(out)

	m.liveCount.Store(int64(m.live()))
	m.lastPeak.Store(math.Float32bits(m.peak))
}

// applyState folds this frame's published positions into the voice table.
func (m *mixer) applyState(st *frameState) {
	for _, p := range st.voices {
		i, ok := m.byID[p.ID]
		if !ok {
			continue
		}
		v := &m.voices[i]
		if !v.active {
			continue
		}
		// The ID carries a generation, so a slot recycled since the game
		// thread built this snapshot is not steered by the previous owner.
		if v.params.ID != p.ID {
			continue
		}
		v.params = p
	}
}

// mixVoice adds one voice into out and reports whether it has finished.
func (m *mixer) mixVoice(v *voice, out []float32, frames int, l ListenerBasis, lvel mgl32.Vec3, busGain float32) bool {
	c := v.clip
	if c == nil || c.Frames() == 0 {
		return true
	}
	p := v.params

	// ---- target gains for this block ----

	gain := p.Gain
	if gain == 0 {
		gain = 1
	}
	gain *= busGain

	pitch := p.Pitch
	if pitch <= 0 {
		pitch = 1
	}

	var gl, gr float32
	if p.Spatial {
		dist := p.Position.Sub(l.Position).Len()
		if dist >= p.Falloff.Max && p.Falloff.Max > 0 {
			// Out of range. A one-shot out here would never be heard, but
			// freeing it would be wrong — the listener may walk toward it —
			// so it keeps its slot and costs one subtract per block.
			gain = 0
		} else {
			gain *= Attenuation(dist, p.Falloff)
			gain *= ConeGain(l, p.Position, p.Dir, p.Cone)
		}
		// Sources collapse to the centre as the listener passes through them,
		// so walking over a sound does not slam it hard from ear to ear.
		spread := float32(0)
		if p.Falloff.Ref > 0 {
			spread = clamp01(1 - dist/p.Falloff.Ref)
		}
		pl, pr := PanGains(l, p.Position, spread)
		gl, gr = gain*pl, gain*pr

		pitch *= DopplerRatio(lvel, p.Position, p.Velocity, l.Position, SpeedOfSound, dopplerFactorOf(p))
	} else {
		// Non-positional. A mono clip goes to both ears at constant power so
		// it sits at the same loudness as a stereo one.
		if c.Channels == 1 {
			gl, gr = gain*0.70710678, gain*0.70710678
		} else {
			gl, gr = gain, gain
		}
	}

	// ---- envelopes ----

	if v.fadeIn > 0 && v.fadeInPos < 1 {
		gl *= v.fadeInPos
		gr *= v.fadeInPos
	}
	if v.stopping {
		// The ramp target is silence; reaching it frees the voice.
		gl, gr = 0, 0
	}

	v.targetL, v.targetR = gl, gr
	if !v.primed {
		// Start at the target rather than ramping from zero, or every
		// percussive sound would have its transient blunted by the declick
		// window. A clip that starts on a non-zero sample is the author's
		// business, not the mixer's.
		v.curL, v.curR = gl, gr
		v.primed = true
	}

	// Per-sample gain deltas across the block.
	fN := float32(frames)
	dL := (v.targetL - v.curL) / fN
	dR := (v.targetR - v.curR) / fN
	// A stop ramps at its own rate rather than over the whole block, so a
	// 5 ms declick is 5 ms regardless of how long a block happens to be.
	if v.stopping {
		dL = -v.stopRate
		dR = -v.stopRate
	}

	// ---- resampling step ----

	step := float64(c.Rate) / float64(m.rate) * float64(pitch)
	if step <= 0 {
		step = 1
	}

	srcFrames := float64(c.Frames())
	fadeStep := float32(0)
	if v.fadeIn > 0 {
		fadeStep = 1 / (v.fadeIn * float32(m.rate))
	}

	// Skip the sample loop entirely for a voice that is silent at both ends
	// of the block — the common case for a level full of distant ambience.
	if !v.stopping && v.curL < cullGain && v.curR < cullGain && v.targetL < cullGain && v.targetR < cullGain {
		v.cursor += step * float64(frames)
		v.curL, v.curR = v.targetL, v.targetR
		return m.advanceCursor(v, srcFrames)
	}

	for i := 0; i < frames; i++ {
		if v.cursor >= srcFrames {
			if !v.loop {
				// Ran out mid-block. The rest of the block stays as whatever
				// the other voices put there.
				v.curL, v.curR = 0, 0
				return true
			}
			v.cursor -= srcFrames
		}

		sl, sr := sampleAt(c, v.cursor)

		out[i*2] += sl * v.curL
		out[i*2+1] += sr * v.curR

		v.curL += dL
		v.curR += dR
		if v.curL < denormalFloor {
			v.curL = 0
		}
		if v.curR < denormalFloor {
			v.curR = 0
		}

		if fadeStep > 0 && v.fadeInPos < 1 {
			v.fadeInPos += fadeStep
		}
		v.cursor += step
	}

	if v.stopping && v.curL <= 0 && v.curR <= 0 {
		return true
	}
	return m.advanceCursor(v, srcFrames)
}

// advanceCursor wraps or ends a voice whose cursor ran past the clip.
func (m *mixer) advanceCursor(v *voice, srcFrames float64) bool {
	if v.cursor < srcFrames {
		return false
	}
	if !v.loop {
		return true
	}
	// math.Mod rather than repeated subtraction: a heavily pitched-up voice
	// can pass the end several times in one block.
	v.cursor = math.Mod(v.cursor, srcFrames)
	return false
}

// sampleAt reads the clip at a fractional frame position, linearly
// interpolated, and returns a left/right pair.
//
// Linear rather than cubic on purpose: the playback rate here only ever
// deviates from 1 by pitch and doppler, both clamped to [0.5, 2], where
// linear's error sits well below the noise floor of the source material. The
// expensive interpolation belongs at load time, not per sample per voice.
//
// A stereo clip played positionally is downmixed rather than panned — its
// stereo image is already baked in, and panning it would fight the author.
func sampleAt(c *Clip, pos float64) (l, r float32) {
	n := c.Frames()
	i0 := int(pos)
	if i0 < 0 {
		i0 = 0
	}
	if i0 >= n {
		i0 = n - 1
	}
	i1 := i0 + 1
	if i1 >= n {
		i1 = i0 // hold the last frame rather than wrapping into a click
	}
	t := float32(pos - float64(i0))

	if c.Channels == 1 {
		s := lerp(c.Samples[i0], c.Samples[i1], t)
		return s, s
	}
	l = lerp(c.Samples[i0*2], c.Samples[i1*2], t)
	r = lerp(c.Samples[i0*2+1], c.Samples[i1*2+1], t)
	return l, r
}

func lerp(a, b, t float32) float32 { return a + (b-a)*t }

// dopplerFactorOf reads the per-source doppler amount. Zero velocity on both
// ends makes the whole calculation a no-op, so the common case costs one
// comparison.
func dopplerFactorOf(p VoiceParams) float32 {
	if !DopplerEnabled || p.Velocity == (mgl32.Vec3{}) {
		return 0
	}
	return 1
}

// SpeedOfSound is in world units per second. A world unit here is not a metre,
// so this is a tuning knob rather than a physical constant — it is exposed for
// the same reason platform.MovementSpeed is.
var SpeedOfSound float32 = 343

// DopplerEnabled switches the pitch shift off wholesale.
var DopplerEnabled = true

// ---------- limiter ----------

// limit keeps the block inside [-1,1] without the harshness of a hard clamp.
//
// Twenty voices landing on the same transient will sum past unity, and
// clamping each sample there is precisely the artefact this package exists to
// avoid. Instead the gain is pulled down instantly when a peak demands it and
// released slowly, so a single loud frame ducks the block rather than
// squaring off its top.
func (m *mixer) limit(out []float32) {
	const release = 0.9995

	peak := float32(0)
	for _, s := range out {
		if a := absF(s); a > peak {
			peak = a
		}
	}
	m.peak = peak

	need := float32(1)
	if peak > 1 {
		need = 1 / peak
	}
	if need < m.limiterGain {
		m.limiterGain = need // instant attack
	}
	if m.limiterGain == 0 {
		m.limiterGain = 1
	}

	g := m.limiterGain
	for i := range out {
		out[i] *= g
		g += (1 - g) * (1 - release)
		// A sample can still exceed unity in the first instants of an attack;
		// clamp as a backstop so nothing downstream ever wraps.
		if out[i] > 1 {
			out[i] = 1
		} else if out[i] < -1 {
			out[i] = -1
		}
	}
	m.limiterGain = g
}

func absF(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// float32bits is math.Float32bits, kept here so audio_sink.go does not need
// the math import for one call.
func float32bits(f float32) uint32 { return math.Float32bits(f) }
