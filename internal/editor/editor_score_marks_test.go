package editor

// editor_score_marks_test.go — expression in the Score editor: the keys
// (S, H, Shift+H, Alt+G, Ctrl+E, the articulation keys, the keypad's
// second layout), the expression box's words, the palette's form tools,
// and the dynamics lane — its drag model, a drag through the real pane,
// and an override heard while the score plays.

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/engrave"
	"foundry/internal/score/interpret"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// scoreMarksState is the test state with a melody in the flute's first
// two bars, in the editor (scEd) with the Sibelius keys.
func scoreMarksState(t *testing.T) *scoreState {
	t.Helper()
	e := scoreTestState(t)
	v, err := score.ParseVoice("| qG4 qA4 qB4 qC5 | qD5 qE5 qF#5 qG5 |")
	if err != nil {
		t.Fatal(err)
	}
	e.Score.Lines[0].Staves[0].Voices = []*score.Voice{v}
	prevEd, prevKeys := scEd, scoreKeys
	t.Cleanup(func() {
		scEd, scoreKeys = prevEd, prevKeys
		scoreShowExpr, scoreShowTempo, scoreShowRehearse, scoreShowRepeat, scoreShowEnding, scoreShowClefAt = false, false, false, false, false, false
		scoreShowMarks = false
	})
	scEd, scoreKeys = e, scoreSibeliusKeymap()
	return e
}

// scoreNoErrors fails on any error the score validates with.
func scoreNoErrors(t *testing.T, s *score.Score) {
	t.Helper()
	for _, is := range s.Validate(nil) {
		if is.Severity == score.Error {
			t.Errorf("bar %d: %s", is.Bar, is.Msg)
		}
	}
}

// press runs a chord through the keymap as the window does.
func press(t *testing.T, ch scoreChord) {
	t.Helper()
	if !scoreDispatch(ch, false) {
		t.Fatalf("%s runs nothing", ch)
	}
}

// marksOf is an event's marks as the grammar writes them.
func marksOf(e *scoreState, bar, ev int) string {
	m := score.FormatMeasure(edit.Voice(e.Score, 0, 0, 0).Measure(bar))
	f := strings.Fields(m)
	if ev >= len(f) {
		return ""
	}
	if i := strings.Index(f[ev], "("); i >= 0 {
		return f[ev][i:]
	}
	return ""
}

func TestScoreExpressionKeys(t *testing.T) {
	e := scoreMarksState(t)
	sel := func(bar, ev int) { e.selectEvent(edit.Loc{Bar: bar, Event: ev}, -1) }

	// S: a slur to the next note; again on its first note, one further.
	sel(1, 0)
	press(t, key(sdl.SCANCODE_S))
	if marksOf(e, 1, 0) != "(slur)" || marksOf(e, 1, 1) != "(slur_end)" {
		t.Fatalf("slur: %q %q (%s)", marksOf(e, 1, 0), marksOf(e, 1, 1), e.Status)
	}
	press(t, key(sdl.SCANCODE_S))
	if marksOf(e, 1, 1) != "" || marksOf(e, 1, 2) != "(slur_end)" {
		t.Errorf("slur again: %q %q", marksOf(e, 1, 1), marksOf(e, 1, 2))
	}
	// H and Shift+H: hairpins from the selected note to the next.
	sel(2, 0)
	press(t, key(sdl.SCANCODE_H))
	if marksOf(e, 2, 0) != "(cresc)" || marksOf(e, 2, 1) != "(hairpin_end)" {
		t.Errorf("cresc: %q %q (%s)", marksOf(e, 2, 0), marksOf(e, 2, 1), e.Status)
	}
	sel(2, 2)
	press(t, shiftKey(sdl.SCANCODE_H))
	if marksOf(e, 2, 2) != "(dim)" || marksOf(e, 2, 3) != "(hairpin_end)" {
		t.Errorf("dim: %q %q (%s)", marksOf(e, 2, 2), marksOf(e, 2, 3), e.Status)
	}
	// Alt+G: a glissando to the next note (G alone is still the note G).
	sel(1, 3)
	press(t, altKey(sdl.SCANCODE_G))
	if !strings.Contains(marksOf(e, 1, 3), "gliss") {
		t.Errorf("gliss: %q (%s)", marksOf(e, 1, 3), e.Status)
	}
	// The articulation keys toggle, on the selected note.
	sel(1, 1)
	for _, c := range []struct {
		ch   scoreChord
		mark string
	}{
		{key(sdl.SCANCODE_PERIOD), "stacc"}, {key(sdl.SCANCODE_APOSTROPHE), "staccmo"}, {shiftKey(sdl.SCANCODE_PERIOD), "acc"},
		{shiftKey(sdl.SCANCODE_MINUS), "ten"}, {shiftKey(sdl.SCANCODE_APOSTROPHE), "marc"},
	} {
		press(t, c.ch)
		if !e.event().Has(c.mark) {
			t.Errorf("%s: no %s (%s)", c.ch, c.mark, e.Status)
		}
	}
	// A mark of a group takes its neighbour's place (staccatissimo the
	// staccato's, marcato the accent's); again takes it off.
	if got := marksOf(e, 1, 1); got != "(staccmo,ten,marc)" {
		t.Errorf("after the five keys: %q", got)
	}
	press(t, key(sdl.SCANCODE_APOSTROPHE))
	if got := marksOf(e, 1, 1); got != "(ten,marc)" {
		t.Errorf("staccatissimo again: %q", got)
	}
	// Keypad 7-9 and +: accidentals and the accent on the first layout,
	// articulations on the second.
	sel(1, 2)
	press(t, key(sdl.SCANCODE_KP_8))
	if p := e.event().Notes[0].Pitch; p.String() != "B#4" {
		t.Errorf("keypad 8, first layout: %s", p)
	}
	press(t, key(sdl.SCANCODE_KP_PLUS))
	if !e.event().Has("acc") {
		t.Error("keypad +, first layout: no accent")
	}
	press(t, key(sdl.SCANCODE_F8))
	for _, c := range []struct {
		sc   sdl.Scancode
		mark string
	}{{sdl.SCANCODE_KP_7, "stacc"}, {sdl.SCANCODE_KP_8, "ten"}, {sdl.SCANCODE_KP_9, "staccmo"}, {sdl.SCANCODE_KP_PLUS, "marc"}} {
		press(t, key(c.sc))
		if !e.event().Has(c.mark) {
			t.Errorf("keypad %s, second layout: no %s", scoreScancodeName(c.sc), c.mark)
		}
	}
	if p := e.event().Notes[0].Pitch; p.String() != "B#4" {
		t.Errorf("the second layout changed the pitch: %s", p)
	}
	// The keypad panel shows the second layout's articulations, lit.
	for _, c := range scoreKeypadCells() {
		if c.cmd == scKeypad8 && (c.glyph != "articTenutoAbove" || !c.lit) {
			t.Errorf("keypad 8 cell on the second layout: %+v", c)
		}
	}
	press(t, key(sdl.SCANCODE_F8))

	// Bars selected: an articulation on every note in them, a slur over
	// each voice's notes there.
	e.clickBar(0, 2, false, false)
	press(t, key(sdl.SCANCODE_PERIOD))
	for i := 0; i < 4; i++ {
		if !strings.Contains(marksOf(e, 2, i), "stacc") {
			t.Errorf("bar 2 note %d: %q", i, marksOf(e, 2, i))
		}
	}
	press(t, key(sdl.SCANCODE_S))
	if !strings.Contains(marksOf(e, 2, 0), "slur") || !strings.Contains(marksOf(e, 2, 3), "slur_end") {
		t.Errorf("slur over bar 2: %q ... %q", marksOf(e, 2, 0), marksOf(e, 2, 3))
	}
	scoreNoErrors(t, e.Score)
	// Every mark was an undo step of its own.
	n := e.undo.len()
	e.undoStep()
	if strings.Contains(marksOf(e, 2, 0), "slur") || e.undo.len() != n-1 {
		t.Errorf("undo of the slur: %q", marksOf(e, 2, 0))
	}
}

// The expression box (Ctrl+E) files what is typed by what it is.
func TestScoreExpressionBox(t *testing.T) {
	e := scoreMarksState(t)
	press(t, ctrlKey(sdl.SCANCODE_E))
	if scoreShowExpr || !strings.Contains(e.Status, "Select") {
		t.Errorf("with nothing selected: open %v, %q", scoreShowExpr, e.Status)
	}
	e.selectEvent(edit.Loc{Bar: 1, Event: 0}, -1)
	press(t, ctrlKey(sdl.SCANCODE_E))
	if !scoreShowExpr {
		t.Fatal("Ctrl+E did not open the box")
	}
	for _, c := range []struct {
		text string
		want string
	}{
		{"mf", "(mf)"},
		{" FF ", "(ff)"},
		{"pizz", "(ff,tech=pizz.)"},
		{"dolce", `(ff,tech=pizz.,expr=dolce)`},
	} {
		if !e.expression(c.text) {
			t.Fatalf("%q: %s", c.text, e.Status)
		}
		if got := marksOf(e, 1, 0); got != c.want {
			t.Errorf("%q: %s, want %s", c.text, got, c.want)
		}
	}
	// cresc. and dim. are text hairpins to the next note.
	e.selectEvent(edit.Loc{Bar: 1, Event: 1}, -1)
	e.expression("cresc.")
	if got := marksOf(e, 1, 1); got != "(cresc=text)" {
		t.Errorf("cresc.: %q", got)
	}
	// Words around it print as the line's text.
	e.selectEvent(edit.Loc{Bar: 1, Event: 2}, -1)
	if !e.expression("poco dim.") {
		t.Fatal(e.Status)
	}
	if got := barOf(e, 0, 0, 1); !strings.Contains(got, `qB4(dim="poco dim.")`) || e.Status != "poco dim. to bar 1" {
		t.Errorf("poco dim.: %q (%s)", got, e.Status)
	}
	scoreNoErrors(t, e.Score)
	// Tempo text is a tempo mark at the selection.
	e.selectEvent(edit.Loc{Bar: 2, Event: 0}, -1)
	if !e.expression("rit.") {
		t.Fatal(e.Status)
	}
	if tm := edit.TempoMarkAt(e.Score, score.Pos{Bar: 2}); tm == nil || tm.Text != "rit." || tm.BPM != 0 {
		t.Errorf("rit.: %+v", tm)
	}
	// Bars: a dynamic on each line's first note there.
	e.clickBar(0, 2, true, false)
	e.Score.Lines[1].Staves[0].Voices[0], _ = score.ParseVoice("| R | wC3 |")
	if !e.expression("pp") {
		t.Fatal(e.Status)
	}
	if !strings.Contains(marksOf(e, 2, 0), "pp") || edit.Dynamic(&edit.Voice(e.Score, 1, 0, 0).Measure(2).Events[0]) != "pp" {
		t.Errorf("pp over bar 2 of both lines: %q", marksOf(e, 2, 0))
	}
	e.selectEvent(edit.Loc{Bar: 1, Event: 0}, -1)
	e.removeText()
	if got := marksOf(e, 1, 0); got != "(ff)" {
		t.Errorf("text removed: %q", got)
	}
	e.dynamic("")
	if got := marksOf(e, 1, 0); got != "" {
		t.Errorf("dynamic removed: %q", got)
	}
}

// The palette's form tools: repeats, endings, jumps, rehearsal marks,
// tempo marks and clef changes, each an undo step.
func TestScoreFormTools(t *testing.T) {
	e := scoreMarksState(t)
	e.clickBar(0, 1, false, false)
	e.clickBar(0, 2, false, true)
	scoreOpenRepeat()
	if !scoreShowRepeat || scoreFormA != 1 || scoreFormZ != 2 {
		t.Fatalf("repeat dialog: %v %d-%d", scoreShowRepeat, scoreFormA, scoreFormZ)
	}
	if !e.setRepeat(1, 2, 2) || len(e.Score.Repeats) != 1 {
		t.Fatalf("repeat: %s", e.Status)
	}
	if !e.setEnding(2, 2, []int{1}) || len(e.Score.Voltas) != 1 {
		t.Fatalf("ending: %s", e.Status)
	}
	// The next ending's dialog suggests the next pass.
	e.clickBar(0, 3, false, false)
	scoreOpenEnding()
	if scoreEndingText != "2" {
		t.Errorf("the second ending's passes: %q", scoreEndingText)
	}
	passes, _ := edit.ParsePasses(scoreEndingText)
	if !e.setEnding(3, 3, passes) {
		t.Fatalf("second ending: %s", e.Status)
	}
	// Jumps: a segno at the first selected bar, D.S. al Coda at the last.
	e.clickBar(0, 2, false, false)
	e.setJump(score.JumpSegno, 0)
	// "D.S. al Fine" needs its Fine first.
	e.clickBar(0, 4, false, false)
	if e.setJump(score.JumpDS, score.JumpFine) || !strings.Contains(e.Status, "Fine") {
		t.Errorf("D.S. al Fine before the Fine: %s", e.Status)
	}
	e.clickBar(0, 3, false, false)
	e.setJump(score.JumpFine, 0)
	e.clickBar(0, 4, false, false)
	e.setJump(score.JumpDS, score.JumpFine)
	if len(e.Score.Jumps) != 3 {
		t.Fatalf("jumps: %+v (%s)", e.Score.Jumps, e.Status)
	}
	// It plays: 1 2 3 4 (the endings) then from the segno to the Fine.
	perf, err := interpret.Interpret(e.Score, interpret.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var bars []int
	for _, b := range perf.Bars {
		bars = append(bars, b.Bar)
	}
	if got := fmt.Sprint(bars); got != "[1 2 1 3 4 3]" {
		t.Errorf("played bars %s", got)
	}
	// A rehearsal mark, a tempo mark, a clef change.
	e.clickBar(0, 3, false, false)
	scoreOpenRehearsal()
	if !scoreShowRehearse || scoreRehearseBar != 3 {
		t.Errorf("rehearsal dialog at bar %d", scoreRehearseBar)
	}
	if !e.setRehearsal(3, "chorus") || e.Score.SectionIndex("chorus") < 0 {
		t.Errorf("rehearsal: %s", e.Status)
	}
	e.selectEvent(edit.Loc{Bar: 2, Event: 2}, -1)
	scoreOpenTempo()
	if scoreTempoDlg.At != (score.Pos{Bar: 2, Offset: score.F(1, 2)}) || scoreTempoDlg.Has {
		t.Errorf("tempo dialog: %+v", scoreTempoDlg)
	}
	if !e.setTempo(score.TempoMark{At: scoreTempoDlg.At, BPM: 132, Text: "Allegro"}) {
		t.Fatal(e.Status)
	}
	scoreOpenTempo()
	if !scoreTempoDlg.Has || scoreTempoDlg.BPM != 132 || scoreTempoDlg.Text != "Allegro" {
		t.Errorf("tempo dialog on the mark: %+v", scoreTempoDlg)
	}
	scoreOpenClefChange()
	if !scoreShowClefAt || scoreDlgClef != "treble" {
		t.Errorf("clef change dialog: %v %q", scoreShowClefAt, scoreDlgClef)
	}
	if !e.setClefChange(0, 0, scoreClefAtPos, "alto") || e.Score.Lines[0].Staves[0].ClefAt(score.Pos{Bar: 3}) != "alto" {
		t.Errorf("clef change: %s", e.Status)
	}
	scoreNoErrors(t, e.Score)
	// Each was one step; undo takes them off in turn.
	steps := e.undo.len()
	e.undoStep()
	e.undoStep()
	if e.undo.len() != steps-2 || len(e.Score.Lines[0].Staves[0].Clefs) != 0 || len(e.Score.Tempo) != 1 {
		t.Errorf("undo: %d clef changes, %d tempo marks", len(e.Score.Lines[0].Staves[0].Clefs), len(e.Score.Tempo))
	}
	// Removing.
	e.clickBar(0, 1, false, false)
	e.clickBar(0, 4, false, true)
	if !e.removeJumps() || len(e.Score.Jumps) != 0 {
		t.Errorf("jumps removed: %+v", e.Score.Jumps)
	}
	if !e.removeEndings(1, 4) || len(e.Score.Voltas) != 0 {
		t.Errorf("endings removed: %+v", e.Score.Voltas)
	}
	if !e.removeRepeats(1, 4) || len(e.Score.Repeats) != 0 {
		t.Errorf("repeats removed: %+v", e.Score.Repeats)
	}
}

// The dynamics lane's drag model: a drag across it makes an override over
// the beats it covers at the pointer's level (growing and shrinking as it
// goes), a drag on one moves its level, each drag one undo step; Delete
// takes it out. The printed marks never change.
func TestScoreLaneDragModel(t *testing.T) {
	e := scoreMarksState(t)
	e.selectEvent(edit.Loc{Bar: 1, Event: 0}, -1)
	e.dynamic("f")
	marks := score.FormatVoice(edit.Voice(e.Score, 0, 0, 0))
	press(t, ctrlShift(sdl.SCANCODE_L))
	if !e.laneShown(0) || e.laneBelow()[0] != scoreLaneHeight {
		t.Fatalf("lane: %v %v (%s)", e.laneShown(0), e.laneBelow(), e.Status)
	}
	l := engrave.Engrave(e.Score, engrave.Options{BarNumbers: -1, Below: e.laneBelow()})
	if len(l.Lanes) != 1 || l.Lanes[0].Line != 0 {
		t.Fatalf("layout lanes %+v", l.Lanes)
	}
	steps := e.undo.len()
	at := func(bar int, beat int64) score.Pos { return score.Pos{Bar: bar, Offset: score.F(beat, 4)} }
	e.lanePress(0, at(1, 2), 0.3, -1)
	e.laneMove(at(2, 0), 0.3)
	e.laneMove(at(2, 2), 0.25)
	o := e.Score.Lines[0].Overrides
	if len(o) != 1 || o[0].From != at(1, 2) || o[0].To != at(2, 2) || o[0].Level != 0.25 {
		t.Fatalf("while dragging: %+v", o)
	}
	// Back across the anchor: the span flips and shrinks.
	e.laneMove(at(1, 1), 0.25)
	o = e.Score.Lines[0].Overrides
	if len(o) != 1 || o[0].From != at(1, 1) || o[0].To != at(1, 2) {
		t.Fatalf("dragged back: %+v", o)
	}
	e.laneMove(at(2, 1), 0.25)
	e.laneRelease()
	if e.undo.len() != steps+1 {
		t.Errorf("a drag made %d undo steps", e.undo.len()-steps)
	}
	if score.FormatVoice(edit.Voice(e.Score, 0, 0, 0)) != marks {
		t.Error("an override changed the printed marks")
	}
	// What plays: the override's level inside it, f outside.
	ds := e.laneCurve(0)
	tick := func(p score.Pos) float64 { return e.writtenTick(p) + 1 }
	if v := ds.Effective.At(tick(at(1, 3))); math.Abs(float64(v-0.25)) > 1e-4 {
		t.Errorf("effective inside: %v", v)
	}
	if v := ds.Written.At(tick(at(1, 3))); math.Abs(float64(v-score.Dynamics["f"].Level)) > 1e-4 {
		t.Errorf("written inside: %v", v)
	}
	if v := ds.Effective.At(tick(at(3, 0))); math.Abs(float64(v-score.Dynamics["f"].Level)) > 1e-4 {
		t.Errorf("effective after: %v", v)
	}
	// A drag on it moves its level only.
	e.lanePress(0, at(1, 3), 0.25, 0)
	e.laneMove(at(1, 3), 0.7)
	e.laneMove(at(1, 3), 0.8)
	e.laneRelease()
	o = e.Score.Lines[0].Overrides
	if len(o) != 1 || o[0].Level != 0.8 || o[0].From != at(1, 2) || o[0].To != at(2, 1) || e.undo.len() != steps+2 {
		t.Fatalf("level drag: %+v, %d steps", o, e.undo.len()-steps)
	}
	// A press that never moves makes nothing.
	e.lanePress(0, at(3, 0), 0.5, -1)
	e.laneRelease()
	if len(e.Score.Lines[0].Overrides) != 1 || e.undo.len() != steps+2 {
		t.Errorf("a click on the lane: %+v", e.Score.Lines[0].Overrides)
	}
	// Undo the level drag; select it and Delete.
	e.undoStep()
	if e.Score.Lines[0].Overrides[0].Level != 0.25 {
		t.Errorf("undo of the level: %v", e.Score.Lines[0].Overrides[0].Level)
	}
	e.LaneSel = scoreLaneSel{Line: 0, Index: 0}
	e.Sel = scoreSel{}
	scoreRunCmd(scDelete)
	if len(e.Score.Lines[0].Overrides) != 0 {
		t.Errorf("Delete left %+v", e.Score.Lines[0].Overrides)
	}
	// The lane goes with its line through a rename.
	if !e.renameLine(0, "Piccolo") || !e.laneShown(0) {
		t.Errorf("the lane after a rename: %v (%s)", e.laneShown(0), e.Status)
	}
	// Closing the lane leaves the score alone.
	e.toggleLane(0)
	if e.laneShown(0) || e.laneBelow() != nil {
		t.Error("the lane did not close")
	}
}

// The lane through the real pane: a drag across it makes an override at
// the pointer's height, a drag on its level line moves it, a click selects
// it and Delete removes it — and the lane takes the pointer from the notes.
func TestScoreLaneThroughTheView(t *testing.T) {
	scoreDrawTest(t)
	prevKeys := scoreKeys
	t.Cleanup(func() { scoreKeys = prevKeys })
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		scoreShowKeypad = false
		e := scEd
		e.toggleLane(0)
		l := scoreLayout()
		if len(l.Lanes) != 1 {
			t.Fatalf("lanes %+v", l.Lanes)
		}
		ln := l.Lanes[0]
		screen := func(p engrave.Point) imgui.Vec2 {
			return imgui.Vec2{X: scoreHeaderW + scorePad + float32(p.X)*scoreZoom, Y: scoreViewTop + scoreRulerH + scorePad + float32(p.Y)*scoreZoom}
		}
		b1, b2 := l.Bar(1), l.Bar(2)
		from := screen(engrave.Point{X: b1.XAt(0.25), Y: scoreLaneY(ln, 0.4)})
		to := screen(engrave.Point{X: b2.XAt(0.5), Y: scoreLaneY(ln, 0.4)})
		scoreFrame(from, false)
		scoreFrame(from, true)
		scoreFrame(imgui.Vec2{X: (from.X + to.X) / 2, Y: from.Y}, true)
		scoreFrame(to, true)
		scoreFrame(to, false)
		o := e.Score.Lines[0].Overrides
		if len(o) != 1 || o[0].From != (score.Pos{Bar: 1, Offset: score.F(1, 4)}) || o[0].To != (score.Pos{Bar: 2, Offset: score.F(1, 2)}) ||
			math.Abs(float64(o[0].Level-0.4)) > 0.03 {
			t.Fatalf("dragged across the lane: %+v (%s)", o, e.Status)
		}
		if e.Sel.Kind != scoreSelNone || barOf(e, 0, 0, 1) != "" {
			t.Errorf("the drag reached the music: %+v", e.Sel)
		}
		// Drag its level line up.
		l = scoreLayout()
		ln = l.Lanes[0]
		grab := screen(engrave.Point{X: l.Bar(1).XAt(0.75), Y: scoreLaneY(ln, o[0].Level)})
		up := imgui.Vec2{X: grab.X, Y: screen(engrave.Point{Y: scoreLaneY(ln, 0.85)}).Y}
		scoreFrame(grab, false)
		scoreFrame(grab, true)
		scoreFrame(up, true)
		scoreFrame(up, false)
		o = e.Score.Lines[0].Overrides
		if len(o) != 1 || math.Abs(float64(o[0].Level-0.85)) > 0.03 || o[0].From.Bar != 1 {
			t.Fatalf("level dragged: %+v", o)
		}
		// Every frame drew the lane: the header's label too.
		// A click on it selects it; Delete takes it out.
		on := screen(engrave.Point{X: l.Bar(1).XAt(0.75), Y: scoreLaneY(ln, o[0].Level)})
		scoreClickAt(on)
		if e.laneSelected() == nil {
			t.Fatalf("a click on the override did not select it (%s)", e.Status)
		}
		scoreRunCmd(scDelete)
		if len(e.Score.Lines[0].Overrides) != 0 {
			t.Errorf("Delete left %+v", e.Score.Lines[0].Overrides)
		}
		// The D button in the header closes the lane.
		e.toggleLane(0)
		if len(scoreLayout().Lanes) != 0 {
			t.Error("the lane stayed in the layout")
		}
	})
}

// An override dragged while the score plays is heard: the held note on a
// sustaining instrument drops to the override's level, and comes back
// when it is deleted.
func TestScoreLaneHeardWhilePlaying(t *testing.T) {
	scoreUseTestInstruments(t)
	e := scoreLinesState(t)
	p := scoreWithPlayer(t, e)
	prevEvery := scoreLaneSendEvery
	scoreLaneSendEvery = 0
	t.Cleanup(func() { scoreLaneSendEvery = prevEvery })
	// Bar 5 on: whole notes. Play from bar 5.
	e.seek(score.Pos{Bar: 5})
	e.togglePlay(false, e.Transport.Head)
	scoreApplyOps()
	before := scoreLevelAt(scoreRender(p, 0.3))
	if before == 0 {
		t.Fatal("silent")
	}
	// Hold line A at a quarter of its level over bars 5-8, mid-drag.
	e.toggleLane(0)
	lvl := score.Dynamics["mf"].Level / 4
	e.lanePress(0, score.Pos{Bar: 5}, lvl, -1)
	e.laneMove(score.Pos{Bar: 9}, lvl)
	scoreApplyOps()
	during := scoreLevelAt(scoreRender(p, 0.4))
	if during >= before*0.6 || during == 0 {
		t.Errorf("held at a quarter while dragging: %v, was %v", during, before)
	}
	// Line B is untouched.
	e.laneRelease()
	scoreApplyOps()
	out := scoreRender(p, 0.2)
	if r := out[len(out)-199]; r == 0 {
		t.Error("line B went silent")
	}
	// Deleted, the level comes back.
	e.LaneSel = scoreLaneSel{Line: 0, Index: 0}
	e.laneDelete()
	scoreApplyOps()
	after := scoreLevelAt(scoreRender(p, 0.4))
	if math.Abs(float64(after-before)) > float64(before)*0.05 {
		t.Errorf("after delete: %v, was %v", after, before)
	}
}

// The window draws with the palette, the lane and every marks dialog.
func TestScoreMarksWindowDraws(t *testing.T) {
	scoreDrawTest(t)
	t.Cleanup(func() {
		scoreShowMarks = false
		scoreShowExpr, scoreShowTempo, scoreShowRehearse, scoreShowRepeat, scoreShowEnding, scoreShowClefAt = false, false, false, false, false, false
	})
	withImgui(func() {
		imgui.CurrentIO().SetIniFilename("")
		away := imgui.Vec2{X: 5, Y: 5}
		e := scEd
		e.Score.Lines[0].Staves[0].Voices[0], _ = score.ParseVoice("| qG4(f) qA4(cresc) qB4 qC5(hairpin_end,ff) | wD5 |")
		e.touch()
		e.toggleLane(0)
		e.toggleLane(1)
		scoreShowMarks = true
		e.selectEvent(edit.Loc{Bar: 1, Event: 0}, -1)
		scoreFrame(away, false)
		scoreFrame(away, false)
		for _, show := range []*bool{&scoreShowExpr, &scoreShowTempo, &scoreShowRehearse, &scoreShowRepeat, &scoreShowEnding, &scoreShowClefAt} {
			*show = true
			scoreFrame(away, false)
			scoreFrame(away, false)
			imgui.CloseCurrentPopup()
		}
		e.Layout2 = true
		scoreFrame(away, false)
	})
}

// Grace notes typed in note input lean on the note typed after them; the
// concert-pitch view and a line's glissando style are switches.
func TestScoreGraceEntryAndSwitches(t *testing.T) {
	e := scoreMarksState(t)
	e.Score.Lines[0].Staves[0].Voices = []*score.Voice{{}}
	e.clickBar(0, 1, false, false)
	press(t, key(sdl.SCANCODE_N))
	press(t, key(sdl.SCANCODE_KP_4)) // a quarter
	press(t, key(sdl.SCANCODE_F8))
	press(t, key(sdl.SCANCODE_KP_5)) // acciaccaturas on
	press(t, key(sdl.SCANCODE_F8))
	if e.Grace != score.Acciaccatura {
		t.Fatalf("grace: %v (%s)", e.Grace, e.Status)
	}
	press(t, key(sdl.SCANCODE_D))
	press(t, key(sdl.SCANCODE_F8))
	press(t, key(sdl.SCANCODE_KP_5)) // off again
	press(t, key(sdl.SCANCODE_F8))
	press(t, key(sdl.SCANCODE_C))
	press(t, key(sdl.SCANCODE_E))
	if got := barOf(e, 0, 0, 1); got != "/eD5 qC5 qE5 hr" {
		t.Errorf("grace then notes: %q (%s)", got, e.Status)
	}
	press(t, key(sdl.SCANCODE_ESCAPE))

	// Concert pitch: the score's switch, not an undo step.
	steps := e.undo.len()
	scoreRunCmd(scConcert)
	if !e.Score.Concert || e.undo.len() != steps {
		t.Errorf("concert pitch: %v, %d undo steps", e.Score.Concert, e.undo.len()-steps)
	}
	scoreRunCmd(scConcert)
	if e.Score.Concert {
		t.Error("concert pitch did not switch back")
	}
	// A line's glissando style is an undoable edit.
	if !e.setLineGliss(1, "chromatic") || e.Score.Lines[1].Gliss != "chromatic" || e.undo.len() != steps+1 {
		t.Errorf("gliss style: %q (%s)", e.Score.Lines[1].Gliss, e.Status)
	}
	// Undo takes the edit back, not a concert-pitch switch made after it.
	scoreRunCmd(scConcert)
	e.undoStep()
	if e.Score.Lines[1].Gliss != "" {
		t.Errorf("undo of the gliss style: %q", e.Score.Lines[1].Gliss)
	}
	if !e.Score.Concert {
		t.Error("undo switched concert pitch back off")
	}
	e.redoStep()
	if !e.Score.Concert || e.Score.Lines[1].Gliss != "chromatic" {
		t.Errorf("redo: concert %v, gliss %q", e.Score.Concert, e.Score.Lines[1].Gliss)
	}
	e.undoStep()
	scoreRunCmd(scConcert)
	// The lane key wants a line when there are several.
	e.Sel = scoreSel{}
	scoreRunCmd(scLane)
	if e.laneShown(0) || e.laneShown(1) || !strings.Contains(e.Status, "Select a line") {
		t.Errorf("the lane key with nothing selected: %s", e.Status)
	}
	e.clickBar(1, 2, false, false)
	scoreRunCmd(scLane)
	if !e.laneShown(1) || e.laneShown(0) {
		t.Errorf("the lane key on the cello: %s", e.Status)
	}
}

// Ctrl+Alt+2 on selected bars moves their notes into voice 2 (Sibelius),
// marks and grace notes with them, leaving voice 1's rests; Ctrl+Alt+1
// brings them back. A tuplet's notes stay. One undo step each.
func TestScoreBarsMoveToAVoice(t *testing.T) {
	e := scoreMarksState(t)
	v, err := score.ParseVoice("| qG4(p) /eB4 qA4(stacc) hB4 | 3{eD5 eE5 eF#5} qG5 hA5 |")
	if err != nil {
		t.Fatal(err)
	}
	e.Score.Lines[0].Staves[0].Voices = []*score.Voice{v}
	e.Sel = scoreSel{Kind: scoreSelBars, LineA: 0, LineB: 0, BarA: 1, BarB: 2}
	steps := e.undo.len()
	press(t, ctrlAlt(scoreDigitKeys[2]))
	if got := barOf(e, 0, 0, 1); got != "qr qr hr" {
		t.Errorf("voice 1, bar 1: %q (%s)", got, e.Status)
	}
	if got := barOf(e, 0, 1, 1); got != "qG4(p) /eB4 qA4(stacc) hB4" {
		t.Errorf("voice 2, bar 1: %q", got)
	}
	if got := barOf(e, 0, 0, 2); got != "3{eD5 eE5 eF#5} qr hr" {
		t.Errorf("voice 1, bar 2 keeps its tuplet: %q", got)
	}
	if got := barOf(e, 0, 1, 2); got != "qr qG5 hA5" {
		t.Errorf("voice 2, bar 2: %q", got)
	}
	if e.undo.len() != steps+1 || e.Voice != 1 || e.Sel.Kind != scoreSelBars || !strings.Contains(e.Status, "Moved 5 note(s) to voice 2; 3 in tuplets stayed") {
		t.Errorf("undo steps %d, voice %d, sel %v, status %q", e.undo.len()-steps, e.Voice, e.Sel.Kind, e.Status)
	}
	scoreNoErrors(t, e.Score)
	if _, err := interpret.Interpret(e.Score, interpret.Options{}); err != nil {
		t.Fatal(err)
	}
	// And back.
	press(t, ctrlAlt(scoreDigitKeys[1]))
	if got := barOf(e, 0, 0, 1); got != "qG4(p) /eB4 qA4(stacc) hB4" {
		t.Errorf("back in voice 1: %q (%s)", got, e.Status)
	}
	e.undoStep()
	e.undoStep()
	if got := barOf(e, 0, 0, 1); got != "qG4(p) /eB4 qA4(stacc) hB4" {
		t.Errorf("undone: %q", got)
	}
	// Bars with nothing to move say so and only switch the voice.
	e.Sel = scoreSel{Kind: scoreSelBars, LineA: 0, LineB: 0, BarA: 1, BarB: 1}
	press(t, ctrlAlt(scoreDigitKeys[1]))
	if e.Voice != 0 || !strings.Contains(e.Status, "Nothing") {
		t.Errorf("nothing to move: voice %d, %q", e.Voice, e.Status)
	}
}
