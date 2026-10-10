package musicxml

import (
	"archive/zip"
	"bytes"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/score"
	"foundry/internal/score/interpret"
	"foundry/internal/tracker"
)

// useRepoResources points the library at this repository's resources/, so
// instrument guesses are checked against the real starter library.
func useRepoResources(t *testing.T) {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(filepath.Dir(here), "..", "..", "..", "..", "resources")
	t.Cleanup(func() {
		fileutil.ResourcesPath = prev
		tracker.ReloadInstruments()
	})
	tracker.ReloadInstruments()
}

func load(t *testing.T, name string) (*score.Score, []string) {
	t.Helper()
	d, err := ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	s, warns, err := Import(d, Options{Name: strings.TrimSuffix(name, filepath.Ext(name))})
	if err != nil {
		t.Fatal(err)
	}
	if is := s.Validate(nil); score.HasErrors(is) {
		t.Fatalf("%s does not validate: %v", name, is)
	}
	b, err := score.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := score.Parse(b); err != nil {
		t.Fatalf("%s does not survive its own file: %v\n%s", name, err, b)
	}
	return s, warns
}

func voice(s *score.Score, line, staff, v int) string {
	return score.FormatVoice(s.Lines[line].Staves[staff].Voices[v])
}

func TestImportSimple(t *testing.T) {
	useRepoResources(t)
	s, warns := load(t, "simple.musicxml")
	if len(warns) > 0 {
		t.Errorf("warnings: %v", warns)
	}
	if s.Title != "Simple Tune" || s.Composer != "A. Writer" || s.Measures() != 5 {
		t.Errorf("title %q composer %q bars %d", s.Title, s.Composer, s.Measures())
	}
	if len(s.Tempo) != 1 || s.Tempo[0].BPM != 96 || s.Tempo[0].Text != "Allegretto" {
		t.Errorf("tempo %+v", s.Tempo)
	}
	var meters []string
	for _, m := range s.Meter {
		meters = append(meters, m.Meter.String())
	}
	if strings.Join(meters, " ") != "4/4 3/4 6/8" || s.Meter[2].Bar != 5 {
		t.Errorf("meters %v", s.Meter)
	}
	if len(s.Key) != 2 || s.Key[1] != (score.KeyMark{Bar: 3, Fifths: -1, Mode: "major"}) {
		t.Errorf("keys %+v", s.Key)
	}
	if l := s.Lines[0]; l.Name != "Flute" || l.Instrument != "woodwinds/flute" {
		t.Errorf("line %s %s", l.Name, l.Instrument)
	}
	want := "| qG4(mf) e.A4 sB4 hC5~\n| qC5 qr hE4+G4+C5\n| qF4(p) qBb4(stacc) qA4(acc,ten)\n| R\n| eD5 eC5 eBb4 q.F4(ferm)\n|\n"
	if got := voice(s, 0, 0, 0); got != want {
		t.Errorf("flute:\n%s\nwant:\n%s", got, want)
	}
}

// What Sibelius writes: divisions that do not hold a triplet, voices 1, 2
// and 5 over two staves, a pickup, nested tuplets with bare brackets.
func TestImportSibeliusPiano(t *testing.T) {
	useRepoResources(t)
	for _, file := range []string{"sibelius_piano.musicxml", "utf16.musicxml"} {
		s, warns := load(t, file)
		if len(warns) > 0 {
			t.Errorf("%s warnings: %v", file, warns)
		}
		if !s.Pickup.Eq(score.F(1, 4)) || s.Measures() != 5 || s.Title != "Piano Study" {
			t.Errorf("%s: pickup %s, %d bars, %q", file, s.Pickup, s.Measures(), s.Title)
		}
		p := s.Lines[0]
		if p.Instrument != "keys/grand_piano" || len(p.Staves) != 2 || p.Staves[0].Clef != "treble" || p.Staves[1].Clef != "bass" {
			t.Fatalf("%s: piano %s with %d staves", file, p.Instrument, len(p.Staves))
		}
		if len(p.Staves[0].Voices) != 2 || len(p.Staves[1].Voices) != 1 {
			t.Errorf("%s: voices %d and %d", file, len(p.Staves[0].Voices), len(p.Staves[1].Voices))
		}
		upper := "| qD5\n| 3{eG5 eA5 eB5} qC6 /eA5 hB5\n| 3{qE5(p,cresc) 3{eF#5! eG5 eA5} qB5(hairpin_end)} hC6(f)\n| hD5~ qD5 qr\n| wG4+B4+D5(arp)\n|\n"
		if got := voice(s, 0, 0, 0); got != upper {
			t.Errorf("%s upper voice 1:\n%s\nwant:\n%s", file, got, upper)
		}
		if got := voice(s, 0, 0, 1); got != "|\n| hD5\n|\n| w_\n|\n" {
			t.Errorf("%s upper voice 2:\n%s", file, got)
		}
		// The cross-staff D4 stays in the lower staff's voice; pedal marks.
		if got := voice(s, 0, 1, 0); got != "| qr\n| wG2+G3\n| hD3(ped) qG3 qD4(ped_end)\n| R\n| wG2\n|\n" {
			t.Errorf("%s lower:\n%s", file, got)
		}
		if len(s.Sections) != 1 || s.Sections[0] != (score.Section{Name: "A", Bar: 3}) {
			t.Errorf("%s sections %+v", file, s.Sections)
		}
		if len(s.Repeats) != 1 || s.Repeats[0].Start != 3 || s.Repeats[0].End != 4 {
			t.Errorf("%s repeats %+v", file, s.Repeats)
		}
		if n := len(s.Tempo); n != 2 || s.Tempo[1].Text != "Andante" || s.Tempo[1].BPM != 72 || s.Tempo[0].BPM != 72 {
			t.Errorf("%s tempo %+v", file, s.Tempo)
		}
		// Time is exact: the triplets Sibelius wrote as 85, 85 and 86
		// divisions start on thirds of a beat.
		perf, err := interpret.Interpret(s, interpret.Options{NoRepeats: true})
		if err != nil {
			t.Fatal(err)
		}
		var starts []int64
		for _, n := range perf.Notes {
			if n.Line == 0 && n.Staff == 0 && n.Voice == 0 && n.Bar == 2 && n.Grace == score.NotGrace {
				starts = append(starts, n.Tick)
			}
		}
		bar2 := perf.BarTick(2)
		want := []int64{0, 320, 640, 960, 1920}
		if len(starts) != len(want) {
			t.Fatalf("%s: bar 2 starts %v", file, starts)
		}
		for i := range want {
			if starts[i]-bar2 != want[i] {
				t.Errorf("%s: bar 2 note %d at %d, want %d", file, i, starts[i]-bar2, want[i])
			}
		}
	}
}

func TestImportTransposingAndPercussion(t *testing.T) {
	useRepoResources(t)
	s, warns := load(t, "transposing.musicxml")
	if len(warns) > 0 {
		t.Errorf("warnings: %v", warns)
	}
	// The concert key is the violin's (C, then G), not the clarinet's D.
	if len(s.Key) != 2 || s.Key[0].Fifths != 0 || s.Key[1] != (score.KeyMark{Bar: 2, Fifths: 1, Mode: "major"}) {
		t.Errorf("keys %+v", s.Key)
	}
	if s.MeterAt(3).String() != "4+3/8" {
		t.Errorf("composite meter %s", s.MeterAt(3))
	}
	type want struct{ name, inst, clef, transpose, music string }
	for i, w := range []want{
		{"Clarinet in Bb", "woodwinds/clarinet", "treble", "-M2", "| eD5 eE5 qF5\n| hB4\n| h..G4\n|\n"},
		{"Horn in F", "brass/horn", "treble", "-P5", "| qC4 qG4\n| hr\n| R\n|\n"},
		{"Violin", "strings/violin_section", "treble", "P1", "| hC5(tech=pizz.)\n| hF#5(tech=arco)\n| h..G5\n|\n"},
		{"Viola", "strings/viola_section", "alto", "P1", "| hC4\n|\n"},
		{"Drum Set", "percussion/drum_kit", "percussion", "P1", "| qD2 qD2\n|\n"},
		{"Guitar", "plucked/guitar_nylon", "treble_8vb", "P1", "| qE2 qB3\n|\n"},
	} {
		l := s.Lines[i]
		st := l.Staves[0]
		if l.Name != w.name || l.Instrument != w.inst || st.Clef != w.clef || st.Transpose.String() != w.transpose {
			t.Errorf("line %d: %s %s %s %s, want %+v", i, l.Name, l.Instrument, st.Clef, st.Transpose, w)
		}
		if got := voice(s, i, 0, 0); got != w.music {
			t.Errorf("%s:\n%s\nwant:\n%s", w.name, got, w.music)
		}
	}
	// The kick is its own voice, at its General MIDI key.
	if got := voice(s, 4, 0, 1); got != "| hC2\n|\n" {
		t.Errorf("kick:\n%s", got)
	}
	if s.Lines[0].Pan != -0.5 {
		t.Errorf("clarinet pan %v", s.Lines[0].Pan)
	}
}

func TestImportTimewiseAndCompressed(t *testing.T) {
	useRepoResources(t)
	s, _ := load(t, "timewise.musicxml")
	if len(s.Lines) != 2 || s.MeterAt(1).String() != "C" || s.KeyAt(1) != (score.KeyMark{Bar: 1, Fifths: -1, Mode: "minor"}) {
		t.Fatalf("timewise: %d lines, %s, %+v", len(s.Lines), s.MeterAt(1), s.KeyAt(1))
	}
	if voice(s, 0, 0, 0) != "| hD5 hA4\n| wBb4(ferm)\n|\n" || voice(s, 1, 0, 0) != "| wD3\n| wG2\n|\n" || s.Lines[1].Instrument != "woodwinds/bassoon" {
		t.Errorf("timewise music:\n%s%s", voice(s, 0, 0, 0), voice(s, 1, 0, 0))
	}
	// The .mxl's container names the score in its subfolder, not the decoy.
	c, _ := load(t, "compressed.mxl")
	plain, _ := load(t, "simple.musicxml")
	if voice(c, 0, 0, 0) != voice(plain, 0, 0, 0) || c.Title != plain.Title {
		t.Errorf("compressed differs from plain")
	}
}

func TestReadRejectsNonScores(t *testing.T) {
	var zipped bytes.Buffer
	z := zip.NewWriter(&zipped)
	w, _ := z.Create("META-INF/container.xml")
	w.Write([]byte(`<container><rootfiles><rootfile full-path="missing.xml"/></rootfiles></container>`))
	z.Close()
	for name, b := range map[string][]byte{
		"html":          []byte("<html><body>no</body></html>"),
		"opus":          []byte(`<opus><title>x</title></opus>`),
		"broken":        []byte("<score-partwise><part-list>"),
		"empty archive": zipped.Bytes(),
		"empty":         nil,
	} {
		if d, err := Read(b); err == nil {
			if _, _, err := Import(d, Options{}); err == nil {
				t.Errorf("%s: imported", name)
			}
		}
	}
}

func TestPartsForTheDialog(t *testing.T) {
	useRepoResources(t)
	d, err := ReadFile(filepath.Join("testdata", "transposing.musicxml"))
	if err != nil {
		t.Fatal(err)
	}
	ps := d.Parts()
	if len(ps) != 6 || ps[0].Program != 71 || ps[0].Transpose.String() != "-M2" || !ps[4].Percussion || ps[5].Clefs[0] != "treble_8vb" {
		t.Errorf("parts %+v", ps)
	}
	// Skip and override.
	s, _, err := Import(d, Options{Choices: []Choice{{Skip: true}, {Skip: true}, {Name: "Fiddle", Instrument: "plucked/banjo"}, {Skip: true}, {Skip: true}, {Skip: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Lines) != 1 || s.Lines[0].Name != "Fiddle" || s.Lines[0].Instrument != "plucked/banjo" {
		t.Errorf("choices: %+v", s.Lines)
	}
}

// What a score cannot hold yet is said, not dropped silently.
func TestImportWarnsAboutWhatItCannotKeep(t *testing.T) {
	useRepoResources(t)
	doc := `<?xml version="1.0"?>
<score-partwise version="4.0"><part-list><score-part id="P1"><part-name>Voice</part-name></score-part></part-list>
<part id="P1">
 <measure number="1"><attributes><divisions>1</divisions><time><beats>2</beats><beat-type>4</beat-type></time><clef><sign>G</sign><line>2</line></clef></attributes>
  <harmony><root><root-step>C</root-step></root><kind>major</kind></harmony>
  <note><pitch><step>C</step><octave>5</octave></pitch><duration>2</duration><voice>1</voice><type>half</type><lyric><text>la</text></lyric></note>
  <barline location="right"><ending number="1" type="stop"/><repeat direction="backward"/></barline></measure>
 <measure number="2"><attributes><clef><sign>F</sign><line>4</line></clef></attributes>
  <note><pitch><step>C</step><octave>3</octave></pitch><duration>2</duration><voice>1</voice><type>half</type></note>
  <sound dacapo="yes"/></measure>
</part></score-partwise>`
	d, err := Read([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	s, warns, err := Import(d, Options{})
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(warns, "\n")
	for _, want := range []string{"voltas", "D.C.", "clef change", "lyrics", "chord symbols"} {
		if !strings.Contains(all, want) {
			t.Errorf("no warning about %s:\n%s", want, all)
		}
	}
	if s.Lines[0].Instrument != "choir/choir" || len(s.Repeats) != 1 {
		t.Errorf("voice part %s, repeats %+v", s.Lines[0].Instrument, s.Repeats)
	}
}

// Some exporters write <time-modification> on every note of a tuplet and
// no <tuplet> brackets at all: each group closes where its notes come to
// a plain value.
func TestImportTupletsWithoutBrackets(t *testing.T) {
	useRepoResources(t)
	note := func(step string, dur int, typ string, actual, normal int) string {
		tm := ""
		if actual > 0 {
			tm = "<time-modification><actual-notes>" + itoa(actual) + "</actual-notes><normal-notes>" + itoa(normal) + "</normal-notes></time-modification>"
		}
		return "<note><pitch><step>" + step + "</step><octave>5</octave></pitch><duration>" + itoa(dur) + "</duration><voice>1</voice><type>" + typ + "</type>" + tm + "</note>"
	}
	// 60 divisions: a triplet eighth is 20, a quintuplet 16th is 12.
	body := note("C", 20, "eighth", 3, 2) + note("D", 20, "eighth", 3, 2) + note("E", 20, "eighth", 3, 2) +
		note("F", 40, "quarter", 3, 2) + note("G", 20, "eighth", 3, 2) +
		note("A", 12, "16th", 5, 4) + note("B", 12, "16th", 5, 4) + note("A", 24, "eighth", 5, 4) + note("G", 12, "16th", 5, 4) +
		note("C", 60, "quarter", 0, 0)
	doc := `<score-partwise version="3.0"><part-list><score-part id="P1"><part-name>Flute</part-name></score-part></part-list><part id="P1"><measure number="1">
<attributes><divisions>60</divisions><time><beats>4</beats><beat-type>4</beat-type></time></attributes>` + body + `</measure></part></score-partwise>`
	d, err := Read([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	s, warns, err := Import(d, Options{})
	if err != nil || len(warns) > 0 {
		t.Fatalf("%v %v", err, warns)
	}
	if got, want := voice(s, 0, 0, 0), "| 3{eC5 eD5 eE5} 3{qF5 eG5} 5{sA5 sB5 eA5 sG5} qC5\n|\n"; got != want {
		t.Errorf("got\n%swant\n%s", got, want)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// A measure longer than any cadenza is refused at once, not written out as
// a million breves.
func TestImportRefusesRunawayMeasures(t *testing.T) {
	for _, dur := range []string{"4000", "4000000000"} {
		x := `<?xml version="1.0"?><score-partwise><part-list><score-part id="P1"><part-name>A</part-name></score-part></part-list>` +
			`<part id="P1"><measure number="1"><attributes><divisions>1</divisions><time><beats>4</beats><beat-type>4</beat-type></time></attributes>` +
			`<note><pitch><step>C</step><octave>4</octave></pitch><duration>` + dur + `</duration><voice>1</voice><type>whole</type></note></measure></part></score-partwise>`
		d, err := Read([]byte(x))
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		if _, _, err := Import(d, Options{Name: "x"}); err == nil || !strings.Contains(err.Error(), "at most") {
			t.Errorf("a %s-quarter measure: %v", dur, err)
		}
		if el := time.Since(start); el > 2*time.Second {
			t.Errorf("refusing took %v", el)
		}
	}
}
