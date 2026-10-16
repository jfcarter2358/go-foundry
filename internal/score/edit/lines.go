package edit

// lines.go — whole lines: adding one for an instrument (a staff, a grand
// staff or a percussion staff, by what the instrument says it is written
// on), duplicating one with its notes or empty, removing, reordering and
// renaming. Scripts and bindings find a line by name, so a name stays
// unique and a rename carries the score's bindings with it.

import (
	"fmt"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/io/gm"
	"foundry/internal/tracker"
)

// UniqueLineName is base, or base with the first number after it no other
// line has ("Violin I 2"); line skip (-1 for none) does not count, so a
// line keeps its own name.
func UniqueLineName(s *score.Score, base string, skip int) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "Line"
	}
	taken := func(n string) bool {
		for i, l := range s.Lines {
			if i != skip && strings.EqualFold(l.Name, n) {
				return true
			}
		}
		return false
	}
	if !taken(base) {
		return base
	}
	for k := 2; ; k++ {
		if n := fmt.Sprintf("%s %d", base, k); !taken(n) {
			return n
		}
	}
}

// NewLineFor is a line for a library instrument written the way its
// playback defaults say: a grand staff (treble and bass) for "grand", a
// percussion staff, or one staff in the instrument's clef (treble when it
// names none), transposed as the instrument is (written → sounding).
func NewLineFor(name, instrument string, pb tracker.Playback) *score.Line {
	clef := pb.Clef
	if clef == "" {
		clef = "treble"
	}
	if clef == "grand" {
		l := score.NewLine(name, instrument, "treble")
		l.Staves = append(l.Staves, &score.Staff{Clef: "bass", Voices: []*score.Voice{{}}})
		return l
	}
	l := score.NewLine(name, instrument, clef)
	if pb.Transposition != 0 && clef != "percussion" {
		d, c := gm.Transposition(pb.Transposition)
		l.Staves[0].Transpose = score.Interval{Diatonic: d, Chromatic: c}
	}
	return l
}

// Retranspose writes a line's staves the way another instrument reads
// them: each staff (not a percussion one) takes the instrument's
// transposition, as Sibelius does on a change of instrument. The notes are
// sounding pitches, so they sound the same; only the page moves. It
// reports whether any staff changed.
func Retranspose(l *score.Line, pb tracker.Playback) bool {
	var iv score.Interval
	if pb.Transposition != 0 && pb.Clef != "grand" {
		d, c := gm.Transposition(pb.Transposition)
		iv = score.Interval{Diatonic: d, Chromatic: c}
	}
	changed := false
	for _, st := range l.Staves {
		if st.Clef == "percussion" || st.Transpose == iv {
			continue
		}
		st.Transpose, changed = iv, true
	}
	return changed
}

// AddLine puts a line at index at (clamped; len(s.Lines) appends) under a
// name no other line has, and returns where it went.
func AddLine(s *score.Score, at int, l *score.Line) int {
	at = max(0, min(at, len(s.Lines)))
	l.Name = UniqueLineName(s, l.Name, -1)
	s.Lines = append(s.Lines, nil)
	copy(s.Lines[at+1:], s.Lines[at:])
	s.Lines[at] = l
	return at
}

// DuplicateLine copies line i to just after it and returns the copy's
// index. With notes it is the whole line — staves, voices, lanes; empty it
// is the same instrument, clefs, transpositions and mixer settings (fader,
// pan, sends, inserts, mute) with nothing written. A copy never comes
// soloed: two soloed lines would silence the rest twice over.
func DuplicateLine(s *score.Score, i int, withNotes bool) (int, error) {
	if i < 0 || i >= len(s.Lines) {
		return 0, fmt.Errorf("no line %d", i+1)
	}
	src := s.Lines[i]
	var c *score.Line
	if withNotes {
		c = src.Clone()
	} else {
		c = &score.Line{Name: src.Name, Instrument: src.Instrument, Volume: src.Volume, Pan: src.Pan, Mute: src.Mute,
			SendA: src.SendA, SendB: src.SendB}
		for _, f := range src.Inserts {
			f.Params = append([]score.FXParam(nil), f.Params...)
			c.Inserts = append(c.Inserts, f)
		}
		for _, st := range src.Staves {
			c.Staves = append(c.Staves, &score.Staff{Clef: st.Clef, Transpose: st.Transpose, Voices: []*score.Voice{{}}})
		}
	}
	c.Solo = false
	return AddLine(s, i+1, c), nil
}

// RemoveLine takes line i out, with the bindings that name it. The last
// line stays: a score is written on something.
func RemoveLine(s *score.Score, i int) error {
	if i < 0 || i >= len(s.Lines) {
		return fmt.Errorf("no line %d", i+1)
	}
	if len(s.Lines) == 1 {
		return fmt.Errorf("a score needs at least one line")
	}
	name := s.Lines[i].Name
	s.Lines = append(s.Lines[:i], s.Lines[i+1:]...)
	kept := s.Bindings[:0]
	for _, b := range s.Bindings {
		if !strings.EqualFold(b.Line, name) {
			kept = append(kept, b)
		}
	}
	s.Bindings = kept
	// Its part's own layout goes with it.
	parts := s.Layout.Parts[:0]
	for _, p := range s.Layout.Parts {
		if !strings.EqualFold(p.Line, name) {
			parts = append(parts, p)
		}
	}
	s.Layout.Parts = parts
	return nil
}

// MoveLine moves line i to index to (the others close up around it).
func MoveLine(s *score.Score, i, to int) error {
	n := len(s.Lines)
	if i < 0 || i >= n {
		return fmt.Errorf("no line %d", i+1)
	}
	to = max(0, min(to, n-1))
	if to == i {
		return fmt.Errorf("the line is already there")
	}
	l := s.Lines[i]
	if to < i {
		copy(s.Lines[to+1:i+1], s.Lines[to:i])
	} else {
		copy(s.Lines[i:to], s.Lines[i+1:to+1])
	}
	s.Lines[to] = l
	return nil
}

// RenameLine renames line i. The name must be new to the score (lines are
// found by name); bindings that named the old name follow it.
func RenameLine(s *score.Score, i int, name string) error {
	if i < 0 || i >= len(s.Lines) {
		return fmt.Errorf("no line %d", i+1)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("a line needs a name")
	}
	old := s.Lines[i].Name
	if name == old {
		return fmt.Errorf("the line is already called %s", name)
	}
	if UniqueLineName(s, name, i) != name {
		return fmt.Errorf("another line is called %s", name)
	}
	for k := range s.Bindings {
		if strings.EqualFold(s.Bindings[k].Line, old) {
			s.Bindings[k].Line = name
		}
	}
	// The part's own layout follows, as the bindings do.
	for k := range s.Layout.Parts {
		if strings.EqualFold(s.Layout.Parts[k].Line, old) {
			s.Layout.Parts[k].Line = name
		}
	}
	s.Lines[i].Name = name
	return nil
}
