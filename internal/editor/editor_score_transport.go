package editor

// editor_score_transport.go — the Score editor's transport, with no ImGui in
// it: the play head, the loop region, the tempo slider and follow, and the
// arithmetic between the written score (a bar and an offset into it), the
// performance's ticks and the clock.
//
// The written score and the performance are different times. A repeat plays
// bar 5 twice, so "bar 5" is two ticks; the transport works in written
// positions wherever a person points (the ruler, a note, the selected bars)
// and turns them into the tick a bar first plays at only when it talks to the
// player (play.Player.MoveTo, PlayFrom, SetLoop). The loop region is kept as
// written positions too, so an edit that moves the music under it — a meter
// change, bars repeated — leaves it on the same bars.
//
// What the transport asks of the player is queued as ops (scorePlayerOp) the
// window hands to the player each frame, the way auditions are: that is what
// lets editor_score_transport_test.go drive the loop, the seeks and the tempo
// with no audio device.

import (
	"fmt"
	"math"

	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/interpret"
)

// The tempo slider's range: playback only, the score unchanged.
const (
	scoreTempoMin = 0.25
	scoreTempoMax = 2.0
)

// scoreLoop is the loop region: from A to B in written positions, B
// possibly a bar's end (Offset == the bar's length). Set says there is a
// region, On that playback loops it; a region switched off stays on the
// ruler, grey, until it is replaced.
type scoreLoop struct {
	Set, On bool
	A, B    score.Pos
}

// scoreTransport is the play head, the loop, the tempo slider and follow.
type scoreTransport struct {
	Loop scoreLoop
	// Follow scrolls the view to keep the play head in sight while playing.
	Follow bool
	// Scale is the tempo slider, scoreTempoMin..scoreTempoMax; 1 is the
	// written tempo.
	Scale float64
	// Head is the play head's tick while the transport is stopped; while it
	// plays the player's position is the truth.
	Head int64
}

func newScoreTransport() scoreTransport { return scoreTransport{Follow: true, Scale: 1} }

// scoreOpKind is one thing the transport wants the player to do.
type scoreOpKind uint8

const (
	// scoreOpSeek moves the play head (playing or not) to Tick.
	scoreOpSeek scoreOpKind = iota
	// scoreOpPlay starts the transport at Tick.
	scoreOpPlay
	scoreOpStop
	// scoreOpLoop loops A..B (ticks); A >= B clears the loop.
	scoreOpLoop
	// scoreOpTempo sets the tempo scale to Scale.
	scoreOpTempo
	// scoreOpSwap plays Line on the library instrument Name from now on.
	scoreOpSwap
)

// scorePlayerOp is a queued request to the player.
type scorePlayerOp struct {
	Kind  scoreOpKind
	Tick  int64
	A, B  int64
	Scale float64
	Line  int
	Name  string
}

// ---------- written positions, ticks and seconds ----------

// scoreBeatOffsets are where a bar's beats start, as the meter counts them
// (Meter.Beats: 6/8 is two, 3+2+2/8 three); a short bar — a pickup —
// counts its beats back from its end, as it is played.
func scoreBeatOffsets(s *score.Score, bar int) []score.Frac {
	length := s.BarLength(bar)
	beats := s.MeterAt(bar).Beats()
	full := score.Frac{}
	for _, b := range beats {
		full = full.Add(b)
	}
	out := []score.Frac{{}}
	if full.Eq(length) {
		at := score.Frac{}
		for _, b := range beats[:len(beats)-1] {
			at = at.Add(b)
			out = append(out, at)
		}
		return out
	}
	// Back from the end: the pickup's beats are the last beats of a bar.
	at := length
	var back []score.Frac
	for i := len(beats) - 1; i >= 0; i-- {
		at = at.Sub(beats[i])
		if !score.F(0, 1).Less(at) {
			break
		}
		back = append(back, at)
	}
	for i := len(back) - 1; i >= 0; i-- {
		out = append(out, back[i])
	}
	return out
}

// scoreSnapBeat is the beat boundary nearest an offset (whole notes) into a
// bar: one of its beats, or its end, which is the next bar's start.
func scoreSnapBeat(s *score.Score, bar int, off float64) score.Pos {
	length := s.BarLength(bar)
	best, bd := score.Frac{}, math.Inf(1)
	for _, c := range append(scoreBeatOffsets(s, bar), length) {
		if d := math.Abs(c.Float() - off); d < bd {
			best, bd = c, d
		}
	}
	return score.Pos{Bar: bar, Offset: best}
}

// scorePosTick is the tick a written position first plays at, -1 when its
// bar never plays.
func scorePosTick(perf *interpret.Performance, p score.Pos) int64 {
	if perf == nil {
		return -1
	}
	t := perf.BarTick(p.Bar)
	if t < 0 {
		return -1
	}
	return t + p.Offset.Ticks()
}

// scoreTickPos is the written bar a tick plays and how far into it, in
// whole notes, with the time through it (a repeat's second pass is 2).
func scoreTickPos(perf *interpret.Performance, tick int64) (bar int, off float64, pass int) {
	if perf == nil || len(perf.Bars) == 0 {
		return 1, 0, 1
	}
	b := perf.Bars[perf.BarAt(float64(tick))]
	off = float64(tick-b.Tick) / float64(score.TicksPerWhole)
	return b.Bar, math.Max(0, off), b.Pass
}

// scoreClock writes seconds as the transport shows them: "1:05.3".
func scoreClock(sec float64) string {
	if sec < 0 || math.IsNaN(sec) {
		sec = 0
	}
	sec = math.Floor(sec*10+1e-6) / 10
	m := int(sec / 60)
	return fmt.Sprintf("%d:%04.1f", m, sec-float64(m*60))
}

// scoreReadout is the transport's position: "Bar 5 · Beat 2 · 0:07.3" —
// the clock as heard, at the tempo slider's speed; a repeat's later pass
// says which.
func scoreReadout(perf *interpret.Performance, tick int64, scale float64) string {
	if perf == nil || len(perf.Bars) == 0 {
		return "Bar 1 · Beat 1 · 0:00.0"
	}
	bar, _, pass := scoreTickPos(perf, tick)
	beat := 1
	if k := perf.BeatAt(float64(tick)); k >= 0 {
		beat = perf.Beats[k].Beat
	}
	if scale <= 0 {
		scale = 1
	}
	where := fmt.Sprintf("Bar %d", bar)
	if pass > 1 {
		where += fmt.Sprintf(" (x%d)", pass)
	}
	return fmt.Sprintf("%s · Beat %d · %s", where, beat, scoreClock(perf.Tempo.Seconds(float64(tick))/scale))
}

// scoreTempoText is the slider's label: the tempo heard at a tick, "72 bpm
// (75%)", in quarters a minute.
func scoreTempoText(perf *interpret.Performance, tick int64, scale float64) string {
	qpm := 120.0
	if perf != nil && perf.Tempo != nil {
		qpm = perf.Tempo.QPM(float64(tick))
	}
	return fmt.Sprintf("%.0f bpm (%.0f%%)", qpm*scale, scale*100)
}

// scoreClampScale keeps the slider in its range, on whole percents.
func scoreClampScale(v float64) float64 {
	return math.Round(math.Max(scoreTempoMin, math.Min(scoreTempoMax, v))*100) / 100
}

// ---------- the loop ----------

// scoreLoopBetween is a loop over two written positions in either order,
// snapped to beats; two on one beat loop that beat.
func scoreLoopBetween(s *score.Score, a, b score.Pos) scoreLoop {
	if score.PosLess(b, a) {
		a, b = b, a
	}
	if !score.PosLess(a, b) {
		b = scoreNextBeat(s, a)
	}
	return scoreLoop{Set: true, On: true, A: a, B: b}
}

// scoreNextBeat is the beat boundary after a position (a bar's end at its
// last beat).
func scoreNextBeat(s *score.Score, p score.Pos) score.Pos {
	for _, o := range scoreBeatOffsets(s, p.Bar) {
		if p.Offset.Less(o) {
			return score.Pos{Bar: p.Bar, Offset: o}
		}
	}
	return score.Pos{Bar: p.Bar, Offset: s.BarLength(p.Bar)}
}

// scoreLoopBars loops bars a..z whole.
func scoreLoopBars(s *score.Score, a, z int) scoreLoop {
	if z < a {
		a, z = z, a
	}
	return scoreLoop{Set: true, On: true, A: score.Pos{Bar: a}, B: score.Pos{Bar: z, Offset: s.BarLength(z)}}
}

// scoreLoopTicks is a loop's span in the performance: from where A first
// plays to where B does. ok is false for a loop the performance cannot
// hold (a bar that never plays, an empty span).
func scoreLoopTicks(perf *interpret.Performance, l scoreLoop) (a, b int64, ok bool) {
	if !l.Set {
		return 0, 0, false
	}
	a, b = scorePosTick(perf, l.A), scorePosTick(perf, l.B)
	return a, b, a >= 0 && b > a
}

// scoreFitLoop keeps a loop inside the score after an edit took bars away
// or shortened them: a region wholly past the end goes, one that runs past
// it stops at the end.
func scoreFitLoop(s *score.Score, l scoreLoop) scoreLoop {
	if !l.Set {
		return l
	}
	n := s.Measures()
	if l.A.Bar > n {
		return scoreLoop{}
	}
	fit := func(p score.Pos) score.Pos {
		if p.Bar > n {
			return score.Pos{Bar: n, Offset: s.BarLength(n)}
		}
		if length := s.BarLength(p.Bar); length.Less(p.Offset) {
			p.Offset = length
		}
		return p
	}
	l.A, l.B = fit(l.A), fit(l.B)
	if !score.PosLess(l.A, l.B) {
		return scoreLoop{}
	}
	return l
}

// scoreLoopText describes a loop for the status line: "Loop: bar 2 beat 1
// to bar 4 beat 3", a bar's end said as the next bar's start.
func scoreLoopText(s *score.Score, l scoreLoop) string {
	if !l.Set {
		return "No loop"
	}
	at := func(p score.Pos) string {
		if p.Offset.Eq(s.BarLength(p.Bar)) && !p.Offset.IsZero() {
			if p.Bar >= s.Measures() {
				return fmt.Sprintf("the end of bar %d", p.Bar)
			}
			p = score.Pos{Bar: p.Bar + 1}
		}
		return fmt.Sprintf("bar %d %s", p.Bar, scoreBeatText(s, p.Bar, p.Offset))
	}
	state := "Loop"
	if !l.On {
		state = "Loop (off)"
	}
	return fmt.Sprintf("%s: %s to %s", state, at(l.A), at(l.B))
}

// scorePlayStart is where Play starts from a play head: the loop's start
// when the loop is on and the head is outside it, the top when the head is
// at the end.
func scorePlayStart(perf *interpret.Performance, t scoreTransport, head int64) int64 {
	if perf == nil {
		return 0
	}
	if t.Loop.On {
		if a, b, ok := scoreLoopTicks(perf, t.Loop); ok && (head < a || head >= b) {
			return a
		}
	}
	if head >= perf.Length || head < 0 {
		return 0
	}
	return head
}

// ---------- tempo marks ----------

// scoreScaleTempoMarks writes the slider into the score: every tempo mark
// with a number at scale times its tempo (to a whole bpm, at least 1), so
// the piece plays as it was just heard and its tempo changes keep their
// proportions. Text marks ("rit.", "a tempo") are relative already.
func scoreScaleTempoMarks(s *score.Score, scale float64) {
	for i := range s.Tempo {
		if s.Tempo[i].BPM > 0 {
			s.Tempo[i].BPM = math.Max(1, math.Round(s.Tempo[i].BPM*scale))
		}
	}
}

// scoreSetTempoAt puts a tempo mark of bpm quarters a minute at a bar's
// start (replacing one there, keeping its text and unit), so the music
// from there plays at what the slider gave it.
func scoreSetTempoAt(s *score.Score, bar int, qpm float64) {
	at := score.Pos{Bar: bar}
	mark := score.TempoMark{At: at, BPM: math.Max(1, math.Round(qpm)), Unit: score.Duration{Base: score.Quarter}}
	for i := range s.Tempo {
		t := &s.Tempo[i]
		if t.At.Bar != bar || !t.At.Offset.IsZero() {
			continue
		}
		switch {
		case t.BPM > 0:
			// Keep the mark's unit: a dotted-quarter mark stays one.
			t.BPM = math.Max(1, math.Round(qpm/(t.QuarterBPM()/t.BPM)))
			t.Ramp = false
		case t.Change() == score.TempoSet:
			// A tempo word with no number ("Andante") gets one.
			t.BPM, t.Unit = mark.BPM, mark.Unit
		default:
			// A relative mark ("rit.", "a tempo") keeps its meaning; the
			// number goes just before it, so it changes from there.
			s.Tempo = append(s.Tempo[:i], append([]score.TempoMark{mark}, s.Tempo[i:]...)...)
		}
		return
	}
	s.Tempo = append(s.Tempo, mark)
	s.SortMaps()
}

// scoreTempoWords is "q = 72" for a status line, in the mark's unit.
func scoreTempoWords(t score.TempoMark) string {
	if t.BPM <= 0 {
		return t.Text
	}
	u := score.Duration{Base: score.Quarter}
	if t.Unit != (score.Duration{}) {
		u = t.Unit
	}
	return fmt.Sprintf("%s = %.0f", edit.DurName(u), t.BPM)
}

// ---------- the transport on the editing model ----------

// perf is the score as it performs, interpreted once per edit (a mixer
// change does not move a note); nil for a score the interpreter refuses.
func (e *scoreState) perf() *interpret.Performance {
	if e.perfComputed && e.perfScore == e.Score && e.perfRev == e.Rev {
		return e.perfCache
	}
	p, err := interpret.Interpret(e.Score, interpret.Options{})
	if err != nil {
		p = nil
	}
	e.perfCache, e.perfScore, e.perfRev, e.perfComputed = p, e.Score, e.Rev, true
	return p
}

func (e *scoreState) op(o scorePlayerOp) { e.ops = append(e.ops, o) }

// takeOps hands the window the player requests and forgets them.
func (e *scoreState) takeOps() []scorePlayerOp {
	o := e.ops
	e.ops = nil
	return o
}

// seek moves the play head to a written position (where its bar first
// plays).
func (e *scoreState) seek(p score.Pos) bool {
	t := scorePosTick(e.perf(), p)
	if t < 0 {
		e.Status = fmt.Sprintf("Bar %d does not play", p.Bar)
		return false
	}
	e.seekTick(t)
	return true
}

// seekTick moves the play head to a tick of the performance, playing or
// not.
func (e *scoreState) seekTick(t int64) {
	if perf := e.perf(); perf != nil {
		t = max(0, min(t, perf.Length))
	}
	e.Transport.Head = t
	e.op(scorePlayerOp{Kind: scoreOpSeek, Tick: t})
}

// seekToSel moves the play head to the selection: a note's onset, a bar's
// start.
func (e *scoreState) seekToSel() bool {
	return e.seek(score.Pos{Bar: e.selBar(), Offset: e.selOnset()})
}

// toStart is Home: the loop's start when the loop is on, else the top.
func (e *scoreState) toStart() {
	if e.Transport.Loop.On {
		if a, _, ok := scoreLoopTicks(e.perf(), e.Transport.Loop); ok {
			e.seekTick(a)
			return
		}
	}
	e.seekTick(0)
}

// loopSelection is L: loop the selected bars; with none selected, switch
// the loop there is on or off.
func (e *scoreState) loopSelection() {
	switch {
	case e.Sel.Kind == scoreSelBars:
		e.setLoop(scoreLoopBars(e.Score, e.Sel.BarA, e.Sel.BarB))
	case e.Transport.Loop.Set:
		e.toggleLoop()
	default:
		e.Status = "Select bars, then L loops them (or drag along the ruler)"
	}
}

// toggleLoop is the Loop button: the region on or off; with no region yet,
// the selected bars, or the selected note's bar.
func (e *scoreState) toggleLoop() {
	switch {
	case e.Transport.Loop.Set:
		e.Transport.Loop.On = !e.Transport.Loop.On
		e.sendLoop()
		e.Status = scoreLoopText(e.Score, e.Transport.Loop)
	case e.Sel.Kind == scoreSelBars:
		e.setLoop(scoreLoopBars(e.Score, e.Sel.BarA, e.Sel.BarB))
	case e.Sel.Kind != scoreSelNone:
		b := e.selBar()
		e.setLoop(scoreLoopBars(e.Score, b, b))
	default:
		e.Status = "Select bars to loop, or drag along the ruler"
	}
}

// setLoop replaces the loop region.
func (e *scoreState) setLoop(l scoreLoop) {
	e.Transport.Loop = scoreFitLoop(e.Score, l)
	e.sendLoop()
	e.Status = scoreLoopText(e.Score, e.Transport.Loop)
}

// sendLoop tells the player the loop as it stands (cleared when off).
func (e *scoreState) sendLoop() {
	o := scorePlayerOp{Kind: scoreOpLoop}
	if e.Transport.Loop.On {
		if a, b, ok := scoreLoopTicks(e.perf(), e.Transport.Loop); ok {
			o.A, o.B = a, b
		}
	}
	e.op(o)
}

// setTempoScale moves the tempo slider: playback only.
func (e *scoreState) setTempoScale(v float64) {
	e.Transport.Scale = scoreClampScale(v)
	e.op(scorePlayerOp{Kind: scoreOpTempo, Scale: e.Transport.Scale})
}

// writeTempoScaled writes the slider into the score as tempo marks: every
// mark at the slider's speed, the slider back to 100%. One undo step.
func (e *scoreState) writeTempoScaled() bool {
	k := e.Transport.Scale
	if k == 1 {
		e.Status = "The tempo slider is at 100%: nothing to write"
		return false
	}
	if !e.change(func() error {
		scoreScaleTempoMarks(e.Score, k)
		return nil
	}) {
		return false
	}
	e.setTempoScale(1)
	if len(e.Score.Tempo) == 1 {
		e.Status = "Tempo mark written: " + scoreTempoWords(e.Score.Tempo[0])
	} else {
		e.Status = fmt.Sprintf("%d tempo marks scaled to %.0f%%", len(e.Score.Tempo), k*100)
	}
	return true
}

// writeTempoAt writes the tempo heard at a bar's start as a tempo mark
// there (the slider's speed times the written tempo), the slider back to
// 100%: the music before it keeps its own tempo.
func (e *scoreState) writeTempoAt(bar int) bool {
	perf := e.perf()
	t := scorePosTick(perf, score.Pos{Bar: bar})
	if t < 0 {
		e.Status = fmt.Sprintf("Bar %d does not play", bar)
		return false
	}
	qpm := perf.Tempo.QPM(float64(t)) * e.Transport.Scale
	if !e.change(func() error {
		scoreSetTempoAt(e.Score, bar, qpm)
		return nil
	}) {
		return false
	}
	e.setTempoScale(1)
	for _, m := range e.Score.Tempo {
		if m.At.Bar == bar && m.At.Offset.IsZero() {
			e.Status = fmt.Sprintf("Tempo mark at bar %d: %s", bar, scoreTempoWords(m))
		}
	}
	return true
}

// headBar is the written bar the play head is in.
func (e *scoreState) headBar() int {
	b, _, _ := scoreTickPos(e.perf(), e.Transport.Head)
	return b
}

// togglePlay is Space: stop where the music is (at, the player's tick; the
// play head stays there), or play from the play head — the loop's start
// when the loop is on and the head is outside it, the top from the end.
func (e *scoreState) togglePlay(playing bool, at int64) {
	if playing {
		e.Transport.Head = max(at, 0)
		e.op(scorePlayerOp{Kind: scoreOpStop})
		return
	}
	t := scorePlayStart(e.perf(), e.Transport, e.Transport.Head)
	e.Transport.Head = t
	e.op(scorePlayerOp{Kind: scoreOpPlay, Tick: t})
}

// playFromSelection is P: play from the selected note or bar (from the
// play head with nothing selected).
func (e *scoreState) playFromSelection() {
	t := e.Transport.Head
	if e.Sel.Kind != scoreSelNone {
		t = scorePosTick(e.perf(), score.Pos{Bar: e.selBar(), Offset: e.selOnset()})
		if t < 0 {
			e.Status = fmt.Sprintf("Bar %d does not play", e.selBar())
			return
		}
	}
	e.Transport.Head = t
	e.op(scorePlayerOp{Kind: scoreOpPlay, Tick: t})
}
