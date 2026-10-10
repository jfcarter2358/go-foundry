package texdoc

// import_layer.go — bringing an outside image into one layer of an open
// document, as opposed to ImportPNG (package texture), which makes a whole
// new document out of one. The image lands on whichever channel is checked
// out: on the colour channel it is quantised to d.Pal, on the bump channel
// its brightness becomes the height.

import (
	"fmt"
	"image"
	"image/color"
)

// ImportFit says how an image whose size differs from the canvas is laid
// onto it. A same-size image comes out identical either way.
type ImportFit uint8

const (
	// FitStretch resamples the image (nearest texel, so pixel art stays
	// crisp) to exactly cover the canvas.
	FitStretch ImportFit = iota
	// FitActualSize keeps one image pixel per texel, centred on the canvas:
	// a smaller image leaves an empty border, a larger one is cropped.
	FitActualSize
)

// ImageLayerIdx converts img into one layer's worth of bytes for the
// checked-out channel, W*H in row-major order. Fully transparent pixels, and
// texels the image doesn't reach, come out 0 — the "nothing here" value on
// both channels — so an imported sprite over a transparent background
// leaves the layers below it showing.
func (d *TexDoc) ImageLayerIdx(img image.Image, fit ImportFit) []uint8 {
	out := make([]uint8, d.W*d.H)
	b := img.Bounds()
	iw, ih := b.Dx(), b.Dy()
	if iw <= 0 || ih <= 0 {
		return out
	}
	offX, offY := (d.W-iw)/2, (d.H-ih)/2
	q := NewQuantCache(&d.Pal)
	for y := 0; y < d.H; y++ {
		for x := 0; x < d.W; x++ {
			var sx, sy int
			if fit == FitStretch {
				sx, sy = x*iw/d.W, y*ih/d.H
			} else {
				sx, sy = x-offX, y-offY
				if sx < 0 || sy < 0 || sx >= iw || sy >= ih {
					continue
				}
			}
			c := color.NRGBAModel.Convert(img.At(b.Min.X+sx, b.Min.Y+sy)).(color.NRGBA)
			if c.A == 0 {
				continue
			}
			if d.channel == ChanBump {
				out[y*d.W+x] = heightFromColor(c)
			} else {
				out[y*d.W+x] = q.Quantize(color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A})
			}
		}
	}
	return out
}

// heightFromColor reads a pixel as a height: Rec. 601 luma, scaled by alpha
// so a soft edge ramps down. Any visible pixel is at least 1, because 0 would
// read as "nothing here" and let the layers below show through an opaque
// black region.
func heightFromColor(c color.NRGBA) uint8 {
	luma := (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
	v := luma * int(c.A) / 255
	return uint8(max(v, 1))
}

// ImportImageLayer puts img into the checked-out channel: as a new layer
// named name inserted directly above the active one (which it then
// becomes), or, with replace, over the active layer's contents. Replacing
// keeps the layer's name, visibility, opacity and blend.
func (d *TexDoc) ImportImageLayer(img image.Image, name string, fit ImportFit, replace bool) error {
	idx := d.ImageLayerIdx(img, fit)
	if replace {
		if d.Active < 0 || d.Active >= len(d.Layers) {
			return fmt.Errorf("no active layer to import into")
		}
		copy(d.Layers[d.Active].Idx, idx)
		d.dirty = true
		return nil
	}
	if len(d.Layers) >= TexMaxLayers {
		return fmt.Errorf("cannot import into a new layer: already at the %d-layer cap", TexMaxLayers)
	}
	at := min(max(d.Active+1, 0), len(d.Layers))
	d.Layers = append(d.Layers, TexLayer{})
	copy(d.Layers[at+1:], d.Layers[at:])
	d.Layers[at] = TexLayer{Name: name, Visible: true, Opacity: 1, Blend: BlendNormal, Idx: idx}
	d.Active = at
	d.dirty = true
	return nil
}
