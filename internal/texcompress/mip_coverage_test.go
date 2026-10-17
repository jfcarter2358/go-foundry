package texcompress

// mip_coverage_test.go — a thin cutout's coverage survives the chain that
// keeps it (never below level 0's), and fades in a plain box-filtered one.

import (
	"image"
	"testing"
)

func TestCoverageMipChainKeepsCoverage(t *testing.T) {
	// Vertical one-texel lines every fourth column: 25% opaque.
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			o := y*img.Stride + x*4
			// The transparent texels are white, as a scan's atlas often is.
			img.Pix[o], img.Pix[o+1], img.Pix[o+2] = 255, 255, 255
			if x%4 == 0 {
				img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = 40, 90, 30, 255
			}
		}
	}
	plain := MipChain(img)
	kept := CoverageMipChain(img)
	if len(plain) != len(kept) {
		t.Fatalf("%d levels, want %d", len(kept), len(plain))
	}
	c0 := coverage(img, 1)
	if c := coverage(plain[2], 1); c > 0.01 {
		t.Errorf("the plain chain's level 2 still covers %.2f (expected to fade)", c)
	}
	// Never less than level 0's: where a level's alpha is uniform (one-texel
	// lines averaged away) the smallest scale reaching the coverage makes it
	// all opaque — foliage goes solid at distance rather than vanishing.
	for i := 1; i < len(kept); i++ {
		if c := coverage(kept[i], 1); c < c0 {
			t.Errorf("level %d covers %.2f, level 0 %.2f", i, c, c0)
		}
	}
	// Colour is the opaque texels' (alpha-weighted): the transparent ones'
	// white never bleeds in.
	if kept[2].Pix[0] != 40 || kept[2].Pix[1] != 90 {
		t.Errorf("level 2 colour %v, want the lines' (40, 90, 30)", kept[2].Pix[:3])
	}
	if !CoverageTexture("ph_fir_tree_01_twig_cov") || !CoverageTexture("/a/b/ph_x_cov.png") || CoverageTexture("ph_x") {
		t.Error("CoverageTexture names")
	}
}
