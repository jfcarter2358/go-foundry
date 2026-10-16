package scorecli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/io/audioenc"
	"foundry/internal/score/io/musicxml"
	"foundry/internal/tracker"
)

// The demo out as MusicXML, back in as a score, the same notes.
func TestExportMusicXMLVerb(t *testing.T) {
	root := scratchProject(t)
	demo := filepath.Join(useRepoResources(t), "resources", "sounds", "score_demo.fscore")
	dir := t.TempDir()
	for _, name := range []string{"demo.musicxml", "demo.mxl"} {
		dst := filepath.Join(dir, name)
		out, code := run(t, root, "export-musicxml", demo, "-o", dst)
		if code != 0 || !strings.Contains(out, "MusicXML 4.0, 5 parts") {
			t.Fatalf("export (%d):\n%s", code, out)
		}
		d, err := musicxml.ReadFile(dst)
		if err != nil || d.Software != musicxml.Software {
			t.Fatalf("%s: %v %q", name, err, d.Software)
		}
	}
	out, code := run(t, root, "import", filepath.Join(dir, "demo.mxl"), "-o", "again")
	if code != 0 || !strings.Contains(out, "5 lines") {
		t.Fatalf("import of the export (%d):\n%s", code, out)
	}
	a, _ := score.LoadFile(demo)
	b, err := score.LoadFile(filepath.Join(root, "resources", "sounds", "again.fscore"))
	if err != nil {
		t.Fatal(err)
	}
	for li := range a.Lines {
		if got, want := score.FormatVoice(b.Lines[li].Staves[0].Voices[0]), score.FormatVoice(a.Lines[li].Staves[0].Voices[0]); len(got) == 0 || got[:20] != want[:20] {
			t.Errorf("%s came back\n%s\nwas\n%s", a.Lines[li].Name, got, want)
		}
	}
	if _, code := run(t, root, "export-musicxml"); code != 2 {
		t.Error("no score accepted")
	}
}

// Audio in each format, a range, stems, a fold; -verify decodes them.
func TestExportAudioVerb(t *testing.T) {
	root := scratchProject(t)
	demo := filepath.Join(useRepoResources(t), "resources", "sounds", "score_demo.fscore")
	dir := t.TempDir()
	formats := []string{"wav", "flac"}
	for _, f := range []audioenc.Format{audioenc.OGG, audioenc.MP3} {
		if audioenc.Available(f) == nil {
			formats = append(formats, string(f))
		} else if os.Getenv("FOUNDRY_REQUIRE_CODECS") != "" {
			t.Fatal(audioenc.Available(f))
		}
	}
	for _, f := range formats {
		dst := filepath.Join(dir, "bars."+f)
		out, code := run(t, root, "export-audio", demo, "-o", dst, "-bars", "1-2", "-rate", "44100", "-bits", "16", "-tail", "0.5", "-verify")
		if code != 0 || !strings.Contains(out, dst+":") {
			t.Fatalf("%s (%d):\n%s", f, code, out)
		}
		if f == "flac" && !strings.Contains(out, "MD5 checked") {
			t.Errorf("flac verify:\n%s", out)
		}
	}
	out, code := run(t, root, "export-audio", demo, "-format", "flac", "-o", filepath.Join(dir, "loop.flac"), "-section", "bridge", "-stems", "-fold", "-tail", "1", "-verify")
	if code != 0 || strings.Count(out, "MD5 checked") != 6 {
		t.Fatalf("stems (%d):\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "loop_piano.flac")); err != nil {
		t.Error(err)
	}
	for _, bad := range [][]string{{"-format", "aiff"}, {"-rate", "22050"}, {"-bits", "8"}, {"-bars", "x"}, {"-section", "nope"}, {"-quality", "12"}, {"-bitrate", "999"}} {
		if _, code := run(t, root, append([]string{"export-audio", demo}, bad...)...); code == 0 {
			t.Errorf("%v accepted", bad)
		}
	}
}

// The demo as a Tracker song, loaded by the Tracker, and the song back as
// a score.
func TestExportFTRKVerbAndImport(t *testing.T) {
	root := scratchProject(t)
	demo := filepath.Join(useRepoResources(t), "resources", "sounds", "score_demo.fscore")
	dst := filepath.Join(root, "resources", "sounds", "demo_song.ftrk")
	out, code := run(t, root, "export-ftrk", demo, "-o", dst)
	if code != 0 || !strings.Contains(out, "rows a beat") {
		t.Fatalf("export (%d):\n%s", code, out)
	}
	song, err := tracker.LoadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if song.Name != "demo_song" || len(song.Instruments) != 5 || song.Instruments[0].Ref != "woodwinds/flute" {
		t.Errorf("song %q instruments %d", song.Name, len(song.Instruments))
	}
	out, code = run(t, root, "import", dst, "-list")
	if code != 0 || !strings.Contains(out, "woodwinds/flute") {
		t.Fatalf("list (%d):\n%s", code, out)
	}
	out, code = run(t, root, "import", "demo_song.ftrk", "-o", "from_song")
	if code != 0 || !strings.Contains(out, "5 lines") {
		t.Fatalf("import (%d):\n%s", code, out)
	}
	s, err := score.LoadFile(filepath.Join(root, "resources", "sounds", "from_song.fscore"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Sections) == 0 || s.Lines[4].Instrument != "keys/grand_piano" || len(s.Lines[4].Staves) != 2 {
		t.Errorf("sections %v, piano %s with %d staves", s.Sections, s.Lines[4].Instrument, len(s.Lines[4].Staves))
	}
}

// Every format into one folder, as the dialog writes them.
func TestExportVerb(t *testing.T) {
	root := scratchProject(t)
	demo := filepath.Join(useRepoResources(t), "resources", "sounds", "score_demo.fscore")
	dir := t.TempDir()
	out, code := run(t, root, "export", demo, "-formats", "pdf,musicxml,midi,ftrk,wav", "-o", dir, "-parts=false", "-bits", "16", "-tail", "0")
	if code != 0 {
		t.Fatalf("export (%d):\n%s", code, out)
	}
	for _, f := range []string{"score_demo.pdf", "score_demo.musicxml", "score_demo.mid", "score_demo.ftrk", "score_demo.wav"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v\n%s", f, err, out)
		}
	}
	if !strings.Contains(out, "[1/") {
		t.Errorf("no progress:\n%s", out)
	}
	// The default folder is exports/<score> beside resources/.
	if out, code := run(t, root, "export", demo, "-formats", "midi"); code != 0 || !strings.Contains(out, filepath.Join(root, "exports", "score_demo", "score_demo.mid")) {
		t.Errorf("default folder (%d):\n%s", code, out)
	}
	if _, code := run(t, root, "export", demo, "-formats", "aiff"); code != 2 {
		t.Error("aiff accepted")
	}
	if out, code := run(t, root, "codecs"); !strings.Contains(out, "libvorbisenc") || !strings.Contains(out, "libmp3lame") {
		t.Errorf("codecs (%d):\n%s", code, out)
	}
}
