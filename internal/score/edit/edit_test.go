package edit

import (
	"math/rand"
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/engrave"
)

// one is a score of one treble line whose voices are the given texts, in a
// meter and key.
func one(t *testing.T, meter string, fifths int, voices ...string) *score.Score {
	t.Helper()
	s := score.New("t")
	m, err := score.ParseMeter(meter)
	if err != nil {
		t.Fatal(err)
	}
	s.Meter = []score.MeterMark{{Bar: 1, Meter: m}}
	s.Key = []score.KeyMark{{Bar: 1, Fifths: fifths, Mode: "major"}}
	l := score.NewLine("Flute", "woodwinds/flute", "treble")
	l.Staves[0].Voices = nil
	for _, text := range voices {
		v, err := score.ParseVoice(text)
		if err != nil {
			t.Fatal(err)
		}
		l.Staves[0].Voices = append(l.Staves[0].Voices, v)
	}
	if len(l.Staves[0].Voices) == 0 {
		l.Staves[0].Voices = []*score.Voice{{}}
	}
	s.Lines = []*score.Line{l}
	s.Bars = s.Measures()
	return s
}

// barText is a voice's bar as the grammar prints it.
func barText(s *score.Score, voice, bar int) string {
	v := Voice(s, 0, 0, voice)
	if v == nil {
		return "<no voice>"
	}
	return score.FormatMeasure(v.Measure(bar))
}

func note(p string, d string) score.Event {
	dur, err := parseDur(d)
	if err != nil {
		panic(err)
	}
	pt, err := score.ParsePitch(p)
	if err != nil {
		panic(err)
	}
	return score.Event{Kind: score.EventChord, Dur: dur, Notes: []score.Note{{Pitch: pt}}}
}

func parseDur(d string) (score.Duration, error) {
	m, err := score.ParseMeasure(d + "r")
	if err != nil {
		return score.Duration{}, err
	}
	return m.Events[0].Dur, nil
}

// fullBars checks every bar of every voice adds up to its bar exactly (or
// is empty), and that the score validates without errors.
func fullBars(t *testing.T, s *score.Score) {
	t.Helper()
	for li, l := range s.Lines {
		for si, st := range l.Staves {
			for vi, v := range st.Voices {
				for bi, m := range v.Measures {
					if len(m.Events) == 0 {
						continue
					}
					_, filled := m.Offsets(s.BarLength(bi + 1))
					if !filled.Eq(s.BarLength(bi+1)) && !(len(m.Events) == 1 && m.Events[0].Kind == score.EventBarRest) {
						t.Fatalf("line %d staff %d voice %d bar %d holds %s, wants %s: %s",
							li, si, vi, bi+1, filled, s.BarLength(bi+1), score.FormatMeasure(m))
					}
				}
			}
		}
	}
	for _, is := range s.Validate(nil) {
		if is.Severity == score.Error {
			t.Fatalf("invalid after edit: %v", is)
		}
	}
}

func TestWriteIntoAnEmptyBar(t *testing.T) {
	s := one(t, "4/4", 0)
	loc, err := Write(s, 0, 0, 0, 1, score.Frac{}, note("C5", "q"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Event != 0 {
		t.Errorf("written at %d", loc.Event)
	}
	if got := barText(s, 0, 1); got != "qC5 qr hr" {
		t.Errorf("bar = %q", got)
	}
	// Beat 3 of an empty bar: rests before it by the beat.
	s = one(t, "4/4", 0)
	if _, err := Write(s, 0, 0, 0, 1, score.F(1, 2), note("E5", "e")); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "hr eE5 er qr" {
		t.Errorf("bar = %q", got)
	}
	fullBars(t, s)
}

func TestWriteGrowsTheScoreAndVoices(t *testing.T) {
	s := one(t, "3/4", 0, "| qC5 qD5 qE5 |")
	if _, err := Write(s, 0, 0, 1, 4, score.F(1, 4), note("G4", "h")); err != nil {
		t.Fatal(err)
	}
	if s.Bars != 4 || s.Measures() != 4 {
		t.Errorf("bars = %d / %d", s.Bars, s.Measures())
	}
	if got := barText(s, 1, 4); got != "qr hG4" {
		t.Errorf("voice 2 bar 4 = %q", got)
	}
	if _, err := Write(s, 0, 0, 4, 1, score.Frac{}, note("C5", "q")); err == nil {
		t.Error("a fifth voice was written")
	}
	fullBars(t, s)
}

func TestWriteReflowsTheBar(t *testing.T) {
	cases := []struct {
		bar, at, pitch, dur, want string
	}{
		// A note in the middle of a longer one: cut before, rests after.
		{"hC5 hD5", "1/4", "E5", "q", "qC5 qE5 hD5"},
		// Truncate the note it lands in, take what it covers, rest to the next.
		{"qC5 qD5 qE5 qF5", "1/8", "G5", "h", "eC5 hG5 er qF5"},
		// Too long for what is left of the bar: the longest value that fits.
		{"qC5 qD5 qE5 qF5", "3/4", "G5", "w", "qC5 qD5 qE5 qG5"},
		{"qC5 qD5 qE5 qF5", "1/2", "G5", "w", "qC5 qD5 hG5"},
		{"qC5 qD5 qE5 qF5", "5/8", "G5", "w", "qC5 qD5 eE5 q.G5"},
		// A rest it lands in splits into rests.
		{"hr hC5", "1/8", "A4", "e", "er eA4 qr hC5"},
		// Over a whole tuplet: it goes.
		{"qC5 3{eD5 eE5 eF5} hr", "0", "G5", "h", "hG5 hr"},
		// Across part of a tuplet: the tuplet goes whole, its time rests.
		{"qC5 3{eD5 eE5 eF5} hr", "1/8", "A5", "q", "eC5 qA5 er hr"},
		// Inside a tuplet: the note joins it.
		{"3{eC5 eD5 eE5} qr hr", "1/12", "F5", "e", "3{eC5 eF5 eE5} qr hr"},
		{"3{eC5 eD5 eE5} qr hr", "0", "F5", "q", "3{qF5 eE5} qr hr"},
		// Longer than the tuplet has room for: cut to it.
		{"3{eC5 eD5 eE5} qr hr", "1/12", "F5", "w", "3{eC5 qF5} qr hr"},
		// A chord keeps its notes.
		{"wC5+E5+G5", "1/2", "D5", "q", "hC5+E5+G5 qD5 qr"},
		// A bar rest is a bar of rests to write into.
		{"R", "1/4", "B4", "q", "qr qB4 hr"},
		// 6/8 rests by the dotted beat.
		{"q.C5 q.D5", "1/8", "E5", "e", "eC5 eE5 er q.D5"},
	}
	for _, c := range cases {
		meter := "4/4"
		if strings.Contains(c.bar, "q.") {
			meter = "6/8"
		}
		s := one(t, meter, 0, c.bar)
		at, err := score.ParseFrac(c.at)
		if err != nil {
			t.Fatal(err)
		}
		loc, err := Write(s, 0, 0, 0, 1, at, note(c.pitch, c.dur))
		if err != nil {
			t.Errorf("%s at %s: %v", c.bar, c.at, err)
			continue
		}
		if got := barText(s, 0, 1); got != c.want {
			t.Errorf("%q + %s%s at %s = %q, want %q", c.bar, c.dur, c.pitch, c.at, got, c.want)
		}
		e := Event(s, loc)
		if e == nil || e.Kind != score.EventChord || e.Notes[0].Pitch.String() != c.pitch {
			t.Errorf("%q: the written event is not at %+v", c.bar, loc)
		}
		if on, _ := Onset(s, loc); !on.Eq(at) {
			t.Errorf("%q: written at %s, want %s", c.bar, on, at)
		}
		fullBars(t, s)
	}
}

func TestWriteRefusesWhatIsNotTime(t *testing.T) {
	s := one(t, "4/4", 0, "wC5")
	if _, err := Write(s, 0, 0, 0, 1, score.Whole(1), note("C5", "q")); err == nil {
		t.Error("written at the barline")
	}
	g := note("D5", "e")
	g.Grace = score.Acciaccatura
	if _, err := Write(s, 0, 0, 0, 1, score.Frac{}, g); err == nil {
		t.Error("a grace note took time")
	}
	if _, err := Write(s, 0, 1, 0, 1, score.Frac{}, note("C5", "q")); err == nil {
		t.Error("written to a staff that does not exist")
	}
}

func TestSetDurationAndDots(t *testing.T) {
	s := one(t, "4/4", 0, "qC5 qD5 qE5 qF5")
	loc := Loc{Bar: 1, Event: 1}
	l2, err := SetDuration(s, loc, score.Duration{Base: score.Half})
	if err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "qC5 hD5 qF5" || l2.Event != 1 {
		t.Errorf("lengthened: %q at %d", got, l2.Event)
	}
	if _, err := SetDuration(s, l2, score.Duration{Base: score.Eighth}); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "qC5 eD5 er qr qF5" {
		t.Errorf("shortened: %q", got)
	}
	// Dots cycle one, two, none.
	s = one(t, "4/4", 0, "qC5 qD5 qE5 qF5")
	loc = Loc{Bar: 1, Event: 0}
	for _, want := range []string{"q.C5 er qE5 qF5", "q..C5 sr qE5 qF5", "qC5 e.r sr qE5 qF5"} {
		var err error
		loc, err = ToggleDots(s, loc)
		if err != nil {
			t.Fatal(err)
		}
		if got := barText(s, 0, 1); got != want {
			t.Errorf("dots: %q, want %q", got, want)
		}
	}
	fullBars(t, s)
	// A grace note just takes the value.
	s = one(t, "4/4", 0, "/eD5 wC5")
	if _, err := SetDuration(s, Loc{Bar: 1, Event: 0}, score.Duration{Base: score.D16}); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "/sD5 wC5" {
		t.Errorf("grace: %q", got)
	}
}

func TestRestsTiesAndDelete(t *testing.T) {
	s := one(t, "4/4", 0, "qC5+E5 qD5 /eF5 hE5", "hr hG4")
	if err := MakeRest(s, Loc{Bar: 1, Event: 1}); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "qC5+E5 qr /eF5 hE5" {
		t.Errorf("rest: %q", got)
	}
	// One note of a chord.
	if err := Delete(s, Loc{Bar: 1, Event: 0}, 1); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "qC5 qr /eF5 hE5" {
		t.Errorf("chord note deleted: %q", got)
	}
	// A grace note comes out.
	if err := Delete(s, Loc{Bar: 1, Event: 2}, -1); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "qC5 qr hE5" {
		t.Errorf("grace deleted: %q", got)
	}
	// A rest stays a rest in voice 1, and prints nothing in voice 2.
	if err := Delete(s, Loc{Bar: 1, Event: 1}, -1); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "qC5 qr hE5" {
		t.Errorf("voice 1 rest deleted: %q", got)
	}
	if err := Delete(s, Loc{Voice: 1, Bar: 1, Event: 0}, -1); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 1, 1); got != "h_ hG4" {
		t.Errorf("voice 2 rest deleted: %q", got)
	}
	// Ties: every note, then one.
	s = one(t, "4/4", 0, "hC5+E5 hC5+E5")
	l := Loc{Bar: 1, Event: 0}
	if err := ToggleTie(s, l, -1); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "hC5~+E5~ hC5+E5" {
		t.Errorf("tied: %q", got)
	}
	if err := ToggleTie(s, l, 1); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "hC5~+E5 hC5+E5" {
		t.Errorf("one untied: %q", got)
	}
	if err := ToggleTie(s, l, -1); err != nil {
		t.Fatal(err)
	}
	if err := ToggleTie(s, l, -1); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "hC5+E5 hC5+E5" {
		t.Errorf("untied: %q", got)
	}
	if err := ToggleTie(s, Loc{Voice: 0, Bar: 1, Event: 9}, -1); err != ErrNoEvent {
		t.Errorf("tie on nothing: %v", err)
	}
}

func TestTuplets(t *testing.T) {
	cases := []struct {
		bar   string
		event int
		n     int
		want  string
	}{
		{"qC5 qD5 hE5", 0, 3, "3{qC5 qr qr} hE5"},
		{"eC5 eD5 eE5 eF5 hG5", 0, 3, "3{eC5 er er} eE5 eF5 hG5"},
		{"eC5 eD5 eE5 eF5 hG5", 1, 5, "eC5 5{eD5 er er er er} er qr"},
		{"qC5 qD5 hE5", 1, 2, "qC5 2{qD5 qr}"},
		// Nested in a triplet.
		{"3{qC5 qr qr} hr", 0, 3, "3{3{qC5 qr qr} qr} hr"},
	}
	for _, c := range cases {
		s := one(t, "4/4", 0, c.bar)
		loc, err := MakeTuplet(s, Loc{Bar: 1, Event: c.event}, c.n)
		if err != nil {
			t.Errorf("%q: %v", c.bar, err)
			continue
		}
		if got := barText(s, 0, 1); got != c.want {
			t.Errorf("%q %d-tuplet on %d = %q, want %q", c.bar, c.n, c.event, got, c.want)
		}
		if e := Event(s, loc); e == nil || e.Tuplet == 0 {
			t.Errorf("%q: the tuplet's first note is not at %+v", c.bar, loc)
		}
		fullBars(t, s)
	}
	// No room.
	s := one(t, "4/4", 0, "qC5 qD5 qE5 hF5")
	s = one(t, "4/4", 0, "qC5 qD5 qE5 qF5")
	if _, err := MakeTuplet(s, Loc{Bar: 1, Event: 3}, 3); err == nil {
		t.Error("a triplet of quarters fitted in the last beat")
	}
	if _, err := MakeTuplet(s, Loc{Bar: 1, Event: 0}, 1); err == nil {
		t.Error("a 1-tuplet")
	}
}

func TestFitMeasureAndMeterChanges(t *testing.T) {
	cases := []struct {
		bar, meter, want string
		cut              bool
	}{
		{"qC5 qD5 qE5 qF5", "3/4", "qC5 qD5 qE5", true},
		{"qC5 qD5 qE5 qF5", "5/4", "qC5 qD5 qE5 qF5 qr", false},
		{"hC5 hD5", "3/4", "hC5 qD5", true},
		{"hC5 3{qD5 qE5 qF5}", "3/4", "hC5 qr", true},
		{"R", "3/4", "R", false},
		{"", "3/4", "", false},
	}
	for _, c := range cases {
		s := one(t, "4/4", 0, "| "+c.bar+" |")
		m, _ := score.ParseMeter(c.meter)
		from, to, cut, err := SetMeter(s, 1, m)
		if err != nil {
			t.Fatal(err)
		}
		if from != 1 || to != 1 || cut != c.cut {
			t.Errorf("%q to %s: bars %d-%d cut %v", c.bar, c.meter, from, to, cut)
		}
		if got := barText(s, 0, 1); got != c.want {
			t.Errorf("%q to %s = %q, want %q", c.bar, c.meter, got, c.want)
		}
		fullBars(t, s)
	}
	// A change mid-score governs only up to the next time signature, and a
	// redundant one leaves no mark.
	s := one(t, "4/4", 0, "| wC5 | wD5 | wE5 |")
	s.Meter = append(s.Meter, score.MeterMark{Bar: 3, Meter: score.FourFour()})
	threeFour, _ := score.ParseMeter("3/4")
	if _, to, _, err := SetMeter(s, 2, threeFour); err != nil || to != 2 {
		t.Fatalf("to %d, %v", to, err)
	}
	if got := barText(s, 0, 2) + " | " + barText(s, 0, 3); got != "h.D5 | wE5" {
		t.Errorf("bars 2-3 = %q", got)
	}
	if _, _, _, err := SetMeter(s, 2, score.FourFour()); err != nil {
		t.Fatal(err)
	}
	if len(s.Meter) != 2 {
		t.Errorf("meter marks = %v", s.Meter)
	}
}

func TestKeysAndClefs(t *testing.T) {
	s := one(t, "4/4", 0, "| wC5 | wD5 | wE5 |")
	if err := SetKey(s, 2, 2, "major"); err != nil {
		t.Fatal(err)
	}
	if s.KeyAt(2).Fifths != 2 || s.KeyAt(1).Fifths != 0 || len(s.Key) != 2 {
		t.Errorf("keys = %v", s.Key)
	}
	// Back to C at bar 2: the mark is redundant and goes.
	if err := SetKey(s, 2, 0, "major"); err != nil {
		t.Fatal(err)
	}
	if len(s.Key) != 1 {
		t.Errorf("keys = %v", s.Key)
	}
	if err := SetKey(s, 1, -3, "minor"); err != nil || s.KeyAt(3).Fifths != -3 {
		t.Errorf("bar 1 key: %v %v", s.Key, err)
	}
	if err := SetKey(s, 1, 9, ""); err == nil {
		t.Error("nine sharps")
	}
	// The notes do not change with the key.
	if got := barText(s, 0, 2); got != "wD5" {
		t.Errorf("bar 2 = %q", got)
	}
	if err := SetClef(s, 0, 0, "bass"); err != nil || s.Lines[0].Staves[0].Clef != "bass" {
		t.Errorf("clef: %v", err)
	}
	if err := SetClef(s, 0, 0, "grand"); err == nil {
		t.Error("a staff took the grand clef")
	}
}

func TestPitchOperations(t *testing.T) {
	P := func(s string) score.Pitch {
		p, err := score.ParsePitch(s)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name string
		got  score.Pitch
		want string
	}{
		{"third above D5 in G", IntervalFrom(P("D5"), 3, false, 1), "F#5"},
		{"sixth below D5 in Bb", IntervalFrom(P("D5"), 6, true, -2), "F4"},
		{"second below C5", IntervalFrom(P("C5"), 2, true, 0), "B4"},
		{"octave above", IntervalFrom(P("E4"), 8, false, 0), "E5"},
		{"sharp", Accidental(P("F4"), Sharp), "F#4"},
		{"sharp again", Accidental(P("F#4"), Sharp), "F##4"},
		{"flat", Accidental(P("B4"), Flat), "Bb4"},
		{"flat again", Accidental(P("Bb4"), Flat), "Bbb4"},
		{"sharp on a flat", Accidental(P("Bb4"), Sharp), "B#4"},
		{"natural", Accidental(P("C##4"), Natural), "C4"},
		{"semitone up E", Semitone(P("E4"), 1, 0), "F4"},
		{"semitone up F, sharps up", Semitone(P("F4"), 1, 0), "F#4"},
		{"semitone down F#", Semitone(P("F#4"), -1, 0), "F4"},
		{"semitone down C in F", Semitone(P("C5"), -1, -1), "B4"},
		{"semitone down B in F: the key's Bb", Semitone(P("B4"), -1, -1), "Bb4"},
		{"semitone down A in Eb: the key's Ab", Semitone(P("A4"), -1, -3), "Ab4"},
		{"semitone up across the octave", Semitone(P("B4"), 1, 0), "C5"},
		{"step up E in G", Step(P("E5"), 1, 1), "F#5"},
		{"step down C in C", Step(P("C5"), -1, 0), "B4"},
		{"octave up", Octave(P("Bb3"), 1), "Bb4"},
		{"octave down", Octave(P("C4"), -1), "C3"},
		{"respell F#", Respell(P("F#4")), "Gb4"},
		{"respell Gb", Respell(P("Gb4")), "F#4"},
		{"respell C", Respell(P("C5")), "B#4"},
		{"respell B#", Respell(P("B#4")), "C5"},
		{"respell D", Respell(P("D4")), "Ebb4"},
		{"respell Ebb", Respell(P("Ebb4")), "C##4"},
		{"respell C##", Respell(P("C##4")), "D4"},
		{"letter C after B4", NearestLetter(0, ptr(P("B4")), 0), "C5"},
		{"letter A after D5", NearestLetter(5, ptr(P("D5")), 0), "A4"},
		{"letter F in G", NearestLetter(3, ptr(P("E5")), 1), "F#5"},
		{"letter B in F", NearestLetter(6, ptr(P("A4")), -1), "Bb4"},
		{"letter with nothing before", NearestLetter(6, nil, 0), "B4"},
	}
	for _, c := range cases {
		if c.got.String() != c.want {
			t.Errorf("%s = %s, want %s", c.name, c.got, c.want)
		}
	}
	for _, p := range []string{"C4", "F#4", "Bbb2", "E##6", "Cb4", "B#3"} {
		if q := Respell(P(p)); q.MIDI() != P(p).MIDI() {
			t.Errorf("respell %s = %s sounds different", p, q)
		}
	}
}

func ptr(p score.Pitch) *score.Pitch { return &p }

func TestChordEdits(t *testing.T) {
	s := one(t, "4/4", 1, "wD5")
	l := Loc{Bar: 1, Event: 0}
	i, p, err := AddInterval(s, l, -1, 3, false, 1)
	if err != nil || p.String() != "F#5" || i != 1 {
		t.Fatalf("third above: %d %s %v", i, p, err)
	}
	if _, _, err := AddInterval(s, l, -1, 3, false, 1); err != nil {
		t.Fatalf("third above F#: %v", err)
	}
	if got := barText(s, 0, 1); got != "wD5+F#5+A5" {
		t.Errorf("chord = %q", got)
	}
	// Below the bottom note.
	if _, p, err := AddInterval(s, l, -1, 4, true, 1); err != nil || p.String() != "A4" {
		t.Errorf("fourth below: %s %v", p, err)
	}
	// A letter the chord has is refused: a fourth above A4 is the D5 there.
	if _, _, err := AddInterval(s, l, 0, 4, false, 1); err == nil {
		t.Error("doubled D5")
	}
	// Moving a note keeps the chord in order and follows the note.
	k, err := Transform(s, l, 0, func(p score.Pitch) score.Pitch { return Octave(p, 1) })
	if err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "wD5+F#5+A5" || k != 2 {
		t.Errorf("A4 up an octave = %q at %d (the A5 already there merges)", got, k)
	}
	k, err = Transform(s, l, 0, func(p score.Pitch) score.Pitch { return Accidental(p, Flat) })
	if err != nil || barText(s, 0, 1) != "wDb5+F#5+A5" || k != 0 {
		t.Errorf("flat: %q %d %v", barText(s, 0, 1), k, err)
	}
	// Every note at once.
	if _, err := Transform(s, l, -1, func(p score.Pitch) score.Pitch { return Octave(p, -1) }); err != nil {
		t.Fatal(err)
	}
	if got := barText(s, 0, 1); got != "wDb4+F#4+A4" {
		t.Errorf("down an octave = %q", got)
	}
	// Out of range is refused whole.
	s2 := one(t, "4/4", 0, "wG9")
	if _, err := Transform(s2, l, -1, func(p score.Pitch) score.Pitch { return Octave(p, 1) }); err == nil {
		t.Error("G10")
	}
	if got := barText(s2, 0, 1); got != "wG9" {
		t.Errorf("refused change changed: %q", got)
	}
	if _, err := AddPitch(s2, l, score.Pitch{Step: 4, Octave: 9}); err == nil {
		t.Error("G9 twice")
	}
	// A rest is not a chord.
	s3 := one(t, "4/4", 0, "wr")
	if _, _, err := AddInterval(s3, l, -1, 3, false, 0); err == nil {
		t.Error("an interval on a rest")
	}
}

func TestCopyPasteBars(t *testing.T) {
	s := one(t, "4/4", 0, "| qC5 qD5 qE5 qF5 | wG5 |", "| wC4 | wD4 |")
	c, err := CopyBars(s, 0, 0, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if c.Bars != 2 || len(c.Lines) != 1 || len(c.Lines[0][0]) != 2 {
		t.Fatalf("clip %+v", c)
	}
	// Paste past the end: the score grows.
	if _, _, err := PasteBars(s, c, 0, 3); err != nil {
		t.Fatal(err)
	}
	if s.Measures() != 4 || barText(s, 0, 3) != "qC5 qD5 qE5 qF5" || barText(s, 1, 4) != "wD4" {
		t.Errorf("pasted: %d bars, %q, %q", s.Measures(), barText(s, 0, 3), barText(s, 1, 4))
	}
	// A clip's copy is its own: editing the source later does not change it.
	Voice(s, 0, 0, 0).Measures[0] = score.Measure{}
	if score.FormatMeasure(c.Lines[0][0][0][0]) != "qC5 qD5 qE5 qF5" {
		t.Error("the clip shares the score's music")
	}
	// Into a bar of another meter: fitted.
	s.Meter = append(s.Meter, score.MeterMark{Bar: 5, Meter: score.Meter{Groups: []int{3}, Den: 4}})
	_, cut, err := PasteBars(s, c, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !cut || barText(s, 0, 5) != "qC5 qD5 qE5" || barText(s, 0, 6) != "h.G5" {
		t.Errorf("fitted: cut %v %q %q", cut, barText(s, 0, 5), barText(s, 0, 6))
	}
	// A voice the clip does not have is cleared where it lands.
	one1, _ := CopyBars(one(t, "4/4", 0, "wA4"), 0, 0, 1, 1)
	if _, _, err := PasteBars(s, one1, 0, 3); err != nil {
		t.Fatal(err)
	}
	if barText(s, 0, 3) != "wA4" || barText(s, 1, 3) != "" {
		t.Errorf("voice 2 kept: %q", barText(s, 1, 3))
	}
	fullBars(t, s)
	ClearBars(s, 0, 0, 1, 2)
	if barText(s, 0, 2) != "" || barText(s, 1, 2) != "" {
		t.Error("not cleared")
	}
	if _, err := CopyBars(s, 0, 3, 1, 1); err == nil {
		t.Error("copied lines that do not exist")
	}
	if _, _, err := PasteBars(s, nil, 0, 1); err == nil {
		t.Error("pasted nothing")
	}
}

// Random edits keep every bar exactly full, the score valid, and the voice
// text round-tripping.
func TestRandomEditsKeepBarsFull(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	meters := []string{"4/4", "3/4", "6/8", "5/8", "3+2+2/8", "2/2", "7/16"}
	durs := []score.DurBase{score.Half, score.Quarter, score.Eighth, score.D16, score.D32, score.WholeNote}
	for round := 0; round < 300; round++ {
		s := one(t, meters[rng.Intn(len(meters))], rng.Intn(7)-3)
		for step := 0; step < 40; step++ {
			bar := 1 + rng.Intn(3)
			voice := rng.Intn(2)
			v := Voice(s, 0, 0, voice)
			var events int
			if v != nil {
				events = len(v.Measure(bar).Events)
			}
			loc := Loc{Voice: voice, Bar: bar, Event: rng.Intn(max(events, 1))}
			var err error
			switch op := rng.Intn(8); {
			case op < 3 || events == 0:
				bl := s.BarLength(bar)
				grid := score.F(1, 16)
				at := grid.MulInt(int64(rng.Intn(int(bl.Div(grid).Floor()))))
				e := note("C5", "q")
				e.Dur = score.Duration{Base: durs[rng.Intn(len(durs))], Dots: uint8(rng.Intn(2))}
				e.Notes[0].Pitch = score.InKey(28+rng.Intn(14), 0)
				if rng.Intn(5) == 0 {
					e = score.Event{Kind: score.EventRest, Dur: e.Dur}
				}
				if rng.Intn(2) == 0 {
					_, err = Write(s, 0, 0, voice, bar, at, e)
				} else {
					// Note entry's write: over the barline, tied.
					_, _, err = WriteOver(s, 0, 0, voice, bar, at, e)
				}
			case op == 3:
				_, err = SetDuration(s, loc, score.Duration{Base: durs[rng.Intn(len(durs))]})
			case op == 4:
				_, err = ToggleDots(s, loc)
			case op == 5:
				_, err = MakeTuplet(s, loc, 2+rng.Intn(6))
			case op == 6:
				err = Delete(s, loc, rng.Intn(2)-1)
			default:
				_, _, err = AddInterval(s, loc, -1, 2+rng.Intn(8), rng.Intn(2) == 0, 0)
			}
			_ = err // refusals are fine; what is written must stay sound
			fullBars(t, s)
		}
		// Whatever the edits made, the engraver lays it out.
		if l := engrave.Engrave(s, engrave.Options{}); len(l.Items) == 0 {
			t.Fatalf("round %d: nothing engraved", round)
		}
		for _, v := range s.Lines[0].Staves[0].Voices {
			text := score.FormatVoice(v)
			back, err := score.ParseVoice(text)
			if err != nil {
				t.Fatalf("round %d: %v\n%s", round, err, text)
			}
			if score.FormatVoice(back) != text {
				t.Fatalf("round %d: does not round-trip\n%s", round, text)
			}
		}
	}
}

func TestWriteOverTiesAcrossTheBarline(t *testing.T) {
	cases := []struct {
		name, meter, voice string
		bar                int
		at                 score.Frac
		e                  score.Event
		want               []string // bars 1..n
		first, last        Loc
	}{
		{"fits", "4/4", "| qC5 qD5 qE5 qF5", 1, score.F(1, 4), note("G4", "h"),
			[]string{"qC5 hG4 qF5"}, Loc{Bar: 1, Event: 1}, Loc{Bar: 1, Event: 1}},
		{"half on beat 4", "4/4", "| qC5 qD5 qE5 qF5", 1, score.F(3, 4), note("G4", "h"),
			[]string{"qC5 qD5 qE5 qG4~", "qG4 qr hr"}, Loc{Bar: 1, Event: 3}, Loc{Bar: 2, Event: 0}},
		{"over the next bar's music", "4/4", "| wC5 | qD5 qE5 qF5 qG5", 1, score.F(1, 2), note("A4", "w"),
			[]string{"hC5 hA4~", "hA4 qF5 qG5"}, Loc{Bar: 1, Event: 1}, Loc{Bar: 2, Event: 0}},
		{"5/8 left is h~e", "4/4", "| wr", 1, score.F(3, 8), note("C5", "w"),
			[]string{"qr er hC5~ eC5~", "q.C5 er hr"}, Loc{Bar: 1, Event: 2}, Loc{Bar: 2, Event: 0}},
		{"through a whole bar", "3/4", "| h.r | h.r | h.r", 1, score.F(1, 2), note("E5", "w."),
			[]string{"hr qE5~", "h.E5~", "hE5 qr"}, Loc{Bar: 1, Event: 1}, Loc{Bar: 3, Event: 0}},
		{"a rest carries no tie", "4/4", "| qC5 qD5 qE5 qF5 | qG5 qA5 qB5 qC6", 1, score.F(3, 4),
			score.Event{Kind: score.EventRest, Dur: score.Duration{Base: score.Half}},
			[]string{"qC5 qD5 qE5 qr", "qr qA5 qB5 qC6"}, Loc{Bar: 1, Event: 3}, Loc{Bar: 2, Event: 0}},
		{"the score grows", "4/4", "| qC5 qD5 qE5 qF5", 1, score.F(3, 4), note("G4", "h"),
			[]string{"qC5 qD5 qE5 qG4~", "qG4 qr hr"}, Loc{Bar: 1, Event: 3}, Loc{Bar: 2, Event: 0}},
		{"a tuplet at the next bar's start stops it", "4/4", "| wr | 3{qC5 qD5 qE5} hr", 1, score.F(3, 4), note("G4", "h"),
			[]string{"hr qr qG4", "3{qC5 qD5 qE5} hr"}, Loc{Bar: 1, Event: 2}, Loc{Bar: 1, Event: 2}},
		{"inside a tuplet it is cut as Write cuts", "4/4", "| 3{eC5 eD5 eE5} qr hr", 1, score.F(1, 12), note("G4", "q"),
			[]string{"3{eC5 qG4} qr hr"}, Loc{Bar: 1, Event: 1}, Loc{Bar: 1, Event: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := one(t, c.meter, 0, c.voice)
			first, last, err := WriteOver(s, 0, 0, 0, c.bar, c.at, c.e)
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range c.want {
				if got := barText(s, 0, i+1); got != want {
					t.Errorf("bar %d = %q, want %q", i+1, got, want)
				}
			}
			if first.Bar != c.first.Bar || first.Event != c.first.Event || last.Bar != c.last.Bar || last.Event != c.last.Event {
				t.Errorf("first %+v last %+v, want %+v %+v", first, last, c.first, c.last)
			}
			fullBars(t, s)
		})
	}
	// A tie the note had stays on its last part.
	s := one(t, "4/4", 0, "| wr")
	n := note("C5", "h")
	n.Notes[0].Tie = true
	if _, last, err := WriteOver(s, 0, 0, 0, 1, score.F(3, 4), n); err != nil || !Event(s, last).Notes[0].Tie {
		t.Errorf("last part's tie lost (%v): %q | %q", err, barText(s, 0, 1), barText(s, 0, 2))
	}
}
