package play

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/tracker"
)

// parseOverride is one dynamics-lane override between two written
// positions.
func parseOverride(t *testing.T, s *score.Score, from, to string, level float32) []score.DynOverride {
	t.Helper()
	a, err := s.ParsePos(from)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.ParsePos(to)
	if err != nil {
		t.Fatal(err)
	}
	return []score.DynOverride{{From: a, To: b, Level: level}}
}

// sineInstrument is a looped sine at hz labelled C4, so a note's pitch can
// be read off the output by counting zero crossings.
func sineInstrument(name string, hz float64) *tracker.Instrument {
	period := float64(rate) / hz
	n := int(math.Round(period * 48))
	d := make([]float32, n)
	for i := range d {
		d[i] = float32(0.5 * math.Sin(2*math.Pi*float64(i)/period))
	}
	in := tracker.NewInstrument(name, name)
	in.FadeOut = 0.001
	in.SetLoaded(0, &tracker.Sample{Name: name, Data: d, Channels: 1, Rate: rate, BaseNote: tracker.NoteC4, Volume: 1,
		Loop: tracker.LoopForward, LoopStart: 0, LoopEnd: len(d)})
	return in
}

// useExpressionInstruments adds, over the DC test instruments:
//
//	sine/<hz>      a looped sine
//	decay/<level>  a DC level that does not loop (an instrument that only
//	               dies away: it keeps the level it starts at)
//	arts/<level>   a DC level whose "staccato" articulation plays at half
//	               the level (-6 dB) and "pizzicato" at a quarter (-12 dB),
//	               with a staccato length of a quarter
func useExpressionInstruments(t *testing.T) {
	t.Helper()
	useTestInstruments(t)
	dc := LoadInstrument
	LoadInstrument = func(name string) (*tracker.Instrument, error) {
		var v float32
		switch {
		case strings.HasPrefix(name, "sine/"):
			fmt.Sscanf(name, "sine/%g", &v)
			return sineInstrument(name, float64(v)), nil
		case strings.HasPrefix(name, "decay/"):
			fmt.Sscanf(name, "decay/%g", &v)
			in := dcInstrument(name, v)
			d := make([]float32, 3*rate) // long enough to outlast the tests' notes
			for i := range d {
				d[i] = v
			}
			in.SetLoaded(0, &tracker.Sample{Name: name, Data: d, Channels: 1, Rate: rate, BaseNote: tracker.NoteC4, Volume: 1})
			return in, nil
		case strings.HasPrefix(name, "arts/"):
			fmt.Sscanf(name, "arts/%g", &v)
			in := dcInstrument(name, v)
			in.Articulations = []tracker.Articulation{
				{Name: "staccato", Triggers: []string{"stacc."}, Gain: -20 * float32(math.Log10(2))},
				{Name: "pizzicato", Triggers: []string{"pizz."}, Gain: -20 * float32(math.Log10(4))},
			}
			in.Playback.Staccato = 0.25
			return in, nil
		}
		return dc(name)
	}
}

// levelAt is the left channel at a time, in seconds.
func levelAt(out []float32, sec float64) float32 { return out[int(sec*rate)*2] }

// centre is the left channel's share of a centred note.
var centre = float32(math.Cos(math.Pi / 4))

func approx(a, b, tol float32) bool { return math.Abs(float64(a-b)) <= float64(tol) }

// A held note on a sustaining instrument follows its line's hairpin: the
// level rises through the note.
func TestAHeldNoteSwellsUnderAHairpin(t *testing.T) {
	useExpressionInstruments(t)
	s := build(t, "bars: 2", "A|dc/0.5|0|| wC4(p,cresc,hairpin_end) | wr(f) |")
	out := Render(prepare(t, s), RenderOptions{Rate: rate})
	lv := func(name string) float32 { return float32(map[string]float64{"p": 48, "f": 96}[name] / 127) }
	for _, sec := range []float64{0.1, 0.5, 1, 1.5, 1.9} {
		k := float32(sec / 2) // the bar lasts 2 s at 120
		want := 0.5 * VelocityGain(lv("p")+(lv("f")-lv("p"))*k) * centre
		if got := levelAt(out, sec); !approx(got, want, want*0.02) {
			t.Errorf("at %v s: %v, want %v", sec, got, want)
		}
	}
	// The same hairpin on an instrument that only dies away: the note
	// keeps the level it started at.
	s.Lines[0].Instrument = "decay/0.5"
	out = Render(prepare(t, s), RenderOptions{Rate: rate})
	want := 0.5 * VelocityGain(lv("p")) * centre
	for _, sec := range []float64{0.1, 1, 1.9} {
		if got := levelAt(out, sec); !approx(got, want, 1e-4) {
			t.Errorf("decaying instrument at %v s: %v, want %v", sec, got, want)
		}
	}
}

// An accent starts a held note louder and falls back to the line's level
// within a 16th; an override in the dynamics lane holds a level the marks
// do not.
func TestAccentsFallBackAndOverridesHold(t *testing.T) {
	useExpressionInstruments(t)
	s := build(t, "bars: 2", "A|dc/0.5|0|| wC4(p,>) | wC4 |")
	s.Lines[0].Overrides = nil
	prog := prepare(t, s)
	if prog.Perf.Notes[0].Accent != 1.25 {
		t.Fatalf("accent %v", prog.Perf.Notes[0].Accent)
	}
	out := Render(prog, RenderOptions{Rate: rate})
	p := float32(48.0 / 127)
	if got, want := levelAt(out, 0.004), 0.5*VelocityGain(p*1.25)*centre; !approx(got, want, want*0.05) {
		t.Errorf("accented attack %v, want %v", got, want)
	}
	if got, want := levelAt(out, 1), 0.5*VelocityGain(p)*centre; !approx(got, want, want*0.01) {
		t.Errorf("after the accent %v, want %v", got, want)
	}
	// Bar 2 dragged down to 0.2 in the lane.
	s = build(t, "bars: 2", "A|dc/0.5|0|| wC4(p) | wC4 |")
	s.Lines[0].Overrides = parseOverride(t, s, "2", "3", 0.2)
	out = Render(prepare(t, s), RenderOptions{Rate: rate})
	if got, want := levelAt(out, 3), 0.5*VelocityGain(0.2)*centre; !approx(got, want, want*0.01) {
		t.Errorf("overridden bar %v, want %v", got, want)
	}
}

// A slide is one note whose pitch glides to the next note's and holds
// there: no second attack.
func TestASlideGlides(t *testing.T) {
	useExpressionInstruments(t)
	s := build(t, "bars: 1", "A|sine/480|0|| hC4(gliss) hG4 |")
	s.Lines[0].Gliss = "slide"
	prog := prepare(t, s)
	if len(prog.Perf.Notes) != 1 {
		t.Fatalf("%d notes", len(prog.Perf.Notes))
	}
	out := Render(prog, RenderOptions{Rate: rate})
	freq := func(from, to float64) float64 {
		a, b := int(from*rate), int(to*rate)
		n := 0
		for i := a + 1; i < b; i++ {
			if out[(i-1)*2] < 0 && out[i*2] >= 0 {
				n++
			}
		}
		return float64(n) / (to - from)
	}
	g4 := 480 * math.Pow(2, 7.0/12)
	// The half lasts 1 s: C4 for its first quarter, gliding to G4 by 1 s.
	mid := 480 * math.Pow(2, 7*(0.6-0.25)/0.75/12)
	for _, c := range []struct{ from, to, want float64 }{{0.05, 0.24, 480}, {0.55, 0.65, mid}, {1.2, 1.9, g4}} {
		if got := freq(c.from, c.to); math.Abs(got-c.want) > c.want*0.03 {
			t.Errorf("%v..%v s: %.1f Hz, want %.1f", c.from, c.to, got, c.want)
		}
	}
	// Unbroken: the level holds through the join, 10 ms at a time.
	rms := func(from float64) float64 {
		a := int(from * rate)
		sum := 0.0
		for i := a; i < a+480; i++ {
			sum += float64(out[i*2]) * float64(out[i*2])
		}
		return math.Sqrt(sum / 480)
	}
	ref := rms(0.9)
	for sec := 0.9; sec < 1.1; sec += 0.01 {
		if r := rms(sec); math.Abs(r-ref) > ref*0.05 {
			t.Errorf("level at %.2f s is %.4f, %.4f before the join", sec, r, ref)
		}
	}
	// A run instead: separate attacks on every semitone.
	s.Lines[0].Gliss = "chromatic"
	if n := len(prepare(t, s).Perf.Notes); n != 8 {
		t.Errorf("a chromatic run C4..G4 is %d notes, want 8", n)
	}
}

// Articulation marks and technique text pick the instrument's
// articulations; staccato is the instrument's length.
func TestMarksPickArticulations(t *testing.T) {
	useExpressionInstruments(t)
	s := build(t, "bars: 1", "A|arts/0.5|0|| qC4 qC4(stacc) qC4(tech=pizz.) qC4(tech=arco) |")
	prog := prepare(t, s)
	if got := []int{prog.arts[0], prog.arts[1], prog.arts[2], prog.arts[3]}; got[0] != 0 || got[1] != 1 || got[2] != 2 || got[3] != 0 {
		t.Fatalf("articulations %v", got)
	}
	if n := prog.Perf.Notes[1]; n.Ticks != 240 {
		t.Errorf("the instrument's staccato is a quarter of the note: %d ticks", n.Ticks)
	}
	out := Render(prog, RenderOptions{Rate: rate})
	base := 0.5 * VelocityGain(80.0/127) * centre
	for _, c := range []struct {
		sec  float64
		want float32
	}{{0.2, base}, {0.6, base / 2}, {0.7, 0}, {1.2, base / 4}, {1.7, base}} {
		if got := levelAt(out, c.sec); !approx(got, c.want, base*0.01) {
			t.Errorf("at %v s: %v, want %v", c.sec, got, c.want)
		}
	}
}

// Another instrument on a line whose notes depend on it performs the
// score again (here the staccato's length); on any other line the
// performance is shared.
func TestSwappingReperformsWhatDependsOnTheInstrument(t *testing.T) {
	useExpressionInstruments(t)
	s := build(t, "bars: 1", "A|dc/0.5|0|| qC4(stacc) qC4 hr |", "B|dc/0.5|0|| qC4 qC4 hr |")
	prog := prepare(t, s)
	if prog.Perf.Notes[0].Ticks != 480 {
		t.Fatalf("dc staccato %d", prog.Perf.Notes[0].Ticks)
	}
	a, err := prog.WithInstrument(0, "arts/0.5")
	if err != nil {
		t.Fatal(err)
	}
	if a.Perf == prog.Perf || a.Perf.Notes[0].Ticks != 240 || a.arts[0] != 1 {
		t.Errorf("swap on the staccato line: same perf %v, ticks %d, art %d", a.Perf == prog.Perf, a.Perf.Notes[0].Ticks, a.arts[0])
	}
	b, err := prog.WithInstrument(1, "arts/0.5")
	if err != nil || b.Perf != prog.Perf {
		t.Errorf("swap on the plain line re-performed: %v", err)
	}
}

// What a score does to the order and time of its bars plays: the onsets of
// a score with endings, a D.S. al Coda and a fermata land on their frames.
func TestFormAndFermataPlay(t *testing.T) {
	useExpressionInstruments(t)
	s := build(t, "bars: 5\nrepeats: [{start: 1, end: 2}]\nvoltas: [{start: 2, end: 2, passes: [1]}, {start: 3, end: 3, passes: [2]}]\n"+
		"jumps: [{mark: segno, bar: 1}, {mark: to_coda, bar: 1}, {mark: ds, bar: 4, al: coda}, {mark: coda, bar: 5}]",
		"A|dc/0.5|0|| hC4 hr | hD4 hr | hE4 hr | hF4(ferm) hr | wG4 |")
	prog := prepare(t, s)
	var order []int
	for _, b := range prog.Perf.Bars {
		order = append(order, b.Bar)
	}
	if fmt.Sprint(order) != "[1 2 1 3 4 1 5]" {
		t.Fatalf("bars %v", order)
	}
	out := Render(prog, RenderOptions{Rate: rate, Block: 333, Tail: 0.5})
	eqInts(t, "form", onsets(out), expectedOnsets(prog, 1))
	// The fermata: F4 holds two seconds, not one.
	f := prog.Perf.Notes[4]
	if math.Abs(f.End-f.Start-2) > 1e-9 {
		t.Errorf("fermata: %v s", f.End-f.Start)
	}
}

// Following notes and bending ones allocate nothing on the mixer.
func TestExpressionRendersWithoutAllocating(t *testing.T) {
	useExpressionInstruments(t)
	s := build(t, "bars: 4\nloop: true", "A|dc/0.5|0|| wC4(p,cresc,hairpin_end) | wC4(f,>) | hC4(gliss) hG4 | wC4(trem=3) |",
		"B|sine/480|0|| hC4(gliss) hE4(gliss) | wC4(tr) | qC4+E4+G4(arp) qr hr | R |")
	s.Lines[0].Gliss, s.Lines[1].Gliss = "slide", "slide"
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	buf := make([]float32, 480*2)
	for i := 0; i < 20; i++ {
		p.Render(buf, 480, rate)
	}
	if n := testing.AllocsPerRun(1200, func() { p.Render(buf, 480, rate) }); n != 0 {
		t.Errorf("Render allocates %v times a block", n)
	}
}

// A dynamics-lane drag while the music plays reaches a note already
// sounding: the new program's copy of the held note takes over, so the
// note follows the override at once, with no new attack.
func TestAHeldNoteHearsAnOverrideMadeWhileItPlays(t *testing.T) {
	useExpressionInstruments(t)
	s := build(t, "bars: 2", "A|dc/0.5|0|| wC4(p) | wC4 |")
	p := NewFromProgram(prepare(t, s))
	p.Start("")
	edited := s.Clone()
	edited.Lines[0].Overrides = parseOverride(t, edited, "1", "2", 0.2)
	out := render(p, 1.5, map[int]func(){
		50: func() {
			if _, err := p.SetScore(edited); err != nil {
				t.Fatal(err)
			}
		},
	})
	pl := float32(48.0 / 127)
	if got, want := levelAt(out, 0.3), 0.5*VelocityGain(pl)*centre; !approx(got, want, want*0.01) {
		t.Errorf("before the drag %v, want %v", got, want)
	}
	if got, want := levelAt(out, 1.2), 0.5*VelocityGain(0.2)*centre; !approx(got, want, want*0.01) {
		t.Errorf("the held note after the drag %v, want %v", got, want)
	}
	if n := len(onsets(out)); n != 1 {
		t.Errorf("%d attacks: the held note started again", n)
	}
}
