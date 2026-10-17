package svg

import (
	"bytes"
	"encoding/xml"
	"image/color"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/engrave"
	"foundry/internal/score/glyph"
)

func demoList(t *testing.T) *engrave.List {
	t.Helper()
	s, err := score.LoadFile(filepath.Join("..", "..", "..", "..", "resources", "sounds", "score_demo.fscore"))
	if err != nil {
		t.Fatal(err)
	}
	return engrave.Engrave(s, engrave.Options{From: 9, To: 12, Lines: []int{0, 4}})
}

// TestTheSVGIsWellFormedAndComplete parses the document back: every use
// names a definition, every item is drawn, the size is the layout's.
func TestTheSVGIsWellFormedAndComplete(t *testing.T) {
	l := demoList(t)
	var buf bytes.Buffer
	if err := Write(&buf, l, Options{Scale: 12, Title: "Bars 9–12 & more", Paper: color.RGBA{255, 255, 240, 255}}); err != nil {
		t.Fatal(err)
	}
	dec := xml.NewDecoder(&buf)
	defs := map[string]bool{}
	var uses []string
	counts := map[string]int{}
	var root xml.StartElement
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("not well-formed XML: %v", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local == "svg" {
			root = se
		}
		counts[se.Name.Local]++
		for _, a := range se.Attr {
			switch {
			case a.Name.Local == "id":
				defs[a.Value] = true
			case a.Name.Local == "href":
				uses = append(uses, strings.TrimPrefix(a.Value, "#"))
			}
		}
	}
	for _, u := range uses {
		if !defs[u] {
			t.Errorf("use of #%s, which is not defined", u)
		}
	}
	attr := func(n string) string {
		for _, a := range root.Attr {
			if a.Name.Local == n {
				return a.Value
			}
		}
		return ""
	}
	if attr("width") != num(l.Width*12) || attr("viewBox") != "0 0 "+num(l.Width)+" "+num(l.Height) {
		t.Errorf("size %s, viewBox %s", attr("width"), attr("viewBox"))
	}
	var glyphs, lines, paths int
	for _, it := range l.Items {
		switch it.Kind {
		case engrave.ItemGlyph:
			glyphs++
		case engrave.ItemLine:
			lines++
		case engrave.ItemPath:
			paths++
		}
	}
	if counts["line"] != lines || counts["path"] != paths+len(defs) {
		t.Errorf("%d lines for %d, %d paths for %d + %d defs", counts["line"], lines, counts["path"], paths, len(defs))
	}
	if len(uses) < glyphs || counts["rect"] != 1 || counts["title"] < 2 {
		t.Errorf("%d uses for %d glyphs and the text; %d rects, %d titles", len(uses), glyphs, counts["rect"], counts["title"])
	}
	// Noteheads are one definition used many times.
	if n := strings.Count(strings.Join(uses, " "), "g-noteheadBlack"); n < 20 {
		t.Errorf("%d black noteheads", n)
	}
}

func TestClipAndEscapes(t *testing.T) {
	l := demoList(t)
	var buf bytes.Buffer
	clip := engrave.Rect{Min: engrave.Point{X: 2, Y: 3}, Max: engrave.Point{X: 12, Y: 8}}
	if err := Write(&buf, l, Options{Clip: clip, Title: `<a & "b">`}); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if !strings.Contains(s, `viewBox="2 3 10 5"`) || !strings.Contains(s, `width="100"`) || !strings.Contains(s, "&lt;a &amp; &quot;b&quot;&gt;") {
		t.Errorf("clip or escapes:\n%.400s", s)
	}
	if strings.Contains(s, "<rect") {
		t.Errorf("a transparent image has a background")
	}
}

func TestPathData(t *testing.T) {
	got := pathData([]glyph.Segment{
		{Op: glyph.MoveTo, P: [3]engrave.Point{{X: 0, Y: 0}}},
		{Op: glyph.LineTo, P: [3]engrave.Point{{X: 1.5, Y: 0}}},
		{Op: glyph.QuadTo, P: [3]engrave.Point{{X: 2, Y: 1}, {X: 1, Y: 2}}},
		{Op: glyph.CubeTo, P: [3]engrave.Point{{X: 0, Y: 2}, {X: -1, Y: 1}, {X: 0, Y: 0.25}}},
	})
	if got != "M0 0L1.5 0Q2 1 1 2C0 2 -1 1 0 0.25Z" {
		t.Errorf("%q", got)
	}
}
