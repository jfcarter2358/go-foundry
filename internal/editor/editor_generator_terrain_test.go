package editor

// editor_generator_terrain_test.go — the Generator editor over the terrain
// stage: its whole parameter panel (strata list, canyon list with a spline
// and a curve in each item) draws, the preview's relief and mask layers load
// as layer textures, a mask overlays the relief, and a canyon path in a list
// item is editable in the top-down view.

import (
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/stages"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

func terrainEditorDoc() *procgen.Doc {
	return &procgen.Doc{Name: "canyon", Seed: 2, Stages: []procgen.Stage{{Kind: stages.TerrainKind, ID: "ground", Params: procgen.Params{
		"size": procgen.Float(256), "resolution": procgen.Float(2), "tile_size": procgen.Float(128),
		"preview_cells": procgen.Int(48), "drainage_iterations": procgen.Int(5), "talus_iterations": procgen.Int(4),
		"canyons": procgen.List(procgen.Params{
			"path":    procgen.Spline([]float64{-100, -40, 40, 30}, []float64{0, 0, 50, 35}, []float64{100, 40, 60, 40}),
			"profile": procgen.Enum("custom"),
		}),
	}}}}
}

func TestGeneratorDrawsTheTerrainStage(t *testing.T) {
	withGeneratorWindow(t)
	genSetDoc(terrainEditorDoc(), "")
	genForceTreesOpen = true
	t.Cleanup(func() { genForceTreesOpen = false })
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		genSelected = 0
		genViewMode = genView2D
		genFrame(imgui.Vec2{X: -100, Y: -100}, false, false)
		genLoadPreview(t)
		names := map[string]int{}
		for i, im := range genPreview.data.Images {
			names[im.Name] = i
		}
		for _, want := range []string{"Relief", "Height", "Albedo", "Flow", "Slope", "Deposition", "Cliff", "Strata", "Curvature"} {
			if _, ok := names[want]; !ok {
				t.Errorf("the preview has no %s layer", want)
			}
		}
		// The relief under a mask at half opacity.
		genV2.base, genV2.overlay, genV2.alpha = names["Relief"], names["Flow"], 0.5
		genFrame(imgui.Vec2{X: 600, Y: 400}, false, false)
		if len(genLayerTex.ids) != len(genPreview.data.Images) {
			t.Errorf("%d layer textures for %d images", len(genLayerTex.ids), len(genPreview.data.Images))
		}
		// The canyon's path, in the first canyons item, is one of the
		// stage's splines and can be made the active one.
		found := false
		for _, sp := range genStageSplines() {
			if sp.def.Name == "path" && len(sp.path) == 2 && sp.path[0].Name == "canyons" && sp.path[0].Item == 0 {
				found = true
				genSpline = genSplineRef{stage: sp.stage, path: sp.path}
			}
		}
		if !found {
			t.Fatal("the canyon path in the canyons list is not an editable spline")
		}
		genFrame(imgui.Vec2{X: 600, Y: 400}, false, false)
		drawUIOnlyFrame(func() { genView3DOverlay(230, 54) })
	})
	genSpline = genSplineRef{}
}
