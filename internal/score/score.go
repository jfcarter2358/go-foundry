// Package score is the Score editor's music: a notated piece and its file.
//
// A Score is lines of music — a violin section, a piano, a choir — each
// pointing at a library instrument (.finst, shared with the Tracker) and
// written on one or more staves (a piano has two), each staff holding up to
// four independent voices. A voice is a list of measures, a measure a list
// of events: notes and chords with spelled pitches, rests, grace notes,
// tuplets nested to any depth, each with an open list of marks (dynamics,
// articulations, text). Over the lines sit the score-wide maps: tempo,
// meter, key, rehearsal marks (which are also the sections a game jumps
// between), repeats, markers and params.
//
// Time is exact (frac.go). What a score sounds like is not this package's
// business: internal/score/interpret turns it into timed note events and
// internal/score/play schedules those on a tracker.Engine. The file format
// is fscore.go; the per-measure text the voices are stored in is
// grammar.go.
package score

import (
	"fmt"
	"sort"
	"strings"

	"foundry/internal/tracker"
)

// MaxVoices is how many voices a staff holds.
const MaxVoices = 4

// MaxBars is the most bars a score holds, and MaxBarLength the longest a
// bar may be (a cadenza), in whole notes. A file past them is refused
// rather than taking minutes and gigabytes to lay out: an importer meeting
// a stray event a day into a MIDI file says so instead of writing millions
// of empty bars.
const MaxBars = 10000

// MaxBarLength is 32 whole notes: 128 beats of 4/4.
var MaxBarLength = Whole(32)

// Score is a whole piece.
type Score struct {
	// Name is the file's library name (a path under sounds/, no
	// extension); Title and Composer are what the page prints.
	Name     string
	Title    string
	Composer string

	// Bars is the measure count. Voices may be shorter — a voice that
	// stops early is silent after — and the count never drops below the
	// longest voice (Measures).
	Bars int
	// Pickup, when not zero, is the length of an incomplete first bar
	// (an anacrusis), in whole notes.
	Pickup Frac

	Tempo []TempoMark
	Meter []MeterMark
	Key   []KeyMark

	// Sections are the rehearsal marks; a game moves between them by name.
	Sections []Section
	Markers  []Marker
	Repeats  []Repeat
	// Voltas are the 1st, 2nd… endings of the repeats; Jumps the D.C.,
	// D.S., segno, coda, To Coda and Fine marks (form.go).
	Voltas []Volta
	Jumps  []Jump

	// Concert shows every staff at sounding pitch (the Concert Pitch
	// toggle); off, a transposing staff shows its written pitch. Pitches
	// are stored as they sound either way.
	Concert bool
	// Playback holds how the score plays rather than what it says.
	Playback PlaybackOptions

	Params   []Param
	Bindings []Binding

	// Loop plays the piece again from the top when it ends.
	Loop bool
	// Gain is the master level; 1 is unity.
	Gain float32

	Lines []*Line

	// Buses are the two send buses; Master the chain on the mix.
	Buses  Buses
	Master []FX
	// MasterLanes automate the master: its level (times Gain) and its
	// effects' settings (mix.go, ScopeMaster).
	MasterLanes []Lane
}

// Pos is a point in the written score: a bar (1-based) and an offset into
// it in whole notes. In a file it is written "bar:beat" — "5", "5:3",
// "5:2+1/2" — beats counted as the bar's meter counts them (Meter.Beats).
type Pos struct {
	Bar    int
	Offset Frac
}

// TempoMark sets the tempo from a point on.
type TempoMark struct {
	At Pos
	// BPM is beats a minute, of Unit (a quarter when zero).
	BPM  float64
	Unit Duration
	// Text is what is printed: "Andante", "Allegro con brio".
	Text string
	// Ramp moves the tempo smoothly from this mark to the next one
	// instead of stepping at it.
	Ramp bool
	// To, with HasTo, is where a gradual change ("rit.", "accel.") ends —
	// the end of its dashes; without it the change runs to the next mark.
	// tempo.go has what tempo text means.
	To    Pos
	HasTo bool
}

// QuarterBPM is the mark's tempo in quarters a minute; 0 for a mark that
// is only text ("a tempo", "rit.").
func (t TempoMark) QuarterBPM() float64 {
	u := t.Unit
	if u == (Duration{}) {
		u = Duration{Base: Quarter}
	}
	return t.BPM * u.Value().Float() * 4
}

// MeterMark changes the time signature at a bar.
type MeterMark struct {
	Bar   int
	Meter Meter
}

// KeyMark changes the key signature at a bar.
type KeyMark struct {
	Bar int
	// Fifths is sharps (positive) or flats (negative).
	Fifths int
	// Mode is "major" or "minor" (or a church mode); it changes nothing
	// but the name.
	Mode string
}

// Section is a rehearsal mark: a named place a game can jump to. It runs
// to the next section's bar, or the end.
type Section struct {
	Name string
	Bar  int
	// Loop repeats the section until something asks for another; Next
	// moves to that section at its end instead of carrying on.
	Loop bool
	Next string
}

// Marker posts a named event to scripts when playback passes it.
type Marker struct {
	Name string
	At   Pos
}

// Repeat plays bars Start..End (inclusive) Times times (2 when zero).
type Repeat struct {
	Start, End int
	Times      int
}

// Plays is how many times the repeat plays.
func (r Repeat) Plays() int {
	if r.Times < 2 {
		return 2
	}
	return r.Times
}

// Param is a named 0..1 value a script ramps while the score plays.
type Param struct {
	Name    string
	Default float32
}

// Binding maps a param onto a line's mixer: at 0 the property is From, at 1
// To (cutoff in octaves). Targets are the Tracker's.
type Binding struct {
	Param  string
	Line   string
	Target tracker.BindTarget
	From   float32
	To     float32
}

// FX is one effect in a chain: a kind ("reverb", "eq", "compressor") and
// its settings, in the order written (mix.go; internal/tracker/fx has the
// kinds). Bypass keeps it in the chain but out of the signal.
type FX struct {
	Kind   string
	Params []FXParam
	Bypass bool
}

// FXParam is one effect setting.
type FXParam struct {
	Name  string
	Value float64
}

// Param reads a setting, or def when it is not set.
func (f FX) Param(name string, def float64) float64 {
	for _, p := range f.Params {
		if p.Name == name {
			return p.Value
		}
	}
	return def
}

// Bus is a send bus: its chain, its return level, and the return's pan
// and mute.
type Bus struct {
	FX     []FX
	Return float32
	Pan    float32
	Mute   bool
	// Lanes automate the bus: its return level, its pan, its effects'
	// settings (mix.go, ScopeBus).
	Lanes []Lane
}

// Buses are send buses A and B.
type Buses struct {
	A, B Bus
}

// Line is one instrument's music.
type Line struct {
	Name string
	// Instrument names a library .finst: "strings/violin_section".
	Instrument string

	Volume float32
	Pan    float32
	Mute   bool
	Solo   bool
	// SendA and SendB feed the two buses; PreA and PreB take a send
	// before the fader (after the inserts).
	SendA, SendB float32
	PreA, PreB   bool
	// Inserts is the line's own effect chain.
	Inserts []FX

	Staves []*Staff
	// Lanes are automation over the line: "volume" multiplies its fader;
	// mix.go has the names.
	Lanes []Lane
	// Swing and Humanize, when set, replace the score's for this line.
	Swing    *Swing
	Humanize *Humanize

	// Gliss is how the line's glissandi play: "slide", "chromatic" or
	// "diatonic"; "" is the instrument's own (.finst playback.gliss).
	Gliss string
	// Overrides replace the level the line's dynamics give between two
	// points — a segment dragged in the dynamics lane — without changing a
	// printed mark.
	Overrides []DynOverride
}

// DynOverride holds a line's dynamic level flat from From to To (written
// positions, To exclusive). Level is on the dynamics' scale: MIDI velocity
// / 127, mf = 80/127.
type DynOverride struct {
	From, To Pos
	Level    float32
}

// GlissStyles are the values Line.Gliss takes ("" aside).
var GlissStyles = []string{"slide", "chromatic", "diatonic"}

// PlaybackOptions are a score's own playback settings.
type PlaybackOptions struct {
	// IgnoreRepeats plays the score straight through as written: every
	// bar once, every ending, no D.C. or D.S.
	IgnoreRepeats bool
	// Graces is when grace notes play.
	Graces GraceTiming
	// Swing and Humanize are the whole score's feel (a line may have its
	// own; mix.go).
	Swing    Swing
	Humanize Humanize
}

// GraceTiming is when grace notes play against their beat.
type GraceTiming uint8

const (
	// GraceAuto plays acciaccaturas just before the beat and appoggiaturas
	// on it.
	GraceAuto GraceTiming = iota
	// GraceBefore plays every grace note before the beat.
	GraceBefore
	// GraceOn plays every grace note on the beat.
	GraceOn
)

// GraceTimings are GraceTiming's names in a file, in order.
var GraceTimings = []string{"auto", "before", "on"}

// Staff is one five-line staff (or a percussion staff) of a line.
type Staff struct {
	// Clef is one of tracker.Clefs (not "grand": a grand staff is two).
	Clef string
	// Transpose is written to sounding: a B-flat clarinet's -M2. Pitches
	// are stored as they sound; this only changes how they are shown.
	Transpose Interval
	Voices    []*Voice
	// Clefs are clef changes inside the staff, in order (staff.go); Clef
	// is the clef it starts with.
	Clefs []ClefChange
}

// Voice is one independent rhythm on a staff.
type Voice struct {
	Measures []Measure
}

// Measure is a voice's bar.
type Measure struct {
	Events []Event
	// Tuplets are the bar's tuplet groups; an event's Tuplet indexes them
	// 1-based. They are in the order they open.
	Tuplets []Tuplet
}

// Tuplet plays its events at Den/Num of their written length: 3:2 is a
// triplet. Parent is the enclosing tuplet (1-based, 0 for none).
type Tuplet struct {
	Num, Den int
	Parent   int
}

// Ratio is the factor the tuplet applies, Den/Num.
func (t Tuplet) Ratio() Frac { return F(int64(t.Den), int64(t.Num)) }

// DefaultTupletDen is the usual "in the time of" for n notes: 3 in 2, 2
// in 3, otherwise the power of two below n (5, 6, 7 in 4; 9 in 8).
func DefaultTupletDen(n int) int {
	switch {
	case n == 2:
		return 3
	case n <= 1:
		return 1
	}
	d := 1
	for d*2 < n {
		d *= 2
	}
	return d
}

// EventKind is what an event is.
type EventKind uint8

const (
	// EventChord is one note or several sounding together.
	EventChord EventKind = iota
	// EventRest is a printed rest.
	EventRest
	// EventSpace takes time and prints nothing — a voice that is silent for
	// part of a bar.
	EventSpace
	// EventBarRest fills the whole bar, whatever its meter.
	EventBarRest
)

// Grace marks a grace note: no time in the bar, played around the beat.
type Grace uint8

const (
	NotGrace Grace = iota
	// Acciaccatura is the slashed, crushed grace: played just before the beat.
	Acciaccatura
	// Appoggiatura leans on the beat and takes time from the note after it.
	Appoggiatura
)

// Event is one thing in a voice's bar.
type Event struct {
	Kind  EventKind
	Dur   Duration
	Grace Grace
	// Notes are a chord's pitches (EventChord only).
	Notes []Note
	// Tuplet is the innermost tuplet the event is in, 1-based into the
	// measure's Tuplets; 0 for none.
	Tuplet int
	Marks  []Mark
}

// Note is one pitch of a chord.
type Note struct {
	Pitch Pitch
	// Tie holds this pitch into the next event of the voice.
	Tie bool
	// Acc is how the accidental shows.
	Acc AccShow
}

// AccShow is when a note's accidental is printed.
type AccShow uint8

const (
	// AccAuto prints it when the key and the bar so far need it.
	AccAuto AccShow = iota
	// AccForce always prints it (a natural in the key's own spelling too).
	AccForce
	// AccCautionary prints it in parentheses.
	AccCautionary
)

// Mark is something attached to an event: a dynamic ("p", "sfz"), an
// articulation ("stacc", "acc"), an ornament, a text. Value carries a
// setting or the text. The vocabulary is marks.go; unknown names are kept.
type Mark struct {
	Name  string
	Value string
}

// Lane is automation over a line: a breakpoint curve.
type Lane struct {
	Name   string
	Points []LanePoint
}

// LanePoint is a breakpoint; with HasTo it holds Value from At to To.
// From it (from To when it holds) to the next point the value moves as
// Shape says: in a straight line, a step, or a curve bent by Curve
// (-1..1).
type LanePoint struct {
	At    Pos
	To    Pos
	HasTo bool
	Value float32
	Shape LaneShape
	Curve float32
}

// ---------- construction ----------

// New returns an empty score: one bar of 4/4 at 120, C major, no lines.
func New(name string) *Score {
	return &Score{
		Name: name, Title: name, Bars: 1, Gain: 1,
		Tempo:  []TempoMark{{At: Pos{Bar: 1}, BPM: 120, Unit: Duration{Base: Quarter}}},
		Meter:  []MeterMark{{Bar: 1, Meter: FourFour()}},
		Key:    []KeyMark{{Bar: 1, Mode: "major"}},
		Buses:  DefaultBuses(),
		Master: []FX{DefaultCompressor()},
	}
}

// DefaultBuses are a room reverb on A and a dotted-eighth delay on B, the
// Tracker's defaults.
func DefaultBuses() Buses {
	return Buses{
		A: Bus{FX: []FX{{Kind: "reverb", Params: []FXParam{{"size", 0.6}, {"damping", 0.4}, {"width", 1}}}}, Return: 1},
		B: Bus{FX: []FX{{Kind: "delay", Params: []FXParam{{"beats", 0.75}, {"feedback", 0.35}, {"tone", 0.6}, {"ping_pong", 1}}}}, Return: 1},
	}
}

// DefaultCompressor is the master chain's gentle compressor, off.
func DefaultCompressor() FX {
	return FX{Kind: "compressor", Params: []FXParam{{"enabled", 0}, {"threshold", -12}, {"ratio", 3}, {"attack", 0.005}, {"release", 0.15}}}
}

// NewLine returns a line for an instrument with one staff in clef and an
// empty first voice.
func NewLine(name, instrument, clef string) *Line {
	return &Line{Name: name, Instrument: instrument, Volume: 1,
		Staves: []*Staff{{Clef: clef, Voices: []*Voice{{}}}}}
}

// ---------- copying ----------

// Clone copies the score deeply.
func (s *Score) Clone() *Score {
	c := *s
	c.Tempo = append([]TempoMark(nil), s.Tempo...)
	c.Meter = make([]MeterMark, len(s.Meter))
	for i, m := range s.Meter {
		m.Meter.Groups = append([]int(nil), m.Meter.Groups...)
		c.Meter[i] = m
	}
	if s.Meter == nil {
		c.Meter = nil
	}
	c.Key = append([]KeyMark(nil), s.Key...)
	c.Sections = append([]Section(nil), s.Sections...)
	c.Markers = append([]Marker(nil), s.Markers...)
	c.Repeats = append([]Repeat(nil), s.Repeats...)
	c.Voltas = make([]Volta, len(s.Voltas))
	for i, v := range s.Voltas {
		v.Passes = append([]int(nil), v.Passes...)
		c.Voltas[i] = v
	}
	if s.Voltas == nil {
		c.Voltas = nil
	}
	c.Jumps = append([]Jump(nil), s.Jumps...)
	c.Params = append([]Param(nil), s.Params...)
	c.Bindings = append([]Binding(nil), s.Bindings...)
	c.Buses.A.FX, c.Buses.B.FX = cloneFX(s.Buses.A.FX), cloneFX(s.Buses.B.FX)
	c.Buses.A.Lanes, c.Buses.B.Lanes = cloneLanes(s.Buses.A.Lanes), cloneLanes(s.Buses.B.Lanes)
	c.Master = cloneFX(s.Master)
	c.MasterLanes = cloneLanes(s.MasterLanes)
	c.Lines = make([]*Line, len(s.Lines))
	for i, l := range s.Lines {
		c.Lines[i] = l.Clone()
	}
	return &c
}

func cloneLanes(ls []Lane) []Lane {
	if ls == nil {
		return nil
	}
	out := make([]Lane, len(ls))
	for i, ln := range ls {
		ln.Points = append([]LanePoint(nil), ln.Points...)
		out[i] = ln
	}
	return out
}

func cloneFX(fx []FX) []FX {
	if fx == nil {
		return nil
	}
	out := make([]FX, len(fx))
	for i, f := range fx {
		f.Params = append([]FXParam(nil), f.Params...)
		out[i] = f
	}
	return out
}

// Clone copies the line deeply.
func (l *Line) Clone() *Line {
	c := *l
	c.Inserts = cloneFX(l.Inserts)
	c.Staves = make([]*Staff, len(l.Staves))
	for i, st := range l.Staves {
		c.Staves[i] = st.Clone()
	}
	c.Lanes = cloneLanes(l.Lanes)
	c.Overrides = append([]DynOverride(nil), l.Overrides...)
	if l.Swing != nil {
		w := *l.Swing
		c.Swing = &w
	}
	if l.Humanize != nil {
		h := *l.Humanize
		c.Humanize = &h
	}
	return &c
}

// Clone copies the staff deeply.
func (st *Staff) Clone() *Staff {
	c := *st
	c.Clefs = append([]ClefChange(nil), st.Clefs...)
	c.Voices = make([]*Voice, len(st.Voices))
	for i, v := range st.Voices {
		c.Voices[i] = v.Clone()
	}
	return &c
}

// Clone copies the voice deeply.
func (v *Voice) Clone() *Voice {
	c := &Voice{Measures: make([]Measure, len(v.Measures))}
	for i, m := range v.Measures {
		c.Measures[i] = m.Clone()
	}
	return c
}

// Clone copies the measure deeply.
func (m Measure) Clone() Measure {
	c := Measure{Tuplets: append([]Tuplet(nil), m.Tuplets...)}
	if m.Events != nil {
		c.Events = make([]Event, len(m.Events))
		for i, e := range m.Events {
			c.Events[i] = e.Clone()
		}
	}
	return c
}

// Clone copies the event deeply.
func (e Event) Clone() Event {
	e.Notes = append([]Note(nil), e.Notes...)
	e.Marks = append([]Mark(nil), e.Marks...)
	return e
}

// ---------- reading the maps ----------

// Measures is the bar count: Bars, or the longest voice if longer.
func (s *Score) Measures() int {
	n := s.Bars
	for _, l := range s.Lines {
		for _, st := range l.Staves {
			for _, v := range st.Voices {
				n = max(n, len(v.Measures))
			}
		}
	}
	return max(n, 1)
}

// MeterAt is the time signature in force at a bar.
func (s *Score) MeterAt(bar int) Meter {
	m := FourFour()
	for _, mm := range s.Meter {
		if mm.Bar <= bar {
			m = mm.Meter
		}
	}
	return m
}

// KeyAt is the key signature in force at a bar.
func (s *Score) KeyAt(bar int) KeyMark {
	k := KeyMark{Bar: 1, Mode: "major"}
	for _, km := range s.Key {
		if km.Bar <= bar {
			k = km
		}
	}
	return k
}

// BarLength is a bar's written length: the pickup for an anacrusis,
// otherwise the meter's.
func (s *Score) BarLength(bar int) Frac {
	if bar == 1 && s.Pickup.Sign() > 0 {
		return s.Pickup
	}
	return s.MeterAt(bar).Length()
}

// BarStarts are each bar's start in whole notes from the top, written
// order (no repeats), with one more entry for the end.
func (s *Score) BarStarts() []Frac {
	n := s.Measures()
	out := make([]Frac, n+1)
	at := Frac{0, 1}
	for b := 1; b <= n; b++ {
		out[b-1] = at
		at = at.Add(s.BarLength(b))
	}
	out[n] = at
	return out
}

// SortMaps puts the tempo, meter, key, section and marker lists in score
// order (a stable sort, so equal places keep their written order).
func (s *Score) SortMaps() {
	sort.SliceStable(s.Tempo, func(i, j int) bool { return posLess(s.Tempo[i].At, s.Tempo[j].At) })
	sort.SliceStable(s.Meter, func(i, j int) bool { return s.Meter[i].Bar < s.Meter[j].Bar })
	sort.SliceStable(s.Key, func(i, j int) bool { return s.Key[i].Bar < s.Key[j].Bar })
	sort.SliceStable(s.Sections, func(i, j int) bool { return s.Sections[i].Bar < s.Sections[j].Bar })
	sort.SliceStable(s.Markers, func(i, j int) bool { return posLess(s.Markers[i].At, s.Markers[j].At) })
	sort.SliceStable(s.Repeats, func(i, j int) bool { return s.Repeats[i].Start < s.Repeats[j].Start })
	sort.SliceStable(s.Voltas, func(i, j int) bool { return s.Voltas[i].Start < s.Voltas[j].Start })
	sort.SliceStable(s.Jumps, func(i, j int) bool { return s.Jumps[i].Bar < s.Jumps[j].Bar })
}

func posLess(a, b Pos) bool {
	if a.Bar != b.Bar {
		return a.Bar < b.Bar
	}
	return a.Offset.Less(b.Offset)
}

// PosLess orders positions.
func PosLess(a, b Pos) bool { return posLess(a, b) }

// SectionIndex finds a section by name, or -1.
func (s *Score) SectionIndex(name string) int {
	for i, sec := range s.Sections {
		if sec.Name == name {
			return i
		}
	}
	return -1
}

// LineIndex finds a line by name (without case) or 1-based number, or -1.
func (s *Score) LineIndex(name string) int {
	for i, l := range s.Lines {
		if strings.EqualFold(l.Name, name) {
			return i
		}
	}
	var n int
	if _, err := fmt.Sscanf(name, "%d", &n); err == nil && fmt.Sprint(n) == name && n >= 1 && n <= len(s.Lines) {
		return n - 1
	}
	return -1
}

// ParamIndex finds a param by name, or -1.
func (s *Score) ParamIndex(name string) int {
	for i, p := range s.Params {
		if p.Name == name {
			return i
		}
	}
	return -1
}

// Lane finds a lane by name, or nil.
func (l *Line) Lane(name string) *Lane { return laneIn(l.Lanes, name) }

func laneIn(ls []Lane, name string) *Lane {
	for i := range ls {
		if ls[i].Name == name {
			return &ls[i]
		}
	}
	return nil
}

// ---------- positions as text ----------

// FormatPos writes a position as "bar" or "bar:beat", beats 1-based in the
// bar's meter, a fraction of a beat after a "+": "5:2+1/2".
func (s *Score) FormatPos(p Pos) string {
	if p.Offset.IsZero() {
		return fmt.Sprint(p.Bar)
	}
	beats := s.MeterAt(p.Bar).Beats()
	at := Frac{0, 1}
	for i, b := range beats {
		end := at.Add(b)
		if p.Offset.Less(end) || i == len(beats)-1 {
			rest := p.Offset.Sub(at).Div(b)
			beat := fmt.Sprint(i + 1)
			if !rest.IsZero() {
				if rest.Sign() > 0 && rest.Less(Whole(1)) {
					beat += "+" + rest.String()
				} else {
					beat = Whole(int64(i + 1)).Add(rest).Mixed()
				}
			}
			return fmt.Sprintf("%d:%s", p.Bar, beat)
		}
		at = end
	}
	return fmt.Sprint(p.Bar)
}

// ParsePos reads what FormatPos writes.
func (s *Score) ParsePos(t string) (p Pos, err error) {
	defer RecoverOverflow(&err)
	t = strings.TrimSpace(t)
	barS, beatS, hasBeat := strings.Cut(t, ":")
	if _, err := fmt.Sscanf(barS, "%d", &p.Bar); err != nil || fmt.Sprint(p.Bar) != barS || p.Bar < 1 {
		return Pos{}, fmt.Errorf("bad position %q (want bar or bar:beat, e.g. 5:3)", t)
	}
	if !hasBeat {
		return p, nil
	}
	beat, err := ParseFrac(beatS)
	if err != nil || beat.Less(Whole(1)) {
		return Pos{}, fmt.Errorf("bad position %q: beats count from 1", t)
	}
	beats := s.MeterAt(p.Bar).Beats()
	k := beat.Floor() - 1
	rest := beat.Sub(Whole(k + 1))
	at := Frac{0, 1}
	for i := int64(0); i < k; i++ {
		if int(i) < len(beats) {
			at = at.Add(beats[i])
		} else {
			at = at.Add(beats[len(beats)-1])
		}
	}
	unit := beats[min(int(k), len(beats)-1)]
	p.Offset = at.Add(rest.Mul(unit))
	return p, nil
}

// PosFrac is a position in whole notes from the top, written order.
func (s *Score) PosFrac(p Pos, starts []Frac) Frac {
	if p.Bar < 1 {
		return p.Offset
	}
	if p.Bar > len(starts) {
		return starts[len(starts)-1].Add(p.Offset)
	}
	return starts[p.Bar-1].Add(p.Offset)
}

// PosAt is a position from whole notes from the top, written order: the
// inverse of PosFrac. A point past the last bar is in the bar after it.
func (s *Score) PosAt(f Frac, starts []Frac) Pos {
	for b := len(starts) - 1; b >= 1; b-- {
		if !f.Less(starts[b-1]) {
			return Pos{Bar: b, Offset: f.Sub(starts[b-1])}
		}
	}
	return Pos{Bar: 1, Offset: f}
}
