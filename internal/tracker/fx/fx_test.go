package fx

import (
	"math"
	"math/rand"
	"testing"
)

const testRate = 48000

func mk(t *testing.T, k Kind, place Placement, set map[string]float32) Effect {
	t.Helper()
	e := New(k, testRate, place)
	if e == nil {
		t.Fatalf("New(%v) = nil", k)
	}
	for name, v := range set {
		i := k.Spec().Param(name)
		if i < 0 {
			t.Fatalf("%v has no %s", k, name)
		}
		e.SetParam(i, v, 0)
	}
	return e
}

// run processes a copy of in through e in 480-frame blocks.
func run(e Effect, in []float32) []float32 {
	out := append([]float32(nil), in...)
	for i := 0; i < len(out); i += 960 {
		e.Process(out[i:min(i+960, len(out))])
	}
	return out
}

func impulse(frames int) []float32 {
	b := make([]float32, frames*2)
	b[0], b[1] = 1, 1
	return b
}

func sine(hz, amp float64, frames int) []float32 {
	b := make([]float32, frames*2)
	for f := 0; f < frames; f++ {
		v := float32(amp * math.Sin(2*math.Pi*hz*float64(f)/testRate))
		b[f*2], b[f*2+1] = v, v
	}
	return b
}

func constant(v float32, frames int) []float32 {
	b := make([]float32, frames*2)
	for i := range b {
		b[i] = v
	}
	return b
}

// peakFrom is the largest |sample| from frame from on (left channel).
func peakFrom(b []float32, from int) float64 {
	p := 0.0
	for f := from; f*2 < len(b); f++ {
		p = math.Max(p, math.Abs(float64(b[f*2])))
	}
	return p
}

func rmsRange(b []float32, from, to int) float64 {
	s, n := 0.0, 0
	for f := from; f < to && f*2+1 < len(b); f++ {
		s += float64(b[f*2])*float64(b[f*2]) + float64(b[f*2+1])*float64(b[f*2+1])
		n += 2
	}
	if n == 0 {
		return 0
	}
	return math.Sqrt(s / float64(n))
}

func TestSpecsAreConsistent(t *testing.T) {
	for k := Kind(0); k < numKinds; k++ {
		sp := k.Spec()
		if sp.Kind != k || sp.Name == "" || sp.Label == "" || sp.Desc == "" {
			t.Errorf("kind %d: spec %+v", k, sp)
		}
		if got, ok := Lookup(sp.Name); !ok || got != k {
			t.Errorf("Lookup(%s) = %v, %v", sp.Name, got, ok)
		}
		if len(sp.Params) > MaxParams {
			t.Errorf("%s: %d params", sp.Name, len(sp.Params))
		}
		seen := map[string]bool{}
		for i, p := range sp.Params {
			if seen[p.Name] {
				t.Errorf("%s: %s twice", sp.Name, p.Name)
			}
			seen[p.Name] = true
			if p.Min > p.Max || p.Default < p.Min || p.Default > p.Max {
				t.Errorf("%s.%s: default %g outside %g..%g", sp.Name, p.Name, p.Default, p.Min, p.Max)
			}
			if p.Log && p.Min <= 0 {
				t.Errorf("%s.%s: a log parameter needs a positive range", sp.Name, p.Name)
			}
			if sp.Param(p.Name) != i {
				t.Errorf("%s: Param(%s) = %d", sp.Name, p.Name, sp.Param(p.Name))
			}
			if p.Format(p.Default) == "" {
				t.Errorf("%s.%s: no text for the default", sp.Name, p.Name)
			}
		}
		e := New(k, testRate, Insert)
		for i := range sp.Params {
			if e.Param(i) != sp.Params[i].Default {
				t.Errorf("%s.%s starts at %g, want %g", sp.Name, sp.Params[i].Name, e.Param(i), sp.Params[i].Default)
			}
		}
	}
	if _, ok := Lookup("flanger"); ok {
		t.Error("Lookup found a kind that does not exist")
	}
	if New(KindReverb, testRate, Bus).Param(KindReverb.Spec().Param("mix")) != 1 {
		t.Error("a reverb on a bus is not all wet by default")
	}
}

func TestEveryEffectIsAllocationFree(t *testing.T) {
	buf := sine(220, 0.5, 480)
	c := NewChain(int(numKinds))
	for k := Kind(0); k < numKinds; k++ {
		e := New(k, testRate, Insert)
		allocs := testing.AllocsPerRun(20, func() {
			e.SetParam(0, e.Param(0), 64)
			e.SetTempo(100)
			e.Process(buf)
		})
		if allocs != 0 {
			t.Errorf("%s: %v allocations per block", k, allocs)
		}
		c.Add(New(k, testRate, Insert), k%3 == 0)
	}
	if allocs := testing.AllocsPerRun(20, func() {
		c.SetBypass(1, true)
		c.SetBypass(0, false)
		c.SetParam(2, 0, 0.4, 100)
		c.Process(buf)
	}); allocs != 0 {
		t.Errorf("a chain of every effect: %v allocations per block", allocs)
	}
}

// subnormal reports whether v is a nonzero float32 below the normal range.
func subnormal(v float32) bool {
	a := math.Abs(float64(v))
	return a != 0 && a < 1.1754943508222875e-38
}

func TestNoDenormals(t *testing.T) {
	// Every effect at settings that keep memory longest, fed a burst and
	// then a long silence: nothing may come out subnormal as it dies away.
	sets := map[Kind]map[string]float32{
		KindEQ:         {"low": 12, "b1": -12, "b2": 9, "high": 6},
		KindChorus:     {"feedback": 0.9, "mix": 1},
		KindPhaser:     {"feedback": 0.95, "mix": 1},
		KindDrive:      {"tone": 0.2},
		KindCrusher:    {"bits": 12},
		KindDelay:      {"feedback": 0.95, "mix": 1, "tone": 0.3},
		KindReverb:     {"size": 1, "damping": 0, "mix": 1},
		KindCompressor: {"threshold": -30, "ratio": 8},
		KindLimiter:    {"ceiling": -6},
		KindFilter:     {"resonance": 0.95, "slope": 1, "cutoff": 400},
	}
	rng := rand.New(rand.NewSource(1))
	for k := Kind(0); k < numKinds; k++ {
		e := mk(t, k, Insert, sets[k])
		e.SetTempo(140)
		in := make([]float32, testRate*12*2)
		for i := 0; i < testRate/5*2; i++ {
			in[i] = float32(rng.Float64()*2-1) * 0.8
		}
		out := run(e, in)
		bad := 0
		for _, v := range out {
			if subnormal(v) {
				bad++
			}
		}
		if bad > 0 {
			t.Errorf("%s: %d subnormal samples in a 12 s decay", k, bad)
		}
	}
}

func TestEQ(t *testing.T) {
	// Flat: an exact wire.
	in := sine(440, 0.7, 4800)
	if out := run(New(KindEQ, testRate, Insert), in); !equal(out, in) {
		t.Error("a flat EQ changed the signal")
	}
	// A +6 dB low shelf doubles DC (the step settles there).
	out := run(mk(t, KindEQ, Insert, map[string]float32{"low": 6.0206}), constant(0.25, 48000))
	if v := float64(out[len(out)-2]); math.Abs(v-0.5) > 0.002 {
		t.Errorf("low shelf +6 dB: DC settles at %g, want 0.5", v)
	}
	// A high shelf moves Nyquist and leaves DC.
	nyq := make([]float32, 9600*2)
	for f := range 9600 {
		v := float32(0.25)
		if f%2 == 1 {
			v = -0.25
		}
		nyq[f*2], nyq[f*2+1] = v, v
	}
	hs := mk(t, KindEQ, Insert, map[string]float32{"high": -6.0206})
	if v := peakFrom(run(hs, nyq), 4800); math.Abs(v-0.125) > 0.002 {
		t.Errorf("high shelf -6 dB: Nyquist at %g, want 0.125", v)
	}
	if v := float64(run(hs, constant(0.25, 48000))[48000*2-2]); math.Abs(v-0.25) > 0.002 {
		t.Errorf("high shelf: DC moved to %g", v)
	}
	// A +12 dB bell at 1 kHz: 4x there, about unity two decades away.
	bell := map[string]float32{"b1": 12, "b1_freq": 1000, "b1_q": 2}
	if g := peakFrom(run(mk(t, KindEQ, Insert, bell), sine(1000, 0.1, 48000)), 24000) / 0.1; math.Abs(g-3.98) > 0.05 {
		t.Errorf("bell +12 dB at its centre: gain %g, want 3.98", g)
	}
	if g := peakFrom(run(mk(t, KindEQ, Insert, bell), sine(50, 0.1, 48000)), 24000) / 0.1; math.Abs(g-1) > 0.02 {
		t.Errorf("bell at 1 kHz moved 50 Hz by %g", g)
	}
	// Output gain.
	if g := peakFrom(run(mk(t, KindEQ, Insert, map[string]float32{"output": -6.0206}), sine(440, 0.5, 9600)), 4800); math.Abs(g-0.25) > 0.002 {
		t.Errorf("EQ output -6 dB: %g", g)
	}
}

func TestFilter(t *testing.T) {
	lp := map[string]float32{"cutoff": 1000, "resonance": 0}
	// Low-pass: DC passes (the step response settles at 1), 10 kHz is far
	// down (12 dB an octave), 24 dB further.
	if v := float64(run(mk(t, KindFilter, Insert, lp), constant(1, 9600))[9600*2-2]); math.Abs(v-1) > 1e-3 {
		t.Errorf("low-pass step settles at %g", v)
	}
	if g := peakFrom(run(mk(t, KindFilter, Insert, lp), sine(100, 1, 24000)), 12000); math.Abs(g-1) > 0.02 {
		t.Errorf("low-pass at 1 kHz passes 100 Hz at %g", g)
	}
	g12 := peakFrom(run(mk(t, KindFilter, Insert, lp), sine(10000, 1, 24000)), 12000)
	lp24 := map[string]float32{"cutoff": 1000, "resonance": 0, "slope": 1}
	g24 := peakFrom(run(mk(t, KindFilter, Insert, lp24), sine(10000, 1, 24000)), 12000)
	if g12 > 0.02 || g24 > g12*g12*1.5 {
		t.Errorf("low-pass at 1 kHz lets 10 kHz through at %g (12 dB) and %g (24 dB)", g12, g24)
	}
	// High-pass: the step dies away.
	hp := map[string]float32{"mode": 1, "cutoff": 500}
	if v := float64(run(mk(t, KindFilter, Insert, hp), constant(1, 24000))[24000*2-2]); math.Abs(v) > 1e-3 {
		t.Errorf("high-pass step settles at %g", v)
	}
	// Mix 0 is the dry signal exactly.
	in := sine(440, 0.6, 4800)
	if out := run(mk(t, KindFilter, Insert, map[string]float32{"mix": 0}), in); !equal(out, in) {
		t.Error("a filter at mix 0 changed the signal")
	}
}

func TestDriveKeepsUnityForQuietSignals(t *testing.T) {
	// The Tracker's trap: a little drive must not turn quiet things up.
	for _, amt := range []float32{0, 0.3, 1} {
		e := mk(t, KindDrive, Insert, map[string]float32{"drive": amt})
		if g := peakFrom(run(e, sine(440, 0.001, 4800)), 0) / 0.001; math.Abs(g-1) > 0.002 {
			t.Errorf("drive %g: small-signal gain %g, want 1", amt, g)
		}
		k := float64(DriveK(amt))
		if p := peakFrom(run(mk(t, KindDrive, Insert, map[string]float32{"drive": amt}), sine(440, 10, 4800)), 0); p > 1/k+1e-6 {
			t.Errorf("drive %g: a loud signal peaks at %g, past 1/k = %g", amt, p, 1/k)
		}
	}
	in := sine(440, 0.9, 4800)
	if out := run(mk(t, KindDrive, Insert, map[string]float32{"mix": 0}), in); !equal(out, in) {
		t.Error("drive at mix 0 changed the signal")
	}
	if g := peakFrom(run(mk(t, KindDrive, Insert, map[string]float32{"drive": 0, "output": -6.0206}), sine(440, 0.001, 4800)), 0) / 0.001; math.Abs(g-0.5) > 0.002 {
		t.Errorf("drive output -6 dB: gain %g", g)
	}
}

func TestCrusher(t *testing.T) {
	e := mk(t, KindCrusher, Insert, map[string]float32{"bits": 2, "downsample": 4})
	out := run(e, sine(220, 0.9, 4800))
	for i, v := range out {
		if q := v * 2; q != float32(math.Round(float64(q))) {
			t.Fatalf("sample %d = %g: 2 bits leave only halves", i, v)
		}
	}
	for f := 1; f < 4800; f++ {
		if out[f*2] != out[(f-1)*2] && f%4 != 0 {
			t.Fatalf("downsample 4 changed the held sample at frame %d", f)
		}
	}
}

func TestDelayIsTempoSyncedAndGlides(t *testing.T) {
	e := mk(t, KindDelay, Insert, map[string]float32{"beats": 0.5, "feedback": 0.5, "tone": 1, "ping_pong": 0, "mix": 1})
	e.SetTempo(120)
	out := run(e, impulse(48000))
	// Half a beat at 120 is 0.25 s: 12000 frames, then each repeat half.
	if out[12000*2] != 1 || out[12000*2+1] != 1 {
		t.Errorf("first echo at 12000 = %g, %g", out[12000*2], out[12000*2+1])
	}
	if math.Abs(float64(out[24000*2])-0.5) > 1e-6 {
		t.Errorf("second echo = %g, want 0.5", out[24000*2])
	}
	for f := 1; f < 48000; f++ {
		if f != 12000 && f != 24000 && f != 36000 && out[f*2] != 0 {
			t.Fatalf("an echo at frame %d (%g)", f, out[f*2])
		}
	}
	// A tempo change mid-tone bends the echo instead of jumping it.
	e = mk(t, KindDelay, Insert, map[string]float32{"beats": 0.25, "feedback": 0, "tone": 1, "ping_pong": 0, "mix": 1})
	e.SetTempo(120)
	in := sine(200, 0.5, 48000)
	buf := append([]float32(nil), in...)
	maxStep := 0.0
	for i := 0; i < len(buf); i += 960 {
		if i == 24000*2 {
			e.SetTempo(90)
		}
		e.Process(buf[i:min(i+960, len(buf))])
	}
	for f := 7000; f < 48000; f++ {
		maxStep = math.Max(maxStep, math.Abs(float64(buf[f*2]-buf[(f-1)*2])))
	}
	natural := 2 * math.Pi * 200 / testRate * 0.5
	if maxStep > natural*1.6 {
		t.Errorf("the tempo change made a step of %g (a 200 Hz sine steps at most %g)", maxStep, natural)
	}
	// After the glide the echo sits at the new length exactly.
	e = mk(t, KindDelay, Insert, map[string]float32{"beats": 0.25, "feedback": 0, "tone": 1, "ping_pong": 0, "mix": 1})
	e.SetTempo(120)
	run(e, make([]float32, 9600*2))
	e.SetTempo(60)
	run(e, make([]float32, 9600*2)) // past the glide
	out = run(e, impulse(20000))
	if out[12000*2] != 1 {
		t.Errorf("after 120 -> 60 the echo is not at 12000 frames")
	}
}

func TestReverbTail(t *testing.T) {
	tail := func(size float32) (early, late float64) {
		e := mk(t, KindReverb, Insert, map[string]float32{"size": size, "mix": 1})
		out := run(e, impulse(testRate*3))
		return rmsRange(out, 0, testRate/2), rmsRange(out, testRate*3/2, testRate*2)
	}
	e0, l0 := tail(0)
	e1, l1 := tail(1)
	if e0 == 0 || l0 >= e0 || l1 >= e1 {
		t.Errorf("tails do not decay: size 0 %g -> %g, size 1 %g -> %g", e0, l0, e1, l1)
	}
	if l1/e1 <= l0/e0*2 {
		t.Errorf("a big room does not ring longer: %g vs %g", l1/e1, l0/e0)
	}
	in := sine(330, 0.5, 4800)
	if out := run(mk(t, KindReverb, Insert, map[string]float32{"mix": 0}), in); !equal(out, in) {
		t.Error("reverb at mix 0 changed the signal")
	}
}

func TestCompressor(t *testing.T) {
	c := mk(t, KindCompressor, Insert, map[string]float32{"threshold": -20, "ratio": 4, "attack": 0.001, "release": 0.05})
	// A 0 dB step is turned down by (0 - -20)(1 - 1/4) = 15 dB.
	if v := float64(run(c, constant(1, 9600))[9600*2-2]); math.Abs(v-math.Pow(10, -15.0/20)) > 0.002 {
		t.Errorf("0 dB into -20 dB at 4:1 settles at %g, want %g", v, math.Pow(10, -15.0/20))
	}
	// Under the threshold (and its knee) nothing changes.
	c = mk(t, KindCompressor, Insert, map[string]float32{"threshold": -20, "ratio": 4})
	if v := float64(run(c, constant(0.01, 9600))[9600*2-2]); math.Abs(v-0.01) > 1e-6 {
		t.Errorf("-40 dB passes at %g", v)
	}
	in := sine(440, 1, 4800)
	if out := run(mk(t, KindCompressor, Insert, map[string]float32{"enabled": 0, "threshold": -40}), in); !equal(out, in) {
		t.Error("a compressor that is off changed the signal")
	}
}

func TestLimiterIsABrickWall(t *testing.T) {
	l := mk(t, KindLimiter, Insert, map[string]float32{"ceiling": -1})
	ceil := math.Pow(10, -1.0/20)
	rng := rand.New(rand.NewSource(3))
	in := make([]float32, testRate*2*2)
	for i := range in {
		amp := 0.2
		if (i/2000)%3 == 1 {
			amp = 4 // loud bursts
		}
		in[i] = float32((rng.Float64()*2 - 1) * amp)
	}
	for i := 4000; i < 4010; i++ {
		in[i] = 9 // a spike
	}
	out := run(l, in)
	if p := peakFrom(out, 0); p > ceil*(1+1e-5) {
		t.Errorf("limiter at -1 dB let %g through (ceiling %g)", p, ceil)
	}
	for i := range out {
		if math.Abs(float64(out[i])) > ceil*(1+1e-5) {
			t.Fatalf("sample %d = %g past the ceiling", i, out[i])
		}
	}
	// A quiet signal passes untouched, only delayed by the look-ahead.
	l = mk(t, KindLimiter, Insert, nil)
	look := l.(*limiter).Latency()
	q := sine(440, 0.3, 4800)
	got := run(l, q)
	for f := look; f < 4800; f++ {
		if got[f*2] != q[(f-look)*2] {
			t.Fatalf("frame %d: %g, want the input %d frames back (%g)", f, got[f*2], look, q[(f-look)*2])
		}
	}
}

func TestChorusAndPhaser(t *testing.T) {
	in := sine(440, 0.5, 4800)
	if out := run(mk(t, KindChorus, Insert, map[string]float32{"mix": 0}), in); !equal(out, in) {
		t.Error("chorus at mix 0 changed the signal")
	}
	if out := run(mk(t, KindPhaser, Insert, map[string]float32{"mix": 0}), in); !equal(out, in) {
		t.Error("phaser at mix 0 changed the signal")
	}
	// Chorus with no depth is its delay: 12 ms is 576 frames.
	out := run(mk(t, KindChorus, Insert, map[string]float32{"depth": 0, "delay": 12, "mix": 1}), impulse(2000))
	if math.Abs(float64(out[576*2])-1) > 1e-6 || out[575*2] != 0 {
		t.Errorf("chorus impulse: %g at 576, %g at 575", out[576*2], out[575*2])
	}
	// With depth it moves: the echo spreads over more than one frame.
	out = run(mk(t, KindChorus, Insert, map[string]float32{"depth": 5, "rate": 2, "mix": 1}), sine(440, 0.5, 24000))
	if rmsRange(out, 4800, 24000) < 0.2 {
		t.Errorf("a modulated chorus is too quiet: %g", rmsRange(out, 4800, 24000))
	}
	// A phaser's allpasses keep a tone's level; mixed half and half with
	// the dry, a tone at the centre (each of six stages turning it 90°,
	// 540° in all) cancels.
	wet := run(mk(t, KindPhaser, Insert, map[string]float32{"depth": 0, "feedback": 0, "mix": 1, "freq": 800}), sine(300, 0.5, 24000))
	if g := peakFrom(wet, 12000); math.Abs(g-0.5) > 0.01 {
		t.Errorf("phaser wet level %g, want 0.5", g)
	}
	notch := run(mk(t, KindPhaser, Insert, map[string]float32{"depth": 0, "feedback": 0, "mix": 0.5, "freq": 800, "stages": 6}), sine(800, 0.5, 24000))
	if g := peakFrom(notch, 12000); g > 0.01 {
		t.Errorf("phaser notch at its centre lets %g through", g)
	}
}

func TestParametersGlide(t *testing.T) {
	// A gain turned down over 480 frames ramps there: no step.
	e := mk(t, KindDrive, Insert, map[string]float32{"drive": 0})
	in := constant(0.001, 2400)
	e.Process(in[:960])
	e.SetParam(KindDrive.Spec().Param("output"), -20, 480)
	e.Process(in[960:])
	maxStep := 0.0
	for f := 1; f < 2400; f++ {
		maxStep = math.Max(maxStep, math.Abs(float64(in[f*2]-in[(f-1)*2])))
	}
	// Decibels glide in a straight line, so the gain falls fastest at the
	// start: 20 dB over 480 frames is at most ln(10)·0.001/480 a frame.
	total := 0.001 * (1 - 0.1)
	if maxStep > math.Ln10*0.001/480*1.1 {
		t.Errorf("a glide over 480 frames stepped %g (the whole move is %g)", maxStep, total)
	}
	if v := float64(in[2399*2]); math.Abs(v-0.0001) > 1e-7 {
		t.Errorf("the glide ended at %g, want 0.0001", v)
	}
	if e.Param(KindDrive.Spec().Param("output")) != -20 {
		t.Error("Param is not the glide's target")
	}
	// A log parameter glides on a log scale and lands exactly.
	f := New(KindFilter, testRate, Insert).(*filter)
	f.SetParam(ftCutoff, 100, 0)
	f.SetParam(ftCutoff, 10000, 640)
	f.advance(320)
	if v := f.val(ftCutoff); math.Abs(float64(v)-1000) > 1 {
		t.Errorf("halfway from 100 Hz to 10 kHz is %g, want 1000 (log)", v)
	}
	f.advance(320)
	if f.val(ftCutoff) != 10000 {
		t.Errorf("the glide landed at %g", f.val(ftCutoff))
	}
	// A switch steps.
	f.SetParam(ftMode, 1, 1000)
	if f.val(ftMode) != 1 {
		t.Error("a switch glided")
	}
	// Out of range is clamped.
	f.SetParam(ftCutoff, 1e9, 0)
	if f.Param(ftCutoff) != 20000 {
		t.Errorf("cutoff 1e9 clamped to %g", f.Param(ftCutoff))
	}
}

func TestChainBypassAndTake(t *testing.T) {
	gain := func(db float32) Effect {
		e := New(KindEQ, testRate, Insert)
		e.SetParam(eqOutput, db, 0)
		return e
	}
	c := NewChain(2)
	c.Add(gain(-6.0206), false)
	c.Add(gain(-6.0206), true)
	b := constant(1, 1000)
	c.Process(b)
	if math.Abs(float64(b[999*2])-0.5) > 1e-4 {
		t.Errorf("one active -6 dB slot gives %g", b[999*2])
	}
	// Bringing the second in fades: no step, then 0.25.
	c.SetBypass(1, false)
	b = constant(1, 1000)
	c.Process(b)
	for f := 1; f < 1000; f++ {
		if d := math.Abs(float64(b[f*2] - b[(f-1)*2])); d > 0.25/bypassFrames*1.5 {
			t.Fatalf("bypass off stepped %g at frame %d", d, f)
		}
	}
	if math.Abs(float64(b[999*2])-0.25) > 1e-4 {
		t.Errorf("both slots give %g", b[999*2])
	}
	// Take keeps a delay's memory: echoes from before the new settings
	// still come.
	mkDelay := func(fb float32) *Chain {
		ch := NewChain(1)
		d := New(KindDelay, testRate, Bus)
		d.SetParam(dlBeats, 0.25, 0)
		d.SetParam(dlFeedback, fb, 0)
		d.SetParam(dlTone, 1, 0)
		d.SetParam(dlPingPong, 0, 0)
		ch.Add(d, false)
		ch.SetTempo(120)
		return ch
	}
	live, next := mkDelay(0), mkDelay(0.5)
	if !live.SameKinds(next) || live.SameKinds(NewChain(0)) {
		t.Fatal("SameKinds")
	}
	imp := impulse(3000)
	live.Process(imp)
	live.Take(next, 0)
	rest := make([]float32, 6000*2)
	live.Process(rest)
	if rest[(6000-3000)*2] != 1 {
		t.Errorf("the echo from before Take did not come: %g", rest[(6000-3000)*2])
	}
	if live.Effect(0).Param(dlFeedback) != 0.5 {
		t.Error("Take did not take the parameters")
	}
	var nilChain *Chain
	nilChain.Process(rest) // a nil chain is a wire
	if nilChain.Len() != 0 {
		t.Error("nil chain length")
	}
}

func TestEchoKernelJumpsLikeTheTracker(t *testing.T) {
	// The Tracker's delay: SetLength moves the read point at once, on
	// whole samples.
	d := NewEcho(testRate)
	d.SetVoice(0, 1, false)
	d.SetLength(100)
	var got []float32
	for f := 0; f < 300; f++ {
		x := float32(0)
		if f == 0 {
			x = 1
		}
		if f == 150 {
			d.SetLength(50)
		}
		l, _ := d.Process(x, x)
		got = append(got, l)
	}
	if got[100] != 1 {
		t.Errorf("echo at 100: %g", got[100])
	}
	for f, v := range got {
		if v != 0 && f != 100 {
			t.Errorf("echo at %d", f)
		}
	}
}

func equal(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func BenchmarkChain(b *testing.B) {
	c := NewChain(int(numKinds))
	for k := Kind(0); k < numKinds; k++ {
		c.Add(New(k, testRate, Insert), false)
	}
	buf := sine(220, 0.5, 480)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Process(buf)
	}
}
