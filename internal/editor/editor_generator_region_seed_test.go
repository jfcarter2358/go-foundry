package editor

// editor_generator_region_seed_test.go — phase 6: a template's region layout
// written into its level from the Generator, headless. With the level open:
// the half-second sync waits (it would wipe the layout), the write is
// queued onto the main window, lands as one undo step, and the doc syncs
// from it. With the level closed: a run's sync writes the level file.

import (
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/procgen/regions"
	"foundry/internal/procgen/stages"
	"foundry/internal/uiwin"
	"foundry/internal/world"
)

func TestGeneratorSeedsTheOpenLevelWithTheTemplatesRegions(t *testing.T) {
	withRegionLevel(t)
	Editor.level = &world.Level{}
	genDoc, genPath = stages.CanyonSavannah("arena_world"), ""
	before := levelUndoStack.len()

	if genSyncRegions(false) {
		t.Fatal("the tick synced before the layout reached the level")
	}
	if !genlevel.WantsSeed(genDoc) || len(genDoc.StageByID("regions").Params.Items(regions.ParamRegions)) != 2 {
		t.Fatal("the layout was lost before it reached the level")
	}
	if !genSeedWaiting(genDoc) {
		t.Error("a run would not wait for the queued write")
	}
	uiwin.DrainWindowOpens()

	if n := len(Editor.level.Volumes); n != 2 {
		t.Fatalf("the level has %d volumes", n)
	}
	if levelUndoStack.len() != before+1 {
		t.Errorf("undo steps %d -> %d, want one", before, levelUndoStack.len())
	}
	if genlevel.WantsSeed(genDoc) {
		t.Error("the switch is still on")
	}
	items := genDoc.StageByID("regions").Params.Items(regions.ParamRegions)
	if len(items) != 2 || items[0].Str("id") != "canyon_west" || items[1].Str("id") != "savannah_east" {
		t.Fatalf("synced %v", items)
	}
	v := Editor.level.Volumes[1]
	if v.Region == nil || v.Region.Environment != stages.SavannahEnvironment || v.Position.X() != 256 {
		t.Errorf("savannah_east %+v at %v", v.Region, v.Position)
	}
}

func TestGeneratorSeedsAClosedLevelsFile(t *testing.T) {
	withRegionLevel(t)
	Editor.SavePath = "" // no level open
	genDoc, genPath = stages.CanyonSavannah("field_world"), ""
	if !genSyncRegions(true) {
		t.Fatal("the run's sync changed nothing")
	}
	raw, err := genlevel.ReadRawLevel(filepath.Join(fileutil.ResourcesPath, "levels", "field.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !genlevel.HasRegionVolumes(&raw) || len(raw.Volumes) != 2 || genlevel.WantsSeed(genDoc) {
		t.Errorf("level volumes %d, switch %v", len(raw.Volumes), genlevel.WantsSeed(genDoc))
	}
}
