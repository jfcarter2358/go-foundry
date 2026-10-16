package engrave

// page.go — the score on pages: systems, page breaks, the title, names
// and page numbers. The page view, the PDF (internal/score/io/pdf) and the
// parts all come from here.
//
// Paginate measures every bar once (measure, with the layout's multi-bar
// rests), then breaks the bars into systems: the breaks are chosen
// together, the way a paragraph is set (Knuth and Plass), so that each
// system's natural width is as close as it can be to the measure — a
// little stretching costs little, squeezing toward the ink's least width
// costs more, a system that cannot fit at all is not considered — with
// the breaks the writer asked for kept and no break inside a multi-bar
// rest. Each system is then engraved on its own (Engrave with From/To)
// and justified to the measure (Options.Width); the last is left at its
// natural width when that is less than three quarters of the measure.
// Every system shows its clef and key, its first bar's number, and at its
// end the cautionary signatures for a change at the next one.
//
// Systems go down the page with at least the layout's system gap between
// one system's bottom line and the next one's top line, more where their
// ink needs it; a system that does not fit starts the next page, as does
// a page break. A page that is more than two thirds full spreads what is
// left between its systems (vertical justification); the last page does
// not.
//
// The first page carries the title block — title, subtitle, composer, and
// for a part its name — or, with a title page, a page of its own does.
// Later pages are numbered at the outer top corner (odd pages right, even
// left) beside a running header (the title, and the part's name).
// Instrument names stand at the left of each system: full names on the
// first, short names after (score.Layout.Names); the first system is
// indented by its names.
//
// Everything is in staff spaces; Book.Space says how many millimetres one
// is, so a backend scales once. Text that should not grow with the staff
// (the title, headers, page numbers) is sized in points and converted.

import (
	"math"

	"foundry/internal/score"
)

// PageOptions choose what to lay out on pages.
type PageOptions struct {
	// Layout is the page layout: the score's, or score.Layout.ForPart's
	// for a part.
	Layout score.Layout
	// Lines are the lines to engrave, by index; nil is every line.
	Lines []int
	// Part, when not empty, is the part's name: it heads the first page
	// and the running header, and systems carry no instrument names.
	Part string
	// Concert shows transposing staves at sounding pitch.
	Concert bool
	// MultiRests gathers rest bars into multi-bar rests (parts).
	MultiRests bool
	// Spacing scales the room durations ask for; 0 is the layout's.
	Spacing float64
}

// Book is a laid-out score or part.
type Book struct {
	// Space is a staff space in millimetres; Width and Height are a page
	// in staff spaces.
	Space         float64
	Width, Height float64
	Pages         []*Page
	// Title, Composer and Part are what the pages say, for a document's
	// metadata.
	Title, Composer, Part string
	// Staff is the staff size the pages are set at, in millimetres;
	// Reduced says it is smaller than the layout's because a system was
	// taller than the page (Paginate shrinks the staff until every system
	// fits, down to score.MinStaff).
	Staff   float64
	Reduced bool
}

// Page is one page.
type Page struct {
	// Number is the page's number, from 1 (a title page is page 1).
	Number int
	// TitlePage is a page with the title block and no music.
	TitlePage bool
	// Items are the page's own: the title block, the header, the page
	// number, instrument names — in page coordinates.
	Items   []Item
	Systems []*System
}

// System is one system on a page.
type System struct {
	From, To int
	// At is where the system's List's (0,0) is on the page.
	At   Point
	List *List
}

// Point sizes of a page's text (it does not scale with the staff).
const (
	titlePt    = 22.0
	subtitlePt = 14.0
	composerPt = 11.0
	partPt     = 14.0
	headerPt   = 9.0
	pageNumPt  = 11.0
	// nameSize is an instrument name's size, in staff spaces (it does
	// scale: Sibelius's default).
	nameSize  = 1.7
	nameGap   = 1.2 // a name's right end to the system's brace or barline
	braceRoom = 1.6 // left of the opening barline for a brace
	// noNamesIndent indents a first system with no names.
	noNamesIndent = 3.0
	// justifyLast is how full the last system must be to be justified.
	justifyLast = 0.75
	// fillJustify is how full a page must be to spread its systems, and
	// spreadMax the most a gap grows, as a share of the system gap.
	fillJustify = 0.66
	spreadMax   = 2.5
	sysInkPad   = 1.5 // the least room between one system's ink and the next's
)

// mmPerPt converts points to millimetres.
const mmPerPt = 25.4 / 72

// Paginate lays a score out on pages. A system taller than the page's
// music area (many staves at a large staff size) does not run off the
// paper: the staff size is made smaller until every system fits, and the
// book says so (Book.Reduced).
func Paginate(s *score.Score, o PageOptions) *Book {
	if o.Spacing == 0 {
		o.Spacing = o.Layout.Spacing
	}
	b, over := paginate(s, o)
	staff := o.Layout.StaffOrDefault()
	for i := 0; i < 6 && over > 1 && staff > score.MinStaff; i++ {
		// Text in points does not scale with the staff, so the fit is
		// not exact: aim a little under and try again if need be.
		staff = math.Max(score.MinStaff, math.Floor(staff/over*0.98*10)/10)
		o.Layout.Staff = staff
		b, over = paginate(s, o)
		b.Reduced = true
	}
	return b
}

// paginate is one pagination at the layout's staff size, and how much
// taller than its page's music area the tallest system is (a ratio, at
// most 1 when every system fits).
func paginate(s *score.Score, o PageOptions) (*Book, float64) {
	lay := o.Layout
	space := lay.StaffOrDefault() / 4
	pw, ph := lay.Paper.PaperSize()
	b := &Book{Space: space, Width: pw / space, Height: ph / space, Title: s.Title, Composer: s.Composer, Part: o.Part, Staff: lay.StaffOrDefault()}
	m := lay.Paper.TextMargins()
	p := pager{s: s, o: o, b: b, ml: m.Left / space, mr: m.Right / space, mt: m.Top / space, mb: m.Bottom / space}
	p.pt = func(pt float64) float64 { return pt * mmPerPt / space }
	p.lines = o.Lines
	if p.lines == nil {
		for i := range s.Lines {
			p.lines = append(p.lines, i)
		}
	}
	if lay.TitlePage {
		pg := p.newPage()
		pg.TitlePage = true
		p.titleBlock(pg, true)
	}
	if len(p.lines) == 0 || s.Measures() == 0 {
		if len(b.Pages) == 0 {
			p.titleBlock(p.newPage(), false)
		}
		return b, 0
	}
	p.names()
	breaks := p.breakSystems()
	p.place(breaks)
	return b, p.over
}

// pager is one pagination in progress.
type pager struct {
	s     *score.Score
	o     PageOptions
	b     *Book
	lines []int
	// The margins, in staff spaces, and points → staff spaces.
	ml, mr, mt, mb float64
	pt             func(float64) float64

	// full and short are each line's names, and their widths; fullSize
	// and shortSize the sizes they print at (smaller than nameSize for a
	// name too long for the name column).
	full, short         map[int]string
	fullSize, shortSize map[int]float64
	fullW, shortW       float64
	brace               bool

	// over is the tallest system's height over its page's music area.
	over float64
}

func (p *pager) newPage() *Page {
	pg := &Page{Number: len(p.b.Pages) + 1}
	p.b.Pages = append(p.b.Pages, pg)
	return pg
}

// names works out the instrument names and how wide they are.
func (p *pager) names() {
	p.full, p.short = map[int]string{}, map[int]string{}
	p.fullSize, p.shortSize = map[int]float64{}, map[int]float64{}
	mode := p.o.Layout.Names
	// A name may take at most a quarter of the text width (full) or a
	// sixth (short); a longer one prints smaller rather than squeezing the
	// music off the page.
	text := p.b.Width - p.ml - p.mr
	fit := func(name string, most float64) (float64, float64) {
		size := nameSize
		w := TextWidth(name, size)
		if w > most && w > 0 {
			size *= most / w
			w = most
		}
		return size, w
	}
	for _, li := range p.lines {
		l := p.s.Lines[li]
		if len(l.Staves) > 1 {
			p.brace = true
		}
		if p.o.Part != "" || mode == score.NamesNone {
			continue
		}
		full, short := l.Name, l.ShortName()
		switch mode {
		case score.NamesFull:
			short = full
		case score.NamesShort:
			full = short
		}
		p.full[li], p.short[li] = full, short
		fs, fw := fit(full, text/4)
		ss, sw := fit(short, text/6)
		p.fullSize[li], p.shortSize[li] = fs, ss
		p.fullW = math.Max(p.fullW, fw)
		p.shortW = math.Max(p.shortW, sw)
	}
}

// indent is how far a system's opening barline is from the left margin.
func (p *pager) indent(first bool) float64 {
	w := p.shortW
	if first {
		w = p.fullW
	}
	in := 0.0
	if w > 0 {
		in = w + nameGap
	} else if first {
		in = noNamesIndent
	}
	if p.brace {
		in += braceRoom
	} else if w > 0 {
		in += 0.4
	}
	return in
}

// measureWidth is the room a system has from its opening barline.
func (p *pager) measureWidth(first bool) float64 {
	return p.b.Width - p.ml - p.mr - p.indent(first)
}

func (p *pager) engraveOptions() Options {
	return Options{Lines: p.lines, Concert: p.o.Concert, Spacing: p.o.Spacing, BarNumbers: 0,
		Cautionary: !p.o.Layout.NoCautionary, Courtesy: p.o.Layout.Courtesy, MultiRests: p.o.MultiRests}
}

// barMetric is one bar measured for line breaking.
type barMetric struct {
	// first is the fixed room before the bar's music when it starts a
	// system (clef, key, time, lead), mid when it does not (barline,
	// signature changes, lead); end is what closes a system ending with it
	// (barline and cautionary signatures).
	first, mid, end float64
	rods, springs   []float64
	// folded bars are inside a multi-bar rest: no system starts there.
	folded bool
}

// metrics measures every bar.
func (p *pager) metrics() []barMetric {
	o := p.engraveOptions()
	o.runBreaks = map[int]bool{}
	for _, br := range p.o.Layout.Breaks {
		o.runBreaks[br.Bar] = true
	}
	e := newEngraver(p.s, o)
	e.collect()
	e.measure()
	out := make([]barMetric, len(e.bars))
	for i, bc := range e.bars {
		bm := barMetric{rods: bc.rods, springs: bc.springs, folded: bc.folded}
		sl := e.startPositionsAt(i)
		lead := math.Max(prefixGap, bc.cols[0].left+0.8)
		if e.repeatStarts(bc.bar) {
			lead = startRepeatGap + e.barlineWidth(barRepeatStart) + math.Max(barGapAfter, bc.cols[0].left+0.6)
		}
		if bc.folded {
			lead = 0
		}
		bm.first = sl.end + lead
		if i > 0 {
			bm.mid = e.barlineWidth(e.barlineBefore(i)) + bc.prefixW + bc.lead
		}
		k := e.barlineBefore(i + 1)
		switch k {
		case barRepeatStart:
			k = barSingle // the repeat opens the next system
		case barRepeatBoth:
			k = barRepeatEnd
		case barNone:
			k = barSingle
		}
		bm.end = e.barlineWidth(k)
		if o.Cautionary {
			key, f, meter, mm := nextChanges(p.s, bc.bar)
			if key && k == barSingle {
				bm.end = e.barlineWidth(barDouble)
			}
			bm.end += e.tailAfter(bc.bar, key, f, meter, mm).w
		}
		out[i] = bm
	}
	return out
}

// breakSystems chooses where systems start: indices into the bars (bar
// From-1), the first 0.
func (p *pager) breakSystems() []int {
	ms := p.metrics()
	n := len(ms)
	forced := map[int]bool{}
	for _, br := range p.o.Layout.Breaks {
		if br.Bar >= 2 && br.Bar <= n {
			forced[br.Bar-1] = true
		}
	}
	// width is a system's width over bars [i, j) at stretch k.
	width := func(i, j int, k float64) float64 {
		w := ms[i].first + ms[j-1].end
		for b := i; b < j; b++ {
			if b > i {
				w += ms[b].mid
			}
			for g := range ms[b].rods {
				w += math.Max(ms[b].rods[g], k*ms[b].springs[g])
			}
		}
		return w
	}
	const inf = math.MaxFloat64
	cost := make([]float64, n+1)
	from := make([]int, n+1)
	for i := 1; i <= n; i++ {
		cost[i] = inf
	}
	for i := 0; i < n; i++ {
		if cost[i] == inf || ms[i].folded {
			continue
		}
		avail := p.measureWidth(i == 0)
		for j := i + 1; j <= n; j++ {
			if j < n && ms[j].folded {
				continue
			}
			least := width(i, j, 0)
			if least > avail && j > i+1 {
				break
			}
			nat := width(i, j, 1)
			var bad float64
			switch {
			case j == n && nat <= avail:
				bad = 0 // the last system may be short
			case nat <= avail:
				r := avail/nat - 1
				bad = r * r
			default:
				c := (nat - avail) / math.Max(nat-least, 1e-9)
				bad = 3 * c * c
			}
			bad += 0.1 // each system costs a little: fewer, fuller ones
			if c := cost[i] + bad; c < cost[j] {
				cost[j], from[j] = c, i
			}
			if j < n && forced[j] {
				break
			}
		}
	}
	var starts []int
	if cost[n] == inf {
		// Unreachable unless something cannot be measured: a system a bar.
		for i := 0; i < n; i++ {
			if !ms[i].folded {
				starts = append(starts, i)
			}
		}
		return starts
	}
	for j := n; j > 0; j = from[j] {
		starts = append(starts, from[j])
	}
	for i, k := 0, len(starts)-1; i < k; i, k = i+1, k-1 {
		starts[i], starts[k] = starts[k], starts[i]
	}
	return starts
}

// place engraves the systems and puts them on pages.
func (p *pager) place(starts []int) {
	n := p.s.Measures()
	pageBreak := map[int]bool{}
	for _, br := range p.o.Layout.Breaks {
		if br.Page {
			pageBreak[br.Bar] = true
		}
	}
	gap := p.o.Layout.SystemGapOrDefault()
	var pg *Page
	var top, bottom float64 // the page's music area
	var y float64           // the last system's top line
	var prev *System
	musicPages := 0
	startPage := func() {
		pg = p.newPage()
		if musicPages == 0 && !p.o.Layout.TitlePage {
			top = p.titleBlock(pg, false)
		} else {
			top = p.header(pg)
		}
		musicPages++
		bottom = p.b.Height - p.mb
		prev = nil
	}
	for si, a := range starts {
		z := n - 1
		if si+1 < len(starts) {
			z = starts[si+1] - 1
		}
		first := si == 0
		o := p.engraveOptions()
		o.From, o.To = a+1, z+1
		avail := p.measureWidth(first)
		var l *List
		if si == len(starts)-1 {
			// The last system stays at its natural width when it is short.
			if ln := Engrave(p.s, o); ln.End-ln.Origin.X < avail*justifyLast {
				l = ln
			}
		}
		if l == nil {
			o.Width = avail
			l = Engrave(p.s, o)
		}
		// On paper an empty bar is a bar rest like any other.
		for i := range l.Items {
			l.Items[i].Faint = false
		}
		sys := &System{From: a + 1, To: z + 1, List: l}
		up := l.Origin.Y - margin
		down := l.Height - margin - l.Origin.Y
		if pg == nil {
			startPage()
		} else if pageBreak[a+1] {
			p.spread(pg, bottom)
			startPage()
		}
		if prev == nil {
			y = top + up
			p.overflow(top, bottom, up+down)
		} else {
			pl := prev.List
			pBottom := 4.0
			if k := len(pl.Staves); k > 0 {
				pBottom = pl.Staves[k-1].Top + 4 - pl.Origin.Y
			}
			pDown := pl.Height - margin - pl.Origin.Y
			ny := y + math.Max(pBottom+gap, pDown+up+sysInkPad)
			if ny+down > bottom {
				p.spread(pg, bottom)
				startPage()
				ny = top + up
				p.overflow(top, bottom, up+down)
			}
			y = ny
		}
		sys.At = Point{X: p.ml + p.indent(first) - l.Origin.X, Y: y - l.Origin.Y}
		pg.Systems = append(pg.Systems, sys)
		p.nameItems(pg, sys, first)
		prev = sys
	}
	p.pageNumbers()
}

// overflow notes a system of height h starting a page whose music area
// is top..bottom.
func (p *pager) overflow(top, bottom, h float64) {
	if area := bottom - top; area > 0 {
		p.over = math.Max(p.over, h/area)
	} else {
		p.over = math.Max(p.over, 2)
	}
}

// spread justifies a full page vertically: what is left under its last
// system is shared between the gaps.
func (p *pager) spread(pg *Page, bottom float64) {
	k := len(pg.Systems)
	if k < 2 {
		return
	}
	first, last := pg.Systems[0], pg.Systems[k-1]
	used := last.At.Y + last.List.Height - margin
	topInk := first.At.Y + margin
	area := bottom - topInk
	left := bottom - used
	if area <= 0 || left <= 0 || (used-topInk)/area < fillJustify {
		return
	}
	per := math.Min(left/float64(k-1), spreadMax*p.o.Layout.SystemGapOrDefault())
	for i, sys := range pg.Systems {
		d := per * float64(i)
		sys.At.Y += d
	}
	// Names moved with their systems.
	for i := range pg.Items {
		it := &pg.Items[i]
		if it.Role != RoleName {
			continue
		}
		for si, sys := range pg.Systems {
			if it.Ref.Bar == sys.From {
				d := per * float64(si)
				it.P.Y += d
			}
		}
	}
}

// nameItems puts a system's instrument names at its left, each centred on
// its line's staves.
func (p *pager) nameItems(pg *Page, sys *System, first bool) {
	names, sizes := p.short, p.shortSize
	if first {
		names, sizes = p.full, p.fullSize
	}
	if len(names) == 0 {
		return
	}
	l := sys.List
	x := sys.At.X + l.Origin.X - nameGap
	if p.brace {
		x -= braceRoom - 0.4
	}
	for _, li := range p.lines {
		name := names[li]
		if name == "" {
			continue
		}
		t, b := math.Inf(1), math.Inf(-1)
		for _, st := range l.Staves {
			if st.Line == li {
				t, b = math.Min(t, st.Top), math.Max(b, st.Top+4)
			}
		}
		if math.IsInf(t, 1) {
			continue
		}
		yc := sys.At.Y + (t+b)/2
		size := sizes[li]
		pg.Items = append(pg.Items, Item{Kind: ItemText, Role: RoleName, Ref: Ref{Line: li, Staff: -1, Voice: -1, Bar: sys.From, Event: -1, Note: -1},
			P: Point{X: x, Y: yc + 0.35*size}, Text: name, Font: TextPlain, Size: size, Align: AlignRight})
	}
}

// titleBlock heads a page with the title, subtitle, composer and part
// name, and returns where the music may start. alone sets them on a page
// of their own, a third of the way down.
func (p *pager) titleBlock(pg *Page, alone bool) float64 {
	s := p.s
	ref := Ref{Line: -1, Staff: -1, Voice: -1, Bar: 0, Event: -1, Note: -1}
	cx := p.ml + (p.b.Width-p.ml-p.mr)/2
	y := p.mt
	scale := 1.0
	if alone {
		y = p.mt + (p.b.Height-p.mt-p.mb)/3
		scale = 1.6
	}
	text := p.b.Width - p.ml - p.mr
	add := func(line string, font TextFont, pt float64, x float64, align Align) {
		size := p.pt(pt * scale)
		// A title wider than the page prints smaller.
		if w := TextWidthFont(line, font, size); w > text && w > 0 {
			size *= text / w
		}
		y += 0.8 * size
		pg.Items = append(pg.Items, Item{Kind: ItemText, Role: RoleTitle, Ref: ref, P: Point{X: x, Y: y}, Text: line, Font: font, Size: size, Align: align})
		y += 0.45 * size
	}
	if s.Title != "" {
		add(s.Title, TextBold, titlePt, cx, AlignCenter)
	}
	if s.Subtitle != "" {
		add(s.Subtitle, TextItalic, subtitlePt, cx, AlignCenter)
	}
	if alone && p.o.Part != "" {
		add(p.o.Part, TextPlain, partPt, cx, AlignCenter)
	}
	if s.Composer != "" || p.o.Part != "" && !alone {
		size := p.pt(composerPt * scale)
		y += 0.6 * size
		line := y + 0.8*size
		// The part's name (left) and the composer (right) share the line:
		// each fits in its half.
		half := text
		if p.o.Part != "" && !alone {
			half = text/2 - 1
		}
		shrink := func(t string, f TextFont, size float64) float64 {
			if w := TextWidthFont(t, f, size); w > half && w > 0 {
				return size * half / w
			}
			return size
		}
		if s.Composer != "" {
			x, align := p.b.Width-p.mr, AlignRight
			if alone {
				x, align = cx, AlignCenter
			}
			pg.Items = append(pg.Items, Item{Kind: ItemText, Role: RoleTitle, Ref: ref, P: Point{X: x, Y: line}, Text: s.Composer, Font: TextPlain, Size: shrink(s.Composer, TextPlain, size), Align: align})
		}
		if p.o.Part != "" && !alone {
			ps := shrink(p.o.Part, TextBold, p.pt(partPt))
			pg.Items = append(pg.Items, Item{Kind: ItemText, Role: RoleTitle, Ref: ref, P: Point{X: p.ml, Y: line}, Text: p.o.Part, Font: TextBold, Size: ps})
		}
		y = line + 0.45*size
	}
	if y == p.mt {
		return p.mt
	}
	return y + p.pt(composerPt)*1.5
}

// header is a later page's running header (the title, the part); the
// page number goes beside it (pageNumbers). It returns where the music
// may start.
func (p *pager) header(pg *Page) float64 {
	text := p.s.Title
	if p.o.Part != "" {
		if text != "" {
			text += " — "
		}
		text += p.o.Part
	}
	size := p.pt(headerPt)
	// Room is left either side for the page number.
	if w, room := TextWidthFont(text, TextItalic, size), (p.b.Width-p.ml-p.mr)*0.7; w > room && w > 0 {
		size *= room / w
	}
	if text != "" {
		pg.Items = append(pg.Items, Item{Kind: ItemText, Role: RoleTitle, Ref: Ref{Line: -1, Staff: -1, Voice: -1, Event: -1, Note: -1},
			P: Point{X: p.ml + (p.b.Width-p.ml-p.mr)/2, Y: p.headerY()}, Text: text, Font: TextItalic, Size: size, Align: AlignCenter})
	}
	return p.mt + p.pt(pageNumPt)
}

// headerY is the running header's baseline: in the top margin.
func (p *pager) headerY() float64 { return p.mt - p.pt(pageNumPt)*0.2 }

// pageNumbers numbers every page but the first, at the outer top corner.
func (p *pager) pageNumbers() {
	if p.o.Layout.NoPageNumbers {
		return
	}
	size := p.pt(pageNumPt)
	for _, pg := range p.b.Pages {
		if pg.Number == 1 || pg.TitlePage {
			continue
		}
		x, align := p.b.Width-p.mr, AlignRight
		if pg.Number%2 == 0 {
			x, align = p.ml, AlignLeft
		}
		pg.Items = append(pg.Items, Item{Kind: ItemText, Role: RolePageNumber, Ref: Ref{Line: -1, Staff: -1, Voice: -1, Event: -1, Note: -1},
			P: Point{X: x, Y: p.headerY()}, Text: itoa(pg.Number), Font: TextPlain, Size: size, Align: align})
	}
}

func itoa(n int) string {
	if n < 0 {
		return "-" + itoa(-n)
	}
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

// List is the whole page as one display list, in page coordinates: its
// systems moved to their places and the page's own items, Width and
// Height the paper's. The boxes (staves, bars, events) come with the
// systems, moved too.
func (b *Book) List(pg *Page) *List {
	l := &List{Width: b.Width, Height: b.Height}
	for _, sys := range pg.Systems {
		c := *sys.List
		c.Items = append([]Item(nil), sys.List.Items...)
		for i := range c.Items {
			c.Items[i].Path = append(c.Items[i].Path[:0:0], c.Items[i].Path...)
		}
		c.Staves = append([]StaffBox(nil), sys.List.Staves...)
		for i := range c.Staves {
			c.Staves[i].Changes = append([]ClefSpan(nil), c.Staves[i].Changes...)
		}
		c.Bars = append([]BarBox(nil), sys.List.Bars...)
		for i := range c.Bars {
			c.Bars[i].Columns = append([]Column(nil), c.Bars[i].Columns...)
		}
		c.Events = append([]EventBox(nil), sys.List.Events...)
		c.Lanes = nil
		c.Translate(sys.At)
		l.Items = append(l.Items, c.Items...)
		l.Staves = append(l.Staves, c.Staves...)
		l.Bars = append(l.Bars, c.Bars...)
		l.Events = append(l.Events, c.Events...)
	}
	l.Items = append(l.Items, pg.Items...)
	return l
}

// SystemAt is the system of a page under a point (page coordinates), or
// nil: the one whose staves are nearest in y among those the point is
// beside.
func (pg *Page) SystemAt(p Point) *System {
	var best *System
	bd := math.Inf(1)
	for _, sys := range pg.Systems {
		l := sys.List
		if len(l.Staves) == 0 {
			continue
		}
		top := sys.At.Y + l.Staves[0].Top
		bot := sys.At.Y + l.Staves[len(l.Staves)-1].Top + 4
		d := 0.0
		switch {
		case p.Y < top:
			d = top - p.Y
		case p.Y > bot:
			d = p.Y - bot
		}
		if d < bd {
			best, bd = sys, d
		}
	}
	return best
}

// Find is the page and system a bar is on.
func (b *Book) Find(bar int) (*Page, *System) {
	for _, pg := range b.Pages {
		for _, sys := range pg.Systems {
			if bar >= sys.From && bar <= sys.To {
				return pg, sys
			}
		}
	}
	return nil, nil
}

// ScoreBook lays out a score's full score with its own layout.
func ScoreBook(s *score.Score, concert bool) *Book {
	return Paginate(s, PageOptions{Layout: s.Layout, Concert: concert || s.Concert})
}

// PartBook lays out one line's part with the part's layout: multi-bar
// rests unless the part says not, at written pitch unless concert.
func PartBook(s *score.Score, line int, concert bool) *Book {
	if line < 0 || line >= len(s.Lines) {
		return &Book{}
	}
	name := s.Lines[line].Name
	lay, multi := s.Layout.ForPart(name)
	return Paginate(s, PageOptions{Layout: lay, Lines: []int{line}, Part: name, Concert: concert, MultiRests: multi})
}
