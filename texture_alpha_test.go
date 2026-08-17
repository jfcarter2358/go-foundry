package main

import (
	"image"
	"image/color"
	"testing"

	"foundry/internal/texdoc"
)

func alphaTestPalette() texdoc.TexPalette {
	var pal texdoc.TexPalette
	pal[0] = color.RGBA{0, 0, 0, 0}     // transparent
	pal[1] = color.RGBA{255, 0, 0, 255} // opaque
	pal[2] = color.RGBA{0, 255, 0, 128} // semi-transparent
	pal[3] = color.RGBA{0, 0, 255, 5}   // near-cutout but not zero
	pal[4] = color.RGBA{9, 9, 9, 255}   // opaque, unused
	return pal
}

func paletted(w, h int, pal texdoc.TexPalette, pix []uint8) *image.Paletted {
	pcm := make(color.Palette, texdoc.TexPaletteSz)
	for i, c := range pal {
		pcm[i] = c
	}
	img := image.NewPaletted(image.Rect(0, 0, w, h), pcm)
	copy(img.Pix, pix)
	return img
}

func TestComputeHasAlphaOnlyOpaqueAndTransparentUsed(t *testing.T) {
	pal := alphaTestPalette()
	img := paletted(2, 1, pal, []uint8{0, 1}) // transparent + opaque only
	if ComputeHasAlpha(&pal, img) {
		t.Error("expected no semi-transparent texel")
	}
}

func TestComputeHasAlphaSemiTransparentUsed(t *testing.T) {
	pal := alphaTestPalette()
	img := paletted(2, 1, pal, []uint8{1, 2}) // opaque + semi-transparent
	if !ComputeHasAlpha(&pal, img) {
		t.Error("expected semi-transparent texel to be detected")
	}
}

func TestComputeHasAlphaUnusedSlotIgnored(t *testing.T) {
	pal := alphaTestPalette()
	// Palette has a semi-transparent slot (2) but no texel uses it.
	img := paletted(2, 1, pal, []uint8{0, 1})
	if ComputeHasAlpha(&pal, img) {
		t.Error("an unused semi-transparent palette slot must not count")
	}
}

func TestComputeHasAlphaLowAlphaTreatedAsCutout(t *testing.T) {
	pal := alphaTestPalette()
	// Slot 3 has alpha 5, inside (alphaCutoffLow, alphaCutoffHigh) since
	// alphaCutoffLow is 0 — 5 > 0, so it IS semi-transparent by this band.
	img := paletted(1, 1, pal, []uint8{3})
	if !ComputeHasAlpha(&pal, img) {
		t.Error("alpha=5 is inside the semi-transparent band (0, 250) and should be detected")
	}
}

func TestComputeHasAlphaFullyTransparentIsCutout(t *testing.T) {
	pal := alphaTestPalette()
	img := paletted(1, 1, pal, []uint8{0}) // alpha 0
	if ComputeHasAlpha(&pal, img) {
		t.Error("alpha=0 is exact cutout, not the semi-transparent band")
	}
}

func TestUpdateTextureHasAlphaRecordsFlag(t *testing.T) {
	defer delete(textureHasAlpha, "test-alpha-tex")
	pal := alphaTestPalette()
	img := paletted(1, 1, pal, []uint8{2})
	UpdateTextureHasAlpha("test-alpha-tex", &pal, img)
	if !textureHasAlpha["test-alpha-tex"] {
		t.Error("UpdateTextureHasAlpha did not record true for a semi-transparent texture")
	}
}

func TestGeometryIsTransparentRoutesOnTextureAlpha(t *testing.T) {
	defer delete(textureHasAlpha, "glass")
	g := &Geometry{Texture: "glass", Alpha: 1.0}
	if g.IsTransparent() {
		t.Fatal("opaque Alpha with no registered texture alpha should not be transparent")
	}
	textureHasAlpha["glass"] = true
	if !g.IsTransparent() {
		t.Error("IsTransparent should route on textureHasAlpha even with Alpha == 1.0")
	}
}

func TestGeometryIsTransparentStillRoutesOnAlphaScalar(t *testing.T) {
	g := &Geometry{Texture: "opaque-tex", Alpha: 0.5}
	if !g.IsTransparent() {
		t.Error("existing per-geometry Alpha routing regressed")
	}
}
