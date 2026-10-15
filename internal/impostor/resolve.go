package impostor

// resolve.go — a supersampled view down to the card's kept texels: colour
// and normal averaged over the covered samples, alpha the covered share, and
// the covered texels bled into the clear ones round them (as
// render/foliage_impostor.go does for the procedural trees), so a mip or a
// bilinear tap at the silhouette mixes in the crown's colour and normal,
// never black and never a flat card.

import (
	"image"

	"github.com/go-gl/mathgl/mgl32"
)

// bleedPasses is how far, in texels, colour spreads into the clear ones.
const bleedPasses = 4

// resolve is view v (cell*super on a side) as card cd's albedo (RGB, alpha
// the coverage) and its tangent-space normal map (see the package comment).
func resolve(v *view, cd card, cell, super int) (alb, nor *image.NRGBA) {
	tx, ty, tz := cd.right.Normalize(), cd.down.Normalize(), cd.n
	n := cell * cell
	col := make([]mgl32.Vec3, n)
	nrm := make([]mgl32.Vec3, n)
	cov := make([]float32, n)
	known := make([]bool, n)
	for y := 0; y < cell; y++ {
		for x := 0; x < cell; x++ {
			var c, nn mgl32.Vec3
			hits := 0
			for dy := 0; dy < super; dy++ {
				for dx := 0; dx < super; dx++ {
					i := (y*super+dy)*v.size + x*super + dx
					if !v.hit[i] {
						continue
					}
					hits++
					c = c.Add(v.col[i])
					nn = nn.Add(v.nor[i])
				}
			}
			o := y*cell + x
			if hits == 0 {
				continue
			}
			col[o] = c.Mul(1 / float32(hits))
			if l := nn.Len(); l > 1e-6 {
				nn = nn.Mul(1 / l)
			} else {
				nn = tz
			}
			// Into the card's tangent space: x along +u, y along +v, z out.
			ts := mgl32.Vec3{nn.Dot(tx), nn.Dot(ty), max(nn.Dot(tz), 0.05)}
			nrm[o] = ts.Normalize()
			cov[o] = float32(hits) / float32(super*super)
			known[o] = true
		}
	}
	// Bleed: each clear texel takes the mean of its known neighbours, a ring
	// at a time.
	for pass := 0; pass < bleedPasses; pass++ {
		next := append([]bool(nil), known...)
		for y := 0; y < cell; y++ {
			for x := 0; x < cell; x++ {
				o := y*cell + x
				if known[o] {
					continue
				}
				var c, nn mgl32.Vec3
				k := 0
				for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					nx, ny := x+d[0], y+d[1]
					if nx < 0 || ny < 0 || nx >= cell || ny >= cell || !known[ny*cell+nx] {
						continue
					}
					c = c.Add(col[ny*cell+nx])
					nn = nn.Add(nrm[ny*cell+nx])
					k++
				}
				if k > 0 {
					col[o] = c.Mul(1 / float32(k))
					if l := nn.Len(); l > 1e-6 {
						nrm[o] = nn.Mul(1 / l)
					}
					next[o] = true
				}
			}
		}
		known = next
	}
	matchCoverage(cov)
	alb = image.NewNRGBA(image.Rect(0, 0, cell, cell))
	nor = image.NewNRGBA(image.Rect(0, 0, cell, cell))
	for o := 0; o < n; o++ {
		ts := nrm[o]
		if !known[o] {
			ts = mgl32.Vec3{0, 0, 1}
		}
		alb.Pix[4*o] = unorm(col[o][0])
		alb.Pix[4*o+1] = unorm(col[o][1])
		alb.Pix[4*o+2] = unorm(col[o][2])
		alb.Pix[4*o+3] = unorm(cov[o])
		nor.Pix[4*o] = unorm(ts[0]*0.5 + 0.5)
		nor.Pix[4*o+1] = unorm(ts[1]*0.5 + 0.5)
		nor.Pix[4*o+2] = unorm(ts[2]*0.5 + 0.5)
		nor.Pix[4*o+3] = 255
	}
	return alb, nor
}

func unorm(f float32) uint8 { return uint8(min(max(f, 0), 1)*255 + 0.5) }

// matchCoverage rescales a card's alpha so that the share of its texels the
// cutout keeps (alpha >= 0.5) is the share of the card the mesh actually
// covers (the mean alpha). A scanned fir's twigs are thinner than a texel:
// their texels come out at a quarter or a third covered, which the cutout
// drops, while a texel half covered by a branch is kept whole — so left as
// baked, the card shows the crown as a different density than the mesh does
// at the distance it takes over, and the coverage-preserving mips
// (texcompress/mip_coverage.go) keep that density all the way out. Matched,
// the card covers what the mesh covers: dense crowns stay dense, wispy ones
// wispy, and the hand-over does not darken or lighten the stand.
func matchCoverage(cov []float32) {
	var mean float64
	for _, a := range cov {
		mean += float64(a)
	}
	if len(cov) == 0 || mean == 0 {
		return
	}
	target := mean / float64(len(cov))
	kept := func(sc float32) float64 {
		n := 0
		for _, a := range cov {
			if a*sc >= 0.5 {
				n++
			}
		}
		return float64(n) / float64(len(cov))
	}
	lo, hi := float32(0.25), float32(8)
	for it := 0; it < 24; it++ {
		mid := (lo + hi) / 2
		if kept(mid) < target {
			lo = mid
		} else {
			hi = mid
		}
	}
	sc := (lo + hi) / 2
	for i := range cov {
		cov[i] = min(cov[i]*sc, 1)
	}
}
