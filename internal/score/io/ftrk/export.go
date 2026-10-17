// Package ftrk turns a score into a Tracker song (.ftrk) and a song into
// a score.
//
// A song is a grid: rows of cells, a fixed number of rows a beat. A score
// is not, so a song made of one is quantised: Export plays the score
// (internal/score/interpret, repeats played out) and writes every note at
// the row nearest its start, held to the row nearest its end, on the grid
// of rows a quarter note that holds the score's rhythms exactly when one
// will do (16ths are 4 rows a beat, 16th triplets 6, both 12). Each line
// becomes as many channels as it has notes sounding together, each line
// one instrument naming the line's .finst, so the song plays the same
// library sounds; velocities go in the volume column, articulations as @
// commands, tempo changes as M, score markers as E.
//
// Import goes the other way through the MIDI importer (import.go): the
// song is played through once into notes in time and those are written
// as notation with the MIDI import's quantising, voices and dynamics.
package ftrk

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/interpret"
	"foundry/internal/score/play"
	"foundry/internal/tracker"
)

// ExportOptions shape an export.
type ExportOptions struct {
	// RowsPerBeat is the grid's rows a quarter note (the song's LPB,
	// 1..32). Zero chooses the fewest rows that hold every note exactly,
	// up to MaxAutoRows, and failing that MaxAutoRows.
	RowsPerBeat int
	// NoRepeats writes the score once through as written.
	NoRepeats bool
}

// MaxAutoRows is the finest grid an export chooses by itself: 24 rows a
// beat hold 32nds' triplets and 16ths together.
const MaxAutoRows = 24

// gridSizes are the rows a beat that divide 960 ticks a quarter evenly,
// coarsest first: what the grid may be.
var gridSizes = []int{1, 2, 3, 4, 6, 8, 12, 16, 24, 32}

// The effect columns: a note's articulation in the first, the song-wide
// commands (tempo, markers) in the second of the first channel free.
const (
	fxArt    = 0
	fxGlobal = 1
)

// gridNote is a note of the performance on the grid.
type gridNote struct {
	line       int
	start, end int // rows
	key        uint8
	vol        uint8
	art        int // the instrument's articulation (1-based), 0 its default
}

// Export makes a song of a score. The warnings are what the song could
// not hold.
func Export(s *score.Score, o ExportOptions) (song *tracker.Song, warnings []string, err error) {
	defer score.RecoverOverflow(&err)
	seen := map[string]bool{}
	warn := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		if !seen[msg] {
			seen[msg] = true
			warnings = append(warnings, msg)
		}
	}
	if len(s.Lines) == 0 {
		return nil, nil, fmt.Errorf("ftrk: the score has no lines")
	}
	insts := make([]*tracker.Instrument, len(s.Lines))
	for i, l := range s.Lines {
		if l.Instrument == "" {
			continue
		}
		if in, err := play.LoadInstrument(l.Instrument); err == nil {
			insts[i] = in
		} else {
			warn("line %s: %v (its articulations are not written)", l.Name, err)
		}
	}
	perf, err := interpret.Interpret(s, interpret.Options{NoRepeats: o.NoRepeats, NoSlides: true, Instruments: insts})
	if err != nil {
		return nil, nil, err
	}
	lpb := o.RowsPerBeat
	if lpb == 0 {
		lpb = chooseGrid(perf)
	}
	if lpb < 1 || lpb > 32 || score.TicksPerQuarter%lpb != 0 {
		return nil, nil, fmt.Errorf("ftrk: %d rows a beat (want one of %v)", lpb, gridSizes)
	}
	rowTicks := int64(score.TicksPerQuarter / lpb)
	row := func(tick int64) int { return int(math.Round(float64(tick) / float64(rowTicks))) }
	totalRows := max(1, row(perf.Length))

	// Notes onto the grid, each line's dealt to its channels.
	var notes []gridNote
	offGrid := 0
	for _, n := range perf.Notes {
		if n.MIDI < 12 || n.MIDI > 131 {
			warn("a note at MIDI %d is outside the Tracker's C-0..B-9 and is left out", n.MIDI)
			continue
		}
		a, z := row(n.Tick), row(n.Tick+n.Ticks)
		if n.Tick%rowTicks != 0 || (n.Tick+n.Ticks)%rowTicks != 0 {
			offGrid++
		}
		if z <= a {
			z = a + 1
		}
		vol := uint8(math.Round(math.Max(1, math.Min(64, float64(n.Velocity)*64))))
		art := 0
		if in := insts[n.Line]; in != nil {
			if art = in.ArticulationIndex(n.Articulation); art == 0 {
				art = in.ArticulationIndex(n.Technique)
			}
		}
		notes = append(notes, gridNote{line: n.Line, start: a, end: min(z, totalRows), key: uint8(n.MIDI - 11), vol: vol, art: art})
	}
	if offGrid > 0 {
		warn("%d note start(s) or end(s) moved to the nearest of %d rows a beat", offGrid, lpb)
	}
	sort.SliceStable(notes, func(i, j int) bool {
		if notes[i].line != notes[j].line {
			return notes[i].line < notes[j].line
		}
		if notes[i].start != notes[j].start {
			return notes[i].start < notes[j].start
		}
		return notes[i].key < notes[j].key
	})
	type channel struct {
		line   int
		free   int // the row it is free from
		art    int
		events []int // into notes
	}
	var chans []*channel
	firstChan := make([]int, len(s.Lines))
	for li := range s.Lines {
		firstChan[li] = len(chans)
		var mine []*channel
		for ni, n := range notes {
			if n.line != li {
				continue
			}
			var c *channel
			for _, m := range mine {
				if m.free <= n.start {
					c = m
					break
				}
			}
			if c == nil {
				c = &channel{line: li}
				mine = append(mine, c)
				chans = append(chans, c)
			}
			c.events = append(c.events, ni)
			c.free = n.end
		}
		if len(mine) == 0 {
			// A silent line keeps a channel: its mixer settings and name.
			chans = append(chans, &channel{line: li})
		}
	}
	if len(chans) > tracker.MaxChannels {
		return nil, warnings, fmt.Errorf("ftrk: the score needs %d channels (a song holds %d)", len(chans), tracker.MaxChannels)
	}

	// The song and its mixer.
	song = tracker.NewSong(s.Name)
	song.LPB, song.TicksPerRow, song.FXColumns = lpb, 6, 2
	song.Loop = s.Loop
	song.Gain = s.Gain
	song.BeatEvents = true
	m0 := s.MeterAt(1)
	song.BeatsPerBar = max(1, int(m0.Length().MulInt(4).Round()))
	song.Channels = nil
	count := map[int]int{}
	for _, c := range chans {
		l := s.Lines[c.line]
		count[c.line]++
		name := l.Name
		if count[c.line] > 1 {
			name = fmt.Sprintf("%s %d", l.Name, count[c.line])
		}
		ch := tracker.Channel{Name: name, Volume: clamp01(l.Volume), Pan: l.Pan, SendA: l.SendA, SendB: l.SendB, Mute: play.LineSilenced(s, c.line)}
		for _, f := range l.Inserts {
			switch {
			case f.Bypass:
			case f.Kind == "drive":
				ch.Drive = clamp01(float32(f.Param("drive", 0.3)))
			case f.Kind == "crusher":
				ch.Crush = int(f.Param("bits", 8))
			default:
				warn("line %s: the %s effect is not in a song (a channel has drive and crush only)", l.Name, f.Kind)
			}
		}
		if len(l.Lanes) > 0 {
			warn("line %s: automation lanes are not in a song", l.Name)
		}
		song.Channels = append(song.Channels, ch)
	}
	song.Instruments = nil
	for _, l := range s.Lines {
		in := tracker.NewInstrument(l.Name, "")
		in.Ref = l.Instrument
		song.Instruments = append(song.Instruments, in)
	}
	exportMix(s, song, warn)
	for _, p := range s.Params {
		song.Params = append(song.Params, tracker.Param{Name: p.Name, Default: p.Default})
	}
	for _, b := range s.Bindings {
		li := s.LineIndex(b.Line)
		if li < 0 {
			continue
		}
		song.Bindings = append(song.Bindings, tracker.Binding{Param: b.Param, Channel: song.Channels[firstChan[li]].Name, Target: b.Target, From: b.From, To: b.To})
	}

	// The cells, as one long grid first.
	grid := make([][]tracker.Cell, totalRows)
	for r := range grid {
		grid[r] = make([]tracker.Cell, len(chans))
		for c := range grid[r] {
			grid[r][c] = tracker.EmptyCell
		}
	}
	for ci, c := range chans {
		for k, ni := range c.events {
			n := notes[ni]
			if n.start >= totalRows {
				continue
			}
			cell := &grid[n.start][ci]
			cell.Note, cell.Instr, cell.Vol = n.key, uint8(n.line+1), n.vol
			if n.art != c.art {
				cell.FX[fxArt] = tracker.Effect{Cmd: tracker.FxArticulation, Param: uint8(n.art)}
				c.art = n.art
			}
			// A note off where it ends, unless the next note starts there.
			next := totalRows
			if k+1 < len(c.events) {
				next = notes[c.events[k+1]].start
			}
			if n.end < next && n.end < totalRows {
				grid[n.end][ci].Note = tracker.NoteOff
			}
		}
	}
	// Tempo: M at each change (a ramp a beat at a time), BPM the first.
	global := func(r int, e tracker.Effect) bool {
		for ci := 0; ci < len(chans); ci++ {
			if r < totalRows && grid[r][ci].FX[fxGlobal].Cmd == 0 {
				grid[r][ci].FX[fxGlobal] = e
				return true
			}
		}
		return false
	}
	bpm := func(q float64) uint8 {
		if q < 20 || q > 255 {
			warn("a tempo of %.0f is outside the Tracker's 20..255 and is clamped", q)
		}
		return uint8(math.Round(math.Max(20, math.Min(255, q))))
	}
	pts := perf.Tempo.Points
	if len(pts) > 0 {
		song.BPM = float32(bpm(pts[0].QPM))
	}
	last := song.BPM
	setTempo := func(tick float64, q float64) {
		b := bpm(q)
		if float32(b) == last {
			return
		}
		if !global(row(int64(tick)), tracker.Effect{Cmd: tracker.FxTempo, Param: b}) {
			warn("no free effect column for a tempo change")
		}
		last = float32(b)
	}
	for i, p := range pts {
		if i > 0 {
			setTempo(p.Tick, p.QPM)
		}
		if p.Ramp && i+1 < len(pts) {
			for at := p.Tick + score.TicksPerQuarter; at < pts[i+1].Tick; at += score.TicksPerQuarter {
				setTempo(at, perf.Tempo.QPM(at))
			}
		}
	}
	for i, mk := range perf.Markers {
		if i >= 255 {
			warn("past 255 markers; the rest are left out")
			break
		}
		if song.Markers == nil {
			song.Markers = map[uint8]string{}
		}
		id := uint8(i + 1)
		song.Markers[id] = mk.Name
		if !global(row(mk.Tick), tracker.Effect{Cmd: tracker.FxMarker, Param: id}) {
			warn("no free effect column for marker %s", mk.Name)
		}
	}

	// Patterns: up to four bars (as many as fit in 256 rows), broken where
	// a section starts so sections are whole order positions; a pattern
	// the same as one before is that one again.
	cuts := map[int]bool{0: true}
	for _, sec := range perf.Sections {
		cuts[row(sec.Tick)] = true
	}
	var bounds []int
	barsIn := 0
	start := 0
	for bi, b := range perf.Bars {
		r := row(b.Tick)
		end := row(b.Tick + b.Ticks)
		if bi == len(perf.Bars)-1 {
			end = totalRows
		}
		if r > start && (cuts[r] || barsIn >= 4 || end-start > tracker.MaxRows) {
			bounds = append(bounds, start)
			start, barsIn = r, 0
		}
		barsIn++
		for end-start > tracker.MaxRows {
			// One bar longer than a pattern: split it.
			bounds = append(bounds, start)
			start += tracker.MaxRows
			barsIn = 1
		}
	}
	bounds = append(bounds, start, totalRows)
	song.Patterns, song.Order = nil, nil
	seenPat := map[string]int{}
	orderAt := map[int]int{} // row → order index
	for i := 0; i+1 < len(bounds); i++ {
		a, z := bounds[i], bounds[i+1]
		if z <= a {
			continue
		}
		p := tracker.NewPattern("", z-a, len(chans))
		for r := a; r < z; r++ {
			copy(p.Cells[r-a], grid[r])
		}
		key := tracker.FormatPattern(p, song.FXColumns)
		idx, ok := seenPat[key]
		if !ok {
			if len(song.Patterns) >= tracker.MaxPatterns {
				return nil, warnings, fmt.Errorf("ftrk: the score needs more than %d patterns", tracker.MaxPatterns)
			}
			idx = len(song.Patterns)
			seenPat[key] = idx
			song.Patterns = append(song.Patterns, p)
		}
		orderAt[a] = len(song.Order)
		song.Order = append(song.Order, idx)
	}
	for _, sec := range perf.Sections {
		a, ok := orderAt[row(sec.Tick)]
		if !ok {
			continue
		}
		z := len(song.Order)
		if e, ok := orderAt[row(sec.End)]; ok && e > a {
			z = e
		}
		ts := tracker.Section{Name: sec.Name, Start: a, End: z, Loop: sec.Loop}
		if sec.Next >= 0 && sec.Next < len(s.Sections) {
			ts.Next = s.Sections[sec.Next].Name
		}
		if song.SectionIndex(sec.Name) < 0 {
			song.Sections = append(song.Sections, ts)
		}
	}
	for _, p := range song.Validate() {
		warn("%s", p)
	}
	return song, warnings, nil
}

// chooseGrid is the fewest rows a beat that put every note's start and end
// on a row, up to MaxAutoRows (or that many when none does).
func chooseGrid(perf *interpret.Performance) int {
	for _, g := range gridSizes {
		if g > MaxAutoRows {
			break
		}
		rt := int64(score.TicksPerQuarter / g)
		ok := true
		for _, n := range perf.Notes {
			if n.Tick%rt != 0 || (n.Tick+n.Ticks)%rt != 0 {
				ok = false
				break
			}
		}
		if ok {
			return g
		}
	}
	return MaxAutoRows
}

// exportMix sets the song's reverb, delay and compressor from the score's
// buses and master chain.
func exportMix(s *score.Score, song *tracker.Song, warn func(string, ...any)) {
	bus := func(b score.Bus, name, want string) (score.FX, bool) {
		var found score.FX
		ok := false
		for _, f := range b.FX {
			if f.Kind == want && !f.Bypass && !ok {
				found, ok = f, true
				continue
			}
			warn("bus %s: the %s effect is not in a song (bus %s is a %s)", name, f.Kind, name, want)
		}
		return found, ok
	}
	if f, ok := bus(s.Buses.A, "A", "reverb"); ok {
		song.Reverb = tracker.ReverbSpec{Size: float32(f.Param("size", 0.6)), Damping: float32(f.Param("damping", 0.4)), Width: float32(f.Param("width", 1)), Return: s.Buses.A.Return}
	} else {
		song.Reverb.Return = 0
	}
	if f, ok := bus(s.Buses.B, "B", "delay"); ok {
		song.Delay = tracker.DelaySpec{Beats: float32(f.Param("beats", 0.75)), Feedback: float32(f.Param("feedback", 0.35)), Tone: float32(f.Param("tone", 0.6)),
			PingPong: f.Param("ping_pong", 1) > 0.5, Return: s.Buses.B.Return}
	} else {
		song.Delay.Return = 0
	}
	for _, f := range s.Master {
		if f.Kind != "compressor" || f.Bypass {
			warn("the master's %s effect is not in a song (its master is a compressor)", f.Kind)
			continue
		}
		song.Compressor = tracker.CompressorSpec{Enabled: f.Param("enabled", 1) > 0.5, Threshold: float32(f.Param("threshold", -12)),
			Ratio: float32(f.Param("ratio", 3)), Attack: float32(f.Param("attack", 0.005)), Release: float32(f.Param("release", 0.15)),
			Makeup: float32(f.Param("makeup", 0))}
	}
}

func clamp01(v float32) float32 { return float32(math.Max(0, math.Min(1, float64(v)))) }

// SongName is the song's library name for a score's: the same name.
func SongName(scoreName string) string { return strings.TrimSuffix(scoreName, score.FileExt) }
