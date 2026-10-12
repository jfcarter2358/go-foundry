package score

import (
	"strings"
	"testing"
)

func parseOne(t *testing.T, header string, voices ...string) *Score {
	t.Helper()
	var b strings.Builder
	b.WriteString(header + "\nlines:\n  - name: A\n    instrument: x\n    staves:\n      - clef: treble\n        voices:\n")
	for _, v := range voices {
		b.WriteString("          - \"" + v + "\"\n")
	}
	s, err := Parse([]byte(b.String()))
	if err != nil {
		t.Fatalf("%v\n%s", err, b.String())
	}
	return s
}

func TestClefChanges(t *testing.T) {
	s := parseOne(t, "bars: 3", "| wC4 | wC4 | wC4 |")
	st := s.Lines[0].Staves[0]
	st.Clefs = []ClefChange{{At: Pos{Bar: 3}, Clef: "alto"}, {At: Pos{Bar: 2, Offset: F(1, 2)}, Clef: "bass"}}
	st.SortClefs()
	for _, c := range []struct {
		at   Pos
		want string
	}{{Pos{Bar: 1}, "treble"}, {Pos{Bar: 2, Offset: F(1, 4)}, "treble"}, {Pos{Bar: 2, Offset: F(1, 2)}, "bass"}, {Pos{Bar: 3, Offset: F(1, 4)}, "alto"}} {
		if got := st.ClefAt(c.at); got != c.want {
			t.Errorf("clef at %+v: %s, want %s", c.at, got, c.want)
		}
	}
	b, _ := Marshal(s)
	back, err := Parse(b)
	if err != nil || len(back.Lines[0].Staves[0].Clefs) != 2 || back.Lines[0].Staves[0].ClefAt(Pos{Bar: 2, Offset: F(3, 4)}) != "bass" {
		t.Errorf("clefs round trip: %v\n%s", err, b)
	}
	st.Clefs = append(st.Clefs, ClefChange{At: Pos{Bar: 9}, Clef: "bass"})
	if !strings.Contains(issueText(s.Validate(nil)), "clef change to bass outside the score") {
		t.Error("a clef change past the end is an error")
	}
}

func issueText(is []Issue) string {
	var w []string
	for _, i := range is {
		w = append(w, i.String())
	}
	return strings.Join(w, "\n")
}

func TestOttavaSpans(t *testing.T) {
	s := parseOne(t, "bars: 3", "| qC6(8va) qD6 qE6(ottava_end) qF6 | hC2(8vb) hD2 | qC7(15ma) qD7(8va) hE7 |")
	st := s.Lines[0].Staves[0]
	got := s.Ottavas(st)
	want := []OttavaSpan{
		{From: Pos{Bar: 1}, To: Pos{Bar: 1, Offset: F(3, 4)}, Octaves: 1},
		{From: Pos{Bar: 2}, To: Pos{Bar: 3}, Octaves: -1},                 // runs to the next ottava
		{From: Pos{Bar: 3}, To: Pos{Bar: 3, Offset: F(1, 4)}, Octaves: 2}, // closed by the next
		{From: Pos{Bar: 3, Offset: F(1, 4)}, To: Pos{Bar: 4}, Octaves: 1}, // to the voice's end
	}
	if len(got) != len(want) {
		t.Fatalf("spans %+v", got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.From.Bar != w.From.Bar || !g.From.Offset.Eq(w.From.Offset) || g.To.Bar != w.To.Bar || !g.To.Offset.Eq(w.To.Offset) || g.Octaves != w.Octaves {
			t.Errorf("span %d: %+v, want %+v", i, g, w)
		}
	}
	for _, c := range []struct {
		at   Pos
		want int
	}{{Pos{Bar: 1, Offset: F(1, 2)}, 1}, {Pos{Bar: 1, Offset: F(3, 4)}, 0}, {Pos{Bar: 2, Offset: F(1, 2)}, -1}, {Pos{Bar: 3}, 2}, {Pos{Bar: 3, Offset: F(1, 2)}, 1}} {
		if got := s.OttavaAt(st, c.at); got != c.want {
			t.Errorf("ottava at %+v: %d, want %d", c.at, got, c.want)
		}
	}
}

func TestWrittenAndSoundingPitch(t *testing.T) {
	// A B-flat clarinet: written D sounds C; a horn in F: written G sounds C.
	cl := &Staff{Clef: "treble", Transpose: Interval{Diatonic: -1, Chromatic: -2}}
	hn := &Staff{Clef: "treble", Transpose: Interval{Diatonic: -4, Chromatic: -7}}
	c4 := Pitch{Step: 0, Octave: 4}
	for _, c := range []struct {
		st      *Staff
		written string
	}{{cl, "D4"}, {hn, "G4"}, {&Staff{Clef: "treble"}, "C4"}} {
		w := c.st.Written(c4)
		if w.String() != c.written || c.st.Sounding(w) != c4 {
			t.Errorf("%v: concert C4 written %s (want %s), back %s", c.st.Transpose, w, c.written, c.st.Sounding(w))
		}
	}
	// Bb4 concert on the clarinet is a written C5 — spelled, not respelled.
	if w := cl.Written(Pitch{Step: 6, Alter: -1, Octave: 4}); w.String() != "C5" {
		t.Errorf("Bb4 on a B-flat clarinet is written %s", w)
	}
}

// The accidental a staff position has at a point: the key, then whatever
// an earlier note in the bar wrote there, in any voice of the staff.
func TestAlterInForce(t *testing.T) {
	s := parseOne(t, "bars: 2\nkey: [{bar: 1, fifths: 1}]", "| qFn4 qG4 qF#4~ qF#4 | qF4 qr hr |", "| hr qr qBb4 | hB4 hr |")
	st := s.Lines[0].Staves[0]
	f4 := Pitch{Step: 3, Octave: 4}.Diatonic()
	b4 := Pitch{Step: 6, Octave: 4}.Diatonic()
	for _, c := range []struct {
		bar        int
		at         Frac
		pos, alter int
	}{
		{1, F(0, 1), f4, 1},     // the key's F#
		{1, F(1, 4), f4, 0},     // after the natural
		{1, F(3, 4), f4, 1},     // after the F#4
		{1, F(1, 2), b4, 0},     // the second voice's Bb is later
		{1, F(7, 8), b4, -1},    // …and counts once it is past
		{1, F(1, 2), f4 + 7, 1}, // another octave keeps the key's
		{2, F(0, 1), f4, 1},     // a new bar: the key again
		{2, F(1, 2), f4, 0},     // after bar 2's F natural
	} {
		if got := s.AlterInForce(st, c.bar, c.at, c.pos); got != c.alter {
			t.Errorf("bar %d at %s, position %d: %d, want %d", c.bar, c.at, c.pos, got, c.alter)
		}
	}
	// A note tied over the barline neither shows its accidental nor sets
	// the bar's: an F later in bar 2 is the key's again.
	tied := parseOne(t, "bars: 2", "| h.r qF#4~ | qF#4 qG4 hr |")
	if got := tied.AlterInForce(tied.Lines[0].Staves[0], 2, F(1, 2), f4); got != 0 {
		t.Errorf("after a tied-over F#: %d, want 0", got)
	}
}

func TestDynamicSteps(t *testing.T) {
	for _, c := range []struct {
		from string
		dir  int
		want string
	}{{"mp", 1, "mf"}, {"mf", -1, "mp"}, {"fff", 1, "ffff"}, {"ffff", 1, "ffff"}, {"pppp", -1, "niente"}, {"niente", -1, "niente"}, {"p", 1, "mp"}} {
		if got := StepDynamic(Dynamics[c.from].Level, c.dir); got != Dynamics[c.want].Level {
			t.Errorf("%s %+d: %v, want %s", c.from, c.dir, got, c.want)
		}
	}
	// Between steps: the nearest step's neighbour.
	if got := StepDynamic((Dynamics["p"].Level+Dynamics["mp"].Level)/2+0.01, 1); got != Dynamics["mf"].Level {
		t.Errorf("from just over half way p..mp: %v", got)
	}
}

func TestMarkValues(t *testing.T) {
	for v, want := range map[string][5]int{"": {0, 0, 0, 0, 1}, "#": {1, 0, 1, 0, 1}, "b/#": {-1, 1, 1, 1, 1}, "/n": {0, 0, 0, 1, 1}, "x": {2, 0, 1, 0, 1}, "q": {0, 0, 0, 0, 0}} {
		u, l, hu, hl, ok := OrnamentAccidentals(v)
		b := func(x bool) int {
			if x {
				return 1
			}
			return 0
		}
		if got := [5]int{u, l, b(hu), b(hl), b(ok)}; ok && got != want || !ok && want[4] != 0 {
			t.Errorf("%q: %v, want %v", v, got, want)
		}
	}
	for v, want := range map[string]int{"": 3, "1": 1, "4": 4, "5": 0, "x": 0} {
		if got, _ := TremoloStrokes(v); got != want {
			t.Errorf("trem=%q: %d strokes, want %d", v, got, want)
		}
	}
	s := parseOne(t, "bars: 1", "| qC4(tr=q) qC4(trem=9) qC4(ferm=forever) qC4(arp=sideways,gliss=zigzag) |")
	text := issueText(s.Validate(nil))
	for _, want := range []string{"tr=q", "trem=9", "ferm=forever", "arp=sideways", "gliss=zigzag"} {
		if !strings.Contains(text, want) {
			t.Errorf("no issue for %s in:\n%s", want, text)
		}
	}
}

func TestValidateExpression(t *testing.T) {
	s := parseOne(t, `bars: 4
tempo: [{bar: 1, bpm: 90}, {bar: 2, text: Andante}, {bar: 3, text: a tempo, to: "4"}, {bar: 4, text: rit., to: "3"}]
repeats: [{start: 1, end: 2}]
voltas: [{start: 2, end: 2, passes: [1]}, {start: 2, end: 3, passes: [5]}]
jumps: [{mark: ds, bar: 4, al: coda}, {mark: to_coda, bar: 9}]`,
		"| wC4(hairpin_end) | wC4(slur_end) | wC4 | wC4(ottava_end) |")
	s.Lines[0].Overrides = []DynOverride{{From: Pos{Bar: 3}, To: Pos{Bar: 2}, Level: 0.5}}
	text := issueText(s.Validate(nil))
	for _, want := range []string{
		"tempo text \"Andante\" sets no tempo", "\"a tempo\" has an end (to:) but is not a gradual", "\"rit.\" ends before it starts",
		"endings overlap", "plays 2 times", "D.S. al Coda but no segno", "To Coda past the last bar",
		"hairpin_end with no cresc", "slur_end with no slur", "ottava_end with no ottava", "dynamic override ends before it starts",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("no issue %q in:\n%s", want, text)
		}
	}
	ok := parseOne(t, `bars: 4
repeats: [{start: 1, end: 2}]
voltas: [{start: 2, end: 2, passes: [1]}, {start: 3, end: 3, passes: [2]}]
jumps: [{mark: segno, bar: 1}, {mark: to_coda, bar: 1}, {mark: ds, bar: 3, al: coda}, {mark: coda, bar: 4}]
tempo: [{bar: 1, bpm: 90}, {bar: 2, text: poco rit., to: "3"}, {bar: 3, text: a tempo}]`,
		"| wC4(p,cresc,slur,8va) | wC4(hairpin_end,slur_end,ottava_end) | wC4(tr=#) | wC4(trem=2) |")
	if is := ok.Validate(nil); len(is) > 0 {
		t.Errorf("a sound score has issues:\n%s", issueText(is))
	}
}
