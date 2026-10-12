package editor

// editor_score_test.go — the Score editor's commands, without a window.
//
// Every command runs on a scoreState built here, which is why the editing
// model (editor_score_edit.go) was kept apart from the drawing: note input
// by click and by letter, intervals, accidentals, pitch moves, durations,
// dots, rests, ties, voices, tuplets, delete, the bar selection, the
// clipboard, repeat, signatures, undo and transposing staves can all be
// driven with no GL context and no ImGui frame.

import (
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/edit"
)

// scoreTestState is an editor on one treble line (and a second, bass, for
// the multi-line tests) of four bars of 4/4 in G major.
func scoreTestState(t *testing.T) *scoreState {
	t.Helper()
	s := score.New("t")
	s.Bars = 4
	s.Key = []score.KeyMark{{Bar: 1, Fifths: 1, Mode: "major"}}
	s.Lines = []*score.Line{
		score.NewLine("Flute", "woodwinds/flute", "treble"),
		score.NewLine("Cello", "strings/cello_section", "bass"),
	}
	return newScoreState(s)
}

// barOf is a voice's bar as the grammar writes it.
func barOf(e *scoreState, line, voice, bar int) string {
	v := edit.Voice(e.Score, line, 0, voice)
	if v == nil {
		return ""
	}
	return score.FormatMeasure(v.Measure(bar))
}

func pitchD(s string) int {
	p, err := score.ParsePitch(s)
	if err != nil {
		panic(err)
	}
	return p.Diatonic()
}

func TestScoreClickWritesTheKeysPitch(t *testing.T) {
	e := scoreTestState(t)
	e.toggleInput()
	// The top line of a treble staff in G major is F#.
	if p := e.ghost(0, 0, 1, pitchD("F5")); p.String() != "F#5" {
		t.Errorf("ghost on the F line = %s", p)
	}
	if !e.clickWrite(0, 0, 1, score.Frac{}, pitchD("F5")) {
		t.Fatal(e.Status)
	}
	if got := barOf(e, 0, 0, 1); got != "qF#5 qr hr" {
		t.Errorf("bar = %q", got)
	}
	if e.Sel.Kind != scoreSelEvent || e.Sel.Loc.Event != 0 || e.Sel.Note != 0 {
		t.Errorf("selection = %+v", e.Sel)
	}
	if a := e.takeAuditions(); len(a) != 1 || a[0].MIDI[0] != 78 {
		t.Errorf("auditions = %+v", a)
	}
	// Writing an eighth over beat 2 truncates nothing and rests the rest.
	e.Dur = score.Duration{Base: score.Eighth}
	e.clickWrite(0, 0, 1, score.F(1, 4), pitchD("G4"))
	if got := barOf(e, 0, 0, 1); got != "qF#5 eG4 er hr" {
		t.Errorf("bar = %q", got)
	}
	// An armed accidental goes on the next note only.
	e.Sel = scoreSel{}
	e.accidental(edit.Flat)
	if !e.Armed {
		t.Fatal("not armed")
	}
	if p := e.ghost(0, 0, 1, pitchD("E5")); p.String() != "Eb5" {
		t.Errorf("armed ghost = %s", p)
	}
	e.clickWrite(0, 0, 1, score.F(1, 2), pitchD("E5"))
	if got := barOf(e, 0, 0, 1); got != "qF#5 eG4 er eEb5 er qr" || e.Armed {
		t.Errorf("bar = %q, armed %v", got, e.Armed)
	}
	// Voice 2 is its own line of music.
	e.setVoice(1)
	e.Dur = score.Duration{Base: score.Half}
	e.clickWrite(0, 0, 2, score.Frac{}, pitchD("D4"))
	if got := barOf(e, 0, 1, 2); got != "hD4 hr" {
		t.Errorf("voice 2 = %q", got)
	}
}

func TestScoreLetterEntry(t *testing.T) {
	e := scoreTestState(t)
	// Nothing selected: nothing to write after.
	e.letter(4)
	if !strings.Contains(e.Status, "Select") {
		t.Errorf("status = %q", e.Status)
	}
	e.clickBar(0, 1, false, false)
	// A letter on selected bars starts note input at the bar.
	e.letter(4) // G: nearest the middle line's B4 is G4
	if !e.Input {
		t.Error("note input did not start")
	}
	for _, step := range []int{5, 6, 0, 3} { // A B C F#
		e.letter(step)
	}
	if got := barOf(e, 0, 0, 1); got != "qG4 qA4 qB4 qC5" {
		t.Errorf("bar 1 = %q", got)
	}
	// The bar is full: the next letter goes into bar 2, F spelled F# in G,
	// a fourth above the C rather than a fifth below.
	if got := barOf(e, 0, 0, 2); got != "qF#5 qr hr" {
		t.Errorf("bar 2 = %q", got)
	}
	// Out of note input a letter changes the selected note.
	e.Input = false
	e.letter(1) // D: nearest F#5 is D5
	if got := barOf(e, 0, 0, 2); got != "qD5 qr hr" {
		t.Errorf("changed = %q", got)
	}
	// Shift+letter adds above the top note.
	e.addLetter(3)
	if got := barOf(e, 0, 0, 2); got != "qD5+F#5 qr hr" {
		t.Errorf("added = %q", got)
	}
	// At the end of the score, entry grows it.
	e.Input = true
	e.clickBar(0, 4, false, false)
	e.Dur = score.Duration{Base: score.WholeNote}
	e.letter(0)
	e.letter(0)
	if e.Score.Measures() != 5 || e.Score.Bars != 5 {
		t.Errorf("bars = %d", e.Score.Measures())
	}
}

func TestScoreIntervalsAndPitchMoves(t *testing.T) {
	e := scoreTestState(t)
	e.Input = true
	e.clickWrite(0, 0, 1, score.Frac{}, pitchD("D5"))
	e.interval(3, false) // F#5 in G
	e.interval(3, false) // A5 above the top note (F# is selected now)
	if got := barOf(e, 0, 0, 1); got != "qD5+F#5+A5 qr hr" {
		t.Errorf("chord = %q", got)
	}
	e.Sel.Note = -1
	e.interval(2, true) // a second below the bottom: C5
	if got := barOf(e, 0, 0, 1); got != "qC5+D5+F#5+A5 qr hr" {
		t.Errorf("below = %q", got)
	}
	e.interval(2, false) // a second above C5 is the D5 already there
	if !strings.Contains(e.Status, "already") {
		t.Errorf("status = %q", e.Status)
	}
	// The bottom note (selected since it was added) up a semitone: C#5.
	e.semitone(1)
	if got := barOf(e, 0, 0, 1); got != "qC#5+D5+F#5+A5 qr hr" {
		t.Errorf("semitone = %q", got)
	}
	e.accidental(edit.Sharp) // C# -> C##
	if got := barOf(e, 0, 0, 1); got != "qC##5+D5+F#5+A5 qr hr" {
		t.Errorf("double sharp = %q", got)
	}
	e.accidental(edit.Natural)
	e.step(-1) // C5 down a step in G: B4
	if got := barOf(e, 0, 0, 1); got != "qB4+D5+F#5+A5 qr hr" {
		t.Errorf("step = %q", got)
	}
	e.octave(-1)
	if got := barOf(e, 0, 0, 1); got != "qB3+D5+F#5+A5 qr hr" {
		t.Errorf("octave = %q", got)
	}
	// The whole chord.
	e.Sel.Note = -1
	e.octave(1)
	if got := barOf(e, 0, 0, 1); got != "qB4+D6+F#6+A6 qr hr" {
		t.Errorf("chord octave = %q", got)
	}
	e.Sel.Note = 0
	e.respell()
	if got := barOf(e, 0, 0, 1); got != "qCb5+D6+F#6+A6 qr hr" {
		t.Errorf("respell = %q", got)
	}
	// With nothing selected the moves say so and change nothing.
	e.Sel = scoreSel{}
	before := barOf(e, 0, 0, 1)
	e.semitone(1)
	e.interval(3, false)
	if barOf(e, 0, 0, 1) != before {
		t.Error("changed with nothing selected")
	}
}

func TestScoreDurationsAndRhythm(t *testing.T) {
	e := scoreTestState(t)
	for slot, want := range map[int]score.DurBase{1: score.D32, 2: score.D16, 3: score.Eighth, 4: score.Quarter, 5: score.Half, 6: score.WholeNote} {
		e.keypad(slot)
		if e.Dur.Base != want {
			t.Errorf("keypad %d = %v", slot, e.Dur)
		}
	}
	e.Layout2 = true
	for slot, want := range map[int]score.DurBase{1: score.D64, 2: score.D128, 3: score.D256, 4: score.Breve} {
		e.keypad(slot)
		if e.Dur.Base != want {
			t.Errorf("layout 2 keypad %d = %v", slot, e.Dur)
		}
	}
	e.Dur = score.Duration{Base: score.Breve}
	e.keypad(5)
	if e.Dur.Base != score.Breve || !strings.Contains(e.Status, "nothing") {
		t.Errorf("layout 2 keypad 5: %v %q", e.Dur, e.Status)
	}
	e.Layout2 = false

	// In note input a value is the next note's: the note just written keeps
	// its own (Sibelius: 4 C 3 D is a quarter then an eighth).
	e.Input = true
	e.Dur = score.Duration{Base: score.Quarter}
	e.clickWrite(0, 0, 1, score.Frac{}, pitchD("G4"))
	e.keypad(5)
	e.toggleDot()
	if got := barOf(e, 0, 0, 1); got != "qG4 qr hr" || e.Dur != (score.Duration{Base: score.Half, Dots: 1}) {
		t.Errorf("value in note input = %q (%v)", got, e.Dur)
	}
	if d, _, _ := e.shownDur(); d != e.Dur {
		t.Errorf("toolbar shows %v in note input, want %v", d, e.Dur)
	}
	// Out of note input a selected note takes the value; the bar reflows.
	e.Input = false
	e.keypad(5)
	if got := barOf(e, 0, 0, 1); got != "hG4 hr" {
		t.Errorf("half = %q", got)
	}
	e.toggleDot()
	if got := barOf(e, 0, 0, 1); got != "h.G4 qr" || e.Dur.Dots != 1 {
		t.Errorf("dot = %q (%v)", got, e.Dur)
	}
	if d, _, _ := e.shownDur(); d != (score.Duration{Base: score.Half, Dots: 1}) {
		t.Errorf("toolbar shows %v", d)
	}
	// Rest in note input writes a rest after the selection.
	e.Input = true
	e.Dur = score.Duration{Base: score.Quarter}
	e.rest()
	if got := barOf(e, 0, 0, 1); got != "h.G4 qr" || e.Sel.Loc.Event != 1 {
		t.Errorf("rest = %q at %+v", got, e.Sel.Loc)
	}
	// Out of note input the selected note becomes a rest.
	e.Input = false
	e.selectEvent(edit.Loc{Bar: 1, Event: 0}, -1)
	e.tie()
	if !e.chord().Notes[0].Tie {
		t.Error("not tied")
	}
	e.rest()
	if got := barOf(e, 0, 0, 1); got != "h.r qr" {
		t.Errorf("made rest = %q", got)
	}
	// Dots with nothing selected set the next value.
	e.Sel = scoreSel{}
	e.toggleDot()
	e.toggleDot()
	if e.Dur.Dots != 2 {
		t.Errorf("dots = %d", e.Dur.Dots)
	}
}

func TestScoreTupletsDeleteAndFlip(t *testing.T) {
	e := scoreTestState(t)
	e.Input = true
	e.Dur = score.Duration{Base: score.Eighth}
	e.clickWrite(0, 0, 1, score.Frac{}, pitchD("C5"))
	e.tuplet(3)
	if got := barOf(e, 0, 0, 1); got != "3{eC5 er er} qr hr" {
		t.Errorf("triplet = %q", got)
	}
	if ev := e.event(); ev == nil || ev.Tuplet == 0 {
		t.Error("the triplet's first note is not selected")
	}
	// Letters carry on inside the triplet.
	e.letter(1)
	e.letter(2)
	if got := barOf(e, 0, 0, 1); got != "3{eC5 eD5 eE5} qr hr" {
		t.Errorf("triplet filled = %q", got)
	}
	// Delete: a note becomes a rest; one note of a chord goes.
	e.interval(3, false)
	e.deleteSel()
	if got := barOf(e, 0, 0, 1); got != "3{eC5 eD5 eE5} qr hr" {
		t.Errorf("chord note deleted = %q", got)
	}
	e.deleteSel()
	if got := barOf(e, 0, 0, 1); got != "3{eC5 eD5 er} qr hr" {
		t.Errorf("note deleted = %q", got)
	}
	// Flip forces the stem the other way, then back.
	e.selectEvent(edit.Loc{Bar: 1, Event: 0}, -1)
	e.flip(false)
	if !e.chord().Has("up") {
		t.Error("not forced up")
	}
	e.flip(true)
	if e.chord().Has("up") || e.chord().Has("down") {
		t.Error("not released")
	}
	// Bars: Delete clears them.
	e.clickBar(0, 1, false, false)
	e.deleteSel()
	if got := barOf(e, 0, 0, 1); got != "" {
		t.Errorf("cleared = %q", got)
	}
}

func TestScoreBarSelection(t *testing.T) {
	e := scoreTestState(t)
	e.clickBar(1, 2, false, false)
	if q := e.Sel; q.Kind != scoreSelBars || q.LineA != 1 || q.LineB != 1 || q.BarA != 2 || q.BarB != 2 {
		t.Errorf("click = %+v", q)
	}
	// Shift extends bars and lines.
	e.clickBar(0, 4, false, true)
	if q := e.Sel; q.LineA != 0 || q.LineB != 1 || q.BarA != 2 || q.BarB != 4 {
		t.Errorf("extend = %+v", q)
	}
	// Ctrl takes every line.
	e.clickBar(0, 3, true, false)
	if q := e.Sel; q.LineA != 0 || q.LineB != 1 || q.BarA != 3 || q.BarB != 3 {
		t.Errorf("ctrl = %+v", q)
	}
	if got := e.describeSel(); got != "All lines, bar 3" {
		t.Errorf("describe = %q", got)
	}
	e.selectAll()
	if q := e.Sel; q.BarA != 1 || q.BarB != 4 || q.LineB != 1 {
		t.Errorf("all = %+v", q)
	}
	e.escape()
	if e.Sel.Kind != scoreSelNone {
		t.Error("escape kept the selection")
	}
	// Moving through a voice crosses barlines.
	e.Input = true
	e.Dur = score.Duration{Base: score.Half}
	e.clickWrite(0, 0, 1, score.F(1, 2), pitchD("G4"))
	e.clickWrite(0, 0, 2, score.Frac{}, pitchD("A4"))
	e.selectEvent(edit.Loc{Bar: 1, Event: 1}, -1)
	e.moveSel(1)
	if e.Sel.Loc.Bar != 2 || e.Sel.Loc.Event != 0 {
		t.Errorf("next = %+v", e.Sel.Loc)
	}
	e.moveSel(-1)
	e.moveSel(-1)
	if e.Sel.Loc.Bar != 1 || e.Sel.Loc.Event != 0 {
		t.Errorf("previous = %+v", e.Sel.Loc)
	}
}

func TestScoreCopyPasteRepeat(t *testing.T) {
	e := scoreTestState(t)
	e.Input = true
	e.clickWrite(0, 0, 1, score.Frac{}, pitchD("G4"))
	e.Input = false
	e.paste()
	if !strings.Contains(e.Status, "Nothing copied") {
		t.Errorf("status = %q", e.Status)
	}
	// Bars: copy bar 1, paste into bar 3.
	e.clickBar(0, 1, false, false)
	e.copySel()
	e.clickBar(0, 3, false, false)
	e.paste()
	if got := barOf(e, 0, 0, 3); got != "qG4 qr hr" {
		t.Errorf("pasted = %q", got)
	}
	// R repeats after the selection, growing the score, and selects the copy.
	e.clickBar(0, 3, false, true) // bars 3 only (extend from itself)
	e.clickBar(0, 4, false, true)
	e.repeat()
	if e.Score.Measures() != 6 || barOf(e, 0, 0, 5) != "qG4 qr hr" || e.Sel.BarA != 5 || e.Sel.BarB != 6 {
		t.Errorf("repeat: %d bars, bar 5 %q, sel %+v", e.Score.Measures(), barOf(e, 0, 0, 5), e.Sel)
	}
	// Alt+click pastes the clipboard at the click.
	e.pasteAt(1, 0, 2, score.Frac{})
	if got := barOf(e, 1, 0, 2); got != "qG4 qr hr" {
		t.Errorf("alt+click paste into the cello = %q", got)
	}
	// An event: copied, pasted over a selected note, repeated after itself.
	e.selectEvent(edit.Loc{Bar: 1, Event: 0}, -1)
	e.copySel()
	e.repeat()
	if got := barOf(e, 0, 0, 1); got != "qG4 qG4 hr" {
		t.Errorf("note repeated = %q", got)
	}
	e.repeat()
	e.repeat()
	if got := barOf(e, 0, 0, 1); got != "qG4 qG4 qG4 qG4" {
		t.Errorf("note repeated four times = %q", got)
	}
	e.pasteAt(0, 0, 2, score.F(3, 4))
	if got := barOf(e, 0, 0, 2); got != "hr qr qG4" {
		t.Errorf("event alt+click = %q", got)
	}
}

func TestScoreUndoRedo(t *testing.T) {
	e := scoreTestState(t)
	e.Input = true
	e.clickWrite(0, 0, 1, score.Frac{}, pitchD("G4"))
	e.interval(3, false)
	if !e.canUndo() || e.undo.len() != 2 {
		t.Fatalf("undo depth = %d", e.undo.len())
	}
	e.undoStep()
	if got := barOf(e, 0, 0, 1); got != "qG4 qr hr" {
		t.Errorf("undone = %q", got)
	}
	e.undoStep()
	if got := barOf(e, 0, 0, 1); got != "" {
		t.Errorf("undone twice = %q", got)
	}
	e.redoStep()
	e.redoStep()
	if got := barOf(e, 0, 0, 1); got != "qG4+B4 qr hr" {
		t.Errorf("redone = %q", got)
	}
	// A refused edit leaves no step and no trace.
	n := e.undo.len()
	rev := e.Rev
	e.Sel = scoreSel{Kind: scoreSelEvent, Loc: edit.Loc{Bar: 1, Event: 0}, Note: -1}
	e.tuplet(9) // nine quarters do not fit in a bar of 4/4
	if e.undo.len() != n || e.Rev != rev {
		t.Errorf("a refused tuplet pushed undo (%d → %d) or touched (%d → %d)", n, e.undo.len(), rev, e.Rev)
	}
	if got := barOf(e, 0, 0, 1); got != "qG4+B4 qr hr" {
		t.Errorf("refused tuplet changed the bar: %q", got)
	}
	// A new edit clears redo.
	e.undoStep()
	e.semitone(1)
	if e.canRedo() {
		t.Error("redo survived an edit")
	}
}

func TestScoreSignaturesAndClefs(t *testing.T) {
	e := scoreTestState(t)
	e.Input = true
	e.Dur = score.Duration{Base: score.WholeNote}
	e.clickWrite(0, 0, 2, score.Frac{}, pitchD("F5"))
	e.setKey(2, -2, "major")
	if e.Score.KeyAt(2).Fifths != -2 || e.Score.KeyAt(1).Fifths != 1 {
		t.Errorf("keys = %v", e.Score.Key)
	}
	// The F# stays F#; a click on the F line is F now.
	if got := barOf(e, 0, 0, 2); got != "wF#5" {
		t.Errorf("bar 2 = %q", got)
	}
	if p := e.ghost(0, 0, 2, pitchD("B4")); p.String() != "Bb4" {
		t.Errorf("ghost in Bb = %s", p)
	}
	m, _ := score.ParseMeter("3/4")
	e.setMeter(2, m)
	if got := barOf(e, 0, 0, 2); got != "h.F#5" || !strings.Contains(e.Status, "cut") {
		t.Errorf("3/4: %q, %q", got, e.Status)
	}
	e.setClef(1, 0, "tenor")
	if e.Score.Lines[1].Staves[0].Clef != "tenor" {
		t.Error("clef")
	}
	e.setClef(1, 0, "nonsense")
	if e.Score.Lines[1].Staves[0].Clef != "tenor" || !strings.Contains(e.Status, "clef") {
		t.Errorf("bad clef: %q", e.Status)
	}
	if scoreKeyName(-2, "major") != "Bb major" || scoreKeyName(3, "minor") != "F# minor" {
		t.Error("key names")
	}
}

// A transposing staff is shown, clicked and edited at written pitch, and
// stored at concert pitch.
func TestScoreTransposingStaff(t *testing.T) {
	s := score.New("t")
	s.Bars = 2
	cl := score.NewLine("Clarinet in Bb", "woodwinds/clarinet", "treble")
	cl.Staves[0].Transpose = score.Interval{Diatonic: -1, Chromatic: -2}
	s.Lines = []*score.Line{cl}
	e := newScoreState(s)
	e.Input = true
	// In concert C the clarinet's written key is D: its F line is F#.
	if p := e.ghost(0, 0, 1, pitchD("F5")); p.String() != "F#5" {
		t.Errorf("written ghost in D = %s", p)
	}
	// In concert Bb it reads in C.
	s.Key = []score.KeyMark{{Bar: 1, Fifths: -2, Mode: "major"}}
	if p := e.ghost(0, 0, 1, pitchD("F5")); p.String() != "F5" {
		t.Errorf("written ghost in C = %s", p)
	}
	e.clickWrite(0, 0, 1, score.Frac{}, pitchD("C5"))
	if got := barOf(e, 0, 0, 1); got != "qBb4 qr hr" {
		t.Errorf("stored = %q, want concert Bb4", got)
	}
	if got := e.describeSel(); !strings.Contains(got, ": C5") {
		t.Errorf("described at written pitch: %q", got)
	}
	// A natural on the written C leaves it alone.
	e.accidental(edit.Natural)
	if got := barOf(e, 0, 0, 1); got != "qBb4 qr hr" {
		t.Errorf("natural on written C = %q", got)
	}
	e.accidental(edit.Sharp)
	if got := barOf(e, 0, 0, 1); got != "qB4 qr hr" {
		t.Errorf("written C# = %q, want concert B4", got)
	}
	// A third above the written C# in written C major is E: concert D.
	e.interval(3, false)
	if got := barOf(e, 0, 0, 1); got != "qB4+D5 qr hr" {
		t.Errorf("third above = %q (written C#+E)", got)
	}
	// In concert view the same staff is edited as it sounds.
	e.Score.Concert = true
	if p := e.ghost(0, 0, 1, pitchD("B4")); p.String() != "Bb4" {
		t.Errorf("concert ghost = %s", p)
	}
}
