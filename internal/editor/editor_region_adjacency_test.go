package editor

// editor_region_adjacency_test.go — phase 5's editor paths, headless: the
// level editor's off-thread adjacency check finds a forbidden pair, one
// "Insert intermediate region" (one undo step) clears it, and the
// Generator's 2D footprint edits land in the open level's region volume
// through the main window's queue, one undo step per drag, and sync back
// into the doc.

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/procgen/regions"
	"foundry/internal/procgen/stages"
	"foundry/internal/uiwin"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func forbiddenTestLevel() *world.Level {
	mk := func(id, env string, x float32) world.Volume {
		v := world.NewVolume(id, mgl32.Vec3{x, 5, 0})
		v.HalfX, v.HalfZ = 300, 300
		v.Region = world.NewVolumeRegion(&v, env)
		v.FitRegion()
		return v
	}
	return &world.Level{Volumes: []world.Volume{
		mk("dry", regions.EnvDesertCanyon, -300),
		mk("cold", regions.EnvTundra, 300),
	}}
}

// waitAdjacency ticks the level's check until it answers the level as it is.
func waitAdjacency(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		regionAdjacencyTick()
		if _, ok, current := levelAdj.latest(); ok && current {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the adjacency check never answered")
}

func withRegionLevel(t *testing.T) {
	withNoUploads(t)
	root := t.TempDir()
	prevRes := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(root, "resources")
	os.MkdirAll(filepath.Join(fileutil.ResourcesPath, "levels"), 0o755)
	prevLvl, prevPath, prevIdx := Editor.level, Editor.SavePath, Editor.ActiveVolumeIdx
	prevDoc, prevGenPath, prevSel := genDoc, genPath, genRegionsUI.sel
	t.Cleanup(func() {
		fileutil.ResourcesPath = prevRes
		Editor.level, Editor.SavePath, Editor.ActiveVolumeIdx = prevLvl, prevPath, prevIdx
		genDoc, genPath, genRegionsUI.sel = prevDoc, prevGenPath, prevSel
		levelUndoStack.clear()
		uiwin.DrainWindowOpens()
	})
	Editor.level = forbiddenTestLevel()
	Editor.SavePath = filepath.Join(fileutil.ResourcesPath, "levels", "arena.yaml")
	levelUndoStack.clear()
}

func TestLevelEditorWarnsAndInsertsAnIntermediateRegion(t *testing.T) {
	withRegionLevel(t)
	waitAdjacency(t)
	forb := levelAdj.forbidden("dry")
	if len(forb) != 1 || forb[0].Suggestion() != regions.EnvSavannah {
		t.Fatalf("forbidden for dry: %+v", forb)
	}
	before := levelUndoStack.len()
	ids := editorInsertIntermediate(forb[0].IDA, forb[0].IDB)
	if len(ids) != 1 {
		t.Fatalf("inserted %v", ids)
	}
	if levelUndoStack.len() != before+1 {
		t.Errorf("undo steps %d -> %d, want one", before, levelUndoStack.len())
	}
	nv := activeVolume()
	if nv == nil || nv.ID != ids[0] || nv.Region == nil || nv.Region.Environment != regions.EnvSavannah {
		t.Fatalf("the new region is not selected: %+v", nv)
	}
	waitAdjacency(t)
	if f := levelAdj.forbidden(""); len(f) != 0 {
		t.Fatalf("still forbidden after the insert: %s", f[0].Warning())
	}
}

func TestGeneratorFootprintEditsWriteBackToTheLevel(t *testing.T) {
	withRegionLevel(t)
	genDoc, genPath = stages.LevelWorld("arena_world"), ""
	genDoc.Level = "arena"
	if !genRegionLevelOpen() {
		t.Fatal("the doc's level does not read as open")
	}
	genSyncRegions(false)
	genSelectRegion("dry")

	// A drag: two moves, one undo step; the end refits and resyncs.
	before := levelUndoStack.len()
	genRegionDrag.pushed = false
	genRegionMovePoint("dry", 0, -650, -320)
	genRegionMovePoint("dry", 0, -660, -330)
	genRegionEndDrag("dry")
	uiwin.DrainWindowOpens()
	if levelUndoStack.len() != before+1 {
		t.Errorf("a drag made %d undo steps, want 1", levelUndoStack.len()-before)
	}
	v, _ := genRegionVolume("dry")
	if p := v.RegionWorldFootprint()[0]; math.Abs(p[0]+660) > 1e-3 || math.Abs(p[1]+330) > 1e-3 {
		t.Fatalf("the level's point 0 is at %v, want (-660, -330)", p)
	}
	items := genlevel.RegionsStage(genDoc).Params.Items(regions.ParamRegions)
	var dry []float64
	for _, it := range items {
		if it.Str(regions.FieldID) == "dry" {
			dry = it.Points(regions.FieldFootprint)[0]
		}
	}
	if len(dry) < 2 || dry[0] != -660 || dry[1] != -330 {
		t.Errorf("the doc's synced point 0 is %v, want the level's", dry)
	}

	// Insert after point 1, then delete it: one undo step each.
	n := len(v.Region.Footprint)
	before = levelUndoStack.len()
	genRegionInsertPoint("dry", 1, -300, -310)
	uiwin.DrainWindowOpens()
	v, _ = genRegionVolume("dry")
	if len(v.Region.Footprint) != n+1 || levelUndoStack.len() != before+1 {
		t.Fatalf("insert: %d points (want %d), %d undo steps", len(v.Region.Footprint), n+1, levelUndoStack.len()-before)
	}
	if p := v.RegionWorldFootprint()[2]; math.Abs(p[0]+300) > 1e-3 || math.Abs(p[1]+310) > 1e-3 {
		t.Errorf("inserted point at %v", p)
	}
	genRegionDeletePoint("dry", 2)
	uiwin.DrainWindowOpens()
	v, _ = genRegionVolume("dry")
	if len(v.Region.Footprint) != n || levelUndoStack.len() != before+2 {
		t.Errorf("delete: %d points, %d undo steps", len(v.Region.Footprint), levelUndoStack.len()-before)
	}
	// Undo takes the delete back.
	EditorUndo()
	if v, _ = genRegionVolume("dry"); v == nil || len(v.Region.Footprint) != n+1 {
		t.Errorf("undo did not restore the deleted point")
	}
}
