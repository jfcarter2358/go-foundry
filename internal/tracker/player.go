package tracker

// player.go — the sequencer's state and the game thread's handle on it.
//
// A Player is an audio.Generator. Its Render runs on the mixer goroutine and
// owns every field below the "mixer-owned" line outright; the game thread
// reaches it only through the command channel (Section, SetParam, Mute, …),
// the event channel it drains (Events), and a few atomics it reads (Position,
// ChannelLevel). That is the same split internal/audio's mixer uses, for the
// same reason: nothing the game thread does can stall the audio.
//
// Render processes in control blocks of at most controlBlock frames, cut short
// at every tick boundary, so effects land exactly on their tick and envelopes
// and parameter ramps update often enough to be smooth.

import (
	"math"
	"sync/atomic"
)

// MaxVoices caps sounding notes per song, released tails included.
const MaxVoices = 128

// controlBlock is the most frames rendered between control updates.
const controlBlock = 64

// Quantize says when a requested change takes effect.
type Quantize uint8

const (
	QuantBeat Quantize = iota
	QuantNow
	QuantBar
	QuantPattern
	// QuantSection waits for the playing section to finish.
	QuantSection
)

// ParseQuantize reads a script's spelling; unknown words read as a beat.
func ParseQuantize(s string) Quantize {
	switch s {
	case "now":
		return QuantNow
	case "bar":
		return QuantBar
	case "pattern":
		return QuantPattern
	case "section", "end":
		return QuantSection
	}
	return QuantBeat
}

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
	Kind    EventKind
	Name    string // marker or section name
	Value   int    // marker number
	Order   int
	Row     int
	Section string
}

// Position is where the song is, as last published.
type Position struct {
	Playing bool
	Section int // -1 when the song has no sections
	Order   int
	Row     int
}

type cmdKind uint8

const (
	cmdSection cmdKind = iota
	cmdParam
	cmdMute
	cmdTempo
	cmdStinger
	cmdSetSong
	cmdPlayFrom
	cmdStop
	cmdNote
	cmdNoteOff
	cmdSolo
	cmdPause
)

type command struct {
	kind   cmdKind
	name   string
	i, j   int
	f, f2  float32
	b      bool
	q      Quantize
	song   *Song
	player *Player
	hold   float32
}

type paramState struct {
	cur, target, rate float32
}

type resolvedBind struct {
	param, ch int
	target    BindTarget
	from, to  float32
}

type stingerReq struct {
	p *Player
	q Quantize
}

// Player plays one song.
type Player struct {
	cmds   chan command
	events chan Event
	pos    atomic.Uint64
	levels [MaxChannels]atomic.Uint32
	scope  [scopeLen]atomic.Uint32
	// voicesLive is published for the editor's CPU/voice readout.
	voicesLive atomic.Int32

	// ---- mixer-owned ----
	song  *Song
	rate  int
	child bool // a stinger, mixed by its parent

	playing    bool
	paused     bool
	finished   bool
	tailFrames int
	order, row int
	tick       int
	tickRemain float64
	section    int

	bpm, bpmTarget, bpmRate float32

	jumpOrder, breakRow int
	loopStart, loopLeft int
	loopJump            bool

	pendingSection int
	pendingQuant   Quantize

	chans   []chanState
	preview chanState
	keys    [16]previewKey
	voices  [MaxVoices]pvoice
	age     uint64

	params []paramState
	binds  []resolvedBind
	strip  []stripState

	rev  *reverb
	dly  *delay
	comp compressor

	chanBuf   [][]float32
	used      []bool // channel buffer written this block
	prevBuf   []float32
	dry, a, b []float32

	stingers   []*Player
	pending    []stingerReq
	stingerBuf []float32
	scopePos   int
	rng        uint64
}

type previewKey struct {
	key   int
	voice int
	used  bool
}

const scopeLen = 512

// NewPlayer prepares a song to play. It does not start the transport; call
// Start, or hand the player to audio.PlayGenerator after Start.
func NewPlayer(s *Song) *Player {
	p := &Player{
		cmds:           make(chan command, 256),
		events:         make(chan Event, 256),
		section:        -1,
		pendingSection: -1,
		jumpOrder:      -1,
		breakRow:       -1,
		rng:            0x9E3779B97F4A7C15,
		stingers:       make([]*Player, 0, 4),
		pending:        make([]stingerReq, 0, 4),
	}
	p.installSong(s)
	for i := range p.voices {
		p.voices[i].ch = -2
	}
	return p
}

// Start puts the transport at a section (or the top when name is "" or
// unknown) and starts it. Call before the player is handed to the mixer; once
// it is playing, use Section or PlayFrom.
func (p *Player) Start(section string) {
	p.playing = true
	p.finished = false
	p.order, p.row, p.tick, p.tickRemain = 0, 0, 0, 0
	p.section = -1
	if i := p.song.SectionIndex(section); i >= 0 {
		p.section = i
		p.order = p.song.Sections[i].Start
	} else if len(p.song.Sections) > 0 {
		p.section = p.sectionAt(0)
	}
	p.publish()
}

// StartAt puts the transport at an order position and row, for save games.
func (p *Player) StartAt(order, row int) {
	p.Start("")
	p.order = clampInt(order, 0, len(p.song.Order)-1)
	p.row = clampInt(row, 0, p.pattern().Rows()-1)
	p.section = p.sectionAt(p.order)
	p.publish()
}

// SetParamNow sets a param before playback starts (save games, level YAML).
func (p *Player) SetParamNow(name string, v float32) {
	if i := p.song.ParamIndex(name); i >= 0 {
		p.params[i] = paramState{cur: v, target: v}
	}
}

// Song returns the song being played. The game thread must treat it as
// read-only; the editor swaps in edits with SetSong.
func (p *Player) Song() *Song { return p.song }

// ---------- game-thread control ----------

func (p *Player) send(c command) bool {
	select {
	case p.cmds <- c:
		return true
	default:
		return false
	}
}

// Section queues a jump to a named section at the next q boundary.
func (p *Player) Section(name string, q Quantize) bool {
	return p.send(command{kind: cmdSection, name: name, q: q})
}

// SetParam ramps a param to v over secs seconds.
func (p *Player) SetParam(name string, v, secs float32) bool {
	return p.send(command{kind: cmdParam, name: name, f: clamp32(v, 0, 1), f2: secs})
}

// Mute fades a channel (by name or 1-based number) out or back in.
func (p *Player) Mute(channel string, on bool, secs float32) bool {
	return p.send(command{kind: cmdMute, name: channel, b: on, f2: secs})
}

// Solo mutes every channel but one; a negative index unsolos.
func (p *Player) Solo(ch int) bool { return p.send(command{kind: cmdSolo, i: ch}) }

// MuteIndex is Mute by 0-based channel index, for the editor.
func (p *Player) MuteIndex(ch int, on bool) bool {
	return p.send(command{kind: cmdMute, i: ch, name: "", b: on, f2: 0.01})
}

// Tempo ramps the tempo to bpm over secs seconds.
func (p *Player) Tempo(bpm, secs float32) bool {
	return p.send(command{kind: cmdTempo, f: bpm, f2: secs})
}

// Stinger plays another song over this one, starting on the next q boundary.
// The stinger does not loop.
func (p *Player) Stinger(s *Song, q Quantize) bool {
	c := s.Clone()
	c.Loop = false
	sp := NewPlayer(c)
	sp.child = true
	sp.Start("")
	return p.send(command{kind: cmdStinger, player: sp, q: q})
}

// SetSong swaps in an edited copy of the song, keeping the position. The
// caller must not touch s afterwards.
func (p *Player) SetSong(s *Song) bool { return p.send(command{kind: cmdSetSong, song: s}) }

// PlayFrom starts (or moves) the transport to an order position and row.
func (p *Player) PlayFrom(order, row int) bool {
	return p.send(command{kind: cmdPlayFrom, i: order, j: row})
}

// StopTransport halts the song; notes release. Preview notes still play.
func (p *Player) StopTransport() bool { return p.send(command{kind: cmdStop}) }

// Note plays instrument instr (1-based) at note, outside the song, keyed so
// NoteOff can release it — the editor's audition and a script's play_note.
func (p *Player) Note(key int, instr uint8, note uint8, vol float32) bool {
	return p.send(command{kind: cmdNote, i: key, j: int(instr), f: vol, f2: float32(note)})
}

// NoteFor plays a note outside the song that releases itself after secs —
// a script's play_note, which has no key-up to wait for.
func (p *Player) NoteFor(instr uint8, note uint8, vol, secs float32) bool {
	if secs <= 0 {
		secs = 0.5
	}
	return p.send(command{kind: cmdNote, i: -1, j: int(instr), f: vol, f2: float32(note), hold: secs})
}

// Pause freezes the song where it is, silent; false resumes it. The level's
// hard pause drives this, so a paused game is quiet and picks up mid-phrase.
func (p *Player) Pause(on bool) bool { return p.send(command{kind: cmdPause, b: on}) }

// NoteOff releases a note started with Note.
func (p *Player) NoteOff(key int) bool { return p.send(command{kind: cmdNoteOff, i: key}) }

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
	v := p.pos.Load()
	return Position{
		Playing: v&1 != 0,
		Section: int((v>>1)&0x7FFF) - 1,
		Order:   int((v >> 16) & 0xFFFF),
		Row:     int((v >> 32) & 0xFFFF),
	}
}

// ChannelLevel is a channel's recent peak, 0..1+, for meters.
func (p *Player) ChannelLevel(ch int) float32 {
	if ch < 0 || ch >= MaxChannels {
		return 0
	}
	return math.Float32frombits(p.levels[ch].Load())
}

// Scope copies the most recent master output (mono) into dst, oldest first.
func (p *Player) Scope(dst []float32) {
	for i := range dst {
		dst[i] = math.Float32frombits(p.scope[i%scopeLen].Load())
	}
}

// LiveVoices is how many notes are sounding.
func (p *Player) LiveVoices() int { return int(p.voicesLive.Load()) }

// ---------- mixer side ----------

func (p *Player) publish() {
	var v uint64
	if p.playing {
		v |= 1
	}
	v |= uint64(p.section+1) & 0x7FFF << 1
	v |= uint64(p.order&0xFFFF) << 16
	v |= uint64(p.row&0xFFFF) << 32
	p.pos.Store(v)
}

func (p *Player) post(e Event) {
	select {
	case p.events <- e:
	default:
	}
}

// installSong adopts a song: channel state, params, bindings. Allocates, so
// it runs on the game thread for NewPlayer and only for editor swaps on the
// mixer side.
func (p *Player) installSong(s *Song) {
	old := p.song
	p.song = s
	if p.bpm == 0 || old == nil || old.BPM != s.BPM {
		p.bpm, p.bpmTarget = s.BPM, s.BPM
	}
	if len(p.chans) != len(s.Channels) {
		chans := make([]chanState, len(s.Channels))
		copy(chans, p.chans)
		for i := len(p.chans); i < len(chans); i++ {
			chans[i] = newChanState()
		}
		p.chans = chans
		strip := make([]stripState, len(s.Channels))
		copy(strip, p.strip)
		for i := len(p.strip); i < len(strip); i++ {
			strip[i] = stripState{mute: 1, muteTarget: 1}
		}
		p.strip = strip
	}
	for i, ch := range s.Channels {
		m := float32(1)
		if ch.Mute {
			m = 0
		}
		if old == nil || i >= len(old.Channels) || old.Channels[i].Mute != ch.Mute {
			p.strip[i].mute, p.strip[i].muteTarget = m, m
		}
	}
	params := make([]paramState, len(s.Params))
	for i, prm := range s.Params {
		params[i] = paramState{cur: prm.Default, target: prm.Default}
		if old != nil {
			if j := old.ParamIndex(prm.Name); j >= 0 && j < len(p.params) {
				params[i] = p.params[j]
			}
		}
	}
	p.params = params
	p.binds = p.binds[:0]
	for _, b := range s.Bindings {
		pi, ci := s.ParamIndex(b.Param), s.ChannelIndex(b.Channel)
		if pi < 0 || ci < 0 {
			continue
		}
		p.binds = append(p.binds, resolvedBind{param: pi, ch: ci, target: b.Target, from: b.From, to: b.To})
	}
	if p.order >= len(s.Order) {
		p.order = 0
	}
	if p.row >= p.pattern().Rows() {
		p.row = 0
	}
	if p.section >= len(s.Sections) {
		p.section = -1
	}
	if p.rate > 0 {
		p.configureFX()
		p.ensureBuffers()
	}
}

func (p *Player) configureFX() {
	p.rev.set(p.song.Reverb)
	p.dly.set(p.song.Delay, p.bpm, p.rate)
	p.comp.set(p.song.Compressor, p.rate)
}

func (p *Player) ensureBuffers() {
	n := controlBlock * 2
	for len(p.chanBuf) < len(p.chans) {
		p.chanBuf = append(p.chanBuf, make([]float32, n))
		p.used = append(p.used, true)
	}
	if p.prevBuf == nil {
		p.prevBuf = make([]float32, n)
		p.dry = make([]float32, n)
		p.a = make([]float32, n)
		p.b = make([]float32, n)
	}
}

func (p *Player) init(rate int) {
	p.rate = rate
	p.rev = newReverb(rate)
	p.dly = newDelay(rate)
	p.configureFX()
	p.ensureBuffers()
}

func (p *Player) pattern() *Pattern {
	s := p.song
	if p.order < 0 || p.order >= len(s.Order) {
		return s.Patterns[0]
	}
	return s.Patterns[s.Order[p.order]]
}

// sectionAt is the section containing an order position, or -1.
func (p *Player) sectionAt(order int) int {
	for i, sec := range p.song.Sections {
		if order >= sec.Start && order < sec.End {
			return i
		}
	}
	return -1
}

func (p *Player) samplesPerTick() float64 {
	bpm := float64(p.bpm)
	if bpm < 16 {
		bpm = 16
	}
	return float64(p.rate) * 60 / (bpm * float64(p.song.LPB) * float64(p.song.TicksPerRow))
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
	s := p.song
	switch c.kind {
	case cmdSection:
		i := s.SectionIndex(c.name)
		if i < 0 {
			return
		}
		if c.q == QuantNow || !p.playing {
			p.gotoSection(i)
			if !p.playing {
				p.playing, p.finished = true, false
			}
			return
		}
		p.pendingSection, p.pendingQuant = i, c.q
	case cmdParam:
		i := s.ParamIndex(c.name)
		if i < 0 {
			return
		}
		ps := &p.params[i]
		ps.target = c.f
		if c.f2 <= 0 {
			ps.cur, ps.rate = c.f, 0
		} else {
			ps.rate = absf(ps.target-ps.cur) / c.f2
		}
	case cmdMute:
		ci := c.i
		if c.name != "" {
			ci = s.ChannelIndex(c.name)
		}
		if ci < 0 || ci >= len(p.strip) {
			return
		}
		p.strip[ci].setMute(c.b, c.f2)
	case cmdSolo:
		for i := range p.strip {
			p.strip[i].setMute(c.i >= 0 && i != c.i, 0.01)
		}
	case cmdTempo:
		p.bpmTarget = clamp32(c.f, 16, 999)
		if c.f2 <= 0 {
			p.bpm, p.bpmRate = p.bpmTarget, 0
			p.dly.set(s.Delay, p.bpm, p.rate)
		} else {
			p.bpmRate = absf(p.bpmTarget-p.bpm) / c.f2
		}
	case cmdStinger:
		if c.q == QuantNow || !p.playing {
			p.addStinger(c.player)
			return
		}
		if len(p.pending) < cap(p.pending) {
			p.pending = append(p.pending, stingerReq{p: c.player, q: c.q})
		}
	case cmdSetSong:
		p.installSong(c.song)
	case cmdPlayFrom:
		p.playing, p.finished = true, false
		p.order = clampInt(c.i, 0, len(s.Order)-1)
		p.row = clampInt(c.j, 0, p.pattern().Rows()-1)
		p.tick, p.tickRemain = 0, 0
		p.section = p.sectionAt(p.order)
		p.jumpOrder, p.breakRow, p.loopJump = -1, -1, false
		p.bpm, p.bpmTarget = s.BPM, s.BPM
		p.publish()
	case cmdStop:
		p.playing = false
		for i := range p.chans {
			p.releaseChannel(i)
		}
		p.publish()
	case cmdNote:
		p.previewNote(c.i, uint8(c.j), uint8(c.f2), c.f, c.hold)
	case cmdPause:
		p.paused = c.b
	case cmdNoteOff:
		p.previewOff(c.i)
	}
}

func (p *Player) addStinger(sp *Player) {
	if sp == nil {
		return
	}
	if len(p.stingers) < cap(p.stingers) {
		p.stingers = append(p.stingers, sp)
	}
}

// gotoSection moves the transport to a section's first row.
func (p *Player) gotoSection(i int) {
	sec := p.song.Sections[i]
	p.section = i
	p.order, p.row, p.tick = sec.Start, 0, 0
	p.tickRemain = 0
	p.jumpOrder, p.breakRow, p.loopJump = -1, -1, false
	p.pendingSection = -1
	p.post(Event{Kind: EventSection, Name: sec.Name, Section: sec.Name, Order: p.order})
	p.publish()
}

// Render implements audio.Generator.
func (p *Player) Render(dst []float32, frames, rate int) bool {
	if p.rate != rate {
		p.init(rate)
	}
	p.drain()
	out := dst[:frames*2]
	if p.paused {
		clear(out)
		return true
	}
	i := 0
	for i < frames {
		if p.playing && p.tickRemain <= 0 {
			p.doTick()
			p.tickRemain += p.samplesPerTick()
			continue
		}
		n := frames - i
		if n > controlBlock {
			n = controlBlock
		}
		if p.playing {
			if t := int(math.Ceil(p.tickRemain)); t < n {
				n = t
			}
			if n < 1 {
				n = 1
			}
		}
		p.renderBlock(out[i*2:(i+n)*2], n)
		if p.playing {
			p.tickRemain -= float64(n)
		}
		i += n
	}
	p.mixStingers(out, frames, rate)
	p.voicesLive.Store(int32(p.liveVoices()))
	if p.finished {
		p.tailFrames -= frames
		if p.tailFrames <= 0 && p.liveVoices() == 0 && len(p.stingers) == 0 {
			return false
		}
	}
	return true
}

func (p *Player) mixStingers(out []float32, frames, rate int) {
	if len(p.stingers) == 0 {
		return
	}
	if cap(p.stingerBuf) < frames*2 {
		p.stingerBuf = make([]float32, frames*2)
	}
	buf := p.stingerBuf[:frames*2]
	k := 0
	for _, sp := range p.stingers {
		more := sp.Render(buf, frames, rate)
		for j := range out {
			out[j] += buf[j]
		}
		if more {
			p.stingers[k] = sp
			k++
		}
	}
	for j := k; j < len(p.stingers); j++ {
		p.stingers[j] = nil
	}
	p.stingers = p.stingers[:k]
}

func (p *Player) liveVoices() int {
	n := 0
	for i := range p.voices {
		if p.voices[i].active {
			n++
		}
	}
	return n
}

// rand is a xorshift for anything random the player does, seeded per song
// so a render is repeatable.
func (p *Player) rand() uint64 {
	p.rng ^= p.rng << 13
	p.rng ^= p.rng >> 7
	p.rng ^= p.rng << 17
	return p.rng
}
