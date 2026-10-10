package tracker

// engine.go — the sound half of a song: voices, strips and buses.
//
// An Engine is what a sequencer drives. It owns every sounding voice, one
// mixer strip per line of music (a Tracker channel, a Score line), the two
// send buses — a reverb on A, a tempo-synced delay on B — and the master
// compressor, and renders them into one stereo stream in control blocks. It
// knows nothing about patterns, rows or bars: a sequencer starts and releases
// notes, sets each strip's controls, and has the engine render the frames
// between its events, so every note starts on the frame it was meant to.
//
// There are two ways to drive one. The Tracker's Player works inside the
// package and cuts its own render at every tick (mixBlock). A sequencer
// outside it queues Cues stamped with a frame offset into the next block and
// calls Render, which cuts the block at each cue itself — sample-accurate
// without the sequencer having to split anything.
//
// Like the Player, an Engine belongs to the mixer goroutine once it renders:
// nothing in it locks, and nothing on the render path allocates. The meters,
// the scope and the live-voice count are atomics the game thread may read at
// any time.

import (
	"math"
	"sync/atomic"
)

// MaxVoices caps sounding notes per engine, released tails included.
const MaxVoices = 128

// MaxStrips is how many strips an engine meters (and how many a Tracker
// song has channels).
const MaxStrips = MaxChannels

// PreviewStrip is the strip number of the preview bus: notes on it skip the
// strips and go straight to the master, as the editor's auditions do.
const PreviewStrip = -1

// controlBlock is the most frames rendered between control updates.
const controlBlock = 64

// maxCues is how many cues an engine holds between renders.
const maxCues = 1024

// rrSlots is how many (instrument, layer) round-robin counters an engine
// remembers; the least recently used is forgotten first.
const rrSlots = 64

const scopeLen = 512

// NoteID names one note-on: every voice it started (one, or two across a
// velocity crossfade) carries it. Zero is no note. A note whose voices have
// all ended or been stolen simply stops answering.
type NoteID uint64

// NoteOptions shape a note-on. The zero value is a plain full-velocity note
// in the instrument's default articulation.
type NoteOptions struct {
	// Velocity 0..1 picks the velocity layer. 0 reads as 1.
	Velocity float32
	// Articulation is 1-based into Instrument.Articulations; 0 plays the
	// instrument's own zones.
	Articulation int
	// Volume is the note's own level. 0 reads as 1. A following note takes
	// its strip's level instead.
	Volume float32
	// Pan is added to the instrument's pan (non-following notes).
	Pan float32
	// Hold releases the note by itself after this many seconds; 0 waits for
	// a NoteOff.
	Hold float32
	// Release, above 0, replaces the instrument's fade-out in seconds.
	Release float32
	// Offset 0..1 starts that far into the sample; Reverse plays it
	// backwards from there.
	Offset  float32
	Reverse bool
	// Follow ties the note to its strip's follow values (SetFollow) every
	// block — how a Tracker channel's slides and vibrato reach its note —
	// until it is detached.
	Follow bool
}

// StripControls are a strip's mixer settings.
type StripControls struct {
	Volume       float32 // linear gain
	Pan          float32 // -1 left .. 1 right
	SendA, SendB float32 // reverb and delay sends
	// CutoffHz is a low-pass over the strip; 0, or 19 kHz and up, is open.
	CutoffHz  float32
	Resonance float32 // 0..1
	// Drive (0..1) saturates the strip; Crush (0 off, else 1..16 bits)
	// reduces its bit depth. Inserts, before the sends.
	Drive float32
	Crush int
}

// stripState is a strip's controls, mute fade, filter and meter.
type stripState struct {
	mute, muteTarget, muteRate float32
	vol, pan, sendA, sendB     float32
	cutoffHz, reso             float32
	drive                      float32
	crush                      int
	filt                       svf
	filtOn                     bool
	peak                       float32
	// What following voices copy every block: the line's momentary level,
	// pitch offset (semitones) and pan.
	fVol, fPitch, fPan float32
}

func newStrip() stripState { return stripState{mute: 1, muteTarget: 1, vol: 1, fVol: 1} }

func (st *stripState) setMute(on bool, secs float32) {
	st.muteTarget = 1
	if on {
		st.muteTarget = 0
	}
	if secs <= 0.005 {
		secs = 0.005
	}
	st.muteRate = 1 / secs
}

// CueKind is what a cue does.
type CueKind uint8

const (
	// CueNoteOn starts Inst at Pitch on Strip as Note (from NewNoteID).
	CueNoteOn CueKind = iota
	// CueNoteOff releases Note.
	CueNoteOff
	// CueKill cuts Note with a declick.
	CueKill
	// CueNoteControl sets Note's Volume, Bend (semitones) and Pan.
	CueNoteControl
	// CueStrip sets Strip's Controls.
	CueStrip
	// CueMute fades Strip out (On) or back in over Secs.
	CueMute
	// CueReleaseAll releases every note.
	CueReleaseAll
)

// Cue is one engine event, At frames into the next Render.
type Cue struct {
	At    int
	Kind  CueKind
	Note  NoteID
	Strip int
	Inst  *Instrument
	Pitch float32 // note number, fractional for microtones (NoteC4 is middle C)
	Opts  NoteOptions
	// CueNoteControl.
	Volume, Bend, Pan float32
	// CueStrip.
	Controls StripControls
	// CueMute.
	On   bool
	Secs float32
}

type rrSlot struct {
	in   *Instrument
	key  int32
	n    uint32
	used uint64
}

// Engine renders voices through strips and buses. See the file comment.
type Engine struct {
	levels     [MaxStrips]atomic.Uint32
	scope      [scopeLen]atomic.Uint32
	voicesLive atomic.Int32

	// ---- mixer-owned ----
	rate   int
	voices [MaxVoices]pvoice
	age    uint64 // voice starts, for stealing the oldest
	serial uint64 // note-ons, for NoteIDs
	interp Interp // what InterpSong voices use

	strips    []stripState
	stripBuf  [][]float32
	used      []bool // strip buffer written this block
	prevBuf   []float32
	dry, a, b []float32

	reverbSpec ReverbSpec
	delaySpec  DelaySpec
	compSpec   CompressorSpec
	gain       float32
	bpm        float32
	rev        *reverb
	dly        *delay
	comp       compressor
	scopePos   int

	rr      [rrSlots]rrSlot
	rrClock uint64

	cues []Cue
}

// NewEngine returns an engine with strips strips and default buses. It
// renders at whatever rate Render is first called with.
func NewEngine(strips int) *Engine {
	e := &Engine{gain: 1, bpm: 120, interp: InterpCubic, cues: make([]Cue, 0, maxCues)}
	ns := NewSong("")
	e.reverbSpec, e.delaySpec, e.compSpec = ns.Reverb, ns.Delay, ns.Compressor
	for i := range e.voices {
		e.voices[i].strip = -2
	}
	e.SetStripCount(strips)
	return e
}

// init (re)builds the buses for a sample rate. Allocates.
func (e *Engine) init(rate int) {
	e.rate = rate
	e.rev = newReverb(rate)
	e.dly = newDelay(rate)
	e.applyBuses()
	e.ensureBuffers()
}

// SetRate prepares the engine for a sample rate. Render calls it when the
// rate changes; call it first to keep the allocation off the mixer.
func (e *Engine) SetRate(rate int) {
	if rate > 0 && rate != e.rate {
		e.init(rate)
	}
}

// Rate is the rate the engine renders at, 0 before the first render.
func (e *Engine) Rate() int { return e.rate }

// SetStripCount grows or shrinks the strips, keeping the ones that stay. New
// strips are unmuted at unity. Allocates when it grows.
func (e *Engine) SetStripCount(n int) {
	if n < 0 {
		n = 0
	}
	if n == len(e.strips) {
		return
	}
	strips := make([]stripState, n)
	copy(strips, e.strips)
	for i := len(e.strips); i < n; i++ {
		strips[i] = newStrip()
	}
	e.strips = strips
	if e.rate > 0 {
		e.ensureBuffers()
	}
}

// StripCount is how many strips the engine has.
func (e *Engine) StripCount() int { return len(e.strips) }

func (e *Engine) ensureBuffers() {
	n := controlBlock * 2
	for len(e.stripBuf) < len(e.strips) {
		e.stripBuf = append(e.stripBuf, make([]float32, n))
		e.used = append(e.used, true)
	}
	if e.prevBuf == nil {
		e.prevBuf = make([]float32, n)
		e.dry = make([]float32, n)
		e.a = make([]float32, n)
		e.b = make([]float32, n)
	}
}

// SetBuses configures the send buses, the master compressor and the master
// gain (0 reads as 1).
func (e *Engine) SetBuses(rev ReverbSpec, dly DelaySpec, comp CompressorSpec, gain float32) {
	e.reverbSpec, e.delaySpec, e.compSpec = rev, dly, comp
	if gain == 0 {
		gain = 1
	}
	e.gain = gain
	if e.rate > 0 {
		e.applyBuses()
	}
}

func (e *Engine) applyBuses() {
	e.rev.set(e.reverbSpec)
	e.dly.set(e.delaySpec, e.bpm, e.rate)
	e.comp.set(e.compSpec, e.rate)
}

// SetTempo re-syncs the delay bus to a tempo.
func (e *Engine) SetTempo(bpm float32) {
	e.bpm = bpm
	if e.dly != nil {
		e.dly.set(e.delaySpec, bpm, e.rate)
	}
}

// SetInterp sets the interpolation for instruments that leave theirs unset.
func (e *Engine) SetInterp(i Interp) {
	if i == InterpSong {
		i = InterpCubic
	}
	e.interp = i
}

// SetStrip sets a strip's controls for the coming blocks.
func (e *Engine) SetStrip(i int, c StripControls) {
	if i < 0 || i >= len(e.strips) {
		return
	}
	st := &e.strips[i]
	st.vol, st.pan, st.sendA, st.sendB = c.Volume, c.Pan, c.SendA, c.SendB
	st.cutoffHz, st.reso = c.CutoffHz, c.Resonance
	st.drive, st.crush = c.Drive, c.Crush
}

// SetFollow sets what a strip's following notes play at: a level, a pitch
// offset in semitones and a pan.
func (e *Engine) SetFollow(i int, vol, pitch, pan float32) {
	if i < 0 || i >= len(e.strips) {
		return
	}
	st := &e.strips[i]
	st.fVol, st.fPitch, st.fPan = vol, pitch, pan
}

// SetMute fades a strip out (on) or back in over secs.
func (e *Engine) SetMute(i int, on bool, secs float32) {
	if i >= 0 && i < len(e.strips) {
		e.strips[i].setMute(on, secs)
	}
}

// SetMuteNow mutes or unmutes a strip with no fade.
func (e *Engine) SetMuteNow(i int, on bool) {
	if i < 0 || i >= len(e.strips) {
		return
	}
	m := float32(1)
	if on {
		m = 0
	}
	e.strips[i].mute, e.strips[i].muteTarget = m, m
}

// NewNoteID reserves an id for a note a cue will start.
func (e *Engine) NewNoteID() NoteID {
	e.serial++
	return NoteID(e.serial)
}

// NoteOn starts a note now — at the next frame rendered — and returns its
// id, or 0 when the instrument has nothing to play for it.
func (e *Engine) NoteOn(strip int, in *Instrument, pitch float32, o NoteOptions) NoteID {
	return e.startNote(e.NewNoteID(), strip, in, pitch, o)
}

// NoteOff releases a note: it fades by its envelope or fade-out.
func (e *Engine) NoteOff(id NoteID) {
	for i := range e.voices {
		if v := &e.voices[i]; v.active && v.group == id {
			v.release()
		}
	}
}

// Kill cuts a note with a few milliseconds' declick.
func (e *Engine) Kill(id NoteID) {
	for i := range e.voices {
		if v := &e.voices[i]; v.active && v.group == id {
			v.attached = false
			v.kill()
		}
	}
}

// SetNoteControls sets a non-following note's level, pitch bend in
// semitones, and pan. The level ramps over the next block, so stepping it
// once a block is a smooth curve.
func (e *Engine) SetNoteControls(id NoteID, vol, bend, pan float32) {
	for i := range e.voices {
		if v := &e.voices[i]; v.active && v.group == id && !v.attached {
			v.vol, v.pitchOff, v.pan = vol, bend, pan
		}
	}
}

// Playing reports whether any voice of a note still sounds.
func (e *Engine) Playing(id NoteID) bool {
	for i := range e.voices {
		if v := &e.voices[i]; v.active && v.group == id {
			return true
		}
	}
	return false
}

// ReleaseAll releases every sounding note.
func (e *Engine) ReleaseAll() {
	for i := range e.voices {
		if e.voices[i].active {
			e.voices[i].release()
		}
	}
}

// ReleaseStrip releases every note on a strip.
func (e *Engine) ReleaseStrip(strip int) {
	for i := range e.voices {
		if v := &e.voices[i]; v.active && v.strip == strip {
			v.release()
		}
	}
}

// Cue queues an event at c.At frames into the next Render (later ones carry
// over to the renders after). False when the queue is full.
func (e *Engine) Cue(c Cue) bool {
	if len(e.cues) >= cap(e.cues) {
		return false
	}
	if c.At < 0 {
		c.At = 0
	}
	// Insertion keeps the queue in time order, and cues at the same frame
	// in the order they were queued.
	e.cues = append(e.cues, c)
	i := len(e.cues) - 1
	for i > 0 && e.cues[i-1].At > c.At {
		e.cues[i] = e.cues[i-1]
		i--
	}
	e.cues[i] = c
	return true
}

// PendingCues is how many cues wait for a later render.
func (e *Engine) PendingCues() int { return len(e.cues) }

func (e *Engine) applyCue(c *Cue) {
	switch c.Kind {
	case CueNoteOn:
		if c.Inst != nil {
			id := c.Note
			if id == 0 {
				id = e.NewNoteID()
			}
			e.startNote(id, c.Strip, c.Inst, c.Pitch, c.Opts)
		}
	case CueNoteOff:
		e.NoteOff(c.Note)
	case CueKill:
		e.Kill(c.Note)
	case CueNoteControl:
		e.SetNoteControls(c.Note, c.Volume, c.Bend, c.Pan)
	case CueStrip:
		e.SetStrip(c.Strip, c.Controls)
	case CueMute:
		e.SetMute(c.Strip, c.On, c.Secs)
	case CueReleaseAll:
		e.ReleaseAll()
	}
}

// Render implements the cue-driven path: frames of interleaved stereo into
// out, cut at every cue so each lands on its frame. It returns whether any
// voice is still sounding or any cue still waits.
func (e *Engine) Render(out []float32, frames, rate int) bool {
	if rate != e.rate {
		e.init(rate)
	}
	out = out[:frames*2]
	head := 0
	i := 0
	for i < frames {
		for head < len(e.cues) && e.cues[head].At <= i {
			e.applyCue(&e.cues[head])
			head++
		}
		n := min(frames-i, controlBlock)
		if head < len(e.cues) {
			if t := e.cues[head].At - i; t < n {
				n = t
			}
		}
		e.mixBlock(out[i*2:(i+n)*2], n)
		i += n
	}
	// What is left belongs to later renders.
	k := copy(e.cues, e.cues[head:])
	for j := 0; j < k; j++ {
		e.cues[j].At -= frames
	}
	e.cues = e.cues[:k]
	live := e.liveVoices()
	e.voicesLive.Store(int32(live))
	return live > 0 || k > 0
}

// StripLevel is a strip's recent peak, 0..1+, for meters.
func (e *Engine) StripLevel(i int) float32 {
	if i < 0 || i >= MaxStrips {
		return 0
	}
	return math.Float32frombits(e.levels[i].Load())
}

// Scope copies the most recent master output (mono) into dst, oldest first.
func (e *Engine) Scope(dst []float32) {
	for i := range dst {
		dst[i] = math.Float32frombits(e.scope[i%scopeLen].Load())
	}
}

// LiveVoices is how many voices were sounding at the last render.
func (e *Engine) LiveVoices() int { return int(e.voicesLive.Load()) }

func (e *Engine) liveVoices() int {
	n := 0
	for i := range e.voices {
		if e.voices[i].active {
			n++
		}
	}
	return n
}

// prepStrips moves mute fades on and sets each strip's filter for the block.
func (e *Engine) prepStrips(dt float32) {
	for ci := range e.strips {
		st := &e.strips[ci]
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
		if hz := st.cutoffHz; hz > 0 && hz < 19000 {
			if !st.filtOn {
				st.filt.reset()
			}
			st.filtOn = true
			st.filt.set(FilterLowPass, hz, st.reso, e.rate)
		} else {
			st.filtOn = false
		}
	}
}

// mixBlock renders n (at most controlBlock) frames into out: voices into
// strip buffers, strips into the dry and send buses, sends through their
// effects, then the master compressor.
func (e *Engine) mixBlock(out []float32, n int) {
	dt := float32(n) / float32(e.rate)
	nn := n * 2
	for i := range e.strips {
		if e.used[i] {
			clear(e.stripBuf[i][:nn])
			e.used[i] = false
		}
	}
	clear(e.prevBuf[:nn])
	clear(e.dry[:nn])
	clear(e.a[:nn])
	clear(e.b[:nn])

	e.prepStrips(dt)

	for i := range e.voices {
		v := &e.voices[i]
		if !v.active {
			continue
		}
		var buf []float32
		switch {
		case v.strip == PreviewStrip:
			buf = e.prevBuf
		case v.strip >= 0 && v.strip < len(e.strips):
			buf = e.stripBuf[v.strip]
		default:
			v.active = false
			continue
		}
		if v.attached {
			st := &e.strips[v.strip]
			v.vol, v.pitchOff, v.pan = st.fVol, st.fPitch, st.fPan
		}
		if v.strip >= 0 {
			e.used[v.strip] = true
		}
		if done := e.renderVoice(v, buf[:nn], n, dt); done {
			v.attached = false
			v.active = false
		}
	}

	for ci := range e.strips {
		e.mixStrip(ci, n)
	}
	for i := 0; i < nn; i++ {
		e.dry[i] += e.prevBuf[i]
	}

	revRet, dlyRet := e.reverbSpec.Return, e.delaySpec.Return
	gain := e.gain
	for f := 0; f < n; f++ {
		l, r := e.dry[f*2], e.dry[f*2+1]
		if revRet > 0 {
			wl, wr := e.rev.process(e.a[f*2], e.a[f*2+1])
			l += wl * revRet
			r += wr * revRet
		}
		if dlyRet > 0 {
			wl, wr := e.dly.process(e.b[f*2], e.b[f*2+1])
			l += wl * dlyRet
			r += wr * dlyRet
		}
		l, r = e.comp.process(l, r)
		out[f*2], out[f*2+1] = l*gain, r*gain
		if f&3 == 0 {
			e.scope[e.scopePos].Store(math.Float32bits((l + r) * 0.5 * gain))
			e.scopePos = (e.scopePos + 1) % scopeLen
		}
	}
}

// mixStrip runs one strip buffer through its inserts into the buses.
func (e *Engine) mixStrip(ci, n int) {
	st := &e.strips[ci]
	if !e.used[ci] {
		// Nothing played into this strip: let its meter fall, skip the mix.
		st.peak *= 0.9
		if ci < MaxStrips {
			e.levels[ci].Store(math.Float32bits(st.peak))
		}
		return
	}
	buf := e.stripBuf[ci]
	g := st.vol * st.mute
	gl, gr := g, g
	if st.pan < 0 {
		gr *= 1 + st.pan
	} else if st.pan > 0 {
		gl *= 1 - st.pan
	}
	peak := st.peak * 0.9
	drive := st.drive > 0
	// tanh saturation at unity small-signal gain: quiet passages pass as they
	// are and only peaks above about 1/dk are squashed, so drive adds edge
	// without the level jump a peak-normalised curve gives everything quiet.
	dk := 1 + 9*st.drive
	dnorm := 1 / dk
	crush := st.crush > 0 && st.crush < 16
	steps := float32(int(1) << max(st.crush-1, 0))
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
		e.dry[f*2] += l
		e.dry[f*2+1] += r
		if st.sendA > 0 {
			e.a[f*2] += l * st.sendA
			e.a[f*2+1] += r * st.sendA
		}
		if st.sendB > 0 {
			e.b[f*2] += l * st.sendB
			e.b[f*2+1] += r * st.sendB
		}
		if a := absf(l) + absf(r); a*0.5 > peak {
			peak = a * 0.5
		}
	}
	st.peak = peak
	if ci < MaxStrips {
		e.levels[ci].Store(math.Float32bits(peak))
	}
}

// rrNext is the round-robin turn for one (instrument, layer) of n samples,
// advancing it.
func (e *Engine) rrNext(in *Instrument, key int32, n int) int {
	if n <= 1 {
		return 0
	}
	e.rrClock++
	oldest := 0
	for i := range e.rr {
		s := &e.rr[i]
		if s.in == in && s.key == key {
			t := int(s.n % uint32(n))
			s.n++
			s.used = e.rrClock
			return t
		}
		if s.used < e.rr[oldest].used {
			oldest = i
		}
	}
	e.rr[oldest] = rrSlot{in: in, key: key, n: 1, used: e.rrClock}
	return 0
}
