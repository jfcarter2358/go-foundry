package tracker

// voice.go — one sounding sample of a note.
//
// A note-on starts one voice, or two when its velocity falls in the overlap
// of two layers. A voice attached to its strip follows the strip's follow
// values (a Tracker channel's volume slides, vibrato, pan) every control
// block. When the channel moves on — a new note, a note-off with polyphony,
// a steal — the voice is detached and keeps the last values it saw while it
// releases or declicks. A note the Score plays never follows: its sequencer
// steers it directly (SetNoteControls).

import "math"

// declickSeconds is the fade a cut note gets instead of a step to silence.
const declickSeconds = 0.004

type pvoice struct {
	active   bool
	attached bool // follows its strip's follow values
	strip    int  // engine strip; PreviewStrip; -2 never used
	inst     *Instrument
	smp      *Sample
	interp   Interp
	group    NoteID // the note-on that started it
	lead     bool   // the first voice of its note (the one polyphony counts)
	env      *Envelope
	fadeOut  float32
	level    float32 // layer crossfade and articulation gain, 1 for most notes

	pos  float64
	dir  int
	base uint8
	note float32

	// Last strip values, frozen once detached; a non-following note's own.
	vol, pitchOff, pan float32

	released bool
	killing  bool
	fade     float32 // 1..0 release ramp
	fadeRate float32 // per second

	envT  [4]float32 // vol, pan, pitch, filter clocks
	age   uint64
	t     float32 // seconds since start, for vibrato delay
	hold  float32 // release after this many seconds; 0 waits for a note-off
	vibPh float32

	filt       svf
	curL, curR float32
	primed     bool
}

func (v *pvoice) release() {
	if v.released || v.killing {
		return
	}
	v.released = true
	env := v.env
	if env.On() && (env.Sustain >= 0 || env.LoopEnd > env.LoopStart) {
		return // the envelope carries the release from its sustain point
	}
	fo := v.fadeOut
	if fo < declickSeconds {
		fo = declickSeconds
	}
	v.fadeRate = 1 / fo
}

func (v *pvoice) kill() {
	v.killing = true
	v.released = true
	v.fadeRate = 1 / declickSeconds
}

// startNote starts the voices of one note-on: the sample the zone (or the
// articulation's zone) maps the note to, one voice per velocity layer it
// sounds — two across a crossfade — with the layer's round-robin turn.
func (e *Engine) startNote(id NoteID, strip int, in *Instrument, pitch float32, o NoteOptions) NoteID {
	if in == nil || id == 0 {
		return 0
	}
	key := int(math.Floor(float64(pitch) + 0.5))
	key = clampInt(key, int(NoteMin), int(NoteMax))
	vel := o.Velocity
	if vel <= 0 {
		vel = 1
	}
	var picks [2]voicePick
	n := in.pick(uint8(key), vel, o.Articulation, e, &picks)
	if n == 0 {
		return 0
	}
	env, fadeOut, artGain := &in.VolEnv, in.FadeOut, float32(1)
	if a := in.articulation(o.Articulation); a != nil {
		if a.VolEnv.On() {
			env = &a.VolEnv
		}
		if a.FadeOut > 0 {
			fadeOut = a.FadeOut
		}
		artGain = dbGain(a.Gain)
	}
	if o.Release > 0 {
		fadeOut = o.Release
	}
	vol := o.Volume
	if vol <= 0 {
		vol = 1
	}
	interp := in.Interp
	if interp == InterpSong {
		interp = e.interp
	}
	for k := 0; k < n; k++ {
		pk := picks[k]
		i := e.allocVoice()
		v := &e.voices[i]
		e.age++
		*v = pvoice{
			active: true, attached: o.Follow, strip: strip, inst: in, smp: pk.smp,
			base: pk.base, note: pitch, vol: vol, pan: o.Pan, fade: 1, dir: 1,
			age: e.age, interp: interp, group: id, lead: k == 0,
			env: env, fadeOut: fadeOut, level: pk.level * artGain, hold: o.Hold,
		}
		if o.Reverse {
			v.dir = -1
			v.pos = float64(pk.smp.Frames()-1) * float64(1-o.Offset)
		} else {
			v.pos = float64(pk.smp.Frames()) * float64(o.Offset)
		}
		// Starting mid-sample would click; ramp in over the first block.
		v.primed = o.Offset == 0 && !o.Reverse
	}
	return id
}

// allocVoice finds a free voice, or steals the least audible.
func (e *Engine) allocVoice() int {
	best, bestScore := 0, float32(math.MaxFloat32)
	for i := range e.voices {
		v := &e.voices[i]
		if !v.active {
			return i
		}
		score := v.curL + v.curR
		if v.killing {
			score -= 10
		} else if v.released {
			score -= 5
		}
		score -= float32(e.age-v.age) * 1e-6
		if score < bestScore {
			best, bestScore = i, score
		}
	}
	e.voices[best].attached = false
	return best
}

// attached reports whether any voice of a note still follows its strip.
func (e *Engine) attached(id NoteID) bool {
	if id == 0 {
		return false
	}
	for i := range e.voices {
		if v := &e.voices[i]; v.active && v.attached && v.group == id {
			return true
		}
	}
	return false
}

// attachedLead is a following note's first voice, or nil.
func (e *Engine) attachedLead(id NoteID) *pvoice {
	if id == 0 {
		return nil
	}
	for i := range e.voices {
		if v := &e.voices[i]; v.active && v.attached && v.lead && v.group == id {
			return v
		}
	}
	return nil
}

// detach stops a strip steering a note; its voices keep the last values.
func (e *Engine) detach(id NoteID) {
	for i := range e.voices {
		if v := &e.voices[i]; v.active && v.group == id {
			v.attached = false
		}
	}
}

// releaseAttached and killAttached act on a note only while it still
// follows its strip — a channel's hold on its note ends when it detaches.
func (e *Engine) releaseAttached(id NoteID) {
	for i := range e.voices {
		if v := &e.voices[i]; v.active && v.attached && v.group == id {
			v.release()
		}
	}
}

// stripNotes counts the notes sounding on a strip, by their lead voices.
func (e *Engine) stripNotes(strip int) int {
	n := 0
	for i := range e.voices {
		if v := &e.voices[i]; v.active && v.lead && v.strip == strip && !v.killing {
			n++
		}
	}
	return n
}

// renderVoice adds n frames of one voice into buf and reports whether it ended.
func (e *Engine) renderVoice(v *pvoice, buf []float32, n int, dt float32) bool {
	in, smp := v.inst, v.smp
	ended := false

	// ---- envelopes and release ----
	env := float32(1)
	if ve := v.env; ve.On() {
		env = ve.Value(v.envT[0])
		v.envT[0] = ve.Advance(v.envT[0], dt, v.released)
		if v.released && ve.Done(v.envT[0]) && ve.Value(v.envT[0]) <= 0.0001 {
			ended = true
		}
	}
	if v.fadeRate > 0 {
		v.fade -= v.fadeRate * dt
		if v.fade <= 0 {
			v.fade = 0
			ended = true
		}
	}
	pan := v.pan + in.Pan
	if in.PanEnv.On() {
		pan += in.PanEnv.Value(v.envT[1])
		v.envT[1] = in.PanEnv.Advance(v.envT[1], dt, v.released)
	}
	semis := v.note - float32(v.base) + float32(in.Transpose) + (in.FineTune+smp.FineTune)/100 + v.pitchOff
	if in.PitchEnv.On() {
		semis += in.PitchEnv.Value(v.envT[2])
		v.envT[2] = in.PitchEnv.Advance(v.envT[2], dt, v.released)
	}
	v.t += dt
	if v.hold > 0 && v.t >= v.hold {
		v.release()
	}
	if in.VibDepth > 0 && v.t > in.VibDelay {
		v.vibPh += in.VibRate * dt
		semis += in.VibDepth * float32(math.Sin(2*math.Pi*float64(v.vibPh)))
	}
	if in.Filter != FilterOff {
		cut := in.Cutoff
		if in.FilterEnv.On() {
			cut *= float32(math.Exp2(float64(in.FilterEnv.Value(v.envT[3]))))
			v.envT[3] = in.FilterEnv.Advance(v.envT[3], dt, v.released)
		}
		setFilter(&v.filt, in.Filter, cut, in.Resonance, e.rate)
	}

	gain := v.vol * in.Volume * smp.Volume * env * v.fade * v.level
	if ended {
		gain = 0
	}
	var tl, tr float32
	if smp.Channels == 1 {
		pl, pr := equalPower(pan)
		tl, tr = gain*pl, gain*pr
	} else {
		// A stereo sample keeps its image; pan acts as balance.
		tl, tr = gain, gain
		if pan < 0 {
			tr *= 1 + clamp32(pan, -1, 0)
		} else {
			tl *= 1 - clamp32(pan, 0, 1)
		}
	}
	if !v.primed {
		v.primed = true
		v.curL, v.curR = 0, 0
	}
	dl := (tl - v.curL) / float32(n)
	dr := (tr - v.curR) / float32(n)

	step := semitoneRatio(semis) * float64(smp.Rate) / float64(e.rate)
	frames := smp.Frames()
	looped := smp.Looped() && v.dir > 0 || smp.Loop == LoopPingPong && smp.Looped()
	ls, le := float64(smp.LoopStart), float64(smp.LoopEnd)
	data := smp.Data
	stereo := smp.Channels == 2

	for f := 0; f < n; f++ {
		if v.pos < 0 || v.pos >= float64(frames) {
			ended = true
			break
		}
		var sl, sr float32
		if ip := int(v.pos); !stereo && ip >= 1 && ip+2 < frames {
			// The common case, read inline: a mono frame away from the ends.
			t := float32(v.pos - float64(ip))
			switch v.interp {
			case InterpNearest:
				sl = data[ip]
			case InterpLinear:
				sl = data[ip] + (data[ip+1]-data[ip])*t
			default:
				sl = hermite(t, data[ip-1], data[ip], data[ip+1], data[ip+2])
			}
			sr = sl
		} else {
			sl, sr = readSample(data, frames, stereo, v.pos, v.interp)
		}
		if in.Filter != FilterOff {
			sl = v.filt.Process(0, sl)
			if stereo {
				sr = v.filt.Process(1, sr)
			} else {
				sr = sl
			}
		}
		v.curL += dl
		v.curR += dr
		buf[f*2] += sl * v.curL
		buf[f*2+1] += sr * v.curR

		v.pos += step * float64(v.dir)
		if looped {
			if v.dir > 0 && v.pos >= le {
				if smp.Loop == LoopPingPong {
					v.pos = 2*le - v.pos - 1
					v.dir = -1
				} else {
					v.pos -= le - ls
				}
			} else if v.dir < 0 && smp.Loop == LoopPingPong && v.pos < ls {
				v.pos = 2*ls - v.pos
				v.dir = 1
			}
		}
	}
	return ended
}

// readSample reads a frame at a fractional position.
func readSample(data []float32, frames int, stereo bool, pos float64, interp Interp) (float32, float32) {
	i := int(pos)
	t := float32(pos - float64(i))
	at := func(k, c int) float32 {
		if k < 0 {
			k = 0
		} else if k >= frames {
			k = frames - 1
		}
		if stereo {
			return data[k*2+c]
		}
		return data[k]
	}
	read := func(c int) float32 {
		switch interp {
		case InterpNearest:
			return at(i, c)
		case InterpLinear:
			a := at(i, c)
			return a + (at(i+1, c)-a)*t
		}
		return hermite(t, at(i-1, c), at(i, c), at(i+1, c), at(i+2, c))
	}
	l := read(0)
	if !stereo {
		return l, l
	}
	return l, read(1)
}
