package editor

import (
	"strings"
	"testing"
	"time"

	"foundry/internal/mididev"
	"foundry/internal/score"
	"foundry/internal/score/play"
)

func noteOn(key, vel int) mididev.Message {
	return mididev.Message{Status: mididev.NoteOn, Data1: byte(key), Data2: byte(vel)}
}

func noteOff(key int) mididev.Message {
	return mididev.Message{Status: mididev.NoteOff, Data1: byte(key)}
}

// Step entry: in note input a key writes a note of the current value after
// the selection, spelled in the key (sharps rising, flats falling); keys
// played while it is held join it as a chord; thru sounds every key and
// the written note is not auditioned a second time.
func TestScoreMIDIStepEntry(t *testing.T) {
	e := scoreTestState(t) // G major
	e.MIDI.Thru = true
	// Out of note input the keyboard only sounds.
	e.clickBar(0, 1, false, false)
	e.midiIn(noteOn(62, 90), 0, false)
	e.midiIn(noteOff(62), 0, false)
	if got := barOf(e, 0, 0, 1); got != "" {
		t.Fatalf("out of note input nothing is written: %q", got)
	}
	if th := e.takeThru(); len(th) != 2 || !th[0].On || th[0].Key != 62 || th[0].Line != 0 || th[1].On {
		t.Fatalf("thru = %+v", th)
	}
	e.toggleInput()
	play := func(keys ...int) {
		for _, k := range keys {
			e.midiIn(noteOn(k, 80), 0, false)
		}
		for _, k := range keys {
			e.midiIn(noteOff(k), 0, false)
		}
	}
	play(62)
	e.Dur = score.Duration{Base: score.Eighth}
	play(64, 67)     // a chord, played together
	play(66)         // F#: in the key
	play(70)         // up from F#: A#
	play(68)         // down from A#: Ab
	play(65, 69, 72) // F natural going down, then the chord's others
	if got := barOf(e, 0, 0, 1); got != "qD4 eE4+G4 eF#4 eA#4 eAb4 eF4+A4+C5 er" {
		t.Errorf("bar 1 = %q", got)
	}
	if a := e.takeAuditions(); len(a) != 0 {
		t.Errorf("thru on: the written notes are not auditioned again: %+v", a)
	}
	// Thru off: the written note is auditioned, nothing is sent thru.
	e.takeThru()
	e.MIDI.Thru = false
	play(74)
	if a := e.takeAuditions(); len(a) != 1 || a[0].MIDI[0] != 74 {
		t.Errorf("thru off: auditions %+v", a)
	}
	if th := e.takeThru(); len(th) != 0 {
		t.Errorf("thru off: %+v", th)
	}
	// A key repeated inside a held chord is not added twice.
	e.midiIn(noteOn(76, 80), 0, false)
	e.midiIn(noteOn(76, 80), 0, false)
	e.midiIn(noteOff(76), 0, false)
	if got := barOf(e, 0, 0, 2); !strings.HasPrefix(got, "eE5 ") {
		t.Errorf("bar 2 = %q", got)
	}
	// Undo takes a key back like any entry.
	scoreRunCmd2(e, scUndo)
	if got := barOf(e, 0, 0, 2); strings.Contains(got, "E5") {
		t.Errorf("undo: %q", got)
	}
}

// A key is a sound: a B-flat clarinet's key C4 is stored as C4 and shown
// as D4; a drum kit's key is its drum.
func TestScoreMIDISoundingPitchAndDrums(t *testing.T) {
	e := scoreTestState(t)
	e.Score.Key = nil
	e.Score.Lines[0].Staves[0].Transpose = score.Interval{Diatonic: -1, Chromatic: -2}
	e.clickBar(0, 1, false, false)
	e.toggleInput()
	e.midiIn(noteOn(60, 80), 0, false)
	e.midiIn(noteOff(60), 0, false)
	if got := barOf(e, 0, 0, 1); got != "qC4 qr hr" {
		t.Errorf("stored %q, want the sounding C4", got)
	}
	if !strings.Contains(e.Status, "D4") {
		t.Errorf("the status names the written D4: %q", e.Status)
	}

	useTestKit(t)
	d := scoreTestState(t)
	d.Score.Lines[0] = score.NewLine("Drums", "percussion/test", "percussion")
	d.clickBar(0, 1, false, false)
	d.toggleInput()
	for _, k := range []int{36, 46} {
		d.midiIn(noteOn(k, 100), 0, false)
		d.midiIn(noteOff(k), 0, false)
	}
	if got := barOf(d, 0, 0, 1); got != "qC2 qA#2 hr" {
		t.Errorf("drums %q", got)
	}
}

// Recording: the take goes into the armed line from the selected bar; keys
// are kept with their ticks (one early in the count-in goes on the
// downbeat), held keys end where the transport stops, and the take is
// written over the bars it covered as one undo step.
func TestScoreMIDIRecording(t *testing.T) {
	e := scoreTestState(t)
	e.Score.Key = nil
	e.toggleArm(1) // the cello
	e.clickBar(0, 2, false, false)
	cfg := scoreMIDIConfig{NoDynamics: true}
	if !e.startTake(cfg) {
		t.Fatal(e.Status)
	}
	tk := e.MIDI.Take
	if tk.Line != 1 || tk.Bar != 2 || tk.Tick != 3840 || tk.Count != 4 {
		t.Fatalf("take %+v", tk)
	}
	ops := e.takeOps()
	if len(ops) != 1 || ops[0].Kind != scoreOpRecord || ops[0].Tick != 3840 || ops[0].Bars != 1 || ops[0].Level != 0.6 {
		t.Fatalf("ops %+v", ops)
	}
	q := int64(score.TicksPerQuarter)
	e.midiIn(noteOn(43, 80), 3840, true) // G2 on the downbeat (early, clamped)
	e.midiIn(noteOff(43), 3840+q-30, true)
	e.midiIn(noteOn(45, 80), 3840+q+12, true)
	e.midiIn(noteOff(45), 3840+2*q-5, true)
	e.midiIn(noteOn(47, 80), 3840+2*q+3, true) // held to the stop
	e.midiIn(mididev.Message{Status: mididev.ControlChange, Data1: 64, Data2: 127}, 3840+2*q+10, true)
	e.midiIn(noteOn(48, 80), 0, false) // not on the transport's clock: dropped
	if tk.Notes() != 3 {
		t.Fatalf("take holds %d keys", tk.Notes())
	}
	if !e.finishTake(3840*2+2, cfg) {
		t.Fatal(e.Status)
	}
	if e.MIDI.Take != nil {
		t.Fatal("the take is over")
	}
	if got := barOf(e, 1, 0, 2); got != "qG2 qA2 hB2(ped,ped_end)" {
		t.Errorf("bar 2 = %q (%s)", got, e.Status)
	}
	if got := barOf(e, 1, 0, 3); got != "" {
		t.Errorf("bar 3 untouched: %q", got)
	}
	if got := barOf(e, 0, 0, 2); got != "" {
		t.Errorf("the flute is not the armed line: %q", got)
	}
	if e.Sel.Kind != scoreSelBars || e.Sel.LineA != 1 || e.Sel.BarA != 2 || e.Sel.BarB != 2 {
		t.Errorf("the take's bars are selected: %+v", e.Sel)
	}
	ops = e.takeOps()
	if len(ops) < 1 || ops[0].Kind != scoreOpClick || ops[0].Level != 0 {
		t.Errorf("the click goes off: %+v", ops)
	}
	scoreRunCmd2(e, scUndo)
	if got := barOf(e, 1, 0, 2); got != "" {
		t.Errorf("undo: %q", got)
	}
	// Nothing played: nothing written.
	e.startTake(cfg)
	if e.finishTake(3840*3, cfg) || e.Status != "Nothing was recorded" {
		t.Errorf("empty take: %q", e.Status)
	}
}

// A take stops where the performance goes back to bars it has played:
// a repeat's second time is not written over its first.
func TestScoreMIDIRecordingStopsAtARepeat(t *testing.T) {
	e := scoreTestState(t)
	e.Score.Key = nil
	e.Score.Repeats = []score.Repeat{{Start: 1, End: 2}}
	e.clickBar(0, 1, false, false)
	cfg := scoreMIDIConfig{NoDynamics: true, CountIn: new(int)}
	if !e.startTake(cfg) {
		t.Fatal(e.Status)
	}
	if e.MIDI.Take.Limit != 2*3840 {
		t.Fatalf("limit %d", e.MIDI.Take.Limit)
	}
	if ops := e.takeOps(); ops[0].Bars != 0 {
		t.Errorf("no count-in: %+v", ops)
	}
	e.midiIn(noteOn(72, 80), 0, true)
	e.midiIn(noteOff(72), 2*3840, true)
	e.midiIn(noteOn(74, 80), 2*3840+10, true) // the repeat: dropped
	e.midiIn(noteOff(74), 3*3840, true)
	if !e.finishTake(4*3840, cfg) {
		t.Fatal(e.Status)
	}
	if b1, b2 := barOf(e, 0, 0, 1), barOf(e, 0, 0, 2); b1 != "wC5~" || b2 != "wC5" {
		t.Errorf("bars %q %q", b1, b2)
	}
}

// The window's poll feeds a device's messages to the model, keeping only
// the channel asked for.
func TestScoreMIDIPollAVirtualDevice(t *testing.T) {
	prev, prevIn := scEd, scoreMIDIIn
	t.Cleanup(func() { scEd, scoreMIDIIn = prev, prevIn })
	scEd = scoreTestState(t)
	scEd.clickBar(0, 1, false, false)
	scEd.toggleInput()
	in := mididev.Virtual("test")
	scoreMIDIIn = in
	in.Send(noteOn(62, 90), noteOff(62), mididev.Message{Status: 0xB0, Data1: 64, Data2: 127})
	scorePollMIDI()
	if got := barOf(scEd, 0, 0, 1); got != "qD4 qr hr" {
		t.Errorf("polled entry %q", got)
	}
	if scEd.MIDI.Last != "CC 64 = 127 ch 1" {
		t.Errorf("last heard %q", scEd.MIDI.Last)
	}
	in.Close()
	scorePollMIDI()
	if scoreMIDIIn != nil || !strings.Contains(scEd.Status, "MIDI") {
		t.Errorf("a closed device is dropped and said so: %v %q", scoreMIDIIn, scEd.Status)
	}
}

// A recording through the real scheduler, rendered offline: the record
// command counts in, keys sent at known score times land on those ticks
// (the player's published clock), and the record command again stops the
// transport and writes the take.
func TestScoreMIDIRecordingThroughThePlayer(t *testing.T) {
	prev, prevIn, prevP, prevCfg := scEd, scoreMIDIIn, scorePlayer, *scoreSettings()
	t.Cleanup(func() {
		scEd, scoreMIDIIn, scorePlayer = prev, prevIn, prevP
		*scoreSettings() = prevCfg
		scorePlayerSet = false
	})
	scoreSettings().MIDI = scoreMIDIConfig{NoDynamics: true}
	scEd = scoreTestState(t)
	scEd.Score.Key = nil
	scEd.Score.Tempo = []score.TempoMark{{At: score.Pos{Bar: 1}, BPM: 120}}
	p, _ := play.New(scEd.Score.Clone())
	if p == nil {
		t.Fatal("no player")
	}
	scorePlayer = p
	scoreAdopted()
	in := mididev.Virtual("test")
	scoreMIDIIn = in
	scEd.clickBar(0, 2, false, false)
	scoreRecordToggle()
	if !scoreRecording() {
		t.Fatal(scEd.Status)
	}
	scoreApplyOps()
	const rate = 48000
	buf := make([]float32, 480*2)
	block := func() {
		p.Render(buf, 480, rate)
		scorePollMIDI()
	}
	// The count-in: one bar of 4/4 at 120 is two seconds.
	for i := 0; i < 400 && !p.Position().Playing; i++ {
		block()
	}
	if !p.Position().Playing || p.Position().Tick < 3840 {
		t.Fatalf("after the count-in: %+v", p.Position())
	}
	// Keys at score seconds: bar 2 starts at 2 s; a quarter is 0.5 s.
	at := func(sec float64) time.Time {
		pos := p.Position()
		return pos.Published.Add(time.Duration((sec - pos.Seconds) * float64(time.Second)))
	}
	send := func(m mididev.Message, sec float64) {
		for p.Position().Seconds+0.01 < sec {
			block()
		}
		m.Time = at(sec)
		in.Send(m)
		scorePollMIDI()
	}
	send(noteOn(60, 80), 2.004)
	send(noteOff(60), 2.98)
	send(noteOn(62, 80), 3.0)
	send(noteOff(62), 3.95)
	for p.Position().Seconds < 4.05 {
		block()
	}
	scoreRecordToggle()
	if scoreRecording() {
		t.Fatal("still recording")
	}
	if got := barOf(scEd, 0, 0, 2); got != "hC4 hD4" {
		t.Errorf("bar 2 = %q (%s)", got, scEd.Status)
	}
	if got := barOf(scEd, 0, 0, 3); got != "" {
		t.Errorf("bar 3 = %q", got)
	}
}
