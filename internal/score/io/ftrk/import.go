package ftrk

// import.go — a Tracker song as a score.
//
// A song's patterns say when notes sound, not how they are written, which
// is a MIDI file's situation exactly: so the song is played through once
// into one (ToMIDI) and the MIDI importer writes the notation, with the
// same dialog — grid, voices, key, meter, dynamics — and the same
// guesses. What only a song knows comes over after: each instrument is a
// line playing the .finst its slot names, the sections keep their loops
// and next sections, the params and bindings come along, and the reverb,
// delay and compressor become the score's buses and master.
//
// Playing through follows the order list once: a pattern break (K) moves
// on early, a jump (B) forward is taken and one back (a loop) ends the
// pass, a row loop (L) plays once. Note delays (N) and cuts (C) move the
// notes; tempo commands (M) are tempo changes, markers (E) the score's
// markers.

import (
	"fmt"
	"math"
	"sort"

	"foundry/internal/score"
	"foundry/internal/score/io/gm"
	"foundry/internal/score/io/midi"
	"foundry/internal/tracker"
)

// division is the MIDI file's ticks a quarter, for a song's rows a beat.
func division(lpb int) int {
	if 960%lpb == 0 {
		return 960
	}
	return lpb * 60
}

// playedNote is a note as the song plays it.
type playedNote struct {
	on, off  int64
	key, vel int
	instr    int // 1-based
	channel  int
}

// ToMIDI plays a song through once into a MIDI file: a conductor track
// (tempo, time signature, sections as markers, marker commands as cue
// points) and a track for each instrument that plays, named after it,
// with the library instrument it names as its instrument name, its
// General MIDI program, and its main channel's volume and pan.
func ToMIDI(song *tracker.Song) (*midi.File, []string, error) {
	var warnings []string
	seen := map[string]bool{}
	warn := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		if !seen[msg] {
			seen[msg] = true
			warnings = append(warnings, msg)
		}
	}
	lpb := max(1, song.LPB)
	div := division(lpb)
	rowTicks := int64(div / lpb)
	tpr := max(1, song.TicksPerRow)
	conductor := midi.Track{}
	name := song.Name
	if name == "" {
		name = "song"
	}
	conductor.Events = append(conductor.Events, midi.TextMeta(0, midi.MetaTrackName, name))
	bpm := float64(song.BPM)
	if bpm <= 0 {
		bpm = 120
	}
	conductor.Events = append(conductor.Events, midi.TempoMeta(0, bpm))
	conductor.Events = append(conductor.Events, midi.TimeSignatureMeta(0, max(1, song.BeatsPerBar), 4))

	open := make([]*playedNote, len(song.Channels))
	lastInstr := make([]int, len(song.Channels))
	var notes []playedNote
	end := func(c int, at int64) {
		if n := open[c]; n != nil {
			n.off = max(n.on+1, at)
			notes = append(notes, *n)
			open[c] = nil
		}
	}
	var tick int64
	visited := map[int]bool{}
	sectionAt := map[int][]string{}
	for _, sec := range song.Sections {
		sectionAt[sec.Start] = append(sectionAt[sec.Start], sec.Name)
	}
	startRow := 0
	const maxRows = 1 << 20
	rows := 0
	for oi := 0; oi < len(song.Order) && rows < maxRows; {
		if visited[oi] {
			break
		}
		visited[oi] = true
		for _, sn := range sectionAt[oi] {
			conductor.Events = append(conductor.Events, midi.TextMeta(tick, midi.MetaMarker, sn))
		}
		p := song.Patterns[song.Order[oi]]
		next, nextRow := oi+1, 0
		jumped := false
		for r := startRow; r < p.Rows() && !jumped; r++ {
			rows++
			for c, cell := range p.Cells[r] {
				if c >= len(song.Channels) {
					break
				}
				at := tick
				cut := int64(-1)
				for _, e := range cell.FX {
					switch e.Cmd {
					case tracker.FxNoteDelay:
						at = tick + int64(e.Param)*rowTicks/int64(tpr)
					case tracker.FxNoteCut:
						cut = tick + int64(e.Param)*rowTicks/int64(tpr)
					case tracker.FxTempo:
						if e.Param >= 20 {
							conductor.Events = append(conductor.Events, midi.TempoMeta(tick, float64(e.Param)))
						}
					case tracker.FxMarker:
						mk := song.Markers[e.Param]
						if mk == "" {
							mk = fmt.Sprintf("marker %d", e.Param)
						}
						conductor.Events = append(conductor.Events, midi.TextMeta(tick, midi.MetaCue, mk))
					case tracker.FxBreak:
						nextRow, jumped = int(e.Param), true
					case tracker.FxJump:
						if int(e.Param) > oi {
							next, nextRow, jumped = int(e.Param), 0, true
						} else {
							warn("the jump back at order %d is a loop: the song is read once through", oi)
							next, jumped = len(song.Order), true
						}
					case tracker.FxLoop:
						if e.Param > 0 {
							warn("row loops (L) are read once through")
						}
					}
				}
				switch {
				case cell.Note >= tracker.NoteMin && cell.Note <= tracker.NoteMax:
					end(c, at)
					instr := int(cell.Instr)
					if instr == 0 {
						instr = lastInstr[c]
					}
					lastInstr[c] = instr
					if instr < 1 || instr > len(song.Instruments) {
						warn("a note on channel %s plays no instrument and is left out", song.Channels[c].Name)
						continue
					}
					vol := tracker.VolMax
					if cell.Vol != tracker.VolNone {
						vol = min(cell.Vol, tracker.VolMax)
					}
					vel := int(math.Round(float64(vol) / float64(tracker.VolMax) * 127))
					open[c] = &playedNote{on: at, key: int(cell.Note) + 11, vel: max(1, min(127, vel)), instr: instr, channel: c}
				case cell.Note == tracker.NoteOff || cell.Note == tracker.NoteCut:
					end(c, at)
				default:
					if cell.Instr != 0 {
						lastInstr[c] = int(cell.Instr)
					}
				}
				if cut >= 0 {
					end(c, cut)
				}
			}
			tick += rowTicks
		}
		oi, startRow = next, nextRow
	}
	for c := range open {
		end(c, tick)
	}
	if rows >= maxRows {
		warn("the song was read for its first %d rows", maxRows)
	}

	f := &midi.File{Format: 1, Division: div, Tracks: []midi.Track{conductor}}
	// One track an instrument, in instrument order.
	byInstr := map[int][]playedNote{}
	for _, n := range notes {
		byInstr[n.instr] = append(byInstr[n.instr], n)
	}
	var order []int
	for k := range byInstr {
		order = append(order, k)
	}
	sort.Ints(order)
	nextCh := 0
	for _, k := range order {
		in := song.Instruments[k-1]
		ns := byInstr[k]
		t := midi.Track{}
		t.Events = append(t.Events, midi.TextMeta(0, midi.MetaTrackName, in.Name))
		ref := in.Ref
		if ref == "" {
			if ref = libraryFor(in); ref != "" {
				warn("%s is the song's own instrument: the score plays the library's %s", in.Name, ref)
			}
		}
		if ref != "" {
			t.Events = append(t.Events, midi.TextMeta(0, midi.MetaInstrument, ref))
		}
		ch := uint8(gm.DrumChannel)
		if !gm.Percussive(ref) {
			ch = uint8(nextCh)
			nextCh = (nextCh + 1) % 16
			if nextCh == gm.DrumChannel {
				nextCh++
			}
			prog := 0
			if ref != "" {
				prog = gm.Program(ref)
			} else if m := gm.Match(in.Name); m != "" {
				prog = gm.Program(m)
			}
			t.Events = append(t.Events, midi.Event{Kind: midi.Program, Channel: ch, A: uint8(prog)})
		}
		// The channel that plays it most is its mixer.
		use := map[int]int{}
		for _, n := range ns {
			use[n.channel]++
		}
		main, most := 0, -1
		for c, u := range use {
			if u > most || u == most && c < main {
				main, most = c, u
			}
		}
		mc := song.Channels[main]
		t.Events = append(t.Events,
			midi.Event{Kind: midi.Controller, Channel: ch, A: midi.CCVolume, B: uint8(math.Round(math.Max(0, math.Min(1.27, float64(mc.Volume))) * 100))},
			midi.Event{Kind: midi.Controller, Channel: ch, A: midi.CCPan, B: uint8(math.Round(64 + math.Max(-1, math.Min(1, float64(mc.Pan)))*63))})
		// One key at a time on a channel: a key struck again ends itself.
		sort.SliceStable(ns, func(i, j int) bool { return ns[i].on < ns[j].on })
		lastOf := map[int]int{}
		for i := range ns {
			if j, ok := lastOf[ns[i].key]; ok && ns[j].off > ns[i].on {
				ns[j].off = max(ns[j].on+1, ns[i].on)
			}
			lastOf[ns[i].key] = i
		}
		var evs []midi.Event
		for _, n := range ns {
			if n.key < 0 || n.key > 127 {
				continue
			}
			evs = append(evs,
				midi.Event{Tick: n.on, Kind: midi.NoteOn, Channel: ch, A: uint8(n.key), B: uint8(n.vel)},
				midi.Event{Tick: n.off, Kind: midi.NoteOff, Channel: ch, A: uint8(n.key), B: 64})
		}
		sort.SliceStable(evs, func(i, j int) bool {
			if evs[i].Tick != evs[j].Tick {
				return evs[i].Tick < evs[j].Tick
			}
			return evs[i].Kind == midi.NoteOff && evs[j].Kind != midi.NoteOff
		})
		t.Events = append(t.Events, evs...)
		f.Tracks = append(f.Tracks, t)
	}
	sort.SliceStable(f.Tracks[0].Events, func(i, j int) bool { return f.Tracks[0].Events[i].Tick < f.Tracks[0].Events[j].Tick })
	if len(f.Tracks) == 1 {
		return nil, warnings, fmt.Errorf("ftrk: the song plays no notes")
	}
	return f, warnings, nil
}

// libraryFor is the library instrument nearest a song's own (embedded)
// instrument: the library's instrument of its first sample's name (the
// starter library has one per synth sample, drums/kick_808 for the
// sample drums/kick_808), else one its name matches; "" when there is
// none.
func libraryFor(in *tracker.Instrument) string {
	for _, smp := range in.Samples {
		if tracker.InstrumentPath(smp) != "" {
			return smp
		}
	}
	if m := gm.Match(in.Name); m != "" && tracker.InstrumentPath(m) != "" {
		return m
	}
	return ""
}

// Analyze is a song as an import dialog shows it: its parts (one an
// instrument that plays), the file the MIDI importer reads, and the
// choices that keep each instrument's library sound.
func Analyze(song *tracker.Song) (*midi.Analysis, *midi.File, []midi.Choice, []string, error) {
	f, warns, err := ToMIDI(song)
	if err != nil {
		return nil, nil, nil, warns, err
	}
	a := midi.Analyze(f)
	choices := make([]midi.Choice, len(a.Parts))
	for i, p := range a.Parts {
		if ref := instrumentOfTrack(f, p.Track); ref != "" && tracker.InstrumentPath(ref) != "" {
			choices[i].Instrument = ref
		}
	}
	return a, f, choices, warns, nil
}

// instrumentOfTrack is the library instrument a track names.
func instrumentOfTrack(f *midi.File, ti int) string {
	if ti < 0 || ti >= len(f.Tracks) {
		return ""
	}
	for _, e := range f.Tracks[ti].Events {
		if e.Kind == midi.Meta && e.MetaType == midi.MetaInstrument {
			return e.Text()
		}
	}
	return ""
}

// Import makes a score of a song with the MIDI importer's options; a
// choice with no instrument keeps its slot's library instrument.
func Import(song *tracker.Song, o midi.Options) (*score.Score, []string, error) {
	a, f, choices, warns, err := Analyze(song)
	if err != nil {
		return nil, warns, err
	}
	for i := range choices {
		if i < len(o.Choices) {
			c := o.Choices[i]
			if c.Instrument == "" {
				c.Instrument = choices[i].Instrument
			}
			choices[i] = c
		}
	}
	o.Choices = choices
	if o.Name == "" {
		o.Name = song.Name
	}
	s, mw, err := midi.Import(f, o)
	warns = append(warns, mw...)
	if err != nil {
		return nil, warns, err
	}
	s.Loop = song.Loop
	if song.Gain > 0 {
		s.Gain = song.Gain
	}
	for i := range s.Sections {
		if k := song.SectionIndex(s.Sections[i].Name); k >= 0 {
			s.Sections[i].Loop = song.Sections[k].Loop
			s.Sections[i].Next = song.Sections[k].Next
		}
	}
	for _, p := range song.Params {
		s.Params = append(s.Params, score.Param{Name: p.Name, Default: p.Default})
	}
	// A binding's channel is the line its instrument plays on most.
	lineOf := map[int]string{}
	for pi, p := range a.Parts {
		if pi < len(choices) && choices[pi].Skip {
			continue
		}
		if p.Track-1 >= 0 && p.Track-1 < len(f.Tracks)-1 {
			lineOf[p.Track] = p.Name
		}
	}
	for _, b := range song.Bindings {
		c := song.ChannelIndex(b.Channel)
		line := ""
		if c >= 0 {
			line = channelLine(song, c, f, lineOf)
		}
		if line == "" || s.LineIndex(line) < 0 {
			warns = append(warns, fmt.Sprintf("binding of %s to channel %s has no line to go on", b.Param, b.Channel))
			continue
		}
		s.Bindings = append(s.Bindings, score.Binding{Param: b.Param, Line: line, Target: b.Target, From: b.From, To: b.To})
	}
	importMix(song, s)
	return s, warns, nil
}

// channelLine is the line a channel's notes went to: its most played
// instrument's track's part.
func channelLine(song *tracker.Song, c int, f *midi.File, lineOf map[int]string) string {
	count := map[uint8]int{}
	for _, oi := range song.Order {
		for _, row := range song.Patterns[oi].Cells {
			if c < len(row) && row[c].Instr != 0 {
				count[row[c].Instr]++
			}
		}
	}
	best, most := uint8(0), 0
	for in, n := range count {
		if n > most || n == most && in < best {
			best, most = in, n
		}
	}
	if best == 0 || int(best) > len(song.Instruments) {
		return ""
	}
	name := song.Instruments[best-1].Name
	for ti := 1; ti < len(f.Tracks); ti++ {
		for _, e := range f.Tracks[ti].Events {
			if e.Kind == midi.Meta && e.MetaType == midi.MetaTrackName && e.Text() == name {
				return lineOf[ti]
			}
		}
	}
	return ""
}

// importMix makes the song's reverb, delay and compressor the score's
// buses and master.
func importMix(song *tracker.Song, s *score.Score) {
	r, d, c := song.Reverb, song.Delay, song.Compressor
	s.Buses.A = score.Bus{FX: []score.FX{{Kind: "reverb", Params: []score.FXParam{{Name: "size", Value: float64(r.Size)}, {Name: "damping", Value: float64(r.Damping)}, {Name: "width", Value: float64(r.Width)}}}}, Return: r.Return}
	pp := 0.0
	if d.PingPong {
		pp = 1
	}
	s.Buses.B = score.Bus{FX: []score.FX{{Kind: "delay", Params: []score.FXParam{{Name: "beats", Value: float64(d.Beats)}, {Name: "feedback", Value: float64(d.Feedback)}, {Name: "tone", Value: float64(d.Tone)}, {Name: "ping_pong", Value: pp}}}}, Return: d.Return}
	en := 0.0
	if c.Enabled {
		en = 1
	}
	s.Master = []score.FX{{Kind: "compressor", Params: []score.FXParam{{Name: "enabled", Value: en}, {Name: "threshold", Value: float64(c.Threshold)}, {Name: "ratio", Value: float64(c.Ratio)},
		{Name: "attack", Value: float64(c.Attack)}, {Name: "release", Value: float64(c.Release)}, {Name: "makeup", Value: float64(c.Makeup)}}}}
	// Each line takes the sends of the channel its instrument plays on most.
	for _, l := range s.Lines {
		for ci, ch := range song.Channels {
			if ch.Name == l.Name || channelPlays(song, ci, l.Instrument) {
				l.SendA, l.SendB = ch.SendA, ch.SendB
				break
			}
		}
	}
}

// channelPlays reports whether a channel plays a library instrument.
func channelPlays(song *tracker.Song, c int, ref string) bool {
	if ref == "" {
		return false
	}
	for _, oi := range song.Order {
		for _, row := range song.Patterns[oi].Cells {
			if c < len(row) && row[c].Instr != 0 && int(row[c].Instr) <= len(song.Instruments) && song.Instruments[row[c].Instr-1].Ref == ref {
				return true
			}
		}
	}
	return false
}
