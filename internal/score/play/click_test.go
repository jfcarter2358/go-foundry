package play

import (
	"math"
	"testing"
)

// blips are the frames where the left channel rises from silence after at
// least gap silent frames: each click (and each note) once.
func blips(out []float32, gap int) []int {
	var on []int
	quiet := gap
	for i := 0; i < len(out)/2; i++ {
		if out[i*2] == 0 {
			quiet++
			continue
		}
		if quiet >= gap {
			on = append(on, i)
		}
		quiet = 0
	}
	return on
}

// renderBlocks runs a player for secs in blocks of block frames.
func renderBlocks(p *Player, secs float64, block int) []float32 {
	total := int(secs * rate)
	out := make([]float32, total*2)
	for i := 0; i < total; i += block {
		n := min(block, total-i)
		p.Render(out[i*2:(i+n)*2], n, rate)
	}
	return out
}

// A count-in clicks its bar's beats, then the transport starts on the frame
// its last beat ends — whatever the block size, from the middle of a bar,
// and under the tempo slider.
func TestCountInStartsOnTime(t *testing.T) {
	useTestInstruments(t)
	// 120 bpm: a beat is 0.5 s, 24000 frames.
	s := build(t, "bars: 2\ntempo: [{bar: 1, bpm: 120}]", "A|dc/0.5|0|| qC4 qr qD4 qr | qE4 qr qr qr |")
	prog := prepare(t, s)
	for _, block := range []int{480, 1001, 64} {
		p := NewFromProgram(prog)
		p.CountIn(0, 1, 0.8)
		out := renderBlocks(p, 6, block)
		// Clicks start with sin(0) = 0, so a click shows a frame after it.
		want := []int{1, 24001, 48001, 72001, 96000, 144000, 192000}
		eqInts(t, "count-in from bar 1", blips(out, 400), want)
	}
	// From beat 3 of bar 1: four beats of count, then D4 at once.
	p := NewFromProgram(prog)
	p.CountIn(1920, 1, 0.8)
	eqInts(t, "count-in from beat 3", blips(renderBlocks(p, 4, 480), 400), []int{1, 24001, 48001, 72001, 96000, 144000})
	// At half speed everything takes twice as long.
	p = NewFromProgram(prog)
	p.TempoScale(0.5, 0)
	p.CountIn(0, 2, 0.8)
	out := renderBlocks(p, 12, 480)
	b := blips(out, 400)
	if len(b) < 9 || b[7] != 336001 || b[8] != 384000 {
		t.Fatalf("two bars of count-in at half speed: %v", b)
	}
}

// The position says a count-in is running and which beat was heard; Stop
// during it cancels it and nothing plays.
func TestCountInPositionAndStop(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 2\ntempo: [{bar: 1, bpm: 120}]", "A|dc/0.5|0|| qC4 qr qD4 qr | qE4 qr qr qr |")
	p := NewFromProgram(prepare(t, s))
	p.CountIn(0, 1, 1)
	buf := make([]float32, 480*2)
	beats := []int{}
	for i := 0; i < 200; i++ {
		p.Render(buf, 480, rate)
		pos := p.Position()
		if pos.CountIn {
			if pos.Playing {
				t.Fatal("playing during the count-in")
			}
			if len(beats) == 0 || beats[len(beats)-1] != pos.CountBeat {
				beats = append(beats, pos.CountBeat)
			}
		}
	}
	eqInts(t, "count beats heard", beats, []int{1, 2, 3, 4})
	if pos := p.Position(); pos.CountIn || !pos.Playing {
		t.Fatalf("after the count: %+v", pos)
	}
	if time := p.Position().Published; time.IsZero() {
		t.Error("the position carries when it was published")
	}

	p = NewFromProgram(prepare(t, s))
	p.CountIn(0, 1, 1)
	out := render(p, 4, map[int]func(){20: func() { p.StopTransport() }})
	if b := blips(out, 400); len(b) != 1 {
		t.Fatalf("stopped after the first click: %v", b)
	}
	if p.Position().Playing || p.Position().CountIn {
		t.Fatalf("still going: %+v", p.Position())
	}
}

// The metronome clicks every beat the transport plays, the downbeat higher
// and louder; off, it is silent.
func TestClickOnEveryBeat(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 2\nmeter: [{bar: 1, sig: 3/4}]\ntempo: [{bar: 1, bpm: 120}]", "A|dc/0|0|| qr qr qr | qr qr qr |")
	p := NewFromProgram(prepare(t, s))
	p.SetClick(true, 1)
	p.Start("")
	out := renderBlocks(p, 3.2, 480)
	eqInts(t, "clicks", blips(out, 400), []int{1, 24001, 48001, 72001, 96001, 120001})
	peak := func(from int) float64 {
		m := 0.0
		for i := from; i < from+1900; i++ {
			m = math.Max(m, math.Abs(float64(out[i*2])))
		}
		return m
	}
	if d, b := peak(0), peak(24000); d < 0.45 || b > 0.36 || d <= b {
		t.Errorf("downbeat peak %v, beat peak %v", d, b)
	}
	// Zero crossings in the downbeat's first 20 ms: 1760 Hz has more.
	cross := func(from int) int {
		n := 0
		for i := from + 1; i < from+960; i++ {
			if (out[i*2] > 0) != (out[(i-1)*2] > 0) {
				n++
			}
		}
		return n
	}
	if cross(1) <= cross(24001) {
		t.Errorf("the downbeat should be higher: %d vs %d crossings", cross(1), cross(24001))
	}
	p = NewFromProgram(prepare(t, s))
	p.SetClick(true, 1)
	p.SetClick(false, 1)
	p.Start("")
	if b := blips(renderBlocks(p, 3, 480), 400); len(b) != 0 {
		t.Errorf("click off: %v", b)
	}
}

func TestClickAndCountInDoNotAllocate(t *testing.T) {
	useTestInstruments(t)
	s := build(t, "bars: 4\nloop: true\ntempo: [{bar: 1, bpm: 200}]", "A|dc/0.5|0|| qC4 qr qD4 qr | qE4 qr qr qr | qr qr qr qr | wC4 |")
	p := NewFromProgram(prepare(t, s))
	p.SetClick(true, 0.7)
	p.CountIn(0, 2, 0.7)
	buf := make([]float32, 480*2)
	p.Render(buf, 480, rate)
	if n := testing.AllocsPerRun(600, func() { p.Render(buf, 480, rate) }); n != 0 {
		t.Errorf("Render with a count-in and the click allocates %v times a block", n)
	}
}
