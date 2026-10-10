package scorecli

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/score"
	"foundry/internal/score/interpret"
	"foundry/internal/score/play"
	"foundry/internal/tracker"
)

// useRepoResources points the engine at this repository's resources/.
func useRepoResources(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	prev := fileutil.ResourcesPath
	res := filepath.Join(filepath.Dir(here), "..", "..", "..", "resources")
	fileutil.ResourcesPath = res
	t.Cleanup(func() {
		fileutil.ResourcesPath = prev
		score.Reload()
		tracker.ReloadSongs()
	})
	score.Reload()
	tracker.ReloadSongs()
	return filepath.Join(res, "..")
}

// Every score the project ships parses, validates, finds its instruments
// and is saved in canonical form.
func TestShippedScoresCheckClean(t *testing.T) {
	useRepoResources(t)
	var out bytes.Buffer
	if code := check(nil, &out); code != 0 {
		t.Fatalf("score check failed:\n%s", out.String())
	}
	out.Reset()
	if code := fmtCmd([]string{"-check"}, &out); code != 0 {
		t.Fatalf("score fmt -check:\n%s", out.String())
	}
	out.Reset()
	if code := inspect([]string{"score_demo"}, &out); code != 0 || strings.Contains(out.String(), "warning") {
		t.Fatalf("inspect score_demo:\n%s", out.String())
	}
	for _, want := range []string{"plays 20 (repeats unrolled)", "line 5 Piano", "staff 2 bass, 1 voice(s)", "section bridge", "transposed -P5"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("inspect does not say %q:\n%s", want, out.String())
		}
	}
}

// The demo has what it is meant to show: several lines, chords, two
// voices on the piano's staff, tuplets.
func TestScoreDemoShowsTheModel(t *testing.T) {
	useRepoResources(t)
	s, err := score.Load("score_demo")
	if err != nil {
		t.Fatal(err)
	}
	chords, tuplets := 0, 0
	for _, l := range s.Lines {
		for _, st := range l.Staves {
			for _, v := range st.Voices {
				for _, m := range v.Measures {
					tuplets += len(m.Tuplets)
					for _, e := range m.Events {
						if len(e.Notes) > 1 {
							chords++
						}
					}
				}
			}
		}
	}
	pno := s.Lines[s.LineIndex("Piano")]
	if len(s.Lines) < 4 || chords < 10 || tuplets < 10 || len(pno.Staves) != 2 || len(pno.Staves[0].Voices) != 2 {
		t.Errorf("%d lines, %d chords, %d tuplets, piano %d staves / %d voices", len(s.Lines), chords, tuplets, len(pno.Staves), len(pno.Staves[0].Voices))
	}
}

// Each line of the demo sounds, at a sane level, and the whole is loud
// without clipping.
func TestScoreDemoSounds(t *testing.T) {
	useRepoResources(t)
	s, err := score.Load("score_demo")
	if err != nil {
		t.Fatal(err)
	}
	data, errs, err := play.RenderScore(s.Clone(), play.RenderOptions{Section: "bridge", Seconds: 4})
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	if pk := tracker.Peak(data); pk < 0.2 || pk > 1 {
		t.Errorf("bridge peaks at %.2f", pk)
	}
	for li, l := range s.Lines {
		solo := s.Clone()
		for i, o := range solo.Lines {
			o.Solo = i == li
		}
		d, _, err := play.RenderScore(solo, play.RenderOptions{Section: "coda", Seconds: 3})
		if err != nil {
			t.Fatal(err)
		}
		if r := rms(d); r < 0.005 {
			t.Errorf("line %s alone is silent (rms %.4f)", l.Name, r)
		}
	}
}

func rms(d []float32) float64 {
	var s float64
	for _, v := range d {
		s += float64(v) * float64(v)
	}
	return math.Sqrt(s / float64(max(len(d), 1)))
}

// scoreGolden are hashes of the shipped scores rendered whole; a change
// means the scores sound different (a deliberate change re-captures them
// with SCORE_GOLDEN_PRINT=1).
var scoreGolden = map[string]string{
	"score_demo": "4a6f89d0e9c4c939",
}

func TestShippedScoresGolden(t *testing.T) {
	if testing.Short() {
		t.Skip("renders every shipped score")
	}
	useRepoResources(t)
	print := os.Getenv("SCORE_GOLDEN_PRINT") != ""
	for _, name := range score.ScanScoreNames() {
		s, err := score.Load(name)
		if err != nil {
			t.Fatal(err)
		}
		data, _, err := play.RenderScore(s.Clone(), play.RenderOptions{Tail: 2})
		if err != nil {
			t.Fatal(err)
		}
		got := tracker.Hash(data)
		if print {
			t.Logf("%q: %q,", name, got)
			continue
		}
		if want, ok := scoreGolden[name]; !ok {
			t.Errorf("%s: no golden hash (a new score? capture it with SCORE_GOLDEN_PRINT=1)", name)
		} else if got != want {
			t.Errorf("%s: hash %s, want %s — the score sounds different", name, got, want)
		}
	}
}

func TestRenderVerbWritesAWAV(t *testing.T) {
	useRepoResources(t)
	dst := filepath.Join(t.TempDir(), "demo.wav")
	var out bytes.Buffer
	if code := render([]string{"score_demo", "-o", dst, "-from", "9:3", "-seconds", "1.5", "-tempo", "1.25", "-tail", "0"}, &out); code != 0 {
		t.Fatalf("render: %s", out.String())
	}
	st, err := os.Stat(dst)
	if err != nil || st.Size() != 44+int64(1.5*48000)*2*3 {
		t.Fatalf("WAV: %v %v\n%s", st, err, out.String())
	}
	out.Reset()
	if code := render([]string{"-section", "nowhere", "score_demo"}, &out); code == 0 || !strings.Contains(out.String(), "no section") {
		t.Errorf("unknown section: %d %s", code, out.String())
	}
}

func TestFmtRewritesToCanonical(t *testing.T) {
	useRepoResources(t)
	path := filepath.Join(t.TempDir(), "messy.fscore")
	messy := "bars: 1\nlines:\n  - name: P\n    instrument: keys/grand_piano\n    staves: [{clef: treble, voices: [\"qC4 D4 E4 F4\"]}]\n"
	if err := os.WriteFile(path, []byte(messy), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := fmtCmd([]string{"-check", path}, &out); code == 0 {
		t.Error("-check passed a messy file")
	}
	if code := fmtCmd([]string{path}, &out); code != 0 {
		t.Fatal(out.String())
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "| qC4 qD4 qE4 qF4\n") {
		t.Errorf("not canonical:\n%s", b)
	}
	out.Reset()
	if code := fmtCmd([]string{"-check", path}, &out); code != 0 {
		t.Errorf("rewritten file still not canonical: %s", out.String())
	}
}

// The interpreter agrees with inspect about how long the demo is: the
// tempo map, the ramp and the repeat.
func TestScoreDemoLength(t *testing.T) {
	useRepoResources(t)
	s, _ := score.Load("score_demo")
	p, err := interpret.Interpret(s, interpret.Options{})
	if err != nil {
		t.Fatal(err)
	}
	// 18 bars at 92 (4 beats a bar), the rit. bar from 92 to 66, the last at 66.
	ramp := 4 * 60 / (92.0 - 66) * math.Log(92.0/66)
	want := 18*4*60/92.0 + ramp + 4*60/66.0
	if math.Abs(p.Seconds-want) > 1e-6 {
		t.Errorf("demo lasts %.4f s, want %.4f", p.Seconds, want)
	}
}

// check takes names and paths; render refuses a tempo scale the transport
// would not play, and says so when a score has errors or the mix clips.
func TestCheckAndRenderReportProblems(t *testing.T) {
	useRepoResources(t)
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.fscore")
	src := "bars: 2\ngain: 4\nlines:\n  - name: P\n    instrument: keys/grand_piano\n    staves: [{clef: treble, voices: [\"| wC4+E4+G4+C5(fff) qD4 | wC3+C4+C5+E5+G5 |\"]}]\n"
	if err := os.WriteFile(bad, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := check([]string{"score_demo"}, &out); code != 0 || !strings.Contains(out.String(), "all 1 score(s) ok") {
		t.Errorf("check score_demo: %d %s", code, out.String())
	}
	out.Reset()
	if code := check([]string{bad}, &out); code == 0 || !strings.Contains(out.String(), "bar holds 5/4") {
		t.Errorf("check of a bar that runs over: %d %s", code, out.String())
	}
	out.Reset()
	if code := render([]string{bad, "-o", filepath.Join(dir, "bad.wav"), "-tail", "0"}, &out); code != 0 {
		t.Fatalf("render: %s", out.String())
	}
	if s := out.String(); !strings.Contains(s, "error: line 1, staff 1, voice 1, bar 1: bar holds 5/4") || !strings.Contains(s, "the mix clips") {
		t.Errorf("render did not report the problems:\n%s", s)
	}
	out.Reset()
	if code := render([]string{"score_demo", "-tempo", "3"}, &out); code == 0 || !strings.Contains(out.String(), "0.25 to 2") {
		t.Errorf("-tempo 3: %d %s", code, out.String())
	}
}
