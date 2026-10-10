package play

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/score"
	"foundry/internal/tracker"
)

const rate = 48000

// dcInstrument plays a constant level, so where a note starts and stops
// and how loud it is can be read straight off the output.
func dcInstrument(name string, level float32) *tracker.Instrument {
	d := make([]float32, rate)
	for i := range d {
		d[i] = level
	}
	in := tracker.NewInstrument(name, name)
	in.FadeOut = 0.001
	in.SetLoaded(0, &tracker.Sample{Name: name, Data: d, Channels: 1, Rate: rate, BaseNote: tracker.NoteC4, Volume: 1,
		Loop: tracker.LoopForward, LoopStart: 0, LoopEnd: len(d)})
	return in
}

// useTestInstruments makes "dc/<level>" a DC instrument at that level.
func useTestInstruments(t *testing.T) {
	t.Helper()
	prev := LoadInstrument
	LoadInstrument = func(name string) (*tracker.Instrument, error) {
		var lv float32
		if _, err := fmt.Sscanf(name, "dc/%g", &lv); err != nil {
			return nil, fmt.Errorf("no instrument %q", name)
		}
		return dcInstrument(name, lv), nil
	}
	t.Cleanup(func() { LoadInstrument = prev })
}

// build makes a score from a header and lines given as "name|instrument|pan|voice".
func build(t *testing.T, header string, lines ...string) *score.Score {
	t.Helper()
	var b strings.Builder
	b.WriteString(header + "\nmaster: []\nbuses: {a: {return: 0}, b: {return: 0}}\nlines:\n")
	for _, l := range lines {
		f := strings.SplitN(l, "|", 4)
		fmt.Fprintf(&b, "  - name: %s\n    instrument: %s\n    pan: %s\n    staves:\n      - clef: treble\n        voices: [%q]\n", f[0], f[1], f[2], f[3])
	}
	s, err := score.Parse([]byte(b.String()))
	if err != nil {
		t.Fatalf("%v\n%s", err, b.String())
	}
	return s
}

func prepare(t *testing.T, s *score.Score) *Program {
	t.Helper()
	prog, errs, err := Prepare(s)
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	return prog
}

// onsets are the frames where the left channel rises from silence.
func onsets(out []float32) []int {
	var on []int
	prev := float32(0)
	for i := 0; i < len(out)/2; i++ {
		v := out[i*2]
		if prev == 0 && v != 0 {
			on = append(on, i)
		}
		prev = v
	}
	return on
}

func expectedOnsets(prog *Program, scale float64) []int {
	var out []int
	for _, n := range prog.Perf.Notes {
		out = append(out, int(math.Round(n.Start/scale*rate)))
	}
	return out
}

func eqInts(t *testing.T, what string, got, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %v, want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: onset %d at frame %d, want %d (got %v, want %v)", what, i, got[i], want[i], got, want)
		}
	}
}

// Every note starts on the frame its time in seconds falls on — through
// tuplets, a tempo change and the tempo slider, whatever the block size.
func TestOnsetsAreSampleAccurate(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 3\ntempo: [{bar: 1, bpm: 120}, {bar: 2, beat: \"3\", bpm: 77}]",
		"A|dc/0.5|0|| eC4 er 3{eD4 er eE4} sr sF4 er 5{sG4 sr sA4 sr sr} | qC5 qr qD5 qr | 3{qE5 qr qF5} er eG5 tr s.A5 er |")
	prog := prepare(t, s)
	for _, scale := range []float64{1, 0.5, 1.37} {
		for _, block := range []int{480, 64, 1001} {
			out := Render(prog, RenderOptions{Rate: rate, Block: block, TempoScale: scale, Tail: 0.2})
			eqInts(t, fmt.Sprintf("scale %v, block %d", scale, block), onsets(out), expectedOnsets(prog, scale))
		}
	}
}

// A note sounds until its written end, then releases.
func TestNoteOffsAtTheirEnd(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 1", "A|dc/0.5|0|| qC4 qr 3{eD4 er er} qr |")
	prog := prepare(t, s)
	out := Render(prog, RenderOptions{Rate: rate})
	for _, n := range prog.Perf.Notes {
		end := int(math.Round(n.End * rate))
		if out[(end-10)*2] == 0 {
			t.Errorf("note %d silent before its end (frame %d)", n.MIDI, end)
		}
		if v := out[(end+400)*2]; v != 0 {
			t.Errorf("note %d still sounding 400 frames after its end: %v", n.MIDI, v)
		}
	}
	// Velocity: mf at centre pan is 0.5 (dc) x mf^1.5 x cos 45°.
	want := 0.5 * VelocityGain(score.Dynamics["mf"].Level) * float32(math.Cos(math.Pi/4))
	if v := out[2000*2]; math.Abs(float64(v-want)) > 1e-4 {
		t.Errorf("level %v, want %v", v, want)
	}
}

// The same score renders to the same samples every time.
func TestRenderIsDeterministic(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 2\nrepeats: [{start: 1, end: 2}]",
		"A|dc/0.5|-0.5|| qC4+E4 eD4(p) eE4 3{qF4 qG4 qA4} | hB4~ qB4(sfz) /eC5 qD5 |",
		"B|dc/0.3|0.5|| wC3 | hG2 hC3 |")
	h1 := tracker.Hash(Render(prepare(t, s), RenderOptions{Rate: rate, Tail: 1}))
	h2 := tracker.Hash(Render(prepare(t, s.Clone()), RenderOptions{Rate: rate, Tail: 1}))
	if h1 != h2 {
		t.Errorf("two renders differ: %s %s", h1, h2)
	}
}

// Nothing on the render path allocates once it has started.
func TestRenderAllocationFree(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 2\nloop: true\nsections: [{name: a, bar: 1}, {name: b, bar: 2, loop: true}]\nmarkers: [{name: m, bar: 1, beat: \"2\"}]",
		"A|dc/0.5|0|| 3{tC4 tD4 tE4} sF4 sG4 eA4 qB4+D5 h_ | 5{sC4 sD4 sE4 sF4 sG4} qA4 hB4 |",
		"B|dc/0.2|0|| wC3 | wG2 |")
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	buf := make([]float32, 480*2)
	for i := 0; i < 50; i++ {
		p.Render(buf, 480, rate)
	}
	if n := testing.AllocsPerRun(400, func() { p.Render(buf, 480, rate) }); n != 0 {
		t.Errorf("Render allocates %v times a block", n)
	}
	p.SetLoop(960, 3000)
	p.TempoScale(1.5, 0.2)
	p.Section("a", tracker.QuantBeat)
	if n := testing.AllocsPerRun(400, func() { p.Render(buf, 480, rate) }); n != 0 {
		t.Errorf("Render with a loop and a section jump allocates %v times a block", n)
	}
}

// render runs a player for secs seconds, applying cmd before block k.
func render(p *Player, secs float64, cmds map[int]func()) []float32 {
	total := int(secs * rate)
	out := make([]float32, total*2)
	for i, k := 0, 0; i < total; i, k = i+480, k+1 {
		if f := cmds[k]; f != nil {
			f()
		}
		p.Render(out[i*2:(i+480)*2], 480, rate)
	}
	return out
}

func TestLoopRegion(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 3", "A|dc/0.5|0|| qC4 qr qD4 qr | qE4 qr qF4 qr | qG4 qr qA4 qr |")
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	p.SetLoop(3840, 7680) // bar 2, at 120: 2 s .. 4 s
	out := render(p, 7.5, nil)
	// Bar 1's two notes, then bar 2's (at 2 s and 3 s) again every 2 s.
	want := []int{0, 48000, 96000, 144000, 192000, 240000, 288000, 336000}
	eqInts(t, "loop", onsets(out), want)
	pos := p.Position()
	if !pos.Playing || pos.Bar != 2 {
		t.Errorf("position after looping: %+v", pos)
	}
}

func TestSectionJumpsAndLoops(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 4\nsections: [{name: intro, bar: 1}, {name: main, bar: 2, loop: true}, {name: outro, bar: 4}]",
		"A|dc/0.5|0|| qC4 qr qr qr | qD4 qr qE4 qr | qF4 qr qr qr | qG4 qr qr qr |")
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	// Main (bars 2-3) loops: 2, 3, 4 s, then 6, 7, 8 s, … Half way through
	// its third bar (at 9 s) ask for the outro on the next bar: it starts at
	// 10 s, and the piece ends two seconds later.
	out := render(p, 13, map[int]func(){900: func() { p.Section("outro", tracker.QuantBar) }})
	want := []int{0, 2, 3, 4, 6, 7, 8, 10}
	for i := range want {
		want[i] *= rate
	}
	eqInts(t, "sections", onsets(out), want)
	var evs []Event
	evs = p.Events(evs)
	var got []string
	for _, e := range evs {
		switch e.Kind {
		case EventSection:
			got = append(got, "§"+e.Name)
		case EventEnd:
			got = append(got, "end")
		}
	}
	if strings.Join(got, " ") != "§intro §main §main §outro end" {
		t.Errorf("section events: %v", got)
	}
	if pos := p.Position(); pos.Playing {
		t.Errorf("still playing after the end: %+v", pos)
	}
}

func TestEventsInOrder(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 2\nmeter: [{bar: 1, sig: 3/4}, {bar: 2, sig: 6/8}]\nmarkers: [{name: hit, bar: 1, beat: \"2+1/2\"}]",
		"A|dc/0.5|0|| h.C4 | q.D4 q.E4 |")
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	render(p, 4, nil)
	var got []string
	for _, e := range p.Events(nil) {
		switch e.Kind {
		case EventBar:
			got = append(got, fmt.Sprintf("bar%d", e.Bar))
		case EventBeat:
			got = append(got, fmt.Sprintf("%d.%d", e.Bar, e.Beat))
		case EventMarker:
			got = append(got, e.Name)
		case EventEnd:
			got = append(got, "end")
		}
	}
	if s := strings.Join(got, " "); s != "bar1 1.1 1.2 hit 1.3 bar2 2.1 2.2 end" {
		t.Errorf("events: %s", s)
	}
}

func TestMuteSoloAndInstrumentSwap(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 4",
		"L|dc/0.5|-1|| wC4 | wC4 | wC4 | wC4 |",
		"R|dc/0.5|1|| wC4 | wC4 | wC4 | wC4 |")
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	// 0-2 s both; 2-4 s left muted; 4-6 s right soloed... (left unmuted
	// first); 6-8 s the right line on an instrument twice as loud.
	out := render(p, 8, map[int]func(){
		200: func() { p.MuteIndex(0, true, 0.01) },
		400: func() { p.MuteIndex(0, false, 0.01); p.Solo(1) },
		600: func() {
			p.Solo(-1)
			if err := p.SetInstrument(1, "dc/1"); err != nil {
				t.Fatal(err)
			}
		},
	})
	at := func(sec float64) (float32, float32) { i := int(sec*rate) * 2; return out[i], out[i+1] }
	l1, r1 := at(1)
	l3, r3 := at(3)
	l5, r5 := at(5)
	l7, r7 := at(7)
	if l1 == 0 || r1 == 0 || math.Abs(float64(l1-r1)) > 1e-6 {
		t.Errorf("both lines: %v %v", l1, r1)
	}
	if l3 != 0 || r3 != r1 {
		t.Errorf("left muted: %v %v", l3, r3)
	}
	if l5 != 0 || r5 != r1 {
		t.Errorf("right soloed: %v %v", l5, r5)
	}
	if math.Abs(float64(r7-2*r1)) > 1e-5 || math.Abs(float64(l7-l1)) > 1e-6 {
		t.Errorf("swapped instrument: %v %v (was %v)", l7, r7, r1)
	}
	if p.LineLevel(1) <= p.LineLevel(0) {
		t.Errorf("meters: %v %v", p.LineLevel(0), p.LineLevel(1))
	}
}

func TestTempoScaleAndPosition(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 4", "A|dc/0.5|0|| qC4 qr qD4 qr | qE4 qr qF4 qr | qG4 qr qA4 qr | qB4 qr qC5 qr |")
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	// One second at the written tempo (bar 1, beat 3), then half speed.
	out := render(p, 3.1, map[int]func(){100: func() { p.TempoScale(0.5, 0) }})
	want := []int{0, 48000, 48000 + 96000}
	eqInts(t, "tempo scale", onsets(out), want)
	pos := p.Position()
	// 1 s at 1x + 2.1 s at 0.5x = 2.05 s of score: bar 2, beat 1.
	if math.Abs(pos.Seconds-2.05) > 1e-9 || pos.Bar != 2 || pos.Beat != 1 || pos.Tick != 3936 {
		t.Errorf("position: %+v", pos)
	}
	p.PlayFrom(3*3840 + 1920)
	render(p, 0.01, nil)
	if pos := p.Position(); pos.Bar != 4 || pos.Beat != 3 {
		t.Errorf("after PlayFrom: %+v", pos)
	}
}

func TestParamBindings(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 2\nparams: [{name: level, default: 1}]\nbindings: [{param: level, line: A, target: volume, from: 0, to: 1}]",
		"A|dc/0.5|0|| wC4 | wC4 |")
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	out := render(p, 3, map[int]func(){100: func() { p.SetParam("level", 0.5, 0) }})
	full, half := out[48000], out[int(1.5*rate)*2]
	if math.Abs(float64(half-full/2)) > 1e-5 {
		t.Errorf("param 0.5 plays at %v of %v", half, full)
	}
}

func TestVolumeLane(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 2", "A|dc/0.5|0|| wC4 | wC4 |")
	s.Lines[0].Lanes = []score.Lane{{Name: "volume", Points: []score.LanePoint{{At: score.Pos{Bar: 1}, Value: 1}, {At: score.Pos{Bar: 2}, Value: 0}}}}
	out := Render(prepare(t, s), RenderOptions{Rate: rate})
	a, b := out[int(0.2*rate)*2], out[int(1.0*rate)*2]
	if math.Abs(float64(b/a-0.5/0.9)) > 0.02 {
		t.Errorf("a lane from 1 to 0 over the bar: %v at 0.2 s, %v at 1 s", a, b)
	}
}

// BenchmarkScheduler16Lines is a busy score: sixteen lines of 16th-note
// dyads at 140, looping — the scheduler's cost over the engine's for a 10 ms
// block.
func BenchmarkScheduler16Lines(b *testing.B) {
	prev := LoadInstrument
	LoadInstrument = func(name string) (*tracker.Instrument, error) { return dcInstrument(name, 0.05), nil }
	defer func() { LoadInstrument = prev }()
	var sb strings.Builder
	sb.WriteString("bars: 2\nloop: true\ntempo: [{bar: 1, bpm: 140}]\nlines:\n")
	bar := strings.Repeat("sC4+E4 sD4+F4 sE4+G4 sF4+A4 ", 4)
	for i := 0; i < 16; i++ {
		fmt.Fprintf(&sb, "  - name: L%d\n    instrument: dc\n    staves: [{clef: treble, voices: [\"| %s | %s |\"]}]\n", i, bar, bar)
	}
	s, err := score.Parse([]byte(sb.String()))
	if err != nil {
		b.Fatal(err)
	}
	prog, _, err := Prepare(s)
	if err != nil {
		b.Fatal(err)
	}
	p := NewFromProgram(prog)
	p.Start("")
	buf := make([]float32, 480*2)
	for i := 0; i < 100; i++ {
		p.Render(buf, 480, rate)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Render(buf, 480, rate)
	}
}

// FitGain brings a loud score under the target and leaves a quiet one be.
func TestFitGain(t *testing.T) {
	useTestInstruments(t)
	var lines []string
	for _, n := range []string{"A", "B", "C", "D"} {
		lines = append(lines, n+"|dc/0.5|0|| wC4(fff) | wC4")
	}
	s := build(t, "bars: 2", lines...)
	s.Gain = 1
	peak, err := FitGain(s, 0.9)
	if err != nil {
		t.Fatal(err)
	}
	if peak <= 0.9 || s.Gain >= 1 {
		t.Fatalf("peak %.2f, gain %.2f", peak, s.Gain)
	}
	after := float64(tracker.Peak(Render(prepare(t, s), RenderOptions{Rate: 32000, Tail: 1})))
	if after > 0.9 || after < 0.85 {
		t.Errorf("peak after %.3f", after)
	}
	gain := s.Gain
	if _, err := FitGain(s, 0.9); err != nil || s.Gain != gain {
		t.Errorf("a second fit moved the gain %.2f → %.2f", gain, s.Gain)
	}
}

// Unsoloing goes back to the score's own mutes: a line the score mutes
// stays muted after another line's solo is lifted.
func TestUnsoloKeepsTheScoresMutes(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 4",
		"L|dc/0.5|-1|| wC4 | wC4 | wC4 | wC4 |",
		"R|dc/0.5|1|| wC4 | wC4 | wC4 | wC4 |")
	s.Lines[0].Mute = true
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	out := render(p, 6, map[int]func(){
		200: func() { p.Solo(0) },
		400: func() { p.Solo(-1) },
	})
	at := func(sec float64) (float32, float32) { i := int(sec*rate) * 2; return out[i], out[i+1] }
	if l, r := at(1); l != 0 || r == 0 {
		t.Errorf("score mute: %v %v", l, r)
	}
	if l, r := at(3); l == 0 || r != 0 {
		t.Errorf("left soloed: %v %v", l, r)
	}
	if l, r := at(5); l != 0 || r == 0 {
		t.Errorf("after unsolo: %v %v (the left line should be muted again)", l, r)
	}
}

// An offline render clamps its tempo scale the way the transport does.
func TestRenderClampsTempoScale(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 1", "A|dc/0.5|0|| wC4 |")
	prog := prepare(t, s)
	fast := Render(prog, RenderOptions{Rate: rate, TempoScale: 10})
	if want := int(math.Ceil(prog.Perf.Seconds / 2 * rate)); len(fast)/2 != want {
		t.Errorf("tempo 10 rendered %d frames, want %d (clamped to 2)", len(fast)/2, want)
	}
}

// Whatever the game thread asks, in whatever order, the player keeps its
// play head inside the piece, never hangs a note, and goes quiet once
// stopped.
func TestRandomCommandsKeepThePlayerSane(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 4\nrepeats: [{start: 2, end: 3}]\nsections: [{name: a, bar: 1}, {name: b, bar: 2, loop: true}, {name: c, bar: 4, next: a}]\nmarkers: [{name: m, bar: 3, beat: \"2\"}]\ntempo: [{bar: 1, bpm: 140}, {bar: 3, bpm: 100, ramp: true}, {bar: 4, bpm: 70}]",
		"A|dc/0.3|-0.5|| /eB3 qC4~ 3{eC4 eD4 eE4} hF4 | wG4~ | wG4 | 5{sA4 sB4 sC5 sD5 sE5} q.F5 e_ hG5 |",
		"B|dc/0.2|0.5|| wC3+G3 | hD3 hE3 | R | wC3 |")
	short := build(t, "bars: 1", "A|dc/0.3|0|| qC4 qD4 qE4 qF4 |")
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	rng := rand.New(rand.NewSource(7))
	length := p.Performance().Length
	buf := make([]float32, 1024*2)
	for step := 0; step < 3000; step++ {
		switch rng.Intn(15) {
		case 13:
			p.SetMix(rng.Intn(4)-1, LineMix{Volume: rng.Float32() * 1.5, Pan: rng.Float32()*2 - 1, SendA: rng.Float32()})
			if rng.Intn(4) == 0 {
				p.SetGain(rng.Float32() * 2)
			}
		case 0:
			p.PlayFrom(rng.Int63n(length + 2000))
		case 1:
			p.MoveTo(rng.Int63n(length+2000) - 1000)
		case 2:
			a := rng.Int63n(length)
			p.SetLoop(a, a+rng.Int63n(4000))
		case 3:
			p.ClearLoop()
		case 4:
			p.Section([]string{"a", "b", "c", "nowhere"}[rng.Intn(4)], tracker.Quantize(rng.Intn(4)))
		case 5:
			p.TempoScale(rng.Float64()*3, rng.Float64())
		case 6:
			p.StopTransport()
		case 7:
			p.Play()
		case 8:
			p.MuteIndex(rng.Intn(3)-1, rng.Intn(2) == 0, 0.05)
		case 9:
			p.Solo(rng.Intn(3) - 1)
		case 10:
			p.Pause(rng.Intn(4) == 0)
		case 11:
			p.NoteFor(rng.Intn(3)-1, 40+rng.Intn(40), 0.5, 0.1)
		case 12:
			if rng.Intn(20) == 0 {
				if rng.Intn(2) == 0 {
					p.SetScore(short)
				} else {
					p.SetScore(s)
				}
				length = p.Performance().Length
			}
		}
		frames := 1 + rng.Intn(1024)
		p.Render(buf, frames, rate)
		for _, v := range buf[:frames*2] {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || math.Abs(float64(v)) > 4 {
				t.Fatalf("step %d: sample %v", step, v)
			}
		}
		pos := p.Position()
		if pos.Tick < 0 || pos.Tick > p.Performance().Length+1 {
			t.Fatalf("step %d: play head at tick %d of %d", step, pos.Tick, p.Performance().Length)
		}
		p.Events(nil)
	}
	p.Pause(false)
	p.StopTransport()
	for i := 0; i < 400; i++ {
		p.Render(buf, 480, rate)
	}
	if n := p.LiveVoices(); n != 0 {
		t.Errorf("%d voices still sounding 4 s after stopping", n)
	}
}

// SetMix moves a line's fader and pan at once, with no new program, and a
// later SetScore takes the score's values back; SetGain is the master.
func TestLiveMixAndGain(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 4", "A|dc/0.5|0|| wC4 | wC4 | wC4 | wC4 |")
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	out := render(p, 6, map[int]func(){
		100: func() { p.SetMix(0, LineMix{Volume: 0.5, Pan: -1}) },
		200: func() { p.SetGain(0.25) },
		300: func() {
			p.SetGain(1)
			if _, err := p.SetScore(s.Clone()); err != nil {
				t.Fatal(err)
			}
		},
		400: func() { p.SetMix(7, LineMix{Volume: 9}) }, // no such line: ignored
	})
	at := func(sec float64) (float32, float32) { i := int(sec*rate) * 2; return out[i], out[i+1] }
	l0, r0 := at(0.5)
	l1, r1 := at(1.5)
	l2, r2 := at(2.5)
	l3, r3 := at(3.5)
	l4, r4 := at(4.5)
	if l0 == 0 || math.Abs(float64(l0-r0)) > 1e-6 {
		t.Fatalf("centred at unity: %v %v", l0, r0)
	}
	// Hard left at half volume: the strip's balance keeps the left at its
	// centre level and silences the right.
	want := l0 * 0.5
	if math.Abs(float64(l1-want)) > 1e-4 || math.Abs(float64(r1)) > 1e-6 {
		t.Errorf("after SetMix: %v %v, want %v 0", l1, r1, want)
	}
	if math.Abs(float64(l2-l1*0.25)) > 1e-4 || math.Abs(float64(r2)) > 1e-6 {
		t.Errorf("after SetGain: %v %v, want %v", l2, r2, l1*0.25)
	}
	if math.Abs(float64(l3-l0)) > 1e-5 || math.Abs(float64(r3-r0)) > 1e-5 {
		t.Errorf("after SetScore the score's mix is back: %v %v, want %v %v", l3, r3, l0, r0)
	}
	if l4 != l3 || r4 != r3 {
		t.Errorf("a mix for no line changed something: %v %v", l4, r4)
	}
	if n := testing.AllocsPerRun(200, func() {
		p.SetMix(0, LineMix{Volume: 0.7})
		p.Render(make([]float32, 0, 960)[:960], 480, rate)
	}); n > 1 { // the one allocation is the test's own buffer
		t.Errorf("a mix command allocates on the render path: %v", n)
	}
}

// RenderOptions.To renders a region: notes sounding at its end release
// there, and the tail after it is the release and nothing else.
func TestRenderRegion(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 3", "A|dc/0.5|0|| qC4 qD4 qE4 qF4 | wG4 | qA4 qB4 qC5 qD5 |")
	prog := prepare(t, s)
	// Bar 2 alone (2 s .. 4 s at 120) with a half-second tail.
	out := Render(prog, RenderOptions{Rate: rate, From: 3840, To: 7680, Tail: 0.5})
	if got, want := len(out)/2, int(2.5*rate); got != want {
		t.Fatalf("length %d frames, want %d", got, want)
	}
	eqInts(t, "region", onsets(out), []int{0})
	if out[int(1.9*rate)*2] == 0 {
		t.Error("the bar's note is silent before the region's end")
	}
	if v := out[(2*rate+400)*2]; v != 0 {
		t.Errorf("still sounding 400 frames after the region's end: %v", v)
	}
	// At half speed the region takes twice as long.
	slow := Render(prog, RenderOptions{Rate: rate, From: 3840, To: 7680, TempoScale: 0.5})
	if got, want := len(slow)/2, 4*rate; got != want {
		t.Errorf("half speed: %d frames, want %d", got, want)
	}
	// Past the end it is the rest of the piece.
	all := Render(prog, RenderOptions{Rate: rate, From: 3840, To: 1 << 40})
	if got, want := len(all)/2, 4*rate; got != want {
		t.Errorf("to past the end: %d frames, want %d", got, want)
	}
}

// SetReturns brings a send bus in live: a dry line with its send up is
// dry until the reverb's return is raised.
func TestLiveReturns(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 4", "A|dc/0.5|0|| qC4 qr hr | R | R | R |")
	s.Lines[0].SendA = 1
	s.Buses.A = score.Bus{FX: []score.FX{{Kind: "reverb", Params: []score.FXParam{{Name: "size", Value: 0.8}}}}, Return: 0}
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	dry := render(p, 2, nil)
	q := NewFromProgram(prepare(t, s))
	q.Start("")
	wet := render(q, 2, map[int]func(){0: func() { q.SetReturns(1, 0) }})
	tail := func(out []float32) float64 {
		e := 0.0
		for i := int(1.0 * rate); i < int(1.9*rate); i++ {
			e += math.Abs(float64(out[i*2]))
		}
		return e
	}
	if tail(dry) != 0 {
		t.Errorf("dry line has a tail: %v", tail(dry))
	}
	if tail(wet) == 0 {
		t.Error("raising the return gave no reverb tail")
	}
}

// constGen is a stinger that plays a constant level for n frames.
type constGen struct {
	level float32
	n     int
}

func (g *constGen) Render(dst []float32, frames, rate int) bool {
	k := min(frames, g.n)
	for i := 0; i < frames; i++ {
		v := float32(0)
		if i < k {
			v = g.level
		}
		dst[i*2], dst[i*2+1] = v, v
	}
	g.n -= k
	return g.n > 0
}

// A stinger starts on the frame its boundary falls on — the next bar,
// beat, the end of the section — or at once, plays to its own end over the
// score, and a score that has finished keeps rendering until it is done.
func TestStingers(t *testing.T) {
	useTestInstruments(t)
	// 120 bpm in 4/4: a bar is 2 s, a beat half a second. Only rests, so
	// what is heard is the stinger.
	s := build(t, "bars: 2", "A|dc/0.5|0|| wr | wr |")
	for _, c := range []struct {
		q    tracker.Quantize
		want int
	}{
		{tracker.QuantBar, 2 * rate},
		{tracker.QuantBeat, rate},
		{tracker.QuantSection, 4 * rate}, // no section: the end of the piece
	} {
		p := NewFromProgram(prepare(t, s))
		p.Start("")
		g := &constGen{level: 0.25, n: rate / 4}
		// Asked for 0.7 s in: the next beat is 1 s, the next bar 2 s.
		out := render(p, 4.5, map[int]func(){70: func() { p.Stinger(g, c.q) }})
		eqInts(t, fmt.Sprintf("stinger on quantise %d", c.q), onsets(out), []int{c.want})
		if g.n != 0 {
			t.Errorf("quantise %d: the stinger has %d frames left", c.q, g.n)
		}
		if v := out[(c.want+rate/8)*2]; v != 0.25 {
			t.Errorf("quantise %d: the stinger plays at %v, want 0.25", c.q, v)
		}
	}

	// Now: the next block.
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	g := &constGen{level: 0.25, n: rate}
	out := render(p, 1, map[int]func(){10: func() { p.Stinger(g, tracker.QuantNow) }})
	eqInts(t, "stinger now", onsets(out), []int{4800})

	// A loop jump is a boundary: a stinger waiting for a bar that the loop
	// never reaches starts at the jump.
	p = NewFromProgram(prepare(t, build(t, "bars: 3", "A|dc/0.5|0|| wr | wr | wr |")))
	p.Start("")
	p.SetLoop(0, 1920) // the first half of bar 1
	g = &constGen{level: 0.25, n: 100}
	out = render(p, 1.5, map[int]func(){10: func() { p.Stinger(g, tracker.QuantBar) }})
	eqInts(t, "stinger at a loop jump", onsets(out), []int{rate})

	// The score ends (it does not loop) while a long stinger plays: the
	// player keeps rendering until the stinger is done.
	p = NewFromProgram(prepare(t, build(t, "bars: 1", "A|dc/0.5|0|| wr |")))
	p.Start("")
	g = &constGen{level: 0.25, n: 8 * rate}
	p.Stinger(g, tracker.QuantNow)
	buf := make([]float32, 480*2)
	blocks := 0
	for p.Render(buf, 480, rate) {
		blocks++
		if blocks > 2000 {
			t.Fatal("the player never finished")
		}
	}
	if secs := float64(blocks*480) / rate; secs < 7.9 {
		t.Errorf("the player stopped %.2f s in, before its 8 s stinger ended", secs)
	}
}

// Stingers past the cap are refused, and mixing them allocates nothing.
func TestStingerCapAndAllocations(t *testing.T) {
	useTestInstruments(t)
	p := NewFromProgram(prepare(t, build(t, "bars: 8\nloop: true", "A|dc/0.5|0|| wC4 | R | R | R | R | R | R | R |")))
	p.Start("")
	buf := make([]float32, 480*2)
	gens := make([]*constGen, maxStingers+2)
	for i := range gens {
		gens[i] = &constGen{level: 0.01, n: 30 * rate}
		p.Stinger(gens[i], tracker.QuantNow)
	}
	p.Render(buf, 480, rate)
	if len(p.stingers) != maxStingers {
		t.Errorf("%d stingers playing, want the cap of %d", len(p.stingers), maxStingers)
	}
	if n := testing.AllocsPerRun(200, func() { p.Render(buf, 480, rate) }); n != 0 {
		t.Errorf("Render with stingers allocates %v times a block", n)
	}
}

// A score stinger is the score once: no loop, no section looping back.
func TestNewStingerPlaysOnce(t *testing.T) {
	useTestInstruments(t)
	dir := t.TempDir()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = dir
	t.Cleanup(func() { fileutil.ResourcesPath = prev; score.Reload() })
	score.Reload()
	src := build(t, "bars: 1\nloop: true\nsections: [{name: a, bar: 1, loop: true}]", "A|dc/0.5|0|| qC4 qr qr qr |")
	if err := score.Save("sting", src); err != nil {
		t.Fatal(err)
	}
	p, err := NewStinger("sting")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]float32, 480*2)
	frames := 0
	for p.Render(buf, 480, rate) {
		frames += 480
		if frames > 20*rate {
			t.Fatal("the stinger loops")
		}
	}
	if secs := float64(frames) / rate; secs < 2 || secs > 2+tailSeconds+0.1 {
		t.Errorf("the stinger ran %.2f s, want its 2 s bar and the tail", secs)
	}
	if !src.Loop || !src.Sections[0].Loop {
		t.Error("NewStinger changed the score it loaded")
	}
	if _, err := NewStinger("missing"); err == nil {
		t.Error("NewStinger of a missing score did not fail")
	}
}

// Play has no device to play on here: the player comes back started where
// it was asked, its voice zero.
func TestPlayStartsWhereAsked(t *testing.T) {
	useTestInstruments(t)
	dir := t.TempDir()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = dir
	t.Cleanup(func() { fileutil.ResourcesPath = prev; score.Reload() })
	score.Reload()
	src := build(t, "bars: 2\nsections: [{name: a, bar: 1}, {name: b, bar: 2}]\nparams: [{name: x, default: 0}]", "A|dc/0.5|0|| wC4 | wD4 |")
	if err := score.Save("theme", src); err != nil {
		t.Fatal(err)
	}
	p, id, err := Play("theme", PlayOptions{Section: "b", Params: map[string]float32{"x": 0.75}})
	if err != nil || p == nil || !id.IsZero() {
		t.Fatalf("Play: %v %v %v", p, id, err)
	}
	if pos := p.Position(); !pos.Playing || pos.Bar != 2 || pos.Tick != 3840 {
		t.Errorf("started at %+v, want bar 2 (tick 3840)", pos)
	}
	if p.params[0].cur != 0.75 {
		t.Errorf("param x = %v, want 0.75", p.params[0].cur)
	}
	p, _, _ = Play("theme", PlayOptions{Resume: true, Tick: 960})
	if pos := p.Position(); pos.Tick != 960 || pos.Beat != 2 {
		t.Errorf("resumed at %+v, want tick 960, beat 2", pos)
	}
	if err := Preload("theme"); err != nil {
		t.Error(err)
	}
	if _, _, err := Play("nothing", PlayOptions{}); err == nil {
		t.Error("Play of a missing score did not fail")
	}
}

// A new program re-mutes the lines its score silences differently from the
// last one's — a solo on one line silences the others, whose own flags did
// not change — and leaves the rest alone.
func TestSetScoreFollowsAnotherLinesSolo(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 4", "A|dc/0.5|0|| wC4 | wC4 | wC4 | wC4 |", "B|dc/0.25|0|| wC4 | wC4 | wC4 | wC4 |")
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	soloed := s.Clone()
	soloed.Lines[1].Solo = true
	out := render(p, 3, map[int]func(){
		100: func() {
			if _, err := p.SetScore(soloed); err != nil {
				t.Fatal(err)
			}
		},
		200: func() {
			if _, err := p.SetScore(s.Clone()); err != nil {
				t.Fatal(err)
			}
		},
	})
	at := func(sec float64) float32 { return out[int(sec*rate)*2] }
	both, solo, back := at(0.5), at(1.5), at(2.5)
	if both == 0 || math.Abs(float64(solo-both/3)) > 1e-4 {
		t.Errorf("B soloed: %v, want B alone (%v)", solo, both/3)
	}
	if math.Abs(float64(back-both)) > 1e-4 {
		t.Errorf("the solo taken off: %v, want both again (%v)", back, both)
	}
}
