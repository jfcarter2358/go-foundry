//go:build !release

package app

// startup_dev.go — how a development build boots.
//
// Unchanged from what Run did inline before the release build existed: state is
// written beside the executable, the window keeps the engine's own title, and
// the engine comes up on a blank in-memory level with the 3D editor open over
// it, because an engine with no project loaded has nothing to play.
//
// startup_release.go answers the same five functions with the other half of the
// story: a manifest, a per-user data directory, and the game's own start level
// with no editor over it at all.

import (
	"path/filepath"

	"foundry/internal/levelio"
	"foundry/internal/world"
)

// startupConfigure runs before the log file is opened and before the window
// exists. A development build has nothing to configure: fileutil.UserDataRoot
// stays empty, which keeps every write beside the executable.
func startupConfigure() {}

// startupVerify reports anything startupConfigure could not, now that logging
// is up. Nothing can go wrong in a development boot, so nothing does.
func startupVerify() {}

// startupApplyWindow runs immediately after the window is created and before
// its real size is read back.
func startupApplyWindow() {}

// startupLevel is the level the engine boots with, and the path and directory
// the editor will save it to.
func startupLevel() (world.Level, string, string, error) {
	// "untitled" is also the start level of the game.yaml a new project is given
	// (game.DefaultManifest), so keep the two in step.
	levelsDir := levelio.LevelsDir()
	level, err := levelio.NewBlankLevel()
	return level, filepath.Join(levelsDir, "untitled"+levelio.LevelExt), levelsDir, err
}

// startupDescribeLevel is what the boot log says it just loaded.
func startupDescribeLevel() string { return "starting with blank level" }

// startupEnterGame would put the engine into gameplay. A development build
// boots into the editor instead — devInit has already opened it — and gameplay
// is entered later, by pressing tilde.
func startupEnterGame(level *world.Level) {}
