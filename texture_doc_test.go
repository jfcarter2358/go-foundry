package main

import (
	"image/color"
	"testing"
)

// flattenPalette gives distinct, easily-reasoned-about colors: 0 transparent,
// 1 red, 2 green, 3 blue, 4 a 50% grey used to probe blend-quantisation.
func flattenPalette() TexPalette {
	var pal TexPalette
	pal[0] = color.RGBA{0, 0, 0, 0}
	pal[1] = color.RGBA{255, 0, 0, 255}
	pal[2] = color.RGBA{0, 255, 0, 255}
	pal[3] = color.RGBA{0, 0, 255, 255}
	pal[4] = color.RGBA{128, 128, 128, 255}
	return pal
}

func onePixelDoc(pal TexPalette) *TexDoc {
	d, err := NewTexDoc("t", 1, 1, pal)
	if err != nil {
		panic(err)
	}
	return d
}

// everyTexelOnPalette is the invariant that matters most: Flatten must never
// produce a color outside d.Pal.
func everyTexelOnPalette(t *testing.T, d *TexDoc) {
	t.Helper()
	out := d.Flatten()
	for i, px := range out.Pix {
		found := false
		for _, c := range d.Pal {
			if out.Palette[px] == c {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("texel %d (index %d) is not a palette entry", i, px)
		}
	}
}

func TestFlattenOpaqueOverOpaque(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	d.Layers[0].Idx[0] = 1 // red
	if err := d.AddLayer("top"); err != nil {
		t.Fatal(err)
	}
	d.Layers[1].Idx[0] = 3 // blue, fully opaque, fully covers red

	out := d.Flatten()
	if got := out.Pix[0]; got != 3 {
		t.Errorf("top opaque layer index = %d, want 3 (blue)", got)
	}
	everyTexelOnPalette(t, d)
}

func TestFlattenFiftyPercentOver(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	d.Layers[0].Idx[0] = 1 // red
	if err := d.AddLayer("top"); err != nil {
		t.Fatal(err)
	}
	d.Layers[1].Idx[0] = 3 // blue
	d.Layers[1].Opacity = 0.5

	out := d.Flatten()
	// 50% blue over red composites to roughly (128,0,128); with this palette's
	// only candidates being pure red/green/blue/grey, grey (slot 4) is the
	// nearest entry — the key invariant is that it lands on-palette at all.
	everyTexelOnPalette(t, d)
	if got := out.Pix[0]; got != 4 {
		t.Errorf("50%% blend index = %d, want 4 (grey, nearest to the blended color)", got)
	}
}

func TestFlattenHiddenLayerSkipped(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	d.Layers[0].Idx[0] = 1 // red
	if err := d.AddLayer("top"); err != nil {
		t.Fatal(err)
	}
	d.Layers[1].Idx[0] = 3 // blue
	d.Layers[1].Visible = false

	out := d.Flatten()
	if got := out.Pix[0]; got != 1 {
		t.Errorf("hidden top layer still visible: index = %d, want 1 (red)", got)
	}
}

func TestFlattenLayerOpacityZeroSkipped(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	d.Layers[0].Idx[0] = 1
	if err := d.AddLayer("top"); err != nil {
		t.Fatal(err)
	}
	d.Layers[1].Idx[0] = 3
	d.Layers[1].Opacity = 0

	out := d.Flatten()
	if got := out.Pix[0]; got != 1 {
		t.Errorf("zero-opacity layer contributed: index = %d, want 1 (red)", got)
	}
}

func TestFlattenBlendModes(t *testing.T) {
	pal := flattenPalette()
	pal[5] = color.RGBA{64, 0, 0, 255}    // dark red, multiply(red,red)/255 target-ish
	pal[6] = color.RGBA{255, 255, 0, 255} // yellow, screen(red,green) target-ish

	cases := []struct {
		name  string
		blend TexBlend
	}{
		{"normal", BlendNormal},
		{"multiply", BlendMultiply},
		{"add", BlendAdd},
		{"screen", BlendScreen},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := onePixelDoc(pal)
			d.Layers[0].Idx[0] = 1 // red
			if err := d.AddLayer("top"); err != nil {
				t.Fatal(err)
			}
			d.Layers[1].Idx[0] = 2 // green
			d.Layers[1].Blend = c.blend
			everyTexelOnPalette(t, d)
		})
	}
}

func TestFlattenSingleLayerPassthrough(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	d.Layers[0].Idx[0] = 2
	out := d.Flatten()
	if got := out.Pix[0]; got != 2 {
		t.Errorf("single layer passthrough index = %d, want 2", got)
	}
}

func TestFlattenEmptyDoc(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal) // layer index defaults to 0 (transparent)
	out := d.Flatten()
	if got := out.Pix[0]; got != 0 {
		t.Errorf("empty doc index = %d, want 0 (transparent)", got)
	}
}

func TestFlattenEveryTexelOnPalette(t *testing.T) {
	pal := DefaultPalette()
	d, err := NewTexDoc("t", 8, 8, pal)
	if err != nil {
		t.Fatal(err)
	}
	for i := range d.Layers[0].Idx {
		d.Layers[0].Idx[i] = uint8(i % texPaletteSz)
	}
	if err := d.AddLayer("top"); err != nil {
		t.Fatal(err)
	}
	for i := range d.Layers[1].Idx {
		d.Layers[1].Idx[i] = uint8((i * 7) % texPaletteSz)
	}
	d.Layers[1].Opacity = 0.37
	d.Layers[1].Blend = BlendScreen
	everyTexelOnPalette(t, d)
}

func TestNewTexDocRejectsOversizedCanvas(t *testing.T) {
	pal := flattenPalette()
	if _, err := NewTexDoc("t", texMaxDim+1, 64, pal); err == nil {
		t.Error("NewTexDoc accepted width > texMaxDim")
	}
	if _, err := NewTexDoc("t", 64, texMaxDim+1, pal); err == nil {
		t.Error("NewTexDoc accepted height > texMaxDim")
	}
	if _, err := NewTexDoc("t", texMaxDim, texMaxDim, pal); err != nil {
		t.Errorf("NewTexDoc rejected the exact cap: %v", err)
	}
}

func TestLayerLimits(t *testing.T) {
	pal := flattenPalette()
	d := onePixelDoc(pal)
	for i := 1; i < texMaxLayers; i++ {
		if err := d.AddLayer("l"); err != nil {
			t.Fatalf("AddLayer %d: %v", i, err)
		}
	}
	if len(d.Layers) != texMaxLayers {
		t.Fatalf("layer count = %d, want %d", len(d.Layers), texMaxLayers)
	}
	if err := d.AddLayer("overflow"); err == nil {
		t.Error("AddLayer beyond the cap should fail")
	}
	if err := d.DuplicateLayer(0); err == nil {
		t.Error("DuplicateLayer beyond the cap should fail")
	}
}
