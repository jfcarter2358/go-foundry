// Package play schedules a score's performance on a tracker.Engine: the
// Score's equivalent of tracker.Player, and like it an audio.Generator a
// game plays on the music bus and the editor plays for the transport.
//
// The game thread and the mixer goroutine split it the way they split a
// tracker.Player: the game thread prepares (interprets the score, resolves
// the instruments — everything that allocates) and talks to the mixer only
// through the command channel (Section, Seek, TempoScale, Mute,
// SetInstrument …), the event channel it drains (Events) and atomics it
// reads (Position, LineLevel). Render owns everything else outright.
//
// Render works in score seconds — the performance's own time, before the
// tempo slider — and maps them to frames through the tempo scale. Each
// block it walks forward to the next boundary (the block's end, a loop
// point, a section's end, a queued jump, the end of the piece), cues every
// note-on and note-off inside on the frame it falls on, and has the engine
// render the whole block once; the engine cuts the block at the cues, so
// every onset is sample-accurate. Control changes — fader, curves, params,
// mutes — are set once a block, on its boundary, where the Engine is
// cheapest (docs/score/PHASE_0.md).
package play

import (
	"fmt"
	"math"
	"sync/atomic"

	"foundry/internal/score"
	"foundry/internal/score/interpret"
	"foundry/internal/tracker"
)

// EventKind classifies what the player tells scripts.
type EventKind uint8

const (
	EventMarker EventKind = iota
	EventBeat
	EventBar
	EventSection
	EventEnd
)

// Event is posted from the mixer goroutine and drained on the game thread.
type Event struct {
	Kind EventKind
	// Name is the marker's or the section's name.
	Name string
	// Bar and Pass are the written bar and the time through it; Beat is
	// 1-based in the bar.
	Bar, Pass, Beat int
	Tick            int64
}

// Position is where the score is, as last published.
type Position struct {
	Playing, Paused bool
	Tick            int64
	// Seconds is score time at the score's own tempo.
	Seconds   float64
	Bar, Pass int
	Beat      int
	// Section indexes the performance's sections, -1 for none.
	Section int
}

type cmdKind uint8

const (
	cmdSection cmdKind = iota
	cmdParam
	cmdMute
	cmdSolo
	cmdTempoScale
	cmdProgram
	cmdPlayFrom
	cmdSeek
	cmdStop
	cmdPlay
	cmdLoop
	cmdNote
	cmdNoteOff
	cmdPause
)

type command struct {
	kind cmdKind
	name string
	i, j int
	f, g float64
	b    bool
	q    tracker.Quantize
	tick int64
	prog *Program
}

// maxActive is how many scheduled notes can sound at once; past it the one
// due to end soonest is cut.
const maxActive = 256

// tailSeconds is how long a finished score keeps rendering for its reverb
// and releases to die away.
const tailSeconds = 2.5

type activeNote struct {
	id   tracker.NoteID
	end  float64
	line int
}

type paramState struct {
	cur, target, rate float32
}

type resolvedBind struct {
	param, line int
	target      tracker.BindTarget
	from, to    float32
}

type previewKey struct {
	key  int
	note tracker.NoteID
	used bool
}

// Player plays one score.
type Player struct {
	cmds    chan command
	events  chan Event
	posTick atomic.Int64
	posSec  atomic.Uint64
	posInfo atomic.Uint64
	eng     *tracker.Engine

	// game is the program last handed to the mixer, for the game thread's
	// own reads (Score, Performance) and for instrument swaps.
	game *Program

	// ---- mixer-owned ----
	prog *Program
	rate int

	playing, paused, finished bool
	tail                      int

	sec                  float64 // score seconds at the play head
	next                 int     // next note to start
	nextBeat, nextMarker int
	section              int

	scale, scaleTarget, scaleRate float64
	engBPM                        float64

	loopOn       bool
	loopA, loopB float64

	pendingSection int
	pendingAt      float64

	active  [maxActive]activeNote
	nActive int
	keys    [16]previewKey

	params []paramState
	binds  []resolvedBind
}

// New prepares a score to play. It does not start the transport: call
// Start (before handing the player to the mixer) or PlayFrom. Instruments
// that will not load leave their lines silent and are reported.
func New(s *score.Score) (*Player, []error) {
	prog, errs, err := Prepare(s)
	if err != nil {
		return nil, append(errs, err)
	}
	return NewFromProgram(prog), errs
}

// NewFromProgram is New for a program already prepared.
func NewFromProgram(prog *Program) *Player {
	p := &Player{
		cmds:           make(chan command, 256),
		events:         make(chan Event, 256),
		eng:            tracker.NewEngine(len(prog.Score.Lines)),
		section:        -1,
		pendingSection: -1,
		scale:          1,
		scaleTarget:    1,
	}
	p.install(prog)
	p.game = prog
	p.publish()
	return p
}

// Start puts the play head at a section (or the top when name is "" or
// unknown) and starts the transport. Call before the player is handed to
// the mixer; afterwards use Section or PlayFrom.
func (p *Player) Start(section string) {
	p.playing, p.finished = true, false
	at := 0.0
	if i := p.prog.Perf.SectionIndex(section); i >= 0 {
		at = p.prog.Perf.Sections[i].Start
	}
	p.jump(0, at)
	p.publish()
}

// StartAt puts the play head at a tick and starts the transport, for save
// games.
func (p *Player) StartAt(tick int64) {
	p.playing, p.finished = true, false
	p.jump(0, p.prog.Perf.Tempo.Seconds(float64(clampTick(tick, p.prog.Perf.Length))))
	p.publish()
}

// SetParamNow sets a param before playback starts.
func (p *Player) SetParamNow(name string, v float32) {
	if i := p.prog.Score.ParamIndex(name); i >= 0 {
		p.params[i] = paramState{cur: v, target: v}
	}
}

// Score is the score being played. Read-only: the editor swaps in edits
// with SetScore.
func (p *Player) Score() *score.Score { return p.game.Score }

// Performance is the performance being played. Read-only.
func (p *Player) Performance() *interpret.Performance { return p.game.Perf }

// ---------- game-thread control ----------

func (p *Player) send(c command) bool {
	select {
	case p.cmds <- c:
		return true
	default:
		return false
	}
}

// Section queues a jump to a named section at the next q boundary (a beat,
// a bar, the end of the playing section, or now).
func (p *Player) Section(name string, q tracker.Quantize) bool {
	return p.send(command{kind: cmdSection, name: name, q: q})
}

// SetParam ramps a param to v over secs seconds.
func (p *Player) SetParam(name string, v, secs float32) bool {
	return p.send(command{kind: cmdParam, name: name, f: float64(clamp32(v, 0, 1)), g: float64(secs)})
}

// Mute fades a line (by name or 1-based number) out or back in.
func (p *Player) Mute(line string, on bool, secs float32) bool {
	i := p.game.Score.LineIndex(line)
	if i < 0 {
		return false
	}
	return p.MuteIndex(i, on, secs)
}

// MuteIndex is Mute by 0-based line index.
func (p *Player) MuteIndex(line int, on bool, secs float32) bool {
	return p.send(command{kind: cmdMute, i: line, b: on, g: float64(secs)})
}

// Solo mutes every line but one; a negative index unsolos.
func (p *Player) Solo(line int) bool { return p.send(command{kind: cmdSolo, i: line}) }

// TempoScale plays at scale times the written tempo (0.25..2), reached
// over secs seconds. The score is not changed.
func (p *Player) TempoScale(scale, secs float64) bool {
	return p.send(command{kind: cmdTempoScale, f: math.Max(0.25, math.Min(2, scale)), g: secs})
}

// SetScore swaps in an edited score, keeping the play head at the same
// tick. It interprets the score and loads its instruments here, on the
// caller's thread. The caller must not change s afterwards.
func (p *Player) SetScore(s *score.Score) ([]error, error) {
	prog, errs, err := Prepare(s)
	if err != nil {
		return errs, err
	}
	p.game = prog
	if !p.send(command{kind: cmdProgram, prog: prog}) {
		return errs, fmt.Errorf("score player: command queue full")
	}
	return errs, nil
}

// SetInstrument plays a line on another library instrument from now on —
// the notes are unchanged, and notes already sounding finish on the old one.
func (p *Player) SetInstrument(line int, name string) error {
	prog, err := p.game.WithInstrument(line, name)
	if err != nil {
		return err
	}
	p.game = prog
	if !p.send(command{kind: cmdProgram, prog: prog}) {
		return fmt.Errorf("score player: command queue full")
	}
	return nil
}

// PlayFrom starts (or moves) the transport at a tick.
func (p *Player) PlayFrom(tick int64) bool { return p.send(command{kind: cmdPlayFrom, tick: tick}) }

// MoveTo moves the play head to a tick without starting or stopping.
func (p *Player) MoveTo(tick int64) bool { return p.send(command{kind: cmdSeek, tick: tick}) }

// Play starts the transport from the play head.
func (p *Player) Play() bool { return p.send(command{kind: cmdPlay}) }

// StopTransport halts the score where it is; notes release. Preview notes
// still play.
func (p *Player) StopTransport() bool { return p.send(command{kind: cmdStop}) }

// SetLoop loops playback between two ticks; from >= to clears the loop.
func (p *Player) SetLoop(from, to int64) bool {
	return p.send(command{kind: cmdLoop, tick: from, i: int(to)})
}

// ClearLoop stops looping a region.
func (p *Player) ClearLoop() bool { return p.SetLoop(0, 0) }

// Note plays a MIDI note on a line's instrument outside the score, keyed so
// NoteOff can release it — the editor's audition.
func (p *Player) Note(key, line, midi int, velocity float32) bool {
	return p.send(command{kind: cmdNote, i: key, j: line, f: float64(midi), g: float64(velocity)})
}

// NoteFor plays a note outside the score that releases itself after secs.
func (p *Player) NoteFor(line, midi int, velocity, secs float32) bool {
	if secs <= 0 {
		secs = 0.5
	}
	return p.send(command{kind: cmdNote, i: -1, j: line, f: float64(midi), g: float64(velocity), tick: int64(math.Float32bits(secs))})
}

// NoteOff releases a note started with Note.
func (p *Player) NoteOff(key int) bool { return p.send(command{kind: cmdNoteOff, i: key}) }

// Pause freezes the score where it is, silent; false resumes it.
func (p *Player) Pause(on bool) bool { return p.send(command{kind: cmdPause, b: on}) }

// Events drains what the player has posted since the last call into dst.
func (p *Player) Events(dst []Event) []Event {
	for {
		select {
		case e := <-p.events:
			dst = append(dst, e)
		default:
			return dst
		}
	}
}

// Position reads the last published position.
func (p *Player) Position() Position {
	info := p.posInfo.Load()
	return Position{
		Playing: info&1 != 0,
		Paused:  info&2 != 0,
		Section: int((info>>2)&0xFFF) - 1,
		Beat:    int((info >> 14) & 0xFF),
		Pass:    int((info >> 22) & 0xFF),
		Bar:     int((info >> 30) & 0xFFFFF),
		Tick:    p.posTick.Load(),
		Seconds: math.Float64frombits(p.posSec.Load()),
	}
}

// LineLevel is a line's recent peak, 0..1+, for meters.
func (p *Player) LineLevel(line int) float32 { return p.eng.StripLevel(line) }

// Scope copies the most recent master output (mono) into dst, oldest first.
func (p *Player) Scope(dst []float32) { p.eng.Scope(dst) }

// LiveVoices is how many voices are sounding.
func (p *Player) LiveVoices() int { return p.eng.LiveVoices() }

// ---------- mixer side ----------

func (p *Player) publish() {
	perf := p.prog.Perf
	tick := perf.Tempo.Tick(p.sec)
	var info uint64
	if p.playing {
		info |= 1
	}
	if p.paused {
		info |= 2
	}
	info |= uint64(p.section+1) & 0xFFF << 2
	if len(perf.Bars) > 0 {
		b := perf.Bars[perf.BarAt(tick)]
		beat := 0
		if k := perf.BeatAt(tick); k >= 0 {
			beat = perf.Beats[k].Beat
		}
		info |= uint64(beat&0xFF) << 14
		info |= uint64(b.Pass&0xFF) << 22
		info |= uint64(b.Bar&0xFFFFF) << 30
	}
	p.posTick.Store(int64(math.Round(tick)))
	p.posSec.Store(math.Float64bits(p.sec))
	p.posInfo.Store(info)
}

func (p *Player) post(e Event) {
	select {
	case p.events <- e:
	default:
	}
}

// install adopts a program: strips, buses, params, bindings. Allocates, so
// it runs on the game thread for New and only for editor swaps on the
// mixer side.
func (p *Player) install(prog *Program) {
	old := p.prog
	p.prog = prog
	s := prog.Score
	p.eng.SetStripCount(len(s.Lines))
	for i, l := range s.Lines {
		if old == nil || i >= len(old.Score.Lines) || old.Score.Lines[i].Mute != l.Mute || old.Score.Lines[i].Solo != l.Solo {
			p.eng.SetMuteNow(i, p.scoreMute(i))
		}
	}
	p.eng.SetBuses(prog.reverb, prog.delay, prog.comp, s.Gain)
	params := make([]paramState, len(s.Params))
	for i, prm := range s.Params {
		params[i] = paramState{cur: prm.Default, target: prm.Default}
		if old != nil {
			if j := old.Score.ParamIndex(prm.Name); j >= 0 && j < len(p.params) {
				params[i] = p.params[j]
			}
		}
	}
	p.params = params
	p.binds = p.binds[:0]
	for _, b := range s.Bindings {
		pi, li := s.ParamIndex(b.Param), s.LineIndex(b.Line)
		if pi < 0 || li < 0 {
			continue
		}
		p.binds = append(p.binds, resolvedBind{param: pi, line: li, target: b.Target, from: b.From, to: b.To})
	}
	if old != nil {
		// Keep the play head at the same tick of the new performance.
		tick := old.Perf.Tempo.Tick(p.sec)
		at := prog.Perf.Tempo.Seconds(math.Min(tick, float64(prog.Perf.Length)))
		p.reindex(at)
		for k := 0; k < p.nActive; k++ {
			if p.active[k].line >= len(s.Lines) {
				p.eng.Kill(p.active[k].id)
			}
		}
	}
}

// scoreMute is whether the score itself mutes a line: its own mute, or
// another line's solo.
func (p *Player) scoreMute(i int) bool {
	lines := p.prog.Score.Lines
	if i < 0 || i >= len(lines) {
		return false
	}
	if lines[i].Mute {
		return true
	}
	for _, l := range lines {
		if l.Solo {
			return !lines[i].Solo
		}
	}
	return false
}

func (p *Player) drain() {
	for {
		select {
		case c := <-p.cmds:
			p.apply(c)
		default:
			return
		}
	}
}

func (p *Player) apply(c command) {
	perf := p.prog.Perf
	switch c.kind {
	case cmdSection:
		i := perf.SectionIndex(c.name)
		if i < 0 {
			return
		}
		if c.q == tracker.QuantNow || !p.playing {
			p.jump(0, perf.Sections[i].Start)
			p.playing, p.finished = true, false
			return
		}
		p.pendingSection, p.pendingAt = i, p.quantized(c.q)
	case cmdParam:
		i := p.prog.Score.ParamIndex(c.name)
		if i < 0 {
			return
		}
		ps := &p.params[i]
		ps.target = float32(c.f)
		if c.g <= 0 {
			ps.cur, ps.rate = ps.target, 0
		} else {
			ps.rate = absf(ps.target-ps.cur) / float32(c.g)
		}
	case cmdMute:
		p.eng.SetMute(c.i, c.b, float32(c.g))
	case cmdSolo:
		if c.i < 0 {
			// Unsolo: back to the score's own mutes and solos.
			for i := 0; i < p.eng.StripCount(); i++ {
				p.eng.SetMute(i, p.scoreMute(i), 0.01)
			}
			break
		}
		for i := 0; i < p.eng.StripCount(); i++ {
			p.eng.SetMute(i, i != c.i, 0.01)
		}
	case cmdTempoScale:
		p.scaleTarget = c.f
		if c.g <= 0 {
			p.scale, p.scaleRate = c.f, 0
		} else {
			p.scaleRate = math.Abs(p.scaleTarget-p.scale) / c.g
		}
	case cmdProgram:
		p.install(c.prog)
	case cmdPlayFrom:
		p.jump(0, perf.Tempo.Seconds(float64(clampTick(c.tick, perf.Length))))
		p.playing, p.finished = true, false
	case cmdSeek:
		p.jump(0, perf.Tempo.Seconds(float64(clampTick(c.tick, perf.Length))))
		if p.sec < perf.Seconds {
			p.finished = false
		}
	case cmdPlay:
		if p.sec >= perf.Seconds {
			p.jump(0, 0)
		}
		p.playing, p.finished = true, false
	case cmdStop:
		p.playing = false
		p.pendingSection = -1
		p.releaseAll(0)
	case cmdLoop:
		a, b := clampTick(c.tick, perf.Length), clampTick(int64(c.i), perf.Length)
		p.loopOn = a < b
		if p.loopOn {
			p.loopA, p.loopB = perf.Tempo.Seconds(float64(a)), perf.Tempo.Seconds(float64(b))
		}
	case cmdNote:
		p.previewNote(c)
	case cmdNoteOff:
		p.previewOff(c.i)
	case cmdPause:
		p.paused = c.b
	}
	p.publish()
}

// quantized is when a change asked for now lands: the next beat or bar,
// the end of the playing section, or the end of the piece.
func (p *Player) quantized(q tracker.Quantize) float64 {
	perf := p.prog.Perf
	const eps = 1e-9
	switch q {
	case tracker.QuantSection:
		if p.section >= 0 {
			return perf.Sections[p.section].EndSec
		}
		return perf.Seconds
	case tracker.QuantBar, tracker.QuantPattern:
		for _, b := range perf.Bars {
			if b.Start > p.sec+eps {
				return b.Start
			}
		}
		return perf.Seconds
	}
	for k := max(p.nextBeat-1, 0); k < len(perf.Beats); k++ {
		if perf.Beats[k].Start > p.sec+eps {
			return perf.Beats[k].Start
		}
	}
	return perf.Seconds
}

// reindex points the scheduler at a time without touching what sounds.
func (p *Player) reindex(sec float64) {
	perf := p.prog.Perf
	p.sec = sec
	p.next = perf.FirstNoteAt(sec)
	p.nextBeat = searchStart(len(perf.Beats), func(i int) float64 { return perf.Beats[i].Start }, sec)
	p.nextMarker = searchStart(len(perf.Markers), func(i int) float64 { return perf.Markers[i].Start }, sec)
	p.section = perf.SectionAt(perf.Tempo.Tick(sec) + 1e-6)
	if p.pendingSection >= len(perf.Sections) {
		p.pendingSection = -1
	}
}

// jump moves the play head, releasing what sounds at frame at, and tells
// scripts the section it lands in.
func (p *Player) jump(at int, sec float64) {
	p.releaseAll(at)
	p.reindex(sec)
	p.pendingSection = -1
	if p.section >= 0 {
		sp := p.prog.Perf.Sections[p.section]
		if math.Abs(sp.Start-sec) < 1e-9 {
			p.post(Event{Kind: EventSection, Name: sp.Name, Tick: sp.Tick})
		}
	}
}

func searchStart(n int, at func(int) float64, sec float64) int {
	lo, hi := 0, n
	for lo < hi {
		m := (lo + hi) / 2
		if at(m) < sec {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo
}

// Render implements audio.Generator.
func (p *Player) Render(dst []float32, frames, rate int) bool {
	if p.rate != rate {
		p.rate = rate
		p.eng.SetRate(rate)
	}
	p.drain()
	out := dst[:frames*2]
	if p.paused {
		clear(out)
		return true
	}
	p.rampControls(float64(frames) / float64(rate))
	if p.playing {
		p.schedule(frames)
	}
	more := p.eng.Render(out, frames, rate)
	p.publish()
	if p.finished && !p.playing {
		p.tail -= frames
		if p.tail <= 0 && !more {
			return false
		}
	}
	return true
}

type action uint8

const (
	actNone action = iota
	actEnd
	actSection
	actJump
	actLoop
)

// schedule cues one block's notes and moves the play head through it,
// taking every jump that falls inside.
func (p *Player) schedule(frames int) {
	perf := p.prog.Perf
	rate := float64(p.rate)
	fpos := 0.0
	for guard := 0; p.playing && fpos < float64(frames) && guard < 256; guard++ {
		bound := p.sec + (float64(frames)-fpos)/rate*p.scale
		act := actNone
		// The earliest boundary wins; of equal ones the later check does.
		if perf.Seconds <= bound {
			bound, act = perf.Seconds, actEnd
		}
		if p.section >= 0 {
			sp := &perf.Sections[p.section]
			if (sp.Loop || sp.Next >= 0) && sp.EndSec <= bound && p.sec < sp.EndSec {
				bound, act = sp.EndSec, actSection
			}
		}
		if p.pendingSection >= 0 && p.pendingAt <= bound {
			bound, act = math.Max(p.pendingAt, p.sec), actJump
		}
		if p.loopOn && p.sec < p.loopB && p.loopB <= bound {
			bound, act = p.loopB, actLoop
		}
		p.cueOffs(bound, fpos)
		p.cueOns(bound, fpos)
		p.cueOffs(bound, fpos)
		p.postTimed(bound)
		fpos += (bound - p.sec) / p.scale * rate
		p.sec = bound
		at := int(math.Round(fpos))
		switch act {
		case actEnd:
			if p.prog.Score.Loop {
				p.jump(at, 0)
				continue
			}
			p.releaseAll(at)
			p.playing, p.finished = false, true
			p.tail = int(tailSeconds * rate)
			p.post(Event{Kind: EventEnd, Tick: perf.Length})
		case actSection:
			sp := perf.Sections[p.section]
			to := sp.Start
			if !sp.Loop {
				to = perf.Sections[sp.Next].Start
			}
			p.jump(at, to)
		case actJump:
			p.jump(at, perf.Sections[p.pendingSection].Start)
		case actLoop:
			p.jump(at, p.loopA)
		}
	}
}

// frameAt is where a score time falls in the block.
func (p *Player) frameAt(sec, fpos float64) int {
	return int(math.Round(fpos + (sec-p.sec)/p.scale*float64(p.rate)))
}

// cueOffs releases every scheduled note that ends before bound.
func (p *Player) cueOffs(bound, fpos float64) {
	for k := 0; k < p.nActive; {
		a := &p.active[k]
		if a.end >= bound {
			k++
			continue
		}
		p.cue(tracker.Cue{At: p.frameAt(math.Max(a.end, p.sec), fpos), Kind: tracker.CueNoteOff, Note: a.id})
		p.nActive--
		p.active[k] = p.active[p.nActive]
	}
}

// cueOns starts every note that begins before bound.
func (p *Player) cueOns(bound, fpos float64) {
	prog := p.prog
	notes := prog.Perf.Notes
	for p.next < len(notes) && notes[p.next].Start < bound {
		i := p.next
		p.next++
		n := &notes[i]
		in := prog.insts[n.Line]
		if in == nil || n.Start < p.sec-1e-9 {
			continue
		}
		at := p.frameAt(n.Start, fpos)
		if p.nActive == maxActive {
			// Out of room: cut the note due to end soonest.
			k := 0
			for j := 1; j < p.nActive; j++ {
				if p.active[j].end < p.active[k].end {
					k = j
				}
			}
			p.cue(tracker.Cue{At: at, Kind: tracker.CueKill, Note: p.active[k].id})
			p.nActive--
			p.active[k] = p.active[p.nActive]
		}
		id := p.eng.NewNoteID()
		p.cue(tracker.Cue{At: at, Kind: tracker.CueNoteOn, Note: id, Strip: n.Line, Inst: in, Pitch: EnginePitch(n.MIDI, n.Cents),
			Opts: tracker.NoteOptions{Velocity: n.Velocity, Volume: VelocityGain(n.Velocity), Articulation: prog.arts[i], Release: prog.release[n.Line]}})
		p.active[p.nActive] = activeNote{id: id, end: n.End, line: n.Line}
		p.nActive++
	}
}

// cue queues an engine event, or — with the queue full, which a block
// would need over a thousand events to do — applies it at the block's
// start rather than lose it.
func (p *Player) cue(c tracker.Cue) {
	if p.eng.Cue(c) {
		return
	}
	switch c.Kind {
	case tracker.CueNoteOn:
		p.eng.NoteOn(c.Strip, c.Inst, c.Pitch, c.Opts)
	case tracker.CueNoteOff:
		p.eng.NoteOff(c.Note)
	case tracker.CueKill:
		p.eng.Kill(c.Note)
	}
}

// releaseAll releases every scheduled note at frame at.
func (p *Player) releaseAll(at int) {
	for k := 0; k < p.nActive; k++ {
		p.cue(tracker.Cue{At: at, Kind: tracker.CueNoteOff, Note: p.active[k].id})
	}
	p.nActive = 0
}

// postTimed tells scripts about the beats, bars, sections and markers
// before bound.
func (p *Player) postTimed(bound float64) {
	perf := p.prog.Perf
	for p.nextBeat < len(perf.Beats) && perf.Beats[p.nextBeat].Start < bound {
		bt := perf.Beats[p.nextBeat]
		p.nextBeat++
		b := perf.Bars[bt.Bar]
		if bt.Beat == 1 || bt.Tick == b.Tick {
			for k, sp := range perf.Sections {
				if sp.Tick == b.Tick && k != p.section {
					p.section = k
					p.post(Event{Kind: EventSection, Name: sp.Name, Bar: b.Bar, Pass: b.Pass, Tick: sp.Tick})
				}
			}
			p.post(Event{Kind: EventBar, Bar: b.Bar, Pass: b.Pass, Beat: bt.Beat, Tick: b.Tick})
		}
		p.post(Event{Kind: EventBeat, Bar: b.Bar, Pass: b.Pass, Beat: bt.Beat, Tick: bt.Tick})
	}
	for p.nextMarker < len(perf.Markers) && perf.Markers[p.nextMarker].Start < bound {
		mk := perf.Markers[p.nextMarker]
		p.nextMarker++
		b := perf.Bars[perf.BarAt(float64(mk.Tick))]
		p.post(Event{Kind: EventMarker, Name: mk.Name, Bar: b.Bar, Pass: b.Pass, Tick: mk.Tick})
	}
}

// rampControls moves the tempo scale and the params toward their targets
// and sets every line's strip for the block: fader, curves, bindings.
func (p *Player) rampControls(dt float64) {
	if p.scale != p.scaleTarget {
		step := p.scaleRate * dt
		if p.scaleRate == 0 || math.Abs(p.scaleTarget-p.scale) <= step {
			p.scale = p.scaleTarget
		} else if p.scaleTarget > p.scale {
			p.scale += step
		} else {
			p.scale -= step
		}
	}
	for i := range p.params {
		ps := &p.params[i]
		if ps.cur != ps.target {
			step := ps.rate * float32(dt)
			if ps.rate == 0 || absf(ps.target-ps.cur) <= step {
				ps.cur = ps.target
			} else if ps.target > ps.cur {
				ps.cur += step
			} else {
				ps.cur -= step
			}
		}
	}
	prog := p.prog
	tick := prog.Perf.Tempo.Tick(p.sec)
	if bpm := prog.Perf.Tempo.QPM(tick) * p.scale; math.Abs(bpm-p.engBPM) > 0.5 {
		p.engBPM = bpm
		p.eng.SetTempo(float32(bpm))
	}
	for li, l := range prog.Score.Lines {
		c := tracker.StripControls{Volume: l.Volume, Pan: l.Pan, SendA: l.SendA, SendB: l.SendB}
		if k := prog.volCurve[li]; k >= 0 {
			c.Volume *= prog.Perf.Curves[k].At(tick)
		}
		if k := prog.panCurve[li]; k >= 0 {
			c.Pan = clamp32(prog.Perf.Curves[k].At(tick), -1, 1)
		}
		for _, b := range p.binds {
			if b.line != li {
				continue
			}
			v := p.params[b.param].cur
			lin := b.from + (b.to-b.from)*v
			switch b.target {
			case tracker.BindVolume:
				c.Volume *= lin
			case tracker.BindPan:
				c.Pan = clamp32(c.Pan+lin, -1, 1)
			case tracker.BindSendA:
				c.SendA = lin
			case tracker.BindSendB:
				c.SendB = lin
			case tracker.BindCutoff:
				from, to := max(b.from, 16), max(b.to, 16)
				hz := from * float32(math.Pow(float64(to/from), float64(v)))
				if c.CutoffHz == 0 || hz < c.CutoffHz {
					c.CutoffHz = hz
				}
			}
		}
		p.eng.SetStrip(li, c)
	}
}

func (p *Player) previewNote(c command) {
	line := c.j
	if line < 0 || line >= len(p.prog.insts) || p.prog.insts[line] == nil {
		return
	}
	in := p.prog.insts[line]
	vel := float32(c.g)
	o := tracker.NoteOptions{Velocity: vel, Volume: VelocityGain(vel), Release: p.prog.release[line]}
	if c.i < 0 {
		o.Hold = math.Float32frombits(uint32(c.tick))
		p.eng.NoteOn(line, in, EnginePitch(int(c.f), 0), o)
		return
	}
	p.previewOff(c.i)
	slot := -1
	for k := range p.keys {
		if !p.keys[k].used {
			slot = k
			break
		}
	}
	if slot < 0 {
		return
	}
	p.keys[slot] = previewKey{key: c.i, note: p.eng.NoteOn(line, in, EnginePitch(int(c.f), 0), o), used: true}
}

func (p *Player) previewOff(key int) {
	for k := range p.keys {
		if p.keys[k].used && p.keys[k].key == key {
			p.eng.NoteOff(p.keys[k].note)
			p.keys[k].used = false
		}
	}
}

// EnginePitch is a MIDI note (plus cents) as the engine's note number
// (tracker.NoteC4 is MIDI 60).
func EnginePitch(midi int, cents float32) float32 {
	return float32(midi-60+int(tracker.NoteC4)) + cents/100
}

// VelocityGain is the level a velocity plays at: velocity^1.5, so mf
// (80) is about -6 dB and pp (32) about -18 dB below full.
func VelocityGain(v float32) float32 {
	if v <= 0 {
		return 0.001
	}
	return float32(math.Pow(float64(v), 1.5))
}

func clampTick(t, length int64) int64 {
	if t < 0 {
		return 0
	}
	if t > length {
		return length
	}
	return t
}

func clamp32(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
