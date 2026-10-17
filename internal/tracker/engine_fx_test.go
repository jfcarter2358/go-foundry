package tracker

import (
	"math"
	"math/rand"
	"testing"

	"foundry/internal/tracker/fx"
)

// noiseInstrument plays a looping burst of noise: every effect has
// something to work on.
func noiseInstrument() *Instrument {
	rng := rand.New(rand.NewSource(9))
	d := make([]float32, testRate/2)
	for i := range d {
		d[i] = float32(rng.Float64()*2-1) * 0.4
	}
	in := NewInstrument("noise", "noise")
	in.SetLoaded(0, &Sample{Name: "noise", Data: d, Channels: 1, Rate: testRate, BaseNote: NoteC4, Volume: 1,
		Loop: LoopForward, LoopStart: 0, LoopEnd: len(d)})
	in.FadeOut = 0.05
	return in
}

func fxChain(place fx.Placement, kinds ...fx.Kind) *fx.Chain {
	c := fx.NewChain(len(kinds))
	for _, k := range kinds {
		c.Add(fx.New(k, testRate, place), false)
	}
	return c
}

func renderEngine(e *Engine, frames int) []float32 { return renderEngineBy(e, frames, 480) }

func renderEngineBy(e *Engine, frames, block int) []float32 {
	out := make([]float32, frames*2)
	for i := 0; i < frames; i += block {
		n := min(block, frames-i)
		e.Render(out[i*2:(i+n)*2], n, testRate)
	}
	return out
}

// The effects path with the Tracker's own effects on it — a reverb on A,
// a delay on B, the compressor on the master — mixes bit for bit what the
// Tracker's fixed mix does, so a score is the same music through either.
// (Renders and cues on control-block boundaries: the Tracker's mix lets a
// strip that falls idle on a shorter sub-block replay a few stale frames
// in the next longer one, which the effects path does not.)
func TestEffectsPathMatchesTheTrackersMix(t *testing.T) {
	rev := ReverbSpec{Size: 0.7, Damping: 0.3, Width: 0.8, Return: 0.6}
	dly := DelaySpec{Beats: 0.5, Feedback: 0.4, Tone: 0.7, PingPong: true, Return: 0.5}
	comp := CompressorSpec{Enabled: true, Threshold: -18, Ratio: 4, Attack: 0.003, Release: 0.1, Makeup: 2}
	strips := []StripControls{
		{Volume: 0.8, Pan: -0.4, SendA: 0.5, SendB: 0.2, CutoffHz: 3000, Resonance: 0.3},
		{Volume: 1.1, Pan: 0.6, SendA: 0.1, Drive: 0.4},
		{Volume: 0.5, SendB: 0.7, Crush: 6},
	}
	build := func(effects bool) *Engine {
		e := NewEngine(len(strips))
		e.SetBuses(rev, dly, comp, 0.9)
		e.SetRate(testRate)
		e.SetTempo(132)
		if effects {
			a := fxChain(fx.Bus, fx.KindReverb)
			a.SetParam(0, 0, rev.Size, 0)
			a.SetParam(0, 1, rev.Damping, 0)
			a.SetParam(0, 2, rev.Width, 0)
			b := fxChain(fx.Bus, fx.KindDelay)
			for i, v := range []float32{dly.Beats, dly.Feedback, dly.Tone, 1} {
				b.SetParam(0, i, v, 0)
			}
			m := fxChain(fx.Insert, fx.KindCompressor)
			for i, v := range []float32{comp.Threshold, comp.Ratio, comp.Attack, comp.Release, comp.Makeup, 1} {
				m.SetParam(0, i, v, 0)
			}
			e.SetBusChains(a, b, m)
			e.SetBus(0, rev.Return, 0, false)
			e.SetBus(1, dly.Return, 0, false)
		}
		for i, c := range strips {
			e.SetStrip(i, c)
		}
		in := noiseInstrument()
		for k := 0; k < 12; k++ {
			id := e.NewNoteID()
			e.Cue(Cue{At: k * 3072, Kind: CueNoteOn, Note: id, Strip: k % 3, Inst: in, Pitch: float32(int(NoteC4) + k), Opts: NoteOptions{Volume: 0.7}})
			e.Cue(Cue{At: k*3072 + 1984, Kind: CueNoteOff, Note: id})
		}
		return e
	}
	legacy := renderEngineBy(build(false), testRate*2, 512)
	path := renderEngineBy(build(true), testRate*2, 512)
	for i := range legacy {
		if legacy[i] != path[i] {
			t.Fatalf("sample %d: effects path %g, the Tracker's mix %g", i, path[i], legacy[i])
		}
	}
	if Peak(legacy) == 0 {
		t.Fatal("silent")
	}
}

func onePathEngine(strips int) *Engine {
	e := NewEngine(strips)
	e.SetBuses(ReverbSpec{}, DelaySpec{}, CompressorSpec{}, 1)
	e.SetRate(testRate)
	e.SetBusChains(nil, nil, nil)
	e.SetBus(0, 0, 0, false)
	e.SetBus(1, 0, 0, false)
	return e
}

// A strip's level moved by a cue changes on the cue's frame and reaches
// its new value within one control block, in a straight line.
func TestEffectsPathRampsStripControls(t *testing.T) {
	in := NewInstrument("dc", "dc")
	in.SetLoaded(0, dcSample("dc", 0.5, 2))
	e := onePathEngine(1)
	e.SetStrip(0, StripControls{Volume: 1})
	e.Cue(Cue{At: 0, Kind: CueNoteOn, Strip: 0, Inst: in, Pitch: float32(NoteC4)})
	const at = 1000
	e.Cue(Cue{At: at, Kind: CueStrip, Strip: 0, Controls: StripControls{Volume: 0}})
	out := renderEngine(e, 2400)
	level := out[(at-1)*2]
	if level == 0 {
		t.Fatal("silent before the cue")
	}
	if out[(at-10)*2] != level {
		t.Error("the level moved before its cue")
	}
	if out[(at+controlBlock)*2] != 0 {
		t.Errorf("still %g a control block after the cue", out[(at+controlBlock)*2])
	}
	for f := at; f < at+controlBlock; f++ {
		if d := out[(f-1)*2] - out[f*2]; d > level/controlBlock*1.01 || d < 0 {
			t.Fatalf("frame %d: the level stepped %g (a straight line steps %g)", f, d, level/controlBlock)
		}
	}
}

func TestEffectsPathInsertsSendsAndBuses(t *testing.T) {
	in := NewInstrument("dc", "dc")
	in.SetLoaded(0, dcSample("dc", 0.5, 2))
	in.FadeOut = 0.001
	// A delay insert rings on after its note: the note stops at 0.1 s, the
	// echo comes a quarter beat later at 120 (0.125 s) and after it ends.
	e := onePathEngine(1)
	e.SetTempo(120)
	e.SetStrip(0, StripControls{Volume: 1})
	d := fxChain(fx.Insert, fx.KindDelay)
	for i, v := range []float32{0.25, 0, 1, 0, 1} {
		d.SetParam(0, i, v, 0)
	}
	e.SetInserts(0, d)
	id := e.NewNoteID()
	e.Cue(Cue{At: 0, Kind: CueNoteOn, Note: id, Strip: 0, Inst: in, Pitch: float32(NoteC4)})
	e.Cue(Cue{At: 4800, Kind: CueNoteOff, Note: id})
	out := renderEngine(e, 24000)
	if out[100*2] != 0 {
		t.Errorf("an all-wet delay let the dry note through: %g", out[100*2])
	}
	if out[(6000+3000)*2] == 0 {
		t.Error("no echo after the note ended: the insert stopped with it")
	}
	if out[(6000+4800+600)*2] != 0 {
		t.Errorf("the echo did not end: %g", out[(6000+4800+600)*2])
	}

	// Pre-fader send: the fader at 0 silences the dry line, the bus (empty,
	// returning its input at 1) still hears it; a post-fader send would not.
	for _, pre := range []bool{true, false} {
		e := onePathEngine(1)
		e.SetBus(0, 1, 0, false)
		e.SetStrip(0, StripControls{Volume: 0, SendA: 1, PreA: pre})
		e.Cue(Cue{At: 0, Kind: CueNoteOn, Strip: 0, Inst: in, Pitch: float32(NoteC4)})
		out := renderEngine(e, 960)
		got := out[500*2]
		if pre && math.Abs(float64(got)-float64(out[400*2])) > 1e-6 || pre && got == 0 {
			t.Errorf("pre-fader send: %g", got)
		}
		if !pre && got != 0 {
			t.Errorf("post-fader send with the fader down: %g", got)
		}
	}

	// A bus's return, pan and mute.
	e = onePathEngine(1)
	e.SetStrip(0, StripControls{Volume: 0, SendB: 1, PreB: true})
	e.SetBus(1, 0.5, -1, false)
	e.Cue(Cue{At: 0, Kind: CueNoteOn, Strip: 0, Inst: in, Pitch: float32(NoteC4)})
	out = renderEngine(e, 960)
	l, r := out[800*2], out[800*2+1]
	if r != 0 || l == 0 {
		t.Errorf("bus B hard left: %g, %g", l, r)
	}
	if e.BusLevel(1) == 0 {
		t.Error("bus meter did not move")
	}
	e.SetBus(1, 0.5, -1, true)
	out = renderEngine(e, 960)
	if out[900*2] != 0 {
		t.Errorf("a muted bus returned %g", out[900*2])
	}
}

// A CueFX moves an effect's parameter on its frame.
func TestEffectsPathCuesAnEffectParameter(t *testing.T) {
	in := NewInstrument("dc", "dc")
	in.SetLoaded(0, dcSample("dc", 0.5, 2))
	e := onePathEngine(1)
	e.SetStrip(0, StripControls{Volume: 1})
	eq := fxChain(fx.Insert, fx.KindEQ)
	e.SetInserts(0, eq)
	out := fx.KindEQ.Spec().Param("output")
	e.Cue(Cue{At: 0, Kind: CueNoteOn, Strip: 0, Inst: in, Pitch: float32(NoteC4)})
	e.Cue(Cue{At: 700, Kind: CueFX, Strip: 0, Slot: 0, Param: out, Value: -6.0206, Frames: 0})
	got := renderEngine(e, 1440)
	before, after := got[699*2], got[700*2]
	if math.Abs(float64(after/before)-0.5) > 1e-3 {
		t.Errorf("the output gain cued at 700: %g then %g", before, after)
	}
	if e.Chain(0) != eq || e.Chain(FXMaster) != nil {
		t.Error("Chain")
	}
}

func TestEffectsPathRendersWithoutAllocating(t *testing.T) {
	in := noiseInstrument()
	e := NewEngine(4)
	e.SetRate(testRate)
	e.SetBusChains(fxChain(fx.Bus, fx.KindReverb, fx.KindFilter), fxChain(fx.Bus, fx.KindDelay), fxChain(fx.Insert, fx.KindEQ, fx.KindCompressor, fx.KindLimiter))
	for i := 0; i < 4; i++ {
		e.SetInserts(i, fxChain(fx.Insert, fx.Kind(i), fx.KindDrive))
		e.SetStrip(i, StripControls{Volume: 0.7, SendA: 0.3, SendB: 0.2, PreA: i%2 == 0})
		e.NoteOn(i, in, float32(int(NoteC4)+i*3), NoteOptions{})
	}
	buf := make([]float32, 480*2)
	e.Render(buf, 480, testRate)
	k := 0
	if allocs := testing.AllocsPerRun(50, func() {
		k++
		e.Cue(Cue{At: 100, Kind: CueStrip, Strip: k % 4, Controls: StripControls{Volume: float32(k%3) * 0.3, SendA: 0.2}})
		e.Cue(Cue{At: 200, Kind: CueFX, Strip: k % 4, Slot: 1, Param: 0, Value: float32(k%5) * 0.2, Frames: 64})
		e.Render(buf, 480, testRate)
	}); allocs != 0 {
		t.Errorf("%v allocations per render on the effects path", allocs)
	}
}
