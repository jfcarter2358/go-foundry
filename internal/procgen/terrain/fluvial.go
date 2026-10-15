package terrain

// fluvial.go — the drainage phase: stream-power erosion on a coarse grid.
//
// Droplets (droplet.go) cut gullies a few metres long; they cannot organise a
// kilometre of ground into a drainage network, because no droplet lives long
// enough to know where the big channel is. Real washes are dendritic for a
// reason a droplet never sees: erosion goes as the WATER a channel carries,
// and that is the area draining into it. So this phase models exactly that,
// on a grid coarse enough (Cell, 4 m by default) that the whole map's
// routing is cheap:
//
//	every iteration:
//	  route   priority flood to the map edge (flood.go): receivers and order
//	  area    accumulate drainage area A, each sample sharing its area
//	          among every lower neighbour by slope (multiple flow
//	          directions: single-receiver area draws every channel as a
//	          straight 0°/45° line and cuts V notches along them)
//	  incise  from base level up, the implicit stream-power step
//	            h ← (h + F·h_r) / (1 + F),   F = K·erodibility·A^m / distance
//	          (unconditionally stable, Braun & Willett 2013); a sample below
//	          its receiver is a pit and fills toward it instead (Fill)
//
// K is Strength·0.015 per iteration; m is Exponent (0.5 is the textbook
// value). K and the erodibility are the material model's (model.go): a
// region's own strength and strata, blended across a transition band.
// Erodibility is the strata's at the sample's height, so a channel
// meeting a hard ledge slows there and steepens: a knickpoint, the pour-off
// lip every desert wash has.
//
// The coarse change is then added back onto the full-resolution field (a
// cubic B-spline; in a union of several regions fitted against the height
// too, edgeAwareChange), so the fine detail the base shape had survives
// under the new channels, and the droplets refine them afterwards.

import (
	"math"
)

// drainage erodes the field by stream power. The incision constant and the
// erodibility are the material model's at each coarse sample (model.go);
// the iterations, the coarse cell, the fill and the creep are the knobs'.
func drainage(fd *field, pool Pool, progress func(float32)) error {
	g, deposit := fd.g, fd.deposit
	d := fd.k.drainage
	if !fd.drains() || d.Iterations <= 0 {
		return nil
	}
	f := max(int(math.Round(d.Cell/g.Cell)), 1)
	nc := (g.N + f - 1) / f
	sc := nc + 1
	cell := g.Cell * float64(f)
	fs := g.S()
	// Box-downsample the field (and the material model, with its strata
	// offsets) onto the coarse grid.
	hc := make([]float32, sc*sc)
	if err := rows(pool, sc, func(J int) {
		for I := 0; I < sc; I++ {
			sum, n := float32(0), float32(0)
			for dj := -f / 2; dj <= f/2; dj++ {
				for di := -f / 2; di <= f/2; di++ {
					i, j := min(max(I*f+di, 0), fs-1), min(max(J*f+dj, 0), fs-1)
					sum += g.H[j*fs+i]
					n++
				}
			}
			hc[J*sc+I] = sum / n
		}
	}); err != nil {
		return err
	}
	mc, err := fd.m.downsample(fs, f, sc, pool)
	if err != nil {
		return err
	}
	before := append([]float32(nil), hc...)
	fl := newFlood(sc * sc)
	area := make([]float32, sc*sc)
	m := d.Exponent
	if m <= 0 {
		m = 0.5
	}
	cellArea := float32(cell * cell)
	fill := float32(min(max(d.Fill, 0), 1))
	tmp := make([]float32, sc*sc)
	for it := 0; it < d.Iterations; it++ {
		if err := pool.Err(); err != nil {
			return err
		}
		fl.run(hc, sc, 1e-3*float32(cell))
		fl.accumulateMFD(area, sc)
		for _, ci := range fl.order {
			r := fl.receiver[ci]
			if r < 0 {
				continue
			}
			h, hr := hc[ci], hc[r]
			if h > hr {
				a := float64(area[ci] * cellArea)
				F := mc.drainK(int(ci), h) * float32(math.Pow(a, m)) / (fl.rdist[ci] * float32(cell))
				hc[ci] = (h + F*hr) / (1 + F)
			} else if fill > 0 {
				hc[ci] = h + (hr-h)*fill
			}
		}
		if d.Diffusion > 0 {
			diffuse(hc, tmp, sc, float32(min(d.Diffusion, 0.24)))
		}
		if progress != nil {
			progress(float32(it+1) / float32(d.Iterations))
		}
	}
	// The change, back onto the full grid.
	for i := range hc {
		hc[i] -= before[i]
	}
	// The guided upsampling's regulariser (m²): coarse heights spread less
	// than about two metres round a sample leave it bspline's.
	const guideEps = 4
	return rows(pool, fs, func(j int) {
		for i := 0; i < fs; i++ {
			var dh float32
			if fd.k.edgeAware {
				dh = edgeAwareChange(hc, before, sc, float64(i)/float64(f), float64(j)/float64(f), g.H[j*fs+i], guideEps)
			} else {
				dh = bspline(hc, sc, float64(i)/float64(f), float64(j)/float64(f))
			}
			k := j*fs + i
			g.H[k] += dh
			if dh > 0 {
				deposit[k] += dh
			}
		}
	})
}

// diffuse is one explicit step of hillslope diffusion on an s x s grid:
// h += k·Laplacian(h), edges held. It rounds what the channels leave
// between them and smooths the grid-aligned streaks D8 routing draws across
// flats.
func diffuse(h, tmp []float32, s int, k float32) {
	copy(tmp, h)
	for j := 1; j < s-1; j++ {
		for i := 1; i < s-1; i++ {
			c := j*s + i
			h[c] = tmp[c] + k*(tmp[c-1]+tmp[c+1]+tmp[c-s]+tmp[c+s]-4*tmp[c])
		}
	}
}

// bspline samples an s x s array at fractional sample coordinates with the
// cubic B-spline: smooth to the second derivative, so a coarse change
// spread onto a fine grid leaves no creases along the coarse grid's lines
// (bilinear does, and a shaded relief shows every one of them).
func bspline(h []float32, s int, fx, fz float64) float32 {
	i, j := int(math.Floor(fx)), int(math.Floor(fz))
	u, v := fx-float64(i), fz-float64(j)
	var wu, wv [4]float64
	bsplineWeights(u, &wu)
	bsplineWeights(v, &wv)
	sum := 0.0
	for b := 0; b < 4; b++ {
		jj := min(max(j-1+b, 0), s-1)
		row := 0.0
		for a := 0; a < 4; a++ {
			ii := min(max(i-1+a, 0), s-1)
			row += wu[a] * float64(h[jj*s+ii])
		}
		sum += wv[b] * row
	}
	return float32(sum)
}

// edgeAwareChange is the coarse change at a fine sample whose height is h,
// spread by a local linear model of the change against the height — a
// guided upsampling (He, Sun and Tang's guided filter, with the cubic
// B-spline's weights as its window): over the 4×4 coarse samples bspline
// would read, the weighted least-squares line change ≈ a·height + b is
// fitted (heights before the drainage), regularised by eps, and read at h;
// the result is kept within the coarse samples' own changes.
//
// Why: the coarse grid (Cell, 4 m) cannot see a landform sharper than
// itself. Where a canyon wall cut into high ground meets a band whose soft
// rock the drainage lowers by twenty metres (docs/regions/PHASE_4.md), the
// coarse samples on the canyon's floor are barely lowered and those on the
// plateau a lot; spread by distance alone (bspline), the plateau's lowering
// fades out over the last coarse cell before the rim, and the rim — a fine
// sample at the plateau's height — keeps half its height: a knife-edged
// levee one sample wide along every such rim, which a regular grid draws as
// a sawtooth along its crest. Fitted against height, the rim takes the
// plateau's change and the floor the floor's, and the wall between them a
// share by its height.
//
// Where the coarse heights hardly vary (flats, gentle ground: their
// variance well under eps) a is near 0 and the change is bspline's: the
// weights sum to one, so b is its weighted mean. (A bilateral weight by
// height was tried first: it switched coarse samples on and off along the
// rim, and beaded it at the coarse grid's pitch.)
func edgeAwareChange(change, before []float32, s int, fx, fz float64, h, eps float32) float32 {
	i, j := int(math.Floor(fx)), int(math.Floor(fz))
	u, v := fx-float64(i), fz-float64(j)
	var wu, wv [4]float64
	bsplineWeights(u, &wu)
	bsplineWeights(v, &wv)
	var mh, md, mhh, mhd float64
	lo, hi := math.Inf(1), math.Inf(-1)
	for b := 0; b < 4; b++ {
		jj := min(max(j-1+b, 0), s-1)
		for a := 0; a < 4; a++ {
			ii := min(max(i-1+a, 0), s-1)
			k := jj*s + ii
			w := wu[a] * wv[b]
			x, y := float64(before[k]), float64(change[k])
			mh += w * x
			md += w * y
			mhh += w * x * x
			mhd += w * x * y
			if w > 0 {
				lo, hi = math.Min(lo, y), math.Max(hi, y)
			}
		}
	}
	vh := mhh - mh*mh
	cov := mhd - mh*md
	a := cov / (vh + float64(eps))
	d := md + a*(float64(h)-mh)
	return float32(math.Min(math.Max(d, lo), hi))
}

func bsplineWeights(t float64, w *[4]float64) {
	t2, t3 := t*t, t*t*t
	w[0] = (1 - 3*t + 3*t2 - t3) / 6
	w[1] = (4 - 6*t2 + 3*t3) / 6
	w[2] = (1 + 3*t + 3*t2 - 3*t3) / 6
	w[3] = t3 / 6
}
