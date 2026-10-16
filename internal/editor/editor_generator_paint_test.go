package editor

// editor_generator_paint_test.go — the 2D view's paint brush from simulated
// mouse input: a new layer, a drag that paints it (and marks the document
// dirty), the brush's own undo and redo, a document undo that leaves the
// pixels alone, Save writing the tiles, and the mask param's widget.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
	"foundry/internal/procgen/stages"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

func TestGeneratorPaintBrush(t *testing.T) {
	withGeneratorWindow(t)
	procgen.RegisterStage(procgen.StageKind{Name: "zpaintmask", Category: "Test",
		Params: []procgen.ParamDef{{Name: "layer", Type: procgen.ParamMask, Default: procgen.Mask(""), Doc: "d"},
			{Name: "rules", Type: procgen.ParamList, Default: procgen.List(procgen.Params{}), Doc: "d", Fields: []procgen.ParamDef{
				{Name: "paint", Type: procgen.ParamMask, Default: procgen.Mask("lush"), Doc: "d"}}}},
		Run: func(*procgen.RunContext) error { return nil }})
	t.Cleanup(func() { procgen.UnregisterStage("zpaintmask") })
	savedPaint := genPaint
	saveStateFresh = true // the chip retakes its fingerprint every ask
	t.Cleanup(func() { genPaint = savedPaint; genPaintReset(); saveStateFresh = false })

	d := &procgen.Doc{Name: "brush", Seed: 1, Stages: []procgen.Stage{
		{Kind: stages.NoisePreviewKind, ID: "ground", Params: procgen.Params{}},
		{Kind: "zpaintmask", ID: "reads", Params: procgen.Params{}},
	}}
	genSetDoc(d, "")
	genForceTreesOpen = true
	t.Cleanup(func() { genForceTreesOpen = false })
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		genSelected = 0
		genViewMode = genView2D
		genLoadPreview(t)
		genFrame(imgui.Vec2{}, false, false)
		genV2.cx, genV2.cz, genV2.scale = 0, 0, 4 // 4 px a metre about the origin

		genPaintCreateLayer(paint.LayerDef{Name: "lush", Kind: paint.KindDensity})
		if genDoc.PaintDef("lush") == nil || genPaint.layer != "lush" {
			t.Fatal("New layer did not declare and select the layer")
		}
		generatorMarkSaved()
		genPaint.on, genPaint.radius, genPaint.strength, genPaint.falloff = true, 6, 1, 0
		genFrame(imgui.Vec2{X: 560, Y: 460}, false, false)
		// Press, drag 40 px right, release.
		genFrame(imgui.Vec2{X: 560, Y: 460}, true, false)
		for x := float32(565); x <= 600; x += 5 {
			genFrame(imgui.Vec2{X: x, Y: 460}, true, false)
		}
		genFrame(imgui.Vec2{X: 600, Y: 460}, false, false)
		l := genDoc.PaintLayer("lush")
		if l.Empty() {
			t.Fatal("the drag painted nothing")
		}
		if genPaintUndoStack.len() != 1 {
			t.Fatalf("%d undo steps for one stroke", genPaintUndoStack.len())
		}
		if !generatorEditorDirty() {
			t.Error("a stroke did not mark the document dirty")
		}
		if len(genPaintOverlay.tex) == 0 {
			t.Error("no overlay textures for the painted tiles")
		}
		painted := l.Hash()

		// A document edit and its undo leave the pixels alone.
		GeneratorPushUndo()
		genDoc.Seed = 99
		genAfterEdit()
		GeneratorUndo()
		if genDoc.PaintLayer("lush") == nil || genDoc.PaintLayer("lush").Hash() != painted {
			t.Error("a document undo changed the paint")
		}

		genPaintUndo()
		if !genDoc.PaintLayer("lush").Empty() {
			t.Error("undo left paint behind")
		}
		genPaintRedo()
		if genDoc.PaintLayer("lush").Hash() != painted {
			t.Error("redo did not bring the stroke back")
		}

		// Save writes the tiles beside the file.
		path := filepath.Join(t.TempDir(), "brush.fgen")
		if err := genSave(path); err != nil {
			t.Fatal(err)
		}
		files, _ := os.ReadDir(filepath.Join(filepath.Dir(path), "brush.paint", "lush"))
		if len(files) == 0 {
			t.Error("Save wrote no tiles")
		}
		if generatorEditorDirty() {
			t.Error("dirty after Save")
		}

		// The mask widget, in the panel of a stage that has one.
		genSelected = 1
		genFrame(imgui.Vec2{}, false, false)
	})
}
