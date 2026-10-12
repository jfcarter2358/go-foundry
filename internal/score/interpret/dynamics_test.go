package interpret

import (
	"math"
	"testing"

	"foundry/internal/score"
)

func lv(name string) float32 { return score.Dynamics[name].Level }

func near32(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-5 }

// velocities are the notes' velocities in onset order.
func velocities(p *Performance) []float32 {
	var out []float32
	for _, n := range p.Notes {
		out = append(out, n.Velocity)
	}
	return out
}

func eqLevels(t *testing.T, what string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %v, want %v", what, got, want)
	}
	for i := range want {
		if !near32(got[i], want[i]) {
			t.Errorf("%s: note %d at %v, want %v (all %v)", what, i, got[i], want[i], got)
		}
	}
}

// ramp is the level k of the way from a to b.
func ramp(a, b string, k float32) float32 { return lv(a) + (lv(b)-lv(a))*k }

func TestHairpinsRampTheLevel(t *testing.T) {
	for _, c := range []struct {
		name   string
		voices []string
		want   []float32
	}{
		{"p < f", []string{"| qC4(p,cresc) qC4 qC4 qC4(hairpin_end) | wC4(f) |"},
			[]float32{lv("p"), ramp("p", "f", 0.25), ramp("p", "f", 0.5), ramp("p", "f", 0.75), lv("f")}},
		{"open-ended: one step, then held", []string{"| hC4(mp,cresc) hC4(hairpin_end) | wC4 |"},
			[]float32{lv("mp"), ramp("mp", "mf", 0.5), lv("mf")}},
		{"cresc. text runs to the next dynamic", []string{"| qC4(p,cresc=text) qC4 qC4 qC4 | wC4(ff) |"},
			[]float32{lv("p"), ramp("p", "ff", 0.25), ramp("p", "ff", 0.5), ramp("p", "ff", 0.75), lv("ff")}},
		{"dim. al niente", []string{"| hC4(mf,dim) hC4(hairpin_end) | wr(niente) |"},
			[]float32{lv("mf"), lv("mf") / 2}},
		{"through a dynamic on the way", []string{"| qC4(p,cresc) qC4 qC4(f) qC4(hairpin_end) | wC4 |"},
			[]float32{lv("p"), ramp("p", "f", 0.5), lv("f"), ramp("f", "ff", 0.5), lv("ff")}},
		{"the same hairpin in two voices is one", []string{"| qC4(p,cresc) qC4 qC4 qC4(hairpin_end) | wC4(f) |", "| hE3(p,cresc) hE3(hairpin_end) | wE3(f) |"},
			[]float32{lv("p"), lv("p"), ramp("p", "f", 0.25), ramp("p", "f", 0.5), ramp("p", "f", 0.5), ramp("p", "f", 0.75), lv("f"), lv("f")}},
		{"a decrescendo closes the voice's crescendo", []string{"| hC4(p,cresc) hC4(dim) | hC4(hairpin_end) hC4 |"},
			[]float32{lv("p"), lv("mp"), ramp("mp", "p", 0.5), lv("p")}},
	} {
		s := one(t, "bars: 2", c.voices...)
		p := perform(t, s)
		eqLevels(t, c.name, velocities(p), c.want)
	}
}

func TestMomentaryDynamicsAndAccents(t *testing.T) {
	s := one(t, "bars: 2", "| qC4(p) qC4(sfz) qC4 qC4(fp) | qC4(>) qC4(^) qC4(_) qC4(ff,^) |")
	p := perform(t, s)
	eqLevels(t, "levels", velocities(p), []float32{lv("p"), lv("sfz"), lv("p"), lv("fp"), lv("p") * 1.25, lv("p") * 1.4, lv("p") * 1.05, 1})
	for i, want := range []float32{1, lv("sfz") / lv("p"), 1, lv("fp") / score.Dynamics["fp"].After} {
		if !near32(p.Notes[i].Accent, want) {
			t.Errorf("note %d accent %v, want %v", i, p.Notes[i].Accent, want)
		}
	}
	// The curve carries on beneath an sfz: the line is still p.
	d := p.DynamicsOf(0)
	if got := d.Written.At(960 + 10); !near32(got, lv("p")) {
		t.Errorf("under the sfz the line is at %v", got)
	}
	if got := d.Written.At(2880 + 10); !near32(got, score.Dynamics["fp"].After) {
		t.Errorf("after fp the line is at %v", got)
	}
	// ff with a marcato is clamped at full velocity, its accent what is left.
	if n := p.Notes[7]; n.Velocity != 1 || !near32(n.Accent, 1/lv("ff")) {
		t.Errorf("ff marcato: %+v", n)
	}
}

func TestOverridesReplaceTheLevel(t *testing.T) {
	s := one(t, "bars: 3\nrepeats: [{start: 2, end: 2}]", "| qC4(p) qC4 qC4 qC4 | qC4 qC4(sfz) qC4 qC4 | wC4(f) |")
	s.Lines[0].Overrides = []score.DynOverride{{From: score.Pos{Bar: 2}, To: score.Pos{Bar: 2, Offset: score.F(3, 4)}, Level: 0.2}}
	p := perform(t, s)
	o := float32(0.2)
	sfz := o * lv("sfz") / lv("p")
	eqLevels(t, "overridden", velocities(p), []float32{
		lv("p"), lv("p"), lv("p"), lv("p"),
		o, sfz, o, lv("p"), // bar 2, first time
		o, sfz, o, lv("p"), // and again
		lv("f")})
	d := p.DynamicsOf(0)
	for _, tick := range []float64{3840 + 100, 7680 + 100} {
		if w, e := d.Written.At(tick), d.Effective.At(tick); !near32(w, lv("p")) || !near32(e, 0.2) {
			t.Errorf("tick %v: written %v, effective %v", tick, w, e)
		}
	}
	if w, e := d.Written.At(3840+3*960+10), d.Effective.At(3840+3*960+10); w != e {
		t.Errorf("after the override: written %v, effective %v", w, e)
	}
	// The marks are as printed.
	if got := score.FormatVoice(s.Lines[0].Staves[0].Voices[0]); got != "| qC4(p) qC4 qC4 qC4\n| qC4 qC4(sfz) qC4 qC4\n| wC4(f)\n|\n" {
		t.Errorf("voice changed: %q", got)
	}
	// The written lanes for the editor: no repeat, written ticks.
	lanes, err := WrittenDynamics(s)
	if err != nil || len(lanes) != 1 {
		t.Fatal(err)
	}
	if e := lanes[0].Effective.At(3840 + 100); !near32(e, 0.2) || !near32(lanes[0].Effective.At(7680+100), lv("f")) {
		t.Errorf("written lane: %+v", lanes[0].Effective.Points)
	}
}

// Every voice plays its own rhythm — tuplets, ties, grace notes — and
// reads its line's dynamics.
func TestFourVoicesAreIndependent(t *testing.T) {
	s := one(t, "bars: 2",
		"| qC5(p) qD5 hE5 | wF5 |",
		"| 3{qA4 qB4 qC5} hD5~ | qD5 h.r |",
		"| /sF4 hG4 hA4 | R |",
		"| 5{sC4 sD4 sE4 sF4 sG4} qr hr | 5:4{qC4 qD4 qE4 qF4 qG4} |")
	p := perform(t, s)
	want := map[int][]int64{
		0: {0, 960, 1920, 3840},
		1: {0, 640, 1280, 1920}, // the tied D5 lasts into bar 2
		// The acciaccatura at the top of the score has nothing before
		// it to take time from: it plays on the beat, a 32nd long.
		2: {0, 120, 1920},
		3: {0, 192, 384, 576, 768, 3840, 4608, 5376, 6144, 6912},
	}
	got := map[int][]int64{}
	for _, n := range p.Notes {
		got[n.Voice] = append(got[n.Voice], n.Tick)
		if !near32(n.Velocity, lv("p")) {
			t.Errorf("voice %d note at %d: velocity %v, want p (voice 1's mark)", n.Voice, n.Tick, n.Velocity)
		}
	}
	for v, w := range want {
		g := got[v]
		// Notes are in onset order; compare as sets of ticks.
		seen := map[int64]int{}
		for _, x := range g {
			seen[x]++
		}
		for _, x := range w {
			if seen[x] == 0 {
				t.Errorf("voice %d: no note at %d (got %v)", v, x, g)
			}
			seen[x]--
		}
	}
	for _, n := range p.Notes {
		if n.Voice == 1 && n.MIDI == 74 && n.Ticks != 1920+960 {
			t.Errorf("voice 2's tie: %+v", n)
		}
	}
}
