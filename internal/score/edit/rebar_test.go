package edit

import (
	"testing"

	"foundry/internal/score"
)

func meter(t *testing.T, s string) score.Meter {
	t.Helper()
	m, err := score.ParseMeter(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// 4/4 re-barred as 3/4: the same music in time, across the new barlines,
// tied where a note now crosses one; the last bar filled out.
func TestRebarWritesTheMusicAgain(t *testing.T) {
	s := one(t, "4/4", 0, "| qC5(p) qD5 qE5 qF5 | hG5 hA5(stacc) |")
	d, err := Rebar(s, 1, meter(t, "3/4"))
	if err != nil {
		t.Fatal(err)
	}
	if d != 1 || s.Measures() != 3 {
		t.Fatalf("delta %d, %d bars", d, s.Measures())
	}
	want := []string{"qC5(p) qD5 qE5", "qF5 hG5", "hA5(stacc) qr"}
	for b, w := range want {
		if got := barText(s, 0, b+1); got != w {
			t.Errorf("bar %d = %q, want %q", b+1, got, w)
		}
	}
	if !s.MeterAt(1).Eq(meter(t, "3/4")) {
		t.Errorf("meter %v", s.MeterAt(1))
	}
	fullBars(t, s)
	// Back to 4/4: the original again (two tied quarters for the half
	// whose middle a barline cut come back whole).
	if _, err := Rebar(s, 1, meter(t, "4/4")); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1) + " | " + barText(s, 0, 2); got != "qC5(p) qD5 qE5 qF5 | hG5 hA5(stacc)" {
		t.Errorf("back to 4/4: %q", got)
	}
}

// A note crossing a new barline is tied over it; the bars after the
// passage, and what is placed in them, move by the bars gained.
func TestRebarMovesWhatComesAfter(t *testing.T) {
	s := one(t, "4/4", 0, "| wC5 | wD5 | wE5 | wF5 |")
	s.Meter = append(s.Meter, score.MeterMark{Bar: 3, Meter: meter(t, "4/4")})
	s.Sections = []score.Section{{Name: "a", Bar: 1}, {Name: "b", Bar: 2}, {Name: "c", Bar: 3}}
	s.Lines[0].Chords = []score.ChordSymbol{{At: score.Pos{Bar: 2}, Chord: mustChord(t, "G")}, {At: score.Pos{Bar: 4}, Chord: mustChord(t, "F")}}
	s.Layout.Breaks = []score.Break{{Bar: 4}}
	d, err := Rebar(s, 1, meter(t, "3/4"))
	if err != nil {
		t.Fatal(err)
	}
	// Two bars of 4/4 are 8 quarters: three bars of 3/4.
	if d != 1 {
		t.Fatalf("delta %d", d)
	}
	for b, w := range []string{"h.C5~", "qC5 hD5~", "hD5 qr", "wE5", "wF5"} {
		if got := barText(s, 0, b+1); got != w {
			t.Errorf("bar %d = %q, want %q", b+1, got, w)
		}
	}
	if s.MeterAt(4).String() != "4/4" || s.MeterAt(3).String() != "3/4" {
		t.Errorf("meters %+v", s.Meter)
	}
	if s.Sections[1].Bar != 2 || s.Sections[2].Bar != 4 {
		t.Errorf("sections %+v", s.Sections)
	}
	if c := s.Lines[0].Chords[0].At; c.Bar != 2 || !c.Offset.Eq(score.F(1, 4)) {
		t.Errorf("chord symbol moved to %+v, want 2:+1/4", c)
	}
	if c := s.Lines[0].Chords[1].At; c.Bar != 5 {
		t.Errorf("a chord symbol after the passage: %+v", c)
	}
	if s.Layout.Breaks[0].Bar != 5 {
		t.Errorf("break %+v", s.Layout.Breaks)
	}
	fullBars(t, s)
}

// Triplets, a tie and words keep themselves through a re-barring.
func TestRebarKeepsTupletsTiesAndWords(t *testing.T) {
	s := one(t, "3/4", 0, "| 3{eC5(ly=one) eD5 eE5} qF5~ qF5 | hG5 qr |")
	if _, err := Rebar(s, 1, meter(t, "2/4")); err != nil {
		t.Fatal(err)
	}
	for b, w := range []string{"3{eC5(ly=one) eD5 eE5} qF5~", "qF5 qG5~", "qG5 qr"} {
		if got := barText(s, 0, b+1); got != w {
			t.Errorf("bar %d = %q, want %q", b+1, got, w)
		}
	}
	fullBars(t, s)
	if _, err := Rebar(s, 9, meter(t, "2/4")); err == nil {
		t.Error("a bar past the end")
	}
}
