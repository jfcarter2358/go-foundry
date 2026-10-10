package texdoc

// bump_from_normal.go — the way back: a height field from a normal map.
//
// A document stores height, not normals (bump.go says why), so a normal map
// painted or generated outside the editor can only come in as the height
// field whose derived map looks like it. Each texel of a normal map is a
// slope — n = (-s·∂h/∂u, -s·∂h/∂v, 1), normalised, in NormalMapFromHeight's
// convention — so the height is the surface whose gradient best matches
// those slopes: the least-squares solution of ∇²H = ∇·g, a Poisson equation.
//
// It is solved on a wrapped grid, as the map is derived on one: a world
// texture tiles. The solve runs coarse to fine — each level starts from the
// level below, upsampled, and relaxes only the detail that level adds — so a
// 2048² map needs a few dozen sweeps, not the thousands plain relaxation
// takes to carry a slope across the whole image.
//
// A map drawn by hand is rarely the gradient of any surface at all (nothing
// forces its slopes to add up around a loop); the solve finds the nearest one
// that is, which is what a height field can hold.

import (
	"image"
	"math"
)

// HeightFromNormalMap reconstructs the height field whose derived normal map
// best matches img, in NormalMapFromHeight's encoding and green convention.
// It returns the heights as bump-layer bytes (1..255 — 0 is "nothing here" to
// a bump layer) and the strength that turns them back into the same slopes.
// ok is false when the map is flat, so there is no relief to keep.
//
// The strength is capped at MaxBumpStrength; relief steeper than that comes
// back flatter rather than clipped, since clipping would flatten the tops of
// the tallest features to nothing.
func HeightFromNormalMap(img image.Image) (heights []uint8, strength float32, ok bool) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, 0, false
	}
	gx := make([]float64, w*h)
	gy := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			nx := float64(r)/65535*2 - 1
			ny := float64(g)/65535*2 - 1
			nz := float64(bl)/65535*2 - 1
			// A normal at or past the horizon has no finite slope; this caps
			// it at about 84 degrees.
			if nz < 0.1 {
				nz = 0.1
			}
			gx[y*w+x] = -nx / nz
			gy[y*w+x] = -ny / nz
		}
	}

	H := solvePoisson(w, h, gx, gy)

	lo, hi := H[0], H[0]
	for _, v := range H {
		lo = math.Min(lo, v)
		hi = math.Max(hi, v)
	}
	span := hi - lo
	if span < 1e-3 {
		return nil, 0, false
	}
	// Stored heights run 1/255..1, a span of 254/255, so the strength that
	// gives back H's slopes is span/(254/255).
	s := span * 255 / 254
	if s > MaxBumpStrength {
		s = MaxBumpStrength
	}
	heights = make([]uint8, w*h)
	for i, v := range H {
		heights[i] = uint8(1 + math.Round((v-lo)/span*254))
	}
	return heights, float32(s), true
}

// solvePoisson returns the H (up to a constant) whose central differences
// best match the slopes gx, gy on a w×h grid that wraps at every edge.
func solvePoisson(w, h int, gx, gy []float64) []float64 {
	div := make([]float64, w*h)
	for y := 0; y < h; y++ {
		yu, yd := wrap(y-1, h), wrap(y+1, h)
		for x := 0; x < w; x++ {
			xl, xr := wrap(x-1, w), wrap(x+1, w)
			div[y*w+x] = (gx[y*w+xr]-gx[y*w+xl])/2 + (gy[yd*w+x]-gy[yu*w+x])/2
		}
	}

	var H []float64
	sweeps := 40
	if w >= 16 && h >= 16 {
		// A slope per coarse texel is two fine texels' worth.
		cw, ch := (w+1)/2, (h+1)/2
		cgx, cgy := make([]float64, cw*ch), make([]float64, cw*ch)
		for y := 0; y < ch; y++ {
			for x := 0; x < cw; x++ {
				var sx, sy float64
				for _, o := range [4][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
					i := wrap(2*y+o[1], h)*w + wrap(2*x+o[0], w)
					sx += gx[i]
					sy += gy[i]
				}
				cgx[y*cw+x] = sx / 2
				cgy[y*cw+x] = sy / 2
			}
		}
		coarse := solvePoisson(cw, ch, cgx, cgy)
		H = make([]float64, w*h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				H[y*w+x] = sampleWrapped(coarse, cw, ch, (float64(x)-0.5)/2, (float64(y)-0.5)/2)
			}
		}
	} else {
		H = make([]float64, w*h)
		sweeps = 2000
	}

	// Red-black Gauss-Seidel with over-relaxation on the 5-point Laplacian.
	const omega = 1.7
	for it := 0; it < sweeps; it++ {
		for parity := 0; parity < 2; parity++ {
			for y := 0; y < h; y++ {
				yu, yd := wrap(y-1, h), wrap(y+1, h)
				for x := (y + parity) % 2; x < w; x += 2 {
					xl, xr := wrap(x-1, w), wrap(x+1, w)
					i := y*w + x
					nb := H[y*w+xl] + H[y*w+xr] + H[yu*w+x] + H[yd*w+x]
					H[i] += omega * ((nb-div[i])/4 - H[i])
				}
			}
		}
	}
	return H
}

// sampleWrapped bilinearly samples a wrapped w×h grid at (fx, fy).
func sampleWrapped(g []float64, w, h int, fx, fy float64) float64 {
	x0, y0 := int(math.Floor(fx)), int(math.Floor(fy))
	tx, ty := fx-float64(x0), fy-float64(y0)
	at := func(x, y int) float64 { return g[wrap(y, h)*w+wrap(x, w)] }
	top := at(x0, y0)*(1-tx) + at(x0+1, y0)*tx
	bot := at(x0, y0+1)*(1-tx) + at(x0+1, y0+1)*tx
	return top*(1-ty) + bot*ty
}

func wrap(v, n int) int { return ((v % n) + n) % n }
