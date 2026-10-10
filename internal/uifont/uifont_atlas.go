package uifont

// uifont_atlas.go — one (font, size) baked into one page, and the shelf packer
// that places glyphs on it.
//
// One page per atlas is a deliberate constraint, not a simplification waiting
// to be lifted. internal/uidraw batches on (texture, clip rect), so a text run
// that spanned two pages would split into two draw calls at an arbitrary
// character boundary. Keeping every glyph of a face on one page makes a run of
// text exactly one batch, and the page doubles rather than spilling when it
// fills.

import (
	"image"
	"image/color"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// notdefRune keys the substitute glyph. It is not a valid rune, so it can never
// collide with something the font actually has.
const notdefRune rune = -1

const (
	firstPage = 512  // holds printable ASCII up to roughly 48px
	maxPage   = 2048 // past here a face is asking for more than a HUD needs
	glyphPad  = 1    // transparent gutter so bilinear filtering cannot bleed
)

// Glyph is one baked glyph. Distances are in atlas pixels — that is, pixels at
// the atlas's own Size — and the caller scales.
type Glyph struct {
	// U0..V1 are the glyph's corners in the atlas, already normalised.
	U0, V0, U1, V1 float32
	// W and H are the drawn size. Both are zero for a space.
	W, H float32
	// BearingX and BearingY carry the pen to the quad's top-left corner, in the
	// Y-down convention the rest of the UI uses. BearingY is therefore negative
	// for anything that rises above the baseline, which is nearly everything.
	BearingX, BearingY float32
	// Advance moves the pen to the next glyph's origin.
	Advance float32
	// Rev is the atlas revision this glyph was baked at. internal/uidraw
	// compares it against UploadedRev and substitutes notdef for anything the
	// GPU has not seen yet, so a glyph that first appears mid-frame draws as a
	// box for one frame rather than sampling an unwritten region of the page.
	Rev uint64
}

// Atlas is one font at one pixel size, baked into a single page.
//
// The page is white RGB with coverage in alpha, so internal/uidraw needs no
// mode uniform: a glyph quad and an image quad both come out of
// `Color * texture(Tex, UV)`. RGB is 255 even where alpha is 0, which matters —
// filtering between an opaque white texel and a transparent *black* one gives a
// grey fringe, and between an opaque white and a transparent white it does not.
type Atlas struct {
	Font string
	Size float32

	W, H int
	Pix  *image.RGBA

	Ascent, Descent, LineHeight float32

	// TexName is the key this page is registered under in gfx.Textures. The
	// "__ui/" prefix keeps it out of the editor's texture pickers.
	TexName string

	// Rev counts bakes. UploadedRev is what the GPU last saw; the uploader sets
	// it, and nothing here touches it.
	Rev         uint64
	UploadedRev uint64

	face   font.Face // nil means the compiled-in bitmap fallback
	glyphs map[rune]Glyph
	order  []rune // insertion order, so a re-bake after a grow is deterministic

	shelfX, shelfY, shelfH int
}

// newAtlas builds an empty page and bakes printable ASCII into it.
//
// ASCII is baked eagerly rather than on demand because it is what almost every
// label is made of, and doing it in one pass means the common case never
// triggers the mid-frame notdef substitution.
func newAtlas(name string, px float32, f *sfnt.Font) *Atlas {
	a := &Atlas{
		Font:    name,
		Size:    px,
		TexName: atlasTexName(name, px),
		face:    newFace(f, px),
		glyphs:  map[rune]Glyph{},
	}

	if a.face != nil {
		m := a.face.Metrics()
		a.Ascent = f26(m.Ascent)
		a.Descent = f26(m.Descent)
		a.LineHeight = f26(m.Height)
	} else {
		a.Ascent, a.Descent, a.LineHeight = fallbackMetrics(px)
	}

	a.newPage(firstPage)
	a.bake(notdefRune)
	for r := rune(' '); r <= '~'; r++ {
		a.bake(r)
	}
	return a
}

// Glyph returns a rune's glyph, baking it if this is the first time it has been
// asked for. The bool is false when the face has nothing for this rune; the
// caller substitutes Notdef.
func (a *Atlas) Glyph(r rune) (Glyph, bool) {
	if g, ok := a.glyphs[r]; ok {
		return g, g.ok()
	}
	g := a.bake(r)
	return g, g.ok()
}

// Notdef is the substitute box, drawn for a rune the face lacks and for one
// baked too late in the frame to have reached the GPU.
func (a *Atlas) Notdef() Glyph { return a.glyphs[notdefRune] }

// Kern is the adjustment between two adjacent runes, zero for the fallback face
// which has no kerning tables to consult.
func (a *Atlas) Kern(prev, cur rune) float32 {
	if a.face == nil {
		return 0
	}
	return f26(a.face.Kern(prev, cur))
}

// ok reports whether a glyph is a real one rather than the "face has nothing
// here" marker. A space is real: it has no pixels but it does have an advance.
func (g Glyph) ok() bool { return g.Advance != 0 || g.W != 0 }

// bake rasterises one rune onto the page, growing it if there is no room.
func (a *Atlas) bake(r rune) Glyph {
	mask, dr, advance, ok := a.rasterize(r)
	if !ok {
		g := Glyph{Rev: a.Rev}
		a.glyphs[r] = g
		return g
	}

	w, h := dr.Dx(), dr.Dy()
	g := Glyph{
		W: float32(w), H: float32(h),
		BearingX: float32(dr.Min.X), BearingY: float32(dr.Min.Y),
		Advance: advance,
	}

	if w > 0 && h > 0 {
		x, y, placed := a.place(w, h)
		for !placed {
			if !a.grow() {
				// Out of room at the cap. Everything already on the page keeps
				// working; this one rune renders as a box.
				g = a.glyphs[notdefRune]
				a.glyphs[r] = g
				return g
			}
			x, y, placed = a.place(w, h)
		}
		a.blit(mask, dr, x, y)
		a.setUVs(&g, x, y, w, h)
	}

	a.Rev++
	g.Rev = a.Rev
	a.glyphs[r] = g
	a.order = append(a.order, r)
	return g
}

// rasterize produces the coverage mask for one rune, with dr measured from a
// pen sitting at the origin on the baseline.
func (a *Atlas) rasterize(r rune) (mask image.Image, dr image.Rectangle, advance float32, ok bool) {
	if r == notdefRune {
		return notdefMask(a.Size)
	}
	if a.face == nil {
		return fallbackMask(r, a.Size)
	}
	dr, mask, maskp, adv, ok := a.face.Glyph(fixed.P(0, 0), r)
	if !ok {
		return nil, image.Rectangle{}, 0, false
	}
	// face.Glyph hands back a sub-region of a shared mask; translate so that
	// mask coordinates line up with dr.
	if maskp != (image.Point{}) {
		mask = &offsetMask{src: mask, off: maskp.Sub(dr.Min)}
	}
	return mask, dr, f26(adv), true
}

// place finds room for a w x h glyph on the current shelf, opening a new shelf
// when the current one is full. Reports false when the page has no room left.
func (a *Atlas) place(w, h int) (x, y int, ok bool) {
	if a.shelfX+w+glyphPad > a.W {
		a.shelfX = 0
		a.shelfY += a.shelfH + glyphPad
		a.shelfH = 0
	}
	if a.shelfY+h+glyphPad > a.H {
		return 0, 0, false
	}
	x, y = a.shelfX, a.shelfY
	a.shelfX += w + glyphPad
	if h > a.shelfH {
		a.shelfH = h
	}
	return x, y, true
}

// grow doubles the page and re-bakes everything onto it. Every glyph's UVs
// change, so Rev outruns UploadedRev for all of them at once and the whole
// atlas is treated as new — which it is.
func (a *Atlas) grow() bool {
	if a.W >= maxPage {
		return false
	}
	// Detach rather than truncate: a.order[:0] would share its backing array
	// with was, and the re-bake below would overwrite the list it is reading.
	was := a.order
	a.order = nil
	a.newPage(a.W * 2)
	a.glyphs = map[rune]Glyph{}
	a.bake(notdefRune)
	for _, r := range was {
		if r != notdefRune {
			a.bake(r)
		}
	}
	return true
}

// newPage allocates a page of the given side and fills it with transparent
// white. See the Atlas doc for why the RGB matters even at zero alpha.
func (a *Atlas) newPage(side int) {
	a.W, a.H = side, side
	a.Pix = image.NewRGBA(image.Rect(0, 0, side, side))
	for i := 0; i < len(a.Pix.Pix); i += 4 {
		a.Pix.Pix[i] = 255
		a.Pix.Pix[i+1] = 255
		a.Pix.Pix[i+2] = 255
	}
	a.shelfX, a.shelfY, a.shelfH = 0, 0, 0
}

// blit copies a coverage mask into the page at (x, y), writing alpha only.
func (a *Atlas) blit(mask image.Image, dr image.Rectangle, x, y int) {
	for row := 0; row < dr.Dy(); row++ {
		off := a.Pix.PixOffset(x, y+row)
		for col := 0; col < dr.Dx(); col++ {
			_, _, _, al := mask.At(dr.Min.X+col, dr.Min.Y+row).RGBA()
			a.Pix.Pix[off+col*4+3] = uint8(al >> 8)
		}
	}
}

// setUVs converts a pixel rect on the page to normalised coordinates.
func (a *Atlas) setUVs(g *Glyph, x, y, w, h int) {
	fw, fh := float32(a.W), float32(a.H)
	g.U0 = float32(x) / fw
	g.V0 = float32(y) / fh
	g.U1 = float32(x+w) / fw
	g.V1 = float32(y+h) / fh
}

// offsetMask shifts a mask's coordinate space, for the case where face.Glyph
// returns a window into a larger shared image.
type offsetMask struct {
	src image.Image
	off image.Point
}

func (m *offsetMask) ColorModel() color.Model { return m.src.ColorModel() }
func (m *offsetMask) Bounds() image.Rectangle { return m.src.Bounds().Sub(m.off) }
func (m *offsetMask) At(x, y int) color.Color { return m.src.At(x+m.off.X, y+m.off.Y) }

// f26 converts a 26.6 fixed-point value to pixels.
func f26(v fixed.Int26_6) float32 { return float32(v) / 64 }

// atlasTexName is the gfx.Textures key for a page. The "__" prefix is what
// sortedTextureNames in the editor filters on to keep these out of the texture
// pickers.
func atlasTexName(font string, px float32) string {
	return "__ui/" + font + "@" + itoa(int(px))
}

// itoa is strconv.Itoa for small non-negative values, kept local so this
// package's imports stay down to what it genuinely needs.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
