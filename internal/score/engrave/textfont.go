package engrave

// textfont.go — the faces text is set in.
//
// A text item names a style (TextFont), not a file; every backend sets it
// in the Go fonts x/image carries (a humanist sans and its italic, bold and
// bold italic), so the layout can measure text exactly as it will be
// drawn: the raster backend draws with these faces, the editor's imgui
// backend rasterises its text runs with them, and the PDF and SVG writers
// draw their outlines (TextGlyphs): no font is embedded anywhere.

import (
	"math"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"foundry/internal/score/glyph"
)

var (
	textOnce  sync.Once
	textFonts [4]*sfnt.Font
	faceMu    sync.Mutex
	faces     = map[faceKey]font.Face{}
)

type faceKey struct {
	f  TextFont
	px int // quarter pixels
}

// TextFace is a style's face at a pixel size (an em of px pixels), shared
// and cached by quarter pixels; nil when the font will not load. A face is
// not safe for concurrent drawing: callers on several goroutines take
// their own.
func TextFace(f TextFont, px float64) font.Face {
	textOnce.Do(func() {
		for i, ttf := range [][]byte{goregular.TTF, goitalic.TTF, gobold.TTF, gobolditalic.TTF} {
			textFonts[i], _ = opentype.Parse(ttf)
		}
	})
	k := faceKey{f, int(math.Round(px * 4))}
	faceMu.Lock()
	defer faceMu.Unlock()
	if fc, ok := faces[k]; ok {
		return fc
	}
	src := textFonts[int(f)%len(textFonts)]
	if src == nil {
		return nil
	}
	of, err := opentype.NewFace(src, &opentype.FaceOptions{Size: float64(k.px) / 4, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil
	}
	// The signs the Go fonts lack (♭ ♮ ♯) come from Bravura (textsign.go).
	fc := &signFace{Face: of, f: f, px: float64(k.px) / 4}
	faces[k] = fc
	return fc
}

// measurePx is the em text is measured at; widths scale linearly from it
// (the faces are unhinted).
const measurePx = 64

var measureMu sync.Mutex

// TextWidthFont is a run of text's width in a style at a size (staff
// spaces, a size of 1 being a 1-space em), as the backends set it.
func TextWidthFont(s string, f TextFont, size float64) float64 {
	measureMu.Lock()
	defer measureMu.Unlock()
	fc := TextFace(f, measurePx)
	if fc == nil {
		return estimateWidth(s, size)
	}
	return float64(font.MeasureString(fc, s)) / 64 / measurePx * size
}

// TextWidth is a run's width in the plain style.
func TextWidth(s string, size float64) float64 { return TextWidthFont(s, TextPlain, size) }

// estimateWidth guesses a width from a serif's average advances, for a
// build whose fonts will not load.
func estimateWidth(s string, size float64) float64 {
	w := 0.0
	for _, r := range s {
		switch {
		case r == ' ' || r == '.' || r == ',' || r == 'i' || r == 'l' || r == 'j' || r == 't' || r == 'f' || r == 'r':
			w += 0.3
		case r == 'm' || r == 'w' || r == 'M' || r == 'W':
			w += 0.8
		case r >= 'A' && r <= 'Z':
			w += 0.68
		default:
			w += 0.52
		}
	}
	return w * size
}

// TextGlyph is one letter of a run of text as outlines: its outline in
// ems (a 1-em size), y down from the baseline, and its pen position from
// the run's start, also in ems. Backends that draw text as paths (the PDF
// and SVG writers: no fonts are embedded) scale both by the item's size.
type TextGlyph struct {
	Font TextFont
	Rune rune
	X    float64
	Path []glyph.Segment
}

var (
	outlineMu sync.Mutex
	outlines  = map[outlineKey][]glyph.Segment{}
)

type outlineKey struct {
	f TextFont
	r rune
}

// TextGlyphs sets a run in a style as outlines, advanced and kerned as
// TextWidthFont measures it. The signs the font lacks are Bravura's
// (textsign.go); other letters the font does not have are left out (their
// advance kept).
func TextGlyphs(s string, f TextFont) []TextGlyph {
	TextFace(f, measurePx) // loads the fonts
	src := textFonts[int(f)%len(textFonts)]
	if src == nil {
		return nil
	}
	outlineMu.Lock()
	defer outlineMu.Unlock()
	var buf sfnt.Buffer
	ppem := fixed.I(measurePx)
	var out []TextGlyph
	pen := 0.0
	prev := sfnt.GlyphIndex(0)
	for i, r := range s {
		if g := textSign(f, r); g != nil {
			out = append(out, TextGlyph{Font: f, Rune: r, X: pen, Path: signOutline(g)})
			pen += signAdvance(g)
			prev = 0
			continue
		}
		gi, err := src.GlyphIndex(&buf, r)
		if err != nil {
			continue
		}
		if i > 0 && prev != 0 && gi != 0 {
			if k, err := src.Kern(&buf, prev, gi, ppem, font.HintingNone); err == nil {
				pen += float64(k) / 64 / measurePx
			}
		}
		k := outlineKey{f, r}
		path, ok := outlines[k]
		if !ok && gi != 0 {
			segs, err := src.LoadGlyph(&buf, gi, ppem, nil)
			if err == nil {
				path = make([]glyph.Segment, 0, len(segs))
				for _, sg := range segs {
					var g glyph.Segment
					switch sg.Op {
					case sfnt.SegmentOpMoveTo:
						g.Op = glyph.MoveTo
					case sfnt.SegmentOpLineTo:
						g.Op = glyph.LineTo
					case sfnt.SegmentOpQuadTo:
						g.Op = glyph.QuadTo
					case sfnt.SegmentOpCubeTo:
						g.Op = glyph.CubeTo
					}
					for j := range sg.Args {
						g.P[j] = glyph.Point{X: float64(sg.Args[j].X) / 64 / measurePx, Y: float64(sg.Args[j].Y) / 64 / measurePx}
					}
					path = append(path, g)
				}
			}
			outlines[k] = path
		}
		if len(path) > 0 {
			out = append(out, TextGlyph{Font: f, Rune: r, X: pen, Path: path})
		}
		if adv, err := src.GlyphAdvance(&buf, gi, ppem, font.HintingNone); err == nil {
			pen += float64(adv) / 64 / measurePx
		}
		prev = gi
	}
	return out
}

// TextStart is where a text item's run starts on its baseline, after its
// alignment.
func TextStart(it *Item) float64 {
	x := it.P.X
	switch it.Align {
	case AlignCenter:
		x -= TextWidthFont(it.Text, it.Font, it.Size) / 2
	case AlignRight:
		x -= TextWidthFont(it.Text, it.Font, it.Size)
	}
	return x
}
