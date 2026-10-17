package editor

// editor_generator_paint_presets.go — the paint bar's Preset picker: brush
// presets (resources/paint/*.fpb; procgen/stages paint_preset.go) that paint
// several layers per stroke, in the 2D view and the 3D viewport alike
// (docs/guide/36-terrain-paint.md "Brush presets").
//
// Picking a preset makes every stamp write each of its layers at the brush's
// strength times the layer's own (editor_generator_paint.go's
// genPaintStampAll); the stroke is still one undo step, holding every layer
// it touched. The active layer becomes the preset's first, so the overlay
// shows the main thing the preset lays.
//
// A preset whose layers the document does not declare cannot paint yet: the
// picker then opens a popup listing what its RECIPE would add — the layers,
// the Poly Haven stages, the grass_field and scatter rules that read them
// and the splat entries — and Add applies it as ONE document undo step
// (stages.ApplyRecipe), so "Forest floor" on a fresh generator just works
// once the generator has run. (none) goes back to painting one layer.
//
// The preset files are read when the combo opens (a file added or edited
// shows the next time), so there is nothing to reload.

import (
	"strconv"
	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/procgen/stages"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// genPaintPresets is the presets as last read, and the recipe popup's
// preset.
var genPaintPresets = struct {
	list []*stages.Preset
	errs []error
	// ask is the preset the recipe popup offers; askOpen opens it next
	// frame.
	ask     *stages.Preset
	askOpen bool
}{}

// genPaintLoadPresets rereads the preset folder.
func genPaintLoadPresets() {
	genPaintPresets.list, genPaintPresets.errs = stages.LoadPresets(stages.PresetsDir())
}

// genPaintCurrentPreset is the active preset, or nil.
func genPaintCurrentPreset() *stages.Preset {
	if genPaint.preset == "" {
		return nil
	}
	if genPaintPresets.list == nil {
		genPaintLoadPresets()
	}
	return stages.FindPreset(genPaintPresets.list, genPaint.preset)
}

// genPaintPresetCombo is the bar's Preset combo and its recipe popup.
func genPaintPresetCombo() {
	cur := "(no preset)"
	if p := genPaintCurrentPreset(); p != nil {
		cur = p.Label()
	}
	imgui.SetNextItemWidth(130)
	if imguibackend.BeginCombo("##genpaintpreset", cur) {
		if imgui.IsWindowAppearing() {
			genPaintLoadPresets()
		}
		if imguibackend.SelectableV("(no preset)", genPaint.preset == "", 0, imgui.Vec2{}) {
			genPaint.preset = ""
		}
		for _, p := range genPaintPresets.list {
			if imguibackend.SelectableV(p.Label()+"##pp"+p.ID, p.ID == genPaint.preset, 0, imgui.Vec2{}) {
				genPaintPickPreset(p)
			}
			editorMenuTooltip(genPaintPresetTip(p))
		}
		for _, e := range genPaintPresets.errs {
			imgui.Text(e.Error())
		}
		if len(genPaintPresets.list) == 0 {
			imgui.Text("No presets in resources/paint/")
		}
		imguibackend.EndCombo()
	}
	editorMenuTooltip("A brush preset paints several layers per stroke (each at the brush's strength times its own).")
	if genPaintPresets.askOpen {
		genPaintPresets.askOpen = false
		imgui.OpenPopup("##genpaintrecipe")
	}
	genPaintRecipePopup()
}

// genPaintPresetTip is a preset's tooltip: its note and what it paints.
func genPaintPresetTip(p *stages.Preset) string {
	var ls []string
	for _, l := range p.Layers {
		ls = append(ls, l.Layer+" ×"+strconv.FormatFloat(l.Strength, 'g', 3, 64))
	}
	tip := strings.Join(ls, ", ")
	if p.Doc != "" {
		tip = p.Doc + "\n" + tip
	}
	return tip
}

// genPaintPickPreset makes p the brush's preset, or asks to apply its
// recipe when the document lacks its layers.
func genPaintPickPreset(p *stages.Preset) {
	if genDoc == nil {
		return
	}
	if stages.NeedsRecipe(genDoc, p) {
		genPaintPresets.ask, genPaintPresets.askOpen = p, true
		return
	}
	genPaintUsePreset(p)
}

// genPaintUsePreset activates p: its first layer becomes the active one.
func genPaintUsePreset(p *stages.Preset) {
	genPaint.preset = p.ID
	if len(p.Layers) > 0 && genDoc.PaintDef(p.Layers[0].Layer) != nil && genPaint.layer != p.Layers[0].Layer {
		genPaint.layer = p.Layers[0].Layer
		genPaintFreeOverlay()
	}
	genStatusf("Brush preset %s", p.Label())
}

// genPaintRecipePopup offers to apply the asked preset's recipe.
func genPaintRecipePopup() {
	if !imgui.BeginPopup("##genpaintrecipe") {
		return
	}
	defer imgui.EndPopup()
	p := genPaintPresets.ask
	if p == nil || genDoc == nil {
		imgui.CloseCurrentPopup()
		return
	}
	if p.River != nil {
		// A river brush (editor_generator_river.go): its strokes go into a
		// ground_edit, dressed by the recipe.
		imgui.Text(p.Label() + " draws rivers into a ground_edit, dressed by its recipe.")
	} else {
		imgui.Text(p.Label() + " paints layers this generator does not have:")
		imgui.Text("  " + strings.Join(stages.MissingLayers(genDoc, p), ", "))
	}
	imgui.Separator()
	imgui.Text("Its recipe adds them, and what reads them:")
	for _, line := range genPaintRecipeSummary(p) {
		imgui.Text("  " + line)
	}
	imgui.Separator()
	if imgui.Button("Add##genpaintrecipeok") {
		genPaintApplyRecipe(p)
		imgui.CloseCurrentPopup()
	}
	imgui.SameLine()
	if imgui.Button("Cancel##genpaintrecipecancel") {
		genPaintPresets.ask = nil
		imgui.CloseCurrentPopup()
	}
}

// genPaintRecipeSummary is what applying p's recipe would change, worked
// out on a copy of the document.
func genPaintRecipeSummary(p *stages.Preset) []string {
	notes, err := stages.ApplyRecipe(genDoc.Clone(), p)
	if err != nil {
		notes = append(notes, "error: "+err.Error())
	}
	if len(notes) == 0 {
		notes = []string{"(nothing)"}
	}
	return notes
}

// genPaintApplyRecipe applies p's recipe as one document undo step and
// activates p.
func genPaintApplyRecipe(p *stages.Preset) {
	GeneratorPushUndo()
	notes, err := stages.ApplyRecipe(genDoc, p)
	if err != nil {
		generatorUndoStack.pop()
		genStatusf("Recipe %s failed: %v", p.Label(), err)
		return
	}
	genPaintPresets.ask = nil
	genAfterEdit()
	genPaintUsePreset(p)
	genStatusf("Recipe %s: %d changes (%s); run the generator to grow it", p.Label(), len(notes), strings.Join(notes, "; "))
}
