package edit

import (
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/io/notate"
)

// played is a note played a little off the grid: whole notes from the top.
func played(start, end float64, midi, vel int) notate.Note {
	f := func(x float64) score.Frac { return score.F(int64(x*3840+0.5), 3840) }
	return notate.Note{Start: f(start), End: f(end), MIDI: midi, Velocity: vel}
}

// A take replaces the bars it covers, quantised and spelled in the key, and
// leaves the bars around it alone.
func TestWriteTakeReplacesItsBars(t *testing.T) {
	s := one(t, "4/4", 1, "| qG4 qA4 qB4 qC5 | wD5 | wE5 | qF#5 qG5 hr |")
	// Bar 2-3 played: quarters F# A, a half B (late and early by a few
	// ms), then a whole C... cut where the take ends.
	notes := []notate.Note{
		played(1.004, 1.24, 66, 80), played(1.247, 1.49, 69, 82), played(1.502, 1.99, 71, 78),
		played(2.0, 3.4, 72, 80),
	}
	n, err := WriteTake(s, 0, 2, 3, notes, TakeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("wrote %d notes, want 4", n)
	}
	want := []string{"qG4 qA4 qB4 qC5", "qF#4(mf) qA4 hB4", "wC5", "qF#5 qG5 hr"}
	for b, w := range want {
		if got := barText(s, 0, b+1); got != w {
			t.Errorf("bar %d = %q, want %q", b+1, got, w)
		}
	}
	fullBars(t, s)
}

// A grand staff splits a take at middle C; a second voice appears where the
// playing overlaps, and goes again from bars the take writes without one.
func TestWriteTakeGrandStaffAndVoices(t *testing.T) {
	s := score.New("t")
	m44, _ := score.ParseMeter("4/4")
	s.Meter = []score.MeterMark{{Bar: 1, Meter: m44}}
	l := score.NewLine("Piano", "keys/piano", "treble")
	l.Staves = append(l.Staves, &score.Staff{Clef: "bass", Voices: []*score.Voice{{}}})
	s.Lines = []*score.Line{l}
	v2, _ := score.ParseVoice("| wE4 | wE4 |")
	l.Staves[0].Voices = append(l.Staves[0].Voices, v2)
	s.Bars = 2
	notes := []notate.Note{
		played(0, 1, 48, 70), // C3, the left hand
		played(0, 0.5, 72, 70), played(0.5, 1, 74, 70),
		played(0, 1, 64, 70), // E4 held under the right hand's line
	}
	if _, err := WriteTake(s, 0, 1, 1, notes, TakeOptions{NoDynamics: true}); err != nil {
		t.Fatal(err)
	}
	up := l.Staves[0]
	if got := score.FormatMeasure(up.Voices[0].Measure(1)); got != "hC5 hD5" {
		t.Errorf("upper voice 1 = %q", got)
	}
	if got := score.FormatMeasure(up.Voices[1].Measure(1)); got != "wE4" {
		t.Errorf("upper voice 2 = %q", got)
	}
	if got := score.FormatMeasure(l.Staves[1].Voices[0].Measure(1)); got != "wC3" {
		t.Errorf("lower staff = %q", got)
	}
	if got := score.FormatMeasure(up.Voices[1].Measure(2)); got != "wE4" {
		t.Errorf("bar 2 is not the take's: %q", got)
	}
	// A take in bar 2 with one voice clears voice 2 there.
	if _, err := WriteTake(s, 0, 2, 2, []notate.Note{played(1, 2, 67, 70)}, TakeOptions{NoDynamics: true}); err != nil {
		t.Fatal(err)
	}
	if got := score.FormatMeasure(up.Voices[0].Measure(2)); got != "wG4" {
		t.Errorf("bar 2 voice 1 = %q", got)
	}
	if got := score.FormatMeasure(up.Voices[1].Measure(2)); got != "" {
		t.Errorf("bar 2 voice 2 should be empty: %q", got)
	}
	if got := score.FormatMeasure(l.Staves[1].Voices[0].Measure(2)); got != "R" {
		t.Errorf("bar 2 lower staff: %q", got)
	}
}

// An empty take leaves bar rests; a tie into the replaced bars comes off;
// bad ranges are refused.
func TestWriteTakeEdges(t *testing.T) {
	s := one(t, "3/4", 0, "| h.C5~ | h.C5 | qD5 qE5 qF5 |")
	if _, err := WriteTake(s, 0, 2, 2, nil, TakeOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 2); got != "R" {
		t.Errorf("an empty take: %q", got)
	}
	if got := barText(s, 0, 1); got != "h.C5" {
		t.Errorf("the tie into the take should come off: %q", got)
	}
	if _, err := WriteTake(s, 0, 3, 5, nil, TakeOptions{}); err == nil {
		t.Error("bars past the end")
	}
	if _, err := WriteTake(s, 2, 1, 1, nil, TakeOptions{}); err == nil {
		t.Error("no such line")
	}
	fullBars(t, s)
}

// A take keeps what belongs to the staff in the bars it replaces — a
// hairpin, an 8va and a slur begun before it still end where they did,
// technique text still switches — while the replaced notes' own marks go.
func TestWriteTakeKeepsStaffMarks(t *testing.T) {
	s := one(t, "4/4", 1, `| qC5(cresc,8va,slur) qD5 qE5 qF5 | qG5(hairpin_end,tech="pizz.") qA5(stacc,ly=la) qB5(ottava_end) qC6(slur_end) | wC5 |`)
	notes := []notate.Note{played(1.0, 1.5, 79, 80), played(1.5, 2.0, 84, 80)}
	if _, err := WriteTake(s, 0, 2, 2, notes, TakeOptions{NoDynamics: true}); err != nil {
		t.Fatal(err)
	}
	if got, want := barText(s, 0, 2), `hG5(hairpin_end,tech=pizz.) hC6(ottava_end,slur_end)`; got != want {
		t.Errorf("bar 2 = %q, want %q", got, want)
	}
	// With the take's own dynamics and pedal, the old ones in its bars go.
	s = one(t, "4/4", 1, "| qC5(ped) qD5 qE5 qF5 | qG5(p) qA5(ped_end) hB5 | wC5 |")
	o := TakeOptions{Pedal: []notate.PedalSpan{{Down: score.F(1, 1), Up: score.F(3, 2)}}}
	if _, err := WriteTake(s, 0, 2, 2, notes, o); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 2); got == "" || containsMark(s, 2, "p") || countMark(s, "ped_end") != 1 {
		t.Errorf("bar 2 = %q: the old dynamic or pedal stayed", got)
	}
	fullBars(t, s)
}

func containsMark(s *score.Score, bar int, name string) bool {
	for _, ev := range s.Lines[0].Staves[0].Voices[0].Measures[bar-1].Events {
		if ev.Has(name) {
			return true
		}
	}
	return false
}

func countMark(s *score.Score, name string) int {
	n := 0
	for _, m := range s.Lines[0].Staves[0].Voices[0].Measures {
		for _, ev := range m.Events {
			if ev.Has(name) {
				n++
			}
		}
	}
	return n
}
