package texdoc

import (
	"image/color"
	"testing"
)

func bumpDoc(t *testing.T, w, h int) *TexDoc {
	t.Helper()
	d, err := NewTexDoc("b", w, h, DefaultPalette())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestBumpChannelIsSeparateStorage is the property everything else rests on:
// the two channels share every layer operation but not a single byte, so
// painting a height can never disturb the albedo.
func TestBumpChannelIsSeparateStorage(t *testing.T) {
	d := bumpDoc(t, 4, 4)
	d.Layers[0].Idx[0] = 7 // a colour texel

	d.SetChannel(ChanBump)
	if !d.HasBump() {
		t.Fatal("SetChannel(ChanBump) did not create the bump channel")
	}
	if got := d.Layers[0].Idx[0]; got != 0 {
		t.Errorf("bump channel starts at %d, want 0 — it is showing the colour layer", got)
	}
	d.Layers[0].Idx[0] = 200 // a height

	d.SetChannel(ChanColor)
	if got := d.Layers[0].Idx[0]; got != 7 {
		t.Errorf("colour texel = %d after painting the bump channel, want 7", got)
	}
	d.SetChannel(ChanBump)
	if got := d.Layers[0].Idx[0]; got != 200 {
		t.Errorf("height texel = %d after switching away and back, want 200", got)
	}
}

// TestFlattenIsAlwaysTheColourChannel pins the trap the checkout model
// creates: d.Layers is the checked-out *channel*, so a Flatten that read it
// directly would upload the height field to the world as the albedo.
func TestFlattenIsAlwaysTheColourChannel(t *testing.T) {
	d := bumpDoc(t, 2, 2)
	d.Pal[9] = color.RGBA{R: 10, G: 20, B: 30, A: 255}
	for i := range d.Layers[0].Idx {
		d.Layers[0].Idx[i] = 9
	}

	d.SetChannel(ChanBump)
	for i := range d.Layers[0].Idx {
		d.Layers[0].Idx[i] = 255
	}

	flat := d.Flatten()
	for i, px := range flat.Pix {
		if px != 9 {
			t.Fatalf("flattened texel %d = %d, want the colour index 9", i, px)
		}
	}
}

// TestBumpSurvivesFrameSwitch checks the per-frame bookkeeping: each frame
// carries its own height stack, and checking another frame out and back does
// not lose either channel.
func TestBumpSurvivesFrameSwitch(t *testing.T) {
	d := bumpDoc(t, 2, 2)
	d.SetChannel(ChanBump)
	d.Layers[0].Idx[0] = 111
	if err := d.AddFrame(); err != nil {
		t.Fatal(err)
	}
	if err := d.SetActiveFrame(1); err != nil {
		t.Fatal(err)
	}
	if got := d.Layers[0].Idx[0]; got != 0 {
		t.Errorf("frame 1's height = %d, want 0 — frames are sharing a stack", got)
	}
	d.Layers[0].Idx[0] = 222
	if err := d.SetActiveFrame(0); err != nil {
		t.Fatal(err)
	}
	if got := d.Layers[0].Idx[0]; got != 111 {
		t.Errorf("frame 0's height = %d after a round trip, want 111", got)
	}
}

// TestDisableBumpDropsEveryFrame — a deleted bump map has to be gone
// everywhere, or a save writes a normal map back out from a leftover frame.
func TestDisableBumpDropsEveryFrame(t *testing.T) {
	d := bumpDoc(t, 2, 2)
	d.SetChannel(ChanBump)
	d.Layers[0].Idx[0] = 90
	if err := d.AddFrame(); err != nil {
		t.Fatal(err)
	}
	d.DisableBump()
	if d.HasBump() {
		t.Fatal("HasBump still true after DisableBump")
	}
	if d.Channel() != ChanColor {
		t.Error("DisableBump left the bump channel checked out")
	}
	for i, f := range d.Frames() {
		if f.Bump != nil {
			t.Errorf("frame %d kept its height stack", i)
		}
	}
}

// TestHeightZeroIsTransparent: an upper height layer's zeroes must let the
// stack below show through, the same way palette slot 0 does on the colour
// channel — otherwise a stroke on a new layer flattens everything around it.
func TestHeightZeroIsTransparent(t *testing.T) {
	d := bumpDoc(t, 2, 1)
	d.SetChannel(ChanBump)
	d.Layers[0].Idx[0] = 255
	d.Layers[0].Idx[1] = 255
	if err := d.AddLayer("Height 2"); err != nil {
		t.Fatal(err)
	}
	d.Layers[1].Idx[1] = 128 // and index 0 left at zero

	got := d.Height()
	if got[0] != 1 {
		t.Errorf("height under an unpainted upper texel = %v, want 1", got[0])
	}
	if want := float32(128) / 255; got[1] != want {
		t.Errorf("height under a painted upper texel = %v, want %v", got[1], want)
	}
}

// TestNormalMapOfFlatHeightIsStraightUp — a document whose bump channel has
// never been painted must not tilt anything.
func TestNormalMapOfFlatHeightIsStraightUp(t *testing.T) {
	d := bumpDoc(t, 4, 4)
	d.SetChannel(ChanBump)
	img := d.NormalMap()
	for i := 0; i < 16; i++ {
		got := color.RGBA{img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3]}
		if got != FlatNormalTexel {
			t.Fatalf("texel %d = %v, want %v", i, got, FlatNormalTexel)
		}
	}
}

// TestNormalMapTiltsAgainstTheSlope: on a surface climbing along +U, the
// normal must lean back along -U (red below 128), which is the sign
// convention fragment.glsl's tangent frame expects.
func TestNormalMapTiltsAgainstTheSlope(t *testing.T) {
	const w, h = 8, 4
	height := make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			height[y*w+x] = float32(x) / float32(w-1)
		}
	}
	img := NormalMapFromHeight(w, h, height, 4)

	// Sample away from the wrap seam at x=0 / x=w-1, where the ramp falls off
	// a cliff back to zero and the gradient legitimately reverses.
	p := (1*w + 3) * 4
	if img.Pix[p] >= 128 {
		t.Errorf("red = %d on a surface rising along +U, want < 128", img.Pix[p])
	}
	if img.Pix[p+1] != 128 {
		t.Errorf("green = %d with no gradient along V, want 128", img.Pix[p+1])
	}
	if img.Pix[p+2] <= 128 {
		t.Errorf("blue = %d, want > 128 — the normal must still point outwards", img.Pix[p+2])
	}
}

// TestNormalMapStrengthDeepensTheTilt: strength is baked in at derivation
// time, so it has to actually change the bytes.
func TestNormalMapStrengthDeepensTheTilt(t *testing.T) {
	const w, h = 8, 2
	height := make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			height[y*w+x] = float32(x) / float32(w-1)
		}
	}
	p := (1*w + 3) * 4
	weak := NormalMapFromHeight(w, h, height, 1).Pix[p]
	strong := NormalMapFromHeight(w, h, height, 16).Pix[p]
	if strong >= weak {
		t.Errorf("red at strength 16 = %d, at strength 1 = %d — more strength must tilt further", strong, weak)
	}
}

// TestNormalMapWrapsAtTheEdges: world textures tile, so the gradient at the
// last column has to be taken against the first, not clamped — a clamp puts a
// ridge of false relief down every tile seam.
func TestNormalMapWrapsAtTheEdges(t *testing.T) {
	const w, h = 4, 4
	height := make([]float32, w*h)
	for y := 0; y < h; y++ {
		height[y*w+0] = 1 // a bar down the left edge only
	}
	img := NormalMapFromHeight(w, h, height, 4)

	// The last column sees the bar one texel to its "right" through the wrap,
	// so it must be tilted. A clamped implementation would leave it flat.
	p := (1*w + (w - 1)) * 4
	got := color.RGBA{img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3]}
	if got == FlatNormalTexel {
		t.Error("the column before the wrap is flat — the gradient is not wrapping")
	}
}

// TestPaletteRemapLeavesHeightsAlone: bump bytes are heights, not palette
// indices, so the one whole-document layer walk must not reach them.
func TestPaletteRemapLeavesHeightsAlone(t *testing.T) {
	d := bumpDoc(t, 2, 2)
	d.SetChannel(ChanBump)
	for i := range d.Layers[0].Idx {
		d.Layers[0].Idx[i] = 77
	}
	d.SetChannel(ChanColor)

	seen := 0
	d.EachLayer(func(l *TexLayer) {
		seen++
		for i := range l.Idx {
			l.Idx[i] = 1
		}
	})
	if seen != 1 {
		t.Errorf("EachLayer visited %d layers, want 1 (the single colour layer)", seen)
	}
	d.SetChannel(ChanBump)
	for i, v := range d.Layers[0].Idx {
		if v != 77 {
			t.Fatalf("height texel %d = %d after a walk over the colour layers, want 77", i, v)
		}
	}
}

// TestCloneCopiesBothChannels — undo snapshots go through CloneTexDoc, so a
// clone that aliased (or dropped) the height stack would corrupt it.
func TestCloneCopiesBothChannels(t *testing.T) {
	d := bumpDoc(t, 2, 2)
	d.SetChannel(ChanBump)
	d.Layers[0].Idx[0] = 50

	cp := CloneTexDoc(d)
	d.Layers[0].Idx[0] = 60

	if cp.Channel() != ChanBump {
		t.Error("clone lost the checked-out channel")
	}
	if got := cp.Layers[0].Idx[0]; got != 50 {
		t.Errorf("clone's height texel = %d after mutating the original, want 50", got)
	}
}
