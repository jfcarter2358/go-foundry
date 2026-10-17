package edit

// rebar.go — a time signature that re-bars the music after it.
//
// SetMeter fits each bar to its new length (cutting what spills over);
// Rebar does what Sibelius's "rewrite bars up to the next time signature"
// does: the music from the new time signature to the next one (or the
// end) runs on unchanged in time and is written again across the new
// barlines — notes that now cross one tied over it, beamed and grouped by
// the new meter, tuplets where their notes need them, every note's
// spelling, accidental display, marks, words and grace notes kept, a rest's
// marks kept where it stood. The last new bar is filled out with rests.
//
// Everything placed by bar or position goes with the music: inside the
// passage by its time (a chord symbol, a tempo mark, a clef change, a lane
// point keeps its moment; a rehearsal mark, key change, break, repeat or
// ending its bar's start), after it by the number of bars gained or lost.

import (
	"fmt"
	"sort"

	"foundry/internal/score"
	"foundry/internal/score/io/notate"
)

// Rebar puts a time signature at a bar and writes the music it governs
// again across the new barlines. It returns how many bars the passage
// gained (negative: lost).
func Rebar(s *score.Score, at int, m score.Meter) (delta int, err error) {
	defer score.RecoverOverflow(&err)
	if at < 1 || at > s.Measures() {
		return 0, fmt.Errorf("no bar %d", at)
	}
	if m.Den <= 0 || m.Num() <= 0 {
		return 0, fmt.Errorf("bad time signature")
	}
	B := m.Length()
	if B.Cmp(score.MaxBarLength) > 0 {
		return 0, fmt.Errorf("a bar is at most %s whole notes", score.MaxBarLength)
	}
	// The passage: from the bar (past a pickup, which keeps its length) to
	// the next time signature's bar, or the end.
	a := at
	if at == 1 && !s.Pickup.IsZero() {
		a = 2
	}
	z := s.Measures()
	for _, mm := range s.Meter {
		if mm.Bar > at {
			z = mm.Bar - 1
			break
		}
	}
	old := s.BarStarts()
	var t0, t1 score.Frac
	if a <= z {
		t0, t1 = old[a-1], old[z]
	}
	L := t1.Sub(t0)
	n := 0
	if L.Sign() > 0 {
		n = int(L.Div(B).Floor())
		if !B.MulInt(int64(n)).Eq(L) {
			n++
		}
	}
	if a > z {
		n = 0
	}
	delta = n - max(z-a+1, 0)
	if s.Measures()+delta > score.MaxBars {
		return 0, fmt.Errorf("the score would hold more than %d bars", score.MaxBars)
	}

	// The new bar grid of the passage, from its start.
	keyAt := func(t score.Frac) int { return s.KeyAt(barOfTime(old, t)).Fifths }
	lay := notate.Layout{}
	for k := 0; k <= n; k++ {
		lay.Starts = append(lay.Starts, B.MulInt(int64(k)))
	}
	for k := 0; k < n; k++ {
		lay.Meters = append(lay.Meters, m)
		lay.Fifths = append(lay.Fifths, keyAt(t0.Add(B.MulInt(int64(k)))))
	}

	// Every voice, written again.
	for _, l := range s.Lines {
		for _, st := range l.Staves {
			for vi, v := range st.Voices {
				if err := rebarVoice(s, v, vi, a, z, n, t0, old, lay); err != nil {
					return 0, err
				}
			}
		}
	}

	// The maps and everything placed in the score.
	mapPos := func(p score.Pos) score.Pos {
		switch {
		case p.Bar < a:
			return p
		case p.Bar > z:
			p.Bar += delta
			return p
		}
		t := s.PosFrac(p, old).Sub(t0)
		k := int(t.Div(B).Floor())
		k = max(0, min(k, max(n-1, 0)))
		return score.Pos{Bar: a + k, Offset: t.Sub(B.MulInt(int64(k)))}
	}
	mapBar := func(b int) int { return mapPos(score.Pos{Bar: b}).Bar }
	// mapEnd is the new bar holding the end of an old bar.
	mapEnd := func(b int) int {
		if b < a || b > z {
			return mapBar(b)
		}
		t := old[b].Sub(t0)
		k := int(t.Div(B).Floor())
		if B.MulInt(int64(k)).Eq(t) {
			k--
		}
		return a + max(0, min(k, n-1))
	}
	shiftAfter := func(b int) int {
		if b > z {
			return b + delta
		}
		return b
	}

	var meters []score.MeterMark
	for _, mm := range s.Meter {
		if mm.Bar == at {
			continue
		}
		mm.Bar = shiftAfter(mm.Bar)
		meters = append(meters, mm)
	}
	if at == 1 || !s.MeterAt(at-1).Eq(m) {
		m.Groups = append([]int(nil), m.Groups...)
		meters = append(meters, score.MeterMark{Bar: at, Meter: m})
	}
	s.Meter = meters
	for i := range s.Key {
		s.Key[i].Bar = mapBar(s.Key[i].Bar)
	}
	for i := range s.Tempo {
		s.Tempo[i].At = mapPos(s.Tempo[i].At)
		if s.Tempo[i].HasTo {
			s.Tempo[i].To = mapPos(s.Tempo[i].To)
		}
	}
	for i := range s.Sections {
		s.Sections[i].Bar = mapBar(s.Sections[i].Bar)
	}
	for i := range s.Markers {
		s.Markers[i].At = mapPos(s.Markers[i].At)
	}
	for i := range s.Repeats {
		s.Repeats[i].Start, s.Repeats[i].End = mapBar(s.Repeats[i].Start), mapEnd(s.Repeats[i].End)
	}
	for i := range s.Voltas {
		s.Voltas[i].Start, s.Voltas[i].End = mapBar(s.Voltas[i].Start), mapEnd(s.Voltas[i].End)
	}
	for i := range s.Jumps {
		s.Jumps[i].Bar = mapBar(s.Jumps[i].Bar)
	}
	mapBreaks := func(bs []score.Break) []score.Break {
		var out []score.Break
		seen := map[int]bool{}
		for _, br := range bs {
			br.Bar = mapBar(br.Bar)
			if br.Bar > 1 && !seen[br.Bar] {
				seen[br.Bar] = true
				out = append(out, br)
			}
		}
		return out
	}
	s.Layout.Breaks = mapBreaks(s.Layout.Breaks)
	for i := range s.Layout.Parts {
		s.Layout.Parts[i].Breaks = mapBreaks(s.Layout.Parts[i].Breaks)
	}
	mapLanes := func(ls []score.Lane) {
		for li := range ls {
			for pi := range ls[li].Points {
				p := &ls[li].Points[pi]
				p.At = mapPos(p.At)
				if p.HasTo {
					p.To = mapPos(p.To)
				}
			}
		}
	}
	mapLanes(s.MasterLanes)
	mapLanes(s.Buses.A.Lanes)
	mapLanes(s.Buses.B.Lanes)
	for _, l := range s.Lines {
		mapLanes(l.Lanes)
		for i := range l.Chords {
			l.Chords[i].At = mapPos(l.Chords[i].At)
		}
		for i := range l.Overrides {
			l.Overrides[i].From, l.Overrides[i].To = mapPos(l.Overrides[i].From), mapPos(l.Overrides[i].To)
		}
		for _, st := range l.Staves {
			for i := range st.Clefs {
				st.Clefs[i].At = mapPos(st.Clefs[i].At)
			}
		}
	}
	s.Bars = max(s.Bars+delta, 1)
	s.SortMaps()
	dropRepeatedKeys(s)
	return delta, nil
}

// rebarVoice writes bars a..z of a voice again as n bars of the new grid.
func rebarVoice(s *score.Score, v *score.Voice, vi, a, z, n int, t0 score.Frac, old []score.Frac, lay notate.Layout) error {
	type at struct {
		t    score.Frac
		midi int
	}
	var chords []notate.Chord
	accs := map[at]score.AccShow{}
	restMarks := map[score.Frac][]score.Mark{}
	var graces []score.Event
	for b := a; b <= z && b <= len(v.Measures); b++ {
		m := v.Measures[b-1]
		barLen := s.BarLength(b)
		offs, _ := m.Offsets(barLen)
		for i, e := range m.Events {
			if e.Grace != score.NotGrace {
				graces = append(graces, e.Clone())
				continue
			}
			t := old[b-1].Add(offs[i]).Sub(t0)
			if e.Kind != score.EventChord {
				if len(e.Marks) > 0 {
					restMarks[fk(t)] = append(restMarks[fk(t)], e.Marks...)
				}
				graces = nil
				continue
			}
			c := notate.Chord{Start: t, End: t.Add(m.Actual(e, barLen)), Marks: append([]score.Mark(nil), e.Marks...), GraceEvents: graces}
			graces = nil
			for _, nt := range e.Notes {
				c.MIDI = append(c.MIDI, nt.Pitch.MIDI())
				c.Pitches = append(c.Pitches, nt.Pitch)
				if nt.Tie {
					c.TieNext = append(c.TieNext, nt.Pitch.MIDI())
				}
				accs[at{fk(t), nt.Pitch.MIDI()}] = nt.Acc
			}
			sort.Slice(c.Pitches, func(x, y int) bool { return c.Pitches[x].MIDI() < c.Pitches[y].MIDI() })
			chords = append(chords, c)
		}
	}
	// A voice silent over the whole passage stays silent (no bar rests
	// where it had nothing).
	silent := len(chords) == 0 && len(restMarks) == 0
	for b := a; b <= z && b <= len(v.Measures) && silent; b++ {
		silent = len(v.Measures[b-1].Events) == 0
	}
	var w *score.Voice
	if n > 0 && !silent {
		var err error
		w, err = notate.Write(chords, lay, notate.WriteOptions{Gaps: notate.Rests, BarRests: vi == 0})
		if err != nil {
			return err
		}
	}
	nm := make([]score.Measure, n)
	for k := 0; k < n; k++ {
		if w != nil && k < len(w.Measures) {
			nm[k] = w.Measures[k]
		}
		offs, _ := nm[k].Offsets(lay.BarLen(k))
		for i := range nm[k].Events {
			e := &nm[k].Events[i]
			if e.Grace != score.NotGrace {
				continue
			}
			t := lay.Starts[k].Add(offs[i])
			switch e.Kind {
			case score.EventChord:
				for q := range e.Notes {
					if acc, ok := accs[at{fk(t), e.Notes[q].Pitch.MIDI()}]; ok {
						e.Notes[q].Acc = acc
					}
				}
			default:
				if ms, ok := restMarks[fk(t)]; ok {
					e.Marks = mergeMarks(e.Marks, ms)
					delete(restMarks, fk(t))
				}
			}
		}
	}
	// A rest's marks with nowhere of their own: the event sounding then.
	for t, ms := range restMarks {
		k := min(int(t.Div(lay.BarLen(0)).Floor()), n-1)
		if k < 0 {
			continue
		}
		offs, _ := nm[k].Offsets(lay.BarLen(k))
		for i := len(nm[k].Events) - 1; i >= 0; i-- {
			if !t.Less(lay.Starts[k].Add(offs[i])) && nm[k].Events[i].Grace == score.NotGrace {
				nm[k].Events[i].Marks = mergeMarks(nm[k].Events[i].Marks, ms)
				break
			}
		}
	}
	// Splice: the bars before, the new ones, the bars after.
	var out []score.Measure
	for b := 1; b < a; b++ {
		out = append(out, v.Measure(b))
	}
	out = append(out, nm...)
	for b := z + 1; b <= len(v.Measures); b++ {
		out = append(out, v.Measures[b-1])
	}
	v.Measures = out
	v.Trim()
	return nil
}
