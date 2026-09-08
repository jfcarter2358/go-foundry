// Package texdoc is the pure-Go layered texture document model used by the
// texture editor: TexDoc/TexLayer/TexPalette plus the RGBA<->palette-index
// quantiser. It has no GL/SDL/imgui dependency — GPU upload lives in
// go and the on-disk .ftex/.pal format in texture_file.go, both in
// package main, which import this package. Extracted from what used to be
// texture_doc.go + texture_palette.go in package main; see
// RESTRUCTURE_PROPOSAL.md §5 Phase B.
package texdoc

import (
	"fmt"
	"image"
	"image/color"
)

const (
	TexMaxLayers = 8
	TexMaxFrames = 32
	TexMaxDim    = 1024
	TexPaletteSz = 256
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
type TexPalette [TexPaletteSz]color.RGBA

// TexLayer is one layer of a TexDoc: one palette index per texel.
type TexLayer struct {
	Name    string
	Visible bool
	Opacity float32 // 0..1
	Blend   TexBlend
	Idx     []uint8 // one palette index per texel, len == W*H
}

// TexFrame is one entry in a TexDoc's animation stack: its own independent
// layer stacks (collapsed to a single image by Flatten/FlattenFrame when the
// texture is used), plus which of their layers is active for editing.
//
// There are two stacks, not one: Layers is the colour channel (palette
// indices) and Bump the height channel the normal map is derived from (see
// bump.go). Bump is nil on a document with no bump channel, which is every
// document until somebody paints one.
type TexFrame struct {
	Layers     []TexLayer // colour, index 0 = bottom
	Active     int
	Bump       []TexLayer // heights, index 0 = bottom; nil when there is no bump channel
	BumpActive int
}

// TexDoc is a layered texture source document, animated over one or more
// frames. A single-frame doc behaves exactly like the plain layered image
// this type used to be; a multi-frame doc is an animation stack played back
// at (frame count) frames per second — see the texture package's runtime
// registration and gfx.AdvanceAnimatedTextures.
//
// Only one frame is ever being edited at a time, and Layers/Active are that
// frame *checked out*: every painting and layer op in the texture package
// reads and writes them directly, exactly as before frames existed. frames
// holds every frame including the checked-out one, but frames[activeFrame]
// is only guaranteed fresh right after sync() runs — every method that needs
// to see every frame (Frames, FlattenFrame, CloneTexDoc, WriteTexDoc,
// EachLayer, and the frame structural ops below) calls it first. Frames
// other than the checked-out one are never stale: they are only ever
// written by checking out onto them, which immediately makes them "the
// checked-out one".
type TexDoc struct {
	Name        string
	W, H        int
	Pal         TexPalette // copy of the palette this document was authored against
	Layers      []TexLayer // the checked-out frame+channel's layers; see frames
	Active      int        // active layer index within Layers
	frames      []TexFrame
	activeFrame int
	channel     TexChannel // which of the checked-out frame's two stacks Layers is
	// Bump channel state. bumpEnabled is what makes the channel exist at all
	// — see EnableBump/DisableBump in bump.go — and bumpStrength is baked into
	// the normal map the save path derives.
	bumpEnabled  bool
	bumpStrength float32
	Path         string // .ftex path; "" until first save
	dirty        bool
}

// ValidTexSize is the single place canvas dimensions are checked, so a
// hand-edited or oversized .ftex can't smuggle a larger-than-allowed canvas
// past NewTexDoc, ReadTexDoc, or PNG import.
func ValidTexSize(w, h int) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("texture dimensions must be positive, got %dx%d", w, h)
	}
	if w > TexMaxDim || h > TexMaxDim {
		return fmt.Errorf("texture dimensions %dx%d exceed the %dx%d cap", w, h, TexMaxDim, TexMaxDim)
	}
	return nil
}

func emptyLayer(w, h int) TexLayer {
	return TexLayer{Name: "Layer 1", Visible: true, Opacity: 1, Blend: BlendNormal, Idx: make([]uint8, w*h)}
}

// NewTexDoc creates a single-frame document with a single empty (fully
// transparent) layer.
func NewTexDoc(name string, w, h int, pal TexPalette) (*TexDoc, error) {
	if err := ValidTexSize(w, h); err != nil {
		return nil, err
	}
	layers := []TexLayer{emptyLayer(w, h)}
	return &TexDoc{
		Name:         name,
		W:            w,
		H:            h,
		Pal:          pal,
		Layers:       layers,
		Active:       0,
		frames:       []TexFrame{{Layers: layers, Active: 0}},
		activeFrame:  0,
		bumpStrength: DefaultBumpStrength,
	}, nil
}

// NewTexDocFromFrames builds a document from an already-decoded animation
// stack — the .ftex reader's entry point (texture_file.go), which parses
// every frame's layers itself and has no other way to populate the
// unexported frames/activeFrame bookkeeping. frames must be non-empty; frame
// 0's colour channel is checked out.
//
// bumpStrength is only meaningful when the frames carry bump layers; a file
// written before the bump channel existed passes 0 and gets the default,
// which nothing then reads because there is no height field to scale.
func NewTexDocFromFrames(w, h int, pal TexPalette, frames []TexFrame, bumpStrength float32) (*TexDoc, error) {
	if err := ValidTexSize(w, h); err != nil {
		return nil, err
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("a texture document needs at least one frame")
	}
	bump := false
	for i := range frames {
		if len(frames[i].Bump) > 0 {
			bump = true
			break
		}
	}
	d := &TexDoc{
		W:            w,
		H:            h,
		Pal:          pal,
		frames:       frames,
		bumpEnabled:  bump,
		bumpStrength: clampBumpStrength(bumpStrength),
	}
	// A file that carries a bump channel in some frames but not others (only
	// reachable by hand-editing one) gets the missing ones filled in, so
	// every frame's two stacks are always both present or both absent.
	if bump {
		for i := range d.frames {
			if len(d.frames[i].Bump) == 0 {
				d.frames[i].Bump = []TexLayer{emptyBumpLayer(w, h)}
				d.frames[i].BumpActive = 0
			}
		}
	}
	d.checkout(0, ChanColor)
	return d, nil
}

// checkout points Layers/Active at frame i's channel c, creating that
// frame's height stack if the document has a bump channel and this frame
// somehow doesn't. Callers must sync() whatever was checked out before.
func (d *TexDoc) checkout(i int, c TexChannel) {
	d.activeFrame = i
	d.channel = c
	f := &d.frames[i]
	if c == ChanBump {
		if len(f.Bump) == 0 {
			f.Bump = []TexLayer{emptyBumpLayer(d.W, d.H)}
			f.BumpActive = 0
		}
		d.Layers, d.Active = f.Bump, f.BumpActive
		return
	}
	d.Layers, d.Active = f.Layers, f.Active
}

// sync writes the checked-out frame (Layers/Active) back into its slot in
// frames, so every frame in frames is current. Called by anything that needs
// to see every frame rather than just the one being edited.
func (d *TexDoc) sync() {
	f := &d.frames[d.activeFrame]
	if d.channel == ChanBump {
		f.Bump, f.BumpActive = d.Layers, d.Active
		return
	}
	f.Layers, f.Active = d.Layers, d.Active
}

// Frames returns every frame in the animation stack, the checked-out one
// included and up to date. The result aliases the document's own storage —
// treat it as read-only, or route mutations through EachLayer.
func (d *TexDoc) Frames() []TexFrame {
	d.sync()
	return d.frames
}

// FrameCount reports the size of the animation stack. 1 means "not animated".
func (d *TexDoc) FrameCount() int { return len(d.frames) }

// ActiveFrameIndex is the frame currently checked out into Layers/Active.
func (d *TexDoc) ActiveFrameIndex() int { return d.activeFrame }

// FPS is the document's playback rate: however many frames are in the stack,
// that many play per second, so a full cycle through the stack always takes
// one second. A single-frame document has no meaningful rate — 0 signals
// "not animated" rather than "1 fps".
func (d *TexDoc) FPS() float32 {
	if len(d.frames) <= 1 {
		return 0
	}
	return float32(len(d.frames))
}

// SetActiveFrame checks out frame i for editing, first writing back whatever
// was checked out before it. Not an undoable action, matching how switching
// the active layer within a frame isn't either.
func (d *TexDoc) SetActiveFrame(i int) error {
	if i < 0 || i >= len(d.frames) {
		return fmt.Errorf("frame index %d out of range", i)
	}
	if i == d.activeFrame {
		return nil
	}
	d.sync()
	d.checkout(i, d.channel)
	return nil
}

// AddFrame appends a new frame — a single empty layer, same starting point
// as NewTexDoc — to the end of the animation stack, up to TexMaxFrames.
func (d *TexDoc) AddFrame() error {
	if len(d.frames) >= TexMaxFrames {
		return fmt.Errorf("cannot add frame: already at the %d-frame cap", TexMaxFrames)
	}
	d.sync()
	f := TexFrame{Layers: []TexLayer{emptyLayer(d.W, d.H)}, Active: 0}
	if d.bumpEnabled {
		f.Bump = []TexLayer{emptyBumpLayer(d.W, d.H)}
	}
	d.frames = append(d.frames, f)
	d.dirty = true
	return nil
}

// cloneFrame deep-copies f — both channels, including every layer's index
// buffer.
func cloneFrame(f TexFrame) TexFrame {
	return TexFrame{
		Layers:     cloneLayers(f.Layers),
		Active:     f.Active,
		Bump:       cloneLayers(f.Bump),
		BumpActive: f.BumpActive,
	}
}

// cloneLayers deep-copies one layer stack, preserving nil (which is how a
// document with no bump channel is told apart from one whose height stack
// happens to be empty).
func cloneLayers(src []TexLayer) []TexLayer {
	if src == nil {
		return nil
	}
	out := make([]TexLayer, len(src))
	for i, l := range src {
		out[i] = l
		out[i].Idx = append([]uint8(nil), l.Idx...)
	}
	return out
}

// DuplicateFrame copies frame i and inserts the copy directly after it.
func (d *TexDoc) DuplicateFrame(i int) error {
	if i < 0 || i >= len(d.frames) {
		return fmt.Errorf("frame index %d out of range", i)
	}
	if len(d.frames) >= TexMaxFrames {
		return fmt.Errorf("cannot duplicate frame: already at the %d-frame cap", TexMaxFrames)
	}
	d.sync()
	dup := cloneFrame(d.frames[i])
	d.frames = append(d.frames, TexFrame{})
	copy(d.frames[i+2:], d.frames[i+1:])
	d.frames[i+1] = dup
	if d.activeFrame > i {
		d.activeFrame++
	}
	d.dirty = true
	return nil
}

// DeleteFrame removes frame i. At least one frame must always remain. If i
// was the checked-out frame, the frame that lands in its slot (or the new
// last frame, if i was last) is checked out in its place.
func (d *TexDoc) DeleteFrame(i int) error {
	if i < 0 || i >= len(d.frames) {
		return fmt.Errorf("frame index %d out of range", i)
	}
	if len(d.frames) <= 1 {
		return fmt.Errorf("cannot delete the last frame")
	}
	d.sync()
	deletingActive := i == d.activeFrame
	d.frames = append(d.frames[:i], d.frames[i+1:]...)
	switch {
	case deletingActive:
		next := d.activeFrame
		if next >= len(d.frames) {
			next = len(d.frames) - 1
		}
		d.checkout(next, d.channel)
	case d.activeFrame > i:
		d.activeFrame--
	}
	d.dirty = true
	return nil
}

// MoveFrame swaps frame i with the frame after it (up=true) or before it
// (up=false) it — "up"/"down" naming this shares with MoveLayer, though a
// filmstrip usually draws frames left-to-right rather than bottom-to-top.
func (d *TexDoc) MoveFrame(i int, up bool) error {
	if i < 0 || i >= len(d.frames) {
		return fmt.Errorf("frame index %d out of range", i)
	}
	j := i + 1
	if !up {
		j = i - 1
	}
	if j < 0 || j >= len(d.frames) {
		return fmt.Errorf("cannot move frame past the end of the stack")
	}
	d.sync()
	d.frames[i], d.frames[j] = d.frames[j], d.frames[i]
	if d.activeFrame == i {
		d.activeFrame = j
	} else if d.activeFrame == j {
		d.activeFrame = i
	}
	d.dirty = true
	return nil
}

// EachLayer calls fn once per COLOUR layer in every frame of the animation
// stack, the checked-out frame included, with a pointer into the document's
// own storage — a palette remap (the one existing whole-document layer walk)
// is the reason this exists: it has to reach layers no matter which frame is
// active, and reach the checked-out one for real, not a stale copy.
//
// Bump layers are deliberately not walked. Their bytes are heights, not
// palette indices (see bump.go), so putting them through a palette remap
// would silently rewrite the height field into whatever the nearest colour
// happened to map to.
func (d *TexDoc) EachLayer(fn func(l *TexLayer)) {
	d.sync()
	for fi := range d.frames {
		for li := range d.frames[fi].Layers {
			fn(&d.frames[fi].Layers[li])
		}
	}
	// When the colour channel is the checked-out one, its Layers slice aliases
	// the same backing array as d.frames[d.activeFrame].Layers (sync() just
	// copied the slice header), so mutations fn made above are already visible
	// through d.Layers too. When the bump channel is checked out, d.Layers
	// points at the height stack, which this walk does not touch either way.
}

// CloneTexDoc deep-copies d, including every frame and each of their layers'
// index buffers, so the clone can be mutated (e.g. held as an undo snapshot)
// without aliasing d.
func CloneTexDoc(d *TexDoc) *TexDoc {
	d.sync()
	cp := *d
	cp.frames = make([]TexFrame, len(d.frames))
	for i, f := range d.frames {
		cp.frames[i] = cloneFrame(f)
	}
	cp.checkout(cp.activeFrame, cp.channel)
	return &cp
}

// MarkDirty flags the document as having unsaved changes.
func (d *TexDoc) MarkDirty()  { d.dirty = true }
func (d *TexDoc) Dirty() bool { return d.dirty }
func (d *TexDoc) ClearDirty() { d.dirty = false }

// AddLayer appends a new empty layer above the current top of the
// checked-out frame, up to TexMaxLayers.
func (d *TexDoc) AddLayer(name string) error {
	if len(d.Layers) >= TexMaxLayers {
		return fmt.Errorf("cannot add layer: already at the %d-layer cap", TexMaxLayers)
	}
	d.Layers = append(d.Layers, TexLayer{
		Name: name, Visible: true, Opacity: 1, Blend: BlendNormal,
		Idx: make([]uint8, d.W*d.H),
	})
	d.dirty = true
	return nil
}

// DuplicateLayer copies layer i (of the checked-out frame) and inserts the
// copy directly above it.
func (d *TexDoc) DuplicateLayer(i int) error {
	if i < 0 || i >= len(d.Layers) {
		return fmt.Errorf("layer index %d out of range", i)
	}
	if len(d.Layers) >= TexMaxLayers {
		return fmt.Errorf("cannot duplicate layer: already at the %d-layer cap", TexMaxLayers)
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

// DeleteLayer removes layer i from the checked-out frame. At least one layer
// must always remain.
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

// MoveLayer swaps layer i (of the checked-out frame) with the layer above
// (up=true) or below (up=false) it.
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

// flattenLayers composites layers bottom-to-top in RGBA (honoring opacity and
// blend mode), then quantises the result back to d.Pal so the returned image
// is provably on-palette — image.Paletted carries a real PLTE chunk when
// PNG-encoded. Shared by Flatten (the checked-out frame) and FlattenFrame
// (any frame, by index).
func (d *TexDoc) flattenLayers(layers []TexLayer) *image.Paletted {
	rgba := make([]color.RGBA, d.W*d.H) // fully transparent zero value

	for li := range layers {
		layer := &layers[li]
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

	pcm := make(color.Palette, TexPaletteSz)
	for i, c := range d.Pal {
		pcm[i] = c
	}
	out := image.NewPaletted(image.Rect(0, 0, d.W, d.H), pcm)
	q := NewQuantCache(&d.Pal)
	for i, c := range rgba {
		out.Pix[i] = q.Quantize(c)
	}
	return out
}

// Flatten composites the checked-out frame's COLOUR channel — see
// FlattenFrame for any other frame in the stack. It goes through
// FlattenFrame rather than reading d.Layers directly because d.Layers is the
// checked-out *channel*: while the bump channel is being painted it holds
// heights, and flattening those as palette indices would upload the height
// field to the world as if it were the albedo.
func (d *TexDoc) Flatten() *image.Paletted {
	return d.FlattenFrame(d.activeFrame)
}

// FlattenFrame composites frame i of the animation stack, whether or not it
// is the one currently checked out for editing.
func (d *TexDoc) FlattenFrame(i int) *image.Paletted {
	return d.flattenLayers(d.Frames()[i].Layers)
}
