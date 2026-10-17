package interpret

import (
	"math"
	"strings"
	"testing"

	"foundry/internal/score"
)

// one builds a score with one line and one staff holding the voices.
func one(t *testing.T, header string, voices ...string) *score.Score {
	t.Helper()
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\nlines:\n  - name: A\n    instrument: keys/grand_piano\n    staves:\n      - clef: treble\n        voices:\n")
	for _, v := range voices {
		b.WriteString("          - \"" + v + "\"\n")
	}
	s, err := score.Parse([]byte(b.String()))
	if err != nil {
		t.Fatalf("%v\n%s", err, b.String())
	}
	return s
}

func perform(t *testing.T, s *score.Score) *Performance {
	t.Helper()
	p, err := Interpret(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func ticks(p *Performance) []int64 {
	var out []int64
	for _, n := range p.Notes {
		out = append(out, n.Tick)
	}
	return out
}

func eqTicks(t *testing.T, what string, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: ticks %v, want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: note %d at tick %d, want %d (all: %v)", what, i, got[i], want[i], got)
		}
	}
}

func TestTempoMapSteadyAndChanges(t *testing.T) {
	m := NewTempoMap([]TempoPoint{{0, 120, false}, {1920, 60, false}})
	// Two beats at 120 is a second; after that each beat is a second.
	for tick, want := range map[float64]float64{0: 0, 960: 0.5, 1920: 1, 2880: 2, 4800: 4} {
		if got := m.Seconds(tick); !near(got, want) {
			t.Errorf("Seconds(%v) = %v, want %v", tick, got, want)
		}
		if got := m.Tick(want); !near(got, tick) {
			t.Errorf("Tick(%v) = %v, want %v", want, got, tick)
		}
	}
	if NewTempoMap(nil).Seconds(960) != 0.5 {
		t.Error("an empty map is 120")
	}
}

func TestTempoMapRampIntegrates(t *testing.T) {
	// 120 down to 60 over four beats, then steady.
	m := NewTempoMap([]TempoPoint{{0, 120, true}, {3840, 60, false}})
	var want float64
	const steps = 200000
	for i := 0; i < steps; i++ {
		x := (float64(i) + 0.5) / steps * 3840
		q := 120 - 60*x/3840
		want += 3840.0 / steps * 60 / (q * 960)
	}
	if got := m.Seconds(3840); math.Abs(got-want) > 1e-6 {
		t.Errorf("the ramp lasts %v s, want %v", got, want)
	}
	for _, tick := range []float64{1, 500, 1920, 3839, 3840, 5000} {
		if back := m.Tick(m.Seconds(tick)); math.Abs(back-tick) > 1e-6 {
			t.Errorf("Tick(Seconds(%v)) = %v", tick, back)
		}
	}
	if q := m.QPM(1920); !near(q, 90) {
		t.Errorf("half way down the ramp is %v", q)
	}
}

func TestTupletsAreExact(t *testing.T) {
	s := one(t, "bars: 1\ntempo: [{bar: 1, bpm: 60}]", "| 3{eC4 eD4 eE4} 5{sC4 sD4 sE4 sF4 sG4} 3{qC4 3{eC4 eD4 eE4} qG4} |")
	p := perform(t, s)
	// Triplet eighths 320 ticks apart, quintuplet 16ths 192; a triplet of
	// eighths inside a half-note triplet 213 1/3, which rounds.
	eqTicks(t, "tuplets", ticks(p), []int64{0, 320, 640, 960, 1152, 1344, 1536, 1728, 1920, 2560, 2773, 2987, 3200})
	// Seconds come from the exact position, not the rounded tick.
	if n := p.Notes[10]; !near(n.Start, 2+8.0/9) {
		t.Errorf("nested triplet starts at %v s, want %v", n.Start, 2+8.0/9)
	}
	if n := p.Notes[12]; !near(n.End, 4) || n.Ticks != 640 {
		t.Errorf("last note ends at %v s, %d ticks long", n.End, n.Ticks)
	}
	if !near(p.Seconds, 4) || p.Length != 3840 {
		t.Errorf("one bar of 4/4 at 60 is %v s, %d ticks", p.Seconds, p.Length)
	}
}

func TestTiesMerge(t *testing.T) {
	s := one(t, "bars: 3", "| hC4~+E4 hC4~+G4 | wC4~ | qC4 qr hD4~ |")
	p := perform(t, s)
	// C4 is one note of two bars and a quarter; E4 and G4 stand alone; the
	// tie from D4 at the end of the voice just ends.
	if len(p.Notes) != 4 {
		t.Fatalf("%d notes: %+v", len(p.Notes), p.Notes)
	}
	c := p.Notes[0]
	if c.MIDI != 60 || c.Tick != 0 || c.Ticks != 2*3840+960 {
		t.Errorf("tied C4: %+v", c)
	}
	if !near(c.End-c.Start, 4.5) {
		t.Errorf("tied C4 lasts %v s at 120", c.End-c.Start)
	}
	if d := p.Notes[3]; d.MIDI != 62 || d.Ticks != 1920 {
		t.Errorf("D4: %+v", d)
	}
}

func TestTempoChangesMoveSeconds(t *testing.T) {
	s := one(t, "bars: 3\ntempo: [{bar: 1, bpm: 120}, {bar: 2, bpm: 60}, {bar: 3, beat: \"3\", bpm: 90, unit: h}]",
		"| wC4 | qD4 qD4 hE4 | hF4 hG4 |")
	p := perform(t, s)
	// Bar 1 at 120: 2 s. Bar 2 at 60: 4 s. Bar 3: two beats at 60, then
	// 90 halves a minute = 180 quarters: the last half takes 2/3 s.
	want := []float64{0, 2, 3, 4, 6, 8}
	for i, n := range p.Notes {
		if !near(n.Start, want[i]) {
			t.Errorf("note %d starts at %v, want %v", i, n.Start, want[i])
		}
	}
	if !near(p.Seconds, 8+2.0/3) {
		t.Errorf("score lasts %v", p.Seconds)
	}
	if !near(p.Tempo.QPM(float64(p.Notes[5].Tick)), 180) {
		t.Error("a half-note tempo mark is not in quarters")
	}
}

func TestGraceNotes(t *testing.T) {
	// Acciaccaturas before the beat steal from the note before; at the top
	// of the score they go on the beat; appoggiaturas take half the note.
	s := one(t, "bars: 3\ntempo: [{bar: 1, bpm: 60}]",
		"| /eD4 hC4 /sE4 /sF4 hG4 | ^qA4 hB4 h_ | ^eC5 ^eD5 sE5 s_ q.r hr |")
	p := perform(t, s)
	var got []string
	for _, n := range p.Notes {
		got = append(got, score.SpellMIDI(n.MIDI, 0, 1).String())
	}
	if strings.Join(got, " ") != "D4 C4 E4 F4 G4 A4 B4 C5 D5 E5" {
		t.Fatalf("order: %v", got)
	}
	n := p.Notes
	// The opening acciaccatura: a 32nd on the beat, C4 after it.
	if n[0].Tick != 0 || n[0].Ticks != 120 || n[1].Tick != 120 {
		t.Errorf("opening grace: %+v / %+v", n[0], n[1])
	}
	// Two before G4 at 1920: 32nds at 1680 and 1800, C4 cut to end at 1680.
	if n[2].Tick != 1680 || n[3].Tick != 1800 || n[4].Tick != 1920 || n[1].Tick+n[1].Ticks != 1680 {
		t.Errorf("crushed graces: %+v %+v %+v (C4 ends %d)", n[2], n[3], n[4], n[1].Tick+n[1].Ticks)
	}
	if n[2].Grace != score.Acciaccatura || n[2].Velocity != n[4].Velocity {
		t.Error("graces take their main note's level")
	}
	// The appoggiatura: a quarter taken from the half.
	if n[5].Tick != 3840 || n[5].Ticks != 960 || n[6].Tick != 4800 || n[6].Ticks != 960 {
		t.Errorf("appoggiatura: %+v / %+v", n[5], n[6])
	}
	// Two eighth appoggiaturas before a 16th: squeezed into half of it.
	if n[7].Ticks != 60 || n[8].Ticks != 60 || n[9].Tick != 7680+120 || n[9].Ticks != 120 {
		t.Errorf("squeezed appoggiaturas: %+v %+v %+v", n[7], n[8], n[9])
	}
}

func TestDynamicsSetVelocity(t *testing.T) {
	s := one(t, "bars: 2",
		"| qC4 qC4(p) qC4 qC4(sfz) | qC4 qC4(fp) qC4 qC4(ff) |",
		"| hE4 hE4 | qE4 qE4 qE4 qE4 |")
	p := perform(t, s)
	lv := func(name string) float32 { return score.Dynamics[name].Level }
	want := map[int64][]float32{
		0:    {lv("mf"), lv("mf")},
		960:  {lv("p")},
		1920: {lv("p"), lv("p")},
		2880: {lv("sfz")},
		3840: {lv("p"), lv("p")},
		4800: {lv("fp"), lv("fp")},
		5760: {lv("p"), lv("p")},
		6720: {lv("ff"), lv("ff")},
	}
	got := map[int64][]float32{}
	for _, n := range p.Notes {
		got[n.Tick] = append(got[n.Tick], n.Velocity)
	}
	for tick, w := range want {
		g := got[tick]
		if len(g) != len(w) {
			t.Errorf("tick %d: velocities %v, want %v", tick, g, w)
			continue
		}
		for i := range w {
			if g[i] != w[i] {
				t.Errorf("tick %d: velocities %v, want %v (voice 2 reads voice 1's marks)", tick, g, w)
			}
		}
	}
}

func TestRepeatsUnroll(t *testing.T) {
	s := one(t, "bars: 4\nrepeats: [{start: 2, end: 3, times: 3}]\nmarkers: [{name: m, bar: 3}]\ntempo: [{bar: 1, bpm: 120}, {bar: 3, bpm: 60}]",
		"| wC4 | wD4 | wE4 | wF4 |")
	p := perform(t, s)
	var bars []int
	for _, b := range p.Bars {
		bars = append(bars, b.Bar*10+b.Pass)
	}
	if want := []int{11, 21, 31, 22, 32, 23, 33, 41}; len(bars) != len(want) {
		t.Fatalf("bars %v, want %v", bars, want)
	} else {
		for i := range want {
			if bars[i] != want[i] {
				t.Fatalf("bars %v, want %v", bars, want)
			}
		}
	}
	if len(p.Notes) != 8 || len(p.Markers) != 3 {
		t.Fatalf("%d notes, %d markers", len(p.Notes), len(p.Markers))
	}
	// Bar 2 after the jump back is at 120 again, bar 3 at 60.
	for i, n := range p.Notes {
		dur := n.End - n.Start
		want := 2.0
		if n.Bar >= 3 {
			want = 4 // the mark at bar 3 holds on into bar 4
		}
		if !near(dur, want) {
			t.Errorf("note %d (bar %d) lasts %v, want %v", i, n.Bar, dur, want)
		}
	}
	if p.BarTick(3) != 2*3840 || p.BarTick(4) != 7*3840 {
		t.Error("BarTick finds the first time a bar plays")
	}
	q, err := Interpret(s, Options{NoRepeats: true})
	if err != nil || len(q.Bars) != 4 {
		t.Error("NoRepeats")
	}
}

func TestBeatsSectionsAndCurves(t *testing.T) {
	s := one(t, `bars: 4
pickup: 1/4
meter: [{bar: 1, sig: 4/4}, {bar: 3, sig: 6/8}, {bar: 4, sig: 3+2+2/8}]
sections: [{name: intro, bar: 1}, {name: main, bar: 3, loop: true, next: intro}]`,
		"| qC4 | wD4 | qE4 eE4 qE4 eE4 | q.F4 qF4 qF4 |")
	s.Lines[0].Lanes = []score.Lane{{Name: "volume", Points: []score.LanePoint{
		{At: score.Pos{Bar: 2}, Value: 1},
		{At: score.Pos{Bar: 2, Offset: score.F(1, 2)}, To: score.Pos{Bar: 3}, HasTo: true, Value: 0.5},
	}}}
	p := perform(t, s)
	var beats []int
	for _, b := range p.Beats {
		beats = append(beats, p.Bars[b.Bar].Bar*10+b.Beat)
	}
	want := []int{14, 21, 22, 23, 24, 31, 32, 41, 42, 43}
	if len(beats) != len(want) {
		t.Fatalf("beats %v, want %v", beats, want)
	}
	for i := range want {
		if beats[i] != want[i] {
			t.Fatalf("beats %v, want %v", beats, want)
		}
	}
	if len(p.Sections) != 2 || p.Sections[0].End != p.Sections[1].Tick || p.Sections[1].Next != 0 || !p.Sections[1].Loop || p.Sections[1].End != p.Length {
		t.Errorf("sections: %+v", p.Sections)
	}
	if p.SectionAt(0) != 0 || p.SectionAt(float64(p.Length-1)) != 1 {
		t.Error("SectionAt")
	}
	c := p.Curves[0]
	bar2 := float64(960)
	for tick, want := range map[float64]float32{0: 1, bar2: 1, bar2 + 960: 0.75, bar2 + 1920: 0.5, bar2 + 3000: 0.5, float64(p.Length): 0.5} {
		if got := c.At(tick); math.Abs(float64(got-want)) > 1e-6 {
			t.Errorf("volume at %v is %v, want %v", tick, got, want)
		}
	}
}

func TestArticulationsAndTechnique(t *testing.T) {
	s := one(t, "bars: 1", "| qC4(tech=pizz.) qC4(stacc) qC4(tech=arco) qC4(>) |")
	p := perform(t, s)
	got := []string{}
	for _, n := range p.Notes {
		got = append(got, n.Articulation+"/"+n.Technique)
	}
	if strings.Join(got, " ") != "/pizz. staccato/pizz. / accent/" {
		t.Errorf("articulations: %v", got)
	}
}

func TestOverflowIsAnError(t *testing.T) {
	// Tuplets nested past what int64 can hold come back as an error.
	text := "| "
	for i := 0; i < 24; i++ {
		text += "61:47{"
	}
	text += "zC4"
	for i := 0; i < 24; i++ {
		text += "}"
	}
	text += " |"
	s := one(t, "bars: 1", text)
	if _, err := Interpret(s, Options{}); err == nil {
		t.Error("no error")
	}
}

// Grace notes with no note after them play in time where the voice
// stopped — at the end of its last note, not the end of the score — and
// ones before a silent bar play before it.
func TestGracesWithNoMainNote(t *testing.T) {
	s := one(t, "bars: 4", "| qC4 qD4 qE4 qF4 | qG4 qA4 qB4 /eC5 |", "| wC3 | | | |")
	s.Lines[0].Staves[0].Voices[1] = &score.Voice{Measures: []score.Measure{mustMeasure(t, "wC3 /eD3"), {}, mustMeasure(t, "qE3 h.r")}}
	p := perform(t, s)
	var at []int64
	for _, n := range p.Notes {
		if n.Grace != score.NotGrace {
			at = append(at, n.Tick, n.Ticks)
		}
	}
	// C5 after the B4 that ends at beat 4 of bar 2 (tick 6720), an eighth
	// long; D3 after bar 1's whole note, before the empty bar 2.
	if len(at) != 4 || at[0] != 3840 || at[1] != 480 || at[2] != 6720 || at[3] != 480 {
		t.Errorf("graces at %v, want [3840 480 6720 480]", at)
	}
}

func mustMeasure(t *testing.T, text string) score.Measure {
	t.Helper()
	m, err := score.ParseMeasure(text)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
