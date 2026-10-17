package edit

// pitch.go — the notes of a chord: adding pitches and intervals spelled in
// the key, accidentals, moving by a semitone, a step or an octave, and
// respelling.
//
// Pitches are concert pitch, spelled (score.Pitch), and the key given is the
// concert key. On a transposing staff that is still right: a third above in
// the written key is a third above in the concert key, and the spelling a
// key implies moves with the transposition.

import (
	"fmt"
	"sort"

	"foundry/internal/score"
)

// AccKind is an accidental key: sharp, flat or natural.
type AccKind uint8

const (
	Sharp AccKind = iota
	Flat
	Natural
)

// chordAt is the chord at l.
func chordAt(s *score.Score, l Loc) (*score.Event, error) {
	e := Event(s, l)
	if e == nil {
		return nil, ErrNoEvent
	}
	if e.Kind != score.EventChord {
		return nil, fmt.Errorf("that is a rest, not a note")
	}
	return e, nil
}

// Playable reports a pitch the score can hold: MIDI 0..127, octaves -1..9.
func Playable(p score.Pitch) bool {
	m := p.MIDI()
	return m >= 0 && m <= 127 && p.Octave >= -1 && p.Octave <= 9
}

// sortNotes orders a chord bottom to top (by staff position, then pitch)
// and returns where the note that was at index k went.
func sortNotes(e *score.Event, k int) int {
	type tagged struct {
		n score.Note
		i int
	}
	ts := make([]tagged, len(e.Notes))
	for i, n := range e.Notes {
		ts[i] = tagged{n, i}
	}
	sort.SliceStable(ts, func(a, b int) bool {
		pa, pb := ts[a].n.Pitch, ts[b].n.Pitch
		if pa.Diatonic() != pb.Diatonic() {
			return pa.Diatonic() < pb.Diatonic()
		}
		return pa.MIDI() < pb.MIDI()
	})
	at := -1
	for i, t := range ts {
		e.Notes[i] = t.n
		if t.i == k {
			at = i
		}
	}
	return at
}

// AddPitch adds a pitch to a chord and returns its index in it. The same
// pitch twice is refused.
func AddPitch(s *score.Score, l Loc, p score.Pitch) (int, error) {
	e, err := chordAt(s, l)
	if err != nil {
		return -1, err
	}
	if !Playable(p) {
		return -1, fmt.Errorf("%s is out of range", p)
	}
	for _, n := range e.Notes {
		if n.Pitch == p {
			return -1, fmt.Errorf("%s is already in the chord", p)
		}
	}
	e.Notes = append(e.Notes, score.Note{Pitch: p})
	return sortNotes(e, len(e.Notes)-1), nil
}

// IntervalFrom is the pitch n letters above p (below when below), spelled
// as the key spells that letter: a third above D in G major is F#, a
// second below C is B. n is the interval's number: 2 a second, 8 an octave.
func IntervalFrom(p score.Pitch, n int, below bool, fifths int) score.Pitch {
	d := n - 1
	if below {
		d = -d
	}
	return score.InKey(p.Diatonic()+d, fifths)
}

// AddInterval adds a diatonic interval above (or below) a note of a chord —
// note k, or with k < 0 the top note (the bottom one, below) — spelled in
// the key, and returns the new note's index and pitch. A letter the chord
// already has is refused.
func AddInterval(s *score.Score, l Loc, k, n int, below bool, fifths int) (int, score.Pitch, error) {
	e, err := chordAt(s, l)
	if err != nil {
		return -1, score.Pitch{}, err
	}
	if n < 2 || n > 9 {
		return -1, score.Pitch{}, fmt.Errorf("intervals are a second to a ninth")
	}
	if k < 0 || k >= len(e.Notes) {
		k = len(e.Notes) - 1
		if below {
			k = 0
		}
	}
	p := IntervalFrom(e.Notes[k].Pitch, n, below, fifths)
	for _, q := range e.Notes {
		if q.Pitch.Diatonic() == p.Diatonic() {
			return -1, p, fmt.Errorf("%s is already in the chord", PitchName(q.Pitch))
		}
	}
	i, err := AddPitch(s, l, p)
	return i, p, err
}

// Transform changes a note of a chord (every note, k < 0) by f, keeps the
// chord in order without doubled pitches, and returns where note k went.
// A pitch f takes out of range refuses the whole change.
func Transform(s *score.Score, l Loc, k int, f func(score.Pitch) score.Pitch) (int, error) {
	e, err := chordAt(s, l)
	if err != nil {
		return -1, err
	}
	notes := append([]score.Note(nil), e.Notes...)
	for i := range notes {
		if k >= 0 && i != k {
			continue
		}
		p := f(notes[i].Pitch)
		if !Playable(p) {
			return k, fmt.Errorf("%s is out of range", p)
		}
		notes[i].Pitch = p
	}
	// Drop a pitch the change doubled, keeping the moved note.
	seen := map[score.Pitch]bool{}
	var kept []score.Note
	newK := -1
	if k >= 0 && k < len(notes) {
		seen[notes[k].Pitch] = true
	}
	for i, n := range notes {
		if i == k {
			newK = len(kept)
			kept = append(kept, n)
			continue
		}
		if seen[n.Pitch] {
			continue
		}
		seen[n.Pitch] = true
		kept = append(kept, n)
	}
	e.Notes = kept
	return sortNotes(e, newK), nil
}

// Accidental is what the sharp, flat and natural keys do to a pitch: a
// sharp on a sharp makes a double sharp (the "second press"), a flat on a
// flat a double flat, a natural takes the accidental off.
func Accidental(p score.Pitch, k AccKind) score.Pitch {
	switch k {
	case Sharp:
		if p.Alter == 1 {
			p.Alter = 2
		} else {
			p.Alter = 1
		}
	case Flat:
		if p.Alter == -1 {
			p.Alter = -2
		} else {
			p.Alter = -1
		}
	default:
		p.Alter = 0
	}
	return p
}

// Semitone moves a pitch a semitone up (dir > 0) or down, spelled as the
// key would, sharps going up and flats coming down.
func Semitone(p score.Pitch, dir, fifths int) score.Pitch {
	if dir == 0 {
		return p
	}
	return score.SpellMIDI(p.MIDI()+dir, fifths, dir)
}

// Step moves a pitch to the next letter up or down, as the key spells it.
func Step(p score.Pitch, dir, fifths int) score.Pitch {
	return score.InKey(p.Diatonic()+dir, fifths)
}

// Octave moves a pitch an octave, spelling kept.
func Octave(p score.Pitch, dir int) score.Pitch {
	p.Octave += int8(dir)
	return p
}

// Enharmonics are the spellings of a pitch's sound, lowest letter first:
// F#4 is E##4, F#4 and Gb4.
func Enharmonics(p score.Pitch) []score.Pitch {
	midi := p.MIDI()
	var out []score.Pitch
	for d := p.Diatonic() - 2; d <= p.Diatonic()+2; d++ {
		q := score.FromDiatonic(d)
		alt := midi - q.MIDI()
		if alt < -2 || alt > 2 {
			continue
		}
		q.Alter = int8(alt)
		out = append(out, q)
	}
	return out
}

// Respell is the next spelling of the same sound (the Return key): between
// the single accidentals where a pitch has two (F# and Gb, C and B#),
// through the double ones where it has no other (D, C## and Ebb).
func Respell(p score.Pitch) score.Pitch {
	all := Enharmonics(p)
	var single []score.Pitch
	for _, q := range all {
		if q.Alter >= -1 && q.Alter <= 1 {
			single = append(single, q)
		}
	}
	cands := all
	if len(single) > 1 && p.Alter >= -1 && p.Alter <= 1 {
		cands = single
	}
	for i, q := range cands {
		if q == p {
			return cands[(i+1)%len(cands)]
		}
	}
	return p
}

// NearestLetter is a letter (0 = C .. 6 = B) in the octave that puts it
// closest to the note before (within a fourth), spelled in the key — what
// typing a letter writes. With no note before it is the octave from middle C.
func NearestLetter(step int, prev *score.Pitch, fifths int) score.Pitch {
	ref := score.Pitch{Step: 0, Octave: 4}.Diatonic() + 3 // around F4..G4
	if prev != nil {
		ref = prev.Diatonic()
	}
	best := 0
	for oct := -1; oct <= 9; oct++ {
		d := oct*7 + step
		if absI(d-ref) < absI(best-ref) || oct == -1 {
			best = d
		}
	}
	p := score.InKey(best, fifths)
	for !Playable(p) && p.Octave > -1 {
		p.Octave--
	}
	for !Playable(p) && p.Octave < 9 {
		p.Octave++
	}
	return p
}

func absI(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// PitchName is a pitch as a musician reads it, in plain text (the editor's
// font has no accidentals): "F#5", "Bb3", "C##4", "C4".
func PitchName(p score.Pitch) string {
	acc := map[int8]string{-2: "bb", -1: "b", 1: "#", 2: "##"}[p.Alter]
	return fmt.Sprintf("%c%s%d", p.Letter(), acc, p.Octave)
}
