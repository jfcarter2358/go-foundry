package editor

// editor_generator_water_test.go — the ground_edit stage's water bodies
// panel (editor_generator_water.go) and the brush's flatten mode
// (editor_generator_paint.go): the panel draws from the last run's table,
// Lock pins a body at its anchor and level, a preset adds an auto entry,
// Auto takes the pin away again; a flatten stroke levels the ground under
// a height layer to the height where the stroke started.

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
	"foundry/internal/procgen/stages"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"gopkg.in/yaml.v2"
)

func groundEditTestDoc() *procgen.Doc {
	return &procgen.Doc{Name: "water", Seed: 1, Paint: []paint.LayerDef{{Name: "pond", Kind: paint.KindWater},
		{Name: "lift", Kind: paint.KindHeight}},
		Stages: []procgen.Stage{
			{Kind: stages.NoisePreviewKind, ID: "ground", Params: procgen.Params{}},
			{Kind: stages.GroundEditKind, ID: "edit", Params: procgen.Params{
				"height": procgen.List(procgen.Params{"layer": procgen.Mask("lift")}),
				"water":  procgen.List(procgen.Params{"layer": procgen.Mask("pond")})}},
		}}
}

func TestGeneratorWaterBodiesPanel(t *testing.T) {
	withGeneratorWindow(t)
	saved := genPaintLive
	t.Cleanup(func() { genPaintLive = saved })
	d := groundEditTestDoc()
	genSetDoc(d, "")
	genSelected = 1
	s := &genDoc.Stages[1]

	// Before a run: the panel says so, and draws.
	withImgui(func() { genFrame(imgui.Vec2{}, false, false) })

	// A run's table.
	body := stages.GroundBody{Layer: "pond", Style: "pond", Anchor: [2]float64{12.5, -3}, Level: 9.42, Spill: 9.47, Entry: -1,
		Material: "water_lake", Painted: 420, Area: 380, MaxDepth: 1.8}
	path := filepath.Join(procgen.OutDirFor(genDoc, procgen.Options{}), "edit", stages.BodiesFile)
	write := func(b stages.GroundBodies) {
		data, _ := yaml.Marshal(b)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		genWaterUI.path = "" // reread
	}
	write(stages.GroundBodies{Bodies: []stages.GroundBody{body}})
	withImgui(func() { genFrame(imgui.Vec2{}, false, false) })
	if b, err := genWaterBodies(s); err != nil || b == nil || len(b.Bodies) != 1 {
		t.Fatalf("table %+v, %v", b, err)
	}

	// Lock: an entry at the anchor holding the level, one undo step.
	undo := generatorUndoStack.len()
	genWaterLock(s, body)
	items := s.Params["bodies"].Items
	if len(items) != 1 || items[0].Vec2("at") != [2]float64{12.5, -3} || items[0].Float("level") != 9.42 || items[0].Bool("auto") {
		t.Fatalf("after Lock: %v", items)
	}
	if generatorUndoStack.len() != undo+1 {
		t.Fatal("Lock should be one document undo step")
	}
	// The next run matched it: locked, entry 0. A preset keeps the pin.
	body.Locked, body.Entry = true, 0
	write(stages.GroundBodies{Bodies: []stages.GroundBody{body}})
	withImgui(func() { genFrame(imgui.Vec2{}, false, false) })
	genWaterSetMaterial(s, body, "water_swamp")
	if items = s.Params["bodies"].Items; items[0].Str("material") != "water_swamp" || items[0].Bool("auto") {
		t.Fatalf("after the preset: %v", items)
	}
	// Auto keeps the entry for its material, marked auto.
	genWaterAuto(s, body)
	if items = s.Params["bodies"].Items; len(items) != 1 || !items[0].Bool("auto") {
		t.Fatalf("after Auto: %v", items)
	}
	// Back to the layer's material: the auto entry has nothing left to say.
	genWaterSetMaterial(s, body, "")
	if items = s.Params["bodies"].Items; len(items) != 0 {
		t.Fatalf("after clearing the preset: %v", items)
	}
	// An orphaned entry draws with Remove.
	s.Params["bodies"] = procgen.List(procgen.Params{"at": procgen.Vec2(-50, -50), "level": procgen.Float(3)})
	write(stages.GroundBodies{Bodies: []stages.GroundBody{}, Orphans: []int{0}})
	withImgui(func() { genFrame(imgui.Vec2{}, false, false) })
}

func TestGeneratorPaintFlatten(t *testing.T) {
	withGeneratorWindow(t)
	savedPaint, savedShift, savedBase := genPaint, genPaintShiftHeld, genPaintFlattenBase
	t.Cleanup(func() {
		genPaint, genPaintShiftHeld, genPaintFlattenBase = savedPaint, savedShift, savedBase
		genPaintReset()
	})
	genPaintShiftHeld = func() bool { return false }
	ground := func(x, z float64) float64 { return 10 + 0.1*x }
	genPaintFlattenBase = func(string) func(x, z float64) float64 { return ground }
	genSetDoc(groundEditTestDoc(), "")
	genPaint.layer, genPaint.preset = "lift", ""
	if !genPaintIsHeight() {
		t.Fatal("lift should be offered flatten")
	}
	genPaint.mode = paint.ModeFlatten
	genPaint.radius, genPaint.strength, genPaint.falloff, genPaint.spacing = 5, 1, 0, 0.25
	genPaintBegin(0, 0)
	genPaintDrag(20, 0)
	genPaintEnd()
	l := genDoc.PaintLayer("lift")
	for _, x := range []float64{0, 6, 14, 20} {
		if g := ground(x, 0) + l.Sample(x, 0); math.Abs(g-10) > 0.02 {
			t.Errorf("ground at x=%g: %.3f, want 10 (the height where the stroke started)", x, g)
		}
	}
	// A density layer is not offered flatten.
	genPaint.layer = "pond"
	if genPaintIsHeight() {
		t.Error("a water layer offered flatten")
	}
}
