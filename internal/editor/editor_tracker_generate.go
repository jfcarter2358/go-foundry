package editor

// editor_tracker_generate.go — the sample editor's Generate dialog.
//
// The stock sample library is synthesised by recipes (internal/tracker/synth),
// and this dialog re-runs one with its knobs turned: pick a recipe, drag its
// params, Preview to hear it through the editor's player, and Save to put it
// in the library under its own name or a variation's.
//
// The dialog reaches the recipes through trkSampleGenerator rather than by
// importing the synth package, so the editor builds whether or not that
// package does yet; editor_tracker_synth.go installs the real one. With no
// generator installed the Generate button is disabled.

import (
	"fmt"
	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/tracker"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// trkRecipeParam is one knob of a recipe.
type trkRecipeParam struct {
	Name              string
	Default, Min, Max float32
	Help              string
}

// trkRecipe describes one recipe for the dialog.
type trkRecipe struct {
	Name, Category, Desc string
	// LibraryName is the name songs use for its sample ("drums/kick_808").
	LibraryName string
	Params      []trkRecipeParam
}

// trkSampleGenerator is what the dialog needs from the synth package.
type trkSampleGenerator interface {
	Recipes() []trkRecipe
	Generate(libraryName string, rate int, params map[string]float32) *tracker.Sample
}

// trkGenerator is installed by editor_tracker_synth.go; nil leaves Generate
// disabled.
var trkGenerator trkSampleGenerator

// Dialog state.
var (
	trkShowGenerate bool
	trkGenFilter    string
	trkGenRecipe    = -1
	trkGenParams    map[string]float32
	trkGenSaveName  string
	trkGenRate      = int32(44100)
)

// trkGenerateButton opens the dialog, on the current sample's recipe when it
// has one.
func trkGenerateButton() {
	imguibackend.EditorPushDisabled(trkGenerator == nil)
	if imgui.Button("Generate...##trkgen") {
		trkShowGenerate = true
		trkGenRecipe = -1
		if _, src := trkSampleSource(); src != nil {
			trkGenSelectByName(src.Name)
		}
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHoveredV(imgui.HoveredFlagsAllowWhenDisabled) {
		imgui.SetTooltip("Synthesise a sample from a recipe with its knobs turned")
	}
}

func trkGenSelectByName(name string) {
	for i, r := range trkGenerator.Recipes() {
		if r.LibraryName == name || r.Name == name {
			trkGenSelect(i)
			return
		}
	}
}

func trkGenSelect(i int) {
	recipes := trkGenerator.Recipes()
	if i < 0 || i >= len(recipes) {
		return
	}
	trkGenRecipe = i
	trkGenParams = map[string]float32{}
	for _, p := range recipes[i].Params {
		trkGenParams[p.Name] = p.Default
	}
	trkGenSaveName = recipes[i].LibraryName
}

func trkGenerateDialog() {
	if trkGenerator == nil {
		trkShowGenerate = false
		return
	}
	imgui.SetNextWindowSizeV(imgui.Vec2{X: 720, Y: 480}, imgui.ConditionAppearing)
	if trkShowGenerate {
		imgui.OpenPopup("Generate Sample##trkgendlg")
		trkShowGenerate = false
	}
	if !imgui.BeginPopupModalV("Generate Sample##trkgendlg", nil, 0) {
		return
	}
	recipes := trkGenerator.Recipes()
	// Left: the recipes, filterable, by category.
	imgui.BeginChildV("##trkgenlist", imgui.Vec2{X: 250, Y: -imgui.FrameHeightWithSpacing()}, true, 0)
	imgui.SetNextItemWidth(-1)
	imgui.InputTextWithHintV("##trkgenfilter", "Type to filter...", &trkGenFilter, 0, nil)
	cat := ""
	for i, r := range recipes {
		if !imguibackend.ComboFilterMatches(trkGenFilter, r.Category+" "+r.Name+" "+r.Desc) {
			continue
		}
		if r.Category != cat {
			cat = r.Category
			editorTextMuted(cat)
		}
		// imguibackend's Selectable: a plain imgui.Selectable in a modal
		// dismisses the modal.
		if imguibackend.SelectableV(r.Name+"##trkgen"+itoa(i), i == trkGenRecipe, imgui.SelectableFlagsDontClosePopups, imgui.Vec2{}) {
			trkGenSelect(i)
		}
		if imgui.IsItemHovered() && r.Desc != "" {
			imgui.SetTooltip(r.Desc)
		}
	}
	imgui.EndChild()
	imgui.SameLine()

	// Right: the knobs.
	imgui.BeginChildV("##trkgenknobs", imgui.Vec2{X: 0, Y: -imgui.FrameHeightWithSpacing()}, false, 0)
	if trkGenRecipe >= 0 && trkGenRecipe < len(recipes) {
		r := recipes[trkGenRecipe]
		imgui.Text(r.LibraryName)
		editorTextMutedWrapped(r.Desc)
		imgui.Separator()
		imgui.PushItemWidth(-140)
		for _, p := range r.Params {
			v := trkGenParams[p.Name]
			if imgui.SliderFloatV(p.Name+"##trkgenp", &v, p.Min, p.Max, "%.3f", 0) {
				trkGenParams[p.Name] = v
			}
			if p.Help != "" && imgui.IsItemHovered() {
				imgui.SetTooltip(p.Help)
			}
		}
		imgui.Separator()
		imgui.SliderIntV("Rate##trkgenrate", &trkGenRate, 22050, 48000, "%d Hz", 0)
		imgui.InputTextV("Save as##trkgenname", &trkGenSaveName, 0, nil)
		imgui.PopItemWidth()
		if imgui.Button("Defaults##trkgendef") {
			trkGenSelect(trkGenRecipe)
		}
	}
	imgui.EndChild()

	ok := trkGenRecipe >= 0 && trkGenRecipe < len(recipes)
	imguibackend.EditorPushDisabled(!ok)
	if imgui.Button("Preview##trkgenprev") && ok {
		if s := trkGenerator.Generate(recipes[trkGenRecipe].LibraryName, int(trkGenRate), trkGenParams); s != nil {
			trkPreviewPlay(s, float32(s.Frames())/float32(max(1, s.Rate))+0.2)
		}
	}
	imgui.SameLine()
	if imgui.Button("Save to library##trkgensave") && ok {
		trkGenSave(recipes[trkGenRecipe])
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	if imgui.Button("Close##trkgenclose") {
		imgui.CloseCurrentPopup()
	}
	imgui.EndPopup()
}

// trkGenSave renders the recipe, writes it to the library under the chosen
// name, puts it in the cache and — when the current instrument has no sample
// yet — gives it to that instrument.
func trkGenSave(r trkRecipe) {
	name := strings.Trim(strings.TrimSpace(trkGenSaveName), "/")
	if name == "" {
		name = r.LibraryName
	}
	s := trkGenerator.Generate(r.LibraryName, int(trkGenRate), trkGenParams)
	if s == nil {
		trkEd.Status = "The recipe made nothing"
		return
	}
	s.Name = name
	if err := tracker.SaveSample(s); err != nil {
		trkEd.Status = "Save failed: " + err.Error()
		return
	}
	tracker.PutSample(s)
	trkSampleNames = nil
	trkSampleWork = nil
	if in := trkInstrument(); in != nil {
		if len(in.Samples) == 0 || in.Samples[0] == "" {
			trkEd.pushUndo("")
			if len(in.Samples) == 0 {
				in.Samples = []string{name}
			} else {
				in.Samples[0] = name
			}
			trkEd.touch()
		}
		in.Resolve(tracker.SampleFor)
	}
	trkSyncPlayer(true)
	trkEd.Status = fmt.Sprintf("Saved %s to the library", name)
}
