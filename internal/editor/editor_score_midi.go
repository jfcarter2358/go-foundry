package editor

// editor_score_midi.go — a MIDI keyboard in the Score editor: step entry,
// real-time recording and MIDI thru. The model, with no ImGui and no
// device in it (the window side is editor_score_midi_view.go), so the tests
// play it with messages.
//
// Step entry is Sibelius's: in note input, a key played writes a note of
// the current value after the selection, and keys played while it is still
// held join it as a chord — so a chord is played as a chord. Rests, dots,
// ties and durations stay on the computer keyboard (keypad 0, keypad ., the
// duration keys), as they do in Sibelius. Out of note input the keyboard
// only sounds (thru).
//
// A key is a sound, not a place on the page: what is written is the pitch
// that key sounds — the pitch MIDI thru just played — spelled in the key
// signature (sharps going up, flats going down when it is not in the key),
// and shown where the staff shows that sound (a transposing instrument's
// written pitch, under an ottava line an octave down). On a percussion
// staff a key is the drum the line's drum map plays on it.
//
// Recording (Flexi-time) records into the first armed line (Timeline's R),
// else the selected one, from the selected bar (else the play head's): a
// count-in, then the score plays with a click while every key is kept with
// its tick; when the transport stops (Space, the record command again, or
// the score's end) the take is written over the bars it covered through
// edit.WriteTake — quantised, split, voiced and spelled exactly as a MIDI
// file's part would be, the sustain pedal as pedal marks. One undo step.

import (
	"fmt"
	"sort"

	"foundry/internal/mididev"
	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/io/notate"
)

// scoreMIDIConfig is the MIDI part of score.yaml. Its zero value is the
// defaults (the No… switches read that way round for it).
type scoreMIDIConfig struct {
	// Device is the input port's name (mididev.Port.Name).
	Device string `yaml:"device,omitempty"`
	// Channel takes only that channel (1-16); 0 takes them all.
	Channel int `yaml:"channel,omitempty"`
	// NoThru keeps the keyboard silent (thru plays each key on the line's
	// instrument as it is pressed).
	NoThru bool `yaml:"no_thru,omitempty"`
	// CountIn is the count-in's bars before a recording (nil: 1).
	CountIn *int `yaml:"count_in,omitempty"`
	// NoClick records with no metronome; ClickLevel is its level (0: 0.6).
	NoClick    bool    `yaml:"no_click,omitempty"`
	ClickLevel float32 `yaml:"click_level,omitempty"`
	// Grid is what a take is quantised to ("" : 16ths with triplets).
	Grid string `yaml:"grid,omitempty"`
	// Voices is the most voices a take is written in (0: 2).
	Voices int `yaml:"voices,omitempty"`
	// Split is a grand staff's split point (0: 60, middle C).
	Split int `yaml:"split,omitempty"`
	// NoLegato, NoGraces and NoDynamics are the MIDI import's switches.
	NoLegato   bool `yaml:"no_legato,omitempty"`
	NoGraces   bool `yaml:"no_graces,omitempty"`
	NoDynamics bool `yaml:"no_dynamics,omitempty"`
	// LatencyMS moves every recorded key earlier: what the audio device
	// delays the click and the score by, as the player hears them.
	LatencyMS int `yaml:"latency_ms,omitempty"`
}

// countIn is the count-in's bars.
func (c scoreMIDIConfig) countIn() int {
	if c.CountIn == nil {
		return 1
	}
	return max(0, min(*c.CountIn, 4))
}

// clickLevel is the metronome's level, 0 for none.
func (c scoreMIDIConfig) clickLevel() float32 {
	if c.NoClick {
		return 0
	}
	if c.ClickLevel <= 0 {
		return 0.6
	}
	return min(c.ClickLevel, 1)
}

// take is how a recording is written.
func (c scoreMIDIConfig) take() (edit.TakeOptions, error) {
	o := edit.TakeOptions{Voices: c.Voices, Split: c.Split, NoGraces: c.NoGraces, NoLegato: c.NoLegato, NoDynamics: c.NoDynamics}
	if o.Voices <= 0 {
		o.Voices = 2
	}
	o.Grid = notate.DefaultGrid()
	if c.Grid != "" {
		g, err := notate.ParseGrid(c.Grid)
		if err != nil {
			return o, err
		}
		o.Grid = g
	}
	return o, nil
}

// scoreMIDIEntry is step entry's and recording's state.
type scoreMIDIEntry struct {
	// Thru sounds each key on the line's instrument (the window sets it
	// from the settings).
	Thru bool
	// held are the keys down now.
	held map[int]bool
	// chord is a chord being played: the selected chord was written by a
	// key still held, so the next key joins it.
	chord bool
	// thru are notes for the window to start and stop.
	thru []scoreThru
	// Take is a recording under way, nil for none.
	Take *scoreTake
	// Last is the last message heard, for the MIDI dialog.
	Last string
}

// scoreThru is one key to sound or release on a line.
type scoreThru struct {
	On   bool
	Line int
	Key  int
	Vel  float32
}

// scoreTake is a recording: where it goes and what has been played.
type scoreTake struct {
	Line int
	// Bar is the first bar recorded over, Tick where it plays.
	Bar  int
	Tick int64
	// Limit is the first tick the take cannot reach: where the
	// performance next plays a bar it has already played (a repeat), or
	// its end.
	Limit int64
	notes []scoreTakeNote
	// open is each held key's note.
	open map[int]int
	// pedal are the damper pedal's presses (ticks; up -1 while held).
	pedal [][2]int64
	// Count is the count-in's beats (for the window: a key in its last
	// beat goes on the downbeat).
	Count int
}

// scoreTakeNote is a key held from On to Off (ticks, playing order); Off
// is -1 while it is held.
type scoreTakeNote struct {
	Key, Vel int
	On, Off  int64
}

// midiLine is the line the keyboard plays: the selection's (the first
// armed line while recording).
func (e *scoreState) midiLine() int {
	if t := e.MIDI.Take; t != nil {
		return t.Line
	}
	return max(0, min(e.selLine(), len(e.Score.Lines)-1))
}

// midiIn takes one message. tick is where it falls in the performance
// (playing order) when the transport is recording; ok false otherwise.
func (e *scoreState) midiIn(m mididev.Message, tick int64, ok bool) {
	if e.MIDI.held == nil {
		e.MIDI.held = map[int]bool{}
	}
	e.MIDI.Last = m.String()
	switch {
	case m.IsNoteOn():
		e.midiNoteOn(m.Note(), m.Velocity(), tick, ok)
	case m.IsNoteOff():
		e.midiNoteOff(m.Note(), tick, ok)
	case m.Kind() == mididev.ControlChange && int(m.Data1) == mididev.SustainPedal:
		if t := e.MIDI.Take; t != nil && ok {
			t.pedalAt(m.Data2 >= 64, tick)
		}
	}
}

// pedalAt is the damper pedal going down or up during a take.
func (t *scoreTake) pedalAt(down bool, tick int64) {
	tick = min(max(tick, t.Tick), t.Limit)
	n := len(t.pedal)
	switch {
	case down && (n == 0 || t.pedal[n-1][1] >= 0):
		t.pedal = append(t.pedal, [2]int64{tick, -1})
	case !down && n > 0 && t.pedal[n-1][1] < 0:
		t.pedal[n-1][1] = max(tick, t.pedal[n-1][0]+1)
	}
}

func (e *scoreState) midiNoteOn(key, vel int, tick int64, ok bool) {
	wasHeld := len(e.MIDI.held) > 0
	e.MIDI.held[key] = true
	line := e.midiLine()
	if e.MIDI.Thru && len(e.Score.Lines) > 0 {
		e.MIDI.thru = append(e.MIDI.thru, scoreThru{On: true, Line: line, Key: key, Vel: float32(vel) / 127})
	}
	if t := e.MIDI.Take; t != nil {
		if ok {
			t.noteOn(key, vel, tick)
		}
		return
	}
	if !e.Input {
		return
	}
	if wasHeld && e.MIDI.chord && e.chord() != nil {
		e.midiAddKey(key)
		return
	}
	e.MIDI.chord = e.midiWriteKey(key)
}

func (e *scoreState) midiNoteOff(key int, tick int64, ok bool) {
	delete(e.MIDI.held, key)
	if len(e.MIDI.held) == 0 {
		e.MIDI.chord = false
	}
	if e.MIDI.Thru && len(e.Score.Lines) > 0 {
		e.MIDI.thru = append(e.MIDI.thru, scoreThru{Line: e.midiLine(), Key: key})
	}
	if t := e.MIDI.Take; t != nil && ok {
		t.noteOff(key, tick)
	}
}

// takeThru hands the window the keys to sound and release.
func (e *scoreState) takeThru() []scoreThru {
	t := e.MIDI.thru
	e.MIDI.thru = nil
	return t
}

// midiPitch is the pitch a key sounds, spelled in the key at a place
// (rising from the note before: sharps, falling: flats), as the staff
// writes it there — or, on a percussion staff, the drum on that key.
func (e *scoreState) midiPitch(key int, l edit.Loc, at score.Frac, from *score.Pitch) score.Pitch {
	if e.percussion(l.Line, l.Staff) {
		if d, ok := e.kitOf(l.Line).ForKey(key); ok {
			return score.DrumPitch(d)
		}
		return score.SpellMIDI(key, 0, 1)
	}
	dir := 0
	if from != nil {
		switch c := e.toConcert(l.Line, l.Staff, *from).MIDI(); {
		case key > c:
			dir = 1
		case key < c:
			dir = -1
		}
	}
	concert := score.SpellMIDI(key, e.fifths(l.Bar), dir)
	return e.toWritten(l.Line, l.Staff, concert)
}

// midiWriteKey writes a key as a new note after the selection, reporting
// whether it did. The note it writes takes the written pitch the key
// sounds; write puts an ottava's octave back.
func (e *scoreState) midiWriteKey(key int) bool {
	l, at, ok := e.caret()
	if !ok {
		e.Status = "Select a bar or a note first"
		return false
	}
	prev := e.prevPitch(l, at)
	p := e.midiPitch(key, l, at, &prev)
	if st := edit.Staff(e.Score, l.Line, l.Staff); st != nil && !e.percussion(l.Line, l.Staff) {
		p.Octave -= int8(e.Score.OttavaAt(st, score.Pos{Bar: l.Bar, Offset: at}))
	}
	e.Armed = false
	n := len(e.auditions)
	wrote := e.write(l, at, &p)
	if e.MIDI.Thru {
		e.auditions = e.auditions[:n] // thru has sounded it already
	}
	return wrote
}

// midiAddKey adds a key to the selected chord (a key played while the one
// that wrote it is held). A pitch the chord has is left.
func (e *scoreState) midiAddKey(key int) {
	ev := e.chord()
	l := e.Sel.Loc
	for _, n := range ev.Notes {
		if n.Pitch.MIDI() == key {
			return
		}
	}
	top := e.toWritten(l.Line, l.Staff, ev.Notes[len(ev.Notes)-1].Pitch)
	on, _ := edit.Onset(e.Score, l)
	p := e.midiPitch(key, l, on, &top)
	n := len(e.auditions)
	e.addPitch(p)
	if e.MIDI.Thru {
		e.auditions = e.auditions[:n]
	}
}

// ---------- recording ----------

// recordLine is the line a recording goes into: the first armed line,
// else the selected one.
func (e *scoreState) recordLine() int {
	for i := range e.Score.Lines {
		if e.armed(i) {
			return i
		}
	}
	return max(0, min(e.selLine(), len(e.Score.Lines)-1))
}

// startTake starts a recording: into recordLine from the selected bar
// (the play head's with nothing selected), after the count-in. The window
// starts the player (scoreOpRecord).
func (e *scoreState) startTake(cfg scoreMIDIConfig) bool {
	if len(e.Score.Lines) == 0 {
		e.Status = "Add a line to record into"
		return false
	}
	if _, err := cfg.take(); err != nil {
		e.Status = "MIDI settings: " + err.Error()
		return false
	}
	perf := e.perf()
	if perf == nil || len(perf.Bars) == 0 {
		e.Status = "The score does not play; nothing to record over"
		return false
	}
	bar := e.headBar()
	if e.Sel.Kind != scoreSelNone {
		bar = e.selBar()
	}
	tick := scorePosTick(perf, score.Pos{Bar: bar})
	if tick < 0 {
		e.Status = fmt.Sprintf("Bar %d does not play", bar)
		return false
	}
	t := &scoreTake{Line: e.recordLine(), Bar: bar, Tick: tick, Limit: perf.Length, open: map[int]int{}}
	// The take ends where the performance goes back to a bar it played.
	bi := perf.BarAt(float64(tick))
	t.Count = cfg.countIn() * max(len(perf.Bars[bi].Meter.Beats()), 1)
	for k := bi + 1; k < len(perf.Bars); k++ {
		if perf.Bars[k].Bar <= perf.Bars[k-1].Bar {
			t.Limit = perf.Bars[k].Tick
			break
		}
	}
	e.MIDI.Take = t
	e.MIDI.chord = false
	e.Transport.Head = tick
	e.op(scorePlayerOp{Kind: scoreOpRecord, Tick: tick, Bars: cfg.countIn(), Level: cfg.clickLevel()})
	e.Status = fmt.Sprintf("Recording into %s from bar %d", e.lineName(t.Line), bar)
	if n := cfg.countIn(); n > 0 {
		e.Status += fmt.Sprintf(" after %d bar(s) of count-in", n)
	}
	e.Status += ": Space stops"
	return true
}

func (t *scoreTake) noteOn(key, vel int, tick int64) {
	tick = max(tick, t.Tick)
	if tick >= t.Limit {
		return
	}
	if k, ok := t.open[key]; ok {
		// Pressed again without a release (two keyboards, a lost message).
		t.notes[k].Off = tick
	}
	t.open[key] = len(t.notes)
	t.notes = append(t.notes, scoreTakeNote{Key: key, Vel: vel, On: tick, Off: -1})
}

func (t *scoreTake) noteOff(key int, tick int64) {
	k, ok := t.open[key]
	if !ok {
		return
	}
	delete(t.open, key)
	t.notes[k].Off = min(max(tick, t.notes[k].On+1), t.Limit)
}

// Notes is how many keys the take holds.
func (t *scoreTake) Notes() int { return len(t.notes) }

// finishTake ends a recording at a tick (where the transport stopped):
// held keys end there, and the take is written over its bars as one undo
// step. The window has stopped the player.
func (e *scoreState) finishTake(end int64, cfg scoreMIDIConfig) bool {
	t := e.MIDI.Take
	if t == nil {
		return false
	}
	e.MIDI.Take = nil
	e.MIDI.chord = false
	e.op(scorePlayerOp{Kind: scoreOpClick})
	e.sendLoop()
	end = min(max(end, t.Tick), t.Limit)
	for _, k := range t.open {
		t.notes[k].Off = max(end, t.notes[k].On+1)
	}
	perf := e.perf()
	if len(t.notes) == 0 || perf == nil {
		e.Status = "Nothing was recorded"
		return false
	}
	o, err := cfg.take()
	if err != nil {
		e.Status = "MIDI settings: " + err.Error()
		return false
	}
	starts := e.Score.BarStarts()
	// written is where a tick falls on the page; an end is read from the
	// tick before it, so a note ending on a barline ends its own bar.
	written := func(tick int64, end bool) score.Frac {
		k := tick
		if end {
			k--
		}
		b := perf.Bars[perf.BarAt(float64(max(k, 0)))]
		return starts[b.Bar-1].Add(score.F(tick-b.Tick, score.TicksPerWhole))
	}
	last := end
	notes := make([]notate.Note, 0, len(t.notes))
	for _, n := range t.notes {
		if n.Off <= n.On {
			continue
		}
		notes = append(notes, notate.Note{Start: written(n.On, false), End: written(n.Off, true), MIDI: n.Key, Velocity: n.Vel})
		last = max(last, n.Off)
	}
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].Start.Less(notes[j].Start) })
	for _, p := range t.pedal {
		up := p[1]
		if up < 0 {
			up = end
		}
		if up > p[0] {
			o.Pedal = append(o.Pedal, notate.PedalSpan{Down: written(p[0], false), Up: written(up, true)})
		}
	}
	// The bars covered: the first to the one the take's last moment is in,
	// less half a grid step, so stopping just past a barline does not take
	// the next bar too.
	slack := o.Grid.Unit.Ticks() / 2
	z := perf.Bars[perf.BarAt(float64(max(min(last, t.Limit)-1-slack, t.Tick)))].Bar
	z = max(z, t.Bar)
	var count int
	ok := e.change(func() error {
		var err error
		count, err = edit.WriteTake(e.Score, t.Line, t.Bar, z, notes, o)
		e.Sel = scoreSel{Kind: scoreSelBars, LineA: t.Line, LineB: t.Line, BarA: t.Bar, BarB: z}
		return err
	})
	if !ok {
		return false
	}
	e.Transport.Head = t.Tick
	e.Status = fmt.Sprintf("Recorded %d note(s) into %s, bars %d-%d (Ctrl+Z takes them back)", count, e.lineName(t.Line), t.Bar, z)
	return true
}
