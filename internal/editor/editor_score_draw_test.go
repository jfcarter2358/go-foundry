package editor

import (
	"image"
	"testing"

	"foundry/internal/score"
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
	r.UploadText = func(*image.RGBA, uint32) uint32 { return 2 }
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
	scoreDrawFrame(1400, 900)
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
		on := imgui.Vec2{X: scoreHeaderW + scorePad + float32(bb.XAt(0))*scoreZoom, Y: scoreViewTop + scoreRulerH + scorePad + float32(sb.Top+1)*scoreZoom}
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
		at := imgui.Vec2{X: scoreHeaderW + scorePad + float32(pt.X)*scoreZoom, Y: scoreViewTop + scoreRulerH + scorePad + float32(pt.Y)*scoreZoom}
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
		at = imgui.Vec2{X: scoreHeaderW + scorePad + float32(head.X)*scoreZoom, Y: scoreViewTop + scoreRulerH + scorePad + float32(head.Y)*scoreZoom}
		scoreFrame(at, false)
		scoreFrame(at, true)
		scoreFrame(at, false)
		if scEd.Sel.Kind != scoreSelEvent || scEd.Sel.Loc.Event != 0 {
			t.Errorf("selection after a click on the note: %+v", scEd.Sel)
		}
		// A click on the rest after it moves the play head to its onset
		// (SPEC: "click a note → playhead").
		scEd.takeOps()
		rest := itemCentre(t, l, engrave.RoleRest, edit.Loc{Bar: 1, Event: 1}, -1)
		at = imgui.Vec2{X: scoreHeaderW + scorePad + float32(rest.X)*scoreZoom, Y: scoreViewTop + scoreRulerH + scorePad + float32(rest.Y)*scoreZoom}
		scoreClickAt(at)
		if want := int64(score.TicksPerWhole / 4); scEd.Sel.Kind != scoreSelEvent || scEd.Sel.Loc.Event != 1 || scEd.Transport.Head != want {
			t.Errorf("a click on the rest: selection %+v, play head %d, want %d", scEd.Sel, scEd.Transport.Head, want)
		}
		if ops := scEd.takeOps(); len(ops) != 1 || ops[0].Kind != scoreOpSeek || ops[0].Tick != int64(score.TicksPerWhole/4) {
			t.Errorf("the click's seek: %+v", ops)
		}
		// A click in the empty bar 3 of the cello selects that bar.
		b3 := l.Bar(3)
		cs := l.Staves[1]
		at = imgui.Vec2{X: scoreHeaderW + scorePad + float32((b3.X0+b3.X1)/2)*scoreZoom, Y: scoreViewTop + scoreRulerH + scorePad + float32(cs.Top+0.5)*scoreZoom}
		scoreFrame(at, false)
		scoreFrame(at, true)
		scoreFrame(at, false)
		if q := scEd.Sel; q.Kind != scoreSelBars || q.LineA != 1 || q.BarA != 3 {
			t.Errorf("bar click: %+v", q)
		}
	})
}

// scoreAt is a layout point on screen, the view not scrolled.
func scoreAt(p engrave.Point) imgui.Vec2 {
	return imgui.Vec2{X: scoreHeaderW + scorePad + float32(p.X)*scoreZoom, Y: scoreViewTop + scoreRulerH + scorePad + float32(p.Y)*scoreZoom}
}

// scoreClickAt is a press and a release at a point, a frame each.
func scoreClickAt(at imgui.Vec2) {
	scoreFrame(at, false)
	scoreFrame(at, true)
	scoreFrame(at, false)
}

// The staff headers, the ruler, the transport row and the mixer take
// their clicks and drags through the real panes.
func TestScoreHeadersRulerAndMixerThroughTheView(t *testing.T) {
	scoreDrawTest(t)
	prevView := scoreView
	t.Cleanup(func() { scoreView, scoreRenameLine = prevView, -1 })
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		scoreShowKeypad = false
		l := scoreLayout()
		lo, hi, _ := scoreLineBand(l, 0)
		mid := scoreAt(engrave.Point{Y: (lo + hi) / 2}).Y
		// M on the flute's header.
		scoreClickAt(imgui.Vec2{X: 20, Y: mid + 21})
		if !scEd.Score.Lines[0].Mute {
			t.Errorf("M did not mute the flute (%s)", scEd.Status)
		}
		// S on the cello's.
		lo1, hi1, _ := scoreLineBand(l, 1)
		mid1 := scoreAt(engrave.Point{Y: (lo1 + hi1) / 2}).Y
		scoreClickAt(imgui.Vec2{X: 43, Y: mid1 + 21})
		if !scEd.Score.Lines[1].Solo {
			t.Errorf("S did not solo the cello (%s)", scEd.Status)
		}
		// A click on the name selects the line; a double-click renames it.
		// (ImGui counts a click within its double-click time of the last
		// as that click's second, so the clock moves on first.)
		name := imgui.Vec2{X: 30, Y: mid - 22}
		for i := 0; i < 30; i++ {
			scoreFrame(name, false)
		}
		scoreClickAt(name)
		if q := scEd.Sel; q.Kind != scoreSelBars || q.LineA != 0 || q.BarB != scEd.Score.Measures() {
			t.Errorf("name click: %+v", q)
		}
		scoreFrame(name, true)
		scoreFrame(name, false)
		if scoreRenameLine != 0 {
			t.Errorf("double-click did not start a rename (%d)", scoreRenameLine)
		}
		scoreRenameLine = -1
		// The chip opens the picker.
		scorePickLine = -1
		scoreClickAt(imgui.Vec2{X: 60, Y: mid})
		scoreFrame(imgui.Vec2{X: 5, Y: 5}, false)
		if scorePickLine != 0 || scorePickAdd {
			t.Errorf("picker for line %d (add %v)", scorePickLine, scorePickAdd)
		}
		// A click away closes the picker; the duplicate button opens its menu.
		away := imgui.Vec2{X: 700, Y: 895}
		scoreClickAt(away)
		scoreMenuLine = -1
		scoreClickAt(imgui.Vec2{X: 67, Y: mid + 21})
		if scoreMenuLine != 0 {
			t.Errorf("duplicate menu for line %d", scoreMenuLine)
		}
		scoreClickAt(away)
		scEd.takeOps()

		// The flute's name dragged below the cello's header: the flute is
		// the second line now.
		for i := 0; i < 30; i++ {
			scoreFrame(name, false)
		}
		below := imgui.Vec2{X: 30, Y: mid1 + 40}
		scoreFrame(name, true)
		scoreFrame(imgui.Vec2{X: 30, Y: mid + 10}, true)
		scoreFrame(below, true)
		scoreFrame(below, false)
		if scEd.Score.Lines[1].Name != "Flute" || scoreHeaderDrag != -1 {
			t.Errorf("drag: lines %s, %s (drag %d, %s)", scEd.Score.Lines[0].Name, scEd.Score.Lines[1].Name, scoreHeaderDrag, scEd.Status)
		}
		scoreUndo()
		scoreList = nil
		l = scoreLayout()

		// The ruler: a click on bar 3's barline moves the play head there.
		rulerY := scoreViewTop + 20
		b3 := l.Bar(3)
		scoreClickAt(imgui.Vec2{X: scoreAt(engrave.Point{X: b3.X0 + 0.3}).X, Y: rulerY})
		if want := 2 * int64(score.TicksPerWhole); scEd.Transport.Head != want {
			t.Errorf("ruler click: head %d, want %d (%s)", scEd.Transport.Head, want, scEd.Status)
		}
		// A drag from bar 2's start to bar 3's middle loops it, snapped.
		b2 := l.Bar(2)
		a := imgui.Vec2{X: scoreAt(engrave.Point{X: b2.X0 + 0.2}).X, Y: rulerY}
		z := imgui.Vec2{X: scoreAt(engrave.Point{X: b3.XAt(0.5)}).X, Y: rulerY}
		scoreFrame(a, false)
		scoreFrame(a, true)
		scoreFrame(z, true)
		scoreFrame(z, false)
		lp := scEd.Transport.Loop
		if !lp.On || lp.A != (score.Pos{Bar: 2}) || lp.B.Bar != 3 || !lp.B.Offset.Eq(score.F(1, 2)) {
			t.Errorf("ruler drag: %+v (%s)", lp, scEd.Status)
		}
		ops := scEd.takeOps()
		if len(ops) == 0 || ops[len(ops)-1].Kind != scoreOpLoop {
			t.Errorf("the loop was not sent: %+v", ops)
		}

		// The Mixer tab, and the flute's fader dragged down.
		scoreView = scoreViewMixer
		scoreFrame(imgui.Vec2{X: 5, Y: 5}, false)
		before := scEd.Score.Lines[0].Volume
		cap := imgui.Vec2{X: 10 + 30 + 13, Y: 0}
		// The fader runs from y0 to y1; find the cap at its value.
		top := scoreViewTop + 10
		stripH := min(max(900-scoreViewTop-scoreStatusH-20-scoreScrollW, 280), 440)
		y0 := top + 42 + 22 + 4 + 56 + 7 // title, the "+ Insert" slot, the knobs
		y1 := top + stripH - 50 - 7
		cap.Y = y1 - (y1-y0)*before/scoreFaderMax
		scoreFrame(cap, false)
		scoreFrame(cap, true)
		scoreFrame(imgui.Vec2{X: cap.X, Y: cap.Y + 40}, true)
		scoreFrame(imgui.Vec2{X: cap.X, Y: cap.Y + 40}, false)
		if after := scEd.Score.Lines[0].Volume; after >= before {
			t.Errorf("fader drag: %v -> %v", before, after)
		}
		if scEd.mixKey != "" {
			t.Error("the drag's end did not close the undo step")
		}
		// Every popup and the export dialog draw.
		for _, open := range []func(){
			func() { scoreOpenPicker(1, imgui.Vec2{X: 100, Y: 100}) },
			func() { scoreOpenAddLine(0) },
			func() { scoreDupOpen, scoreMenuLine = true, 0 },
			func() { scoreLineOpen, scoreMenuLine = true, 1 },
			func() { scoreOpenChordPicker(0, imgui.Vec2{X: 100, Y: 100}) },
			scoreOpenExport,
		} {
			open()
			scoreFrame(imgui.Vec2{X: 5, Y: 5}, false)
			scoreFrame(imgui.Vec2{X: 5, Y: 5}, false)
			imgui.CloseCurrentPopup()
			scoreShowExport = false
		}
		scoreView = scoreViewScore
		scoreFrame(imgui.Vec2{X: 5, Y: 5}, false)
	})
}

// The lyric and chord symbol fields draw at their places, in the
// continuous view and on the pages, and take what is typed.
func TestScoreWordFieldsDraw(t *testing.T) {
	scoreDrawTest(t)
	v, _ := score.ParseVoice("| qD5 qE5 qF#5 qG5 | wA5 |")
	scEd.Score.Lines[0].Staves[0].Voices[0] = v
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		away := imgui.Vec2{X: 5, Y: 5}
		scEd.selectEvent(edit.Loc{Bar: 1}, -1)
		scEd.startLyrics(1)
		scoreFrame(away, false)
		l := scoreLayout()
		if _, _, ok := scoreWordFieldAt(l, scEd.Words); !ok {
			t.Fatal("the lyric field has no place")
		}
		scoreFrame(away, false)
		scEd.wordsKey(scoreWordEscape)
		scEd.startChord()
		scoreFrame(away, false)
		if _, _, ok := scoreWordFieldAt(l, scEd.Words); !ok {
			t.Fatal("the chord field has no place")
		}
		prev := scorePageView
		scorePageView = true
		scoreFrame(away, false)
		scoreFrame(away, false)
		scorePageView = prev
		// Scrolled away from its place, the field is pinned at the view's
		// foot and keeps the keyboard.
		io := imgui.CurrentIO()
		io.SetDisplaySize(imgui.Vec2{X: 1400, Y: 900})
		io.SetDeltaTime(1.0 / 60.0)
		imgui.NewFrame()
		imgui.Begin("pin")
		far := scoreViewGeom{origin: imgui.Vec2{X: -50000, Y: 0}, zoom: 10, visMin: imgui.Vec2{X: 0, Y: 0}, visMax: imgui.Vec2{X: 800, Y: 600}}
		if scoreDrawWordField(l, far) {
			t.Error("a field whose place is out of sight drawn there")
		}
		scoreDrawPinnedWordField(far)
		imgui.End()
		imgui.Render()
		scEd.wordsKey(scoreWordEscape)
		scoreFrame(away, false)
	})
}
