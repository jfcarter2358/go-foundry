package texdoc

import (
	"image"
	"image/color"
	"testing"
)

func solidImage(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = c.R, c.G, c.B, c.A
	}
	return img
}

func TestImportImageLayerNewLayerAboveActive(t *testing.T) {
	d, _ := NewTexDoc("t", 4, 4, DefaultPalette())
	_ = d.AddLayer("top")
	d.Active = 0
	want := DefaultPalette()[7]
	img := solidImage(2, 2, color.NRGBA{want.R, want.G, want.B, want.A})
	if err := d.ImportImageLayer(img, "imp", FitStretch, false); err != nil {
		t.Fatal(err)
	}
	if len(d.Layers) != 3 || d.Active != 1 || d.Layers[1].Name != "imp" || d.Layers[2].Name != "top" {
		t.Fatalf("layer stack wrong: active=%d %v", d.Active, []string{d.Layers[0].Name, d.Layers[1].Name, d.Layers[2].Name})
	}
	for i, v := range d.Layers[1].Idx {
		if v != 7 {
			t.Fatalf("texel %d = %d, want 7 (stretch should cover the canvas)", i, v)
		}
	}
	if len(d.Frames()[0].Layers) != 3 {
		t.Fatal("imported layer did not reach the frame")
	}
}

func TestImportImageLayerActualSizeCentresAndLeavesBorderEmpty(t *testing.T) {
	d, _ := NewTexDoc("t", 4, 4, DefaultPalette())
	p := DefaultPalette()[5]
	img := solidImage(2, 2, color.NRGBA{p.R, p.G, p.B, 255})
	if err := d.ImportImageLayer(img, "imp", FitActualSize, true); err != nil {
		t.Fatal(err)
	}
	if len(d.Layers) != 1 {
		t.Fatal("replace added a layer")
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			v := d.Layers[0].Idx[y*4+x]
			inside := x >= 1 && x <= 2 && y >= 1 && y <= 2
			if inside && v != 5 || !inside && v != 0 {
				t.Fatalf("texel (%d,%d) = %d", x, y, v)
			}
		}
	}
}

func TestImportImageLayerBumpUsesBrightness(t *testing.T) {
	d, _ := NewTexDoc("t", 2, 1, DefaultPalette())
	d.SetChannel(ChanBump)
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	copy(img.Pix, []uint8{255, 255, 255, 255, 0, 0, 0, 255})
	if err := d.ImportImageLayer(img, "h", FitStretch, true); err != nil {
		t.Fatal(err)
	}
	if got := d.Layers[0].Idx; got[0] != 255 || got[1] != 1 {
		t.Fatalf("heights = %v, want [255 1] (opaque black must not read as empty)", got)
	}
}

func TestImportImageLayerRespectsCap(t *testing.T) {
	d, _ := NewTexDoc("t", 2, 2, DefaultPalette())
	for len(d.Layers) < TexMaxLayers {
		_ = d.AddLayer("x")
	}
	if err := d.ImportImageLayer(solidImage(2, 2, color.NRGBA{A: 255}), "imp", FitStretch, false); err == nil {
		t.Fatal("import past the layer cap succeeded")
	}
}
