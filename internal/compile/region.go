package compile

// region.go — a convex region as a set of half-spaces, and the polygons that
// bound it.
//
// A BSP leaf is not stored as a mesh; it is stored as the list of planes the
// path from the root imposed on it, and a point is in the leaf when it is in
// front of every one. That representation is free to produce and exact, but it
// says nothing about the leaf's SHAPE — where it is, how big it is, which parts
// of which planes actually bound it — and portal generation is entirely a
// question about shape.
//
// Turning the half-spaces back into polygons is the bridge. For each plane, lay
// down a quad on it large enough to span the world, then clip that quad against
// every other plane of the region. What is left is exactly the region's face on
// that plane: empty if the plane turned out to be redundant (a leaf carries
// every plane from the root down, and most of them end up nowhere near it), and
// the actual bounding polygon otherwise.

import "github.com/go-gl/mathgl/mgl32"

// regionPolygons is the polygons bounding a convex region, one per plane that
// actually contributes; nil in the slot of a plane that does not.
//
// The result is indexed BY PLANE rather than compacted, because the caller that
// matters — portal generation — needs to know which plane a polygon came from
// in order to find the neighbouring leaf that shares it.
//
// worldLo/worldHi size the starting quad. It has to be big enough that the
// region's own planes, not the quad's edges, are what bound the result; the
// diagonal of the world box is used, which is generous by construction.
func regionPolygons(planes []Plane, worldLo, worldHi mgl32.Vec3) [][]mgl32.Vec3 {
	span := worldHi.Sub(worldLo).Len()
	if span < 1 {
		span = 1
	}
	out := make([][]mgl32.Vec3, len(planes))
	for i, pl := range planes {
		poly := quadOnPlane(pl, span)
		for j, other := range planes {
			if i == j || poly == nil {
				continue
			}
			poly = ClipPolygon(poly, other)
		}
		if len(poly) >= 3 && PolygonArea(poly) >= minFragmentArea {
			out[i] = poly
		}
	}
	return out
}

// regionBounds is the axis-aligned box around a convex region, clamped to the
// world box.
//
// Derived from the bounding polygons rather than from the planes directly:
// a region's planes include every one from the root down, and reading a box off
// them would give the box of the ROOT for any leaf whose own planes happen not
// to be axial. A region with no polygons at all — an empty intersection, which
// a degenerate split can produce — comes back as the world box rather than as
// an inverted one, so that nothing downstream reads it as covering everything
// or as covering nothing at random.
func regionBounds(planes []Plane, worldLo, worldHi mgl32.Vec3) (lo, hi mgl32.Vec3) {
	polys := regionPolygons(planes, worldLo, worldHi)
	first := true
	for _, p := range polys {
		if p == nil {
			continue
		}
		l, h := boundsOf(p)
		if first {
			lo, hi, first = l, h, false
			continue
		}
		for i := 0; i < 3; i++ {
			lo[i] = minf(lo[i], l[i])
			hi[i] = maxf(hi[i], h[i])
		}
	}
	if first {
		return worldLo, worldHi
	}
	for i := 0; i < 3; i++ {
		lo[i] = maxf(lo[i], worldLo[i])
		hi[i] = minf(hi[i], worldHi[i])
	}
	return lo, hi
}

// quadOnPlane is a square of the given half-extent lying on a plane, wound
// counter-clockwise seen from the front — the same convention every face in the
// engine follows, so a polygon cut out of it has the plane's own normal.
func quadOnPlane(pl Plane, size float32) []mgl32.Vec3 {
	u := perpendicularTo(pl.N)
	v := pl.N.Cross(u)
	c := pl.N.Mul(pl.D)
	u, v = u.Mul(size), v.Mul(size)
	return []mgl32.Vec3{
		c.Sub(u).Sub(v),
		c.Add(u).Sub(v),
		c.Add(u).Add(v),
		c.Sub(u).Add(v),
	}
}

// perpendicularTo is any unit vector at right angles to n. Built by crossing
// with whichever axis n is least aligned to, so the cross product is never near
// zero and the result never degenerates.
func perpendicularTo(n mgl32.Vec3) mgl32.Vec3 {
	var axis mgl32.Vec3
	axis[axisOfSmallest(n)] = 1
	return n.Cross(axis).Normalize()
}

// axisOfSmallest is the index of n's smallest-magnitude component.
func axisOfSmallest(n mgl32.Vec3) int {
	a := 0
	for i := 1; i < 3; i++ {
		if geomAbs(n[i]) < geomAbs(n[a]) {
			a = i
		}
	}
	return a
}
