package score

import (
	"math"
	"strings"
	"testing"
)

func TestLayoutFile(t *testing.T) {
	src := `version: 1
title: T
subtitle: Sub
bars: 4
layout:
  paper: a4
  landscape: true
  margins: {top: 10, bottom: 11, left: 12, right: 13}
  staff: 6
  system_gap: 12
  spacing: 0.8
  breaks: [{bar: 3}, {bar: 2, page: true}]
  title_page: true
  page_numbers: false
  names: short
  cautionary: false
  courtesy: true
  brackets: false
  hide_empty: true
  parts:
    - {line: Flute, staff: 7.5, breaks: [{bar: 4}], multi_rests: false, paper: tabloid}
lines:
  - name: Flute
    short: Fl.
    instrument: winds/flute
    staves: [{clef: treble, voices: ["| wr | wr | wr | wr |"]}]
`
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	l := s.Layout
	if s.Subtitle != "Sub" || s.Lines[0].Short != "Fl." {
		t.Fatalf("subtitle %q short %q", s.Subtitle, s.Lines[0].Short)
	}
	if w, h := l.Paper.PaperSize(); w != 297 || h != 210 {
		t.Errorf("a4 landscape = %gx%g", w, h)
	}
	if l.Paper.MarginsOrDefault() != (Margins{10, 11, 12, 13}) || l.StaffOrDefault() != 6 || l.SystemGapOrDefault() != 12 || l.SpacingOrDefault() != 0.8 {
		t.Errorf("margins/staff/gap: %+v", l)
	}
	if len(l.Breaks) != 2 || l.Breaks[0] != (Break{Bar: 2, Page: true}) || l.Breaks[1] != (Break{Bar: 3}) {
		t.Errorf("breaks sorted: %+v", l.Breaks)
	}
	if !l.TitlePage || !l.NoPageNumbers || l.Names != NamesShort || !l.NoCautionary || !l.Courtesy || !l.NoBrackets || !l.HideEmpty {
		t.Errorf("switches: %+v", l)
	}
	pl, multi := l.ForPart("flute")
	if multi || pl.StaffOrDefault() != 7.5 || len(pl.Breaks) != 1 || pl.Breaks[0].Bar != 4 || pl.TitlePage {
		t.Errorf("part layout: %+v multi %v", pl, multi)
	}
	if w, _ := pl.Paper.PaperSize(); w != 279.4 {
		t.Errorf("part paper width %g", w)
	}
	// A line with no part settings takes the score's, without its breaks.
	ol, multi := l.ForPart("Oboe")
	if !multi || ol.StaffOrDefault() != 6 || len(ol.Breaks) != 0 {
		t.Errorf("default part: %+v %v", ol, multi)
	}
	b, err := Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(b)
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if !strings.Contains(string(b), "layout:") || back.Layout.Names != NamesShort || len(back.Layout.Parts) != 1 {
		t.Errorf("round trip:\n%s", b)
	}
	// A score with no layout writes none.
	plain := New("x")
	pb, _ := Marshal(plain)
	if strings.Contains(string(pb), "layout") {
		t.Errorf("empty layout written:\n%s", pb)
	}
}

func TestLayoutErrors(t *testing.T) {
	for name, block := range map[string]string{
		"paper":       "{paper: quarto}",
		"custom":      "{paper: custom}",
		"size":        "{paper: a4, size: [100, 100]}",
		"small":       "{size: [10, 100]}",
		"staff":       "{staff: 30}",
		"gap":         "{system_gap: -1}",
		"break":       "{breaks: [{bar: 1}]}",
		"twice":       "{breaks: [{bar: 2}, {bar: 2, page: true}]}",
		"names":       "{names: initials}",
		"margins":     "{margins: {top: 200, bottom: 0, left: 0, right: 0}}",
		"part line":   "{parts: [{staff: 6}]}",
		"part twice":  "{parts: [{line: A}, {line: a}]}",
		"part staff":  "{parts: [{line: A, staff: 1}]}",
		"part breaks": "{parts: [{line: A, breaks: [{bar: 0}]}]}",
		"spacing":     "{spacing: 3}",
		"tight":       "{spacing: 0.1}",
	} {
		src := "version: 1\nbars: 2\nlayout: " + block + "\nlines: []\n"
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: %s parsed", name, block)
		}
	}
}

func TestLayoutValidateAndBreaks(t *testing.T) {
	s := New("x")
	s.Lines = []*Line{NewLine("Violin I", "strings/violin", "treble")}
	s.Bars = 4
	s.Layout.SetBreak(3, false, false)
	s.Layout.SetBreak(9, false, true)
	s.Layout.SetBreak(2, false, true)
	if len(s.Layout.Breaks) != 3 || s.Layout.Breaks[0].Bar != 2 || !s.Layout.Breaks[0].Page {
		t.Fatalf("breaks %+v", s.Layout.Breaks)
	}
	s.Layout.SetBreak(2, false, false)
	if b, _ := s.Layout.BreakAt(2); b.Page {
		t.Errorf("a set break replaces the old one")
	}
	s.Layout.SetBreak(3, true, false)
	if _, ok := s.Layout.BreakAt(3); ok {
		t.Errorf("removed break still there")
	}
	s.Layout.EnsurePart("Viola")
	s.Layout.EnsurePart("viola").Staff = 8
	if len(s.Layout.Parts) != 1 || s.Layout.Parts[0].Staff != 8 {
		t.Errorf("parts %+v", s.Layout.Parts)
	}
	var got []string
	for _, is := range s.Validate(func(string) bool { return true }) {
		got = append(got, is.String())
	}
	all := strings.Join(got, "\n")
	if !strings.Contains(all, "break before bar 9") || !strings.Contains(all, `part "Viola" names no line`) {
		t.Errorf("validation:\n%s", all)
	}
	c := s.Clone()
	c.Layout.Breaks[0].Bar = 3
	c.Layout.Parts[0].Staff = 5
	if s.Layout.Breaks[0].Bar != 2 || s.Layout.Parts[0].Staff != 8 {
		t.Errorf("clone shares the layout")
	}
}

func TestShortNames(t *testing.T) {
	for full, want := range map[string]string{
		"Violin I":       "Vln. I",
		"Violin Section": "Vln.",
		"Clarinet in Bb": "Cl. in Bb",
		"Double Bass":    "D.B.",
		"Horn 2":         "Hn. 2",
		"Piano":          "Pno.",
		"Hangar Lead":    "Han. Ld.",
		"Synth Pad":      "Synth. Pad",
		"Drum Kit":       "Dr.",
		"Bass":           "Bass",
		"Alto Saxophone": "A. Sax.",
		"English Horn":   "E.H.",
	} {
		if got := AbbreviateName(full); got != want {
			t.Errorf("%q → %q, want %q", full, got, want)
		}
	}
	l := &Line{Name: "Violin I", Short: "V. I"}
	if l.ShortName() != "V. I" {
		t.Errorf("own short name not used")
	}
}

// Margins that would leave the music no paper are made smaller (in
// proportion, to leave MinTextArea), and Validate says so; a file with
// them still loads.
func TestTightMarginsAreMadeSmaller(t *testing.T) {
	src := "version: 1\nbars: 2\nlayout: {paper: custom, size: [60, 200], margins: {top: 10, bottom: 10, left: 30, right: 10}, parts: [{line: A, paper: custom, size: [210, 220], margins: {top: 100, bottom: 100, left: 10, right: 10}, staff: 6}], names: short}\n" +
		"lines: [{name: A, instrument: keys/grand_piano, staves: [{clef: treble, voices: ['| wC4 | wC4 |']}]}]\n"
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	m := s.Layout.Paper.TextMargins()
	if math.Abs(m.Left-26.25) > 1e-9 || math.Abs(m.Right-8.75) > 1e-9 || m.Top != 10 || m.Bottom != 10 {
		t.Errorf("text margins %+v", m)
	}
	if ok := (Paper{}).TextMargins() == (Paper{}).MarginsOrDefault(); !ok {
		t.Errorf("the default margins were changed")
	}
	var got []string
	for _, is := range s.Validate(func(string) bool { return true }) {
		got = append(got, is.String())
	}
	all := strings.Join(got, "\n")
	if !strings.Contains(all, "the margins leave the music less than") || !strings.Contains(all, `part "A": the margins`) {
		t.Errorf("validation:\n%s", all)
	}
}
