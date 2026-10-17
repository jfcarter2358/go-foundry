package regions

// weights.go — the per-point answer: how much of (x, z) each region owns.
//
// See the package comment for the three steps (raw smoothstep weights on a
// warped signed distance, priority compositing, the remainder to the nearest
// region). Weights answers SPARSELY — only the regions with a share, usually
// one — because that is what a per-sample consumer wants (phase 2's blend
// touches a few regions per sample at most); Weight is one region's share,
// for a placement stage asking about its own region.

import (
	"math"
	"sort"
)

// W is one region's share of a point.
type W struct {
	// Region indexes Set.Regions.
	Region int
	Weight float64
}

// raw is region i's raw weight at the WARPED point (wx, wz): the smoothstep
// of its signed distance across its band, 1 half a band inside the edge, 0
// half a band outside, 0.5 on it.
//
// A region with pair widths (pairband.go) blends over a band that varies
// along its edge with whichever neighbour is near; one without blends over
// Band() everywhere, by exactly the arithmetic it always did.
func (s *Set) raw(i int, wx, wz float64) float64 {
	band := s.maxBand[i]
	b := s.polys[i].bounds.Grow(band / 2)
	if !b.Contains(wx, wz) {
		return 0
	}
	d := SignedDistance(s.polys[i].pts, wx, wz)
	if s.nb[i] != nil {
		// Past the widest band the answer does not depend on which.
		if d <= -band/2 {
			return 1
		}
		if d >= band/2 {
			return 0
		}
		band = s.pairBandAt(i, wx, wz)
	}
	return smooth(0.5 - d/band)
}

// smooth is the cubic smoothstep of t clamped to [0, 1].
func smooth(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return t * t * (3 - 2*t)
}

// Weights is every region's share of world point (x, z) that is above zero,
// summing to 1, sorted by region index. Appended to out[:0] so a caller
// sampling a grid reuses one slice. Empty only for an empty set.
func (s *Set) Weights(x, z float64, out []W) []W {
	out = out[:0]
	if len(s.Regions) == 0 {
		return out
	}
	wx, wz := s.Warp.at(x, z)
	remaining := 1.0
	for _, g := range s.groups {
		if remaining <= 1e-12 {
			break
		}
		// The group's coverage is the union of its members' raw weights
		// (1 - Π(1 - w)), shared between them in proportion to raw weight:
		// two equal neighbours on their shared edge cover 0.75 between them
		// and split it evenly; the remaining 0.25 is split evenly too (they
		// are equally near), so each has exactly 0.5.
		sum, miss := 0.0, 1.0
		start := len(out)
		for _, i := range g {
			w := s.raw(i, wx, wz)
			if w <= 0 {
				continue
			}
			out = append(out, W{Region: i, Weight: w})
			sum += w
			miss *= 1 - w
		}
		if sum <= 0 {
			continue
		}
		take := remaining * (1 - miss)
		for k := start; k < len(out); k++ {
			out[k].Weight = take * out[k].Weight / sum
		}
		remaining -= take
	}
	// What no group took goes to the nearest region — split smoothly with
	// regions nearly as near, so the hand-off half way across a gap (or past
	// two neighbours' outer corners) is a band rather than a step.
	if remaining > 1e-12 {
		out = s.fill(wx, wz, remaining, out)
	}
	// Merge (a region can hold a composited share and a fill share) and drop
	// the empty ones.
	sort.SliceStable(out, func(a, b int) bool { return out[a].Region < out[b].Region })
	n := 0
	for _, w := range out {
		switch {
		case w.Weight <= 0:
		case n > 0 && out[n-1].Region == w.Region:
			out[n-1].Weight += w.Weight
		default:
			out[n] = w
			n++
		}
	}
	out = out[:n]
	// Normalise away rounding.
	tot := 0.0
	for _, w := range out {
		tot += w.Weight
	}
	for k := range out {
		out[k].Weight /= tot
	}
	return out
}

// fill shares remaining between the regions nearest the warped point: each
// gets smoothstep(1 - (d - dMin)/band) of its signed distance d over the
// nearest one's dMin, normalised — 1 for the nearest alone, an even split on
// the line equidistant from two. Priority plays no part: it decides
// overlaps, and here nothing overlaps.
func (s *Set) fill(wx, wz, remaining float64, out []W) []W {
	var dbuf [16]float64
	ds := dbuf[:0]
	dMin := math.Inf(1)
	for i := range s.Regions {
		d := SignedDistance(s.polys[i].pts, wx, wz)
		ds = append(ds, d)
		dMin = math.Min(dMin, d)
	}
	start := len(out)
	sum := 0.0
	for i, d := range ds {
		if w := smooth(1 - (d-dMin)/s.Regions[i].Band()); w > 0 {
			out = append(out, W{Region: i, Weight: w})
			sum += w
		}
	}
	for k := start; k < len(out); k++ {
		out[k].Weight = remaining * out[k].Weight / sum
	}
	return out
}

// Weight is region i's share of (x, z), 0..1.
func (s *Set) Weight(i int, x, z float64) float64 {
	var buf [8]W
	for _, w := range s.Weights(x, z, buf[:0]) {
		if w.Region == i {
			return w.Weight
		}
	}
	return 0
}

// WeightFunc is region i's Weight as a closure, for procgen.Env.Region.
func (s *Set) WeightFunc(i int) func(x, z float64) float64 {
	return func(x, z float64) float64 { return s.Weight(i, x, z) }
}
