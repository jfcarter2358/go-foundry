package play

import (
	"fmt"
	"math"
	"testing"

	"foundry/internal/score"
	"foundry/internal/tracker"
	"foundry/internal/tracker/fx"
)

// held is one line holding a DC note through two bars of 4/4 at 120: two
// seconds a bar, every frame's level readable off the output.
func held(t *testing.T) *score.Score {
	return build(t, "bars: 2\ntempo: [{bar: 1, bpm: 120}]", "A|dc/0.5|0|| wC4~ | wC4 |")
}

func renderFrom(t *testing.T, s *score.Score, block int) []float32 {
	t.Helper()
	return Render(prepare(t, s), RenderOptions{Rate: rate, Block: block, Tail: 0.1})
}

func absAt(out []float32, f int) float64 { return math.Abs(float64(out[f*2])) }

func TestInsertsPlay(t *testing.T) {
	useTestInstruments(t)
	dry := renderFrom(t, held(t), 480)
	s := held(t)
	eq := score.NewFX(fx.KindEQ, fx.Insert)
	eq.SetParam("output", -6.0206)
	s.Lines[0].Inserts = []score.FX{eq}
	wet := renderFrom(t, s, 480)
	if r := absAt(wet, 48000) / absAt(dry, 48000); math.Abs(r-0.5) > 1e-3 {
		t.Errorf("an EQ at -6 dB plays the line at %g of its level", r)
	}
	// Bypassed, it is out of the signal.
	s.Lines[0].Inserts[0].Bypass = true
	if r := absAt(renderFrom(t, s, 480), 48000) / absAt(dry, 48000); r != 1 {
		t.Errorf("a bypassed EQ plays the line at %g", r)
	}
	// An effect this build lacks is reported and plays nothing.
	s.Lines[0].Inserts = []score.FX{{Kind: "flanger"}}
	if _, errs, err := Prepare(s); err != nil || len(errs) != 1 {
		t.Errorf("an unknown insert: %v %v", errs, err)
	}
}

func TestBusesAndMasterPlayTheirChains(t *testing.T) {
	useTestInstruments(t)
	dry := renderFrom(t, held(t), 480)
	level := absAt(dry, 48000)
	// The fader down, a pre-fader send to bus A (empty: it returns what it
	// is sent) at half, bus A returned at 1 and panned hard right.
	s := held(t)
	l := s.Lines[0]
	l.Volume, l.SendA, l.PreA = 0, 0.5, true
	s.Buses.A = score.Bus{Return: 1, Pan: 1}
	out := renderFrom(t, s, 480)
	if v := math.Abs(float64(out[48000*2+1])); math.Abs(v/level-0.5) > 1e-3 || out[48000*2] != 0 {
		t.Errorf("pre-fader send through bus A: %g left, %g right (dry %g)", out[48000*2], v, level)
	}
	// Post-fader with the fader down sends nothing.
	l.PreA = false
	if out := renderFrom(t, s, 480); absAt(out, 48000) != 0 || out[48000*2+1] != 0 {
		t.Error("a post-fader send with the fader down was heard")
	}
	// A muted bus returns nothing.
	l.PreA = true
	s.Buses.A.Mute = true
	if out := renderFrom(t, s, 480); out[48000*2+1] != 0 {
		t.Error("a muted bus was heard")
	}
	// The master chain: a limiter at -12 dB holds a loud mix under it.
	s = held(t)
	s.Lines[0].Volume = 2
	lim := score.NewFX(fx.KindLimiter, fx.Insert)
	lim.SetParam("ceiling", -12)
	s.Master = []score.FX{lim}
	out = renderFrom(t, s, 480)
	if pk := float64(tracker.Peak(out)); pk > math.Pow(10, -12.0/20)*1.0001 || pk < 0.2 {
		t.Errorf("master limiter at -12 dB: peak %g", pk)
	}
}

// A step in a lane changes the line's level on the frame it falls on —
// whatever the render's block size — reaching its new value within a
// control block.
func TestAStepLaneIsSampleAccurate(t *testing.T) {
	useTestInstruments(t)
	for _, block := range []int{480, 333, 1024} {
		s := held(t)
		// At bar 1 beat 3 (one second in, frame 48000) from full to half.
		s.Lines[0].Lanes = []score.Lane{{Name: "volume", Points: []score.LanePoint{
			{At: score.Pos{Bar: 1}, Value: 1, Shape: score.LaneStep},
			{At: score.Pos{Bar: 1, Offset: score.F(1, 2)}, Value: 0.5}}}}
		out := renderFrom(t, s, block)
		full := absAt(out, 47999)
		if full == 0 || absAt(out, 47000) != full {
			t.Fatalf("block %d: level before the step %g, %g", block, absAt(out, 47000), full)
		}
		if got := absAt(out, 48000+64) / full; math.Abs(got-0.5) > 1e-4 {
			t.Errorf("block %d: a control block after the step the level is %g of full", block, got)
		}
		if absAt(out, 48001) >= full {
			t.Errorf("block %d: the step had not begun a frame after its point", block)
		}
	}
}

// A straight lane is followed sample by sample: no stair steps once a
// block, the level halfway along is halfway.
func TestALinearLaneIsFollowedSmoothly(t *testing.T) {
	useTestInstruments(t)
	dry := renderFrom(t, held(t), 480)
	s := held(t)
	s.Lines[0].Lanes = []score.Lane{{Name: "volume", Points: []score.LanePoint{
		{At: score.Pos{Bar: 1}, Value: 1}, {At: score.Pos{Bar: 2}, Value: 0}}}}
	for _, block := range []int{480, 701} {
		out := renderFrom(t, s, block)
		for f := 1000; f < 95000; f += 17 {
			want := 1 - float64(f)/96000
			if got := absAt(out, f) / absAt(dry, f); math.Abs(got-want) > 2e-4 {
				t.Fatalf("block %d, frame %d: level %g of dry, the lane is at %g", block, f, got, want)
			}
		}
	}
}

// An insert's parameter follows its lane: an EQ's output from 0 to -24 dB
// across a bar.
func TestALaneMovesAnEffectParameter(t *testing.T) {
	useTestInstruments(t)
	dry := renderFrom(t, held(t), 480)
	s := held(t)
	s.Lines[0].Inserts = []score.FX{score.NewFX(fx.KindEQ, fx.Insert)}
	s.Lines[0].Lanes = []score.Lane{{Name: "eq.output", Points: []score.LanePoint{
		{At: score.Pos{Bar: 1}, Value: 0}, {At: score.Pos{Bar: 2}, Value: -24}}}}
	for _, block := range []int{480, 701} {
		out := renderFrom(t, s, block)
		for f := 1000; f < 95000; f += 101 {
			want := -24 * float64(f) / 96000
			if got := 20 * math.Log10(absAt(out, f)/absAt(dry, f)); math.Abs(got-want) > 0.05 {
				t.Fatalf("block %d, frame %d: the EQ's output is %.3f dB, the lane %.3f", block, f, got, want)
			}
		}
		if got := 20 * math.Log10(absAt(out, 96000+100)/absAt(dry, 96000+100)); math.Abs(got+24) > 0.01 {
			t.Errorf("after the lane: %.2f dB", got)
		}
	}
	// The same lane named for the second EQ moves only that one.
	s.Lines[0].Inserts = append(s.Lines[0].Inserts, score.NewFX(fx.KindEQ, fx.Insert))
	s.Lines[0].Lanes[0].Name = "eq#2.output"
	prog := prepare(t, s)
	if a := prog.lanes[0]; len(a) != 1 || a[0].slot != 1 {
		t.Errorf("eq#2's lane resolves to %+v", a)
	}
}

// The dynamics (velocity) and a volume lane multiply.
func TestDynamicsComposeWithTheVolumeLane(t *testing.T) {
	useTestInstruments(t)
	loud := build(t, "bars: 1\ntempo: [{bar: 1, bpm: 120}]", "A|dc/0.5|0|| wC4(f) |")
	soft := build(t, "bars: 1\ntempo: [{bar: 1, bpm: 120}]", "A|dc/0.5|0|| wC4(p) |")
	soft.Lines[0].Lanes = []score.Lane{{Name: "volume", Points: []score.LanePoint{{At: score.Pos{Bar: 1}, Value: 0.5}}}}
	a, b := renderFrom(t, loud, 480), renderFrom(t, soft, 480)
	want := float64(VelocityGain(score.Dynamics["p"].Level)/VelocityGain(score.Dynamics["f"].Level)) * 0.5
	if got := absAt(b, 24000) / absAt(a, 24000); math.Abs(got-want) > 1e-3 {
		t.Errorf("p under a 0.5 volume lane plays at %g of f, want %g", got, want)
	}
}

// Every lane kind and an effect lane cue with no allocation.
func TestAutomationRendersWithoutAllocating(t *testing.T) {
	useTestInstruments(t)
	s := held(t)
	s.Lines[0].Inserts = []score.FX{score.NewFX(fx.KindFilter, fx.Insert), score.NewFX(fx.KindDelay, fx.Insert)}
	pts := func(a, b float32) []score.LanePoint {
		return []score.LanePoint{{At: score.Pos{Bar: 1}, Value: a, Shape: score.LaneCurve, Curve: 0.5}, {At: score.Pos{Bar: 2}, Value: b}}
	}
	s.Lines[0].Lanes = []score.Lane{{Name: "volume", Points: pts(1, 0.2)}, {Name: "pan", Points: pts(-1, 1)},
		{Name: "cutoff", Points: pts(200, 8000)}, {Name: "send_a", Points: pts(0, 1)}, {Name: "send_b", Points: pts(1, 0)},
		{Name: "filter.cutoff", Points: pts(300, 3000)}, {Name: "delay.feedback", Points: pts(0, 0.8)}}
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	buf := make([]float32, 480*2)
	p.Render(buf, 480, rate)
	if allocs := testing.AllocsPerRun(100, func() { p.Render(buf, 480, rate) }); allocs != 0 {
		t.Errorf("%v allocations per block with every lane moving", allocs)
	}
}

// The editor's live effect commands, and SetEffects keeping a chain's
// sound when its effects stay.
func TestLiveEffectCommands(t *testing.T) {
	useTestInstruments(t)
	s := held(t)
	s.Lines[0].Inserts = []score.FX{score.NewFX(fx.KindEQ, fx.Insert)}
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	buf := make([]float32, 4800*2)
	p.Render(buf, 4800, rate)
	full := absAt(buf, 4799)
	p.SetFX(0, 0, fx.KindEQ.Spec().Param("output"), -6.0206)
	p.Render(buf, 4800, rate)
	if r := absAt(buf, 4799) / full; math.Abs(r-0.5) > 1e-3 {
		t.Errorf("SetFX -6 dB: %g", r)
	}
	p.SetBypass(0, 0, true)
	p.Render(buf, 4800, rate)
	if r := absAt(buf, 4799) / full; math.Abs(r-1) > 1e-4 {
		t.Errorf("SetBypass: %g", r)
	}
	// SetEffects with the same effects keeps the chain (and so its
	// state); with others it is a new chain.
	before := p.eng.Chain(0)
	s2 := s.Clone()
	s2.Lines[0].Inserts[0].SetParam("output", -12)
	if _, err := p.SetEffects(s2); err != nil {
		t.Fatal(err)
	}
	p.Render(buf, 4800, rate)
	if p.eng.Chain(0) != before {
		t.Error("SetEffects replaced a chain whose effects did not change")
	}
	if r := 20 * math.Log10(absAt(buf, 4799)/full); math.Abs(r+12) > 0.05 {
		t.Errorf("SetEffects -12 dB: %.2f dB (the bypass came back from the score too)", r)
	}
	s3 := s2.Clone()
	s3.Lines[0].Inserts = append(s3.Lines[0].Inserts, score.NewFX(fx.KindDrive, fx.Insert))
	p.SetEffects(s3)
	p.Render(buf, 4800, rate)
	if c := p.eng.Chain(0); c == before || c.Len() != 2 {
		t.Error("SetEffects kept a chain whose effects changed")
	}
	if p.Performance() != p.game.Perf {
		t.Error("performance")
	}
	// The buses live.
	p.SetBus(0, 0.3, 0.5, true)
	p.Render(buf, 480, rate)
}

// A swung score's notes start on the frames their swung ticks give.
func TestSwingPlays(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 1\ntempo: [{bar: 1, bpm: 120}]\nplayback: {swing: {amount: 0.75}}", "A|dc/0.5|0|| eC4 er eD4 er eE4 er eF4 er |")
	prog := prepare(t, s)
	out := Render(prog, RenderOptions{Rate: rate, Tail: 0.1})
	// Each beat is 24000 frames; the off-beat rests start at 3/4 of it.
	eqInts(t, "swung onsets", onsets(out), []int{0, 24000, 48000, 72000})
	if n := prog.Perf.Notes[0]; math.Abs(n.End-n.Start-0.375) > 1e-9 {
		t.Errorf("the swung eighth lasts %g s, want 0.375", n.End-n.Start)
	}
}

// A held note is still found in an edited performance when humanise has
// moved the notes off their ticks' order.
func TestSameNoteUnderHumanise(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 1\ntempo: [{bar: 1, bpm: 120}]\nplayback: {humanize: {timing: 40, seed: 2}}", "A|dc/0.5|0|| xC4 xD4 xE4 xF4 xG4 xA4 xB4 xC5 xC4 xD4 xE4 xF4 xG4 xA4 xB4 xC5 h.r |")
	a := prepare(t, s).Perf
	b := prepare(t, s.Clone()).Perf
	out := 0
	for i := 1; i < len(a.Notes); i++ {
		if a.Notes[i].Tick < a.Notes[i-1].Tick {
			out++
		}
	}
	if out == 0 {
		t.Fatal("the humanise kept every note in tick order: the test proves nothing")
	}
	for i := range a.Notes {
		if j := sameNote(a, b, i); j != i {
			t.Errorf("note %d found as %d", i, j)
		}
	}
}

// Many automated lines under a long device buffer: more lane cues than the
// engine's queue holds at laneStep. The lanes are cued coarser instead of
// being refused, so every line still reaches each value of its lane, and
// notes keep their room in the queue.
func TestManyMovingLanesInALongBlock(t *testing.T) {
	useTestInstruments(t)
	const n = 40
	var lines []string
	for i := 0; i < n; i++ {
		lines = append(lines, fmt.Sprintf("L%d|dc/0.5|0|| wC4~ | wC4 |", i))
	}
	mk := func(lanes bool) *score.Score {
		s := build(t, "bars: 2\ntempo: [{bar: 1, bpm: 120}]", lines...)
		for _, l := range s.Lines {
			l.Volume = 1.0 / n
			if lanes {
				l.Lanes = []score.Lane{{Name: "volume", Points: []score.LanePoint{
					{At: score.Pos{Bar: 1}, Value: 1}, {At: score.Pos{Bar: 2}, Value: 0}}}}
			}
		}
		return s
	}
	dry := renderFrom(t, mk(false), 8192)
	out := renderFrom(t, mk(true), 8192)
	for f := 1000; f < 95000; f += 101 {
		want := 1 - float64(f)/96000
		// Coarse pieces of a straight lane are still on the line: only
		// the per-frame ramp's rounding differs.
		if got := absAt(out, f) / absAt(dry, f); math.Abs(got-want) > 2e-3 {
			t.Errorf("frame %d: level %g of dry, the lanes are at %g", f, got, want)
		}
	}
}

// The master's lanes: its volume fades the whole mix in a straight line,
// sample by sample; its effects' settings follow their lanes.
func TestMasterLanes(t *testing.T) {
	useTestInstruments(t)
	dry := renderFrom(t, held(t), 480)
	s := held(t)
	s.MasterLanes = []score.Lane{{Name: "volume", Points: []score.LanePoint{
		{At: score.Pos{Bar: 1}, Value: 1}, {At: score.Pos{Bar: 2}, Value: 0}}}}
	for _, block := range []int{480, 701} {
		out := renderFrom(t, s, block)
		for f := 1000; f < 95000; f += 37 {
			want := 1 - float64(f)/96000
			if got := absAt(out, f) / absAt(dry, f); math.Abs(got-want) > 2e-4 {
				t.Fatalf("block %d, frame %d: the mix at %g of dry, the master lane at %g", block, f, got, want)
			}
		}
	}
	// A step: from full to a quarter on the frame (a master gain of 2
	// under it multiplies).
	s = held(t)
	s.Gain = 2
	s.MasterLanes = []score.Lane{{Name: "volume", Points: []score.LanePoint{
		{At: score.Pos{Bar: 1}, Value: 1, Shape: score.LaneStep}, {At: score.Pos{Bar: 1, Offset: score.F(1, 2)}, Value: 0.25}}}}
	out := renderFrom(t, s, 333)
	if got := absAt(out, 47990) / absAt(dry, 47990); math.Abs(got-2) > 1e-4 {
		t.Errorf("before the step: %g of dry, want 2", got)
	}
	if got := absAt(out, 48000+64) / absAt(dry, 48000+64); math.Abs(got-0.5) > 1e-4 {
		t.Errorf("after the step: %g of dry, want 0.5", got)
	}
	// An effect on the master: its EQ's output across a bar.
	s = held(t)
	s.Master = []score.FX{score.NewFX(fx.KindEQ, fx.Insert)}
	s.MasterLanes = []score.Lane{{Name: "eq.output", Points: []score.LanePoint{
		{At: score.Pos{Bar: 1}, Value: 0}, {At: score.Pos{Bar: 2}, Value: -24}}}}
	out = renderFrom(t, s, 480)
	for f := 1000; f < 95000; f += 101 {
		want := -24 * float64(f) / 96000
		if got := 20 * math.Log10(absAt(out, f)/absAt(dry, f)); math.Abs(got-want) > 0.05 {
			t.Fatalf("frame %d: the master EQ's output is %.3f dB, the lane %.3f", f, got, want)
		}
	}
}

// A bus's lanes: its return level and pan, and its effects' settings.
func TestBusLanes(t *testing.T) {
	useTestInstruments(t)
	dry := renderFrom(t, held(t), 480)
	// The line heard only through a pre-fader send to bus A at full; the
	// bus's return falls from 1 to 0 across bar 1 while its pan holds hard
	// right.
	mk := func() *score.Score {
		s := held(t)
		s.Lines[0].Volume, s.Lines[0].SendA, s.Lines[0].PreA = 0, 1, true
		s.Buses.A = score.Bus{Return: 1, Pan: 1}
		return s
	}
	s := mk()
	s.Buses.A.Lanes = []score.Lane{{Name: "return", Points: []score.LanePoint{
		{At: score.Pos{Bar: 1}, Value: 1}, {At: score.Pos{Bar: 2}, Value: 0}}}}
	out := renderFrom(t, s, 701)
	for f := 1000; f < 95000; f += 53 {
		want := 1 - float64(f)/96000
		if got := math.Abs(float64(out[f*2+1])) / absAt(dry, f); math.Abs(got-want) > 2e-4 || out[f*2] != 0 {
			t.Fatalf("frame %d: bus A returns %g of dry (left %g), the lane %g", f, got, out[f*2], want)
		}
	}
	// Its pan lane sweeps it from hard right to hard left.
	s = mk()
	s.Buses.A.Lanes = []score.Lane{{Name: "pan", Points: []score.LanePoint{
		{At: score.Pos{Bar: 1}, Value: 1}, {At: score.Pos{Bar: 2}, Value: -1}}}}
	out = renderFrom(t, s, 480)
	if l, r := absAt(out, 1000), math.Abs(float64(out[1000*2+1])); l > r*0.05 {
		t.Errorf("start of the pan lane: left %g right %g", l, r)
	}
	if l, r := absAt(out, 94000), math.Abs(float64(out[94000*2+1])); r > l*0.05 {
		t.Errorf("end of the pan lane: left %g right %g", l, r)
	}
	// An effect on bus B: its EQ's output.
	s = held(t)
	s.Lines[0].Volume, s.Lines[0].SendB, s.Lines[0].PreB = 0, 1, true
	s.Buses.B = score.Bus{Return: 1, FX: []score.FX{score.NewFX(fx.KindEQ, fx.Bus)}}
	s.Buses.B.Lanes = []score.Lane{{Name: "eq.output", Points: []score.LanePoint{
		{At: score.Pos{Bar: 1}, Value: 0}, {At: score.Pos{Bar: 2}, Value: -24}}}}
	out = renderFrom(t, s, 480)
	for f := 1000; f < 95000; f += 101 {
		want := -24 * float64(f) / 96000
		if got := 20 * math.Log10(absAt(out, f)/absAt(dry, f)); math.Abs(got-want) > 0.05 {
			t.Fatalf("frame %d: bus B's EQ output is %.3f dB, the lane %.3f", f, got, want)
		}
	}
	// The editor's live bus command moves what the lanes do not: a return
	// lane holds the return while a pan set live stands.
	s = mk()
	s.Buses.A.Lanes = []score.Lane{{Name: "return", Points: []score.LanePoint{{At: score.Pos{Bar: 1}, Value: 0.5}}}}
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	buf := make([]float32, 480*2)
	p.Render(buf, 480, rate)
	p.SetBus(0, 1, -1, false)
	for i := 0; i < 20; i++ {
		p.Render(buf, 480, rate)
	}
	if r := math.Abs(float64(buf[100*2+1])); r > 1e-6 || buf[100*2] == 0 {
		t.Errorf("a live pan under a return lane: left %g right %g", buf[100*2], buf[100*2+1])
	}
	if a, d := absAt(buf, 100), absAt(dry, 48000); math.Abs(a/d-0.5) > 1e-3 {
		t.Errorf("the return lane's 0.5 under a live return of 1: %g of dry", a/d)
	}
}

// Mix lanes cue with no allocation.
func TestMixLanesRenderWithoutAllocating(t *testing.T) {
	useTestInstruments(t)
	s := held(t)
	s.Lines[0].SendA, s.Lines[0].SendB = 0.5, 0.5
	s.Buses.A = score.Bus{Return: 1, FX: []score.FX{score.NewFX(fx.KindReverb, fx.Bus)}}
	s.Buses.B = score.Bus{Return: 1, FX: []score.FX{score.NewFX(fx.KindDelay, fx.Bus)}}
	s.Master = []score.FX{score.NewFX(fx.KindLimiter, fx.Insert)}
	pts := func(a, b float32) []score.LanePoint {
		return []score.LanePoint{{At: score.Pos{Bar: 1}, Value: a, Shape: score.LaneCurve, Curve: -0.5}, {At: score.Pos{Bar: 2}, Value: b}}
	}
	s.Buses.A.Lanes = []score.Lane{{Name: "return", Points: pts(1, 0)}, {Name: "reverb.size", Points: pts(0.2, 0.9)}}
	s.Buses.B.Lanes = []score.Lane{{Name: "pan", Points: pts(-1, 1)}, {Name: "delay.feedback", Points: pts(0, 0.8)}}
	s.MasterLanes = []score.Lane{{Name: "volume", Points: pts(1, 0.3)}, {Name: "limiter.ceiling", Points: pts(-1, -12)}}
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	buf := make([]float32, 480*2)
	p.Render(buf, 480, rate)
	if allocs := testing.AllocsPerRun(100, func() { p.Render(buf, 480, rate) }); allocs != 0 {
		t.Errorf("%v allocations per block with the mix's lanes moving", allocs)
	}
}
