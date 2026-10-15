package texcompress

// mip_coverage.go — a mip chain that keeps an alpha cutout's COVERAGE.
//
// A box-filtered mip level averages alpha, so a texture of thin cutout
// shapes — a fir's needle twigs (24% of the texels opaque), grass blades —
// fades toward its mean alpha as it shrinks, and once that mean is below
// the shader's cutoff (0.5, fragment.glsl alphaCutoffLevel) the shapes
// vanish: a scanned fir seen from 20 m is a bare skeleton of branches. The
// fix (Castaño, "Computing alpha mipmaps", 2010) scales each level's alpha
// so the share of its texels at or above the cutoff reaches level 0's — the
// silhouette keeps its density at every distance.
//
// The colour channels are averaged WEIGHTED BY ALPHA: a plain box filter
// mixes in the colour of the transparent texels round a cutout (often white
// or grey in a scan's atlas), and once the alpha is scaled up those mixed
// texels are drawn — distant foliage came out pale grey before this.
//
// Used only for textures that opt in by name (CoverageSuffix): the Poly
// Haven importer gives its cutout albedos that suffix when an import asks
// for thinned foliage (phimport/decimate_cards.go). Every other texture's
// chain is the driver's glGenerateMipmap, as always.

import (
	"image"
	"strings"
)

// CoverageSuffix ends the name of a texture whose mip chain keeps its
// alpha coverage (CoverageMipChain).
const CoverageSuffix = "_cov"

// CoverageTexture reports whether a texture of this name (or file path,
// with or without its extension) asks for a coverage-preserving chain.
func CoverageTexture(name string) bool {
	if i := strings.LastIndexByte(name, '.'); i > strings.LastIndexAny(name, `/\`) {
		name = name[:i]
	}
	return strings.HasSuffix(name, CoverageSuffix)
}

// CoverageCutoff is the alpha the coverage is measured at: the shader's
// default cutout level.
const CoverageCutoff = 128

// CoverageMipChain is img's mip chain with each level's alpha scaled so its
// coverage at CoverageCutoff matches level 0's. Level 0 is img itself. Each
// level is box-filtered from the previous UNSCALED level, so the scaling
// does not compound.
func CoverageMipChain(img *image.RGBA) []*image.RGBA {
	target := coverage(img, 1)
	chain := []*image.RGBA{img}
	plain := img
	for plain.Rect.Dx() > 1 || plain.Rect.Dy() > 1 {
		plain = downsampleAlphaWeighted(plain)
		lvl := image.NewRGBA(plain.Rect)
		copy(lvl.Pix, plain.Pix)
		if target > 0 {
			// The coverage rises with the scale: bisect for the one that
			// matches.
			lo, hi := 0.0, 16.0
			for it := 0; it < 20; it++ {
				mid := (lo + hi) / 2
				if coverage(plain, mid) < target {
					lo = mid
				} else {
					hi = mid
				}
			}
			s := hi
			for k := 3; k < len(lvl.Pix); k += 4 {
				lvl.Pix[k] = uint8(min(255, float64(plain.Pix[k])*s+0.5))
			}
		}
		chain = append(chain, lvl)
	}
	return chain
}

// coverage is the share of img's texels whose alpha times scale reaches the
// cutoff.
func coverage(img *image.RGBA, scale float64) float64 {
	n, in := 0, 0
	for k := 3; k < len(img.Pix); k += 4 {
		n++
		if float64(img.Pix[k])*scale+0.5 >= CoverageCutoff {
			in++
		}
	}
	if n == 0 {
		return 0
	}
	return float64(in) / float64(n)
}

// downsampleAlphaWeighted is Downsample with each colour channel the
// alpha-weighted mean of its 2x2 box (the plain mean where all four are
// transparent); alpha is the plain mean.
func downsampleAlphaWeighted(img *image.RGBA) *image.RGBA {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	nw, nh := max(1, w/2), max(1, h/2)
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	src, stride := img.Pix, img.Stride
	for y := 0; y < nh; y++ {
		rows := [2]int{min(2*y, h-1) * stride, min(2*y+1, h-1) * stride}
		o := y * out.Stride
		for x := 0; x < nw; x++ {
			cols := [2]int{min(2*x, w-1) * 4, min(2*x+1, w-1) * 4}
			var aw, as uint32
			var cw, cs [3]uint32
			for _, r := range rows {
				for _, c := range cols {
					a := uint32(src[r+c+3])
					as += a
					for k := 0; k < 3; k++ {
						cw[k] += uint32(src[r+c+k]) * a
						cs[k] += uint32(src[r+c+k])
					}
					aw += a
				}
			}
			for k := 0; k < 3; k++ {
				if aw > 0 {
					out.Pix[o+x*4+k] = uint8((cw[k] + aw/2) / aw)
				} else {
					out.Pix[o+x*4+k] = uint8((cs[k] + 2) >> 2)
				}
			}
			out.Pix[o+x*4+3] = uint8((as + 2) >> 2)
		}
	}
	return out
}
