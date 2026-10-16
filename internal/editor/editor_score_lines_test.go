package editor

// editor_score_lines_test.go — lines and the mixer through scoreState, and
// through the window's player sync with a real score scheduler rendering
// offline: an instrument swapped while playing (notes unchanged, nothing
// re-prepared, the next note on the new sound), faders and mutes heard live,
// line duplication, percussion entry and the WAV export.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/io/exportset"
	"foundry/internal/score/play"
	"foundry/internal/tracker"
)

// scoreUseTestInstruments makes "dc/<level>" a constant-level instrument
// (so loudness can be read off the output), and "grand/x" one whose
// playback defaults ask for a grand staff, "kit/x" a percussion staff and
// "horn/x" a horn in F.
func scoreUseTestInstruments(t *testing.T) {
	t.Helper()
	prev := play.LoadInstrument
	play.LoadInstrument = func(name string) (*tracker.Instrument, error) {
		var lv float32 = 0.5
		var pb tracker.Playback
		switch {
		case len(name) > 3 && name[:3] == "dc/":
			if _, err := fmt.Sscanf(name, "dc/%g", &lv); err != nil {
				return nil, err
			}
		case name == "grand/x":
			pb.Clef = "grand"
		case name == "kit/x":
			pb.Clef = "percussion"
		case name == "horn/x":
			pb.Clef, pb.Transposition = "treble", -7
		default:
			return nil, fmt.Errorf("no instrument %q", name)
		}
		d := make([]float32, 48000)
		for i := range d {
			d[i] = lv
		}
		in := tracker.NewInstrument("Test "+name, name)
		in.FadeOut = 0.001
		in.Playback = pb
		in.SetLoaded(0, &tracker.Sample{Name: name, Data: d, Channels: 1, Rate: 48000, BaseNote: tracker.NoteC4, Volume: 1,
			Loop: tracker.LoopForward, LoopStart: 0, LoopEnd: len(d)})
		return in, nil
	}
	t.Cleanup(func() { play.LoadInstrument = prev })
}

// scoreWithPlayer puts e in the editor with a player on its score (no
// device: the test renders it), restoring the editor's globals after.
func scoreWithPlayer(t *testing.T, e *scoreState) *play.Player {
	t.Helper()
	prevEd, prevP, prevSet := scEd, scorePlayer, scorePlayerSet
	t.Cleanup(func() {
		scEd, scorePlayer, scorePlayerSet = prevEd, prevP, prevSet
		scoreSentMix, scoreSentMute = nil, nil
	})
	scEd = e
	p, errs := play.New(e.Score.Clone())
	if p == nil || len(errs) > 0 {
		t.Fatal(errs)
	}
	scorePlayer = p
	scoreAdopted()
	return p
}

// scoreRender runs the player for secs seconds in 10 ms blocks.
func scoreRender(p *play.Player, secs float64) []float32 {
	n := int(secs * 48000)
	out := make([]float32, n*2)
	for i := 0; i < n; i += 480 {
		p.Render(out[i*2:(i+480)*2], 480, 48000)
	}
	return out
}

// scoreLevelAt is the left channel near the end of a render.
func scoreLevelAt(out []float32) float32 { return out[len(out)-200] }

func scoreLinesState(t *testing.T) *scoreState {
	t.Helper()
	s := score.New("t")
	s.Bars = 8
	s.Master = nil
	s.Buses.A.Return, s.Buses.B.Return = 0, 0
	for _, n := range []string{"A", "B"} {
		l := score.NewLine(n, "dc/0.5", "treble")
		v, err := score.ParseVoice("| qC4 qC4 qC4 qC4 | qC4 qC4 qC4 qC4 | qC4 qC4 qC4 qC4 | qC4 qC4 qC4 qC4 | wC4 | wC4 | wC4 | wC4 |")
		if err != nil {
			t.Fatal(err)
		}
		l.Staves[0].Voices = []*score.Voice{v}
		s.Lines = append(s.Lines, l)
	}
	s.Lines[0].Pan, s.Lines[1].Pan = -1, 1 // A on the left, B on the right
	return newScoreState(s)
}

// An instrument swapped while the score plays: the notes are the same, the
// player is not handed a new program (the performance is the same one),
// and the next note plays the new instrument.
func TestScoreHotSwapWhilePlaying(t *testing.T) {
	scoreUseTestInstruments(t)
	e := scoreLinesState(t)
	p := scoreWithPlayer(t, e)
	notes := score.FormatVoice(e.Score.Lines[0].Staves[0].Voices[0])
	e.togglePlay(false, 0)
	scoreApplyOps()
	before := scoreRender(p, 0.6)
	perf := p.Performance()
	rev := e.Rev
	e.setInstrument(0, "dc/1", true)
	if e.Score.Lines[0].Instrument != "dc/1" || score.FormatVoice(e.Score.Lines[0].Staves[0].Voices[0]) != notes {
		t.Fatalf("swap changed the line: %+v", e.Score.Lines[0])
	}
	if e.Rev != rev {
		t.Error("an instrument swap counted as a change to the music")
	}
	scoreApplyOps()
	if p.Performance() != perf {
		t.Error("the swap prepared the score again")
	}
	// The note sounding at the swap (the second quarter, 0.5 s) finishes on
	// the old sound; the next (1 s) is twice as loud.
	mid := scoreRender(p, 0.3)
	after := scoreRender(p, 0.4)
	l0, l1, l2 := scoreLevelAt(before), scoreLevelAt(mid), scoreLevelAt(after)
	if l0 == 0 || math.Abs(float64(l1-l0)) > 1e-5 {
		t.Errorf("the note at the swap: %v, was %v", l1, l0)
	}
	if math.Abs(float64(l2-2*l0)) > 1e-4 {
		t.Errorf("the next note: %v, want %v", l2, 2*l0)
	}
	if !e.canUndo() {
		t.Error("the swap is not undoable")
	}
	e.undoStep()
	if e.Score.Lines[0].Instrument != "dc/0.5" {
		t.Errorf("undo: %s", e.Score.Lines[0].Instrument)
	}
}

// Faders, pan, mutes, solos and the master are heard live while playing,
// with no new program; a drag of one fader is one undo step.
func TestScoreLiveMixer(t *testing.T) {
	scoreUseTestInstruments(t)
	e := scoreLinesState(t)
	p := scoreWithPlayer(t, e)
	e.togglePlay(false, 0)
	scoreApplyOps()
	out := scoreRender(p, 0.2)
	left, right := scoreLevelAt(out), out[len(out)-199]
	if left == 0 || right == 0 {
		t.Fatalf("both lines: %v %v", left, right)
	}
	perf, rev := p.Performance(), e.Rev
	// A fader dragged in three steps: one undo step.
	undo := e.undo.len()
	for _, v := range []float32{0.9, 0.7, 0.5} {
		e.setVolume(0, v)
	}
	if e.undo.len() != undo+1 || e.Rev != rev {
		t.Errorf("a drag: %d undo steps, rev %d -> %d", e.undo.len()-undo, rev, e.Rev)
	}
	e.endMixGesture()
	scoreApplyOps()
	out = scoreRender(p, 0.1)
	if l := scoreLevelAt(out); math.Abs(float64(l-left/2)) > 1e-4 {
		t.Errorf("left at half volume: %v, want %v", l, left/2)
	}
	// Mute B, then solo A (B stays silent), then unmute and unsolo.
	e.toggleMute(1)
	scoreApplyOps()
	out = scoreRender(p, 0.1)
	if r := out[len(out)-199]; r != 0 {
		t.Errorf("B muted: %v", r)
	}
	e.toggleMute(1)
	e.toggleSolo(0)
	scoreApplyOps()
	out = scoreRender(p, 0.1)
	if r := out[len(out)-199]; r != 0 || scoreLevelAt(out) == 0 {
		t.Errorf("A soloed: %v %v", scoreLevelAt(out), r)
	}
	e.toggleSolo(0)
	e.setGain(0.5)
	scoreApplyOps()
	out = scoreRender(p, 0.1)
	if r := out[len(out)-199]; math.Abs(float64(r-right/2)) > 1e-4 {
		t.Errorf("master at half: right %v, want %v", r, right/2)
	}
	if p.Performance() != perf {
		t.Error("a mixer change prepared the score again")
	}
	// Undo walks back through the mixer: the master, the solo off and on,
	// the mute off and on, the fader.
	for i := 0; i < 6; i++ {
		e.undoStep()
	}
	if e.Score.Lines[0].Volume != 1 || e.Score.Gain != 1 || e.Score.Lines[1].Mute || e.Score.Lines[0].Solo {
		t.Errorf("after undo: %+v %+v gain %v", e.Score.Lines[0], e.Score.Lines[1], e.Score.Gain)
	}
	// The undo is a change to the score: the player gets it whole.
	scoreApplyOps()
	if p.Performance() == perf {
		t.Error("undo did not reach the player")
	}
	out = scoreRender(p, 0.1)
	if l := scoreLevelAt(out); math.Abs(float64(l-left)) > 1e-4 {
		t.Errorf("after undo the left is %v, want %v", l, left)
	}
}

// A mute heard live and then undone: the undo is a new program whose flags
// match the player's old one, and the line must still come back.
func TestScoreUndoneMuteSounds(t *testing.T) {
	scoreUseTestInstruments(t)
	e := scoreLinesState(t)
	p := scoreWithPlayer(t, e)
	e.togglePlay(false, 0)
	scoreApplyOps()
	right := scoreRender(p, 0.2)[int(0.2*48000)*2-199]
	e.toggleMute(1)
	scoreApplyOps()
	if r := scoreRender(p, 0.1)[int(0.1*48000)*2-199]; r != 0 {
		t.Fatalf("muted: %v", r)
	}
	e.undoStep()
	scoreApplyOps()
	if r := scoreRender(p, 0.1)[int(0.1*48000)*2-199]; math.Abs(float64(r-right)) > 1e-5 {
		t.Errorf("after undoing the mute the right is %v, want %v", r, right)
	}
	// And a line moved under a mute: the strip follows the line.
	e.toggleMute(0)
	scoreApplyOps()
	e.moveLine(0, 1) // the muted A (panned left) is now the second line
	scoreApplyOps()
	out := scoreRender(p, 0.1)
	if l, r := out[len(out)-200], out[len(out)-199]; l != 0 || math.Abs(float64(r-right)) > 1e-5 {
		t.Errorf("after the move: left %v (A, muted), right %v (B), want 0 and %v", l, r, right)
	}
}

// Duplicating, adding, moving and removing lines through scoreState: the
// selection follows, every one is an undo step.
func TestScoreLineCommands(t *testing.T) {
	scoreUseTestInstruments(t)
	e := scoreTestState(t)
	e.toggleInput()
	e.clickWrite(0, 0, 1, score.Frac{}, pitchD("B4"))
	e.toggleInput()
	e.setVolume(0, 0.6)
	if !e.duplicateLine(0, true) {
		t.Fatal(e.Status)
	}
	if got := lineNamesOf(e); got != "Flute,Flute 2,Cello" {
		t.Fatalf("lines %s", got)
	}
	if barOf(e, 1, 0, 1) != barOf(e, 0, 0, 1) || e.Score.Lines[1].Volume != 0.6 {
		t.Errorf("copy with notes: %q, volume %v", barOf(e, 1, 0, 1), e.Score.Lines[1].Volume)
	}
	if q := e.Sel; q.Kind != scoreSelBars || q.LineA != 1 || q.LineB != 1 {
		t.Errorf("the copy is not selected: %+v", q)
	}
	e.duplicateLine(0, false)
	if got := lineNamesOf(e); got != "Flute,Flute 3,Flute 2,Cello" || barOf(e, 1, 0, 1) != "" || e.Score.Lines[1].Instrument != "woodwinds/flute" {
		t.Errorf("empty copy: %s %q", got, barOf(e, 1, 0, 1))
	}
	// Move the cello up past both copies, the selection on it.
	e.selectLine(3)
	e.moveLine(3, -1)
	e.moveLine(2, -1)
	if got := lineNamesOf(e); got != "Flute,Cello,Flute 3,Flute 2" || e.Sel.LineA != 1 {
		t.Errorf("moved: %s, selection on line %d", got, e.Sel.LineA)
	}
	if e.moveLine(0, -1) {
		t.Error("moved the top line up")
	}
	// Remove a line under the selection: it goes; one below shifts up.
	e.selectLine(3)
	e.removeLine(1)
	if got := lineNamesOf(e); got != "Flute,Flute 3,Flute 2" || e.Sel.LineA != 2 {
		t.Errorf("removed: %s, selection on line %d", got, e.Sel.LineA)
	}
	e.removeLine(2)
	if e.Sel.Kind != scoreSelNone {
		t.Errorf("the selected line went but the selection stayed: %+v", e.Sel)
	}
	if !e.renameLine(1, "Piccolo") || e.renameLine(1, "flute") {
		t.Errorf("rename: %s %s", lineNamesOf(e), e.Status)
	}
	// Lines for instruments: a grand staff, a percussion staff, a horn in F.
	e.addLine(len(e.Score.Lines), "grand/x")
	e.addLine(len(e.Score.Lines), "kit/x")
	e.addLine(0, "horn/x")
	if l := e.Score.Lines[len(e.Score.Lines)-2]; len(l.Staves) != 2 || l.Staves[1].Clef != "bass" || l.Name != "Test grand/x" {
		t.Errorf("grand staff line %+v", l)
	}
	if l := e.Score.Lines[len(e.Score.Lines)-1]; l.Staves[0].Clef != "percussion" {
		t.Errorf("percussion line %+v", l.Staves[0])
	}
	if l := e.Score.Lines[0]; l.Staves[0].Transpose.Chromatic != -7 {
		t.Errorf("horn %+v", l.Staves[0])
	}
	steps := 0
	for e.undoStep() {
		steps++
	}
	// Note, fader, two duplicates, two moves, two removes, a rename, three
	// adds; the refused move and rename left none.
	if steps != 12 || lineNamesOf(e) != "Flute,Cello" {
		t.Errorf("undid %d steps to %s", steps, lineNamesOf(e))
	}
}

func lineNamesOf(e *scoreState) string {
	out := ""
	for i, l := range e.Score.Lines {
		if i > 0 {
			out += ","
		}
		out += l.Name
	}
	return out
}

// A percussion staff takes drums by place: the snare in the third space,
// the hi-hat above the staff; a place with no drum, and letters, are
// refused.
func TestScorePercussionEntry(t *testing.T) {
	e := scoreTestState(t)
	e.insertLine(len(e.Score.Lines), scorePercussionLine())
	kit := len(e.Score.Lines) - 1
	e.toggleInput()
	snare := score.PercussionBottom + 5
	if p := e.ghost(kit, 0, 1, snare); p.MIDI() != 38 {
		t.Errorf("ghost in the third space: %s", p)
	}
	e.Armed, e.ArmedAcc = true, 0 // a sharp armed does not touch a drum
	if !e.clickWrite(kit, 0, 1, score.Frac{}, snare) {
		t.Fatal(e.Status)
	}
	e.Dur = score.Duration{Base: score.Eighth}
	e.clickWrite(kit, 0, 1, score.F(1, 4), score.PercussionBottom+9)
	if got := barOf(e, kit, 0, 1); got != "qD2 eF#2 er hr" {
		t.Errorf("drums written: %q", got)
	}
	if got := scoreEventText(e, kit, 0, e.event()); got != "Closed Hi-Hat (8th)" {
		t.Errorf("described: %q", got)
	}
	if e.clickWrite(kit, 0, 1, score.F(1, 2), score.PercussionBottom-4) {
		t.Error("wrote a drum where none sits")
	}
	e.letter(0)
	if got := barOf(e, kit, 0, 1); got != "qD2 eF#2 er hr" {
		t.Errorf("a letter on a percussion staff wrote %q", got)
	}
}

// The export covers what it says, and writes a WAV of that length.
func TestScoreExportAudio(t *testing.T) {
	scoreUseTestInstruments(t)
	e := scoreLinesState(t)
	bar := int64(score.TicksPerWhole)
	if from, to, _, err := e.exportSpan(scoreExportWhole); err != nil || from != 0 || to != 8*bar {
		t.Errorf("whole: %d %d %v", from, to, err)
	}
	if _, _, _, err := e.exportSpan(scoreExportLoop); err == nil {
		t.Error("a loop export with no loop")
	}
	if _, _, _, err := e.exportSpan(scoreExportSelection); err == nil {
		t.Error("a selection export with nothing selected")
	}
	e.setLoop(scoreLoopBars(e.Score, 2, 2))
	if from, to, name, err := e.exportSpan(scoreExportLoop); err != nil || from != bar || to != 2*bar || name != "loop" {
		t.Errorf("loop: %d %d %q %v", from, to, name, err)
	}
	e.clickBar(0, 3, false, false)
	e.clickBar(0, 4, false, true)
	if from, to, name, err := e.exportSpan(scoreExportSelection); err != nil || from != 2*bar || to != 4*bar || name != "bars3-4" {
		t.Errorf("selection: %d %d %q %v", from, to, name, err)
	}
	// Bars 3-4 at 120 (4 s) at the slider's 50% (8 s) plus half a bar of
	// tail (a 4/4 bar at 120 is 2 s; at 50%, 4).
	e.setTempoScale(0.5)
	if got := e.exportSeconds(scoreExportSelection, true); math.Abs(got-8) > 1e-9 {
		t.Errorf("seconds %v", got)
	}
	scEd = e
	d := defaultExportDlg()
	d.Formats = map[exportset.Format]bool{exportset.WAV: true}
	d.What, d.Scaled, d.TailBars = scoreExportSelection, true, 0.25
	d.Dir = filepath.Join(t.TempDir(), "out")
	p, err := e.exportPlan(d)
	if err != nil {
		t.Fatal(err)
	}
	if p.Audio.RangeName != "bars3-4" || p.Audio.TempoScale != 0.5 {
		t.Errorf("plan %+v", p.Audio)
	}
	msg, err := scoreRunExport(e, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(d.Dir, "t_bars3-4.wav")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err, msg)
	}
	// 9 s of 48 kHz 24-bit stereo, after a 44-byte header.
	if want := int64(44 + 9*48000*2*3); fi.Size() != want {
		t.Errorf("file %d bytes, want %d (%s)", fi.Size(), want, msg)
	}
	if !strings.Contains(msg, "Exported 1 file") {
		t.Errorf("message %q", msg)
	}
}

// A score that loops (`loop: true`) exports one pass and its tail: the
// transport stops at the end rather than starting the piece again in the
// tail, so the tail is the last notes' release and then silence.
func TestScoreExportOfALoopingScoreStopsAtTheEnd(t *testing.T) {
	scoreUseTestInstruments(t)
	e := scoreLinesState(t)
	scEd = e
	e.Score.Loop = true
	d := defaultExportDlg()
	d.Formats = map[exportset.Format]bool{exportset.WAV: true}
	d.Bits, d.TailBars, d.Dir = 16, 1, t.TempDir()
	p, err := e.exportPlan(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scoreRunExport(e, p, nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(d.Dir, "t.wav"))
	if err != nil {
		t.Fatal(err)
	}
	data := make([]float32, (len(b)-44)/2)
	for i := range data {
		data[i] = float32(int16(uint16(b[44+2*i])|uint16(b[45+2*i])<<8)) / 32767
	}
	// Eight bars at 120 are 16 s; the music sounds up to there.
	end := 16 * 48000
	if len(data)/2 < end {
		t.Fatalf("rendered %d frames, want at least %d", len(data)/2, end)
	}
	if v := data[(end-4800)*2]; v == 0 {
		t.Error("the last bar is silent")
	}
	// A tenth of a second after the end, nothing sounds: the top did not
	// come round again.
	for i := (end + 4800) * 2; i < len(data); i++ {
		if data[i] != 0 {
			t.Fatalf("sound in the tail at %.2f s: the export looped", float64(i/2)/48000)
		}
	}
}

// A swap to a transposing instrument writes the staff at its
// transposition (as Sibelius does): the notes, the sound and the program
// are unchanged, the page is engraved again, and undo takes it back.
func TestScoreSwapTakesTheInstrumentsTransposition(t *testing.T) {
	scoreUseTestInstruments(t)
	e := scoreLinesState(t)
	scEd = e
	notes := score.FormatVoice(e.Score.Lines[0].Staves[0].Voices[0])
	rev, lanes := e.Rev, e.laneRev
	before := scoreLayout()
	e.setInstrument(0, "horn/x", false)
	st := e.Score.Lines[0].Staves[0]
	if st.Transpose != (score.Interval{Diatonic: -4, Chromatic: -7}) {
		t.Fatalf("the horn's staff: %+v (%s)", st.Transpose, e.Status)
	}
	if score.FormatVoice(st.Voices[0]) != notes || e.Rev != rev || e.laneRev == lanes {
		t.Errorf("notes %q, rev %d→%d, layout rev %d→%d", score.FormatVoice(st.Voices[0]), rev, e.Rev, lanes, e.laneRev)
	}
	if scoreLayout() == before {
		t.Error("the page was not engraved again")
	}
	if e.Score.Lines[1].Staves[0].Transpose != (score.Interval{}) {
		t.Error("the other line moved")
	}
	// Back to an instrument in C: written at sounding pitch again.
	e.setInstrument(0, "dc/1", false)
	if e.Score.Lines[0].Staves[0].Transpose != (score.Interval{}) {
		t.Errorf("back in C: %+v", e.Score.Lines[0].Staves[0].Transpose)
	}
	e.undoStep()
	if e.Score.Lines[0].Instrument != "horn/x" || e.Score.Lines[0].Staves[0].Transpose.Chromatic != -7 {
		t.Errorf("undo: %s %+v", e.Score.Lines[0].Instrument, e.Score.Lines[0].Staves[0].Transpose)
	}
}
