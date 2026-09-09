//go:build release

package app

// startup_release.go — how a shipped game boots.
//
// The difference between the engine and a game is one file: game.yaml, in the
// install directory, which names the level to open, what the window is called
// and where the player's settings belong. Everything here is reading it and
// acting on it.
//
// Three orderings in this file are not free choices:
//
//   - The manifest is read before the log file is created, because the log file
//     goes in the data directory and the manifest is what names it.
//   - A manifest that will not load is therefore reported twice: recorded here,
//     and fatal in startupVerify once there is somewhere to report it to. A
//     release build is the one configuration with no console to fall back on.
//   - world.CurrentLevel is installed in startupEnterGame. In a development
//     build EditorInit installs it; nothing here calls EditorInit, and the
//     geometry-rebuild path a script's writes end up in reads it — so it has to
//     be installed by the boot that replaced EditorInit, not left at its
//     nil-returning default.

import (
	"log"

	"foundry/internal/fileutil"
	"foundry/internal/game"
	"foundry/internal/gfx"
	"foundry/internal/levelio"
	"foundry/internal/platform"
	"foundry/internal/script"
	"foundry/internal/ui"
	"foundry/internal/world"

	"github.com/veandco/go-sdl2/sdl"
)

// manifest is the game this binary is, read once at boot.
var manifest game.Manifest

// manifestErr is what went wrong reading it, held until there is a log to say
// it in.
var manifestErr error

// startupConfigure reads the manifest and applies everything about it that has
// to be settled before the log file is opened and before the window is created.
func startupConfigure() {
	manifest, manifestErr = game.Load(game.Path())
	if manifestErr != nil {
		return
	}

	// Writes move off the install directory. A game installed under Program
	// Files or /opt cannot write beside its own executable, and the log is the
	// first thing that would try.
	//
	// A failure here is not fatal: the fallback is the development behaviour,
	// writing beside the executable, which is wrong in an installed tree but
	// still better than refusing to start over a settings file.
	if dir, err := fileutil.UseUserDataDir(manifest.DataDirName()); err != nil {
		log.Printf("WARNING: no user data directory (%v) — settings and logs will be written beside the executable", err)
	} else {
		_ = dir // logged in startupVerify, once the log file exists to carry it
	}

	platform.WinTitle = manifest.WindowTitle()
	if manifest.Window.Width > 0 {
		platform.WinWidth = manifest.Window.Width
	}
	if manifest.Window.Height > 0 {
		platform.WinHeight = manifest.Window.Height
	}
	platform.WinHalfWidth = platform.WinWidth / 2
	platform.WinHalfHeight = platform.WinHeight / 2
}

// startupVerify fails the boot on a manifest that could not be read, now that
// the log exists to say so in.
//
// Fatal rather than falling back to some default game: there is no editor in
// this binary and no blank level worth showing, so a missing or malformed
// manifest means the install is broken and the only useful thing to do is say
// which file and why.
func startupVerify() {
	if manifestErr != nil {
		log.Fatalf("%v\n\nThis looks like a broken or incomplete installation: %s should sit beside the executable.",
			manifestErr, game.FileName)
	}
	log.Printf("game: %s (%s)", manifest.Title, manifest.Version)
	log.Printf("user data directory: %s", fileutil.StateDir())
}

// startupApplyWindow puts the window into fullscreen when the manifest asks for
// it, before Run reads the size back — so the projection is built from the size
// the game actually got rather than from the windowed one it briefly had.
func startupApplyWindow() {
	if manifest.Window.Fullscreen {
		if err := platform.Window.SetFullscreen(uint32(sdl.WINDOW_FULLSCREEN_DESKTOP)); err != nil {
			log.Printf("fullscreen: %v (staying windowed)", err)
		}
	}
}

// startupLevel loads the game's start level — the COMPILED one, which is the
// only kind this build reads (levelsource_release.go).
//
// Synchronously, unlike a level change during play: there is no frame on screen
// yet to cover with a loading screen, so there is nothing for the stepped
// LoadJob to buy. The path and directory returned are what an editor would save
// to, and are unused here.
func startupLevel() (world.Level, string, string, error) {
	path := levelSourcePath(manifest.StartLevel)
	level, err := levelio.LoadCompiledLevel(path)
	return level, path, levelio.LevelsDir(), err
}

// startupDescribeLevel is what the boot log says it just loaded.
func startupDescribeLevel() string {
	return "start level: " + manifest.StartLevel + " (" + levelSourceExt + ")"
}

// startupEnterGame puts the engine into gameplay, doing for a shipped game what
// pressing tilde does for a development one.
//
// It is EditorToggle's play half (editor.go) minus the parts that only exist so
// that play can be left again: there is no snapshot taken, because nothing here
// will ever restore the authored level — the install directory is read-only and
// the process ends rather than returning to an editor.
func startupEnterGame(level *world.Level) {
	// The inversion EditorInit installs in a development build. Read by the
	// geometry-rebuild path that a script's writes to the world end up in.
	world.CurrentLevel = func() *world.Level { return level }

	script.ScriptRuntimeLoad(level)
	script.ScriptEventsReset()

	// The level's own screens, then the manifest's start screen over them. The
	// order is what makes a main menu sit on top of a HUD that is already up
	// rather than behind it.
	ui.Game.Reset()
	for _, name := range level.UI {
		if _, err := ui.Game.Show(name); err != nil {
			log.Printf("UI screen %q: %v", name, err)
		}
	}
	if manifest.StartScreen != "" {
		if _, err := ui.Game.Show(manifest.StartScreen); err != nil {
			log.Printf("start screen %q: %v", manifest.StartScreen, err)
		}
	}

	platform.GravityEnabled = true

	// Mouse-look, unless a screen that came up above wants a pointer. Going
	// through the same predicate the frame loop uses keeps a game that starts
	// on a menu from grabbing the cursor for one frame and handing it back.
	gfx.SetMouseLock(!ui.Game.WantsCursor())
}
