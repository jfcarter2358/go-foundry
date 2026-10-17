package midi

// export.go — a score as a MIDI file.
//
// The file is what the score sounds like, from the interpreter
// (internal/score/interpret): repeats played out (or not, NoRepeats),
// ties joined, grace notes where they sound, dynamics as velocities, the
// shapes the interpreter gives articulations, ornaments, tremolos and
// glissandi — a slide as pitch bend when nothing else on its channel
// sounds while it bends (the channel's bend range set to the widest slide
// by RPN 0), else as the chromatic run it passes. A kit's notes are
// written as the General MIDI keys its drum map's sounds are; the sustain
// pedal is CC 64, pressed and lifted where the score's pedal marks are. It is
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
	perf, err := interpret.Interpret(s, interpret.Options{NoRepeats: o.NoRepeats, PedalPresses: true})
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
	// The volume lane as expression, the pan lane as pan and the cutoff
	// lane as brightness (CC 74, 16 Hz to 20 kHz on a log scale), at their
	// breakpoints and every 16th along a slope, whenever the value moves.
	for _, c := range perf.Curves {
		if c.Line != li || len(c.Points) == 0 {
			continue
		}
		var ctl int
		var value func(v float32) int
		switch c.Target {
		case "volume":
			ctl, value = CCExpression, func(v float32) int { return int(math.Round(float64(v) * 127)) }
		case "pan":
			ctl, value = CCPan, func(v float32) int { return int(math.Round(64 + float64(v)*63)) }
		case "cutoff":
			ctl, value = CCBrightness, func(v float32) int {
				return int(math.Round(math.Log(math.Max(float64(v), 16)/16) / math.Log(20000.0/16) * 127))
			}
		default:
			continue
		}
		last := -1
		emit := func(tick float64) {
			v := value(c.At(tick))
			if v != last {
				t.Events = append(t.Events, cc(int64(math.Round(tick)), ctl, v))
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
	var kit *score.Kit
	if ch == gm.DrumChannel {
		kit = score.KitFor(l.Instrument)
	}
	var mine []*interpret.Note
	for i := range perf.Notes {
		if n := &perf.Notes[i]; n.Line == li && n.MIDI >= 0 && n.MIDI <= 127 {
			mine = append(mine, n)
		}
	}
	bends, runs := bendable(mine, ch)
	for _, n := range mine {
		vel := uint8(max(1, min(127, int(math.Round(float64(n.Velocity)*127)))))
		key := n.MIDI
		if kit != nil {
			key, _ = kit.ToGM(key)
		}
		if runs[n] {
			// The run a slide passes, a note a semitone.
			for _, r := range slideRun(n) {
				ons = append(ons, on{r.tick, r.end, uint8(max(0, min(127, r.key))), vel})
			}
			continue
		}
		ons = append(ons, on{n.Tick, n.Tick + max(1, n.Ticks), uint8(key), vel})
	}
	t.Events = append(t.Events, bendEvents(bends, ch)...)
	// The sustain pedal as CC 64, the notes at their written lengths.
	for _, pd := range perf.Pedals {
		if pd.Line == li {
			t.Events = append(t.Events, cc(pd.Tick, CCSustain, 127), cc(pd.End, CCSustain, 0))
		}
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

// bendRange is the most semitones a channel's bend range is set to (RPN
// 0); a slide further than that is written as its run.
const bendRange = 24

// bendable sorts a line's sliding notes: those nothing else on the
// channel overlaps while they bend (written as pitch bend), and the rest
// (written as their runs). A drum channel never bends.
func bendable(notes []*interpret.Note, ch uint8) (bends []*interpret.Note, runs map[*interpret.Note]bool) {
	runs = map[*interpret.Note]bool{}
	for _, n := range notes {
		if len(n.Bend) == 0 {
			continue
		}
		alone := ch != gm.DrumChannel
		reach := 0.0
		for _, p := range n.Bend {
			reach = math.Max(reach, math.Abs(float64(p.Semis)))
		}
		if reach > bendRange {
			alone = false
		}
		for _, o := range notes {
			if o != n && o.Tick < n.Tick+max(1, n.Ticks) && n.Tick < o.Tick+max(1, o.Ticks) {
				alone = false
				break
			}
		}
		if alone {
			bends = append(bends, n)
		} else {
			runs[n] = true
		}
	}
	return bends, runs
}

// slideStep is how often a written slide's bend is sampled, in ticks (a
// 64th note).
const slideStep = score.TicksPerQuarter / 16

type runNote struct {
	tick, end int64
	key       int
}

// slideRun is a slide as a chromatic run: a note each time the bend
// passes the next semitone.
func slideRun(n *interpret.Note) []runNote {
	end := n.Tick + max(1, n.Ticks)
	var out []runNote
	cur := runNote{tick: n.Tick, key: n.MIDI + int(math.Round(float64(n.BendAt(float64(n.Tick)))))}
	for t := n.Tick + slideStep; t < end; t += slideStep {
		k := n.MIDI + int(math.Round(float64(n.BendAt(float64(t)))))
		if k != cur.key {
			cur.end = t
			out = append(out, cur)
			cur = runNote{tick: t, key: k}
		}
	}
	cur.end = end
	return append(out, cur)
}

// bendEvents are a channel's pitch bend: its range set once (RPN 0, the
// widest slide in whole semitones, then the null RPN), each slide's curve
// every slideStep while it moves, and the wheel back to the middle when
// it ends.
func bendEvents(bends []*interpret.Note, ch uint8) []Event {
	if len(bends) == 0 {
		return nil
	}
	reach := 1.0
	for _, n := range bends {
		for _, p := range n.Bend {
			reach = math.Max(reach, math.Abs(float64(p.Semis)))
		}
	}
	rng := min(bendRange, int(math.Ceil(reach)))
	cc := func(c, v int) Event { return Event{Tick: 0, Kind: Controller, Channel: ch, A: uint8(c), B: uint8(v)} }
	out := []Event{cc(101, 0), cc(100, 0), cc(6, rng), cc(38, 0), cc(101, 127), cc(100, 127)}
	wheel := func(tick int64, semis float64) Event {
		v := int(math.Round(8192 + semis/float64(rng)*8192))
		v = max(0, min(16383, v))
		return Event{Tick: tick, Kind: PitchBend, Channel: ch, A: uint8(v & 0x7F), B: uint8(v >> 7)}
	}
	for _, n := range bends {
		end := n.Tick + max(1, n.Ticks)
		last := -1.0
		for t := n.Tick; t < end; t += slideStep {
			v := float64(n.BendAt(float64(t)))
			if math.Abs(v-last) > 1e-3 {
				out = append(out, wheel(t, v))
				last = v
			}
		}
		out = append(out, wheel(end, 0))
	}
	return out
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
