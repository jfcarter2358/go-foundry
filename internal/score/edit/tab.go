package edit

// tab.go — tablature: giving a staff its strings, and writing frets.
//
// A tablature is a view of a staff's notes (score.Tab), so writing on it
// is writing notes: a fret on a string is the pitch that string sounds
// there, spelled in the key, with the string kept (the str= mark) so the
// tablature shows it where it was written. Moving a note to another string
// keeps its pitch and changes its fret.

import (
	"fmt"

	"foundry/internal/score"
)

// SetTab gives a staff a tablature (nil takes it off). A percussion staff
// has none.
func SetTab(s *score.Score, line, staff int, t *score.Tab) error {
	st := Staff(s, line, staff)
	if st == nil {
		return fmt.Errorf("no staff %d of line %d", staff+1, line+1)
	}
	if t != nil {
		if st.Percussion() {
			return fmt.Errorf("a percussion staff has no tablature")
		}
		if n := t.Strings(); n < 1 || n > score.MaxStrings {
			return fmt.Errorf("a tablature has 1 to %d strings", score.MaxStrings)
		}
		if t.Capo < 0 || t.Capo > 12 {
			return fmt.Errorf("a capo goes at frets 0 to 12")
		}
		t = t.Clone()
	}
	st.Tab = t
	return nil
}

// FretPitch is the pitch a string sounds at a fret, spelled in a key.
func FretPitch(t *score.Tab, str, fret, fifths int) (score.Pitch, error) {
	if t == nil {
		return score.Pitch{}, fmt.Errorf("the staff has no tablature")
	}
	if str < 1 || str > t.Strings() {
		return score.Pitch{}, fmt.Errorf("the tablature has strings 1 to %d", t.Strings())
	}
	if fret < 0 || fret > score.MaxFret-t.Capo {
		return score.Pitch{}, fmt.Errorf("frets run 0 to %d", score.MaxFret-t.Capo)
	}
	return score.SpellMIDI(t.Open(str)+fret, fifths, 0), nil
}

// TabPlaces is where the notes of the event at l stand on its staff's
// tablature, per note, as the tablature shows them (the voice walked from
// its start: score.Tab.VoicePlaces); nil without a tablature.
func TabPlaces(s *score.Score, l Loc) []score.TabNote {
	st := Staff(s, l.Line, l.Staff)
	v := Voice(s, l.Line, l.Staff, l.Voice)
	if st == nil || st.Tab == nil || v == nil || Event(s, l) == nil {
		return nil
	}
	ps := st.Tab.VoicePlaces(v)
	if l.Bar < 1 || l.Bar > len(ps) || l.Event >= len(ps[l.Bar-1]) {
		return nil
	}
	return ps[l.Bar-1][l.Event]
}

// SetFret writes a fret on a string into the chord at l: the note shown on
// that string takes the fret's pitch, or (none there) a note is added; the
// string is kept on the note. It returns the note's index.
func SetFret(s *score.Score, l Loc, str, fret, fifths int) (int, error) {
	st := Staff(s, l.Line, l.Staff)
	if st == nil || st.Tab == nil {
		return 0, fmt.Errorf("the staff has no tablature")
	}
	e, err := chordAt(s, l)
	if err != nil {
		return 0, err
	}
	p, err := FretPitch(st.Tab, str, fret, fifths)
	if err != nil {
		return 0, err
	}
	if !Playable(p) {
		return 0, fmt.Errorf("%s is out of range", p)
	}
	k := -1
	for i, at := range TabPlaces(s, l) {
		if at.OK && at.String == str {
			k = i
		}
	}
	// The other notes keep their strings through the change.
	strs := e.TabStrings()
	byPitch := map[score.Pitch]int{}
	for i, n := range e.Notes {
		if i != k {
			byPitch[n.Pitch] = strs[i]
		}
	}
	if k >= 0 {
		for i, n := range e.Notes {
			if i != k && n.Pitch.MIDI() == p.MIDI() {
				return 0, fmt.Errorf("the chord already has %s", n.Pitch)
			}
		}
		e.Notes[k].Pitch = p
		k = sortNotes(e, k)
	} else {
		if k, err = AddPitch(s, l, p); err != nil {
			return 0, err
		}
	}
	next := make([]int, len(e.Notes))
	for i, n := range e.Notes {
		next[i] = byPitch[n.Pitch]
	}
	next[k] = str
	e.SetTabStrings(next)
	return k, nil
}

// SetTabString puts one note of the chord at l on a string, keeping its
// pitch (its fret changes). 0 lets the tablature choose again.
func SetTabString(s *score.Score, l Loc, note, str int) error {
	st := Staff(s, l.Line, l.Staff)
	if st == nil || st.Tab == nil {
		return fmt.Errorf("the staff has no tablature")
	}
	e, err := chordAt(s, l)
	if err != nil {
		return err
	}
	if note < 0 || note >= len(e.Notes) {
		return fmt.Errorf("no note %d in the chord", note+1)
	}
	if str != 0 {
		if _, ok := st.Tab.Fret(e.Notes[note].Pitch, str); !ok {
			return fmt.Errorf("%s cannot be played on string %d", e.Notes[note].Pitch, str)
		}
		for i, at := range TabPlaces(s, l) {
			if i != note && at.OK && at.String == str {
				// The note there moves out of the way.
				e.SetTabString(i, 0)
			}
		}
	}
	e.SetTabString(note, str)
	return nil
}
