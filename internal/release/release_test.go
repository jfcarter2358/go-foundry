package release

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"foundry/internal/pack"
)

// jobHelperEnv turns this test binary into a stand-in for `foundry
// build-release`, so the job runner can be tested against a real child process
// without building the engine.
const jobHelperEnv = "FOUNDRY_RELEASE_JOB_HELPER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(jobHelperEnv); mode != "" {
		fmt.Println(StepPrefix + "Compiling levels")
		fmt.Fprintln(os.Stderr, WarningPrefix+"said on stderr")
		fmt.Println(ArtifactPrefix + "/somewhere/test-game-1.0.0-linux-amd64.tar.gz")
		switch mode {
		case "fail":
			fmt.Println(FailedPrefix + "it did not work")
			os.Exit(1)
		case "hang":
			time.Sleep(time.Minute)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfigDefaultsAndRoundTrip(t *testing.T) {
	root := t.TempDir()
	got, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, DefaultConfig()) {
		t.Errorf("a project with no %s = %+v, want the defaults %+v", ConfigFileName, got, DefaultConfig())
	}

	want := Config{Platforms: []string{"linux", "windows"}, Output: "out/releases", Prune: false,
		AllowLeaks: true, Source: "../foundry"}
	if err := SaveConfig(root, want); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadConfig(root); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("round trip = %+v, %v; want %+v", got, err, want)
	}
	if _, err := os.Stat(filepath.Join(root, ConfigFileName+".bak")); err == nil {
		t.Error("saving the settings left a .bak in the project")
	}
}

// A key the file leaves out keeps its default — prune above all, which is the
// one whose zero value is the dangerous direction.
func TestConfigKeepsDefaultsForMissingKeys(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{ConfigFileName: "platforms: [windows]\n"})
	got, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Prune || got.Output != "build" || !reflect.DeepEqual(got.Platforms, []string{"windows"}) {
		t.Errorf("partial %s = %+v", ConfigFileName, got)
	}
}

// release.yaml sits in the project beside the editor; the packer must know to
// leave it there wherever it turns up.
func TestTheSettingsFileIsNeverPacked(t *testing.T) {
	if !pack.IsDevelopmentFile(ConfigFileName) {
		t.Errorf("internal/pack would ship %s", ConfigFileName)
	}
}

func TestTargetsAreCheckedAndOrdered(t *testing.T) {
	got, err := Config{Platforms: []string{"windows", "Linux"}}.Targets()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "linux" || got[1].Name != "windows" {
		t.Errorf("targets = %+v, want linux then windows", got)
	}
	if _, err := (Config{Platforms: []string{"amiga"}}).Targets(); err == nil {
		t.Error("an unknown platform was accepted")
	}
	if err := (Config{Platforms: nil, Output: "build"}).Validate(); err == nil {
		t.Error("a build with no platforms was accepted")
	}
	if err := (Config{Platforms: []string{"linux"}, Output: " "}).Validate(); err == nil {
		t.Error("a build with no output directory was accepted")
	}
}

func TestSourceIsFoundAboveTheProject(t *testing.T) {
	checkout := t.TempDir()
	writeFiles(t, checkout, map[string]string{
		"go.mod":                   "module foundry\n\ngo 1.23.1\n",
		"cmd/foundry/main.go":      "package main\n",
		"resources/game.yaml":      "title: x\nstart_level: y\n",
		"dist/resources/game.yaml": "title: x\nstart_level: y\n",
	})
	for _, root := range []string{".", "dist"} {
		got, err := FindSource(filepath.Join(checkout, root), "")
		if err != nil || got != checkout {
			t.Errorf("FindSource(%s) = %q, %v; want the checkout %q", root, got, err, checkout)
		}
	}

	project := t.TempDir()
	if got, err := FindSource(project, ""); err != nil || got != "" {
		t.Errorf("a project outside any checkout found source %q, %v", got, err)
	}
	// Configured, relative to the project.
	rel, err := filepath.Rel(project, checkout)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := FindSource(project, rel); err != nil || got != filepath.Join(project, rel) {
		t.Errorf("a configured relative checkout = %q, %v", got, err)
	}
	if _, err := FindSource(project, project); err == nil {
		t.Error("a configured directory that is not a checkout was accepted")
	}

	other := t.TempDir()
	writeFiles(t, other, map[string]string{"go.mod": "module example.com/foundry\n", "cmd/foundry/main.go": "package main\n"})
	if IsFoundrySource(other) {
		t.Error("a module that is not foundry was taken for a checkout")
	}
}

// buildProbe builds a trivial Go program, with or without the release tag.
func buildProbe(t *testing.T, tags string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a program")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go on PATH")
	}
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":  "module probe\n\ngo 1.21\n",
		"main.go": "package main\n\nfunc main() {}\n",
	})
	out := filepath.Join(dir, "probe"+(Platform{GOOS: runtime.GOOS}).ExeExt())
	args := []string{"build", "-o", out}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	cmd := exec.Command(goTool, append(args, ".")...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	return out
}

// A development build in runtimes/ would boot as the game and ship the editor
// with it. It is refused by what the binary says it is, not by its name.
func TestOnlyAReleaseBuildShipsAsTheGame(t *testing.T) {
	host := Platform{Name: "host", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}

	release := buildProbe(t, "release")
	if _, err := CheckPlayer(release, host); err != nil {
		t.Errorf("a release build was refused: %v", err)
	}

	dev := buildProbe(t, "")
	if _, err := CheckPlayer(dev, host); err == nil || !strings.Contains(err.Error(), "development build") {
		t.Errorf("a development build was accepted as the game: %v", err)
	}

	other := Platform{Name: "other", GOOS: "plan9", GOARCH: runtime.GOARCH}
	if _, err := CheckPlayer(release, other); err == nil {
		t.Error("a binary for this machine was accepted for another platform")
	}

	notGo := filepath.Join(t.TempDir(), "notgo")
	writeFiles(t, filepath.Dir(notGo), map[string]string{"notgo": "#!/bin/sh\n"})
	if _, err := CheckPlayer(notGo, host); err == nil {
		t.Error("a file that is not a Go program was accepted as the game")
	}
}

func TestBuildTagsAreReadAsAList(t *testing.T) {
	for _, tc := range []struct {
		tags string
		want bool
	}{
		{"release", true},
		{"netgo,release", true},
		{"netgo release", true},
		{"releases", false},
		{"", false},
	} {
		if got := hasBuildTag(tc.tags, "release"); got != tc.want {
			t.Errorf("hasBuildTag(%q) = %v, want %v", tc.tags, got, tc.want)
		}
	}
}

// Everything that can be wrong without compiling is found before compiling:
// here, no game binary to wrap the content in.
func TestBuildStopsBeforeCompilingWithoutAGameBinary(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"resources/game.yaml": "title: Test Game\nstart_level: intro\n"})
	var out strings.Builder
	cfg := DefaultConfig()
	cfg.Platforms = []string{"linux", "windows"}
	_, err := Build(context.Background(), Options{Root: root, Config: cfg, Out: &out})
	if err == nil || !strings.Contains(err.Error(), "no game binary") {
		t.Fatalf("err = %v, want a missing game binary", err)
	}
	if strings.Contains(out.String(), "Compiling levels") {
		t.Error("the build compiled levels before finding it had nothing to ship them in")
	}
	if _, err := os.Stat(filepath.Join(root, "build")); err == nil {
		t.Error("a build that could not start still made its output directory")
	}
}

func TestBuildRefusesToWriteOverTheProject(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"resources/game.yaml": "title: Test Game\nstart_level: intro\n"})
	cfg := DefaultConfig()
	cfg.Output = "."
	if _, err := Build(context.Background(), Options{Root: root, Config: cfg}); err == nil {
		t.Error("a build with the project as its output was allowed")
	}
	cfg.Output = "resources"
	if _, err := Build(context.Background(), Options{Root: root, Config: cfg}); err == nil {
		t.Error("a build with the project's resources/ as its output was allowed")
	}
	cfg.Output = "resources/levels/out"
	if _, err := Build(context.Background(), Options{Root: root, Config: cfg}); err == nil {
		t.Error("a build into a content directory was allowed")
	}
}

func TestMainParsesItsFlags(t *testing.T) {
	if code := Main(t.TempDir(), []string{"-h"}); code != 0 {
		t.Errorf("-h exited %d", code)
	}
	if code := Main(t.TempDir(), []string{"-no-such-flag"}); code != 2 {
		t.Errorf("an unknown flag exited %d, want 2", code)
	}
	if code := Main(t.TempDir(), []string{"stray"}); code != 2 {
		t.Errorf("a stray argument exited %d, want 2", code)
	}
}

// The window passes every setting, so what it shows is what gets built.
func TestArgsCarryEverySetting(t *testing.T) {
	c := Config{Platforms: []string{"linux", "windows"}, Output: "out", Prune: false, AllowLeaks: true, Source: "/src"}
	got := strings.Join(Args("/project", c), " ")
	want := "build-release -root /project -platforms linux,windows -out out -prune=false -allow-leaks=true -source /src"
	if got != want {
		t.Errorf("Args = %q\nwant   %q", got, want)
	}
}

func startHelper(t *testing.T, mode string) *Job {
	t.Helper()
	j, err := startProcess(os.Args[0], t.TempDir(), []string{"-test.run=^$"}, []string{jobHelperEnv + "=" + mode})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func waitJob(t *testing.T, j *Job) {
	t.Helper()
	select {
	case <-j.done:
	case <-time.After(20 * time.Second):
		t.Fatal("the job never finished")
	}
}

func TestJobCollectsOutputAndArtifacts(t *testing.T) {
	j := startHelper(t, "ok")
	waitJob(t, j)
	s := j.Snapshot()
	if s.State != JobSucceeded || s.Err != nil {
		t.Errorf("state = %v, err = %v", s.State, s.Err)
	}
	for _, want := range []string{StepPrefix + "Compiling levels", WarningPrefix + "said on stderr"} {
		found := false
		for _, l := range s.Lines {
			found = found || l == want
		}
		if !found {
			t.Errorf("%q is not in the output %q", want, s.Lines)
		}
	}
	if !reflect.DeepEqual(s.Artifacts, []string{"/somewhere/test-game-1.0.0-linux-amd64.tar.gz"}) {
		t.Errorf("artifacts = %q", s.Artifacts)
	}
}

func TestJobReportsFailure(t *testing.T) {
	j := startHelper(t, "fail")
	waitJob(t, j)
	if s := j.Snapshot(); s.State != JobFailed || s.Err == nil {
		t.Errorf("state = %v, err = %v; want a failure", s.State, s.Err)
	}
}

func TestJobCancels(t *testing.T) {
	j := startHelper(t, "hang")
	deadline := time.Now().Add(20 * time.Second)
	for len(j.Snapshot().Lines) < 3 {
		if time.Now().After(deadline) {
			t.Fatal("the helper never started talking")
		}
		time.Sleep(10 * time.Millisecond)
	}
	j.Cancel()
	waitJob(t, j)
	if s := j.Snapshot(); s.State != JobCancelled {
		t.Errorf("state = %v, want cancelled", s.State)
	}
	j.Cancel() // and again, on a job that has finished
}
