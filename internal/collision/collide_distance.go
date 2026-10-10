package collision

// collide_distance.go — how far apart two disjoint convex shapes are.
//
// GJK's other half. collide_gjk.go asks whether the Minkowski difference
// contains the origin; this asks how far the origin is *from* it, which is the
// gap between the two shapes. Same support functions, same simplex, different
// question — and the one conservative advancement needs, because "how far may I
// travel before touching" is exactly "how much clearance is there".
//
// The naive alternative, measuring the gap only along the direction of travel,
// is wrong in a way that is easy to miss: it says nothing about whether the two
// are anywhere near each other on the other axes, so something passing cleanly
// to one side reads as a head-on approach.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	distanceMaxIter = 64
	// distanceTolerance is relative, so a gap of 0.001 and a gap of 1000
	// converge to the same number of significant figures.
	distanceTolerance = float32(1e-6)
)

// Distance is the gap between two convex shapes, and 0 when they overlap or
// touch.
func Distance(a, b Shape) float32 {
	dir := b.Center().Sub(a.Center())
	if dir.Len() < degenerate {
		dir = mgl32.Vec3{1, 0, 0}
	}

	simplex := []mgl32.Vec3{minkowskiSupport(a, b, dir)}

	for i := 0; i < distanceMaxIter; i++ {
		v, subset := closestToOrigin(simplex)
		vLen := v.Len()
		if vLen < sweepEpsilon {
			return 0 // the origin is in the difference: the shapes overlap
		}
		simplex = subset

		// Search toward the origin from the closest feature so far.
		w := minkowskiSupport(a, b, v.Mul(-1))

		// Converged when the supporting plane through the new point coincides
		// with the one through v: the set reaches no closer to the origin than
		// v does, so v is the closest point and |v| is the gap.
		//
		// v·v is the minimum of x·v over the set if v really is closest, and
		// v·w is that minimum as actually measured, so their difference is how
		// wrong v still is. Relative to v·v, so the test means the same thing at
		// every scale.
		if v.Dot(v)-v.Dot(w) <= distanceTolerance*v.Dot(v) {
			return vLen
		}
		// A repeat means the search has stalled on the same feature.
		for _, p := range simplex {
			if p.Sub(w).Len() < 1e-9 {
				return vLen
			}
		}
		simplex = append(simplex, w)
		if len(simplex) > 4 {
			simplex = simplex[1:]
		}
	}
	// Out of iterations on a shape pair that will not settle; the best gap so
	// far is still a lower bound, which is what conservative advancement needs.
	v, _ := closestToOrigin(simplex)
	return v.Len()
}

// closestToOrigin returns the point of the simplex's convex hull nearest the
// origin, and the smallest sub-simplex that point lies on.
//
// Reducing to the sub-simplex is what keeps the search from dragging along
// vertices that no longer contribute; the brute force over sub-simplices is
// cheap at four points and avoids the bookkeeping the incremental version needs
// to get right.
func closestToOrigin(pts []mgl32.Vec3) (mgl32.Vec3, []mgl32.Vec3) {
	best := mgl32.Vec3{}
	bestLen := float32(math.MaxFloat32)
	var bestSet []mgl32.Vec3

	consider := func(p mgl32.Vec3, set ...mgl32.Vec3) {
		if l := p.Len(); l < bestLen {
			best, bestLen, bestSet = p, l, set
		}
	}

	n := len(pts)
	// A tetrahedron that encloses the origin means the shapes overlap, and the
	// gap is zero. Without this the sub-simplex search below would report the
	// nearest *face*, which is a positive number for a pair that is actually
	// interpenetrating.
	if n == 4 && originInTetrahedron(pts[0], pts[1], pts[2], pts[3]) {
		return mgl32.Vec3{}, pts
	}
	for i := 0; i < n; i++ {
		consider(pts[i], pts[i])
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			consider(closestOnSegment(pts[i], pts[j]), pts[i], pts[j])
		}
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			for k := j + 1; k < n; k++ {
				consider(closestOnTriangle(pts[i], pts[j], pts[k]), pts[i], pts[j], pts[k])
			}
		}
	}
	return best, bestSet
}

// closestOnSegment is the point of segment ab nearest the origin.
func closestOnSegment(a, b mgl32.Vec3) mgl32.Vec3 {
	ab := b.Sub(a)
	den := ab.Dot(ab)
	if den < 1e-18 {
		return a
	}
	t := -a.Dot(ab) / den
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return a.Add(ab.Mul(t))
}

// closestOnTriangle is the point of triangle abc nearest the origin, by the
// standard Voronoi-region case analysis: each vertex, each edge, then the face.
func closestOnTriangle(a, b, c mgl32.Vec3) mgl32.Vec3 {
	ab, ac := b.Sub(a), c.Sub(a)
	ao := a.Mul(-1)

	d1, d2 := ab.Dot(ao), ac.Dot(ao)
	if d1 <= 0 && d2 <= 0 {
		return a
	}
	bo := b.Mul(-1)
	d3, d4 := ab.Dot(bo), ac.Dot(bo)
	if d3 >= 0 && d4 <= d3 {
		return b
	}
	if vc := d1*d4 - d3*d2; vc <= 0 && d1 >= 0 && d3 <= 0 {
		return a.Add(ab.Mul(d1 / (d1 - d3)))
	}
	co := c.Mul(-1)
	d5, d6 := ab.Dot(co), ac.Dot(co)
	if d6 >= 0 && d5 <= d6 {
		return c
	}
	if vb := d5*d2 - d1*d6; vb <= 0 && d2 >= 0 && d6 <= 0 {
		return a.Add(ac.Mul(d2 / (d2 - d6)))
	}
	if va := d3*d6 - d5*d4; va <= 0 && (d4-d3) >= 0 && (d5-d6) >= 0 {
		return b.Add(c.Sub(b).Mul((d4 - d3) / ((d4 - d3) + (d5 - d6))))
	}
	// Inside the face: project the origin onto its plane.
	n := ab.Cross(ac)
	if nn := n.Dot(n); nn > 1e-18 {
		return n.Mul(a.Dot(n) / nn)
	}
	return closestOnSegment(a, b)
}

// originInTetrahedron reports whether the origin lies inside abcd. Each face's
// normal is oriented away from the vertex opposite it, so "inside" is the
// origin being on the inward side of all four.
func originInTetrahedron(a, b, c, d mgl32.Vec3) bool {
	faces := [4][4]mgl32.Vec3{
		{a, b, c, d},
		{a, b, d, c},
		{a, c, d, b},
		{b, c, d, a},
	}
	for _, f := range faces {
		n := f[1].Sub(f[0]).Cross(f[2].Sub(f[0]))
		if n.Len() < degenerate {
			return false // flat: no interior to be inside of
		}
		n = outward(n, f[3].Sub(f[0]))
		if n.Dot(f[0].Mul(-1)) > 0 {
			return false // the origin is beyond this face
		}
	}
	return true
}
