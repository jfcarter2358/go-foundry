package midi

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"foundry/internal/score"
	"foundry/internal/tracker"
)

// throughFile exports a score, writes and reads the file, and imports it.
func throughFile(t *testing.T, s *score.Score) (*File, *score.Score) {
	t.Helper()
	f, err := Export(s, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := f.Write(&b); err != nil {
		t.Fatal(err)
	}
	g, err := Read(&b)
	if err != nil {
		t.Fatal(err)
	}
	back, _ := mustImport(t, g, Options{Name: "back", NoDynamics: true})
	return g, back
}

func lineScore(t *testing.T, inst, clef string, voices ...string) *score.Score {
	t.Helper()
	s := score.New("t")
	l := score.NewLine("A", inst, clef)
	l.Staves[0].Voices = nil
	for _, text := range voices {
		v, err := score.ParseVoice(text)
		if err != nil {
			t.Fatal(err)
		}
		l.Staves[0].Voices = append(l.Staves[0].Voices, v)
	}
	s.Lines = []*score.Line{l}
	s.Bars = s.Measures()
	return s
}

func eventsOf(f *File, k Kind) []Event {
	var out []Event
	for _, t := range f.Tracks {
		for _, e := range t.Events {
			if e.Kind == k {
				out = append(out, e)
			}
		}
	}
	return out
}

// A slide on its own goes out as pitch bend (the range set by RPN 0 to
// cover it, the wheel back to the middle after) and comes back as the two
// notes and their glissando.
func TestSlidesAsPitchBend(t *testing.T) {
	s := lineScore(t, "", "treble", "| qC5(gliss) qG5 hr |")
	f, back := throughFile(t, s)
	ons := 0
	for _, e := range eventsOf(f, NoteOn) {
		if e.B > 0 {
			ons++
		}
	}
	if ons != 1 {
		t.Errorf("%d note-ons, want the slide's one", ons)
	}
	bends := eventsOf(f, PitchBend)
	if len(bends) < 3 {
		t.Fatalf("%d pitch bends", len(bends))
	}
	last := bends[len(bends)-1]
	if v := int(last.A) | int(last.B)<<7; v != 8192 {
		t.Errorf("the wheel ends at %d, not the middle", v)
	}
	var rng int
	for _, e := range eventsOf(f, Controller) {
		if e.A == 6 {
			rng = int(e.B)
		}
	}
	if rng != 7 {
		t.Errorf("bend range %d, want 7 (the slide's reach)", rng)
	}
	if got := flat(voiceText(back, 0, 0, 0)); got != "| qC5(gliss) qG5 hr |" {
		t.Errorf("came back as %q", got)
	}
}

// A slide another note sounds under goes out as the run it passes.
func TestASlideWithCompanyIsARun(t *testing.T) {
	s := lineScore(t, "", "treble", "| qC5(gliss) qE5 hr |", "| wC4 |")
	f, _ := throughFile(t, s)
	if b := eventsOf(f, PitchBend); len(b) != 0 {
		t.Errorf("%d pitch bends under a held note", len(b))
	}
	ons := 0
	for _, e := range eventsOf(f, NoteOn) {
		if e.B > 0 {
			ons++
		}
	}
	// C4 held, and C5 C#5 D5 D#5 E5 run.
	if ons < 5 {
		t.Errorf("%d note-ons: the run is missing", ons)
	}
}

// The pedal is CC 64 at its marks, the notes at their written lengths; it
// comes back as pedal marks.
func TestPedalAsCC64(t *testing.T) {
	s := lineScore(t, "keys/piano", "treble", "| qC4(ped) qD4 qE4 qF4(ped_end) | wG4 |")
	f, back := throughFile(t, s)
	var ped []Event
	for _, e := range eventsOf(f, Controller) {
		if e.A == CCSustain {
			ped = append(ped, e)
		}
	}
	if len(ped) != 2 || ped[0].Tick != 0 || ped[0].B != 127 || ped[1].Tick != 3840 || ped[1].B != 0 {
		t.Fatalf("pedal %+v", ped)
	}
	for _, e := range eventsOf(f, NoteOff) {
		if e.A == 60 && e.Tick != 960 {
			t.Errorf("C4 held to %d under the pedal; a file's pedal holds it", e.Tick)
		}
	}
	if got := flat(voiceText(back, 0, 0, 0)); got != "| qC4(ped) qD4 qE4 qF4(ped_end) | wG4 |" {
		t.Errorf("came back as %q", got)
	}
}

// A kit whose map has its own keys goes out as General MIDI drums and
// comes back as its own keys.
func TestKitKeysThroughGeneralMIDI(t *testing.T) {
	prev := score.LoadKitInstrument
	kit := &tracker.Instrument{Name: "Odd Kit", Playback: tracker.Playback{Clef: "percussion", Drums: []tracker.DrumNote{
		{Key: 60, Name: "Bass Drum", Line: 1, Head: "normal"},
		{Key: 62, Name: "Snare", Line: 5, Head: "normal"},
	}}}
	score.LoadKitInstrument = func(string) (*tracker.Instrument, time.Time, error) { return kit, time.Unix(1, 0), nil }
	score.ReloadKits()
	t.Cleanup(func() { score.LoadKitInstrument = prev; score.ReloadKits() })
	s := lineScore(t, "drums/odd", "percussion", "| qC4 qD4 qC4 qD4 |")
	f, back := throughFile(t, s)
	keys := map[int]bool{}
	for _, e := range eventsOf(f, NoteOn) {
		if e.B > 0 {
			keys[int(e.A)] = true
		}
	}
	if !keys[36] || !keys[38] || keys[60] {
		t.Errorf("file keys %v, want GM 36 and 38", keys)
	}
	back.Lines[0].Instrument = "drums/odd"
	if got := flat(voiceText(back, 0, 0, 0)); got != "| qC4 qD4 qC4 qD4 |" {
		t.Errorf("came back as %q (the kit's own keys 60 and 62)", got)
	}
}

// flat is a voice's text on one line.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }
