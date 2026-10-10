package geom

// triangulate.go — ear-clipping triangulation with hole support.
//
// All polygons are in 2D and assumed to be simple (non-self-intersecting).
// Outer polygons must be CCW; holes must be CW.

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// triCross returns the 2D cross product of (b-a) x (c-a).
func triCross(a, b, c mgl32.Vec2) float32 {
	return (b.X()-a.X())*(c.Y()-a.Y()) - (b.Y()-a.Y())*(c.X()-a.X())
}

// pointInTriangle returns true if p is strictly inside or on the edge of
// triangle (a, b, c) assumed CCW.
func pointInTriangle(p, a, b, c mgl32.Vec2) bool {
	d1 := triCross(a, b, p)
	d2 := triCross(b, c, p)
	d3 := triCross(c, a, p)
	hasNeg := (d1 < 0) || (d2 < 0) || (d3 < 0)
	hasPos := (d1 > 0) || (d2 > 0) || (d3 > 0)
	return !(hasNeg && hasPos)
}

// isEar checks whether vertex at index i is an ear in the polygon described
// by the index list. polygon contains original 2D points.
func isEar(polygon []mgl32.Vec2, indices []int, i int) bool {
	n := len(indices)
	prev := indices[(i-1+n)%n]
	curr := indices[i]
	next := indices[(i+1)%n]

	a := polygon[prev]
	b := polygon[curr]
	c := polygon[next]

	cross := triCross(a, b, c)

	// Must be a convex vertex (CCW polygon → positive cross product)
	if cross <= 0 {
		return false
	}

	// No other vertex may be inside the triangle
	for j := 0; j < n; j++ {
		if j == (i-1+n)%n || j == i || j == (i+1)%n {
			continue
		}
		if pointInTriangle(polygon[indices[j]], a, b, c) {
			return false
		}
	}
	return true
}

// isEarDegenerate is like isEar but accepts collinear vertices (cross == 0).
// Used as a fallback when no strictly convex ear exists, which happens when
// hole vertices coincide with the outer boundary (e.g., an extrusion covering
// the top half of a wall).
func isEarDegenerate(polygon []mgl32.Vec2, indices []int, i int) bool {
	n := len(indices)
	prev := indices[(i-1+n)%n]
	curr := indices[i]
	next := indices[(i+1)%n]

	a := polygon[prev]
	b := polygon[curr]
	c := polygon[next]

	cross := triCross(a, b, c)

	// Accept convex or collinear, reject only reflex
	if cross < 0 {
		return false
	}

	for j := 0; j < n; j++ {
		if j == (i-1+n)%n || j == i || j == (i+1)%n {
			continue
		}
		if pointInTriangle(polygon[indices[j]], a, b, c) {
			return false
		}
	}
	return true
}

// EarClip triangulates a simple polygon using ear clipping.
// polygon must be CCW. Returns triangle indices into the polygon slice.
func EarClip(polygon []mgl32.Vec2) []int {
	n := len(polygon)
	if n < 3 {
		return nil
	}

	indices := make([]int, n)
	for i := range indices {
		indices[i] = i
	}

	var tris []int
	for len(indices) > 2 {
		earFound := false
		// First pass: look for a strictly convex ear
		for i := 0; i < len(indices); i++ {
			if isEar(polygon, indices, i) {
				n := len(indices)
				prev := indices[(i-1+n)%n]
				curr := indices[i]
				next := indices[(i+1)%n]
				tris = append(tris, prev, curr, next)
				indices = append(indices[:i], indices[i+1:]...)
				earFound = true
				break
			}
		}
		if earFound {
			continue
		}
		// Fallback: allow collinear ears to make progress when hole
		// vertices coincide with the outer polygon boundary.
		for i := 0; i < len(indices); i++ {
			if isEarDegenerate(polygon, indices, i) {
				n := len(indices)
				prev := indices[(i-1+n)%n]
				curr := indices[i]
				next := indices[(i+1)%n]
				tris = append(tris, prev, curr, next)
				indices = append(indices[:i], indices[i+1:]...)
				earFound = true
				break
			}
		}
		if !earFound {
			break
		}
	}
	return tris
}

// segmentsIntersectProper returns true if segments (a,b) and (c,d) intersect
// properly (not counting shared endpoints or collinear overlap).
func segmentsIntersectProper(a, b, c, d mgl32.Vec2) bool {
	d1 := triCross(c, d, a)
	d2 := triCross(c, d, b)
	d3 := triCross(a, b, c)
	d4 := triCross(a, b, d)

	if ((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) &&
		((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0)) {
		return true
	}
	return false
}

// segmentIntersectsPolygon returns true if segment (a,b) properly intersects
// any edge of the polygon.
func segmentIntersectsPolygon(poly []mgl32.Vec2, a, b mgl32.Vec2) bool {
	n := len(poly)
	for i := 0; i < n; i++ {
		c := poly[i]
		d := poly[(i+1)%n]
		if segmentsIntersectProper(a, b, c, d) {
			return true
		}
	}
	return false
}

// mergeHoles bridges each hole into the outer polygon, producing a single
// merged polygon suitable for ear-clipping. Both outer and holes are modified
// in-place via the returned merged polygon.
// outer must be CCW; each hole must be CW.
func mergeHoles(outer []mgl32.Vec2, holes [][]mgl32.Vec2) []mgl32.Vec2 {
	if len(holes) == 0 {
		return outer
	}

	// Sort holes by the minimum X of any vertex, ascending. bridgeHole works
	// from a hole's leftmost point, so taking the leftmost hole first means each
	// bridge is cut into an outline that no later bridge has yet complicated —
	// and it makes the merge deterministic, which matters because the vertex
	// order it produces is what every caller indexes into.
	type holeEntry struct {
		idx  int
		minX float32
	}
	entries := make([]holeEntry, len(holes))
	for i, h := range holes {
		mx := float32(math.MaxFloat32)
		for _, p := range h {
			if p.X() < mx {
				mx = p.X()
			}
		}
		entries[i] = holeEntry{idx: i, minX: mx}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].minX < entries[j].minX
	})

	merged := make([]mgl32.Vec2, len(outer))
	copy(merged, outer)

	for _, entry := range entries {
		hole := holes[entry.idx]
		merged = bridgeHole(merged, hole)
	}

	return merged
}

// findHoleBridge returns the index of the outer-polygon vertex that can see
// hole[holeIdx], which must be the hole's leftmost vertex.
//
// The naive answer — the nearest outer vertex whose connecting segment does not
// properly cross an edge — is wrong, and wrong in a way that only shows up on
// symmetric shapes: a square hole centred in a square floor picks a corner
// whose bridge runs diagonally *through the hole itself*, touching two hole
// vertices without properly crossing either edge. The merged polygon is then
// self-intersecting and EarClip returns nothing, so the surface silently
// vanishes. A doorway centred in a wall is exactly that shape.
//
// This is the standard construction instead (Eberly; the same one earcut uses):
// cast a ray left from the hole's leftmost point, take the edge it first hits,
// and bridge to that edge's left-hand endpoint — with a correction for the case
// where another vertex of the outer polygon blocks the view, in which case the
// one subtending the smallest angle with the ray is used. That vertex is
// mutually visible by construction rather than by a test that can miss.
func findHoleBridge(outer []mgl32.Vec2, hole []mgl32.Vec2, holeIdx int) int {
	h := hole[holeIdx]
	n := len(outer)
	qx := float32(-math.MaxFloat32)
	best := -1

	for i := 0; i < n; i++ {
		a := outer[i]
		b := outer[(i+1)%n]
		// Only edges the leftward ray from h can cross, and never a horizontal
		// one, which the ray runs along rather than through.
		if a.Y() == b.Y() {
			continue
		}
		if h.Y() > a.Y() && h.Y() > b.Y() {
			continue
		}
		if h.Y() < a.Y() && h.Y() < b.Y() {
			continue
		}
		x := a.X() + (h.Y()-a.Y())*(b.X()-a.X())/(b.Y()-a.Y())
		if x > h.X() || x <= qx {
			continue
		}
		qx = x
		// The endpoint on the left is the one the bridge can reach without
		// crossing the edge it came from.
		if a.X() < b.X() {
			best = i
		} else {
			best = (i + 1) % n
		}
		if x == h.X() {
			return best
		}
	}
	if best < 0 {
		return -1
	}

	// A vertex of the outer polygon lying in the triangle (h, intersection,
	// candidate) stands between the two. The one at the smallest angle to the
	// ray is visible from h, so take it instead.
	m := outer[best]
	tanMin := float32(math.MaxFloat32)
	for i := 0; i < n; i++ {
		p := outer[i]
		if p.X() > h.X() || p.X() < m.X() || p.X() == h.X() {
			continue
		}
		var ax, ay, cx, cy float32
		if h.Y() < m.Y() {
			ax, ay, cx, cy = h.X(), h.Y(), qx, h.Y()
		} else {
			ax, ay, cx, cy = qx, h.Y(), h.X(), h.Y()
		}
		if !pointInTriangle(p, mgl32.Vec2{ax, ay}, m, mgl32.Vec2{cx, cy}) {
			continue
		}
		den := h.X() - p.X()
		if den == 0 {
			continue
		}
		tan := float32(math.Abs(float64(h.Y()-p.Y()))) / den
		if tan < tanMin || (tan == tanMin && p.X() > m.X()) {
			m = p
			tanMin = tan
			best = i
		}
	}
	return best
}

// bridgeHole connects a single hole polygon into the outer polygon, inserting a
// bridge (two coincident edges) at the mutually-visible vertex pair
// findHoleBridge picks out.
func bridgeHole(outer []mgl32.Vec2, hole []mgl32.Vec2) []mgl32.Vec2 {
	if len(hole) < 3 || len(outer) < 3 {
		return outer
	}
	// The leftmost vertex of the hole, which is what findHoleBridge's leftward
	// ray is cast from. Ties break on Y so the choice is deterministic.
	holeIdx := 0
	for i, p := range hole {
		if p.X() < hole[holeIdx].X() || (p.X() == hole[holeIdx].X() && p.Y() < hole[holeIdx].Y()) {
			holeIdx = i
		}
	}
	bestIdx := findHoleBridge(outer, hole, holeIdx)
	if bestIdx < 0 {
		// The hole is not inside the outer polygon at all. Leaving the outline
		// uncut is the honest failure: something visible and wrong beats a
		// self-intersecting polygon that triangulates into nothing.
		return outer
	}

	// The bridge creates two pairs of coincident vertices (one at the outer
	// bridge point, one at the hole's). Each duplicate is offset perpendicular
	// to the bridge so EarClip sees distinct vertices and its ear test does not
	// reject every candidate on a zero-area sliver. The offset is far below any
	// tolerance a caller samples with, so the duplicates still read as their
	// originals to anything looking a point up by position.
	outerBridge := outer[bestIdx]
	holeBridge := hole[holeIdx]
	bridgeDir := mgl32.Vec2{
		holeBridge.X() - outerBridge.X(),
		holeBridge.Y() - outerBridge.Y(),
	}
	bridgeLen := float32(math.Sqrt(float64(bridgeDir.X()*bridgeDir.X() + bridgeDir.Y()*bridgeDir.Y())))
	var perpX, perpY float32
	if bridgeLen > 1e-9 {
		perpX = -bridgeDir.Y() / bridgeLen
		perpY = bridgeDir.X() / bridgeLen
	} else {
		perpX = 0
		perpY = 1
	}
	const eps = 1e-5
	offX := perpX * eps
	offY := perpY * eps

	outerA := mgl32.Vec2{outerBridge.X() + offX, outerBridge.Y() + offY}
	outerB := mgl32.Vec2{outerBridge.X() - offX, outerBridge.Y() - offY}
	holeA := mgl32.Vec2{holeBridge.X() + offX, holeBridge.Y() + offY}
	holeB := mgl32.Vec2{holeBridge.X() - offX, holeBridge.Y() - offY}

	var result []mgl32.Vec2
	result = append(result, outer[:bestIdx]...)
	result = append(result, outerA)
	result = append(result, holeA)
	for i := 1; i < len(hole); i++ {
		result = append(result, hole[(holeIdx+i)%len(hole)])
	}
	result = append(result, holeB)
	result = append(result, outerB)
	result = append(result, outer[bestIdx+1:]...)
	return result
}

// EarClipWithHoles triangulates an outer polygon with holes.
// outer must be CCW; each hole must be CW.
// Returns triangle indices into the merged polygon vertex array, along with
// the merged polygon itself (needed for coordinate lookup).
func EarClipWithHoles(outer []mgl32.Vec2, holes [][]mgl32.Vec2) ([]mgl32.Vec2, []int) {
	merged := mergeHoles(outer, holes)
	tris := EarClip(merged)
	return merged, tris
}

// ---------- Bowyer-Watson Delaunay triangulation ----------

// delaunayTri is a triangle used during Bowyer-Watson, storing indices into the point array.
type delaunayTri struct {
	v [3]int
}

// circumcircleContains returns true if the point p is inside the circumcircle of triangle (a,b,c).
func circumcircleContains(a, b, c, p mgl32.Vec2) bool {
	ax, ay := float64(a.X()-p.X()), float64(a.Y()-p.Y())
	bx, by := float64(b.X()-p.X()), float64(b.Y()-p.Y())
	cx, cy := float64(c.X()-p.X()), float64(c.Y()-p.Y())
	det := ax*(by*(cx*cx+cy*cy)-cy*(bx*bx+by*by)) -
		ay*(bx*(cx*cx+cy*cy)-cx*(bx*bx+by*by)) +
		(ax*ax+ay*ay)*(bx*cy-by*cx)
	// Positive if CCW triangle and p inside circumcircle
	area := ax*(by-cy) - ay*(bx-cx) + (bx*cy - by*cx)
	if area > 0 {
		return det > 0
	}
	return det < 0
}

// DelaunayTriangulate performs Bowyer-Watson Delaunay triangulation on a set of 2D points.
// Returns triangle indices (triplets) into the points slice. Only triangles whose centroids
// are inside the boundary polygon are kept. If boundary is nil, all non-super-triangle
// triangles are returned.
func DelaunayTriangulate(points []mgl32.Vec2, boundary []mgl32.Vec2) []int {
	n := len(points)
	if n < 3 {
		return nil
	}

	// Compute bounding box
	minX, minY := float32(math.MaxFloat32), float32(math.MaxFloat32)
	maxX, maxY := float32(-math.MaxFloat32), float32(-math.MaxFloat32)
	for _, p := range points {
		if p.X() < minX {
			minX = p.X()
		}
		if p.Y() < minY {
			minY = p.Y()
		}
		if p.X() > maxX {
			maxX = p.X()
		}
		if p.Y() > maxY {
			maxY = p.Y()
		}
	}

	dx := maxX - minX
	dy := maxY - minY
	deltaMax := dx
	if dy > deltaMax {
		deltaMax = dy
	}
	midX := (minX + maxX) / 2
	midY := (minY + maxY) / 2

	// Create super-triangle (large enough to contain all points)
	// Indices n, n+1, n+2
	allPts := make([]mgl32.Vec2, n+3)
	copy(allPts, points)
	allPts[n] = mgl32.Vec2{midX - 20*deltaMax, midY - deltaMax}
	allPts[n+1] = mgl32.Vec2{midX, midY + 20*deltaMax}
	allPts[n+2] = mgl32.Vec2{midX + 20*deltaMax, midY - deltaMax}

	tris := []delaunayTri{{v: [3]int{n, n + 1, n + 2}}}

	type edge struct{ a, b int }

	for i := 0; i < n; i++ {
		p := allPts[i]

		// Find all triangles whose circumcircle contains p
		var badTris []int
		for ti, t := range tris {
			if circumcircleContains(allPts[t.v[0]], allPts[t.v[1]], allPts[t.v[2]], p) {
				badTris = append(badTris, ti)
			}
		}

		// Collect boundary edges of the "hole" (edges that appear in only one bad triangle)
		edgeCount := map[edge]int{}
		for _, ti := range badTris {
			t := tris[ti]
			for j := 0; j < 3; j++ {
				e := edge{t.v[j], t.v[(j+1)%3]}
				// Normalize edge direction for counting
				if e.a > e.b {
					e.a, e.b = e.b, e.a
				}
				edgeCount[e]++
			}
		}

		// Remove bad triangles (iterate in reverse to preserve indices)
		sort.Sort(sort.Reverse(sort.IntSlice(badTris)))
		for _, ti := range badTris {
			tris = append(tris[:ti], tris[ti+1:]...)
		}

		// Create new triangles from boundary edges to the new point
		for e, count := range edgeCount {
			if count == 1 {
				tris = append(tris, delaunayTri{v: [3]int{e.a, e.b, i}})
			}
		}
	}

	// Remove triangles that reference super-triangle vertices
	var result []int
	for _, t := range tris {
		if t.v[0] >= n || t.v[1] >= n || t.v[2] >= n {
			continue
		}
		// If boundary given, only keep triangles whose centroid is inside
		if boundary != nil {
			cx := (points[t.v[0]].X() + points[t.v[1]].X() + points[t.v[2]].X()) / 3
			cy := (points[t.v[0]].Y() + points[t.v[1]].Y() + points[t.v[2]].Y()) / 3
			if !PointInPolygon(mgl32.Vec2{cx, cy}, boundary) {
				continue
			}
		}
		result = append(result, t.v[0], t.v[1], t.v[2])
	}

	return result
}

// PointInPolygon uses ray-casting to test if pt is inside polygon (XZ coordinates).
func PointInPolygon(pt mgl32.Vec2, polygon []mgl32.Vec2) bool {
	n := len(polygon)
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		xi, yi := polygon[i].X(), polygon[i].Y()
		xj, yj := polygon[j].X(), polygon[j].Y()
		if ((yi > pt.Y()) != (yj > pt.Y())) &&
			(pt.X() < (xj-xi)*(pt.Y()-yi)/(yj-yi)+xi) {
			inside = !inside
		}
		j = i
	}
	return inside
}

// ---------- triangle queries ----------
//
// Used by the collision resolver, by the terrain height lookup a sector does
// for its own floor, and by the biome mesher. Pure geometry, so it sits with
// the rest of it rather than inside whichever caller needed it first.

// PointInTriangle2D tests whether p lies inside the 2D triangle (a, b, c).
// Returns (inside, wA, wB, wC) where wA/wB/wC are the barycentric weights
// for vertices A, B, C respectively.
func PointInTriangle2D(p, a, b, c mgl32.Vec2) (bool, float32, float32, float32) {
	v0 := c.Sub(a)
	v1 := b.Sub(a)
	v2 := p.Sub(a)
	d00 := v0.Dot(v0)
	d01 := v0.Dot(v1)
	d02 := v0.Dot(v2)
	d11 := v1.Dot(v1)
	d12 := v1.Dot(v2)
	denom := d00*d11 - d01*d01
	if denom > -1e-12 && denom < 1e-12 {
		return false, 0, 0, 0 // degenerate triangle
	}
	inv := float32(1.0) / denom
	u := (d11*d02 - d01*d12) * inv // weight for C
	v := (d00*d12 - d01*d02) * inv // weight for B
	w := 1 - u - v                 // weight for A
	const eps = float32(-0.001)
	if u >= eps && v >= eps && w >= eps {
		return true, w, v, u
	}
	return false, 0, 0, 0
}

// TriangleNormal returns the (unnormalized) cross-product normal of the
// triangle defined by 3D vertices a, b, c.
func TriangleNormal(a, b, c mgl32.Vec3) mgl32.Vec3 {
	e1 := b.Sub(a)
	e2 := c.Sub(a)
	return mgl32.Vec3{
		e1.Y()*e2.Z() - e1.Z()*e2.Y(),
		e1.Z()*e2.X() - e1.X()*e2.Z(),
		e1.X()*e2.Y() - e1.Y()*e2.X(),
	}
}
