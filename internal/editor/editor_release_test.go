package editor

// The Release window, drawn for real with no window and no GL, in each state a
// build goes through — and the two decisions it makes before one starts.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/release"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// withReleaseProject points the editor at a scratch project with a game.yaml.
func withReleaseProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(fileutil.ResourcesDir(root), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fileutil.ResourcesDir(root), "game.yaml"), []byte("title: Test Game\nstart_level: intro\n"), 0644); err != nil {
		t.Fatal(err)
	}
	prevRoot, prevCfg, prevJob := fileutil.ExecutablePath, releaseCfg, releaseJob
	t.Cleanup(func() {
		fileutil.ExecutablePath, releaseCfg, releaseJob = prevRoot, prevCfg, prevJob
		releaseProbeAt = time.Time{}
	})
	fileutil.ExecutablePath = root
	releaseCfg = release.DefaultConfig()
	releaseJob = nil
	releaseProbeAt = time.Time{}
	return root
}

func drawReleaseFrame(body func()) {
	io := imgui.CurrentIO()
	io.SetDisplaySize(imgui.Vec2{X: 1280, Y: 800})
	io.SetDeltaTime(1.0 / 60.0)
	imgui.NewFrame()
	imgui.Begin("release")
	body()
	imgui.End()
	imgui.Render()
}

func TestReleaseWindowDraws(t *testing.T) {
	withReleaseProject(t)
	withImgui(func() { drawReleaseFrame(drawReleaseBody) })

	for _, state := range []release.JobState{release.JobRunning, release.JobSucceeded, release.JobFailed, release.JobCancelled} {
		snap := release.JobSnapshot{
			State: state,
			Lines: []string{
				release.StepPrefix + "Compiling levels",
				"intro: 12 cells",
				"intro: LEAK — the light is outside the level",
				release.WarningPrefix + "no Setup.exe",
				release.ArtifactPrefix + "/p/build/test-game-0.0.0-linux-amd64.tar.gz",
				release.FailedPrefix + "it did not",
			},
			Artifacts: []string{"/p/build/test-game-0.0.0-linux-amd64.tar.gz"},
			Elapsed:   3 * time.Second,
		}
		withImgui(func() {
			drawReleaseFrame(func() {
				drawReleaseControls(fileutil.ExecutablePath, releaseProbe, snap, state == release.JobRunning)
				drawReleaseOutput(snap)
			})
		})
	}
}

// Build stays disabled until there is something to wrap the content in, and
// comes on as soon as there is.
func TestReleaseIsBlockedWithoutAGameBinary(t *testing.T) {
	root := withReleaseProject(t)
	cfg := release.Config{Platforms: []string{"linux"}, Output: "build", Prune: true}

	if why := releaseBlockedReason(cfg, probeRelease(root, cfg)); why == "" {
		t.Error("Build was enabled with no game binary in runtimes/")
	}

	player := release.PrebuiltPlayer(root, release.Platforms[0])
	if err := os.MkdirAll(filepath.Dir(player), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(player, []byte("\x7fELF"), 0755); err != nil {
		t.Fatal(err)
	}
	if why := releaseBlockedReason(cfg, probeRelease(root, cfg)); why != "" {
		t.Errorf("Build was blocked with a game binary present: %s", why)
	}

	cfg.Platforms = nil
	if why := releaseBlockedReason(cfg, probeRelease(root, cfg)); why == "" {
		t.Error("Build was enabled with no platform chosen")
	}
}

func TestReleasePlatformToggles(t *testing.T) {
	c := release.Config{Platforms: []string{"linux"}}
	c.Platforms = releaseConfigWith(c, "windows", true)
	if !releaseConfigHas(c, "linux") || !releaseConfigHas(c, "windows") {
		t.Errorf("switching windows on = %v", c.Platforms)
	}
	c.Platforms = releaseConfigWith(c, "linux", false)
	if releaseConfigHas(c, "linux") || len(c.Platforms) != 1 {
		t.Errorf("switching linux off = %v", c.Platforms)
	}
	c.Platforms = releaseConfigWith(c, "windows", true)
	if len(c.Platforms) != 1 {
		t.Errorf("switching an already-on platform on again duplicated it: %v", c.Platforms)
	}
}

func TestReleaseOutputColours(t *testing.T) {
	for line, want := range map[string]bool{
		release.StepPrefix + "Compiling levels": true,
		release.ArtifactPrefix + "/x.tar.gz":    true,
		release.FailedPrefix + "no":             true,
		release.WarningPrefix + "hm":            true,
		"FAIL intro.yaml: 3 leaks":              true,
		"intro: LEAK — something":               true,
		"executable: test-game":                 false,
		"levels: pruned 2 unreachable file(s)":  false,
	} {
		if _, got := releaseLineColor(line); got != want {
			t.Errorf("releaseLineColor(%q) coloured = %v, want %v", line, got, want)
		}
	}
}
