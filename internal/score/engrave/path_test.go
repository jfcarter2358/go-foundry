package engrave

import (
	"math"
	"testing"
)

func TestTriangulateTiesAndBeams(t *testing.T) {
	cases := map[string][]Point{}
	// A tie's crescent is concave; a beam's quad convex.
	for i, up := range []bool{true, false} {
		polys := Flatten(crescent(Point{X: 1, Y: 2}, Point{X: 7, Y: 2.5}, up, 0.8, 0.22, 0.1), 0.01)
		if len(polys) != 1 {
			t.Fatalf("crescent: %d contours", len(polys))
		}
		cases[[]string{"tie up", "tie down"}[i]] = polys[0]
	}
	beam := polyItem(RoleBeam, NoRef, Point{X: 0, Y: 0}, Point{X: 3, Y: -0.75}, Point{X: 3, Y: -0.25}, Point{X: 0, Y: 0.5})
	cases["beam"] = Flatten(beam.Path, 0.01)[0]
	cases["L"] = []Point{{X: 0, Y: 0}, {X: 2, Y: 0}, {X: 2, Y: 1}, {X: 1, Y: 1}, {X: 1, Y: 3}, {X: 0, Y: 3}}
	for name, poly := range cases {
		tris := Triangulate(poly)
		if len(tris) != len(poly)-2 {
			t.Errorf("%s: %d triangles for %d points", name, len(tris), len(poly))
		}
		sum := 0.0
		for _, tr := range tris {
			sum += TriangleArea(tr)
		}
		if a := PolygonArea(poly); math.Abs(sum-a) > 1e-9*math.Max(1, a) {
			t.Errorf("%s: triangles cover %g of %g", name, sum, a)
		}
	}
	// The crescent's thickness at its middle is the font's tie midpoint.
	poly := cases["tie up"]
	minY, maxY := math.Inf(1), math.Inf(-1)
	for _, p := range poly {
		if math.Abs(p.X-4) < 0.2 {
			minY, maxY = math.Min(minY, p.Y), math.Max(maxY, p.Y)
		}
	}
	if th := maxY - minY; th < 0.15 || th > 0.35 {
		t.Errorf("tie thickness at the middle %g", th)
	}
}
