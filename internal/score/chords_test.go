package score

import (
	"math/rand"
	"reflect"
	"testing"
)

// Every common way of typing a chord reads as its one spelling, with the
// notes a player would put in it.
func TestParseChordSpellings(t *testing.T) {
	cases := []struct {
		in, out string
		tones   []int
	}{
		{"C", "C", []int{0, 4, 7}},
		{"Cm", "Cm", []int{0, 3, 7}},
		{"Cmin", "Cm", []int{0, 3, 7}},
		{"C-", "Cm", []int{0, 3, 7}},
		{"Cmi7", "Cm7", []int{0, 3, 7, 10}},
		{"C-7", "Cm7", []int{0, 3, 7, 10}},
		{"C7", "C7", []int{0, 4, 7, 10}},
		{"Cmaj7", "Cmaj7", []int{0, 4, 7, 11}},
		{"CM7", "Cmaj7", []int{0, 4, 7, 11}},
		{"CΔ", "Cmaj7", []int{0, 4, 7, 11}},
		{"CΔ7", "Cmaj7", []int{0, 4, 7, 11}},
		{"Cma9", "Cmaj9", []int{0, 4, 7, 11, 14}},
		{"Cmaj", "C", []int{0, 4, 7}},
		{"Cm(maj7)", "Cmmaj7", []int{0, 3, 7, 11}},
		{"CmM7", "Cmmaj7", []int{0, 3, 7, 11}},
		{"C-Δ7", "Cmmaj7", []int{0, 3, 7, 11}},
		{"Cdim", "Cdim", []int{0, 3, 6}},
		{"C°", "Cdim", []int{0, 3, 6}},
		{"Co7", "Cdim7", []int{0, 3, 6, 9}},
		{"Cdim7", "Cdim7", []int{0, 3, 6, 9}},
		{"Cm7b5", "Cm7b5", []int{0, 3, 6, 10}},
		{"Cø", "Cm7b5", []int{0, 3, 6, 10}},
		{"Cø7", "Cm7b5", []int{0, 3, 6, 10}},
		{"Cmi7(♭5)", "Cm7b5", []int{0, 3, 6, 10}},
		{"Caug", "Caug", []int{0, 4, 8}},
		{"C+", "Caug", []int{0, 4, 8}},
		{"C+7", "Caug7", []int{0, 4, 8, 10}},
		{"C7#5", "C7#5", []int{0, 4, 8, 10}},
		{"C7+5", "C7#5", []int{0, 4, 8, 10}},
		{"Csus", "Csus4", []int{0, 5, 7}},
		{"Csus2", "Csus2", []int{0, 2, 7}},
		{"C7sus4", "C7sus4", []int{0, 5, 7, 10}},
		{"C9sus", "C9sus4", []int{0, 5, 7, 10, 14}},
		{"C5", "C5", []int{0, 7}},
		{"C6", "C6", []int{0, 4, 7, 9}},
		{"Cm6", "Cm6", []int{0, 3, 7, 9}},
		{"C6/9", "C6/9", []int{0, 4, 7, 9, 14}},
		{"C69", "C6/9", []int{0, 4, 7, 9, 14}},
		{"C9", "C9", []int{0, 4, 7, 10, 14}},
		{"Cm11", "Cm11", []int{0, 3, 7, 10, 14, 17}},
		{"C13", "C13", []int{0, 4, 7, 10, 14, 21}},
		{"C7b9", "C7b9", []int{0, 4, 7, 10, 13}},
		{"C7(b9, #11)", "C7b9#11", []int{0, 4, 7, 10, 13, 18}},
		{"C7♭9♯9", "C7b9#9", []int{0, 4, 7, 10, 13, 15}},
		{"C13b9", "C13b9", []int{0, 4, 7, 10, 13, 21}},
		{"C7alt", "C7alt", []int{0, 4, 6, 10, 13, 15, 20}},
		{"Cadd9", "Cadd9", []int{0, 4, 7, 14}},
		{"Cmadd9", "Cmadd9", []int{0, 3, 7, 14}},
		{"Cadd2", "Cadd2", []int{0, 2, 4, 7}},
		{"C2", "Cadd2", []int{0, 2, 4, 7}},
		{"Csus2sus4", "Csus4add2", []int{0, 2, 5, 7}},
		{"Csus4sus2", "Csus4add2", []int{0, 2, 5, 7}},
		{"C7+", "C7#5", []int{0, 4, 8, 10}},
		{"C9+", "C9#5", []int{0, 4, 8, 10, 14}},
		{"Cm7+", "Cm7#5", []int{0, 3, 8, 10}},
		{"Cmaj7#11", "Cmaj7#11", []int{0, 4, 7, 11, 18}},
		{"C7no3", "C7no3", []int{0, 7, 10}},
		{"C(omit5)", "Cno5", []int{0, 4}},
		{"Bb7", "Bb7", []int{0, 4, 7, 10}},
		{"Bbm7", "Bbm7", []int{0, 3, 7, 10}},
		{"F#m7b5", "F#m7b5", []int{0, 3, 6, 10}},
		{"Ebmaj7/G", "Ebmaj7/G", []int{0, 4, 7, 11}},
		{"C/E", "C/E", []int{0, 4, 7}},
		{"D7b9/F#", "D7b9/F#", []int{0, 4, 7, 10, 13}},
		{"C6/9/E", "C6/9/E", []int{0, 4, 7, 9, 14}},
		{"Cdim(maj7)", "Cdimmaj7", []int{0, 3, 6, 11}},
		{"N.C.", "N.C.", nil},
		{"NC", "N.C.", nil},
	}
	for _, c := range cases {
		ch, err := ParseChord(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got := ch.String(); got != c.out {
			t.Errorf("%q spells %q, want %q", c.in, got, c.out)
		}
		if got := ch.Tones(); !reflect.DeepEqual(got, c.tones) {
			t.Errorf("%q tones %v, want %v", c.in, got, c.tones)
		}
		back, err := ParseChord(ch.String())
		if err != nil || !reflect.DeepEqual(back, ch) {
			t.Errorf("%q: %q does not read back (%v)", c.in, ch.String(), err)
		}
	}
	if c, _ := ParseChord("D7b9/F#"); !c.HasBass || c.BassPitch() != (Pitch{Step: 3, Alter: 1}) {
		t.Errorf("slash bass: %+v", c)
	}
	for _, bad := range []string{"", "H7", "C7b3", "Cxyz", "C7/", "Cadd8", "C7bb9", "Cno7"} {
		if _, err := ParseChord(bad); err == nil {
			t.Errorf("%q read as a chord", bad)
		}
	}
}

// What a page prints: the root, its accidental, the rest with ♭ and ♯ and
// the lead-sheet signs, the bass.
func TestChordPrinted(t *testing.T) {
	cases := []struct {
		in         string
		root       string
		alter      int
		rest, bass string
	}{
		{"Bb7(b9,#11)/D", "B", -1, "7(♭9,♯11)", "D"},
		{"F#m7b5", "F", 1, "ø7", ""},
		{"Cdim7", "C", 0, "°7", ""},
		{"C+", "C", 0, "+", ""},
		{"Cmaj7", "C", 0, "maj7", ""},
		{"Cm(maj7)", "C", 0, "m(maj7)", ""},
		{"Cmmaj9", "C", 0, "m(maj9)", ""},
		{"N.C.", "N.C.", 0, "", ""},
	}
	for _, c := range cases {
		ch, _ := ParseChord(c.in)
		root, alter, rest, bass, _ := ch.Printed()
		if root != c.root || alter != c.alter || rest != c.rest || bass != c.bass {
			t.Errorf("%q prints %q %d %q / %q", c.in, root, alter, rest, bass)
		}
	}
}

// A chord transposed with a staff keeps its spelling the interval's way.
func TestChordTranspose(t *testing.T) {
	c, _ := ParseChord("C7/E")
	up := c.Transpose(Interval{Diatonic: 1, Chromatic: 2}) // a B-flat clarinet's written chord
	if got := up.String(); got != "D7/F#" {
		t.Errorf("C7/E up a major second is %q", got)
	}
	c, _ = ParseChord("Ebm7")
	if got := c.Transpose(Interval{Diatonic: 4, Chromatic: 7}).String(); got != "Bbm7" {
		t.Errorf("Ebm7 up a fifth is %q", got)
	}
}

// Random chords read back from their spelling as themselves.
func TestChordRoundTripProperty(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 4000; i++ {
		c := Chord{Root: Pitch{Step: int8(r.Intn(7)), Alter: int8(r.Intn(3) - 1)},
			Quality: ChordQuality(r.Intn(8)), Seventh: ChordSeventh(r.Intn(3))}
		switch r.Intn(4) {
		case 0:
			c.Ext = []int{9, 11, 13}[r.Intn(3)]
		case 1:
			c.Six = true
			if r.Intn(2) == 0 {
				c.Ext = 9
			}
		}
		if c.Six && (c.Quality == ChordDiminished || c.Quality == ChordHalfDiminished || c.Quality == ChordPower) {
			c.Six = false
		}
		if c.Six {
			c.Seventh = SeventhNone
		}
		if c.Quality == ChordDiminished && c.Seventh == SeventhMinor {
			c.Seventh = SeventhDiminished
		}
		if c.Quality == ChordHalfDiminished {
			c.Seventh, c.Six = SeventhMinor, false
			if c.Ext == 9 && c.Seventh == SeventhNone {
				c.Ext = 0
			}
		}
		if c.Quality == ChordPower {
			c.Seventh, c.Ext, c.Six = SeventhNone, 0, false
		}
		if c.Seventh == SeventhNone && !c.Six {
			c.Ext = 0
		}
		if r.Intn(3) == 0 && c.Seventh != SeventhNone {
			c.Alters = []Degree{{9, -1}, {11, 1}}[:1+r.Intn(2)]
		}
		if r.Intn(4) == 0 && c.Quality != ChordPower {
			c.Adds = []Degree{{2, 0}}
		}
		if r.Intn(5) == 0 {
			c.Omits = []int{5}
		}
		if r.Intn(3) == 0 {
			c.Bass, c.HasBass = Pitch{Step: int8(r.Intn(7)), Alter: int8(r.Intn(3) - 1)}, true
		}
		c.normalise()
		text := c.String()
		back, err := ParseChord(text)
		if err != nil {
			t.Fatalf("case %d: %+v spells %q, which does not read: %v", i, c, text, err)
		}
		if !reflect.DeepEqual(back, c) {
			t.Fatalf("case %d: %q reads back as %+v, not %+v", i, text, back, c)
		}
	}
}

func TestChordAtAndSort(t *testing.T) {
	l := &Line{}
	c1, _ := ParseChord("C")
	c2, _ := ParseChord("G7")
	l.Chords = []ChordSymbol{{At: Pos{Bar: 2}, Chord: c2}, {At: Pos{Bar: 1}, Chord: c1}}
	l.SortChords()
	if l.Chords[0].Chord.String() != "C" {
		t.Fatal("not sorted")
	}
	if l.ChordAt(Pos{Bar: 1, Offset: F(1, 2)}).Chord.String() != "C" || l.ChordAt(Pos{Bar: 3}).Chord.String() != "G7" {
		t.Error("ChordAt")
	}
	l.Chords = l.Chords[1:]
	if l.ChordAt(Pos{Bar: 1}) != nil {
		t.Error("a chord before the first")
	}
}
