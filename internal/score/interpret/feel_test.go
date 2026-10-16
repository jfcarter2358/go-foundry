package interpret

import (
	"math"
	"testing"

	"foundry/internal/score"
)

const head44 = "version: 1\nbars: 2\ntempo: [{bar: 1, bpm: 120}]\nmeter: [{bar: 1, sig: 4/4}]"

func TestSwingWarpsEachPair(t *testing.T) {
	s := one(t, head44+"\nplayback: {swing: {amount: 0.6667, unit: 8}}", "| eC4 eD4 eE4 eF4 qG4 qA4 | sC4 sD4 sE4 sF4 hG4 qA4 |")
	p := perform(t, s)
	// Eighth swing: each beat's first eighth takes two thirds of it (640
	// of 960 ticks); quarters and 16ths inside the halves move in
	// proportion.
	eqTicks(t, "8th swing", ticks(p), []int64{0, 640, 960, 1600, 1920, 2880,
		3840, 3840 + 320, 3840 + 640, 3840 + 800, 3840 + 960, 3840 + 2880})
	if n := p.Notes[0]; n.Ticks != 640 {
		t.Errorf("the long eighth lasts %d ticks", n.Ticks)
	}
	if n := p.Notes[1]; n.Ticks != 320 {
		t.Errorf("the short eighth lasts %d ticks", n.Ticks)
	}
	if n := p.Notes[4]; n.Ticks != 960 {
		t.Errorf("a quarter on the beat lasts %d ticks", n.Ticks)
	}
	// Sixteenth swing leaves eighths alone.
	s = one(t, head44+"\nplayback: {swing: {amount: 0.75, unit: 16}}", "| eC4 eD4 sE4 sF4 sG4 sA4 hB4 | wC5 |")
	eqTicks(t, "16th swing", ticks(perform(t, s)), []int64{0, 480, 960, 960 + 360, 1440, 1440 + 360, 1920, 3840})
}

func TestSwingKeepsBarsAndFollowsLines(t *testing.T) {
	// In 5/8 the last eighth has no partner: it stays where it is, and the
	// next bar starts on time.
	s := one(t, "version: 1\nbars: 2\ntempo: [{bar: 1, bpm: 120}]\nmeter: [{bar: 1, sig: 5/8}]\nplayback: {swing: {amount: 0.75}}",
		"| eC4 eD4 eE4 eF4 eG4 | eC4 eD4 eE4 eF4 eG4 |")
	eqTicks(t, "5/8", ticks(perform(t, s)), []int64{0, 720, 960, 1680, 1920, 2400, 3120, 3360, 4080, 4320})
	// A pickup counts its pairs from where its bar would have begun: an
	// eighth pickup in 4/4 is the second of a pair, played late.
	s = one(t, "version: 1\nbars: 2\npickup: 1/8\ntempo: [{bar: 1, bpm: 120}]\nmeter: [{bar: 1, sig: 4/4}]\nplayback: {swing: {amount: 0.75}}",
		"| eG3 | eC4 eD4 hE4 qF4 |")
	eqTicks(t, "pickup", ticks(perform(t, s)), []int64{240, 480, 480 + 720, 480 + 960, 480 + 2880})
	// A line of its own plays straight under a swung score.
	s = one(t, head44+"\nplayback: {swing: {amount: 0.75}}", "| eC4 eD4 eE4 eF4 hG4 | wC5 |")
	s.Lines[0].Swing = &score.Swing{Amount: 0.5}
	eqTicks(t, "straight line", ticks(perform(t, s)), []int64{0, 480, 960, 1440, 1920, 3840})
	s.Lines[0].Swing = nil
	eqTicks(t, "swung line", ticks(perform(t, s)), []int64{0, 720, 960, 1680, 1920, 3840})
}

func TestHumanizeIsBoundedAndRepeatable(t *testing.T) {
	src := "| qC4+E4+G4 qD4 qE4 qF4 | qG4 qA4 qB4 qC5 |"
	s := one(t, head44+"\nplayback: {humanize: {timing: 15, velocity: 0.1, seed: 4}}", src)
	plain := perform(t, one(t, head44, src))
	a, b := perform(t, s), perform(t, s)
	moved, velMoved := 0, 0
	for i := range a.Notes {
		x, y := a.Notes[i], b.Notes[i]
		if x.Start != y.Start || x.End != y.End || x.Velocity != y.Velocity {
			t.Fatalf("note %d plays differently the second time", i)
		}
	}
	byTick := map[[2]int64]Note{}
	for _, n := range plain.Notes {
		byTick[[2]int64{n.Tick, int64(n.MIDI)}] = n
	}
	for _, n := range a.Notes {
		p := byTick[[2]int64{n.Tick, int64(n.MIDI)}]
		d := n.Start - p.Start
		if math.Abs(d) > 0.015+1e-9 {
			t.Errorf("note at %d moved %g s, past 15 ms", n.Tick, d)
		}
		if math.Abs((n.End-n.Start)-(p.End-p.Start)) > 1e-9 {
			t.Errorf("note at %d changed length", n.Tick)
		}
		if d != 0 {
			moved++
		}
		if r := n.Velocity / p.Velocity; r > 1.1001 || r < 0.8999 {
			t.Errorf("velocity moved by %g", r)
		} else if r != 1 {
			velMoved++
		}
	}
	if moved < len(a.Notes)/2 || velMoved < len(a.Notes)/2 {
		t.Errorf("only %d notes moved in time and %d in velocity of %d", moved, velMoved, len(a.Notes))
	}
	// A chord's notes move together.
	var chord []float64
	for _, n := range a.Notes {
		if n.Tick == 0 {
			chord = append(chord, n.Start)
		}
	}
	if len(chord) != 3 || chord[0] != chord[1] || chord[1] != chord[2] {
		t.Errorf("the chord's notes start at %v", chord)
	}
	// Another seed plays another way; an edit in bar 2 leaves bar 1's
	// nudges alone.
	s2 := s.Clone()
	s2.Playback.Humanize.Seed = 5
	if perform(t, s2).Notes[3].Start == a.Notes[3].Start {
		t.Error("the seed changed nothing")
	}
	s3 := one(t, head44+"\nplayback: {humanize: {timing: 15, velocity: 0.1, seed: 4}}", "| qC4+E4+G4 qD4 qE4 qF4 | hG4 hC5 |")
	c := perform(t, s3)
	for i := 0; i < 6; i++ {
		if c.Notes[i].Start != a.Notes[i].Start {
			t.Errorf("an edit in bar 2 moved note %d of bar 1", i)
		}
	}
	// A line's own humanise, with the score's seed.
	s4 := one(t, head44+"\nplayback: {humanize: {seed: 4}}", src)
	s4.Lines[0].Humanize = &score.Humanize{Timing: 15, Velocity: 0.1}
	if d := perform(t, s4); d.Notes[3].Start != a.Notes[3].Start {
		t.Error("a line's humanise with the score's seed plays differently from the score's")
	}
}

func TestLaneShapes(t *testing.T) {
	s := one(t, head44, "| wC4 | wC4 |")
	s.Lines[0].Lanes = []score.Lane{
		{Name: "volume", Points: []score.LanePoint{{At: score.Pos{Bar: 1}, Value: 1, Shape: score.LaneStep}, {At: score.Pos{Bar: 1, Offset: score.F(1, 2)}, Value: 0.25}}},
		{Name: "pan", Points: []score.LanePoint{{At: score.Pos{Bar: 1}, Value: -1, Shape: score.LaneCurve, Curve: 1}, {At: score.Pos{Bar: 2}, Value: 1}}},
	}
	p := perform(t, s)
	var vol, pan *Curve
	for i := range p.Curves {
		switch p.Curves[i].Target {
		case "volume":
			vol = &p.Curves[i]
		case "pan":
			pan = &p.Curves[i]
		}
	}
	// The step holds 1 to beat 3, then 0.25; just before, Before is still 1.
	if v := vol.At(1919); v != 1 {
		t.Errorf("step before its point: %g", v)
	}
	if v := vol.At(1920); v != 0.25 {
		t.Errorf("step at its point: %g", v)
	}
	if v := vol.Before(1920); v != 1 {
		t.Errorf("Before at the step: %g", v)
	}
	if !vol.Varies(1800, 2000) || vol.Varies(0, 1900) {
		t.Error("Varies around the step")
	}
	// A curve of 1 starts slowly: halfway along it is well below the line.
	if v := pan.At(1920); v > -0.7 {
		t.Errorf("curved pan halfway = %g, want well under the straight line's 0", v)
	}
	if v := pan.At(3840); math.Abs(float64(v)-1) > 1e-6 {
		t.Errorf("curved pan lands at %g", v)
	}
	prev := float32(-2)
	for tk := 0.0; tk <= 3840; tk += 60 {
		v := pan.At(tk)
		if v < prev {
			t.Fatalf("the curve turns back at %g", tk)
		}
		prev = v
	}
}

func TestHumanizeMovesASlideWithItsNote(t *testing.T) {
	src := "| hC4(gliss) hG4 | hG4(gliss) hC4 |"
	plain := one(t, "bars: 2", src)
	plain.Lines[0].Gliss = "slide"
	hum := plain.Clone()
	hum.Playback.Humanize = score.Humanize{Timing: 40, Seed: 3}
	a, b := perform(t, plain), perform(t, hum)
	if len(a.Notes) != len(b.Notes) {
		t.Fatalf("%d notes straight, %d humanised", len(a.Notes), len(b.Notes))
	}
	bent := 0
	for i := range a.Notes {
		x, y := a.Notes[i], b.Notes[i]
		if len(x.Bend) == 0 {
			continue
		}
		bent++
		d := y.Start - x.Start
		if d == 0 {
			t.Errorf("note %d was not nudged", i)
		}
		for k := range x.Bend {
			got := b.Tempo.Seconds(y.Bend[k].Tick) - a.Tempo.Seconds(x.Bend[k].Tick)
			if math.Abs(got-d) > 1e-9 {
				t.Errorf("note %d's bend point %d moved %g s, its note %g s", i, k, got, d)
			}
		}
	}
	if bent == 0 {
		t.Fatal("no slide to check")
	}
}
