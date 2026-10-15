package editor

// editor_volume_region_test.go — environment region volumes in the editor:
// a duplicate owns its footprint, the scale gizmo's box change scales the
// footprint, the sync reads the open level's regions into the Generator's
// doc, and the world generator is made once.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/procgen"
	"foundry/internal/procgen/gencli"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/procgen/stages"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func withNoUploads(t *testing.T) {
	geo, tex := gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled
	gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled = false, false
	t.Cleanup(func() { gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled = geo, tex })
}

func regionTestLevel() *world.Level {
	v := world.NewVolume("west", mgl32.Vec3{-100, 5, 0})
	v.HalfX, v.HalfZ = 100, 50
	v.Region = world.NewVolumeRegion(&v, "desert_canyon")
	v.FitRegion()
	return &world.Level{Volumes: []world.Volume{v}}
}

func TestRegionDuplicateOwnsItsFootprintAndScaleFollowsTheBox(t *testing.T) {
	withNoUploads(t)
	prevLvl, prevIdx, prevKind := Editor.level, Editor.ActiveVolumeIdx, Editor.GizmoKind
	t.Cleanup(func() { Editor.level, Editor.ActiveVolumeIdx, Editor.GizmoKind = prevLvl, prevIdx, prevKind })
	Editor.level = regionTestLevel()
	editorSelectVolume(0)

	duplicateActiveVolume()
	if len(Editor.level.Volumes) != 2 {
		t.Fatalf("%d volumes after duplicate", len(Editor.level.Volumes))
	}
	a, b := &Editor.level.Volumes[0], &Editor.level.Volumes[1]
	b.Region.Footprint[0][0] = -999
	if a.Region.Footprint[0][0] == -999 {
		t.Fatal("the duplicate shares its source's footprint")
	}

	// The scale gizmo doubles the box's X: the footprint follows.
	editorSelectVolume(0)
	a = &Editor.level.Volumes[0]
	a.HalfX *= 2
	editorReconcileRegion(a)
	if got := a.Region.Footprint[1][0]; got != 200 {
		t.Errorf("footprint's east edge at %v after doubling the box, want 200", got)
	}
	if a.HalfX != 200 || a.HalfZ != 50 {
		t.Errorf("box %v x %v", a.HalfX, a.HalfZ)
	}
	// A reconciled region stays put.
	before := a.RegionWorldFootprint()
	editorReconcileRegion(a)
	if after := a.RegionWorldFootprint(); after[2] != before[2] {
		t.Errorf("a second reconcile moved the footprint %v -> %v", before[2], after[2])
	}
}

func TestGeneratorSyncsTheOpenLevelsRegions(t *testing.T) {
	withNoUploads(t)
	root := t.TempDir()
	prevRes := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(root, "resources")
	os.MkdirAll(filepath.Join(fileutil.ResourcesPath, "levels"), 0o755)
	prevLvl, prevPath, prevDoc, prevGenPath := Editor.level, Editor.SavePath, genDoc, genPath
	t.Cleanup(func() {
		fileutil.ResourcesPath = prevRes
		Editor.level, Editor.SavePath, genDoc, genPath = prevLvl, prevPath, prevDoc, prevGenPath
	})

	Editor.level = regionTestLevel()
	Editor.SavePath = filepath.Join(fileutil.ResourcesPath, "levels", "arena.yaml")

	d := stages.LevelWorld("arena_world")
	genDoc, genPath = d, ""
	if !genSyncRegions(false) {
		t.Fatal("the open level's region did not sync")
	}
	items := genlevel.RegionsStage(genDoc).Params.Items("regions")
	if len(items) != 1 || items[0].Str("id") != "west" {
		t.Fatalf("synced %v", items)
	}
	if pts := items[0].Points("footprint"); pts[0][0] != -200 || pts[2][1] != 50 {
		t.Errorf("synced footprint %v, want world -200..0 x -50..50", pts)
	}
	if genSyncRegions(false) {
		t.Error("an unchanged level synced as a change")
	}
	// Move it in the level: the next sync follows.
	Editor.level.Volumes[0].Position[0] = -150
	if !genSyncRegions(false) {
		t.Error("moving the region did not sync")
	}

	// The world generator is made once, from the template, targeting the level.
	p, created, err := gencli.EnsureWorldDoc("arena")
	if err != nil || !created {
		t.Fatalf("first: %v %v", created, err)
	}
	if _, again, _ := gencli.EnsureWorldDoc("arena"); again {
		t.Error("the world generator was made twice")
	}
	w, err := procgen.ReadDoc(p)
	if err != nil || w.Level != "arena" || genlevel.RegionsStage(w) == nil {
		t.Errorf("world doc %+v %v", w, err)
	}
}
