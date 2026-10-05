package tracker

// voice.go — one sounding note, and the block mix around it.
//
// A voice attached to its channel follows the channel's live state (volume
// slides, vibrato, pan) every control block. When the channel moves on — a
// new note, a note-off with polyphony, a steal — the voice is detached and
// keeps the last values it saw while it releases or declicks.

import "math"

// declickSeconds is the fade a cut note gets instead of a step to silence.
const declickSeconds = 0.004

type pvoice struct {
	active   bool
	attached bool
	ch       int // song channel; -1 preview; -2 never used
	inst     *Instrument
	smp      *Sample
	interp   Interp

	pos  float64
	dir  int
	base uint8
	note float32

	// Last channel values, frozen once detached.
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
	in := v.inst
	env := &in.VolEnv
	if env.On() && (env.Sustain >= 0 || env.LoopEnd > env.LoopStart) {
		return // the envelope carries the release from its sustain point
	}
	fo := in.FadeOut
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

// renderBlock mixes n frames into out: voices into channel buffers, channel
// strips into the dry and send buses, sends through their effects, then the
// master compressor.
func (p *Player) renderBlock(out []float32, n int) {
	dt := float32(n) / float32(p.rate)
	nn := n * 2
	for i := range p.chans {
		if p.used[i] {
			clear(p.chanBuf[i][:nn])
			p.used[i] = false
		}
	}
	clear(p.prevBuf[:nn])
	clear(p.dry[:nn])
	clear(p.a[:nn])
	clear(p.b[:nn])

	p.rampControls(dt)

	for i := range p.voices {
		v := &p.voices[i]
		if !v.active {
			continue
		}
		var buf []float32
		switch {
		case v.ch == -1:
			buf = p.prevBuf
		case v.ch >= 0 && v.ch < len(p.chans):
			buf = p.chanBuf[v.ch]
		default:
			v.active = false
			continue
		}
		if v.attached {
			ch := &p.chans[v.ch]
			v.vol = p.volMod(ch)
			v.pitchOff = p.pitchMod(ch)
			v.pan = ch.pan
		}
		if v.ch >= 0 {
			p.used[v.ch] = true
		}
		if done := p.renderVoice(v, buf[:nn], n, dt); done {
			p.detach(v)
			v.active = false
		}
	}

	for ci := range p.chans {
		p.mixStrip(ci, n)
	}
	for i := 0; i < nn; i++ {
		p.dry[i] += p.prevBuf[i]
	}

	revRet, dlyRet := p.song.Reverb.Return, p.song.Delay.Return
	gain := p.song.Gain
	if gain == 0 {
		gain = 1
	}
	for f := 0; f < n; f++ {
		l, r := p.dry[f*2], p.dry[f*2+1]
		if revRet > 0 {
			wl, wr := p.rev.process(p.a[f*2], p.a[f*2+1])
			l += wl * revRet
			r += wr * revRet
		}
		if dlyRet > 0 {
			wl, wr := p.dly.process(p.b[f*2], p.b[f*2+1])
			l += wl * dlyRet
			r += wr * dlyRet
		}
		l, r = p.comp.process(l, r)
		out[f*2], out[f*2+1] = l*gain, r*gain
		if f&3 == 0 {
			p.scope[p.scopePos].Store(math.Float32bits((l + r) * 0.5 * gain))
			p.scopePos = (p.scopePos + 1) % scopeLen
		}
	}
}

// rampControls moves params, mutes and tempo toward their targets and works
// out each strip's bound values for this block.
func (p *Player) rampControls(dt float32) {
	for i := range p.params {
		ps := &p.params[i]
		if ps.cur != ps.target {
			step := ps.rate * dt
			if ps.rate == 0 || absf(ps.target-ps.cur) <= step {
				ps.cur = ps.target
			} else if ps.target > ps.cur {
				ps.cur += step
			} else {
				ps.cur -= step
			}
		}
	}
	if p.bpm != p.bpmTarget {
		step := p.bpmRate * dt
		if p.bpmRate == 0 || absf(p.bpmTarget-p.bpm) <= step {
			p.bpm = p.bpmTarget
		} else if p.bpmTarget > p.bpm {
			p.bpm += step
		} else {
			p.bpm -= step
		}
		p.dly.set(p.song.Delay, p.bpm, p.rate)
	}
	for ci := range p.strip {
		st := &p.strip[ci]
		if st.mute != st.muteTarget {
			step := st.muteRate * dt
			if absf(st.muteTarget-st.mute) <= step {
				st.mute = st.muteTarget
			} else if st.muteTarget > st.mute {
				st.mute += step
			} else {
				st.mute -= step
			}
		}
		chn := p.song.Channels[ci]
		ch := &p.chans[ci]
		st.vol, st.pan = chn.Volume, chn.Pan
		st.sendA, st.sendB = ch.sendA, ch.sendB
		if !ch.sendSet {
			st.sendA, st.sendB = chn.SendA, chn.SendB
		}
		st.cutoffHz = 0
	}
	for _, b := range p.binds {
		v := p.params[b.param].cur
		st := &p.strip[b.ch]
		lin := b.from + (b.to-b.from)*v
		switch b.target {
		case BindVolume:
			st.vol *= lin
		case BindPan:
			st.pan = clamp32(st.pan+lin, -1, 1)
		case BindSendA:
			st.sendA = lin
		case BindSendB:
			st.sendB = lin
		case BindCutoff:
			from, to := max(b.from, 16), max(b.to, 16)
			hz := from * float32(math.Pow(float64(to/from), float64(v)))
			if st.cutoffHz == 0 || hz < st.cutoffHz {
				st.cutoffHz = hz
			}
		}
	}
	for ci := range p.strip {
		st := &p.strip[ci]
		ch := &p.chans[ci]
		hz := st.cutoffHz
		if ch.cutoff < 1 {
			fx := 20 * float32(math.Pow(1000, float64(ch.cutoff)))
			if hz == 0 || fx < hz {
				hz = fx
			}
		}
		if hz > 0 && hz < 19000 {
			if !st.filtOn {
				st.filt.reset()
			}
			st.filtOn = true
			st.filt.set(FilterLowPass, hz, ch.reso, p.rate)
		} else {
			st.filtOn = false
		}
	}
}

// mixStrip runs one channel buffer through its strip into the buses.
func (p *Player) mixStrip(ci, n int) {
	st := &p.strip[ci]
	if !p.used[ci] {
		// Nothing played into this channel: let its meter fall, skip the mix.
		st.peak *= 0.9
		if ci < MaxChannels {
			p.levels[ci].Store(math.Float32bits(st.peak))
		}
		return
	}
	buf := p.chanBuf[ci]
	g := st.vol * st.mute
	gl, gr := g, g
	if st.pan < 0 {
		gr *= 1 + st.pan
	} else if st.pan > 0 {
		gl *= 1 - st.pan
	}
	peak := st.peak * 0.9
	chn := &p.song.Channels[ci]
	drive := chn.Drive > 0
	// tanh saturation at unity small-signal gain: quiet passages pass as they
	// are and only peaks above about 1/dk are squashed, so drive adds edge
	// without the level jump a peak-normalised curve gives everything quiet.
	dk := 1 + 9*chn.Drive
	dnorm := 1 / dk
	crush := chn.Crush > 0 && chn.Crush < 16
	steps := float32(int(1) << max(chn.Crush-1, 0))
	for f := 0; f < n; f++ {
		l, r := buf[f*2], buf[f*2+1]
		if st.filtOn {
			l, r = st.filt.process(0, l), st.filt.process(1, r)
		}
		if drive {
			l = float32(math.Tanh(float64(l*dk))) * dnorm
			r = float32(math.Tanh(float64(r*dk))) * dnorm
		}
		if crush {
			l = float32(math.Round(float64(l*steps))) / steps
			r = float32(math.Round(float64(r*steps))) / steps
		}
		l, r = l*gl, r*gr
		p.dry[f*2] += l
		p.dry[f*2+1] += r
		if st.sendA > 0 {
			p.a[f*2] += l * st.sendA
			p.a[f*2+1] += r * st.sendA
		}
		if st.sendB > 0 {
			p.b[f*2] += l * st.sendB
			p.b[f*2+1] += r * st.sendB
		}
		if a := absf(l) + absf(r); a*0.5 > peak {
			peak = a * 0.5
		}
	}
	st.peak = peak
	if ci < MaxChannels {
		p.levels[ci].Store(math.Float32bits(peak))
	}
}

// renderVoice adds n frames of one voice into buf and reports whether it ended.
func (p *Player) renderVoice(v *pvoice, buf []float32, n int, dt float32) bool {
	in, smp := v.inst, v.smp
	ended := false

	// ---- envelopes and release ----
	env := float32(1)
	if in.VolEnv.On() {
		env = in.VolEnv.Value(v.envT[0])
		v.envT[0] = in.VolEnv.Advance(v.envT[0], dt, v.released)
		if v.released && in.VolEnv.Done(v.envT[0]) && in.VolEnv.Value(v.envT[0]) <= 0.0001 {
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
		v.filt.set(in.Filter, cut, in.Resonance, p.rate)
	}

	gain := v.vol * in.Volume * smp.Volume * env * v.fade
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

	step := semitoneRatio(semis) * float64(smp.Rate) / float64(p.rate)
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
			sl = v.filt.process(0, sl)
			if stereo {
				sr = v.filt.process(1, sr)
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
