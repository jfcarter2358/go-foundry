// Package pdf writes engraved pages (internal/score/engrave) as a PDF, and
// reads back the PDFs it writes.
//
// The writer is the package's own, with nothing outside the standard
// library: PDF 1.4, one content stream a page, every stream deflated.
// Music is drawn from Bravura's outlines and text from the Go fonts'
// outlines, as filled paths — no font is embedded, so a page looks the
// same in every viewer and the file carries no font licence (Bravura's OFL
// covers drawings made with it). Each glyph and each letter is drawn once,
// as a form XObject in its own units, and placed on the page by a matrix,
// so a score's thousand noteheads are one outline and a thousand short
// `Do`s. Lines are stroked with butt caps (the display list's lines end
// flush with their points), filled paths fill by the nonzero rule.
//
// A page's display list is in staff spaces, y down; the page's content
// stream begins with the one matrix that turns that into PDF points, y up
// (Book.Space millimetres a space). The document's Info dictionary carries
// the title, composer, subject, creator, producer and dates.
//
// read.go parses a PDF of this kind back — the cross-reference table, the
// objects, the streams — and rasterize.go draws its pages, which is how
// the tests check what the writer wrote without a PDF library.
package pdf

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"foundry/internal/score/engrave"
	"foundry/internal/score/glyph"
)

// Info is the document's metadata.
type Info struct {
	Title, Author, Subject, Keywords string
	// Creator is the application that made the document's content,
	// Producer the one that wrote the PDF; empty is Foundry's names.
	Creator, Producer string
	// Created is the creation (and modification) date; zero is now.
	Created time.Time
}

// Options are how to write.
type Options struct {
	Info Info
	// NoCompress writes streams as they are (for reading by eye).
	NoCompress bool
}

// Page is one page to write: a display list in its own units, Scale
// points to a unit, Width and Height the page in points.
type Page struct {
	List          *engrave.List
	Scale         float64
	Width, Height float64
}

// PointsPerMM is a millimetre in PDF points.
const PointsPerMM = 72 / 25.4

// BookPages are a laid-out book's pages, ready to write.
func BookPages(b *engrave.Book) []Page {
	s := b.Space * PointsPerMM
	var out []Page
	for _, pg := range b.Pages {
		out = append(out, Page{List: b.List(pg), Scale: s, Width: b.Width * s, Height: b.Height * s})
	}
	return out
}

// WriteBook writes a laid-out book, its metadata from the book unless
// the options give it.
func WriteBook(w io.Writer, b *engrave.Book, o Options) error {
	if o.Info.Title == "" {
		o.Info.Title = b.Title
		if b.Part != "" {
			if o.Info.Title != "" {
				o.Info.Title += " — "
			}
			o.Info.Title += b.Part
		}
	}
	if o.Info.Author == "" {
		o.Info.Author = b.Composer
	}
	if o.Info.Subject == "" {
		o.Info.Subject = "Full score"
		if b.Part != "" {
			o.Info.Subject = "Part: " + b.Part
		}
	}
	return Write(w, BookPages(b), o)
}

// WriteFile writes a laid-out book to a file.
func WriteFile(path string, b *engrave.Book, o Options) error {
	var buf bytes.Buffer
	if err := WriteBook(&buf, b, o); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// Write writes pages as a PDF document.
func Write(w io.Writer, pages []Page, o Options) error {
	if len(pages) == 0 {
		return fmt.Errorf("pdf: no pages")
	}
	d := &doc{o: o, forms: map[string]*form{}}
	contents := make([][]byte, len(pages))
	for i, p := range pages {
		if p.Width <= 0 || p.Height <= 0 || p.Scale <= 0 || p.List == nil {
			return fmt.Errorf("pdf: page %d has no size", i+1)
		}
		contents[i] = d.content(p)
	}
	return d.write(w, pages, contents)
}

// doc is one document being written.
type doc struct {
	o     Options
	forms map[string]*form // by resource name
	buf   bytes.Buffer
	offs  []int // object offsets, by number-1
}

// form is a reusable outline: a glyph or a letter.
type form struct {
	name string
	path []glyph.Segment
	box  glyph.Rect
	obj  int
}

// glyphForm is the form for a music glyph.
func (d *doc) glyphForm(name string) *form {
	key := "G" + resName(name)
	if f, ok := d.forms[key]; ok {
		return f
	}
	g, err := glyph.Bravura().Glyph(name)
	if err != nil {
		d.forms[key] = nil
		return nil
	}
	f := &form{name: key, path: g.Outline, box: g.Box}
	d.forms[key] = f
	return f
}

// letterForm is the form for one letter of a text style.
func (d *doc) letterForm(tg engrave.TextGlyph) *form {
	key := fmt.Sprintf("T%d_%X", tg.Font, tg.Rune)
	if f, ok := d.forms[key]; ok {
		return f
	}
	f := &form{name: key, path: tg.Path, box: pathBox(tg.Path)}
	d.forms[key] = f
	return f
}

// resName keeps the characters a PDF name takes plainly.
func resName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "_%X", r)
		}
	}
	return b.String()
}

func pathBox(p []glyph.Segment) glyph.Rect {
	var r glyph.Rect
	first := true
	for _, s := range p {
		for k := 0; k < segPoints(s.Op); k++ {
			q := s.P[k]
			if first {
				r, first = glyph.Rect{Min: q, Max: q}, false
				continue
			}
			r.Min.X, r.Min.Y = math.Min(r.Min.X, q.X), math.Min(r.Min.Y, q.Y)
			r.Max.X, r.Max.Y = math.Max(r.Max.X, q.X), math.Max(r.Max.Y, q.Y)
		}
	}
	return r
}

func segPoints(op glyph.SegOp) int {
	switch op {
	case glyph.QuadTo:
		return 2
	case glyph.CubeTo:
		return 3
	}
	return 1
}

// num formats a number the way PDF wants it: no exponent, at most four
// decimals, no trailing zeros.
func num(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		v = 0
	}
	s := strconv.FormatFloat(v, 'f', 4, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "-0" || s == "" {
		return "0"
	}
	return s
}

// content is a page's content stream.
func (d *doc) content(p Page) []byte {
	var b bytes.Buffer
	// Units y down from the top left → points y up from the bottom left.
	fmt.Fprintf(&b, "q %s 0 0 %s 0 %s cm\n0 J 0 j\n", num(p.Scale), num(-p.Scale), num(p.Height))
	grey := -1.0
	colour := func(g float64) {
		if g != grey {
			fmt.Fprintf(&b, "%s g %s G\n", num(g), num(g))
			grey = g
		}
	}
	for i := range p.List.Items {
		it := &p.List.Items[i]
		g := 0.0
		if it.Faint {
			g = 0.6
		}
		colour(g)
		switch it.Kind {
		case engrave.ItemGlyph:
			f := d.glyphForm(it.Glyph)
			if f == nil {
				continue
			}
			sy := it.Size
			if it.ScaleY != 0 {
				sy *= it.ScaleY
			}
			fmt.Fprintf(&b, "q %s 0 0 %s %s %s cm /%s Do Q\n", num(it.Size), num(sy), num(it.P.X), num(it.P.Y), f.name)
		case engrave.ItemLine:
			if it.P == it.Q || it.Width <= 0 {
				continue
			}
			fmt.Fprintf(&b, "%s w %s %s m %s %s l S\n", num(it.Width), num(it.P.X), num(it.P.Y), num(it.Q.X), num(it.Q.Y))
		case engrave.ItemPath:
			if len(it.Path) == 0 {
				continue
			}
			writePath(&b, it.Path)
			b.WriteString("f\n")
		case engrave.ItemText:
			x0 := engrave.TextStart(it)
			for _, tg := range engrave.TextGlyphs(it.Text, it.Font) {
				f := d.letterForm(tg)
				if len(f.path) == 0 {
					continue
				}
				fmt.Fprintf(&b, "q %s 0 0 %s %s %s cm /%s Do Q\n", num(it.Size), num(it.Size), num(x0+tg.X*it.Size), num(it.P.Y), f.name)
			}
		}
	}
	b.WriteString("Q\n")
	return b.Bytes()
}

// writePath writes an outline as path operators, quadratic curves raised
// to cubics, each contour closed.
func writePath(b *bytes.Buffer, segs []glyph.Segment) {
	var cur glyph.Point
	open := false
	for _, s := range segs {
		switch s.Op {
		case glyph.MoveTo:
			if open {
				b.WriteString("h\n")
			}
			cur, open = s.P[0], true
			fmt.Fprintf(b, "%s %s m\n", num(cur.X), num(cur.Y))
		case glyph.LineTo:
			cur = s.P[0]
			fmt.Fprintf(b, "%s %s l\n", num(cur.X), num(cur.Y))
		case glyph.QuadTo:
			q, e := s.P[0], s.P[1]
			c1 := glyph.Point{X: cur.X + 2.0/3*(q.X-cur.X), Y: cur.Y + 2.0/3*(q.Y-cur.Y)}
			c2 := glyph.Point{X: e.X + 2.0/3*(q.X-e.X), Y: e.Y + 2.0/3*(q.Y-e.Y)}
			fmt.Fprintf(b, "%s %s %s %s %s %s c\n", num(c1.X), num(c1.Y), num(c2.X), num(c2.Y), num(e.X), num(e.Y))
			cur = e
		case glyph.CubeTo:
			fmt.Fprintf(b, "%s %s %s %s %s %s c\n", num(s.P[0].X), num(s.P[0].Y), num(s.P[1].X), num(s.P[1].Y), num(s.P[2].X), num(s.P[2].Y))
			cur = s.P[2]
		}
	}
	if open {
		b.WriteString("h\n")
	}
}

// ---------- the file ----------

// newObj starts an object and returns its number.
func (d *doc) newObj() int {
	d.offs = append(d.offs, d.buf.Len())
	n := len(d.offs)
	fmt.Fprintf(&d.buf, "%d 0 obj\n", n)
	return n
}

func (d *doc) endObj() { d.buf.WriteString("endobj\n") }

// reserve takes an object number to be written later.
func (d *doc) reserve() int {
	d.offs = append(d.offs, -1)
	return len(d.offs)
}

// begin writes a reserved object's header.
func (d *doc) begin(n int) {
	d.offs[n-1] = d.buf.Len()
	fmt.Fprintf(&d.buf, "%d 0 obj\n", n)
}

// stream writes a stream object's body: its dictionary entries, then the
// data (deflated unless NoCompress).
func (d *doc) stream(dict string, data []byte) {
	filter := ""
	if !d.o.NoCompress {
		var z bytes.Buffer
		zw, _ := zlib.NewWriterLevel(&z, zlib.BestCompression)
		zw.Write(data)
		zw.Close()
		data, filter = z.Bytes(), " /Filter /FlateDecode"
	}
	fmt.Fprintf(&d.buf, "<< %s%s /Length %d >>\nstream\n", dict, filter, len(data))
	d.buf.Write(data)
	d.buf.WriteString("\nendstream\n")
}

func (d *doc) write(w io.Writer, pages []Page, contents [][]byte) error {
	d.buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	catalog, pagesObj, info, res := d.reserve(), d.reserve(), d.reserve(), d.reserve()

	// The forms, in name order (a stable file for the same pages).
	var names []string
	for k, f := range d.forms {
		if f != nil {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	for _, k := range names {
		f := d.forms[k]
		var body bytes.Buffer
		writePath(&body, f.path)
		body.WriteString("f\n")
		bb := f.box
		f.obj = d.newObj()
		d.stream(fmt.Sprintf("/Type /XObject /Subtype /Form /BBox [%s %s %s %s]", num(bb.Min.X-0.01), num(bb.Min.Y-0.01), num(bb.Max.X+0.01), num(bb.Max.Y+0.01)), body.Bytes())
		d.endObj()
	}
	d.begin(res)
	d.buf.WriteString("<< /ProcSet [/PDF] /XObject <<")
	for _, k := range names {
		fmt.Fprintf(&d.buf, " /%s %d 0 R", k, d.forms[k].obj)
	}
	d.buf.WriteString(" >> >>\n")
	d.endObj()

	var kids []string
	for i, p := range pages {
		c := d.newObj()
		d.stream("", contents[i])
		d.endObj()
		pg := d.newObj()
		fmt.Fprintf(&d.buf, "<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %s %s] /Resources %d 0 R /Contents %d 0 R >>\n",
			pagesObj, num(p.Width), num(p.Height), res, c)
		d.endObj()
		kids = append(kids, fmt.Sprintf("%d 0 R", pg))
	}
	d.begin(pagesObj)
	fmt.Fprintf(&d.buf, "<< /Type /Pages /Kids [%s] /Count %d >>\n", strings.Join(kids, " "), len(pages))
	d.endObj()
	d.begin(catalog)
	fmt.Fprintf(&d.buf, "<< /Type /Catalog /Pages %d 0 R >>\n", pagesObj)
	d.endObj()

	in := d.o.Info
	if in.Creator == "" {
		in.Creator = "Foundry Score editor"
	}
	if in.Producer == "" {
		in.Producer = "Foundry (internal/score/io/pdf)"
	}
	if in.Created.IsZero() {
		in.Created = time.Now()
	}
	date := pdfDate(in.Created)
	d.begin(info)
	d.buf.WriteString("<<")
	for _, kv := range [][2]string{{"Title", in.Title}, {"Author", in.Author}, {"Subject", in.Subject}, {"Keywords", in.Keywords},
		{"Creator", in.Creator}, {"Producer", in.Producer}} {
		if kv[1] != "" {
			fmt.Fprintf(&d.buf, " /%s %s", kv[0], pdfString(kv[1]))
		}
	}
	fmt.Fprintf(&d.buf, " /CreationDate %s /ModDate %s >>\n", pdfString(date), pdfString(date))
	d.endObj()

	for i, off := range d.offs {
		if off < 0 {
			return fmt.Errorf("pdf: object %d never written", i+1)
		}
	}
	xref := d.buf.Len()
	fmt.Fprintf(&d.buf, "xref\n0 %d\n0000000000 65535 f \n", len(d.offs)+1)
	for _, off := range d.offs {
		fmt.Fprintf(&d.buf, "%010d 00000 n \n", off)
	}
	id := md5.Sum(d.buf.Bytes())
	fmt.Fprintf(&d.buf, "trailer\n<< /Size %d /Root %d 0 R /Info %d 0 R /ID [<%x> <%x>] >>\nstartxref\n%d\n%%%%EOF\n",
		len(d.offs)+1, catalog, info, id, id, xref)
	_, err := w.Write(d.buf.Bytes())
	return err
}

// pdfDate is a date in the PDF's form, D:YYYYMMDDHHmmSS+HH'mm'.
func pdfDate(t time.Time) string {
	_, off := t.Zone()
	sign := byte('+')
	if off < 0 {
		sign, off = '-', -off
	}
	return fmt.Sprintf("D:%s%c%02d'%02d'", t.Format("20060102150405"), sign, off/3600, off%3600/60)
}

// pdfString is a text string: literal and escaped when it is plain ASCII,
// UTF-16 with its byte-order mark in hex otherwise.
func pdfString(s string) string {
	ascii := true
	for _, r := range s {
		if r >= 128 || r < 32 {
			ascii = false
		}
	}
	if ascii {
		r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
		return "(" + r.Replace(s) + ")"
	}
	var b strings.Builder
	b.WriteString("<FEFF")
	for _, u := range utf16.Encode([]rune(s)) {
		fmt.Fprintf(&b, "%04X", u)
	}
	b.WriteString(">")
	return b.String()
}
