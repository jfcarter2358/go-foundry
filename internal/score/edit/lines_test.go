package edit

import (
	"testing"

	"foundry/internal/score"
	"foundry/internal/tracker"
)

// lineNames are a score's lines' names, in order.
func lineNames(s *score.Score) []string {
	var out []string
	for _, l := range s.Lines {
		out = append(out, l.Name)
	}
	return out
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A duplicate with notes is the whole line under a new name; an empty one
// keeps the instrument, clefs, transposition and mixer and writes nothing.
func TestDuplicateLine(t *testing.T) {
	s := one(t, "4/4", 0, "| qC5 qD5 hE5 | wF5 |", "| hC4 hD4 | wE4 |")
	l := s.Lines[0]
	l.Name, l.Volume, l.Pan, l.SendA, l.SendB, l.Mute, l.Solo = "Violin I", 0.7, -0.3, 0.2, 0.1, true, true
	l.Staves[0].Transpose = score.Interval{Diatonic: -1, Chromatic: -2}
	l.Inserts = []score.FX{{Kind: "eq", Params: []score.FXParam{{Name: "low", Value: -2}}}}
	l.Lanes = []score.Lane{{Name: "volume", Points: []score.LanePoint{{At: score.Pos{Bar: 1}, Value: 0.5}}}}
	s.Lines = append(s.Lines, score.NewLine("Cello", "strings/cello_section", "bass"))
	s.Params = []score.Param{{Name: "x"}}
	s.Bindings = []score.Binding{{Param: "x", Line: "Violin I", Target: tracker.BindVolume, To: 1}}

	i, err := DuplicateLine(s, 0, true)
	if err != nil || i != 1 {
		t.Fatal(i, err)
	}
	if got := lineNames(s); !eqStrings(got, []string{"Violin I", "Violin I 2", "Cello"}) {
		t.Fatalf("names %v", got)
	}
	c := s.Lines[1]
	if c.Solo || !c.Mute || c.Volume != 0.7 || c.Pan != -0.3 || c.Instrument != l.Instrument || len(c.Lanes) != 1 {
		t.Errorf("copy with notes: %+v", c)
	}
	if got := score.FormatVoice(c.Staves[0].Voices[1]); got != score.FormatVoice(l.Staves[0].Voices[1]) {
		t.Errorf("voice 2 = %q", got)
	}
	// The copy is its own: writing into it leaves the original alone.
	c.Staves[0].Voices[0].Measures[0].Events[0].Notes[0].Pitch.Octave = 3
	if l.Staves[0].Voices[0].Measures[0].Events[0].Notes[0].Pitch.Octave != 5 {
		t.Error("the copy shares the original's notes")
	}
	c.Inserts[0].Params[0].Value = 9
	if l.Inserts[0].Params[0].Value != -2 {
		t.Error("the copy shares the original's inserts")
	}

	i, err = DuplicateLine(s, 0, false)
	if err != nil || i != 1 {
		t.Fatal(i, err)
	}
	e := s.Lines[1]
	if e.Name != "Violin I 3" || e.Instrument != l.Instrument || e.Volume != 0.7 || e.Pan != -0.3 || e.SendA != 0.2 ||
		e.SendB != 0.1 || !e.Mute || e.Solo || len(e.Lanes) != 0 || len(e.Inserts) != 1 {
		t.Errorf("empty copy: %+v", e)
	}
	if len(e.Staves) != 1 || e.Staves[0].Transpose != l.Staves[0].Transpose || e.Staves[0].Clef != "treble" ||
		len(e.Staves[0].Voices) != 1 || len(e.Staves[0].Voices[0].Measures) != 0 {
		t.Errorf("empty copy's staves: %+v", e.Staves[0])
	}
	e.Inserts[0].Params[0].Value = 5
	if l.Inserts[0].Params[0].Value != -2 {
		t.Error("the empty copy shares the original's inserts")
	}
	if len(s.Bindings) != 1 || s.Bindings[0].Line != "Violin I" {
		t.Errorf("bindings %+v", s.Bindings)
	}
	if issues := s.Validate(nil); hasError(issues) {
		t.Errorf("issues after duplicating: %v", issues)
	}
	if _, err := DuplicateLine(s, 9, true); err == nil {
		t.Error("duplicated a line that is not there")
	}
}

func hasError(is []score.Issue) bool {
	for _, i := range is {
		if i.Severity == score.Error {
			return true
		}
	}
	return false
}

// Lines move, go and are renamed; bindings follow a rename and go with a
// removed line; the last line stays.
func TestMoveRemoveRenameLines(t *testing.T) {
	s := score.New("t")
	for _, n := range []string{"A", "B", "C", "D"} {
		AddLine(s, len(s.Lines), score.NewLine(n, "keys/grand_piano", "treble"))
	}
	if err := MoveLine(s, 0, 2); err != nil || !eqStrings(lineNames(s), []string{"B", "C", "A", "D"}) {
		t.Fatalf("move down: %v %v", lineNames(s), err)
	}
	if err := MoveLine(s, 3, 0); err != nil || !eqStrings(lineNames(s), []string{"D", "B", "C", "A"}) {
		t.Fatalf("move up: %v %v", lineNames(s), err)
	}
	if err := MoveLine(s, 1, 1); err == nil {
		t.Error("moved a line onto itself")
	}
	if err := MoveLine(s, 0, 99); err != nil || lineNames(s)[3] != "D" {
		t.Errorf("move past the end: %v %v", lineNames(s), err)
	}
	s.Bindings = []score.Binding{{Param: "p", Line: "b"}, {Param: "p", Line: "C"}}
	s.Layout.Parts = []score.PartLayout{{Line: "b", Staff: 8}, {Line: "C", Staff: 6}}
	if err := RenameLine(s, 0, "  Bass  "); err != nil || s.Lines[0].Name != "Bass" || s.Bindings[0].Line != "Bass" {
		t.Errorf("rename: %v %v %+v", lineNames(s), err, s.Bindings)
	}
	if p := s.Layout.Part("Bass"); p == nil || p.Staff != 8 {
		t.Errorf("the part's layout did not follow the rename: %+v", s.Layout.Parts)
	}
	if err := RenameLine(s, 0, "c"); err == nil {
		t.Error("renamed onto another line's name")
	}
	if err := RenameLine(s, 0, " "); err == nil {
		t.Error("renamed to nothing")
	}
	if err := RemoveLine(s, 1); err != nil || !eqStrings(lineNames(s), []string{"Bass", "A", "D"}) ||
		len(s.Bindings) != 1 || s.Bindings[0].Line != "Bass" {
		t.Errorf("remove: %v %v %+v", lineNames(s), err, s.Bindings)
	}
	if len(s.Layout.Parts) != 1 || s.Layout.Parts[0].Line != "Bass" {
		t.Errorf("the removed line's part layout stayed: %+v", s.Layout.Parts)
	}
	for len(s.Lines) > 1 {
		if err := RemoveLine(s, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveLine(s, 0); err == nil {
		t.Error("removed the last line")
	}
	if got := UniqueLineName(s, "D", -1); got != "D 2" {
		t.Errorf("unique name %q", got)
	}
	if got := UniqueLineName(s, "D", 0); got != "D" {
		t.Errorf("a line's own name %q", got)
	}
	if i := AddLine(s, -5, score.NewLine("D", "", "treble")); i != 0 || !eqStrings(lineNames(s), []string{"D 2", "D"}) {
		t.Errorf("add at the top: %d %v", i, lineNames(s))
	}
}

// A new line is written the way its instrument says.
func TestNewLineFor(t *testing.T) {
	piano := NewLineFor("Piano", "keys/grand_piano", tracker.Playback{Clef: "grand"})
	if len(piano.Staves) != 2 || piano.Staves[0].Clef != "treble" || piano.Staves[1].Clef != "bass" {
		t.Errorf("grand staff: %+v %+v", piano.Staves[0], piano.Staves[len(piano.Staves)-1])
	}
	horn := NewLineFor("Horn", "brass/horn", tracker.Playback{Clef: "treble", Transposition: -7})
	if horn.Staves[0].Transpose != (score.Interval{Diatonic: -4, Chromatic: -7}) {
		t.Errorf("horn transposition %+v", horn.Staves[0].Transpose)
	}
	kit := NewLineFor("Drums", "percussion/drum_kit", tracker.Playback{Clef: "percussion", Transposition: 3})
	if kit.Staves[0].Clef != "percussion" || kit.Staves[0].Transpose != (score.Interval{}) {
		t.Errorf("percussion staff %+v", kit.Staves[0])
	}
	if l := NewLineFor("X", "x", tracker.Playback{}); l.Staves[0].Clef != "treble" || l.Volume != 1 {
		t.Errorf("default line %+v", l.Staves[0])
	}
}

func TestRetranspose(t *testing.T) {
	l := NewLineFor("Cl", "woodwinds/clarinet", tracker.Playback{Clef: "treble"})
	if !Retranspose(l, tracker.Playback{Transposition: -2}) || l.Staves[0].Transpose != (score.Interval{Diatonic: -1, Chromatic: -2}) {
		t.Fatalf("to B flat: %+v", l.Staves[0].Transpose)
	}
	if Retranspose(l, tracker.Playback{Transposition: -2}) {
		t.Error("the same transposition again is no change")
	}
	if !Retranspose(l, tracker.Playback{}) || l.Staves[0].Transpose != (score.Interval{}) {
		t.Errorf("back to C: %+v", l.Staves[0].Transpose)
	}
	kit := NewLineFor("Kit", "drums/kit", tracker.Playback{Clef: "percussion"})
	if Retranspose(kit, tracker.Playback{Transposition: 12}) {
		t.Error("a percussion staff does not transpose")
	}
	grand := NewLineFor("Pno", "keys/piano", tracker.Playback{Clef: "grand"})
	if Retranspose(grand, tracker.Playback{Clef: "grand", Transposition: 12}) {
		t.Error("a grand staff instrument is written as it sounds")
	}
}
