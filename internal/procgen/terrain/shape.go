package terrain

// shape.go — the base shape: lowlands and mesas, before anything erodes.
//
// The lowland is domain-warped FBM, kept low (Relief is a few metres to a
// few tens): in canyon country the relief is in the cliffs, not in rolling
// hills, and FBM left to make the big shapes is exactly the "noise hills"
// look to avoid.
//
// The big shapes are the MESA FIELD. A low-frequency fractal m, its domain
// warped twice (once broadly, so the outlines meander; once finely, scaled
// by Roughness, so the rims are ragged with alcoves and spurs), is cut at
// thresholds into TIERS: tier 1 covers Coverage of the map, each tier above
// it about 45% of the one below — mesas, and buttes standing on them. Each
// tier adds Height through a smoothstep of m across its threshold, and the
// smoothstep's width is chosen so the rise happens over Rim metres of
// ground: steep walls, flat tops.
//
// Both the thresholds and the rim width are MEASURED rather than guessed:
// m is sampled on a coarse grid over the map first (the classic square; a
// region's footprint bounds, in its own coordinates, for an environment
// region's provider — provider.go), the thresholds are its
// quantiles (so Coverage means what it says on any seed) and the width
// comes from m's mean gradient near the first threshold (so Rim is metres
// whatever the Scale).
//
// A gentle ramp below each rim (a seventh of a tier's height, over a few rim
// widths) stands in for the pediment that real mesas sit on; the strata and
// talus phases turn it into ledges and aprons.

import (
	"math"
	"sort"

	"foundry/internal/procgen/noise"
)

// mesaField is the sampled plateau function and its tier thresholds.
type mesaField struct {
	f, warp, rough, top noise.Fractal
	warpAmt, roughAmt   float64
	edge, rimVar        noise.Fractal
	jointSeed           uint64
	jointScale          float64
	jointAmt            float64
	thresholds          []float64
	width               float64 // smoothstep half-width in m units
}

func newMesaField(s *Settings, over Rect) *mesaField {
	m := s.Mesa
	scale := max(m.Scale, 1)
	mf := &mesaField{
		f:          noise.Fractal{Seed: noise.Hash64(s.Seed, 0x4D45_5341), Octaves: 4, Frequency: 1 / scale, Gain: 0.42},
		warp:       noise.Fractal{Seed: noise.Hash64(s.Seed, 0x4D57_5250), Octaves: 3, Frequency: 0.7 / scale},
		rough:      noise.Fractal{Seed: noise.Hash64(s.Seed, 0x4D52_4F55), Octaves: 3, Frequency: 5 / scale},
		edge:       noise.Fractal{Seed: noise.Hash64(s.Seed, 0x4D45_4447), Octaves: 3, Frequency: 6 / scale, Gain: 0.5},
		rimVar:     noise.Fractal{Seed: noise.Hash64(s.Seed, 0x4D52_5641), Octaves: 2, Frequency: 2.5 / scale},
		top:        noise.Fractal{Seed: noise.Hash64(s.Seed, 0x4D54_4F50), Octaves: 3, Frequency: 3 / scale},
		warpAmt:    scale * 0.2,
		roughAmt:   scale * 0.05 * m.Roughness,
		jointSeed:  noise.Hash64(s.Seed, 0x4A4F_494E),
		jointScale: max(m.JointSpacing, 1),
		jointAmt:   m.Joints,
	}
	// Measure m on a coarse grid over the rectangle: its quantiles and its
	// gradient.
	const k = 96
	w, h := over.MaxX-over.MinX, over.MaxZ-over.MinZ
	vals := make([]float64, 0, k*k)
	for j := 0; j < k; j++ {
		for i := 0; i < k; i++ {
			x := over.MinX + w*(float64(i)+0.5)/k
			z := over.MinZ + h*(float64(j)+0.5)/k
			vals = append(vals, mf.raw(x, z))
		}
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	q := func(share float64) float64 {
		share = min(max(share, 0.001), 0.999)
		return sorted[min(int((1-share)*float64(len(sorted))), len(sorted)-1)]
	}
	cov := min(max(m.Coverage, 0), 1)
	for t := 0; t < max(m.Tiers, 0); t++ {
		mf.thresholds = append(mf.thresholds, q(cov*math.Pow(0.45, float64(t))))
	}
	// Mean |grad m| near the first threshold, by central differences.
	if len(mf.thresholds) > 0 {
		t0 := mf.thresholds[0]
		spread := (sorted[len(sorted)*3/4] - sorted[len(sorted)/4]) * 0.5
		d := max(w, h) / k / 4
		sum, cnt := 0.0, 0
		for idx, v := range vals {
			if math.Abs(v-t0) > spread {
				continue
			}
			i, j := idx%k, idx/k
			x := over.MinX + w*(float64(i)+0.5)/k
			z := over.MinZ + h*(float64(j)+0.5)/k
			gx := (mf.raw(x+d, z) - mf.raw(x-d, z)) / (2 * d)
			gz := (mf.raw(x, z+d) - mf.raw(x, z-d)) / (2 * d)
			sum += math.Hypot(gx, gz)
			cnt++
		}
		grad := 1.0 / scale
		if cnt > 0 && sum > 0 {
			grad = sum / float64(cnt)
		}
		mf.width = max(m.Rim, 0.5) * 0.5 * grad
	}
	return mf
}

// raw is the warped plateau function at (x, z), without the fine edge noise.
func (mf *mesaField) raw(x, z float64) float64 {
	wx, wz := noise.Warp2(mf.warp, x, z, mf.warpAmt)
	if mf.roughAmt > 0 {
		wx, wz = noise.Warp2(mf.rough, wx, wz, mf.roughAmt)
	}
	return mf.f.At2(wx, wz)
}

// at is the plateau function with the ragged edge noise, and the tiers'
// rises: share[t] is 0..1 for tier t at (x, z).
func (mf *mesaField) at(x, z, roughness float64, share []float64) float64 {
	v := mf.raw(x, z)
	if roughness > 0 {
		// Edge noise is scaled to the rim width so it ragged-edges the rim
		// (alcoves, spurs) without pocking the flat tops. It is kept at
		// bench scale (tens of metres): finer, the terracing turns every
		// bump into a cobble.
		v += roughness * mf.width * 1.6 * mf.edge.At2(x, z)
	}
	// Joints: the rock is cut by a network of near-vertical fractures, and a
	// rim retreats along them — so the plateau function is lowered along the
	// borders of a Voronoi network, which gives the outlines straight,
	// angular segments, slots and squared-off points instead of the round
	// lobes noise alone draws.
	if mf.jointAmt > 0 {
		// Only near a rim: elsewhere a lowered line would show as a groove
		// across the flat.
		prox := 0.0
		for _, th := range mf.thresholds {
			prox = max(prox, 1-noise.SmoothStep(mf.width, 3*mf.width, math.Abs(v-th)))
		}
		if prox > 0 {
			c := noise.Cellular2(mf.jointSeed, x/mf.jointScale, z/mf.jointScale, 0.9, noise.MetricEuclidean)
			v -= prox * mf.jointAmt * mf.width * 1.2 * (1 - noise.SmoothStep(0, 0.12, c.Edge()))
		}
	}
	// The rise's width breathes along the rim (0.45x to 1.55x), so the
	// benches widen and pinch out instead of running round every mesa like
	// contour lines.
	w := mf.width * (1 + 0.55*mf.rimVar.At2(x, z)*1.6)
	w = max(w, mf.width*0.3)
	// The rise sits mostly BELOW the threshold (from th-1.6w to th+0.4w), so
	// anything that clears the threshold at all is up on the flat top: small
	// plateaus are buttes with tops, not cones.
	for t, th := range mf.thresholds {
		share[t] = noise.SmoothStep(th-1.6*w, th+0.4*w, v)
	}
	return v
}

// baseShape writes the base heights into g, the mesa field measured over
// the rectangle over.
func baseShape(s *Settings, g *Grid, over Rect, pool Pool) error {
	bs := newBaseShaper(s, over)
	return rows(pool, g.S(), func(j int) {
		z := g.Z(j)
		share := make([]float64, max(s.Mesa.Tiers, 1))
		for i := 0; i < g.S(); i++ {
			g.H[j*g.S()+i] = float32(bs.at(g.X(i), z, share))
		}
	})
}

// baseShaper is the base shape as a function of position: the lowland noise
// and the mesa field, which is all of baseShape. Split out so the far
// backdrop (far.go) can continue the same landforms past the map's edge.
type baseShaper struct {
	s         *Settings
	low, warp noise.Fractal
	mf        *mesaField
	// lf is the provider's extra landforms (landform.go); nil, as for
	// every classic terrain, when none is on.
	lf *landformShaper
}

func newBaseShaper(s *Settings, over Rect) *baseShaper {
	b := s.Base
	bs := &baseShaper{s: s,
		low:  noise.Fractal{Seed: noise.Hash64(s.Seed, 0x4241_5345), Octaves: max(b.Octaves, 1), Frequency: b.Frequency, Gain: b.Gain},
		warp: noise.Fractal{Seed: noise.Hash64(s.Seed, 0x4257_5250), Octaves: 3, Frequency: b.Frequency * 0.6},
	}
	if s.Mesa.Height != 0 && s.Mesa.Tiers > 0 {
		bs.mf = newMesaField(s, over)
	}
	bs.lf = newLandformShaper(s, over)
	return bs
}

// at is the base height at (x, z); share is scratch of Tiers (at least 1).
func (bs *baseShaper) at(x, z float64, share []float64) float64 {
	h := bs.classic(x, z, share)
	if bs.lf != nil {
		h = bs.lf.at(x, z, h)
	}
	return h
}

// classic is the lowland and the mesa field: the base shape before any
// landform (landform.go), unchanged since the canyon's goldens.
func (bs *baseShaper) classic(x, z float64, share []float64) float64 {
	b, m := bs.s.Base, bs.s.Mesa
	wx, wz := x, z
	if b.Warp > 0 {
		wx, wz = noise.Warp2(bs.warp, x, z, b.Warp)
	}
	lowN := bs.low.At2(wx, wz)
	h := b.Height
	mf := bs.mf
	if mf == nil {
		return h + b.Relief*lowN
	}
	v := mf.at(x, z, m.Roughness, share)
	p := share[0]
	// The lowland's relief fades under the plateaus: a mesa top
	// is flat, give or take TopRelief.
	h += b.Relief * lowN * (1 - 0.85*p)
	for t := range mf.thresholds {
		h += m.Height * share[t]
	}
	h += m.TopRelief * mf.top.At2(x, z) * p
	// The pediment: a ramp up to the first rim.
	th := mf.thresholds[0]
	h += m.Height / 7 * noise.SmoothStep(th-7*mf.width, th-mf.width, v) * (1 - p)
	return h
}
