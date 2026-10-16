package musicxml

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"foundry/internal/score"
)

// roundTrip exports a score and imports what it wrote.
func roundTrip(t *testing.T, s *score.Score, o ExportOptions) (*score.Score, []byte) {
	t.Helper()
	b, warns, err := Export(s, o)
	if err != nil {
		t.Fatalf("%s: export: %v", s.Name, err)
	}
	for _, w := range warns {
		t.Logf("%s: export: %s", s.Name, w)
	}
	checkWellFormed(t, s.Name, b)
	d, err := Read(b)
	if err != nil {
		t.Fatalf("%s: reading the export: %v", s.Name, err)
	}
	if d.Software != Software || d.Version != "4.0" {
		t.Errorf("%s: software %q version %q", s.Name, d.Software, d.Version)
	}
	back, iw, err := Import(d, Options{Name: s.Name, NoLayout: o.NoLayout})
	if err != nil {
		t.Fatalf("%s: import of the export: %v", s.Name, err)
	}
	for _, w := range iw {
		t.Errorf("%s: import of the export warns: %s", s.Name, w)
	}
	return back, b
}

// checkWellFormed parses the document with encoding/xml in strict mode and
// checks every element is closed where it opened.
func checkWellFormed(t *testing.T, name string, b []byte) {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(b))
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("%s: not well-formed XML: %v", name, err)
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	if depth != 0 {
		t.Fatalf("%s: %d elements left open", name, depth)
	}
}

// canonical is a voice as the comparison reads it: marks in name order,
// the plain tremolo spelled one way, marks MusicXML has no place for
// left out, and the ends a file must write for a line still open at the
// voice's last event (an ottava, a pedal, a hairpin, a slur) not counted.
func canonical(s *score.Score, st *score.Staff, v *score.Voice) string {
	c := v.Clone()
	var last *score.Event
	for bi := range c.Measures {
		m := &c.Measures[bi]
		// Space at the end of a bar is the bar's silence either way.
		for len(m.Events) > 0 && m.Events[len(m.Events)-1].Kind == score.EventSpace {
			m.Events = m.Events[:len(m.Events)-1]
		}
		offs, _ := m.Offsets(s.BarLength(bi + 1))
		for ei := range m.Events {
			e := &m.Events[ei]
			// A forced accidental the bar needs anyway prints the same.
			for ni := range e.Notes {
				n := &e.Notes[ni]
				if n.Acc == score.AccForce && st.Clef != "percussion" && s.AlterInForce(st, bi+1, offs[ei], n.Pitch.Diatonic()) != int(n.Pitch.Alter) {
					n.Acc = score.AccAuto
				}
			}
			var kept []score.Mark
			for _, mk := range e.Marks {
				if mk.Kind() == score.MarkUnknown && mk.Name != "nobeam" {
					continue
				}
				if mk.Name == "nobeam" {
					continue
				}
				if (mk.Name == "trem" || mk.Name == "trem2") && mk.Value == "3" {
					mk.Value = ""
				}
				kept = append(kept, mk)
			}
			sort.SliceStable(kept, func(i, j int) bool {
				if kept[i].Name != kept[j].Name {
					return kept[i].Name < kept[j].Name
				}
				return kept[i].Value < kept[j].Value
			})
			e.Marks = kept
			last = e
		}
	}
	// A hairpin's end on the event before another hairpin starts is where
	// the new one would end it anyway (and an ottava's before an ottava).
	var prev *score.Event
	for bi := range c.Measures {
		m := &c.Measures[bi]
		for ei := range m.Events {
			e := &m.Events[ei]
			if e.Grace != score.NotGrace {
				continue
			}
			if prev != nil {
				drop := map[string]bool{}
				if e.Has("cresc") || e.Has("dim") {
					drop["hairpin_end"] = true
				}
				if e.Has("8va") || e.Has("8vb") || e.Has("15ma") || e.Has("15mb") {
					drop["ottava_end"] = true
				}
				var kept []score.Mark
				for _, mk := range prev.Marks {
					if !drop[mk.Name] {
						kept = append(kept, mk)
					}
				}
				prev.Marks = kept
			}
			prev = e
		}
	}
	if last != nil {
		var kept []score.Mark
		for _, mk := range last.Marks {
			switch mk.Name {
			case "ottava_end", "ped_end", "hairpin_end", "slur_end":
				continue
			}
			kept = append(kept, mk)
		}
		last.Marks = kept
	}
	return score.FormatVoice(c)
}

// compareScores reports how two scores differ in what MusicXML holds.
func compareScores(a, b *score.Score, layout bool) []string {
	var d []string
	diff := func(what string, x, y any) {
		if fmt.Sprint(x) != fmt.Sprint(y) {
			d = append(d, fmt.Sprintf("%s: %v, came back %v", what, x, y))
		}
	}
	diff("title", a.Title, b.Title)
	diff("subtitle", a.Subtitle, b.Subtitle)
	diff("composer", a.Composer, b.Composer)
	diff("bars", a.Measures(), b.Measures())
	diff("pickup", a.Pickup, b.Pickup)
	// The maps as they read bar by bar: a score with none is in 4/4 and C.
	meters := func(s *score.Score) string {
		var out []string
		for b := 1; b <= s.Measures(); b++ {
			if b == 1 || !s.MeterAt(b).Eq(s.MeterAt(b-1)) {
				out = append(out, fmt.Sprintf("%d:%s", b, s.MeterAt(b)))
			}
		}
		return strings.Join(out, " ")
	}
	diff("meters", meters(a), meters(b))
	keys := func(s *score.Score) string {
		var out []string
		for b := 1; b <= s.Measures(); b++ {
			k := s.KeyAt(b)
			if b > 1 && k.Fifths == s.KeyAt(b-1).Fifths && k.Mode == s.KeyAt(b-1).Mode {
				continue
			}
			mode := k.Mode
			if mode == "" {
				mode = "major"
			}
			out = append(out, fmt.Sprintf("%d:%d%s", b, k.Fifths, mode))
		}
		return strings.Join(out, " ")
	}
	diff("keys", keys(a), keys(b))
	tempo := func(s *score.Score) string {
		var out []string
		ts := s.Tempo
		if len(ts) == 0 {
			ts = []score.TempoMark{{At: score.Pos{Bar: 1}, BPM: 120}}
		}
		for _, t := range ts {
			u := t.Unit
			if u == (score.Duration{}) {
				u = score.Duration{Base: score.Quarter}
			}
			x := fmt.Sprintf("%s=%g/%s %q", s.FormatPos(t.At), t.BPM, u, t.Text)
			if t.HasTo {
				x += " to " + s.FormatPos(t.To)
			}
			out = append(out, x)
		}
		return strings.Join(out, "; ")
	}
	diff("tempo", tempo(a), tempo(b))
	sections := func(s *score.Score) string {
		var out []string
		for _, sec := range s.Sections {
			out = append(out, fmt.Sprintf("%s@%d", sec.Name, sec.Bar))
		}
		return strings.Join(out, " ")
	}
	diff("sections", sections(a), sections(b))
	repeats := func(s *score.Score) string {
		var out []string
		for _, r := range s.Repeats {
			out = append(out, fmt.Sprintf("%d-%d x%d", r.Start, r.End, r.Plays()))
		}
		return strings.Join(out, " ")
	}
	diff("repeats", repeats(a), repeats(b))
	diff("voltas", fmt.Sprint(a.Voltas), fmt.Sprint(b.Voltas))
	jumps := func(s *score.Score) string {
		js := append([]score.Jump(nil), s.Jumps...)
		sort.SliceStable(js, func(i, j int) bool {
			if js[i].Bar != js[j].Bar {
				return js[i].Bar < js[j].Bar
			}
			return js[i].Kind < js[j].Kind
		})
		return fmt.Sprint(js)
	}
	diff("jumps", jumps(a), jumps(b))
	if layout {
		la, lb := a.Layout, b.Layout
		diff("paper", la.Paper, lb.Paper)
		diff("staff", la.Staff, lb.Staff)
		diff("breaks", la.Breaks, lb.Breaks)
	}
	diff("lines", len(a.Lines), len(b.Lines))
	for li := range a.Lines {
		if li >= len(b.Lines) {
			break
		}
		x, y := a.Lines[li], b.Lines[li]
		diff(x.Name+" name", x.Name, y.Name)
		diff(x.Name+" short", x.ShortName(), y.ShortName())
		diff(x.Name+" instrument", x.Instrument, y.Instrument)
		diff(x.Name+" volume", x.Volume, y.Volume)
		diff(x.Name+" pan", x.Pan, y.Pan)
		diff(x.Name+" staves", len(x.Staves), len(y.Staves))
		for si := range x.Staves {
			if si >= len(y.Staves) {
				break
			}
			p, q := x.Staves[si], y.Staves[si]
			diff(fmt.Sprintf("%s staff %d clef", x.Name, si+1), p.Clef, q.Clef)
			diff(fmt.Sprintf("%s staff %d transposition", x.Name, si+1), p.Transpose, q.Transpose)
			diff(fmt.Sprintf("%s staff %d clef changes", x.Name, si+1), p.Clefs, q.Clefs)
			var pv, qv []string
			for _, v := range p.Voices {
				if t := canonical(a, p, v); t != "" {
					pv = append(pv, t)
				}
			}
			for _, v := range q.Voices {
				if t := canonical(b, q, v); t != "" {
					qv = append(qv, t)
				}
			}
			if len(pv) != len(qv) {
				d = append(d, fmt.Sprintf("%s staff %d: %d voices, came back %d", x.Name, si+1, len(pv), len(qv)))
				continue
			}
			for vi := range pv {
				if pv[vi] != qv[vi] {
					d = append(d, fmt.Sprintf("%s staff %d voice %d:\n%s\ncame back\n%s", x.Name, si+1, vi+1, pv[vi], qv[vi]))
				}
			}
		}
	}
	return d
}

// fixtureScores are every score this repository has: the engraver's and
// the CLI's fixtures and the shipped ones.
func fixtureScores(t *testing.T) []*score.Score {
	t.Helper()
	var files []string
	for _, g := range []string{
		filepath.Join("..", "..", "engrave", "testdata", "*"+score.FileExt),
		filepath.Join("..", "..", "scorecli", "testdata", "*"+score.FileExt),
		filepath.Join("..", "..", "..", "..", "resources", "sounds", "*"+score.FileExt),
	} {
		fs, _ := filepath.Glob(g)
		files = append(files, fs...)
	}
	if len(files) < 20 {
		t.Fatalf("found %d fixture scores", len(files))
	}
	var out []*score.Score
	for _, f := range files {
		s, err := score.LoadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		s.Name = strings.TrimSuffix(filepath.Base(f), score.FileExt)
		out = append(out, s)
	}
	return out
}

// overfull reports a bar a voice fills past its meter (the engraver's
// fixtures have some): MusicXML reads such a bar as one of another
// length, so it cannot come back the same.
func overfull(s *score.Score) bool {
	for _, l := range s.Lines {
		for _, st := range l.Staves {
			for _, v := range st.Voices {
				for bi, m := range v.Measures {
					if _, end := m.Offsets(s.BarLength(bi + 1)); s.BarLength(bi + 1).Less(end) {
						return true
					}
				}
			}
		}
	}
	return false
}

// Every fixture score goes out as MusicXML and comes back the same in
// everything MusicXML holds: the maps, the form, the layout, the lines
// and every voice's notes and marks.
func TestExportRoundTripsEveryFixture(t *testing.T) {
	useRepoResources(t)
	for _, s := range fixtureScores(t) {
		if overfull(s) {
			t.Logf("%s overfills a bar; it is not compared", s.Name)
			continue
		}
		back, _ := roundTrip(t, s, ExportOptions{})
		for _, d := range compareScores(s, back, true) {
			t.Errorf("%s: %s", s.Name, d)
		}
	}
}

// A score written to exercise every construct the writer knows.
const everything = `name: everything
title: Everything
subtitle: a test of the writer
composer: Somebody
bars: 10
pickup: 1/4
tempo:
  - {bar: 1, bpm: 92, text: Andante}
  - {bar: 5, text: rit., to: "6:3"}
  - {bar: 7, bpm: 60, unit: q., text: Lento}
  - {bar: 8, text: a tempo}
meter: [{bar: 1, sig: 4/4}, {bar: 6, sig: 3+2+2/8}, {bar: 7, sig: 6/8}]
key: [{bar: 1, fifths: -2, mode: major}, {bar: 6, fifths: 3, mode: minor}]
sections: [{name: A, bar: 2}, {name: B, bar: 6}]
repeats: [{start: 2, end: 4}]
voltas: [{start: 4, end: 4, passes: [1]}, {start: 5, end: 5, passes: [2]}]
jumps:
  - {mark: segno, bar: 2}
  - {mark: to_coda, bar: 3}
  - {mark: ds, bar: 8, al: coda}
  - {mark: coda, bar: 9}
layout:
  paper: a4
  staff: 6.5
  margins: {top: 15, bottom: 15, left: 14, right: 14}
  breaks: [{bar: 4}, {bar: 7, page: true}]
lines:
  - name: Clarinet in Bb
    short: Cl.
    instrument: woodwinds/clarinet
    volume: 0.8
    pan: -0.25
    staves:
      - clef: treble
        transpose: -M2
        clefs: [{at: "3:3", clef: bass}, {at: "4", clef: treble}]
        voices:
          - |
            | qF4(p,cresc)
            | eG4(slur) eA4 eBb4 eC5(slur_end,hairpin_end) qD5(f,stacc) e.Eb5(acc,ten) sF5(marc)
            | 3{eG5 eA5 eBb5(gliss=wavy)} qC6(ferm=long) qD4(tr=#) qE4(mord=b)
            | sF4 sG4 sA4 sBb4 eC5(staccmo) eD5(portato,turn=b/#) /eF5 hG5(trem)
            | ^eA4 qBb4(arp=down,8va) qC5 hD5(ottava_end,"dolce")
            | qE5(dim=text) e.F#5 sG5 qA5(hairpin_end,mp) eB4(tech=pizz.)
            | 3{sC#5 sD5 sE5} qF#5(fp) qG#5~ eG#5
            | q.A4(vel=72) q.B4(expr=espressivo)
            | q.C#5(trem2,up) q.E5
            | 3:2{eA4 eB4 3{sC#5 sD5 sE5}} qA4 qA4(niente)
  - name: Piano
    instrument: keys/grand_piano
    staves:
      - clef: treble
        voices:
          - |
            | qr
            | qC5+E5+G5 qr hD5+F#5!
            | wE5
            | wF5
            | R
            | hG5 qr er
            | q.A5 q.B5
            | q.C6 q.D6
            | h.A5(arp=up)
            | h.B5
          - |
            |
            |
            | hC5 hB4
      - clef: bass
        voices:
          - |
            | qC3(ped)
            | wC2
            | wD2(ped)
            | wE2(ped_end)
            | R(ferm)
            | h.G2 er
            | R
            | R
            | R
            | R
  - name: Drums
    instrument: percussion/drum_kit
    staves:
      - clef: percussion
        voices:
          - |
            | qr
            | qC2 qD2 qF#2 qA#2
`

func parseEverything(t *testing.T) *score.Score {
	t.Helper()
	s, err := score.Parse([]byte(everything))
	if err != nil {
		t.Fatal(err)
	}
	if is := s.Validate(nil); score.HasErrors(is) {
		t.Fatalf("the test score does not validate: %v", is)
	}
	return s
}

func TestExportRoundTripsEveryConstruct(t *testing.T) {
	useRepoResources(t)
	s := parseEverything(t)
	back, b := roundTrip(t, s, ExportOptions{})
	for _, d := range compareScores(s, back, true) {
		t.Errorf("%s", d)
	}
	if os.Getenv("MUSICXML_DUMP") != "" {
		os.WriteFile(os.Getenv("MUSICXML_DUMP"), b, 0o644)
	}
	// What the document says, read as another program reads it.
	doc := string(b)
	for _, want := range []string{
		`<transpose>`, `<diatonic>-1</diatonic>`, `<chromatic>-2</chromatic>`,
		`<fifths>0</fifths>`, // B-flat major written for a B-flat clarinet is C
		`<beam number="1">begin</beam>`, `<beam number="2">begin</beam>`,
		`<tuplet type="start" number="1" bracket="yes">`, `<tuplet type="start" number="2" bracket="yes">`,
		`<time-modification>`, `<actual-notes>9</actual-notes>`,
		`<wedge type="crescendo" number="1"/>`, `<wedge type="stop" number="1"/>`,
		`<words font-style="italic">dim.</words>`, `<dashes type="start" number="1"/>`,
		`<octave-shift type="down" size="8" number="1"/>`, `<pedal type="start" line="yes"/>`, `<pedal type="change" line="yes"/>`,
		`<glissando type="start" line-type="wavy" number="1"/>`, `<trill-mark placement="above"/>`,
		`<accidental-mark placement="above">sharp</accidental-mark>`, `<tremolo type="single">3</tremolo>`,
		`<fermata type="upright">square</fermata>`, `<arpeggiate direction="down"/>`, `<grace slash="yes"/>`,
		`<rehearsal enclosure="square">A</rehearsal>`, `<segno/>`, `<coda/>`, `<sound dalsegno="segno"/>`,
		`<ending number="1" type="start">1.</ending>`, `<repeat direction="backward"/>`,
		`<print new-system="yes"/>`, `<print new-page="yes"/>`, `<millimeters>6.5</millimeters>`,
		`<per-minute>92</per-minute>`, `<beat-unit-dot/>`, `<sound tempo="90"/>`,
		`<credit-type>subtitle</credit-type>`, `<part-abbreviation>Cl.</part-abbreviation>`,
		`<virtual-name>woodwinds/clarinet</virtual-name>`, `<unpitched>`, `<notehead>x</notehead>`,
		`<midi-unpitched>39</midi-unpitched>`, `<rest measure="yes"/>`, `<accidental>sharp</accidental>`,
		`<clef number="1">`, `<staves>2</staves>`, `<dynamics><n/></dynamics>`,
	} {
		if !strings.Contains(strings.Join(strings.Fields(doc), ""), strings.Join(strings.Fields(want), "")) {
			t.Errorf("the document has no %s", want)
		}
	}
}

// With NoLayout the file has no page and no breaks, and imports with none.
func TestExportWithoutLayout(t *testing.T) {
	useRepoResources(t)
	s := parseEverything(t)
	back, b := roundTrip(t, s, ExportOptions{NoLayout: true})
	if bytes.Contains(b, []byte("<print")) || bytes.Contains(b, []byte("<defaults>")) {
		t.Error("a file without layout has a <print> or <defaults>")
	}
	if len(back.Layout.Breaks) != 0 || back.Layout.Staff != 0 || back.Layout.Paper != (score.Paper{}) {
		t.Errorf("layout came back %+v", back.Layout)
	}
	// An import told to leave the layout out does, from a file with one.
	b2, _, _ := Export(s, ExportOptions{})
	d, _ := Read(b2)
	s2, _, err := Import(d, Options{NoLayout: true})
	if err != nil || len(s2.Layout.Breaks) != 0 || s2.Layout.Staff != 0 {
		t.Errorf("NoLayout import: %v %+v", err, s2.Layout)
	}
}

// The same score always writes the same bytes; a compressed .mxl reads
// back as the plain file does.
func TestExportIsStableAndCompresses(t *testing.T) {
	useRepoResources(t)
	s := parseEverything(t)
	a, _, _ := Export(s, ExportOptions{})
	b, _, _ := Export(s.Clone(), ExportOptions{})
	if !bytes.Equal(a, b) {
		t.Error("two exports of one score differ")
	}
	dir := t.TempDir()
	for _, name := range []string{"x.musicxml", "x.mxl"} {
		p := filepath.Join(dir, name)
		if _, err := ExportFile(p, s, ExportOptions{}); err != nil {
			t.Fatal(err)
		}
		d, err := ReadFile(p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		back, _, err := Import(d, Options{})
		if err != nil {
			t.Fatal(err)
		}
		for _, diff := range compareScores(s, back, true) {
			t.Errorf("%s: %s", name, diff)
		}
	}
}

// What an exporter cannot write is refused or said.
func TestExportRefusesAnEmptyScore(t *testing.T) {
	if _, _, err := Export(score.New("x"), ExportOptions{}); err == nil {
		t.Error("a score with no lines exported")
	}
}
