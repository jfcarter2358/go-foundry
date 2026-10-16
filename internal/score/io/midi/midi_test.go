package midi

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
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

func fixture(t *testing.T, name string) *File {
	t.Helper()
	f, err := ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSMFWriteReadRoundTrip(t *testing.T) {
	f := &File{Format: 1, Division: 480, Tracks: []Track{
		{Events: []Event{
			TextMeta(0, MetaTrackName, "conductor"),
			TempoMeta(0, 132),
			TimeSignatureMeta(0, 7, 8),
			KeySignatureMeta(0, -3, true),
			{Tick: 10, Kind: SysEx, SysExType: 0xF0, Data: []byte{0x7E, 0x7F, 0x09, 0x01, 0xF7}},
			TextMeta(1920, MetaMarker, "B"),
		}},
		{Events: []Event{
			{Tick: 0, Kind: Program, Channel: 3, A: 40},
			{Tick: 0, Kind: Controller, Channel: 3, A: CCVolume, B: 90},
			{Tick: 0, Kind: NoteOn, Channel: 3, A: 60, B: 100},
			{Tick: 0, Kind: NoteOn, Channel: 3, A: 64, B: 90},
			{Tick: 240, Kind: NoteOff, Channel: 3, A: 60, B: 0},
			{Tick: 240, Kind: PitchBend, Channel: 3, A: 0, B: 64},
			{Tick: 200000, Kind: NoteOff, Channel: 3, A: 64, B: 0},
			{Tick: 200000, Kind: ChannelPressure, Channel: 3, A: 12},
		}},
	}}
	var b bytes.Buffer
	if err := f.Write(&b); err != nil {
		t.Fatal(err)
	}
	// Running status: the second note-on (same status) writes no status byte.
	if !bytes.Contains(b.Bytes(), []byte{0x93, 60, 100, 0x00, 64, 90}) {
		t.Errorf("running status not used: % X", b.Bytes())
	}
	g, err := Read(&b)
	if err != nil {
		t.Fatal(err)
	}
	if g.Format != 1 || g.Division != 480 || len(g.Tracks) != 2 {
		t.Fatalf("header: %+v", g)
	}
	for ti := range f.Tracks {
		got := g.Tracks[ti].Events
		// The writer adds the end of track.
		if last := got[len(got)-1]; last.Kind != Meta || last.MetaType != MetaEndOfTrack {
			t.Errorf("track %d does not end with end-of-track", ti)
		}
		got = got[:len(got)-1]
		if !reflect.DeepEqual(got, f.Tracks[ti].Events) {
			t.Errorf("track %d:\n got %+v\nwant %+v", ti, got, f.Tracks[ti].Events)
		}
	}
	if q, ok := g.Tracks[0].Events[1].Tempo(); !ok || q < 131.99 || q > 132.01 {
		t.Errorf("tempo %v", q)
	}
	if n, d, ok := g.Tracks[0].Events[2].TimeSignature(); !ok || n != 7 || d != 8 {
		t.Errorf("time signature %d/%d", n, d)
	}
	if fi, mi, ok := g.Tracks[0].Events[3].KeySignature(); !ok || fi != -3 || !mi {
		t.Errorf("key %d %v", fi, mi)
	}
}

func TestReadRejectsWhatIsNotMIDI(t *testing.T) {
	for _, b := range [][]byte{
		[]byte("hello"),
		[]byte("MThd\x00\x00\x00\x06\x00\x01\x00\x01\xE7\x28"), // SMPTE
		[]byte("MThd\x00\x00\x00\x06\x00\x00\x00\x01\x01\xE0"), // no tracks
	} {
		if _, err := Read(bytes.NewReader(b)); err == nil {
			t.Errorf("% X read", b)
		}
	}
}

// roundTripScore exercises what a MIDI file can carry: a pickup, a tempo
// change, a meter and a key change, triplets over one beat and over two,
// ties, rests, chords, two voices on a grand staff, a transposing
// instrument, dynamics at bar lines, a kit, rehearsal marks and a marker.
const roundTripScore = `version: 1
title: Round Trip
composer: Test Composer
bars: 8
pickup: 1/4
tempo: [{bar: 1, bpm: 100}, {bar: 5, bpm: 76}]
meter: [{bar: 1, sig: 4/4}, {bar: 6, sig: 3/4}]
key: [{bar: 1, fifths: -2, mode: major}, {bar: 5, fifths: 1, mode: major}]
sections: [{name: intro, bar: 1}, {name: theme, bar: 3}]
markers: [{name: hit, bar: 4, beat: "3"}]
lines:
- name: Flute
  instrument: woodwinds/flute
  volume: 0.8
  pan: -0.5
  staves:
  - clef: treble
    voices:
    - |
      | qF5(mp)
      | q.Bb5 eC6 3{eD6 eC6 eBb5} qA5
      | hBb5~ qBb5 qr
      | 3{qG5(f) qA5 qBb5} hC6
      | eD6(mf) eC6 eB5 eA5 hG5
      | q.G5(p) sA5 sB5 qC6
      | qD6 qC6 qB5
      | h.G5
- name: Clarinet in Bb
  instrument: woodwinds/clarinet
  staves:
  - clef: treble
    transpose: -M2
    voices:
    - |
      | qr
      | wD5(pp)
      | hEb5 hF5
      | wD5
      | hG4(ff) qA4 qB4
      | h.C5
      | qB4 qA4 qG4
      | h.G4
- name: Piano
  instrument: keys/grand_piano
  staves:
  - clef: treble
    voices:
    - |
      | qr
      | qD5+F5 qD5+F5 hEb5+G5
      | qD5+F5 qC5+Eb5 hD5+F5
      | wD5+G5
      | qB4+D5 qB4+D5 hC5+E5
      | h.B4+D5
      | qC5+E5 qA4+C5 qB4+D5
      | h.B4+D5
    - |
      | q_
      | hBb4 hBb4
      | wA4
      | hBb4 hA4
      | wG4
  - clef: bass
    voices:
    - |
      | qr
      | wBb2
      | hF2 hBb2
      | wG2
      | hG2 hC3
      | h.G2
      | qC3 qD3 qD2
      | h.G2
- name: Drums
  instrument: percussion/drum_kit
  staves:
  - clef: percussion
    voices:
    - |
      | qr
      | qC2+F#2 qD2+F#2 qC2+F#2 qD2+F#2
      | qC2 qD2 eC2 eC2 qD2
      | R
      | R
      | qC2 qD2 qD2
`

type heard struct {
	line        int
	tick, ticks int64
	midi        int
	velocity    int
}

func performance(t *testing.T, s *score.Score) ([]heard, *interpret.Performance) {
	t.Helper()
	p, err := interpret.Interpret(s, interpret.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var out []heard
	for _, n := range p.Notes {
		out = append(out, heard{n.Line, n.Tick, n.Ticks, n.MIDI, int(n.Velocity*127 + 0.5)})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.line != b.line {
			return a.line < b.line
		}
		if a.tick != b.tick {
			return a.tick < b.tick
		}
		return a.midi < b.midi
	})
	return out, p
}

func TestExportImportPreservesNotes(t *testing.T) {
	useRepoResources(t)
	src, err := score.Parse([]byte(roundTripScore))
	if err != nil {
		t.Fatal(err)
	}
	if is := src.Validate(nil); score.HasErrors(is) {
		t.Fatalf("fixture: %v", is)
	}
	f, err := Export(src, ExportOptions{})
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
	back, warns, err := Import(g, Options{Name: "round_trip"})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) > 0 {
		t.Errorf("warnings: %v", warns)
	}
	want, wp := performance(t, src)
	got, gp := performance(t, back)
	if len(got) != len(want) {
		t.Fatalf("%d notes back, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("note %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
	if d := gp.Seconds - wp.Seconds; d > 1e-3 || d < -1e-3 {
		t.Errorf("length %.4f s, want %.4f", gp.Seconds, wp.Seconds)
	}
	// The maps come back.
	if back.Title != "Round Trip" || back.Composer != "Test Composer" {
		t.Errorf("title %q composer %q", back.Title, back.Composer)
	}
	if !back.Pickup.Eq(score.F(1, 4)) || back.Measures() != 8 {
		t.Errorf("pickup %s, %d bars", back.Pickup, back.Measures())
	}
	if len(back.Meter) != 2 || back.Meter[1].Bar != 6 || back.Meter[1].Meter.String() != "3/4" {
		t.Errorf("meter %+v", back.Meter)
	}
	if len(back.Key) != 2 || back.Key[0].Fifths != -2 || back.Key[1] != (score.KeyMark{Bar: 5, Fifths: 1, Mode: "major"}) {
		t.Errorf("key %+v", back.Key)
	}
	if len(back.Tempo) != 2 || back.Tempo[1].BPM != 76 || back.Tempo[1].At.Bar != 5 {
		t.Errorf("tempo %+v", back.Tempo)
	}
	if len(back.Sections) != 2 || back.Sections[1] != (score.Section{Name: "theme", Bar: 3}) {
		t.Errorf("sections %+v", back.Sections)
	}
	if len(back.Markers) != 1 || back.Markers[0].Name != "hit" || back.FormatPos(back.Markers[0].At) != "4:3" {
		t.Errorf("markers %+v", back.Markers)
	}
	// Lines: names, instruments, mixer, staves, transposition.
	var names []string
	for _, l := range back.Lines {
		names = append(names, l.Name+"="+l.Instrument)
	}
	if got := strings.Join(names, " "); got != "Flute=woodwinds/flute Clarinet in Bb=woodwinds/clarinet Piano=keys/grand_piano Drums=percussion/drum_kit" {
		t.Errorf("lines: %s", got)
	}
	fl := back.Lines[0]
	if fl.Volume != 0.8 || fl.Pan < -0.51 || fl.Pan > -0.49 {
		t.Errorf("flute mixer %v %v", fl.Volume, fl.Pan)
	}
	if st := back.Lines[1].Staves[0]; st.Transpose.String() != "-M2" {
		t.Errorf("clarinet transposition %s", st.Transpose)
	}
	if p := back.Lines[2]; len(p.Staves) != 2 || p.Staves[0].Clef != "treble" || p.Staves[1].Clef != "bass" || len(p.Staves[0].Voices) != 2 {
		t.Errorf("piano staves: %d", len(p.Staves))
	}
	if back.Lines[3].Staves[0].Clef != "percussion" {
		t.Errorf("drum clef %s", back.Lines[3].Staves[0].Clef)
	}
	// The flute reads as written: the triplets, the tie, the keys' spelling.
	fv := score.FormatVoice(fl.Staves[0].Voices[0])
	for _, want := range []string{"| qF5(mp)", "| q.Bb5 eC6 3{eD6 eC6 eBb5} qA5", "| h.Bb5 qr", "| 3{qG5(f) qA5 qBb5} hC6", "| eD6(mf) eC6 eB5 eA5 hG5", "| q.G5(p) sA5 sB5 qC6"} {
		if !strings.Contains(fv, want) {
			t.Errorf("flute does not read %q:\n%s", want, fv)
		}
	}
	// A second export of the import is the same file.
	f2, err := Export(back, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := noteEvents(f2), noteEvents(f); got != want {
		t.Errorf("re-export differs:\n%s\nvs\n%s", got, want)
	}
}

// noteEvents lists a file's channel events, for comparing two exports.
func noteEvents(f *File) string {
	var b strings.Builder
	for ti, t := range f.Tracks {
		for _, e := range t.Events {
			if e.Kind <= PitchBend && e.Kind != Program {
				fmt.Fprintf(&b, "%d %d %d %d %d %d\n", ti, e.Tick, e.Kind, e.Channel, e.A, e.B)
			}
		}
	}
	return b.String()
}

func TestExportRepeatsAndRamps(t *testing.T) {
	src, err := score.Parse([]byte(`version: 1
bars: 3
tempo: [{bar: 1, bpm: 120, ramp: true}, {bar: 3, bpm: 60}]
repeats: [{start: 1, end: 2}]
lines:
- name: A
  instrument: keys/grand_piano
  staves:
  - clef: treble
    voices: ["| wC4 | wD4 | wE4"]
`))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Export(src, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ons, tempos := 0, 0
	for _, e := range f.Tracks[1].Events {
		if e.Kind == NoteOn {
			ons++
		}
	}
	for _, e := range f.Tracks[0].Events {
		if _, ok := e.Tempo(); ok {
			tempos++
		}
	}
	if ons != 5 {
		t.Errorf("%d notes with the repeat played, want 5", ons)
	}
	if tempos < 10 {
		t.Errorf("a ramp over bars of 4/4 wrote %d tempo events", tempos)
	}
	f, _ = Export(src, ExportOptions{NoRepeats: true})
	ons = 0
	for _, e := range f.Tracks[1].Events {
		if e.Kind == NoteOn {
			ons++
		}
	}
	if ons != 3 {
		t.Errorf("%d notes as written, want 3", ons)
	}
	// Reading the ramp back makes one ramp, not dozens of marks.
	back, _, err := Import(f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Tempo) != 2 || !back.Tempo[0].Ramp || back.Tempo[1].BPM > 61 {
		t.Errorf("tempo back: %+v", back.Tempo)
	}
}

// Grace notes and a quintuplet survive score → MIDI → score as notation:
// the graces as graces on the notes they lead to (not chord tones, not a
// second voice), the quintuplet as five notes rather than a 16th chord.
func TestExportImportGracesAndQuintuplets(t *testing.T) {
	useRepoResources(t)
	voice := "| qD5(mf) qB4 /eB5 q.A5 eG5\n| 5{sE5 sF#5 sG5 sA5 sB5} qG5 hE5\n| h.r /sE5 /sF#5 qG5\n"
	src, err := score.Parse([]byte("version: 1\nbars: 3\ntempo: [{bar: 1, bpm: 90}]\nkey: [{bar: 1, fifths: 1, mode: major}]\n" +
		"lines:\n- name: Flute\n  instrument: woodwinds/flute\n  staves:\n  - clef: treble\n    voices:\n    - |\n" + indent(voice, "      ")))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Export(src, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := Import(f, Options{Name: "graces"})
	if err != nil {
		t.Fatal(err)
	}
	st := back.Lines[0].Staves[0]
	if len(st.Voices) != 1 {
		t.Fatalf("%d voices, want 1", len(st.Voices))
	}
	want := "| qD5(mf) qB4 /eB5 q.A5 eG5\n| 5{sE5 sF#5 sG5 sA5 sB5} qG5 hE5\n| hr qr /sE5 /sF#5 qG5\n|\n"
	if got := score.FormatVoice(st.Voices[0]); got != want {
		t.Errorf("flute reads\n%s\nwant\n%s", got, want)
	}
	wantP, _ := performance(t, src)
	gotP, _ := performance(t, back)
	if len(gotP) != len(wantP) {
		t.Fatalf("%d notes back, want %d", len(gotP), len(wantP))
	}
	for i := range wantP {
		if gotP[i] != wantP[i] {
			t.Errorf("note %d: got %+v, want %+v", i, gotP[i], wantP[i])
		}
	}
	// NoGraces leaves them on the grid as ordinary notes.
	plain, _, err := Import(f, Options{Name: "graces", NoGraces: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(score.FormatVoice(plain.Lines[0].Staves[0].Voices[0]), "/") {
		t.Errorf("NoGraces still wrote grace notes")
	}
}

func indent(s, by string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString(by + l + "\n")
	}
	return b.String()
}

// A file whose notes run absurdly long is refused at once, not laid out
// as millions of bars.
func TestImportRefusesRunawayLength(t *testing.T) {
	for _, off := range []int64{480 * 4 * (score.MaxBars + 1), 1 << 34} {
		f := &File{Format: 0, Division: 480, Tracks: []Track{{Events: []Event{
			{Tick: 0, Kind: NoteOn, A: 60, B: 100},
			{Tick: off, Kind: NoteOff, A: 60},
		}}}}
		start := time.Now()
		if _, _, err := Import(f, Options{Name: "x"}); err == nil || !strings.Contains(err.Error(), "at most") {
			t.Errorf("a note %d ticks long: %v", off, err)
		}
		if el := time.Since(start); el > 2*time.Second {
			t.Errorf("refusing took %v", el)
		}
	}
}

// FuzzImport: no byte string panics the reader or the importer, and what
// imports writes, reads back and plays.
func FuzzImport(f *testing.F) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*.mid"))
	for _, p := range files {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		mf, err := Read(bytes.NewReader(b))
		if err != nil {
			return
		}
		s, _, err := Import(mf, Options{Name: "fuzz"})
		if err != nil {
			return
		}
		out, err := score.Marshal(s)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		back, err := score.Parse(out)
		if err != nil {
			t.Fatalf("an import does not read back: %v", err)
		}
		if _, err := interpret.Interpret(back, interpret.Options{}); err != nil {
			t.Fatalf("interpret: %v", err)
		}
	})
}

// The pan lane goes out as pan and the cutoff lane as brightness (CC 74),
// moving where the lanes move.
func TestExportPanAndCutoffLanes(t *testing.T) {
	src, err := score.Parse([]byte(`version: 1
bars: 2
tempo: [{bar: 1, bpm: 120}]
lines:
- name: A
  instrument: keys/grand_piano
  staves:
  - clef: treble
    voices: ["| wC4 | wD4 |"]
  lanes:
    pan: [{at: "1", value: -1}, {at: "3", value: 1}]
    cutoff: [{at: "1", value: 16, step: true}, {at: "2", value: 20000}]
`))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Export(src, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var pans, bright []Event
	for _, e := range f.Tracks[1].Events {
		if e.Kind == Controller && e.A == CCPan {
			pans = append(pans, e)
		}
		if e.Kind == Controller && e.A == CCBrightness {
			bright = append(bright, e)
		}
	}
	if len(pans) < 10 || pans[1].B != 1 || pans[len(pans)-1].B != 127 {
		t.Errorf("pan lane: %d events, %+v … %+v", len(pans), pans[1], pans[len(pans)-1])
	}
	// The step holds dark through bar 1 and opens fully at bar 2.
	if len(bright) != 2 || bright[0].B != 0 || bright[1].B != 127 || bright[1].Tick != 4*score.TicksPerQuarter {
		t.Errorf("cutoff lane: %+v", bright)
	}
}
