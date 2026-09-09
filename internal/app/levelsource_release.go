//go:build release

package app

// levelsource_release.go — where a shipped game's levels come from.
//
// Only compiled ones. A release build loads a .fbsp and never a .yaml: the
// geometry has already been through CSG, the visibility set is already
// computed, the lighting is already on the vertices, and nothing here has the
// code that would do any of that. Reading the authored format would mean
// shipping a level that had never been compiled and quietly performing like it.
//
// That makes compiling part of packaging rather than a thing an author may
// forget. cmd/foundrycompile is the build step; `make release-linux` runs it.
// A level with no .fbsp beside it fails to load with a message that says so,
// which is the right failure — loud, at the boot that would otherwise have
// shown an empty world.
//
// See levelsource_dev.go for the other half.

import (
	"foundry/internal/levelio"
)

const levelSourceExt = levelio.CompiledExt

func levelSourcePath(name string) string {
	return levelio.CompiledPath(levelio.LevelPath(name))
}

func beginLevelLoad(path string) (*levelio.LoadJob, error) {
	return levelio.BeginLoadCompiled(path)
}
