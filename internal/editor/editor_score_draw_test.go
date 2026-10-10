package editor

import (
	"testing"

	"foundry/internal/score/edit"
	"foundry/internal/score/engrave"
	"foundry/internal/score/glyph"
	"foundry/internal/score/scoredraw"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// scoreDrawTest sets the Score editor up to draw with no GL and no window:
// a renderer whose textures are pretend, and a window record the overlay
// reads its size from.
func scoreDrawTest(t *testing.T) {
	t.Helper()
	prevEd, prevRend, prevWin, prevKeys := scEd, scoreRend, scoreEditorWindow, scoreKeys
	t.Cleanup(func() {
		scEd, scoreRend, scoreEditorWindow, scoreKeys = prevEd, prevRend, prevWin, prevKeys
		scoreShowOpen, scoreShowSaveAs, scoreShowKey, scoreShowTime, scoreShowClef, scoreShowKeys = false, false, false, false, false, false
		scoreList = nil
	})
	scEd = scoreTestState(t)
	scoreKeys = scoreSibeliusKeymap()
	r := scoredraw.New()
	r.Upload = func(*glyph.Atlas, uint32) uint32 { return 1 }
	r.Delete = func(uint32) {}
	r.FontSize = func() float32 { return 13 }
	scoreRend = r
	scoreEditorWindow = &uiwin.UIWindow{W: 1400, H: 900}
	scoreShowKeypad = true
}

// scoreFrame draws one frame of the whole window at a mouse position, the
// left button down or up.
func scoreFrame(mouse imgui.Vec2, down bool) {
	io := imgui.CurrentIO()
	io.SetDisplaySize(imgui.Vec2{X: 1400, Y: 900})
	io.SetDeltaTime(1.0 / 60.0)
	io.SetMousePosition(mouse)
	io.SetMouseButtonDown(0, down)
	imgui.NewFrame()
	scoreToolbar(1400)
	scoreViewPane(imgui.Vec2{X: 0, Y: editorTopBarH}, imgui.Vec2{X: 1400, Y: 900 - editorTopBarH - scoreStatusH})
	scoreStatusBar(imgui.Vec2{X: 0, Y: 900 - scoreStatusH}, 1400)
	scoreKeypadPanel(1400, 900)
	scoreDialogs()
	if uiwin.KeybindHelpVisible {
		scoreHelpOverlay(true)
	}
	imgui.Render()
}

// Every part of the window draws with ImGui's stacks even — in select mode,
// in note input with the ghost under the pointer, with a selection of bars
// and of a note, with each dialog open and with the F1 overlay up.
func TestScoreWindowDraws(t *testing.T) {
	scoreDrawTest(t)
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		away := imgui.Vec2{X: 5, Y: 5}
		scoreFrame(away, false)
		// Note input, the pointer on the first staff: the ghost and its label.
		scEd.Input = true
		l := scoreLayout()
		sb := l.Staves[0]
		bb := l.Bar(2)
		on := imgui.Vec2{X: scoreHeaderW + scorePad + float32(bb.XAt(0))*scoreZoom, Y: editorTopBarH + scoreRulerH + scorePad + float32(sb.Top+1)*scoreZoom}
		scoreFrame(on, false)
		scEd.Armed = true
		scoreFrame(on, false)
		scEd.Armed = false
		scEd.Input = false
		scEd.selectAll()
		scoreFrame(away, false)
		for _, show := range []*bool{&scoreShowOpen, &scoreShowSaveAs, &scoreShowKey, &scoreShowTime, &scoreShowClef, &scoreShowKeys} {
			*show = true
			scoreFrame(away, false)
			scoreFrame(away, false)
			imgui.CloseCurrentPopup()
		}
		uiwin.KeybindHelpVisible = true
		scoreFrame(away, false)
		uiwin.KeybindHelpVisible = false
		scEd.Layout2 = true
		scoreFrame(away, false)
	})
}

// A click on the paper in note input writes the note the line means, at the
// beat under the pointer — through the real pane, its InvisibleButton and
// the layout's geometry.
func TestScoreClickThroughTheView(t *testing.T) {
	scoreDrawTest(t)
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		scoreShowKeypad = false
		scEd.Input = true
		l := scoreLayout()
		sb := l.Staves[0]
		bb := l.Bar(1)
		// The top line (F5, F#5 in G) at the bar's start.
		pt := engrave.Point{X: bb.XAt(0) + 0.5, Y: sb.Top}
		at := imgui.Vec2{X: scoreHeaderW + scorePad + float32(pt.X)*scoreZoom, Y: editorTopBarH + scoreRulerH + scorePad + float32(pt.Y)*scoreZoom}
		scoreFrame(at, false)
		scoreFrame(at, true)
		scoreFrame(at, false)
		if got := barOf(scEd, 0, 0, 1); got != "qF#5 qr hr" {
			t.Errorf("clicked: %q (status %q)", got, scEd.Status)
		}
		// Out of note input a click on the note selects it.
		scEd.Input = false
		scEd.Sel = scoreSel{}
		l = scoreLayout()
		head := itemCentre(t, l, engrave.RoleNotehead, edit.Loc{Bar: 1, Event: 0}, -1)
		at = imgui.Vec2{X: scoreHeaderW + scorePad + float32(head.X)*scoreZoom, Y: editorTopBarH + scoreRulerH + scorePad + float32(head.Y)*scoreZoom}
		scoreFrame(at, false)
		scoreFrame(at, true)
		scoreFrame(at, false)
		if scEd.Sel.Kind != scoreSelEvent || scEd.Sel.Loc.Event != 0 {
			t.Errorf("selection after a click on the note: %+v", scEd.Sel)
		}
		// A click in the empty bar 3 of the cello selects that bar.
		b3 := l.Bar(3)
		cs := l.Staves[1]
		at = imgui.Vec2{X: scoreHeaderW + scorePad + float32((b3.X0+b3.X1)/2)*scoreZoom, Y: editorTopBarH + scoreRulerH + scorePad + float32(cs.Top+0.5)*scoreZoom}
		scoreFrame(at, false)
		scoreFrame(at, true)
		scoreFrame(at, false)
		if q := scEd.Sel; q.Kind != scoreSelBars || q.LineA != 1 || q.BarA != 3 {
			t.Errorf("bar click: %+v", q)
		}
	})
}
