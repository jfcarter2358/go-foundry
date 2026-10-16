package score

// layout.go — how a score sits on paper: the page view, the PDF and the
// parts.
//
// A Layout is the paper (a named size or a custom one, portrait or
// landscape), its margins, the staff size, the room between systems, the
// system and page breaks a writer asked for, and the house style choices a
// page needs that the continuous view does not: whether a title page comes
// first, page numbers, instrument names (full on the first system, short
// after), cautionary clefs, keys and time signatures at the end of a
// system, and courtesy accidentals in the bar after an altered note.
//
// Parts are a line each. A part takes the score's layout and overrides
// what it sets itself (PartLayout): its own breaks — a part's systems
// break in other places than the score's — its staff size, its system gap,
// its paper; and it gathers rest bars into multi-bar rests unless told not
// to. Every length is in millimetres except the system gap, which is in
// staff spaces (it scales with the staff).
//
// None of this changes how a score plays; the engraver's page layout
// (internal/score/engrave, page.go) reads it.

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Paper sizes, in millimetres (portrait).
var PaperSizes = map[string][2]float64{
	"letter":  {215.9, 279.4},
	"a4":      {210, 297},
	"tabloid": {279.4, 431.8},
	"a3":      {297, 420},
	"legal":   {215.9, 355.6},
}

// PaperNames are the named sizes in the order a picker shows them, then
// "custom".
var PaperNames = []string{"letter", "a4", "tabloid", "a3", "legal", "custom"}

// Layout defaults.
const (
	DefaultPaper     = "letter"
	DefaultStaff     = 7.0  // mm, the staff's height (Sibelius's default)
	DefaultMargin    = 12.7 // mm (half an inch)
	DefaultSystemGap = 9.0  // staff spaces, bottom line of one system to the top of the next, at least
	// MinStaff and MaxStaff bound the staff size: 3 mm is a miniature
	// score, 12 mm a large-print part.
	MinStaff = 3.0
	MaxStaff = 12.0
	// MinSpacing and MaxSpacing bound the note spacing: half as tight as
	// the default to twice as loose.
	MinSpacing = 0.5
	MaxSpacing = 2.0
	// MinTextArea is the least room, in mm each way, margins may leave
	// the music (TextMargins makes larger margins smaller).
	MinTextArea = 25.0
)

// NameMode is how instrument names print at the left of the systems.
type NameMode uint8

const (
	// NamesFullThenShort prints full names on the first system and short
	// names after.
	NamesFullThenShort NameMode = iota
	// NamesFull prints full names on every system.
	NamesFull
	// NamesShort prints short names on every system.
	NamesShort
	// NamesNone prints no names.
	NamesNone
)

// NameModes are NameMode's names in a file, in order.
var NameModes = []string{"full_then_short", "full", "short", "none"}

// Margins are the page's margins, in millimetres.
type Margins struct {
	Top, Bottom, Left, Right float64
}

// Break is a break a writer asked for before a bar: a new system there,
// or (Page) a new page.
type Break struct {
	Bar  int
	Page bool
}

// Paper is a page size and its margins.
type Paper struct {
	// Name is one of PaperNames ("" is the default, letter); "custom"
	// takes Width and Height.
	Name          string
	Width, Height float64
	Landscape     bool
	// Margins of zero are the default margins.
	Margins Margins
}

// Layout is a score's page layout.
type Layout struct {
	Paper Paper
	// Staff is the staff's height in millimetres (a staff space is a
	// quarter of it); 0 is DefaultStaff.
	Staff float64
	// SystemGap is the least room between systems, staff spaces from one
	// system's bottom line to the next one's top line; 0 is the default.
	SystemGap float64
	// Spacing scales the room each duration asks for (Sibelius's note
	// spacing rule): below 1 more bars fit a system, above 1 fewer; 0 is 1.
	// The systems are justified either way.
	Spacing float64
	// Breaks are the system and page breaks asked for, by bar.
	Breaks []Break
	// TitlePage puts the title, subtitle and composer on a page of their
	// own before the music; off, they head the first page.
	TitlePage bool
	// NoPageNumbers leaves pages unnumbered (numbers are on by default,
	// from the second page).
	NoPageNumbers bool
	Names         NameMode
	// NoCautionary leaves out the clef, key and time signatures a system
	// shows at its end for a change at the next system's start.
	NoCautionary bool
	// Courtesy prints a courtesy accidental, in parentheses, on a note in
	// the bar after one where its pitch was altered.
	Courtesy bool
	// Parts are the parts' own settings, by line name.
	Parts []PartLayout
}

// PartLayout is one line's part: what it sets differently from the score.
// Zero values take the score's.
type PartLayout struct {
	Line      string
	Paper     Paper
	Staff     float64
	SystemGap float64
	// Breaks are the part's own breaks (the score's do not apply to it).
	Breaks []Break
	// NoMultiRests prints every rest bar on its own.
	NoMultiRests bool
}

// PaperSize is the page's width and height in millimetres, landscape
// turned.
func (p Paper) PaperSize() (w, h float64) {
	switch {
	case p.Name == "custom":
		w, h = p.Width, p.Height
	default:
		sz, ok := PaperSizes[p.Name]
		if !ok {
			sz = PaperSizes[DefaultPaper]
		}
		w, h = sz[0], sz[1]
	}
	if p.Landscape {
		w, h = h, w
	}
	return w, h
}

// MarginsOrDefault is the margins, the default where none are set.
func (p Paper) MarginsOrDefault() Margins {
	if p.Margins == (Margins{}) {
		return Margins{DefaultMargin, DefaultMargin, DefaultMargin, DefaultMargin}
	}
	return p.Margins
}

// TextMargins are the margins a page is laid out with: the paper's, made
// smaller where they would leave the music less than MinTextArea either
// way (Validate warns about those).
func (p Paper) TextMargins() Margins {
	w, h := p.PaperSize()
	m := p.MarginsOrDefault()
	fit := func(a, b *float64, size float64) {
		if over := *a + *b - (size - MinTextArea); over > 0 {
			if sum := *a + *b; sum > 0 {
				k := math.Max(0, size-MinTextArea) / sum
				*a, *b = *a*k, *b*k
			}
		}
	}
	fit(&m.Left, &m.Right, w)
	fit(&m.Top, &m.Bottom, h)
	return m
}

// roomy reports whether the paper's margins leave the music room.
func (p Paper) roomy() bool { return p.TextMargins() == p.MarginsOrDefault() }

// StaffOrDefault is the staff height in mm.
func (l Layout) StaffOrDefault() float64 {
	if l.Staff <= 0 {
		return DefaultStaff
	}
	return l.Staff
}

// SpacingOrDefault is the note spacing factor.
func (l Layout) SpacingOrDefault() float64 {
	if l.Spacing <= 0 {
		return 1
	}
	return l.Spacing
}

// SystemGapOrDefault is the least gap between systems, in staff spaces.
func (l Layout) SystemGapOrDefault() float64 {
	if l.SystemGap <= 0 {
		return DefaultSystemGap
	}
	return l.SystemGap
}

// BreakAt is the break asked for before a bar, if any.
func (l Layout) BreakAt(bar int) (Break, bool) { return BreakIn(l.Breaks, bar) }

// SetBreak sets (or, with none, removes) the break before a bar, keeping
// the breaks in bar order.
func (l *Layout) SetBreak(bar int, none, page bool) { l.Breaks = SetBreakIn(l.Breaks, bar, none, page) }

// SetBreak sets or removes a part's break before a bar.
func (p *PartLayout) SetBreak(bar int, none, page bool) {
	p.Breaks = SetBreakIn(p.Breaks, bar, none, page)
}

// SetBreakIn is breaks with the one before a bar set (or, with none,
// removed), in bar order; the slice passed in is not changed.
func SetBreakIn(bs []Break, bar int, none, page bool) []Break {
	out := bs[:0:0]
	for _, b := range bs {
		if b.Bar != bar {
			out = append(out, b)
		}
	}
	if !none {
		out = append(out, Break{Bar: bar, Page: page})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bar < out[j].Bar })
	return out
}

// BreakIn is the break before a bar in a list of breaks, if any.
func BreakIn(bs []Break, bar int) (Break, bool) {
	for _, b := range bs {
		if b.Bar == bar {
			return b, true
		}
	}
	return Break{}, false
}

// Part finds a line's part settings by the line's name, or nil.
func (l *Layout) Part(line string) *PartLayout {
	for i := range l.Parts {
		if strings.EqualFold(l.Parts[i].Line, line) {
			return &l.Parts[i]
		}
	}
	return nil
}

// EnsurePart is a line's part settings, made when it has none.
func (l *Layout) EnsurePart(line string) *PartLayout {
	if p := l.Part(line); p != nil {
		return p
	}
	l.Parts = append(l.Parts, PartLayout{Line: line})
	return &l.Parts[len(l.Parts)-1]
}

// ForPart is the layout a line's part is set with: the score's, with the
// part's own settings over it. The part's breaks replace the score's, and
// a part never has a title page.
func (l Layout) ForPart(line string) (Layout, bool) {
	out := l
	out.Parts = nil
	out.Breaks = nil
	out.TitlePage = false
	multi := true
	if p := l.Part(line); p != nil {
		if p.Paper.Name != "" || p.Paper.Width > 0 {
			out.Paper = p.Paper
		}
		if p.Staff > 0 {
			out.Staff = p.Staff
		}
		if p.SystemGap > 0 {
			out.SystemGap = p.SystemGap
		}
		out.Breaks = append([]Break(nil), p.Breaks...)
		multi = !p.NoMultiRests
	}
	return out, multi
}

// Clone copies a layout deeply.
func (l Layout) Clone() Layout {
	c := l
	c.Breaks = append([]Break(nil), l.Breaks...)
	if l.Parts != nil {
		c.Parts = make([]PartLayout, len(l.Parts))
		for i, p := range l.Parts {
			p.Breaks = append([]Break(nil), p.Breaks...)
			c.Parts[i] = p
		}
	}
	return c
}

// IsZero reports a layout with nothing set (the file leaves it out).
func (l Layout) IsZero() bool {
	return l.Paper == (Paper{}) && l.Staff == 0 && l.SystemGap == 0 && l.Spacing == 0 && len(l.Breaks) == 0 && !l.TitlePage &&
		!l.NoPageNumbers && l.Names == NamesFullThenShort && !l.NoCautionary && !l.Courtesy && len(l.Parts) == 0
}

// ShortName is a line's name on systems after the first: its own short
// name, or one made from the full name — common instrument words
// abbreviated as orchestral scores print them (Violin I → Vln. I), numbers
// and short words kept, any other word cut to three letters and a stop.
func (l *Line) ShortName() string {
	if l.Short != "" {
		return l.Short
	}
	return AbbreviateName(l.Name)
}

// AbbreviateName makes a short instrument name from a full one.
func AbbreviateName(name string) string {
	words := strings.Fields(name)
	var out []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		lw := strings.ToLower(strings.Trim(w, ".,"))
		if i+1 < len(words) {
			if a, ok := phraseAbbrev[lw+" "+strings.ToLower(words[i+1])]; ok {
				out = append(out, a)
				i++
				continue
			}
		}
		if a, ok := nameAbbrev[lw]; ok {
			if a != "" {
				out = append(out, a)
			}
			continue
		}
		switch {
		case lw == "in" && i+1 < len(words):
			// "Clarinet in Bb": the key stays, short.
			out = append(out, "in", words[i+1])
			i++
		case len([]rune(w)) <= 3 || isRoman(w) || strings.ContainsAny(w, "0123456789"):
			out = append(out, w)
		default:
			r := []rune(w)
			out = append(out, string(r[:3])+".")
		}
	}
	return strings.Join(out, " ")
}

func isRoman(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range w {
		if !strings.ContainsRune("IVXL", r) {
			return false
		}
	}
	return true
}

// phraseAbbrev are two-word names with their own abbreviation.
var phraseAbbrev = map[string]string{
	"double bass": "D.B.", "english horn": "E.H.", "cor anglais": "C.A.", "bass clarinet": "B. Cl.",
	"drum kit": "Dr.", "french horn": "Hn.", "bass drum": "B.D.", "snare drum": "S.D.", "electric bass": "El. Bass",
}

// nameAbbrev are the orchestral abbreviations (Gould, Behind Bars,
// "Instrument names").
var nameAbbrev = map[string]string{
	"violin": "Vln.", "violins": "Vlns.", "viola": "Vla.", "violas": "Vlas.", "violoncello": "Vc.", "cello": "Vc.", "cellos": "Vcs.",
	"contrabass": "Cb.", "double": "D.", "bass": "Bass", "basses": "Bs.", "flute": "Fl.", "flutes": "Fls.", "piccolo": "Picc.",
	"oboe": "Ob.", "oboes": "Obs.", "clarinet": "Cl.", "clarinets": "Cls.", "bassoon": "Bsn.", "bassoons": "Bsns.",
	"contrabassoon": "Cbsn.", "horn": "Hn.", "horns": "Hns.", "trumpet": "Tpt.", "trumpets": "Tpts.", "trombone": "Tbn.",
	"trombones": "Tbns.", "tuba": "Tba.", "piano": "Pno.", "harp": "Hp.", "timpani": "Timp.", "percussion": "Perc.",
	"drums": "Dr.", "drum": "Dr.", "kit": "Kit", "guitar": "Gtr.", "soprano": "S.", "alto": "A.", "tenor": "T.",
	"baritone": "Bar.", "choir": "Ch.", "organ": "Org.", "celesta": "Cel.", "glockenspiel": "Glock.", "xylophone": "Xyl.",
	"vibraphone": "Vib.", "marimba": "Mar.", "synth": "Synth.", "synthesizer": "Synth.", "strings": "Str.",
	"saxophone": "Sax.", "sax": "Sax.", "english": "E.", "cor": "C.", "anglais": "A.", "electric": "El.", "acoustic": "Ac.",
	"section": "", "lead": "Ld.", "pad": "Pad", "voice": "V.", "vocals": "Vox", "harpsichord": "Hpd.", "accordion": "Acc.",
}

// ---------- the file ----------

type rawLayout struct {
	Paper      string        `yaml:"paper,omitempty"`
	Size       []float64     `yaml:"size,flow,omitempty"`
	Landscape  bool          `yaml:"landscape,omitempty"`
	Margins    *rawMargins   `yaml:"margins,flow,omitempty"`
	Staff      float64       `yaml:"staff,omitempty"`
	SystemGap  float64       `yaml:"system_gap,omitempty"`
	Spacing    float64       `yaml:"spacing,omitempty"`
	Breaks     []rawBreak    `yaml:"breaks,flow,omitempty"`
	TitlePage  bool          `yaml:"title_page,omitempty"`
	PageNums   *bool         `yaml:"page_numbers,omitempty"`
	Names      string        `yaml:"names,omitempty"`
	Cautionary *bool         `yaml:"cautionary,omitempty"`
	Courtesy   bool          `yaml:"courtesy,omitempty"`
	Parts      []rawPartPage `yaml:"parts,omitempty"`
}

type rawMargins struct {
	Top    float64 `yaml:"top"`
	Bottom float64 `yaml:"bottom"`
	Left   float64 `yaml:"left"`
	Right  float64 `yaml:"right"`
}

type rawBreak struct {
	Bar  int  `yaml:"bar"`
	Page bool `yaml:"page,omitempty"`
}

type rawPartPage struct {
	Line       string      `yaml:"line"`
	Paper      string      `yaml:"paper,omitempty"`
	Size       []float64   `yaml:"size,flow,omitempty"`
	Landscape  bool        `yaml:"landscape,omitempty"`
	Margins    *rawMargins `yaml:"margins,flow,omitempty"`
	Staff      float64     `yaml:"staff,omitempty"`
	SystemGap  float64     `yaml:"system_gap,omitempty"`
	Breaks     []rawBreak  `yaml:"breaks,flow,omitempty"`
	MultiRests *bool       `yaml:"multi_rests,omitempty"`
}

func paperFromRaw(name string, size []float64, landscape bool, m *rawMargins) (Paper, error) {
	p := Paper{Name: strings.ToLower(name), Landscape: landscape}
	if p.Name != "" && p.Name != "custom" {
		if _, ok := PaperSizes[p.Name]; !ok {
			return p, fmt.Errorf("paper %q (want %s)", name, strings.Join(PaperNames, ", "))
		}
	}
	if len(size) > 0 {
		if len(size) != 2 || size[0] < 50 || size[1] < 50 || size[0] > 2000 || size[1] > 2000 {
			return p, fmt.Errorf("size %v: want [width, height] in mm, 50 to 2000", size)
		}
		if p.Name == "" {
			p.Name = "custom"
		}
		if p.Name != "custom" {
			return p, fmt.Errorf("size is for paper: custom (paper is %q)", p.Name)
		}
		p.Width, p.Height = size[0], size[1]
	} else if p.Name == "custom" {
		return p, fmt.Errorf("paper: custom needs size: [width, height] in mm")
	}
	if m != nil {
		p.Margins = Margins{m.Top, m.Bottom, m.Left, m.Right}
		for _, v := range []float64{m.Top, m.Bottom, m.Left, m.Right} {
			if v < 0 || v > 100 {
				return p, fmt.Errorf("margins %v: each 0 to 100 mm", *m)
			}
		}
	}
	return p, nil
}

func paperToRaw(p Paper) (name string, size []float64, landscape bool, m *rawMargins) {
	name, landscape = p.Name, p.Landscape
	if p.Name == "custom" {
		size = []float64{p.Width, p.Height}
	}
	if p.Margins != (Margins{}) {
		m = &rawMargins{p.Margins.Top, p.Margins.Bottom, p.Margins.Left, p.Margins.Right}
	}
	return
}

func breaksFromRaw(rb []rawBreak) ([]Break, error) {
	var out []Break
	for _, b := range rb {
		if b.Bar < 2 {
			return nil, fmt.Errorf("break at bar %d: a break goes before a bar from 2 on", b.Bar)
		}
		out = append(out, Break{Bar: b.Bar, Page: b.Page})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bar < out[j].Bar })
	for i := 1; i < len(out); i++ {
		if out[i].Bar == out[i-1].Bar {
			return nil, fmt.Errorf("two breaks at bar %d", out[i].Bar)
		}
	}
	return out, nil
}

func breaksToRaw(bs []Break) []rawBreak {
	var out []rawBreak
	for _, b := range bs {
		out = append(out, rawBreak{Bar: b.Bar, Page: b.Page})
	}
	return out
}

func checkStaff(v float64) error {
	if v != 0 && (v < MinStaff || v > MaxStaff) {
		return fmt.Errorf("staff %g mm: %g to %g", v, MinStaff, MaxStaff)
	}
	return nil
}

func checkGap(v float64) error {
	if v < 0 || v > 60 {
		return fmt.Errorf("system_gap %g: 0 to 60 staff spaces", v)
	}
	return nil
}

func layoutFromRaw(r *rawLayout) (Layout, error) {
	var l Layout
	if r == nil {
		return l, nil
	}
	p, err := paperFromRaw(r.Paper, r.Size, r.Landscape, r.Margins)
	if err != nil {
		return l, fmt.Errorf("layout: %w", err)
	}
	l.Paper = p
	if err := checkStaff(r.Staff); err != nil {
		return l, fmt.Errorf("layout: %w", err)
	}
	if err := checkGap(r.SystemGap); err != nil {
		return l, fmt.Errorf("layout: %w", err)
	}
	if r.Spacing != 0 && (r.Spacing < MinSpacing || r.Spacing > MaxSpacing) {
		return l, fmt.Errorf("layout: spacing %g: %g to %g (1 is the default)", r.Spacing, MinSpacing, MaxSpacing)
	}
	l.Staff, l.SystemGap, l.Spacing = r.Staff, r.SystemGap, r.Spacing
	if l.Breaks, err = breaksFromRaw(r.Breaks); err != nil {
		return l, fmt.Errorf("layout: %w", err)
	}
	l.TitlePage, l.Courtesy = r.TitlePage, r.Courtesy
	l.NoPageNumbers = r.PageNums != nil && !*r.PageNums
	l.NoCautionary = r.Cautionary != nil && !*r.Cautionary
	if r.Names != "" {
		found := false
		for i, n := range NameModes {
			if n == r.Names {
				l.Names, found = NameMode(i), true
			}
		}
		if !found {
			return l, fmt.Errorf("layout: names %q (want %s)", r.Names, strings.Join(NameModes, ", "))
		}
	}
	for _, rp := range r.Parts {
		if rp.Line == "" {
			return l, fmt.Errorf("layout: a part needs line: (the line's name)")
		}
		pp, err := paperFromRaw(rp.Paper, rp.Size, rp.Landscape, rp.Margins)
		if err != nil {
			return l, fmt.Errorf("layout: part %q: %w", rp.Line, err)
		}
		if err := checkStaff(rp.Staff); err != nil {
			return l, fmt.Errorf("layout: part %q: %w", rp.Line, err)
		}
		if err := checkGap(rp.SystemGap); err != nil {
			return l, fmt.Errorf("layout: part %q: %w", rp.Line, err)
		}
		bs, err := breaksFromRaw(rp.Breaks)
		if err != nil {
			return l, fmt.Errorf("layout: part %q: %w", rp.Line, err)
		}
		if l.Part(rp.Line) != nil {
			return l, fmt.Errorf("layout: two parts for line %q", rp.Line)
		}
		l.Parts = append(l.Parts, PartLayout{Line: rp.Line, Paper: pp, Staff: rp.Staff, SystemGap: rp.SystemGap, Breaks: bs,
			NoMultiRests: rp.MultiRests != nil && !*rp.MultiRests})
	}
	return l, nil
}

func layoutToRaw(l Layout) *rawLayout {
	if l.IsZero() {
		return nil
	}
	r := &rawLayout{Staff: l.Staff, SystemGap: l.SystemGap, Spacing: l.Spacing, Breaks: breaksToRaw(l.Breaks), TitlePage: l.TitlePage, Courtesy: l.Courtesy}
	r.Paper, r.Size, r.Landscape, r.Margins = paperToRaw(l.Paper)
	if l.NoPageNumbers {
		f := false
		r.PageNums = &f
	}
	if l.NoCautionary {
		f := false
		r.Cautionary = &f
	}
	if l.Names != NamesFullThenShort && int(l.Names) < len(NameModes) {
		r.Names = NameModes[l.Names]
	}
	for _, p := range l.Parts {
		rp := rawPartPage{Line: p.Line, Staff: p.Staff, SystemGap: p.SystemGap, Breaks: breaksToRaw(p.Breaks)}
		rp.Paper, rp.Size, rp.Landscape, rp.Margins = paperToRaw(p.Paper)
		if p.NoMultiRests {
			f := false
			rp.MultiRests = &f
		}
		r.Parts = append(r.Parts, rp)
	}
	return r
}

// validateLayout warns about breaks past the end and parts naming no line.
func (s *Score) validateLayout(warn func(string, ...any)) {
	n := s.Measures()
	check := func(what string, bs []Break) {
		for _, b := range bs {
			if b.Bar > n {
				warn("%sbreak before bar %d: the score has %d bars (it does nothing)", what, b.Bar, n)
			}
		}
	}
	check("", s.Layout.Breaks)
	if !s.Layout.Paper.roomy() {
		warn("layout: the margins leave the music less than %g mm of the page; they are made smaller", MinTextArea)
	}
	for _, p := range s.Layout.Parts {
		if s.LineIndex(p.Line) < 0 {
			warn("layout: part %q names no line", p.Line)
		}
		if lay, _ := s.Layout.ForPart(p.Line); !lay.Paper.roomy() {
			warn("layout: part %q: the margins leave the music less than %g mm of the page; they are made smaller", p.Line, MinTextArea)
		}
		check(fmt.Sprintf("part %q: ", p.Line), p.Breaks)
	}
}
