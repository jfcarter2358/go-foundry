// Package regions is the environment-regions model of the level generation
// doc (docs/regions/SPEC.md): which environment owns each point of the world,
// and how much.
//
// A level carries region volumes (world.VolumeRegion): a closed polygon
// footprint in world XZ, the environment preset it grows ("desert_canyon",
// "savannah"), a priority and a transition width. The level generation doc's
// `regions` stage copies them into its params (genlevel.SyncRegions), and
// everything downstream asks this package one question — given a world point,
// what share of it does each region own? — through Set.Weights.
//
// The answer is a pure function of the point and the region list, so any
// tile, any stage and any goroutine can compute its own, and two runs of the
// same list agree bit for bit:
//
//  1. Each region's RAW weight is a smoothstep of the signed distance from
//     the point to its footprint (negative inside), across the region's
//     transition width, centred on the edge: 1 half a band inside, 0 half a
//     band outside, 0.5 on the line itself. The point is domain-warped first
//     (one warp for the whole set, so a shared edge stays shared), which is
//     what makes a boundary meander instead of following the straight sides
//     an author clicked.
//  2. Raw weights are COMPOSITED in priority order: the highest priority
//     group takes its share first (the union of its members' coverage, split
//     between them by raw weight), the next group shares what is left, and
//     so on down.
//  3. Whatever share no group took — all of it, at a point no region's band
//     reaches — goes to the NEAREST region by signed distance, split smoothly
//     with any region nearly as near (within its band of the nearest), so a
//     gap between regions hands over across a band at its midline instead of
//     with a step. The shares sum to 1.
//
// So deep inside one region its weight is exactly 1; on the edge between two
// equal-priority neighbours each has exactly 0.5 and the band is symmetric;
// a higher-priority region carves its footprint out of a lower one it
// overlaps, blending over its own band.
//
// Layering: pure CPU, below procgen's core (internal/layering_test.go). The
// core never imports it — Env.Region carries a weight FUNCTION — and the
// stages and genlevel share the regions stage's parameter names through
// names.go.
package regions

// regions.go — Region, Set and building one.

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"foundry/internal/procgen/noise"
)

// DefaultTransition is a region's blend band width when the level does not
// say, in metres (ENVIRONMENT.md: "default 150 m").
const DefaultTransition = 150.0

// Point is a world XZ position.
type Point = [2]float64

// Rect is a world-space XZ rectangle.
type Rect struct {
	MinX, MinZ, MaxX, MaxZ float64
}

// Empty reports whether the rectangle has no area.
func (r Rect) Empty() bool { return r.MaxX <= r.MinX || r.MaxZ <= r.MinZ }

// Grow is the rectangle pushed out by d on every side.
func (r Rect) Grow(d float64) Rect { return Rect{r.MinX - d, r.MinZ - d, r.MaxX + d, r.MaxZ + d} }

// Union is the smallest rectangle holding both (an empty one is ignored).
func (r Rect) Union(o Rect) Rect {
	if r.Empty() {
		return o
	}
	if o.Empty() {
		return r
	}
	return Rect{math.Min(r.MinX, o.MinX), math.Min(r.MinZ, o.MinZ), math.Max(r.MaxX, o.MaxX), math.Max(r.MaxZ, o.MaxZ)}
}

// Contains reports whether (x, z) is inside the rectangle (edges included).
func (r Rect) Contains(x, z float64) bool {
	return x >= r.MinX && x <= r.MaxX && z >= r.MinZ && z <= r.MaxZ
}

// Overlaps reports whether two rectangles share any area or edge.
func (r Rect) Overlaps(o Rect) bool {
	return r.MinX <= o.MaxX && o.MinX <= r.MaxX && r.MinZ <= o.MaxZ && o.MinZ <= r.MaxZ
}

// Region is one environment region, in world space.
type Region struct {
	// ID is the level volume's ID; Key is ID made safe for a stage ID
	// segment (Key). Keys are unique within a Set.
	ID, Key string
	// Environment names the preset (resources/environments/<name>.fgen).
	Environment string
	// Generator, when set, is a .fgen to use instead of the environment's
	// preset (a resources-relative path, or a bare preset name).
	Generator string
	// Priority: higher wins where regions overlap.
	Priority int
	// Transition is the blend band's width in metres, centred on the
	// footprint's edge. <= 0 reads as DefaultTransition.
	Transition float64
	// Anchor is the origin the preset's own coordinates are measured from;
	// HasAnchor false means the footprint's centroid (see AnchorPoint).
	Anchor    Point
	HasAnchor bool
	// Footprint is the closed polygon, world XZ, at least 3 points, either
	// winding, concave allowed, not self-intersecting.
	Footprint []Point
	// PairTransitions is, by the environment of a NEIGHBOURING region, the
	// band this region blends over against that neighbour — the adjacency
	// table's width for the pair, filled by ResolvePairTransitions only for
	// a region left at the default transition (pairband.go). Empty: Band()
	// against every neighbour, exactly as before pair widths existed.
	PairTransitions map[string]float64
}

// Band is the transition width this region blends over.
func (r *Region) Band() float64 {
	if r.Transition <= 0 {
		return DefaultTransition
	}
	return r.Transition
}

// AnchorPoint is Anchor, or the footprint's area centroid.
func (r *Region) AnchorPoint() Point {
	if r.HasAnchor {
		return r.Anchor
	}
	return Centroid(r.Footprint)
}

// Bounds is the footprint's bounding rectangle.
func (r *Region) Bounds() Rect { return PolygonBounds(r.Footprint) }

// Warp is the boundary meander: a domain warp applied to every point before
// any distance is measured. Amount 0 turns it off.
type Warp struct {
	// Amount is the largest displacement, metres; Scale the wavelength of
	// the first octave, metres.
	Amount, Scale float64
	Seed          uint64
}

// at is the warped point.
func (w Warp) at(x, z float64) (float64, float64) {
	if w.Amount <= 0 || w.Scale <= 0 {
		return x, z
	}
	f := noise.Fractal{Seed: noise.Hash64(w.Seed, 0x5245_4749_4f4e), Octaves: 3, Frequency: 1 / w.Scale, Gain: 0.5}
	return noise.Warp2(f, x, z, w.Amount)
}

// Set is a resolved, validated region list: what Weights answers from.
// Read-only once built, so it is safe to share between goroutines.
type Set struct {
	// Regions are sorted by Key.
	Regions []Region
	Warp    Warp

	polys  []polygon
	groups [][]int // region indices by priority, highest first
	// nb[i] is region i's neighbours with their pair bands, nil when every
	// neighbour blends over Band() (pairband.go); maxBand[i] the widest band
	// region i ever uses.
	nb      [][]pairBand
	maxBand []float64
}

// NewSet validates the regions and builds a Set. The input is copied and
// sorted by Key, so the order the level lists volumes in changes nothing.
// Keys are filled from IDs when empty.
func NewSet(in []Region, warp Warp) (*Set, error) {
	s := &Set{Warp: warp, Regions: make([]Region, len(in))}
	for i, r := range in {
		r.Footprint = append([]Point(nil), r.Footprint...)
		r.PairTransitions = clonePairs(r.PairTransitions)
		if r.Key == "" {
			r.Key = Key(r.ID)
		}
		s.Regions[i] = r
	}
	sort.SliceStable(s.Regions, func(a, b int) bool { return s.Regions[a].Key < s.Regions[b].Key })
	if probs := Validate(s.Regions); len(probs) > 0 {
		return nil, fmt.Errorf("regions: %s", strings.Join(probs, "; "))
	}
	s.polys = make([]polygon, len(s.Regions))
	byPri := map[int][]int{}
	var pris []int
	for i := range s.Regions {
		s.polys[i] = newPolygon(s.Regions[i].Footprint)
		p := s.Regions[i].Priority
		if _, ok := byPri[p]; !ok {
			pris = append(pris, p)
		}
		byPri[p] = append(byPri[p], i)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(pris)))
	for _, p := range pris {
		s.groups = append(s.groups, byPri[p])
	}
	s.buildPairBands()
	return s, nil
}

// Validate lists what is wrong with a region list: a missing or duplicate
// key, a missing environment, a footprint with fewer than 3 points, no area
// or crossing edges, a non-finite number.
func Validate(rs []Region) []string {
	var out []string
	keys := map[string]string{}
	for i := range rs {
		r := &rs[i]
		name := r.ID
		if name == "" {
			name = fmt.Sprintf("#%d", i+1)
		}
		key := r.Key
		if key == "" {
			key = Key(r.ID)
		}
		if key == "" {
			out = append(out, fmt.Sprintf("region %s: needs an id", name))
		} else if prev, dup := keys[key]; dup {
			out = append(out, fmt.Sprintf("regions %s and %s: both make the key %q; rename one", prev, name, key))
		} else {
			keys[key] = name
		}
		if r.Environment == "" {
			out = append(out, fmt.Sprintf("region %s: names no environment", name))
		}
		if math.IsNaN(r.Transition) || math.IsInf(r.Transition, 0) {
			out = append(out, fmt.Sprintf("region %s: transition is not a number", name))
		}
		for env, w := range r.PairTransitions {
			if !(w > 0) || math.IsInf(w, 0) {
				out = append(out, fmt.Sprintf("region %s: the pair transition against %s is not a positive number", name, env))
			}
		}
		if len(r.Footprint) < 3 {
			out = append(out, fmt.Sprintf("region %s: a footprint needs at least 3 points (has %d)", name, len(r.Footprint)))
			continue
		}
		finite := true
		for _, p := range r.Footprint {
			for _, c := range p {
				finite = finite && !math.IsNaN(c) && !math.IsInf(c, 0)
			}
		}
		if !finite {
			out = append(out, fmt.Sprintf("region %s: a footprint point is not a number", name))
			continue
		}
		if math.Abs(SignedArea(r.Footprint)) < 1e-6 {
			out = append(out, fmt.Sprintf("region %s: the footprint has no area", name))
		}
		if a, b, ok := SelfIntersection(r.Footprint); ok {
			out = append(out, fmt.Sprintf("region %s: footprint edges %d and %d cross", name, a, b))
		}
	}
	return out
}

// Key is a region ID made safe to be a segment of a stage ID (and so of a
// folder name and a level object ID): lower case, every run of characters
// outside [a-z0-9] one underscore, no leading or trailing underscore, and a
// leading letter ("r" is prefixed to one that starts with a digit). Empty for
// an ID with nothing usable in it.
func Key(id string) string {
	var b strings.Builder
	under := false
	for _, c := range strings.ToLower(id) {
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if !ok {
			under = b.Len() > 0
			continue
		}
		if under {
			b.WriteByte('_')
			under = false
		}
		b.WriteRune(c)
	}
	k := b.String()
	if k != "" && k[0] >= '0' && k[0] <= '9' {
		k = "r" + k
	}
	return k
}

// Index is the index of the region with the given key, or -1.
func (s *Set) Index(key string) int {
	for i := range s.Regions {
		if s.Regions[i].Key == key {
			return i
		}
	}
	return -1
}

// Len is how many regions there are.
func (s *Set) Len() int { return len(s.Regions) }

// RegionBounds is region i's footprint bounds.
func (s *Set) RegionBounds(i int) Rect { return s.polys[i].bounds }

// BandBounds is region i's footprint bounds plus half its widest band (its
// own, or a pair width from the adjacency table) and the warp's reach:
// outside it the region's RAW weight is 0 everywhere (it can still be the
// nearest region to an unclaimed gap, which Weights fills).
func (s *Set) BandBounds(i int) Rect {
	return s.polys[i].bounds.Grow(s.maxBand[i]/2 + s.warpReach())
}

// MaxBand is the widest band region i blends over: Band(), or a wider pair
// width against one of its neighbours.
func (s *Set) MaxBand(i int) float64 { return s.maxBand[i] }

// Bounds is the union of every region's band bounds: everything the set
// gives a raw weight to.
func (s *Set) Bounds() Rect {
	var r Rect
	for i := range s.Regions {
		r = r.Union(s.BandBounds(i))
	}
	return r
}

// warpReach is how far the warp can move a point: the fractal is about
// [-1, 1] per axis, so Amount per axis and Amount·√2 diagonally — rounded
// up to 2·Amount, since "about" is not a bound and a conservative box costs
// only a few empty samples.
func (s *Set) warpReach() float64 {
	if s.Warp.Amount <= 0 || s.Warp.Scale <= 0 {
		return 0
	}
	return s.Warp.Amount * 2
}
