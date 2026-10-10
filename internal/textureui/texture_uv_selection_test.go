package textureui

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// A face's UV loop can arrive in either winding and can be concave; the fill
// must cover exactly its area either way, indexing the loop as given.
func TestUVLoopTrianglesCoversConcaveLoopInEitherWinding(t *testing.T) {
	// An L shape, area 3.
	ccw := []mgl32.Vec2{{0, 0}, {2, 0}, {2, 1}, {1, 1}, {1, 2}, {0, 2}}
	cw := make([]mgl32.Vec2, len(ccw))
	for i := range ccw {
		cw[i] = ccw[len(ccw)-1-i]
	}
	for name, loop := range map[string][]mgl32.Vec2{"ccw": ccw, "cw": cw} {
		tris := uvLoopTriangles(loop)
		if len(tris) != len(loop)-2 {
			t.Errorf("%s: %d triangles, want %d", name, len(tris), len(loop)-2)
		}
		var area float32
		for _, tr := range tris {
			a, b, c := loop[tr[0]], loop[tr[1]], loop[tr[2]]
			x := (b.X()-a.X())*(c.Y()-a.Y()) - (c.X()-a.X())*(b.Y()-a.Y())
			if x < 0 {
				x = -x
			}
			area += x / 2
		}
		if area < 2.999 || area > 3.001 {
			t.Errorf("%s: triangles cover %v, want 3", name, area)
		}
	}
	if uvLoopTriangles([]mgl32.Vec2{{0, 0}, {1, 1}}) != nil {
		t.Error("a two-corner loop was filled")
	}
}
