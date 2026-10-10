package harness

// cmd_sim_test.go — the command line, run as the command line.
//
// SimMain takes its arguments and its output as values rather than reading
// os.Args and writing to stdout, which is what lets this exist: the flags, the
// scenario runner and the printed format are all checked here rather than by
// running a binary and reading a terminal.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSimRunsFromTheCommandLine(t *testing.T) {
	dir := scratchProject(t, "scratch")
	var out strings.Builder
	code := SimMain(dir, []string{
		"-level", "scratch", "-seconds", "0.5",
		"-at", "0,6,0@0",
		"-watch", "player.position",
	}, &out)
	if code != 0 {
		t.Errorf("exit code %d, want 0\n%s", code, out.String())
	}
	got := out.String()
	for _, want := range []string{"loaded scratch", "PLAYER", "player.position", "done"} {
		if !strings.Contains(got, want) {
			t.Errorf("the run printed no %q:\n%s", want, got)
		}
	}
}

// A level that is not there is a mistake worth an exit code, not a panic.
func TestSimReportsAMissingLevel(t *testing.T) {
	dir := scratchProject(t, "scratch")
	var out strings.Builder
	if code := SimMain(dir, []string{"-level", "nope", "-seconds", "0.1"}, &out); code == 0 {
		t.Errorf("a missing level exited 0:\n%s", out.String())
	}
}

// Named with no level and no scenario, it prints its usage rather than running
// something arbitrary.
func TestSimNeedsALevel(t *testing.T) {
	var out strings.Builder
	if code := SimMain(t.TempDir(), nil, &out); code != 2 {
		t.Errorf("exit code %d, want 2 (usage)", code)
	}
	if !strings.Contains(out.String(), "-level") {
		t.Errorf("the usage does not mention -level:\n%s", out.String())
	}
}

// A scenario holds a run and what should be true during it. Both halves are
// checked: an expectation that holds passes, and one that does not fails the
// command — which is what makes a scenario usable from a build step.
func TestAScenarioPassesAndFails(t *testing.T) {
	dir := scratchProject(t, "scratch")

	good := writeScenario(t, dir, "good.yaml", `
level: scratch
seconds: 0.5
watch: [player.position]
steps:
  - at: 0
    teleport: [1, 2, 3]
  - at: 0.2
    expect:
      - path: player.position
        near: [1, 2, 3]
        within: 2
`)
	var out strings.Builder
	if code := SimMain(dir, []string{"-scenario", good}, &out); code != 0 {
		t.Errorf("the scenario failed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "all expectations held") {
		t.Errorf("a passing scenario did not say so:\n%s", out.String())
	}

	bad := writeScenario(t, dir, "bad.yaml", `
level: scratch
seconds: 0.5
steps:
  - at: 0
    teleport: [1, 2, 3]
  - at: 0.2
    expect:
      - path: player.position
        near: [100, 2, 3]
        within: 1
`)
	out.Reset()
	if code := SimMain(dir, []string{"-scenario", bad}, &out); code == 0 {
		t.Errorf("a scenario whose expectation could not hold exited 0:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "EXPECT player.position") {
		t.Errorf("the failure does not say what did not hold:\n%s", out.String())
	}
}

func writeScenario(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
