// Package scoredraw draws engraved notation (internal/score/engrave) into
// an imgui draw list: the Score editor's view of the music.
//
// Glyphs are textured quads cut from the font atlas (internal/score/glyph,
// atlas.go) for the view's zoom bucket, the page uploaded here as a GL
// texture whenever it has changed; text runs likewise from a page of their
// own, set in the faces the layout measured them with (text.go); lines
// are imgui's antialiased lines;
// filled paths (beams, ties) are flattened and triangulated; horizontal and
// vertical lines (staff lines, stems) are snapped to whole pixels. Colours come
// per item from the caller, so a selection, a voice colour or a playing
// note is a colour, not a second engraving.
//
// Drawing is two passes over the visible items: the first asks the atlas
// for every glyph (which may bake new ones, or grow the page and move every
// UV), then the page is uploaded, then the second pass emits quads with
// UVs that are final for this frame.
package scoredraw

import (
	"image"
	"math"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/inkyblackness/imgui-go/v4"

	"foundry/internal/score/engrave"
	"foundry/internal/score/glyph"
)

// Canvas is what Draw draws into; imgui.DrawList is one, and tests use a
// recorder.
type Canvas interface {
	AddImageV(textureID imgui.TextureID, posMin, posMax, uvMin, uvMax imgui.Vec2, tintCol imgui.PackedColor)
	AddLineV(p1, p2 imgui.Vec2, col imgui.PackedColor, thickness float32)
	AddRectFilled(min, max imgui.Vec2, col imgui.PackedColor)
	AddTriangleFilled(p1, p2, p3 imgui.Vec2, col imgui.PackedColor)
	AddText(pos imgui.Vec2, col imgui.PackedColor, text string)
}

// View is where and how a list is drawn.
type View struct {
	// Origin is the screen position of the list's (0, 0).
	Origin imgui.Vec2
	// Scale is pixels per staff space.
	Scale float32
	// Clip, when not empty, is the part of the list (staff spaces) that is
	// visible; items wholly outside it are skipped.
	Clip engrave.Rect
	// Ink is every item's colour unless Colour says otherwise.
	Ink imgui.PackedColor
	// Colour, when set, picks an item's colour.
	Colour func(it *engrave.Item) imgui.PackedColor
	// Faint is the alpha a faint item (an implied bar rest) is drawn at;
	// 0 is 0.35.
	Faint float32
}

// Renderer keeps the atlas pages and their textures across frames.
type Renderer struct {
	atlases *glyph.Atlases
	tex     map[*glyph.Atlas]uint32

	// text is the page of text runs (text.go).
	text *textPage

	// Upload makes or refreshes a page's texture and returns its name; old
	// is the page's texture so far (0 for none); UploadText does the same
	// for the text page. Delete frees one. All are GL by default; tests
	// without GL stand in.
	Upload     func(a *glyph.Atlas, old uint32) uint32
	UploadText func(img *image.RGBA, old uint32) uint32
	Delete     func(id uint32)
}

// New makes a renderer over the embedded font.
func New() *Renderer {
	return &Renderer{
		atlases: glyph.NewAtlases(glyph.Bravura()), tex: map[*glyph.Atlas]uint32{}, text: newTextPage(),
		Upload: uploadGL, UploadText: uploadImageGL, Delete: deleteGL,
	}
}

// Release frees every texture (with the GL context current).
func (r *Renderer) Release() {
	for a, id := range r.tex {
		if id != 0 {
			r.Delete(id)
		}
		a.UploadedRev = 0
	}
	r.tex = map[*glyph.Atlas]uint32{}
	if r.text.tex != 0 {
		r.Delete(r.text.tex)
		r.text.tex, r.text.uploaded = 0, 0
	}
}

// Texture is the texture for a zoom's atlas page after Draw (0 before).
func (r *Renderer) Texture(scale float32) uint32 { return r.tex[r.atlases.For(float64(scale))] }

// Draw draws a list. The GL context must be current (the page may upload).
func (r *Renderer) Draw(c Canvas, l *engrave.List, v View) {
	if v.Scale <= 0 || l == nil {
		return
	}
	a := r.atlases.For(float64(v.Scale))
	visible := func(it *engrave.Item) bool {
		if v.Clip.Empty() {
			return true
		}
		b := engrave.ItemBounds(it)
		return b.Max.X >= v.Clip.Min.X && b.Min.X <= v.Clip.Max.X && b.Max.Y >= v.Clip.Min.Y && b.Min.Y <= v.Clip.Max.Y
	}
	// Pass one: every glyph and text run onto its page. A text page that
	// starts again part way bakes the frame's runs once more.
	for pass := 0; pass < 2; pass++ {
		reset := r.text.reset
		for i := range l.Items {
			it := &l.Items[i]
			if !visible(it) {
				continue
			}
			switch it.Kind {
			case engrave.ItemGlyph:
				a.Glyph(it.Glyph, it.Size)
			case engrave.ItemText:
				r.text.slot(textKeyOf(it, v.Scale))
			}
		}
		if r.text.reset == reset {
			break
		}
	}
	if a.Rev != a.UploadedRev || r.tex[a] == 0 {
		r.tex[a] = r.Upload(a, r.tex[a])
		a.UploadedRev = a.Rev
	}
	tex := imgui.TextureID(r.tex[a])
	tp := r.text
	if len(tp.slots) > 0 && (tp.rev != tp.uploaded || tp.tex == 0) {
		tp.tex = r.UploadText(tp.img, tp.tex)
		tp.uploaded = tp.rev
	}

	s := v.Scale
	pt := func(p engrave.Point) imgui.Vec2 {
		return imgui.Vec2{X: v.Origin.X + float32(p.X)*s, Y: v.Origin.Y + float32(p.Y)*s}
	}
	faint := v.Faint
	if faint <= 0 {
		faint = 0.35
	}
	for i := range l.Items {
		it := &l.Items[i]
		if !visible(it) {
			continue
		}
		col := v.Ink
		if v.Colour != nil {
			col = v.Colour(it)
		}
		if it.Faint {
			col = withAlpha(col, faint)
		}
		switch it.Kind {
		case engrave.ItemGlyph:
			g, ok := a.Glyph(it.Glyph, it.Size)
			if !ok {
				continue
			}
			sy := 1.0
			if it.ScaleY != 0 {
				sy = it.ScaleY
			}
			p0 := pt(engrave.Point{X: it.P.X + g.Quad.Min.X, Y: it.P.Y + g.Quad.Min.Y*sy})
			p1 := pt(engrave.Point{X: it.P.X + g.Quad.Max.X, Y: it.P.Y + g.Quad.Max.Y*sy})
			c.AddImageV(tex, p0, p1, imgui.Vec2{X: g.U0, Y: g.V0}, imgui.Vec2{X: g.U1, Y: g.V1}, col)
		case engrave.ItemLine:
			a, b := pt(it.P), pt(it.Q)
			if r0, r1, ok := pixelRect(a, b, float32(it.Width)*s); ok {
				c.AddRectFilled(r0, r1, col)
				continue
			}
			// No thinner than a pixel, so a slanted line never fades out
			// at a small zoom.
			w := float32(math.Max(float64(float32(it.Width)*s), 1))
			c.AddLineV(a, b, col, w)
		case engrave.ItemPath:
			tol := 0.25 / float64(s) // a quarter pixel
			for _, poly := range engrave.Flatten(it.Path, tol) {
				for _, t := range engrave.Triangulate(poly) {
					c.AddTriangleFilled(pt(t[0]), pt(t[1]), pt(t[2]), col)
				}
			}
		case engrave.ItemText:
			sl, ok := tp.slot(textKeyOf(it, s))
			if !ok {
				continue
			}
			p := pt(it.P)
			switch it.Align {
			case engrave.AlignCenter:
				p.X -= sl.adv / 2
			case engrave.AlignRight:
				p.X -= sl.adv
			}
			// Whole pixels, so the run's pixels land on the screen's.
			p0 := imgui.Vec2{X: float32(math.Round(float64(p.X - sl.ox))), Y: float32(math.Round(float64(p.Y - sl.oy)))}
			p1 := imgui.Vec2{X: p0.X + float32(sl.w), Y: p0.Y + float32(sl.h)}
			pw, ph := float32(tp.img.Bounds().Dx()), float32(tp.img.Bounds().Dy())
			c.AddImageV(imgui.TextureID(tp.tex), p0, p1,
				imgui.Vec2{X: float32(sl.x) / pw, Y: float32(sl.y) / ph}, imgui.Vec2{X: float32(sl.x+sl.w) / pw, Y: float32(sl.y+sl.h) / ph}, col)
		}
	}
}

// pixelRect is a horizontal or vertical line as a rectangle on whole pixels,
// at least one thick — staff lines, stems, ledger lines and barlines. Drawn
// as an antialiased line, a line a pixel or so wide that falls between two
// rows is two half-grey rows, and a staff of them reads as faint; snapped,
// it is one row of ink. A slanted line is not a rectangle (ok is false).
func pixelRect(a, b imgui.Vec2, width float32) (min, max imgui.Vec2, ok bool) {
	w := float32(math.Max(1, math.Round(float64(width))))
	snap := func(lo, hi float32) (float32, float32) {
		if lo > hi {
			lo, hi = hi, lo
		}
		l, h := float32(math.Round(float64(lo))), float32(math.Round(float64(hi)))
		if h < l+1 {
			h = l + 1
		}
		return l, h
	}
	across := func(c float32) (float32, float32) {
		top := float32(math.Floor(float64(c-w/2) + 0.5))
		return top, top + w
	}
	switch {
	case math.Abs(float64(a.Y-b.Y)) < 1e-3:
		x0, x1 := snap(a.X, b.X)
		y0, y1 := across(a.Y)
		return imgui.Vec2{X: x0, Y: y0}, imgui.Vec2{X: x1, Y: y1}, true
	case math.Abs(float64(a.X-b.X)) < 1e-3:
		y0, y1 := snap(a.Y, b.Y)
		x0, x1 := across(a.X)
		return imgui.Vec2{X: x0, Y: y0}, imgui.Vec2{X: x1, Y: y1}, true
	}
	return min, max, false
}

// withAlpha scales a packed colour's alpha.
func withAlpha(c imgui.PackedColor, k float32) imgui.PackedColor {
	a := float32(uint32(c)>>24) * k
	return imgui.PackedColor(uint32(c)&0x00ffffff | uint32(a+0.5)<<24)
}

// uploadGL makes or refreshes a page's texture: linear filtering, no
// mipmaps (they would blend neighbouring glyphs), clamped edges.
func uploadGL(a *glyph.Atlas, old uint32) uint32 {
	t := old
	if t == 0 {
		gl.GenTextures(1, &t)
	}
	gl.BindTexture(gl.TEXTURE_2D, t)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, int32(a.W), int32(a.H), 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(a.Pix.Pix))
	gl.BindTexture(gl.TEXTURE_2D, 0)
	return t
}

// uploadImageGL makes or refreshes the text page's texture.
func uploadImageGL(img *image.RGBA, old uint32) uint32 {
	t := old
	if t == 0 {
		gl.GenTextures(1, &t)
	}
	gl.BindTexture(gl.TEXTURE_2D, t)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	b := img.Bounds()
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, int32(b.Dx()), int32(b.Dy()), 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(img.Pix))
	gl.BindTexture(gl.TEXTURE_2D, 0)
	return t
}

func deleteGL(id uint32) { gl.DeleteTextures(1, &id) }
