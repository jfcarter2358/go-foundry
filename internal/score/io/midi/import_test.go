package midi

import (
	"sort"
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/io/notate"
)

func mustImport(t *testing.T, f *File, o Options) (*score.Score, []string) {
	t.Helper()
	s, warns, err := Import(f, o)
	if err != nil {
		t.Fatal(err)
	}
	if is := s.Validate(nil); score.HasErrors(is) {
		t.Fatalf("import does not validate: %v", is)
	}
	if _, err := score.Parse(mustMarshal(t, s)); err != nil {
		t.Fatalf("import does not survive its own file: %v", err)
	}
	return s, warns
}

func mustMarshal(t *testing.T, s *score.Score) []byte {
	t.Helper()
	b, err := score.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func voiceText(s *score.Score, line, staff, voice int) string {
	return score.FormatVoice(s.Lines[line].Staves[staff].Voices[voice])
}

// A format-0 file: one track, three channels interleaved with running
// status, sysex and an unknown meta event on the way.
func TestImportFormat0SplitsChannels(t *testing.T) {
	useRepoResources(t)
	f := fixture(t, "format0_band.mid")
	if f.Format != 0 || len(f.Tracks) != 1 {
		t.Fatalf("format %d, %d tracks", f.Format, len(f.Tracks))
	}
	a := Analyze(f)
	if len(a.Parts) != 3 || a.Notes != 100 || a.MeterGuessed || a.KeyGuessed || a.Fifths != 2 || a.Tempo != 100 {
		t.Fatalf("analysis: %+v", a)
	}
	wantParts := []struct{ name, inst, clef string }{
		{"Acoustic Grand Piano", "keys/grand_piano", "grand"},
		{"Electric Bass (finger)", "bass/bass_finger", "bass"},
		{"Drums", "percussion/drum_kit", "percussion"},
	}
	for i, w := range wantParts {
		p := a.Parts[i]
		if p.Name != w.name || p.Instrument != w.inst || p.Clef != w.clef {
			t.Errorf("part %d: %q %s %s, want %+v", i, p.Name, p.Instrument, p.Clef, w)
		}
	}
	s, _ := mustImport(t, f, Options{})
	if s.Title != "Band" || s.Measures() != 4 {
		t.Errorf("title %q, %d bars", s.Title, s.Measures())
	}
	if got := voiceText(s, 0, 0, 0); !strings.HasPrefix(got, "| qD4+F#4+A4(mf) qD4+F#4+A4 qD4+F#4+A4 qD4+F#4+A4\n| qG4+B4+D5") {
		t.Errorf("piano, spelled in D:\n%s", got)
	}
	if got := voiceText(s, 1, 0, 0); got != "| wD2(f)\n| wG2\n| wA2\n| wD2\n|\n" {
		t.Errorf("bass:\n%s", got)
	}
	if got := voiceText(s, 2, 0, 0); !strings.HasPrefix(got, "| eC2+F#2(mf) eF#2 eD2+F#2 eF#2") {
		t.Errorf("drums:\n%s", got)
	}
}

// A format-1 file as a sequencer writes it, played by a human: onsets
// wander, notes are released early, velocity rises bar by bar.
func TestImportFormat1Orchestra(t *testing.T) {
	useRepoResources(t)
	f := fixture(t, "format1_orchestra.mid")
	s, warns := mustImport(t, f, Options{Name: "hangar_sketch"})
	if len(warns) != 0 {
		t.Errorf("warnings: %v", warns)
	}
	if s.Title != "Hangar Sketch" || s.Composer != "J. Composer" || s.Measures() != 12 {
		t.Errorf("title %q composer %q bars %d", s.Title, s.Composer, s.Measures())
	}
	// Maps: meter and key change at bar 9, a ramp over bar 7, rehearsal
	// marks, a cue.
	if len(s.Meter) != 2 || s.Meter[1].Bar != 9 || s.Meter[1].Meter.String() != "3/4" {
		t.Errorf("meter %+v", s.Meter)
	}
	if len(s.Key) != 2 || s.Key[1] != (score.KeyMark{Bar: 9, Fifths: -3, Mode: "major"}) {
		t.Errorf("key %+v", s.Key)
	}
	if len(s.Tempo) != 3 || s.Tempo[1].At.Bar != 7 || !s.Tempo[1].Ramp || s.Tempo[1].BPM != 96 || s.Tempo[2].BPM != 120 || s.Tempo[2].At.Bar != 8 {
		t.Errorf("tempo %+v", s.Tempo)
	}
	if len(s.Sections) != 2 || s.Sections[0].Name != "A" || s.Sections[1] != (score.Section{Name: "B", Bar: 5}) {
		t.Errorf("sections %+v", s.Sections)
	}
	if len(s.Markers) != 1 || s.Markers[0].Name != "boom" || s.FormatPos(s.Markers[0].At) != "10:2" {
		t.Errorf("markers %+v", s.Markers)
	}
	var lines []string
	for _, l := range s.Lines {
		lines = append(lines, l.Name+"="+l.Instrument)
	}
	if got := strings.Join(lines, " "); got != "Flute=woodwinds/flute Violins=strings/violin_section Cello=strings/cello_section Piano=keys/grand_piano Click=percussion/drum_kit" {
		t.Errorf("lines %s", got)
	}
	// The flute: quantised (triplets in bar 3), released notes held,
	// velocity as dynamics, the expression swell as a volume lane.
	want := "| qD5(p) qE5 hF#5\n| q.G5 eF#5 hE5\n| qD5(mp) 3{eE5 eF#5 eG5} hA5\n| wG5\n| eB5(mf) eA5 eG5 eF#5 hE5\n| hD5 hG5\n| qF#5(f) qE5 qD5 qE5\n| wG5\n| qEb5(mf) qF5 qG5\n| h.Ab5\n| qG5(mp) qF5 qEb5\n| h.Eb5\n|\n"
	if got := voiceText(s, 0, 0, 0); got != want {
		t.Errorf("flute:\n%s\nwant:\n%s", got, want)
	}
	lane := s.Lines[0].Lane("volume")
	if lane == nil || len(lane.Points) != 3 || lane.Points[1].Value != 1 || s.FormatPos(lane.Points[1].At) != "2:3" {
		t.Errorf("expression lane %+v", lane)
	}
	// The violins: a held half note under moving quarters is two voices.
	vl := s.Lines[1].Staves[0]
	if len(vl.Voices) != 2 || !strings.HasPrefix(score.FormatVoice(vl.Voices[0]), "| qD5(mp) qE5 qD5 qC5") || !strings.HasPrefix(score.FormatVoice(vl.Voices[1]), "| hG4 hF#4") {
		t.Errorf("violins:\n%s\n%s", score.FormatVoice(vl.Voices[0]), score.FormatVoice(vl.Voices[1]))
	}
	// The cello's fader and pan came from its controllers.
	if c := s.Lines[2]; c.Volume != 0.9 || c.Pan != -0.381 {
		t.Errorf("cello mixer %v %v", c.Volume, c.Pan)
	}
	// The piano is a grand staff split at middle C.
	pno := s.Lines[3]
	if len(pno.Staves) != 2 || !strings.HasPrefix(score.FormatVoice(pno.Staves[0].Voices[0]), "| qG4+B4+D5(mp) qG4+B4+D5") || !strings.HasPrefix(score.FormatVoice(pno.Staves[1].Voices[0]), "| wG1+G2") {
		t.Errorf("piano:\n%s\n%s", score.FormatVoice(pno.Staves[0].Voices[0]), score.FormatVoice(pno.Staves[1].Voices[0]))
	}
	// Short clicks keep their rests.
	if got := voiceText(s, 4, 0, 0); !strings.HasPrefix(got, "| sF#2(p) e.r sF#2 e.r") {
		t.Errorf("click:\n%s", got)
	}
}

func TestImportGuessesMeterAndKey(t *testing.T) {
	useRepoResources(t)
	f := fixture(t, "waltz_nometa.mid")
	a := Analyze(f)
	if !a.MeterGuessed || a.Meter.String() != "3/4" || !a.KeyGuessed || a.Fifths != -1 || a.Minor {
		t.Fatalf("guessed %s, key %d minor %v", a.Meter, a.Fifths, a.Minor)
	}
	s, warns := mustImport(t, f, Options{})
	if len(warns) != 2 || !strings.Contains(warns[0], "guessed 3/4") || !strings.Contains(warns[1], "guessed F major") {
		t.Errorf("warnings %v", warns)
	}
	if s.Measures() != 16 || s.MeterAt(1).String() != "3/4" || s.KeyAt(1).Fifths != -1 {
		t.Errorf("%d bars of %s in %d", s.Measures(), s.MeterAt(1), s.KeyAt(1).Fifths)
	}
	// The tune: B-flat, not A-sharp, in F.
	if got := voiceText(s, 0, 0, 0); !strings.Contains(got, "| hBb5 qA5") {
		t.Errorf("tune:\n%s", got)
	}
	// An override wins over the guess.
	m := score.Meter{Groups: []int{6}, Den: 8}
	s, _ = mustImport(t, f, Options{Meter: &m, Key: &score.KeyMark{Fifths: 2, Mode: "minor"}})
	if s.MeterAt(1).String() != "6/8" || s.Measures() != 16 || s.KeyAt(1) != (score.KeyMark{Bar: 1, Fifths: 2, Mode: "minor"}) {
		t.Errorf("override: %s %d bars %+v", s.MeterAt(1), s.Measures(), s.KeyAt(1))
	}
}

func TestImportReadsSloppyFiles(t *testing.T) {
	useRepoResources(t)
	f := fixture(t, "sloppy.mid")
	s, _ := mustImport(t, f, Options{})
	if s.Lines[0].Instrument != "strings/violin_section" {
		t.Errorf("program 40 is %s", s.Lines[0].Instrument)
	}
	// The note never released stops a beat on.
	if got := voiceText(s, 0, 0, 0); got != "| qC4(f) qD4 qE4 qr\n|\n" {
		t.Errorf("sloppy:\n%s", got)
	}
}

func TestImportOptions(t *testing.T) {
	useRepoResources(t)
	f := fixture(t, "format1_orchestra.mid")
	// Skip parts, rename one, put the violins on a viola, no dynamics, one
	// voice per staff, an eighth grid without triplets.
	s, _ := mustImport(t, f, Options{
		Choices:    []Choice{{Skip: true}, {Name: "Strings", Instrument: "strings/viola_section", Clef: "alto"}, {Skip: true}, {Clef: "treble"}, {Skip: true}},
		NoDynamics: true, Voices: 1, Grid: notate.Grid{Unit: score.F(1, 8)},
	})
	if len(s.Lines) != 2 || s.Lines[0].Name != "Strings" || s.Lines[0].Instrument != "strings/viola_section" || s.Lines[0].Staves[0].Clef != "alto" {
		t.Fatalf("lines %+v", s.Lines)
	}
	if v := s.Lines[0].Staves[0].Voices; len(v) != 1 || !strings.HasPrefix(score.FormatVoice(v[0]), "| qG4+D5 qE5 qF#4+D5 qC5") {
		t.Errorf("one voice:\n%s", score.FormatVoice(v[0]))
	}
	if len(s.Lines[1].Staves) != 1 || strings.Contains(score.FormatVoice(s.Lines[1].Staves[0].Voices[0]), "(") {
		t.Errorf("piano on one staff without dynamics:\n%s", score.FormatVoice(s.Lines[1].Staves[0].Voices[0]))
	}
	// Every part skipped is an error, not an empty score.
	if _, _, err := Import(f, Options{Choices: []Choice{{Skip: true}, {Skip: true}, {Skip: true}, {Skip: true}, {Skip: true}}}); err == nil {
		t.Error("an import of nothing worked")
	}
}

// played is a file of one part: notes at ticks (480 a quarter), each with
// its length, pitch and velocity.
func played(notes [][4]int64) *File {
	var evs []Event
	for _, n := range notes {
		evs = append(evs, Event{Tick: n[0], Kind: NoteOn, A: uint8(n[2]), B: uint8(n[3])},
			Event{Tick: n[0] + n[1], Kind: NoteOff, A: uint8(n[2])})
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].Tick < evs[j].Tick })
	return &File{Format: 0, Division: 480, Tracks: []Track{{Events: evs}}}
}

// bars plays a pattern of (eighth offset, length in eighths, accent) for
// a number of bars of the given eighths: a loud low note on each accent.
func barsOf(eighths int, pattern [][3]int, bars int) *File {
	var ns [][4]int64
	for b := 0; b < bars; b++ {
		for _, p := range pattern {
			at := int64(b*eighths+p[0]) * 240
			pitch, vel := int64(67), int64(60)
			if p[2] == 1 {
				pitch, vel = 43, 112
			}
			ns = append(ns, [4]int64{at, int64(p[1]) * 240, pitch, vel})
		}
	}
	return played(ns)
}

// With no time signature the meter is guessed from where the strong notes
// fall and how the beats divide: 4/4, 3/4, 6/8, 5/4, 7/8, 9/8 and 12/8.
func TestImportGuessesMoreMeters(t *testing.T) {
	quarters := func(n int) [][3]int {
		var p [][3]int
		for i := 0; i < n; i++ {
			acc := 0
			if i == 0 {
				acc = 1
			}
			p = append(p, [3]int{2 * i, 2, acc})
		}
		return p
	}
	cases := []struct {
		name string
		f    *File
		want string
	}{
		{"4/4", barsOf(8, quarters(4), 12), "4/4"},
		{"3/4", barsOf(6, quarters(3), 12), "3/4"},
		{"5/4", barsOf(10, quarters(5), 12), "5/4"},
		{"6/8", barsOf(6, [][3]int{{0, 1, 1}, {1, 1, 0}, {2, 1, 0}, {3, 1, 0}, {4, 1, 0}, {5, 1, 0}, {3, 1, 0}}, 12), "6/8"},
		{"7/8", barsOf(7, [][3]int{{0, 2, 1}, {2, 2, 0}, {4, 3, 0}}, 12), "7/8"},
		{"9/8", barsOf(9, [][3]int{{0, 3, 1}, {3, 3, 0}, {6, 3, 0}}, 12), "9/8"},
	}
	for _, c := range cases {
		a := Analyze(c.f)
		if !a.MeterGuessed || a.Meter.String() != c.want {
			t.Errorf("%s: guessed %s", c.name, a.Meter)
		}
	}
	// Triplet eighths through 4/4's beats: 12/8.
	var ns [][4]int64
	for b := 0; b < 12; b++ {
		for k := 0; k < 12; k++ {
			vel, pitch := int64(60), int64(67)
			if k == 0 {
				vel, pitch = 112, 43
			}
			ns = append(ns, [4]int64{int64(b*1920 + k*160), 160, pitch, vel})
		}
	}
	if a := Analyze(played(ns)); a.Meter.String() != "12/8" {
		t.Errorf("triplets: guessed %s", a.Meter)
	}
}
