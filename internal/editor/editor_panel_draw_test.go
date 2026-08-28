package editor

// Drawing the side panel, for real, with no window and no GL.
//
// The panel is nearly all imgui stack discipline: every group opened has to be
// closed, on every path, including the early returns a body takes when the
// thing it is drawing has nothing to say. imgui catches an imbalance with an
// assert inside End() — which aborts the process — so a test that draws the
// panel once per kind of selection is what stands between a mistyped group and
// a crash the moment somebody clicks the object it belongs to.
//
// It runs the real editorPanelBody against a real level, so it also covers the
// blocks reached only through a selection: with nothing selected the panel is a
// fraction of itself, and the sector's block is the biggest part of it.

import (
	"foundry/internal/world"
	"testing"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// drawPanelOnce runs one full imgui frame with the panel in it.
//
// focus is the PanelFocus a fresh selection would have left behind, which is
// what force-opens the groups belonging to it (editorFocusOpen) — so the bodies
// inside them are drawn rather than skipped as a shut group.
func drawPanelOnce(lvl *world.Level, kind editorPanelKind, focus string) {
	io := imgui.CurrentIO()
	io.SetDisplaySize(imgui.Vec2{X: 1280, Y: 800})
	io.SetDeltaTime(1.0 / 60.0)
	imgui.NewFrame()

	Editor.PanelFocus = focus
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: 0})
	imgui.SetNextWindowSize(imgui.Vec2{X: 360, Y: 800})
	imgui.Begin("panel")
	editorPanelBody(lvl, kind)
	imgui.End()

	imgui.Render()
}

// withImgui runs body inside a fresh imgui context. The font atlas has to be
// built or Render() refuses to run; nothing else about the backend is needed,
// since the panel only ever asks imgui to lay widgets out.
func withImgui(body func()) {
	ctx := imgui.CreateContext(nil)
	defer ctx.Destroy()
	imgui.CurrentIO().Fonts().TextureDataAlpha8()
	body()
}

// panelTestLevel is a level with one of everything the panel can be pointed at.
func panelTestLevel(t *testing.T) *world.Level {
	t.Helper()
	return selectTestLevel(t)
}

// Every selection, in both editors: the panel draws and imgui's stacks come out
// even. A group left unclosed aborts inside End(); one closed twice aborts
// inside TreePop.
func TestPanelDrawsForEverySelection(t *testing.T) {
	defer saveEditorSelection()()

	selections := []struct {
		name  string
		focus string
		do    func()
	}{
		{"nothing", panelFocusNone, editorClearObjectSelections},
		{"spawn", panelFocusSpawn, editorSelectSpawn},
		{"chunk", panelFocusChunk, func() { editorSelectChunk(0) }},
		{"sector", panelFocusSector, func() { editorSelectSector(0, 0) }},
		{"floor", panelFocusFloor, func() { editorSelectSector(0, 0) }},
		{"ceiling", panelFocusCeiling, func() { editorSelectSector(0, 0) }},
		{"wall", panelFocusWall, func() {
			editorSelectSector(0, 0)
			Editor.ActiveEdgeKind = "wall"
			Editor.ActiveEdgeIdx = 0
		}},
		{"portal", panelFocusWall, func() {
			editorSelectSector(0, 0)
			Editor.ActiveEdgeKind = "portal"
			Editor.ActiveEdgeIdx = 0
		}},
		{"biome sector", panelFocusBiome, func() { editorSelectBiomeSector(0, 0) }},
		{"entity", panelFocusEntity, func() { editorSelectEntity(0) }},
		{"volume", panelFocusVolume, func() { editorSelectVolume(0) }},
		{"global light", panelFocusLight, func() { editorSelectLight(lightKindGlobal, 0) }},
		{"point light", panelFocusLight, func() { editorSelectLight(lightKindPoint, 0) }},
	}

	for _, mode := range []struct {
		name string
		kind editorPanelKind
	}{{"3D", panelKind3D}, {"2D", panelKind2D}} {
		for _, sel := range selections {
			t.Run(mode.name+"/"+sel.name, func(t *testing.T) {
				lvl := panelTestLevel(t)
				sel.do()
				withImgui(func() {
					// Twice: the first frame creates the window and every
					// group's storage, the second draws with them already
					// there, which is the path a real session spends its life
					// on.
					drawPanelOnce(lvl, mode.kind, sel.focus)
					drawPanelOnce(lvl, mode.kind, sel.focus)
				})
			})
		}
	}
}

// A directional light, separate from the table above because it needs one
// adding to the level first.
func TestPanelDrawsForADirectionalLight(t *testing.T) {
	defer saveEditorSelection()()
	lvl := panelTestLevel(t)
	lvl.Lights.Directional = []world.DirectionalLight{{Brightness: 1, CastShadows: true}}
	editorSelectLight(lightKindDir, 0)

	withImgui(func() {
		drawPanelOnce(lvl, panelKind3D, panelFocusLight)
		drawPanelOnce(lvl, panelKind3D, panelFocusLight)
	})
}

// The Explorer's tree is drawn from the level, so a level holding nothing has
// to draw as cleanly as a full one: every category still renders, and the rows
// that would have carried a context menu are simply absent.
func TestPanelDrawsForAnEmptyLevel(t *testing.T) {
	defer saveEditorSelection()()
	lvl := &world.Level{}
	Editor.level = lvl
	Editor.ActiveChunkIdx = 0
	editorClearObjectSelections()

	withImgui(func() {
		drawPanelOnce(lvl, panelKind3D, panelFocusNone)
		drawPanelOnce(lvl, panelKind2D, panelFocusNone)
	})
}
