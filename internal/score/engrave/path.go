package engrave

// path.go — filled paths for backends that draw triangles.
//
// A PDF or the PNG rasteriser fills an item's path directly, curves and
// all. imgui draws triangles, so for it a path is flattened into polygons
// (Flatten) and each polygon cut into triangles by ear clipping
// (Triangulate). Beams are quads and ties crescents, so the polygons are
// small, simple and without holes; ear clipping is exact for those.

import (
	"math"

	"foundry/internal/score/glyph"
)

// Flatten turns a path into closed polygons, one per contour, its curves
// cut into straight pieces no further than tol from the curve.
func Flatten(segs []glyph.Segment, tol float64) [][]Point {
	if tol <= 0 {
		tol = 0.02
	}
	var out [][]Point
	var cur []Point
	flush := func() {
		if len(cur) >= 3 {
			out = append(out, cur)
		}
		cur = nil
	}
	at := Point{}
	for _, s := range segs {
		switch s.Op {
		case glyph.MoveTo:
			flush()
			at = s.P[0]
			cur = append(cur, at)
		case glyph.LineTo:
			at = s.P[0]
			cur = append(cur, at)
		case glyph.QuadTo:
			p0, p1, p2 := at, s.P[0], s.P[1]
			n := curvePieces(math.Hypot(p1.X-p0.X, p1.Y-p0.Y)+math.Hypot(p2.X-p1.X, p2.Y-p1.Y), tol)
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				u := 1 - t
				cur = append(cur, Point{X: u*u*p0.X + 2*u*t*p1.X + t*t*p2.X, Y: u*u*p0.Y + 2*u*t*p1.Y + t*t*p2.Y})
			}
			at = p2
		case glyph.CubeTo:
			p0, p1, p2, p3 := at, s.P[0], s.P[1], s.P[2]
			n := curvePieces(math.Hypot(p1.X-p0.X, p1.Y-p0.Y)+math.Hypot(p2.X-p1.X, p2.Y-p1.Y)+math.Hypot(p3.X-p2.X, p3.Y-p2.Y), tol)
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				u := 1 - t
				a, b, c, d := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
				cur = append(cur, Point{X: a*p0.X + b*p1.X + c*p2.X + d*p3.X, Y: a*p0.Y + b*p1.Y + c*p2.Y + d*p3.Y})
			}
			at = p3
		}
	}
	flush()
	// A contour that ends where it starts repeats its first point; drop it.
	for i, c := range out {
		if n := len(c); n > 3 && c[0] == c[n-1] {
			out[i] = c[:n-1]
		}
	}
	return out
}

// curvePieces is how many straight pieces a curve of a control-polygon
// length needs to stay within tol.
func curvePieces(length, tol float64) int {
	n := int(math.Ceil(math.Sqrt(length / tol / 2)))
	return min(max(n, 2), 64)
}

// Triangulate cuts a simple polygon (either winding) into triangles by ear
// clipping. A polygon it cannot cut (self-crossing) falls back to a fan,
// which is right for anything convex.
func Triangulate(poly []Point) [][3]Point {
	n := len(poly)
	if n < 3 {
		return nil
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	// Work counter-clockwise (in y-down coordinates: positive area).
	if signedArea(poly) < 0 {
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			idx[i], idx[j] = idx[j], idx[i]
		}
	}
	var out [][3]Point
	for guard := 0; len(idx) > 3 && guard < n*n; guard++ {
		cut := false
		for i := range idx {
			a, b, c := poly[idx[(i+len(idx)-1)%len(idx)]], poly[idx[i]], poly[idx[(i+1)%len(idx)]]
			if cross(a, b, c) <= 1e-12 {
				continue // reflex or flat
			}
			ear := true
			for _, k := range idx {
				p := poly[k]
				if p == a || p == b || p == c {
					continue
				}
				if inTriangle(p, a, b, c) {
					ear = false
					break
				}
			}
			if !ear {
				continue
			}
			out = append(out, [3]Point{a, b, c})
			idx = append(idx[:i], idx[i+1:]...)
			cut = true
			break
		}
		if !cut {
			// Degenerate: fan what is left.
			for i := 1; i+1 < len(idx); i++ {
				out = append(out, [3]Point{poly[idx[0]], poly[idx[i]], poly[idx[i+1]]})
			}
			return out
		}
	}
	if len(idx) == 3 {
		out = append(out, [3]Point{poly[idx[0]], poly[idx[1]], poly[idx[2]]})
	}
	return out
}

func signedArea(p []Point) float64 {
	a := 0.0
	for i := range p {
		q, r := p[i], p[(i+1)%len(p)]
		a += q.X*r.Y - r.X*q.Y
	}
	return a / 2
}

func cross(a, b, c Point) float64 { return (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X) }

func inTriangle(p, a, b, c Point) bool {
	return cross(a, b, p) >= 0 && cross(b, c, p) >= 0 && cross(c, a, p) >= 0
}

// TriangleArea is a triangle's area.
func TriangleArea(t [3]Point) float64 { return math.Abs(cross(t[0], t[1], t[2])) / 2 }

// PolygonArea is a simple polygon's area.
func PolygonArea(p []Point) float64 { return math.Abs(signedArea(p)) }
