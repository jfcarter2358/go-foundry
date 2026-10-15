package regions

// pairband.go — a region's transition band, pair by pair, from the
// adjacency table (docs/regions/SPEC.md, "Adjacency rules": "when a pair has
// a table transition and a region's own transition is the default, the
// table's width is used for that pair").
//
// A region left at the default 150 m next to a savannah should blend over
// the table's 200 m; the same region next to dunes over 150 m. One number
// per region cannot say that, so a region carries PairTransitions — the
// table's width by the neighbouring environment — and its band at a point is
// a SMOOTH mix of its pair widths, weighted by how near each neighbour is:
//
//	band_i(p) = Σ_j a_j · w_ij / Σ_j a_j,   a_j = smoothstep(1 − (d_j − d_min) / Band_i)
//
// over region i's NEIGHBOURS j (every other region whose band could reach
// i's), where d_j is the signed distance from p to j's footprint, d_min the
// nearest neighbour's, and w_ij i's pair width against j's environment (its
// own Band() when the table says nothing). So along the stretch of edge a
// savannah faces the band is the savannah pair's width, where the dunes take
// over it is theirs, and at the corner where both are near it is a blend —
// continuous everywhere, so the weights stay continuous. The neighbour on
// the other side, if it is also at the default, takes the same width for the
// same pair, so the band is symmetric about the shared edge.
//
// The resolution is done once, from the table, by ResolvePairTransitions
// (the regions stage does it before building its Set, and writes the
// resolved widths to regions.yaml, so a load needs no table and a table edit
// changes the stage's hash). A region with an explicit transition keeps it
// against every neighbour; a forbidden pair has no width (it is a warning,
// adjacent.go). A set where no region has a pair width different from its
// own Band() takes the old path in raw() untouched — the same arithmetic,
// the same bits.

import "math"

// pairBand is one neighbour of a region and the band they blend over.
type pairBand struct {
	j    int
	band float64
}

// IsDefaultTransition reports whether a region's transition is the default:
// unset, or exactly DefaultTransition (the level writes the default out).
func IsDefaultTransition(t float64) bool { return t <= 0 || t == DefaultTransition }

// ResolvePairTransitions fills each default-transition region's
// PairTransitions from the table: for every other environment in the list
// whose pair rule has a width (not forbidden), that width. A region with an
// explicit transition gets none. rs is changed in place; a nil table clears
// them all.
func ResolvePairTransitions(rs []Region, adj *Adjacency) {
	envs := map[string]bool{}
	for i := range rs {
		envs[rs[i].Environment] = true
	}
	for i := range rs {
		r := &rs[i]
		r.PairTransitions = nil
		if adj == nil || !IsDefaultTransition(r.Transition) {
			continue
		}
		for env := range envs {
			if env == r.Environment {
				continue
			}
			rule, ok := adj.Lookup(r.Environment, env)
			if !ok || rule.Forbidden || rule.Transition <= 0 || rule.Transition == r.Band() {
				continue
			}
			if r.PairTransitions == nil {
				r.PairTransitions = map[string]float64{}
			}
			r.PairTransitions[env] = rule.Transition
		}
	}
}

func clonePairs(m map[string]float64) map[string]float64 {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// pairWidth is region i's band against region j.
func (s *Set) pairWidth(i, j int) float64 {
	if w, ok := s.Regions[i].PairTransitions[s.Regions[j].Environment]; ok {
		return w
	}
	return s.Regions[i].Band()
}

// buildPairBands finds each region's neighbours and their pair bands (nb,
// maxBand). Two regions are neighbours when their footprint bounds, each
// grown by half its widest possible band and the warp's reach, overlap —
// beyond that neither's raw weight reaches the other's.
func (s *Set) buildPairBands() {
	n := len(s.Regions)
	s.nb = make([][]pairBand, n)
	s.maxBand = make([]float64, n)
	widest := make([]float64, n)
	for i := range s.Regions {
		widest[i] = s.Regions[i].Band()
		for _, w := range s.Regions[i].PairTransitions {
			widest[i] = math.Max(widest[i], w)
		}
	}
	reach := s.warpReach()
	for i := range s.Regions {
		own := s.Regions[i].Band()
		s.maxBand[i] = own
		if len(s.Regions[i].PairTransitions) == 0 {
			continue
		}
		var nb []pairBand
		varies := false
		for j := range s.Regions {
			if j == i {
				continue
			}
			bi := s.polys[i].bounds.Grow(widest[i]/2 + reach)
			bj := s.polys[j].bounds.Grow(widest[j]/2 + reach)
			if !bi.Overlaps(bj) {
				continue
			}
			w := s.pairWidth(i, j)
			nb = append(nb, pairBand{j: j, band: w})
			if w != own {
				varies = true
				s.maxBand[i] = math.Max(s.maxBand[i], w)
			}
		}
		if varies {
			s.nb[i] = nb
		}
	}
}

// pairBandAt is region i's band at the warped point (wx, wz): its pair
// widths mixed by how near each neighbour is (file comment).
func (s *Set) pairBandAt(i int, wx, wz float64) float64 {
	nb := s.nb[i]
	var dbuf [16]float64
	ds := dbuf[:0]
	dMin := math.Inf(1)
	for _, p := range nb {
		d := SignedDistance(s.polys[p.j].pts, wx, wz)
		ds = append(ds, d)
		dMin = math.Min(dMin, d)
	}
	scale := s.Regions[i].Band()
	sum, wsum := 0.0, 0.0
	for k, p := range nb {
		a := smooth(1 - (ds[k]-dMin)/scale)
		sum += a * p.band
		wsum += a
	}
	if wsum <= 0 {
		return scale
	}
	return sum / wsum
}

// PairBandAt is region i's blend band at world point (x, z) (through the
// warp), for tools and tests.
func (s *Set) PairBandAt(i int, x, z float64) float64 {
	if s.nb[i] == nil {
		return s.Regions[i].Band()
	}
	wx, wz := s.Warp.at(x, z)
	return s.pairBandAt(i, wx, wz)
}
