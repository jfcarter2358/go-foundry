package edit

import (
	"testing"

	"foundry/internal/score"
)

// Lyric entry walks the sung notes: past rests, grace notes and notes tied
// over, across barlines and back.
func TestSungNotes(t *testing.T) {
	s := one(t, "4/4", 0, "| qC4 qr /eE4 qD4~ qD4 | hE4 qF4 qG4 |")
	l := Loc{Bar: 1, Event: 0}
	var got []Loc
	for at, ok := FirstSung(s, l); ok; at, ok = NextSung(s, at) {
		got = append(got, at)
	}
	want := []Loc{{Bar: 1, Event: 0}, {Bar: 1, Event: 3}, {Bar: 2, Event: 0}, {Bar: 2, Event: 1}, {Bar: 2, Event: 2}}
	if len(got) != len(want) {
		t.Fatalf("sung %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("note %d at %+v, want %+v", i, got[i], want[i])
		}
	}
	if p, ok := PrevSung(s, Loc{Bar: 2, Event: 0}); !ok || p != (Loc{Bar: 1, Event: 3}) {
		t.Errorf("before bar 2: %+v", p)
	}
	if _, ok := PrevSung(s, Loc{Bar: 1, Event: 0}); ok {
		t.Error("before the first note")
	}
	if at, ok := FirstSung(s, Loc{Bar: 1, Event: 1}); !ok || at != (Loc{Bar: 1, Event: 3}) {
		t.Errorf("from the rest: %+v", at)
	}
}

func TestSetLyricAndMelisma(t *testing.T) {
	s := one(t, "4/4", 0, "| qC4 qD4 qE4 qr |")
	if err := SetLyric(s, Loc{Bar: 1, Event: 0}, score.Lyric{Verse: 1, Text: "glo"}); err != nil {
		t.Fatal(err)
	}
	if err := SetLyric(s, Loc{Bar: 1, Event: 3}, score.Lyric{Verse: 1, Text: "x"}); err == nil {
		t.Error("a syllable on a rest")
	}
	if err := SetLyric(s, Loc{Bar: 1, Event: 0}, score.Lyric{Verse: 9, Text: "x"}); err == nil {
		t.Error("verse 9")
	}
	// A melisma from the D: "glo" holds on.
	if p, ok := Melisma(s, Loc{Bar: 1, Event: 2}, 1); !ok || p.Event != 0 {
		t.Fatalf("melisma from %+v", p)
	}
	if barText(s, 0, 1) != "qC4(ly=glo_) qD4 qE4 qr" {
		t.Errorf("bar: %s", barText(s, 0, 1))
	}
	// A hyphenated syllable keeps its hyphen.
	SetLyric(s, Loc{Bar: 1, Event: 0}, score.Lyric{Verse: 1, Text: "glo", Hyphen: true})
	Melisma(s, Loc{Bar: 1, Event: 2}, 1)
	if barText(s, 0, 1) != "qC4(ly=glo-) qD4 qE4 qr" {
		t.Errorf("hyphen kept: %s", barText(s, 0, 1))
	}
	SetLyric(s, Loc{Bar: 1, Event: 1}, score.Lyric{Verse: 2, Text: "ah"})
	if n := ClearLyrics(s, 0, 0, 1, 1, 2); n != 1 || barText(s, 0, 1) != "qC4(ly=glo-) qD4 qE4 qr" {
		t.Errorf("cleared %d: %s", n, barText(s, 0, 1))
	}
	if n := ClearLyrics(s, 0, 0, 1, 1, 0); n != 1 || barText(s, 0, 1) != "qC4 qD4 qE4 qr" {
		t.Errorf("cleared %d: %s", n, barText(s, 0, 1))
	}
}

func TestChordSymbolEdits(t *testing.T) {
	s := one(t, "3/4", 0, "| h.C4 | qD4 eE4 eF4 qG4 |")
	c := func(sym string) score.Chord {
		ch, err := score.ParseChord(sym)
		if err != nil {
			t.Fatal(err)
		}
		return ch
	}
	if _, err := SetChordSymbol(s, 0, score.Pos{Bar: 2}, c("G7")); err != nil {
		t.Fatal(err)
	}
	if i, _ := SetChordSymbol(s, 0, score.Pos{Bar: 1}, c("C")); i != 0 {
		t.Errorf("C at %d", i)
	}
	if i, _ := SetChordSymbol(s, 0, score.Pos{Bar: 2}, c("G7b9")); i != 1 || len(s.Lines[0].Chords) != 2 {
		t.Errorf("replaced at %d: %v", i, s.Lines[0].Chords)
	}
	if _, err := SetChordSymbol(s, 0, score.Pos{Bar: 3}, c("C")); err == nil {
		t.Error("a chord past the end")
	}
	if cs := ChordSymbolAt(s, 0, score.Pos{Bar: 2, Offset: score.F(0, 1)}); cs == nil || cs.Chord.String() != "G7b9" {
		t.Errorf("at bar 2: %v", cs)
	}
	// Space walks beats and note onsets: bar 1 by its beats, bar 2 by both.
	var places []string
	for p, ok := (score.Pos{Bar: 1}), true; ok; p, ok = NextChordPlace(s, 0, p) {
		places = append(places, s.FormatPos(p))
	}
	want := "[1 1:2 1:3 2 2:2 2:2+1/2 2:3]"
	if got := sprint(places); got != want {
		t.Errorf("places %s, want %s", got, want)
	}
	if p, ok := PrevChordPlace(s, 0, score.Pos{Bar: 2}); !ok || s.FormatPos(p) != "1:3" {
		t.Errorf("back from bar 2: %s", s.FormatPos(p))
	}
	TransposeChordSymbols(s, 0, 1, 2, score.Interval{Diatonic: 1, Chromatic: 2})
	if s.Lines[0].Chords[1].Chord.String() != "A7b9" {
		t.Errorf("transposed: %s", s.Lines[0].Chords[1].Chord)
	}
	// Bars copied carry their chord symbols; a cleared bar loses its own.
	clip, _ := CopyBars(s, 0, 0, 1, 2)
	PasteBars(s, clip, 0, 3)
	if len(s.Lines[0].Chords) != 4 || s.Lines[0].Chords[2].At.Bar != 3 {
		t.Errorf("pasted: %v", s.Lines[0].Chords)
	}
	ClearBars(s, 0, 0, 1, 1)
	if len(s.Lines[0].Chords) != 3 {
		t.Errorf("cleared: %v", s.Lines[0].Chords)
	}
	if !RemoveChordSymbol(s, 0, score.Pos{Bar: 2}) || RemoveChordSymbol(s, 0, score.Pos{Bar: 2}) {
		t.Error("remove")
	}
}

func sprint(ss []string) string {
	out := "["
	for i, s := range ss {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out + "]"
}
