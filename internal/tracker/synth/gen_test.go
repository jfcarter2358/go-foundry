package synth

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/tracker"
)

// TestGenerateLibrary writes the stock library into the repo when
// FOUNDRY_GEN_SAMPLES=1: the way to regenerate it after changing a recipe
// without wiring up the CLI.
//
//	FOUNDRY_GEN_SAMPLES=1 go test ./internal/tracker/synth -run TestGenerateLibrary
func TestGenerateLibrary(t *testing.T) {
	if os.Getenv("FOUNDRY_GEN_SAMPLES") != "1" {
		t.Skip("set FOUNDRY_GEN_SAMPLES=1 to regenerate resources/sounds/samples")
	}
	dir := filepath.Join("..", "..", "..", "resources", "sounds", tracker.SamplesDirName)
	var out bytes.Buffer
	if code := Main([]string{"-out", dir}, &out); code != 0 {
		t.Fatalf("gen-samples failed (%d):\n%s", code, out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	t.Log(lines[len(lines)-1])
}

func TestMainWritesAndReloads(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if code := Main([]string{"-out", dir, "-only", "kick_808, leads/lead_square"}, &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	s, err := tracker.LoadSampleFile("leads/lead_square", filepath.Join(dir, "leads", "lead_square.wav"))
	if err != nil {
		t.Fatal(err)
	}
	want := ByName("lead_square").Generate(Rate, nil)
	if s.BaseNote != want.BaseNote || s.Loop != tracker.LoopForward ||
		s.LoopStart != want.LoopStart || s.LoopEnd != want.LoopEnd || s.Frames() != want.Frames() {
		t.Errorf("reloaded sample differs: base %d loop %v %d..%d frames %d", s.BaseNote, s.Loop, s.LoopStart, s.LoopEnd, s.Frames())
	}
	if s.Description == "" || len(s.Tags) == 0 {
		t.Errorf("sidecar lost metadata: %+v", s.Tags)
	}
	if _, err := os.Stat(filepath.Join(dir, "drums", "kick_808.fsmp")); err != nil {
		t.Error(err)
	}
	if code := Main([]string{"-out", dir, "-only", "nope"}, &out); code == 0 {
		t.Error("unknown recipe should fail")
	}
	out.Reset()
	if code := Main([]string{"-list"}, &out); code != 0 || strings.Count(out.String(), "\n") != 128 {
		t.Errorf("-list printed %d lines", strings.Count(out.String(), "\n"))
	}
}
