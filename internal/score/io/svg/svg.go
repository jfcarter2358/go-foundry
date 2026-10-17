// Package svg writes an engraved display list (internal/score/engrave) as
// an SVG image: a selection of bars exported for a document, a web page or
// another program.
//
// Like the PDF writer it embeds no font. Every music glyph and every
// letter of text is one <path> in <defs>, drawn where it is used by a
// <use> with a transform, so the image looks the same everywhere and a
// thousand noteheads are one outline. Lines are <line>s with butt caps
// (the display list's lines end flush with their points), filled paths
// <path>s with the nonzero rule. Coordinates are staff spaces (the
// viewBox), the image's width and height are those times the scale.
package svg

import (
	"bufio"
	"bytes"
	"fmt"
	"image/color"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"foundry/internal/score/engrave"
	"foundry/internal/score/glyph"
)

// Options are how to write.
type Options struct {
	// Scale is the image's units (CSS pixels) per staff space; 0 is 10.
	Scale float64
	// Clip, when not empty, is the part of the layout to show; the whole
	// layout otherwise.
	Clip engrave.Rect
	// Ink is the colour of the music; zero is black. Paper, when not
	// zero, fills the background (transparent otherwise).
	Ink, Paper color.RGBA
	// Title is the image's <title>, for a reader.
	Title string
}

// Write writes a list as an SVG document.
func Write(w io.Writer, l *engrave.List, o Options) error {
	if o.Scale <= 0 {
		o.Scale = 10
	}
	if o.Ink == (color.RGBA{}) {
		o.Ink = color.RGBA{0, 0, 0, 255}
	}
	clip := o.Clip
	if clip.Empty() {
		clip = engrave.Rect{Max: engrave.Point{X: l.Width, Y: l.Height}}
	}
	d := &doc{defs: map[string]string{}, ink: hex(o.Ink)}
	var body bytes.Buffer
	for i := range l.Items {
		d.item(&body, &l.Items[i])
	}
	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	fmt.Fprintf(bw, "<svg xmlns=\"http://www.w3.org/2000/svg\" xmlns:xlink=\"http://www.w3.org/1999/xlink\" version=\"1.1\" width=\"%s\" height=\"%s\" viewBox=\"%s %s %s %s\">\n",
		num(clip.W()*o.Scale), num(clip.H()*o.Scale), num(clip.Min.X), num(clip.Min.Y), num(clip.W()), num(clip.H()))
	if o.Title != "" {
		fmt.Fprintf(bw, "<title>%s</title>\n", escape(o.Title))
	}
	var ids []string
	for id := range d.defs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	bw.WriteString("<defs>\n")
	for _, id := range ids {
		fmt.Fprintf(bw, "<path id=\"%s\" d=\"%s\"/>\n", id, d.defs[id])
	}
	bw.WriteString("</defs>\n")
	if o.Paper != (color.RGBA{}) {
		fmt.Fprintf(bw, "<rect x=\"%s\" y=\"%s\" width=\"%s\" height=\"%s\" fill=\"%s\"/>\n", num(clip.Min.X), num(clip.Min.Y), num(clip.W()), num(clip.H()), hex(o.Paper))
	}
	ink := hex(o.Ink)
	fmt.Fprintf(bw, "<g fill=\"%s\" stroke=\"none\" fill-rule=\"nonzero\">\n", ink)
	bw.Write(body.Bytes())
	bw.WriteString("</g>\n</svg>\n")
	return bw.Flush()
}

// WriteFile writes a list to a file.
func WriteFile(path string, l *engrave.List, o Options) error {
	var buf bytes.Buffer
	if err := Write(&buf, l, o); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

type doc struct {
	defs map[string]string // id → path data
	ink  string
}

func (d *doc) glyphDef(name string) string {
	id := "g-" + idSafe(name)
	if _, ok := d.defs[id]; ok {
		return id
	}
	g, err := glyph.Bravura().Glyph(name)
	if err != nil {
		return ""
	}
	d.defs[id] = pathData(g.Outline)
	return id
}

func (d *doc) letterDef(tg engrave.TextGlyph) string {
	id := fmt.Sprintf("t%d-%x", tg.Font, tg.Rune)
	if _, ok := d.defs[id]; !ok {
		d.defs[id] = pathData(tg.Path)
	}
	return id
}

func (d *doc) item(b *bytes.Buffer, it *engrave.Item) {
	faint := ""
	if it.Faint {
		faint = ` opacity="0.4"`
	}
	switch it.Kind {
	case engrave.ItemGlyph:
		id := d.glyphDef(it.Glyph)
		if id == "" {
			return
		}
		sy := it.Size
		if it.ScaleY != 0 {
			sy *= it.ScaleY
		}
		fmt.Fprintf(b, "<use xlink:href=\"#%s\" transform=\"translate(%s %s) scale(%s %s)\"%s/>\n", id, num(it.P.X), num(it.P.Y), num(it.Size), num(sy), faint)
	case engrave.ItemLine:
		if it.P == it.Q || it.Width <= 0 {
			return
		}
		fmt.Fprintf(b, "<line x1=\"%s\" y1=\"%s\" x2=\"%s\" y2=\"%s\" stroke=\"%s\" stroke-width=\"%s\" stroke-linecap=\"butt\"%s/>\n",
			num(it.P.X), num(it.P.Y), num(it.Q.X), num(it.Q.Y), d.ink, num(it.Width), faint)
	case engrave.ItemPath:
		if len(it.Path) == 0 {
			return
		}
		fmt.Fprintf(b, "<path d=\"%s\"%s/>\n", pathData(it.Path), faint)
	case engrave.ItemText:
		x0 := engrave.TextStart(it)
		gs := engrave.TextGlyphs(it.Text, it.Font)
		if len(gs) == 0 {
			return
		}
		fmt.Fprintf(b, "<g%s><title>%s</title>", faint, escape(it.Text))
		for _, tg := range gs {
			fmt.Fprintf(b, "<use xlink:href=\"#%s\" transform=\"translate(%s %s) scale(%s)\"/>", d.letterDef(tg), num(x0+tg.X*it.Size), num(it.P.Y), num(it.Size))
		}
		b.WriteString("</g>\n")
	}
}

// pathData is an outline as SVG path data.
func pathData(segs []glyph.Segment) string {
	var b strings.Builder
	open := false
	for _, s := range segs {
		switch s.Op {
		case glyph.MoveTo:
			if open {
				b.WriteString("Z")
			}
			fmt.Fprintf(&b, "M%s %s", num(s.P[0].X), num(s.P[0].Y))
			open = true
		case glyph.LineTo:
			fmt.Fprintf(&b, "L%s %s", num(s.P[0].X), num(s.P[0].Y))
		case glyph.QuadTo:
			fmt.Fprintf(&b, "Q%s %s %s %s", num(s.P[0].X), num(s.P[0].Y), num(s.P[1].X), num(s.P[1].Y))
		case glyph.CubeTo:
			fmt.Fprintf(&b, "C%s %s %s %s %s %s", num(s.P[0].X), num(s.P[0].Y), num(s.P[1].X), num(s.P[1].Y), num(s.P[2].X), num(s.P[2].Y))
		}
	}
	if open {
		b.WriteString("Z")
	}
	return b.String()
}

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

func hex(c color.RGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// idSafe keeps what an XML id takes plainly.
func idSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "_%x", r)
		}
	}
	return b.String()
}

func escape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
