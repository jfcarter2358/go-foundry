package audio

// audio_generator.go — voices whose samples are computed, not decoded.
//
// A Clip is a finished buffer; some sounds are not finished until they play.
// The tracker's sequencer (internal/tracker) is the case this exists for: a
// song is note data plus samples, rendered a block at a time, and a script can
// change what it plays while it plays. Running it as a voice — rather than as
// a second mixer — means the music slider, the master gain, the fades, the
// stealing policy and the limiter all reach it with no new code, and the song
// costs the pool exactly one slot.
//
// The generator runs on the mixer goroutine, inside render. That is what makes
// its timing sample-accurate regardless of frame rate, and it is also the rule
// a Generator must live by: no allocation in steady state, no locks the game
// thread can hold for long, no I/O. Control from the game thread reaches it the
// same way commands reach the mixer — through a queue it drains itself.

// Generator produces audio on demand.
type Generator interface {
	// Render fills dst with frames interleaved stereo frames at the given
	// sample rate, overwriting it, and reports whether the generator has more
	// to give. Returning false ends the voice once this block is mixed.
	Render(dst []float32, frames, rate int) bool
}

// PlayGenerator starts a voice fed by g. Only the non-spatial fields of o are
// honoured — a generated stream is music or UI, never a point in the world —
// and o.Clip and o.Loop are ignored. The zero handle comes back when there is
// no device, exactly as for Play.
func PlayGenerator(g Generator, o PlayOptions) VoiceID {
	d := active.Load()
	if d == nil || g == nil {
		return 0
	}
	if o.Gain == 0 {
		o.Gain = 1
	}
	if o.Bus == "" {
		o.Bus = BusMusic
	}
	id := newVoiceID()
	if !d.send(command{kind: cmdPlay, id: id, req: PlayRequest{
		Gen:      g,
		Bus:      o.Bus,
		Gain:     o.Gain,
		Pitch:    1,
		Loop:     true, // sustained, for the steal policy and the loop cap
		Priority: o.Priority,
		FadeIn:   o.FadeIn,
	}}) {
		return 0
	}
	return id
}

// mixGenerator is mixVoice for a generated voice: pull a block, then apply the
// same gain smoothing, fade-in and stop ramp a clip voice gets.
func (m *mixer) mixGenerator(v *voice, out []float32, frames int, busGain float32) bool {
	if cap(m.scratch) < frames*2 {
		m.scratch = make([]float32, frames*2)
	}
	buf := m.scratch[:frames*2]
	more := v.gen.Render(buf, frames, m.rate)

	gain := v.params.Gain
	if gain == 0 {
		gain = 1
	}
	gain *= busGain
	if v.fadeIn > 0 && v.fadeInPos < 1 {
		gain *= v.fadeInPos
	}
	if v.stopping {
		gain = 0
	}
	v.targetL, v.targetR = gain, gain
	if !v.primed {
		v.curL, v.curR = gain, gain
		v.primed = true
	}
	d := (v.targetL - v.curL) / float32(frames)
	if v.stopping {
		d = -v.stopRate
	}
	fadeStep := float32(0)
	if v.fadeIn > 0 {
		fadeStep = 1 / (v.fadeIn * float32(m.rate))
	}
	for i := 0; i < frames; i++ {
		out[i*2] += buf[i*2] * v.curL
		out[i*2+1] += buf[i*2+1] * v.curL
		v.curL += d
		if v.curL < denormalFloor {
			v.curL = 0
		}
		if fadeStep > 0 && v.fadeInPos < 1 {
			v.fadeInPos += fadeStep
		}
	}
	v.curR = v.curL
	if v.stopping && v.curL <= 0 {
		return true
	}
	return !more
}
