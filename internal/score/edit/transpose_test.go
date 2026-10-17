package edit

import (
	"testing"

	"foundry/internal/score"
)

func iv(s string) score.Interval {
	i, err := score.ParseInterval(s)
	if err != nil {
		panic(err)
	}
	return i
}

// Chromatic: the interval's spelling; the key signatures with it on
// request, put back after the passage.
func TestTransposeChromatic(t *testing.T) {
	s := one(t, "4/4", 0, "| qC4 qE4 qG4 qBb4 | qC4 qE4 qG4 qB4 | wC5 |")
	r, err := Transpose(s, 0, 0, 1, 2, Transposition{Interval: iv("+M2"), Keys: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Moved != 8 || r.Kept != 0 || r.Interval != iv("+M2") {
		t.Errorf("result %+v", r)
	}
	if got := barText(s, 0, 1); got != "qD4 qF#4 qA4 qC5" {
		t.Errorf("bar 1 = %q", got)
	}
	if got := barText(s, 0, 3); got != "wC5" {
		t.Errorf("bar 3 is outside the passage: %q", got)
	}
	if k := s.KeyAt(1); k.Fifths != 2 {
		t.Errorf("key at 1: %+v", k)
	}
	if k := s.KeyAt(3); k.Fifths != 0 {
		t.Errorf("the key after the passage is put back: %+v", k)
	}
	fullBars(t, s)
}

// A key past seven (or six with a simpler enharmonic) takes the enharmonic
// interval, notes and all: Db major up a minor second is D major, not Ebb.
func TestTransposeFitsTheKey(t *testing.T) {
	s := one(t, "4/4", -5, "| qDb4 qF4 qAb4 qC5 |")
	r, err := Transpose(s, 0, 0, 1, 1, Transposition{Interval: iv("+m2"), Keys: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Interval != iv("+A1") {
		t.Errorf("interval %v, want the augmented unison", r.Interval)
	}
	if got := barText(s, 0, 1); got != "qD4 qF#4 qA4 qC#5" {
		t.Errorf("bar 1 = %q", got)
	}
	if k := s.KeyAt(1); k.Fifths != 2 {
		t.Errorf("key %+v", k)
	}
	// Without the keys the interval is as asked.
	s = one(t, "4/4", -5, "| qDb4 qF4 qAb4 qC5 |")
	r, _ = Transpose(s, 0, 0, 1, 1, Transposition{Interval: iv("+m2")})
	if r.Interval != iv("+m2") || barText(s, 0, 1) != "qD4 qGb4 qA4 qDb5" {
		t.Errorf("no keys: %v %q", r.Interval, barText(s, 0, 1))
	}
}

// Double sharps and flats are respelled unless kept.
func TestTransposeDoubles(t *testing.T) {
	s := one(t, "4/4", 0, "| qF#4 qBb4 hr |")
	Transpose(s, 0, 0, 1, 1, Transposition{Interval: iv("+A1")})
	if got := barText(s, 0, 1); got != "qG4 qB4 hr" {
		t.Errorf("respelled: %q", got)
	}
	s = one(t, "4/4", 0, "| qF#4 qBb4 hr |")
	Transpose(s, 0, 0, 1, 1, Transposition{Interval: iv("+A1"), Doubles: true})
	if got := barText(s, 0, 1); got != "qF##4 qB4 hr" {
		t.Errorf("kept: %q", got)
	}
}

// Diatonic: letters in the key, the alteration against it kept; chord
// symbols move the same way; percussion stays.
func TestTransposeDiatonic(t *testing.T) {
	s := one(t, "4/4", 1, "| qF#4 qG4 qC5 qF4 |")
	s.Lines[0].Chords = []score.ChordSymbol{{At: score.Pos{Bar: 1}, Chord: mustChord(t, "G")}, {At: score.Pos{Bar: 1, Offset: score.F(1, 2)}, Chord: mustChord(t, "D7/F#")}}
	drums := score.NewLine("Drums", "drums/kit", "percussion")
	dv, _ := score.ParseVoice("| qC2 qD2 qF#2 qr |")
	drums.Staves[0].Voices = []*score.Voice{dv}
	s.Lines = append(s.Lines, drums)
	if _, err := Transpose(s, 0, 1, 1, 1, Transposition{Diatonic: true, Steps: 2}); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "qA4 qB4 qE5 qAb4" {
		t.Errorf("bar 1 = %q", got)
	}
	if got := score.FormatMeasure(s.Lines[1].Staves[0].Voices[0].Measure(1)); got != "qC2 qD2 qF#2 qr" {
		t.Errorf("drums moved: %q", got)
	}
	if a, b := s.Lines[0].Chords[0].Chord.String(), s.Lines[0].Chords[1].Chord.String(); a != "B" || b != "F#7/A" {
		t.Errorf("chords %s %s", a, b)
	}
	if s.KeyAt(1).Fifths != 1 {
		t.Error("a diatonic transposition keeps the key")
	}
}

func mustChord(t *testing.T, s string) score.Chord {
	t.Helper()
	c, err := score.ParseChord(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Out of range stays; nothing to move and bad ranges are refused.
func TestTransposeEdges(t *testing.T) {
	s := one(t, "4/4", 0, "| qG9 qC4 hr |")
	r, err := Transpose(s, 0, 0, 1, 1, Transposition{Interval: iv("+P8")})
	if err != nil {
		t.Fatal(err)
	}
	if r.Kept != 1 || r.Moved != 1 || barText(s, 0, 1) != "qG9 qC5 hr" {
		t.Errorf("%+v %q", r, barText(s, 0, 1))
	}
	if _, err := Transpose(s, 0, 0, 1, 1, Transposition{}); err == nil {
		t.Error("a unison moves nothing")
	}
	if _, err := Transpose(s, 0, 3, 1, 1, Transposition{Interval: iv("+M2")}); err == nil {
		t.Error("no such lines")
	}
	if _, err := Transpose(s, 0, 0, 1, 9, Transposition{Interval: iv("+M2")}); err == nil {
		t.Error("no such bars")
	}
}

func TestKeysAndIntervals(t *testing.T) {
	for _, c := range []struct {
		p    string
		want int
	}{{"C4", 0}, {"F#4", 6}, {"Bb4", -2}, {"B#4", 12}, {"Fb4", -8}} {
		p, _ := score.ParsePitch(c.p)
		if got := FifthsOfPitch(p); got != c.want {
			t.Errorf("FifthsOfPitch(%s) = %d, want %d", c.p, got, c.want)
		}
	}
	if got := KeyTonic(0, "minor").String(); got != "A4" {
		t.Errorf("A minor's tonic %s", got)
	}
	if got := KeyTonic(-3, "major").String(); got != "Eb4" {
		t.Errorf("Eb major's tonic %s", got)
	}
	for _, c := range []struct {
		from, to, dir int
		want          string
	}{
		{0, -3, 1, "+m3"}, {0, -3, -1, "-M6"}, {0, -3, 0, "+m3"},
		{0, 3, 0, "-m3"}, {0, 3, 1, "+M6"}, {2, 2, 0, "P1"}, {0, 6, 0, "+A4"},
	} {
		if got := KeyInterval(c.from, c.to, "major", c.dir).String(); got != c.want {
			t.Errorf("KeyInterval(%d, %d, %d) = %s, want %s", c.from, c.to, c.dir, got, c.want)
		}
	}
	if d := IntervalFifths(iv("+M2")); d != 2 {
		t.Errorf("up a major second moves a key %d fifths", d)
	}
}
