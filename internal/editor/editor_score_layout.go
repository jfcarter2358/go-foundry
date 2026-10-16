package editor

// editor_score_layout.go — the page layout and the printed output, with no
// ImGui: what the page view shows (the full score or one line's part, laid
// out by internal/score/engrave's Paginate and cached per edit), the
// layout's edits — system and page breaks, paper, margins, staff size,
// system gap, titles, names, the house-style switches, a part's own
// settings — each one undo step (one per drag of a control) that the player
// never hears, and the exports: PDFs of the score and parts
// (internal/score/io/pdf) and a PNG or SVG of the selected bars.
//
// Breaks follow Sibelius: a break goes after the selected bar (the last of
// selected bars), so the next bar starts a system or a page; the command
// again takes it away, and a page break where a system break is (or the
// other way) changes its kind. While the page view shows a part, breaks and
// the setup's part settings are the part's own (score.PartLayout); the
// full score's are untouched.

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/score"
	"foundry/internal/score/engrave"
	"foundry/internal/score/engrave/raster"
	"foundry/internal/score/io/pdf"
	"foundry/internal/score/io/svg"
)

// The page view's state (view state: not saved).
var (
	// scorePageView shows the Score tab as pages instead of one
	// continuous system.
	scorePageView bool
	// scorePagePart is the part the page view shows: -1 the full score,
	// else a line's index.
	scorePagePart = -1
	// scorePageZoom is pixels per staff space in the page view.
	scorePageZoom = float32(5)
)

// scorePageCache is the page layout, kept until the score, its layout or
// what it shows changes.
var scorePageCache struct {
	book                *engrave.Book
	s                   *score.Score
	rev, lrev, mix, lan uint64
	part                int
	concert             bool
}

// scorePageBook is the page view's layout.
func scorePageBook() *engrave.Book {
	e := scEd
	c := &scorePageCache
	part := e.pagePart()
	if c.book == nil || c.s != e.Score || c.rev != e.Rev || c.lrev != e.LayoutRev || c.mix != e.MixRev || c.lan != e.laneRev ||
		c.part != part || c.concert != e.Score.Concert {
		if part >= 0 {
			c.book = engrave.PartBook(e.Score, part, false)
		} else {
			c.book = engrave.ScoreBook(e.Score, false)
		}
		c.s, c.rev, c.lrev, c.mix, c.lan, c.part, c.concert = e.Score, e.Rev, e.LayoutRev, e.MixRev, e.laneRev, part, e.Score.Concert
	}
	return c.book
}

// pagePart is the part the page view shows, or -1: a line that has gone
// (removed, or another score loaded) is the full score again.
func (e *scoreState) pagePart() int {
	if scorePagePart < 0 || scorePagePart >= len(e.Score.Lines) {
		return -1
	}
	return scorePagePart
}

// layoutChange runs a layout edit as one undo step — one per gesture of a
// control (key), as the mixer's are — that lays the pages out again and
// leaves the player alone.
func (e *scoreState) layoutChange(key string, f func()) {
	key = "layout:" + key
	if key != e.mixKey {
		e.undo.push(scoreSnapshot{Score: e.Score.Clone(), Sel: e.Sel})
		e.redo.clear()
	}
	f()
	e.mixKey = key
	e.LayoutRev++
}

// endLayoutGesture closes a drag of a layout control.
func (e *scoreState) endLayoutGesture() {
	if strings.HasPrefix(e.mixKey, "layout:") {
		e.mixKey = ""
	}
}

// breaksOf are the breaks the page view's layout keeps: the part's own
// when it shows a part.
func (e *scoreState) breaksOf(part int) []score.Break {
	if part >= 0 && part < len(e.Score.Lines) {
		if p := e.Score.Layout.Part(e.Score.Lines[part].Name); p != nil {
			return p.Breaks
		}
		return nil
	}
	return e.Score.Layout.Breaks
}

// toggleBreak puts a system (or page) break after the selected bar, takes
// one away, or changes one's kind.
func (e *scoreState) toggleBreak(page bool) bool {
	_, z, ok := e.selBars()
	if !ok {
		e.Status = "Select a bar to break after"
		return false
	}
	bar := z + 1
	if bar > e.Score.Measures() {
		e.Status = fmt.Sprintf("Bar %d is the last: there is nothing to break before", z)
		return false
	}
	part := e.pagePart()
	cur, had := score.BreakIn(e.breaksOf(part), bar)
	remove := had && cur.Page == page
	what := "System"
	if page {
		what = "Page"
	}
	e.layoutChange("", func() {
		if part >= 0 {
			e.Score.Layout.EnsurePart(e.Score.Lines[part].Name).SetBreak(bar, remove, page)
		} else {
			e.Score.Layout.SetBreak(bar, remove, page)
		}
	})
	e.endLayoutGesture()
	where := ""
	if part >= 0 {
		where = " in the " + e.Score.Lines[part].Name + " part"
	}
	if remove {
		e.Status = fmt.Sprintf("%s break after bar %d removed%s", what, z, where)
	} else {
		e.Status = fmt.Sprintf("%s break after bar %d%s", what, z, where)
	}
	return true
}

// ---------- page setup ----------

// setLayout changes the score's layout through f, one step per key.
func (e *scoreState) setLayout(key string, f func(l *score.Layout)) {
	e.layoutChange(key, func() { f(&e.Score.Layout) })
}

// setPart changes a line's part settings through f.
func (e *scoreState) setPart(line int, key string, f func(p *score.PartLayout)) {
	if line < 0 || line >= len(e.Score.Lines) {
		return
	}
	e.layoutChange(fmt.Sprintf("part%d.%s", line, key), func() { f(e.Score.Layout.EnsurePart(e.Score.Lines[line].Name)) })
}

// setText changes one of the score's printed texts (title, subtitle,
// composer).
func (e *scoreState) setText(key string, to string) {
	var at *string
	switch key {
	case "title":
		at = &e.Score.Title
	case "subtitle":
		at = &e.Score.Subtitle
	case "composer":
		at = &e.Score.Composer
	default:
		return
	}
	if *at == to {
		return
	}
	e.layoutChange(key, func() { *at = to })
}

// setShortName sets a line's short name ("" makes one from its name).
func (e *scoreState) setShortName(line int, to string) {
	l := e.line(line)
	if l == nil || l.Short == to {
		return
	}
	e.layoutChange(fmt.Sprintf("short%d", line), func() { l.Short = to })
}

// ---------- exports ----------

// scoreExportsDir is exports/<name>/ beside resources/, where the printed
// output goes by default (outside sounds/).
func scoreExportsDir() string {
	return filepath.Join(filepath.Dir(filepath.Clean(fileutil.ResourcesPath)), "exports", filepath.FromSlash(scoreExportBase()))
}

// scoreExportBase is the files' name: the score's library name's last
// part, its title, or "score".
func scoreExportBase() string {
	name := filepath.Base(scEd.Name)
	if scEd.Name == "" {
		name = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(scEd.Score.Title), " ", "_"))
	}
	if name == "" || name == "." {
		name = "score"
	}
	return name
}

// scorePDFRequest is what the PDF dialog asks for.
type scorePDFRequest struct {
	Score   bool
	Parts   []bool // by line
	Paper   string // "" the layout's
	Staff   float32
	Concert bool
	Dir     string
}

// pdfOptions turns a request into the exporter's options.
func (r scorePDFRequest) pdfOptions() pdf.ExportOptions {
	o := pdf.ExportOptions{Score: r.Score, Paper: r.Paper, Staff: float64(r.Staff), Concert: r.Concert}
	for i, on := range r.Parts {
		if on {
			o.Parts = append(o.Parts, i)
		}
	}
	return o
}

// scoreExportPDFs writes the PDFs: the dialog's work, off the frame.
func scoreExportPDFs(s *score.Score, r scorePDFRequest) (string, error) {
	files, err := pdf.Export(s, r.Dir, scoreExportBase(), r.pdfOptions())
	if err != nil {
		return "", err
	}
	pages := 0
	for _, f := range files {
		pages += f.Pages
	}
	return fmt.Sprintf("Exported %d PDF(s), %d page(s), to %s", len(files), pages, r.Dir), nil
}

// imageSpan is what an image export draws: the selected bars and lines,
// or why there is nothing.
func (e *scoreState) imageSpan() (from, to int, lines []int, err error) {
	switch e.Sel.Kind {
	case scoreSelBars:
		for li := e.Sel.LineA; li <= e.Sel.LineB; li++ {
			lines = append(lines, li)
		}
		return e.Sel.BarA, e.Sel.BarB, lines, nil
	case scoreSelEvent, scoreSelCaret:
		return e.Sel.Loc.Bar, e.Sel.Loc.Bar, []int{e.Sel.Loc.Line}, nil
	}
	return 0, 0, nil, fmt.Errorf("select the bars to export (click a bar; Shift+click to extend; Ctrl+click for every line)")
}

// scoreImagePath is the default image file for the selection.
func scoreImagePath(svgFormat bool) string {
	from, to, _, err := scEd.imageSpan()
	name := scoreExportBase()
	if err == nil {
		name += fmt.Sprintf("_bars%d-%d", from, to)
		if from == to {
			name = scoreExportBase() + fmt.Sprintf("_bar%d", from)
		}
	}
	ext := ".png"
	if svgFormat {
		ext = ".svg"
	}
	return filepath.Join(scoreExportsDir(), name+ext)
}

// exportImage engraves the selection and writes it as a PNG or an SVG
// (by the path's extension) at a scale, pixels per staff space.
func (e *scoreState) exportImage(path string, scale float64) error {
	from, to, lines, err := e.imageSpan()
	if err != nil {
		return err
	}
	if scale <= 0 || scale > 200 {
		return fmt.Errorf("scale %g: 1 to 200 pixels a staff space", scale)
	}
	l := engrave.Engrave(e.Score, engrave.Options{From: from, To: to, Lines: lines})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".svg":
		err = svg.WriteFile(path, l, svg.Options{Scale: scale, Title: e.Score.Title})
	case ".png":
		f, ferr := os.Create(path)
		if ferr != nil {
			return ferr
		}
		err = png.Encode(f, raster.Render(l, raster.Options{Scale: scale}))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	default:
		return fmt.Errorf("%s: name a .png or an .svg", filepath.Base(path))
	}
	if err != nil {
		return err
	}
	e.Status = fmt.Sprintf("Exported bars %d-%d to %s", from, to, path)
	return nil
}
