// Package interpret turns a written score into a performance: every note
// as a timed event — tick, seconds, MIDI pitch, velocity, line — plus the
// control curves, the bars and beats, the sections and the markers that a
// scheduler (internal/score/play), an exporter or the editor's playhead
// needs.
//
// It is the one place that knows what notation means for sound. Today
// that is: the tempo map (marks and ramps), repeats unrolled, tuplets at
// any depth, ties merged into one long note (across bars and passes),
// grace notes placed around the beat, dynamics marks as velocities (sfz
// and fp too), technique text and articulation marks named for the
// instrument's articulations, and automation lanes as curves. Hairpins,
// slurs, articulation lengths, voltas and D.S./coda arrive in later phases
// as more passes over the same walk; Options is where they will be turned
// on and off.
//
// Everything is computed in exact score time (score.Frac) and only turned
// into ticks (score.TicksPerQuarter) and seconds at the end, so a note
// after a thousand triplets is exactly where it was written.
package interpret

import (
	"sort"

	"foundry/internal/score"
)

// Options shape an interpretation.
type Options struct {
	// NoRepeats plays the score straight through, once, as written.
	NoRepeats bool
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
	// Velocity is 0..1 (MIDI velocity / 127).
	Velocity float32
	// Line, Staff and Voice are 0-based indexes into the score.
	Line, Staff, Voice int
	// Bar is the written bar the note starts in; Pass the time through it.
	Bar, Pass int
	Grace     score.Grace
	// Articulation is a word naming how this note is played ("staccato",
	// from its marks), Technique the line's technique text in force
	// ("pizz."). A scheduler looks each up in the instrument
	// (Instrument.ArticulationIndex), the note's own first.
	Articulation string
	Technique    string
}

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
// the ends. Target is the lane it came from ("volume", "pan", …).
type Curve struct {
	Line   int
	Target string
	Points []CurvePoint
}

// At reads the curve at a tick. Of two points at one tick (a jump at a
// repeat) the later wins from that tick on.
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

// Performance is a score as it sounds.
type Performance struct {
	Score *score.Score
	// Notes are in onset order (then line, staff, voice, pitch).
	Notes    []Note
	Curves   []Curve
	Bars     []Bar
	Beats    []Beat
	Sections []SectionSpan
	Markers  []MarkerAt
	Tempo    *TempoMap
	// Length is the whole performance in ticks; Seconds its duration.
	Length  int64
	Seconds float64
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
	midi    int
	line    int
	staff   int
	voice   int
	bar     int
	pass    int
	grace   score.Grace
	art     string
}

// dynPoint is a dynamic mark in performance time.
type dynPoint struct {
	at  score.Frac
	dyn score.Dynamic
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
	return in.finish(), nil
}

type interp struct {
	s      *score.Score
	o      Options
	starts []score.Frac // written bar starts
	order  []played
	tempo  *TempoMap

	notes []rawNote
	dyns  [][]dynPoint
	techs [][]techPoint
}

// maxPlayedBars caps the unrolled length against a runaway repeat.
const maxPlayedBars = 1 << 16

// layout unrolls the repeats into the bars in playing order.
func (in *interp) layout() {
	s := in.s
	n := s.Measures()
	in.starts = s.BarStarts()
	reps := append([]score.Repeat(nil), s.Repeats...)
	sort.SliceStable(reps, func(i, j int) bool { return reps[i].Start < reps[j].Start })
	if in.o.NoRepeats {
		reps = nil
	}
	count := make([]int, len(reps))
	at := score.Frac{Num: 0, Den: 1}
	for b := 1; b <= n && len(in.order) < maxPlayedBars; {
		pass := 1
		ri := -1
		for i, r := range reps {
			if b >= r.Start && b <= r.End && r.Start >= 1 && r.End <= n && r.End >= r.Start {
				ri = i
				pass = count[i] + 1
				break
			}
		}
		l := s.BarLength(b)
		in.order = append(in.order, played{bar: b, pass: pass, start: at, length: l})
		at = at.Add(l)
		if ri >= 0 && b == reps[ri].End && count[ri]+1 < reps[ri].Plays() {
			count[ri]++
			b = reps[ri].Start
			continue
		}
		b++
	}
}

// writtenTempo is the tempo in force at a written position and whether it
// is ramping there.
func (in *interp) writtenTempo(pos score.Frac, marks []score.TempoMark, at []score.Frac) (float64, bool) {
	if len(marks) == 0 {
		return DefaultQPM, false
	}
	k := -1
	for i := range marks {
		if !pos.Less(at[i]) {
			k = i
		}
	}
	if k < 0 {
		return marks[0].QuarterBPM(), false
	}
	q := marks[k].QuarterBPM()
	if marks[k].Ramp && k+1 < len(marks) && at[k].Less(at[k+1]) {
		q1 := marks[k+1].QuarterBPM()
		t := pos.Sub(at[k]).Div(at[k+1].Sub(at[k])).Float()
		return q + (q1-q)*t, true
	}
	return q, false
}

// buildTempo lays the tempo marks over the bars as played: a point at each
// played bar's start with the tempo in force there (so a repeat jumping
// back picks the right one up), and one at each mark inside a bar.
func (in *interp) buildTempo() {
	marks := append([]score.TempoMark(nil), in.s.Tempo...)
	sort.SliceStable(marks, func(i, j int) bool { return score.PosLess(marks[i].At, marks[j].At) })
	at := make([]score.Frac, len(marks))
	for i, m := range marks {
		at[i] = in.s.PosFrac(m.At, in.starts)
	}
	var pts []TempoPoint
	push := func(tick float64, q float64, ramp bool) {
		if n := len(pts); n > 0 {
			last := &pts[n-1]
			if last.Tick == tick {
				last.QPM, last.Ramp = q, ramp
				return
			}
			if !last.Ramp && !ramp && last.QPM == q {
				return
			}
		}
		pts = append(pts, TempoPoint{Tick: tick, QPM: q, Ramp: ramp})
	}
	for _, pb := range in.order {
		ws := in.starts[pb.bar-1]
		we := ws.Add(pb.length)
		q, ramp := in.writtenTempo(ws, marks, at)
		push(pb.start.TicksFloat(), q, ramp)
		for i, m := range marks {
			if ws.Less(at[i]) && at[i].Less(we) {
				q, ramp := in.writtenTempo(at[i], marks, at)
				push(pb.start.Add(at[i].Sub(ws)).TicksFloat(), q, ramp && m.Ramp)
			}
		}
	}
	in.tempo = NewTempoMap(pts)
}

// acciaccaturaMax is the most an acciaccatura takes before its beat: a
// 32nd note of the score's time.
var acciaccaturaMax = score.F(1, 32)

// walk visits every event of every voice in playing order, collecting
// notes (ties merged, graces placed), dynamics and technique text.
func (in *interp) walk() {
	s := in.s
	in.dyns = make([][]dynPoint, len(s.Lines))
	in.techs = make([][]techPoint, len(s.Lines))
	for li, l := range s.Lines {
		for si, st := range l.Staves {
			for vi, v := range st.Voices {
				in.walkVoice(li, si, vi, v)
			}
		}
		sort.SliceStable(in.dyns[li], func(i, j int) bool { return in.dyns[li][i].at.Less(in.dyns[li][j].at) })
		sort.SliceStable(in.techs[li], func(i, j int) bool { return in.techs[li][i].at.Less(in.techs[li][j].at) })
	}
}

type graceNote struct {
	ev   score.Event
	dur  score.Frac // written value, tuplets applied
	bar  int
	pass int
}

func (in *interp) walkVoice(li, si, vi int, v *score.Voice) {
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
	flush := func() {
		at := end
		for _, g := range graces {
			for _, nt := range g.ev.Notes {
				in.notes = append(in.notes, rawNote{at: at, dur: g.dur, velAt: at, midi: nt.Pitch.MIDI(), line: li, staff: si, voice: vi,
					bar: g.bar, pass: g.pass, grace: g.ev.Grace, art: articulationWord(g.ev)})
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
		for ei, e := range m.Events {
			at := pb.start.Add(offs[ei])
			in.collectMarks(li, e, at)
			if e.Grace != score.NotGrace {
				graces = append(graces, graceNote{ev: e, dur: m.Actual(score.Event{Dur: e.Dur, Tuplet: e.Tuplet}, pb.length), bar: pb.bar, pass: pb.pass})
				continue
			}
			dur := m.Actual(e, pb.length)
			end = at.Add(dur)
			mainAt, mainDur := at, dur
			if len(graces) > 0 {
				mainAt, mainDur = in.placeGraces(graces, at, dur, li, si, vi, sounding)
				graces = graces[:0]
			}
			if e.Kind != score.EventChord {
				clear(open)
				sounding = sounding[:0]
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
					in.notes[idx].dur = mainAt.Add(mainDur).Sub(in.notes[idx].at)
				} else {
					in.notes = append(in.notes, rawNote{at: mainAt, dur: mainDur, velAt: at, midi: midi, line: li, staff: si, voice: vi,
						bar: pb.bar, pass: pb.pass, art: art})
					idx = len(in.notes) - 1
				}
				if nt.Tie {
					next[midi] = idx
				}
				sounding = append(sounding, idx)
			}
			open = next
		}
	}
	flush()
}

// placeGraces adds a group of grace notes before the event at `at` (length
// dur) and returns where the main note now starts and how long it is.
//
// Appoggiaturas lean on the beat: they take their written value from the
// main note, at most half of it. Acciaccaturas are crushed in just before
// the beat — each a 32nd at most — taking the time from the notes still
// sounding into it; at the very top of the score there is nothing to take
// from, so they go on the beat instead.
func (in *interp) placeGraces(gs []graceNote, at, dur score.Frac, li, si, vi int, sounding []int) (score.Frac, score.Frac) {
	app := false
	for _, g := range gs {
		if g.ev.Grace == score.Appoggiatura {
			app = true
		}
	}
	var lens []score.Frac
	total := score.Frac{Num: 0, Den: 1}
	for _, g := range gs {
		l := g.dur
		if !app {
			l = score.MinFrac(l, acciaccaturaMax)
		}
		lens = append(lens, l)
		total = total.Add(l)
	}
	onBeat := app || at.Less(total)
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
			}
		}
	}
	t := start
	for i, g := range gs {
		for _, nt := range g.ev.Notes {
			in.notes = append(in.notes, rawNote{at: t, dur: lens[i], velAt: at, midi: nt.Pitch.MIDI(), line: li, staff: si, voice: vi,
				bar: g.bar, pass: g.pass, grace: g.ev.Grace, art: articulationWord(g.ev)})
		}
		t = t.Add(lens[i])
	}
	if onBeat {
		return at.Add(total), dur.Sub(total)
	}
	return at, dur
}

// collectMarks notes an event's dynamics and technique text for its line.
func (in *interp) collectMarks(li int, e score.Event, at score.Frac) {
	for _, mk := range e.Marks {
		if d, ok := score.Dynamics[mk.Name]; ok {
			in.dyns[li] = append(in.dyns[li], dynPoint{at: at, dyn: d})
		}
		if mk.Name == "tech" && mk.Value != "" {
			in.techs[li] = append(in.techs[li], techPoint{at: at, text: mk.Value})
		}
	}
}

// articulationWords name the instrument articulation a mark asks for.
var articulationWords = map[string]string{
	"stacc": "staccato", "staccmo": "staccatissimo", "ten": "tenuto", "acc": "accent",
	"marc": "marcato", "legato": "legato", "portato": "portato", "trem": "tremolo",
}

func articulationWord(e score.Event) string {
	for _, m := range e.Marks {
		if w, ok := articulationWords[m.Name]; ok {
			return w
		}
	}
	return ""
}

// techniqueResets are the words that go back to the instrument's own way
// of playing.
var techniqueResets = map[string]bool{"arco": true, "ord.": true, "ord": true, "normal": true, "nat.": true, "senza sord.": true}

// velocity is the level at x on a line: the last dynamic at or before it,
// or that dynamic's momentary level when it is exactly there.
func velocity(pts []dynPoint, x score.Frac) float32 {
	level := score.Dynamics[score.DefaultDynamic].Level
	for _, p := range pts {
		if x.Less(p.at) {
			break
		}
		if p.at.Eq(x) {
			level = p.dyn.Level
			continue
		}
		switch {
		case p.dyn.After > 0:
			level = p.dyn.After
		case p.dyn.Momentary:
			// sfz accents its own notes only; the level carries on.
		default:
			level = p.dyn.Level
		}
	}
	return level
}

// technique is the technique text in force at x on a line, "" for the
// instrument's own way of playing.
func technique(pts []techPoint, x score.Frac) string {
	t := ""
	for _, p := range pts {
		if x.Less(p.at) {
			break
		}
		t = p.text
		if techniqueResets[t] {
			t = ""
		}
	}
	return t
}

func (in *interp) finish() *Performance {
	s := in.s
	p := &Performance{Score: s, Tempo: in.tempo}
	sec := func(f score.Frac) float64 { return in.tempo.Seconds(f.TicksFloat()) }

	p.Notes = make([]Note, 0, len(in.notes))
	for _, r := range in.notes {
		if r.dur.Sign() <= 0 {
			continue
		}
		end := r.at.Add(r.dur)
		n := Note{
			Tick: r.at.Ticks(), Start: sec(r.at), End: sec(end),
			MIDI: r.midi, Velocity: velocity(in.dyns[r.line], r.velAt),
			Line: r.line, Staff: r.staff, Voice: r.voice, Bar: r.bar, Pass: r.pass, Grace: r.grace,
			Articulation: r.art, Technique: technique(in.techs[r.line], r.velAt),
		}
		n.Ticks = end.Ticks() - n.Tick
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
