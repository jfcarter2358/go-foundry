package main

// level_context.go — the one seam through which level-rebuild code reaches the
// level currently being edited.
//
// Most of the level package takes the world it operates on as a parameter, and
// that is the rule. The exception is the biome-dirtying chain
// (level_resync.go): a sector reload has to mark every biome polygon *over* the
// changed floor, and those polygons live on whichever chunk they were painted
// in — not necessarily the chunk holding the floor. Threading a *Level through
// reloadSector and markBiomesDirtyOverBox would push a parameter neither of
// them uses onto every call site in the editor.
//
// So the level arrives through a function the owner installs, rather than
// through a direct read of editor state. That is what keeps this code below the
// editor in the import graph instead of beside it — the same inversion
// script_engine_hooks.go uses for Editor.Active, and internal/flang's Host
// interface uses at a larger scale.

// CurrentLevel returns the level being edited, or nil when there is none.
//
// The default is deliberately a nil-returning stub rather than a nil func: this
// is read from geometry-rebuild paths that also run in tests and in the
// standalone tools, where no editor ever initialises. EditorInit replaces it.
var CurrentLevel = func() *Level { return nil }
