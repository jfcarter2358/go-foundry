package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/engrave"
	"foundry/internal/score/io/pdf"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// scorePageTestState is the test score with music in the flute, eight bars.
func scorePageTestState(t *testing.T) *scoreState {
	t.Helper()
	e := scoreTestState(t)
	e.Score.Bars = 8
	v, err := score.ParseVoice("| qG5 qA5 qB5 qC6 | hD6 hB5 | R | R | wG5 | qA5 qB5 hC6 | R | wG5 |")
	if err != nil {
		t.Fatal(err)
	}
	e.Score.Lines[0].Staves[0].Voices = []*score.Voice{v}
	e.touch()
	return e
}

func TestScoreBreakCommands(t *testing.T) {
	e := scorePageTestState(t)
	prevPart := scorePagePart
	t.Cleanup(func() { scorePagePart = prevPart })
	scorePagePart = -1
	if e.toggleBreak(false) {
		t.Errorf("a break with nothing selected")
	}
	e.Sel = scoreSel{Kind: scoreSelBars, LineA: 0, LineB: 1, BarA: 2, BarB: 3}
	rev, lrev := e.Rev, e.LayoutRev
	if !e.toggleBreak(false) || len(e.Score.Layout.Breaks) != 1 || e.Score.Layout.Breaks[0] != (score.Break{Bar: 4}) {
		t.Fatalf("system break after bar 3: %+v (%s)", e.Score.Layout.Breaks, e.Status)
	}
	if e.Rev != rev || e.LayoutRev == lrev {
		t.Errorf("a break re-performs the score (rev %d→%d) or does not lay out again", rev, e.Rev)
	}
	if !e.toggleBreak(true) || !e.Score.Layout.Breaks[0].Page {
		t.Errorf("page break where a system break is: %+v", e.Score.Layout.Breaks)
	}
	if !e.toggleBreak(true) || len(e.Score.Layout.Breaks) != 0 {
		t.Errorf("a page break again is none: %+v", e.Score.Layout.Breaks)
	}
	// Each step undoes.
	e.undoStep()
	if len(e.Score.Layout.Breaks) != 1 || !e.Score.Layout.Breaks[0].Page {
		t.Errorf("undo: %+v", e.Score.Layout.Breaks)
	}
	e.undoStep()
	if len(e.Score.Layout.Breaks) != 1 || e.Score.Layout.Breaks[0].Page {
		t.Errorf("undo twice: %+v", e.Score.Layout.Breaks)
	}
	// Return on selected bars is a system break (Sibelius); on a note it
	// respells.
	scEdPrev := scEd
	scEd = e
	t.Cleanup(func() { scEd = scEdPrev })
	e.Sel = scoreSel{Kind: scoreSelBars, LineA: 0, LineB: 0, BarA: 5, BarB: 5}
	scoreRunCmd(scRespell)
	if _, ok := score.BreakIn(e.Score.Layout.Breaks, 6); !ok {
		t.Errorf("Return on bar 5 made no break: %+v", e.Score.Layout.Breaks)
	}
	// The last bar has nothing after it.
	e.Sel = scoreSel{Kind: scoreSelBars, BarA: 8, BarB: 8}
	if e.toggleBreak(false) {
		t.Errorf("a break after the last bar")
	}
	// Showing a part, breaks are the part's.
	scorePagePart = 0
	e.Sel = scoreSel{Kind: scoreSelEvent, Loc: edit.Loc{Line: 0, Bar: 1, Event: 0}}
	before := len(e.Score.Layout.Breaks)
	if !e.toggleBreak(false) || len(e.Score.Layout.Breaks) != before {
		t.Fatalf("part break: %v", e.Status)
	}
	if p := e.Score.Layout.Part("Flute"); p == nil || len(p.Breaks) != 1 || p.Breaks[0].Bar != 2 || !strings.Contains(e.Status, "Flute part") {
		t.Errorf("the flute part's breaks: %+v (%s)", p, e.Status)
	}
	// The page view's book follows.
	scEd = e
	b := scorePageBook()
	if _, sys := b.Find(2); sys == nil || sys.From != 2 {
		t.Errorf("the part's break is not in the page view")
	}
	if b.Part != "Flute" {
		t.Errorf("the page view shows %q", b.Part)
	}
}

func TestScorePageSetupEdits(t *testing.T) {
	e := scorePageTestState(t)
	prevEd := scEd
	scEd = e
	t.Cleanup(func() { scEd = prevEd })
	b0 := scorePageBook()
	e.setLayout("staff", func(l *score.Layout) { l.Staff = 5 })
	e.setLayout("staff", func(l *score.Layout) { l.Staff = 4.5 })
	e.endLayoutGesture()
	if b := scorePageBook(); b == b0 || b.Space != 4.5/4 {
		t.Errorf("the staff size did not lay the pages out again: %g", b.Space)
	}
	// One drag, one step.
	e.undoStep()
	if e.Score.Layout.Staff != 0 {
		t.Errorf("one undo after a drag: staff %g", e.Score.Layout.Staff)
	}
	e.setText("title", "Hangar")
	e.setText("title", "Hangar Theme")
	e.endLayoutGesture()
	e.setText("subtitle", "for test")
	e.setShortName(1, "Vc")
	if e.Score.Title != "Hangar Theme" || e.Score.Subtitle != "for test" || e.Score.Lines[1].Short != "Vc" {
		t.Errorf("texts: %q %q %q", e.Score.Title, e.Score.Subtitle, e.Score.Lines[1].Short)
	}
	e.setPart(0, "multi", func(p *score.PartLayout) { p.NoMultiRests = true })
	if p := e.Score.Layout.Part("Flute"); p == nil || !p.NoMultiRests {
		t.Errorf("part setting: %+v", p)
	}
	// The save chip sees layout edits.
	if scoreFingerprint(e.Score) == scoreFingerprint(scorePageTestState(t).Score) {
		t.Errorf("a layout edit leaves the file as it was")
	}
}

func TestScoreExports(t *testing.T) {
	e := scorePageTestState(t)
	prevEd := scEd
	scEd = e
	t.Cleanup(func() { scEd = prevEd })
	dir := t.TempDir()
	msg, err := scoreExportPDFs(e.Score, scorePDFRequest{Score: true, Parts: []bool{true, false}, Paper: "a4", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"t.pdf", "t_flute.pdf"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v (%s)", name, err, msg)
		}
		if _, err := pdf.Parse(data); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if !strings.Contains(msg, "2 PDF(s)") {
		t.Errorf("message %q", msg)
	}
	if _, err := scoreExportPDFs(e.Score, scorePDFRequest{Dir: dir}); err == nil {
		t.Errorf("nothing to export was exported")
	}
	// An image of the selection.
	if err := e.exportImage(filepath.Join(dir, "x.png"), 10); err == nil {
		t.Errorf("an image with nothing selected")
	}
	e.Sel = scoreSel{Kind: scoreSelBars, LineA: 0, LineB: 1, BarA: 1, BarB: 2}
	for _, name := range []string{"sel.png", "sel.svg"} {
		if err := e.exportImage(filepath.Join(dir, name), 10); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if st, err := os.Stat(filepath.Join(dir, name)); err != nil || st.Size() < 500 {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := e.exportImage(filepath.Join(dir, "sel.gif"), 10); err == nil {
		t.Errorf("a .gif was written")
	}
	if p := scoreImagePath(true); !strings.HasSuffix(p, filepath.Join("exports", "t", "t_bars1-2.svg")) {
		t.Errorf("default image path %q", p)
	}
}

// TestScorePageViewThroughTheFrame draws the page view and clicks it:
// a click on a note selects it, a click in note input writes, every dialog
// draws, a part shows.
func TestScorePageViewThroughTheFrame(t *testing.T) {
	scoreDrawTest(t)
	scEd = scorePageTestState(t)
	prevView, prevPart, prevZoom := scorePageView, scorePagePart, scorePageZoom
	t.Cleanup(func() {
		scorePageView, scorePagePart, scorePageZoom = prevView, prevPart, prevZoom
		scoreShowPageSetup, scoreShowExport, scoreShowImage = false, false, false
	})
	scorePageView, scorePagePart, scorePageZoom = true, -1, 6
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		scoreShowKeypad = false
		away := imgui.Vec2{X: 5, Y: 5}
		scoreFrame(away, false)
		scoreFrame(away, false)
		b := scorePageBook()
		area := imgui.Vec2{X: 1400, Y: 900 - scoreViewTop - scoreStatusH - scorePageStripH}
		g := scorePageGeomAt(b, imgui.Vec2{X: 0, Y: scoreViewTop + scorePageStripH}, area, 0, 0)
		pg := b.Pages[0]
		sys := pg.Systems[0]
		sg := g.systemGeom(0, sys)
		head := itemCentre(t, sys.List, engrave.RoleNotehead, edit.Loc{Line: 0, Bar: 2, Event: 1}, -1)
		scoreClickAt(sg.toScreen(head))
		if q := scEd.Sel; q.Kind != scoreSelEvent || q.Loc.Bar != 2 || q.Loc.Event != 1 {
			t.Errorf("a click on bar 2's B5: %+v (status %q)", q, scEd.Status)
		}
		// Note input: a click on the cello's top line in an empty bar
		// writes the A the key implies there.
		scEd.Input = true
		var cello engrave.StaffBox
		for _, st := range sys.List.Staves {
			if st.Line == 1 {
				cello = st
			}
		}
		bb := sys.List.Bar(3)
		scoreClickAt(sg.toScreen(engrave.Point{X: bb.XAt(0) + 0.5, Y: cello.Top}))
		if got := barOf(scEd, 1, 0, 3); !strings.HasPrefix(got, "qA3") {
			t.Errorf("note input in the page view wrote %q (status %q)", got, scEd.Status)
		}
		scEd.Input = false
		// A click on the desk clears the selection.
		scoreClickAt(imgui.Vec2{X: g.left - 10, Y: g.top + 50})
		if scEd.Sel.Kind != scoreSelNone {
			t.Errorf("a click beside the page kept %+v", scEd.Sel)
		}
		// Ctrl+wheel zooms.
		io := imgui.CurrentIO()
		const ctrlKey = 200
		io.KeyPress(ctrlKey)
		io.KeyCtrl(ctrlKey, ctrlKey)
		scoreFrame(sg.toScreen(head), false)
		io.AddMouseWheelDelta(0, 1)
		scoreFrame(sg.toScreen(head), false)
		io.KeyRelease(ctrlKey)
		io.KeyCtrl(ctrlKey, ctrlKey)
		scoreFrame(sg.toScreen(head), false)
		if scorePageZoom <= 6 {
			t.Errorf("Ctrl+wheel: zoom %g", scorePageZoom)
		}
		// A part, and every dialog.
		scorePagePart = 1
		scoreFrame(away, false)
		if scorePageBook().Part != "Cello" {
			t.Errorf("the part is not shown")
		}
		scEd.Sel = scoreSel{Kind: scoreSelBars, LineA: 0, LineB: 1, BarA: 1, BarB: 2}
		for _, open := range []func(){func() { scoreShowPageSetup = true }, scoreOpenPDF, scoreOpenImage} {
			open()
			scoreFrame(away, false)
			scoreFrame(away, false)
			imgui.CloseCurrentPopup()
			scoreShowPageSetup, scoreShowExport, scoreShowImage = false, false, false
			scoreFrame(away, false)
		}
		// Back to the continuous view.
		scoreRunCmd(scPageView)
		scoreFrame(away, false)
		if scorePageView {
			t.Errorf("the page view did not switch off")
		}
	})
}

// The page view's part is a line: it follows the line when lines move,
// and a removed line's part is the full score again; a renamed line keeps
// its part's own layout.
func TestScorePagePartFollowsItsLine(t *testing.T) {
	e := scorePageTestState(t)
	prevPart := scorePagePart
	t.Cleanup(func() { scorePagePart = prevPart })
	e.Score.Layout.EnsurePart("Cello").Staff = 8
	scorePagePart = 1
	if !e.moveLine(1, -1) || scorePagePart != 0 || e.Score.Lines[scorePagePart].Name != "Cello" {
		t.Fatalf("after a move the part shows line %d", scorePagePart)
	}
	if !e.renameLine(0, "Violoncello") || e.Score.Layout.Part("Violoncello") == nil || e.Score.Layout.Part("Violoncello").Staff != 8 {
		t.Errorf("the renamed line lost its part layout: %+v", e.Score.Layout.Parts)
	}
	if !e.removeLine(0) || scorePagePart != -1 || len(e.Score.Layout.Parts) != 0 {
		t.Errorf("after removing the shown line: part %d, parts %+v", scorePagePart, e.Score.Layout.Parts)
	}
}
