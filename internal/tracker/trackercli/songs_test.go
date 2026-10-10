package trackercli

import (
	"bytes"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/tracker"
)

// useRepoResources points the engine at this repository's resources/.
func useRepoResources(t *testing.T) {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(filepath.Dir(here), "..", "..", "..", "resources")
	t.Cleanup(func() { fileutil.ResourcesPath = prev; tracker.ReloadSongs() })
	tracker.ReloadSongs()
}

// Every song the project ships parses, validates and finds every sample it
// names — the check a release would otherwise fail silently, as a quiet track.
func TestShippedSongsCheckClean(t *testing.T) {
	useRepoResources(t)
	var out bytes.Buffer
	if code := check(&out); code != 0 {
		t.Fatalf("tracker check failed:\n%s", out.String())
	}
}

func TestDemoThemeRendersEverySection(t *testing.T) {
	useRepoResources(t)
	s, err := tracker.Load("demo_theme")
	if err != nil {
		t.Fatal(err)
	}
	for _, sec := range []string{"intro", "combat", "outro"} {
		data := tracker.Render(s, tracker.RenderOptions{Section: sec, Seconds: 6})
		pk := tracker.Peak(data)
		if pk < 0.1 || pk > 1.5 {
			t.Errorf("section %s peaks at %.2f", sec, pk)
		}
	}
	quiet := tracker.Render(s, tracker.RenderOptions{Section: "combat", Seconds: 4, Params: map[string]float32{"intensity": 0}})
	loud := tracker.Render(s, tracker.RenderOptions{Section: "combat", Seconds: 4, Params: map[string]float32{"intensity": 1}})
	if rms(quiet) >= rms(loud)*0.8 {
		t.Errorf("intensity 0 is not clearly quieter: rms %.3f vs %.3f", rms(quiet), rms(loud))
	}
}

func rms(d []float32) float64 {
	var s float64
	for _, v := range d {
		s += float64(v) * float64(v)
	}
	return s / float64(len(d))
}

func TestCLIVerbs(t *testing.T) {
	useRepoResources(t)
	root := filepath.Dir(fileutil.ResourcesPath)
	var out bytes.Buffer
	if code := Main(root, []string{"inspect", "demo_theme"}, &out); code != 0 || !strings.Contains(out.String(), "section combat") {
		t.Fatalf("inspect: %d\n%s", code, out.String())
	}
	out.Reset()
	if code := Main(root, []string{"list"}, &out); code != 0 || !strings.Contains(out.String(), "demo_theme") {
		t.Fatalf("list: %d\n%s", code, out.String())
	}
	out.Reset()
	wav := filepath.Join(t.TempDir(), "d.wav")
	if code := Main(root, []string{"render", "demo_theme", "-seconds", "2", "-o", wav}, &out); code != 0 {
		t.Fatalf("render: %d\n%s", code, out.String())
	}
}
