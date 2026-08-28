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

	"foundry/internal/collision"
	"foundry/internal/editor"
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
	if !ok || pendingLoad != nil {
		return
	}

	job, err := levelio.BeginLoad(levelio.LevelPath(name))
	if err != nil {
		log.Printf("load_level %q: %v", name, err)
		return
	}
	pendingLoad = job

	// The screen goes up before the first step, so the first frame of the load
	// is already covered.
	if _, err := ui.Game.Show(loadingScreenName); err != nil {
		log.Printf("loading screen: %v", err)
	}
}

// stepLevelLoad advances the load by one slice and installs the level when it
// finishes.
func stepLevelLoad(level *world.Level) {
	if pendingLoad == nil {
		return
	}
	if !pendingLoad.Step() {
		return
	}

	job := pendingLoad
	pendingLoad = nil
	hooks.ClearLoadProgress()
	ui.Game.Hide(loadingScreenName)

	if err := job.Err(); err != nil {
		// A level that failed halfway is not installed: the player stays where
		// they were, which is the only state that is still coherent.
		log.Printf("loading %s: %v", job.Path(), err)
		return
	}

	installLevel(level, job.Level())
}

// installLevel replaces the running level with a freshly built one.
//
// The order matters and is the same one EditorLoadLevel uses: release what the
// outgoing level owns on the GPU *before* overwriting the struct, because
// overwriting it drops the handles rather than freeing them.
func installLevel(level *world.Level, built *world.Level) {
	collision.DeleteEntities(level)
	render.DeleteVolumes(level)

	*level = *built

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
	// objects that have just been replaced.
	editor.EditorAdoptLoadedLevel()
}
