package bsp

// plane.go — planes and polygon clipping, the primitives everything here is
// built from.
//
// CSG clips a face against a brush's planes. The BSP splits a set of faces by
// one of their planes. Portal generation clips a leaf's boundary against its
// neighbours. All three are the same operation — cut a convex polygon with a
// plane and keep one or both halves — so it is written once here, carefully,
// and everything above it is bookkeeping.
//
// # On the epsilon
//
// Every classification here is against OnPlaneEps rather than against zero.
// That is not defensive rounding; it is what makes the algorithms terminate.
// A vertex that lies exactly on a splitting plane will, in exact arithmetic,
// classify as neither in front nor behind, and both halves of a split get a
// copy of it — which is correct. In floating point, the same vertex classifies
// as a hair in front on one pass and a hair behind on the next, and a BSP build
// that keeps splitting a polygon it has already split does not stop. Snapping a
// band around the plane to "on it" is what closes that.
//
// The value is deliberately generous relative to the scale a level is authored
// at. A brush is placed on a grid measured in tenths of a unit, and two faces
// meant to be coplanar are coplanar to well within a thousandth; nothing an
// author can express falls in the gap.

import "github.com/go-gl/mathgl/mgl32"

// OnPlaneEps is how close to a plane a point counts as being ON it, in world
// units. See the file comment on why this exists at all.
const OnPlaneEps = float32(0.001)

// Plane is an oriented plane: the set of points p where N·p = D. N is unit
// length, and "in front" is the side N points toward.
type Plane struct {
	N mgl32.Vec3
	D float32
}

// PlaneFromPoints builds the plane a wound polygon lies in, with its normal on
// the side the winding reads as outside (counter-clockwise seen from the front,
// the convention every brush face follows).
//
// ok is false for a degenerate loop — fewer than three points, or three that
// are collinear — which has no plane rather than an arbitrary one. Callers
// treat that as "this face is not geometry" rather than substituting a guess,
// because a guessed plane would silently split the level along it.
func PlaneFromPoints(pts []mgl32.Vec3) (Plane, bool) {
	if len(pts) < 3 {
		return Plane{}, false
	}
	// Newell's method rather than one cross product of the first three points:
	// a face's first three corners can be very nearly collinear while the face
	// as a whole is perfectly well defined, and Newell's uses all of them.
	var n mgl32.Vec3
	for i := range pts {
		a, b := pts[i], pts[(i+1)%len(pts)]
		n[0] += (a.Y() - b.Y()) * (a.Z() + b.Z())
		n[1] += (a.Z() - b.Z()) * (a.X() + b.X())
		n[2] += (a.X() - b.X()) * (a.Y() + b.Y())
	}
	l := n.Len()
	if l < 1e-9 {
		return Plane{}, false
	}
	n = n.Mul(1 / l)
	return Plane{N: n, D: n.Dot(Centroid(pts))}, true
}

// centroid is the average of a polygon's corners. Used as the plane's own
// reference point rather than one corner, so a corner that is slightly off the
// plane does not drag the whole plane with it.
func Centroid(pts []mgl32.Vec3) mgl32.Vec3 {
	var sum mgl32.Vec3
	for _, p := range pts {
		sum = sum.Add(p)
	}
	return sum.Mul(1 / float32(len(pts)))
}

// Distance is the signed distance from p to the plane: positive in front.
func (pl Plane) Distance(p mgl32.Vec3) float32 { return pl.N.Dot(p) - pl.D }

// Flip returns the same plane facing the other way.
func (pl Plane) Flip() Plane { return Plane{N: pl.N.Mul(-1), D: -pl.D} }

// SameAs reports whether two planes are the same surface facing the same way,
// within the tolerances a level is authored to.
func (pl Plane) SameAs(o Plane) bool {
	return pl.N.Dot(o.N) > 0.9999 && Abs(pl.D-o.D) < OnPlaneEps
}

// Side is where something sits relative to a plane.
type Side int

const (
	// SideOn is within OnPlaneEps of the plane — for a polygon, coplanar with
	// it.
	SideOn Side = iota
	SideFront
	SideBack
	// SideSpanning is a polygon with points on both sides, which is the only
	// case that needs a split.
	SideSpanning
)

// ClassifyPoint is which side of the plane a single point is on.
func (pl Plane) ClassifyPoint(p mgl32.Vec3) Side {
	d := pl.Distance(p)
	switch {
	case d > OnPlaneEps:
		return SideFront
	case d < -OnPlaneEps:
		return SideBack
	default:
		return SideOn
	}
}

// ClassifyPolygon is where a whole polygon sits. A polygon every one of whose
// points is on the plane is SideOn — coplanar — which the BSP treats
// differently from either half, because a coplanar face belongs to the node
// itself rather than to one of its children.
func (pl Plane) ClassifyPolygon(pts []mgl32.Vec3) Side {
	front, back := false, false
	for _, p := range pts {
		switch pl.ClassifyPoint(p) {
		case SideFront:
			front = true
		case SideBack:
			back = true
		}
		if front && back {
			return SideSpanning
		}
	}
	switch {
	case front:
		return SideFront
	case back:
		return SideBack
	default:
		return SideOn
	}
}

// SplitPolygon cuts a convex polygon with a plane and returns the two halves.
//
// Either may be nil: a polygon entirely on one side comes back whole on that
// side and nil on the other, and a coplanar one comes back on the FRONT, which
// is a convention rather than a fact — the caller that cares (the BSP) never
// asks this about a coplanar polygon, because it has already classified it and
// keeps it at the node.
//
// The intersection point is computed once per crossing edge and shared by both
// halves, so the two are watertight against each other. Computing it twice — a
// separate lerp per half — is the classic way to produce a hairline crack that
// only appears at certain camera angles.
func SplitPolygon(pts []mgl32.Vec3, pl Plane) (front, back []mgl32.Vec3) {
	n := len(pts)
	if n < 3 {
		return nil, nil
	}
	dists := make([]float32, n)
	sides := make([]Side, n)
	hasFront, hasBack := false, false
	for i, p := range pts {
		dists[i] = pl.Distance(p)
		switch {
		case dists[i] > OnPlaneEps:
			sides[i], hasFront = SideFront, true
		case dists[i] < -OnPlaneEps:
			sides[i], hasBack = SideBack, true
		default:
			sides[i] = SideOn
		}
	}
	if !hasBack {
		return pts, nil
	}
	if !hasFront {
		return nil, pts
	}

	for i := 0; i < n; i++ {
		j := (i + 1) % n
		p, q := pts[i], pts[j]

		// A point ON the plane goes into BOTH halves. That is what keeps the
		// two sharing an edge rather than each ending a vertex short of it.
		if sides[i] != SideBack {
			front = append(front, p)
		}
		if sides[i] != SideFront {
			back = append(back, p)
		}

		// An edge only crosses when its ends are strictly on opposite sides;
		// an edge with an end ON the plane has already contributed that end to
		// both halves above.
		if sides[i] == SideOn || sides[j] == SideOn || sides[i] == sides[j] {
			continue
		}
		t := dists[i] / (dists[i] - dists[j])
		x := p.Add(q.Sub(p).Mul(t))
		front = append(front, x)
		back = append(back, x)
	}

	// A "half" of fewer than three points is a sliver the split produced out of
	// numerical noise, not a polygon. Dropping it is safer than keeping a
	// degenerate that every downstream stage then has to guard against.
	if len(front) < 3 {
		front = nil
	}
	if len(back) < 3 {
		back = nil
	}
	return front, back
}

// ClipPolygon keeps only the part of a polygon in FRONT of the plane. The
// workhorse of CSG: a face is clipped against each of a brush's planes in turn,
// and what survives all of them is the part outside that brush.
func ClipPolygon(pts []mgl32.Vec3, pl Plane) []mgl32.Vec3 {
	front, _ := SplitPolygon(pts, pl)
	return front
}

// PolygonArea is a polygon's area, used to drop fragments too small to matter.
//
// Computed as the length of the summed cross products about the centroid, which
// is the same quantity Newell's normal above is the direction of — so a polygon
// this calls degenerate is exactly one PlaneFromPoints would refuse.
func PolygonArea(pts []mgl32.Vec3) float32 {
	if len(pts) < 3 {
		return 0
	}
	c := Centroid(pts)
	var sum mgl32.Vec3
	for i := range pts {
		a := pts[i].Sub(c)
		b := pts[(i+1)%len(pts)].Sub(c)
		sum = sum.Add(a.Cross(b))
	}
	return sum.Len() * 0.5
}

// MinFragmentArea is the smallest area a clipped fragment is worth keeping, in
// square world units. Below it the fragment is a sliver produced by two nearly
// coincident planes, contributes nothing visible, and costs a draw call and a
// BSP leaf like any other face.
const MinFragmentArea = float32(1e-5)

func Abs(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

// Bounds is the axis-aligned box around a set of points.
func Bounds(pts []mgl32.Vec3) (lo, hi mgl32.Vec3) {
	if len(pts) == 0 {
		return mgl32.Vec3{}, mgl32.Vec3{}
	}
	lo, hi = pts[0], pts[0]
	for _, p := range pts[1:] {
		for i := 0; i < 3; i++ {
			if p[i] < lo[i] {
				lo[i] = p[i]
			}
			if p[i] > hi[i] {
				hi[i] = p[i]
			}
		}
	}
	return lo, hi
}

// BoxesOverlap reports whether two AABBs share any volume, with a tolerance so
// two brushes placed exactly flush against each other count as touching. CSG
// needs that: the faces buried by a neighbour are precisely the ones on the
// surface they share.
func BoxesOverlap(aLo, aHi, bLo, bHi mgl32.Vec3) bool {
	const eps = OnPlaneEps
	for i := 0; i < 3; i++ {
		if aHi[i] < bLo[i]-eps || aLo[i] > bHi[i]+eps {
			return false
		}
	}
	return true
}
