package engrave

import (
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/glyph"
)

func loadFixture(t *testing.T, name string) *score.Score {
	t.Helper()
	path := filepath.Join("testdata", name+score.FileExt)
	if name == "demo" {
		path = filepath.Join("..", "..", "..", "resources", "sounds", "score_demo.fscore")
	}
	s, err := score.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// checkBook checks what every page layout must be: every bar on exactly
// one system, in order; systems inside the margins and justified to the
// measure (the last one at most to it); systems on a page apart by at
// least the system gap and their ink; names and page numbers where they
// go.
func checkBook(t *testing.T, s *score.Score, b *Book, lay score.Layout) {
	t.Helper()
	m := lay.Paper.MarginsOrDefault()
	ml, mr, mt, mb := m.Left/b.Space, m.Right/b.Space, m.Top/b.Space, m.Bottom/b.Space
	next := 1
	var systems []*System
	for pi, pg := range b.Pages {
		if pg.Number != pi+1 {
			t.Errorf("page %d numbered %d", pi+1, pg.Number)
		}
		for si, sys := range pg.Systems {
			systems = append(systems, sys)
			if sys.From != next || sys.To < sys.From {
				t.Fatalf("page %d system %d holds bars %d-%d, want from %d", pi+1, si+1, sys.From, sys.To, next)
			}
			next = sys.To + 1
			l := sys.List
			left, right := sys.At.X+l.Origin.X, sys.At.X+l.End
			if right > b.Width-mr+0.05 {
				t.Errorf("bars %d-%d end at %.2f, past the right margin %.2f", sys.From, sys.To, right, b.Width-mr)
			}
			ink := l.Bounds().Offset(sys.At)
			// A rehearsal box at a part's system start strokes its edge a
			// hair over the barline, into the margin.
			if ink.Min.X < ml-0.15 {
				what := ""
				for i := range l.Items {
					if b := ItemBounds(&l.Items[i]); b.Min.X+sys.At.X < ml-0.15 {
						what += fmt.Sprintf(" %v %q", l.Items[i].Role, l.Items[i].Glyph+l.Items[i].Text)
					}
				}
				t.Errorf("bars %d-%d: ink at %.2f, left of the margin %.2f:%s", sys.From, sys.To, ink.Min.X, ml, what)
			}
			// Only a system alone on its page at the smallest staff may
			// run past the bottom: Paginate shrinks the staff until it fits.
			if ink.Max.Y > b.Height-mb+0.05 && (len(pg.Systems) > 1 || b.Staff > score.MinStaff) {
				t.Errorf("page %d: bars %d-%d run past the bottom margin (%.1f > %.1f)", pi+1, sys.From, sys.To, ink.Max.Y, b.Height-mb)
			}
			if ink.Min.Y < mt-0.05 {
				t.Errorf("page %d: bars %d-%d run into the top margin (%.1f < %.1f)", pi+1, sys.From, sys.To, ink.Min.Y, mt)
			}
			if si > 0 {
				p := pg.Systems[si-1]
				pInk := p.List.Bounds().Offset(p.At)
				if ink.Min.Y < pInk.Max.Y {
					t.Errorf("page %d: bars %d-%d overlap the system above", pi+1, sys.From, sys.To)
				}
				pBottom := p.At.Y + p.List.Staves[len(p.List.Staves)-1].Top + 4
				top := sys.At.Y + l.Staves[0].Top
				if top-pBottom < lay.SystemGapOrDefault()-0.01 {
					t.Errorf("page %d: %.2f spaces between systems, want at least %.2f", pi+1, top-pBottom, lay.SystemGapOrDefault())
				}
			}
			_ = left
		}
	}
	if next != s.Measures()+1 {
		t.Errorf("the systems end at bar %d of %d", next-1, s.Measures())
	}
	for i, sys := range systems {
		w := sys.List.End - sys.List.Origin.X
		avail := b.Width - ml - mr - (sys.At.X + sys.List.Origin.X - ml)
		if i < len(systems)-1 && math.Abs(w-avail) > 0.05 && sys.To > sys.From {
			t.Errorf("bars %d-%d are %.2f wide, not justified to %.2f", sys.From, sys.To, w, avail)
		}
	}
}

func TestTheDemoOnPages(t *testing.T) {
	s := loadFixture(t, "demo")
	b := ScoreBook(s, false)
	checkBook(t, s, b, s.Layout)
	if b.Space != 7.0/4 || math.Abs(b.Width-215.9/1.75) > 1e-9 {
		t.Errorf("space %g, width %g", b.Space, b.Width)
	}
	// Full names on the first system, short ones after.
	names := map[int][]string{}
	for _, pg := range b.Pages {
		for _, it := range pg.Items {
			if it.Role == RoleName {
				names[it.Ref.Bar] = append(names[it.Ref.Bar], it.Text)
			}
		}
	}
	// A transposing line's full name says what it is in.
	if got := strings.Join(names[1], ","); got != "Flute,Horn in F,Violins,Cello,Piano" {
		t.Errorf("first system's names %q", got)
	}
	_, second := b.Find(b.Pages[0].Systems[len(b.Pages[0].Systems)-1].To + 1)
	if got := strings.Join(names[second.From], ","); got != "Fl.,Hn.,Vlns.,Vc.,Pno." {
		t.Errorf("later names %q", got)
	}
	// The title on page 1, page numbers from page 2 at the outer corner.
	var title bool
	for _, it := range b.Pages[0].Items {
		title = title || it.Role == RoleTitle && it.Text == "Score Demo"
		if it.Role == RolePageNumber {
			t.Errorf("page 1 is numbered")
		}
	}
	if !title {
		t.Errorf("no title on page 1")
	}
	for _, pg := range b.Pages[1:] {
		found := false
		for _, it := range pg.Items {
			if it.Role == RolePageNumber {
				found = true
				right := it.Align == AlignRight
				if it.Text != fmt.Sprint(pg.Number) || right != (pg.Number%2 == 1) {
					t.Errorf("page %d numbered %q (right %v)", pg.Number, it.Text, right)
				}
			}
		}
		if !found {
			t.Errorf("page %d has no number", pg.Number)
		}
	}
	// A system's own list maps a pointer as the continuous view's does.
	pg, sys := b.Find(5)
	if pg == nil || sys.List.Bar(5) == nil {
		t.Fatalf("bar 5 not found on a page")
	}
	at := sys.At.Add(Point{X: sys.List.Bar(5).X1 - 1, Y: sys.List.Staves[0].Top + 2})
	if got := pg.SystemAt(at); got != sys {
		t.Errorf("SystemAt found bars %d-%d", got.From, got.To)
	}
	// The flattened page holds every system's items.
	n := len(pg.Items)
	for _, sy := range pg.Systems {
		n += len(sy.List.Items)
	}
	if l := b.List(pg); len(l.Items) != n || l.Width != b.Width {
		t.Errorf("flattened page: %d items of %d", len(l.Items), n)
	}
}

func TestBreaksAreKept(t *testing.T) {
	s := loadFixture(t, "pages")
	b := ScoreBook(s, false)
	checkBook(t, s, b, s.Layout)
	starts := map[int]*Page{}
	for _, pg := range b.Pages {
		for i, sys := range pg.Systems {
			starts[sys.From] = pg
			if i == 0 && sys.From == 17 {
				starts[-17] = pg
			}
		}
	}
	for _, bar := range []int{9, 13, 17} {
		if starts[bar] == nil {
			t.Errorf("no system starts at bar %d", bar)
		}
	}
	if starts[-17] == nil || starts[17] == starts[13] {
		t.Errorf("bar 17 does not start a page")
	}
	// Removing the breaks lets the systems run on.
	plain := s.Clone()
	plain.Layout.Breaks = nil
	pb := ScoreBook(plain, false)
	checkBook(t, plain, pb, plain.Layout)
	if len(pb.Pages) >= len(b.Pages) && countSystems(pb) >= countSystems(b) {
		t.Errorf("without breaks: %d pages, %d systems (with: %d, %d)", len(pb.Pages), countSystems(pb), len(b.Pages), countSystems(b))
	}
}

func countSystems(b *Book) int {
	n := 0
	for _, pg := range b.Pages {
		n += len(pg.Systems)
	}
	return n
}

// TestCautionaries checks the signatures a system shows at its end for the
// next one: a key after a double barline, a time, a clef before the
// barline; and none when the layout says no.
func TestCautionaries(t *testing.T) {
	s := loadFixture(t, "pages")
	b := ScoreBook(s, false)
	_, sys1 := b.Find(8)
	_, sys2 := b.Find(12)
	_, sys3 := b.Find(16)
	if sys1.To != 8 || sys2.To != 12 || sys3.To != 16 {
		t.Fatalf("systems end at %d, %d, %d", sys1.To, sys2.To, sys3.To)
	}
	count := func(l *List, role Role, bar int) int {
		n := 0
		for _, it := range l.Items {
			if it.Role == role && it.Ref.Bar == bar {
				n++
			}
		}
		return n
	}
	// Bar 9: E flat major (three flats in the piano, the clarinet's F
	// major after cancelling its D major's two sharps).
	if n := count(sys1.List, RoleKey, 9); n != 3+3+3 {
		t.Errorf("%d cautionary key accidentals at the end of bars 1-8, want 9", n)
	}
	doubles := 0
	for _, it := range sys1.List.Items {
		if it.Role == RoleBarline && it.Ref.Bar == 8 && it.Ref.Staff == 0 && it.Ref.Line == 1 {
			doubles++
		}
	}
	if doubles != 2 {
		t.Errorf("the barline before the key change has %d strokes, want a double", doubles)
	}
	if n := count(sys2.List, RoleTime, 13); n == 0 {
		t.Errorf("no cautionary 3/4 at the end of bars 9-12")
	}
	clefs := 0
	for _, it := range sys3.List.Items {
		if it.Role == RoleClef && it.Ref.Bar == 16 && it.Glyph == "gClefChange" {
			clefs++
			if it.P.X >= sys3.List.Bar(16).X1 {
				t.Errorf("the cautionary clef is after the barline")
			}
		}
	}
	if clefs != 1 {
		t.Errorf("%d cautionary clefs at the end of bars 13-16", clefs)
	}
	off := s.Clone()
	off.Layout.NoCautionary = true
	ob := ScoreBook(off, false)
	_, o1 := ob.Find(8)
	if n := count(o1.List, RoleKey, 9); n != 0 {
		t.Errorf("cautionary: false still shows %d accidentals", n)
	}
}

func TestCourtesyAccidentals(t *testing.T) {
	s := loadFixture(t, "pages")
	parens := func(courtesy bool) int {
		l := Engrave(s, Options{From: 1, To: 2, Lines: []int{0}, Courtesy: courtesy})
		n := 0
		for _, it := range l.Items {
			if it.Glyph == "accidentalParensLeft" && it.Ref.Bar == 2 {
				n++
			}
		}
		return n
	}
	if parens(false) != 0 || parens(true) != 1 {
		t.Errorf("courtesy natural in bar 2: %d without, %d with", parens(false), parens(true))
	}
	// From the bar before the layout too.
	l := Engrave(s, Options{From: 2, To: 2, Lines: []int{0}, Courtesy: true})
	n := 0
	for _, it := range l.Items {
		if it.Glyph == "accidentalParensLeft" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("courtesy from the bar before the layout: %d", n)
	}
}

func TestPartsGatherRests(t *testing.T) {
	s := loadFixture(t, "pages")
	b := PartBook(s, 0, false)
	lay, _ := s.Layout.ForPart(s.Lines[0].Name)
	checkBook(t, s, b, lay)
	// Bars 3-8 rest, broken by rehearsal mark A at bar 6; 19-22 rest.
	if got := strings.Join(multiRests(PartBook(s, 0, false)), " "); got != "3:3 6:3 19:4" {
		t.Errorf("multi-bar rests %q, want 3:3 6:3 19:4", got)
	}
	// No names on a part's systems; its name heads the first page.
	part := false
	for _, it := range b.Pages[0].Items {
		if it.Role == RoleName {
			t.Errorf("a part shows instrument names")
		}
		part = part || it.Role == RoleTitle && it.Text == "Clarinet in Bb"
	}
	if !part {
		t.Errorf("the part's name is not on its first page")
	}
	// A part break inside a run splits it (bar 3 is then a bar rest of
	// its own); multi_rests: false prints every bar.
	s2 := s.Clone()
	s2.Layout.EnsurePart("Clarinet in Bb").Breaks = []score.Break{{Bar: 4}}
	if got := strings.Join(multiRests(PartBook(s2, 0, false)), " "); got != "4:2 6:3 19:4" {
		t.Errorf("multi-bar rests with a break at 4: %q", got)
	}
	_, sys := PartBook(s2, 0, false).Find(4)
	if sys.From != 4 {
		t.Errorf("the part break at 4 is not kept: bars %d-%d", sys.From, sys.To)
	}
	s2.Layout.EnsurePart("Clarinet in Bb").NoMultiRests = true
	if got := multiRests(PartBook(s2, 0, false)); len(got) != 0 {
		t.Errorf("multi_rests: false still gathers %q", got)
	}
	// The full score never gathers them.
	if got := multiRests(ScoreBook(s, false)); len(got) != 0 {
		t.Errorf("the score gathers %q", got)
	}
}

// multiRests are a book's multi-bar rests, as bar:count.
func multiRests(b *Book) []string {
	var runs []string
	for _, pg := range b.Pages {
		for _, sys := range pg.Systems {
			for _, it := range sys.List.Items {
				if it.Role == RoleRest && strings.HasPrefix(it.Glyph, "timeSig") {
					runs = append(runs, fmt.Sprintf("%d:%s", it.Ref.Bar, strings.TrimPrefix(it.Glyph, "timeSig")))
				}
			}
		}
	}
	return runs
}

func TestTitlePageAndPaper(t *testing.T) {
	s := loadFixture(t, "demo")
	s.Subtitle = "for the hangar"
	s.Layout = score.Layout{TitlePage: true, Paper: score.Paper{Name: "a4", Landscape: true}, Staff: 5}
	b := ScoreBook(s, false)
	checkBook(t, s, b, s.Layout)
	if !b.Pages[0].TitlePage || len(b.Pages[0].Systems) != 0 {
		t.Fatalf("page 1 is not a title page")
	}
	var texts []string
	for _, it := range b.Pages[0].Items {
		texts = append(texts, it.Text)
	}
	if strings.Join(texts, "|") != "Score Demo|for the hangar|Foundry" {
		t.Errorf("title page %q", texts)
	}
	if math.Abs(b.Width*b.Space-297) > 1e-6 || math.Abs(b.Height*b.Space-210) > 1e-6 {
		t.Errorf("A4 landscape is %gx%g mm", b.Width*b.Space, b.Height*b.Space)
	}
	// Smaller staves on a wider page: more bars to a system than the
	// default layout.
	d := ScoreBook(loadFixture(t, "demo"), false)
	if countSystems(b) >= countSystems(d) {
		t.Errorf("%d systems at 5 mm on A4 landscape, %d at 7 mm on letter", countSystems(b), countSystems(d))
	}
	s.Layout.NoPageNumbers = true
	for _, pg := range ScoreBook(s, false).Pages {
		for _, it := range pg.Items {
			if it.Role == RolePageNumber {
				t.Errorf("page_numbers: false still numbers page %d", pg.Number)
			}
		}
	}
}

func TestBreakingFillsSystems(t *testing.T) {
	// A long single line: every system but the last at least 60% full at
	// its natural width, and the breaker's choice no worse than greedy
	// filling.
	s := score.New("x")
	l := score.NewLine("Flute", "woodwinds/flute", "treble")
	s.Lines = []*score.Line{l}
	var bars []string
	vals := []string{"qC5 qD5 qE5 qF5", "hG5 qA5 qB5", "eC6 eB5 eA5 eG5 hF5", "wE5", "q.D5 eC5 hB4", "sC5 sD5 sE5 sF5 sG5 sA5 sB5 sC6 hD6"}
	for i := 0; i < 60; i++ {
		bars = append(bars, vals[(i*7)%len(vals)])
	}
	v, err := score.ParseVoice("| " + strings.Join(bars, " | ") + " |")
	if err != nil {
		t.Fatal(err)
	}
	l.Staves[0].Voices = []*score.Voice{v}
	b := ScoreBook(s, false)
	checkBook(t, s, b, s.Layout)
	p := &pager{s: s, o: PageOptions{Layout: s.Layout}, b: b}
	m := s.Layout.Paper.MarginsOrDefault()
	p.ml, p.mr = m.Left/b.Space, m.Right/b.Space
	p.lines = []int{0}
	p.names()
	ms := p.metrics()
	systems := 0
	for _, pg := range b.Pages {
		for _, sys := range pg.Systems {
			systems++
			nat := ms[sys.From-1].first + ms[sys.To-1].end
			for bi := sys.From - 1; bi < sys.To; bi++ {
				if bi > sys.From-1 {
					nat += ms[bi].mid
				}
				for g := range ms[bi].rods {
					nat += math.Max(ms[bi].rods[g], ms[bi].springs[g])
				}
			}
			if sys.To < s.Measures() && nat < 0.6*p.measureWidth(sys.From == 1) {
				t.Errorf("bars %d-%d fill %.0f%% of the measure", sys.From, sys.To, 100*nat/p.measureWidth(sys.From == 1))
			}
		}
	}
	if systems < 6 {
		t.Errorf("60 bars in %d systems", systems)
	}
}

// Text drawn as outlines (the PDF and SVG writers) is set as it is
// measured: the last letter ends where the run's width says, capitals rise
// above the baseline (y down), kerning applied.
func TestTextGlyphsMatchTheirWidth(t *testing.T) {
	for _, f := range []TextFont{TextPlain, TextItalic, TextBold, TextBoldItalic} {
		gs := TextGlyphs("Andante con moto", f)
		if len(gs) != 14 { // no outlines for the two spaces
			t.Fatalf("font %d: %d glyphs", f, len(gs))
		}
		// The last letter starts where the rest of the run measures to
		// (kerning, if the font has any, applies to both).
		last := gs[len(gs)-1]
		if w := TextWidthFont("Andante con mot", f, 1); math.Abs(last.X-w) > 0.05 {
			t.Errorf("font %d: the last letter at %.3f em, the run before it measures %.3f", f, last.X, w)
		}
		if a := pathBounds(gs[0].Path); a.Min.Y > -0.6 || a.Max.Y > 0.05 {
			t.Errorf("font %d: 'A' spans %.2f..%.2f em (want above the baseline, y down)", f, a.Min.Y, a.Max.Y)
		}
	}
	// A pair is set as the measure sets it.
	av := TextGlyphs("AV", TextPlain)
	if want := TextWidthFont("AV", TextPlain, 1) - TextWidthFont("V", TextPlain, 1); math.Abs(av[1].X-want) > 1e-6 {
		t.Errorf("V of AV at %.4f em, measured %.4f", av[1].X, want)
	}
	it := &Item{Kind: ItemText, Text: "Flute", Size: 2, P: Point{X: 10}, Align: AlignRight}
	if x := TextStart(it); math.Abs(x+TextWidth("Flute", 2)-10) > 1e-9 {
		t.Errorf("right-aligned text starts at %g", x)
	}
}

func pathBounds(p []glyph.Segment) Rect {
	it := Item{Kind: ItemPath, Path: p}
	return ItemBounds(&it)
}

// TestEveryScorePaginates lays out every fixture and shipped score, and
// random music (long, with rest bars for multi-bar rests, meter and key
// changes) on random paper, as full scores and as parts: every bar on a
// system, nothing past the margins, items finite.
func TestEveryScorePaginates(t *testing.T) {
	var paths []string
	fx, _ := filepath.Glob(filepath.Join("testdata", "*"+score.FileExt))
	paths = append(paths, fx...)
	shipped, _ := filepath.Glob(filepath.Join("..", "..", "..", "resources", "sounds", "*"+score.FileExt))
	paths = append(paths, shipped...)
	var scores []*score.Score
	for _, p := range paths {
		s, err := score.LoadFile(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		scores = append(scores, s)
	}
	r := rand.New(rand.NewSource(11))
	for i := 0; i < 40; i++ {
		s := randomScore(r)
		// Longer: the bars again, with rest bars and changes between.
		v := s.Lines[0].Staves[0].Voices[0]
		n := len(v.Measures)
		for k := 0; k < 6; k++ {
			for b := 0; b < n; b++ {
				v.Measures = append(v.Measures, v.Measures[b].Clone())
			}
			if r.Intn(2) == 0 {
				for j := 0; j < 1+r.Intn(5); j++ {
					v.Measures = append(v.Measures, score.Measure{Events: []score.Event{{Kind: score.EventBarRest}}})
				}
			}
		}
		for _, other := range s.Lines[0].Staves[0].Voices[1:] {
			other.Measures = nil
		}
		s.Bars = len(v.Measures)
		s.Key = append(s.Key, score.KeyMark{Bar: 1 + s.Bars/2, Fifths: r.Intn(15) - 7})
		papers := []string{"letter", "a4", "tabloid", "a3"}
		s.Layout = score.Layout{Paper: score.Paper{Name: papers[r.Intn(len(papers))], Landscape: r.Intn(3) == 0},
			Staff: 4 + r.Float64()*6, Courtesy: r.Intn(2) == 0, TitlePage: r.Intn(4) == 0}
		if r.Intn(2) == 0 {
			s.Layout.Breaks = []score.Break{{Bar: 2 + r.Intn(s.Bars-1), Page: r.Intn(2) == 0}}
		}
		scores = append(scores, s)
	}
	for _, s := range scores {
		for _, b := range []*Book{ScoreBook(s, false), PartBook(s, 0, false)} {
			lay := s.Layout
			if b.Part != "" {
				lay, _ = s.Layout.ForPart(b.Part)
			}
			checkBook(t, s, b, lay)
			for _, pg := range b.Pages {
				checkList(t, s.Title, b.List(pg))
			}
		}
	}
}

// A name or title longer than the page prints smaller: the names keep to
// their column (a quarter of the text width on the first system, a sixth
// after), so the music still has the page, and every text stays inside
// the paper.
func TestLongNamesAndTitlesFit(t *testing.T) {
	s := score.New("long")
	s.Title = strings.Repeat("A Very Long Title Indeed ", 6)
	s.Composer = strings.Repeat("Somebody Hyphenated-Smythe ", 4)
	s.Bars = 24
	long := score.NewLine(strings.Repeat("Contrabassoon in a very long name ", 3), "woodwinds/clarinet", "bass")
	s.Lines = []*score.Line{long, score.NewLine("Flute", "woodwinds/flute", "treble")}
	for _, part := range []int{-1, 0} {
		var b *Book
		if part < 0 {
			b = ScoreBook(s, false)
		} else {
			b = PartBook(s, part, false)
		}
		checkBook(t, s, b, s.Layout)
		m := s.Layout.Paper.MarginsOrDefault()
		ml, mr := m.Left/b.Space, m.Right/b.Space
		text := b.Width - ml - mr
		for _, pg := range b.Pages {
			for si, sys := range pg.Systems {
				col := text / 6
				if pg.Number == 1 && si == 0 {
					col = text / 4
				}
				if open := sys.At.X + sys.List.Origin.X - ml; open > col+nameGap+braceRoom+0.5 {
					t.Errorf("part %d page %d system %d opens %.1f spaces in (the name column is %.1f)", part, pg.Number, si, open, col)
				}
			}
			l := b.List(pg)
			for _, it := range l.Items {
				if it.Kind != ItemText {
					continue
				}
				x0 := TextStart(&it)
				x1 := x0 + TextWidthFont(it.Text, it.Font, it.Size)
				if x0 < -0.01 || x1 > b.Width+0.01 {
					t.Errorf("part %d page %d: %q runs off the paper (%.1f..%.1f of %.1f)", part, pg.Number, it.Text, x0, x1, b.Width)
				}
			}
		}
	}
}

// The layout's note spacing: tighter fits more bars a system, looser
// fewer, and every system is still justified to the measure.
func TestLayoutSpacing(t *testing.T) {
	s := loadFixture(t, "demo")
	systems := map[float64]int{}
	for _, k := range []float64{0.6, 1, 1.6} {
		lay := s.Layout
		lay.Spacing = k
		b := Paginate(s, PageOptions{Layout: lay})
		checkBook(t, s, b, lay)
		systems[k] = countSystems(b)
	}
	if !(systems[0.6] < systems[1] && systems[1] < systems[1.6]) {
		t.Errorf("systems by spacing: %v", systems)
	}
	// An option's spacing wins over the layout's.
	lay := s.Layout
	lay.Spacing = 1.6
	if a, b := countSystems(Paginate(s, PageOptions{Layout: lay, Spacing: 0.6})), systems[0.6]; a != b {
		t.Errorf("the option's 0.6 gave %d systems, the layout's %d", a, b)
	}
}

// A system taller than the page — twenty-four staves on letter at the
// default 7 mm — is set at a smaller staff so it fits, and the book says
// so; a score that fits keeps its staff.
func TestTallSystemsShrinkTheStaff(t *testing.T) {
	s := score.New("tall")
	s.Bars = 12
	for i := 0; i < 24; i++ {
		l := score.NewLine(fmt.Sprintf("Line %d", i+1), "keys/grand_piano", "treble")
		v, err := score.ParseVoice(strings.Repeat("| qC5 qD5(f) qE5 qF5 ", 12) + "|")
		if err != nil {
			t.Fatal(err)
		}
		l.Staves[0].Voices = []*score.Voice{v}
		s.Lines = append(s.Lines, l)
	}
	b := ScoreBook(s, false)
	if !b.Reduced || b.Staff >= score.DefaultStaff || b.Staff < score.MinStaff {
		t.Fatalf("reduced %v at %.1f mm", b.Reduced, b.Staff)
	}
	if math.Abs(b.Space*4-b.Staff) > 1e-9 {
		t.Errorf("space %.3f mm for a %.1f mm staff", b.Space, b.Staff)
	}
	checkBook(t, s, b, s.Layout)
	s.Lines = s.Lines[:4]
	if b := ScoreBook(s, false); b.Reduced || b.Staff != score.DefaultStaff {
		t.Errorf("four lines reduced to %.1f mm", b.Staff)
	}
	// More than the smallest staff can hold: as small as it goes, no loop.
	for i := 0; i < 80; i++ {
		s.Lines = append(s.Lines, s.Lines[i%4].Clone())
	}
	if b := ScoreBook(s, false); b.Staff != score.MinStaff {
		t.Errorf("eighty-four lines at %.1f mm", b.Staff)
	}
}

// A page's systems set their staves the same distance apart: the most any
// of them needs, so a line keeps its place down the page.
func TestStaffSpacingIsUniformOnAPage(t *testing.T) {
	s := score.New("t")
	s.Bars = 8
	s.Layout.Breaks = []score.Break{{Bar: 3}, {Bar: 5}, {Bar: 7}}
	hi := score.NewLine("Flute", "woodwinds/flute", "treble")
	lo := score.NewLine("Oboe", "woodwinds/oboe", "treble")
	// The oboe climbs high in bars 3-4 only: that system needs more room
	// between the staves than the others.
	fv, _ := score.ParseVoice("| wC5 | wC5 | wC5 | wC5 | wC5 | wC5 | wC5 | wC5 |")
	ov, _ := score.ParseVoice("| wC5 | wC5 | wC7 | wD7 | wC5 | wC5 | wC5 | wC5 |")
	hi.Staves[0].Voices = []*score.Voice{fv}
	lo.Staves[0].Voices = []*score.Voice{ov}
	s.Lines = []*score.Line{hi, lo}
	b := ScoreBook(s, false)
	checkBook(t, s, b, s.Layout)
	if len(b.Pages) != 1 || len(b.Pages[0].Systems) != 4 {
		t.Fatalf("%d pages, %d systems on the first", len(b.Pages), len(b.Pages[0].Systems))
	}
	var steps []float64
	for _, sys := range b.Pages[0].Systems {
		st := sys.List.Staves
		steps = append(steps, st[1].Top-st[0].Top)
	}
	for i := 1; i < len(steps); i++ {
		if math.Abs(steps[i]-steps[0]) > 1e-9 {
			t.Errorf("staff steps differ down the page: %v", steps)
			break
		}
	}
	// And they are the high system's, not the others' natural distance.
	natural := Engrave(s, Options{From: 1, To: 2})
	if d := natural.Staves[1].Top - natural.Staves[0].Top; steps[0] <= d+0.5 {
		t.Errorf("uniform step %v is not the high system's (a plain one's is %v)", steps[0], d)
	}
}

// With hide_empty a system after the first leaves out the lines with no
// notes in its bars; the first shows every line; without it every system
// does.
func TestHideEmptyStaves(t *testing.T) {
	s := score.New("t")
	s.Bars = 6
	s.Layout.Breaks = []score.Break{{Bar: 3}, {Bar: 5}}
	s.Layout.HideEmpty = true
	mk := func(name, inst, text string) *score.Line {
		l := score.NewLine(name, inst, "treble")
		v, err := score.ParseVoice(text)
		if err != nil {
			t.Fatal(err)
		}
		l.Staves[0].Voices = []*score.Voice{v}
		return l
	}
	s.Lines = []*score.Line{
		mk("Flute", "woodwinds/flute", "| wC5 | wC5 | wC5 | wC5 | wC5 | wC5 |"),
		mk("Horn", "brass/horn", "| wr | wr | wr | wr | wr | wr |"),
		mk("Violins", "strings/violin_section", "| wr | wr | wr | wr | wC5 | wC5 |"),
	}
	b := ScoreBook(s, false)
	checkBook(t, s, b, s.Layout)
	var counts []int
	var names []string
	for _, pg := range b.Pages {
		for _, sys := range pg.Systems {
			lines := map[int]bool{}
			for _, st := range sys.List.Staves {
				lines[st.Line] = true
			}
			counts = append(counts, len(lines))
		}
		for _, it := range pg.Items {
			if it.Role == RoleName {
				names = append(names, it.Text)
			}
		}
	}
	if fmt.Sprint(counts) != "[3 1 2]" {
		t.Errorf("lines per system %v, want [3 1 2]", counts)
	}
	if strings.Count(strings.Join(names, " "), "Hn.") != 0 {
		t.Errorf("a hidden line's name printed: %v", names)
	}
	s.Layout.HideEmpty = false
	b = ScoreBook(s, false)
	for _, pg := range b.Pages {
		for _, sys := range pg.Systems {
			if len(sys.List.Staves) != 3 {
				t.Errorf("hide_empty off: a system of %d staves", len(sys.List.Staves))
			}
		}
	}
}

// A part for a transposing instrument is headed with its key ("Horn in
// F"), as is its staff in a written full score; at concert pitch, or an
// octave away, or already named so, the name alone.
func TestTransposedPartTitles(t *testing.T) {
	s := loadFixture(t, "demo")
	horn := -1
	for i, l := range s.Lines {
		if l.Name == "Horn" {
			horn = i
		}
	}
	if horn < 0 {
		t.Fatal("no horn in the demo")
	}
	title := func(b *Book) string {
		for _, it := range b.Pages[0].Items {
			if it.Role == RoleTitle && strings.HasPrefix(it.Text, "Horn") {
				return it.Text
			}
		}
		return ""
	}
	if b := PartBook(s, horn, false); b.Part != "Horn in F" || title(b) != "Horn in F" {
		t.Errorf("the horn's part: %q, headed %q", b.Part, title(b))
	}
	if b := PartBook(s, horn, true); b.Part != "Horn" {
		t.Errorf("at concert pitch: %q", b.Part)
	}
	for _, c := range []struct {
		name string
		iv   score.Interval
		want string
	}{
		{"Clarinet", score.Interval{Diatonic: -1, Chromatic: -2}, "Clarinet in B♭"},
		{"Alto Sax", score.Interval{Diatonic: -5, Chromatic: -9}, "Alto Sax in E♭"},
		{"Trumpet", score.Interval{Diatonic: -1, Chromatic: -2}, "Trumpet in B♭"},
		{"Clarinet in A", score.Interval{Diatonic: -2, Chromatic: -3}, "Clarinet in A"},
		{"Guitar", score.Interval{Diatonic: -7, Chromatic: -12}, "Guitar"},
		{"Piccolo", score.Interval{Diatonic: 7, Chromatic: 12}, "Piccolo"},
		{"Violin", score.Interval{}, "Violin"},
	} {
		l := score.NewLine(c.name, "", "treble")
		l.Staves[0].Transpose = c.iv
		if got := TransposedName(l, false); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.name, c.iv, got, c.want)
		}
		if got := TransposedName(l, true); got != c.name {
			t.Errorf("%s at concert pitch: %q", c.name, got)
		}
	}
}
