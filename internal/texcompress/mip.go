package texcompress

// mip.go — the CPU mip chain.
//
// An uncompressed texture gets its mip chain from glGenerateMipmap, but a
// compressed one cannot: the driver will not build levels of a format it would
// have to encode. So the chain is built here, before encoding, with the same
// filter the driver uses in practice — a 2x2 box — so a compressed texture's
// distant look matches the uncompressed one's to within the encoding error.
//
// Sizes halve with the floor, never below 1, as GL's do (level n is
// max(1, w>>n) wide). An odd dimension drops its last column or row into the
// box beside it rather than being averaged over three: the same compromise
// most drivers make, and invisible at the distances a mip level is sampled at.
//
// The same Downsample is what the uncompressed path's "skip top mips" setting
// uses to shrink a texture before it is uploaded (gfx/texture_compress.go), so
// both paths drop the same detail.

import "image"

// Downsample is img halved in each dimension (floor, at least 1) by a 2x2 box
// filter, rounding to nearest. A 1x1 image comes back as a 1x1 copy.
func Downsample(img *image.RGBA) *image.RGBA {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	nw, nh := max(1, w/2), max(1, h/2)
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	src, stride := img.Pix, img.Stride
	for y := 0; y < nh; y++ {
		y0 := min(2*y, h-1)
		y1 := min(2*y+1, h-1)
		r0, r1 := y0*stride, y1*stride
		o := y * out.Stride
		for x := 0; x < nw; x++ {
			x0 := min(2*x, w-1) * 4
			x1 := min(2*x+1, w-1) * 4
			for c := 0; c < 4; c++ {
				s := uint32(src[r0+x0+c]) + uint32(src[r0+x1+c]) +
					uint32(src[r1+x0+c]) + uint32(src[r1+x1+c])
				out.Pix[o+x*4+c] = uint8((s + 2) >> 2)
			}
		}
	}
	return out
}

// MipChain is img followed by every smaller level down to 1x1. Level 0 is img
// itself, not a copy.
func MipChain(img *image.RGBA) []*image.RGBA {
	chain := []*image.RGBA{img}
	for cur := img; cur.Rect.Dx() > 1 || cur.Rect.Dy() > 1; {
		cur = Downsample(cur)
		chain = append(chain, cur)
	}
	return chain
}

// MipCount is how many levels a w x h texture's full chain has.
func MipCount(w, h int) int {
	n := 1
	for w > 1 || h > 1 {
		w, h = max(1, w/2), max(1, h/2)
		n++
	}
	return n
}
