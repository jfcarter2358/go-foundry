package app

// level_load.go — changing level while the game is running, covered by a
// loading screen.
//
// The load is a state machine the frame loop pumps rather than a call, because
// it ends in gl.* work that cannot leave the main thread and cannot leave the
// frame — the same constraint DrainScriptWork exists for. Pumping it a slice at
// a time is what lets the screen over it animate and show a bar that moves,
// rather than one static frame followed by a hang.
//
// While a load is in flight the world does not tick: there is nothing coherent
// to tick, since half of it belongs to the outgoing level and half to the
// incoming one.

import (
	"log"
	"time"

	"foundry/internal/collision"
	"foundry/internal/hooks"
	"foundry/internal/levelio"
	"foundry/internal/render"
	"foundry/internal/script"
	"foundry/internal/ui"
	"foundry/internal/world"
)

// loadingScreenName is the screen put up over a load. A project without one
// simply gets no cover; the load still works.
const loadingScreenName = "loading"

// levelLoadFrameBudget is how much of each frame the load may spend. Enough to
// leave the loading screen animating smoothly; the rest of the frame is the
// screen itself.
const levelLoadFrameBudget = 12 * time.Millisecond

// pendingLoad is the level being built, or nil.
var pendingLoad *levelio.LoadJob

// levelLoadPending reports whether the world should stand still this frame.
func levelLoadPending() bool { return pendingLoad != nil }

// beginRequestedLevelLoad starts a load a script asked for.
//
// Failing to *start* is reported and dropped: a script naming a level that does
// not exist should leave the player where they are, with the reason in the log,
// rather than take the game down.
func beginRequestedLevelLoad() {
	name, ok := hooks.TakeLevelLoadRequest()
	if !ok {
		return
	}
	startLevelLoad(name, nil)
}

// startLevelLoad starts loading a level by name, with a save to lay over it
// once it is in (savegame.go) or nil. It reports whether the load started: one
// already in flight wins, and a level that will not open is logged and
// dropped, leaving the player where they are.
func startLevelLoad(name string, restore []byte) bool {
	if pendingLoad != nil {
		return false
	}
	// Which file that is depends on the build: authored in a development one,
	// compiled in a release one. See levelsource_dev.go.
	job, err := beginLevelLoad(levelSourcePath(name))
	if err != nil {
		log.Printf("load_level %q: %v", name, err)
		return false
	}
	pendingLoad = job
	pendingRestore = restore

	// The screen goes up before the first step, so the first frame of the load
	// is already covered.
	if _, err := ui.Game.Show(loadingScreenName); err != nil {
		log.Printf("loading screen: %v", err)
	}
	return true
}

// stepLevelLoad advances the load by a frame's worth of slices and installs
// the level when it finishes.
func stepLevelLoad(level *world.Level) {
	if pendingLoad == nil {
		return
	}
	if !pendingLoad.StepFor(levelLoadFrameBudget) {
		return
	}

	job := pendingLoad
	restore := pendingRestore
	pendingLoad, pendingRestore = nil, nil
	hooks.ClearLoadProgress()
	ui.Game.Hide(loadingScreenName)

	if err := job.Err(); err != nil {
		// A level that failed halfway is not installed: the player stays where
		// they were, which is the only state that is still coherent.
		log.Printf("loading %s: %v", job.Path(), err)
		return
	}

	installLevel(level, job.Level())
	applyPendingRestore(level, restore)
}

// installLevel replaces the running level with a freshly built one.
//
// The order matters and is the same one EditorLoadLevel uses: release what the
// outgoing level owns on the GPU *before* overwriting the struct, because
// overwriting it drops the handles rather than freeing them.
func installLevel(level *world.Level, built *world.Level) {
	collision.DeleteEntities(level)
	render.DeleteVolumes(level)
	world.ReleaseLevelGeometry(level)
	// And its voices, which name placements about to stop existing: without
	// this a restart or a retry left the old level's ambience looping under the
	// new one's. ApplyRawLevel does the same for an undo.
	world.StopAllSounds(level)
	world.StopMusic(level, 0)

	*level = *built

	// Every chunk's and compiled surface's vertex buffers, now, behind the
	// loading screen that has only just gone, rather than on the first frame
	// the level is seen — and for the chunks that start switched off as well,
	// so switching one on later costs no upload. See render/chunk_batch.go.
	render.SyncChunkBatches(level)
	// The raycast trees likewise, which the first sight line cast in the level
	// would otherwise build mid-play. See collision.WarmRaycasts.
	collision.WarmRaycasts(level)

	world.UploadLights(level.Lights)
	world.ApplySpawnPoint(level)

	// Everything the old level's runtime remembered describes objects that no
	// longer exist.
	script.ScriptRuntimeLoad(level)
	script.ScriptEventsReset()

	// The new level's own screens. Reset first, so a HUD from the level just
	// left does not stay up over the one just arrived.
	ui.Game.Reset()
	for _, name := range level.UI {
		if _, err := ui.Game.Show(name); err != nil {
			log.Printf("UI screen %q: %v", name, err)
		}
	}

	// The editor is pointed at the same level struct, so its selections name
	// objects that have just been replaced. A stub in a release build, which
	// has no selections to re-point.
	devAdoptLoadedLevel()
}
