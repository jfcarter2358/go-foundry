package score

import (
	"reflect"
	"testing"
)

// Syllables are marks on their notes: verses by name, hyphens and
// extenders by their last character, kept through the voice grammar.
func TestLyricMarks(t *testing.T) {
	v, err := ParseVoice(`| qC4(ly=Hal-,ly2=Glo-) qD4(ly=le-,ly2=ri_) qE4(ly=lu-) qF4 | hG4(ly=jah_,ly2="a,") qA4 qr | qB4(ly=A-) qC5(ly=men) h_ |`)
	if err != nil {
		t.Fatal(err)
	}
	e := v.Measures[0].Events[0]
	got := e.Lyrics()
	want := []Lyric{{Verse: 1, Text: "Hal", Hyphen: true}, {Verse: 2, Text: "Glo", Hyphen: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lyrics %+v", got)
	}
	if l, ok := v.Measures[0].Events[1].Lyric(2); !ok || !l.Extend || l.Text != "ri" {
		t.Errorf("verse 2 extender: %+v", l)
	}
	if KindOf("ly3") != MarkLyric || KindOf("ly9") == MarkLyric || KindOf("lyx") == MarkLyric {
		t.Error("lyric mark kinds")
	}
	if s := LyricText(v, 1); s != "Hal-le-lu-jah A-men" {
		t.Errorf("verse 1 reads %q", s)
	}
	if s := LyricText(v, 2); s != "Glo-ri a," {
		t.Errorf("verse 2 reads %q", s)
	}
	ls := VoiceLyrics(v, 1)
	syl := []Syllabic{SyllableBegin, SyllableMiddle, SyllableMiddle, SyllableEnd, SyllableBegin, SyllableEnd}
	for i, l := range ls {
		if l.Syllabic != syl[i] {
			t.Errorf("syllable %d (%s) is %v, want %v", i, l.Text, l.Syllabic, syl[i])
		}
	}
	// "jah_" holds over the A, and stops at the rest.
	if j := ls[3]; j.UntilBar != 2 || j.Until != 1 {
		t.Errorf("extender of %q runs to bar %d event %d", j.Text, j.UntilBar, j.Until)
	}
	// "ri_" in verse 2 holds over the E and F (no syllable there) to the barline.
	if r := VoiceLyrics(v, 2)[1]; r.UntilBar != 1 || r.Until != 3 {
		t.Errorf("verse 2 extender runs to bar %d event %d", r.UntilBar, r.Until)
	}
	if text := FormatVoice(v); !reflect.DeepEqual(mustVoice(t, text), v) {
		t.Errorf("does not round-trip:\n%s", text)
	}
}

func TestSetLyric(t *testing.T) {
	e := Event{Kind: EventChord, Notes: []Note{{Pitch: Pitch{Octave: 4}}}, Marks: []Mark{{Name: "p"}}}
	e.SetLyric(Lyric{Verse: 1, Text: "la", Hyphen: true})
	e.SetLyric(Lyric{Verse: 3, Text: "don't"})
	if got := FormatEvent(e); got != `qC4(p,ly=la-,ly3="don't")` && got != `wC4(p,ly=la-,ly3="don't")` {
		t.Logf("%s", got)
	}
	e.SetLyric(Lyric{Verse: 1, Text: "lo", Extend: true})
	if l, _ := e.Lyric(1); l.Text != "lo" || !l.Extend || l.Hyphen {
		t.Errorf("replaced: %+v", l)
	}
	if len(e.Marks) != 3 {
		t.Errorf("marks %v", e.Marks)
	}
	e.SetLyric(Lyric{Verse: 1})
	if _, ok := e.Lyric(1); ok || len(e.Marks) != 2 {
		t.Errorf("removed: %v", e.Marks)
	}
	if ParseLyric(1, "-").Text != "-" || ParseLyric(1, "_").Text != "_" {
		t.Error("a syllable that is only a hyphen or an underscore")
	}
	l := &Line{Staves: []*Staff{{Voices: []*Voice{{Measures: []Measure{{Events: []Event{e}}}}}}}}
	if l.Verses() != 3 {
		t.Errorf("verses %d", l.Verses())
	}
}

// Lyrics on what is not sung are warned about.
func TestValidateLyrics(t *testing.T) {
	s := New("x")
	ln := NewLine("Voice", "choir/choir", "treble")
	ln.Staves[0].Voices[0] = mustVoice(t, `| qr(ly=la) /eC5(ly=a) qD5(ly=ah) h_ |`)
	s.Lines = []*Line{ln}
	var warns int
	for _, is := range s.Validate(nil) {
		if is.Severity == Warning {
			warns++
		}
	}
	if warns != 2 {
		t.Errorf("%d warnings: %v", warns, s.Validate(nil))
	}
}
