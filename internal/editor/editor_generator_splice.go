package editor

// editor_generator_splice.go — a finished run's placements into its level.
//
// Two cases, by whether the target level is the one open in the level
// editor:
//
//   - OPEN: the splice goes into the in-memory level through the level
//     editor's own reload path (editorBeginReloadLevel), exactly as the YAML
//     editor's save does — the level as it stands is taken as a RawLevel,
//     the generator's entries (ID prefix gen.<name>.) are replaced, and the
//     result is rebuilt behind the loading bar as ONE undo step in the level
//     editor. The level is then dirty: the author saves it, or Ctrl+Z takes
//     the whole splice back. `chunk: auto` placements are resolved against
//     the open level's chunks here (world.SceneryChunkAt), which a file
//     splice cannot do.
//   - NOT OPEN: the level file is spliced on disk (genlevel.SpliceFile, which
//     writes only when something changed and keeps a .bak).
//
// A Run Region (the Run menu, a level generation doc) splices with a scope
// (genlevel.RegionScope): only that region's expanded stages' placements
// and those of the shared stages that ran are replaced; every other
// region's stay as they were.
//
// Never while the level is being compiled (EditorCompiling — a compile reads
// the level on another goroutine), loaded, or played: the splice waits in
// genPendingSplice and is retried each frame until it can land.
//
// Runs on the main window's context (uiwin.RequestWindowOpen), since the
// reload uploads the new placements' meshes.

import (
	"path/filepath"

	"foundry/internal/fileutil"
	"foundry/internal/levelio"
	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/procgen/regions"
	"foundry/internal/uiwin"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// genPendingSplice is a run's placements waiting to land.
var genPendingSplice struct {
	doc *procgen.Doc
	res *procgen.Result
	// regions, when set, limit the splice to those regions' placements
	// (and the shared stages that ran): a Run Region.
	regions []string
	// queued says a RequestWindowOpen for it is outstanding.
	queued bool
}

// genLevelIsOpen reports whether path is the level the level editor holds.
func genLevelIsOpen(path string) bool {
	return Editor.level != nil && Editor.SavePath != "" && filepath.Clean(Editor.SavePath) == filepath.Clean(path)
}

// genRequestSplice queues a run's placements for its level (regions: only
// those regions' — genlevel.RegionScope).
func genRequestSplice(d *procgen.Doc, res *procgen.Result, regions []string) {
	genPendingSplice.doc, genPendingSplice.res, genPendingSplice.regions = d, res, regions
	genTrySplice()
}

// genSpliceScope is the pending splice's scope: nil for a whole run.
func genSpliceScope(d *procgen.Doc, res *procgen.Result, regions []string) ([]string, error) {
	if len(regions) == 0 {
		return nil, nil
	}
	return genlevel.RegionScope(d, res, regions)
}

// genSpliceBlocked says why a splice into the open level must wait, or "".
func genSpliceBlocked() string {
	switch {
	case EditorCompiling():
		return "the level is compiling"
	case EditorLevelLoading():
		return "a level is loading"
	case inPlaySession():
		return "a play session is running"
	}
	return ""
}

// genTrySplice lands the pending splice if it can. Called when a run
// finishes and from genStepJobs each frame while one waits.
func genTrySplice() {
	ps := &genPendingSplice
	if ps.res == nil || ps.queued {
		return
	}
	path := levelio.LevelPath(ps.doc.Level)
	if genLevelIsOpen(path) && genSpliceBlocked() != "" {
		genStatusf("Placements wait: %s", genSpliceBlocked())
		return
	}
	ps.queued = true
	uiwin.RequestWindowOpen(func() {
		ps.queued = false
		if ps.res == nil {
			return
		}
		if genLevelIsOpen(path) {
			if why := genSpliceBlocked(); why != "" {
				genStatusf("Placements wait: %s", why)
				return
			}
			genSpliceOpenLevel(ps.doc, ps.res, ps.regions)
		} else {
			genSpliceLevelFile(ps.doc, path, ps.res, ps.regions)
		}
		ps.doc, ps.res, ps.regions = nil, nil, nil
	})
}

// genSpliceOpenLevel splices into the level editor's level as one undoable
// reload.
func genSpliceOpenLevel(d *procgen.Doc, res *procgen.Result, regions []string) {
	raw, err := editorLevelToRaw()
	if err != nil {
		genStatusf("Splice failed: %v", err)
		return
	}
	lvl := Editor.level
	opt := genlevel.Options{ChunkAt: func(x, y, z float32) string {
		return world.SceneryChunkAt(lvl, mgl32.Vec3{x, y, z})
	}}
	if opt.Scope, err = genSpliceScope(d, res, regions); err != nil {
		genStatusf("Splice failed: %v", err)
		return
	}
	st, err := genlevel.SpliceResult(&raw, res, opt)
	if err != nil {
		genStatusf("Splice failed: %v", err)
		return
	}
	if !st.Changed() {
		genStatusf("%s: nothing to place", d.Name)
		return
	}
	if !editorBeginReloadLevel(Editor.SavePath, raw) {
		genStatusf("Splice failed: the level editor is busy loading")
		return
	}
	genStatusf("Placed into %s (open; unsaved): %s", d.Level, st)
	editorSetStatus("Generator %s placed into this level: %s — Ctrl+Z takes it back", d.Name, st)
}

// genSpliceLevelFile splices into a level file that is not open.
func genSpliceLevelFile(d *procgen.Doc, path string, res *procgen.Result, regions []string) {
	scope, err := genSpliceScope(d, res, regions)
	if err != nil {
		genStatusf("Splice into %s failed: %v", fileutil.RelAssetPath(path), err)
		return
	}
	st, err := genlevel.SpliceFile(path, res.Generator, res.Placements(), res.Volumes(), genlevel.Options{Settings: res.LevelSettings(), Scope: scope})
	if err != nil {
		genStatusf("Splice into %s failed: %v", fileutil.RelAssetPath(path), err)
		return
	}
	if !st.Changed() {
		genStatusf("%s already holds these placements", d.Level)
		return
	}
	genStatusf("Placed into %s (saved): %s", fileutil.RelAssetPath(path), st)
}

// genRegionIDs is the open document's environment regions by level volume
// ID, as its regions stage holds them (nil: not a level generation doc) —
// the Run menu's Run Region list.
func genRegionIDs() []string {
	if genDoc == nil {
		return nil
	}
	st := genlevel.RegionsStage(genDoc)
	if st == nil {
		return nil
	}
	var out []string
	for _, it := range st.Params[regions.ParamRegions].Items {
		if id := it.Str(regions.FieldID); id != "" {
			out = append(out, id)
		}
	}
	return out
}
