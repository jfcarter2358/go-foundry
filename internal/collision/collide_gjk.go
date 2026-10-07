package collision

// collide_gjk.go — do two convex shapes overlap, and by how much.
//
// GJK answers the first question and EPA the second, both over nothing but the
// support functions in shape.go. That is the whole reason for the support-function
// detour: one pair of routines covers box-against-cylinder, ellipsoid-against-
// rotated-hull and every other combination, including the ones that have no
// tidy closed form.
//
// Both work on the *Minkowski difference* A ⊖ B — the set of every a−b. Two
// shapes overlap exactly when that set contains the origin, and the depth of the
// overlap is the distance from the origin to its surface. Neither set is ever
// built; the support function of the difference is support_A(d) − support_B(−d),
// and that is all either algorithm looks at.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	gjkMaxIter = 64
	// epaMaxIter is higher than the textbook 32 on purpose. A polytope
	// approximates a curved surface with flat triangles, so a *deep* overlap of
	// two smooth shapes needs a lot of facets before the closest one settles —
	// two unit spheres overlapping by seven eighths of their radius are still
	// moving at iteration 130, and stopping early there reports a normal tens of
	// degrees off the true one. Everything else converges in a handful, so the
	// cap is only ever reached by the cases that need it and costs nothing in
	// practice.
	epaMaxIter = 192
	// epaTolerance is how much closer a new support point must be before the
	// expansion is worth another face. Below it the polytope has converged on
	// the surface and further iterations only chase float noise.
	epaTolerance = float32(1e-4)
	// degenerate is the length below which a direction carries no information.
	degenerate = float32(1e-9)
)

// minkowskiSupport is the farthest point of A ⊖ B along dir.
func minkowskiSupport(a, b Shape, dir mgl32.Vec3) mgl32.Vec3 {
	return a.Support(dir).Sub(b.Support(dir.Mul(-1)))
}

// Overlap reports whether two convex shapes intersect.
func Overlap(a, b Shape) bool {
	_, hit := gjk(a, b)
	return hit
}

// Penetration reports how far b has to move along normal to stop overlapping a.
//
// normal points from a toward b, so `b.C + normal*depth` separates them and
// `a.C - normal*depth` does the same the other way. ok is false when the shapes
// are not touching at all, in which case normal and depth are meaningless.
func Penetration(a, b Shape) (normal mgl32.Vec3, depth float32, ok bool) {
	simplex, hit := gjk(a, b)
	if !hit {
		return mgl32.Vec3{}, 0, false
	}
	return epa(a, b, simplex)
}

// ---------- GJK ----------

// gjk searches the Minkowski difference for the origin. On success it returns
// the tetrahedron enclosing it, which is what EPA starts from.
//
// The simplex is kept newest-last throughout; every case below reads the newest
// point as `a` and works backward, which is what lets the triangle and line
// cases be reached by shrinking and re-entering rather than duplicated.
func gjk(a, b Shape) ([]mgl32.Vec3, bool) {
	dir := b.Center().Sub(a.Center())
	if dir.Len() < degenerate {
		// Concentric shapes overlap for certain, but give no starting
		// direction; any axis will do to get the search going.
		dir = mgl32.Vec3{1, 0, 0}
	}

	simplex := []mgl32.Vec3{minkowskiSupport(a, b, dir)}
	dir = simplex[0].Mul(-1)

	for i := 0; i < gjkMaxIter; i++ {
		if dir.Len() < degenerate {
			// The origin sits exactly on the current feature. That is a touch,
			// which counts as an overlap; pad the simplex out so EPA has a
			// volume to expand.
			return padSimplex(a, b, simplex), true
		}
		p := minkowskiSupport(a, b, dir)
		if p.Dot(dir) < 0 {
			// The difference does not reach the origin along the one direction
			// it would have to. No overlap, and no further search can find one.
			return nil, false
		}
		simplex = append(simplex, p)
		if doSimplex(&simplex, &dir) {
			return simplex, true
		}
	}
	// Out of iterations: report the shapes as apart rather than inventing a
	// depth. A false negative costs one frame of missed contact; a fabricated
	// normal teleports something.
	return nil, false
}

// doSimplex reduces the simplex to the feature nearest the origin and points
// dir at the origin from it. It returns true when the simplex is a tetrahedron
// containing the origin.
func doSimplex(simplex *[]mgl32.Vec3, dir *mgl32.Vec3) bool {
	s := *simplex
	a := s[len(s)-1] // newest
	ao := a.Mul(-1)

	switch len(s) {
	case 2:
		ab := s[0].Sub(a)
		if ab.Dot(ao) > 0 {
			*dir = tripleCross(ab, ao, ab)
			if dir.Len() < degenerate {
				// The origin lies on segment AB: any perpendicular gets the
				// search off the line.
				*dir = anyPerpendicular(ab)
			}
		} else {
			*simplex = []mgl32.Vec3{a}
			*dir = ao
		}
		return false

	case 3:
		b, c := s[1], s[0]
		ab, ac := b.Sub(a), c.Sub(a)
		abc := ab.Cross(ac)

		// Outside the AC edge?
		if abc.Cross(ac).Dot(ao) > 0 {
			if ac.Dot(ao) > 0 {
				*simplex = []mgl32.Vec3{c, a}
				*dir = tripleCross(ac, ao, ac)
				return false
			}
			*simplex = []mgl32.Vec3{b, a}
			return doSimplex(simplex, dir)
		}
		// Outside the AB edge?
		if ab.Cross(abc).Dot(ao) > 0 {
			*simplex = []mgl32.Vec3{b, a}
			return doSimplex(simplex, dir)
		}
		// Above or below the face. Keep the winding consistent with the side
		// the origin is on, so the tetrahedron case can trust it.
		if abc.Dot(ao) > 0 {
			*dir = abc
		} else {
			*simplex = []mgl32.Vec3{b, c, a}
			*dir = abc.Mul(-1)
		}
		if dir.Len() < degenerate {
			// Origin is in the plane of the triangle.
			return false
		}
		return false

	case 4:
		b, c, d := s[2], s[1], s[0]
		ab, ac, ad := b.Sub(a), c.Sub(a), d.Sub(a)

		// Each face normal is oriented away from the vertex it does not
		// contain, so "origin is outside this face" is a plain sign test
		// however the simplex happened to be wound.
		if outward(ab.Cross(ac), d.Sub(a)).Dot(ao) > 0 {
			*simplex = []mgl32.Vec3{c, b, a} // drop d
			return doSimplex(simplex, dir)
		}
		if outward(ac.Cross(ad), b.Sub(a)).Dot(ao) > 0 {
			*simplex = []mgl32.Vec3{d, c, a} // drop b
			return doSimplex(simplex, dir)
		}
		if outward(ad.Cross(ab), c.Sub(a)).Dot(ao) > 0 {
			*simplex = []mgl32.Vec3{b, d, a} // drop c
			return doSimplex(simplex, dir)
		}
		// Outside none of the three: the origin is enclosed.
		return true
	}
	return false
}

// outward flips n so it points away from the opposite vertex.
func outward(n, toOpposite mgl32.Vec3) mgl32.Vec3 {
	if n.Dot(toOpposite) > 0 {
		return n.Mul(-1)
	}
	return n
}

// tripleCross is (a × b) × c, the "perpendicular to ab, toward the origin"
// direction GJK's edge cases are written in terms of.
func tripleCross(a, b, c mgl32.Vec3) mgl32.Vec3 {
	return a.Cross(b).Cross(c)
}

// anyPerpendicular returns some unit vector at right angles to v.
func anyPerpendicular(v mgl32.Vec3) mgl32.Vec3 {
	axis := mgl32.Vec3{1, 0, 0}
	if math.Abs(float64(v.Normalize().X())) > 0.9 {
		axis = mgl32.Vec3{0, 1, 0}
	}
	return v.Cross(axis)
}

// padSimplex grows a degenerate simplex to four points so EPA has a polytope.
// It only runs for exact touches, where the origin is on the surface of the
// difference and any enclosing volume gives the same answer.
func padSimplex(a, b Shape, simplex []mgl32.Vec3) []mgl32.Vec3 {
	axes := []mgl32.Vec3{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}}
	for _, ax := range axes {
		if len(simplex) >= 4 {
			break
		}
		p := minkowskiSupport(a, b, ax)
		distinct := true
		for _, q := range simplex {
			if p.Sub(q).Len() < 1e-6 {
				distinct = false
				break
			}
		}
		if distinct {
			simplex = append(simplex, p)
		}
	}
	return simplex
}

// ---------- EPA ----------

// epaFace is one triangle of the expanding polytope, with its outward normal
// and that plane's distance from the origin.
type epaFace struct {
	a, b, c int
	normal  mgl32.Vec3
	dist    float32
}

// epa grows the GJK simplex outward until it hugs the surface of the Minkowski
// difference, and reports the closest point on that surface to the origin —
// which is the shallowest way out of the overlap.
func epa(a, b Shape, simplex []mgl32.Vec3) (mgl32.Vec3, float32, bool) {
	if len(simplex) < 4 {
		return mgl32.Vec3{}, 0, false
	}
	verts := append([]mgl32.Vec3(nil), simplex...)

	// A point known to be inside the polytope, used to orient every face
	// outward. The origin itself will not do: it can lie exactly *on* a face,
	// and then the face's distance is zero and its normal carries no sign — the
	// case two spheres hit immediately, because their support points land
	// precisely on the axes. An inward-pointing normal sends the expansion the
	// wrong way and the polytope never converges. The simplex centroid is
	// strictly inside the starting tetrahedron, and the polytope only grows.
	interior := verts[0].Add(verts[1]).Add(verts[2]).Add(verts[3]).Mul(0.25)

	// The horizon's edges, in the order the torn-off faces first name them.
	// A list rather than a map: a map's iteration order is randomised, and the
	// order the new faces are stitched in decides which of two equally near
	// faces closestFace picks next — so with a map, two runs over the same
	// overlap could push a body out along different normals.
	var edges []horizonEdge

	faces := []epaFace{
		makeFace(verts, 0, 1, 2, interior),
		makeFace(verts, 0, 1, 3, interior),
		makeFace(verts, 0, 2, 3, interior),
		makeFace(verts, 1, 2, 3, interior),
	}

	for iter := 0; iter < epaMaxIter; iter++ {
		fi := closestFace(faces)
		if fi < 0 {
			return mgl32.Vec3{}, 0, false
		}
		f := faces[fi]

		p := minkowskiSupport(a, b, f.normal)
		if p.Dot(f.normal)-f.dist < epaTolerance {
			// The surface is no farther out than this face: converged.
			return f.normal, f.dist, true
		}

		// Tear off every face this new point can see, then stitch the hole shut
		// against it. The hole's boundary is exactly the edges that belonged to
		// one removed face rather than two.
		var kept []epaFace
		edges = edges[:0]
		for _, g := range faces {
			if g.normal.Dot(p.Sub(verts[g.a])) > 0 {
				addHorizonEdge(&edges, g.a, g.b)
				addHorizonEdge(&edges, g.b, g.c)
				addHorizonEdge(&edges, g.c, g.a)
				continue
			}
			kept = append(kept, g)
		}

		verts = append(verts, p)
		pi := len(verts) - 1
		for _, e := range edges {
			if e.n != 1 {
				continue // interior edge: both its faces went
			}
			kept = append(kept, makeFace(verts, e.i, e.j, pi, interior))
		}
		if len(kept) == 0 {
			return mgl32.Vec3{}, 0, false
		}
		faces = kept
	}

	// Ran out of expansions. The best face so far is still a real separating
	// direction, just possibly a slightly shallow depth — better than reporting
	// no contact for something visibly interpenetrating.
	if fi := closestFace(faces); fi >= 0 {
		return faces[fi].normal, faces[fi].dist, true
	}
	return mgl32.Vec3{}, 0, false
}

// makeFace builds a face with its normal pointing out of the polytope, judged
// against a point known to be inside it rather than against the origin — see
// the note in epa for why the origin is not a safe reference.
//
// dist is then the distance from the origin to the face's plane, and is
// non-negative for as long as the origin stays enclosed; a small negative value
// is float noise on a face the origin sits on, and clamps to zero.
func makeFace(verts []mgl32.Vec3, ia, ib, ic int, interior mgl32.Vec3) epaFace {
	n := verts[ib].Sub(verts[ia]).Cross(verts[ic].Sub(verts[ia]))
	l := n.Len()
	if l <= degenerate {
		// Collinear vertices: no plane, and no normal. Report it degenerate and
		// let closestFace skip it rather than dividing by nothing.
		return epaFace{a: ia, b: ib, c: ic}
	}
	n = n.Mul(1 / l)
	if n.Dot(verts[ia].Sub(interior)) < 0 {
		n = n.Mul(-1)
		ib, ic = ic, ib
	}
	d := n.Dot(verts[ia])
	if d < 0 {
		d = 0
	}
	return epaFace{a: ia, b: ib, c: ic, normal: n, dist: d}
}

// closestFace returns the face whose plane passes nearest the origin — the
// shallowest exit, and so the one to push along.
func closestFace(faces []epaFace) int {
	best, bestDist := -1, float32(math.MaxFloat32)
	for i, f := range faces {
		if f.normal.Len() < 0.5 {
			continue // degenerate sliver; it carries no direction
		}
		if f.dist < bestDist {
			best, bestDist = i, f.dist
		}
	}
	return best
}

// horizonEdge is one edge of the hole EPA tears in its polytope, and how many
// of the torn-off faces named it.
type horizonEdge struct {
	i, j, n int
}

// addHorizonEdge counts an edge irrespective of which way round it was walked,
// so the two faces sharing it agree they share it. The horizon is a few dozen
// edges at most, so the search is a scan.
func addHorizonEdge(edges *[]horizonEdge, i, j int) {
	if i > j {
		i, j = j, i
	}
	for k := range *edges {
		if e := &(*edges)[k]; e.i == i && e.j == j {
			e.n++
			return
		}
	}
	*edges = append(*edges, horizonEdge{i: i, j: j, n: 1})
}
