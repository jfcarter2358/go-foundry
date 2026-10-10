// Package tracker is Foundry's music tracker: the song model, the .ftrk file
// format, and the sequencer that plays a song through internal/audio.
//
// A song is the classic tracker shape — an order list of patterns, each a grid
// of rows × channels, each cell a note, an instrument, a volume and a couple of
// effect commands — with the parts a game needs bolted on: named sections a
// script can switch between on a beat, named params that steer channel levels
// and filters while it plays, and markers that fire script events.
//
// The package depends on internal/audio and nothing above it. The sequencer is
// an audio.Generator: it runs on the mixer goroutine and renders one stereo
// stream, so a playing song is one voice on the music bus.
package tracker

import "fmt"

// Limits. Generous for a game score; small enough that a whole song's state is
// a few kilobytes and a pattern edit can clone the song without thinking.
const (
	MaxChannels    = 64
	MaxRows        = 256
	MaxFXColumns   = 4
	MaxInstruments = 255
	MaxPatterns    = 256
)

// Note values. 1..120 are C-0..B-9; 0 is an empty cell.
const (
	NoteNone uint8 = 0
	NoteMin  uint8 = 1
	NoteMax  uint8 = 120
	NoteOff  uint8 = 254
	NoteCut  uint8 = 255

	// NoteC4 is middle C, the base note a sample plays at its own pitch unless
	// its sidecar says otherwise.
	NoteC4 uint8 = 49
)

// VolNone marks an empty volume column; 0..VolMax set the note's level.
const (
	VolNone uint8 = 0xFF
	VolMax  uint8 = 0x40
)

// Effect is one effect-column command: a letter and a byte. Cmd 0 is empty.
type Effect struct {
	Cmd   byte
	Param uint8
}

// Cell is one channel's slot on one row.
type Cell struct {
	Note  uint8
	Instr uint8 // 1-based instrument number; 0 keeps the channel's last
	Vol   uint8 // VolNone or 0..VolMax
	FX    [MaxFXColumns]Effect
}

// EmptyCell is a cell with nothing in it. The zero Cell is not empty — its
// volume column reads as "set volume 0" — so new rows are filled with this.
var EmptyCell = Cell{Vol: VolNone}

// IsEmpty reports whether the cell does nothing.
func (c Cell) IsEmpty() bool {
	if c.Note != NoteNone || c.Instr != 0 || c.Vol != VolNone {
		return false
	}
	for _, e := range c.FX {
		if e.Cmd != 0 {
			return false
		}
	}
	return true
}

// Pattern is a grid of Rows rows, each holding one cell per song channel.
type Pattern struct {
	Name string
	// Cells is [row][channel]. Every row is as wide as the song has channels;
	// Song.SetChannelCount keeps that true.
	Cells [][]Cell
}

// Rows is the pattern's length.
func (p *Pattern) Rows() int { return len(p.Cells) }

// NewPattern returns an empty pattern.
func NewPattern(name string, rows, channels int) *Pattern {
	p := &Pattern{Name: name}
	p.Resize(rows, channels)
	return p
}

// Resize changes the row count and width, keeping what fits.
func (p *Pattern) Resize(rows, channels int) {
	rows = clampInt(rows, 1, MaxRows)
	out := make([][]Cell, rows)
	for r := range out {
		row := make([]Cell, channels)
		for c := range row {
			if r < len(p.Cells) && c < len(p.Cells[r]) {
				row[c] = p.Cells[r][c]
			} else {
				row[c] = EmptyCell
			}
		}
		out[r] = row
	}
	p.Cells = out
}

// Clone deep-copies the pattern.
func (p *Pattern) Clone() *Pattern {
	q := &Pattern{Name: p.Name, Cells: make([][]Cell, len(p.Cells))}
	for r := range p.Cells {
		q.Cells[r] = append([]Cell(nil), p.Cells[r]...)
	}
	return q
}

// Channel is one column of the song and its mixer strip.
type Channel struct {
	Name   string
	Volume float32 // 0..1
	Pan    float32 // -1 left .. 1 right
	Mute   bool
	// SendA and SendB feed the song's reverb and delay buses.
	SendA, SendB float32
	// Drive (0..1) saturates the channel; Crush (0 off, else 1..16 bits)
	// reduces its bit depth. Inserts, before the sends.
	Drive float32
	Crush int
}

// Section is a named run of the order list a script can ask for by name.
type Section struct {
	Name string
	// Start and End index the order list, End exclusive.
	Start, End int
	// Loop repeats the section until something asks for another. When false
	// the song moves to Next, or on down the order list when Next is empty.
	Loop bool
	Next string
}

// Param is a named 0..1 value a script ramps while the song plays.
type Param struct {
	Name    string
	Default float32
}

// BindTarget names what a param binding drives on a channel.
type BindTarget string

const (
	BindVolume BindTarget = "volume"
	BindPan    BindTarget = "pan"
	BindSendA  BindTarget = "send_a"
	BindSendB  BindTarget = "send_b"
	BindCutoff BindTarget = "cutoff" // a low-pass over the channel, in Hz
)

// Binding maps a param onto a channel property: at param 0 the property is
// From, at 1 it is To, linearly between (cutoff interpolates in octaves).
type Binding struct {
	Param   string
	Channel string
	Target  BindTarget
	From    float32
	To      float32
}

// ReverbSpec is send bus A.
type ReverbSpec struct {
	Size    float32 // 0..1 room size
	Damping float32 // 0..1 high-frequency loss
	Width   float32 // 0..1 stereo width
	Return  float32 // bus return level
}

// DelaySpec is send bus B, tempo-synced.
type DelaySpec struct {
	Beats    float32 // delay time in beats; 0.75 is a dotted eighth
	Feedback float32 // 0..0.95
	Tone     float32 // 0..1, low-pass in the feedback path (1 = bright)
	PingPong bool
	Return   float32
}

// CompressorSpec is the master bus.
type CompressorSpec struct {
	Enabled   bool
	Threshold float32 // dBFS
	Ratio     float32
	Attack    float32 // seconds
	Release   float32 // seconds
	Makeup    float32 // dB
}

// Song is a whole piece of music.
type Song struct {
	Name string
	// BPM is beats per minute; LPB is rows per beat; TicksPerRow is the effect
	// resolution — slides and vibrato update once a tick.
	BPM         float32
	LPB         int
	TicksPerRow int
	// BeatsPerBar sets the bar for quantised transitions and bar events.
	BeatsPerBar int
	// FXColumns is how many effect columns cells show and save (1..4).
	FXColumns int
	Gain      float32
	// Loop restarts the order list at its end; false ends the song there.
	Loop bool
	// BeatEvents asks the player to post beat and bar events.
	BeatEvents bool
	// Interp is the default sample interpolation for instruments that leave
	// theirs unset.
	Interp Interp

	Channels    []Channel
	Patterns    []*Pattern
	Order       []int
	Sections    []Section
	Instruments []*Instrument
	Params      []Param
	Bindings    []Binding
	// Markers names the values an E (event marker) command fires with.
	Markers map[uint8]string

	Reverb     ReverbSpec
	Delay      DelaySpec
	Compressor CompressorSpec
}

// NewSong returns an empty song with one pattern and four channels.
func NewSong(name string) *Song {
	s := &Song{
		Name: name, BPM: 120, LPB: 4, TicksPerRow: 6, BeatsPerBar: 4,
		FXColumns: 2, Gain: 1, Loop: true, Interp: InterpCubic,
		Reverb:     ReverbSpec{Size: 0.6, Damping: 0.4, Width: 1, Return: 1},
		Delay:      DelaySpec{Beats: 0.75, Feedback: 0.35, Tone: 0.6, PingPong: true, Return: 1},
		Compressor: CompressorSpec{Threshold: -12, Ratio: 3, Attack: 0.005, Release: 0.15},
	}
	for i := 0; i < 4; i++ {
		s.Channels = append(s.Channels, Channel{Name: fmt.Sprintf("Ch %d", i+1), Volume: 1})
	}
	s.Patterns = []*Pattern{NewPattern("", 64, 4)}
	s.Order = []int{0}
	return s
}

// SetChannelCount grows or shrinks the song to n channels, every pattern with it.
func (s *Song) SetChannelCount(n int) {
	n = clampInt(n, 1, MaxChannels)
	for len(s.Channels) < n {
		s.Channels = append(s.Channels, Channel{Name: fmt.Sprintf("Ch %d", len(s.Channels)+1), Volume: 1})
	}
	s.Channels = s.Channels[:n]
	for _, p := range s.Patterns {
		p.Resize(p.Rows(), n)
	}
}

// InsertChannel adds an empty channel at index at (0..len, len appends),
// shifting the channels from at onward one to the right in every pattern.
// Its name is "Ch N", made unique among the song's channels, since bindings
// and scripts find a channel by name. False when the song is at MaxChannels.
func (s *Song) InsertChannel(at int) bool {
	if len(s.Channels) >= MaxChannels {
		return false
	}
	at = clampInt(at, 0, len(s.Channels))
	taken := map[string]bool{}
	for _, c := range s.Channels {
		taken[c.Name] = true
	}
	name := ""
	for n := len(s.Channels) + 1; ; n++ {
		if name = fmt.Sprintf("Ch %d", n); !taken[name] {
			break
		}
	}
	s.Channels = append(s.Channels, Channel{})
	copy(s.Channels[at+1:], s.Channels[at:])
	s.Channels[at] = Channel{Name: name, Volume: 1}
	for _, p := range s.Patterns {
		for r, row := range p.Cells {
			row = append(row, EmptyCell)
			copy(row[at+1:], row[at:])
			row[at] = EmptyCell
			p.Cells[r] = row
		}
	}
	return true
}

// DeleteChannel removes channel c and its column from every pattern, along
// with any param bindings that named it. False for the song's last channel or
// an index out of range.
func (s *Song) DeleteChannel(c int) bool {
	if len(s.Channels) <= 1 || c < 0 || c >= len(s.Channels) {
		return false
	}
	name := s.Channels[c].Name
	s.Channels = append(s.Channels[:c], s.Channels[c+1:]...)
	for _, p := range s.Patterns {
		for r, row := range p.Cells {
			if c < len(row) {
				p.Cells[r] = append(row[:c], row[c+1:]...)
			}
		}
	}
	kept := s.Bindings[:0]
	for _, b := range s.Bindings {
		if b.Channel != name {
			kept = append(kept, b)
		}
	}
	s.Bindings = kept
	return true
}

// Clone deep-copies the song. Instruments share their decoded sample data,
// which is immutable once loaded.
func (s *Song) Clone() *Song {
	c := *s
	c.Channels = append([]Channel(nil), s.Channels...)
	c.Patterns = make([]*Pattern, len(s.Patterns))
	for i, p := range s.Patterns {
		c.Patterns[i] = p.Clone()
	}
	c.Order = append([]int(nil), s.Order...)
	c.Sections = append([]Section(nil), s.Sections...)
	c.Instruments = make([]*Instrument, len(s.Instruments))
	for i, in := range s.Instruments {
		c.Instruments[i] = in.Clone()
	}
	c.Params = append([]Param(nil), s.Params...)
	c.Bindings = append([]Binding(nil), s.Bindings...)
	if s.Markers != nil {
		c.Markers = make(map[uint8]string, len(s.Markers))
		for k, v := range s.Markers {
			c.Markers[k] = v
		}
	}
	return &c
}

// Instrument returns the 1-based instrument n, or nil.
func (s *Song) Instrument(n uint8) *Instrument {
	if n == 0 || int(n) > len(s.Instruments) {
		return nil
	}
	return s.Instruments[n-1]
}

// SectionIndex finds a section by name, or -1.
func (s *Song) SectionIndex(name string) int {
	for i, sec := range s.Sections {
		if sec.Name == name {
			return i
		}
	}
	return -1
}

// ChannelIndex finds a channel by name, or -1. A number in range also works,
// 1-based, so a script can say "3".
func (s *Song) ChannelIndex(name string) int {
	for i, ch := range s.Channels {
		if ch.Name == name {
			return i
		}
	}
	var n int
	if _, err := fmt.Sscanf(name, "%d", &n); err == nil && n >= 1 && n <= len(s.Channels) {
		return n - 1
	}
	return -1
}

// ParamIndex finds a param by name, or -1.
func (s *Song) ParamIndex(name string) int {
	for i, p := range s.Params {
		if p.Name == name {
			return i
		}
	}
	return -1
}

// SecondsPerRow is how long one row lasts at the song's tempo.
func (s *Song) SecondsPerRow() float64 {
	bpm, lpb := float64(s.BPM), s.LPB
	if bpm <= 0 {
		bpm = 120
	}
	if lpb <= 0 {
		lpb = 4
	}
	return 60 / (bpm * float64(lpb))
}

// Duration is one pass through the order list at the song's starting tempo,
// in seconds. Tempo commands and jumps inside the song are not followed.
func (s *Song) Duration() float64 {
	rows := 0
	for _, o := range s.Order {
		if o >= 0 && o < len(s.Patterns) {
			rows += s.Patterns[o].Rows()
		}
	}
	return float64(rows) * s.SecondsPerRow()
}

// Validate fixes what can be fixed (out-of-range settings) and reports what
// cannot. It is run on load, so a hand-edited file plays rather than panics.
func (s *Song) Validate() []string {
	var problems []string
	if s.BPM < 16 || s.BPM > 999 {
		s.BPM = float32(clampF(float64(s.BPM), 16, 999))
	}
	s.LPB = clampInt(s.LPB, 1, 32)
	s.TicksPerRow = clampInt(s.TicksPerRow, 1, 32)
	if s.BeatsPerBar <= 0 {
		s.BeatsPerBar = 4
	}
	s.FXColumns = clampInt(s.FXColumns, 1, MaxFXColumns)
	if len(s.Channels) == 0 {
		s.Channels = []Channel{{Name: "Ch 1", Volume: 1}}
	}
	if len(s.Channels) > MaxChannels {
		problems = append(problems, fmt.Sprintf("%d channels; only %d are kept", len(s.Channels), MaxChannels))
		s.Channels = s.Channels[:MaxChannels]
	}
	if len(s.Patterns) == 0 {
		s.Patterns = []*Pattern{NewPattern("", 64, len(s.Channels))}
	}
	for _, p := range s.Patterns {
		p.Resize(p.Rows(), len(s.Channels))
	}
	order := s.Order[:0]
	for _, o := range s.Order {
		if o < 0 || o >= len(s.Patterns) {
			problems = append(problems, fmt.Sprintf("order names pattern %d, which does not exist", o))
			continue
		}
		order = append(order, o)
	}
	s.Order = order
	if len(s.Order) == 0 {
		s.Order = []int{0}
	}
	for i := range s.Sections {
		sec := &s.Sections[i]
		sec.Start = clampInt(sec.Start, 0, len(s.Order)-1)
		if sec.End <= sec.Start || sec.End > len(s.Order) {
			sec.End = clampInt(sec.End, sec.Start+1, len(s.Order))
		}
		if sec.Next != "" && s.SectionIndex(sec.Next) < 0 {
			problems = append(problems, fmt.Sprintf("section %q goes to %q, which does not exist", sec.Name, sec.Next))
			sec.Next = ""
		}
	}
	for _, b := range s.Bindings {
		if s.ParamIndex(b.Param) < 0 {
			problems = append(problems, fmt.Sprintf("binding names param %q, which does not exist", b.Param))
		}
		if s.ChannelIndex(b.Channel) < 0 {
			problems = append(problems, fmt.Sprintf("binding names channel %q, which does not exist", b.Channel))
		}
	}
	return problems
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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
