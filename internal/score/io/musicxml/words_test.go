package musicxml

import (
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/score"
)

func loadFixture(t *testing.T, name string) *score.Score {
	t.Helper()
	s, err := score.LoadFile(filepath.Join("..", "..", "engrave", "testdata", name+score.FileExt))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Lyrics go out as <lyric>s with their verse, place in the word and
// extenders; chord symbols as <harmony> with our spelling; tablature as a
// TAB staff with its tuning and every note's string and fret.
func TestExportWordsAndStrings(t *testing.T) {
	useRepoResources(t)
	for _, c := range []struct {
		fixture string
		want    []string
	}{
		{"lyrics", []string{`<lyric number="1">`, `<lyric number="2">`, `<syllabic>begin</syllabic>`, `<syllabic>middle</syllabic>`,
			`<syllabic>end</syllabic>`, `<syllabic>single</syllabic>`, `<text>ma</text>`, `<extend/>`,
			`<kind text="m7">minor-seventh</kind>`, `<kind text="m7b5">half-diminished</kind>`, `<kind text="N.C.">none</kind>`,
			`<kind text="dim7">diminished-seventh</kind>`, `<kind text="7alt">dominant</kind>`, `<degree-type>add</degree-type>`,
			`<bass-step>F</bass-step>`, `<bass-alter>1</bass-alter>`, `<offset>`}},
		{"tab", []string{`<sign>TAB</sign>`, `<staff-lines>6</staff-lines>`, `<staff-lines>4</staff-lines>`, `<staff-tuning line="1">`,
			`<tuning-step>E</tuning-step>`, `<string>5</string>`, `<fret>3</fret>`, `<staves>2</staves>`}},
		{"kits", []string{`<staff-lines>1</staff-lines>`, `<instrument-name>Open Hi-Hat</instrument-name>`, `<stopped/>`, `<open/>`}},
	} {
		s := loadFixture(t, c.fixture)
		b, warns, err := Export(s, ExportOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(warns) > 0 {
			t.Errorf("%s: warnings %v", c.fixture, warns)
		}
		doc := string(b)
		for _, w := range c.want {
			if !strings.Contains(doc, w) {
				t.Errorf("%s: no %s", c.fixture, w)
			}
		}
		// The notation staff over a linked tablature carries no strings
		// of its own (the TAB staff after it does).
		if c.fixture == "tab" && strings.Count(doc, `<staff-details number="1">`) != 1 {
			t.Errorf("tab: staff details %d", strings.Count(doc, "<staff-details"))
		}
	}
}

// Chord symbols from another program are read from their kind and
// degrees, or the text when it agrees with the kind.
func TestImportForeignHarmony(t *testing.T) {
	useRepoResources(t)
	h := func(body string) string {
		return `<harmony>` + body + `</harmony>`
	}
	cases := []struct{ xml, want string }{
		{h(`<root><root-step>C</root-step></root><kind>dominant</kind><bass><bass-step>E</bass-step></bass><degree><degree-value>9</degree-value><degree-alter>-1</degree-alter><degree-type>add</degree-type></degree>`), "C7b9/E"},
		{h(`<root><root-step>B</root-step><root-alter>-1</root-alter></root><kind text="Maj7">major-seventh</kind>`), "Bbmaj7"},
		{h(`<root><root-step>F</root-step><root-alter>1</root-alter></root><kind text="m7b5">half-diminished</kind>`), "F#m7b5"},
		{h(`<root><root-step>D</root-step></root><kind text="add9">major</kind><degree><degree-value>9</degree-value><degree-alter>0</degree-alter><degree-type>add</degree-type></degree>`), "Dadd9"},
		{h(`<root><root-step>G</root-step></root><kind>suspended-fourth</kind><degree><degree-value>7</degree-value><degree-alter>-1</degree-alter><degree-type>add</degree-type></degree>`), "G7sus4"},
		{h(`<root><root-step>A</root-step></root><kind>minor-seventh</kind><degree><degree-value>5</degree-value><degree-alter>0</degree-alter><degree-type>subtract</degree-type></degree>`), "Am7no5"},
		{h(`<root><root-step>C</root-step></root><kind text="">none</kind>`), "N.C."},
		{h(`<root><root-step>E</root-step></root><kind text="X">major-ninth</kind>`), "Emaj9"},
	}
	for _, c := range cases {
		doc := `<?xml version="1.0"?>
<score-partwise version="4.0"><part-list><score-part id="P1"><part-name>Guitar</part-name></score-part></part-list>
<part id="P1"><measure number="1"><attributes><divisions>1</divisions><time><beats>4</beats><beat-type>4</beat-type></time></attributes>
<note><pitch><step>C</step><octave>4</octave></pitch><duration>2</duration><voice>1</voice><type>half</type></note>` + c.xml +
			`<note><pitch><step>D</step><octave>4</octave></pitch><duration>2</duration><voice>1</voice><type>half</type></note></measure></part></score-partwise>`
		d, err := Read([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		s, _, err := Import(d, Options{})
		if err != nil {
			t.Fatal(err)
		}
		ch := s.Lines[0].Chords
		if len(ch) != 1 || ch[0].Chord.String() != c.want || s.FormatPos(ch[0].At) != "1:3" {
			t.Errorf("%s: %+v", c.want, ch)
		}
	}
}

// MuseScore writes a linked tablature as a TAB staff repeating the
// notation (this file is its re-export of our tab fixture): it comes back
// as the notation's linked tablature, with the strings MuseScore kept.
func TestImportMuseScoreLinkedTab(t *testing.T) {
	useRepoResources(t)
	d, err := ReadFile(filepath.Join("testdata", "musescore_tab.musicxml"))
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := Import(d, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := loadFixture(t, "tab")
	if len(s.Lines) != 2 || len(s.Lines[0].Staves) != 1 {
		t.Fatalf("lines %d, guitar staves %d", len(s.Lines), len(s.Lines[0].Staves))
	}
	for li := range want.Lines {
		a, b := want.Lines[li].Staves[0], s.Lines[li].Staves[0]
		if b.Tab == nil || b.Tab.Only != a.Tab.Only || b.Tab.Preset() != a.Tab.Preset() {
			t.Errorf("line %d tab %+v", li, b.Tab)
		}
		if got, w := score.FormatVoice(b.Voices[0]), score.FormatVoice(a.Voices[0]); got != w {
			t.Errorf("line %d:\n%s\nwant\n%s", li, got, w)
		}
	}
	if b := s.Lines[1].Staves[0]; b.Clef != "bass" {
		t.Errorf("the bass's tab-only staff in %s", b.Clef)
	}
}

// Text keeps its music signs (every backend draws them), while a part's
// name, which the editor's headers set in ImGui's font, spells them as
// letters — Sibelius's part-name-display with its accidental-text too.
func TestImportKeepsMusicSignsInText(t *testing.T) {
	useRepoResources(t)
	doc := `<?xml version="1.0"?>
<score-partwise version="4.0"><work><work-title>Prelude in C♯ minor</work-title></work>
<part-list><score-part id="P1"><part-name></part-name><part-name-display><display-text>Clarinet in B</display-text><accidental-text>flat</accidental-text></part-name-display></score-part></part-list>
<part id="P1"><measure number="1"><attributes><divisions>1</divisions><time><beats>4</beats><beat-type>4</beat-type></time></attributes>
<direction placement="above"><direction-type><words>sempre E♮</words></direction-type></direction>
<note><pitch><step>C</step><octave>4</octave></pitch><duration>4</duration><voice>1</voice><type>whole</type><lyric number="1"><syllabic>single</syllabic><text>B♭</text></lyric></note></measure></part></score-partwise>`
	d, err := Read([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := Import(d, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Title != "Prelude in C♯ minor" {
		t.Errorf("title %q", s.Title)
	}
	if n := s.Lines[0].Name; n != "Clarinet in Bb" {
		t.Errorf("part name %q", n)
	}
	e := s.Lines[0].Staves[0].Voices[0].Measures[0].Events[0]
	if l, ok := e.Lyric(1); !ok || l.Text != "B♭" {
		t.Errorf("lyric %+v", l)
	}
	found := false
	for _, m := range e.Marks {
		found = found || strings.Contains(m.Value, "sempre E♮")
	}
	if !found {
		t.Errorf("the words lost their natural: %+v", e.Marks)
	}
}
