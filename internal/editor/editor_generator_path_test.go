package editor

// editor_generator_path_test.go — footpaths in the Generator
// (editor_generator_path.go): a path brush stroke becomes a simplified
// spline in the ground_edit in the preset's style, as one document step
// the brush's Ctrl+Z takes back; the paths panel draws from the last run's
// table and its style, flatten and width controls edit the item; a drag on
// a direction layer paints the drag's direction.

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
	"foundry/internal/procgen/stages"
	"foundry/internal/procgen/terrain"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"gopkg.in/yaml.v2"
)

func TestGeneratorPathBrushAndPanel(t *testing.T) {
	withGeneratorWindow(t)
	savedPaint, savedPresets, savedShift, savedSpline, savedLive := genPaint, genPaintPresets, genPaintShiftHeld, genSpline, genPaintLive
	t.Cleanup(func() {
		genPaint, genPaintPresets, genPaintShiftHeld, genSpline, genPaintLive = savedPaint, savedPresets, savedShift, savedSpline, savedLive
		genPaintReset()
	})
	genPaintShiftHeld = func() bool { return false }
	p, err := stages.ParsePreset("trail", []byte(`name: Trail
doc: test
path: {style: stepping_stones, width_scale: 0.5, flatten: false, shape: {stride: 0.8}}
`))
	if err != nil {
		t.Fatal(err)
	}
	genPaintPresets.list = []*stages.Preset{p}
	genSetDoc(groundEditTestDoc(), "")
	genSelected = 1
	genPaint.on, genPaint.preset, genPaint.radius = true, "trail", 2
	if !genPaintPathActive() || genPaintRiverActive() || !genPaintSplineBrush() {
		t.Fatal("a path preset is not a path brush")
	}
	genPaintBegin(-30, 0)
	for x := -28.0; x <= 30; x += 1 {
		genPaintDrag(x, 10*math.Sin(x/12))
	}
	genPaintEnd()
	items := genDoc.StageByID("edit").Params["paths"].Items
	if len(items) != 1 {
		t.Fatalf("%d paths after a stroke", len(items))
	}
	it := items[0]
	pts := it.Points("path")
	if len(pts) < 3 || pts[0][2] != 2 || it.Str("style") != terrain.PathStepping || it["flatten"].Bool || it.Float("stride") != 0.8 {
		t.Fatalf("path %v", it)
	}
	if !genSpline.matches("edit", genParamPath{{Name: "paths", Item: 0}, {Name: "path", Item: -1}}) {
		t.Fatalf("the new path is not the active spline: %+v", genSpline)
	}
	genPaintUndo()
	if n := len(genDoc.StageByID("edit").Params["paths"].Items); n != 0 {
		t.Fatalf("undo left %d paths", n)
	}
	genPaintRedo()
	s := genDoc.StageByID("edit")
	if len(s.Params["paths"].Items) != 1 {
		t.Fatal("redo lost the path")
	}

	// The panel, before a run and from a run's table.
	withImgui(func() { genFrame(imgui.Vec2{}, false, false) })
	table := stages.GroundPaths{Paths: []stages.GroundPath{{Name: "trail_1", Style: terrain.PathStepping, Entry: 0, Length: 62,
		Crossings: 1, Stones: map[string]int{"step": 70, "ford": 4}}}}
	file := filepath.Join(procgen.OutDirFor(genDoc, procgen.Options{}), "edit", stages.PathsFile)
	data, _ := yaml.Marshal(table)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	genPathUI.path = ""
	withImgui(func() { genFrame(imgui.Vec2{}, false, false) })
	if genPathUI.paths == nil || len(genPathUI.paths.Paths) != 1 {
		t.Fatalf("panel read %+v", genPathUI.paths)
	}
	if r := genPathRun(genPathUI.paths.Paths[0]); r != "62 m, 1 ford, 70 step / 4 ford stones" {
		t.Fatalf("run column %q", r)
	}
	// Style, flatten and width through the panel's edits.
	genPathEdit(s, func(items []procgen.Params) []procgen.Params {
		items[0]["style"] = procgen.Enum(terrain.PathRock)
		items[0]["flatten"] = procgen.Bool(true)
		return items
	})
	genPathScaleWidth(genDoc.StageByID("edit"), 0, 1.25)
	it = genDoc.StageByID("edit").Params["paths"].Items[0]
	if it.Str("style") != terrain.PathRock || !it["flatten"].Bool || it.Points("path")[0][2] != 2.5 {
		t.Fatalf("after the panel's edits %v", it)
	}
}

func TestGeneratorDirectionPaintFollowsTheDrag(t *testing.T) {
	withGeneratorWindow(t)
	savedPaint, savedShift := genPaint, genPaintShiftHeld
	t.Cleanup(func() { genPaint, genPaintShiftHeld = savedPaint, savedShift; genPaintReset() })
	genPaintShiftHeld = func() bool { return false }
	d := groundEditTestDoc()
	d.Paint = append(d.Paint, paint.LayerDef{Name: "flow", Kind: paint.KindDirection})
	genSetDoc(d, "")
	genPaint.on, genPaint.preset, genPaint.layer = true, "", "flow"
	genPaint.radius, genPaint.strength, genPaint.falloff, genPaint.mode = 3, 1, 0, paint.ModeAdd
	// A click alone paints nothing.
	genPaintBegin(0, 0)
	genPaintEnd()
	if l := genDoc.PaintLayer("flow"); l != nil && len(l.Keys()) != 0 {
		t.Fatal("a click painted a direction")
	}
	// A drag south (+Z) paints (0, 1).
	genPaintBegin(0, 0)
	for z := 1.0; z <= 20; z++ {
		genPaintDrag(0, z)
	}
	genPaintEnd()
	v := genDoc.PaintLayer("flow").Direction(0, 10)
	if math.Abs(v[0]) > 0.02 || v[1] < 0.95 {
		t.Fatalf("direction after a drag along +Z: %v", v)
	}
}
