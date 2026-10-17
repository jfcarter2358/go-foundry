package editor

// editor_generator_region_seed.go — a template's region layout written into
// its level from the Generator (docs/regions/PHASE_6.md, "One command").
//
// A world doc made from a template that ships regions (`canyon_savannah`)
// carries its layout in the regions stage with the one-shot `seed_level`
// switch on (genlevel/regions_seed.go). The level is the source of truth, so
// the first sync would otherwise copy the target level's NO regions over
// the layout. Instead, while the switch is on:
//
//   - with the target level OPEN in the level editor, the layout becomes
//     region volumes in it through the level editor's own mutation path —
//     one EditorPushUndo, the volumes loaded (their cages), queued onto the
//     main window as every Generator edit of the level is — and then syncs;
//   - with the level not open, a sync that may read the file (a run) writes
//     them into the level FILE (making the level if it does not exist), as
//     `foundry gen run` does;
//   - the half-second tick leaves the doc alone (a sync then would wipe the
//     layout), and a run started before the queued write lands is refused
//     with a "run again" status, so no run ever hashes the switch.
//
// A level that already has region volumes keeps them: the switch just turns
// off and the level leads.

import (
	"strings"

	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/uiwin"
)

// genSeedQueued is set while a seed of the open level waits for the main
// window.
var genSeedQueued bool

// genSeedRegions handles a doc whose regions stage asks to seed its level.
// It reports whether the caller may go on to sync (false: wait).
func genSeedRegions(d *procgen.Doc, path string, fromFile bool) bool {
	if !genlevel.WantsSeed(d) {
		return true
	}
	switch {
	case genLevelIsOpen(path):
		if !genSeedQueued {
			genSeedQueued = true
			uiwin.RequestWindowOpen(func() {
				genSeedQueued = false
				if genDoc != d || !genlevel.WantsSeed(d) || genSpliceBlocked() != "" {
					return // a later tick or run tries again
				}
				if ids := editorSeedRegions(d); len(ids) > 0 {
					genStatusf("Wrote the template's regions into the level: %s", strings.Join(ids, ", "))
				}
				genAfterEdit()
				genSyncRegions(false)
			})
		}
		return false
	case fromFile:
		ids, err := genlevel.SeedLevelFile(d, path)
		if err != nil {
			genStatusf("The template's regions were not written into %s: %v", d.Level, err)
			return false
		}
		if len(ids) > 0 {
			genStatusf("Wrote the template's regions into level %s: %s", d.Level, strings.Join(ids, ", "))
		}
		genAfterEdit()
		return true
	}
	return false
}

// editorSeedRegions writes d's region list into the open level as region
// volumes (one undo step) when it has none, and clears d's switch either
// way. Returns the IDs added.
func editorSeedRegions(d *procgen.Doc) []string {
	lvl := Editor.level
	if lvl == nil {
		return nil
	}
	if genlevel.HasRegionVolumes(genOpenLevelVolumes()) {
		genlevel.ClearSeed(d)
		return nil
	}
	taken := map[string]bool{}
	for i := range lvl.Volumes {
		taken[lvl.Volumes[i].ID] = true
	}
	vs, err := genlevel.SeedVolumes(d, 0, taken)
	if err != nil {
		genStatusf("The template's regions were not written: %v", err)
		return nil
	}
	EditorPushUndo()
	var ids []string
	for i := range vs {
		v := vs[i]
		if err := v.Load(); err != nil {
			editorSetStatus("Volume %s: %v", v.ID, err)
		}
		lvl.Volumes = append(lvl.Volumes, v)
		ids = append(ids, v.ID)
	}
	genlevel.ClearSeed(d)
	return ids
}

// genSeedWaiting reports, for a run about to start, that the template's
// regions are still on their way into the open level.
func genSeedWaiting(d *procgen.Doc) bool { return d != nil && genlevel.WantsSeed(d) }
