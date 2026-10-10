package notate

import (
	"math/rand"
	"strings"
	"testing"

	"foundry/internal/score"
)

// layout is n bars of a meter in a key.
func layout(t *testing.T, sig string, fifths, n int) Layout {
	t.Helper()
	m, err := score.ParseMeter(sig)
	if err != nil {
		t.Fatal(err)
	}
	s := score.New("t")
	s.Meter = []score.MeterMark{{Bar: 1, Meter: m}}
	s.Key = []score.KeyMark{{Bar: 1, Fifths: fifths, Mode: "major"}}
	s.Bars = n
	return LayoutOf(s)
}

func f(n, d int64) score.Frac { return score.F(n, d) }

// line makes a chord per (start, end, pitches…) in sixteenths.
func line(spec ...[]int) []Chord {
	var out []Chord
	for _, s := range spec {
		out = append(out, Chord{Start: f(int64(s[0]), 16), End: f(int64(s[1]), 16), MIDI: s[2:]})
	}
	return out
}

func write(t *testing.T, cs []Chord, l Layout) string {
	t.Helper()
	v, err := Write(cs, l, WriteOptions{BarRests: true})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(score.FormatVoice(v), "|", " | ")), " ")
}

func TestWriteValuesFollowTheMeter(t *testing.T) {
	cases := []struct {
		name, sig string
		chords    []Chord
		want      string
	}{
		{"dotted quarter, eighth, half", "4/4", line([]int{0, 6, 60}, []int{6, 8, 62}, []int{8, 16, 64}),
			"| q.C4 eD4 hE4 |"},
		// A quarter may cross beat 2 off the beat, never the middle.
		{"syncopation", "4/4", line([]int{0, 2, 60}, []int{2, 6, 60}, []int{6, 10, 60}, []int{10, 14, 60}, []int{14, 16, 60}),
			"| eC4 qC4 eC4~ eC4 qC4 eC4 |"},
		// Rests go beat to beat and never across the middle.
		{"rests", "4/4", line([]int{0, 4, 67}),
			"| qG4 qr hr |"},
		{"rest off the beat", "4/4", line([]int{0, 2, 67}, []int{12, 16, 67}),
			"| eG4 er qr qr qG4 |"},
		// 6/8: nothing crosses the middle beat unless it starts on a beat.
		{"compound", "6/8", line([]int{0, 2, 60}, []int{2, 8, 62}, []int{8, 12, 64}),
			"| eC4 qD4~ eD4 qE4 |"},
		{"compound beat to beat", "6/8", line([]int{0, 12, 60}),
			"| h.C4 |"},
		{"whole bar and tie over the bar line", "3/4", line([]int{0, 12, 60}, []int{12, 20, 62}),
			"| h.C4 | hD4 qr |"},
		{"tie across bars", "4/4", line([]int{8, 24, 64, 67}),
			"| hr hE4~+G4~ | hE4+G4 hr |"},
		{"chord spelled in the key", "4/4", line([]int{0, 16, 62, 66, 69}),
			"| wD4+F#4+A4 |"},
		{"empty bar is a bar rest", "2/4", line([]int{8, 16, 72}),
			"| R | hC5 |"},
	}
	for _, c := range cases {
		l := layout(t, c.sig, 2, 2)
		if c.name != "chord spelled in the key" {
			l = layout(t, c.sig, 0, 2)
		}
		if got := write(t, c.chords, l); !strings.HasPrefix(got+" ", c.want+" ") {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestWriteTuplets(t *testing.T) {
	l := layout(t, "4/4", 0, 1)
	third := func(k int64) score.Frac { return f(k, 12) }
	// Eighth triplets on beat 1.
	cs := []Chord{{Start: third(0), End: third(1), MIDI: []int{60}}, {Start: third(1), End: third(2), MIDI: []int{62}}, {Start: third(2), End: third(3), MIDI: []int{64}},
		{Start: f(1, 4), End: f(1, 2), MIDI: []int{65}}}
	if got, want := write(t, cs, l), "| 3{eC4 eD4 eE4} qF4 hr |"; got != want {
		t.Errorf("eighth triplets: %s, want %s", got, want)
	}
	// Quarter triplets across beats 1 and 2: one bracket.
	cs = []Chord{{Start: f(0, 1), End: f(1, 6), MIDI: []int{60}}, {Start: f(1, 6), End: f(1, 3), MIDI: []int{62}}, {Start: f(1, 3), End: f(1, 2), MIDI: []int{64}}}
	if got, want := write(t, cs, l), "| 3{qC4 qD4 qE4} hr |"; got != want {
		t.Errorf("quarter triplets: %s, want %s", got, want)
	}
	// A triplet group with a rest and a quintuplet.
	cs = []Chord{{Start: third(0), End: third(1), MIDI: []int{60}}, {Start: third(2), End: third(3), MIDI: []int{60}},
		{Start: f(1, 4), End: f(1, 4).Add(f(1, 20)), MIDI: []int{67}}, {Start: f(1, 4).Add(f(1, 20)), End: f(1, 2), MIDI: []int{69}}}
	if got, want := write(t, cs, l), "| 3{eC4 er eC4} 5{sG4 e.A4~ sA4} hr |"; got != want {
		t.Errorf("rest in a triplet, quintuplet: %s, want %s", got, want)
	}
	// Whatever comes out adds up and parses back.
	v, err := Write(cs, l, WriteOptions{BarRests: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, filled := v.Measures[0].Offsets(score.Whole(1)); !filled.Eq(score.Whole(1)) {
		t.Errorf("bar holds %s", filled)
	}
}

// Random rhythms on a 16th-and-triplet grid: every bar adds up exactly and
// the notes come back where they were.
func TestWriteRandomRhythmsAddUp(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, sig := range []string{"4/4", "3/4", "6/8", "5/4", "3+2+2/8", "2/2", "7/16"} {
		l := layout(t, sig, rng.Intn(13)-6, 4)
		for trial := 0; trial < 60; trial++ {
			var cs []Chord
			at := score.Frac{}
			for at.Less(l.End()) {
				step := f(int64(1+rng.Intn(6)), 16)
				if rng.Intn(4) == 0 {
					step = f(int64(1+rng.Intn(3)), 12)
				}
				end := score.MinFrac(at.Add(step), l.End())
				if rng.Intn(3) > 0 {
					cs = append(cs, Chord{Start: at, End: end, MIDI: []int{48 + rng.Intn(30)}})
				}
				at = end
			}
			v, err := Write(cs, l, WriteOptions{BarRests: true})
			if err != nil {
				t.Fatalf("%s: %v", sig, err)
			}
			got := onsets(t, v, l)
			want := map[string]bool{}
			for _, c := range cs {
				want[c.Start.String()] = true
			}
			for k := range want {
				if !got[k] {
					t.Fatalf("%s: onset %s lost\n%s", sig, k, score.FormatVoice(v))
				}
			}
			if len(got) != len(want) {
				t.Fatalf("%s: %d onsets, want %d\n%s", sig, len(got), len(want), score.FormatVoice(v))
			}
			if _, err := score.ParseVoice(score.FormatVoice(v)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// onsets are the starts of the voice's notes that are not tied into.
func onsets(t *testing.T, v *score.Voice, l Layout) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	tied := false
	for b, m := range v.Measures {
		offs, filled := m.Offsets(l.BarLen(b))
		if len(m.Events) > 0 && !filled.Eq(l.BarLen(b)) {
			t.Fatalf("bar %d holds %s of %s: %s", b+1, filled, l.BarLen(b), score.FormatMeasure(m))
		}
		for i, e := range m.Events {
			if e.Kind != score.EventChord {
				tied = false
				continue
			}
			if !tied {
				out[l.Starts[b].Add(offs[i]).String()] = true
			}
			tied = e.Notes[0].Tie
		}
	}
	return out
}

func TestQuantizeChoosesTripletsPerBeat(t *testing.T) {
	l := layout(t, "4/4", 0, 1)
	tick := func(n int64) score.Frac { return f(n, 4*480) }
	// Beat 1: humanised 16ths. Beat 2: humanised eighth triplets.
	var ns []Note
	for i, at := range []int64{4, 118, 247, 362} {
		ns = append(ns, Note{Start: tick(at), End: tick(at + 100), MIDI: 60 + i})
	}
	for i, at := range []int64{480 + 6, 640 - 9, 800 + 11} {
		ns = append(ns, Note{Start: tick(at), End: tick(at + 150), MIDI: 70 + i})
	}
	q, err := Quantize(ns, l, DefaultGrid())
	if err != nil {
		t.Fatal(err)
	}
	want := []score.Frac{f(0, 1), f(1, 16), f(2, 16), f(3, 16), f(1, 4), f(1, 4).Add(f(1, 12)), f(1, 4).Add(f(2, 12))}
	if len(q) != len(want) {
		t.Fatalf("%d notes, want %d", len(q), len(want))
	}
	for i, n := range q {
		if !n.Start.Eq(want[i]) {
			t.Errorf("note %d at %s, want %s", i, n.Start, want[i])
		}
	}
	// Without triplets allowed the second beat stays on 16ths.
	q, _ = Quantize(ns, l, Grid{Unit: f(1, 16)})
	for _, n := range q[4:] {
		if !n.Start.Div(f(1, 16)).IsInt() {
			t.Errorf("onset %s off the 16th grid", n.Start)
		}
	}
	// Ends snap too and a note never vanishes.
	short := []Note{{Start: tick(10), End: tick(20), MIDI: 60}}
	q, _ = Quantize(short, l, DefaultGrid())
	if len(q) != 1 || !q[0].End.Eq(f(1, 16)) {
		t.Errorf("short note: %+v", q)
	}
}

func TestSeparateMelodyOverHeldBass(t *testing.T) {
	var ns []Note
	// A held bass under four quarter notes, then a chord.
	ns = append(ns, Note{Start: f(0, 1), End: f(1, 1), MIDI: 43})
	for i := int64(0); i < 4; i++ {
		ns = append(ns, Note{Start: f(i, 4), End: f(i+1, 4), MIDI: 72 + int(i)})
	}
	ns = append(ns, Note{Start: f(1, 1), End: f(2, 1), MIDI: 60}, Note{Start: f(1, 1), End: f(2, 1), MIDI: 64})
	vs := Separate(ns, 4, score.Frac{})
	if len(vs) != 2 {
		t.Fatalf("%d voices, want 2", len(vs))
	}
	if len(vs[0]) != 5 || vs[0][0].MIDI[0] != 72 || len(vs[0][4].MIDI) != 2 {
		t.Errorf("top voice: %+v", vs[0])
	}
	if len(vs[1]) != 1 || vs[1][0].MIDI[0] != 43 {
		t.Errorf("bass voice: %+v", vs[1])
	}
	// One voice: the bass becomes part of the first chord and is cut.
	vs = Separate(ns, 1, score.Frac{})
	if len(vs) != 1 || len(vs[0][0].MIDI) != 2 || !vs[0][0].End.Eq(f(1, 4)) {
		t.Errorf("one voice: %+v", vs)
	}
}

func TestDetectKey(t *testing.T) {
	scale := func(root int, steps []int) []Note {
		var ns []Note
		at := score.Frac{}
		for _, s := range append(steps, 0, 7, 0) {
			ns = append(ns, Note{Start: at, End: at.Add(f(1, 4)), MIDI: root + s})
			at = at.Add(f(1, 4))
		}
		return ns
	}
	major := []int{0, 2, 4, 5, 7, 9, 11, 12, 7, 4}
	minor := []int{0, 2, 3, 5, 7, 8, 11, 12, 7, 3}
	if fi, mi := DetectKey(scale(67, major)); fi != 1 || mi {
		t.Errorf("G major: %d %v", fi, mi)
	}
	if fi, mi := DetectKey(scale(63, major)); fi != -3 || mi {
		t.Errorf("Eb major: %d %v", fi, mi)
	}
	if fi, mi := DetectKey(scale(57, minor)); fi != 0 || !mi {
		t.Errorf("A minor: %d %v", fi, mi)
	}
}

func TestFillSplitsAGap(t *testing.T) {
	var m score.Measure
	m.Events = append(m.Events, score.Event{Kind: score.EventChord, Dur: score.Duration{Base: score.Eighth}, Notes: []score.Note{{Pitch: score.Pitch{Octave: 4}}}})
	if err := Fill(&m, score.EventSpace, f(1, 8), f(7, 8), score.FourFour(), score.Whole(1)); err != nil {
		t.Fatal(err)
	}
	if got := score.FormatMeasure(m); got != "eC4 e_ q_ h_" {
		t.Errorf("fill: %s", got)
	}
	m = score.Measure{}
	if err := Fill(&m, score.EventRest, score.Frac{}, f(1, 12), score.FourFour(), score.Whole(1)); err != nil {
		t.Fatal(err)
	}
	if got := score.FormatMeasure(m); got != "3{er}" {
		t.Errorf("triplet gap: %s", got)
	}
}

func TestParseGrid(t *testing.T) {
	g, err := ParseGrid("16,3,5")
	if err != nil || !g.Unit.Eq(f(1, 16)) || len(g.Tuplets) != 2 || g.String() != "16,3,5" {
		t.Errorf("%+v %v", g, err)
	}
	if g, err := ParseGrid("8t"); err != nil || len(g.Tuplets) != 1 {
		t.Errorf("8t: %+v %v", g, err)
	}
	for _, bad := range []string{"12", "16,4", "x"} {
		if _, err := ParseGrid(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// A flick just before the beat comes back as a grace note on the note it
// leads to — not a chord tone, and not a second voice — while a run of
// short notes on a coarse grid is left alone.
func TestGraceNotesFromPlaying(t *testing.T) {
	l := layout(t, "4/4", 1, 2)
	tick := func(n int64) score.Frac { return f(n, 4*960) }
	ns := []Note{
		{Start: tick(0), End: tick(960), MIDI: 74},                 // qD5
		{Start: tick(960), End: tick(1800), MIDI: 71},              // qB4, cut short by the grace
		{Start: tick(1800), End: tick(1920), MIDI: 83},             // a 32nd flick: B5
		{Start: tick(1920), End: tick(3360), MIDI: 81},             // q.A5 on beat 3
		{Start: tick(3360), End: tick(3840), MIDI: 79},             // eG5
		{Start: tick(3840 + 2880 - 60), End: tick(6720), MIDI: 76}, // two flicks
		{Start: tick(6720 - 60), End: tick(6720), MIDI: 78},
		{Start: tick(6720), End: tick(7680), MIDI: 79}, // qG5, beat 4 of bar 2
	}
	marked := MarkGraces(ns, DefaultGrid())
	var graces []int
	for _, n := range marked {
		if n.Grace {
			graces = append(graces, n.MIDI)
		}
	}
	if len(graces) != 3 || graces[0] != 83 || graces[1] != 76 || graces[2] != 78 {
		t.Fatalf("graces %v, want [83 76 78]", graces)
	}
	q, err := Quantize(marked, l, DefaultGrid())
	if err != nil {
		t.Fatal(err)
	}
	vs := Separate(q, 4, DefaultGrid().Unit)
	if len(vs) != 1 {
		t.Fatalf("%d voices, want one line", len(vs))
	}
	var gs []Note
	for _, n := range q {
		if n.Grace {
			gs = append(gs, n)
		}
	}
	if left := AttachGraces(vs, gs); len(left) != 0 {
		t.Fatalf("graces with nowhere to go: %+v", left)
	}
	Legato(vs[0], l, f(1, 8))
	if got, want := write(t, vs[0], l), "| qD5 qB4 /eB5 q.A5 eG5 | hr qr /sE5 /sF#5 qG5 |"; got != want {
		t.Errorf("wrote %q, want %q", got, want)
	}

	// Eight 32nds on a 16th grid are a run, not seven graces.
	var run []Note
	for i := int64(0); i < 8; i++ {
		run = append(run, Note{Start: f(i, 32), End: f(i+1, 32), MIDI: 60 + int(i)})
	}
	for _, n := range MarkGraces(run, DefaultGrid()) {
		if n.Grace {
			t.Fatalf("a run of 32nds became graces")
		}
	}
	// A grace and a note starting together are a chord of a short note and
	// a long one, not a grace.
	chord := []Note{{Start: f(0, 1), End: f(1, 64), MIDI: 60}, {Start: f(0, 1), End: f(1, 4), MIDI: 64}, {Start: f(1, 64), End: f(1, 4), MIDI: 67}}
	for _, n := range MarkGraces(chord, DefaultGrid()) {
		if n.Grace {
			t.Fatalf("a chord tone became a grace: %+v", n)
		}
	}
}

// A quintuplet played on the default grid (16ths and triplets) keeps its
// five notes apart instead of merging two of them into a chord.
func TestQuantizeKeepsQuintupletsApart(t *testing.T) {
	l := layout(t, "4/4", 0, 1)
	var ns []Note
	for i := int64(0); i < 5; i++ {
		at := f(i, 20).Add(f(1, 4))
		ns = append(ns, Note{Start: at.Add(f(1, 1024)), End: at.Add(f(1, 20)), MIDI: 64 + int(i)})
	}
	ns = append(ns, Note{Start: f(0, 1), End: f(1, 4), MIDI: 60}, Note{Start: f(1, 2), End: f(1, 1), MIDI: 60})
	q, err := Quantize(ns, l, DefaultGrid())
	if err != nil {
		t.Fatal(err)
	}
	vs := Separate(q, 1, DefaultGrid().Unit)
	if got, want := write(t, vs[0], l), "| qC4 5{sE4 sF4 sF#4 sG4 sG#4} hC4 |"; got != want {
		t.Errorf("wrote %q, want %q", got, want)
	}
}

// A line that splits into two voices for a moment (two notes overlapping)
// comes back to one voice after, rather than hopping between the two by
// nearest pitch.
func TestSeparateLineComesBackTogether(t *testing.T) {
	var ns []Note
	// Quarters C5 D5, then E5 held a half while G4 enters under it on beat
	// 4, then a run of quarters that wander between the two registers.
	ns = append(ns, Note{Start: f(0, 4), End: f(1, 4), MIDI: 72}, Note{Start: f(1, 4), End: f(2, 4), MIDI: 74},
		Note{Start: f(2, 4), End: f(4, 4), MIDI: 76}, Note{Start: f(3, 4), End: f(4, 4), MIDI: 67})
	for i, m := range []int{71, 69, 67, 72, 74, 69, 71, 72} {
		ns = append(ns, Note{Start: f(int64(4+i), 4), End: f(int64(5+i), 4), MIDI: m})
	}
	vs := Separate(ns, 4, score.Frac{})
	if len(vs) != 2 {
		t.Fatalf("%d voices, want 2", len(vs))
	}
	// However it was split, the run after bar 1 stays in one voice.
	for _, v := range vs {
		n := 0
		for _, c := range v {
			if !c.Start.Less(f(1, 1)) {
				n++
			}
		}
		if n != 0 && n != 8 {
			t.Errorf("the run hops between voices: %d of its 8 notes in one", n)
		}
	}
}
