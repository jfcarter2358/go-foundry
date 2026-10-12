// Package raster draws an engraved display list (internal/score/engrave)
// into an image: the PNG `foundry score render -png` writes and the
// snapshot tests compare.
//
// Every item is filled as a vector path — glyphs from the font's outlines,
// lines as quads, beams and ties as their polygons — through
// golang.org/x/image/vector, each into its own coverage mask so that one
// item's winding never cancels another's where they overlap (a stem over
// a notehead). Text is set in the Go fonts, which x/image carries (the
// display list asks for a serif; the PNG is a proof, the PDF in phase 5
// uses the PDF base fonts).
package raster

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"

	"foundry/internal/score/engrave"
	"foundry/internal/score/glyph"
)

// Options are how to draw.
type Options struct {
	// Scale is pixels per staff space; 0 is 10.
	Scale float64
	// Ink and Paper are the colours; zero values are black on white.
	Ink, Paper color.RGBA
	// Colour, when set, picks each item's colour (a selection, a voice).
	Colour func(it *engrave.Item) color.RGBA
	// Clip, when not empty, draws only that part of the layout (staff
	// spaces); the image is the clip's size.
	Clip engrave.Rect
}

// Render draws a list.
func Render(l *engrave.List, o Options) *image.RGBA {
	if o.Scale <= 0 {
		o.Scale = 10
	}
	if o.Ink == (color.RGBA{}) {
		o.Ink = color.RGBA{0, 0, 0, 255}
	}
	if o.Paper == (color.RGBA{}) {
		o.Paper = color.RGBA{255, 255, 255, 255}
	}
	clip := o.Clip
	if clip.Empty() {
		clip = engrave.Rect{Max: engrave.Point{X: l.Width, Y: l.Height}}
	}
	w := int(math.Ceil(clip.W() * o.Scale))
	h := int(math.Ceil(clip.H() * o.Scale))
	img := image.NewRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	draw.Draw(img, img.Bounds(), image.NewUniform(o.Paper), image.Point{}, draw.Src)
	r := &renderer{img: img, s: o.Scale, ox: -clip.Min.X * o.Scale, oy: -clip.Min.Y * o.Scale, f: glyph.Bravura()}
	for i := range l.Items {
		it := &l.Items[i]
		c := o.Ink
		if o.Colour != nil {
			c = o.Colour(it)
		}
		if it.Faint {
			c = fade(c, o.Paper, 0.35)
		}
		r.item(it, c)
	}
	return img
}

// fade mixes a colour toward the paper.
func fade(c, paper color.RGBA, k float64) color.RGBA {
	mix := func(a, b uint8) uint8 { return uint8(math.Round(float64(b) + (float64(a)-float64(b))*k)) }
	return color.RGBA{mix(c.R, paper.R), mix(c.G, paper.G), mix(c.B, paper.B), 255}
}

type renderer struct {
	img    *image.RGBA
	s      float64
	ox, oy float64
	f      *glyph.Font
	z      vector.Rasterizer
}

// px maps a layout point to pixels.
func (r *renderer) px(p engrave.Point) (float64, float64) { return p.X*r.s + r.ox, p.Y*r.s + r.oy }

// fill rasterises one item's path over a pixel box.
func (r *renderer) fill(bx0, by0, bx1, by1 float64, c color.RGBA, add func(z *vector.Rasterizer, dx, dy float64)) {
	x0, y0 := int(math.Floor(bx0))-1, int(math.Floor(by0))-1
	x1, y1 := int(math.Ceil(bx1))+1, int(math.Ceil(by1))+1
	// Only what is on the image: vector's Draw indexes past the image for
	// a box that hangs off it, and its accumulator clips a path to its own
	// bounds correctly.
	b := r.img.Bounds()
	x0, y0, x1, y1 = max(x0, b.Min.X), max(y0, b.Min.Y), min(x1, b.Max.X), min(y1, b.Max.Y)
	if x1 <= x0 || y1 <= y0 {
		return
	}
	r.z.Reset(x1-x0, y1-y0)
	r.z.DrawOp = draw.Over
	add(&r.z, -float64(x0), -float64(y0))
	r.z.Draw(r.img, image.Rect(x0, y0, x1, y1), image.NewUniform(c), image.Point{})
}

func (r *renderer) item(it *engrave.Item, c color.RGBA) {
	switch it.Kind {
	case engrave.ItemGlyph:
		g, err := r.f.Glyph(it.Glyph)
		if err != nil {
			return
		}
		sx := it.Size * r.s
		sy := sx
		if it.ScaleY != 0 {
			sy *= it.ScaleY
		}
		px, py := r.px(it.P)
		b := g.Box
		r.fill(px+b.Min.X*sx, py+b.Min.Y*sy, px+b.Max.X*sx, py+b.Max.Y*sy, c, func(z *vector.Rasterizer, dx, dy float64) {
			glyph.AddPath(z, g.Outline, px+dx, py+dy, sx, sy)
		})
	case engrave.ItemLine:
		ax, ay := r.px(it.P)
		bx, by := r.px(it.Q)
		hw := it.Width * r.s / 2
		l := math.Hypot(bx-ax, by-ay)
		if l == 0 {
			return
		}
		nx, ny := -(by-ay)/l*hw, (bx-ax)/l*hw
		pts := [4][2]float64{{ax + nx, ay + ny}, {bx + nx, by + ny}, {bx - nx, by - ny}, {ax - nx, ay - ny}}
		x0, y0, x1, y1 := bounds4(pts)
		r.fill(x0, y0, x1, y1, c, func(z *vector.Rasterizer, dx, dy float64) {
			z.MoveTo(float32(pts[0][0]+dx), float32(pts[0][1]+dy))
			for _, p := range pts[1:] {
				z.LineTo(float32(p[0]+dx), float32(p[1]+dy))
			}
			z.ClosePath()
		})
	case engrave.ItemPath:
		if len(it.Path) == 0 {
			return
		}
		x0, y0 := math.Inf(1), math.Inf(1)
		x1, y1 := math.Inf(-1), math.Inf(-1)
		for _, s := range it.Path {
			for k := 0; k < segPoints(s.Op); k++ {
				px, py := r.px(s.P[k])
				x0, y0, x1, y1 = math.Min(x0, px), math.Min(y0, py), math.Max(x1, px), math.Max(y1, py)
			}
		}
		r.fill(x0, y0, x1, y1, c, func(z *vector.Rasterizer, dx, dy float64) {
			glyph.AddPath(z, it.Path, r.ox+dx, r.oy+dy, r.s, r.s)
		})
	case engrave.ItemText:
		r.text(it, c)
	}
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

func bounds4(p [4][2]float64) (x0, y0, x1, y1 float64) {
	x0, y0, x1, y1 = p[0][0], p[0][1], p[0][0], p[0][1]
	for _, q := range p[1:] {
		x0, y0 = math.Min(x0, q[0]), math.Min(y0, q[1])
		x1, y1 = math.Max(x1, q[0]), math.Max(y1, q[1])
	}
	return
}

// ---------- text ----------

func (r *renderer) text(it *engrave.Item, c color.RGBA) {
	fc := engrave.TextFace(it.Font, it.Size*r.s)
	if fc == nil {
		return
	}
	d := font.Drawer{Dst: r.img, Src: image.NewUniform(c), Face: fc}
	x, y := r.px(it.P)
	w := float64(d.MeasureString(it.Text)) / 64
	switch it.Align {
	case engrave.AlignCenter:
		x -= w / 2
	case engrave.AlignRight:
		x -= w
	}
	d.Dot = fixed.Point26_6{X: fixed.Int26_6(math.Round(x * 64)), Y: fixed.Int26_6(math.Round(y * 64))}
	d.DrawString(it.Text)
}
