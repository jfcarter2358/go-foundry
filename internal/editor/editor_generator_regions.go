package editor

// editor_generator_regions.go — the Generator's side of environment regions
// (docs/regions/SPEC.md, phase 1).
//
// A level generation doc's regions stage holds a COPY of its level's region
// volumes (genlevel.SyncRegions); the level is the source of truth. The
// Generator keeps the copy current:
//
//   - before every run (genBeginRun), from the open level when it is the
//     doc's target, else from the level file;
//   - while the window is up and the target level is open, about twice a
//     second, so moving a footprint point in the level editor re-previews the
//     biome overlay without a run.
//
// A sync is not an author's edit and pushes no undo step (undoing it would
// only be synced back); it does mark the document changed, so the save chip
// shows the doc no longer matches its file — which is true.
//
// And File > Generate World…: create (or open) the open level's world
// generator, resources/generators/<level>_world.fgen.

import (
	"time"

	"foundry/internal/levelio"
	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// genRegionsSyncEvery bounds how often the open level is re-read.
const genRegionsSyncEvery = 500 * time.Millisecond

var genRegionsSyncedAt time.Time

// genOpenLevelVolumes is the open level's own volumes as raw entries — all a
// region sync reads, without converting the whole level.
func genOpenLevelVolumes() *world.RawLevel {
	raw := &world.RawLevel{}
	for i := range Editor.level.Volumes {
		v := &Editor.level.Volumes[i]
		if v.Region == nil || v.FromEntity != "" || v.FromPrefab != "" {
			continue
		}
		raw.Volumes = append(raw.Volumes, world.VolumeToRaw(v))
	}
	return raw
}

// genSyncRegions copies the target level's regions into the document being
// edited. fromFile allows reading the level file when the level is not open.
// Reports whether the document changed.
func genSyncRegions(fromFile bool) bool {
	d := genDoc
	if d == nil || d.Level == "" || genlevel.RegionsStage(d) == nil {
		return false
	}
	path := levelio.LevelPath(d.Level)
	// A template's layout goes into the level first (editor_generator_region_seed.go).
	seeding := genlevel.WantsSeed(d)
	if !genSeedRegions(d, path, fromFile) {
		return false
	}
	var changed bool
	var err error
	switch {
	case genLevelIsOpen(path):
		changed, err = genlevel.SyncRegions(d, genOpenLevelVolumes())
	case fromFile:
		changed, err = genlevel.SyncRegionsFromFile(d, path)
	default:
		return false
	}
	if err != nil {
		genStatusf("Regions not synced: %v", err)
		return false
	}
	// The switch turning off is a change of the doc too.
	changed = changed || seeding && !genlevel.WantsSeed(d)
	if changed {
		genAfterEdit()
	}
	return changed
}

// genTickRegionsSync is the periodic sync from the open level.
func genTickRegionsSync() {
	if genDoc == nil || time.Since(genRegionsSyncedAt) < genRegionsSyncEvery {
		return
	}
	genRegionsSyncedAt = time.Now()
	genSyncRegions(false)
}

// genGenerateWorldMenuItem is File > Generate World…: the open level's world
// generator, made if it has none.
func genGenerateWorldMenuItem() {
	open := Editor.level != nil && Editor.SavePath != ""
	label := "Generate World..."
	if open {
		label = "Generate World for " + levelio.LevelNameFromPath(Editor.SavePath) + "..."
	}
	if imgui.MenuItemV(label, "", false, open) {
		editorOpenWorldGenerator()
	}
	if imgui.IsItemHovered() {
		if !open {
			imgui.SetTooltip("Open (and save) a level in the level editor first.")
		} else {
			imgui.SetTooltip("Open the level's world generator, " + procgen.GeneratorsDirName +
				"/<level>_world.fgen,\nmaking it from the level_world template if there is none.")
		}
	}
}
