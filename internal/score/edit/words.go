package edit

// words.go — lyrics and chord symbols: the syllables on a voice's notes,
// and the chord symbols over a line.
//
// Lyrics go on notes (score.Lyric), so entry walks a voice from note to
// note: the next one sung is the next note of the voice that is not a
// grace note and not tied over from the note before (a syllable is sung
// once over a tie), across barlines (NextSung, PrevSung). A melisma is a
// syllable held on: Extend makes the syllable before a place reach over
// the notes up to it.
//
// Chord symbols are a line's, at written positions (score.ChordSymbol):
// one at a place, set or replaced, removed; entry moves from a symbol to
// the next beat or the next note of the line, whichever comes first
// (NextChordPlace).

import (
	"fmt"

	"foundry/internal/score"
)

// sung reports an event a syllable can go on: a note that is not a grace
// note and not tied over from the event before.
func sung(s *score.Score, l Loc) bool {
	e := Event(s, l)
	if e == nil || e.Kind != score.EventChord || e.Grace != score.NotGrace {
		return false
	}
	prev, ok := prevMain(s, l)
	if !ok {
		return true
	}
	pe := Event(s, prev)
	if pe == nil || pe.Kind != score.EventChord {
		return true
	}
	for _, n := range e.Notes {
		tied := false
		for _, pn := range pe.Notes {
			if pn.Tie && pn.Pitch.MIDI() == n.Pitch.MIDI() {
				tied = true
			}
		}
		if !tied {
			return true
		}
	}
	return false
}

// nextMain and prevMain are the voice's next and previous main event (not
// a grace note), across barlines; empty bars are skipped.
func nextMain(s *score.Score, l Loc) (Loc, bool) {
	v := Voice(s, l.Line, l.Staff, l.Voice)
	if v == nil {
		return Loc{}, false
	}
	for b, i := l.Bar, l.Event+1; b <= len(v.Measures); b, i = b+1, 0 {
		m := v.Measures[b-1]
		for ; i < len(m.Events); i++ {
			if m.Events[i].Grace == score.NotGrace {
				return Loc{l.Line, l.Staff, l.Voice, b, i}, true
			}
		}
	}
	return Loc{}, false
}

func prevMain(s *score.Score, l Loc) (Loc, bool) {
	v := Voice(s, l.Line, l.Staff, l.Voice)
	if v == nil {
		return Loc{}, false
	}
	for b, i := l.Bar, l.Event-1; b >= 1; b, i = b-1, 1<<30 {
		if b > len(v.Measures) {
			continue
		}
		m := v.Measures[b-1]
		for i = min(i, len(m.Events)-1); i >= 0; i-- {
			if m.Events[i].Grace == score.NotGrace {
				return Loc{l.Line, l.Staff, l.Voice, b, i}, true
			}
		}
	}
	return Loc{}, false
}

// NextSung is the voice's next note a syllable can go on after l.
func NextSung(s *score.Score, l Loc) (Loc, bool) {
	for {
		n, ok := nextMain(s, l)
		if !ok {
			return Loc{}, false
		}
		if sung(s, n) {
			return n, true
		}
		l = n
	}
}

// PrevSung is the voice's last note a syllable can go on before l.
func PrevSung(s *score.Score, l Loc) (Loc, bool) {
	for {
		n, ok := prevMain(s, l)
		if !ok {
			return Loc{}, false
		}
		if sung(s, n) {
			return n, true
		}
		l = n
	}
}

// FirstSung is l itself when a syllable can go on it, else the next note
// that can take one.
func FirstSung(s *score.Score, l Loc) (Loc, bool) {
	if sung(s, l) {
		return l, true
	}
	return NextSung(s, l)
}

// SetLyric puts a syllable on a note in its verse (an empty Text takes it
// off). Only notes are sung: a rest or a grace note is refused.
func SetLyric(s *score.Score, l Loc, ly score.Lyric) error {
	e := Event(s, l)
	if e == nil {
		return ErrNoEvent
	}
	if ly.Verse < 1 || ly.Verse > score.MaxVerses {
		return fmt.Errorf("verses run 1 to %d", score.MaxVerses)
	}
	if ly.Text != "" && (e.Kind != score.EventChord || e.Grace != score.NotGrace) {
		return fmt.Errorf("a syllable goes on a note, not a rest or a grace note")
	}
	e.SetLyric(ly)
	return nil
}

// Melisma holds the verse's syllable before l on to l: the syllable's
// extender then reaches over the notes up to it. A syllable that already
// joins the next with a hyphen keeps its hyphen (a hyphen runs on under a
// melisma inside a word). It reports the syllable it holds, if any.
func Melisma(s *score.Score, l Loc, verse int) (Loc, bool) {
	for p, ok := PrevSung(s, l); ok; p, ok = PrevSung(s, p) {
		e := Event(s, p)
		if ly, has := e.Lyric(verse); has {
			if !ly.Hyphen {
				ly.Extend = true
				e.SetLyric(ly)
			}
			return p, true
		}
	}
	return Loc{}, false
}

// ClearLyrics takes a verse's syllables (verse 0: every verse's) off bars
// a..z of lines lo..hi.
func ClearLyrics(s *score.Score, lo, hi, a, z, verse int) int {
	n := 0
	for li := max(lo, 0); li <= hi && li < len(s.Lines); li++ {
		for _, st := range s.Lines[li].Staves {
			for _, v := range st.Voices {
				for b := max(a, 1); b <= z && b <= len(v.Measures); b++ {
					m := &v.Measures[b-1]
					for i := range m.Events {
						e := &m.Events[i]
						for _, ly := range e.Lyrics() {
							if verse == 0 || ly.Verse == verse {
								e.SetLyric(score.Lyric{Verse: ly.Verse})
								n++
							}
						}
					}
				}
			}
		}
	}
	return n
}

// ---------- chord symbols ----------

// ChordSymbolAt is a line's chord symbol at a written position, or nil.
func ChordSymbolAt(s *score.Score, line int, at score.Pos) *score.ChordSymbol {
	if line < 0 || line >= len(s.Lines) {
		return nil
	}
	for i := range s.Lines[line].Chords {
		if samePos(s.Lines[line].Chords[i].At, at) {
			return &s.Lines[line].Chords[i]
		}
	}
	return nil
}

// SetChordSymbol puts a chord symbol over a line at a written position,
// replacing one there, and returns its index.
func SetChordSymbol(s *score.Score, line int, at score.Pos, c score.Chord) (int, error) {
	if line < 0 || line >= len(s.Lines) {
		return 0, fmt.Errorf("no line %d", line+1)
	}
	if at.Bar < 1 || at.Bar > s.Measures() || at.Offset.Sign() < 0 || !at.Offset.Less(s.BarLength(at.Bar)) {
		return 0, fmt.Errorf("no place %s in the score", s.FormatPos(at))
	}
	l := s.Lines[line]
	if cs := ChordSymbolAt(s, line, at); cs != nil {
		cs.Chord = c
	} else {
		l.Chords = append(l.Chords, score.ChordSymbol{At: at, Chord: c})
		l.SortChords()
	}
	for i := range l.Chords {
		if samePos(l.Chords[i].At, at) {
			return i, nil
		}
	}
	return 0, nil
}

// RemoveChordSymbol takes a line's chord symbol at a position off, and
// reports whether there was one.
func RemoveChordSymbol(s *score.Score, line int, at score.Pos) bool {
	if line < 0 || line >= len(s.Lines) {
		return false
	}
	l := s.Lines[line]
	for i := range l.Chords {
		if samePos(l.Chords[i].At, at) {
			l.Chords = append(l.Chords[:i], l.Chords[i+1:]...)
			return true
		}
	}
	return false
}

// NextChordPlace is where chord symbol entry goes after a position of a
// line: the next of the meter's beats or of the line's note onsets
// (every staff and voice), whichever comes first, into the next bar at a
// bar's end. The score's last bar's end has no place after it.
func NextChordPlace(s *score.Score, line int, at score.Pos) (score.Pos, bool) {
	if line < 0 || line >= len(s.Lines) {
		return score.Pos{}, false
	}
	n := s.Measures()
	for bar, from := at.Bar, at.Offset; bar <= n; bar, from = bar+1, score.F(-1, 1) {
		best, found := score.Frac{}, false
		consider := func(t score.Frac) {
			if from.Less(t) && t.Less(s.BarLength(bar)) && (!found || t.Less(best)) {
				best, found = t, true
			}
		}
		t := score.Frac{Num: 0, Den: 1}
		for _, b := range s.MeterAt(bar).Beats() {
			consider(t)
			t = t.Add(b)
		}
		for _, st := range s.Lines[line].Staves {
			for _, v := range st.Voices {
				m := v.Measure(bar)
				offs, _ := m.Offsets(s.BarLength(bar))
				for i, e := range m.Events {
					if e.Grace == score.NotGrace && e.Kind != score.EventSpace {
						consider(offs[i])
					}
				}
			}
		}
		if found {
			return score.Pos{Bar: bar, Offset: best}, true
		}
	}
	return score.Pos{}, false
}

// PrevChordPlace is the place chord symbol entry goes back to before a
// position: the last beat or note onset before it, into the bar before at
// a bar's start.
func PrevChordPlace(s *score.Score, line int, at score.Pos) (score.Pos, bool) {
	if line < 0 || line >= len(s.Lines) {
		return score.Pos{}, false
	}
	for bar, to := at.Bar, at.Offset; bar >= 1; bar, to = bar-1, score.Whole(1<<20) {
		best, found := score.Frac{}, false
		consider := func(t score.Frac) {
			if t.Less(to) && t.Less(s.BarLength(bar)) && (!found || best.Less(t)) {
				best, found = t, true
			}
		}
		t := score.Frac{Num: 0, Den: 1}
		for _, b := range s.MeterAt(bar).Beats() {
			consider(t)
			t = t.Add(b)
		}
		for _, st := range s.Lines[line].Staves {
			for _, v := range st.Voices {
				m := v.Measure(bar)
				offs, _ := m.Offsets(s.BarLength(bar))
				for i, e := range m.Events {
					if e.Grace == score.NotGrace && e.Kind != score.EventSpace {
						consider(offs[i])
					}
				}
			}
		}
		if found {
			return score.Pos{Bar: bar, Offset: best}, true
		}
	}
	return score.Pos{}, false
}

// TransposeChordSymbols moves a line's chord symbols in bars a..z by an
// interval, as a transposition of the notes does.
func TransposeChordSymbols(s *score.Score, line, a, z int, iv score.Interval) {
	if line < 0 || line >= len(s.Lines) {
		return
	}
	for i := range s.Lines[line].Chords {
		ch := &s.Lines[line].Chords[i]
		if ch.At.Bar >= a && ch.At.Bar <= z {
			ch.Chord = ch.Chord.Transpose(iv)
		}
	}
}
