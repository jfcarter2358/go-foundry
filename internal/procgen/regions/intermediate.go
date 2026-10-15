package regions

// intermediate.go — "Insert intermediate region": the footprint of a region
// that separates a forbidden pair (docs/regions/SPEC.md, phase 5).
//
// A desert against a tundra is forbidden; the table suggests a savannah
// between them. PlanIntermediate draws that savannah: a polygon covering the
// whole band where the two currently overlap, wide enough that once a region
// of the suggested environment sits there, with a priority above both, the
// two no longer touch anywhere. The caller (procgen/genlevel's
// InsertIntermediate, which the editor's button and the tests use) turns it
// into a region volume.
//
// Why it works. Put the new region C above A and B in priority. Wherever C's
// raw weight is 1 — half C's band or more inside its footprint — C's group
// takes the whole share before A's or B's group is asked, so A and B get
// nothing there. Everywhere else A's and B's shares can only SHRINK: C's
// group takes its part first and leaves the rest scaled down, and in the
// nearest-region fill C is one more candidate to share with. So the pair can
// overlap after the insertion only where it overlapped before, and if C's
// footprint covers that overlap grown by half C's widest band, C owns all of
// it outright. The construction, all on a raster in the WARPED domain
// (Set.Weights measures every distance at the warped point, so a region's
// footprint lives in warped space; working there with the warp switched off
// makes the answer correct for any warp):
//
//  1. a mask of the samples where both A and B own more than a tenth of
//     AdjacentThreshold, every region taken at its widest band (a pair
//     width can widen A's band once C is its neighbour; the widest band is a
//     superset of every band it could use), over the set's bounds grown by
//     the warp's reach (every point the adjacency check can ask about);
//  2. DILATED by half C's widest band (its own, or a table width against any
//     environment present), half A's and B's widest bands (so that past C's
//     outer edge, where A and B could still split a gap, C is the nearest
//     region by more than either's band and the fill gives them nothing),
//     plus the simplification tolerance and a cell of rasterisation slack;
//  3. split into 4-connected components (two separate contacts — the two
//     arms of a concave footprint — make two regions rather than one that
//     swallows the ground between them), each with its holes filled, so its
//     outline is one simple polygon (a filled 4-connected component has no
//     corner-only pinch);
//  4. TRACED along cell edges, collinear points merged, simplified
//     (Douglas–Peucker at 1.5 cells, halved until the result does not cross
//     itself; at 0 the staircase itself, which never does).
//
// The answer is deterministic (the raster, the component order and the trace
// start are all fixed by the inputs), and the gate test checks the claim the
// whole way round: after the insertion FindAdjacent reports no forbidden
// pair.

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
)

// IntermediateOptions tune the raster. Zero values are the defaults.
type IntermediateOptions struct {
	// Cell is the largest raster spacing, metres (default 4); MaxSide the
	// most samples along the longer side (default 384).
	Cell    float64
	MaxSide int
	Workers int
}

// Intermediate is the region(s) to insert between a forbidden pair.
type Intermediate struct {
	// Environment is the table's suggestion; Priority one above both of the
	// pair's.
	Environment string
	Priority    int
	// Footprints are world XZ polygons, one per separate contact.
	Footprints [][]Point
	// Between names the pair, for status lines.
	Between [2]string
}

// PlanIntermediate is the region(s) of the table's suggested environment
// that separate regions a and b of s (file comment). An error when the table
// does not forbid the pair, suggests nothing, or the two do not overlap.
func PlanIntermediate(s *Set, adj *Adjacency, a, b int, opt IntermediateOptions) (*Intermediate, error) {
	if a < 0 || b < 0 || a >= s.Len() || b >= s.Len() || a == b {
		return nil, errors.New("intermediate region: name two different regions")
	}
	ra, rb := &s.Regions[a], &s.Regions[b]
	rule, ok := adj.Lookup(ra.Environment, rb.Environment)
	if !ok || !rule.Forbidden {
		return nil, fmt.Errorf("intermediate region: the adjacency table does not forbid %s next to %s", ra.Environment, rb.Environment)
	}
	envC := rule.Intermediate
	if envC == "" {
		return nil, fmt.Errorf("intermediate region: the adjacency table names no environment to put between %s and %s",
			ra.Environment, rb.Environment)
	}
	// C's widest band: its own default, or a table width against any
	// environment present.
	bandC := DefaultTransition
	for i := range s.Regions {
		if r, ok := adj.Lookup(envC, s.Regions[i].Environment); ok && !r.Forbidden {
			bandC = math.Max(bandC, r.Transition)
		}
	}
	// The mask set: every region at its widest band — counting the pair
	// width it will take against C once C is its neighbour — no warp.
	rs := make([]Region, s.Len())
	widest := make([]float64, s.Len())
	for i := range s.Regions {
		widest[i] = s.maxBand[i]
		if IsDefaultTransition(s.Regions[i].Transition) {
			if r, ok := adj.Lookup(s.Regions[i].Environment, envC); ok && !r.Forbidden {
				widest[i] = math.Max(widest[i], r.Transition)
			}
		}
		rs[i] = s.Regions[i]
		rs[i].Transition = widest[i]
		rs[i].PairTransitions = nil
	}
	ms, err := NewSet(rs, Warp{})
	if err != nil {
		return nil, err
	}
	dom := s.Bounds().Grow(s.warpReach())

	cell, side := opt.Cell, opt.MaxSide
	if cell <= 0 {
		cell = 4
	}
	if side <= 0 {
		side = 384
	}
	// The dilation needs the cell, the cell needs the grid's extent: size
	// the grid for a generous guess, then use its real cell.
	reachGuess := math.Max(bandC, math.Max(widest[a], widest[b]))/2 + 6*cell
	g := GridOver(dom.Grow(reachGuess+3*cell), cell, side)
	tol := 1.5 * g.Cell
	dilate := math.Max(bandC, math.Max(widest[a], widest[b]))/2 + tol + 2*g.Cell
	if need := dom.Grow(dilate + 2*g.Cell); !g.Rect().Contains(need.MinX, need.MinZ) || !g.Rect().Contains(need.MaxX, need.MaxZ) {
		g = GridOver(need, g.Cell, 0)
	}

	mask, any := overlapMask(ms, g, dom, a, b, AdjacentThreshold/10, opt.Workers)
	if !any {
		return nil, fmt.Errorf("intermediate region: %s and %s do not overlap", ra.ID, rb.ID)
	}
	mask = dilateMask(mask, g.W, g.H, dilate/g.Cell)
	out := &Intermediate{Environment: envC, Priority: max(ra.Priority, rb.Priority) + 1, Between: [2]string{ra.ID, rb.ID}}
	for _, comp := range components(mask, g.W, g.H) {
		fillHoles(comp, g.W, g.H)
		loop, err := traceOutline(comp, g.W, g.H)
		if err != nil {
			return nil, fmt.Errorf("intermediate region: %w", err)
		}
		pts := make([]Point, len(loop))
		for k, v := range loop {
			pts[k] = Point{g.MinX + float64(v[0])*g.Cell, g.MinZ + float64(v[1])*g.Cell}
		}
		out.Footprints = append(out.Footprints, simplifyPolygon(pts, tol))
	}
	return out, nil
}

// overlapMask marks the samples of g inside dom where regions a and b both
// own more than eps.
func overlapMask(s *Set, g Grid, dom Rect, a, b int, eps float64, workers int) ([]bool, bool) {
	mask := make([]bool, g.W*g.H)
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	var next atomic.Int64
	var hit atomic.Bool
	var wg sync.WaitGroup
	for w := 0; w < min(workers, g.H); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var buf []W
			for {
				j := int(next.Add(1) - 1)
				if j >= g.H {
					return
				}
				for i := 0; i < g.W; i++ {
					x, z := g.At(i, j)
					if !dom.Contains(x, z) {
						continue
					}
					buf = s.Weights(x, z, buf)
					wa, wb := 0.0, 0.0
					for _, w := range buf {
						switch w.Region {
						case a:
							wa = w.Weight
						case b:
							wb = w.Weight
						}
					}
					if wa > eps && wb > eps {
						mask[j*g.W+i] = true
						hit.Store(true)
					}
				}
			}
		}()
	}
	wg.Wait()
	return mask, hit.Load()
}

// dilateMask grows the mask by a disc of r cells: every boundary cell stamps
// the disc.
func dilateMask(m []bool, w, h int, r float64) []bool {
	out := append([]bool(nil), m...)
	R := int(math.Ceil(r))
	r2 := r * r
	var disc [][2]int
	for dj := -R; dj <= R; dj++ {
		for di := -R; di <= R; di++ {
			if float64(di*di+dj*dj) <= r2 {
				disc = append(disc, [2]int{di, dj})
			}
		}
	}
	in := func(i, j int) bool { return i >= 0 && j >= 0 && i < w && j < h && m[j*w+i] }
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			if !m[j*w+i] || (in(i-1, j) && in(i+1, j) && in(i, j-1) && in(i, j+1)) {
				continue
			}
			for _, d := range disc {
				x, z := i+d[0], j+d[1]
				if x >= 0 && z >= 0 && x < w && z < h {
					out[z*w+x] = true
				}
			}
		}
	}
	return out
}

// components splits a mask into 4-connected components, ordered by their
// first cell in row-major order; each is a mask of its own.
func components(m []bool, w, h int) [][]bool {
	label := make([]int32, len(m))
	var out [][]bool
	var stack []int
	for k := range m {
		if !m[k] || label[k] != 0 {
			continue
		}
		comp := make([]bool, len(m))
		id := int32(len(out) + 1)
		label[k] = id
		stack = append(stack[:0], k)
		for len(stack) > 0 {
			c := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			comp[c] = true
			i, j := c%w, c/w
			for _, n := range [4][2]int{{i - 1, j}, {i + 1, j}, {i, j - 1}, {i, j + 1}} {
				if n[0] < 0 || n[1] < 0 || n[0] >= w || n[1] >= h {
					continue
				}
				q := n[1]*w + n[0]
				if m[q] && label[q] == 0 {
					label[q] = id
					stack = append(stack, q)
				}
			}
		}
		out = append(out, comp)
	}
	return out
}

// fillHoles sets every cell the outside cannot reach (4-connected, from the
// grid's border) — the component's holes.
func fillHoles(m []bool, w, h int) {
	seen := make([]bool, len(m))
	var stack []int
	push := func(i, j int) {
		if i < 0 || j < 0 || i >= w || j >= h {
			return
		}
		q := j*w + i
		if m[q] || seen[q] {
			return
		}
		seen[q] = true
		stack = append(stack, q)
	}
	for i := 0; i < w; i++ {
		push(i, 0)
		push(i, h-1)
	}
	for j := 0; j < h; j++ {
		push(0, j)
		push(w-1, j)
	}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		i, j := c%w, c/w
		push(i-1, j)
		push(i+1, j)
		push(i, j-1)
		push(i, j+1)
	}
	for k := range m {
		if !seen[k] {
			m[k] = true
		}
	}
}

// traceOutline walks a filled component's outline along cell edges, interior
// on the left, from its lowest corner; collinear corners are dropped. Grid
// corners are (i, j), cell (i, j) spanning corners (i, j)..(i+1, j+1).
func traceOutline(m []bool, w, h int) ([][2]int, error) {
	in := func(i, j int) bool { return i >= 0 && j >= 0 && i < w && j < h && m[j*w+i] }
	next := map[[2]int][2]int{}
	add := func(a, b [2]int) error {
		if _, dup := next[a]; dup {
			return errors.New("the band's outline pinches at a corner")
		}
		next[a] = b
		return nil
	}
	edges := 0
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			if !in(i, j) {
				continue
			}
			var err error
			if !in(i, j-1) {
				err = errors.Join(err, add([2]int{i, j}, [2]int{i + 1, j}))
				edges++
			}
			if !in(i+1, j) {
				err = errors.Join(err, add([2]int{i + 1, j}, [2]int{i + 1, j + 1}))
				edges++
			}
			if !in(i, j+1) {
				err = errors.Join(err, add([2]int{i + 1, j + 1}, [2]int{i, j + 1}))
				edges++
			}
			if !in(i-1, j) {
				err = errors.Join(err, add([2]int{i, j + 1}, [2]int{i, j}))
				edges++
			}
			if err != nil {
				return nil, err
			}
		}
	}
	if edges == 0 {
		return nil, errors.New("empty band")
	}
	starts := make([][2]int, 0, len(next))
	for k := range next {
		starts = append(starts, k)
	}
	sort.Slice(starts, func(a, b int) bool {
		if starts[a][1] != starts[b][1] {
			return starts[a][1] < starts[b][1]
		}
		return starts[a][0] < starts[b][0]
	})
	start := starts[0]
	var loop [][2]int
	for v, n := start, 0; ; n++ {
		loop = append(loop, v)
		v = next[v]
		if v == start {
			break
		}
		if n > edges {
			return nil, errors.New("the band's outline does not close")
		}
	}
	if len(loop) != edges {
		return nil, errors.New("the band's outline has more than one loop")
	}
	// Drop the corners a straight run passes through.
	var out [][2]int
	n := len(loop)
	for k := range loop {
		p, c, q := loop[(k+n-1)%n], loop[k], loop[(k+1)%n]
		if (c[0]-p[0])*(q[1]-c[1])-(c[1]-p[1])*(q[0]-c[0]) != 0 {
			out = append(out, c)
		}
	}
	return out, nil
}

// simplifyPolygon is Douglas–Peucker on a closed polygon at tol metres,
// halving tol until the result is a valid footprint (≥ 3 points, area, no
// crossing edges); the input itself when nothing smaller works.
func simplifyPolygon(pts []Point, tol float64) []Point {
	for t := tol; t > 1e-6; t /= 2 {
		s := dpClosed(pts, t)
		if len(s) >= 3 && math.Abs(SignedArea(s)) > 1e-6 {
			if _, _, bad := SelfIntersection(s); !bad {
				return s
			}
		}
	}
	return pts
}

// dpClosed splits the ring at point 0 and the point farthest from it and
// simplifies each half.
func dpClosed(pts []Point, tol float64) []Point {
	n := len(pts)
	if n <= 4 {
		return append([]Point(nil), pts...)
	}
	far, fd := 0, -1.0
	for k, p := range pts {
		if d := (p[0]-pts[0][0])*(p[0]-pts[0][0]) + (p[1]-pts[0][1])*(p[1]-pts[0][1]); d > fd {
			far, fd = k, d
		}
	}
	a := dp(pts[:far+1], tol)
	b := dp(append(append([]Point(nil), pts[far:]...), pts[0]), tol)
	out := append(a[:len(a)-1], b[:len(b)-1]...)
	return out
}

// dp is Douglas–Peucker on an open polyline (both ends kept).
func dp(pts []Point, tol float64) []Point {
	if len(pts) < 3 {
		return append([]Point(nil), pts...)
	}
	keep := make([]bool, len(pts))
	keep[0], keep[len(pts)-1] = true, true
	var rec func(a, b int)
	rec = func(a, b int) {
		best, bd := -1, tol*tol
		for k := a + 1; k < b; k++ {
			if d := segmentDistSq(pts[k][0], pts[k][1], pts[a], pts[b]); d > bd {
				best, bd = k, d
			}
		}
		if best < 0 {
			return
		}
		keep[best] = true
		rec(a, best)
		rec(best, b)
	}
	rec(0, len(pts)-1)
	var out []Point
	for k, p := range pts {
		if keep[k] {
			out = append(out, p)
		}
	}
	return out
}
