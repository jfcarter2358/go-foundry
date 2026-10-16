package scorecli

// pdf.go — `foundry score export-pdf`: the full score and its parts as
// PDFs (internal/score/io/pdf), optionally drawn back to PNGs from the
// written files for a look.

import (
	"flag"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/engrave"
	"foundry/internal/score/io/pdf"
)

const exportPDFUsage = `foundry score export-pdf <score> [flags]

Writes the full score as <name>.pdf and every line's part as
<name>_<line>.pdf, laid out by the score's layout: block (paper, staff
size, breaks, title page, names; a part's own settings under layout.parts).

  -o path          a directory, or the score's .pdf (parts go beside it);
                   default the working directory
  -score=false     no full score
  -parts=false     no parts
  -part 1,Viola    only these parts (numbers or names)
  -concert         the full score at concert pitch (parts stay transposed)
  -paper a4        paper for this export: letter, a4, tabloid, a3, legal
  -staff 6.5       staff size in mm for this export
  -png dir         also draw every page of every PDF written, read back
                   from the file, as dir/<file>_p<n>.png
  -dpi 100         the PNGs' resolution
`

func exportPDF(rest []string, out io.Writer) int {
	fs := flag.NewFlagSet("score export-pdf", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { fmt.Fprint(out, exportPDFUsage) }
	dst := fs.String("o", "", "")
	doScore := fs.Bool("score", true, "")
	doParts := fs.Bool("parts", true, "")
	only := fs.String("part", "", "")
	concert := fs.Bool("concert", false, "")
	paper := fs.String("paper", "", "")
	staff := fs.Float64("staff", 0, "")
	pngDir := fs.String("png", "", "")
	dpi := fs.Float64("dpi", 100, "")
	name, flags := splitArgs(rest)
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if name == "" {
		fmt.Fprintln(out, "score export-pdf: name a score")
		return 2
	}
	s, _, err := load(name)
	if err != nil {
		fmt.Fprintf(out, "score export-pdf: %v\n", err)
		return 1
	}
	if *dpi <= 0 || *dpi > 1200 {
		fmt.Fprintf(out, "score export-pdf: -dpi %g: 1 to 1200\n", *dpi)
		return 2
	}
	o := pdf.ExportOptions{Score: *doScore, Concert: *concert, Paper: strings.ToLower(*paper), Staff: *staff}
	switch {
	case *only != "":
		for _, t := range strings.Split(*only, ",") {
			li := s.LineIndex(strings.TrimSpace(t))
			if li < 0 {
				fmt.Fprintf(out, "score export-pdf: -part %q: no such line\n", t)
				return 2
			}
			o.Parts = append(o.Parts, li)
		}
	case *doParts:
		for i := range s.Lines {
			o.Parts = append(o.Parts, i)
		}
	}
	dir, base := ".", filepath.Base(s.Name)
	if base == "" || base == "." {
		base = "score"
	}
	if *dst != "" {
		if strings.EqualFold(filepath.Ext(*dst), ".pdf") {
			dir, base = filepath.Dir(*dst), strings.TrimSuffix(filepath.Base(*dst), filepath.Ext(*dst))
		} else {
			dir = *dst
		}
	}
	files, err := pdf.Export(s, dir, base, o)
	for _, f := range files {
		what := "full score"
		if f.Part != "" {
			what = "part: " + f.Part
		}
		fmt.Fprintf(out, "%s: %d page(s), %d bytes (%s)\n", f.Path, f.Pages, f.Bytes, what)
		if f.Reduced {
			fmt.Fprintf(out, "  note: a system was taller than the page: set at a %.1f mm staff\n", f.Staff)
		}
	}
	if err != nil {
		fmt.Fprintf(out, "score export-pdf: %v\n", err)
		return 1
	}
	if *pngDir != "" {
		if err := os.MkdirAll(*pngDir, 0o755); err != nil {
			fmt.Fprintln(out, err)
			return 1
		}
		for _, f := range files {
			if err := pdfPNGs(f.Path, *pngDir, *dpi/72, out); err != nil {
				fmt.Fprintf(out, "score export-pdf: %v\n", err)
				return 1
			}
		}
	}
	return 0
}

// pdfPNGs reads a written PDF back and draws each page.
func pdfPNGs(path, dir string, pxPerPt float64, out io.Writer) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	f, err := pdf.Parse(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	for i := range f.Pages {
		img, err := pdf.Rasterize(f, i, pxPerPt)
		if err != nil {
			return fmt.Errorf("%s page %d: %w", path, i+1, err)
		}
		p := filepath.Join(dir, fmt.Sprintf("%s_p%d.png", stem, i+1))
		w, err := os.Create(p)
		if err != nil {
			return err
		}
		if err := png.Encode(w, img); err != nil {
			w.Close()
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: %dx%d px\n", p, img.Rect.Dx(), img.Rect.Dy())
	}
	return nil
}

// inspectLayout is inspect's page layout lines: the paper, the staff, the
// pages the full score takes, the breaks, each part's own settings.
func inspectLayout(s *score.Score, out io.Writer) {
	l := s.Layout
	w, h := l.Paper.PaperSize()
	name := l.Paper.Name
	if name == "" {
		name = score.DefaultPaper
	}
	if l.Paper.Landscape {
		name += " landscape"
	}
	b := engrave.ScoreBook(s, false)
	systems := 0
	for _, pg := range b.Pages {
		systems += len(pg.Systems)
	}
	fmt.Fprintf(out, "  layout %s %.0fx%.0f mm, staff %.1f mm, system gap %.1f: %d page(s), %d system(s)", name, w, h,
		l.StaffOrDefault(), l.SystemGapOrDefault(), len(b.Pages), systems)
	var flags []string
	if b.Reduced {
		flags = append(flags, fmt.Sprintf("set at a %.1f mm staff to fit the page", b.Staff))
	}
	if l.Spacing > 0 {
		flags = append(flags, fmt.Sprintf("note spacing %.2g", l.Spacing))
	}
	if l.TitlePage {
		flags = append(flags, "title page")
	}
	if l.NoPageNumbers {
		flags = append(flags, "no page numbers")
	}
	if l.Names != score.NamesFullThenShort {
		flags = append(flags, "names "+score.NameModes[l.Names])
	}
	if l.NoCautionary {
		flags = append(flags, "no cautionaries")
	}
	if l.Courtesy {
		flags = append(flags, "courtesy accidentals")
	}
	if len(flags) > 0 {
		fmt.Fprintf(out, " (%s)", strings.Join(flags, ", "))
	}
	fmt.Fprintln(out)
	if len(l.Breaks) > 0 {
		fmt.Fprintf(out, "         breaks before %s\n", breaksText(l.Breaks))
	}
	for _, p := range l.Parts {
		var bits []string
		if p.Staff > 0 {
			bits = append(bits, fmt.Sprintf("staff %.1f mm", p.Staff))
		}
		if p.SystemGap > 0 {
			bits = append(bits, fmt.Sprintf("system gap %.1f", p.SystemGap))
		}
		if p.Paper.Name != "" {
			bits = append(bits, p.Paper.Name)
		}
		if p.NoMultiRests {
			bits = append(bits, "no multi-bar rests")
		}
		if len(p.Breaks) > 0 {
			bits = append(bits, "breaks before "+breaksText(p.Breaks))
		}
		fmt.Fprintf(out, "         part %s: %s\n", p.Line, strings.Join(bits, ", "))
	}
}

// breaksText is breaks as "9, 13 (page)".
func breaksText(bs []score.Break) string {
	var out []string
	for _, b := range bs {
		t := fmt.Sprint(b.Bar)
		if b.Page {
			t += " (page)"
		}
		out = append(out, t)
	}
	return strings.Join(out, ", ")
}
