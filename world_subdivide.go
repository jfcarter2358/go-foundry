package main

// world_subdivide.go — constrained convex subdivision of grid cells for the
// procedural world builder.
//
// The city (rotated buildings, curved roads) is defined as 2D region polygons in
// world XZ, independent of the cell grid. Each grid cell that a region touches is
// split into convex sub-polygons that each lie entirely inside one region (or
// terrain) by repeatedly slicing the cell with the supporting lines of the region
// boundaries. The sub-polygons become real terrain sectors (a triangle fan over a
// shared, de-duplicated point pool), so the city IS the terrain — no overlay
// primitives, no floating decks.
//
// Seamlessness rests on two guarantees:
//   1. A shared point pool keyed by snapped coordinate, so coincident vertices in
//      neighbouring cells/triangles resolve to the exact same index.
//   2. A T-junction conforming pass that inserts any pool vertex lying on the
//      interior of another sub-polygon's edge into that edge, so a vertex never
//      dangles against a neighbour's straight edge (which would crack on a slope).

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// cityGenSnap is the coordinate quantum (1e-3 world units). Every generated vertex
// is snapped to this grid before pooling so that points computed by different
// formulas (line intersections in adjacent cells) still land on the same index.
const cityGenSnap = 1000.0

func snapCoord(v float32) float32 {
	return float32(math.Round(float64(v)*cityGenSnap)) / cityGenSnap
}

func snap2(p mgl32.Vec2) mgl32.Vec2 {
	return mgl32.Vec2{snapCoord(p.X()), snapCoord(p.Y())}
}

func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// ptKey is the integer key used to de-duplicate pooled points.
func ptKey(p mgl32.Vec2) [2]int64 {
	return [2]int64{
		int64(math.Round(float64(p.X()) * cityGenSnap)),
		int64(math.Round(float64(p.Y()) * cityGenSnap)),
	}
}

// pointPool is a coordinate-keyed de-duplicating vertex pool. The resulting slice
// becomes the chunk's Points and sector PointIndices reference into it.
type pointPool struct {
	pts []mgl32.Vec2
	idx map[[2]int64]int
}

func newPointPool() *pointPool {
	return &pointPool{idx: map[[2]int64]int{}}
}

// add snaps p, returns the existing index for that coordinate or appends a new one.
func (pp *pointPool) add(p mgl32.Vec2) int {
	p = snap2(p)
	k := ptKey(p)
	if i, ok := pp.idx[k]; ok {
		return i
	}
	i := len(pp.pts)
	pp.pts = append(pp.pts, p)
	pp.idx[k] = i
	return i
}

// ---------- convex polygon line splitting ----------

// line2 is the implicit line a*x + b*y + c = 0 with unit normal (a,b).
type line2 struct{ a, b, c float32 }

func (l line2) eval(p mgl32.Vec2) float32 { return l.a*p.X() + l.b*p.Y() + l.c }

// polyArea2 returns twice the signed area of a polygon (CCW positive).
func polyArea2(poly []mgl32.Vec2) float32 {
	n := len(poly)
	if n < 3 {
		return 0
	}
	a := float32(0)
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		a += poly[i].X()*poly[j].Y() - poly[j].X()*poly[i].Y()
	}
	return a
}

// cleanPoly removes consecutive near-duplicate vertices (within the snap quantum).
func cleanPoly(poly []mgl32.Vec2) []mgl32.Vec2 {
	if len(poly) < 2 {
		return poly
	}
	const eps = float32(2.0 / cityGenSnap)
	out := poly[:0:0]
	for i := 0; i < len(poly); i++ {
		p := poly[i]
		q := poly[(i+1)%len(poly)]
		if absf(p.X()-q.X()) < eps && absf(p.Y()-q.Y()) < eps {
			continue
		}
		out = append(out, p)
	}
	return out
}

// splitConvexByLine splits a convex polygon by an infinite line into the part on
// the positive side and the part on the negative side. A part with negligible
// area is returned as nil. Polygons that lie wholly on one side return that side
// intact and nil for the other.
func splitConvexByLine(poly []mgl32.Vec2, l line2) (pos, neg []mgl32.Vec2) {
	const eps = float32(1e-5)
	const areaEps = float32(1e-6)
	n := len(poly)
	if n < 3 {
		return poly, nil
	}
	for i := 0; i < n; i++ {
		cur := poly[i]
		nxt := poly[(i+1)%n]
		dc := l.eval(cur)
		dn := l.eval(nxt)
		if dc >= -eps {
			pos = append(pos, cur)
		}
		if dc <= eps {
			neg = append(neg, cur)
		}
		if (dc > eps && dn < -eps) || (dc < -eps && dn > eps) {
			t := dc / (dc - dn)
			ip := cur.Add(nxt.Sub(cur).Mul(t))
			pos = append(pos, ip)
			neg = append(neg, ip)
		}
	}
	pos = cleanPoly(pos)
	neg = cleanPoly(neg)
	if absf(polyArea2(pos)) < areaEps {
		pos = nil
	}
	if absf(polyArea2(neg)) < areaEps {
		neg = nil
	}
	return
}

// subdivideConvex slices a convex polygon by every line in turn, yielding a set of
// convex pieces each lying wholly on one side of every line.
func subdivideConvex(cell []mgl32.Vec2, lines []line2) [][]mgl32.Vec2 {
	polys := [][]mgl32.Vec2{cell}
	for _, l := range lines {
		next := polys[:0:0]
		for _, p := range polys {
			pos, neg := splitConvexByLine(p, l)
			switch {
			case pos != nil && neg != nil:
				next = append(next, pos, neg)
			case pos != nil:
				next = append(next, pos)
			case neg != nil:
				next = append(next, neg)
			default:
				next = append(next, p)
			}
		}
		polys = next
	}
	return polys
}

// polyCentroid returns the area centroid of a simple polygon.
func polyCentroid(poly []mgl32.Vec2) mgl32.Vec2 {
	n := len(poly)
	if n == 0 {
		return mgl32.Vec2{}
	}
	var cx, cy, a float32
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		cross := poly[i].X()*poly[j].Y() - poly[j].X()*poly[i].Y()
		a += cross
		cx += (poly[i].X() + poly[j].X()) * cross
		cy += (poly[i].Y() + poly[j].Y()) * cross
	}
	if absf(a) < 1e-9 {
		// Degenerate: fall back to vertex average.
		var sx, sy float32
		for _, p := range poly {
			sx += p.X()
			sy += p.Y()
		}
		return mgl32.Vec2{sx / float32(n), sy / float32(n)}
	}
	return mgl32.Vec2{cx / (3 * a), cy / (3 * a)}
}

// ---------- T-junction conforming ----------

// conformPoly is one polygon during the conforming pass: an ordered list of pool
// indices (CCW).
type conformPoly struct {
	idxs []int
}

// distToSegment returns the perpendicular distance of p to segment (a,b) and the
// parametric t in [0,1] of the projection.
func distToSegment(p, a, b mgl32.Vec2) (float32, float32) {
	ab := b.Sub(a)
	lenSq := ab.Dot(ab)
	if lenSq < 1e-12 {
		return p.Sub(a).Len(), 0
	}
	t := p.Sub(a).Dot(ab) / lenSq
	tc := t
	if tc < 0 {
		tc = 0
	}
	if tc > 1 {
		tc = 1
	}
	proj := a.Add(ab.Mul(tc))
	return p.Sub(proj).Len(), t
}

// conformTJunctions inserts any pool vertex that lies on the interior of a
// polygon edge into that edge, eliminating T-junctions between subdivided cells
// and their (possibly unsubdivided) neighbours. Runs to a fixed point.
func conformTJunctions(polys []conformPoly, pool *pointPool) {
	// Spatial buckets of pool points by integer world cell (2-unit buckets).
	const bucket = 2.0
	bkey := func(p mgl32.Vec2) [2]int {
		return [2]int{int(math.Floor(float64(p.X() / bucket))), int(math.Floor(float64(p.Y() / bucket)))}
	}
	buckets := map[[2]int][]int{}
	for i, p := range pool.pts {
		k := bkey(p)
		buckets[k] = append(buckets[k], i)
	}

	const onEdgeDist = float32(2.0 / cityGenSnap) // ~2 snap quanta
	const tEps = float32(1e-4)

	for pass := 0; pass < 8; pass++ {
		changed := false
		for pi := range polys {
			poly := &polys[pi]
			n := len(poly.idxs)
			if n < 3 {
				continue
			}
			var out []int
			for e := 0; e < n; e++ {
				ai := poly.idxs[e]
				bi := poly.idxs[(e+1)%n]
				a := pool.pts[ai]
				b := pool.pts[bi]
				out = append(out, ai)

				// Gather candidate pool points near this edge's bbox.
				minX := float32(math.Min(float64(a.X()), float64(b.X()))) - bucket
				maxX := float32(math.Max(float64(a.X()), float64(b.X()))) + bucket
				minY := float32(math.Min(float64(a.Y()), float64(b.Y()))) - bucket
				maxY := float32(math.Max(float64(a.Y()), float64(b.Y()))) + bucket
				type onEdge struct {
					idx int
					t   float32
				}
				var hits []onEdge
				for bx := int(math.Floor(float64(minX / bucket))); bx <= int(math.Floor(float64(maxX/bucket))); bx++ {
					for by := int(math.Floor(float64(minY / bucket))); by <= int(math.Floor(float64(maxY/bucket))); by++ {
						for _, ci := range buckets[[2]int{bx, by}] {
							if ci == ai || ci == bi {
								continue
							}
							d, t := distToSegment(pool.pts[ci], a, b)
							if d <= onEdgeDist && t > tEps && t < 1-tEps {
								hits = append(hits, onEdge{ci, t})
							}
						}
					}
				}
				if len(hits) > 0 {
					sort.Slice(hits, func(i, j int) bool { return hits[i].t < hits[j].t })
					for _, h := range hits {
						// Avoid duplicate insertion of the same index in a row.
						if out[len(out)-1] != h.idx {
							out = append(out, h.idx)
							changed = true
						}
					}
				}
			}
			poly.idxs = out
		}
		if !changed {
			break
		}
	}
}
