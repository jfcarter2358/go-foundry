package ftrk

import (
	"math"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/score"
	"foundry/internal/score/interpret"
	"foundry/internal/score/io/midi"
	"foundry/internal/score/play"
	"foundry/internal/tracker"
)

func useRepoResources(t *testing.T) {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(filepath.Dir(here), "..", "..", "..", "..", "resources")
	t.Cleanup(func() {
		fileutil.ResourcesPath = prev
		tracker.ReloadInstruments()
		tracker.ReloadSongs()
	})
	tracker.ReloadInstruments()
	tracker.ReloadSongs()
}

func demo(t *testing.T) *score.Score {
	t.Helper()
	s, err := score.LoadFile(filepath.Join(fileutil.ResourcesPath, "sounds", "score_demo"+score.FileExt))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// gridNotes are a song's notes as played once through: per instrument,
// "start-end:key" in rows, sorted.
func gridNotes(song *tracker.Song) map[string][]string {
	out := map[string][]string{}
	open := map[int]struct {
		at    int
		key   uint8
		instr string
	}{}
	row := 0
	flush := func(c, at int) {
		if n, ok := open[c]; ok {
			out[n.instr] = append(out[n.instr], itoa(n.at)+"-"+itoa(at)+":"+tracker.NoteName(n.key))
			delete(open, c)
		}
	}
	last := map[int]uint8{}
	for _, oi := range song.Order {
		for _, cells := range song.Patterns[oi].Cells {
			for c, cell := range cells {
				switch {
				case cell.Note >= tracker.NoteMin && cell.Note <= tracker.NoteMax:
					flush(c, row)
					in := cell.Instr
					if in == 0 {
						in = last[c]
					}
					last[c] = in
					ref := song.Instruments[in-1].Ref
					if ref == "" {
						ref = song.Instruments[in-1].Name
					}
					open[c] = struct {
						at    int
						key   uint8
						instr string
					}{row, cell.Note, ref}
				case cell.Note == tracker.NoteOff || cell.Note == tracker.NoteCut:
					flush(c, row)
				}
			}
			row++
		}
	}
	for c := range open {
		flush(c, row)
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

func itoa(v int) string {
	s := ""
	if v == 0 {
		return "0"
	}
	for v > 0 {
		s = string(rune('0'+v%10)) + s
		v /= 10
	}
	return s
}

// envelope is a render's loudness every 50 ms.
func envelope(data []float32, rate int) []float64 {
	win := rate / 20
	var out []float64
	for i := 0; i+2*win <= len(data); i += 2 * win {
		var s float64
		for _, v := range data[i : i+2*win] {
			s += float64(v) * float64(v)
		}
		out = append(out, math.Sqrt(s/float64(2*win)))
	}
	return out
}

func corr(a, b []float64) float64 {
	n := min(len(a), len(b))
	var ma, mb float64
	for i := 0; i < n; i++ {
		ma += a[i]
		mb += b[i]
	}
	ma /= float64(n)
	mb /= float64(n)
	var ab, aa, bb float64
	for i := 0; i < n; i++ {
		ab += (a[i] - ma) * (b[i] - mb)
		aa += (a[i] - ma) * (a[i] - ma)
		bb += (b[i] - mb) * (b[i] - mb)
	}
	return ab / math.Sqrt(aa*bb)
}

// The demo score as a song: every note on the grid at its row, the
// library instruments by reference, sections, the mixer; it saves and
// loads, and it sounds like the score.
func TestExportTheDemoScore(t *testing.T) {
	useRepoResources(t)
	s := demo(t)
	song, warns, err := Export(s, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("warnings: %v", warns)
	perf, err := interpret.Interpret(s, interpret.Options{NoSlides: true})
	if err != nil {
		t.Fatal(err)
	}
	if song.LPB != chooseGrid(perf) || song.LPB < 1 {
		t.Errorf("rows a beat %d", song.LPB)
	}
	notes := 0
	for _, ns := range gridNotes(song) {
		notes += len(ns)
	}
	if notes != len(perf.Notes) {
		t.Errorf("%d notes in the song, %d in the performance", notes, len(perf.Notes))
	}
	if len(song.Instruments) != len(s.Lines) {
		t.Fatalf("%d instruments for %d lines", len(song.Instruments), len(s.Lines))
	}
	for i, l := range s.Lines {
		if song.Instruments[i].Ref != l.Instrument || song.Instruments[i].Name != l.Name {
			t.Errorf("instrument %d: %s (%s)", i+1, song.Instruments[i].Name, song.Instruments[i].Ref)
		}
	}
	if len(song.Sections) != len(s.Sections) {
		t.Errorf("%d sections, the score has %d", len(song.Sections), len(s.Sections))
	}
	for _, sec := range song.Sections {
		if s.SectionIndex(sec.Name) < 0 {
			t.Errorf("section %s", sec.Name)
		}
	}
	if math.Abs(float64(song.BPM)-perf.Tempo.Points[0].QPM) > 0.5 {
		t.Errorf("BPM %g, the score starts at %g", song.BPM, perf.Tempo.Points[0].QPM)
	}
	// Saved and read back, it is the same song.
	b, err := tracker.Marshal(song)
	if err != nil {
		t.Fatal(err)
	}
	back, err := tracker.Parse(b)
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if got, want := gridNotes(back), gridNotes(song); len(got) != len(want) {
		t.Errorf("saved and loaded: %d instruments, %d before", len(got), len(want))
	}
	// It sounds like the score: the same loudness over time.
	for _, err := range tracker.ResolveSamples(back) {
		t.Fatal(err)
	}
	ws, _, err := play.RenderScore(s, play.RenderOptions{Rate: 24000})
	if err != nil {
		t.Fatal(err)
	}
	// A song's own length is at its first tempo; the score's rit. is
	// longer.
	wt := tracker.Render(back, tracker.RenderOptions{Rate: 24000, Seconds: float64(len(ws)) / 2 / 24000})
	if c := corr(envelope(ws, 24000), envelope(wt, 24000)); c < 0.8 {
		t.Errorf("the song's loudness follows the score's with correlation %.3f", c)
	}
	// Its last note ends where the score's does.
	last := func(d []float32) float64 {
		for i := len(d) - 1; i >= 0; i-- {
			if math.Abs(float64(d[i])) > 1e-3 {
				return float64(i) / 2 / 24000
			}
		}
		return 0
	}
	if a, b := last(ws), last(wt); math.Abs(a-b) > 1.5 {
		t.Errorf("the song sounds until %.1f s, the score until %.1f s", b, a)
	}
}

// The grid is the coarsest that holds the rhythms: 16ths in 4 rows a
// beat, triplet eighths in 3, both in 12.
func TestTheGridFitsTheRhythms(t *testing.T) {
	useRepoResources(t)
	for voice, want := range map[string]int{
		"| qC4 eD4 sE4 sF4 hG4\n|":             4,
		"| 3{eC4 eD4 eE4} hF4 qG4\n|":          3,
		"| 3{eC4 eD4 eE4} sF4 sG4 e.A4 hB4\n|": 12,
		"| wC4\n|":                             1,
	} {
		s := score.New("grid")
		l := score.NewLine("Piano", "keys/grand_piano", "treble")
		v, err := score.ParseVoice(voice)
		if err != nil {
			t.Fatal(err)
		}
		l.Staves[0].Voices[0] = v
		s.Lines = append(s.Lines, l)
		song, warns, err := Export(s, ExportOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if song.LPB != want {
			t.Errorf("%q: %d rows a beat, want %d", voice, song.LPB, want)
		}
		for _, w := range warns {
			if strings.Contains(w, "moved") {
				t.Errorf("%q: %s", voice, w)
			}
		}
		// Forced coarser, the notes move and it says so.
		if want > 1 {
			_, warns, _ := Export(s, ExportOptions{RowsPerBeat: 1})
			if !strings.Contains(strings.Join(warns, " "), "moved") {
				t.Errorf("%q on a beat grid: no warning (%v)", voice, warns)
			}
		}
	}
	if _, _, err := Export(score.New("x"), ExportOptions{}); err == nil {
		t.Error("a score with no lines exported")
	}
	s := score.New("x")
	s.Lines = append(s.Lines, score.NewLine("P", "keys/grand_piano", "treble"))
	if _, _, err := Export(s, ExportOptions{RowsPerBeat: 7}); err == nil {
		t.Error("7 rows a beat accepted")
	}
}

// A chord and overlapping voices take a channel each; tempo changes are M
// commands; markers are E with their names.
func TestChannelsTempoAndMarkers(t *testing.T) {
	useRepoResources(t)
	s := score.New("x")
	s.Tempo = append(s.Tempo, score.TempoMark{At: score.Pos{Bar: 2}, BPM: 90})
	s.Markers = []score.Marker{{Name: "boom", At: score.Pos{Bar: 2, Offset: score.F(1, 2)}}}
	s.Bars = 2
	l := score.NewLine("Piano", "keys/grand_piano", "treble")
	v1, _ := score.ParseVoice("| hC4+E4+G4 hD4\n| wC5\n|")
	v2, _ := score.ParseVoice("| wC3\n| wG3\n|")
	l.Staves[0].Voices = []*score.Voice{v1, v2}
	s.Lines = append(s.Lines, l)
	song, _, err := Export(s, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(song.Channels) != 4 || song.Channels[0].Name != "Piano" || song.Channels[3].Name != "Piano 4" {
		var names []string
		for _, c := range song.Channels {
			names = append(names, c.Name)
		}
		t.Errorf("channels %v", names)
	}
	text := ""
	for _, oi := range song.Order {
		text += tracker.FormatPattern(song.Patterns[oi], song.FXColumns)
	}
	if !strings.Contains(text, "M5A") || !strings.Contains(text, "E01") || song.Markers[1] != "boom" {
		t.Errorf("no tempo or marker command:\n%s\nmarkers %v", text, song.Markers)
	}
}

// song is a small song with what an import has to carry: two library
// instruments, a section that loops into another, a param bound to a
// channel, a tempo change, a note cut and a delay.
func song(t *testing.T) *tracker.Song {
	t.Helper()
	s := tracker.NewSong("Import Me")
	s.BPM, s.LPB, s.BeatsPerBar = 100, 4, 4
	s.SetChannelCount(3)
	s.Channels[0].Name, s.Channels[1].Name, s.Channels[2].Name = "Lead", "Bass", "Bass 2"
	s.Channels[1].SendA = 0.4
	s.Channels[0].Volume = 0.8
	lead := tracker.NewInstrument("Flute", "")
	lead.Ref = "woodwinds/flute"
	bass := tracker.NewInstrument("Cello", "")
	bass.Ref = "strings/cello_section"
	s.Instruments = []*tracker.Instrument{lead, bass}
	a := tracker.NewPattern("a", 16, 3)
	b := tracker.NewPattern("b", 16, 3)
	set := func(p *tracker.Pattern, r, c int, text string) {
		cell, err := tracker.ParseCell(text)
		if err != nil {
			t.Fatal(err)
		}
		p.Cells[r][c] = cell
	}
	// Bar a: a quarter C5, eighths D5 E5, a half G5; under it a whole C3.
	set(a, 0, 0, "C-5 01 40")
	set(a, 4, 0, "D-5 01 30")
	set(a, 6, 0, "E-5 01 30")
	set(a, 8, 0, "G-5 01 30")
	set(a, 0, 1, "C-3 02 30")
	// Bar b: tempo 80; a half F5 cut short, then a note delayed into place.
	set(b, 0, 0, "F-5 01 20 M50")
	set(b, 8, 0, "===")
	set(b, 12, 0, "A-5 01 20")
	set(b, 0, 1, "F-2 02 30")
	set(b, 0, 2, "C-3 02 30")
	set(b, 12, 2, "===")
	s.Patterns = []*tracker.Pattern{a, b}
	s.Order = []int{0, 1, 0}
	s.Sections = []tracker.Section{{Name: "intro", Start: 0, End: 1, Next: "main"}, {Name: "main", Start: 1, End: 3, Loop: true}}
	s.Params = []tracker.Param{{Name: "intensity", Default: 0.2}}
	s.Bindings = []tracker.Binding{{Param: "intensity", Channel: "Bass", Target: tracker.BindVolume, From: 0.2, To: 1}}
	s.Reverb.Size = 0.9
	s.Compressor.Enabled = true
	if p := s.Validate(); len(p) > 0 {
		t.Fatal(p)
	}
	return s
}

func TestImportASong(t *testing.T) {
	useRepoResources(t)
	sg := song(t)
	a, _, choices, warns, err := Analyze(sg)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) > 0 {
		t.Errorf("warnings: %v", warns)
	}
	if len(a.Parts) != 2 || a.Parts[0].Name != "Flute" || a.Parts[1].Name != "Cello" || choices[0].Instrument != "woodwinds/flute" || choices[1].Instrument != "strings/cello_section" {
		t.Fatalf("parts %+v choices %+v", a.Parts, choices)
	}
	s, warns, err := Import(sg, midi.Options{Name: "imported", NoDynamics: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("warnings: %v", warns)
	if is := s.Validate(nil); score.HasErrors(is) {
		t.Fatal(is)
	}
	if len(s.Lines) != 2 || s.Lines[0].Instrument != "woodwinds/flute" || s.Lines[1].Instrument != "strings/cello_section" {
		t.Fatalf("lines %+v", s.Lines)
	}
	flute := score.FormatVoice(s.Lines[0].Staves[0].Voices[0])
	if want := "| qC5 eD5 eE5 hG5\n| hF5 qr qA5\n| qC5 eD5 eE5 hG5\n|\n"; flute != want {
		t.Errorf("flute:\n%s\nwant\n%s", flute, want)
	}
	if s.Measures() != 3 || s.MeterAt(1).String() != "4/4" {
		t.Errorf("%d bars of %s", s.Measures(), s.MeterAt(1))
	}
	if len(s.Tempo) < 2 || s.Tempo[1].BPM != 80 || s.Tempo[1].At.Bar != 2 {
		t.Errorf("tempo %+v", s.Tempo)
	}
	if len(s.Sections) != 2 || s.Sections[0] != (score.Section{Name: "intro", Bar: 1, Next: "main"}) || s.Sections[1] != (score.Section{Name: "main", Bar: 2, Loop: true}) {
		t.Errorf("sections %+v", s.Sections)
	}
	if len(s.Params) != 1 || len(s.Bindings) != 1 || s.Bindings[0].Line != "Cello" {
		t.Errorf("params %+v bindings %+v", s.Params, s.Bindings)
	}
	if s.Lines[1].SendA != 0.4 || math.Abs(s.Buses.A.FX[0].Param("size", 0)-0.9) > 1e-6 || s.Master[0].Param("enabled", 0) != 1 {
		t.Errorf("mix: send %g, reverb %+v, master %+v", s.Lines[1].SendA, s.Buses.A.FX, s.Master)
	}
	if s.Lines[0].Volume != 0.8 {
		t.Errorf("flute volume %g", s.Lines[0].Volume)
	}
}

// A song made into a score and the score made back into a song plays the
// same notes at the same rows.
func TestSongScoreSongRoundTrip(t *testing.T) {
	useRepoResources(t)
	sg := song(t)
	s, _, err := Import(sg, midi.Options{Name: "rt", NoDynamics: true, NoLegato: true})
	if err != nil {
		t.Fatal(err)
	}
	back, warns, err := Export(s, ExportOptions{RowsPerBeat: sg.LPB})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warns {
		if strings.Contains(w, "moved") {
			t.Error(w)
		}
	}
	want, got := gridNotes(sg), gridNotes(back)
	for inst, ns := range want {
		if strings.Join(got[inst], " ") != strings.Join(ns, " ") {
			t.Errorf("%s:\n%v\ncame back\n%v", inst, ns, got[inst])
		}
	}
	if len(back.Sections) != 2 || back.Sections[0].Next != "main" || !back.Sections[1].Loop {
		t.Errorf("sections %+v", back.Sections)
	}
}

// Every shipped song imports as a score that validates.
func TestTheShippedSongsImport(t *testing.T) {
	useRepoResources(t)
	for _, name := range tracker.ScanSongNames() {
		sg, err := tracker.Load(name)
		if err != nil {
			t.Fatal(err)
		}
		s, _, err := Import(sg, midi.Options{Name: name})
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if is := s.Validate(nil); score.HasErrors(is) {
			t.Errorf("%s: %v", name, is)
		}
		if _, _, err := Export(s, ExportOptions{}); err != nil {
			t.Errorf("%s back to a song: %v", name, err)
		}
	}
}

// A song's own instruments (samples and envelopes in the song, no .finst)
// are played by the library's nearest: the instrument named for their
// sample, else one their name matches; never the piano by default.
func TestEmbeddedInstrumentsTakeLibrarySounds(t *testing.T) {
	useRepoResources(t)
	sg, err := tracker.Load("demo_theme")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"Kick": "drums/kick_808", "Bass": "bass/bass_moog", "Pad": "pads/pad_warm", "Riser": "fx/fx_riser"}
	for _, in := range sg.Instruments {
		if in.Ref != "" {
			t.Fatalf("demo_theme's %s names a library instrument; this test wants embedded ones", in.Name)
		}
	}
	s, warns, err := Import(sg, midi.Options{Name: "theme"})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range s.Lines {
		if w, ok := want[l.Name]; ok && l.Instrument != w {
			t.Errorf("%s plays %s, want %s", l.Name, l.Instrument, w)
		}
		if l.Instrument == "keys/grand_piano" {
			t.Errorf("%s fell back to the piano", l.Name)
		}
	}
	said := false
	for _, w := range warns {
		said = said || strings.Contains(w, "the score plays the library's drums/kick_808")
	}
	if !said {
		t.Errorf("the swap is not said: %q", warns)
	}
	if got := libraryFor(&tracker.Instrument{Name: "Violins", Samples: []string{"no/such_sample"}}); got != "strings/violin_section" {
		t.Errorf("by name: %q", got)
	}
	if got := libraryFor(&tracker.Instrument{Name: "Zzyzx", Samples: []string{"no/such_sample"}}); got != "" {
		t.Errorf("nothing near: %q", got)
	}
}

// A drum that is its own instrument comes in on its one line: every note
// the song strikes it at is its sound, and the import says so.
func TestASingleDrumComesInOnItsLine(t *testing.T) {
	useRepoResources(t)
	score.ReloadKits()
	s := tracker.NewSong("Kick")
	s.BPM, s.LPB, s.BeatsPerBar = 120, 4, 4
	s.SetChannelCount(1)
	kick := tracker.NewInstrument("Kick", "")
	kick.Ref = "drums/kick_808"
	s.Instruments = []*tracker.Instrument{kick}
	p := tracker.NewPattern("a", 16, 1)
	for r, n := range []string{"C-4 01 40", "", "", "", "C-5 01 40", "", "", "", "G-3 01 40"} {
		if n == "" {
			continue
		}
		c, err := tracker.ParseCell(n)
		if err != nil {
			t.Fatal(err)
		}
		p.Cells[r][0] = c
	}
	s.Patterns, s.Order = []*tracker.Pattern{p}, []int{0}
	sc, warns, err := Import(s, midi.Options{Name: "kick", NoDynamics: true})
	if err != nil {
		t.Fatal(err)
	}
	kit := score.KitFor("drums/kick_808")
	if len(kit.Drums) != 1 {
		t.Fatalf("drums/kick_808's map: %+v", kit.Drums)
	}
	for _, m := range sc.Lines[0].Staves[0].Voices[0].Measures {
		for _, e := range m.Events {
			for _, n := range e.Notes {
				if n.Pitch.MIDI() != kit.Drums[0].Key {
					t.Errorf("a kick stored at %s, not its sound's key %d", n.Pitch, kit.Drums[0].Key)
				}
			}
		}
	}
	if !strings.Contains(strings.Join(warns, "\n"), "several notes") {
		t.Errorf("warnings %v", warns)
	}
}
