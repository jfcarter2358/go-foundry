// Package interpret turns a written score into a performance: every note
// as a timed event — tick, seconds, MIDI pitch, velocity, line — plus the
// control curves, the bars and beats, the sections and the markers that a
// scheduler (internal/score/play), an exporter or the editor's playhead
// needs.
//
// It is the one place that knows what notation means for sound:
//
//   - form (form.go): repeats unrolled, 1st/2nd endings, D.C./D.S., To
//     Coda and Fine followed — or the score straight through;
//   - time (tempo.go, tempobuild.go): tempo marks, tempo text (rit.,
//     accel., a tempo, meno/più mosso) and fermatas as one tempo map;
//   - notes (this file): tuplets at any depth, ties merged into one long
//     note across bars and passes, grace notes before or on the beat;
//   - level (dynamics.go): dynamics marks and hairpins as a curve per line,
//     the dynamics lane's overrides laid over it, accents and sfz on the
//     notes they are on;
//   - shape (expression.go): articulation lengths, slurs played legato,
//     glissandi as slides or runs, trills, mordents and turns, tremolos,
//     arpeggios, the sustain pedal, technique text;
//   - automation lanes as curves.
//
// Everything is computed in exact score time (score.Frac) and only turned
// into ticks (score.TicksPerQuarter) and seconds at the end, so a note
// after a thousand triplets is exactly where it was written. Pitches are
// sounding pitch: transposition, clefs and ottava lines change what is
// written, not what plays (score/staff.go).
package interpret

import (
	"sort"

	"foundry/internal/score"
	"foundry/internal/tracker"
)

// Options shape an interpretation.
type Options struct {
	// NoRepeats plays the score straight through, once, as written: no
	// repeats, every ending, no D.C. or D.S. (the score's own
	// Playback.IgnoreRepeats does the same).
	NoRepeats bool
	// Instruments are each line's instrument, nil where it is not known.
	// What an instrument changes: a staccato's length (its
	// Playback.Staccato), how a glissandum plays when the line does not
	// say (Playback.Gliss), and whether an unmeasured tremolo is its
	// tremolo articulation or repeated notes. Without one the defaults
	// play: half-length staccato, slides, repeated notes.
	Instruments []*tracker.Instrument
	// NoSlides plays a glissando the line would slide as a chromatic run
	// instead, every note a note: for exports that cannot bend one (a MIDI
	// file's notes), where a slide would leave only its first note.
	NoSlides bool
}

// Note is one note of the performance.
type Note struct {
	// Tick and Ticks are its start and length in performance ticks.
	Tick, Ticks int64
	// Start and End are seconds at the score's own tempo (playback's tempo
	// slider scales them).
	Start, End float64
	// MIDI is the sounding pitch; Cents a fine offset.
	MIDI  int
	Cents float32
	// Velocity is 0..1 (MIDI velocity / 127): the line's dynamic level at
	// the note's start times its Accent.
	Velocity float32
	// Accent is how much the note starts above its line's level (an
	// accent 1.25, a marcato 1.4, an sfz its level over the prevailing
	// one); 1 for none. A scheduler that follows a held note through its
	// line's dynamics lets it fall back to 1 over AccentTicks.
	Accent float32
	// Bend is the note's pitch curve in semitones, breakpoints in
	// performance ticks, linear between and held after the last (a
	// glissando played as a slide); nil for none.
	Bend []BendPoint
	// Line, Staff and Voice are 0-based indexes into the score.
	Line, Staff, Voice int
	// Bar is the written bar the note starts in; Pass the time through it.
	Bar, Pass int
	Grace     score.Grace
	// Articulation is a word naming how this note is played ("staccato",
	// from its marks; "legato" under a slur), Technique the line's
	// technique text in force ("pizz."). A scheduler looks each up in the
	// instrument (Instrument.ArticulationIndex), the note's own first.
	Articulation string
	Technique    string
}

// BendPoint is a breakpoint of a note's pitch curve.
type BendPoint struct {
	Tick  float64
	Semis float32
}

// BendAt is a note's bend at a tick: linear between its points, 0 before
// the first and held after the last.
func (n *Note) BendAt(tick float64) float32 {
	b := n.Bend
	if len(b) == 0 || tick <= b[0].Tick {
		if len(b) > 0 && tick >= b[0].Tick {
			return b[0].Semis
		}
		return 0
	}
	for i := 1; i < len(b); i++ {
		if tick < b[i].Tick {
			x, y := b[i-1], b[i]
			return x.Semis + (y.Semis-x.Semis)*float32((tick-x.Tick)/(y.Tick-x.Tick))
		}
	}
	return b[len(b)-1].Semis
}

// AccentTicks is how long an accent takes to fall back to the line's
// level: a 16th note.
const AccentTicks = score.TicksPerQuarter / 4

// Bar is one bar as played.
type Bar struct {
	// Bar is the written bar number; Pass counts times through it (1..).
	Bar, Pass   int
	Tick, Ticks int64
	Start, End  float64
	Meter       score.Meter
}

// Beat is one conductor's beat as played.
type Beat struct {
	Tick  int64
	Start float64
	// Bar is the index into Performance.Bars; Beat is 1-based in the bar.
	Bar, Beat int
}

// SectionSpan is where a section plays: from its first played bar to the
// first bar after it that belongs to another section.
type SectionSpan struct {
	Index     int
	Name      string
	Tick, End int64
	Start     float64
	EndSec    float64
	Loop      bool
	// Next is the section to go to at the end, -1 to carry on.
	Next int
}

// MarkerAt is a marker as played.
type MarkerAt struct {
	Name  string
	Tick  int64
	Start float64
}

// CurvePoint is one breakpoint of a control curve.
type CurvePoint struct {
	Tick  float64
	Value float32
}

// Curve is a control curve over a line: linear between points, holding
// the ends. Target is the lane it came from ("volume", "pan", …), or
// "dynamics".
type Curve struct {
	Line   int
	Target string
	Points []CurvePoint
}

// At reads the curve at a tick. Of two points at one tick (a jump at a
// repeat, a step) the later wins from that tick on.
func (c *Curve) At(tick float64) float32 {
	p := c.Points
	if len(p) == 0 {
		return 1
	}
	i := sort.Search(len(p), func(i int) bool { return p[i].Tick > tick })
	switch {
	case i == 0:
		return p[0].Value
	case i == len(p):
		return p[len(p)-1].Value
	}
	a, b := p[i-1], p[i]
	if b.Tick <= a.Tick {
		return a.Value
	}
	return a.Value + (b.Value-a.Value)*float32((tick-a.Tick)/(b.Tick-a.Tick))
}

// Varies reports whether the curve moves anywhere strictly between two
// ticks: a breakpoint inside, or a slope through them.
func (c *Curve) Varies(from, to float64) bool {
	p := c.Points
	i := sort.Search(len(p), func(i int) bool { return p[i].Tick > from })
	if i < len(p) && p[i].Tick < to {
		return true
	}
	return c.At(from) != c.At(to-1e-9)
}

// LineDynamics is a line's dynamic level over the performance, on the
// marks' scale (MIDI velocity / 127). Written is what the printed marks
// and hairpins give; Effective is Written with the dynamics lane's
// overrides laid over it, which is what plays.
type LineDynamics struct {
	Line               int
	Written, Effective Curve
}

// Performance is a score as it sounds.
type Performance struct {
	Score *score.Score
	// Notes are in onset order (then line, staff, voice, pitch).
	Notes    []Note
	Curves   []Curve
	Dynamics []LineDynamics
	Bars     []Bar
	Beats    []Beat
	Sections []SectionSpan
	Markers  []MarkerAt
	Tempo    *TempoMap
	// Length is the whole performance in ticks; Seconds its duration.
	Length  int64
	Seconds float64
	// InstrumentDependent marks the lines whose notes depend on their
	// instrument (Options.Instruments): another instrument on one of them
	// needs the score interpreted again; on any other line it does not.
	InstrumentDependent []bool
}

// played is one bar in playing order.
type played struct {
	bar, pass int
	start     score.Frac // performance position, whole notes
	length    score.Frac
}

// rawNote is a note before it has a velocity and a time in seconds.
type rawNote struct {
	at, dur score.Frac
	velAt   score.Frac // where its dynamic is read (a grace's main note)
	pitch   score.Pitch
	midi    int
	line    int
	staff   int
	voice   int
	bar     int
	pass    int
	grace   score.Grace
	art     string
	accent  float32 // from its first event's marks
	// segAt and segDur are its last tied event, whose marks (segMarks)
	// decide how long it really sounds.
	segAt, segDur score.Frac
	segMarks      []score.Mark
	bends         []bendAt
	// drop leaves it out of the performance; into, when not 0, is the
	// note (index+1) it became part of (a slide's target).
	drop bool
	into int
	// shaped notes have been cut by an ornament, a tremolo or a run, so
	// the articulation and slur passes leave them alone.
	shaped bool
}

type bendAt struct {
	at    score.Frac
	semis float32
}

// vev is one event of a voice in playing order.
type vev struct {
	at, dur         score.Frac // the event's slot
	mainAt, mainDur score.Frac // after on-beat grace notes
	off             score.Frac // written offset in its bar
	bar, pass       int
	e               *score.Event
	// notes are the rawNotes each of a chord's pitches started or carried
	// on (a tie), as written.
	notes []int
}

// voiceEvents is one voice's events in playing order.
type voiceEvents struct {
	line, staff, voice int
	evs                []vev
	end                score.Frac // where the voice's last event ends
}

// techPoint is technique text in performance time.
type techPoint struct {
	at   score.Frac
	text string
}

// Interpret performs a score.
func Interpret(s *score.Score, o Options) (perf *Performance, err error) {
	defer score.RecoverOverflow(&err)
	in := &interp{s: s, o: o}
	in.layout()
	in.buildTempo()
	in.walk()
	in.dynamics()
	in.express()
	return in.finish(), nil
}

type interp struct {
	s      *score.Score
	o      Options
	starts []score.Frac // written bar starts
	order  []played
	tempo  *TempoMap

	notes   []rawNote
	voices  []voiceEvents
	techs   [][]techPoint
	lines   []lineMarks
	dyn     []lineCurves
	instDep []bool
}

// maxPlayedBars caps the unrolled length against a runaway repeat.
const maxPlayedBars = 1 << 16

// acciaccaturaMax is the most a grace note takes before its beat: a 32nd
// note of the score's time.
var acciaccaturaMax = score.F(1, 32)

// walk visits every event of every voice in playing order, collecting
// notes (ties merged, graces placed) and the marks that act on a whole
// line: dynamics, hairpins, technique text, pedal.
func (in *interp) walk() {
	s := in.s
	in.techs = make([][]techPoint, len(s.Lines))
	in.lines = make([]lineMarks, len(s.Lines))
	in.instDep = make([]bool, len(s.Lines))
	for li, l := range s.Lines {
		for si, st := range l.Staves {
			for vi, v := range st.Voices {
				in.voices = append(in.voices, in.walkVoice(li, si, vi, v))
			}
		}
		sort.SliceStable(in.techs[li], func(i, j int) bool { return in.techs[li][i].at.Less(in.techs[li][j].at) })
	}
}

type graceNote struct {
	ev   *score.Event
	dur  score.Frac // written value, tuplets applied
	bar  int
	pass int
	off  score.Frac
}

func (in *interp) walkVoice(li, si, vi int, v *score.Voice) voiceEvents {
	ve := voiceEvents{line: li, staff: si, voice: vi}
	// open maps a tied pitch to the note it extends.
	open := map[int]int{}
	var graces []graceNote
	// sounding are the voice's notes that end where the next event starts,
	// so an acciaccatura can take its time from them.
	var sounding []int
	// end is where the voice's last timed event ended: graces left with no
	// main note (the voice stops, or falls silent for a bar) play there, in
	// time.
	end := score.Frac{Num: 0, Den: 1}
	hp := &hairpinState{}
	flush := func() {
		at := end
		for _, g := range graces {
			for _, nt := range g.ev.Notes {
				in.notes = append(in.notes, rawNote{at: at, dur: g.dur, velAt: at, pitch: nt.Pitch, midi: nt.Pitch.MIDI(), line: li, staff: si, voice: vi,
					bar: g.bar, pass: g.pass, grace: g.ev.Grace, art: articulationWord(g.ev), accent: accentOf(g.ev),
					segAt: at, segDur: g.dur, segMarks: g.ev.Marks})
			}
			at = at.Add(g.dur)
		}
		graces = graces[:0]
	}
	for _, pb := range in.order {
		m := v.Measure(pb.bar)
		if len(m.Events) == 0 {
			flush()
			clear(open)
			sounding = sounding[:0]
			continue
		}
		offs, _ := m.Offsets(pb.length)
		for ei := range m.Events {
			e := &m.Events[ei]
			at := pb.start.Add(offs[ei])
			in.collectMarks(li, e, at, at.Add(m.Actual(*e, pb.length)), hp)
			if e.Grace != score.NotGrace {
				graces = append(graces, graceNote{ev: e, dur: m.Actual(score.Event{Dur: e.Dur, Tuplet: e.Tuplet}, pb.length), bar: pb.bar, pass: pb.pass, off: offs[ei]})
				continue
			}
			dur := m.Actual(*e, pb.length)
			end = at.Add(dur)
			mainAt, mainDur := at, dur
			if len(graces) > 0 {
				mainAt, mainDur = in.placeGraces(graces, at, dur, li, si, vi, sounding)
				graces = graces[:0]
			}
			x := vev{at: at, dur: dur, mainAt: mainAt, mainDur: mainDur, off: offs[ei], bar: pb.bar, pass: pb.pass, e: e}
			if e.Kind != score.EventChord {
				clear(open)
				sounding = sounding[:0]
				ve.evs = append(ve.evs, x)
				continue
			}
			art := articulationWord(e)
			next := map[int]int{}
			sounding = sounding[:0]
			for _, nt := range e.Notes {
				midi := nt.Pitch.MIDI()
				idx, tied := open[midi]
				if tied {
					// The tie carries the first note on to this one's end.
					n := &in.notes[idx]
					n.dur = mainAt.Add(mainDur).Sub(n.at)
					n.segAt, n.segDur, n.segMarks = mainAt, mainDur, e.Marks
				} else {
					in.notes = append(in.notes, rawNote{at: mainAt, dur: mainDur, velAt: at, pitch: nt.Pitch, midi: midi, line: li, staff: si, voice: vi,
						bar: pb.bar, pass: pb.pass, art: art, accent: accentOf(e), segAt: mainAt, segDur: mainDur, segMarks: e.Marks})
					idx = len(in.notes) - 1
				}
				if nt.Tie {
					next[midi] = idx
				}
				sounding = append(sounding, idx)
				x.notes = append(x.notes, idx)
			}
			open = next
			ve.evs = append(ve.evs, x)
		}
	}
	flush()
	ve.end = end
	hp.close(in, li, end)
	return ve
}

// placeGraces adds a group of grace notes before the event at `at` (length
// dur) and returns where the main note now starts and how long it is.
//
// On the beat, graces lean on it: they take their written value from the
// main note, at most half of it. Before the beat they are crushed in just
// ahead of it — each a 32nd at most — taking the time from the notes still
// sounding into it; at the very top of the score there is nothing to take
// from, so they go on the beat instead. Which it is: an "on_beat" or
// "before_beat" mark on the group, else the score's Playback.Graces, else
// appoggiaturas on the beat and acciaccaturas before it.
func (in *interp) placeGraces(gs []graceNote, at, dur score.Frac, li, si, vi int, sounding []int) (score.Frac, score.Frac) {
	app := false
	forced, onBeat := false, false
	for _, g := range gs {
		if g.ev.Grace == score.Appoggiatura {
			app = true
		}
		if g.ev.Has("on_beat") {
			forced, onBeat = true, true
		} else if g.ev.Has("before_beat") {
			forced, onBeat = true, false
		}
	}
	if !forced {
		switch in.s.Playback.Graces {
		case score.GraceOn:
			onBeat = true
		case score.GraceBefore:
			onBeat = false
		default:
			onBeat = app
		}
	}
	var lens []score.Frac
	total := score.Frac{Num: 0, Den: 1}
	for _, g := range gs {
		l := g.dur
		if !onBeat {
			l = score.MinFrac(l, acciaccaturaMax)
		}
		lens = append(lens, l)
		total = total.Add(l)
	}
	if at.Less(total) {
		onBeat = true
	}
	if onBeat {
		if limit := dur.Div(score.Whole(2)); limit.Less(total) {
			// Squeeze the group into half the main note.
			k := limit.Div(total)
			for i := range lens {
				lens[i] = lens[i].Mul(k)
			}
			total = limit
		}
	}
	start := at
	if !onBeat {
		start = at.Sub(total)
		for _, idx := range sounding {
			n := &in.notes[idx]
			if n.at.Less(start) && start.Less(n.at.Add(n.dur)) {
				n.dur = start.Sub(n.at)
				if n.segAt.Less(start) {
					n.segDur = start.Sub(n.segAt)
				}
			}
		}
	}
	t := start
	for i, g := range gs {
		for _, nt := range g.ev.Notes {
			in.notes = append(in.notes, rawNote{at: t, dur: lens[i], velAt: at, pitch: nt.Pitch, midi: nt.Pitch.MIDI(), line: li, staff: si, voice: vi,
				bar: g.bar, pass: g.pass, grace: g.ev.Grace, art: articulationWord(g.ev), accent: accentOf(g.ev),
				segAt: t, segDur: lens[i], segMarks: g.ev.Marks})
		}
		t = t.Add(lens[i])
	}
	if onBeat {
		return at.Add(total), dur.Sub(total)
	}
	return at, dur
}

// collectMarks notes an event's line-wide marks: dynamics, hairpins,
// technique text, pedal. end is where the event ends.
func (in *interp) collectMarks(li int, e *score.Event, at, end score.Frac, hp *hairpinState) {
	lm := &in.lines[li]
	for _, mk := range e.Marks {
		if d, ok := score.Dynamics[mk.Name]; ok {
			lm.dyns = append(lm.dyns, dynMark{at: at, name: mk.Name, dyn: d})
		}
		switch mk.Name {
		case "tech":
			if mk.Value != "" {
				in.techs[li] = append(in.techs[li], techPoint{at: at, text: mk.Value})
			}
		case "cresc", "dim":
			dir := 1
			if mk.Name == "dim" {
				dir = -1
			}
			hp.start(in, li, at, dir)
		case "hairpin_end":
			hp.end(in, li, end)
		case "ped":
			lm.pedal = append(lm.pedal, pedalMark{at: at, down: true})
		case "ped_end":
			lm.pedal = append(lm.pedal, pedalMark{at: end})
		}
	}
}

// articulationWords name the instrument articulation a mark asks for.
var articulationWords = map[string]string{
	"stacc": "staccato", "staccmo": "staccatissimo", "ten": "tenuto", "acc": "accent",
	"marc": "marcato", "legato": "legato", "portato": "portato", "trem": "tremolo",
}

func articulationWord(e *score.Event) string {
	for _, m := range e.Marks {
		if w, ok := articulationWords[m.Name]; ok {
			return w
		}
	}
	// An sfz asks for an instrument's accented attack when it has one.
	for _, m := range e.Marks {
		if d, ok := score.Dynamics[m.Name]; ok && d.Momentary {
			return m.Name
		}
	}
	return ""
}

// accents are how much louder an articulation starts a note.
var accents = map[string]float32{"acc": 1.25, "marc": 1.4, "ten": 1.05}

func accentOf(e *score.Event) float32 {
	a := float32(1)
	for _, m := range e.Marks {
		if k, ok := accents[m.Name]; ok {
			a *= k
		}
	}
	return a
}

// techFamilies group technique words that replace each other: a word
// stays in force until another of its family (or "ord.") replaces it, so
// "con sord." survives "pizz." and "arco". The words that end a family's
// technique map to "".
var techFamilies = map[string]string{
	"pizz.": "pizz", "pizz": "pizz", "pizzicato": "pizz", "arco": "pizz",
	"con sord.": "sord", "con sordino": "sord", "sord.": "sord", "mute": "sord", "muted": "sord",
	"senza sord.": "sord", "senza sordino": "sord", "open": "sord",
	"trem.": "trem", "trem": "trem", "tremolo": "trem", "non trem.": "trem",
}

// techniqueEnds are the words that end their family's technique.
var techniqueEnds = map[string]bool{"arco": true, "senza sord.": true, "senza sordino": true, "open": true, "non trem.": true}

// techniqueResets go back to the instrument's own way of playing in every
// family.
var techniqueResets = map[string]bool{"ord.": true, "ord": true, "normal": true, "norm.": true, "nat.": true}

// technique is the technique text in force at x on a line, "" for the
// instrument's own way of playing: the most recent word still in force.
func technique(pts []techPoint, x score.Frac) string {
	type live struct {
		family, word string
	}
	var on []live
	for _, p := range pts {
		if x.Less(p.at) {
			break
		}
		w := p.text
		if techniqueResets[w] {
			on = on[:0]
			continue
		}
		fam, ok := techFamilies[w]
		if !ok {
			fam = "word:" + w
		}
		for i := 0; i < len(on); i++ {
			if on[i].family == fam {
				on = append(on[:i], on[i+1:]...)
				i--
			}
		}
		if !techniqueEnds[w] {
			on = append(on, live{fam, w})
		}
	}
	if len(on) == 0 {
		return ""
	}
	return on[len(on)-1].word
}

// resolve follows a note that became part of another (a slide's target)
// to the one that sounds.
func (in *interp) resolve(i int) int {
	for k := 0; k < 64 && in.notes[i].into != 0; k++ {
		i = in.notes[i].into - 1
	}
	return i
}

func (in *interp) finish() *Performance {
	s := in.s
	p := &Performance{Score: s, Tempo: in.tempo, InstrumentDependent: in.instDep}
	sec := func(f score.Frac) float64 { return in.tempo.Seconds(f.TicksFloat()) }

	p.Notes = make([]Note, 0, len(in.notes))
	for i := range in.notes {
		r := &in.notes[i]
		if r.drop || r.dur.Sign() <= 0 {
			continue
		}
		end := r.at.Add(r.dur)
		vel, acc := in.velocity(r)
		n := Note{
			Tick: r.at.Ticks(), Start: sec(r.at), End: sec(end),
			MIDI: r.midi, Velocity: vel, Accent: acc,
			Line: r.line, Staff: r.staff, Voice: r.voice, Bar: r.bar, Pass: r.pass, Grace: r.grace,
			Articulation: r.art, Technique: technique(in.techs[r.line], r.velAt),
		}
		n.Ticks = end.Ticks() - n.Tick
		if len(r.bends) > 0 {
			n.Bend = make([]BendPoint, len(r.bends))
			for k, b := range r.bends {
				n.Bend[k] = BendPoint{Tick: b.at.TicksFloat(), Semis: b.semis}
			}
		}
		p.Notes = append(p.Notes, n)
	}
	sort.SliceStable(p.Notes, func(i, j int) bool {
		a, b := &p.Notes[i], &p.Notes[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Staff != b.Staff {
			return a.Staff < b.Staff
		}
		if a.Voice != b.Voice {
			return a.Voice < b.Voice
		}
		return a.MIDI < b.MIDI
	})

	var end score.Frac
	for i, pb := range in.order {
		be := pb.start.Add(pb.length)
		p.Bars = append(p.Bars, Bar{Bar: pb.bar, Pass: pb.pass, Tick: pb.start.Ticks(), Ticks: be.Ticks() - pb.start.Ticks(),
			Start: sec(pb.start), End: sec(be), Meter: s.MeterAt(pb.bar)})
		at := pb.start
		beats := s.MeterAt(pb.bar).Beats()
		first := 0
		if pb.bar == 1 && s.Pickup.Sign() > 0 {
			// A pickup is the end of a bar: count its beats from the back.
			rest := s.MeterAt(1).Length()
			for first < len(beats) && pb.length.Less(rest) {
				rest = rest.Sub(beats[first])
				first++
			}
			if !rest.Eq(pb.length) {
				first, beats = 0, []score.Frac{pb.length}
			}
		}
		for k := first; k < len(beats) && at.Less(be); k++ {
			p.Beats = append(p.Beats, Beat{Tick: at.Ticks(), Start: sec(at), Bar: i, Beat: k + 1})
			at = at.Add(beats[k])
		}
		for _, mk := range s.Markers {
			if mk.At.Bar == pb.bar {
				x := pb.start.Add(mk.At.Offset)
				p.Markers = append(p.Markers, MarkerAt{Name: mk.Name, Tick: x.Ticks(), Start: sec(x)})
			}
		}
		end = be
	}
	p.Length = end.Ticks()
	p.Seconds = sec(end)

	in.sections(p)
	in.curves(p)
	for li, d := range in.dyn {
		p.Dynamics = append(p.Dynamics, LineDynamics{Line: li,
			Written:   Curve{Line: li, Target: "dynamics", Points: d.written.ticks()},
			Effective: Curve{Line: li, Target: "dynamics", Points: d.effective.ticks()}})
	}
	return p
}

// sections finds each section's span in the playing order.
func (in *interp) sections(p *Performance) {
	secs := append([]score.Section(nil), in.s.Sections...)
	if len(secs) == 0 {
		return
	}
	sectionOf := func(bar int) int {
		k := -1
		for i, sec := range secs {
			if sec.Bar <= bar {
				k = i
			}
		}
		return k
	}
	for k, sec := range secs {
		span := SectionSpan{Index: k, Name: sec.Name, Loop: sec.Loop, Next: in.s.SectionIndex(sec.Next), Tick: -1}
		for i, b := range p.Bars {
			inside := sectionOf(b.Bar) == k
			if span.Tick < 0 {
				if inside {
					span.Tick, span.Start = b.Tick, b.Start
					span.End, span.EndSec = b.Tick+b.Ticks, b.End
				}
				continue
			}
			if !inside {
				break
			}
			span.End, span.EndSec = p.Bars[i].Tick+p.Bars[i].Ticks, p.Bars[i].End
		}
		if span.Tick >= 0 {
			p.Sections = append(p.Sections, span)
		}
	}
	// Next named a score section; make it index the spans.
	for i := range p.Sections {
		if n := p.Sections[i].Next; n >= 0 {
			p.Sections[i].Next = p.SectionIndex(secs[n].Name)
		}
	}
}

// curves turns each line's lanes into curves over the playing order.
func (in *interp) curves(p *Performance) {
	for li, l := range in.s.Lines {
		for _, ln := range l.Lanes {
			type bp struct {
				at score.Frac
				v  float32
			}
			var pts []bp
			for _, lp := range ln.Points {
				pts = append(pts, bp{in.s.PosFrac(lp.At, in.starts), lp.Value})
				if lp.HasTo {
					pts = append(pts, bp{in.s.PosFrac(lp.To, in.starts), lp.Value})
				}
			}
			if len(pts) == 0 {
				continue
			}
			sort.SliceStable(pts, func(i, j int) bool { return pts[i].at.Less(pts[j].at) })
			eval := func(x score.Frac) float32 {
				if x.Less(pts[0].at) {
					return pts[0].v
				}
				for i := 1; i < len(pts); i++ {
					if x.Less(pts[i].at) {
						a, b := pts[i-1], pts[i]
						t := x.Sub(a.at).Div(b.at.Sub(a.at)).Float()
						return a.v + (b.v-a.v)*float32(t)
					}
				}
				return pts[len(pts)-1].v
			}
			c := Curve{Line: li, Target: ln.Name}
			for i, pb := range in.order {
				ws := in.starts[pb.bar-1]
				we := ws.Add(pb.length)
				c.Points = append(c.Points, CurvePoint{Tick: pb.start.TicksFloat(), Value: eval(ws)})
				for _, q := range pts {
					if ws.Less(q.at) && q.at.Less(we) {
						c.Points = append(c.Points, CurvePoint{Tick: pb.start.Add(q.at.Sub(ws)).TicksFloat(), Value: q.v})
					}
				}
				// Before a jump, hold the written value to the bar's end.
				if i+1 < len(in.order) && in.order[i+1].bar != pb.bar+1 {
					c.Points = append(c.Points, CurvePoint{Tick: pb.start.Add(pb.length).TicksFloat(), Value: eval(we)})
				}
			}
			p.Curves = append(p.Curves, c)
		}
	}
}

// ---------- reading a performance ----------

// BarAt is the index into Bars of the bar playing at a tick.
func (p *Performance) BarAt(tick float64) int {
	i := sort.Search(len(p.Bars), func(i int) bool { return float64(p.Bars[i].Tick) > tick }) - 1
	return max(i, 0)
}

// BeatAt is the index into Beats of the beat at a tick, -1 before the first.
func (p *Performance) BeatAt(tick float64) int {
	return sort.Search(len(p.Beats), func(i int) bool { return float64(p.Beats[i].Tick) > tick }) - 1
}

// SectionAt is the index into Sections of the span containing a tick, -1
// for none.
func (p *Performance) SectionAt(tick float64) int {
	for i, s := range p.Sections {
		if tick >= float64(s.Tick) && tick < float64(s.End) {
			return i
		}
	}
	return -1
}

// SectionIndex finds a section span by name, -1 for none.
func (p *Performance) SectionIndex(name string) int {
	for i, s := range p.Sections {
		if s.Name == name {
			return i
		}
	}
	return -1
}

// BarTick is the tick a written bar first plays at, -1 when it never does.
func (p *Performance) BarTick(bar int) int64 {
	for _, b := range p.Bars {
		if b.Bar == bar {
			return b.Tick
		}
	}
	return -1
}

// FirstNoteAt is the index of the first note starting at or after sec.
func (p *Performance) FirstNoteAt(sec float64) int {
	return sort.Search(len(p.Notes), func(i int) bool { return p.Notes[i].Start >= sec })
}

// DynamicsOf is a line's dynamics, nil when the line has none (no line).
func (p *Performance) DynamicsOf(line int) *LineDynamics {
	for i := range p.Dynamics {
		if p.Dynamics[i].Line == line {
			return &p.Dynamics[i]
		}
	}
	return nil
}
