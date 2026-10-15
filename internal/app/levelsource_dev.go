//go:build !release

package app

// levelsource_dev.go — where a development build's levels come from.
//
// The authored ones. A development build is the editor, and the editor edits
// brushes; play evaluates them live, uncompiled, every frame. That costs the
// overdraw CSG would have removed and the culling the visibility set would have
// done, and it is worth it, because the alternative is an editor whose view of
// the level is one compile behind the level.
//
// levelsource_release.go answers the same two functions the other way: only
// compiled levels, never a .yaml. The pair is the whole of decision 4, and it
// is deliberately this small — anything with judgement in it belongs in
// internal/levelio, which both halves call.

import (
	"foundry/internal/levelio"
)

// levelSourceExt is what a level file is, for the log and for error messages.
const levelSourceExt = levelio.LevelExt

// levelSourcePath is the file to load for a level named in a manifest or by a
// script's load_level.
func levelSourcePath(name string) string { return levelio.LevelPath(name) }

// beginLevelLoad starts a stepped load of one, its YAML parsed off the main
// thread so the loading screen moves over it.
func beginLevelLoad(path string) (*levelio.LoadJob, error) { return levelio.BeginLoadAsync(path) }
