package edit

// write.go — writing events into a bar, and changing an event's rhythm:
// its duration, rest or note, its ties, a tuplet started on it.

import (
	"fmt"

	"foundry/internal/score"
)

// Write puts an event into a voice's bar at an offset and returns where it
// went. The bar is reflowed around it: what the event lands on is
// truncated, what it covers goes, and whatever is left of the bar is rests.
// Written inside a tuplet the event joins it; an event longer than the
// room left in the bar (or the tuplet) is cut to the longest value that
// fits. The voice, the bar and the score's bar count grow as needed.
func Write(s *score.Score, line, staff, voice, bar int, at score.Frac, e score.Event) (Loc, error) {
	loc := Loc{Line: line, Staff: staff, Voice: voice, Bar: bar, Event: -1}
	if bar < 1 || bar > score.MaxBars {
		return loc, fmt.Errorf("no bar %d", bar)
	}
	if e.Grace != score.NotGrace || e.Kind == score.EventBarRest {
		return loc, fmt.Errorf("only notes, chords, rests and spaces are written into time")
	}
	v, err := EnsureVoice(s, line, staff, voice)
	if err != nil {
		return loc, err
	}
	b := barOf(s, bar)
	if at.Sign() < 0 || !at.Less(b.barLen) {
		return loc, fmt.Errorf("%s is outside bar %d", at, bar)
	}
	mp := measureOf(s, v, bar)
	m, err := b.normalized(*mp)
	if err != nil {
		return loc, err
	}
	L := lay(m, b.barLen)
	ctx := L.contextAt(at)
	r := ratio(m.Tuplets, ctx)
	room := L.limit(ctx).Sub(at)
	if room.Sign() <= 0 {
		return loc, fmt.Errorf("no room at %s in bar %d", at, bar)
	}
	e.Tuplet = 0
	seq := piece{events: []score.Event{e}}
	length := e.Dur.Value().Mul(r)
	if room.Less(length) {
		d, ok := fitDur(room.Div(r))
		if !ok {
			return loc, fmt.Errorf("no room at %s in bar %d", at, bar)
		}
		e.Dur = d
		used := d.Value().Mul(r)
		rest, err := b.silence(m.Tuplets, ctx, at.Add(used), room.Sub(used), score.EventRest)
		if err != nil {
			return loc, err
		}
		seq = cat(piece{events: []score.Event{e}}, rest)
		length = room
	}
	nm, idx, err := b.replace(m, ctx, at, at.Add(length), seq)
	if err != nil {
		return loc, err
	}
	*mp = nm
	loc.Event = idx
	return loc, nil
}

// WriteOver writes an event the way note entry does: like Write, except
// that a note longer than the room left in its bar carries on over the
// barline as tied notes (a rest as rests), into as many bars as it needs,
// the way Sibelius and MuseScore split a value at the barline. Each bar's
// part is the longest values that fit, tied (a 5/8 left is h~e). Written
// inside a tuplet, or where the next bar starts with a tuplet, the event is
// cut to fit as Write cuts it. It returns where the first and the last part
// went; the last keeps the event's own ties, the others tie on.
func WriteOver(s *score.Score, line, staff, voice, bar int, at score.Frac, e score.Event) (first, last Loc, err error) {
	first = Loc{Line: line, Staff: staff, Voice: voice, Bar: bar, Event: -1}
	if e.Grace != score.NotGrace || e.Kind == score.EventBarRest || bar < 1 || bar > score.MaxBars {
		l, err := Write(s, line, staff, voice, bar, at, e)
		return l, l, err
	}
	if _, err := EnsureVoice(s, line, staff, voice); err != nil {
		return first, first, err
	}
	b := barOf(s, bar)
	room := b.barLen.Sub(at)
	if at.Sign() < 0 || room.Sign() <= 0 {
		return first, first, fmt.Errorf("%s is outside bar %d", at, bar)
	}
	m, err := b.normalized(*measureOf(s, Voice(s, line, staff, voice), bar))
	if err != nil {
		return first, first, err
	}
	total := e.Dur.Value()
	if lay(m, b.barLen).contextAt(at) != 0 || !room.Less(total) {
		l, err := Write(s, line, staff, voice, bar, at, e)
		return l, l, err
	}
	// The parts: per bar, the longest values that fit what is left.
	type part struct {
		bar int
		at  score.Frac
		d   score.Duration
	}
	var parts []part
	left, cb, ca := total, bar, at
	for left.Sign() > 0 {
		if cb > score.MaxBars {
			break // the score ends: the note stops at its last barline
		}
		bl := s.BarLength(cb)
		if cb > bar {
			v := Voice(s, line, staff, voice)
			nm, err := barOf(s, cb).normalized(v.Measure(cb))
			if err != nil || lay(nm, bl).contextAt(score.Frac{}) != 0 {
				break // a tuplet at the bar's start: stop at the barline
			}
		}
		take := score.MinFrac(left, bl.Sub(ca))
		for take.Sign() > 0 {
			d, ok := fitDur(take)
			if !ok {
				return first, first, fmt.Errorf("%s cannot be written in bar %d", take, cb)
			}
			parts = append(parts, part{cb, ca, d})
			ca, take, left = ca.Add(d.Value()), take.Sub(d.Value()), left.Sub(d.Value())
		}
		cb, ca = cb+1, score.Frac{}
	}
	for i, p := range parts {
		pe := e.Clone()
		pe.Dur = p.d
		if i < len(parts)-1 && pe.Kind == score.EventChord {
			for k := range pe.Notes {
				pe.Notes[k].Tie = true
			}
		}
		l, err := Write(s, line, staff, voice, p.bar, p.at, pe)
		if err != nil {
			return first, last, err
		}
		if i == 0 {
			first = l
		}
		last = l
	}
	return first, last, nil
}

// SetDuration rewrites an event at another written value, reflowing the
// bar after it the way Write does. A grace note just takes the value.
func SetDuration(s *score.Score, l Loc, d score.Duration) (Loc, error) {
	e := Event(s, l)
	if e == nil {
		return l, ErrNoEvent
	}
	if e.Kind == score.EventBarRest {
		return l, fmt.Errorf("a bar rest fills its bar; write a note into it instead")
	}
	if e.Grace != score.NotGrace {
		e.Dur = d
		return l, nil
	}
	ne := e.Clone()
	ne.Dur = d
	on, _ := Onset(s, l)
	return Write(s, l.Line, l.Staff, l.Voice, l.Bar, on, ne)
}

// ToggleDots cycles an event's dots: none, one, two, none.
func ToggleDots(s *score.Score, l Loc) (Loc, error) {
	e := Event(s, l)
	if e == nil {
		return l, ErrNoEvent
	}
	d := e.Dur
	d.Dots = (d.Dots + 1) % 3
	return SetDuration(s, l, d)
}

// MakeRest turns a note or chord into a rest of the same value.
func MakeRest(s *score.Score, l Loc) error {
	e := Event(s, l)
	if e == nil {
		return ErrNoEvent
	}
	if e.Kind != score.EventChord {
		return nil
	}
	if e.Grace != score.NotGrace {
		return removeEvent(s, l)
	}
	e.Kind, e.Notes = score.EventRest, nil
	return nil
}

// Delete is the Delete key: one note of a chord goes; a note or chord
// becomes a rest; a rest in voices 2–4 becomes silence that prints nothing
// (in voice 1 it stays: the bar has to show something); a grace note is
// taken out.
func Delete(s *score.Score, l Loc, note int) error {
	e := Event(s, l)
	if e == nil {
		return ErrNoEvent
	}
	switch {
	case e.Grace != score.NotGrace:
		return removeEvent(s, l)
	case e.Kind == score.EventChord && note >= 0 && note < len(e.Notes) && len(e.Notes) > 1:
		e.Notes = append(e.Notes[:note:note], e.Notes[note+1:]...)
		return nil
	case e.Kind == score.EventChord:
		return MakeRest(s, l)
	case e.Kind == score.EventRest && l.Voice > 0:
		e.Kind = score.EventSpace
	}
	return nil
}

// removeEvent takes a grace note out of its bar.
func removeEvent(s *score.Score, l Loc) error {
	v := Voice(s, l.Line, l.Staff, l.Voice)
	m := &v.Measures[l.Bar-1]
	if m.Events[l.Event].Grace == score.NotGrace {
		return fmt.Errorf("only a grace note can be taken out of a bar")
	}
	m.Events = append(m.Events[:l.Event:l.Event], m.Events[l.Event+1:]...)
	*m = compact(m.Events, m.Tuplets)
	return nil
}

// ToggleTie ties a note (or every note of the chord, note < 0) into the next
// event, or unties it. A chord whose notes are all tied unties them all.
func ToggleTie(s *score.Score, l Loc, note int) error {
	e := Event(s, l)
	if e == nil {
		return ErrNoEvent
	}
	if e.Kind != score.EventChord {
		return fmt.Errorf("only notes are tied")
	}
	if note >= 0 && note < len(e.Notes) {
		e.Notes[note].Tie = !e.Notes[note].Tie
		return nil
	}
	all := true
	for _, n := range e.Notes {
		all = all && n.Tie
	}
	for i := range e.Notes {
		e.Notes[i].Tie = !all
	}
	return nil
}

// MakeTuplet starts an n-tuplet on an event: n notes of its value in the
// time of the usual number (3 in 2, 5 in 4 — score.DefaultTupletDen), the
// event first and rests after. Inside a tuplet the new one nests. It must
// fit in the bar (or the tuplet around it).
func MakeTuplet(s *score.Score, l Loc, n int) (Loc, error) {
	if n < 2 || n > 64 {
		return l, fmt.Errorf("tuplets have 2 to 64 notes")
	}
	e := Event(s, l)
	if e == nil {
		return l, ErrNoEvent
	}
	if e.Grace != score.NotGrace || e.Kind == score.EventBarRest {
		return l, fmt.Errorf("a tuplet starts on a note or rest")
	}
	v := Voice(s, l.Line, l.Staff, l.Voice)
	b := barOf(s, l.Bar)
	mp := &v.Measures[l.Bar-1]
	m, err := b.normalized(*mp)
	if err != nil {
		return l, err
	}
	// Normalizing only appends, so the event keeps its index.
	L := lay(m, b.barLen)
	at := L.on[l.Event]
	ctx := m.Events[l.Event].Tuplet
	den := score.DefaultTupletDen(n)
	unit := e.Dur
	length := unit.Value().MulInt(int64(den)).Mul(ratio(m.Tuplets, ctx))
	if room := L.limit(ctx).Sub(at); room.Less(length) {
		return l, fmt.Errorf("%d %ss in the time of %d need %s; bar %d has %s left", n, durName(unit), den, length, l.Bar, room)
	}
	first := m.Events[l.Event].Clone()
	first.Tuplet = 1
	seq := piece{tuplets: []score.Tuplet{{Num: n, Den: den}}, events: []score.Event{first}}
	for i := 1; i < n; i++ {
		seq.events = append(seq.events, score.Event{Kind: score.EventRest, Dur: unit, Tuplet: 1})
	}
	nm, idx, err := b.replace(m, ctx, at, at.Add(length), seq)
	if err != nil {
		return l, err
	}
	*mp = nm
	l.Event = idx
	return l, nil
}

// FlipStem forces an event's stem the other way from where it is drawn
// (up is the engraved direction), or back to the engraver's choice when it
// is already forced.
func FlipStem(s *score.Score, l Loc, up bool) error {
	e := Event(s, l)
	if e == nil {
		return ErrNoEvent
	}
	if e.Kind != score.EventChord {
		return fmt.Errorf("only notes have stems")
	}
	forced := false
	var marks []score.Mark
	for _, m := range e.Marks {
		if m.Name == "up" || m.Name == "down" {
			forced = true
			continue
		}
		marks = append(marks, m)
	}
	if !forced {
		name := "up"
		if up {
			name = "down"
		}
		marks = append(marks, score.Mark{Name: name})
	}
	e.Marks = marks
	return nil
}

// FitMeasure makes a measure as long as its bar: a long one is cut at the
// barline (a note across it keeps what fits, a tuplet across it goes) and a
// short one filled out with rests. An empty bar or a bar rest stays as it
// is. It reports whether music was cut.
func FitMeasure(m score.Measure, barLen score.Frac, meter score.Meter) (score.Measure, bool, error) {
	b := bar{barLen: barLen, meter: meter}
	main := 0
	for _, e := range m.Events {
		if e.Grace == score.NotGrace {
			main++
		}
		if e.Kind == score.EventBarRest {
			return m, false, nil
		}
	}
	if main == 0 {
		return m, false, nil
	}
	_, filled := m.Offsets(barLen)
	switch filled.Cmp(barLen) {
	case 0:
		return m, false, nil
	case -1:
		nm, err := b.normalized(m)
		return nm, false, err
	}
	nm, _, err := b.replace(m.Clone(), 0, barLen, filled, piece{})
	if err != nil {
		return m, false, err
	}
	// Whatever starts past the barline is gone; trailing grace notes go with it.
	L := lay(nm, barLen)
	k := len(nm.Events)
	for k > 0 && !L.on[k-1].Less(barLen) {
		k--
	}
	nm = compact(nm.Events[:k], nm.Tuplets)
	return nm, true, nil
}

// durName is a value's name in English: "quarter", "dotted 8th".
func durName(d score.Duration) string {
	names := map[score.DurBase]string{
		score.Breve: "breve", score.WholeNote: "whole", score.Half: "half", score.Quarter: "quarter",
		score.Eighth: "8th", score.D16: "16th", score.D32: "32nd", score.D64: "64th", score.D128: "128th", score.D256: "256th",
	}
	n := names[d.Base]
	switch d.Dots {
	case 1:
		n = "dotted " + n
	case 2:
		n = "double-dotted " + n
	case 3:
		n = "triple-dotted " + n
	}
	return n
}

// DurName is a value's name: "quarter", "dotted 8th", "64th".
func DurName(d score.Duration) string { return durName(d) }
