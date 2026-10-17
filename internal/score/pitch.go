package score

// pitch.go — spelled pitches, intervals and keys.
//
// A notated pitch is a letter, an alteration and an octave, not a number:
// F#4 and Gb4 are the same key on a piano and different notes on a page,
// and an editor that adds a third above a note, or writes what the key
// signature implies when a line is clicked, has to know which one it has.
// Pitches are stored spelled, at concert (sounding) pitch; MIDI() is the
// number playback wants. Octaves are scientific: C4 is middle C, MIDI 60,
// and the octave goes with the letter, so B#3 is MIDI 60 and Cb4 is 59.

import (
	"fmt"
	"strconv"
	"strings"
)

// Steps are the letter names in order; a Pitch's Step indexes this.
const Steps = "CDEFGAB"

// naturalSemis is each letter's semitone above C.
var naturalSemis = [7]int{0, 2, 4, 5, 7, 9, 11}

// Pitch is a spelled note.
type Pitch struct {
	// Step is the letter, 0 (C) .. 6 (B).
	Step int8
	// Alter is the accidental in semitones: -2 double flat .. 2 double sharp.
	Alter int8
	// Octave is scientific: 4 holds middle C.
	Octave int8
}

// MIDI is the pitch's MIDI note number (C4 = 60).
func (p Pitch) MIDI() int {
	return (int(p.Octave)+1)*12 + naturalSemis[p.Step] + int(p.Alter)
}

// Diatonic is the pitch's staff position counted in letters from C0 — what
// sets its line or space on a staff, whatever its accidental.
func (p Pitch) Diatonic() int { return int(p.Octave)*7 + int(p.Step) }

// FromDiatonic is the natural pitch at a diatonic position.
func FromDiatonic(d int) Pitch {
	oct := floorDiv(d, 7)
	return Pitch{Step: int8(d - oct*7), Octave: int8(oct)}
}

// Letter is the pitch's letter name.
func (p Pitch) Letter() byte { return Steps[p.Step] }

// String spells the pitch as the voice grammar writes it: "Bb4", "F#5",
// "C##4", "Ebb3", "C-1".
func (p Pitch) String() string {
	var b strings.Builder
	b.WriteByte(Steps[p.Step])
	writeAlter(&b, int(p.Alter))
	b.WriteString(strconv.Itoa(int(p.Octave)))
	return b.String()
}

func writeAlter(b *strings.Builder, a int) {
	for ; a > 0; a-- {
		b.WriteByte('#')
	}
	for ; a < 0; a++ {
		b.WriteByte('b')
	}
}

// ParsePitch reads a whole pitch as String writes it.
func ParsePitch(s string) (Pitch, error) {
	p, n, err := scanPitch(s)
	if err != nil {
		return Pitch{}, err
	}
	if n != len(s) {
		return Pitch{}, fmt.Errorf("bad pitch %q", s)
	}
	return p, nil
}

// scanPitch reads a pitch at the start of s and says how many bytes it
// took: an upper-case letter, accidentals (# b, x for a double sharp, n for
// a natural), then the octave, which may be -1.
func scanPitch(s string) (Pitch, int, error) {
	if s == "" {
		return Pitch{}, 0, fmt.Errorf("expected a pitch")
	}
	step := strings.IndexByte(Steps, s[0])
	if step < 0 {
		return Pitch{}, 0, fmt.Errorf("bad pitch letter %q", s[:1])
	}
	i := 1
	alter := 0
	natural, sharp, flat := false, false, false
scan:
	for i < len(s) {
		switch s[i] {
		case '#':
			alter, sharp = alter+1, true
		case 'x':
			alter, sharp = alter+2, true
		case 'b':
			alter, flat = alter-1, true
		case 'n':
			natural = true
		default:
			break scan
		}
		i++
	}
	if sharp && flat {
		return Pitch{}, 0, fmt.Errorf("pitch %q mixes sharps and flats", s[:i])
	}
	if natural && (sharp || flat) {
		return Pitch{}, 0, fmt.Errorf("pitch %q mixes a natural with other accidentals", s[:i])
	}
	if alter < -2 || alter > 2 {
		return Pitch{}, 0, fmt.Errorf("pitch %q: at most a double sharp or flat", s[:i])
	}
	j := i
	if j < len(s) && s[j] == '-' {
		j++
	}
	k := j
	for k < len(s) && s[k] >= '0' && s[k] <= '9' {
		k++
	}
	if k == j {
		return Pitch{}, 0, fmt.Errorf("pitch %q has no octave", s[:min(len(s), k+1)])
	}
	oct, err := strconv.Atoi(s[i:k])
	if err != nil || oct < -1 || oct > 9 {
		return Pitch{}, 0, fmt.Errorf("pitch %q: octave out of range (-1..9)", s[:k])
	}
	return Pitch{Step: int8(step), Alter: int8(alter), Octave: int8(oct)}, k, nil
}

// KeyAlter is the alteration a key signature of fifths sharps (negative:
// flats) gives a letter: in G major (1) F is +1, in B-flat (-2) B and E are -1.
func KeyAlter(fifths, step int) int {
	const sharpOrder, flatOrder = "FCGDAEB", "BEADGCF"
	l := Steps[step]
	if fifths > 0 {
		if i := strings.IndexByte(sharpOrder, l); i < fifths {
			return 1 + (fifths-1-i)/7
		}
	}
	if fifths < 0 {
		if i := strings.IndexByte(flatOrder, l); i < -fifths {
			return -1 - (-fifths-1-i)/7
		}
	}
	return 0
}

// InKey is the pitch at a staff position as the key signature spells it —
// what a click on a line writes (F#4 on the F line in G major).
func InKey(diatonic, fifths int) Pitch {
	p := FromDiatonic(diatonic)
	p.Alter = int8(KeyAlter(fifths, int(p.Step)))
	return p
}

// SpellMIDI spells a MIDI note in a key: the key's own spelling when the
// note is in it, otherwise the simplest accidental, sharps when dir > 0
// (a line going up) and flats when dir < 0.
func SpellMIDI(midi, fifths, dir int) Pitch {
	pc := ((midi % 12) + 12) % 12
	best, bestScore := Pitch{}, 1<<30
	for s := 0; s < 7; s++ {
		alt := pc - naturalSemis[s]
		if alt > 6 {
			alt -= 12
		}
		if alt < -6 {
			alt += 12
		}
		if alt < -2 || alt > 2 {
			continue
		}
		score := 0
		if alt != KeyAlter(fifths, s) {
			score = 2*absInt(alt) + 1
			if alt != 0 && (alt > 0) != (dir > 0 || (dir == 0 && fifths >= 0)) {
				score++
			}
		}
		if score < bestScore {
			oct := floorDiv(midi-naturalSemis[s]-alt, 12) - 1
			best, bestScore = Pitch{Step: int8(s), Alter: int8(alt), Octave: int8(oct)}, score
		}
	}
	return best
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Interval is a distance between spelled pitches: letters and semitones.
// A major third up is {2, 4}; a minor sixth down {-5, -8}; a written-to-
// sounding transposition for a B-flat clarinet {-1, -2}.
type Interval struct {
	Diatonic  int
	Chromatic int
}

// Transpose moves a pitch by an interval, keeping the spelling the
// interval implies (C4 up a minor third is Eb4, not D#4).
//
// A spelling that would need more than a double sharp or flat (G## up an
// augmented second) takes the next letter instead, so a transposed pitch
// can always be written.
func (p Pitch) Transpose(iv Interval) Pitch {
	d := p.Diatonic() + iv.Diatonic
	midi := p.MIDI() + iv.Chromatic
	for {
		q := FromDiatonic(d)
		alter := midi - q.MIDI()
		switch {
		case alter > 2:
			d++
		case alter < -2:
			d--
		default:
			q.Alter = int8(alter)
			return q
		}
	}
}

// Valid reports an interval that can be named: at most doubly augmented
// or doubly diminished.
func (iv Interval) Valid() bool {
	d, c := iv.Diatonic, iv.Chromatic
	if d < 0 || (d == 0 && c < 0) {
		d, c = -d, -c
	}
	diff := c - (majorSemis[d%7] + 12*(d/7))
	if perfectClass(d) {
		return diff >= -2 && diff <= 2
	}
	return diff >= -3 && diff <= 2
}

// IntervalBetween is the interval from a to b.
func IntervalBetween(a, b Pitch) Interval {
	return Interval{Diatonic: b.Diatonic() - a.Diatonic(), Chromatic: b.MIDI() - a.MIDI()}
}

// perfectNumbers are the interval numbers (mod 7, 0-based) that are
// perfect rather than major/minor: unison, fourth, fifth.
func perfectClass(d int) bool { d = ((d % 7) + 7) % 7; return d == 0 || d == 3 || d == 4 }

// majorSemis is the semitones of the major or perfect interval of each
// number in an octave, 0-based (unison 0 .. seventh 11).
var majorSemis = [7]int{0, 2, 4, 5, 7, 9, 11}

// String writes the interval as "+M3", "-P5", "+P8", "-m2", "+A4", "P1".
func (iv Interval) String() string {
	if iv.Diatonic == 0 && iv.Chromatic == 0 {
		return "P1"
	}
	sign := "+"
	d, c := iv.Diatonic, iv.Chromatic
	if d < 0 || (d == 0 && c < 0) {
		sign, d, c = "-", -d, -c
	}
	ref := majorSemis[d%7] + 12*(d/7)
	diff := c - ref
	var q string
	if perfectClass(d) {
		switch {
		case diff == 0:
			q = "P"
		case diff > 0:
			q = strings.Repeat("A", diff)
		default:
			q = strings.Repeat("d", -diff)
		}
	} else {
		switch {
		case diff == 0:
			q = "M"
		case diff == -1:
			q = "m"
		case diff > 0:
			q = strings.Repeat("A", diff)
		default:
			q = strings.Repeat("d", -diff-1)
		}
	}
	return sign + q + strconv.Itoa(d+1)
}

// ParseInterval reads what String writes. The sign is optional (up).
func ParseInterval(s string) (Interval, error) {
	t := strings.TrimSpace(s)
	sign := 1
	switch {
	case strings.HasPrefix(t, "+"):
		t = t[1:]
	case strings.HasPrefix(t, "-"):
		sign, t = -1, t[1:]
	}
	i := 0
	for i < len(t) && strings.IndexByte("PMmAd", t[i]) >= 0 {
		i++
	}
	q, numS := t[:i], t[i:]
	n, err := strconv.Atoi(numS)
	if err != nil || n < 1 || q == "" {
		return Interval{}, fmt.Errorf("bad interval %q (want e.g. -M2, +P8, m3)", s)
	}
	d := n - 1
	ref := majorSemis[d%7] + 12*(d/7)
	perfect := perfectClass(d)
	var diff int
	switch {
	case q == "P" && perfect:
		diff = 0
	case q == "M" && !perfect:
		diff = 0
	case q == "m" && !perfect:
		diff = -1
	case strings.Trim(q, "A") == "":
		diff = len(q)
	case strings.Trim(q, "d") == "":
		diff = -len(q)
		if !perfect {
			diff--
		}
	default:
		return Interval{}, fmt.Errorf("bad interval %q: %s does not go with a %d", s, q, n)
	}
	iv := Interval{Diatonic: sign * d, Chromatic: sign * (ref + diff)}
	if !iv.Valid() {
		return Interval{}, fmt.Errorf("bad interval %q: at most doubly augmented or diminished", s)
	}
	return iv, nil
}

// ---------- durations ----------

// DurBase is a note value as a power of two of a whole note: Breve is -1,
// Whole 0, Half 1 … D256 8.
type DurBase int8

const (
	Breve DurBase = iota - 1
	WholeNote
	Half
	Quarter
	Eighth
	D16
	D32
	D64
	D128
	D256
)

// durLetters are the voice grammar's duration letters, Breve first.
const durLetters = "bwhqestxyz"

// Duration is a written note value: a base and up to three dots.
type Duration struct {
	Base DurBase
	Dots uint8
}

// Value is the written length in whole notes: a dotted quarter is 3/8.
func (d Duration) Value() Frac {
	v := F(1, 1)
	if d.Base < 0 {
		v = Whole(2)
	} else {
		v = F(1, int64(1)<<uint(d.Base))
	}
	// Each dot adds half the last: x (2 - 1/2^dots).
	if d.Dots > 0 {
		v = v.Mul(F(int64(1)<<(d.Dots+1)-1, int64(1)<<d.Dots))
	}
	return v
}

// Letter is the grammar's letter for the base.
func (d Duration) Letter() byte { return durLetters[int(d.Base)+1] }

// String writes the duration as the grammar does: "q", "q.", "e..".
func (d Duration) String() string {
	return string(d.Letter()) + strings.Repeat(".", int(d.Dots))
}

// DurationFor finds the plain or dotted value that is exactly v, if any.
func DurationFor(v Frac) (Duration, bool) {
	for b := Breve; b <= D256; b++ {
		for dots := uint8(0); dots <= 3; dots++ {
			d := Duration{Base: b, Dots: dots}
			if d.Value().Eq(v) {
				return d, true
			}
		}
	}
	return Duration{}, false
}
