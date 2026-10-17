package editor

// editor_score_workflow_test.go — whole entry sessions the way a Sibelius
// user types them, key after key through scoreState: the value before the
// letter, a note longer than the bar tied over the barline, a second voice
// started under the first, a tuplet typed into, a grand staff's lower staff,
// and the passage repeated, copied, pasted and undone.

import (
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/edit"
)

func TestScoreTypingASession(t *testing.T) {
	e := scoreTestState(t)
	e.clickBar(0, 1, false, false)
	e.toggleInput()
	// 4 C, 3 D E, 5 F: the value is chosen before the letter.
	e.keypad(4)
	e.letter(0)
	e.keypad(3)
	e.letter(1)
	e.letter(2)
	e.keypad(5)
	e.letter(3)
	if got := barOf(e, 0, 0, 1); got != "qC5 eD5 eE5 hF#5" {
		t.Errorf("bar 1 = %q", got)
	}
	// 4 G A B, then a half C with a quarter left: tied over the barline.
	e.keypad(4)
	e.letter(4)
	e.letter(5)
	e.letter(6)
	e.keypad(5)
	e.letter(0)
	if got := barOf(e, 0, 0, 2); got != "qG5 qA5 qB5 qC6~" {
		t.Errorf("bar 2 = %q", got)
	}
	if got := barOf(e, 0, 0, 3); got != "qC6 qr hr" {
		t.Errorf("bar 3 = %q", got)
	}
	if e.Sel.Loc.Bar != 3 || e.Sel.Loc.Event != 0 {
		t.Errorf("the tie's last note is not selected: %+v", e.Sel)
	}
	// The dot is the next note's too: 4 . D is a dotted quarter.
	e.keypad(4)
	e.toggleDot()
	e.letter(1)
	if got := barOf(e, 0, 0, 3); got != "qC6 q.D6 er qr" {
		t.Errorf("dotted = %q", got)
	}
	// Undo takes the dotted note out, then the tied half as one step.
	e.undoStep()
	e.undoStep()
	if got := barOf(e, 0, 0, 2) + " | " + barOf(e, 0, 0, 3); got != "qG5 qA5 qB5 qr | " {
		t.Errorf("after two undos = %q", got)
	}
	e.redoStep()
	if got := barOf(e, 0, 0, 3); got != "qC6 qr hr" {
		t.Errorf("redo = %q", got)
	}
}

func TestScoreSecondVoiceStartsUnderTheFirst(t *testing.T) {
	e := scoreTestState(t)
	e.clickBar(0, 1, false, false)
	e.toggleInput()
	e.keypad(5)
	e.letter(4) // hG4
	e.setVoice(1)
	if e.Sel.Kind != scoreSelCaret || e.Sel.Loc.Voice != 1 || !e.Sel.At.Eq(score.F(1, 2)) {
		t.Fatalf("caret = %+v", e.Sel)
	}
	if got := e.describeSel(); got != "Flute, bar 1 at beat 3, voice 2" {
		t.Errorf("describe = %q", got)
	}
	e.keypad(4)
	e.letter(2) // E, nearest the voice's last note (none): around the middle line
	if got := barOf(e, 0, 1, 1); got != "hr qE5 qr" {
		t.Errorf("voice 2 = %q", got)
	}
	if got := barOf(e, 0, 0, 1); got != "hG4 hr" {
		t.Errorf("voice 1 changed: %q", got)
	}
	// Back to voice 1 out of note input: the event at the same onset.
	e.toggleInput()
	e.selectEvent(edit.Loc{Voice: 1, Bar: 1, Event: 1}, -1)
	e.setVoice(0)
	if e.Sel.Kind != scoreSelEvent || e.Sel.Loc.Voice != 0 || e.Sel.Loc.Event != 1 {
		t.Errorf("voice 1 at beat 3 = %+v", e.Sel)
	}
	// Right from a caret goes to the event after it.
	e.Sel = scoreSel{Kind: scoreSelCaret, Loc: edit.Loc{Voice: 1, Bar: 1, Event: -1}, At: score.F(1, 4)}
	e.moveSel(1)
	if e.Sel.Kind != scoreSelEvent || e.Sel.Loc.Event != 1 {
		t.Errorf("next from the caret = %+v", e.Sel)
	}
	// A caret past its bar's end after a meter change is dropped.
	e.Sel = scoreSel{Kind: scoreSelCaret, Loc: edit.Loc{Voice: 1, Bar: 1, Event: -1}, At: score.F(7, 8)}
	m, _ := score.ParseMeter("3/4")
	e.setMeter(1, m)
	if e.Sel.Kind != scoreSelNone {
		t.Errorf("stale caret kept: %+v", e.Sel)
	}
}

func TestScoreTypingIntoATuplet(t *testing.T) {
	e := scoreTestState(t)
	e.clickBar(0, 1, false, false)
	e.toggleInput()
	e.keypad(3)
	e.letter(0)
	e.tuplet(3)
	e.letter(1)
	e.letter(2)
	e.letter(3)
	if got := barOf(e, 0, 0, 1); got != "3{eC5 eD5 eE5} eF#5 er hr" {
		t.Errorf("bar = %q", got)
	}
}

func TestScoreTypingOnTheLowerStaff(t *testing.T) {
	e := newScoreState(scoreTemplatePiano())
	e.clickBarStaff(0, 1, 2, false, false)
	e.letter(0)
	if v := edit.Voice(e.Score, 0, 1, 0); v == nil || score.FormatMeasure(v.Measure(2)) != "qC3 qr hr" {
		t.Errorf("bass staff bar 2 = %v", v)
	}
	if v := edit.Voice(e.Score, 0, 0, 0); v != nil && len(v.Measure(2).Events) > 0 {
		t.Errorf("the treble staff was written: %q", score.FormatMeasure(v.Measure(2)))
	}
	// Ctrl+click over every line starts on the first line's top staff.
	e.clickBarStaff(0, 1, 3, true, false)
	if e.Sel.Staff != 0 {
		t.Errorf("all-lines selection staff = %d", e.Sel.Staff)
	}
}

func TestScorePassageRepeatAndPaste(t *testing.T) {
	e := scoreTestState(t)
	e.clickBar(0, 3, false, false)
	e.toggleInput()
	for _, step := range []int{0, 1, 2, 3, 4, 5, 6, 0} { // two bars of quarters
		e.letter(step)
	}
	e.toggleInput()
	e.clickBar(0, 3, false, false)
	e.clickBar(0, 4, false, true)
	// R after the last bar grows the score and selects the copy.
	e.repeat()
	if e.Score.Measures() != 6 {
		t.Errorf("bars = %d", e.Score.Measures())
	}
	if got := barOf(e, 0, 0, 5) + " | " + barOf(e, 0, 0, 6); got != barOf(e, 0, 0, 3)+" | "+barOf(e, 0, 0, 4) {
		t.Errorf("repeat = %q", got)
	}
	if q := e.Sel; q.Kind != scoreSelBars || q.BarA != 5 || q.BarB != 6 {
		t.Errorf("repeat selection = %+v", q)
	}
	// Copy a bar of every line and Alt+click it into bar 1: the cello's
	// empty bar overwrites, the flute's notes come along.
	e.clickBar(0, 3, true, false)
	e.copySel()
	e.pasteAt(0, 0, 1, score.Frac{})
	if got := barOf(e, 0, 0, 1); got != barOf(e, 0, 0, 3) {
		t.Errorf("pasted = %q", got)
	}
	if q := e.Sel; q.LineA != 0 || q.LineB != 1 || q.BarA != 1 {
		t.Errorf("paste selection = %+v", q)
	}
	// One undo takes the paste back whole.
	e.undoStep()
	if got := barOf(e, 0, 0, 1); got != "" {
		t.Errorf("undone paste = %q", got)
	}
}

// Intervals and pitch moves work on a passage as on a note: every chord of
// the selected bars, in every voice, spelled in each bar's key.
func TestScorePassageIntervalsAndMoves(t *testing.T) {
	e := scoreTestState(t)
	e.clickBar(0, 1, false, false)
	e.toggleInput()
	for _, step := range []int{4, 5, 6, 0} { // G A B C
		e.letter(step)
	}
	e.keypad(5)
	e.letter(1) // D, into bar 2
	e.rest()
	e.toggleInput()
	e.clickBar(0, 1, false, false)
	e.clickBar(0, 2, false, true)
	e.interval(3, false)
	if got := barOf(e, 0, 0, 1) + " | " + barOf(e, 0, 0, 2); got != "qG4+B4 qA4+C5 qB4+D5 qC5+E5 | hD5+F#5 hr" {
		t.Errorf("thirds = %q", got)
	}
	// The same letter twice is not added, and nothing is not a step.
	e.interval(3, false)
	if !strings.Contains(e.Status, "Added") && !strings.Contains(e.Status, "already") {
		t.Errorf("status = %q", e.Status)
	}
	e.octave(1)
	if got := barOf(e, 0, 0, 2); !strings.HasPrefix(got, "hD6+F#6") {
		t.Errorf("octave = %q", got)
	}
	e.undoStep()
	e.semitone(-1)
	if got := barOf(e, 0, 0, 2); !strings.HasPrefix(got, "hDb5+F5") {
		t.Errorf("semitone = %q", got)
	}
	// Empty bars refuse and leave no undo step.
	n := e.undo.len()
	e.clickBar(1, 1, false, false)
	e.interval(3, false)
	if e.undo.len() != n {
		t.Error("a refused passage edit left an undo step")
	}
}
