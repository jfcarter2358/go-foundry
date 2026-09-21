package editor

// editor_release.go — the Release window: a release of this project, built
// from inside the editor.
//
// The project is the one the editor has open (fileutil.ProjectPath), because
// its resources/ is where it already reads and saves every asset
// (fileutil.ResourcesPath). The
// build itself is internal/release, run in a child process (release.StartJob);
// what is here is the settings it runs with and the output it prints.
//
// A window of its own rather than a modal, like the log: a release takes
// minutes, and there is no reason for the level to be out of reach while it
// does. Closing the window does not stop the build — the toolbar's Release
// button stays lit while one is running, and reopening the window shows where it
// has got to.

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/game"
	"foundry/internal/imguibackend"
	"foundry/internal/pack"
	"foundry/internal/release"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

const (
	releaseWindowW = 780
	releaseWindowH = 640

	// releaseShownLines is how much of the output is drawn. A verbose build
	// names every file it packs, and imgui lays out every line it is handed.
	releaseShownLines = 2000
)

var (
	releaseWindow *uiwin.UIWindow

	// releaseCfg is what the window's controls edit: loaded from release.yaml
	// when the window opens, written back when a build starts.
	releaseCfg release.Config
	// releaseCfgErr is release.yaml failing to parse. Shown, and replaced by
	// the next build's settings.
	releaseCfgErr error

	// releaseJob is the build running, or the last one. It outlives the window.
	releaseJob *release.Job
	// releaseStartErr is a build that could not even be started.
	releaseStartErr error

	releaseFollow    = true
	releaseLastLines int

	releaseProbe   releaseProbeResult
	releaseProbeAt time.Time

)

var (
	releaseStepColor  = imgui.Vec4{X: 0.45, Y: 0.75, Z: 1.00, W: 1}
	releaseDoneColor  = imgui.Vec4{X: 0.55, Y: 0.85, Z: 0.60, W: 1}
	releaseWarnColor  = imgui.Vec4{X: 1.00, Y: 0.80, Z: 0.35, W: 1}
	releaseErrorColor = imgui.Vec4{X: 1.00, Y: 0.45, Z: 0.45, W: 1}
)

// ReleaseWindowOpen reports whether the window is up.
func ReleaseWindowOpen() bool { return releaseWindow != nil }

// ReleaseBuildRunning reports whether a release build is in progress.
func ReleaseBuildRunning() bool { return releaseJob != nil && releaseJob.Running() }

// OpenReleaseWindow opens the Release window, or raises it if it is already up.
func OpenReleaseWindow() {
	if releaseWindow != nil {
		releaseWindow.Win.Raise()
		return
	}
	// Reread on every open, so an edit made to release.yaml outside the editor
	// is what the window shows — unless a build is running, whose settings are
	// the ones already on screen.
	if !ReleaseBuildRunning() {
		releaseCfg, releaseCfgErr = release.LoadConfig(fileutil.ProjectPath)
	}
	releaseProbeAt = time.Time{}

	w, err := uiwin.NewUIWindow("Release", releaseWindowW, releaseWindowH,
		drawReleaseWindow, onReleaseWindowClose)
	if err != nil {
		log.Printf("open release window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0.09, 0.09, 0.11, 1}
	// Assigned last, as the log window's is: until it is set ReleaseWindowOpen
	// has to keep saying no.
	releaseWindow = w
}

// onReleaseWindowClose runs with the window's GL context current. The build, if
// there is one, carries on.
func onReleaseWindowClose() {
	releaseWindow = nil
}

// CancelReleaseBuild stops a build that is still running. Called as the editor
// shuts down: a build left behind would carry on writing into the output
// directory with nothing left to say whether it worked.
func CancelReleaseBuild() {
	if releaseJob != nil {
		releaseJob.Cancel()
	}
}

// releaseProbeResult is what the window can say about a build before starting
// one. Read off the disk, so it is refreshed on a timer rather than every frame.
type releaseProbeResult struct {
	root        string
	manifest    game.Manifest
	manifestErr error
	source      string
	sourceErr   error
	// missing names each platform whose prebuilt game binary is not there.
	missing map[string]bool
	// missingSetup is the Windows installer program not being in runtimes/.
	missingSetup bool
	debErr       error
}

func probeRelease(root string, cfg release.Config) releaseProbeResult {
	r := releaseProbeResult{root: root, missing: map[string]bool{}}
	r.manifest, r.manifestErr = game.Load(game.ProjectPath(root))
	r.source, r.sourceErr = release.FindSource(root, cfg.Source)
	if r.source == "" && r.sourceErr == nil {
		for _, p := range release.Platforms {
			if _, err := os.Stat(release.PrebuiltPlayer(root, p)); err != nil {
				r.missing[p.Name] = true
			}
		}
		if _, err := os.Stat(release.PrebuiltSetupStub(root)); err != nil {
			r.missingSetup = true
		}
	}
	r.debErr = pack.DebAvailable()
	return r
}

// releaseBlockedReason is why the Build button is disabled, or "".
func releaseBlockedReason(cfg release.Config, pr releaseProbeResult) string {
	if pr.manifestErr != nil {
		return "game.yaml needs fixing first"
	}
	if err := cfg.Validate(); err != nil {
		return err.Error()
	}
	if pr.sourceErr != nil {
		return "the Foundry source is not a checkout"
	}
	targets, _ := cfg.Targets()
	for _, p := range targets {
		if pr.missing[p.Name] {
			return "no game binary for " + p.Label
		}
		if p.GOOS == "windows" && pr.missingSetup {
			return "no installer program for Windows"
		}
	}
	return ""
}

// drawReleaseWindow renders one frame, with this window's contexts current.
func drawReleaseWindow() {
	w := releaseWindow
	if w == nil {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: 0})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W), Y: float32(w.H)})
	if !imgui.BeginV("##releaseroot", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|
			imgui.WindowFlagsNoMove|imgui.WindowFlagsNoCollapse) {
		imgui.End()
		return
	}
	drawReleaseBody()
	imgui.End()
}

// drawReleaseBody is everything inside the window, split out so a test can draw
// it in a bare imgui frame.
func drawReleaseBody() {
	root := fileutil.ProjectPath
	if time.Since(releaseProbeAt) > time.Second {
		releaseProbe = probeRelease(root, releaseCfg)
		releaseProbeAt = time.Now()
	}
	var snap release.JobSnapshot
	running := false
	if releaseJob != nil {
		snap = releaseJob.Snapshot()
		running = snap.State == release.JobRunning
	}

	drawReleaseProject(releaseProbe)
	imgui.Separator()
	// The settings a running build was started with are the ones on screen, so
	// they stay put until it finishes.
	imguibackend.EditorPushDisabled(running)
	drawReleaseSettings(releaseProbe)
	imguibackend.EditorPopDisabled()
	imgui.Separator()
	drawReleaseControls(snap, running, releaseBlockedReason(releaseCfg, releaseProbe), func() { startReleaseBuild(root) })
	// The one thing about a release build most likely to surprise: it is built
	// from the files, and the level open in front of you may not match them.
	editorTextMuted("Builds what is saved on disk. Save the level first.")
	imgui.Separator()
	drawReleaseOutput(snap)
}

// drawReleaseProject says what is about to be built.
func drawReleaseProject(pr releaseProbeResult) {
	editorTextMuted("Project   " + pr.root)
	if pr.manifestErr != nil {
		editorTextColored(releaseErrorColor, pr.manifestErr.Error())
		return
	}
	imgui.Text(fmt.Sprintf("%s %s", pr.manifest.Title, pr.manifest.Version))
	imgui.SameLine()
	editorTextMuted("   starts on " + pr.manifest.StartLevel)
}

// drawReleaseSettings draws the controls release.yaml is made of.
func drawReleaseSettings(pr releaseProbeResult) {
	if releaseCfgErr != nil {
		editorTextColored(releaseWarnColor, releaseCfgErr.Error()+" — the next build overwrites it")
	}

	imgui.Text("Platforms ")
	for _, p := range release.Platforms {
		imgui.SameLine()
		on := releaseConfigHas(releaseCfg, p.Name)
		if imgui.Checkbox(p.Label, &on) {
			releaseCfg.Platforms = releaseConfigWith(releaseCfg, p.Name, on)
		}
	}
	drawReleasePlayers(pr)

	imgui.Text("Output    ")
	imgui.SameLine()
	imgui.SetNextItemWidth(-1)
	imgui.InputTextV("##releaseoutput", &releaseCfg.Output, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Where the build goes: an install tree and an archive per platform.\nRelative to the project directory.")
	}

	imgui.Checkbox("Ship only what the game reaches", &releaseCfg.Prune)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Copy only the assets reachable from game.yaml's start level.\n" +
			"Turn off if a script builds a level or texture name at runtime\n" +
			"(load_level(\"level_\" + n)), which the walk cannot see.")
	}
	imgui.SameLine()
	imgui.Checkbox("Allow leaking levels", &releaseCfg.AllowLeaks)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A level that leaks compiles with its culling switched off.\nLeft off, a leak fails the build instead of shipping.")
	}

	imgui.Text("Source    ")
	imgui.SameLine()
	imgui.SetNextItemWidth(-1)
	imgui.InputTextV("##releasesource", &releaseCfg.Source, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Optional. A Foundry checkout to build the game binary from with\n" +
			"go build -tags release. Left empty, the prebuilt binaries in\n" +
			release.RuntimesDirName + "/ are used — make runtimes puts them there.")
	}
}

// drawReleasePlayers says where each chosen platform's game binary will come
// from, or that it cannot come from anywhere.
func drawReleasePlayers(pr releaseProbeResult) {
	defer drawReleaseDebNote(pr)
	switch {
	case pr.sourceErr != nil:
		editorTextColored(releaseErrorColor, "Game binary: "+pr.sourceErr.Error())
		return
	case pr.source != "":
		editorTextMuted("Game binary and installer: built from " + pr.source)
		return
	}
	runtimes := filepath.Join(pr.root, release.RuntimesDirName)
	targets, _ := releaseCfg.Targets()
	for _, p := range targets {
		rel := filepath.ToSlash(filepath.Join(release.RuntimesDirName, release.PlayerFileName(p)))
		if pr.missing[p.Name] {
			editorTextColored(releaseErrorColor, fmt.Sprintf("%s: no %s — make runtime-%s RUNTIMES_DIR=%s",
				p.Label, rel, p.Name, runtimes))
			continue
		}
		editorTextMuted(fmt.Sprintf("%s: %s", p.Label, rel))
		if p.GOOS != "windows" {
			continue
		}
		stub := filepath.ToSlash(filepath.Join(release.RuntimesDirName, release.SetupStubFileName))
		if pr.missingSetup {
			editorTextColored(releaseErrorColor, fmt.Sprintf("%s installer: no %s — make runtime-windows RUNTIMES_DIR=%s",
				p.Label, stub, runtimes))
		} else {
			editorTextMuted(fmt.Sprintf("%s installer: %s", p.Label, stub))
		}
	}
}

// drawReleaseDebNote says so when a Linux build will come without its .deb.
func drawReleaseDebNote(pr releaseProbeResult) {
	if pr.debErr != nil && releaseConfigHas(releaseCfg, "linux") {
		editorTextMuted("Linux: no .deb on this machine (" + pr.debErr.Error() + "); the tarball's install.sh still installs it")
	}
}

// drawReleaseControls draws Build, or Cancel, and the state of the build.
// blocked is why Build is disabled, or "".
func drawReleaseControls(snap release.JobSnapshot, running bool, blocked string, start func()) {
	if running {
		if imgui.Button("Cancel") {
			releaseJob.Cancel()
		}
		blocked = ""
	} else {
		imguibackend.EditorPushDisabled(blocked != "")
		if imgui.Button("Build") && blocked == "" {
			start()
		}
		imguibackend.EditorPopDisabled()
	}
	imgui.SameLine()

	elapsed := snap.Elapsed.Round(time.Second)
	switch {
	case releaseStartErr != nil:
		editorTextColored(releaseErrorColor, "Could not start: "+releaseStartErr.Error())
	case blocked != "":
		editorTextColored(releaseWarnColor, blocked)
	case releaseJob == nil:
		editorTextMuted("Ready")
	case running:
		editorTextColored(releaseWarnColor, fmt.Sprintf("Building… %s", elapsed))
	case snap.State == release.JobSucceeded:
		editorTextColored(releaseDoneColor, fmt.Sprintf("Built in %s", elapsed))
	case snap.State == release.JobCancelled:
		editorTextMuted(fmt.Sprintf("Cancelled after %s", elapsed))
	default:
		editorTextColored(releaseErrorColor, fmt.Sprintf("Failed after %s — see the output below", elapsed))
	}
}

// startReleaseBuild saves the settings and starts the build.
func startReleaseBuild(root string) {
	releaseStartErr = nil
	// Saved first, and saved whatever the build then does: they are the
	// settings the author chose, and a terminal build afterwards should do what
	// this one did.
	if err := release.SaveConfig(root, releaseCfg); err != nil {
		log.Printf("release: saving %s: %v", release.ConfigFileName, err)
	}
	releaseCfgErr = nil

	job, err := release.StartJob(root, releaseCfg)
	if err != nil {
		releaseStartErr = err
		return
	}
	releaseJob = job
	releaseFollow = true
	releaseLastLines = 0
}

// drawReleaseOutput draws the files a build made, and everything it said.
func drawReleaseOutput(snap release.JobSnapshot) {
	for i, a := range snap.Artifacts {
		if imgui.Button(fmt.Sprintf("Copy##releaseartifact%d", i)) {
			imguibackend.SdlClipboard{}.SetText(a)
		}
		imgui.SameLine()
		editorTextColored(releaseDoneColor, a)
	}

	imgui.BeginChildV("##releaseoutput", imgui.Vec2{X: 0, Y: 0}, true,
		imgui.WindowFlagsHorizontalScrollbar)

	lines := snap.Lines
	if len(lines) == 0 {
		editorTextMuted("What the build prints appears here.")
	}
	if hidden := len(lines) - releaseShownLines; hidden > 0 {
		editorTextMuted(fmt.Sprintf("… %d earlier lines", hidden))
		lines = lines[hidden:]
	}
	for _, line := range lines {
		if col, ok := releaseLineColor(line); ok {
			editorTextColored(col, line)
		} else {
			imgui.Text(line)
		}
	}

	// The log window's follow rule: pinned to the bottom while new output
	// arrives, released by scrolling up, taken again by scrolling back down.
	if len(snap.Lines) != releaseLastLines {
		releaseLastLines = len(snap.Lines)
		if releaseFollow {
			imgui.SetScrollHereY(1.0)
		}
	}
	if imgui.ScrollY() >= imgui.ScrollMaxY() {
		releaseFollow = true
	} else if imgui.ScrollMaxY() > 0 {
		releaseFollow = false
	}

	imgui.EndChild()
}

// releaseLineColor is the colour a line of build output is drawn in, if it is
// not the default one.
func releaseLineColor(line string) (imgui.Vec4, bool) {
	switch {
	case strings.HasPrefix(line, release.StepPrefix):
		return releaseStepColor, true
	case strings.HasPrefix(line, release.ArtifactPrefix):
		return releaseDoneColor, true
	case strings.HasPrefix(line, release.FailedPrefix), strings.HasPrefix(line, "FAIL "):
		return releaseErrorColor, true
	case strings.HasPrefix(line, release.WarningPrefix), strings.Contains(line, ": LEAK"):
		return releaseWarnColor, true
	}
	return imgui.Vec4{}, false
}

// releaseConfigHas reports whether c builds the named platform.
func releaseConfigHas(c release.Config, name string) bool {
	for _, n := range c.Platforms {
		if n == name {
			return true
		}
	}
	return false
}

// releaseConfigWith is c's platform list with name switched on or off.
func releaseConfigWith(c release.Config, name string, on bool) []string {
	out := make([]string, 0, len(c.Platforms)+1)
	for _, n := range c.Platforms {
		if n != name {
			out = append(out, n)
		}
	}
	if on {
		out = append(out, name)
	}
	return out
}
