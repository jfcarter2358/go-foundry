package score

import (
	"strings"
	"testing"
)

// Single notes go low on the neck and open where they can; chords take a
// string a note and the smallest stretch.
func TestTabPlace(t *testing.T) {
	g := NewTab("guitar")
	p := func(s string) Pitch { q, _ := ParsePitch(s); return q }
	place := func(near float64, fixed []int, ns ...string) []TabNote {
		var ps []Pitch
		for _, n := range ns {
			ps = append(ps, p(n))
		}
		return g.Place(ps, fixed, near)
	}
	if r := place(0, nil, "E2"); !r[0].OK || r[0].String != 6 || r[0].Fret != 0 {
		t.Errorf("low E: %+v", r)
	}
	if r := place(0, nil, "E4"); r[0].String != 1 || r[0].Fret != 0 {
		t.Errorf("high E: %+v", r)
	}
	if r := place(0, nil, "C4"); r[0].String != 2 || r[0].Fret != 1 {
		t.Errorf("middle C: %+v", r)
	}
	// Up the neck the hand stays: a C4 near the 8th fret is on the G string? the D string at 10.
	if r := place(9, nil, "C4"); r[0].Fret < 5 {
		t.Errorf("C4 near fret 9: %+v", r)
	}
	// An open C chord.
	r := place(0, nil, "C3", "E3", "G3", "C4", "E4")
	want := []TabNote{{5, 3, true}, {4, 2, true}, {3, 0, true}, {2, 1, true}, {1, 0, true}}
	for i := range want {
		if r[i] != want[i] {
			t.Errorf("C chord note %d: %+v, want %+v", i, r[i], want[i])
		}
	}
	// A barre F: six notes, six strings.
	r = place(0, nil, "F2", "C3", "F3", "A3", "C4", "F4")
	for i, n := range r {
		if !n.OK || n.Fret > 3 {
			t.Errorf("F barre note %d: %+v", i, n)
		}
	}
	// What the writer chose is kept.
	if r := place(0, []int{3}, "E4"); r[0].String != 3 || r[0].Fret != 9 {
		t.Errorf("E4 on the G string: %+v", r)
	}
	// Out of reach.
	if r := place(0, nil, "C2"); r[0].OK {
		t.Errorf("C2 on a guitar: %+v", r)
	}
	if r := place(0, nil, "E2", "F2"); r[0].OK == r[1].OK && r[0].String == r[1].String {
		t.Errorf("two notes on one string: %+v", r)
	}
	// Seven notes on six strings: one is left off.
	r = place(0, nil, "E2", "A2", "D3", "G3", "B3", "E4", "A4")
	off := 0
	for _, n := range r {
		if !n.OK {
			off++
		}
	}
	if off != 1 {
		t.Errorf("seven notes: %+v", r)
	}
	// A capo counts frets from itself.
	g.Capo = 2
	if r := place(0, nil, "F#2"); r[0].String != 6 || r[0].Fret != 0 {
		t.Errorf("F#2 with a capo at 2: %+v", r)
	}
	if HandAt([]TabNote{{1, 0, true}, {2, 3, true}, {3, 5, true}}) != 4 {
		t.Error("HandAt")
	}
}

func TestTabStringsMark(t *testing.T) {
	v := mustVoice(t, `| qE4(str=2) hG3+D4(str=G3/3+D4/2) qC4 |`)
	m := v.Measures[0]
	if got := m.Events[0].TabStrings(); got[0] != 2 {
		t.Errorf("single: %v", got)
	}
	if got := m.Events[1].TabStrings(); got[0] != 3 || got[1] != 2 {
		t.Errorf("chord: %v", got)
	}
	e := m.Events[1]
	e.SetTabString(0, 0)
	if v := e.Marks[0].Value; v != "D4/2" {
		t.Errorf("one left: %q", v)
	}
	e.SetTabString(1, 0)
	if len(e.Marks) != 0 {
		t.Errorf("none left: %v", e.Marks)
	}
	e = m.Events[2]
	e.SetTabString(0, 4)
	if FormatEvent(e) != "qC4(str=4)" {
		t.Errorf("set: %s", FormatEvent(e))
	}
	s := New("x")
	ln := NewLine("Guitar", "plucked/guitar_nylon", "treble_8vb")
	ln.Staves[0].Tab = NewTab("guitar")
	ln.Staves[0].Voices[0] = mustVoice(t, `| qF4(str=6) qB3+C4(str=B3/2+C4/2) qC4(str=E4/1) qD4(str=9) |`)
	s.Lines = []*Line{ln}
	var msgs []string
	for _, is := range s.Validate(nil) {
		msgs = append(msgs, is.Msg)
	}
	all := strings.Join(msgs, "\n")
	for _, w := range []string{"cannot be played on string 6", "two notes on string 2", "the chord has no E4", "has 6 strings"} {
		if !strings.Contains(all, w) {
			t.Errorf("no %q in:\n%s", w, all)
		}
	}
}

func TestTuningPresetsAndFile(t *testing.T) {
	for _, p := range TuningPresets {
		tb := NewTab(p.Name)
		if tb.Preset() != p.Name || tb.Strings() != len(p.Strings) {
			t.Errorf("%s: preset %q, %d strings", p.Name, tb.Preset(), tb.Strings())
		}
	}
	if NewTab("guitar").Open(1) != 64 || NewTab("bass").Open(4) != 28 {
		t.Error("open strings")
	}
	src := `version: 1
bars: 1
lines:
  - name: Bass
    instrument: bass/bass_finger
    staves:
      - clef: bass_8vb
        tab: {strings: [C1, G1, D2, A2], capo: 1, only: true}
        voices: ["| qC2 qG2 hD3 |"]
  - name: Guitar
    instrument: plucked/guitar_nylon
    staves:
      - clef: treble_8vb
        tab: {tuning: guitar_drop_d}
        voices: ["| wD2 |"]
`
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	b := s.Lines[0].Staves[0].Tab
	if b.Strings() != 4 || b.Capo != 1 || !b.Only || b.Preset() != "" {
		t.Errorf("bass tab %+v", b)
	}
	if s.Lines[1].Staves[0].Tab.Preset() != "guitar_drop_d" {
		t.Error("drop D")
	}
	out, _ := Marshal(s)
	for _, w := range []string{"tab: {strings: [C1, G1, D2, A2], capo: 1, only: true}", "tab: {tuning: guitar_drop_d}"} {
		if !strings.Contains(string(out), w) {
			t.Errorf("no %q in\n%s", w, out)
		}
	}
	for _, bad := range []string{"tab: {tuning: lute}", "tab: {tuning: guitar, strings: [E2]}", "tab: {capo: 20}", "tab: {strings: [E2, Q3]}"} {
		if _, err := Parse([]byte(strings.Replace(src, "tab: {tuning: guitar_drop_d}", bad, 1))); err == nil {
			t.Errorf("%s read", bad)
		}
	}
}
