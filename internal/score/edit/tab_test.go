package edit

import (
	"testing"

	"foundry/internal/score"
)

func guitar(t *testing.T, voice string) *score.Score {
	t.Helper()
	s := one(t, "4/4", 0, voice)
	s.Lines[0].Staves[0].Clef = "treble_8vb"
	if err := SetTab(s, 0, 0, score.NewTab("guitar")); err != nil {
		t.Fatal(err)
	}
	return s
}

// A fret on a string is that string's pitch there; the string is kept.
func TestSetFret(t *testing.T) {
	s := guitar(t, "| qE4 qC3+E3 h_ |")
	l := Loc{Bar: 1, Event: 0}
	// The E4 is on the top string; fret 3 there is G4.
	if k, err := SetFret(s, l, 1, 3, 0); err != nil || k != 0 {
		t.Fatal(k, err)
	}
	if barText(s, 0, 1) != "qG4(str=1) qC3+E3 h_" {
		t.Errorf("fret 3: %s", barText(s, 0, 1))
	}
	// A string with no note of the chord on it gets one.
	if _, err := SetFret(s, l, 2, 1, 0); err != nil {
		t.Fatal(err)
	}
	if barText(s, 0, 1) != "qC4+G4(str=C4/2+G4/1) qC3+E3 h_" {
		t.Errorf("added: %s", barText(s, 0, 1))
	}
	// In a key, the fret is spelled in it.
	if _, err := SetFret(s, Loc{Bar: 1, Event: 1}, 4, 1, -3); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "qC4+G4(str=C4/2+G4/1) qC3+Eb3(str=Eb3/4) h_" {
		t.Errorf("Eb: %s", got)
	}
	if _, err := SetFret(s, l, 7, 0, 0); err == nil {
		t.Error("a seventh string")
	}
	if _, err := SetFret(s, l, 1, 30, 0); err == nil {
		t.Error("fret 30")
	}
	if _, err := SetFret(s, Loc{Bar: 1, Event: 2}, 1, 0, 0); err == nil {
		t.Error("a fret on a space")
	}
	s2 := one(t, "4/4", 0, "| wC4 |")
	if _, err := SetFret(s2, Loc{Bar: 1}, 1, 0, 0); err == nil {
		t.Error("a fret on a staff with no tablature")
	}
}

// Moving a note to another string keeps its pitch, and moves a note that
// was on that string out of the way.
func TestSetTabString(t *testing.T) {
	s := guitar(t, "| qE4 qG3+B3 h_ |")
	if err := SetTabString(s, Loc{Bar: 1}, 0, 2); err != nil {
		t.Fatal(err)
	}
	st := s.Lines[0].Staves[0]
	if p := TabPlaces(s, Loc{Bar: 1}); p[0].String != 2 || p[0].Fret != 5 {
		t.Errorf("E4 on the B string: %+v", p)
	}
	if err := SetTabString(s, Loc{Bar: 1}, 0, 6); err != nil {
		t.Errorf("E4 on the low E at 24: %v", err)
	}
	if err := SetTabString(s, Loc{Bar: 1, Event: 1}, 1, 1); err == nil {
		t.Error("B3 on the top E")
	}
	// B3 to the G string: the G3 moves off it.
	if err := SetTabString(s, Loc{Bar: 1, Event: 1}, 1, 3); err != nil {
		t.Fatal(err)
	}
	p := TabPlaces(s, Loc{Bar: 1, Event: 1})
	if p[1].String != 3 || p[0].String == 3 || !p[0].OK {
		t.Errorf("B3 on G: %+v", p)
	}
	if err := SetTab(s, 0, 0, nil); err != nil || st.Tab != nil {
		t.Error("taking the tablature off")
	}
	s.Lines[0].Staves[0].Clef = "percussion"
	if err := SetTab(s, 0, 0, score.NewTab("bass")); err == nil {
		t.Error("tablature on a percussion staff")
	}
}
