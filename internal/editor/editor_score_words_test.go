package editor

// editor_score_words_test.go — lyrics and chord symbols typed in place,
// drums by their kit's letters, frets on a tablature, and the line
// settings for them, all through scoreState with no window.

import (
	"fmt"
	"testing"
	"time"

	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/tracker"
)

func wordsState(t *testing.T, voice string) *scoreState {
	t.Helper()
	e := scoreTestState(t)
	v, err := score.ParseVoice(voice)
	if err != nil {
		t.Fatal(err)
	}
	e.Score.Lines[0].Staves[0].Voices[0] = v
	return e
}

// A sung line typed as Sibelius has it: syllables, hyphens, a melisma by
// Space, a rest and a tie passed over, Backspace back, a second verse.
func TestScoreLyricEntry(t *testing.T) {
	e := wordsState(t, "| qD5 qE5 qF#5 qr | hG5~ qG5 qA5 | wB5 |")
	e.selectEvent(edit.Loc{Bar: 1}, -1)
	e.startLyrics(1)
	if e.Words == nil || e.Words.Loc != (edit.Loc{Bar: 1}) {
		t.Fatalf("field %+v", e.Words)
	}
	for _, typed := range []string{"A", "A-", "ma", "ma-", "zing ", "grace "} {
		e.wordsInput(typed)
	}
	if got := barOf(e, 0, 0, 1); got != "qD5(ly=A-) qE5(ly=ma-) qF#5(ly=zing) qr" {
		t.Errorf("bar 1: %s", got)
	}
	// "grace" went on the G past the rest; the tied G and its tie were
	// passed over.
	if got := barOf(e, 0, 0, 2); got != "hG5~(ly=grace) qG5 qA5" {
		t.Errorf("bar 2: %s", got)
	}
	if e.Words.Loc != (edit.Loc{Bar: 2, Event: 2}) {
		t.Errorf("the field is at %+v, want the A", e.Words.Loc)
	}
	// Space with nothing typed holds "grace" over the A.
	e.wordsInput(" ")
	if got := barOf(e, 0, 0, 2); got != "hG5~(ly=grace_) qG5 qA5" {
		t.Errorf("melisma: %s", got)
	}
	// Backspace in the empty field goes back to the A, then the G.
	e.wordsKey(scoreWordBackspace)
	e.wordsKey(scoreWordBackspace)
	if e.Words.Loc != (edit.Loc{Bar: 2}) || e.Words.Text != "grace" {
		t.Errorf("back at %+v %q", e.Words.Loc, e.Words.Text)
	}
	e.wordsInput("Grace")
	e.wordsKey(scoreWordEnter)
	if e.Words != nil {
		t.Fatal("Enter leaves the field open")
	}
	if got := barOf(e, 0, 0, 2); got != "hG5~(ly=Grace) qG5 qA5" {
		t.Errorf("retyped: %s", got)
	}
	// One undo step a syllable.
	e.undoStep()
	if got := barOf(e, 0, 0, 2); got != "hG5~(ly=grace_) qG5 qA5" {
		t.Errorf("undone: %s", got)
	}
	// Verse 2 from the first note, and Ctrl+Alt+L on to verse 3.
	e.selectEvent(edit.Loc{Bar: 1}, -1)
	e.startLyrics(2)
	e.wordsInput("Glo- ")
	if got := barOf(e, 0, 0, 1); got != "qD5(ly=A-,ly2=Glo-) qE5(ly=ma-) qF#5(ly=zing) qr" {
		t.Errorf("verse 2: %s", got)
	}
	e.wordsKey(scoreWordNextVerse)
	if e.Words.Verse != 3 {
		t.Errorf("verse %d", e.Words.Verse)
	}
	e.wordsKey(scoreWordEscape)
	// To the end of the voice: the field closes.
	e.selectEvent(edit.Loc{Bar: 3}, -1)
	e.startLyrics(1)
	e.wordsInput("men ")
	if e.Words != nil || barOf(e, 0, 0, 3) != "wB5(ly=men)" {
		t.Errorf("at the end: %+v %s", e.Words, barOf(e, 0, 0, 3))
	}
	// A rest selected starts at the next note.
	e.selectEvent(edit.Loc{Bar: 1, Event: 3}, -1)
	e.startLyrics(1)
	if e.Words == nil || e.Words.Loc != (edit.Loc{Bar: 2}) {
		t.Errorf("from a rest: %+v", e.Words)
	}
}

// Chord symbols typed and spaced along beats and notes; one that will not
// read is refused and stays to be fixed; an empty field takes one away; a
// bar selection starts at its first beat.
func TestScoreChordSymbolEntry(t *testing.T) {
	e := wordsState(t, "| hD5 qE5 eF#5 eG5 | wA5 |")
	e.clickBar(0, 1, false, false)
	e.startChord()
	if e.Words == nil || e.Words.Kind != scoreWordChord || e.Words.At != (score.Pos{Bar: 1}) {
		t.Fatalf("field %+v", e.Words)
	}
	e.wordsInput("G ")
	if w := e.Words; w.At.Bar != 1 || !w.At.Offset.Eq(score.F(1, 4)) {
		t.Errorf("after G at %s", e.Score.FormatPos(w.At))
	}
	e.wordsInput(" ")        // nothing on beat 2
	e.wordsInput("Em7(b5) ") // beat 3
	e.wordsInput("Xm9 ")     // refused
	if e.Words == nil || e.Words.Text != "Xm9" || e.Score.FormatPos(e.Words.At) != "1:4" {
		t.Fatalf("a bad symbol: %+v %q", e.Words, e.Status)
	}
	e.wordsInput("D7/F#")
	e.wordsKey(scoreWordTab)
	if e.Score.FormatPos(e.Words.At) != "1:4+1/2" {
		t.Errorf("Tab moved to %s", e.Score.FormatPos(e.Words.At))
	}
	e.wordsKey(scoreWordEscape)
	var got []string
	for _, c := range e.Score.Lines[0].Chords {
		got = append(got, e.Score.FormatPos(c.At)+" "+c.Chord.String())
	}
	if fmt.Sprint(got) != "[1 G 1:3 Em7b5 1:4 D7/F#]" {
		t.Errorf("symbols %v", got)
	}
	// Editing one and clearing it.
	e.editChordAt(0, score.Pos{Bar: 1, Offset: score.F(1, 2)})
	if e.Words.Text != "Em7b5" {
		t.Errorf("loaded %q", e.Words.Text)
	}
	e.wordsInput("")
	e.wordsKey(scoreWordEnter)
	if len(e.Score.Lines[0].Chords) != 2 {
		t.Errorf("cleared: %v", e.Score.Lines[0].Chords)
	}
	e.undoStep()
	if len(e.Score.Lines[0].Chords) != 3 {
		t.Errorf("undo: %v", e.Score.Lines[0].Chords)
	}
	// Backspace in an empty field goes back a place.
	e.editChordAt(0, score.Pos{Bar: 2})
	e.wordsKey(scoreWordBackspace)
	if e.Score.FormatPos(e.Words.At) != "1:4+1/2" {
		t.Errorf("back to %s", e.Score.FormatPos(e.Words.At))
	}
}

// useTestKit gives every line the drum map of a small kit.
func useTestKit(t *testing.T) {
	t.Helper()
	prev := score.LoadKitInstrument
	kit := &tracker.Instrument{Name: "Test Kit", Playback: tracker.Playback{Clef: "percussion", Drums: []tracker.DrumNote{
		{Key: 36, Name: "Kick", Line: 1, Head: "normal", Shortcut: "B"},
		{Key: 38, Name: "Snare", Line: 5, Head: "normal", Shortcut: "A"},
		{Key: 42, Name: "Hat", Line: 9, Head: "x", Shortcut: "G"},
		{Key: 46, Name: "Open Hat", Line: 9, Head: "x", Articulation: "open"},
	}}}
	score.LoadKitInstrument = func(string) (*tracker.Instrument, time.Time, error) { return kit, time.Unix(1, 0), nil }
	score.ReloadKits()
	t.Cleanup(func() { score.LoadKitInstrument = prev; score.ReloadKits() })
}

// Drums by their kit's letters, Shift adding to the chord, a click by the
// kit's places, Up and Down through the kit.
func TestScoreDrumEntry(t *testing.T) {
	useTestKit(t)
	e := scoreTestState(t)
	e.Score.Lines[0] = score.NewLine("Drums", "percussion/test", "percussion")
	e.clickBar(0, 1, false, false)
	e.toggleInput()
	scoreRunCmd2(e, scLetterC+5) // A: the snare
	scoreRunCmd2(e, scAddC+4)    // Shift+G: the hat with it
	scoreRunCmd2(e, scLetterC+6) // B: the kick
	if got := barOf(e, 0, 0, 1); got != "qD2+F#2 qC2 hr" {
		t.Errorf("bar %s", got)
	}
	scoreRunCmd2(e, scLetterC) // C: not in this kit
	if e.Status != "Test Kit has no drum on C" {
		t.Errorf("status %q", e.Status)
	}
	if !e.clickWrite(0, 0, 1, score.F(1, 2), score.PercussionBottom+9) || barOf(e, 0, 0, 1) != "qD2+F#2 qC2 qF#2 qr" {
		t.Errorf("click on the hat's place: %s %s", barOf(e, 0, 0, 1), e.Status)
	}
	// Up from the hat: the open hat (same place, next in the map).
	e.toggleInput()
	e.selectEvent(edit.Loc{Bar: 1, Event: 2}, -1)
	scoreRunCmd2(e, scSemiUp)
	if got := barOf(e, 0, 0, 1); got != "qD2+F#2 qC2 qA#2 qr" {
		t.Errorf("up: %s", got)
	}
	scoreRunCmd2(e, scSemiDown)
	scoreRunCmd2(e, scSemiDown)
	if got := barOf(e, 0, 0, 1); got != "qD2+F#2 qC2 qD2 qr" {
		t.Errorf("down twice: %s (%s)", got, e.Status)
	}
	if n := scoreEventText(e, 0, 0, edit.Event(e.Score, edit.Loc{Bar: 1})); n != "Snare + Hat (quarter)" {
		t.Errorf("named %q", n)
	}
}

// scoreRunCmd2 runs a command on an editor that is not scEd.
func scoreRunCmd2(e *scoreState, c scoreCmd) {
	prev := scEd
	scEd = e
	defer func() { scEd = prev }()
	scoreRunCmd(c)
}

// Frets typed onto a guitar's tablature: a click on a string writes it
// open, digits set the fret (two in a second: 10 and up), Up and Down move
// the hand between strings, Alt moves a note to another string.
func TestScoreTabEntry(t *testing.T) {
	e := scoreTestState(t)
	e.Score.Key = nil
	e.Score.Lines[0] = score.NewLine("Guitar", "plucked/guitar_nylon", "treble_8vb")
	if !e.setLineTab(0, 0, "guitar", false) || e.Score.Lines[0].Staves[0].Tab == nil {
		t.Fatal(e.Status)
	}
	e.toggleInput()
	if !e.clickTab(0, 0, 1, score.Frac{}, 2) {
		t.Fatal(e.Status)
	}
	if got := barOf(e, 0, 0, 1); got != "qB3(str=2) qr hr" {
		t.Errorf("open B string: %s", got)
	}
	if !e.tabFocus() {
		t.Fatal("the tablature should have the keyboard")
	}
	e.tabDigit(1)
	if got := barOf(e, 0, 0, 1); got != "qC4(str=2) qr hr" {
		t.Errorf("fret 1: %s", got)
	}
	e.tabDigit(2) // a second digit within the second: fret 12
	if got := barOf(e, 0, 0, 1); got != "qB4(str=2) qr hr" {
		t.Errorf("fret 12: %s", got)
	}
	e.fretTyped.at = time.Now().Add(-2 * time.Second)
	e.tabDigit(3) // later: fret 3
	if got := barOf(e, 0, 0, 1); got != "qD4(str=2) qr hr" {
		t.Errorf("fret 3: %s", got)
	}
	// Down a string (the G) and a fret there joins the chord.
	scoreRunCmd2(e, scSemiDown)
	if e.TabString != 3 {
		t.Errorf("string %d", e.TabString)
	}
	e.tabDigit(2)
	if got := barOf(e, 0, 0, 1); got != "qA3+D4(str=A3/3+D4/2) qr hr" {
		t.Errorf("a chord: %s", got)
	}
	// Alt+Up moves the A to the D string (fret 7)... it is selected.
	scoreRunCmd2(e, scStepDown)
	if p := edit.TabPlaces(e.Score, edit.Loc{Bar: 1}); p[0].String != 4 || p[0].Fret != 7 {
		t.Errorf("moved: %+v (%s)", p, e.Status)
	}
	// Capo and taking it off; undo brings it back.
	e.setTabCapo(0, 0, 2)
	if e.Score.Lines[0].Staves[0].Tab.Capo != 2 {
		t.Error("capo")
	}
	e.setLineTab(0, 0, "", false)
	if e.Score.Lines[0].Staves[0].Tab != nil {
		t.Error("tablature off")
	}
	e.undoStep()
	if tb := e.Score.Lines[0].Staves[0].Tab; tb == nil || tb.Capo != 2 {
		t.Errorf("undone: %+v", tb)
	}
	// Away from the tablature the digits are not frets.
	e.TabKeys = false
	if e.tabFocus() {
		t.Error("focus without a click on the strings")
	}
}

func TestScoreChordPlaySettings(t *testing.T) {
	e := scoreTestState(t)
	if !e.setChordPlay(0, score.ChordsPad, "") {
		t.Fatal(e.Status)
	}
	cp := e.Score.Lines[0].ChordPlay
	if cp == nil || cp.Style != score.ChordsPad || cp.Volume != 1 {
		t.Fatalf("%+v", cp)
	}
	e.setChordPlay(0, score.ChordsComp, "synth/warm_pad")
	if cp := e.Score.Lines[0].ChordPlay; cp.Style != score.ChordsComp || cp.Instrument != "synth/warm_pad" {
		t.Errorf("%+v", cp)
	}
	e.setChordPlay(0, score.ChordsSilent, "")
	if e.Score.Lines[0].ChordPlay != nil {
		t.Error("off")
	}
	e.undoStep()
	if e.Score.Lines[0].ChordPlay == nil {
		t.Error("undo")
	}
}
