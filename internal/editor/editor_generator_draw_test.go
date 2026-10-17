package editor

// editor_generator_draw_test.go — the Generator editor's whole window draws,
// for every parameter type, with no GL; and the top-down view's spline
// editing works from simulated mouse input.
//
// The parameter panel is generated from a schema in another package, so its
// imgui stack discipline is not checkable by eye — an unbalanced TreePop or
// PushID aborts the process inside End(). One frame over a document holding
// every type, list items and splines included, with a preview loaded (the
// layer textures stood in for), is what catches that before a user expands
// the wrong node.

import (
	"context"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/stages"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// withGeneratorWindow stands a fake window up (no OS window, no GL), stubs
// the layer texture upload, and puts it all back afterwards.
func withGeneratorWindow(t *testing.T) {
	t.Helper()
	withGeneratorState(t)
	savedWin := generatorEditorWindow
	savedUp, savedDel := genUploadTexture, genDeleteTextures
	next := uint32(100)
	genUploadTexture = func(w, h int, rgba []uint8) uint32 { next++; return next }
	genDeleteTextures = func([]uint32) {}
	generatorEditorWindow = &uiwin.UIWindow{W: 1320, H: 820}
	savedV2 := genV2
	t.Cleanup(func() {
		generatorEditorWindow = savedWin
		genUploadTexture, genDeleteTextures = savedUp, savedDel
		genLayerTex.data, genLayerTex.ids = nil, nil
		genV2 = savedV2
		genPreview.data, genPreview.dataStage, genPreview.err = nil, "", nil
	})
}

// genFrame runs one imgui frame of the whole window with the mouse at pos
// and the left/right buttons as given.
func genFrame(pos imgui.Vec2, left, right bool) {
	io := imgui.CurrentIO()
	io.SetDisplaySize(imgui.Vec2{X: 1320, Y: 820})
	io.SetDeltaTime(1.0 / 60.0)
	io.SetMousePosition(pos)
	io.SetMouseButtonDown(0, left)
	io.SetMouseButtonDown(1, right)
	imgui.NewFrame()
	drawGeneratorEditorWindow()
	imgui.Render()
}

// genLoadPreview computes the selected stage's preview synchronously and
// installs it as if the job had landed.
func genLoadPreview(t *testing.T) {
	t.Helper()
	st := genSelectedStage()
	p, err := procgen.Preview(context.Background(), genDoc, st.ID, procgen.NewPreviewCache(4), procgen.PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	genPreview.data, genPreview.dataRev, genPreview.dataStage, genPreview.err = p, genRev, st.ID, nil
	genPreview.asked = false
}

func TestGeneratorWindowDrawsEveryParamType(t *testing.T) {
	withGeneratorWindow(t)
	// A kind with one of every type, nested list included.
	procgen.RegisterStage(procgen.StageKind{Name: "zdrawall", Category: "Test", Label: "All", Doc: "every type",
		Params: []procgen.ParamDef{
			{Name: "f", Type: procgen.ParamFloat, Default: procgen.Float(0.5), Min: 0, Max: 1, Doc: "d"},
			{Name: "fu", Type: procgen.ParamFloat, Default: procgen.Float(3), Doc: "unbounded"},
			{Name: "i", Type: procgen.ParamInt, Default: procgen.Int(4), Min: 1, Max: 8, Doc: "d"},
			{Name: "b", Type: procgen.ParamBool, Default: procgen.Bool(true), Doc: "d"},
			{Name: "e", Type: procgen.ParamEnum, Default: procgen.Enum("a"), Choices: []string{"a", "b"}, Doc: "d"},
			{Name: "v2", Type: procgen.ParamVec2, Default: procgen.Vec2(1, 2), Doc: "d"},
			{Name: "v3", Type: procgen.ParamVec3, Default: procgen.Vec3(1, 2, 3), Doc: "d"},
			{Name: "c", Type: procgen.ParamColor, Default: procgen.Color(1, 0.5, 0.2), Doc: "d"},
			{Name: "t", Type: procgen.ParamTexture, Default: procgen.Texture(""), Doc: "d"},
			{Name: "s", Type: procgen.ParamString, Default: procgen.String("x"), Doc: "d"},
			{Name: "seed", Type: procgen.ParamSeed, Default: procgen.Seed(3), Doc: "d"},
			{Name: "ref", Type: procgen.ParamStage, Default: procgen.StageRef(""), Doc: "d"},
			{Name: "path", Type: procgen.ParamSpline, Default: procgen.Spline([]float64{0, 0, 5}, []float64{10, 10, 5}),
				Channels: []string{"w"}, Doc: "d"},
			{Name: "curve", Type: procgen.ParamCurve, Default: procgen.Curve([2]float64{0, 0}, [2]float64{1, 1}), Doc: "d"},
			{Name: "rules", Type: procgen.ParamList, Default: procgen.List(procgen.Params{}), Doc: "d", Fields: []procgen.ParamDef{
				{Name: "w", Type: procgen.ParamFloat, Default: procgen.Float(1), Doc: "d"},
				{Name: "edge", Type: procgen.ParamSpline, Default: procgen.Spline([]float64{1, 1}), Spline3D: true, Doc: "d"},
				{Name: "src", Type: procgen.ParamStage, Default: procgen.StageRef(""), Doc: "d"},
			}},
		},
		Run: func(*procgen.RunContext) error { return nil }})
	t.Cleanup(func() { procgen.UnregisterStage("zdrawall") })

	d := &procgen.Doc{Name: "draw", Seed: 1, Level: "arena", Stages: []procgen.Stage{
		{Kind: stages.NoisePreviewKind, ID: "ground", Params: procgen.Params{
			"path": procgen.Spline([]float64{-20, 0, 10, 4}, []float64{20, 10, 10, 4})}},
		{Kind: "zdrawall", ID: "all", Params: procgen.Params{"ref": procgen.StageRef("ground"), "f": procgen.Float(0.25)}},
		{Kind: "from_the_future", ID: "unknown"},
	}}
	genSetDoc(d, "")
	genForceTreesOpen = true
	t.Cleanup(func() { genForceTreesOpen = false })
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		// Every stage selected in turn, in both view modes' UI (the 3D view's
		// GL half is not drawn without a context — only its overlay).
		for sel := range d.Stages {
			genSelected = sel
			for _, mode := range []int{genView2D, genView3D} {
				genViewMode = mode
				if mode == genView3D {
					// The 3D draw needs GL; draw just the UI over it.
					genViewMode = genView2D
					drawUIOnlyFrame(func() { genView3DOverlay(230, 54) })
					continue
				}
				genFrame(imgui.Vec2{X: -100, Y: -100}, false, false)
			}
		}
		// With a preview in, the 2D view draws its layers.
		genSelected = 0
		genLoadPreview(t)
		genFrame(imgui.Vec2{X: 600, Y: 400}, false, false)
		if len(genLayerTex.ids) != len(genPreview.data.Images) {
			t.Errorf("%d layer textures for %d images", len(genLayerTex.ids), len(genPreview.data.Images))
		}
		// The run modal, mid-run.
		genRunJob.running, genRunJob.shown, genRunJob.openPop = true, false, true
		genRunProgress("ground (noise_preview)", 0.4)
		genRunLog("[ground] running")
		genFrame(imgui.Vec2{X: -100, Y: -100}, false, false)
		genFrame(imgui.Vec2{X: -100, Y: -100}, false, false)
		genRunJob.running = false
		genFrame(imgui.Vec2{X: -100, Y: -100}, false, false)
		genRunJob.shown = false
	})
}

// drawUIOnlyFrame runs one frame of body alone.
func drawUIOnlyFrame(body func()) {
	io := imgui.CurrentIO()
	io.SetDisplaySize(imgui.Vec2{X: 1320, Y: 820})
	io.SetDeltaTime(1.0 / 60.0)
	imgui.NewFrame()
	body()
	imgui.Render()
}

// canvasPoint is the screen position of world (x, z) in the 2D view, as the
// canvas laid out last frame.
func canvasPoint(x, z float64) imgui.Vec2 {
	// The canvas starts under the view's bar; its centre is what the view's
	// cx/cz sit at. The bar is one frame-height row plus spacing; measure it
	// rather than guess by drawing a frame and reading the readout is
	// overkill — the view is framed, so world (cx, cz) is the canvas centre.
	ox := generatorListWidth
	oy := generatorToolbarH
	w := float32(1320) - generatorListWidth - generatorPanelWidth
	h := float32(820) - generatorToolbarH - generatorFooterH
	pad := imgui.CurrentStyle().WindowPadding()
	bar := imgui.FrameHeight() + imgui.CurrentStyle().ItemSpacing().Y
	cx := ox + pad.X + (w-2*pad.X)/2
	cy := oy + pad.Y + bar + (h-2*pad.Y-bar)/2
	return imgui.Vec2{X: cx + float32((x-genV2.cx)*genV2.scale), Y: cy + float32((z-genV2.cz)*genV2.scale)}
}

func TestGeneratorSplineEditingInTheTopDownView(t *testing.T) {
	withGeneratorWindow(t)
	d := &procgen.Doc{Name: "spl", Seed: 1, Stages: []procgen.Stage{
		{Kind: stages.NoisePreviewKind, ID: "ground", Params: procgen.Params{
			"path": procgen.Spline([]float64{-50, 0, 12, 6}, []float64{50, 0, 12, 6})}},
	}}
	genSetDoc(d, "")
	path := genParamPath{{Name: "path", Item: -1}}
	genSpline = genSplineRef{stage: "ground", path: path}
	points := func() [][]float64 { return genDoc.Stages[0].Params.Points("path") }

	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		off := imgui.Vec2{X: -100, Y: -100}
		genFrame(off, false, false) // lay out once
		// Frame on a known window: 128 m centred on the origin.
		genV2.cx, genV2.cz, genV2.framed = 0, 0, true
		genV2.scale = 2
		genFrame(off, false, false)

		click := func(at imgui.Vec2, right bool) {
			genFrame(at, false, false)
			genFrame(at, !right, right)
			genFrame(at, false, false)
			genEndEdits()
		}

		// Click ON the segment between the two points: inserted in between.
		click(canvasPoint(0, 0), false)
		if n := len(points()); n != 3 {
			t.Fatalf("a click on the segment left %d points, want 3", n)
		}
		if p := points()[1]; p[0] < -2 || p[0] > 2 || p[2] != 12 {
			t.Errorf("inserted point %v: want near the origin with the channels averaged", p)
		}
		// Click away from it: appended after the nearer end.
		click(canvasPoint(70, 40), false)
		if n := len(points()); n != 4 {
			t.Fatalf("a click off the path left %d points, want 4", n)
		}
		if p := points()[3]; p[0] < 65 || p[1] < 35 {
			t.Errorf("appended point %v, want near (70, 40)", p)
		}
		// Drag the middle point down 20 m.
		at := canvasPoint(points()[1][0], points()[1][1])
		to := imgui.Vec2{X: at.X, Y: at.Y + 40}
		genFrame(at, false, false)
		genFrame(at, true, false)
		genFrame(imgui.Vec2{X: at.X, Y: at.Y + 20}, true, false)
		genFrame(to, true, false)
		genFrame(to, false, false)
		genEndEdits()
		if z := points()[1][1]; z < 18 || z > 22 {
			t.Errorf("dragged point is at z %v, want about 20", z)
		}
		// Right-click it: deleted.
		click(canvasPoint(points()[1][0], points()[1][1]), true)
		if n := len(points()); n != 3 {
			t.Errorf("a right-click left %d points, want 3", n)
		}
		// Each gesture is one undo step: delete, drag, append, insert.
		for i, want := range []int{4, 4, 3, 2} {
			GeneratorUndo()
			if n := len(points()); n != want {
				t.Errorf("undo %d: %d points, want %d", i+1, n, want)
			}
		}
		if GeneratorCanUndo() {
			t.Error("more undo steps than gestures")
		}
	})
}
