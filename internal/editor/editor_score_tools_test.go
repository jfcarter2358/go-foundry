package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/score"
	"foundry/internal/score/edit"

	"github.com/veandco/go-sdl2/sdl"
)

// toolsState is the test state with music in both lines.
func toolsState(t *testing.T) *scoreState {
	t.Helper()
	e := scoreTestState(t) // G major, Flute and Cello, 4 bars
	set := func(line int, text string) {
		v, err := score.ParseVoice(text)
		if err != nil {
			t.Fatal(err)
		}
		e.Score.Lines[line].Staves[0].Voices = []*score.Voice{v}
	}
	set(0, "| qG4(p) qA4(stacc) qB4 qF#5 | qG5(ferm) qD5 hB4(expr=\"dolce\") | qC5~ qC5 qE5(acc) qG#4 | wG4 |")
	set(1, "| wG2 | wD3 | wC3 | wG2 |")
	e.Score.Lines[0].Chords = []score.ChordSymbol{{At: score.Pos{Bar: 1}, Chord: mustChordE(t, "G")}, {At: score.Pos{Bar: 3}, Chord: mustChordE(t, "C/E")}}
	e.Score.Sections = []score.Section{{Name: "theme", Bar: 1}, {Name: "bridge", Bar: 3}}
	e.touch()
	return e
}

func mustChordE(t *testing.T, s string) score.Chord {
	t.Helper()
	c, err := score.ParseChord(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Transpose: the selection or the whole score, by an interval or to a key,
// one undo step; the keys move only when every line does.
func TestScoreTransposeDialog(t *testing.T) {
	e := toolsState(t)
	e.clickBar(0, 1, false, false)
	f := scoreTrFields{Interval: 2, Up: true, Chords: true, Keys: true} // up a major second
	if !e.transposeSel(f.opts()) {
		t.Fatal(e.Status)
	}
	if got := barOf(e, 0, 0, 1); got != "qA4(p) qB4(stacc) qC#5 qG#5" {
		t.Errorf("bar 1 = %q", got)
	}
	if got := barOf(e, 0, 0, 2); got != "qG5(ferm) qD5 hB4(expr=dolce)" {
		t.Errorf("bar 2 is not selected: %q", got)
	}
	if got := barOf(e, 1, 0, 1); got != "wG2" {
		t.Errorf("the cello is not selected: %q", got)
	}
	if e.Score.KeyAt(1).Fifths != 1 {
		t.Error("one line moving leaves the key")
	}
	if e.Score.Lines[0].Chords[0].Chord.String() != "A" {
		t.Errorf("chord symbol %s", e.Score.Lines[0].Chords[0].Chord)
	}
	if e.Sel.Kind != scoreSelBars || !strings.Contains(e.Status, "+M2") {
		t.Errorf("selection kept, status: %+v %q", e.Sel, e.Status)
	}
	scoreRunCmd2(e, scUndo)
	if got := barOf(e, 0, 0, 1); got != "qG4(p) qA4(stacc) qB4 qF#5" {
		t.Errorf("undo: %q", got)
	}
	// The whole score to E-flat major, the closest way (down a major third).
	e.Sel = scoreSel{}
	f = scoreTrFields{ByKey: true, Fifths: -3, Dir: 0, Chords: true}
	if !e.transposeSel(f.opts()) {
		t.Fatal(e.Status)
	}
	if e.Score.KeyAt(1).Fifths != -3 || e.Score.KeyAt(4).Fifths != -3 {
		t.Errorf("keys %+v", e.Score.Key)
	}
	if got := barOf(e, 0, 0, 1); got != "qEb4(p) qF4(stacc) qG4 qD5" {
		t.Errorf("to Eb: %q", got)
	}
	if got := barOf(e, 1, 0, 2); got != "wBb2" {
		t.Errorf("the cello too: %q", got)
	}
	// Diatonic: the flute's bar 4 up a third in the key (Eb now).
	e.clickBar(0, 4, false, false)
	f = scoreTrFields{Diatonic: true, Steps: 2, Up: true, Chords: true}
	if !e.transposeSel(f.opts()) {
		t.Fatal(e.Status)
	}
	if got := barOf(e, 0, 0, 4); got != "wG4" {
		t.Errorf("Eb4 up a third in Eb: %q", got)
	}
}

// Respell flips every selected note's spelling; spell in key tidies a
// passage the key's way.
func TestScoreRespellAndSpellInKey(t *testing.T) {
	e := toolsState(t)
	e.clickBar(0, 3, false, false)
	if !e.respellSel() {
		t.Fatal(e.Status)
	}
	if got := barOf(e, 0, 0, 3); got != "qB#4~ qB#4 qFb5(acc) qAb4" {
		t.Errorf("respelled %q", got)
	}
	if !e.spellInKeySel(false) {
		t.Fatal(e.Status)
	}
	// G major: C, E; G# falling from E is spelled Ab.
	if got := barOf(e, 0, 0, 3); got != "qC5~ qC5 qE5(acc) qAb4" {
		t.Errorf("in the key %q", got)
	}
	if e.spellInKeySel(false) || !strings.Contains(e.Status, "already") {
		t.Errorf("a second time changes nothing: %q", e.Status)
	}
	// One selected note of a chord respells alone.
	e.selectEvent(edit.Loc{Line: 0, Bar: 1, Event: 3}, 0)
	e.respellSel()
	if got := barOf(e, 0, 0, 1); got != "qG4(p) qA4(stacc) qB4 qGb5" {
		t.Errorf("one note: %q", got)
	}
}

// Find: pitches (by sound, by spelling, classes, ranges), marks by their
// musical names, texts in marks, lyrics, chord symbols and rehearsal marks;
// Next and Previous walk the matches from the selection, wrapping round.
func TestScoreFind(t *testing.T) {
	e := toolsState(t)
	find := func(q string, k scoreFindKind) []scoreFindHit {
		e.Find = scoreFind{Query: q, Kind: k}
		return e.findAll(&e.Find)
	}
	if h := find("G4", scoreFindAny); len(h) != 2 || h[0].Bar != 1 || h[1].Bar != 4 {
		t.Errorf("G4: %+v", h)
	}
	if h := find("G", scoreFindPitch); len(h) != 6 {
		t.Errorf("every G (flute 3, cello 2, the chord symbol): %d", len(h))
	}
	if h := find("Ab4", scoreFindAny); len(h) != 1 || h[0].Bar != 3 {
		t.Errorf("Ab4 by sound finds the G#: %+v", h)
	}
	e.Find = scoreFind{Query: "Ab4", Exact: true}
	if h := e.findAll(&e.Find); len(h) != 0 {
		t.Errorf("Ab4 spelled exactly: %+v", h)
	}
	if h := find("C5..E5", scoreFindAny); len(h) != 4 {
		t.Errorf("C5..E5: %d %+v", len(h), h)
	}
	if h := find("staccato", scoreFindAny); len(h) != 1 || h[0].Loc.Event != 1 {
		t.Errorf("staccato: %+v", h)
	}
	if h := find("fermata", scoreFindMark); len(h) != 1 || h[0].Bar != 2 {
		t.Errorf("fermata: %+v", h)
	}
	if h := find("tie", scoreFindMark); len(h) != 1 || h[0].Bar != 3 {
		t.Errorf("tie: %+v", h)
	}
	if h := find("dynamic", scoreFindMark); len(h) != 1 {
		t.Errorf("dynamic: %+v", h)
	}
	if h := find("dol", scoreFindAny); len(h) != 1 || h[0].Bar != 2 {
		t.Errorf("dolce: %+v", h)
	}
	if h := find("bridge", scoreFindText); len(h) != 1 || h[0].Line != -1 || h[0].Bar != 3 {
		t.Errorf("rehearsal mark: %+v", h)
	}
	if h := find("C/E", scoreFindText); len(h) != 1 || h[0].Bar != 3 {
		t.Errorf("chord symbol: %+v", h)
	}
	if h := find("Q#", scoreFindPitch); len(h) != 0 || e.Find.Err == "" {
		t.Errorf("not a pitch: %+v %q", h, e.Find.Err)
	}
	if h := find("wibble", scoreFindMark); len(h) != 0 || e.Find.Err == "" {
		t.Errorf("not a mark: %q", e.Find.Err)
	}
	// Next from a selection in bar 2: the next G is bar 4's; then round to bar 1.
	e.clickBar(0, 2, false, false)
	e.Find = scoreFind{Query: "G4"}
	if !e.find(&e.Find) || e.Sel.Kind != scoreSelEvent || e.Sel.Loc.Bar != 4 {
		t.Fatalf("find from bar 2: %+v %q", e.Sel, e.Status)
	}
	scoreRunCmd2(e, scFindNext)
	if e.Sel.Loc.Bar != 1 || !strings.Contains(e.Status, "1 of 2") {
		t.Errorf("wrapped: %+v %q", e.Sel, e.Status)
	}
	scoreRunCmd2(e, scFindPrev)
	if e.Sel.Loc.Bar != 4 {
		t.Errorf("previous: %+v", e.Sel)
	}
	// In the selection only.
	e.clickBar(1, 1, false, false)
	e.Find = scoreFind{Query: "G", InSel: true}
	if h := e.findAll(&e.Find); len(h) != 1 || h[0].Line != 1 {
		t.Errorf("in the selection: %+v", h)
	}
}

// The palette lists every command with its keys, and a query narrows it
// best first.
func TestScoreCommandPalette(t *testing.T) {
	items := scorePaletteItems()
	seen := map[scoreCmd]bool{}
	for _, it := range items {
		seen[it.Cmd] = true
	}
	for c := scoreCmd(1); c < scCmdCount; c++ {
		if scoreCmdInfos[c].Name == "" {
			continue
		}
		if !seen[c] {
			t.Errorf("the palette leaves out %s", scoreCmdInfos[c].Name)
		}
	}
	first := func(q string) scorePaletteItem {
		m := scorePaletteMatch(items, q)
		if len(m) == 0 {
			t.Fatalf("%q matches nothing", q)
		}
		return items[m[0]]
	}
	if it := first("transpose"); it.Cmd != scTranspose {
		t.Errorf("transpose: %+v", it)
	}
	if it := first("pal"); it.Cmd != scPalette || it.keys() != "Ctrl+Shift+P" {
		t.Errorf("palette: %+v %q", it, it.keys())
	}
	if it := first("voice 2"); it.Cmd != scVoice1+1 {
		t.Errorf("voice 2: %+v", it)
	}
	if it := first("ctrl+shift+f"); it.Cmd != scRecord {
		t.Errorf("by its keys: %+v", it)
	}
	if m := scorePaletteMatch(items, "zzzz"); len(m) != 0 {
		t.Errorf("nonsense matches %d", len(m))
	}
	if m := scorePaletteMatch(items, ""); len(m) != len(items) {
		t.Errorf("an empty query lists everything: %d of %d", len(m), len(items))
	}
	// Return runs the item and closes the palette.
	prev := scEd
	scEd = toolsState(t)
	defer func() { scEd = prev }()
	scoreOpenPalette()
	scorePalette.Query = "select all"
	scorePalette.shown = scorePaletteMatch(scorePalette.items, scorePalette.Query)
	if it := scorePalette.items[scorePalette.shown[0]]; it.Cmd != scSelectAll {
		t.Fatalf("select all: %+v", it)
	}
	if !scorePaletteKey(sdlReturn) || scorePalette.Open {
		t.Fatal("Return should run and close")
	}
	if scEd.Sel.Kind != scoreSelBars || scEd.Sel.BarB != 4 || scEd.Sel.LineB != 1 {
		t.Errorf("select all ran: %+v", scEd.Sel)
	}
}

// sdlReturn is the Return key's scancode.
const sdlReturn = sdl.SCANCODE_RETURN

// The time signature dialog's re-barring: the music keeps its time across
// the new barlines, one undo step.
func TestScoreRebarMeter(t *testing.T) {
	e := scoreTestState(t)
	v, _ := score.ParseVoice("| qG4 qA4 qB4 qC5 | hD5 hE5 |")
	e.Score.Lines[0].Staves[0].Voices = []*score.Voice{v}
	e.touch()
	m, _ := score.ParseMeter("3/4")
	e.rebarMeter(1, m)
	if got := barOf(e, 0, 0, 2); got != "qC5 hD5" {
		t.Errorf("bar 2 = %q (%s)", got, e.Status)
	}
	if !strings.Contains(e.Status, "re-barred") {
		t.Errorf("status %q", e.Status)
	}
	scoreRunCmd2(e, scUndo)
	if got := barOf(e, 0, 0, 1); got != "qG4 qA4 qB4 qC5" {
		t.Errorf("undo: %q", got)
	}
}

// A header dragged to another place moves its line there; the selection
// goes with it; one undo step.
func TestScoreMoveLineTo(t *testing.T) {
	e := scoreTestState(t)
	e.Score.Lines = append(e.Score.Lines, score.NewLine("Oboe", "woodwinds/oboe", "treble"), score.NewLine("Horn", "brass/horn", "treble"))
	e.clickBar(3, 2, false, false) // the horn
	if !e.moveLineTo(3, 0) {
		t.Fatal(e.Status)
	}
	var names []string
	for _, l := range e.Score.Lines {
		names = append(names, l.Name)
	}
	if strings.Join(names, ",") != "Horn,Flute,Cello,Oboe" {
		t.Errorf("order %v", names)
	}
	if e.Sel.LineA != 0 || e.Sel.LineB != 0 {
		t.Errorf("the selection stays on the horn: %+v", e.Sel)
	}
	e.moveLineTo(0, 2)
	names = names[:0]
	for _, l := range e.Score.Lines {
		names = append(names, l.Name)
	}
	if strings.Join(names, ",") != "Flute,Cello,Horn,Oboe" || e.Sel.LineA != 2 {
		t.Errorf("order %v, selection %+v", names, e.Sel)
	}
	scoreRunCmd2(e, scUndo)
	if e.Score.Lines[0].Name != "Horn" {
		t.Errorf("undo: %s first", e.Score.Lines[0].Name)
	}
	if e.moveLineTo(1, 1) {
		t.Error("a line moved onto itself")
	}
}

// A score Claude wrote: the Score editor holding it unedited re-reads it;
// holding edits of its own, it keeps them (a conflict).
func TestScoreFileChangedByClaude(t *testing.T) {
	dir := t.TempDir()
	prevRes, prevEd, prevState := fileutil.ResourcesPath, scEd, fileutil.UserDataRoot
	fileutil.ResourcesPath = dir
	// Opening a score notes it in score.yaml's recent list: keep that here.
	fileutil.UserDataRoot = t.TempDir()
	t.Cleanup(func() {
		fileutil.ResourcesPath, scEd, fileutil.UserDataRoot = prevRes, prevEd, prevState
		score.Reload()
	})
	os.MkdirAll(filepath.Join(dir, "sounds"), 0o755)
	s := score.New("tune")
	s.Lines = []*score.Line{score.NewLine("Flute", "woodwinds/flute", "treble")}
	if err := score.Save("tune", s); err != nil {
		t.Fatal(err)
	}
	scEd = newScoreState(score.New("x"))
	if err := scoreLoad("tune"); err != nil {
		t.Fatal(err)
	}
	// Claude adds a bar.
	s.Bars = 3
	path := filepath.Join(dir, "sounds", "tune"+score.FileExt)
	if err := score.SaveFile(path, s); err != nil {
		t.Fatal(err)
	}
	if !scoreFileChanged(path) || scEd.Score.Measures() != 3 {
		t.Errorf("unedited: re-read %d bars", scEd.Score.Measures())
	}
	// An edit of the editor's own, then Claude again: a conflict.
	scEd.Score.Title = "Mine"
	scEd.touch()
	s.Bars = 5
	score.SaveFile(path, s)
	if scoreFileChanged(path) || scEd.Score.Title != "Mine" {
		t.Errorf("edited: the editor's edit went (%q)", scEd.Score.Title)
	}
	if scoreNameOf(filepath.Join(dir, "elsewhere.fscore")) != "" {
		t.Error("a score outside sounds/ has a library name")
	}
}

// Ctrl+Shift+Up and Down put a grand staff's notes on the other staff and
// back; a line of one staff has nowhere to put them.
func TestScoreMoveStaff(t *testing.T) {
	e := scoreTestState(t)
	piano := score.NewLine("Piano", "keys/piano", "treble")
	piano.Staves = append(piano.Staves, &score.Staff{Clef: "bass", Voices: []*score.Voice{{}}})
	low, _ := score.ParseVoice("| qC3 qG3 qC4 qE4 |")
	piano.Staves[1].Voices = []*score.Voice{low}
	e.Score.Lines = append(e.Score.Lines, piano)
	e.touch()
	e.selectEvent(edit.Loc{Line: 2, Staff: 1, Bar: 1, Event: 2}, -1)
	scoreRunCmd2(e, scCrossUp)
	if got := score.FormatMeasure(e.Score.Lines[2].Staves[1].Voices[0].Measure(1)); got != "qC3 qG3 qC4(cross=up) qE4" {
		t.Errorf("after Ctrl+Shift+Up: %q (%s)", got, e.Status)
	}
	scoreRunCmd2(e, scCrossUp)
	if !strings.Contains(e.Status, "no staff above") {
		t.Errorf("past the top: %q", e.Status)
	}
	scoreRunCmd2(e, scCrossDown)
	if got := score.FormatMeasure(e.Score.Lines[2].Staves[1].Voices[0].Measure(1)); got != "qC3 qG3 qC4 qE4" {
		t.Errorf("back: %q", got)
	}
	// Bars: every chord that can go.
	e.clickBarStaff(2, 1, 1, false, false)
	scoreRunCmd2(e, scCrossUp)
	if got := score.FormatMeasure(e.Score.Lines[2].Staves[1].Voices[0].Measure(1)); got != "qC3(cross=up) qG3(cross=up) qC4(cross=up) qE4(cross=up)" {
		t.Errorf("bars: %q", got)
	}
	// The flute has one staff.
	v, _ := score.ParseVoice("| wC5 |")
	e.Score.Lines[0].Staves[0].Voices = []*score.Voice{v}
	e.selectEvent(edit.Loc{Line: 0, Bar: 1, Event: 0}, -1)
	if e.moveStaff(1) || !strings.Contains(e.Status, "no staff below") {
		t.Errorf("one staff: %q", e.Status)
	}
}
