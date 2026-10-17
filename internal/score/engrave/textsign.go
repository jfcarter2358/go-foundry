package engrave

// textsign.go — flats, sharps and naturals inside a run of text.
//
// Musicians type them everywhere words go: "Clarinet in B♭", "F♯ dolce",
// a title "Prelude in C♯ minor", a syllable. The Go fonts have no ♭ ♮ ♯,
// so every backend would draw the font's empty box (or, in the PDF, a gap).
// Each such sign is set instead from Bravura's chord-symbol accidentals —
// the glyphs SMuFL draws for signs standing among letters — at a size and
// lift matched to the text's capitals, in all three places text is made:
// the width the layout measures, the face the raster backends draw with
// (textFace wraps the Go face), and the outlines the PDF and SVG writers
// fill (TextGlyphs). A letter the Go fonts have is never replaced.

import (
	"image"
	"image/draw"
	"math"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"

	"foundry/internal/score/glyph"
)

// textSignGlyphs are the signs a run of text may hold, by the SMuFL glyph
// set for them.
var textSignGlyphs = map[rune]string{
	'♭': "csymAccidentalFlat",
	'♮': "csymAccidentalNatural",
	'♯': "csymAccidentalSharp",
	'𝄪': "csymAccidentalDoubleSharp",
	'𝄫': "csymAccidentalDoubleFlat",
}

const (
	// textSignScale is the em of text a staff space of the glyph is: the
	// chord-symbol sharp is three spaces tall, so a sign stands about as
	// tall as the text's capitals (0.7 em) with its tail below.
	textSignScale = 0.25
	// textSignLift raises it from the baseline (ems), so a flat's bowl sits
	// on the line as a lower-case letter does.
	textSignLift = 0.1
	// textSignPad is the room either side of it (ems).
	textSignPad = 0.03
)

var (
	signOnce sync.Once
	// signs[f][r] is the glyph a style sets a sign in, for the signs that
	// style's Go font lacks.
	signs [4]map[rune]*glyph.Glyph
)

// textSign is the glyph a style sets r in, or nil when the font draws it.
func textSign(f TextFont, r rune) *glyph.Glyph {
	if _, ok := textSignGlyphs[r]; !ok {
		return nil
	}
	signOnce.Do(func() {
		TextFace(f, measurePx) // loads the fonts
		var buf sfnt.Buffer
		for i := range signs {
			signs[i] = map[rune]*glyph.Glyph{}
			src := textFonts[i]
			for r, name := range textSignGlyphs {
				if src != nil {
					if gi, err := src.GlyphIndex(&buf, r); err == nil && gi != 0 {
						continue
					}
				}
				if g, err := glyph.Bravura().Glyph(name); err == nil {
					signs[i][r] = g
				}
			}
		}
	})
	return signs[int(f)%len(signs)][r]
}

// signAdvance is a sign's advance in ems.
func signAdvance(g *glyph.Glyph) float64 { return g.Box.W()*textSignScale + 2*textSignPad }

// signOutline is a sign's outline in ems from its pen position, y down
// from the baseline, as TextGlyph holds one.
func signOutline(g *glyph.Glyph) []glyph.Segment {
	out := make([]glyph.Segment, len(g.Outline))
	dx := textSignPad - g.Box.Min.X*textSignScale
	for i, sg := range g.Outline {
		out[i].Op = sg.Op
		for j := range sg.P {
			out[i].P[j] = glyph.Point{X: dx + sg.P[j].X*textSignScale, Y: sg.P[j].Y*textSignScale - textSignLift}
		}
	}
	return out
}

// signFace is a Go face that draws the signs it lacks from Bravura.
type signFace struct {
	font.Face
	f  TextFont
	px float64
}

// sign is the glyph the face sets r in, or nil for the Go face's own.
func (s *signFace) sign(r rune) *glyph.Glyph { return textSign(s.f, r) }

func (s *signFace) adv(g *glyph.Glyph) fixed.Int26_6 {
	return fixed.Int26_6(math.Round(signAdvance(g) * s.px * 64))
}

// bounds is a sign's ink from the dot, in pixels.
func (s *signFace) bounds(g *glyph.Glyph) (x0, y0, x1, y1 float64) {
	dx := textSignPad*s.px - g.Box.Min.X*textSignScale*s.px
	k := textSignScale * s.px
	return dx + g.Box.Min.X*k, g.Box.Min.Y*k - textSignLift*s.px, dx + g.Box.Max.X*k, g.Box.Max.Y*k - textSignLift*s.px
}

func (s *signFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	g := s.sign(r)
	if g == nil {
		return s.Face.Glyph(dot, r)
	}
	ox, oy := float64(dot.X)/64, float64(dot.Y)/64
	x0, y0, x1, y1 := s.bounds(g)
	dr := image.Rect(int(math.Floor(ox+x0)), int(math.Floor(oy+y0)), int(math.Ceil(ox+x1)), int(math.Ceil(oy+y1)))
	if dr.Empty() {
		return dr, image.NewAlpha(image.Rectangle{}), image.Point{}, s.adv(g), true
	}
	z := vector.NewRasterizer(dr.Dx(), dr.Dy())
	z.DrawOp = draw.Src
	k := textSignScale * s.px
	bx := ox + textSignPad*s.px - g.Box.Min.X*k - float64(dr.Min.X)
	by := oy - textSignLift*s.px - float64(dr.Min.Y)
	pt := func(p glyph.Point) (float32, float32) { return float32(bx + p.X*k), float32(by + p.Y*k) }
	for _, sg := range g.Outline {
		switch sg.Op {
		case glyph.MoveTo:
			z.MoveTo(pt(sg.P[0]))
		case glyph.LineTo:
			z.LineTo(pt(sg.P[0]))
		case glyph.QuadTo:
			ax, ay := pt(sg.P[0])
			bx, by := pt(sg.P[1])
			z.QuadTo(ax, ay, bx, by)
		case glyph.CubeTo:
			ax, ay := pt(sg.P[0])
			bx, by := pt(sg.P[1])
			cx, cy := pt(sg.P[2])
			z.CubeTo(ax, ay, bx, by, cx, cy)
		}
	}
	z.ClosePath()
	mask := image.NewAlpha(image.Rect(0, 0, dr.Dx(), dr.Dy()))
	z.Draw(mask, mask.Bounds(), image.Opaque, image.Point{})
	return dr, mask, image.Point{}, s.adv(g), true
}

func (s *signFace) GlyphBounds(r rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) {
	g := s.sign(r)
	if g == nil {
		return s.Face.GlyphBounds(r)
	}
	x0, y0, x1, y1 := s.bounds(g)
	f := func(v float64) fixed.Int26_6 { return fixed.Int26_6(math.Round(v * 64)) }
	return fixed.Rectangle26_6{Min: fixed.Point26_6{X: f(x0), Y: f(y0)}, Max: fixed.Point26_6{X: f(x1), Y: f(y1)}}, s.adv(g), true
}

func (s *signFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) {
	if g := s.sign(r); g != nil {
		return s.adv(g), true
	}
	return s.Face.GlyphAdvance(r)
}

func (s *signFace) Kern(r0, r1 rune) fixed.Int26_6 {
	if s.sign(r0) != nil || s.sign(r1) != nil {
		return 0
	}
	return s.Face.Kern(r0, r1)
}
