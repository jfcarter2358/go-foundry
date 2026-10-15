package regions

// polygon.go — the footprint geometry: signed distance, area, centroid,
// bounds and the self-intersection check.
//
// A footprint is a closed polygon given as its corners in order, either
// winding, the closing edge implied. Concave is fine; crossing edges are not
// (Validate refuses them), because "inside" stops meaning anything an author
// would recognise.
//
// SignedDistance is the Euclidean distance to the nearest edge, negative
// inside. Inside is the even-odd crossing test, which for a simple polygon is
// the same as the non-zero rule and needs no winding. It is O(edges) per
// call; footprints are a handful to a few dozen corners and the samples that
// ask are bounded by the band boxes, so nothing cleverer is needed yet. The
// polygon type keeps a bounding box so a far point is rejected without
// walking the edges.

import "math"

// polygon is a footprint ready for distance queries.
type polygon struct {
	pts    []Point
	bounds Rect
}

func newPolygon(pts []Point) polygon {
	return polygon{pts: pts, bounds: PolygonBounds(pts)}
}

// SignedDistance is the distance from (x, z) to the polygon's boundary,
// negative inside, positive outside, 0 on an edge.
func SignedDistance(pts []Point, x, z float64) float64 {
	n := len(pts)
	if n == 0 {
		return math.Inf(1)
	}
	best := math.Inf(1)
	inside := false
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		a, b := pts[j], pts[i]
		if d := segmentDistSq(x, z, a, b); d < best {
			best = d
		}
		// Crossing test against a ray toward +x.
		if (a[1] > z) != (b[1] > z) {
			xc := a[0] + (z-a[1])*(b[0]-a[0])/(b[1]-a[1])
			if x < xc {
				inside = !inside
			}
		}
	}
	d := math.Sqrt(best)
	if inside {
		return -d
	}
	return d
}

// segmentDistSq is the squared distance from (x, z) to segment ab.
func segmentDistSq(x, z float64, a, b Point) float64 {
	dx, dz := b[0]-a[0], b[1]-a[1]
	px, pz := x-a[0], z-a[1]
	l2 := dx*dx + dz*dz
	t := 0.0
	if l2 > 0 {
		t = (px*dx + pz*dz) / l2
		t = math.Max(0, math.Min(1, t))
	}
	ex, ez := px-t*dx, pz-t*dz
	return ex*ex + ez*ez
}

// PointInPolygon reports whether (x, z) is strictly inside (even-odd).
func PointInPolygon(pts []Point, x, z float64) bool {
	return SignedDistance(pts, x, z) < 0
}

// SignedArea is the shoelace area: positive for counter-clockwise in (x, z)
// read as (x, y), negative for clockwise.
func SignedArea(pts []Point) float64 {
	a := 0.0
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		a += pts[j][0]*pts[i][1] - pts[i][0]*pts[j][1]
	}
	return a / 2
}

// Centroid is the polygon's area centroid (the vertex mean for a degenerate
// one).
func Centroid(pts []Point) Point {
	if len(pts) == 0 {
		return Point{}
	}
	a := SignedArea(pts)
	if math.Abs(a) < 1e-9 {
		var c Point
		for _, p := range pts {
			c[0] += p[0]
			c[1] += p[1]
		}
		return Point{c[0] / float64(len(pts)), c[1] / float64(len(pts))}
	}
	var cx, cz float64
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		cross := pts[j][0]*pts[i][1] - pts[i][0]*pts[j][1]
		cx += (pts[j][0] + pts[i][0]) * cross
		cz += (pts[j][1] + pts[i][1]) * cross
	}
	return Point{cx / (6 * a), cz / (6 * a)}
}

// PolygonBounds is the bounding rectangle of the points.
func PolygonBounds(pts []Point) Rect {
	if len(pts) == 0 {
		return Rect{}
	}
	r := Rect{pts[0][0], pts[0][1], pts[0][0], pts[0][1]}
	for _, p := range pts[1:] {
		r.MinX, r.MaxX = math.Min(r.MinX, p[0]), math.Max(r.MaxX, p[0])
		r.MinZ, r.MaxZ = math.Min(r.MinZ, p[1]), math.Max(r.MaxZ, p[1])
	}
	return r
}

// SelfIntersection finds two non-adjacent edges that cross (edge i runs from
// point i to point i+1), and reports whether there are any. O(n²), which is
// nothing at footprint sizes.
func SelfIntersection(pts []Point) (int, int, bool) {
	n := len(pts)
	if n < 4 {
		return 0, 0, false
	}
	for i := 0; i < n; i++ {
		a1, a2 := pts[i], pts[(i+1)%n]
		for j := i + 1; j < n; j++ {
			// Adjacent edges share a corner and always "touch".
			if j == i+1 || (i == 0 && j == n-1) {
				continue
			}
			b1, b2 := pts[j], pts[(j+1)%n]
			if segmentsCross(a1, a2, b1, b2) {
				return i, j, true
			}
		}
	}
	return 0, 0, false
}

// segmentsCross reports whether segments p1p2 and q1q2 intersect (touching
// included).
func segmentsCross(p1, p2, q1, q2 Point) bool {
	o := func(a, b, c Point) float64 { return (b[0]-a[0])*(c[1]-a[1]) - (b[1]-a[1])*(c[0]-a[0]) }
	sgn := func(v float64) int {
		switch {
		case v > 1e-12:
			return 1
		case v < -1e-12:
			return -1
		}
		return 0
	}
	on := func(a, b, c Point) bool {
		return math.Min(a[0], b[0]) <= c[0] && c[0] <= math.Max(a[0], b[0]) &&
			math.Min(a[1], b[1]) <= c[1] && c[1] <= math.Max(a[1], b[1])
	}
	d1, d2 := sgn(o(q1, q2, p1)), sgn(o(q1, q2, p2))
	d3, d4 := sgn(o(p1, p2, q1)), sgn(o(p1, p2, q2))
	if d1 != d2 && d3 != d4 && d1 != 0 && d2 != 0 && d3 != 0 && d4 != 0 {
		return true
	}
	return (d1 == 0 && on(q1, q2, p1)) || (d2 == 0 && on(q1, q2, p2)) ||
		(d3 == 0 && on(p1, p2, q1)) || (d4 == 0 && on(p1, p2, q2))
}
