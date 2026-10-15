package editor

// editor_level_load.go — opening a level from the editor behind a progress
// bar rather than a frozen window.
//
// The same levelio.LoadJob the game's load_level runs, pumped a frame's budget
// at a time from EditorRender. The job builds into a level of its own, so the
// one on screen stays whole and drawable until the new one is installed in a
// single step — there is never a half-loaded level to click on.

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"

	"foundry/internal/collision"
	"foundry/internal/hooks"
	"foundry/internal/imguibackend"
	"foundry/internal/levelio"
	"foundry/internal/render"
	"foundry/internal/script"
	"foundry/internal/world"
)

// editorLevelLoadFrameBudget is how much of each frame the load may spend —
// the same figure the game's loading screen uses, for the same reason: enough
// to finish quickly, little enough that the bar keeps moving.
const editorLevelLoadFrameBudget = 12 * time.Millisecond

const editorLevelLoadPopup = "Loading Level"

// editorLevelLoad is the load in flight. path is set from the moment one is
// asked for; job only once the bar has actually been seen — see drawn.
var editorLevelLoad struct {
	path    string
	job     *levelio.LoadJob
	openPop bool
	// shown is whether the modal is up, which outlives path by a frame: the
	// popup can only be closed from inside its own Begin/End.
	shown bool
	// drawn counts the frames the modal's body has run. ImGui lays out an
	// auto-sizing window on its first frame without showing it, so nothing
	// that could stall starts until the second: the bar was otherwise first
	// seen only after the work it was there to cover.
	drawn int
	// phase is where the load is once the job has finished: the level is
	// installed, and its first frame drawn, behind the same bar, since on a
	// generated level both are seconds of work.
	phase    editorLoadPhase
	title    string
	fraction float32
	label    string
	// status is the status line the load ends with, once installed.
	status string

	// raw, when set, is the level already parsed, and the job builds it rather
	// than reading path. reload says the load is the *same* level coming back
	// changed — the YAML editor's save — rather than a different one being
	// opened: the undo history, the camera and the player are all kept, and
	// the load is one undo step rather than the end of the history.
	raw    *world.RawLevel
	reload bool
}

// editorLoadPhase is what the load is doing after (and before) the job.
type editorLoadPhase int

const (
	// editorLoadBuilding is the job running.
	editorLoadBuilding editorLoadPhase = iota
	// editorLoadInstalling is the job done, its level swapped in and
	// uploaded next frame — after a frame that says so has been shown.
	editorLoadInstalling
	// editorLoadFirstFrame is everything done but the level's first frame,
	// which builds what the passes build lazily; the bar stays up over it.
	// The world draws before this steps, so the frame after the install is
	// that first frame.
	editorLoadFirstFrame
)

// EditorLevelLoading reports whether a level is being opened.
func EditorLevelLoading() bool { return editorLevelLoad.path != "" }

// EditorBeginLoadLevel starts opening path behind a progress bar. The result —
// loaded or failed — lands in the status line when it finishes. A request while
// one is already running is ignored.
func EditorBeginLoadLevel(path string) {
	if EditorLevelLoading() {
		return
	}
	editorLevelLoad.path = path
	editorLevelLoad.job = nil
	editorLevelLoad.openPop = true
	editorLevelLoad.drawn = 0
	editorLevelLoad.phase = editorLoadBuilding
	editorLevelLoad.fraction, editorLevelLoad.label = 0, "Reading level file"
	editorLevelLoad.raw, editorLevelLoad.reload = nil, false
}

// editorBeginReloadLevel rebuilds the open level from raw behind the same
// progress bar, as one undoable step: the snapshot is taken here, before the
// level changes, so Ctrl+Z in the level editor puts back exactly what was on
// screen. Reports whether the reload started — it does not while another load
// is running.
func editorBeginReloadLevel(path string, raw world.RawLevel) bool {
	if EditorLevelLoading() || Editor.level == nil {
		return false
	}
	EditorPushUndo()
	EditorBeginLoadLevel(path)
	editorLevelLoad.raw, editorLevelLoad.reload = &raw, true
	return true
}

// editorStepLevelLoad advances the load by a frame's budget and installs the
// level when it is built. Called once per frame from EditorRender, before the
// UI, so the bar drawn this frame is where the work got to.
func editorStepLevelLoad() {
	ld := &editorLevelLoad
	if ld.path == "" {
		return
	}
	// Nothing starts until the bar has been on screen (see drawn).
	if ld.openPop || ld.drawn < 2 {
		return
	}
	switch ld.phase {
	case editorLoadInstalling:
		editorLevelLoadInstall()
		return
	case editorLoadFirstFrame:
		// The frame just drawn was the level's first, under the bar.
		editorFinishLevelLoad(ld.status)
		return
	}
	if ld.job == nil {
		if ld.raw != nil {
			ld.job = levelio.BeginLoadRaw(ld.path, *ld.raw)
			ld.raw = nil // the job has it now; no reason to hold two
		} else {
			// The parse runs off the main thread, so the bar moves over it.
			job, err := levelio.BeginLoadAsync(ld.path)
			if err != nil {
				editorFinishLevelLoad(fmt.Sprintf("Load failed: %v", err))
				return
			}
			ld.job = job
		}
	}
	done := ld.job.StepFor(editorLevelLoadFrameBudget)
	ld.fraction, ld.label = ld.job.Progress()
	if !done {
		return
	}
	if err := ld.job.Err(); err != nil {
		if ld.reload {
			// The snapshot pushed for this reload describes a change that
			// never happened.
			EditorPopUndo()
		}
		editorFinishLevelLoad(fmt.Sprintf("Load failed: %v", err))
		return
	}
	// Swapped in next frame, once this one has said so.
	ld.phase, ld.fraction, ld.label = editorLoadInstalling, 0.98, "Installing level and uploading to the GPU"
}

// editorLevelLoadInstall swaps the built level in and does its GPU uploads.
func editorLevelLoadInstall() {
	ld := &editorLevelLoad
	job := ld.job
	if ld.reload {
		editorReinstallLevel(*job.Level())
		ld.status = "Applied " + filepath.Base(job.Path()) + " from the YAML editor — Ctrl+Z takes it back"
	} else {
		editorInstallLevel(*job.Level(), job.Path())
		ld.status = "Loaded " + filepath.Base(job.Path())
	}
	ld.job = nil
	// The uploads installLevel does for a level changed in play — the brush
	// batches, the Nanite page pool, the raycast trees — here, under the
	// bar, rather than inside the first frame that draws the level.
	render.SyncChunkBatches(Editor.level)
	render.SyncNaniteScenery(Editor.level)
	collision.WarmRaycasts(Editor.level)
	ld.phase, ld.fraction, ld.label = editorLoadFirstFrame, 0.99, "Drawing the first frame"
}

// editorReinstallLevel swaps in a rebuilt copy of the open level.
//
// editorInstallLevel less the parts that are about opening a *different*
// level — restoreLevelSnapshot's reasoning, since this is an undo step too: the
// undo history stays, the player is not thrown onto the spawn, and the save
// path does not move. What the outgoing level held is released the way
// ApplyRawLevel releases it, sounds included.
func editorReinstallLevel(newLevel world.Level) {
	collision.DeleteEntities(Editor.level)
	render.DeleteVolumes(Editor.level)
	world.ReleaseLevelGeometry(Editor.level)
	world.StopAllSounds(Editor.level)
	world.StopMusic(Editor.level, 0)
	*Editor.level = newLevel

	world.UploadLights(Editor.level.Lights)
	script.ScriptRuntimeLoad(Editor.level)
	editorResetSelectionState()
}

// editorFinishLevelLoad ends the load, successful or not, and reports it.
func editorFinishLevelLoad(status string) {
	editorLevelLoad.path = ""
	editorLevelLoad.job = nil
	editorLevelLoad.phase = editorLoadBuilding
	// The job published its progress for a loading screen's sake; the editor
	// has none, and a script reading load_progress should see 1 again.
	hooks.ClearLoadProgress()
	Editor.StatusMsg = status
	Editor.StatusTime = sdl.GetTicks()
}

// editorDrawLevelLoad draws the progress modal while a load is running. Modal
// so nothing in the editor can be edited meanwhile — an edit to the outgoing
// level would be thrown away the moment the new one is installed.
func editorDrawLevelLoad() {
	ld := &editorLevelLoad
	if ld.path == "" && !ld.shown {
		return
	}
	show := ld.openPop
	if ld.openPop {
		ld.openPop, ld.shown = false, true
		ld.title = filepath.Base(ld.path)
	}
	imguibackend.WithModalPopup(editorLevelLoadPopup, &show, func() {
		if ld.path == "" {
			ld.shown = false
			imgui.CloseCurrentPopup()
			return
		}
		ld.drawn++
		imgui.Text(ld.title)
		imgui.ProgressBarV(ld.fraction, imgui.Vec2{X: 420, Y: 0},
			fmt.Sprintf("%s  %d%%", ld.label, int(ld.fraction*100+0.5)))
	})
}
