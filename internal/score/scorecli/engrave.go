package scorecli

// engrave.go — `foundry score render -png`: a score engraved to an image.

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"image/png"
	"io"
	"os"
	"strconv"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/engrave"
	"foundry/internal/score/engrave/raster"
	"foundry/internal/score/io/svg"
)

// engraveFlags are render's engraving flags.
type engraveFlags struct {
	png        string
	svg        string
	page       int
	part       string
	bars       string
	lines      string
	scale      float64
	spacing    float64
	concert    bool
	barNumbers int
}

func (ef *engraveFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&ef.png, "png", "", "engrave to this .png instead of rendering audio")
	fs.StringVar(&ef.svg, "svg", "", "engrave to this .svg instead of rendering audio (the same flags as -png)")
	fs.IntVar(&ef.page, "page", 0, "with -png/-svg: engrave page n of the page layout instead of the continuous view")
	fs.StringVar(&ef.part, "part", "", "with -page: the page of this line's part (a name or number) instead of the full score")
	fs.StringVar(&ef.bars, "bars", "", "with -png: the bars to engrave, a-b or one bar (default all)")
	fs.StringVar(&ef.lines, "lines", "", "with -png: the lines to engrave, 1-based, comma-separated (default all)")
	fs.Float64Var(&ef.scale, "scale", 12, "with -png: pixels per staff space")
	fs.Float64Var(&ef.spacing, "spacing", 1, "with -png: horizontal spacing factor")
	fs.BoolVar(&ef.concert, "concert", false, "with -png: transposing staves at concert pitch")
	fs.IntVar(&ef.barNumbers, "bar-numbers", 0, "with -png: number every n-th bar (0: the first only, -1: none)")
}

// options turns the flags into engraving options.
func (ef *engraveFlags) options(s *score.Score) (engrave.Options, error) {
	o := engrave.Options{Concert: ef.concert, Spacing: ef.spacing, BarNumbers: ef.barNumbers}
	if ef.bars != "" {
		a, b, isRange := strings.Cut(ef.bars, "-")
		from, err1 := strconv.Atoi(strings.TrimSpace(a))
		to := from
		var err2 error
		if isRange {
			to, err2 = strconv.Atoi(strings.TrimSpace(b))
		}
		if err1 != nil || err2 != nil || from < 1 || to < from {
			return o, fmt.Errorf("-bars %q: want a bar or a range like 5-8", ef.bars)
		}
		if from > s.Measures() {
			return o, fmt.Errorf("-bars %q: the score has %d bars", ef.bars, s.Measures())
		}
		o.From, o.To = from, to
	}
	if ef.lines != "" {
		for _, t := range strings.Split(ef.lines, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(t))
			if err != nil || n < 1 || n > len(s.Lines) {
				return o, fmt.Errorf("-lines %q: lines are numbered 1 to %d", ef.lines, len(s.Lines))
			}
			o.Lines = append(o.Lines, n-1)
		}
	}
	if ef.scale <= 0 || ef.scale > 200 {
		return o, fmt.Errorf("-scale %g: want 1 to 200 pixels a staff space", ef.scale)
	}
	return o, nil
}

// list is what -png and -svg draw: the continuous view of the bars and
// lines asked for, or a page of the layout.
func (ef *engraveFlags) list(s *score.Score) (*engrave.List, error) {
	o, err := ef.options(s)
	if err != nil {
		return nil, err
	}
	if ef.page == 0 {
		if ef.part != "" {
			return nil, fmt.Errorf("-part is for -page")
		}
		return engrave.Engrave(s, o), nil
	}
	if ef.bars != "" || ef.lines != "" {
		return nil, fmt.Errorf("-page lays out the whole score or part: no -bars or -lines")
	}
	var book *engrave.Book
	if ef.part != "" {
		li := s.LineIndex(ef.part)
		if li < 0 {
			return nil, fmt.Errorf("-part %q: no such line", ef.part)
		}
		book = engrave.PartBook(s, li, ef.concert)
	} else {
		book = engrave.ScoreBook(s, ef.concert)
	}
	if ef.page < 1 || ef.page > len(book.Pages) {
		return nil, fmt.Errorf("-page %d: the layout has %d pages", ef.page, len(book.Pages))
	}
	return book.List(book.Pages[ef.page-1]), nil
}

// renderSVG writes the engraving as an SVG.
func renderSVG(s *score.Score, ef engraveFlags, out io.Writer) int {
	l, err := ef.list(s)
	if err != nil {
		fmt.Fprintf(out, "score render: %v\n", err)
		return 2
	}
	if err := svg.WriteFile(ef.svg, l, svg.Options{Scale: ef.scale, Title: s.Title}); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	st, err := os.Stat(ef.svg)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	fmt.Fprintf(out, "%s: %.0fx%.0f, %d bars, %d staves, %d items, %d bytes\n", ef.svg, l.Width*ef.scale, l.Height*ef.scale,
		len(l.Bars), len(l.Staves), len(l.Items), st.Size())
	return 0
}

func renderPNG(s *score.Score, ef engraveFlags, out io.Writer) int {
	l, err := ef.list(s)
	if err != nil {
		fmt.Fprintf(out, "score render: %v\n", err)
		return 2
	}
	img := raster.Render(l, raster.Options{Scale: ef.scale})
	f, err := os.Create(ef.png)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		fmt.Fprintln(out, err)
		return 1
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	h := sha256.Sum256(img.Pix)
	fmt.Fprintf(out, "%s: %dx%d px, %d bars, %d staves, %d items, hash %s\n", ef.png, img.Rect.Dx(), img.Rect.Dy(),
		len(l.Bars), len(l.Staves), len(l.Items), hex.EncodeToString(h[:8]))
	return 0
}
