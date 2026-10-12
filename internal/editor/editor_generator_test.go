package editor

// editor_generator_test.go — the Generator editor's logic, without a window:
// undo and redo, the stage list's actions, param paths into list items, ref
// renaming, the save chip, and a run through the editor's job with the
// splice into a level file.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/procgen/stages"
	"foundry/internal/uiwin"
	"foundry/internal/world"

	"gopkg.in/yaml.v2"
)

// withGeneratorState saves and restores the editor's generator globals, and
// points resources/ at a scratch folder.
func withGeneratorState(t *testing.T) string {
	t.Helper()
	savedDoc, savedPath, savedSel, savedRev := genDoc, genPath, genSelected, genRev
	savedRes := fileutil.ResourcesPath
	savedSpline, savedView := genSpline, genViewMode
	dir := t.TempDir()
	fileutil.ResourcesPath = dir
	t.Cleanup(func() {
		genDoc, genPath, genSelected, genRev = savedDoc, savedPath, savedSel, savedRev
		fileutil.ResourcesPath = savedRes
		genSpline, genViewMode = savedSpline, savedView
		genFileList = nil
		genStaleCache = nil
		GeneratorClearUndoHistory()
	})
	return dir
}

func demoGenerator(t *testing.T) *procgen.Doc {
	t.Helper()
	d, err := procgen.NewFromTemplate("noise_demo", "demo")
	if err != nil {
		t.Fatal(err)
	}
	d.Stages[0].Params["size"] = procgen.Float(64)
	d.Stages[0].Params["resolution"] = procgen.Int(16)
	return d
}

func TestGeneratorUndoRedo(t *testing.T) {
	withGeneratorState(t)
	genSetDoc(demoGenerator(t), "")
	if GeneratorCanUndo() {
		t.Fatal("a fresh document has undo history")
	}
	GeneratorPushUndo()
	genDoc.Stages[0].Params["height"] = procgen.Float(99)
	genAfterEdit()
	GeneratorUndo()
	if _, set := genDoc.Stages[0].Params["height"]; set {
		t.Error("undo did not take the edit back")
	}
	GeneratorRedo()
	if genDoc.Stages[0].Params.Float("height") != 99 {
		t.Error("redo did not put the edit back")
	}
}

func TestGeneratorStageActions(t *testing.T) {
	withGeneratorState(t)
	d := demoGenerator(t)
	d.Stages = append(d.Stages, procgen.Stage{Kind: stages.NoisePreviewKind, ID: "second", Params: procgen.Params{}})
	genSetDoc(d, "")

	genStageAction("dup", 0)
	if len(genDoc.Stages) != 3 || genDoc.Stages[1].ID != "ground_2" || genSelected != 1 {
		t.Fatalf("dup: %d stages, [1]=%q, selected %d", len(genDoc.Stages), genDoc.Stages[1].ID, genSelected)
	}
	// The duplicate's params are its own.
	genDoc.Stages[1].Params["height"] = procgen.Float(1)
	if _, set := genDoc.Stages[0].Params["height"]; set {
		t.Error("a duplicate shares its params with the original")
	}
	genStageAction("down", 1)
	if genDoc.Stages[2].ID != "ground_2" || genSelected != 2 {
		t.Errorf("down: [2]=%q selected %d", genDoc.Stages[2].ID, genSelected)
	}
	genStageAction("up", 2)
	if genDoc.Stages[1].ID != "ground_2" {
		t.Errorf("up: [1]=%q", genDoc.Stages[1].ID)
	}
	genStageAction("remove", 1)
	if len(genDoc.Stages) != 2 || genDoc.Stages[1].ID != "second" {
		t.Errorf("remove: %+v", genDoc.Stages)
	}
	// Four actions, four undo steps.
	for i := 0; i < 4; i++ {
		GeneratorUndo()
	}
	if len(genDoc.Stages) != 2 || genDoc.Stages[1].ID != "second" || GeneratorCanUndo() {
		t.Errorf("four undos did not get back to the start: %d stages", len(genDoc.Stages))
	}
}

func TestGeneratorParamPathsReachListItems(t *testing.T) {
	k := procgen.KindByName(stages.NoisePreviewKind)
	p := procgen.Params{}
	path := genParamPath{{Name: "bumps", Item: -1}}
	// The list is at its default (empty): an item path reads nothing.
	if _, set := genValueAt(k.Params, p, path); set {
		t.Error("a defaulted list reads as set")
	}
	genSetValueAt(k.Params, p, path, procgen.List(procgen.Params{}, procgen.Params{"radius": procgen.Float(7)}))
	field := path.child(1, "radius")
	if v, _ := genValueAt(k.Params, p, field); v.Num != 7 {
		t.Errorf("item 2's radius reads %v", v.Num)
	}
	// A field the item does not hold reads its default.
	if v, _ := genValueAt(k.Params, p, path.child(0, "height")); v.Num != 15 {
		t.Errorf("a missing field reads %v, want its default 15", v.Num)
	}
	genSetValueAt(k.Params, p, path.child(0, "height"), procgen.Float(-3))
	if p.Items("bumps")[0].Float("height") != -3 {
		t.Error("a write into an item did not land")
	}
	genClearValueAt(k.Params, p, path.child(0, "height"))
	if p.Items("bumps")[0].Float("height") != 15 {
		t.Error("clearing an item's field did not restore its default")
	}
	genClearValueAt(k.Params, p, path)
	if _, set := p["bumps"]; set {
		t.Error("clearing a top-level param left it in the stage")
	}
	if d := genDefAt(k.Params, field); d == nil || d.Name != "radius" {
		t.Errorf("genDefAt(%v) = %v", field, d)
	}
}

func TestGeneratorRenameFollowsRefs(t *testing.T) {
	withGeneratorState(t)
	genSetDoc(&procgen.Doc{Name: "x", Seed: 1}, "")
	// No real kind refers to another yet; hand-build one with a ref in a list.
	procgen.RegisterStage(procgen.StageKind{Name: "ztestref", Category: "Test", Doc: "t", Label: "t",
		Params: []procgen.ParamDef{
			{Name: "from", Type: procgen.ParamStage, Default: procgen.StageRef(""), Doc: "d"},
			{Name: "rules", Type: procgen.ParamList, Default: procgen.List(), Doc: "d", Fields: []procgen.ParamDef{
				{Name: "src", Type: procgen.ParamStage, Default: procgen.StageRef(""), Doc: "d"},
			}},
		},
		Run: func(*procgen.RunContext) error { return nil }})
	t.Cleanup(func() { procgen.UnregisterStage("ztestref") })
	genDoc.Stages = []procgen.Stage{
		{Kind: stages.NoisePreviewKind, ID: "ground", Params: procgen.Params{}},
		{Kind: "ztestref", ID: "user", Params: procgen.Params{
			"from":  procgen.StageRef("ground"),
			"rules": procgen.List(procgen.Params{"src": procgen.StageRef("ground")}),
		}},
	}
	genRenameStage("ground", "terrain")
	u := genDoc.Stages[1].Params
	if genDoc.Stages[0].ID != "terrain" || u.Str("from") != "terrain" || u.Items("rules")[0].Str("src") != "terrain" {
		t.Errorf("rename: id %q from %q src %q", genDoc.Stages[0].ID, u.Str("from"), u.Items("rules")[0].Str("src"))
	}
	if probs := genDoc.Validate(); len(probs) != 0 {
		t.Errorf("after a rename: %v", probs)
	}
}

func TestGeneratorSaveChip(t *testing.T) {
	dir := withGeneratorState(t)
	genSetDoc(demoGenerator(t), "")
	saveStateFresh = true
	defer func() { saveStateFresh = false }()
	if generatorEditorDirty() {
		t.Fatal("a fresh document reads dirty")
	}
	GeneratorPushUndo()
	genDoc.Seed = 42
	genAfterEdit()
	if !generatorEditorDirty() {
		t.Error("an edit does not read dirty")
	}
	path := filepath.Join(dir, procgen.GeneratorsDirName, "demo"+procgen.Ext)
	if err := genSave(path); err != nil {
		t.Fatal(err)
	}
	if generatorEditorDirty() {
		t.Error("a save still reads dirty")
	}
	GeneratorUndo()
	if !generatorEditorDirty() {
		t.Error("undo past the save does not read dirty")
	}
	GeneratorRedo()
	if generatorEditorDirty() {
		t.Error("redo back to the saved state reads dirty")
	}
	// It reloads as it was saved.
	if err := genLoad(path); err != nil {
		t.Fatal(err)
	}
	if genDoc.Seed != 42 || genName() != "demo" {
		t.Errorf("reloaded seed %d name %q", genDoc.Seed, genName())
	}
	if err := genSave(filepath.Join(dir, "generators", "Bad Name.fgen")); err == nil {
		t.Error("saved under an illegal name")
	}
}

func TestGeneratorRunJobSplicesIntoALevelFile(t *testing.T) {
	dir := withGeneratorState(t)
	os.MkdirAll(filepath.Join(dir, "levels"), 0o755)
	lvl := world.RawLevel{Scenery: []world.RawScenery{{ID: "barrel", Model: "models/barrel.fmdl"}}}
	data, _ := yaml.Marshal(lvl)
	levelPath := filepath.Join(dir, "levels", "arena.yaml")
	os.WriteFile(levelPath, data, 0o644)

	d := demoGenerator(t)
	d.Level = "arena"
	genSetDoc(d, "")

	genBeginRun(false, nil)
	if !GeneratorRunning() {
		t.Fatal("the run did not start")
	}
	deadline := time.Now().Add(60 * time.Second)
	for GeneratorRunning() && time.Now().Before(deadline) {
		genStepRun()
		time.Sleep(10 * time.Millisecond)
	}
	if GeneratorRunning() {
		t.Fatal("the run did not finish in a minute")
	}
	if genPendingSplice.res == nil && !genPendingSplice.queued {
		t.Fatalf("no splice pending after the run: %s", genRunJob.last)
	}
	// The level is not open, so the splice is a file splice; run the queued
	// main-thread work as DrainWindowOpens would.
	uiwin.DrainWindowOpens()
	back, err := genlevel.ReadRawLevel(levelPath)
	if err != nil {
		t.Fatal(err)
	}
	if sc, _ := genlevel.Count(&back, "demo"); sc != 4 {
		t.Errorf("the level holds %d of the generator's tiles, want 4 (%s)", sc, genStatus)
	}
	if _, err := os.Stat(filepath.Join(dir, "generated", "demo", "ground", "tile_1_1.fnm")); err != nil {
		t.Errorf("no cooked tile: %v", err)
	}
	if genStageStale("ground") {
		t.Error("the stage reads stale after its run")
	}
}
