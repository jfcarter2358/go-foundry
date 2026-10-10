package score

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func mustVoice(t testing.TB, text string) *Voice {
	t.Helper()
	v, err := ParseVoice(text)
	if err != nil {
		t.Fatalf("%q: %v", text, err)
	}
	return v
}

func TestGrammarReadsEveryForm(t *testing.T) {
	v := mustVoice(t, `| q.Bb4(mp) eC5 hD5~
	| qD5 3{eEb5 eD5 eC5} hBb4+D5+F5(ferm)
	| /eA4 ^sB4 qBb4 qr h_
	| R(ferm)
	|
	| qC4! qF#4? eE4~+G4 5:3{sC4 s.D4 3{tE4 tF4 tG4}} wr |`)
	if len(v.Measures) != 6 {
		t.Fatalf("%d bars, want 6 (the empty fifth is kept, a trailing one would not be)", len(v.Measures))
	}
	b1 := v.Measures[0].Events
	if b1[0].Dur != (Duration{Base: Quarter, Dots: 1}) || b1[0].Notes[0].Pitch.String() != "Bb4" || b1[0].Marks[0] != (Mark{Name: "mp"}) {
		t.Errorf("bar 1 event 1: %+v", b1[0])
	}
	if !b1[2].Notes[0].Tie {
		t.Error("tie lost")
	}
	b2 := v.Measures[1]
	if len(b2.Tuplets) != 1 || b2.Tuplets[0] != (Tuplet{Num: 3, Den: 2}) || b2.Events[1].Tuplet != 1 || b2.Events[4].Tuplet != 0 {
		t.Errorf("bar 2 tuplets: %+v / %+v", b2.Tuplets, b2.Events)
	}
	if len(b2.Events[4].Notes) != 3 {
		t.Error("chord")
	}
	_, filled := b2.Offsets(Whole(1))
	if !filled.Eq(Whole(1)) {
		t.Errorf("bar 2 fills %v", filled)
	}
	b3 := v.Measures[2].Events
	if b3[0].Grace != Acciaccatura || b3[1].Grace != Appoggiatura || b3[3].Kind != EventRest || b3[4].Kind != EventSpace {
		t.Errorf("bar 3: %+v", b3)
	}
	if v.Measures[3].Events[0].Kind != EventBarRest || len(v.Measures[4].Events) != 0 {
		t.Error("bar rest / empty bar")
	}
	b6 := v.Measures[5]
	if b6.Events[0].Notes[0].Acc != AccForce || b6.Events[1].Notes[0].Acc != AccCautionary {
		t.Error("accidental display")
	}
	if len(b6.Tuplets) != 2 || b6.Tuplets[1].Parent != 1 || b6.Tuplets[0] != (Tuplet{Num: 5, Den: 3}) {
		t.Errorf("nested tuplets: %+v", b6.Tuplets)
	}
}

func TestGrammarStickyDuration(t *testing.T) {
	v := mustVoice(t, "| eC4 D4 E4 F4 qG4 A4 | B4 r C5 _ |")
	want := []DurBase{Eighth, Eighth, Eighth, Eighth, Quarter, Quarter}
	for i, e := range v.Measures[0].Events {
		if e.Dur.Base != want[i] {
			t.Errorf("event %d is a %v", i, e.Dur)
		}
	}
	for _, e := range v.Measures[1].Events {
		if e.Dur.Base != Quarter {
			t.Errorf("bar 2 carries the quarter on: %v", e.Dur)
		}
	}
	if FormatMeasure(v.Measures[1]) != "qB4 qr qC5 q_" {
		t.Errorf("printer: %q", FormatMeasure(v.Measures[1]))
	}
}

func TestGrammarMarks(t *testing.T) {
	v := mustVoice(t, `| qC4(.,>,_,^,') qD4(tech=pizz.,"dolce",text="a \"b\" c",p) qE4(x_1=a+b/c) qF4 |`)
	got := v.Measures[0].Events[0].Marks
	want := []Mark{{Name: "stacc"}, {Name: "acc"}, {Name: "ten"}, {Name: "marc"}, {Name: "staccmo"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("aliases: %+v", got)
	}
	got = v.Measures[0].Events[1].Marks
	want = []Mark{{Name: "tech", Value: "pizz."}, {Name: "text", Value: "dolce"}, {Name: "text", Value: `a "b" c`}, {Name: "p"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("values: %+v", got)
	}
	if s := FormatMeasure(v.Measures[0]); s != `qC4(stacc,acc,ten,marc,staccmo) qD4(tech=pizz.,"dolce","a \"b\" c",p) qE4(x_1=a+b/c) qF4` {
		t.Errorf("printed: %s", s)
	}
	// A "|" inside a string is not a barline.
	v = mustVoice(t, `| wC4("a|b") | wD4 |`)
	if len(v.Measures) != 2 || v.Measures[0].Events[0].Marks[0].Value != "a|b" {
		t.Errorf("quoted bar: %+v", v.Measures)
	}
}

func TestGrammarErrors(t *testing.T) {
	cases := map[string]string{
		"| C4 |":              "no duration",
		"| q |":               "needs a pitch",
		"| qH4 |":             "bad pitch letter",
		"| qC4 } |":           "closes no tuplet",
		"| 3{qC4 qD4 |":       "not closed",
		"| 3{} |":             "empty tuplet",
		"| q....C4 |":         "three dots",
		"| /qr |":             "grace",
		"| /R |":              "bar rest",
		"| qC4(p |":           "parentheses",
		"| qC4(p)x |":         "after the marks",
		"| qC4(a b) |":        "bad mark",
		"| qC4(\"x) |":        "unterminated",
		"| qC4(v=a b) |":      "",
		"| qC4(,) |":          "empty mark",
		"| 0{qC4} |":          "bad tuplet",
		"| qC4 | qD4 | xx |":  "bar 3",
		"| qC4(t=\"a\\n\") |": "escape",
	}
	for text, want := range cases {
		_, err := ParseVoice(text)
		if err == nil {
			t.Errorf("%q should not parse", text)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %q does not mention %q", text, err, want)
		}
	}
	_, err := ParseVoice("| qC4 qD4\n| qE4 qXx4 |")
	se, ok := err.(*SyntaxError)
	if !ok || se.Bar != 2 || se.Col != 7 {
		t.Errorf("position of the error: %#v", err)
	}
}

func TestFormatVoiceCanonical(t *testing.T) {
	v := mustVoice(t, "qC4 qD4 hE4|wF4||| ")
	want := "| qC4 qD4 hE4\n| wF4\n|\n"
	if got := FormatVoice(v); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if FormatVoice(&Voice{}) != "" || len(mustVoice(t, "").Measures) != 0 || len(mustVoice(t, "| |").Measures) != 0 {
		t.Error("empty voice")
	}
	v = mustVoice(t, "| wC4 | | wE4 |")
	if got := FormatVoice(v); got != "| wC4\n|\n| wE4\n|\n" {
		t.Errorf("empty middle bar: %q", got)
	}
	m := mustVoice(t, "| 3{eC4 3{sD4 sE4 sF4} eG4} 3{eA4 eB4 eC5} 2{q.C4 q.D4} 5:3{sC4 sD4 sE4 sF4 sG4} |").Measures[0]
	if got := FormatMeasure(m); got != "3{eC4 3{sD4 sE4 sF4} eG4} 3{eA4 eB4 eC5} 2{q.C4 q.D4} 5:3{sC4 sD4 sE4 sF4 sG4}" {
		t.Errorf("tuplets: %s", got)
	}
}

// ---------- round trip ----------

// randomVoice builds a canonical voice: what the parser itself would make
// (tuplets in opening order, durations explicit, no trailing empty bar).
func randomVoice(r *rand.Rand) *Voice {
	v := &Voice{}
	bars := 1 + r.Intn(5)
	for b := 0; b < bars; b++ {
		var m Measure
		if r.Intn(6) == 0 && b != bars-1 {
			v.Measures = append(v.Measures, m) // empty bar inside
			continue
		}
		if r.Intn(8) == 0 {
			m.Events = []Event{{Kind: EventBarRest, Marks: randomMarks(r)}}
			v.Measures = append(v.Measures, m)
			continue
		}
		var stack []int
		n := 1 + r.Intn(8)
		for i := 0; i < n; i++ {
			// Open, close or stay; every tuplet gets at least one event.
			if r.Intn(4) == 0 && len(stack) < 3 {
				num := 2 + r.Intn(8)
				den := DefaultTupletDen(num)
				if r.Intn(3) == 0 {
					den = 1 + r.Intn(9)
				}
				t := Tuplet{Num: num, Den: den}
				if len(stack) > 0 {
					t.Parent = stack[len(stack)-1]
				}
				m.Tuplets = append(m.Tuplets, t)
				stack = append(stack, len(m.Tuplets))
			}
			e := randomEvent(r)
			if len(stack) > 0 {
				e.Tuplet = stack[len(stack)-1]
			}
			m.Events = append(m.Events, e)
			if len(stack) > 0 && r.Intn(3) == 0 {
				stack = stack[:len(stack)-1]
			}
		}
		v.Measures = append(v.Measures, m)
	}
	return v
}

func randomEvent(r *rand.Rand) Event {
	e := Event{Dur: Duration{Base: DurBase(r.Intn(10) - 1), Dots: uint8(r.Intn(4))}}
	switch k := r.Intn(10); {
	case k == 0:
		e.Kind = EventRest
	case k == 1:
		e.Kind = EventSpace
	default:
		e.Kind = EventChord
		for n := 1 + r.Intn(4); n > 0; n-- {
			note := Note{Pitch: Pitch{Step: int8(r.Intn(7)), Alter: int8(r.Intn(5) - 2), Octave: int8(r.Intn(11) - 1)},
				Tie: r.Intn(4) == 0, Acc: AccShow(r.Intn(3))}
			e.Notes = append(e.Notes, note)
		}
		if r.Intn(6) == 0 {
			e.Grace = Grace(1 + r.Intn(2))
		}
	}
	e.Marks = randomMarks(r)
	return e
}

func randomMarks(r *rand.Rand) []Mark {
	if r.Intn(3) != 0 {
		return nil
	}
	pool := []Mark{{Name: "p"}, {Name: "sfz"}, {Name: "stacc"}, {Name: "tech", Value: "pizz."}, {Name: "text", Value: "dolce"},
		{Name: "text", Value: `with "quotes", commas | bars (and) \ back`}, {Name: "future_mark", Value: "3"}, {Name: "x", Value: "a b"}, {Name: "ferm"}}
	var out []Mark
	for n := 1 + r.Intn(3); n > 0; n-- {
		out = append(out, pool[r.Intn(len(pool))])
	}
	return out
}

func TestGrammarRoundTripProperty(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 3000; i++ {
		v := randomVoice(r)
		text := FormatVoice(v)
		back, err := ParseVoice(text)
		if err != nil {
			t.Fatalf("case %d: %v\n%s", i, err, text)
		}
		if !reflect.DeepEqual(normVoice(v), normVoice(back)) {
			t.Fatalf("case %d: round trip changed the voice\n%s\n%s", i, text, FormatVoice(back))
		}
		if again := FormatVoice(back); again != text {
			t.Fatalf("case %d: printing is not a fixpoint\n%s\n%s", i, text, again)
		}
	}
}

// normVoice makes nil and empty slices the same for comparison.
func normVoice(v *Voice) *Voice {
	c := v.Clone()
	c.Trim()
	for i := range c.Measures {
		m := &c.Measures[i]
		if len(m.Tuplets) == 0 {
			m.Tuplets = nil
		}
		if len(m.Events) == 0 {
			m.Events = nil
		}
		for j := range m.Events {
			e := &m.Events[j]
			if len(e.Notes) == 0 {
				e.Notes = nil
			}
			if len(e.Marks) == 0 {
				e.Marks = nil
			}
			if e.Kind == EventBarRest {
				e.Dur = Duration{}
			}
		}
	}
	return c
}

// Anything that parses prints to text that parses to the same voice.
func FuzzVoiceGrammar(f *testing.F) {
	for _, s := range []string{
		"| q.Bb4(mp) eC5 hD5~ | qD5 3{eEb5 eD5 eC5} hBb4+D5+F5(ferm) |",
		"| /eA4 ^sB4 qBb4 qr h_ | R(ferm) | |",
		`| qC4(.,>,"dolce",tech=pizz.) 5:3{sC4 s.D4 3{tE4 tF4 tG4}} |`,
		"eC4 D4 E4 F4 | B4 r C5 _",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, err := ParseVoice(s)
		if err != nil {
			return
		}
		text := FormatVoice(v)
		back, err := ParseVoice(text)
		if err != nil {
			t.Fatalf("%q parsed, but its printing %q does not: %v", s, text, err)
		}
		if !reflect.DeepEqual(normVoice(v), normVoice(back)) {
			t.Fatalf("%q: printing %q reads back differently", s, text)
		}
	})
}
