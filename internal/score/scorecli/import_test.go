package scorecli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/score"
	"foundry/internal/score/io/midi"
	"foundry/internal/tracker"
)

// scratchProject is an empty project whose sounds/ borrows this repository's
// instrument library and samples, so imports are written somewhere
// throwaway but find the real instruments.
func scratchProject(t *testing.T) string {
	t.Helper()
	repo := useRepoResources(t)
	dir := t.TempDir()
	sounds := filepath.Join(dir, "resources", "sounds")
	if err := os.MkdirAll(sounds, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"instruments", "samples"} {
		if err := os.Symlink(filepath.Join(repo, "resources", "sounds", n), filepath.Join(sounds, n)); err != nil {
			t.Fatal(err)
		}
	}
	prev := fileutil.ResourcesPath
	t.Cleanup(func() { fileutil.ResourcesPath = prev; score.Reload(); tracker.ReloadInstruments() })
	return dir
}

func run(t *testing.T, root string, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := Main(root, args, &out)
	return out.String(), code
}

func ioFixture(t *testing.T, pkg, name string) string {
	t.Helper()
	repo := useRepoResources(t)
	return filepath.Join(repo, "internal", "score", "io", pkg, "testdata", name)
}

func TestImportVerbMIDI(t *testing.T) {
	src := ioFixture(t, "midi", "format1_orchestra.mid")
	root := scratchProject(t)
	out, code := run(t, root, "import", src, "-list")
	if code != 0 || !strings.Contains(out, "format 1, 6 tracks, 232 notes") || !strings.Contains(out, "49 String Ensemble 1") || !strings.Contains(out, "channel 10 (drums)") {
		t.Fatalf("list (%d):\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, "resources", "sounds", "format1_orchestra.fscore")); err == nil {
		t.Fatal("-list wrote a score")
	}
	out, code = run(t, root, "import", src, "-o", "sketches/hangar", "-skip", "5", "-instrument", "2=strings/viola_section")
	if code != 0 || !strings.Contains(out, `"Hangar Sketch", 12 bars, 4 lines`) || !strings.Contains(out, "strings/viola_section") || !strings.Contains(out, "master gain set to") {
		t.Fatalf("import (%d):\n%s", code, out)
	}
	path := filepath.Join(root, "resources", "sounds", "sketches", "hangar.fscore")
	s, err := score.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Lines) != 4 || s.Lines[1].Instrument != "strings/viola_section" || s.Gain >= 1 || s.Gain < 0.3 {
		t.Errorf("score: %d lines, %s, gain %v", len(s.Lines), s.Lines[1].Instrument, s.Gain)
	}
	// Written in canonical form, and it checks clean.
	if out, code := run(t, root, "fmt", "-check"); code != 0 {
		t.Errorf("fmt -check:\n%s", out)
	}
	if out, code := run(t, root, "check"); code != 0 {
		t.Errorf("check:\n%s", out)
	}
	// Not over an existing score without -force.
	if out, code := run(t, root, "import", src, "-o", "sketches/hangar"); code != 1 || !strings.Contains(out, "-force") {
		t.Errorf("overwrite (%d):\n%s", code, out)
	}
	if _, code := run(t, root, "import", src, "-o", "sketches/hangar", "-force", "-grid", "8", "-voices", "1", "-no-dynamics", "-key", "Em", "-meter", "2/2"); code != 0 {
		t.Error("-force with options failed")
	}
	s, _ = score.LoadFile(path)
	if s.KeyAt(1) != (score.KeyMark{Bar: 1, Fifths: 1, Mode: "minor"}) || s.MeterAt(1).String() != "2/2" {
		t.Errorf("options: %+v %s", s.KeyAt(1), s.MeterAt(1))
	}
	// Bad flags are usage errors.
	for _, bad := range [][]string{{"-grid", "12"}, {"-key", "H"}, {"-split", "Q9"}, {"-skip", "x"}, {"-instrument", "1=no/such"}} {
		if _, code := run(t, root, append([]string{"import", src}, bad...)...); code == 0 {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestImportVerbMusicXMLAndExportMIDI(t *testing.T) {
	src := ioFixture(t, "musicxml", "sibelius_piano.musicxml")
	mxl := ioFixture(t, "musicxml", "compressed.mxl")
	root := scratchProject(t)
	out, code := run(t, root, "import", src, "-list")
	if code != 0 || !strings.Contains(out, "MusicXML 3.1 from Sibelius 2024.3, 1 part") || !strings.Contains(out, "treble + bass") {
		t.Fatalf("list (%d):\n%s", code, out)
	}
	if out, code = run(t, root, "import", src); code != 0 || !strings.Contains(out, "sibelius_piano.fscore") {
		t.Fatalf("import (%d):\n%s", code, out)
	}
	if out, code = run(t, root, "import", mxl, "-o", "tune", "-title", "A Tune"); code != 0 || !strings.Contains(out, `"A Tune", 5 bars`) {
		t.Fatalf("import mxl (%d):\n%s", code, out)
	}
	// Export it and read the file back.
	dst := filepath.Join(t.TempDir(), "piano.mid")
	out, code = run(t, root, "export-midi", "sibelius_piano", "-o", dst)
	if code != 0 || !strings.Contains(out, "2 tracks (a conductor and 1 lines)") {
		t.Fatalf("export (%d):\n%s", code, out)
	}
	f, err := midi.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	ons := 0
	for _, e := range f.Tracks[1].Events {
		if e.Kind == midi.NoteOn {
			ons++
		}
	}
	// 24 notes written but two are tied into one, and the grace note
	// sounds: 24. The repeat of bars 3 and 4 plays 10 more.
	if ons != 24+10 {
		t.Errorf("%d notes in the export", ons)
	}
	if _, code := run(t, root, "export-midi", "sibelius_piano", "-o", dst, "-no-repeats"); code != 0 {
		t.Fatal("-no-repeats failed")
	}
	f, _ = midi.ReadFile(dst)
	ons = 0
	for _, e := range f.Tracks[1].Events {
		if e.Kind == midi.NoteOn {
			ons++
		}
	}
	if ons != 24 {
		t.Errorf("%d notes as written", ons)
	}
	if _, code := run(t, root, "import", filepath.Join(root, "nothing.txt")); code != 2 {
		t.Error("a .txt was accepted")
	}
}

func TestParseKeyAndNames(t *testing.T) {
	for in, want := range map[string]score.KeyMark{
		"Eb": {Bar: 1, Fifths: -3, Mode: "major"}, "F#m": {Bar: 1, Fifths: 3, Mode: "minor"},
		"C minor": {Bar: 1, Fifths: -3, Mode: "minor"}, "d major": {Bar: 1, Fifths: 2, Mode: "major"},
		"-2": {Bar: 1, Fifths: -2, Mode: "major"}, "C#": {Bar: 1, Fifths: 7, Mode: "major"}, "bbm": {Bar: 1, Fifths: -5, Mode: "minor"},
	} {
		if got, err := parseKey(in); err != nil || got != want {
			t.Errorf("parseKey(%q) = %+v %v, want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"H", "9", "Fb minor m", ""} {
		if _, err := parseKey(bad); err == nil {
			t.Errorf("parseKey(%q) worked", bad)
		}
	}
	for in, want := range map[string]string{"Hangar Sketch (v2).mid": "hangar_sketch_v2", "/x/y/Score.MXL": "score", "---.mid": "imported"} {
		if got := libraryName(in); got != want {
			t.Errorf("libraryName(%q) = %q", in, got)
		}
	}
}

// -o with a path writes that file: it used to be joined under sounds/ as
// a library name, an absolute path becoming folders inside the library.
func TestImportToAPath(t *testing.T) {
	src := ioFixture(t, "musicxml", "sibelius_piano.musicxml")
	root := scratchProject(t)
	sounds := filepath.Join(root, "resources", "sounds")
	dst := filepath.Join(t.TempDir(), "out", "piano.fscore")
	out, code := run(t, root, "import", src, "-o", dst)
	if code != 0 || !strings.Contains(out, dst) {
		t.Fatalf("import to %s (%d):\n%s", dst, code, out)
	}
	s, err := score.LoadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "piano" || len(s.Lines) != 1 {
		t.Errorf("the score: %q, %d lines", s.Name, len(s.Lines))
	}
	// Nothing went into the library.
	filepath.WalkDir(sounds, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".fscore") {
			t.Errorf("wrote %s", p)
		}
		return nil
	})
	// Not over it again without -force.
	if out, code := run(t, root, "import", src, "-o", dst); code != 1 || !strings.Contains(out, "-force") {
		t.Errorf("overwrite (%d):\n%s", code, out)
	}
	if _, code := run(t, root, "import", src, "-o", dst, "-force"); code != 0 {
		t.Error("-force to a path failed")
	}
	// A path inside sounds/ is that library name.
	in := filepath.Join(sounds, "sketches", "inside.fscore")
	if out, code := run(t, root, "import", src, "-o", in); code != 0 || !score.IsScore("sketches/inside") {
		t.Errorf("a path in the library (%d):\n%s", code, out)
	}
	// A library name cannot climb out of sounds/.
	if out, code := run(t, root, "import", src, "-o", "../escape"); code != 2 {
		t.Errorf("../escape (%d):\n%s", code, out)
	}
}
