package editor

// editor_score_transport_test.go — the transport's arithmetic and commands
// with no window: beats, snapping, ticks and the clock through repeats and
// tempo changes, the loop region (ruler drags, L, fitting after edits), the
// play head (seeks, Space, P, Home) and the tempo slider written into the
// score.

import (
	"testing"

	"foundry/internal/score"
)

func frac(n, d int64) score.Frac { return score.F(n, d) }

func fracsEq(a, b []score.Frac) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Eq(b[i]) {
			return false
		}
	}
	return true
}

func TestScoreBeatsAndSnapping(t *testing.T) {
	s := score.New("t")
	s.Bars = 4
	set := func(bar int, meter string) {
		m, err := score.ParseMeter(meter)
		if err != nil {
			t.Fatal(err)
		}
		s.Meter = append(s.Meter, score.MeterMark{Bar: bar, Meter: m})
	}
	set(2, "6/8")
	set(3, "3+2+2/8")
	cases := []struct {
		bar  int
		want []score.Frac
	}{
		{1, []score.Frac{{}, frac(1, 4), frac(1, 2), frac(3, 4)}},
		{2, []score.Frac{{}, frac(3, 8)}},
		{3, []score.Frac{{}, frac(3, 8), frac(5, 8)}},
	}
	for _, c := range cases {
		if got := scoreBeatOffsets(s, c.bar); !fracsEq(got, c.want) {
			t.Errorf("bar %d beats %v, want %v", c.bar, got, c.want)
		}
	}
	// A pickup counts its beats back from its end.
	p := score.New("p")
	p.Pickup = frac(3, 8)
	if got := scoreBeatOffsets(p, 1); !fracsEq(got, []score.Frac{{}, frac(1, 8)}) {
		t.Errorf("pickup beats %v", got)
	}
	snaps := []struct {
		bar  int
		off  float64
		want score.Frac
	}{
		{1, 0.3, frac(1, 4)}, {1, 0.1, frac(0, 1)}, {1, 0.97, frac(1, 1)}, {2, 0.2, frac(3, 8)}, {3, 0.55, frac(5, 8)},
	}
	for _, c := range snaps {
		if got := scoreSnapBeat(s, c.bar, c.off); got.Bar != c.bar || !got.Offset.Eq(c.want) {
			t.Errorf("snap bar %d at %v = %+v, want %v", c.bar, c.off, got, c.want)
		}
	}
}

// Ticks, written bars and the clock through a repeat, a tempo change and
// the slider.
func TestScoreTicksAndClock(t *testing.T) {
	e := scoreTestState(t)
	s := e.Score
	s.Repeats = []score.Repeat{{Start: 1, End: 2}}
	s.Tempo = []score.TempoMark{{At: score.Pos{Bar: 1}, BPM: 120}, {At: score.Pos{Bar: 3}, BPM: 60}}
	e.touch()
	perf := e.perf()
	bar := int64(score.TicksPerWhole)
	// Bars 1 2 1 2 3 4: bar 3 first plays four bars in.
	if got := scorePosTick(perf, score.Pos{Bar: 3}); got != 4*bar {
		t.Errorf("bar 3 at %d, want %d", got, 4*bar)
	}
	if got := scorePosTick(perf, score.Pos{Bar: 2, Offset: frac(1, 2)}); got != bar+bar/2 {
		t.Errorf("bar 2 beat 3 at %d", got)
	}
	if got := scorePosTick(perf, score.Pos{Bar: 9}); got != -1 {
		t.Errorf("a bar past the end at %d", got)
	}
	b, off, pass := scoreTickPos(perf, 2*bar+bar/4)
	if b != 1 || pass != 2 || off != 0.25 {
		t.Errorf("tick of bar 1's second pass: bar %d off %v pass %d", b, off, pass)
	}
	if got := scoreReadout(perf, 2*bar+bar/4, 1); got != "Bar 1 (x2) · Beat 2 · 0:04.5" {
		t.Errorf("readout %q", got)
	}
	// Bar 3 at 60: a beat is a second; at half speed two.
	if got := scoreReadout(perf, 4*bar+bar/4, 0.5); got != "Bar 3 · Beat 2 · 0:18.0" {
		t.Errorf("readout at half speed %q", got)
	}
	if got := scoreTempoText(perf, 4*bar, 0.75); got != "45 bpm (75%)" {
		t.Errorf("tempo text %q", got)
	}
	for sec, want := range map[float64]string{0: "0:00.0", 65.34: "1:05.3", 59.96: "0:59.9", -3: "0:00.0", 600: "10:00.0"} {
		if got := scoreClock(sec); got != want {
			t.Errorf("clock %v = %q, want %q", sec, got, want)
		}
	}
	if scoreClampScale(3) != 2 || scoreClampScale(0.1) != 0.25 || scoreClampScale(0.7549) != 0.75 {
		t.Error("tempo slider clamp")
	}
}

func TestScoreLoopRegion(t *testing.T) {
	e := scoreTestState(t)
	s := e.Score
	// Dragged right to left, and onto one beat.
	l := scoreLoopBetween(s, score.Pos{Bar: 3, Offset: frac(1, 2)}, score.Pos{Bar: 2, Offset: frac(1, 4)})
	if !l.On || !l.Set || l.A != (score.Pos{Bar: 2, Offset: frac(1, 4)}) || l.B != (score.Pos{Bar: 3, Offset: frac(1, 2)}) {
		t.Errorf("drag left %+v", l)
	}
	one := scoreLoopBetween(s, score.Pos{Bar: 2, Offset: frac(3, 4)}, score.Pos{Bar: 2, Offset: frac(3, 4)})
	if one.B != (score.Pos{Bar: 2, Offset: frac(1, 1)}) {
		t.Errorf("one beat: %+v", one)
	}
	bars := scoreLoopBars(s, 3, 2)
	a, b, ok := scoreLoopTicks(e.perf(), bars)
	if !ok || a != score.TicksPerWhole || b != 3*score.TicksPerWhole {
		t.Errorf("bars 2-3 = %d..%d %v", a, b, ok)
	}
	if got := scoreLoopText(s, bars); got != "Loop: bar 2 beat 1 to bar 4 beat 1" {
		t.Errorf("text %q", got)
	}
	if got := scoreLoopText(s, scoreLoopBars(s, 4, 4)); got != "Loop: bar 4 beat 1 to the end of bar 4" {
		t.Errorf("text at the end %q", got)
	}
	// The score shrinks under it: cut at the end, or gone.
	s.Bars = 2
	fit := scoreFitLoop(s, bars)
	if !fit.Set || fit.B != (score.Pos{Bar: 2, Offset: frac(1, 1)}) {
		t.Errorf("fitted %+v", fit)
	}
	if gone := scoreFitLoop(s, scoreLoopBars(s, 3, 4)); gone.Set {
		t.Errorf("a loop past the end stayed: %+v", gone)
	}
}

// The transport through scoreState: what each command asks of the player.
func TestScoreTransportCommands(t *testing.T) {
	e := scoreTestState(t)
	bar := int64(score.TicksPerWhole)
	ops := func() []scorePlayerOp { return e.takeOps() }
	// L with nothing selected says what to do.
	e.loopSelection()
	if e.Transport.Loop.Set || len(ops()) != 0 {
		t.Fatal("L with nothing selected made a loop")
	}
	// L loops the selected bars.
	e.clickBar(0, 2, false, false)
	e.clickBar(0, 3, false, true)
	e.loopSelection()
	if o := ops(); len(o) != 1 || o[0].Kind != scoreOpLoop || o[0].A != bar || o[0].B != 3*bar {
		t.Fatalf("L: %+v (%s)", o, e.Status)
	}
	// Space from the top while the loop is on starts at the loop.
	e.togglePlay(false, 0)
	if o := ops(); len(o) != 1 || o[0].Kind != scoreOpPlay || o[0].Tick != bar {
		t.Errorf("Space with the head outside the loop: %+v", o)
	}
	// Space while playing stops there; the head stays.
	e.togglePlay(true, bar+bar/2)
	if o := ops(); len(o) != 1 || o[0].Kind != scoreOpStop || e.Transport.Head != bar+bar/2 {
		t.Errorf("Space while playing: %+v head %d", o, e.Transport.Head)
	}
	// Space again goes on from there (inside the loop).
	e.togglePlay(false, 0)
	if o := ops(); o[0].Tick != bar+bar/2 {
		t.Errorf("resume from %d", o[0].Tick)
	}
	// Home goes to the loop's start; with the loop off, to the top.
	e.toStart()
	if o := ops(); o[0].Kind != scoreOpSeek || o[0].Tick != bar {
		t.Errorf("Home with the loop on: %+v", o)
	}
	e.Sel = scoreSel{}
	e.loopSelection() // no bars selected: switches the loop off
	if o := ops(); len(o) != 1 || o[0].A != 0 || o[0].B != 0 || e.Transport.Loop.On || !e.Transport.Loop.Set {
		t.Errorf("L off: %+v %+v", o, e.Transport.Loop)
	}
	e.toStart()
	if o := ops(); o[0].Tick != 0 {
		t.Errorf("Home with the loop off: %+v", o)
	}
	// A note clicked moves the play head to it; P plays from it.
	e.toggleInput()
	e.clickWrite(0, 0, 3, frac(1, 2), pitchD("B4"))
	e.toggleInput()
	ops()
	if !e.seekToSel() || e.Transport.Head != 2*bar+bar/2 {
		t.Errorf("seek to the note: head %d", e.Transport.Head)
	}
	e.Transport.Head = 0
	e.playFromSelection()
	if o := ops(); o[len(o)-1].Kind != scoreOpPlay || o[len(o)-1].Tick != 2*bar+bar/2 {
		t.Errorf("P: %+v", o)
	}
	// From the end, Space starts at the top.
	e.Transport.Loop = scoreLoop{}
	e.Transport.Head = e.perf().Length
	e.togglePlay(false, 0)
	if o := ops(); o[0].Tick != 0 {
		t.Errorf("Space from the end: %+v", o)
	}
	// The tempo slider clamps, and is sent.
	e.setTempoScale(5)
	if o := ops(); e.Transport.Scale != 2 || o[0].Kind != scoreOpTempo || o[0].Scale != 2 {
		t.Errorf("slider: %v %+v", e.Transport.Scale, o)
	}
}

// The tempo slider written into the score: every mark scaled, or one mark
// at a bar; each one undo step, the slider back to 100%.
func TestScoreWriteTempo(t *testing.T) {
	e := scoreTestState(t)
	e.Score.Tempo = []score.TempoMark{{At: score.Pos{Bar: 1}, BPM: 120, Text: "Allegro"}, {At: score.Pos{Bar: 3}, BPM: 80}}
	if e.writeTempoScaled() {
		t.Error("wrote the slider at 100%")
	}
	e.setTempoScale(0.75)
	if !e.writeTempoScaled() {
		t.Fatal(e.Status)
	}
	if e.Score.Tempo[0].BPM != 90 || e.Score.Tempo[1].BPM != 60 || e.Score.Tempo[0].Text != "Allegro" || e.Transport.Scale != 1 {
		t.Errorf("scaled: %+v slider %v", e.Score.Tempo, e.Transport.Scale)
	}
	e.undoStep()
	if e.Score.Tempo[0].BPM != 120 {
		t.Errorf("undo: %+v", e.Score.Tempo)
	}
	// A mark at bar 2 at what is heard there; one dotted-quarter mark keeps
	// its unit.
	e.setTempoScale(1.5)
	if !e.writeTempoAt(2) {
		t.Fatal(e.Status)
	}
	if len(e.Score.Tempo) != 3 || e.Score.Tempo[1].At.Bar != 2 || e.Score.Tempo[1].BPM != 180 || e.Transport.Scale != 1 {
		t.Errorf("mark at bar 2: %+v", e.Score.Tempo)
	}
	e.Score.Tempo = []score.TempoMark{{At: score.Pos{Bar: 1}, BPM: 60, Unit: score.Duration{Base: score.Quarter, Dots: 1}}}
	e.touch()
	e.setTempoScale(0.5)
	e.writeTempoAt(1) // 90 quarters at 50% = 45 quarters = 30 dotted quarters
	if m := e.Score.Tempo[0]; len(e.Score.Tempo) != 1 || m.BPM != 30 || m.Unit.Dots != 1 {
		t.Errorf("dotted mark: %+v", e.Score.Tempo)
	}
}

// Tempo text with no number ("rit.", "a tempo", "Andante") survives the
// slider's writes: scaling leaves it relative, a mark set at its bar goes
// before a relative one and numbers a plain word.
func TestScoreWriteTempoKeepsTempoText(t *testing.T) {
	s := score.New("t")
	s.Bars = 4
	s.Tempo = []score.TempoMark{{At: score.Pos{Bar: 1}, BPM: 100}, {At: score.Pos{Bar: 2}, Text: "rit."}, {At: score.Pos{Bar: 3}, Text: "Andante"}}
	scoreScaleTempoMarks(s, 1.5)
	if s.Tempo[0].BPM != 150 || s.Tempo[1].BPM != 0 || s.Tempo[2].BPM != 0 {
		t.Errorf("scaled: %+v", s.Tempo)
	}
	scoreSetTempoAt(s, 2, 80)
	if len(s.Tempo) != 4 || s.Tempo[1].BPM != 80 || s.Tempo[2].Text != "rit." || s.Tempo[2].BPM != 0 {
		t.Errorf("a number at a rit.'s bar: %+v", s.Tempo)
	}
	scoreSetTempoAt(s, 3, 70)
	if s.Tempo[3].BPM != 70 || s.Tempo[3].Text != "Andante" || len(s.Tempo) != 4 {
		t.Errorf("a number for Andante: %+v", s.Tempo)
	}
	if w := scoreTempoWords(score.TempoMark{Text: "a tempo"}); w != "a tempo" {
		t.Errorf("words %q", w)
	}
}
