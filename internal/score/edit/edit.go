// Package edit is the Score editor's operations on music, with no window in
// them: writing a note or a rest into a bar and reflowing the bar around it,
// chords and diatonic intervals spelled in the key, accidentals, semitone,
// step and octave moves, respelling, durations and dots, ties, tuplets,
// rests, and whole bars copied, pasted, repeated and cleared, with key, time
// signature and clef changes.
//
// Everything works on a *score.Score in place, in exact time (score.Frac),
// and keeps every bar it touches full: the editor's model is the one a
// notation program has, where a bar always adds up and writing a note over
// the music already there truncates what it lands on and fills what is left
// with rests (the browser prototype's writeAt, docs/score/mockups). An
// operation that cannot be done says why in its error and changes nothing.
//
// The editor (internal/editor/editor_score*.go) decides which of these a key
// or a click means; this package is what they do, which is what lets the
// tests drive every edit with no window, and what a MIDI step-entry mode or
// the Claude panel can reuse.
package edit

import (
	"errors"
	"fmt"

	"foundry/internal/score"
	"foundry/internal/score/io/notate"
)

// Loc addresses one event: a line, a staff of it and a voice of that
// (0-based), a bar (1-based, as the score counts) and an index into the
// bar's Events.
type Loc struct {
	Line, Staff, Voice int
	Bar                int
	Event              int
}

// ErrNoEvent is an address with nothing at it.
var ErrNoEvent = errors.New("no such event")

// Staff returns a staff, or nil.
func Staff(s *score.Score, line, staff int) *score.Staff {
	if line < 0 || line >= len(s.Lines) {
		return nil
	}
	l := s.Lines[line]
	if staff < 0 || staff >= len(l.Staves) {
		return nil
	}
	return l.Staves[staff]
}

// Voice returns a voice, or nil.
func Voice(s *score.Score, line, staff, voice int) *score.Voice {
	st := Staff(s, line, staff)
	if st == nil || voice < 0 || voice >= len(st.Voices) {
		return nil
	}
	return st.Voices[voice]
}

// EnsureVoice returns a voice, adding empty voices to the staff up to it.
func EnsureVoice(s *score.Score, line, staff, voice int) (*score.Voice, error) {
	st := Staff(s, line, staff)
	if st == nil {
		return nil, fmt.Errorf("no staff %d of line %d", staff+1, line+1)
	}
	if voice < 0 || voice >= score.MaxVoices {
		return nil, fmt.Errorf("a staff has voices 1 to %d", score.MaxVoices)
	}
	for len(st.Voices) <= voice {
		st.Voices = append(st.Voices, &score.Voice{})
	}
	return st.Voices[voice], nil
}

// EnsureBars grows the score to at least n bars.
func EnsureBars(s *score.Score, n int) {
	if n > s.Bars {
		s.Bars = min(n, score.MaxBars)
	}
}

// measureOf returns a voice's bar, extending the voice with empty bars to
// reach it, and grows the score's bar count to hold it.
func measureOf(s *score.Score, v *score.Voice, bar int) *score.Measure {
	for len(v.Measures) < bar {
		v.Measures = append(v.Measures, score.Measure{})
	}
	EnsureBars(s, bar)
	return &v.Measures[bar-1]
}

// Event returns the event at l, or nil.
func Event(s *score.Score, l Loc) *score.Event {
	v := Voice(s, l.Line, l.Staff, l.Voice)
	if v == nil || l.Bar < 1 || l.Bar > len(v.Measures) {
		return nil
	}
	m := &v.Measures[l.Bar-1]
	if l.Event < 0 || l.Event >= len(m.Events) {
		return nil
	}
	return &m.Events[l.Event]
}

// Onset is an event's start in its bar and End where it stops (the same
// for a grace note).
func Onset(s *score.Score, l Loc) (on, end score.Frac) {
	v := Voice(s, l.Line, l.Staff, l.Voice)
	if v == nil {
		return
	}
	m := v.Measure(l.Bar)
	if l.Event < 0 || l.Event >= len(m.Events) {
		return
	}
	offs, _ := m.Offsets(s.BarLength(l.Bar))
	on = offs[l.Event]
	return on, on.Add(m.Actual(m.Events[l.Event], s.BarLength(l.Bar)))
}

// ---------- a bar's time ----------

// span is the time a tuplet covers in its bar.
type span struct {
	start, end score.Frac
	set        bool
}

// layout is a measure with every event's time worked out.
type layout struct {
	m      score.Measure
	barLen score.Frac
	on     []score.Frac
	dur    []score.Frac // actual length; zero for a grace
	filled score.Frac
	spans  []span // per tuplet, index t-1
}

func lay(m score.Measure, barLen score.Frac) layout {
	L := layout{m: m, barLen: barLen, spans: make([]span, len(m.Tuplets))}
	L.on, L.filled = m.Offsets(barLen)
	L.dur = make([]score.Frac, len(m.Events))
	for i, e := range m.Events {
		L.dur[i] = m.Actual(e, barLen)
		if e.Grace != score.NotGrace {
			continue
		}
		for _, t := range chain(m.Tuplets, e.Tuplet) {
			sp := &L.spans[t-1]
			a, z := L.on[i], L.on[i].Add(L.dur[i])
			if !sp.set {
				*sp = span{a, z, true}
				continue
			}
			sp.start, sp.end = score.MinFrac(sp.start, a), score.MaxFrac(sp.end, z)
		}
	}
	return L
}

// chain is the tuplets t is in, outermost first, t last; nil for 0.
func chain(ts []score.Tuplet, t int) []int {
	var out []int
	for t > 0 && t <= len(ts) && len(out) <= len(ts) {
		out = append(out, t)
		t = ts[t-1].Parent
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// within reports a tuplet that is t or inside it.
func within(ts []score.Tuplet, inner, t int) bool {
	if t == 0 {
		return true
	}
	for _, c := range chain(ts, inner) {
		if c == t {
			return true
		}
	}
	return false
}

// ratio is what the tuplets of a context scale a written value by.
func ratio(ts []score.Tuplet, ctx int) score.Frac {
	r := score.Whole(1)
	for _, t := range chain(ts, ctx) {
		r = r.Mul(ts[t-1].Ratio())
	}
	return r
}

// contextAt is the tuplet a point in the bar is inside — the innermost
// tuplet of the event sounding there — or 0.
func (L layout) contextAt(at score.Frac) int {
	for i, e := range L.m.Events {
		if e.Grace != score.NotGrace {
			continue
		}
		if !at.Less(L.on[i]) && at.Less(L.on[i].Add(L.dur[i])) {
			return e.Tuplet
		}
	}
	return 0
}

// limit is where a context ends: its tuplet's end, or the bar's.
func (L layout) limit(ctx int) score.Frac {
	if ctx > 0 && ctx <= len(L.spans) && L.spans[ctx-1].set {
		return L.spans[ctx-1].end
	}
	return L.barLen
}

// ---------- pieces of music ----------

// piece is a run of events with tuplets of its own: an event's Tuplet and a
// tuplet's Parent index the piece's tuplets 1-based, 0 meaning the context
// the piece is put into.
type piece struct {
	events  []score.Event
	tuplets []score.Tuplet
}

// cat joins pieces, renumbering each one's tuplets after the ones before.
func cat(ps ...piece) piece {
	var out piece
	for _, p := range ps {
		base := len(out.tuplets)
		for _, t := range p.tuplets {
			if t.Parent > 0 {
				t.Parent += base
			}
			out.tuplets = append(out.tuplets, t)
		}
		for _, e := range p.events {
			if e.Tuplet > 0 {
				e.Tuplet += base
			}
			out.events = append(out.events, e)
		}
	}
	return out
}

// bar is the measure being edited and what its time is.
type bar struct {
	barLen score.Frac
	meter  score.Meter
}

// silence is length of rests (or spaces) at a point in a context: in the
// bar itself split by the meter, the way an importer writes a gap
// (notate.Fill); inside a tuplet in the largest values that fit.
func (b bar) silence(ts []score.Tuplet, ctx int, at, length score.Frac, kind score.EventKind) (piece, error) {
	if length.Sign() <= 0 {
		return piece{}, nil
	}
	if ctx == 0 {
		var m score.Measure
		if err := notate.Fill(&m, kind, at, length, b.meter, b.barLen); err != nil {
			return piece{}, err
		}
		return piece{events: m.Events, tuplets: m.Tuplets}, nil
	}
	w := length.Div(ratio(ts, ctx))
	var p piece
	for base := score.Breve; base <= score.D256 && w.Sign() > 0; base++ {
		v := score.Duration{Base: base}.Value()
		for !w.Less(v) {
			p.events = append(p.events, score.Event{Kind: kind, Dur: score.Duration{Base: base}})
			w = w.Sub(v)
		}
	}
	if w.Sign() != 0 {
		return piece{}, fmt.Errorf("%s cannot be written as rests in the tuplet", length)
	}
	return p, nil
}

// fitDur is the longest value, up to two dots, no longer than w.
func fitDur(w score.Frac) (score.Duration, bool) {
	best, ok := score.Duration{}, false
	for base := score.Breve; base <= score.D256; base++ {
		for dots := uint8(0); dots <= 2; dots++ {
			d := score.Duration{Base: base, Dots: dots}
			v := d.Value()
			if w.Less(v) {
				continue
			}
			if !ok || best.Value().Less(v) {
				best, ok = d, true
			}
		}
	}
	return best, ok
}

// truncated is an event cut to length at a point in its context: a chord
// keeps the longest value that fits and rests make up the rest; a rest or
// space becomes rests or spaces.
func (b bar) truncated(ts []score.Tuplet, ctx int, e score.Event, at, length score.Frac) (piece, error) {
	kind := e.Kind
	if kind == score.EventChord {
		r := ratio(ts, ctx)
		if d, ok := fitDur(length.Div(r)); ok {
			e.Dur = d
			e.Tuplet = 0
			used := d.Value().Mul(r)
			rest, err := b.silence(ts, ctx, at.Add(used), length.Sub(used), score.EventRest)
			if err != nil {
				return piece{}, err
			}
			return cat(piece{events: []score.Event{e}}, rest), nil
		}
		kind = score.EventRest
	}
	if kind != score.EventSpace {
		kind = score.EventRest
	}
	return b.silence(ts, ctx, at, length, kind)
}

// ---------- the bar as it is edited ----------

// normalized is a measure ready to be edited: an empty bar or a bar rest is
// a bar of rests, and a short bar is filled out with rests to its end.
func (b bar) normalized(m score.Measure) (score.Measure, error) {
	main := 0
	for _, e := range m.Events {
		if e.Grace == score.NotGrace {
			main++
		}
	}
	if main == 0 || (main == 1 && len(m.Events) == 1 && m.Events[0].Kind == score.EventBarRest) {
		p, err := b.silence(nil, 0, score.Frac{}, b.barLen, score.EventRest)
		if err != nil {
			return m, err
		}
		// Grace notes alone in a bar stay at its start.
		var graces []score.Event
		for _, e := range m.Events {
			if e.Grace != score.NotGrace {
				graces = append(graces, e)
			}
		}
		p.events = append(graces, p.events...)
		return compact(p.events, p.tuplets), nil
	}
	_, filled := m.Offsets(b.barLen)
	if filled.Less(b.barLen) {
		p, err := b.silence(nil, 0, filled, b.barLen.Sub(filled), score.EventRest)
		if err != nil {
			return m, err
		}
		all := cat(piece{events: m.Clone().Events, tuplets: m.Tuplets}, p)
		return compact(all.events, all.tuplets), nil
	}
	return m.Clone(), nil
}

// replace rewrites the time [a, z) of a measure, inside tuplet ctx (0: the
// bar), with seq — music that fills exactly that time in that context. An
// event the region cuts is truncated (before it) or cut to rests (after
// it); a tuplet the region cuts into but does not hold is taken out whole,
// its time rests, since half a tuplet is not music. It returns the new
// measure and the index of seq's first event in it (-1 for an empty seq).
func (b bar) replace(m score.Measure, ctx int, a, z score.Frac, seq piece) (score.Measure, int, error) {
	L := lay(m, b.barLen)
	ts := m.Tuplets
	ea, ez := a, z
	for changed := true; changed; {
		changed = false
		for t := 1; t <= len(ts); t++ {
			sp := L.spans[t-1]
			if !sp.set || within(ts, ctx, t) {
				continue // ctx itself or a tuplet around it
			}
			if sp.start.Less(ez) && ea.Less(sp.end) && (sp.start.Less(ea) || ez.Less(sp.end)) {
				ea, ez = score.MinFrac(ea, sp.start), score.MaxFrac(ez, sp.end)
				changed = true
			}
		}
	}

	// The output is built against one tuplet table: the measure's own
	// first, then each new piece's after them.
	table := append([]score.Tuplet(nil), ts...)
	var out []score.Event
	first := -1
	put := func(p piece) {
		base := len(table)
		local := func(t int) int {
			if t == 0 {
				return ctx
			}
			return t + base
		}
		for _, t := range p.tuplets {
			t.Parent = local(t.Parent)
			table = append(table, t)
		}
		for _, e := range p.events {
			e.Tuplet = local(e.Tuplet)
			out = append(out, e)
		}
	}
	done := false
	var regionErr error
	region := func() {
		if done {
			return
		}
		done = true
		pre, err := b.silence(ts, ctx, ea, a.Sub(ea), score.EventRest)
		if err != nil {
			regionErr = err
			return
		}
		post, err := b.silence(ts, ctx, z, ez.Sub(z), score.EventRest)
		if err != nil {
			regionErr = err
			return
		}
		put(pre)
		if len(seq.events) > 0 {
			first = len(out)
		}
		put(seq)
		put(post)
	}

	ev := m.Events
	for i := 0; i < len(ev); {
		j := i
		for j < len(ev) && ev[j].Grace != score.NotGrace {
			j++
		}
		if j == len(ev) {
			// Grace notes after the last note, at the bar's end.
			if !L.filled.Less(ez) {
				region()
			}
			out = append(out, cloneAll(ev[i:j])...)
			break
		}
		on, end := L.on[j], L.on[j].Add(L.dur[j])
		switch {
		case !ea.Less(end):
			out = append(out, cloneAll(ev[i:j+1])...)
		case !on.Less(ez):
			region()
			out = append(out, cloneAll(ev[i:j+1])...)
		default:
			if on.Less(ea) {
				out = append(out, cloneAll(ev[i:j])...)
				pc, err := b.truncated(ts, ctx, ev[j].Clone(), on, ea.Sub(on))
				if err != nil {
					return m, -1, err
				}
				put(pc)
			}
			region()
			if ez.Less(end) {
				kind := score.EventRest
				if ev[j].Kind == score.EventSpace {
					kind = score.EventSpace
				}
				pc, err := b.silence(ts, ctx, ez, end.Sub(ez), kind)
				if err != nil {
					return m, -1, err
				}
				put(pc)
			}
		}
		i = j + 1
	}
	region()
	if regionErr != nil {
		return m, -1, regionErr
	}
	nm := compact(out, table)
	return nm, first, nil
}

func cloneAll(es []score.Event) []score.Event {
	out := make([]score.Event, len(es))
	for i, e := range es {
		out[i] = e.Clone()
	}
	return out
}

// compact keeps the tuplets the events use, numbered in the order they
// open (outermost first), as the grammar wants them.
func compact(events []score.Event, table []score.Tuplet) score.Measure {
	idx := map[int]int{}
	var ts []score.Tuplet
	for i := range events {
		for _, t := range chain(table, events[i].Tuplet) {
			if _, ok := idx[t]; ok {
				continue
			}
			tp := table[t-1]
			tp.Parent = idx[tp.Parent]
			ts = append(ts, tp)
			idx[t] = len(ts)
		}
		events[i].Tuplet = idx[events[i].Tuplet]
	}
	return score.Measure{Events: events, Tuplets: ts}
}

// barOf is the editing context for a bar of the score.
func barOf(s *score.Score, n int) bar {
	return bar{barLen: s.BarLength(n), meter: s.MeterAt(n)}
}
