package midi

// import.go — a MIDI file as a score.
//
// A MIDI file knows when notes sound; a score needs them written. Import
// does what a notation program's MIDI import dialog does, in this order:
//
//  1. Parts: every track and channel that has notes is a part (a format-0
//     file is split by channel), named from its track name or program, its
//     instrument guessed from the name and then the General MIDI program
//     (internal/score/io/gm), channel 10 the drum kit.
//  2. The maps: time signatures make the bars (a 1/4 bar before 4/4 is a
//     pickup), key signatures the keys, tempo changes tempo marks (a run of
//     small steps is a ramp), markers at bar lines the rehearsal marks.
//     With no time signature the meter is guessed from where the strong
//     notes fall; with no key signature the key from the pitches.
//  3. Notes: quantised to the grid (per beat, plain or triplet), split onto
//     a grand staff at the split point when the instrument is a piano,
//     separated into voices, velocity turned into dynamic marks, written
//     as bars (notate.Write) with pitches spelled in the key.
//
// Analyze is step 1 and the guesses of step 2 alone, for a dialog to show
// and change before Import.

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/io/gm"
	"foundry/internal/score/io/notate"
)

// Part is one track-and-channel of a file with notes: a line of the score
// unless skipped.
type Part struct {
	Track, Channel int
	Name           string
	Notes          int
	// Program is the first program change, -1 for none.
	Program    int
	Percussion bool
	// Low and High are the MIDI range.
	Low, High int
	// Instrument and Clef are the guesses: a library .finst and a staff
	// clef, or "grand" for a treble and bass pair.
	Instrument string
	Clef       string
	// Volume and Pan are from the part's first volume and pan controllers.
	Volume, Pan float32

	notes  []rawNote
	expr   []ccPoint // expression (CC 11), the volume lane
	hasVol bool
}

type rawNote struct {
	on, off int64
	pitch   int
	vel     int
}

type ccPoint struct {
	tick  int64
	value int
}

// Analysis is what a file holds before it is imported.
type Analysis struct {
	Format, Tracks, Division int
	Notes                    int
	Parts                    []Part
	// Meter and Fifths/Minor are the file's first time and key signature,
	// or the guesses when it has none (MeterGuessed, KeyGuessed).
	Meter        score.Meter
	MeterGuessed bool
	Fifths       int
	Minor        bool
	KeyGuessed   bool
	// Tempo is the opening tempo (quarters a minute); TempoChanges how many
	// changes follow it.
	Tempo        float64
	TempoChanges int

	f        *File
	tempos   []tempoAt
	meters   []meterAt
	keys     []keyAt
	markers  []textAt
	cues     []textAt
	lastTick int64
	lastNote int64        // the last note's end
	barStart []score.Frac // set by buildBars
}

type tempoAt struct {
	tick int64
	qpm  float64
}

type meterAt struct {
	tick     int64
	num, den int
}

type keyAt struct {
	tick   int64
	fifths int
	minor  bool
}

type textAt struct {
	tick int64
	text string
}

// Choice is what to do with one part (Analysis.Parts, by index).
type Choice struct {
	Skip bool
	// Name, Instrument and Clef replace the guesses when set; Clef may be
	// "grand".
	Name, Instrument, Clef string
}

// Options shape an import. The zero value is the dialog's defaults.
type Options struct {
	// Name is the score's library name; Title its title (Name if empty).
	Name, Title string
	// Grid is what notes snap to (zero: 16ths with triplets).
	Grid notate.Grid
	// Voices is the most voices a staff gets (zero: 4); 1 writes overlapping
	// notes as chords in one voice.
	Voices int
	// NoDynamics keeps velocities out of the notation (every line plays at
	// the default level).
	NoDynamics bool
	// NoLegato keeps every gap a player left between notes as a rest
	// (notate.Legato closes the small ones).
	NoLegato bool
	// NoGraces keeps notes played as grace notes (a flick just before
	// the beat, notate.MarkGraces) as ordinary notes on the grid.
	NoGraces bool
	// Split is the lowest note of a grand staff's upper staff (zero: 60,
	// middle C).
	Split int
	// Key and Meter, when set, replace the file's (or the guess) for the
	// whole score.
	Key   *score.KeyMark
	Meter *score.Meter
	// Choices are per part, in Analysis.Parts order; missing ones take the
	// guesses.
	Choices []Choice
}

// Analyze reads a file's parts and maps and makes the guesses Import would.
func Analyze(f *File) *Analysis {
	a := &Analysis{f: f, Format: f.Format, Tracks: len(f.Tracks), Division: f.Division}
	type key struct{ track, ch int }
	parts := map[key]*Part{}
	var order []key
	for ti, t := range f.Tracks {
		trackName := ""
		open := map[[2]int][]rawNote{} // (channel, pitch) → sounding, oldest first
		var trackEnd int64
		get := func(ch int) *Part {
			k := key{ti, ch}
			p := parts[k]
			if p == nil {
				p = &Part{Track: ti, Channel: ch, Program: -1, Percussion: ch == gm.DrumChannel, Low: 128, High: -1, Volume: 1}
				parts[k] = p
				order = append(order, k)
			}
			return p
		}
		for _, e := range t.Events {
			trackEnd = max(trackEnd, e.Tick)
			switch e.Kind {
			case Meta:
				switch e.MetaType {
				case MetaTrackName:
					if trackName == "" {
						trackName = strings.TrimSpace(e.Text())
					}
				case MetaTempo:
					if q, ok := e.Tempo(); ok {
						a.tempos = append(a.tempos, tempoAt{e.Tick, q})
					}
				case MetaTimeSignature:
					// A signature no score can hold (65/4, 3/256) is
					// ignored, as a sequencer would.
					if n, d, ok := e.TimeSignature(); ok && n >= 1 && n <= 64 && d >= 1 && d <= 128 && d&(d-1) == 0 {
						a.meters = append(a.meters, meterAt{e.Tick, n, d})
					}
				case MetaKeySignature:
					if fi, mi, ok := e.KeySignature(); ok {
						a.keys = append(a.keys, keyAt{e.Tick, fi, mi})
					}
				case MetaMarker:
					a.markers = append(a.markers, textAt{e.Tick, strings.TrimSpace(e.Text())})
				case MetaCue:
					a.cues = append(a.cues, textAt{e.Tick, strings.TrimSpace(e.Text())})
				}
			case NoteOn, NoteOff:
				ch, pitch := int(e.Channel), int(e.A)
				k := [2]int{ch, pitch}
				if e.Kind == NoteOn && e.B > 0 {
					open[k] = append(open[k], rawNote{on: e.Tick, pitch: pitch, vel: int(e.B)})
					continue
				}
				if q := open[k]; len(q) > 0 {
					n := q[0]
					open[k] = q[1:]
					n.off = e.Tick
					p := get(ch)
					p.notes = append(p.notes, n)
				}
			case Program:
				if p := get(int(e.Channel)); p.Program < 0 {
					p.Program = int(e.A)
				}
			case Controller:
				p := get(int(e.Channel))
				switch e.A {
				case CCVolume:
					if !p.hasVol {
						p.Volume, p.hasVol = float32(e.B)/100, true
					}
				case CCPan:
					if p.Pan == 0 {
						p.Pan = float32(math.Round(max(-1, min(1, float64(int(e.B)-64)/63))*1000) / 1000)
					}
				case CCExpression:
					p.expr = append(p.expr, ccPoint{e.Tick, int(e.B)})
				}
			}
		}
		// Notes still sounding at the end of the track stop there (or a
		// beat on, at least).
		for k, q := range open {
			for _, n := range q {
				n.off = max(trackEnd, n.on+int64(f.Division))
				p := get(k[0])
				p.notes = append(p.notes, n)
			}
		}
		for _, k := range order {
			if k.track == ti && parts[k].Name == "" {
				parts[k].Name = trackName
			}
		}
		a.lastTick = max(a.lastTick, trackEnd)
	}
	// Parts are the track-and-channels with notes; a track split over
	// channels names each after its program.
	perTrack := map[int]int{}
	for _, k := range order {
		if len(parts[k].notes) > 0 {
			perTrack[k.track]++
		}
	}
	for _, k := range order {
		p := parts[k]
		if len(p.notes) == 0 {
			continue
		}
		sort.SliceStable(p.notes, func(i, j int) bool { return p.notes[i].on < p.notes[j].on })
		for _, n := range p.notes {
			p.Low, p.High = min(p.Low, n.pitch), max(p.High, n.pitch)
			a.lastTick = max(a.lastTick, n.off)
			a.lastNote = max(a.lastNote, n.off)
		}
		p.Notes = len(p.notes)
		a.Notes += p.Notes
		progName := ""
		switch {
		case p.Percussion:
			progName = "Drums"
		case p.Program >= 0:
			progName = gm.Names[p.Program]
		}
		if f.Format == 0 {
			// One track holds the whole piece; its name is the title.
			p.Name = ""
		}
		switch {
		case p.Name == "" && progName != "":
			p.Name = progName
		case p.Name == "":
			p.Name = fmt.Sprintf("Channel %d", p.Channel+1)
		case perTrack[k.track] > 1 && progName != "":
			p.Name = p.Name + " " + progName
		case perTrack[k.track] > 1:
			p.Name = fmt.Sprintf("%s %d", p.Name, p.Channel+1)
		}
		p.Instrument, p.Clef = guessInstrument(p)
		a.Parts = append(a.Parts, *p)
	}
	sort.SliceStable(a.tempos, func(i, j int) bool { return a.tempos[i].tick < a.tempos[j].tick })
	sort.SliceStable(a.meters, func(i, j int) bool { return a.meters[i].tick < a.meters[j].tick })
	sort.SliceStable(a.keys, func(i, j int) bool { return a.keys[i].tick < a.keys[j].tick })
	sort.SliceStable(a.markers, func(i, j int) bool { return a.markers[i].tick < a.markers[j].tick })
	sort.SliceStable(a.cues, func(i, j int) bool { return a.cues[i].tick < a.cues[j].tick })

	a.Tempo = 120
	if len(a.tempos) > 0 && a.tempos[0].tick == 0 {
		a.Tempo = a.tempos[0].qpm
	}
	for i, t := range a.tempos {
		if t.tick > 0 && (i == 0 || math.Abs(t.qpm-a.tempos[i-1].qpm) > 0.01) {
			a.TempoChanges++
		}
	}
	if len(a.meters) > 0 {
		a.Meter = simpleMeter(a.meters[0].num, a.meters[0].den)
		if len(a.meters) > 1 && a.isPickup() {
			a.Meter = simpleMeter(a.meters[1].num, a.meters[1].den)
		}
	} else {
		a.Meter, a.MeterGuessed = a.guessMeter(), true
	}
	if len(a.keys) > 0 {
		a.Fifths, a.Minor = a.keys[0].fifths, a.keys[0].minor
	} else {
		a.Fifths, a.Minor = notate.DetectKey(a.pitchedNotes())
		a.KeyGuessed = true
	}
	return a
}

// guessInstrument picks a part's instrument (its name first, then its
// program) and its clef (the instrument's own, else by range).
func guessInstrument(p *Part) (string, string) {
	if p.Percussion {
		return gm.Pick(gm.DrumKit), "percussion"
	}
	inst := gm.Match(p.Name)
	if inst == "" || gm.Percussive(inst) {
		inst = gm.Instrument(max(p.Program, 0))
	}
	inst = gm.Pick(inst)
	clef := ""
	if pb, ok := gm.Playback(inst); ok {
		clef = pb.Clef
	}
	if clef == "" || clef == "percussion" {
		// By range: mostly below the staff's middle is bass.
		pitches := make([]int, len(p.notes))
		for i, n := range p.notes {
			pitches[i] = n.pitch
		}
		sort.Ints(pitches)
		clef = "treble"
		if len(pitches) > 0 && pitches[len(pitches)/2] < 57 {
			clef = "bass"
		}
	}
	return inst, clef
}

func simpleMeter(num, den int) score.Meter {
	return score.Meter{Groups: []int{num}, Den: den}
}

// isPickup reports a first time signature lasting exactly one bar that is
// shorter than the one after: an anacrusis (what Export writes).
func (a *Analysis) isPickup() bool {
	if len(a.meters) < 2 || a.meters[0].tick != 0 {
		return false
	}
	m0, m1 := a.meters[0], a.meters[1]
	l0 := int64(m0.num) * 4 * int64(a.Division) / int64(m0.den)
	return m1.tick == l0 && m0.num*m1.den < m1.num*m0.den
}

// pitchedNotes are every non-percussion note as notate notes in whole
// notes, unquantised.
func (a *Analysis) pitchedNotes() []notate.Note {
	var out []notate.Note
	for _, p := range a.Parts {
		if p.Percussion {
			continue
		}
		for _, n := range p.notes {
			out = append(out, notate.Note{Start: a.frac(n.on), End: a.frac(n.off), MIDI: n.pitch, Velocity: n.vel})
		}
	}
	return out
}

// frac is a tick in whole notes.
func (a *Analysis) frac(tick int64) score.Frac { return score.F(tick, 4*int64(a.Division)) }

// guessMeter finds the meter whose bar lines best match where the strong
// notes fall — loud, long and low ones — taking 4/4 unless three beats a
// bar is clearly better; three beats are 6/8 when the strong notes come
// every dotted quarter rather than every quarter.
func (a *Analysis) guessMeter() score.Meter {
	q := int64(a.Division)
	tol := max(1, q/8)
	type onset struct {
		tick int64
		w    float64
	}
	var ons []onset
	for _, p := range a.Parts {
		for _, n := range p.notes {
			dur := math.Min(float64(n.off-n.on)/float64(q), 2)
			w := float64(n.vel) / 127 * (1 + dur)
			if p.Percussion && (n.pitch == 35 || n.pitch == 36) || !p.Percussion && n.pitch < 55 {
				w += 0.5
			}
			ons = append(ons, onset{n.on, w})
		}
	}
	if len(ons) == 0 || a.lastNote < 8*q {
		return score.FourFour()
	}
	near := func(t, period int64) bool {
		r := t % period
		return r <= tol || period-r <= tol
	}
	ratio := func(beats int64) float64 {
		bar := beats * q
		bars := float64(a.lastNote) / float64(bar)
		var down, beat float64
		for _, o := range ons {
			if near(o.tick, bar) {
				down += o.w
			}
			if near(o.tick, q) {
				beat += o.w
			}
		}
		if beat == 0 || bars < 1 {
			return 0
		}
		return (down / bars) / (beat / (bars * float64(beats)))
	}
	r4, r3 := ratio(4), ratio(3)
	if r3 <= r4*1.15 {
		return score.FourFour()
	}
	// 3/4 or 6/8: weight on the bar's second dotted quarter against its
	// second and third quarters.
	var dotted, quarters float64
	bar := 3 * q
	for _, o := range ons {
		r := o.tick % bar
		switch {
		case absI(r-3*q/2) <= tol:
			dotted += o.w
		case absI(r-q) <= tol || absI(r-2*q) <= tol:
			quarters += o.w / 2
		}
	}
	if dotted > quarters*1.3 {
		return score.Meter{Groups: []int{6}, Den: 8}
	}
	return score.Meter{Groups: []int{3}, Den: 4}
}

func absI(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// Import makes a score of a file. The warnings are what it could not carry
// over or had to guess.
func Import(f *File, o Options) (s *score.Score, warnings []string, err error) {
	defer score.RecoverOverflow(&err)
	a := Analyze(f)
	if a.Notes == 0 {
		return nil, nil, fmt.Errorf("midi: the file has no notes")
	}
	if o.Grid.Unit.Sign() == 0 {
		o.Grid = notate.DefaultGrid()
	}
	if o.Voices <= 0 {
		o.Voices = score.MaxVoices
	}
	if o.Split == 0 {
		o.Split = 60
	}
	warn := func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }
	if f.Format == 2 {
		warn("format 2 (independent sequences): the tracks are played together")
	}
	name := o.Name
	if name == "" {
		name = "imported"
	}
	s = score.New(name)
	s.Title = o.Title
	if s.Title == "" {
		s.Title = name
	}
	for _, t := range f.Tracks {
		for _, e := range t.Events {
			if e.Kind == Meta && e.MetaType == MetaCopyright && s.Composer == "" {
				s.Composer = strings.TrimSpace(e.Text())
			}
		}
	}
	// A format-1 file's first track, when it holds no notes, is the
	// conductor's, and its name the piece's; so is a format-0 file's one.
	if o.Title == "" && (f.Format == 0 || f.Format == 1 && len(f.Tracks) > 1 && a.Parts[0].Track > 0) {
		for _, e := range f.Tracks[0].Events {
			if e.Kind == Meta && e.MetaType == MetaTrackName && strings.TrimSpace(e.Text()) != "" {
				s.Title = strings.TrimSpace(e.Text())
				break
			}
		}
	}

	if err := a.buildBars(s, o); err != nil {
		return nil, nil, err
	}
	if a.MeterGuessed && o.Meter == nil {
		warn("no time signature in the file: guessed %s", a.Meter)
	}
	a.buildKeys(s, o)
	if a.KeyGuessed && o.Key == nil {
		warn("no key signature in the file: guessed %s", keyName(a.Fifths, a.Minor))
	}
	lay := notate.LayoutOf(s)
	a.buildTempo(s, lay, o.Grid.Unit)
	a.buildSections(s, lay)

	used := map[string]int{}
	for pi, p := range a.Parts {
		var ch Choice
		if pi < len(o.Choices) {
			ch = o.Choices[pi]
		}
		if ch.Skip {
			continue
		}
		l, err := a.line(&p, ch, lay, o)
		if err != nil {
			return nil, warnings, fmt.Errorf("%s: %w", p.Name, err)
		}
		key := strings.ToLower(l.Name)
		if used[key]++; used[key] > 1 {
			l.Name = fmt.Sprintf("%s %d", l.Name, used[key])
		}
		s.Lines = append(s.Lines, l)
	}
	if len(s.Lines) == 0 {
		return nil, warnings, fmt.Errorf("midi: every part was skipped")
	}
	for _, is := range s.Validate(nil) {
		if is.Severity == score.Error {
			return nil, warnings, fmt.Errorf("midi: the import does not add up (%s)", is)
		}
	}
	return s, warnings, nil
}

func keyName(fifths int, minor bool) string {
	major := []string{"Cb", "Gb", "Db", "Ab", "Eb", "Bb", "F", "C", "G", "D", "A", "E", "B", "F#", "C#"}
	minorN := []string{"Ab", "Eb", "Bb", "F", "C", "G", "D", "A", "E", "B", "F#", "C#", "G#", "D#", "A#"}
	if fifths < -7 || fifths > 7 {
		return "?"
	}
	if minor {
		return minorN[fifths+7] + " minor"
	}
	return major[fifths+7] + " major"
}

// buildBars lays the bars out from the time signatures (or the override,
// or the guess) to cover the last note.
func (a *Analysis) buildBars(s *score.Score, o Options) error {
	q := int64(a.Division)
	type sig struct {
		tick int64
		m    score.Meter
	}
	var sigs []sig
	switch {
	case o.Meter != nil:
		sigs = []sig{{0, *o.Meter}}
	case len(a.meters) == 0:
		sigs = []sig{{0, a.Meter}}
	default:
		for _, m := range a.meters {
			if len(sigs) > 0 && sigs[len(sigs)-1].tick == m.tick {
				sigs = sigs[:len(sigs)-1]
			}
			sigs = append(sigs, sig{m.tick, simpleMeter(m.num, m.den)})
		}
		if sigs[0].tick > 0 {
			sigs = append([]sig{{0, score.FourFour()}}, sigs...)
		}
	}
	barTicks := func(m score.Meter) int64 { return int64(m.Num()) * 4 * q / int64(m.Den) }
	pickup := o.Meter == nil && a.isPickup()
	var meters []score.Meter
	at := int64(0)
	end := max(a.lastNote, 1)
	for i, sg := range sigs {
		m := sg.m
		next := int64(math.MaxInt64)
		if i+1 < len(sigs) {
			next = sigs[i+1].tick
		}
		for at < next && (at < end || len(meters) == 0) {
			bt := barTicks(m)
			if bt <= 0 {
				bt = 4 * q
			}
			if at+bt > next {
				// A signature that comes mid-bar: the bar up to it is short,
				// in its own meter.
				short := score.F(next-at, 4*q)
				if d := short.Den; d&(d-1) == 0 && d <= 128 && short.Num <= 64 {
					meters = append(meters, score.Meter{Groups: []int{int(short.Num)}, Den: int(d)})
					at = next
					break
				}
			}
			meters = append(meters, m)
			at += bt
			if len(meters) > score.MaxBars {
				return fmt.Errorf("midi: the notes run past %d bars (the last ends at tick %d); a score holds at most %d", score.MaxBars, a.lastNote, score.MaxBars)
			}
		}
		if at >= end && i+1 < len(sigs) && sigs[i+1].tick >= end {
			break
		}
	}
	if pickup && len(meters) > 1 {
		s.Pickup = meters[0].Length()
		meters[0] = meters[1]
	}
	s.Bars = len(meters)
	s.Meter = nil
	for b, m := range meters {
		if len(s.Meter) == 0 || !s.Meter[len(s.Meter)-1].Meter.Eq(m) {
			s.Meter = append(s.Meter, score.MeterMark{Bar: b + 1, Meter: m})
		}
	}
	a.barStart = make([]score.Frac, 0, len(meters)+1)
	pos := score.Frac{}
	for b := 1; b <= len(meters); b++ {
		a.barStart = append(a.barStart, pos)
		pos = pos.Add(s.BarLength(b))
	}
	a.barStart = append(a.barStart, pos)
	return nil
}

// buildKeys places the key signatures at the bars they fall in.
func (a *Analysis) buildKeys(s *score.Score, o Options) {
	mode := func(minor bool) string {
		if minor {
			return "minor"
		}
		return "major"
	}
	s.Key = nil
	switch {
	case o.Key != nil:
		k := *o.Key
		k.Bar = 1
		s.Key = []score.KeyMark{k}
		return
	case len(a.keys) == 0:
		s.Key = []score.KeyMark{{Bar: 1, Fifths: a.Fifths, Mode: mode(a.Minor)}}
		return
	}
	for _, k := range a.keys {
		bar := a.barNear(a.frac(k.tick))
		km := score.KeyMark{Bar: bar, Fifths: k.fifths, Mode: mode(k.minor)}
		if n := len(s.Key); n > 0 && s.Key[n-1].Bar == bar {
			s.Key[n-1] = km
			continue
		}
		if n := len(s.Key); n > 0 && s.Key[n-1].Fifths == km.Fifths && s.Key[n-1].Mode == km.Mode {
			continue
		}
		s.Key = append(s.Key, km)
	}
	if s.Key[0].Bar != 1 {
		s.Key = append([]score.KeyMark{{Bar: 1, Mode: "major"}}, s.Key...)
	}
}

// barNear is the 1-based bar a time falls in, or the next one when it is
// within a 16th of it (a key change placed a hair early).
func (a *Analysis) barNear(t score.Frac) int {
	for b := len(a.barStart) - 2; b >= 0; b-- {
		if !t.Add(score.F(1, 16)).Less(a.barStart[b]) {
			return b + 1
		}
	}
	return 1
}

// buildTempo turns the tempo changes into tempo marks: a change of less
// than a twentieth of a beat a minute is dropped, and four or more changes
// in a row, each within a beat of the last and all one way, are a ramp
// from the first to the last.
func (a *Analysis) buildTempo(s *score.Score, lay notate.Layout, unit score.Frac) {
	var ts []tempoAt
	for _, t := range a.tempos {
		if n := len(ts); n > 0 && ts[n-1].tick == t.tick {
			ts[n-1] = t
			continue
		}
		ts = append(ts, t)
	}
	if len(ts) == 0 || ts[0].tick > 0 {
		ts = append([]tempoAt{{0, 120}}, ts...)
	}
	beat := int64(a.Division)
	s.Tempo = nil
	for i := 0; i < len(ts); {
		j := i
		for j+1 < len(ts) && ts[j+1].tick-ts[j].tick <= beat && math.Abs(ts[j+1].qpm-ts[j].qpm) >= 0.05 &&
			(j == i || (ts[j+1].qpm-ts[j].qpm)*(ts[j].qpm-ts[j-1].qpm) > 0) {
			j++
		}
		if j-i >= 3 {
			s.Tempo = append(s.Tempo, a.tempoMark(lay, ts[i], unit, true), a.tempoMark(lay, ts[j], unit, false))
			i = j + 1
			continue
		}
		s.Tempo = append(s.Tempo, a.tempoMark(lay, ts[i], unit, false))
		i++
	}
	// Two marks snapped to one place: the later wins. A steady mark at the
	// tempo already playing says nothing.
	out := s.Tempo[:0]
	for _, t := range s.Tempo {
		if n := len(out); n > 0 && out[n-1].At == t.At {
			out[n-1] = t
			continue
		}
		if n := len(out); n > 0 && !t.Ramp && !out[n-1].Ramp && math.Abs(out[n-1].BPM-t.BPM) < 0.05 {
			continue
		}
		out = append(out, t)
	}
	s.Tempo = out
}

func (a *Analysis) tempoMark(lay notate.Layout, t tempoAt, unit score.Frac, ramp bool) score.TempoMark {
	at := a.frac(t.tick)
	b := lay.BarAt(at)
	off := at.Sub(lay.Starts[b]).Div(unit).Round()
	offset := unit.MulInt(off)
	if !offset.Less(lay.BarLen(b)) {
		offset = lay.BarLen(b).Sub(unit)
	}
	return score.TempoMark{At: score.Pos{Bar: b + 1, Offset: offset}, BPM: roundBPM(t.qpm), Unit: score.Duration{Base: score.Quarter}, Ramp: ramp}
}

// roundBPM undoes the microsecond rounding of a tempo: 91.99998 is 92.
func roundBPM(q float64) float64 {
	if r := math.Round(q); math.Abs(q-r) < 0.02 {
		return r
	}
	return math.Round(q*1000) / 1000
}

// buildSections makes a marker at a bar line a rehearsal mark and any
// other marker (and every cue point) a score marker.
func (a *Analysis) buildSections(s *score.Score, lay notate.Layout) {
	names := map[string]int{}
	unique := func(n string) string {
		if n == "" {
			n = "section"
		}
		names[n]++
		if names[n] > 1 {
			return fmt.Sprintf("%s %d", n, names[n])
		}
		return n
	}
	sixteenth := score.F(1, 16)
	for _, m := range a.markers {
		at := a.frac(m.tick)
		b := lay.BarAt(at)
		off := at.Sub(lay.Starts[b])
		if b+1 < len(lay.Starts) && lay.Starts[b+1].Sub(at).Less(sixteenth) {
			b, off = b+1, score.Frac{}
		}
		if off.Less(sixteenth) && b < lay.Bars() {
			if n := len(s.Sections); n == 0 || s.Sections[n-1].Bar < b+1 {
				s.Sections = append(s.Sections, score.Section{Name: unique(m.text), Bar: b + 1})
				continue
			}
		}
		s.Markers = append(s.Markers, a.marker(lay, m))
	}
	for _, c := range a.cues {
		s.Markers = append(s.Markers, a.marker(lay, c))
	}
}

func (a *Analysis) marker(lay notate.Layout, m textAt) score.Marker {
	at := a.frac(m.tick)
	b := min(lay.BarAt(at), lay.Bars()-1)
	off := at.Sub(lay.Starts[b])
	if !off.Less(lay.BarLen(b)) {
		off = score.Frac{}
	}
	name := m.text
	if name == "" {
		name = "marker"
	}
	return score.Marker{Name: name, At: score.Pos{Bar: b + 1, Offset: off}}
}

// dynamicLadder are the dynamics velocity turns into, softest first.
var dynamicLadder = []string{"ppp", "pp", "p", "mp", "mf", "f", "ff", "fff"}

func ladderVelocity(i int) float64 { return float64(score.Dynamics[dynamicLadder[i]].Level) * 127 }

func nearestDynamic(vel float64) int {
	best, bestD := 0, math.Inf(1)
	for i := range dynamicLadder {
		if d := math.Abs(vel - ladderVelocity(i)); d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// line makes one part a line: quantise, staves, voices, dynamics, bars.
func (a *Analysis) line(p *Part, ch Choice, lay notate.Layout, o Options) (*score.Line, error) {
	inst, clef, name := p.Instrument, p.Clef, p.Name
	if ch.Instrument != "" {
		inst = ch.Instrument
	}
	if ch.Clef != "" {
		clef = ch.Clef
	}
	if ch.Name != "" {
		name = ch.Name
	}
	l := score.NewLine(name, inst, "treble")
	l.Volume, l.Pan = p.Volume, p.Pan
	l.Staves = nil

	notes := make([]notate.Note, len(p.notes))
	for i, n := range p.notes {
		notes[i] = notate.Note{Start: a.frac(n.on), End: a.frac(n.off), MIDI: n.pitch, Velocity: n.vel}
	}
	if !o.NoGraces {
		notes = notate.MarkGraces(notes, o.Grid)
	}
	notes, err := notate.Quantize(notes, lay, o.Grid)
	if err != nil {
		return nil, err
	}
	var groups [][]notate.Note
	var clefs []string
	if clef == "grand" {
		var hi, lo []notate.Note
		for _, n := range notes {
			if n.MIDI >= o.Split {
				hi = append(hi, n)
			} else {
				lo = append(lo, n)
			}
		}
		groups, clefs = [][]notate.Note{hi, lo}, []string{"treble", "bass"}
	} else {
		groups, clefs = [][]notate.Note{notes}, []string{clef}
	}
	var transpose score.Interval
	if pb, ok := gm.Playback(inst); ok && pb.Transposition != 0 && !p.Percussion {
		d, c := gm.Transposition(pb.Transposition)
		transpose = score.Interval{Diatonic: d, Chromatic: c}
	}
	voices := o.Voices
	if p.Percussion {
		// A drum part's simultaneous hits are one chord.
		voices = min(voices, 2)
	}
	staffChords := make([][][]notate.Chord, len(groups))
	for gi, g := range groups {
		staffChords[gi] = notate.Separate(g, voices, o.Grid.Unit)
		if !o.NoLegato {
			for _, v := range staffChords[gi] {
				notate.Legato(v, lay, score.F(1, 8))
			}
		}
	}
	// Grace notes go on the note they lead to: in their own staff, else
	// (the main note split to the other one) there.
	for gi, g := range groups {
		var graces []notate.Note
		for _, n := range g {
			if n.Grace {
				graces = append(graces, n)
			}
		}
		left := notate.AttachGraces(staffChords[gi], graces)
		for gj := range groups {
			if gj != gi && len(left) > 0 {
				left = notate.AttachGraces(staffChords[gj], left)
			}
		}
	}
	if !o.NoDynamics {
		markDynamics(staffChords, lay)
	}
	for gi, vs := range staffChords {
		st := &score.Staff{Clef: clefs[gi], Transpose: transpose}
		if len(vs) == 0 {
			vs = [][]notate.Chord{nil}
		}
		for vi, cs := range vs {
			v, err := notate.Write(cs, lay, notate.WriteOptions{BarRests: vi == 0})
			if err != nil {
				return nil, err
			}
			st.Voices = append(st.Voices, v)
		}
		l.Staves = append(l.Staves, st)
	}
	if lane := a.expressionLane(p, lay, o.Grid.Unit); lane != nil {
		l.Lanes = append(l.Lanes, *lane)
	}
	return l, nil
}

// markDynamics turns velocity into dynamic marks: each bar's average
// velocity is read as the nearest of ppp..fff, and a mark is written on
// the bar's first chord (in any staff or voice) whenever the level moves —
// by more than a level's width and a bit, so a played line that wavers
// around a boundary does not flicker between two marks.
func markDynamics(staves [][][]notate.Chord, lay notate.Layout) {
	type ref struct{ s, v, c int }
	bars := make([][]ref, lay.Bars())
	for si, vs := range staves {
		for vi, cs := range vs {
			for ci, c := range cs {
				b := lay.BarAt(c.Start)
				bars[b] = append(bars[b], ref{si, vi, ci})
			}
		}
	}
	cur := -1
	for _, refs := range bars {
		sum, n := 0.0, 0
		var first *notate.Chord
		for _, r := range refs {
			c := &staves[r.s][r.v][r.c]
			if c.Velocity > 0 {
				sum += float64(c.Velocity)
				n++
			}
			if first == nil || c.Start.Less(first.Start) {
				first = c
			}
		}
		if n == 0 || first == nil {
			continue
		}
		mean := sum / float64(n)
		lvl := nearestDynamic(mean)
		if cur >= 0 && (lvl == cur || math.Abs(mean-ladderVelocity(cur)) < 11) {
			continue
		}
		cur = lvl
		first.Marks = append(first.Marks, score.Mark{Name: dynamicLadder[lvl]})
	}
}

// expressionLane is the part's expression controller as a volume lane:
// breakpoints on the grid, with the points a straight line already passes
// through dropped. A part whose expression never leaves full has none.
func (a *Analysis) expressionLane(p *Part, lay notate.Layout, unit score.Frac) *score.Lane {
	if len(p.expr) == 0 {
		return nil
	}
	full := true
	for _, c := range p.expr {
		if c.value != 127 {
			full = false
		}
	}
	if full {
		return nil
	}
	type pt struct {
		at score.Frac
		v  float64
	}
	var pts []pt
	for _, c := range p.expr {
		at := a.frac(c.tick).Div(unit).Round()
		t := unit.MulInt(at)
		if lay.End().Less(t) || t.Eq(lay.End()) {
			t = lay.End().Sub(unit)
		}
		v := float64(c.value) / 127
		if n := len(pts); n > 0 && pts[n-1].at.Eq(t) {
			pts[n-1].v = v
			continue
		}
		pts = append(pts, pt{t, v})
	}
	// Drop points a line between their neighbours passes within a step of.
	const tol = 1.5 / 127
	kept := []pt{pts[0]}
	for i := 1; i+1 < len(pts); i++ {
		prev, next := kept[len(kept)-1], pts[i+1]
		span := next.at.Sub(prev.at).Float()
		if span > 0 {
			k := pts[i].at.Sub(prev.at).Float() / span
			if math.Abs(prev.v+(next.v-prev.v)*k-pts[i].v) <= tol {
				continue
			}
		}
		kept = append(kept, pts[i])
	}
	if len(pts) > 1 {
		kept = append(kept, pts[len(pts)-1])
	}
	lane := &score.Lane{Name: "volume"}
	for _, q := range kept {
		b := lay.BarAt(q.at)
		lane.Points = append(lane.Points, score.LanePoint{At: score.Pos{Bar: b + 1, Offset: q.at.Sub(lay.Starts[b])}, Value: float32(math.Round(q.v*1000) / 1000)})
	}
	return lane
}
