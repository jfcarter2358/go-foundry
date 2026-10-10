// Package engrave lays a score out as music notation: a display list
// (list.go) of glyphs, lines, filled paths and text in staff spaces, which
// any backend draws — a PNG (engrave/raster), the editor's imgui view
// (internal/score/scoredraw), a PDF page in phase 5.
//
// The engraver follows the conventions of Elaine Gould's Behind Bars and
// the metrics of the SMuFL font it draws with (internal/score/glyph):
// stems join noteheads at the font's stemUpSE/stemDownNW anchors, flags
// meet stems at theirs, every thickness is the font's engravingDefaults.
//
// What it engraves, per staff: the five lines; clef, key and time
// signature at the start and wherever they change (cancelling naturals,
// additive and symbol meters); single, double and final barlines;
// noteheads by duration and chords with seconds set either side of the
// stem; accidentals by the bar against the key, shared by the staff's
// voices, tied-over notes not repeated, stacked in columns for chords;
// ledger lines; stems by the furthest note from the middle line, forced up
// and down when a staff has more than one voice, reaching the middle line
// from far outside the staff; flags; beams grouped by the meter (beats,
// half bars of eighths in 4/4, the groups of an additive meter, the dotted
// beats of a compound one), with secondary and partial beams, slopes
// limited by the interval and quantised against the staff lines; dots,
// moved into spaces; rests by duration, offset for the voice in
// multi-voice music and centred as whole-bar rests; ties; tuplet brackets
// and numbers (nested); grace notes, small and slashed.
//
// Layout is in two passes, the way a page layout needs it (phase 5): each
// bar is measured first — its columns (onsets shared by every staff),
// what each column needs left and right of its noteheads (accidentals,
// grace notes, displaced heads, dots, flags) and how much room its
// durations ask for — and then placed on a system, which fixes x for
// every column; only then are glyphs drawn, because beams and ties need
// final positions. Engrave places every bar on one continuous system, the
// editor's galley view. The page layout will break bars into systems and
// stretch each to the measure, reusing measureBar and drawSystem.
package engrave

import (
	"math"
	"sort"

	"foundry/internal/score"
	"foundry/internal/score/glyph"
)

// Options choose what to engrave.
type Options struct {
	// From and To are the bars (1-based, inclusive); zero is the score's
	// start or end.
	From, To int
	// Lines are the lines to engrave, by index; nil is every line.
	Lines []int
	// Concert shows transposing staves at sounding pitch.
	Concert bool
	// Spacing scales the room durations ask for; 0 is 1.
	Spacing float64
	// BarNumbers is how often bars are numbered above the top staff:
	// 0 numbers only the layout's first bar (when it is not bar 1), n > 0
	// every n-th bar as well, -1 none.
	BarNumbers int
}

// Engraving constants not in the font's defaults, in staff spaces.
const (
	graceSize    = 0.66 // a grace note's size against a normal note
	stemLength   = 3.5  // an octave: the standard stem
	beamStep     = 0.75 // a beam's thickness plus the gap to the next (0.5 + 0.25)
	accGap       = 0.2  // accidental to notehead
	accColumnGap = 0.12 // between columns of a chord's accidentals
	dotGap       = 0.3  // notehead to dot
	dotSpacing   = 0.62 // dot to dot (a double dot)
	noteGap      = 0.45 // the least space between one column's ink and the next's
	graceGap     = 0.5  // a grace note's ink to the note it is before
	barGapAfter  = 1.0  // barline to the first note (or its accidentals: at least 0.6)
	prefixGap    = 1.2  // clef/key/time to the first note
	braceGap     = 0.25 // brace to the system's barline
	staffGap     = 7.0  // bottom line of one line's staff to the top of the next
	grandGap     = 6.0  // between the staves of a grand staff
	inkPad       = 1.0  // clearance between one staff's ink and the next's
	margin       = 1.0  // around the whole layout
)

// Engrave lays a score out on one continuous system.
func Engrave(s *score.Score, o Options) *List {
	e := newEngraver(s, o)
	if len(e.staves) == 0 || len(e.bars) == 0 {
		return &List{Width: 2 * margin, Height: 2 * margin}
	}
	e.collect()
	e.measure()
	e.placeGalley()
	e.draw()
	return e.finish()
}

// engraver is one layout in progress.
type engraver struct {
	s *score.Score
	o Options
	f *glyph.Font
	d glyph.Defaults

	from, to int
	last     bool // the layout ends at the score's end: a final barline

	staves []*staffCtx
	bars   []*barCtx // from..to

	items []Item // system-wide items (bar numbers, braces), absolute y
	list  *List
}

// staffCtx is one staff across the layout.
type staffCtx struct {
	line, staff int
	st          *score.Staff
	clef        string
	bottom      int            // diatonic of the bottom line
	iv          score.Interval // sounding → written; zero when concert
	transposed  bool
	// percussion shows drums by place and notehead (score.DrumDisplay),
	// with no key and no accidentals.
	percussion bool
	first, end bool // first / last staff of its line
	nStaves    int  // staves in its line

	bars  []*staffBar // per layout bar
	items []Item      // staff-local y (top line at 0), absolute x
	top   float64     // the top line's y on the layout
	ext   Rect        // ink, staff-local
}

// staffBar is one staff's bar.
type staffBar struct {
	st      *staffCtx
	bar     *barCtx
	voices  [][]*ev         // per voice, graces in place
	forced  bool            // more than one voice has music: stems by voice
	fifths  int             // written key in force
	empty   bool            // nothing written in any voice: an implied bar rest
	meas    []score.Measure // per voice: the measure as written (its tuplets)
	beams   []*beamGroup
	tuplets []*tupletSpan
}

// barCtx is one bar of the layout.
type barCtx struct {
	bar    int
	length score.Frac
	meter  score.Meter
	fifths int // concert key in force

	showKey, showTime bool // a change at its head (or the layout's start)
	prevFifths        int  // concert key before a change
	cols              []*column

	// x on the system: the opening barline, where music starts after the
	// head's signatures, and the closing barline.
	x0, content, x1 float64
	// prefixW is the room the head's signatures take on every staff.
	prefixW float64
	// minW and ideal are the bar's content width as measured.
	widths []float64 // per column gap: x[i+1]-x[i]
	lead   float64   // content start to the first column
}

// column is one onset of a bar, shared by every staff.
type column struct {
	off         score.Frac
	offF        float64
	x           float64 // relative to the bar's content start, then absolute
	left, right float64 // ink each side of a normal notehead's left edge
	end         bool    // the bar's end (only graces and the barline)
}

// ev is one event as engraved.
type ev struct {
	ref     Ref
	e       *score.Event
	sb      *staffBar
	voice   int
	onset   score.Frac
	grace   bool
	size    float64
	notes   []*nt // ascending staff position (chords)
	rest    bool  // a printed rest (incl. a bar rest)
	barRest bool
	space   bool
	base    score.DurBase
	dots    int
	hasStem bool
	up      bool // stem direction (stemless notes: the side ties and dots favour)
	forced  bool
	beam    *beamGroup
	col     *column
	tuplets []int // tuplet chain, innermost first (1-based into the measure)
	actual  score.Frac

	shift float64 // x of the head origin from the column x (voice offset, grace place)
	x     float64 // absolute head origin x

	// head is the notehead glyph the event's stem and width follow: its
	// value's, or on a percussion staff the shape every note shares.
	head     string
	headW    float64 // a normal head's width at this size
	dotX     float64 // relative to x
	dotRows  []int   // staff positions of the dots (odd)
	restY    float64
	restX    float64 // relative to x
	restDotX float64 // from the rest's x
	restDotY float64
	stemX    float64 // absolute stem centre
	tipY     float64 // stem tip y (staff-local)
	box      Rect    // ink, staff-local y
	tupTop   float64 // ink extremes including tuplets drawn so far
	tupBot   float64
	// tupInTop and tupInBot are the extremes of the tuplets alone (inner
	// ones, drawn first).
	tupInTop, tupInBot float64
	graces             []*ev // grace notes before this one (main events only)
	nextEv             *ev   // the next main event of the voice (ties)
}

// nt is one note of a chord.
type nt struct {
	idx     int
	p       score.Pitch // written
	pos     int         // half spaces above the bottom line
	y       float64     // staff-local
	dx      float64     // head offset from the event's x (a displaced head)
	acc     bool        // its accidental shows
	parens  bool
	accX    float64 // the accidental's x relative to the column (or a grace's) x
	tieIn   bool    // tied from the event before
	tie     bool    // tied into the next
	tieTo   *nt
	tieEv   *ev
	tieFrom *nt
	ev      *ev
	// head is a percussion note's own notehead ("" for the event's).
	head string
}

func newEngraver(s *score.Score, o Options) *engraver {
	e := &engraver{s: s, o: o, f: glyph.Bravura()}
	e.d = e.f.Defaults
	if e.o.Spacing <= 0 {
		e.o.Spacing = 1
	}
	n := s.Measures()
	e.from, e.to = max(1, o.From), o.To
	if e.to <= 0 || e.to > n {
		e.to = n
	}
	if e.from > e.to {
		return e
	}
	e.last = e.to == n
	lines := o.Lines
	if lines == nil {
		for i := range s.Lines {
			lines = append(lines, i)
		}
	}
	for _, li := range lines {
		if li < 0 || li >= len(s.Lines) {
			continue
		}
		l := s.Lines[li]
		for si, st := range l.Staves {
			sc := &staffCtx{line: li, staff: si, st: st, clef: st.Clef, bottom: clefBottom(st.Clef),
				first: si == 0, end: si == len(l.Staves)-1, nStaves: len(l.Staves), percussion: st.Clef == "percussion"}
			if sc.percussion {
				// A drum staff does not transpose.
			} else if !o.Concert && (st.Transpose.Diatonic != 0 || st.Transpose.Chromatic != 0) {
				sc.iv = score.Interval{Diatonic: -st.Transpose.Diatonic, Chromatic: -st.Transpose.Chromatic}
				sc.transposed = true
			}
			e.staves = append(e.staves, sc)
		}
	}
	prevMeter, prevKey := score.Meter{}, 0
	if e.from > 1 {
		prevMeter, prevKey = s.MeterAt(e.from-1), s.KeyAt(e.from-1).Fifths
	}
	for b := e.from; b <= e.to; b++ {
		bc := &barCtx{bar: b, length: s.BarLength(b), meter: s.MeterAt(b), fifths: s.KeyAt(b).Fifths, prevFifths: prevKey}
		switch {
		case b == e.from:
			bc.showKey = true
			bc.showTime = b == 1 || meterMarkAt(s, b)
		default:
			bc.showKey = bc.fifths != prevKey
			bc.showTime = !bc.meter.Eq(prevMeter)
		}
		prevMeter, prevKey = bc.meter, bc.fifths
		e.bars = append(e.bars, bc)
	}
	return e
}

func meterMarkAt(s *score.Score, bar int) bool {
	for _, m := range s.Meter {
		if m.Bar == bar {
			return true
		}
	}
	return false
}

// ---------- collecting the music ----------

// collect builds every staff's bars: written pitches, onsets, stems,
// beams, accidentals and tie links.
func (e *engraver) collect() {
	for _, sc := range e.staves {
		sc.bars = make([]*staffBar, len(e.bars))
		for bi, bc := range e.bars {
			sb := &staffBar{st: sc, bar: bc, fifths: sc.key(bc.fifths)}
			sb.voices = make([][]*ev, len(sc.st.Voices))
			sb.meas = make([]score.Measure, len(sc.st.Voices))
			sc.bars[bi] = sb
		}
		for vi, v := range sc.st.Voices {
			e.collectVoice(sc, vi, v)
		}
		for _, sb := range sc.bars {
			e.prepareBar(sb)
		}
	}
	e.columns()
}

// collectVoice reads one voice over the layout's bars, linking ties across
// bars (and from the bar before the layout's first).
func (e *engraver) collectVoice(sc *staffCtx, vi int, v *score.Voice) {
	var prev *ev // the previous main event, across bars
	if e.from > 1 {
		prev = e.lastEventBefore(sc, vi, v, e.from-1)
	}
	for bi, bc := range e.bars {
		sb := sc.bars[bi]
		m := v.Measure(bc.bar)
		sb.meas[vi] = m
		if len(m.Events) == 0 {
			continue
		}
		offs, _ := m.Offsets(bc.length)
		whole := wholeBarRest(m, bc.length)
		var pending []*ev
		for ei := range m.Events {
			se := &m.Events[ei]
			x := e.newEv(sb, vi, bc.bar, ei, se, offs[ei], m)
			if whole && x.rest {
				x.barRest = true // a whole rest filling the bar sits in its middle
			}
			sb.voices[vi] = append(sb.voices[vi], x)
			if x.grace {
				pending = append(pending, x)
				continue
			}
			x.graces, pending = pending, nil
			if prev != nil {
				linkTies(prev, x)
				prev.nextEv = x
			}
			prev = x
		}
	}
}

// wholeBarRest reports a bar written as one whole rest filling it, which
// is engraved as a bar rest.
func wholeBarRest(m score.Measure, barLen score.Frac) bool {
	n := 0
	for _, e := range m.Events {
		if e.Grace == score.NotGrace {
			n++
		}
	}
	return n == 1 && len(m.Events) == 1 && m.Events[0].Kind == score.EventRest &&
		m.Events[0].Dur == score.Duration{Base: score.WholeNote} && m.Actual(m.Events[0], barLen).Eq(barLen)
}

// lastEventBefore is the last main event of a voice in a bar outside the
// layout, built only for its ties into the layout's first bar.
func (e *engraver) lastEventBefore(sc *staffCtx, vi int, v *score.Voice, bar int) *ev {
	m := v.Measure(bar)
	for ei := len(m.Events) - 1; ei >= 0; ei-- {
		se := &m.Events[ei]
		if se.Grace != score.NotGrace {
			continue
		}
		if se.Kind != score.EventChord {
			return nil
		}
		sb := &staffBar{st: sc, bar: &barCtx{bar: bar, length: e.s.BarLength(bar)}, fifths: sc.key(e.s.KeyAt(bar).Fifths)}
		x := e.newEv(sb, vi, bar, ei, se, score.Frac{}, m)
		x.ref.Bar = -bar // outside the layout
		return x
	}
	return nil
}

func (e *engraver) newEv(sb *staffBar, vi, bar, ei int, se *score.Event, onset score.Frac, m score.Measure) *ev {
	sc := sb.st
	x := &ev{
		ref: Ref{Line: sc.line, Staff: sc.staff, Voice: vi, Bar: bar, Event: ei, Note: -1},
		e:   se, sb: sb, voice: vi, onset: onset, size: 1,
		base: se.Dur.Base, dots: int(se.Dur.Dots),
	}
	x.actual = m.Actual(*se, sb.bar.length)
	for t := se.Tuplet; t > 0 && t <= len(m.Tuplets); t = m.Tuplets[t-1].Parent {
		x.tuplets = append(x.tuplets, t)
	}
	if se.Grace != score.NotGrace {
		x.grace, x.size = true, graceSize
	}
	switch se.Kind {
	case score.EventRest:
		x.rest = true
	case score.EventBarRest:
		x.rest, x.barRest = true, true
	case score.EventSpace:
		x.space = true
	case score.EventChord:
		shared, same := score.DrumHead(0), true
		for k, n := range se.Notes {
			p := n.Pitch
			if sc.transposed {
				p = p.Transpose(sc.iv)
			}
			q := &nt{idx: k, p: p, pos: p.Diatonic() - sc.bottom, tie: n.Tie, ev: x}
			if sc.percussion {
				pos, h := score.DrumDisplay(n.Pitch)
				q.p, q.pos, q.head = score.FromDiatonic(pos+sc.bottom), pos, drumHeadGlyph(h, x.base)
				if k == 0 {
					shared = h
				}
				same = same && h == shared
			}
			q.y = 4 - float64(q.pos)/2
			switch {
			case sc.percussion:
				// Drums have no accidentals.
			case n.Acc == score.AccForce:
				q.acc = true
			case n.Acc == score.AccCautionary:
				q.acc, q.parens = true, true
			}
			x.notes = append(x.notes, q)
		}
		sort.SliceStable(x.notes, func(i, j int) bool { return x.notes[i].pos < x.notes[j].pos })
		x.hasStem = x.base >= score.Half
		if sc.percussion && same {
			x.head = drumHeadGlyph(shared, x.base)
		}
	}
	if x.head == "" {
		x.head = headGlyph(x.base)
	}
	x.headW = e.f.Width(x.head) * x.size
	return x
}

// key is the key signature a staff shows for a concert key: none on a
// percussion staff.
func (sc *staffCtx) key(fifths int) int {
	if sc.percussion {
		return 0
	}
	return writtenFifths(fifths, sc.iv)
}

// linkTies joins the tied notes of a to the same written pitch in b.
func linkTies(a, b *ev) {
	for _, n := range a.notes {
		if !n.tie {
			continue
		}
		for _, m := range b.notes {
			if !m.tieIn && m.p == n.p {
				n.tieTo, n.tieEv = m, b
				m.tieIn, m.tieFrom = true, n
				break
			}
		}
	}
}

// WrittenFifths is the key signature a staff transposed by iv (sounding to
// written, the negative of score.Staff.Transpose) shows for a concert key:
// what an editor spells a click on that staff in.
func WrittenFifths(fifths int, iv score.Interval) int { return writtenFifths(fifths, iv) }

// writtenFifths is the key signature a staff shows for a concert key.
func writtenFifths(fifths int, iv score.Interval) int {
	if iv.Diatonic == 0 && iv.Chromatic == 0 {
		return fifths
	}
	// The major key's tonic, moved like a note, then read back as fifths.
	tonic := score.Pitch{Step: 0, Octave: 4}
	for i := 0; i < fifths; i++ {
		tonic = tonic.Transpose(score.Interval{Diatonic: 4, Chromatic: 7})
	}
	for i := 0; i > fifths; i-- {
		tonic = tonic.Transpose(score.Interval{Diatonic: 3, Chromatic: 5})
	}
	t := tonic.Transpose(iv)
	f := lineOfFifths[t.Step] + 7*int(t.Alter)
	for f > 7 {
		f -= 12
	}
	for f < -7 {
		f += 12
	}
	return f
}

// lineOfFifths is each natural letter's place on the line of fifths from C.
var lineOfFifths = [7]int{0, 2, 4, -1, 1, 3, 5}

// prepareBar works out a staff's bar: which voices are written, stem
// directions, beams, tuplet spans and accidentals.
func (e *engraver) prepareBar(sb *staffBar) {
	written := 0
	for _, v := range sb.voices {
		for _, x := range v {
			if !x.space {
				written++
				break
			}
		}
	}
	sb.forced = written > 1
	sb.empty = true
	for _, v := range sb.voices {
		if len(v) > 0 {
			sb.empty = false
		}
	}
	for vi, v := range sb.voices {
		for _, x := range v {
			x.forced = sb.forced
			x.up = e.stemUp(x, vi, sb.forced)
		}
		e.beamVoice(sb, vi, v)
		e.tupletSpans(sb, vi, v)
	}
	e.accidentals(sb)
}

// stemUp decides a lone event's stem: a mark, the voice in multi-voice
// music, then the note furthest from the middle line. Beams decide again
// for their whole group.
func (e *engraver) stemUp(x *ev, voice int, forced bool) bool {
	if x.e.Has("up") {
		return true
	}
	if x.e.Has("down") {
		return false
	}
	if forced {
		return voice%2 == 0
	}
	if x.grace {
		return true
	}
	if len(x.notes) == 0 {
		return true
	}
	ps := make([]int, len(x.notes))
	for i, n := range x.notes {
		ps[i] = n.pos
	}
	return autoUp(ps)
}

// autoUp is the stem direction for notes at staff positions: away from the
// note furthest from the middle line; balanced, the majority's side; a
// note on the middle line itself, down.
func autoUp(ps []int) bool {
	lo, hi := ps[0], ps[0]
	sum := 0
	for _, p := range ps {
		lo, hi = min(lo, p), max(hi, p)
		sum += p - 4
	}
	above, below := hi-4, 4-lo
	if above != below {
		return below > above
	}
	return sum < 0
}

// accidentals decides which notes show theirs: per staff and bar, every
// voice together in time order, against the key; a tied-over note neither
// shows one nor sets the bar's state.
func (e *engraver) accidentals(sb *staffBar) {
	var all []*ev
	for _, v := range sb.voices {
		for _, x := range v {
			if len(x.notes) > 0 {
				all = append(all, x)
			}
		}
	}
	// Time order; at one time grace notes before the main notes, each
	// voice's in its written order.
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if c := a.onset.Cmp(b.onset); c != 0 {
			return c < 0
		}
		if a.grace != b.grace {
			return a.grace
		}
		if a.voice != b.voice {
			return a.voice < b.voice
		}
		return a.ref.Event < b.ref.Event
	})
	state := map[int]int{} // diatonic → alteration in force
	for _, x := range all {
		for _, n := range x.notes {
			d := n.p.Diatonic()
			cur, ok := state[d]
			if !ok {
				cur = score.KeyAlter(sb.fifths, int(n.p.Step))
			}
			if n.tieIn {
				n.acc, n.parens = false, false
				continue
			}
			if int(n.p.Alter) != cur {
				n.acc = true
			}
			state[d] = int(n.p.Alter)
		}
		// An altered unison (C and C# together) shows both.
		for i := 1; i < len(x.notes); i++ {
			a, b := x.notes[i-1], x.notes[i]
			if a.pos == b.pos && a.p.Alter != b.p.Alter {
				a.acc, b.acc = !a.tieIn, !b.tieIn
			}
		}
	}
}

// ---------- the system ----------

// placeGalley puts every bar end to end on one system at its natural
// width.
func (e *engraver) placeGalley() {
	start := e.startPrefix()
	x := start
	for i, bc := range e.bars {
		bc.x0 = x
		if i == 0 {
			bc.x0, bc.content = 0, start
		} else {
			bc.content = x + barlineWidth(e.d, e.barlineBefore(i)) + bc.prefixW
		}
		cx := bc.content + bc.lead
		for k, c := range bc.cols {
			c.x = cx
			if k < len(bc.widths) {
				cx += bc.widths[k]
			}
		}
		bc.x1 = bc.cols[len(bc.cols)-1].x
		x = bc.x1
	}
	for _, sc := range e.staves {
		for _, sb := range sc.bars {
			for _, v := range sb.voices {
				for _, x := range v {
					if x.col != nil {
						x.x = x.col.x + x.shift
					}
				}
			}
		}
	}
}

// barlineKind is the barline before layout bar i (the one closing bar
// i-1).
type barlineKind uint8

const (
	barSingle barlineKind = iota
	barDouble
	barFinal
)

func (e *engraver) barlineBefore(i int) barlineKind {
	if i >= len(e.bars) {
		if e.last {
			return barFinal
		}
		return barSingle
	}
	if i > 0 && e.bars[i].showKey {
		return barDouble
	}
	return barSingle
}

func barlineWidth(d glyph.Defaults, k barlineKind) float64 {
	switch k {
	case barDouble:
		return 2*d.ThinBarlineThickness + d.BarlineSeparation
	case barFinal:
		return d.ThinBarlineThickness + d.ThinThickBarlineSeparation + d.ThickBarlineThickness
	}
	return d.ThinBarlineThickness
}

// finish stacks the staves, adds what spans them, fills the boxes and
// moves the layout inside its margin.
func (e *engraver) finish() *List {
	l := &List{}
	e.list = l
	// Stack the staves by their ink.
	y := 0.0
	for i, sc := range e.staves {
		sc.ext = staffExtent(e.f, sc.items)
		if i == 0 {
			sc.top = 0
		} else {
			p := e.staves[i-1]
			gap := staffGap
			if !sc.first {
				gap = grandGap
			}
			below := math.Max(0, p.ext.Max.Y-4)
			above := math.Max(0, -sc.ext.Min.Y)
			sc.top = p.top + 4 + math.Max(gap, below+above+inkPad)
		}
		y = sc.top
	}
	_ = y
	for _, sc := range e.staves {
		for _, it := range sc.items {
			l.Items = append(l.Items, offsetItem(it, Point{X: 0, Y: sc.top}))
		}
	}
	e.drawSpanning()
	l.Items = append(l.Items, e.items...)

	for _, sc := range e.staves {
		l.Staves = append(l.Staves, StaffBox{Line: sc.line, Staff: sc.staff, Top: sc.top, Clef: sc.clef, Bottom: sc.bottom,
			Fifths: sc.key(e.bars[0].fifths), Transposed: sc.transposed, Extent: sc.ext.Offset(Point{X: 0, Y: sc.top})})
	}
	for _, bc := range e.bars {
		bb := BarBox{Bar: bc.bar, X0: bc.x0, X1: bc.x1, Content: bc.content}
		for _, c := range bc.cols {
			bb.Columns = append(bb.Columns, Column{Offset: c.offF, X: c.x})
		}
		l.Bars = append(l.Bars, bb)
	}
	for _, sc := range e.staves {
		for _, sb := range sc.bars {
			for _, v := range sb.voices {
				for _, x := range v {
					if x.space || x.col == nil {
						continue
					}
					l.Events = append(l.Events, EventBox{Ref: x.ref, X: x.x, Box: x.box.Offset(Point{X: 0, Y: sc.top}), Offset: x.onset.Float()})
				}
			}
		}
	}
	b := l.Bounds()
	// The staves themselves count even where nothing is drawn beyond them.
	b = b.Union(Rect{Min: Point{X: 0, Y: 0}, Max: Point{X: e.bars[len(e.bars)-1].x1, Y: e.staves[len(e.staves)-1].top + 4}})
	l.Translate(Point{X: margin - b.Min.X, Y: margin - b.Min.Y})
	l.Width, l.Height = b.W()+2*margin, b.H()+2*margin
	return l
}

// staffExtent is a staff's ink, never less than its five lines.
func staffExtent(f *glyph.Font, items []Item) Rect {
	r := Rect{Min: Point{X: 0, Y: 0}, Max: Point{X: 0, Y: 4}}
	for i := range items {
		b := itemBounds(f, &items[i])
		r.Min.Y, r.Max.Y = math.Min(r.Min.Y, b.Min.Y), math.Max(r.Max.Y, b.Max.Y)
		r.Min.X, r.Max.X = math.Min(r.Min.X, b.Min.X), math.Max(r.Max.X, b.Max.X)
	}
	return r
}

func offsetItem(it Item, d Point) Item {
	it.P, it.Q = it.P.Add(d), it.Q.Add(d)
	if it.Path != nil {
		p := make([]glyph.Segment, len(it.Path))
		for i, s := range it.Path {
			for k := range s.P {
				s.P[k] = s.P[k].Add(d)
			}
			p[i] = s
		}
		it.Path = p
	}
	return it
}
