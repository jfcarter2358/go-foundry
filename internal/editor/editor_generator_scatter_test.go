package editor

// editor_generator_scatter_test.go — the Generator editor over the
// placement stages: the scatter stage's rule list draws (stage refs inside
// list items, the bands), its preview lands with a dot layer per rule in its
// own colour and an instanced proxy per rule, both views draw it; the 2D
// view's dots stay inside ImGui's 16-bit draw list however many there are.

import (
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/stages"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// scatterEditorDoc is a small terrain, light rubble and a two-rule scatter.
func scatterEditorDoc() *procgen.Doc {
	f, i, e, ref, v2 := procgen.Float, procgen.Int, procgen.Enum, procgen.StageRef, procgen.Vec2
	return &procgen.Doc{Name: "placed", Seed: 5, Stages: []procgen.Stage{
		{Kind: stages.TerrainKind, ID: "ground", Params: procgen.Params{"size": f(64), "resolution": f(1), "tile_size": f(32),
			"preview_cells": i(64), "drainage_iterations": i(5), "droplets": f(0.1), "talus_iterations": i(5)}},
		{Kind: stages.RocksKind, ID: "rubble", Params: procgen.Params{"preset": e("rubble"), "variants": i(2), "preview_triangles": i(800)}},
		{Kind: stages.ScatterKind, ID: "scatter", Params: procgen.Params{"terrain": ref("ground"), "rules": procgen.List(
			procgen.Params{"name": procgen.String("field"), "rocks": ref("rubble"), "density": f(40), "slope": v2(0, 60)},
			procgen.Params{"name": procgen.String("fines"), "rocks": ref("rubble"), "density": f(40), "scale": v2(0.3, 0.5),
				"satellites": f(3), "slope": v2(0, 60)},
		)}},
	}}
}

func TestGeneratorDrawsTheScatterStage(t *testing.T) {
	withGeneratorWindow(t)
	genSetDoc(scatterEditorDoc(), "")
	genForceTreesOpen = true
	t.Cleanup(func() { genForceTreesOpen = false })
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		genSelected = 2
		genViewMode = genView2D
		genFrame(imgui.Vec2{X: -100, Y: -100}, false, false)
		genLoadPreview(t)
		p := genPreview.data
		if p == nil || len(p.Points) != 2 || len(p.Points[0].Points) == 0 || len(p.Points[1].Points) == 0 {
			t.Fatalf("the scatter preview has no dots per rule: %+v", p)
		}
		inst := 0
		for _, m := range p.Meshes {
			inst += len(m.Instances)
		}
		if inst != len(p.Points[0].Points)+len(p.Points[1].Points) {
			t.Errorf("%d instances for %d dots", inst, len(p.Points[0].Points)+len(p.Points[1].Points))
		}
		genFrame(imgui.Vec2{X: 600, Y: 400}, false, false)
		genViewMode = genView3D
		genFrame(imgui.Vec2{X: 600, Y: 400}, false, false)
		drawUIOnlyFrame(func() { genView3DOverlay(230, 54) })
	})
}

func TestGeneratorDotsStayInTheDrawListBudget(t *testing.T) {
	withGeneratorWindow(t)
	genSetDoc(scatterEditorDoc(), "")
	// Sixty thousand dots with footprints: drawn naively, a quarter of a
	// million vertices — past what a 16-bit draw list indexes, which ImGui
	// asserts on.
	pl := procgen.PointLayer{Name: "many", Color: [4]float32{1, 0.5, 0.2, 1}}
	for k := 0; k < 60000; k++ {
		pl.Points = append(pl.Points, [2]float64{float64(k%250) - 125, float64(k/250) - 120})
		pl.Radii = append(pl.Radii, 0.4)
	}
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		genSelected = 2
		genViewMode = genView2D
		genPreview.data = &procgen.PreviewData{Points: []procgen.PointLayer{pl}, Bounds: procgen.Rect{MinX: -130, MinZ: -130, MaxX: 130, MaxZ: 130}}
		genPreview.dataRev, genPreview.dataStage, genPreview.asked = genRev, "scatter", false
		genV2.framed = false
		for k := 0; k < 3; k++ {
			genFrame(imgui.Vec2{X: 600, Y: 400}, false, false)
		}
		// Zoomed in: the circles appear, still within the budget.
		genV2.scale *= 20
		genFrame(imgui.Vec2{X: 600, Y: 400}, false, false)
	})
}
