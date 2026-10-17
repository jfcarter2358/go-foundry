package glyph

// atlas.go — the font rasterised for the editor.
//
// The Score editor draws notation through imgui, which draws textured
// triangles; a glyph there is a quad cut from a texture page. An Atlas is
// that page for one zoom: every glyph asked of it, at every size asked
// (a grace note's head is two thirds of a normal one), rasterised from the
// same outlines the PNG and PDF backends draw, onto one RGBA image that is
// white with the coverage in alpha — so a tint colour multiplies straight
// through and a selected note is the same quad drawn in another colour.
//
// The page is CPU-side. internal/score/scoredraw uploads it (Rev against
// UploadedRev, as internal/uifont's pages are) and hands imgui the texture.
// Zooms come in buckets (ZoomBucket): a pinch that moves the zoom by 1% must
// not bake a new page every frame, and a glyph drawn at a few percent off
// its bucket's size through linear filtering is indistinguishable.

import (
	"fmt"
	"image"
	"math"

	"golang.org/x/image/vector"
)

const (
	atlasFirstPage = 512
	atlasMaxPage   = 4096
	atlasPad       = 2 // a transparent gutter, so filtering cannot bleed between glyphs
)

// MinPxPerSpace and MaxPxPerSpace bound the zooms an atlas is baked at.
const (
	MinPxPerSpace = 2
	MaxPxPerSpace = 64
)

// ZoomBucket rounds a zoom (pixels per staff space) to the size an atlas is
// baked at: quarter steps of an octave (2^(1/4), about 19% apart), clamped.
func ZoomBucket(pxPerSpace float64) float64 {
	p := math.Max(MinPxPerSpace, math.Min(MaxPxPerSpace, pxPerSpace))
	return math.Exp2(math.Round(math.Log2(p)*4) / 4)
}

// AtlasGlyph is one glyph on an atlas page.
type AtlasGlyph struct {
	// U0..V1 are the quad's corners on the page, normalised.
	U0, V0, U1, V1 float32
	// Quad is where the quad goes relative to the glyph's origin, in staff
	// spaces at the size it was asked for: draw the page's [U0,V0]-[U1,V1]
	// into origin+Quad.Min .. origin+Quad.Max.
	Quad Rect
	// Rev is the page revision the glyph was baked at.
	Rev uint64
}

type atlasKey struct {
	name string
	size int // the size asked for, in thousandths
}

// Atlas is the font rasterised at one zoom onto one page.
type Atlas struct {
	Font *Font
	// PxPerSpace is the zoom: pixels in a staff space at size 1.
	PxPerSpace float64

	W, H int
	Pix  *image.RGBA

	// TexName is the key the page is uploaded under.
	TexName string
	// Rev counts bakes; UploadedRev is the revision the GPU has (the
	// uploader sets it).
	Rev, UploadedRev uint64

	glyphs map[atlasKey]AtlasGlyph
	order  []atlasKey

	shelfX, shelfY, shelfH int
	z                      vector.Rasterizer
	mask                   []uint8
}

// NewAtlas makes an empty page for a zoom.
func NewAtlas(f *Font, pxPerSpace float64) *Atlas {
	a := &Atlas{
		Font: f, PxPerSpace: pxPerSpace,
		TexName: fmt.Sprintf("__score/glyphs@%.3f", pxPerSpace),
		glyphs:  map[atlasKey]AtlasGlyph{},
	}
	a.newPage(atlasFirstPage)
	return a
}

// Glyph returns a glyph at a size (1 = normal, 0.66 a grace note), baking
// it on first use. ok is false for a name the font lacks or a glyph too big
// for the largest page.
func (a *Atlas) Glyph(name string, size float64) (AtlasGlyph, bool) {
	k := atlasKey{name, int(math.Round(size * 1000))}
	if g, ok := a.glyphs[k]; ok {
		return g, g.Rev != 0
	}
	g, ok := a.bake(k)
	return g, ok
}

// Len is how many glyph sizes are on the page.
func (a *Atlas) Len() int { return len(a.glyphs) }

func (a *Atlas) bake(k atlasKey) (AtlasGlyph, bool) {
	gl, err := a.Font.Glyph(k.name)
	if err != nil || k.size <= 0 {
		a.glyphs[k] = AtlasGlyph{}
		return AtlasGlyph{}, false
	}
	px := a.PxPerSpace * float64(k.size) / 1000
	box := gl.Box
	// Whole pixels round the ink, one spare each side for antialiasing.
	x0, y0 := math.Floor(box.Min.X*px)-1, math.Floor(box.Min.Y*px)-1
	x1, y1 := math.Ceil(box.Max.X*px)+1, math.Ceil(box.Max.Y*px)+1
	w, h := int(x1-x0), int(y1-y0)
	if w <= 0 || h <= 0 {
		a.glyphs[k] = AtlasGlyph{}
		return AtlasGlyph{}, false
	}
	x, y, placed := a.place(w, h)
	for !placed {
		if !a.grow() {
			a.glyphs[k] = AtlasGlyph{}
			return AtlasGlyph{}, false
		}
		x, y, placed = a.place(w, h)
	}
	a.rasterise(gl, px, -x0, -y0, w, h, x, y)
	a.Rev++
	g := AtlasGlyph{
		U0: float32(x) / float32(a.W), V0: float32(y) / float32(a.H),
		U1: float32(x+w) / float32(a.W), V1: float32(y+h) / float32(a.H),
		Quad: Rect{Min: Point{x0 / a.PxPerSpace, y0 / a.PxPerSpace}, Max: Point{x1 / a.PxPerSpace, y1 / a.PxPerSpace}},
		Rev:  a.Rev,
	}
	a.glyphs[k] = g
	a.order = append(a.order, k)
	return g, true
}

// rasterise draws the glyph's coverage at px pixels a staff space, its
// origin at (ox, oy) in a w×h box, into the page at (x, y).
func (a *Atlas) rasterise(gl *Glyph, px, ox, oy float64, w, h, x, y int) {
	a.z.Reset(w, h)
	AddPath(&a.z, gl.Outline, ox, oy, px, px)
	// An exact-size mask: vector's fast path for an *image.Alpha takes the
	// stride to be the width, so a sub-image of a bigger buffer smears.
	if cap(a.mask) < w*h {
		a.mask = make([]uint8, w*h*2)
	}
	m := &image.Alpha{Pix: a.mask[:w*h], Stride: w, Rect: image.Rect(0, 0, w, h)}
	clear(m.Pix)
	a.z.Draw(m, m.Rect, image.Opaque, image.Point{})
	for row := 0; row < h; row++ {
		src := m.Pix[row*m.Stride : row*m.Stride+w]
		off := a.Pix.PixOffset(x, y+row)
		for col, c := range src {
			a.Pix.Pix[off+col*4+3] = c
		}
	}
}

func (a *Atlas) place(w, h int) (x, y int, ok bool) {
	if a.shelfX+w+atlasPad > a.W {
		a.shelfX = 0
		a.shelfY += a.shelfH + atlasPad
		a.shelfH = 0
	}
	if a.shelfY+h+atlasPad > a.H || w+atlasPad > a.W {
		return 0, 0, false
	}
	x, y = a.shelfX+atlasPad, a.shelfY+atlasPad
	a.shelfX += w + atlasPad
	a.shelfH = max(a.shelfH, h)
	return x, y, true
}

// grow doubles the page and bakes everything again: every UV moves, so the
// whole page is new to the GPU.
func (a *Atlas) grow() bool {
	if a.W >= atlasMaxPage {
		return false
	}
	was := a.order
	a.order = nil
	a.glyphs = map[atlasKey]AtlasGlyph{}
	a.newPage(a.W * 2)
	for _, k := range was {
		a.bake(k)
	}
	return true
}

// newPage allocates transparent white: white even where alpha is zero, so
// filtering at a glyph's edge never mixes in black.
func (a *Atlas) newPage(side int) {
	a.W, a.H = side, side
	a.Pix = image.NewRGBA(image.Rect(0, 0, side, side))
	for i := 0; i < len(a.Pix.Pix); i += 4 {
		a.Pix.Pix[i], a.Pix.Pix[i+1], a.Pix.Pix[i+2] = 255, 255, 255
	}
	a.shelfX, a.shelfY, a.shelfH = 0, 0, 0
}

// PathAdder takes outline segments; *vector.Rasterizer is one.
type PathAdder interface {
	MoveTo(x, y float32)
	LineTo(x, y float32)
	QuadTo(bx, by, cx, cy float32)
	CubeTo(bx, by, cx, cy, dx, dy float32)
	ClosePath()
}

// AddPath feeds an outline to p, each point scaled by (sx, sy) and moved by
// (dx, dy): x' = x·sx + dx. Each contour is closed before the next starts.
func AddPath(p PathAdder, segs []Segment, dx, dy, sx, sy float64) {
	t := func(q Point) (float32, float32) { return float32(q.X*sx + dx), float32(q.Y*sy + dy) }
	open := false
	for _, s := range segs {
		switch s.Op {
		case MoveTo:
			if open {
				p.ClosePath()
			}
			p.MoveTo(t(s.P[0]))
			open = true
		case LineTo:
			p.LineTo(t(s.P[0]))
		case QuadTo:
			bx, by := t(s.P[0])
			cx, cy := t(s.P[1])
			p.QuadTo(bx, by, cx, cy)
		case CubeTo:
			bx, by := t(s.P[0])
			cx, cy := t(s.P[1])
			ex, ey := t(s.P[2])
			p.CubeTo(bx, by, cx, cy, ex, ey)
		}
	}
	if open {
		p.ClosePath()
	}
}

// Atlases are the pages for every zoom bucket in use.
type Atlases struct {
	Font  *Font
	pages map[float64]*Atlas
}

// NewAtlases starts an empty set over a font.
func NewAtlases(f *Font) *Atlases { return &Atlases{Font: f, pages: map[float64]*Atlas{}} }

// For is the page for a zoom's bucket, made on first use.
func (s *Atlases) For(pxPerSpace float64) *Atlas {
	b := ZoomBucket(pxPerSpace)
	a, ok := s.pages[b]
	if !ok {
		a = NewAtlas(s.Font, b)
		s.pages[b] = a
	}
	return a
}

// Pages are the pages made so far.
func (s *Atlases) Pages() []*Atlas {
	out := make([]*Atlas, 0, len(s.pages))
	for _, a := range s.pages {
		out = append(out, a)
	}
	return out
}
