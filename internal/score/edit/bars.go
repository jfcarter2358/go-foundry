package edit

// bars.go — whole bars: copying a passage of one or more lines, pasting it
// (the score grows to hold it), repeating it after itself, clearing it; and
// the changes that happen at a bar — key, time signature — and a staff's clef.

import (
	"fmt"

	"foundry/internal/score"
)

// Clip is a passage of whole bars of one or more lines: every staff and
// voice of each, as written.
type Clip struct {
	// Bars is how many bars wide it is.
	Bars int
	// Lengths are the source bars' lengths, to fit a paste into bars of
	// another meter.
	Lengths []score.Frac
	// Lines are the copied lines' music: [line][staff][voice][bar].
	Lines [][][][]score.Measure
	// Chords are each copied line's chord symbols in the bars, their bars
	// counted from the clip's first (1).
	Chords [][]score.ChordSymbol
	// Names are the lines' names, for the status line.
	Names []string
}

// CopyBars copies bars a..z (inclusive) of lines lo..hi.
func CopyBars(s *score.Score, lo, hi, a, z int) (*Clip, error) {
	if lo < 0 || hi >= len(s.Lines) || lo > hi {
		return nil, fmt.Errorf("no lines %d to %d", lo+1, hi+1)
	}
	if a < 1 || z < a || z > s.Measures() {
		return nil, fmt.Errorf("no bars %d to %d", a, z)
	}
	c := &Clip{Bars: z - a + 1}
	for b := a; b <= z; b++ {
		c.Lengths = append(c.Lengths, s.BarLength(b))
	}
	for li := lo; li <= hi; li++ {
		l := s.Lines[li]
		c.Names = append(c.Names, l.Name)
		staves := make([][][]score.Measure, len(l.Staves))
		for si, st := range l.Staves {
			voices := make([][]score.Measure, len(st.Voices))
			for vi, v := range st.Voices {
				for b := a; b <= z; b++ {
					voices[vi] = append(voices[vi], v.Measure(b).Clone())
				}
			}
			staves[si] = voices
		}
		c.Lines = append(c.Lines, staves)
		var chords []score.ChordSymbol
		for _, ch := range l.Chords {
			if ch.At.Bar >= a && ch.At.Bar <= z {
				ch.At.Bar -= a - 1
				ch.Chord = ch.Chord.Clone()
				chords = append(chords, ch)
			}
		}
		c.Chords = append(c.Chords, chords)
	}
	return c, nil
}

// PasteBars writes a clip over the bars from bar on, its first line into
// line, the next into the line after, and so on (lines past the score's
// last are dropped). Every voice of the destination bars is replaced; a bar
// of another length than its source is fitted (FitMeasure). The score grows
// to hold the paste. It returns the lines and bars written.
func PasteBars(s *score.Score, c *Clip, line, at int) (lines int, cut bool, err error) {
	if c == nil || c.Bars == 0 {
		return 0, false, fmt.Errorf("nothing copied")
	}
	if line < 0 || line >= len(s.Lines) {
		return 0, false, fmt.Errorf("no line %d", line+1)
	}
	if at < 1 || at+c.Bars-1 > score.MaxBars {
		return 0, false, fmt.Errorf("a score holds at most %d bars", score.MaxBars)
	}
	EnsureBars(s, at+c.Bars-1)
	for j, staves := range c.Lines {
		li := line + j
		if li >= len(s.Lines) {
			break
		}
		lines++
		if j < len(c.Chords) {
			pasteChords(s, s.Lines[li], c.Chords[j], at, c.Bars)
		}
		for si, voices := range staves {
			if si >= len(s.Lines[li].Staves) {
				continue
			}
			st := s.Lines[li].Staves[si]
			for vi := 0; vi < max(len(voices), len(st.Voices)); vi++ {
				if vi >= len(voices) {
					// A voice the clip does not have is silent in the pasted bars.
					v := st.Voices[vi]
					for k := 0; k < c.Bars && at+k <= len(v.Measures); k++ {
						v.Measures[at+k-1] = score.Measure{}
					}
					continue
				}
				v, err := EnsureVoice(s, li, si, vi)
				if err != nil {
					return lines, cut, err
				}
				for k, m := range voices[vi] {
					b := at + k
					mp := measureOf(s, v, b)
					nm := m.Clone()
					if bl := s.BarLength(b); !bl.Eq(c.Lengths[k]) {
						fm, didCut, err := FitMeasure(nm, bl, s.MeterAt(b))
						if err != nil {
							return lines, cut, err
						}
						nm, cut = fm, cut || didCut
					}
					*mp = nm
				}
			}
		}
	}
	return lines, cut, nil
}

// pasteChords puts a clip's chord symbols for a line over the bars from at
// on (bars bars), in place of the ones there; a symbol past its new bar's
// end (a shorter meter) is left out.
func pasteChords(s *score.Score, l *score.Line, chords []score.ChordSymbol, at, bars int) {
	var keep []score.ChordSymbol
	for _, ch := range l.Chords {
		if ch.At.Bar < at || ch.At.Bar >= at+bars {
			keep = append(keep, ch)
		}
	}
	for _, ch := range chords {
		ch.At.Bar += at - 1
		if ch.At.Offset.Less(s.BarLength(ch.At.Bar)) {
			ch.Chord = ch.Chord.Clone()
			keep = append(keep, ch)
		}
	}
	l.Chords = keep
	l.SortChords()
}

// ClearBars empties bars a..z of lines lo..hi in every voice, and their
// chord symbols: they show as bar rests again.
func ClearBars(s *score.Score, lo, hi, a, z int) {
	for li := max(lo, 0); li <= hi && li < len(s.Lines); li++ {
		var keep []score.ChordSymbol
		for _, ch := range s.Lines[li].Chords {
			if ch.At.Bar < a || ch.At.Bar > z {
				keep = append(keep, ch)
			}
		}
		s.Lines[li].Chords = keep
		for _, st := range s.Lines[li].Staves {
			for _, v := range st.Voices {
				for b := a; b <= z && b <= len(v.Measures); b++ {
					if b >= 1 {
						v.Measures[b-1] = score.Measure{}
					}
				}
			}
		}
	}
}

// SetKey puts a key signature at a bar (removing any there), leaving no mark
// that only repeats the key already in force. Pitches are spelled, so the
// notes do not change; only what the page prints does.
func SetKey(s *score.Score, at, fifths int, mode string) error {
	if fifths < -7 || fifths > 7 {
		return fmt.Errorf("a key has at most 7 sharps or flats")
	}
	if at < 1 {
		return fmt.Errorf("no bar %d", at)
	}
	if mode == "" {
		mode = "major"
	}
	var keep []score.KeyMark
	for _, k := range s.Key {
		if k.Bar != at {
			keep = append(keep, k)
		}
	}
	s.Key = keep
	prev := s.KeyAt(at - 1)
	if at == 1 || prev.Fifths != fifths || prev.Mode != mode {
		s.Key = append(s.Key, score.KeyMark{Bar: at, Fifths: fifths, Mode: mode})
	}
	s.SortMaps()
	dropRepeatedKeys(s)
	return nil
}

// dropRepeatedKeys removes key marks equal to the key before them.
func dropRepeatedKeys(s *score.Score) {
	var out []score.KeyMark
	for i, k := range s.Key {
		if i > 0 && k.Bar > 1 && out[len(out)-1].Fifths == k.Fifths && out[len(out)-1].Mode == k.Mode {
			continue
		}
		out = append(out, k)
	}
	s.Key = out
}

// SetMeter puts a time signature at a bar and fits the bars it governs (up
// to the next time signature) to their new length in every voice: music
// past the new barline is cut, a bar left short is filled with rests. It
// returns the bars refitted and whether any music was cut.
func SetMeter(s *score.Score, at int, m score.Meter) (from, to int, cut bool, err error) {
	if at < 1 {
		return 0, 0, false, fmt.Errorf("no bar %d", at)
	}
	if m.Den <= 0 || m.Num() <= 0 {
		return 0, 0, false, fmt.Errorf("bad time signature")
	}
	if m.Length().Cmp(score.MaxBarLength) > 0 {
		return 0, 0, false, fmt.Errorf("a bar is at most %s whole notes", score.MaxBarLength)
	}
	var keep []score.MeterMark
	for _, mm := range s.Meter {
		if mm.Bar != at {
			keep = append(keep, mm)
		}
	}
	s.Meter = keep
	if at == 1 || !s.MeterAt(at-1).Eq(m) {
		m.Groups = append([]int(nil), m.Groups...)
		s.Meter = append(s.Meter, score.MeterMark{Bar: at, Meter: m})
	}
	s.SortMaps()
	// Up to the next mark.
	end := s.Measures()
	for _, mm := range s.Meter {
		if mm.Bar > at {
			end = mm.Bar - 1
			break
		}
	}
	for _, l := range s.Lines {
		for _, st := range l.Staves {
			for _, v := range st.Voices {
				for b := at; b <= end && b <= len(v.Measures); b++ {
					fm, didCut, err := FitMeasure(v.Measures[b-1], s.BarLength(b), s.MeterAt(b))
					if err != nil {
						return at, end, cut, err
					}
					v.Measures[b-1], cut = fm, cut || didCut
				}
			}
		}
	}
	return at, end, cut, nil
}

// SetClef changes a staff's clef. The pitches stay; they move on the staff.
func SetClef(s *score.Score, line, staff int, clef string) error {
	st := Staff(s, line, staff)
	if st == nil {
		return fmt.Errorf("no staff %d of line %d", staff+1, line+1)
	}
	for _, c := range score.StaffClefs() {
		if c == clef {
			st.Clef = clef
			return nil
		}
	}
	return fmt.Errorf("no clef %q", clef)
}
