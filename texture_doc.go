package main

// texture_doc.go — pure-Go layered texture document model for the texture
// editor. No GL calls in this file; see texture.go for GPU upload and
// texture_file.go for the on-disk format.

import (
	"fmt"
	"image"
	"image/color"
)

const (
	texMaxLayers = 8
	texMaxDim    = 1024
	texPaletteSz = 256
)

// TexBlend selects how a layer composites onto the layers below it.
type TexBlend uint8

const (
	BlendNormal TexBlend = iota
	BlendMultiply
	BlendAdd
	BlendScreen
)

// TexPalette is the 256-entry colour table shared by every texture. Straight
// (non-premultiplied) alpha; slot 0 is transparent by convention.
type TexPalette [texPaletteSz]color.RGBA

// TexLayer is one layer of a TexDoc: one palette index per texel.
type TexLayer struct {
	Name    string
	Visible bool
	Opacity float32 // 0..1
	Blend   TexBlend
	Idx     []uint8 // one palette index per texel, len == W*H
}

// TexDoc is a layered texture source document.
type TexDoc struct {
	Name   string
	W, H   int
	Pal    TexPalette // copy of the palette this document was authored against
	Layers []TexLayer // index 0 = bottom
	Active int
	Path   string // .ftex path; "" until first save
	dirty  bool
}

// validTexSize is the single place canvas dimensions are checked, so a
// hand-edited or oversized .ftex can't smuggle a larger-than-allowed canvas
// past NewTexDoc, ReadTexDoc, or PNG import.
func validTexSize(w, h int) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("texture dimensions must be positive, got %dx%d", w, h)
	}
	if w > texMaxDim || h > texMaxDim {
		return fmt.Errorf("texture dimensions %dx%d exceed the %dx%d cap", w, h, texMaxDim, texMaxDim)
	}
	return nil
}

// NewTexDoc creates a document with a single empty (fully transparent) layer.
func NewTexDoc(name string, w, h int, pal TexPalette) (*TexDoc, error) {
	if err := validTexSize(w, h); err != nil {
		return nil, err
	}
	idx := make([]uint8, w*h) // zero value == palette slot 0 == transparent
	return &TexDoc{
		Name: name,
		W:    w,
		H:    h,
		Pal:  pal,
		Layers: []TexLayer{
			{Name: "Layer 1", Visible: true, Opacity: 1, Blend: BlendNormal, Idx: idx},
		},
		Active: 0,
	}, nil
}

// cloneTexDoc deep-copies d, including each layer's index buffer, so the
// clone can be mutated (e.g. held as an undo snapshot) without aliasing d.
func cloneTexDoc(d *TexDoc) *TexDoc {
	cp := *d
	cp.Layers = make([]TexLayer, len(d.Layers))
	for i, l := range d.Layers {
		cp.Layers[i] = l
		cp.Layers[i].Idx = append([]uint8(nil), l.Idx...)
	}
	return &cp
}

// MarkDirty flags the document as having unsaved changes.
func (d *TexDoc) MarkDirty()  { d.dirty = true }
func (d *TexDoc) Dirty() bool { return d.dirty }
func (d *TexDoc) ClearDirty() { d.dirty = false }

// AddLayer appends a new empty layer above the current top, up to texMaxLayers.
func (d *TexDoc) AddLayer(name string) error {
	if len(d.Layers) >= texMaxLayers {
		return fmt.Errorf("cannot add layer: already at the %d-layer cap", texMaxLayers)
	}
	d.Layers = append(d.Layers, TexLayer{
		Name: name, Visible: true, Opacity: 1, Blend: BlendNormal,
		Idx: make([]uint8, d.W*d.H),
	})
	d.dirty = true
	return nil
}

// DuplicateLayer copies layer i and inserts the copy directly above it.
func (d *TexDoc) DuplicateLayer(i int) error {
	if i < 0 || i >= len(d.Layers) {
		return fmt.Errorf("layer index %d out of range", i)
	}
	if len(d.Layers) >= texMaxLayers {
		return fmt.Errorf("cannot duplicate layer: already at the %d-layer cap", texMaxLayers)
	}
	src := d.Layers[i]
	dup := TexLayer{
		Name:    src.Name + " copy",
		Visible: src.Visible,
		Opacity: src.Opacity,
		Blend:   src.Blend,
		Idx:     append([]uint8(nil), src.Idx...),
	}
	d.Layers = append(d.Layers, TexLayer{})
	copy(d.Layers[i+2:], d.Layers[i+1:])
	d.Layers[i+1] = dup
	if d.Active > i {
		d.Active++
	}
	d.dirty = true
	return nil
}

// DeleteLayer removes layer i. At least one layer must always remain.
func (d *TexDoc) DeleteLayer(i int) error {
	if i < 0 || i >= len(d.Layers) {
		return fmt.Errorf("layer index %d out of range", i)
	}
	if len(d.Layers) <= 1 {
		return fmt.Errorf("cannot delete the last layer")
	}
	d.Layers = append(d.Layers[:i], d.Layers[i+1:]...)
	if d.Active >= len(d.Layers) {
		d.Active = len(d.Layers) - 1
	}
	d.dirty = true
	return nil
}

// MoveLayer swaps layer i with the layer above (up=true) or below (up=false) it.
func (d *TexDoc) MoveLayer(i int, up bool) error {
	if i < 0 || i >= len(d.Layers) {
		return fmt.Errorf("layer index %d out of range", i)
	}
	j := i + 1
	if !up {
		j = i - 1
	}
	if j < 0 || j >= len(d.Layers) {
		return fmt.Errorf("cannot move layer past the end of the stack")
	}
	d.Layers[i], d.Layers[j] = d.Layers[j], d.Layers[i]
	if d.Active == i {
		d.Active = j
	} else if d.Active == j {
		d.Active = i
	}
	d.dirty = true
	return nil
}

// blendRGBA combines src over dst using the given blend mode (before
// opacity is applied), matching the classic Porter-Duff "over" compositing
// with the blend mode applied to the color channels only.
func blendRGBA(dst, src color.RGBA, mode TexBlend) color.RGBA {
	switch mode {
	case BlendMultiply:
		return color.RGBA{
			R: uint8(uint16(dst.R) * uint16(src.R) / 255),
			G: uint8(uint16(dst.G) * uint16(src.G) / 255),
			B: uint8(uint16(dst.B) * uint16(src.B) / 255),
			A: src.A,
		}
	case BlendAdd:
		return color.RGBA{
			R: addClamp(dst.R, src.R),
			G: addClamp(dst.G, src.G),
			B: addClamp(dst.B, src.B),
			A: src.A,
		}
	case BlendScreen:
		return color.RGBA{
			R: screenChannel(dst.R, src.R),
			G: screenChannel(dst.G, src.G),
			B: screenChannel(dst.B, src.B),
			A: src.A,
		}
	default: // BlendNormal
		return src
	}
}

func addClamp(a, b uint8) uint8 {
	sum := uint16(a) + uint16(b)
	if sum > 255 {
		return 255
	}
	return uint8(sum)
}

func screenChannel(a, b uint8) uint8 {
	// 255 - (255-a)*(255-b)/255
	inv := uint16(255-a) * uint16(255-b) / 255
	return uint8(255 - inv)
}

// compositeOver alpha-blends src (post blend-mode, with layer opacity already
// folded into its alpha) over dst using straight-alpha source-over.
func compositeOver(dst, src color.RGBA) color.RGBA {
	if src.A == 0 {
		return dst
	}
	if src.A == 255 {
		return src
	}
	sa := float32(src.A) / 255
	da := float32(dst.A) / 255
	outA := sa + da*(1-sa)
	if outA <= 0 {
		return color.RGBA{}
	}
	blend := func(sc, dc uint8) uint8 {
		s := float32(sc) / 255
		d := float32(dc) / 255
		out := (s*sa + d*da*(1-sa)) / outA
		v := out * 255
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		return uint8(v + 0.5)
	}
	return color.RGBA{
		R: blend(src.R, dst.R),
		G: blend(src.G, dst.G),
		B: blend(src.B, dst.B),
		A: uint8(outA*255 + 0.5),
	}
}

// Flatten composites every visible layer bottom-to-top in RGBA (honoring
// opacity and blend mode), then quantises the result back to d.Pal so the
// returned image is provably on-palette — image.Paletted carries a real PLTE
// chunk when PNG-encoded.
func (d *TexDoc) Flatten() *image.Paletted {
	rgba := make([]color.RGBA, d.W*d.H) // fully transparent zero value

	for li := range d.Layers {
		layer := &d.Layers[li]
		if !layer.Visible || layer.Opacity <= 0 {
			continue
		}
		op := layer.Opacity
		if op > 1 {
			op = 1
		}
		for i, idx := range layer.Idx {
			src := d.Pal[idx]
			if src.A == 0 {
				continue
			}
			src.A = uint8(float32(src.A) * op)
			if src.A == 0 {
				continue
			}
			blended := blendRGBA(rgba[i], src, layer.Blend)
			rgba[i] = compositeOver(rgba[i], blended)
		}
	}

	pcm := make(color.Palette, texPaletteSz)
	for i, c := range d.Pal {
		pcm[i] = c
	}
	out := image.NewPaletted(image.Rect(0, 0, d.W, d.H), pcm)
	q := newQuantCache(&d.Pal)
	for i, c := range rgba {
		out.Pix[i] = q.Quantize(c)
	}
	return out
}
