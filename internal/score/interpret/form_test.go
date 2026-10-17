package interpret

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"foundry/internal/score"
)

// barOrder is the played bars as "bar.pass" words.
func barOrder(p *Performance) string {
	var w []string
	for _, b := range p.Bars {
		w = append(w, fmt.Sprintf("%d.%d", b.Bar, b.Pass))
	}
	return strings.Join(w, " ")
}

// wholes is a voice of n bars of whole notes, C4 upward, so a note's pitch
// says which bar it came from.
func wholes(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "| w%s ", score.FromDiatonic(28+i))
	}
	return b.String() + "|"
}

func TestEndingsPlayOnTheirPasses(t *testing.T) {
	for _, c := range []struct {
		name, header, want string
	}{
		{"first and second ending", "bars: 6\nrepeats: [{start: 1, end: 4}]\nvoltas: [{start: 3, end: 4, passes: [1]}, {start: 5, end: 5, passes: [2]}]",
			"1.1 2.1 3.1 4.1 1.2 2.2 5.1 6.1"},
		{"three passes, 1.2. and 3.", "bars: 5\nrepeats: [{start: 1, end: 3, times: 3}]\nvoltas: [{start: 3, end: 3, passes: [1, 2]}, {start: 4, end: 4, passes: [3]}]",
			"1.1 2.1 3.1 1.2 2.2 3.2 1.3 2.3 4.1 5.1"},
		{"a second ending inside the repeat", "bars: 5\nrepeats: [{start: 2, end: 4}]\nvoltas: [{start: 3, end: 3, passes: [1]}, {start: 4, end: 4, passes: [2]}]",
			// Pass 1 skips bar 4 and never reaches the repeat's end: it
			// carries on, as written (a second ending is after the first).
			"1.1 2.1 3.1 5.1"},
	} {
		s := one(t, c.header, wholes(barsOf(c.header)))
		if got := barOrder(perform(t, s)); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// barsOf reads a header's "bars: n".
func barsOf(header string) int {
	var n int
	fmt.Sscanf(header, "bars: %d", &n)
	return n
}

func TestJumpsFollowTheNavigation(t *testing.T) {
	for _, c := range []struct {
		name, header, want string
	}{
		{"D.C. al Fine", "bars: 4\njumps: [{mark: fine, bar: 2}, {mark: dc, bar: 4, al: fine}]",
			"1.1 2.1 3.1 4.1 1.2 2.2"},
		{"plain D.C. stops at a Fine", "bars: 4\njumps: [{mark: fine, bar: 2}, {mark: dc, bar: 4}]",
			"1.1 2.1 3.1 4.1 1.2 2.2"},
		{"D.C. with no Fine plays to the end", "bars: 3\njumps: [{mark: dc, bar: 2}]",
			"1.1 2.1 1.2 2.2 3.1"},
		{"D.S. al Coda", "bars: 6\njumps: [{mark: segno, bar: 2}, {mark: to_coda, bar: 3}, {mark: ds, bar: 4, al: coda}, {mark: coda, bar: 5}]",
			"1.1 2.1 3.1 4.1 2.2 3.2 5.1 6.1"},
		{"a To Coda is passed by the first time and by al Fine", "bars: 6\njumps: [{mark: to_coda, bar: 1}, {mark: fine, bar: 2}, {mark: coda, bar: 5}, {mark: dc, bar: 3, al: fine}]",
			"1.1 2.1 3.1 1.2 2.2"},
		{"repeats are played once after a D.C., last ending", "bars: 4\nrepeats: [{start: 1, end: 2}]\nvoltas: [{start: 2, end: 2, passes: [1]}, {start: 3, end: 3, passes: [2]}]\njumps: [{mark: dc, bar: 4}]",
			"1.1 2.1 1.2 3.1 4.1 1.3 3.2 4.2"},
		{"con rip. takes them again", "bars: 3\nrepeats: [{start: 1, end: 1}]\njumps: [{mark: dc, bar: 3, repeats: true}]",
			"1.1 1.2 2.1 3.1 1.3 1.4 2.2 3.2"},
		{"a D.S. into the middle of a repeat is its last pass", "bars: 5\nrepeats: [{start: 1, end: 3}]\nvoltas: [{start: 3, end: 3, passes: [1]}, {start: 4, end: 4, passes: [2]}]\njumps: [{mark: segno, bar: 2}, {mark: ds, bar: 5}]",
			"1.1 2.1 3.1 1.2 2.2 4.1 5.1 2.3 4.2 5.2"},
	} {
		s := one(t, c.header, wholes(barsOf(c.header)))
		if issues := s.Validate(nil); score.HasErrors(issues) {
			t.Fatalf("%s: %v", c.name, issues)
		}
		p := perform(t, s)
		if got := barOrder(p); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
		// Every played bar's note plays, at its bar's tick.
		if len(p.Notes) != len(p.Bars) {
			t.Errorf("%s: %d notes for %d bars", c.name, len(p.Notes), len(p.Bars))
		}
		for i, b := range p.Bars {
			if i < len(p.Notes) && (p.Notes[i].Tick != b.Tick || p.Notes[i].MIDI != score.FromDiatonic(27+b.Bar).MIDI()) {
				t.Errorf("%s: note %d %+v in bar %+v", c.name, i, p.Notes[i], b)
			}
		}
	}
}

// Ignoring repeats plays every bar once, in order, both from the score's
// own option and from Options.NoRepeats.
func TestIgnoreRepeats(t *testing.T) {
	h := "bars: 5\nrepeats: [{start: 1, end: 2}]\nvoltas: [{start: 2, end: 2, passes: [1]}, {start: 3, end: 3, passes: [2]}]\njumps: [{mark: fine, bar: 3}, {mark: dc, bar: 4}]"
	s := one(t, h+"\nplayback: {ignore_repeats: true}", wholes(5))
	if got := barOrder(perform(t, s)); got != "1.1 2.1 3.1 4.1 5.1" {
		t.Errorf("ignore_repeats: %s", got)
	}
	s = one(t, h, wholes(5))
	p, err := Interpret(s, Options{NoRepeats: true})
	if err != nil || barOrder(p) != "1.1 2.1 3.1 4.1 5.1" {
		t.Errorf("NoRepeats: %s %v", barOrder(p), err)
	}
	if got := barOrder(perform(t, s)); got != "1.1 2.1 1.2 3.1 4.1 1.3 3.2" {
		t.Errorf("followed: %s", got)
	}
}

func TestFormSurvivesTheFile(t *testing.T) {
	s := one(t, "bars: 6\nrepeats: [{start: 1, end: 4}]\nvoltas: [{start: 3, end: 4, passes: [1]}, {start: 5, end: 5, passes: [2]}]\n"+
		"jumps: [{mark: segno, bar: 2}, {mark: ds, bar: 6, al: coda, repeats: true}, {mark: to_coda, bar: 2}, {mark: coda, bar: 6}]\nconcert: true\nplayback: {graces: before}", wholes(6))
	b, err := score.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := score.Parse(b)
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	again, _ := score.Marshal(back)
	if string(again) != string(b) || !back.Concert || back.Playback.Graces != score.GraceBefore || len(back.Jumps) != 4 ||
		back.Jumps[1].Al != score.JumpCoda || !back.Jumps[1].Repeats || len(back.Voltas) != 2 || back.Voltas[1].Passes[0] != 2 {
		t.Errorf("round trip:\n%s\nvs\n%s", b, again)
	}
	if back.Jumps[1].Text() != "D.S. al Coda" || back.Voltas[0].Label() != "1." {
		t.Errorf("labels %q %q", back.Jumps[1].Text(), back.Voltas[0].Label())
	}
}

// qpmAt reads the performance's tempo at a written bar and beat (quarters
// from the bar's first play).
func qpmAt(p *Performance, bar int, quarters float64) float64 {
	return p.Tempo.QPM(float64(p.BarTick(bar)) + quarters*score.TicksPerQuarter)
}

func TestTempoText(t *testing.T) {
	near1 := func(a, b float64) bool { return math.Abs(a-b) < 1e-6 }
	// rit. from 120 over bars 2-3, a tempo at 4: the ramp reaches 90
	// (3/4) at bar 4 and steps back.
	s := one(t, "bars: 4\ntempo: [{bar: 1, bpm: 120}, {bar: 2, text: rit.}, {bar: 4, text: a tempo}]", wholes(4))
	p := perform(t, s)
	if q := qpmAt(p, 2, 0); !near1(q, 120) {
		t.Errorf("rit. starts at %v", q)
	}
	if q := qpmAt(p, 3, 0); !near1(q, 105) {
		t.Errorf("half way through the rit.: %v, want 105", q)
	}
	if q := qpmAt(p, 3, 3.999999); math.Abs(q-90) > 0.01 {
		t.Errorf("end of the rit.: %v, want 90", q)
	}
	if q := qpmAt(p, 4, 0); !near1(q, 120) {
		t.Errorf("a tempo: %v", q)
	}
	// The rit. makes bars 2 and 3 longer than at 120 and shorter than at 90.
	d := p.Notes[1].End - p.Notes[1].Start + p.Notes[2].End - p.Notes[2].Start
	if d <= 4 || d >= 16.0/3 {
		t.Errorf("two bars of rit. last %v s", d)
	}

	for _, c := range []struct {
		name, tempo string
		bar         int
		quarters    float64
		want        float64
	}{
		// accel. into the next mark's number.
		{"accel. to a number", "[{bar: 1, bpm: 60}, {bar: 2, text: accel.}, {bar: 3, bpm: 90}]", 2, 2, 75},
		// A rit. with its own end holds what it reached until the next mark.
		{"rit. with an end", "[{bar: 1, bpm: 100}, {bar: 2, text: rit., to: \"2:3\"}, {bar: 4, text: a tempo}]", 3, 0, 75},
		{"poco rit.", "[{bar: 1, bpm: 100}, {bar: 2, text: poco rit., to: \"3\"}]", 3, 0, 87.5},
		{"rit. to its own number", "[{bar: 1, bpm: 100}, {bar: 2, bpm: 50, text: rit., to: \"3\"}]", 3, 1, 50},
		{"meno mosso", "[{bar: 1, bpm: 100}, {bar: 2, text: meno mosso}]", 3, 0, 85},
		{"più mosso", "[{bar: 1, bpm: 100}, {bar: 2, text: Più mosso}]", 2, 0, 115},
		{"a tempo after meno mosso", "[{bar: 1, bpm: 100}, {bar: 2, text: meno mosso}, {bar: 3, text: a tempo}]", 3, 0, 100},
		{"tempo primo", "[{bar: 1, bpm: 100, text: Allegro}, {bar: 2, bpm: 60, text: Adagio}, {bar: 3, text: Tempo I}]", 3, 0, 100},
		{"the older ramp spelling", "[{bar: 1, bpm: 100, ramp: true}, {bar: 3, bpm: 60}]", 2, 0, 80},
		{"a rit. to the very end", "[{bar: 1, bpm: 100}, {bar: 3, text: rit.}]", 3, 2, 93.75},
	} {
		s := one(t, "bars: 4\ntempo: "+c.tempo, wholes(4))
		p := perform(t, s)
		if q := qpmAt(p, c.bar, c.quarters); math.Abs(q-c.want) > 1e-6 {
			t.Errorf("%s: bar %d + %v quarters at %v, want %v", c.name, c.bar, c.quarters, q, c.want)
		}
	}
	for text, want := range map[string]score.TempoChange{
		"rit.": score.TempoRit, "rall.": score.TempoRit, "molto rit.": score.TempoRit, "allargando": score.TempoRit,
		"accel.": score.TempoAccel, "stringendo": score.TempoAccel, "a tempo": score.TempoATempo, "Tempo I": score.TempoPrimo,
		"tempo primo": score.TempoPrimo, "Tempo di valse": score.TempoSet, "Meno mosso": score.TempoMeno, "più mosso": score.TempoPiu,
		"Andante": score.TempoSet,
	} {
		if got := (score.TempoMark{Text: text}).Change(); got != want {
			t.Errorf("%q is change %d, want %d", text, got, want)
		}
	}
}

// A fermata holds its event — and everything sounding with it — its
// scale times longer: the tempo under it is divided.
func TestFermataHolds(t *testing.T) {
	s := one(t, "bars: 2\ntempo: [{bar: 1, bpm: 60}]",
		"| qC4 qD4 hE4(ferm) | qF4(ferm=long) qG4 hA4 |",
		"| wC3 | wD3 |")
	p := perform(t, s)
	byPitch := map[string]Note{}
	for _, n := range p.Notes {
		byPitch[score.SpellMIDI(n.MIDI, 0, 1).String()] = n
	}
	dur := func(n string) float64 { return byPitch[n].End - byPitch[n].Start }
	for n, want := range map[string]float64{"C4": 1, "D4": 1, "E4": 4, "F4": 3, "G4": 1, "A4": 2, "C3": 6, "D3": 6} {
		if got := dur(n); math.Abs(got-want) > 1e-9 {
			t.Errorf("%s lasts %v s, want %v", n, got, want)
		}
	}
	if math.Abs(p.Seconds-12) > 1e-9 || p.Length != 2*3840 {
		t.Errorf("the piece lasts %v s, %d ticks", p.Seconds, p.Length)
	}
	if q := p.Tempo.QPM(float64(1920 + 10)); q != 30 {
		t.Errorf("under the fermata the tempo is %v", q)
	}
}
