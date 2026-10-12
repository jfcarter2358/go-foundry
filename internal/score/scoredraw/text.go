package scoredraw

// text.go — text runs as textures.
//
// imgui-go's draw list sets text only in ImGui's own font at its own size,
// so tempo marks, "cresc.", technique text and rehearsal letters would not
// follow the zoom or the layout's styles. Instead every visible text run is
// rasterised once per pixel size in the same Go faces the layout measures
// with (engrave.TextFace) onto a page of its own, and drawn as a textured
// quad like a glyph. Runs are packed on shelves; a full page grows to
// 2048 px and then starts again (the frame re-bakes what it shows).

import (
	"image"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"

	"foundry/internal/score/engrave"
)

// textKey is one run at one size.
type textKey struct {
	text string
	font engrave.TextFont
	px   int
}

// textSlot is where a run sits on the page: its box in pixels, the
// baseline's left end inside it, and its advance.
type textSlot struct {
	x, y, w, h int
	ox, oy     float32
	adv        float32
}

// textPage is the page of rasterised runs.
type textPage struct {
	img           *image.RGBA
	slots         map[textKey]*textSlot
	shelfX        int
	shelfY        int
	shelfH        int
	rev, uploaded uint64
	tex           uint32
	// reset counts the page starting again: a pass that saw it reset
	// bakes again.
	reset int
}

const (
	textPageStart = 512
	textPageMax   = 2048
	textPad       = 1
)

func newTextPage() *textPage {
	return &textPage{img: image.NewRGBA(image.Rect(0, 0, textPageStart, textPageStart)), slots: map[textKey]*textSlot{}, rev: 1}
}

// clear empties the page at a size.
func (p *textPage) clear(size int) {
	p.img = image.NewRGBA(image.Rect(0, 0, size, size))
	p.slots = map[textKey]*textSlot{}
	p.shelfX, p.shelfY, p.shelfH = 0, 0, 0
	p.rev++
	p.reset++
}

// slot is a run on the page, baked if it is not there yet; ok is false for
// a run the fonts cannot set.
func (p *textPage) slot(k textKey) (*textSlot, bool) {
	if s, ok := p.slots[k]; ok {
		return s, true
	}
	face := engrave.TextFace(k.font, float64(k.px))
	if face == nil || k.text == "" {
		return nil, false
	}
	b, adv := font.BoundString(face, k.text)
	x0, y0 := b.Min.X.Floor(), b.Min.Y.Floor()
	x1, y1 := b.Max.X.Ceil(), b.Max.Y.Ceil()
	w, h := x1-x0+2*textPad, y1-y0+2*textPad
	if w > textPageMax || h > textPageMax {
		return nil, false
	}
	if p.shelfX+w > p.img.Bounds().Dx() {
		p.shelfX, p.shelfY, p.shelfH = 0, p.shelfY+p.shelfH, 0
	}
	if p.shelfY+h > p.img.Bounds().Dy() {
		// Full: a bigger page, or the same one empty.
		size := min(p.img.Bounds().Dx()*2, textPageMax)
		p.clear(size)
	}
	s := &textSlot{x: p.shelfX, y: p.shelfY, w: w, h: h,
		ox: float32(textPad - x0), oy: float32(textPad - y0), adv: float32(adv) / 64}
	d := font.Drawer{Dst: p.img, Src: image.White, Face: face,
		Dot: fixed.Point26_6{X: fixed.I(s.x + textPad - x0), Y: fixed.I(s.y + textPad - y0)}}
	d.DrawString(k.text)
	// White with coverage in alpha (straight, not premultiplied), as the
	// glyph atlas has it, so a tint is the colour with no dark fringe.
	r := image.Rect(s.x, s.y, s.x+w, s.y+h)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := p.img.Pix[y*p.img.Stride:]
		for x := r.Min.X; x < r.Max.X; x++ {
			if row[x*4+3] != 0 {
				row[x*4], row[x*4+1], row[x*4+2] = 255, 255, 255
			}
		}
	}
	p.shelfX += w
	p.shelfH = max(p.shelfH, h)
	p.slots[k] = s
	p.rev++
	return s, true
}

// textKeyOf is an item's run at a scale.
func textKeyOf(it *engrave.Item, scale float32) textKey {
	return textKey{text: it.Text, font: it.Font, px: max(4, int(math.Round(float64(it.Size)*float64(scale))))}
}
