package editor

// editor_generator_river_test.go — rivers in the Generator
// (editor_generator_river.go): a river brush stroke becomes a simplified
// spline in the ground_edit as one document step that the brush's Ctrl+Z
// takes back; the rivers panel draws from the last run's table; Adopt copies
// a traced river into a hand river; the width buttons scale every point.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/stages"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"gopkg.in/yaml.v2"
)

func TestGeneratorRiverBrushAndPanel(t *testing.T) {
	withGeneratorWindow(t)
	savedPaint, savedPresets, savedShift, savedSpline, savedLive := genPaint, genPaintPresets, genPaintShiftHeld, genSpline, genPaintLive
	t.Cleanup(func() {
		genPaint, genPaintPresets, genPaintShiftHeld, genSpline, genPaintLive = savedPaint, savedPresets, savedShift, savedSpline, savedLive
		genPaintReset()
	})
	genPaintShiftHeld = func() bool { return false }
	p, err := stages.ParsePreset("brook", []byte(`name: Brook
doc: test
river: {depth: 0.8, material: water_river, shape: {pools: 0.5}}
`))
	if err != nil {
		t.Fatal(err)
	}
	genPaintPresets.list = []*stages.Preset{p}
	genSetDoc(groundEditTestDoc(), "")
	genSelected = 1
	genPaint.on, genPaint.preset, genPaint.radius = true, "brook", 3
	if !genPaintRiverActive() {
		t.Fatal("a river preset is not a river brush")
	}

	// A drag: a river, its width the brush's diameter.
	genPaintBegin(-30, 0)
	for x := -27.0; x <= 30; x += 1.5 {
		genPaintDrag(x, x*x/200)
	}
	genPaintEnd()
	s := genDoc.StageByID("edit")
	items := s.Params["rivers"].Items
	if len(items) != 1 {
		t.Fatalf("%d rivers after a stroke", len(items))
	}
	path := items[0].Points("path")
	if len(path) < 2 || path[0][2] != 6 || path[0][3] != 0.8 || items[0].Float("pools") != 0.5 {
		t.Fatalf("river %v", items[0])
	}
	if !genSpline.matches("edit", genParamPath{{Name: "rivers", Item: 0}, {Name: "path", Item: -1}}) {
		t.Fatalf("the new river is not the active spline: %+v", genSpline)
	}
	if genPaintUndoStack.len() != 1 {
		t.Fatalf("%d brush undo steps", genPaintUndoStack.len())
	}
	genPaintUndo()
	if n := len(genDoc.StageByID("edit").Params["rivers"].Items); n != 0 {
		t.Fatalf("undo left %d rivers", n)
	}
	genPaintRedo()
	s = genDoc.StageByID("edit")
	if n := len(s.Params["rivers"].Items); n != 1 {
		t.Fatalf("redo brought back %d rivers", n)
	}

	// The panel draws before a run, and from a run's table.
	withImgui(func() { genFrame(imgui.Vec2{}, false, false) })
	tr := stages.GroundTraced{Name: "drainage_1", Points: [][2]float64{{0, 40}, {10, 20}, {12, 2}}, Width: []float64{2, 3, 4},
		Depth: []float64{0.4, 0.5, 0.6}}
	table := stages.GroundRivers{Rivers: []stages.GroundRiver{
		{Name: "brook_1", Kind: "painted", Entry: 0, Material: "water_river", Length: 60, Source: 10, Mouth: 8},
		{Name: "drainage_1", Kind: "drainage", Entry: -1, Material: "water_river", Length: 40, Source: 12, Mouth: 9, Into: "brook_1"},
	}, Traced: []stages.GroundTraced{tr}}
	file := filepath.Join(procgen.OutDirFor(genDoc, procgen.Options{}), "edit", stages.RiversFile)
	data, _ := yaml.Marshal(table)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	genRiverUI.path = ""
	withImgui(func() { genFrame(imgui.Vec2{}, false, false) })
	if genRiverUI.rivers == nil || len(genRiverUI.rivers.Rivers) != 2 {
		t.Fatalf("panel read %+v", genRiverUI.rivers)
	}

	// Adopt: a hand river with the traced points, widths and depths.
	genRiverAdopt(s, &tr)
	items = genDoc.StageByID("edit").Params["rivers"].Items
	if len(items) != 2 || items[1].Str("from") != "adopted" {
		t.Fatalf("adopt: %v", items)
	}
	ap := items[1].Points("path")
	if len(ap) != 3 || ap[2][2] != 4 || ap[2][3] != 0.6 {
		t.Fatalf("adopted path %v", ap)
	}
	// Wider: every point ×1.25.
	genRiverScaleWidth(genDoc.StageByID("edit"), 1, 1.25)
	if w := genDoc.StageByID("edit").Params["rivers"].Items[1].Points("path")[2][2]; w != 5 {
		t.Fatalf("width after ×1.25: %v", w)
	}
	k := procgen.KindByName(stages.GroundEditKind)
	var def *procgen.ParamDef
	for i := range k.Params {
		if k.Params[i].Name == "rivers" {
			for j := range k.Params[i].Fields {
				if k.Params[i].Fields[j].Name == "path" {
					def = &k.Params[i].Fields[j]
				}
			}
		}
	}
	if def == nil || genSplineWidthChannel(def) != 2 {
		t.Fatalf("width channel of a river path: %v", def)
	}
}
