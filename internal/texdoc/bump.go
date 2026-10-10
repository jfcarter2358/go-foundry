package texdoc

// bump.go — the bump (height) channel of a texture document, and the
// tangent-space normal map derived from it.
//
// A TexDoc has two independent layer stacks per frame: the colour channel
// (palette indices, flattened to the .png the world samples) and the bump
// channel. A bump layer's bytes are *heights*, not palette indices — 0 is
// "nothing here", 1..255 is a height of 1/255..1 — so the bump channel is
// completely independent of the palette and a palette edit or remap can
// never disturb it. That is also why EachLayer (texdoc.go), which exists for
// the palette remap, deliberately walks the colour channel only.
//
// The engine never samples the height field directly. What it samples is the
// tangent-space normal map NormalMapFromHeight derives from it, which is
// what SaveTexDoc writes out beside the albedo as <name>_normal.png. Storing
// the *height* rather than the normal map is what makes the channel
// paintable: a brush stroke means "this bit is higher", which is a thing a
// person can draw, where the three channels of a normal map are not.

import (
	"image"
	"image/color"
	"math"
)

// TexChannel selects which of a frame's two layer stacks the document has
// checked out for editing.
type TexChannel uint8

const (
	ChanColor TexChannel = iota // palette indices; the albedo
	ChanBump                    // heights; the source of the normal map
)

// DefaultBumpStrength is how steeply a fresh document's height field tilts
// the surface normal. The gradient NormalMapFromHeight measures is in height
// units per *texel*, so a strength of 1 on a slope that climbs the full 0..1
// range over eight texels tilts the normal by about 7 degrees — visible, but
// barely. 4 is the point where painted detail reads as relief at ordinary
// viewing distances without looking embossed.
const DefaultBumpStrength = 4

// BumpSliderMax bounds the editor's strength slider. For painted relief,
// past about this the normal is essentially the gradient direction and more
// strength stops changing the look.
const BumpSliderMax = 32

// MaxBumpStrength bounds what a document may carry (and anything a
// hand-edited .ftex could ask for). It is well above BumpSliderMax because
// a height field rebuilt from an imported normal map (HeightFromNormalMap)
// needs it: a steep map spans far more than the 0..1 a height byte holds, and
// the strength is what stretches it back.
const MaxBumpStrength = 128

// emptyBumpLayer is the height-channel counterpart of emptyLayer: all zeroes,
// which is a perfectly flat surface.
func emptyBumpLayer(w, h int) TexLayer {
	return TexLayer{Name: "Height 1", Visible: true, Opacity: 1, Blend: BlendNormal, Idx: make([]uint8, w*h)}
}

// Channel reports which layer stack is checked out into Layers/Active.
func (d *TexDoc) Channel() TexChannel { return d.channel }

// HasBump reports whether this document carries a bump channel at all. When
// false nothing about the bump path costs anything: no layers are stored, no
// bump block is written to the .ftex, and no <name>_normal.png reaches the
// disk or the GPU.
func (d *TexDoc) HasBump() bool { return d.bumpEnabled }

// BumpStrength is the multiplier NormalMapFrame applies to the height
// gradient. Always in (0, MaxBumpStrength].
func (d *TexDoc) BumpStrength() float32 { return d.bumpStrength }

// SetBumpStrength changes the strength the derived normal map is baked at.
// The strength is baked rather than sent to the shader as a uniform: the
// renderer then needs to know nothing about it, and every consumer of the
// map — the world pass, the detail pass, a future one — gets the same relief
// without each having to look the value up per draw.
func (d *TexDoc) SetBumpStrength(s float32) {
	s = clampBumpStrength(s)
	if s == d.bumpStrength {
		return
	}
	d.bumpStrength = s
	d.dirty = true
}

func clampBumpStrength(s float32) float32 {
	if !(s > 0) { // also catches NaN
		return DefaultBumpStrength
	}
	if s > MaxBumpStrength {
		return MaxBumpStrength
	}
	return s
}

// EnableBump gives the document a bump channel — one empty height layer per
// frame — if it doesn't have one already. Painting on the bump channel is
// what a caller normally wants this for; SetChannel(ChanBump) calls it
// itself, so this is only needed to turn the channel on without switching to
// it.
func (d *TexDoc) EnableBump() {
	if d.bumpEnabled {
		return
	}
	d.sync()
	d.bumpEnabled = true
	if d.bumpStrength <= 0 {
		d.bumpStrength = DefaultBumpStrength
	}
	for i := range d.frames {
		if len(d.frames[i].Bump) == 0 {
			d.frames[i].Bump = []TexLayer{emptyBumpLayer(d.W, d.H)}
			d.frames[i].BumpActive = 0
		}
	}
	d.checkout(d.activeFrame, d.channel)
	d.dirty = true
}

// DisableBump discards the bump channel entirely — every frame's height
// layers, in every frame, along with the derived normal map once the
// document is next saved. Checking the colour channel back out first is what
// keeps Layers/Active pointing at something real.
func (d *TexDoc) DisableBump() {
	if !d.bumpEnabled {
		return
	}
	d.sync()
	d.bumpEnabled = false
	for i := range d.frames {
		d.frames[i].Bump = nil
		d.frames[i].BumpActive = 0
	}
	d.checkout(d.activeFrame, ChanColor)
	d.dirty = true
}

// SetChannel checks out c's layer stack for editing, writing back whatever
// was checked out before it. Switching to ChanBump on a document with no
// bump channel creates one — the editor's channel selector is the only way
// to reach the height field, so refusing here would leave no way to turn it
// on. Not undoable, matching SetActiveFrame.
func (d *TexDoc) SetChannel(c TexChannel) {
	if c == ChanBump && !d.bumpEnabled {
		d.EnableBump()
	}
	if c == d.channel {
		return
	}
	d.sync()
	d.checkout(d.activeFrame, c)
}

// ---------- flattening ----------

// heightBlend applies a layer blend mode to two heights in 0..1, the
// grayscale counterpart of blendRGBA.
func heightBlend(dst, src float32, mode TexBlend) float32 {
	switch mode {
	case BlendMultiply:
		return dst * src
	case BlendAdd:
		return clampUnit(dst + src)
	case BlendScreen:
		return 1 - (1-dst)*(1-src)
	default:
		return src
	}
}

func clampUnit(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// flattenHeights composites a bump layer stack bottom-to-top into one height
// field in 0..1, W*H values in row-major order.
//
// A height byte of 0 means "this layer says nothing here", exactly the way
// palette slot 0 is the transparent slot on the colour channel — so a layer
// painted over part of the canvas leaves the rest of the stack showing
// through instead of flattening it. That costs the ability to paint an
// explicit zero on an upper layer, which a height field has no use for: zero
// is what the bottom of the stack already is.
func flattenHeights(w, h int, layers []TexLayer) []float32 {
	out := make([]float32, w*h)
	for li := range layers {
		layer := &layers[li]
		if !layer.Visible || layer.Opacity <= 0 || len(layer.Idx) != len(out) {
			continue
		}
		op := layer.Opacity
		if op > 1 {
			op = 1
		}
		for i, v := range layer.Idx {
			if v == 0 {
				continue
			}
			src := float32(v) / 255
			out[i] = out[i]*(1-op) + heightBlend(out[i], src, layer.Blend)*op
		}
	}
	return out
}

// HeightFrame composites frame i's bump layers into a height field in 0..1.
// A document with no bump channel returns an all-zero (perfectly flat) field
// rather than nil, so callers never have to special-case it.
func (d *TexDoc) HeightFrame(i int) []float32 {
	if !d.bumpEnabled {
		return make([]float32, d.W*d.H)
	}
	return flattenHeights(d.W, d.H, d.Frames()[i].Bump)
}

// Height composites the checked-out frame's height field.
func (d *TexDoc) Height() []float32 { return d.HeightFrame(d.activeFrame) }

// HeightImageFrame renders frame i's height field as a grayscale image — the
// canvas preview while the bump channel is being painted, and nothing the
// engine ever samples.
func (d *TexDoc) HeightImageFrame(i int) *image.RGBA {
	hf := d.HeightFrame(i)
	out := image.NewRGBA(image.Rect(0, 0, d.W, d.H))
	for p, v := range hf {
		g := uint8(clampUnit(v)*255 + 0.5)
		out.Pix[p*4], out.Pix[p*4+1], out.Pix[p*4+2], out.Pix[p*4+3] = g, g, g, 255
	}
	return out
}

// ---------- normal map ----------

// NormalMapFrame derives frame i's tangent-space normal map at the
// document's current strength.
func (d *TexDoc) NormalMapFrame(i int) *image.RGBA {
	return NormalMapFromHeight(d.W, d.H, d.HeightFrame(i), d.bumpStrength)
}

// NormalMap derives the checked-out frame's normal map.
func (d *TexDoc) NormalMap() *image.RGBA { return d.NormalMapFrame(d.activeFrame) }

// NormalMapFromHeight converts a W*H height field in 0..1 to a tangent-space
// normal map, RGB-encoded the usual way (n * 0.5 + 0.5, opaque alpha).
//
// The gradient is a Sobel over the height field, taken with the sampling
// coordinates *wrapped*: a world texture tiles, so the texel past the right
// edge really is the texel at the left edge, and clamping there would put a
// seam of false relief down every tile boundary.
//
// X is the gradient along +U and Y the gradient along +V, matching the
// tangent frame fragment.glsl reconstructs from screen-space derivatives —
// where the bitangent runs along +V, which is *down* the image, since the
// flattened texture is uploaded top row first. Generation and sampling agree
// with each other, which is all that matters for maps this engine authors
// itself; a normal map imported from elsewhere with the opposite green
// convention would light from the wrong side.
func NormalMapFromHeight(w, h int, height []float32, strength float32) *image.RGBA {
	strength = clampBumpStrength(strength)
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	if w <= 0 || h <= 0 || len(height) != w*h {
		return out
	}
	at := func(x, y int) float32 {
		// Euclidean modulo: x can be -1 for the left column.
		x = ((x % w) + w) % w
		y = ((y % h) + h) % h
		return height[y*w+x]
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			tl, t, tr := at(x-1, y-1), at(x, y-1), at(x+1, y-1)
			l, r := at(x-1, y), at(x+1, y)
			bl, b, br := at(x-1, y+1), at(x, y+1), at(x+1, y+1)

			// Sobel, divided by 8 so the result is an averaged central
			// difference — height units per texel, independent of the kernel's
			// weights.
			gx := ((tr + 2*r + br) - (tl + 2*l + bl)) / 8
			gy := ((bl + 2*b + br) - (tl + 2*t + tr)) / 8

			nx := float64(-gx * strength)
			ny := float64(-gy * strength)
			inv := 1 / math.Sqrt(nx*nx+ny*ny+1)
			c := color.RGBA{
				R: encodeNormal(nx * inv),
				G: encodeNormal(ny * inv),
				B: encodeNormal(inv),
				A: 255,
			}
			p := (y*w + x) * 4
			out.Pix[p], out.Pix[p+1], out.Pix[p+2], out.Pix[p+3] = c.R, c.G, c.B, c.A
		}
	}
	return out
}

// encodeNormal maps one component of a unit normal from -1..1 into a byte.
func encodeNormal(v float64) uint8 {
	e := (v*0.5 + 0.5) * 255
	if e < 0 {
		e = 0
	}
	if e > 255 {
		e = 255
	}
	return uint8(e + 0.5)
}

// FlatNormalTexel is what NormalMapFromHeight produces everywhere a height
// field is flat: straight up in tangent space. Named so tests can assert
// against it without re-deriving the encoding.
var FlatNormalTexel = color.RGBA{R: 128, G: 128, B: 255, A: 255}
