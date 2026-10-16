package edit

// marks.go — the marks on events: dynamics, articulations, ornaments,
// tremolos, fermatas, technique and expression text, and the spanners
// drawn from one event to another (slurs, hairpins, text crescendos,
// pedal and ottava lines).
//
// A mark is a name and a value on one event (score.Mark, the vocabulary in
// score/marks.go). Some marks take one another's place — a staccato and a
// staccatissimo, an accent and a marcato, one ornament over a note, one
// tremolo, one plain dynamic — so setting one clears the others of its
// group. A spanner is two marks in one voice, its start on the first event
// and its end on the last (inclusive), read in the voice's order the way
// the interpreter and the engraver read them; spanners of one kind do not
// overlap in a voice, so a new one takes the place of any it touches.

import (
	"fmt"
	"strings"

	"foundry/internal/score"
)

// markGroups are marks that stand in one another's place on an event.
var markGroups = [][]string{
	{"stacc", "staccmo", "portato"},
	{"acc", "marc"},
	{"tr", "mord", "imord", "turn", "iturn"},
	{"trem", "trem2"},
}

// groupOf is the marks that replace a mark, itself included (nil for one
// that stands alone).
func groupOf(name string) []string {
	for _, g := range markGroups {
		for _, n := range g {
			if n == name {
				return g
			}
		}
	}
	return nil
}

// markable is the event at l when marks can go on it: a note, a chord or
// a rest (a fermata over a rest, a dynamic at one).
func markable(s *score.Score, l Loc) (*score.Event, error) {
	e := Event(s, l)
	if e == nil {
		return nil, ErrNoEvent
	}
	if e.Kind == score.EventSpace {
		return nil, fmt.Errorf("nothing is written there")
	}
	return e, nil
}

// removeMarks takes every mark of the names off an event and reports
// whether one was there.
func removeMarks(e *score.Event, names ...string) bool {
	found := false
	out := e.Marks[:0:0]
	for _, m := range e.Marks {
		drop := false
		for _, n := range names {
			if m.Name == n {
				drop = true
			}
		}
		if drop {
			found = true
			continue
		}
		out = append(out, m)
	}
	e.Marks = out
	return found
}

// markValue is an event's mark of a name, if it has one.
func markValue(e *score.Event, name string) (string, bool) {
	for _, m := range e.Marks {
		if m.Name == name {
			return m.Value, true
		}
	}
	return "", false
}

// SetMark puts a mark on an event in place of any of the same name (and of
// its group: an accent replaces a marcato).
func SetMark(s *score.Score, l Loc, name, value string) error {
	e, err := markable(s, l)
	if err != nil {
		return err
	}
	if g := groupOf(name); g != nil {
		removeMarks(e, g...)
	} else {
		removeMarks(e, name)
	}
	e.Marks = append(e.Marks, score.Mark{Name: name, Value: value})
	return nil
}

// RemoveMark takes a mark off an event and reports whether it was there.
func RemoveMark(s *score.Score, l Loc, name string) (bool, error) {
	e := Event(s, l)
	if e == nil {
		return false, ErrNoEvent
	}
	return removeMarks(e, name), nil
}

// ToggleMark puts a mark on an event, or takes it off when the event has it
// with that value already; on reports which. Marks of its group go either
// way.
func ToggleMark(s *score.Score, l Loc, name, value string) (on bool, err error) {
	e, err := markable(s, l)
	if err != nil {
		return false, err
	}
	if v, ok := markValue(e, name); ok && v == value {
		removeMarks(e, name)
		return false, nil
	}
	return true, SetMark(s, l, name, value)
}

// noteOnly refuses a mark that a rest cannot carry.
func noteOnly(s *score.Score, l Loc, what string) error {
	e, err := markable(s, l)
	if err != nil {
		return err
	}
	if e.Kind != score.EventChord {
		return fmt.Errorf("%s goes on a note", what)
	}
	return nil
}

// ToggleArticulation toggles an articulation or ornament on a note: stacc,
// staccmo, portato, ten, acc, marc, legato, tr, mord, imord, turn, iturn,
// trem, trem2, arp, gliss.
func ToggleArticulation(s *score.Score, l Loc, name, value string) (bool, error) {
	if err := noteOnly(s, l, "an articulation"); err != nil {
		return false, err
	}
	return ToggleMark(s, l, name, value)
}

// isDynamic reports a plain or accent dynamic's name.
func isDynamic(name string) bool { _, ok := score.Dynamics[name]; return ok }

// SetDynamic puts a dynamic on an event in place of any it has; "" takes
// them off.
func SetDynamic(s *score.Score, l Loc, name string) error {
	e, err := markable(s, l)
	if err != nil {
		return err
	}
	if name != "" && !isDynamic(name) {
		return fmt.Errorf("%q is not a dynamic", name)
	}
	var keep []score.Mark
	for _, m := range e.Marks {
		if !isDynamic(m.Name) {
			keep = append(keep, m)
		}
	}
	e.Marks = keep
	if name != "" {
		e.Marks = append(e.Marks, score.Mark{Name: name})
	}
	return nil
}

// Dynamic is an event's dynamic, or "".
func Dynamic(e *score.Event) string {
	for _, m := range e.Marks {
		if isDynamic(m.Name) {
			return m.Name
		}
	}
	return ""
}

// SetText puts a text mark on an event — "expr" (expression, under the
// staff: "dolce"), "tech" (playing technique, which playback reads:
// "pizz."), "text" (staff text) — in place of one of that kind; "" takes
// it off.
func SetText(s *score.Score, l Loc, kind, text string) error {
	switch kind {
	case "expr", "tech", "text":
	default:
		return fmt.Errorf("no text kind %q", kind)
	}
	e, err := markable(s, l)
	if err != nil {
		return err
	}
	removeMarks(e, kind)
	if t := strings.TrimSpace(text); t != "" {
		e.Marks = append(e.Marks, score.Mark{Name: kind, Value: t})
	}
	return nil
}

// TechniqueWords are the playing-technique words the expression box files
// as technique (what playback switches articulations by), the rest being
// expression.
var TechniqueWords = []string{
	"pizz.", "arco", "con sord.", "senza sord.", "sord.", "trem.", "non trem.", "ord.", "nat.", "norm.",
	"sul pont.", "sul tasto", "col legno", "spicc.", "flz.", "harm.", "mute", "muted", "open", "l.v.",
	"pizzicato", "con sordino", "senza sordino", "tremolo",
}

// Expression is what a typed expression text is.
type Expression uint8

const (
	// ExprText is expression text: "dolce", "espress.".
	ExprText Expression = iota
	// ExprDynamic is a dynamic: "mf", "sfz".
	ExprDynamic
	// ExprTechnique is technique text: "pizz.", "con sord.".
	ExprTechnique
	// ExprCresc and ExprDim are "cresc." and "dim." with dashes.
	ExprCresc
	ExprDim
	// ExprTempo is tempo text: "rit.", "accel.", "a tempo".
	ExprTempo
)

// ClassifyExpression says what a text typed into the expression box is,
// and its canonical spelling.
func ClassifyExpression(text string) (Expression, string) {
	t := strings.TrimSpace(text)
	low := strings.ToLower(t)
	if isDynamic(low) {
		return ExprDynamic, low
	}
	for _, w := range TechniqueWords {
		if low == w || low+"." == w {
			return ExprTechnique, w
		}
	}
	switch low {
	case "cresc", "cresc.", "crescendo", "<":
		return ExprCresc, "cresc."
	case "dim", "dim.", "diminuendo", "decresc", "decresc.", "decrescendo", ">":
		return ExprDim, "dim."
	}
	// Words around one ("poco cresc.", "dim. molto", "cresc. poco a
	// poco") are the same line with those words printed.
	for _, w := range strings.FieldsFunc(low, func(r rune) bool { return r == ' ' || r == ',' }) {
		switch strings.TrimSuffix(w, ".") {
		case "cresc", "crescendo":
			return ExprCresc, t
		case "dim", "diminuendo", "decresc", "decrescendo":
			return ExprDim, t
		}
	}
	if (score.TempoMark{Text: t}).Change() != score.TempoSet {
		return ExprTempo, t
	}
	return ExprText, t
}

// ---------- spanners ----------

// SpanKind is a kind of spanner.
type SpanKind uint8

const (
	// SpanSlur is a slur: slur … slur_end.
	SpanSlur SpanKind = iota
	// SpanHairpin is a hairpin or a text crescendo: cresc or dim (the
	// value "text" or words print as text with dashes) … hairpin_end.
	SpanHairpin
	// SpanPedal is a pedal line: ped … ped_end.
	SpanPedal
	// SpanOttava is an ottava line: 8va, 8vb, 15ma or 15mb … ottava_end.
	SpanOttava
)

// spanEnd is a kind's end mark, and whether a mark starts one.
func spanEnd(k SpanKind) string {
	return [...]string{"slur_end", "hairpin_end", "ped_end", "ottava_end"}[k]
}

func spanStarts(k SpanKind, name string) bool {
	switch k {
	case SpanSlur:
		return name == "slur"
	case SpanHairpin:
		return name == "cresc" || name == "dim"
	case SpanPedal:
		return name == "ped"
	case SpanOttava:
		_, ok := score.OttavaShifts[name]
		return ok
	}
	return false
}

// seqOf is a voice's events in order, grace notes included.
func seqOf(s *score.Score, line, staff, voice int) []Loc {
	v := Voice(s, line, staff, voice)
	if v == nil {
		return nil
	}
	var out []Loc
	for bi, m := range v.Measures {
		for ei := range m.Events {
			out = append(out, Loc{Line: line, Staff: staff, Voice: voice, Bar: bi + 1, Event: ei})
		}
	}
	return out
}

func indexOf(seq []Loc, l Loc) int {
	for i, x := range seq {
		if x == l {
			return i
		}
	}
	return -1
}

// spanRun is one spanner found in a voice: the events of its start and
// end marks (end -1 for one left open).
type spanRun struct{ start, end int }

// spansOf are a voice's spanners of a kind, in order.
func spansOf(s *score.Score, seq []Loc, k SpanKind) []spanRun {
	var out []spanRun
	open := -1
	for i, l := range seq {
		e := Event(s, l)
		starts, ends := false, false
		for _, m := range e.Marks {
			if spanStarts(k, m.Name) {
				starts = true
			}
			if m.Name == spanEnd(k) {
				ends = true
			}
		}
		// A slur's start inside a slur, and a pedal's (a re-pedal) inside
		// a pedal line, start nothing new; a hairpin's or an ottava's ends
		// the one before there.
		if starts && (open < 0 || k == SpanHairpin || k == SpanOttava) {
			if open >= 0 {
				out = append(out, spanRun{open, i})
			}
			open = i
		}
		if ends && open >= 0 {
			out = append(out, spanRun{open, i})
			open = -1
		}
	}
	if open >= 0 {
		out = append(out, spanRun{open, -1})
	}
	return out
}

// startName is the mark a span of a kind starts with.
func startName(k SpanKind, value string) (name, val string, err error) {
	switch k {
	case SpanSlur:
		return "slur", "", nil
	case SpanHairpin:
		switch value {
		case "cresc", "dim":
			return value, "", nil
		case "cresc.", "cresc=text":
			return "cresc", "text", nil
		case "dim.", "dim=text":
			return "dim", "text", nil
		}
		// cresc=<words> / dim=<words>: a text line with those words.
		for _, n := range []string{"cresc", "dim"} {
			if w, ok := strings.CutPrefix(value, n+"="); ok && strings.TrimSpace(w) != "" {
				return n, strings.TrimSpace(w), nil
			}
		}
		return "", "", fmt.Errorf("a hairpin is cresc or dim (or cresc. / dim. as text), not %q", value)
	case SpanPedal:
		return "ped", "", nil
	case SpanOttava:
		if value == "" {
			value = "8va"
		}
		if _, ok := score.OttavaShifts[value]; !ok {
			return "", "", fmt.Errorf("an ottava is 8va, 8vb, 15ma or 15mb, not %q", value)
		}
		return value, "", nil
	}
	return "", "", fmt.Errorf("no spanner kind %d", k)
}

// clearSpanMarks takes a kind's start and end marks off an event.
func clearSpanMarks(e *score.Event, k SpanKind) {
	var keep []score.Mark
	for _, m := range e.Marks {
		if spanStarts(k, m.Name) || m.Name == spanEnd(k) {
			continue
		}
		keep = append(keep, m)
	}
	e.Marks = keep
}

// AddSpan draws a spanner from one event to a later one of the same voice
// (both included). value picks a hairpin's kind ("cresc", "dim", or
// "cresc." / "dim." for text with dashes) or an ottava's ("8va", "8vb",
// "15ma", "15mb"). Spanners of the kind it touches go: in a voice they do
// not overlap.
func AddSpan(s *score.Score, from, to Loc, k SpanKind, value string) error {
	if from.Line != to.Line || from.Staff != to.Staff || from.Voice != to.Voice {
		return fmt.Errorf("a line runs within one voice")
	}
	name, val, err := startName(k, value)
	if err != nil {
		return err
	}
	seq := seqOf(s, from.Line, from.Staff, from.Voice)
	a, z := indexOf(seq, from), indexOf(seq, to)
	if a < 0 || z < 0 {
		return ErrNoEvent
	}
	if z < a {
		a, z = z, a
	}
	if a == z && k != SpanOttava && k != SpanPedal {
		return fmt.Errorf("a %s runs from one note to another: select two notes or a passage", [...]string{"slur", "hairpin", "pedal line", "ottava line"}[k])
	}
	if k == SpanSlur {
		for _, i := range []int{a, z} {
			if Event(s, seq[i]).Kind != score.EventChord {
				return fmt.Errorf("a slur starts and ends on notes")
			}
		}
	}
	// What it touches goes.
	for _, sp := range spansOf(s, seq, k) {
		end := sp.end
		if end < 0 {
			end = len(seq) - 1
		}
		if sp.start <= z && end >= a {
			clearSpanMarks(Event(s, seq[sp.start]), k)
			if sp.end >= 0 {
				clearSpanMarks(Event(s, seq[sp.end]), k)
			}
		}
	}
	for i := a; i <= z; i++ {
		if i > a && i < z {
			clearSpanMarks(Event(s, seq[i]), k)
		}
	}
	fe, te := Event(s, seq[a]), Event(s, seq[z])
	fe.Marks = append(fe.Marks, score.Mark{Name: name, Value: val})
	te.Marks = append(te.Marks, score.Mark{Name: spanEnd(k)})
	return nil
}

// SpanAt is the spanner of a kind over an event: where it starts and ends
// (an open one ends at the voice's last event).
func SpanAt(s *score.Score, l Loc, k SpanKind) (from, to Loc, ok bool) {
	seq := seqOf(s, l.Line, l.Staff, l.Voice)
	i := indexOf(seq, l)
	if i < 0 {
		return from, to, false
	}
	for _, sp := range spansOf(s, seq, k) {
		end := sp.end
		if end < 0 {
			end = len(seq) - 1
		}
		if sp.start <= i && i <= end {
			return seq[sp.start], seq[end], true
		}
	}
	return from, to, false
}

// RemoveSpan takes the spanner of a kind over an event out, and reports
// whether there was one.
func RemoveSpan(s *score.Score, l Loc, k SpanKind) bool {
	from, to, ok := SpanAt(s, l, k)
	if !ok {
		return false
	}
	clearSpanMarks(Event(s, from), k)
	if to != from {
		e := Event(s, to)
		var keep []score.Mark
		for _, m := range e.Marks {
			if m.Name != spanEnd(k) {
				keep = append(keep, m)
			}
		}
		e.Marks = keep
	}
	return true
}

// NextNote is the voice's next note or chord after an event (across bars,
// grace notes skipped).
func NextNote(s *score.Score, l Loc) (Loc, bool) {
	seq := seqOf(s, l.Line, l.Staff, l.Voice)
	for i := indexOf(seq, l) + 1; i > 0 && i < len(seq); i++ {
		e := Event(s, seq[i])
		if e.Grace == score.NotGrace && e.Kind == score.EventChord {
			return seq[i], true
		}
	}
	return l, false
}

// ---------- grace notes ----------

// InsertGrace puts a grace note (or chord) before the event at l, after
// the grace notes already there, and returns where it went. The event at
// l is the note it leans on (a rest too: a note written over the rest
// later keeps the grace notes before it).
func InsertGrace(s *score.Score, l Loc, g score.Event) (Loc, error) {
	if g.Grace == score.NotGrace {
		return l, fmt.Errorf("that is not a grace note")
	}
	if g.Kind != score.EventChord || len(g.Notes) == 0 {
		return l, fmt.Errorf("a grace note is a note")
	}
	e := Event(s, l)
	if e == nil {
		return l, ErrNoEvent
	}
	if e.Grace != score.NotGrace {
		return l, fmt.Errorf("grace notes go before a note, not another grace note")
	}
	if e.Kind == score.EventBarRest {
		return l, fmt.Errorf("a grace note leans on a note: write one there first")
	}
	v := Voice(s, l.Line, l.Staff, l.Voice)
	m := &v.Measures[l.Bar-1]
	g.Tuplet = e.Tuplet
	evs := append([]score.Event(nil), m.Events[:l.Event]...)
	evs = append(evs, g)
	evs = append(evs, m.Events[l.Event:]...)
	m.Events = evs
	return l, nil
}

// SetGrace turns a grace note into the other kind (an acciaccatura into an
// appoggiatura).
func SetGrace(s *score.Score, l Loc, kind score.Grace) error {
	e := Event(s, l)
	if e == nil {
		return ErrNoEvent
	}
	if e.Grace == score.NotGrace || kind == score.NotGrace {
		return fmt.Errorf("only a grace note changes its kind")
	}
	e.Grace = kind
	return nil
}

// ---------- voices ----------

// MoveToVoice moves an event into another voice of its staff, at the same
// onset: written there over what that voice has, and where it was a rest
// (in voice 1, which must show the bar) or nothing (voices 2–4). Its marks
// go with it.
func MoveToVoice(s *score.Score, l Loc, voice int) (Loc, error) {
	e := Event(s, l)
	if e == nil {
		return l, ErrNoEvent
	}
	if voice == l.Voice {
		return l, fmt.Errorf("that is voice %d already", voice+1)
	}
	if voice < 0 || voice >= score.MaxVoices {
		return l, fmt.Errorf("a staff has voices 1 to %d", score.MaxVoices)
	}
	switch {
	case e.Grace != score.NotGrace:
		return l, fmt.Errorf("a grace note moves with its note")
	case e.Kind != score.EventChord && e.Kind != score.EventRest:
		return l, fmt.Errorf("only notes and rests move between voices")
	case e.Tuplet != 0:
		return l, fmt.Errorf("a tuplet's note moves with its whole tuplet: not one note of it")
	}
	moved := e.Clone()
	on, _ := Onset(s, l)
	// The graces before it go with it.
	v := Voice(s, l.Line, l.Staff, l.Voice)
	m := &v.Measures[l.Bar-1]
	first := l.Event
	for first > 0 && m.Events[first-1].Grace != score.NotGrace {
		first--
	}
	graces := cloneAll(m.Events[first:l.Event])
	m.Events = append(m.Events[:first:first], m.Events[l.Event:]...)
	l.Event = first
	left := &m.Events[l.Event]
	left.Notes, left.Marks = nil, nil
	left.Kind = score.EventRest
	if l.Voice > 0 {
		left.Kind = score.EventSpace
	}
	to, err := Write(s, l.Line, l.Staff, voice, l.Bar, on, moved)
	if err != nil {
		return l, err
	}
	if len(graces) > 0 {
		tm := &Voice(s, l.Line, l.Staff, voice).Measures[l.Bar-1]
		evs := append([]score.Event(nil), tm.Events[:to.Event]...)
		evs = append(evs, graces...)
		tm.Events = append(evs, tm.Events[to.Event:]...)
		to.Event += len(graces)
	}
	return to, nil
}
