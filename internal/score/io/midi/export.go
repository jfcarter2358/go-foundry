package midi

// export.go — a score as a MIDI file.
//
// The file is what the score sounds like, from the interpreter
// (internal/score/interpret): repeats played out (or not, NoRepeats),
// ties joined, grace notes where they sound, dynamics as velocities. It is
// format 1 at the score's own 960 ticks a quarter, so every onset is
// exact: track 0 is the conductor (title, composer, tempo — a ramp in
// 16th-note steps — time and key signatures, rehearsal marks as markers,
// score markers as cue points), then one track per line with its name,
// its instrument's General MIDI program, its fader as channel volume, its
// pan, its volume lane as expression, and its notes. Lines that play as a
// kit go on channel 10; the rest share the other fifteen in turn.

import (
	"fmt"
	"math"
	"sort"

	"foundry/internal/score"
	"foundry/internal/score/interpret"
	"foundry/internal/score/io/gm"
)

// ExportOptions shape an export.
type ExportOptions struct {
	// NoRepeats writes the score once through as written.
	NoRepeats bool
}

// Export makes a MIDI file of a score.
func Export(s *score.Score, o ExportOptions) (f *File, err error) {
	defer score.RecoverOverflow(&err)
	perf, err := interpret.Interpret(s, interpret.Options{NoRepeats: o.NoRepeats})
	if err != nil {
		return nil, err
	}
	f = &File{Format: 1, Division: score.TicksPerQuarter}
	f.Tracks = append(f.Tracks, conductor(s, perf))
	channels := assignChannels(s)
	for li, l := range s.Lines {
		f.Tracks = append(f.Tracks, lineTrack(perf, li, l, channels[li]))
	}
	return f, nil
}

// conductor is track 0: the maps.
func conductor(s *score.Score, perf *interpret.Performance) Track {
	var t Track
	title := s.Title
	if title == "" {
		title = s.Name
	}
	t.Events = append(t.Events, TextMeta(0, MetaTrackName, title))
	if s.Composer != "" {
		t.Events = append(t.Events, TextMeta(0, MetaCopyright, s.Composer))
	}
	// Tempo: each point, and a ramp as steps every 16th.
	pts := perf.Tempo.Points
	const step = score.TicksPerQuarter / 4
	for i, p := range pts {
		tick := int64(math.Round(p.Tick))
		t.Events = append(t.Events, TempoMeta(tick, p.QPM))
		if !p.Ramp || i+1 >= len(pts) {
			continue
		}
		end := int64(math.Round(pts[i+1].Tick))
		for at := tick + step; at < end; at += step {
			t.Events = append(t.Events, TempoMeta(at, perf.Tempo.QPM(float64(at))))
		}
	}
	// Time signatures where the meter (or a pickup's length) changes; key
	// signatures where the key does.
	var lastSig [2]int
	lastKey := score.KeyMark{Fifths: 99}
	for _, b := range perf.Bars {
		num, den := b.Meter.Num(), b.Meter.Den
		if length := score.F(b.Ticks, score.TicksPerWhole); !length.Eq(b.Meter.Length()) {
			num, den = int(length.Num), int(length.Den)
			for den < b.Meter.Den {
				num, den = num*2, den*2
			}
		}
		if sig := [2]int{num, den}; sig != lastSig && den > 0 && den&(den-1) == 0 && num < 256 {
			t.Events = append(t.Events, TimeSignatureMeta(b.Tick, num, den))
			lastSig = sig
		}
		if k := s.KeyAt(b.Bar); k.Fifths != lastKey.Fifths || k.Mode != lastKey.Mode {
			t.Events = append(t.Events, KeySignatureMeta(b.Tick, k.Fifths, k.Mode == "minor"))
			lastKey = k
		}
	}
	for _, sec := range perf.Sections {
		t.Events = append(t.Events, TextMeta(sec.Tick, MetaMarker, sec.Name))
	}
	for _, m := range perf.Markers {
		t.Events = append(t.Events, TextMeta(m.Tick, MetaCue, m.Name))
	}
	sortTrack(&t)
	return t
}

// assignChannels gives each line a channel: 10 for a kit, otherwise the
// next of the other fifteen.
func assignChannels(s *score.Score) []uint8 {
	out := make([]uint8, len(s.Lines))
	next := uint8(0)
	for i, l := range s.Lines {
		if percussive(l) {
			out[i] = gm.DrumChannel
			continue
		}
		out[i] = next
		next = (next + 1) % 16
		if next == gm.DrumChannel {
			next++
		}
	}
	return out
}

func percussive(l *score.Line) bool {
	if gm.Percussive(l.Instrument) {
		return true
	}
	for _, st := range l.Staves {
		if st.Clef == "percussion" {
			return true
		}
	}
	return false
}

// lineTrack is one line: setup, expression and notes.
func lineTrack(perf *interpret.Performance, li int, l *score.Line, ch uint8) Track {
	var t Track
	t.Events = append(t.Events, TextMeta(0, MetaTrackName, l.Name))
	if l.Instrument != "" {
		t.Events = append(t.Events, TextMeta(0, MetaInstrument, l.Instrument))
	}
	cc := func(tick int64, c, v int) Event {
		return Event{Tick: tick, Kind: Controller, Channel: ch, A: uint8(c), B: uint8(max(0, min(127, v)))}
	}
	if ch != gm.DrumChannel {
		t.Events = append(t.Events, Event{Tick: 0, Kind: Program, Channel: ch, A: uint8(gm.Program(l.Instrument))})
	}
	t.Events = append(t.Events,
		cc(0, CCVolume, int(math.Round(float64(l.Volume)*100))),
		cc(0, CCPan, int(math.Round(64+float64(l.Pan)*63))))
	// The volume lane as expression, at its breakpoints and every 16th
	// along a slope, whenever the value moves.
	for _, c := range perf.Curves {
		if c.Line != li || c.Target != "volume" || len(c.Points) == 0 {
			continue
		}
		last := -1
		emit := func(tick float64) {
			v := int(math.Round(float64(c.At(tick)) * 127))
			if v != last {
				t.Events = append(t.Events, cc(int64(math.Round(tick)), CCExpression, v))
				last = v
			}
		}
		emit(0)
		for i, p := range c.Points {
			emit(p.Tick)
			if i+1 < len(c.Points) {
				for at := p.Tick + score.TicksPerQuarter/4; at < c.Points[i+1].Tick; at += score.TicksPerQuarter / 4 {
					emit(at)
				}
			}
		}
	}
	// Notes: a note of a pitch still sounding when the pitch starts again
	// stops just there, because one channel cannot hold a key twice.
	type on struct {
		tick, end int64
		key, vel  uint8
	}
	var ons []on
	for _, n := range perf.Notes {
		if n.Line != li || n.MIDI < 0 || n.MIDI > 127 {
			continue
		}
		vel := int(math.Round(float64(n.Velocity) * 127))
		ons = append(ons, on{n.Tick, n.Tick + max(1, n.Ticks), uint8(n.MIDI), uint8(max(1, min(127, vel)))})
	}
	sort.SliceStable(ons, func(i, j int) bool { return ons[i].tick < ons[j].tick })
	lastOf := map[uint8]int{}
	for i := range ons {
		if j, ok := lastOf[ons[i].key]; ok && ons[j].end > ons[i].tick {
			ons[j].end = max(ons[j].tick+1, ons[i].tick)
		}
		lastOf[ons[i].key] = i
	}
	var notes []Event
	for _, n := range ons {
		notes = append(notes,
			Event{Tick: n.tick, Kind: NoteOn, Channel: ch, A: n.key, B: n.vel},
			Event{Tick: n.end, Kind: NoteOff, Channel: ch, A: n.key, B: 64})
	}
	// At one tick, note-offs go before note-ons, so a repeated note is
	// heard again; then by key, so the file is the same whatever voice a
	// note was written in.
	sort.SliceStable(notes, func(i, j int) bool {
		a, b := notes[i], notes[j]
		if a.Tick != b.Tick {
			return a.Tick < b.Tick
		}
		if a.Kind != b.Kind {
			return a.Kind == NoteOff
		}
		return a.A < b.A
	})
	t.Events = append(t.Events, notes...)
	sortTrack(&t)
	return t
}

// sortTrack orders a track's events by tick, keeping the order at a tick.
func sortTrack(t *Track) {
	sort.SliceStable(t.Events, func(i, j int) bool { return t.Events[i].Tick < t.Events[j].Tick })
}

// ExportFile writes a score to a .mid file.
func ExportFile(path string, s *score.Score, o ExportOptions) error {
	f, err := Export(s, o)
	if err != nil {
		return err
	}
	if err := f.WriteFile(path); err != nil {
		return fmt.Errorf("midi: %w", err)
	}
	return nil
}
