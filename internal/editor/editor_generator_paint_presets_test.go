package editor

// editor_generator_paint_presets_test.go — brush presets in the paint tool
// (editor_generator_paint_presets.go): picking a preset whose layers the
// document lacks asks first; Add applies its recipe as one document undo
// step; a stroke then writes every layer at its own strength, as ONE brush
// undo step that takes them all back; and the preset files the editor
// reads are the repo's.

import (
	"math"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/stages"
)

func TestGeneratorPaintPresetStroke(t *testing.T) {
	withGeneratorWindow(t)
	savedPaint, savedPresets, savedShift := genPaint, genPaintPresets, genPaintShiftHeld
	t.Cleanup(func() {
		genPaint, genPaintPresets, genPaintShiftHeld = savedPaint, savedPresets, savedShift
		genPaintReset()
	})
	genPaintShiftHeld = func() bool { return false }

	p, err := stages.ParsePreset("floor", []byte(`name: Floor
doc: test
layers:
  - {layer: litter, strength: 1}
  - {layer: twigs, strength: 0.5}
recipe:
  paint:
    - {name: litter, kind: material}
    - {name: twigs, kind: density}
`))
	if err != nil {
		t.Fatal(err)
	}
	genPaintPresets.list = []*stages.Preset{p}
	d := &procgen.Doc{Name: "presets", Seed: 1, Stages: []procgen.Stage{{Kind: stages.NoisePreviewKind, ID: "ground", Params: procgen.Params{}}}}
	genSetDoc(d, "")

	// The document lacks the layers: picking asks.
	genPaintPickPreset(p)
	if genPaint.preset != "" || genPaintPresets.ask != p {
		t.Fatalf("picking a preset with missing layers should ask first (preset %q)", genPaint.preset)
	}
	undoBefore := generatorUndoStack.len()
	genPaintApplyRecipe(p)
	if genDoc.PaintDef("litter") == nil || genDoc.PaintDef("twigs") == nil || genPaint.preset != "floor" || genPaint.layer != "litter" {
		t.Fatalf("recipe: layers %v preset %q layer %q", genDoc.Paint, genPaint.preset, genPaint.layer)
	}
	if generatorUndoStack.len() != undoBefore+1 {
		t.Fatalf("the recipe should be one document undo step (%d → %d)", undoBefore, generatorUndoStack.len())
	}

	// One stroke, both layers, each at its strength.
	genPaint.radius, genPaint.strength, genPaint.falloff, genPaint.spacing = 4, 1, 0, 0.25
	genPaintBegin(0, 0)
	genPaintDrag(6, 0)
	genPaintDrag(12, 0)
	genPaintEnd()
	litter, twigs := genDoc.PaintLayer("litter"), genDoc.PaintLayer("twigs")
	if math.Abs(litter.Sample(6, 0)-1) > 0.02 || math.Abs(twigs.Sample(6, 0)-0.5) > 0.06 {
		t.Fatalf("litter %.3f (want 1), twigs %.3f (want 0.5 or so)", litter.Sample(6, 0), twigs.Sample(6, 0))
	}
	if genPaintUndoStack.len() != 1 {
		t.Fatalf("%d undo steps for one preset stroke", genPaintUndoStack.len())
	}
	genPaintUndo()
	if !genDoc.PaintLayer("litter").Empty() || !genDoc.PaintLayer("twigs").Empty() {
		t.Fatal("undo left one of the preset's layers painted")
	}
	genPaintRedo()
	if genDoc.PaintLayer("twigs").Empty() || genDoc.PaintLayer("litter").Empty() {
		t.Fatal("redo did not bring both layers back")
	}

	// (no preset) paints the active layer alone again.
	genPaint.preset = ""
	genPaint.layer = "twigs"
	genPaintBegin(40, 40)
	genPaintEnd()
	if genDoc.PaintLayer("litter").Sample(40, 40) != 0 || genDoc.PaintLayer("twigs").Sample(40, 40) == 0 {
		t.Fatal("without a preset the stroke should paint the active layer only")
	}

	// The shipped presets load (the picker's list).
	genPaintLoadPresets()
	if len(genPaintPresets.errs) > 0 {
		t.Fatalf("preset files: %v", genPaintPresets.errs)
	}
}
