package edit

// transpose.go — moving a passage, or the whole score, to another pitch.
//
// Two kinds, as in Sibelius's Transpose dialog:
//
//   - Chromatic: every note by one interval, spelled as the interval
//     implies (C up a minor third is Eb), optionally with the key
//     signatures moved by it too. A key that would need more than seven
//     sharps or flats — or more than six where its enharmonic needs fewer
//     (C# major is written Db) — takes the enharmonic interval instead, for
//     the notes as well, so the notes still read in their new key.
//   - Diatonic: every note by a number of letters within its bar's key,
//     keeping its alteration against the key (in C major E up a step is F
//     and E-flat up a step F-flat; in G major F# up a step is G), the key
//     signatures as they are.
//
// Percussion staves are never transposed (their notes are drums). A note
// the transposition would take out of MIDI's range stays and is counted.
// Chord symbols over the passage move with it unless asked not to; a
// tablature's string choices are dropped (the hand is worked out again).

import (
	"fmt"

	"foundry/internal/score"
)

// Transposition is what Transpose does.
type Transposition struct {
	// Interval is the chromatic transposition.
	Interval score.Interval
	// Diatonic moves by Steps letters in the key instead.
	Diatonic bool
	Steps    int
	// Keys moves the key signatures in the passage by the interval
	// (chromatic only).
	Keys bool
	// NoChords leaves chord symbols as they are.
	NoChords bool
	// Doubles keeps double sharps and flats a chromatic transposition
	// spells; otherwise a note that would take one is respelled with a
	// single accidental (or none).
	Doubles bool
}

// TransposeResult says what Transpose did.
type TransposeResult struct {
	// Moved notes, Kept ones out of range.
	Moved, Kept int
	// Interval is the chromatic interval used: the one asked for, or its
	// enharmonic when a key needed it.
	Interval score.Interval
}

// lineOfFifths is each letter's place on the line of fifths (C 0, G 1 …,
// F -1).
var lineOfFifths = [7]int{0, 2, 4, -1, 1, 3, 5}

// FifthsOfPitch is a pitch name's place on the line of fifths: the
// signature of the major key on it (F# 6, Bb -2, B# 12).
func FifthsOfPitch(p score.Pitch) int { return lineOfFifths[p.Step] + 7*int(p.Alter) }

// IntervalFifths is how far an interval moves a key around the circle of
// fifths (up a major second: +2).
func IntervalFifths(iv score.Interval) int {
	return FifthsOfPitch(score.Pitch{Octave: 4}.Transpose(iv))
}

// KeyTonic is the tonic of a key signature in a mode (major, or minor's
// relative tonic), at octave 4.
func KeyTonic(fifths int, mode string) score.Pitch {
	f := fifths
	if mode == "minor" {
		f += 3
	}
	for s := 0; s < 7; s++ {
		if d := f - lineOfFifths[s]; d%7 == 0 {
			return score.Pitch{Step: int8(s), Alter: int8(d / 7), Octave: 4}
		}
	}
	return score.Pitch{Octave: 4}
}

// KeyInterval is the interval from one key's tonic to another's: dir 1
// up, -1 down, 0 the closer (up on a tritone).
func KeyInterval(fromFifths, toFifths int, mode string, dir int) score.Interval {
	a, b := KeyTonic(fromFifths, mode), KeyTonic(toFifths, mode)
	for b.MIDI() < a.MIDI() {
		b.Octave++
	}
	for b.MIDI()-a.MIDI() >= 12 {
		b.Octave--
	}
	up := score.IntervalBetween(a, b)
	down := up
	if up.Diatonic > 0 || up.Chromatic > 0 {
		down = score.Interval{Diatonic: up.Diatonic - 7, Chromatic: up.Chromatic - 12}
	}
	switch {
	case dir > 0:
		return up
	case dir < 0:
		return down
	case up.Chromatic <= 6:
		return up
	}
	return down
}

// enharmonic is an interval a diminished second away: the same sound, the
// next letter up (dir 1) or down.
func enharmonic(iv score.Interval, dir int) score.Interval {
	return score.Interval{Diatonic: iv.Diatonic + dir, Chromatic: iv.Chromatic}
}

// fitKeys picks the interval (as asked, or an enharmonic of it) whose keys
// in the passage read best: none past seven, the fewest accidentals.
func fitKeys(iv score.Interval, keys []int) score.Interval {
	best, bestCost := iv, -1
	for _, cand := range []score.Interval{iv, enharmonic(iv, 1), enharmonic(iv, -1)} {
		if !cand.Valid() {
			continue
		}
		cost := 0
		d := IntervalFifths(cand)
		for _, f := range keys {
			n := f + d
			if n < -7 || n > 7 {
				cost += 1000
			}
			cost += absI(n)
		}
		// The interval as asked wins a tie.
		if bestCost < 0 || cost < bestCost {
			best, bestCost = cand, cost
		}
	}
	return best
}

// singleAccidental respells a double sharp or flat with one accidental or
// none (Fx is G, Bbb is A).
func singleAccidental(p score.Pitch) score.Pitch {
	if p.Alter >= -1 && p.Alter <= 1 {
		return p
	}
	best := p
	for _, q := range Enharmonics(p) {
		if absI(int(q.Alter)) < absI(int(best.Alter)) {
			best = q
		}
	}
	return best
}

// diatonicStep moves a pitch n letters in a key, keeping its alteration
// against the key.
func diatonicStep(p score.Pitch, n, fifths int) score.Pitch {
	rel := int(p.Alter) - score.KeyAlter(fifths, int(p.Step))
	q := score.FromDiatonic(p.Diatonic() + n)
	a := score.KeyAlter(fifths, int(q.Step)) + rel
	q.Alter = int8(max(-2, min(2, a)))
	return q
}

// Transpose moves the notes of lines lo..hi in bars a..z.
func Transpose(s *score.Score, lo, hi, a, z int, t Transposition) (TransposeResult, error) {
	res := TransposeResult{Interval: t.Interval}
	if lo < 0 || hi >= len(s.Lines) || lo > hi {
		return res, fmt.Errorf("no lines %d-%d", lo+1, hi+1)
	}
	if a < 1 || z < a || z > s.Measures() {
		return res, fmt.Errorf("bars %d-%d are not in the score", a, z)
	}
	if !t.Diatonic && t.Interval == (score.Interval{}) || t.Diatonic && t.Steps == 0 {
		return res, fmt.Errorf("that transposition moves nothing")
	}
	if !t.Diatonic && !t.Interval.Valid() {
		return res, fmt.Errorf("%v is not an interval", t.Interval)
	}
	iv := t.Interval
	if !t.Diatonic && t.Keys {
		var keys []int
		keys = append(keys, s.KeyAt(a).Fifths)
		for _, k := range s.Key {
			if k.Bar > a && k.Bar <= z {
				keys = append(keys, k.Fifths)
			}
		}
		iv = fitKeys(iv, keys)
		res.Interval = iv
	}
	move := func(p score.Pitch, bar int) score.Pitch {
		if t.Diatonic {
			return diatonicStep(p, t.Steps, s.KeyAt(bar).Fifths)
		}
		q := p.Transpose(iv)
		if !t.Doubles {
			q = singleAccidental(q)
		}
		return q
	}
	for li := lo; li <= hi; li++ {
		for _, st := range s.Lines[li].Staves {
			if st.Percussion() {
				continue
			}
			for _, v := range st.Voices {
				for b := a; b <= z && b <= len(v.Measures); b++ {
					for i := range v.Measures[b-1].Events {
						ev := &v.Measures[b-1].Events[i]
						if ev.Kind != score.EventChord {
							continue
						}
						moved := make([]score.Note, len(ev.Notes))
						ok := true
						for k, n := range ev.Notes {
							n.Pitch = move(n.Pitch, b)
							if !Playable(n.Pitch) {
								ok = false
							}
							moved[k] = n
						}
						if !ok {
							res.Kept += len(ev.Notes)
							continue
						}
						ev.Notes = moved
						ev.SetTabStrings(nil)
						res.Moved += len(moved)
					}
				}
			}
		}
		if !t.NoChords {
			if t.Diatonic {
				diatonicChords(s, li, a, z, t.Steps)
			} else {
				TransposeChordSymbols(s, li, a, z, iv)
			}
		}
	}
	if !t.Diatonic && t.Keys {
		transposeKeys(s, a, z, IntervalFifths(iv))
	}
	return res, nil
}

// diatonicChords moves a line's chord symbols in bars a..z n letters in
// their bar's key.
func diatonicChords(s *score.Score, line, a, z, n int) {
	for i := range s.Lines[line].Chords {
		ch := &s.Lines[line].Chords[i]
		if ch.At.Bar < a || ch.At.Bar > z || ch.Chord.NoChord {
			continue
		}
		f := s.KeyAt(ch.At.Bar).Fifths
		mv := func(p score.Pitch) score.Pitch {
			p.Octave = 4
			q := diatonicStep(p, n, f)
			q.Octave = 0
			return q
		}
		c := ch.Chord.Clone()
		c.Root = mv(c.Root)
		if c.HasBass {
			c.Bass = mv(c.Bass)
		}
		ch.Chord = c
	}
}

// transposeKeys moves the key signatures of bars a..z by d fifths: the key
// in force at a gets a mark there, and the key after z is put back at z+1.
func transposeKeys(s *score.Score, a, z, d int) {
	after := s.KeyAt(z + 1)
	hasAfter := z < s.Measures()
	start := s.KeyAt(a)
	var keep []score.KeyMark
	for _, k := range s.Key {
		if k.Bar < a || k.Bar > z {
			keep = append(keep, k)
		}
	}
	marks := []score.KeyMark{{Bar: a, Fifths: start.Fifths + d, Mode: start.Mode}}
	for _, k := range s.Key {
		if k.Bar > a && k.Bar <= z {
			marks = append(marks, score.KeyMark{Bar: k.Bar, Fifths: k.Fifths + d, Mode: k.Mode})
		}
	}
	s.Key = append(keep, marks...)
	if hasAfter {
		has := false
		for _, k := range s.Key {
			has = has || k.Bar == z+1
		}
		if !has {
			s.Key = append(s.Key, score.KeyMark{Bar: z + 1, Fifths: after.Fifths, Mode: after.Mode})
		}
	}
	for i := range s.Key {
		s.Key[i].Fifths = max(-7, min(7, s.Key[i].Fifths))
		if s.Key[i].Mode == "" {
			s.Key[i].Mode = "major"
		}
	}
	s.SortMaps()
	dropRepeatedKeys(s)
}

// SpellInKey respells a pitch as the key spells its sound: the key's own
// note when it is one, else the simplest accidental — sharp when the line
// rises to it (dir > 0), flat when it falls.
func SpellInKey(p score.Pitch, fifths, dir int) score.Pitch {
	return score.SpellMIDI(p.MIDI(), fifths, dir)
}
