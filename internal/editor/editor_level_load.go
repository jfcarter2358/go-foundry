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
// asked for; job only from the frame after, because the parse that starts it
// is itself the first long stall on a big level, and the bar has to be on
// screen before it rather than after.
var editorLevelLoad struct {
	path    string
	job     *levelio.LoadJob
	openPop bool
	// shown is whether the modal is up, which outlives path by a frame: the
	// popup can only be closed from inside its own Begin/End.
	shown    bool
	title    string
	fraction float32
	label    string

	// raw, when set, is the level already parsed, and the job builds it rather
	// than reading path. reload says the load is the *same* level coming back
	// changed — the YAML editor's save — rather than a different one being
	// opened: the undo history, the camera and the player are all kept, and
	// the load is one undo step rather than the end of the history.
	raw    *world.RawLevel
	reload bool
}

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
	// The first frame only puts the bar up; the parse waits for the next.
	if ld.openPop {
		return
	}
	if ld.job == nil {
		if ld.raw != nil {
			ld.job = levelio.BeginLoadRaw(ld.path, *ld.raw)
			ld.raw = nil // the job has it now; no reason to hold two
		} else {
			job, err := levelio.BeginLoad(ld.path)
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
	job := ld.job
	if err := job.Err(); err != nil {
		if ld.reload {
			// The snapshot pushed for this reload describes a change that
			// never happened.
			EditorPopUndo()
		}
		editorFinishLevelLoad(fmt.Sprintf("Load failed: %v", err))
		return
	}
	if ld.reload {
		editorReinstallLevel(*job.Level())
		editorFinishLevelLoad("Applied " + filepath.Base(job.Path()) + " from the YAML editor — Ctrl+Z takes it back")
		return
	}
	editorInstallLevel(*job.Level(), job.Path())
	editorFinishLevelLoad("Loaded " + filepath.Base(job.Path()))
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
		imgui.Text(ld.title)
		imgui.ProgressBarV(ld.fraction, imgui.Vec2{X: 360, Y: 0},
			fmt.Sprintf("%s  %d%%", ld.label, int(ld.fraction*100+0.5)))
	})
}
