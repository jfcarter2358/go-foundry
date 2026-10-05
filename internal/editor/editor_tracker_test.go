package editor

// editor_tracker_test.go — the tracker editor's logic, without a window.
//
// Everything here runs on a trackerState built in the test, which is why the
// editing model (editor_tracker_edit.go), the order and instrument operations
// and the sample DSP were kept apart from the draw calls: the keyboard's
// meaning, the cursor, the clipboard, the selection tools and undo can all be
// exercised with no GL context and no ImGui frame.

import (
	"math"
	"strings"
	"testing"

	"foundry/internal/tracker"

	"github.com/veandco/go-sdl2/sdl"
)

// trkTestState is a fresh editor on a 4-channel, 64-row song with two
// instruments.
func trkTestState() *trackerState {
	s := tracker.NewSong("t")
	s.Instruments = []*tracker.Instrument{tracker.NewInstrument("A", ""), tracker.NewInstrument("B", "")}
	return newTrackerState(s)
}

func trkCellAtT(t *trackerState, r, c int) tracker.Cell { return t.pattern().Cells[r][c] }

// ---------- the keyboard ----------

func TestTrackerPianoKeys(t *testing.T) {
	cases := []struct {
		sc     sdl.Scancode
		octave int
		want   string
	}{
		{sdl.SCANCODE_Z, 4, "C-4"},
		{sdl.SCANCODE_S, 4, "C#4"},
		{sdl.SCANCODE_M, 4, "B-4"},
		{sdl.SCANCODE_COMMA, 4, "C-5"},
		{sdl.SCANCODE_Q, 4, "C-5"},
		{sdl.SCANCODE_2, 4, "C#5"},
		{sdl.SCANCODE_U, 4, "B-5"},
		{sdl.SCANCODE_I, 4, "C-6"},
		{sdl.SCANCODE_Z, 0, "C-0"},
	}
	for _, c := range cases {
		n, ok := trkNoteForKey(c.sc, c.octave)
		if !ok || tracker.NoteName(n) != c.want {
			t.Errorf("key %v octave %d = %s, %v; want %s", c.sc, c.octave, tracker.NoteName(n), ok, c.want)
		}
	}
	if _, ok := trkNoteForKey(sdl.SCANCODE_P, 9); ok {
		t.Error("a note past B-9 should not exist")
	}
	if _, ok := trkNoteForKey(sdl.SCANCODE_1, 4); ok {
		t.Error("1 is note-off, not a piano key")
	}
}

func TestTrackerHexAndLetterKeys(t *testing.T) {
	for sc, want := range map[sdl.Scancode]int{sdl.SCANCODE_0: 0, sdl.SCANCODE_1: 1, sdl.SCANCODE_9: 9,
		sdl.SCANCODE_A: 10, sdl.SCANCODE_F: 15, sdl.SCANCODE_KP_7: 7} {
		if d, ok := trkHexForKey(sc); !ok || d != want {
			t.Errorf("hex %v = %d, %v; want %d", sc, d, ok, want)
		}
	}
	if _, ok := trkHexForKey(sdl.SCANCODE_G); ok {
		t.Error("G is not a hex digit")
	}
	if trkLetterForKey(sdl.SCANCODE_V) != 'V' || trkLetterForKey(sdl.SCANCODE_1) != 0 {
		t.Error("letter mapping")
	}
}

// ---------- cursor ----------

func TestTrackerCursorMovement(t *testing.T) {
	e := trkTestState()
	n := trkFieldCount(e.Song.FXColumns)
	e.moveField(-1)
	if e.Cur.Ch != 0 || e.Cur.Field != 0 {
		t.Fatalf("left of the first field should stay put: %+v", e.Cur)
	}
	e.moveField(n)
	if e.Cur.Ch != 1 || e.Cur.Field != 0 {
		t.Fatalf("moving a cell's worth of fields should land on the next channel: %+v", e.Cur)
	}
	e.moveField(-1)
	if e.Cur.Ch != 0 || e.Cur.Field != n-1 {
		t.Fatalf("left from a note field goes to the previous channel's last field: %+v", e.Cur)
	}
	e.nextChannel(2)
	if e.Cur.Ch != 2 || e.Cur.Field != 0 {
		t.Fatalf("Tab: %+v", e.Cur)
	}
	e.nextChannel(10)
	if e.Cur.Ch != 3 {
		t.Fatalf("Tab past the last channel stays on it: %+v", e.Cur)
	}

	e.end()
	if e.Cur.Row != 63 {
		t.Fatalf("End: row %d", e.Cur.Row)
	}
	e.home()
	if e.Cur.Row != 0 || e.Cur.Ch != 3 {
		t.Fatalf("first Home goes to row 0 only: %+v", e.Cur)
	}
	e.home()
	if e.Cur.Ch != 0 {
		t.Fatalf("second Home goes to the first channel: %+v", e.Cur)
	}

	e.moveRows(e.barRows())
	if e.Cur.Row != 16 {
		t.Fatalf("PgDn moves a bar (16 rows at 4x4), got %d", e.Cur.Row)
	}
}

func TestTrackerCursorCrossesOrderEntries(t *testing.T) {
	e := trkTestState()
	e.Song.Patterns = append(e.Song.Patterns, tracker.NewPattern("", 32, 4))
	e.Song.Order = []int{0, 1}
	e.Cur.Row = 62
	e.moveRows(4)
	if e.Cur.Order != 1 || e.Cur.Row != 2 {
		t.Fatalf("down past the end should continue in the next entry: %+v", e.Cur)
	}
	e.moveRows(-5)
	if e.Cur.Order != 0 || e.Cur.Row != 61 {
		t.Fatalf("up past the top should continue in the previous entry: %+v", e.Cur)
	}
	e.Cur = trkCursor{Order: 1, Row: 30}
	e.moveRows(10)
	if e.Cur.Order != 1 || e.Cur.Row != 31 {
		t.Fatalf("the end of the song stops: %+v", e.Cur)
	}
}

// ---------- entry ----------

func TestTrackerNoteEntryAdvancesByStep(t *testing.T) {
	e := trkTestState()
	e.Instr, e.Step = 2, 3
	if n := e.typeKey(sdl.SCANCODE_Z); tracker.NoteName(n) != "C-4" {
		t.Fatalf("Z audition = %s", tracker.NoteName(n))
	}
	c := trkCellAtT(e, 0, 0)
	if tracker.NoteName(c.Note) != "C-4" || c.Instr != 2 {
		t.Fatalf("cell after Z = %s", tracker.FormatCell(c, 2))
	}
	if e.Cur.Row != 3 {
		t.Fatalf("cursor should advance by the edit step, at %d", e.Cur.Row)
	}
	e.typeKey(sdl.SCANCODE_1)
	if trkCellAtT(e, 3, 0).Note != tracker.NoteOff {
		t.Fatal("1 should enter a note-off")
	}

	e.Step = 0
	e.Cur.Row = 10
	e.typeKey(sdl.SCANCODE_X)
	if e.Cur.Row != 10 {
		t.Fatal("step 0 should not move the cursor")
	}

	e.EditMode = false
	e.Cur.Row = 20
	if n := e.typeKey(sdl.SCANCODE_C); tracker.NoteName(n) != "E-4" {
		t.Fatal("with edit mode off the key still auditions")
	}
	if !trkCellAtT(e, 20, 0).IsEmpty() {
		t.Fatal("with edit mode off nothing is written")
	}
}

func TestTrackerHexEntry(t *testing.T) {
	e := trkTestState()
	e.Step = 0
	e.Cur.Field = 1 // instrument, high nibble
	e.typeKey(sdl.SCANCODE_1)
	e.Cur.Field = 2
	e.typeKey(sdl.SCANCODE_A)
	if got := trkCellAtT(e, 0, 0).Instr; got != 0x1A {
		t.Fatalf("instrument = %02X, want 1A", got)
	}
	e.Cur.Field = 3 // volume high nibble on an empty volume
	e.typeKey(sdl.SCANCODE_7)
	if got := trkCellAtT(e, 0, 0).Vol; got != tracker.VolMax {
		t.Fatalf("volume 70 should clamp to 40, got %02X", got)
	}
	e.Cur.Field = 4
	e.typeKey(sdl.SCANCODE_0)
	if got := trkCellAtT(e, 0, 0).Vol; got != 0x40 {
		t.Fatalf("volume low nibble: %02X", got)
	}
}

func TestTrackerEffectEntry(t *testing.T) {
	e := trkTestState()
	e.Step = 0
	e.Cur.Field = trkFieldsFixed + 1 // effect 0 param, no letter yet
	if e.enterHex(4) {
		t.Fatal("a parameter with no effect letter should be refused")
	}
	e.Cur.Field = trkFieldsFixed
	e.typeKey(sdl.SCANCODE_Q) // Q is retrigger
	e.typeKey(sdl.SCANCODE_K) // K is pattern break, also valid
	if got := trkCellAtT(e, 0, 0).FX[0].Cmd; got != 'K' {
		t.Fatalf("effect letter = %c", got)
	}
	if e.enterEffect('?') {
		t.Fatal("a non-effect letter should be refused")
	}
	e.Cur.Field = trkFieldsFixed + 1
	e.typeKey(sdl.SCANCODE_4)
	e.Cur.Field = trkFieldsFixed + 2
	e.typeKey(sdl.SCANCODE_8)
	if got := trkCellAtT(e, 0, 0).FX[0]; got != (tracker.Effect{Cmd: 'K', Param: 0x48}) {
		t.Fatalf("effect = %s", tracker.FormatEffect(got))
	}
	e.Cur.Field = trkFieldsFixed + 3 // second effect column
	e.setEffect(tracker.FxVibrato)
	if trkCellAtT(e, 0, 0).FX[1].Cmd != 'V' || e.Cur.Field != trkFieldsFixed+4 {
		t.Fatal("the picker's pick should land and move to the parameter")
	}
	e.Cur.Field = trkFieldsFixed
	e.clearField()
	if trkCellAtT(e, 0, 0).FX[0].Cmd != 0 {
		t.Fatal("Delete on an effect clears it")
	}
}

func TestTrackerScaleLock(t *testing.T) {
	e := trkTestState()
	e.Scale = trkScale{Root: 0, Kind: 1} // C major
	e.Step = 1
	e.typeKey(sdl.SCANCODE_S) // C#4 → C-4 (the note below on a tie)
	if got := tracker.NoteName(trkCellAtT(e, 0, 0).Note); got != "C-4" {
		t.Fatalf("C# in C major snapped to %s", got)
	}
	e.typeKey(sdl.SCANCODE_D) // D#4 → D-4
	if got := tracker.NoteName(trkCellAtT(e, 1, 0).Note); got != "D-4" {
		t.Fatalf("D# in C major snapped to %s", got)
	}
	e.typeKey(sdl.SCANCODE_C) // E-4 is in the scale
	if got := tracker.NoteName(trkCellAtT(e, 2, 0).Note); got != "E-4" {
		t.Fatalf("E stayed %s", got)
	}
}

// ---------- clipboard ----------

func TestTrackerCopyPasteRoundTrip(t *testing.T) {
	e := trkTestState()
	p := e.pattern()
	p.Cells[0][0] = tracker.Cell{Note: 49, Instr: 1, Vol: 0x30, FX: [4]tracker.Effect{{Cmd: 'V', Param: 0x44}}}
	p.Cells[1][1] = tracker.Cell{Note: tracker.NoteOff, Vol: tracker.VolNone}
	e.SelOn, e.SelA, e.SelB = true, trkPoint{0, 0}, trkPoint{1, 1}
	text := e.copyText()
	if !strings.Contains(text, "C-4 01 30 V44") || strings.Count(text, "\n") != 2 {
		t.Fatalf("copied text:\n%s", text)
	}
	e.SelOn = false
	e.Cur = trkCursor{Row: 10, Ch: 2}
	if err := e.paste(text, false); err != nil {
		t.Fatal(err)
	}
	if trkCellAtT(e, 10, 2) != p.Cells[0][0] || trkCellAtT(e, 11, 3) != p.Cells[1][1] {
		t.Fatalf("pasted cells differ:\n%s", e.copyText())
	}
	// Pasting at the right edge clips rather than failing.
	e.Cur = trkCursor{Row: 63, Ch: 3}
	if err := e.paste(text, false); err != nil {
		t.Fatal(err)
	}
	if err := e.paste("not tracker text", false); err == nil {
		t.Fatal("garbage should not paste")
	}
}

func TestTrackerMixPasteFillsOnlyEmpty(t *testing.T) {
	e := trkTestState()
	p := e.pattern()
	p.Cells[0][0] = tracker.Cell{Note: 49, Instr: 1, Vol: tracker.VolNone}
	clip := "D-4 02 20 V11\n"
	if err := e.paste(clip, true); err != nil {
		t.Fatal(err)
	}
	c := trkCellAtT(e, 0, 0)
	if tracker.NoteName(c.Note) != "C-4" || c.Instr != 1 {
		t.Fatalf("mix paste overwrote filled fields: %s", tracker.FormatCell(c, 2))
	}
	if c.Vol != 0x20 || c.FX[0].Cmd != 'V' {
		t.Fatalf("mix paste did not fill empty fields: %s", tracker.FormatCell(c, 2))
	}
}

// ---------- selection tools ----------

func TestTrackerTranspose(t *testing.T) {
	e := trkTestState()
	p := e.pattern()
	p.Cells[0][0].Note = 49
	p.Cells[1][0].Note = tracker.NoteOff
	p.Cells[2][0].Note = tracker.NoteMax
	e.SelOn, e.SelA, e.SelB = true, trkPoint{0, 0}, trkPoint{2, 0}
	e.transpose(12)
	if p.Cells[0][0].Note != 61 || p.Cells[1][0].Note != tracker.NoteOff || p.Cells[2][0].Note != tracker.NoteMax {
		t.Fatalf("transpose: %d %d %d", p.Cells[0][0].Note, p.Cells[1][0].Note, p.Cells[2][0].Note)
	}
	e.transpose(-1)
	if p.Cells[0][0].Note != 60 {
		t.Fatal("transpose down a semitone")
	}
}

func TestTrackerInterpolate(t *testing.T) {
	e := trkTestState()
	p := e.pattern()
	p.Cells[0][0].Vol = 0x00
	p.Cells[4][0].Vol = 0x40
	p.Cells[0][0].FX[0] = tracker.Effect{Cmd: tracker.FxVibrato, Param: 0x00}
	p.Cells[4][0].FX[0] = tracker.Effect{Cmd: tracker.FxVibrato, Param: 0x48}
	p.Cells[0][0].FX[1] = tracker.Effect{Cmd: tracker.FxPan, Param: 0x00}
	p.Cells[4][0].FX[1] = tracker.Effect{Cmd: tracker.FxCutoff, Param: 0x80}
	e.SelOn, e.SelA, e.SelB = true, trkPoint{0, 0}, trkPoint{4, 0}
	if !e.interpolate() {
		t.Fatal(e.Status)
	}
	if got := p.Cells[2][0].Vol; got != 0x20 {
		t.Fatalf("mid volume %02X", got)
	}
	// Vibrato is split: speed 0→4 and depth 0→8 interpolate apart.
	if got := p.Cells[2][0].FX[0]; got != (tracker.Effect{Cmd: 'V', Param: 0x24}) {
		t.Fatalf("mid vibrato %s", tracker.FormatEffect(got))
	}
	if p.Cells[2][0].FX[1].Cmd != 0 {
		t.Fatal("different commands at the ends should not interpolate")
	}
	e.SelB = trkPoint{1, 0}
	if e.interpolate() {
		t.Fatal("two rows is nothing to interpolate")
	}
}

func TestTrackerHumaniseIsDeterministic(t *testing.T) {
	run := func(seed uint32) []uint8 {
		e := trkTestState()
		p := e.pattern()
		for r := 0; r < 16; r++ {
			p.Cells[r][0].Note = 49
		}
		e.SelOn, e.SelA, e.SelB = true, trkPoint{0, 0}, trkPoint{15, 0}
		e.humanise(8, seed)
		var out []uint8
		for r := 0; r < 16; r++ {
			v := p.Cells[r][0].Vol
			if v > tracker.VolMax || v < tracker.VolMax-8 {
				t.Fatalf("volume %02X outside the jitter", v)
			}
			out = append(out, v)
		}
		return out
	}
	a, b, c := run(1), run(1), run(2)
	if string(a) != string(b) {
		t.Fatal("the same seed should give the same result")
	}
	if string(a) == string(c) {
		t.Fatal("a different seed should give a different result")
	}
}

func TestTrackerFillAndChord(t *testing.T) {
	e := trkTestState()
	p := e.pattern()
	p.Cells[0][2] = tracker.Cell{Note: 49, Instr: 1, Vol: 0x20}
	e.Cur.Ch = 2
	e.fillEvery(4)
	for r := 0; r < 64; r++ {
		if (r%4 == 0) != (p.Cells[r][2].Note == 49) {
			t.Fatalf("fill: row %d", r)
		}
	}
	e.Cur = trkCursor{Row: 0, Ch: 0}
	p.Cells[0][0] = tracker.Cell{Note: 49, Instr: 2, Vol: tracker.VolNone}
	if !e.chordStamp(1) { // minor
		t.Fatal(e.Status)
	}
	if p.Cells[0][1].Note != 52 || p.Cells[0][2].Note != 56 || p.Cells[0][1].Instr != 2 {
		t.Fatalf("minor chord: %d %d", p.Cells[0][1].Note, p.Cells[0][2].Note)
	}
}

// ---------- undo ----------

func TestTrackerUndoCoalescesTyping(t *testing.T) {
	e := trkTestState()
	e.typeKey(sdl.SCANCODE_Z)
	e.typeKey(sdl.SCANCODE_X)
	e.typeKey(sdl.SCANCODE_C)
	if e.undo.len() != 1 {
		t.Fatalf("a run of typing should be one step, have %d", e.undo.len())
	}
	e.moveRows(5) // a deliberate move ends the run
	e.typeKey(sdl.SCANCODE_V)
	if e.undo.len() != 2 {
		t.Fatalf("typing after a move is a new step, have %d", e.undo.len())
	}
	e.undoStep()
	if !trkCellAtT(e, 8, 0).IsEmpty() || trkCellAtT(e, 0, 0).IsEmpty() {
		t.Fatal("undo should take back only the last run")
	}
	e.undoStep()
	if !trkCellAtT(e, 0, 0).IsEmpty() {
		t.Fatal("second undo takes back the first run")
	}
	if !e.redoStep() || trkCellAtT(e, 2, 0).IsEmpty() {
		t.Fatal("redo puts the run back")
	}
	e.transpose(1)
	if e.canRedo() {
		t.Fatal("a new edit clears redo")
	}
}

func TestTrackerUndoRestoresCursor(t *testing.T) {
	e := trkTestState()
	e.Cur = trkCursor{Row: 12, Ch: 3}
	e.typeKey(sdl.SCANCODE_Z)
	e.Cur = trkCursor{}
	e.undoStep()
	if e.Cur.Row != 12 || e.Cur.Ch != 3 {
		t.Fatalf("undo should put the cursor back where the edit was: %+v", e.Cur)
	}
}

// ---------- templates, fingerprint ----------

func TestTrackerTemplatesValidate(t *testing.T) {
	for _, tpl := range trkTemplates {
		s := tpl.Build()
		if probs := s.Validate(); len(probs) > 0 {
			t.Errorf("%s: %v", tpl.Name, probs)
		}
		b, err := tracker.Marshal(s)
		if err != nil {
			t.Fatalf("%s: %v", tpl.Name, err)
		}
		back, err := tracker.Parse(b)
		if err != nil {
			t.Fatalf("%s does not read back: %v", tpl.Name, err)
		}
		if trkFingerprint(back) != trkFingerprint(s) {
			t.Errorf("%s changes on a save/load round trip", tpl.Name)
		}
		if len(s.Instruments) == 0 {
			t.Errorf("%s has no instruments to type with", tpl.Name)
		}
	}
}

func TestTrackerFingerprintFollowsEdits(t *testing.T) {
	e := trkTestState()
	base := trkFingerprint(e.Song)
	e.typeKey(sdl.SCANCODE_Z)
	if trkFingerprint(e.Song) == base {
		t.Fatal("an edit should change the fingerprint")
	}
	e.undoStep()
	if trkFingerprint(e.Song) != base {
		t.Fatal("undoing back to the saved state should match it again")
	}
}

// ---------- order and instruments ----------

func TestTrackerOrderOpsKeepSections(t *testing.T) {
	e := newTrackerState(trkTemplateCombat())
	// order [0 1 1]; intro [0,1), loop [1,3)
	e.Cur.Order = 0
	e.insertNewPattern()
	if len(e.Song.Order) != 4 || e.Cur.Order != 1 {
		t.Fatalf("insert: order %v cursor %d", e.Song.Order, e.Cur.Order)
	}
	if sec := e.Song.Sections[1]; sec.Start != 2 || sec.End != 4 {
		t.Fatalf("the loop section should move down with its entries: %+v", sec)
	}
	if sec := e.Song.Sections[0]; sec.Start != 0 || sec.End != 1 {
		t.Fatalf("the intro should not grow from an insert after it: %+v", sec)
	}
	e.Cur.Order = 2
	e.repeatOrder() // inside the loop: it grows
	if sec := e.Song.Sections[1]; sec.Start != 2 || sec.End != 5 {
		t.Fatalf("an insert inside a section grows it: %+v", sec)
	}
	e.deleteOrder()
	if sec := e.Song.Sections[1]; sec.End != 4 {
		t.Fatalf("delete inside the section shrinks it: %+v", sec)
	}
	if probs := e.Song.Validate(); len(probs) > 0 {
		t.Fatal(probs)
	}
	e.Cur.Order = 2
	if !e.patternShared(2) {
		t.Fatal("pattern 1 plays twice")
	}
	e.makeUnique()
	if e.patternShared(2) {
		t.Fatal("make unique should give the entry its own pattern")
	}
}

func TestTrackerDeleteInstrumentRenumbers(t *testing.T) {
	e := trkTestState()
	e.Song.Instruments = append(e.Song.Instruments, tracker.NewInstrument("C", ""))
	p := e.pattern()
	p.Cells[0][0].Instr, p.Cells[1][0].Instr, p.Cells[2][0].Instr = 1, 2, 3
	e.deleteInstrument(2)
	if p.Cells[0][0].Instr != 1 || p.Cells[1][0].Instr != 0 || p.Cells[2][0].Instr != 2 {
		t.Fatalf("renumber: %d %d %d", p.Cells[0][0].Instr, p.Cells[1][0].Instr, p.Cells[2][0].Instr)
	}
}

// ---------- layout ----------

func TestTrackerFieldHitTestMatchesLayout(t *testing.T) {
	for _, hex := range []bool{true, false} {
		for fx := 1; fx <= tracker.MaxFXColumns; fx++ {
			for f := 0; f < trkFieldCount(fx); f++ {
				s, w := trkFieldSpan(f, hex)
				if got := trkFieldAt(float32(s)+float32(w)/2, fx, hex); got != f {
					t.Fatalf("hex=%v fx=%d: field %d at col %d hit-tests as %d", hex, fx, f, s, got)
				}
			}
		}
	}
}

func TestTrackerEnvelopeIndexShift(t *testing.T) {
	env := tracker.Envelope{Points: make([]tracker.EnvPoint, 4), Sustain: 2, LoopStart: 1, LoopEnd: 3}
	trkEnvShiftIndices(&env, 0, 1)
	if env.Sustain != 3 || env.LoopStart != 2 || env.LoopEnd != 4 {
		t.Fatalf("insert before: %+v", env)
	}
	trkEnvShiftIndices(&env, 3, -1)
	if env.Sustain != -1 || env.LoopStart != 2 || env.LoopEnd != 3 {
		t.Fatalf("delete the sustain point: %+v", env)
	}
}

// ---------- sample DSP ----------

func trkSine(hz float64, rate, frames int) *tracker.Sample {
	s := &tracker.Sample{Name: "sine", Channels: 1, Rate: rate, BaseNote: tracker.NoteC4, Volume: 1}
	for i := 0; i < frames; i++ {
		s.Data = append(s.Data, float32(0.5*math.Sin(2*math.Pi*hz*float64(i)/float64(rate))))
	}
	return s
}

func TestTrackerSampleEdits(t *testing.T) {
	s := trkSine(100, 8000, 800)
	trkNormalize(s, -1, -1)
	if p := tracker.Peak(s.Data); p < 0.999 || p > 1.001 {
		t.Fatalf("normalised peak %f", p)
	}
	c := trkCopySample(s)
	trkReverse(c, -1, -1)
	if c.Data[0] != s.Data[len(s.Data)-1] {
		t.Fatal("reverse")
	}
	if &c.Data[0] == &s.Data[0] {
		t.Fatal("the copy shares data with the original")
	}
	trkFade(c, 0, 100, true)
	if c.Data[0] != 0 {
		t.Fatal("fade in starts at silence")
	}
	c.Loop, c.LoopStart, c.LoopEnd = tracker.LoopForward, 300, 700
	trkTrim(c, 200, 600)
	if c.Frames() != 400 || c.LoopStart != 100 || c.LoopEnd != 400 {
		t.Fatalf("trim: %d frames, loop %d..%d", c.Frames(), c.LoopStart, c.LoopEnd)
	}
	z := trkZeroCrossing(s, 85, 40)
	if trkMono(s, z-1) > 0 || trkMono(s, z) <= 0 {
		t.Fatalf("frame %d is not an upward zero crossing", z)
	}
}

func TestTrackerPitchDetect(t *testing.T) {
	hz := trkDetectPitch(trkSine(440, 44100, 22050))
	if math.Abs(hz-440) > 2 {
		t.Fatalf("detected %.2f Hz for 440", hz)
	}
	n, cents := trkHzToNote(hz)
	if tracker.NoteName(n) != "A-4" || math.Abs(float64(cents)) > 10 {
		t.Fatalf("440 Hz is %s %+.1f", tracker.NoteName(n), cents)
	}
	if n, _ := trkHzToNote(261.63); tracker.NoteName(n) != "C-4" {
		t.Fatalf("middle C reads as %s", tracker.NoteName(n))
	}
}
