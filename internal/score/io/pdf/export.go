package pdf

// export.go — a score and its parts as PDF files: what
// `foundry score export-pdf` and the editor's Export PDF dialog both do.
// The full score is <base>.pdf, each part <base>_<line>.pdf beside it, the
// line's name made fit for a file name.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"foundry/internal/score"
	"foundry/internal/score/engrave"
)

// ExportOptions say what to write.
type ExportOptions struct {
	// Score writes the full score; Parts the parts of these lines (by
	// index).
	Score bool
	Parts []int
	// Concert sets the full score at concert pitch (parts stay at written
	// pitch, as players read them).
	Concert bool
	// Paper and Staff, when set, replace the score's own paper size
	// (score.PaperNames) and staff size (mm) for this export, score and
	// parts alike.
	Paper string
	Staff float64
	// Created is the documents' date; zero is now.
	Created time.Time
}

// Exported is one file written.
type Exported struct {
	Path string
	// Part is the part's line name ("" for the full score).
	Part  string
	Pages int
	Bytes int
	// Staff is the staff size it is set at (mm); Reduced says smaller
	// than the layout's, so that every system fits the page.
	Staff   float64
	Reduced bool
}

// PartFileName is a part's file name: base_line.pdf.
func PartFileName(base, line string) string {
	return base + "_" + fileSafe(line) + ".pdf"
}

// fileSafe keeps a name's letters and digits, the rest as underscores.
func fileSafe(s string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			under = false
		case !under && b.Len() > 0:
			b.WriteByte('_')
			under = true
		}
	}
	out := strings.TrimRight(b.String(), "_")
	if out == "" {
		return "part"
	}
	return out
}

// Export writes the full score and parts asked for into dir.
func Export(s *score.Score, dir, base string, o ExportOptions) ([]Exported, error) {
	if !o.Score && len(o.Parts) == 0 {
		return nil, fmt.Errorf("pdf: nothing to export (no score, no parts)")
	}
	if o.Paper != "" {
		if _, ok := score.PaperSizes[o.Paper]; !ok {
			return nil, fmt.Errorf("pdf: paper %q (want %s)", o.Paper, strings.Join(score.PaperNames[:len(score.PaperNames)-1], ", "))
		}
	}
	if o.Staff != 0 && (o.Staff < score.MinStaff || o.Staff > score.MaxStaff) {
		return nil, fmt.Errorf("pdf: staff %g mm: %g to %g", o.Staff, score.MinStaff, score.MaxStaff)
	}
	s = s.Clone()
	over := func(l *score.Layout) {
		if o.Paper != "" {
			l.Paper = score.Paper{Name: o.Paper, Landscape: l.Paper.Landscape, Margins: l.Paper.Margins}
		}
		if o.Staff > 0 {
			l.Staff = o.Staff
		}
	}
	over(&s.Layout)
	for i := range s.Layout.Parts {
		p := &s.Layout.Parts[i]
		if o.Paper != "" {
			p.Paper = score.Paper{}
		}
		if o.Staff > 0 {
			p.Staff = 0
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var out []Exported
	write := func(b *engrave.Book, name, part string) error {
		path := filepath.Join(dir, name)
		if err := WriteFile(path, b, Options{Info: Info{Created: o.Created}}); err != nil {
			return err
		}
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		out = append(out, Exported{Path: path, Part: part, Pages: len(b.Pages), Bytes: int(st.Size()), Staff: b.Staff, Reduced: b.Reduced})
		return nil
	}
	if o.Score {
		if err := write(engrave.ScoreBook(s, o.Concert), base+".pdf", ""); err != nil {
			return out, err
		}
	}
	used := map[string]bool{base + ".pdf": o.Score}
	for _, li := range o.Parts {
		if li < 0 || li >= len(s.Lines) {
			return out, fmt.Errorf("pdf: no line %d", li+1)
		}
		name := s.Lines[li].Name
		file := PartFileName(base, name)
		for k := 2; used[file]; k++ {
			file = PartFileName(base, fmt.Sprintf("%s %d", name, k)) // two lines of one name
		}
		used[file] = true
		if err := write(engrave.PartBook(s, li, false), file, name); err != nil {
			return out, err
		}
	}
	return out, nil
}
