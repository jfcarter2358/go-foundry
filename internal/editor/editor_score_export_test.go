package editor

// editor_score_export_test.go — the Export dialog (mockup screen 6) and the
// Import dialog (screen 5): what each builds from its choices, what it
// writes and reads, and that both draw in every state.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/score"
	"foundry/internal/score/io/exportset"
	"foundry/internal/score/io/ftrk"
	"foundry/internal/score/io/musicxml"
	"foundry/internal/testenv"
	"foundry/internal/tracker"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// scoreRepoResources points the library at the repository's resources/.
func scoreRepoResources(t *testing.T) {
	t.Helper()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	t.Cleanup(func() {
		fileutil.ResourcesPath = prev
		tracker.ReloadInstruments()
		score.Reload()
	})
	tracker.ReloadInstruments()
}

// The dialog's choices are the plan: the formats ticked, every part, the
// range the transport has, the tail and the fold, Export PDF and Export
// Audio ticking their own format alone.
func TestScoreExportDialogPlans(t *testing.T) {
	scoreUseTestInstruments(t)
	prevEd, prevExp := scEd, scoreExp
	t.Cleanup(func() { scEd, scoreExp, scoreShowExport = prevEd, prevExp, false })
	e := scoreLinesState(t)
	scEd = e
	scoreExp = scoreExportDlg{}
	scoreOpenExport()
	d := scoreExp
	if got := d.formats(); len(got) != 3 || got[0] != exportset.PDF || got[1] != exportset.MusicXML || got[2] != exportset.OGG {
		t.Errorf("first opened with %v", got)
	}
	if !d.PDF.Score || len(d.PDF.Parts) != 2 || !d.PDF.Parts[0] || !d.PDF.Parts[1] || d.What != scoreExportWhole {
		t.Errorf("pdf %+v, range %d", d.PDF, d.What)
	}
	if !strings.HasSuffix(d.Dir, filepath.Join("exports", "t")) {
		t.Errorf("folder %q", d.Dir)
	}
	p, err := e.exportPlan(d)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range p.Files(e.Score) {
		names = append(names, filepath.Base(f))
	}
	if got := strings.Join(names, " "); got != "t.pdf t_a.pdf t_b.pdf t.musicxml t.ogg" {
		t.Errorf("files %s", got)
	}
	// Stems, a loop, a fold: the plan says so.
	e.setLoop(scoreLoopBars(e.Score, 2, 3))
	scoreOpenExport()
	scoreExp.Stems, scoreExp.Fold, scoreExp.TailBars = true, true, 1
	p, err = e.exportPlan(scoreExp)
	if err != nil {
		t.Fatal(err)
	}
	bar := int64(score.TicksPerWhole)
	if a := p.Audio; a.From != bar || a.To != 3*bar || a.RangeName != "loop" || !a.Stems || !a.FoldTail || a.TailBars != 1 {
		t.Errorf("audio %+v", a)
	}
	if n := len(p.Files(e.Score)); n != 3+1+3 {
		t.Errorf("%d files with stems", n)
	}
	if s := scoreExp.audioSummary(); !strings.Contains(s, "OGG 48 kHz · quality 6") || !strings.Contains(s, "folded into the start") {
		t.Errorf("summary %q", s)
	}
	// One format alone.
	scoreOpenPDF()
	if got := scoreExp.formats(); len(got) != 1 || got[0] != exportset.PDF {
		t.Errorf("Export PDF opened with %v", got)
	}
	scoreRunCmd(scExportAudio)
	if got := scoreExp.formats(); len(got) != 1 || got[0] != exportset.WAV || !scoreShowExport {
		t.Errorf("Export Audio opened with %v", got)
	}
	// What cannot be exported says why.
	bad := scoreExp
	bad.Formats = map[exportset.Format]bool{}
	if _, err := e.exportPlan(bad); err == nil {
		t.Error("no formats planned")
	}
	bad.Formats = map[exportset.Format]bool{exportset.PDF: true}
	bad.PDF = scorePDFRequest{Parts: []bool{false, false}}
	if _, err := e.exportPlan(bad); err == nil {
		t.Error("a PDF of nothing planned")
	}
	bad = scoreExp
	bad.What = scoreExportSelection
	e.Sel = scoreSel{}
	if _, err := e.exportPlan(bad); err == nil {
		t.Error("a selection export with nothing selected planned")
	}
}

// An export of several formats writes what it promised, off the frame,
// the status following it to the end.
func TestScoreExportRunsThroughThePoll(t *testing.T) {
	scoreUseTestInstruments(t)
	prevEd, prevExp := scEd, scoreExp
	t.Cleanup(func() { scEd, scoreExp = prevEd, prevExp })
	e := scoreLinesState(t)
	scEd = e
	scoreOpenExport()
	scoreExp.Formats = map[exportset.Format]bool{exportset.PDF: true, exportset.MusicXML: true, exportset.MIDI: true,
		exportset.FTRK: true, exportset.WAV: true, exportset.FLAC: true}
	scoreExp.Stems, scoreExp.Bits, scoreExp.TailBars = true, 16, 0
	scoreExp.Dir = t.TempDir()
	p, err := e.exportPlan(scoreExp)
	if err != nil {
		t.Fatal(err)
	}
	scoreStartExport()
	for scoreExportBusy() {
		scorePollExport()
	}
	if !strings.HasPrefix(e.Status, "Exported ") {
		t.Fatalf("status %q", e.Status)
	}
	want := p.Files(e.Score)
	sort.Strings(want)
	var got []string
	ents, _ := os.ReadDir(scoreExp.Dir)
	for _, en := range ents {
		got = append(got, filepath.Join(scoreExp.Dir, en.Name()))
	}
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("wrote\n%s\npromised\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(want) != 3+1+1+1+3+3 {
		t.Errorf("%d files", len(want))
	}
}

// The Import dialog reads each kind of file into its table with the
// importer's guesses; what is changed in the table is what comes in.
func TestScoreImportDialog(t *testing.T) {
	scoreRepoResources(t)
	prevEd, prevImp := scEd, scoreImp
	t.Cleanup(func() { scEd, scoreImp = prevEd, prevImp })
	scEd = scoreTestState(t)
	repo := testenv.RepoRoot(t)

	// MIDI: the parts, the maps, quantise and voices.
	mid := filepath.Join(repo, "internal", "score", "io", "midi", "testdata", "format1_orchestra.mid")
	if err := scoreImportChoose(mid); err != nil {
		t.Fatal(err)
	}
	d := &scoreImp
	if d.Kind != scoreImportMIDI || !strings.Contains(d.Summary, "Standard MIDI file, type 1 · 6 tracks · 232 notes") || len(d.Rows) < 4 {
		t.Fatalf("%q %d rows", d.Summary, len(d.Rows))
	}
	if !strings.Contains(d.TempoText, "bpm") || d.KeyLabel == "" || d.MeterText == "" {
		t.Errorf("tempo %q key %q meter %q", d.TempoText, d.KeyLabel, d.MeterText)
	}
	n := d.lines()
	d.Rows[0].Import = false
	d.Rows[1].Instrument = "strings/viola_section"
	d.Rows[1].Name = "Violas"
	d.Grid, d.Triplets, d.OneVoice, d.Dynamics = 0, false, true, false
	d.Key = 15 + 7 + 1 // E minor
	d.Meter = "2/2"
	if d.grid().String() != "8" {
		t.Errorf("grid %s", d.grid())
	}
	s, warns, err := d.run()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("warnings %v", warns)
	if len(s.Lines) != n-1 || s.Lines[0].Name != "Violas" || s.Lines[0].Instrument != "strings/viola_section" {
		t.Errorf("%d lines, first %s %s", len(s.Lines), s.Lines[0].Name, s.Lines[0].Instrument)
	}
	if s.KeyAt(1) != (score.KeyMark{Bar: 1, Fifths: 1, Mode: "minor"}) || s.MeterAt(1).String() != "2/2" {
		t.Errorf("key %+v meter %s", s.KeyAt(1), s.MeterAt(1))
	}
	for _, l := range s.Lines {
		for _, st := range l.Staves {
			if len(st.Voices) > 1 {
				t.Errorf("%s has %d voices with one voice a staff", l.Name, len(st.Voices))
			}
		}
	}

	// MusicXML: parts and their staves; the layout kept or not.
	mx := filepath.Join(t.TempDir(), "sketch.musicxml")
	src := scoreTestState(t).Score
	src.Lines[0].Staves[0].Voices[0], _ = score.ParseVoice("| qG4 qA4 hB4 |")
	src.Layout.Staff = 6
	if _, err := musicxml.ExportFile(mx, src, musicxml.ExportOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := scoreImportChoose(mx); err != nil {
		t.Fatal(err)
	}
	if d.Kind != scoreImportMusicXML || len(d.Rows) != 2 || d.Rows[0].Instrument != "woodwinds/flute" || d.Rows[1].Staves != "Bass" || !d.KeepLayout {
		t.Fatalf("musicxml rows %+v", d.Rows)
	}
	if s, _, err = d.run(); err != nil || s.Layout.Staff != 6 {
		t.Errorf("kept layout: %v %+v", err, s.Layout)
	}
	d.KeepLayout = false
	if s, _, err = d.run(); err != nil || s.Layout.Staff != 0 {
		t.Errorf("no layout: %v %+v", err, s.Layout)
	}

	// A Tracker song: its instruments' library sounds.
	song, _, err := ftrk.Export(src, ftrk.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sp := filepath.Join(t.TempDir(), "sketch.ftrk")
	if err := tracker.SaveFile(sp, song); err != nil {
		t.Fatal(err)
	}
	if err := scoreImportChoose(sp); err != nil {
		t.Fatal(err)
	}
	if d.Kind != scoreImportSong || len(d.Rows) != 1 || d.Rows[0].Instrument != "woodwinds/flute" || !strings.HasPrefix(d.Summary, "Tracker song") {
		t.Fatalf("song rows %+v (%s)", d.Rows, d.Summary)
	}
	s, _, err = d.run()
	// The song's volumes are the score's velocities: mf comes back.
	if err != nil || score.FormatVoice(s.Lines[0].Staves[0].Voices[0]) != "| qG4(mf) qA4 hB4\n|\n" {
		t.Fatalf("song import: %v\n%s", err, score.FormatVoice(s.Lines[0].Staves[0].Voices[0]))
	}
	// Finished, it is the score being edited, unsaved and named.
	scoreFinishImport(s, d.libraryName(), nil)
	if scEd.Score != s || scEd.Name != "sketch" || !strings.Contains(scEd.Status, "not saved yet") {
		t.Errorf("finished: %q %q", scEd.Name, scEd.Status)
	}

	// A file it cannot read stays on the browser and says why.
	bad := filepath.Join(t.TempDir(), "broken.mid")
	os.WriteFile(bad, []byte("MThd nonsense"), 0o644)
	if err := scoreImportChoose(bad); err == nil || d.Err == "" || d.Rows != nil {
		t.Errorf("a broken file: %v %q", err, d.Err)
	}
	d.Rows = []scoreImportRow{{Import: false}}
	if _, _, err := d.run(); err == nil {
		t.Error("an import of nothing ran")
	}
}

// Both dialogs draw in every state with ImGui's stacks even: Export with
// every format ticked and a missing codec, Import on its browser and on
// each kind of file.
func TestScoreExportAndImportDialogsDraw(t *testing.T) {
	scoreRepoResources(t)
	scoreDrawTest(t)
	prevExp, prevImp := scoreExp, scoreImp
	t.Cleanup(func() { scoreExp, scoreImp, scoreShowExport, scoreShowImport = prevExp, prevImp, false, false })
	repo := testenv.RepoRoot(t)
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		away := imgui.Vec2{X: 5, Y: 5}
		scoreFrame(away, false)
		draw := func() {
			scoreFrame(away, false)
			scoreFrame(away, false)
		}
		scoreOpenExport()
		for _, f := range exportset.Formats {
			scoreExp.Formats[f] = true
		}
		scoreExp.Stems, scoreExp.MP3CBR = true, true
		scEd.Transport.Scale = 0.5
		draw()
		scoreExp.MP3CBR, scoreExp.Formats[exportset.PDF] = false, false
		draw()
		scoreExp.Formats = map[exportset.Format]bool{}
		draw()
		imgui.CloseCurrentPopup()
		scoreShowExport = false
		scoreFrame(away, false)

		scoreImp = scoreImportDlg{browseDir: filepath.Join(repo, "internal", "score", "io", "midi", "testdata")}
		scoreOpenImport()
		draw()
		if len(scoreImp.browseList) == 0 {
			t.Error("the browser lists nothing")
		}
		for _, f := range []string{
			filepath.Join(repo, "internal", "score", "io", "midi", "testdata", "format1_orchestra.mid"),
			filepath.Join(repo, "internal", "score", "io", "musicxml", "testdata", "sibelius_piano.musicxml"),
			filepath.Join(repo, "resources", "sounds", "demo_theme.ftrk"),
		} {
			if err := scoreImportChoose(f); err != nil {
				t.Fatal(err)
			}
			scoreShowImport = true
			draw()
		}
		imgui.CloseCurrentPopup()
		scoreShowImport = false
		scoreFrame(away, false)
	})
}
