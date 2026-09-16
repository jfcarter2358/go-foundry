package collision

// concave_brush_test.go — a concave (step-shaped) solid brush resolves
// correctly without an author ever splitting it by hand: a body rests on the
// tower's top, rests on the slab's tread beside it, and is left alone over
// the shape's own missing corner — the one region a naive "GJK against the
// brush's own concave vertex list" would get wrong, since the convex hull of
// those vertices covers the notch too.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// orientedQuad appends a rectangular face to m from four corners in any
// planar order, picking the winding that makes the face's normal point
// toward outward.
func orientedQuad(m *world.BrushMesh, outward mgl32.Vec3, pts [4]mgl32.Vec3) {
	n := pts[1].Sub(pts[0]).Cross(pts[2].Sub(pts[0]))
	order := pts
	if n.Dot(outward) < 0 {
		order = [4]mgl32.Vec3{pts[0], pts[3], pts[2], pts[1]}
	}
	m.Faces = append(m.Faces, world.NewBrushFace([]int{
		vertIndex(m, order[0]), vertIndex(m, order[1]), vertIndex(m, order[2]), vertIndex(m, order[3]),
	}, "concrete"))
}

func vertIndex(m *world.BrushMesh, p mgl32.Vec3) int {
	for i, v := range m.Verts {
		if v.Sub(p).Len() < 1e-4 {
			return i
		}
	}
	m.Verts = append(m.Verts, p)
	return len(m.Verts) - 1
}

// stepShapedBrush is the same L/step solid brush_decompose_test.go uses in
// the world package: a wide low slab (x:[0,2], y:[0,1]) with a narrower tall
// tower stacked on its left half (x:[0,1], y:[1,2]), depth z:[0,1] throughout.
// One reflex edge, every face a convex rectangle.
func stepShapedBrush(id string) world.Brush {
	var m world.BrushMesh
	p := func(x, y, z float32) mgl32.Vec3 { return mgl32.Vec3{x, y, z} }

	orientedQuad(&m, mgl32.Vec3{0, -1, 0}, [4]mgl32.Vec3{p(0, 0, 0), p(2, 0, 0), p(2, 0, 1), p(0, 0, 1)})
	orientedQuad(&m, mgl32.Vec3{0, 1, 0}, [4]mgl32.Vec3{p(0, 2, 0), p(1, 2, 0), p(1, 2, 1), p(0, 2, 1)})
	orientedQuad(&m, mgl32.Vec3{0, 1, 0}, [4]mgl32.Vec3{p(1, 1, 0), p(2, 1, 0), p(2, 1, 1), p(1, 1, 1)})
	orientedQuad(&m, mgl32.Vec3{1, 0, 0}, [4]mgl32.Vec3{p(1, 1, 0), p(1, 2, 0), p(1, 2, 1), p(1, 1, 1)})
	orientedQuad(&m, mgl32.Vec3{-1, 0, 0}, [4]mgl32.Vec3{p(0, 0, 0), p(0, 2, 0), p(0, 2, 1), p(0, 0, 1)})
	orientedQuad(&m, mgl32.Vec3{1, 0, 0}, [4]mgl32.Vec3{p(2, 0, 0), p(2, 1, 0), p(2, 1, 1), p(2, 0, 1)})
	orientedQuad(&m, mgl32.Vec3{0, 0, -1}, [4]mgl32.Vec3{p(0, 0, 0), p(2, 0, 0), p(2, 1, 0), p(0, 1, 0)})
	orientedQuad(&m, mgl32.Vec3{0, 0, -1}, [4]mgl32.Vec3{p(0, 1, 0), p(1, 1, 0), p(1, 2, 0), p(0, 2, 0)})
	orientedQuad(&m, mgl32.Vec3{0, 0, 1}, [4]mgl32.Vec3{p(0, 0, 1), p(2, 0, 1), p(2, 1, 1), p(0, 1, 1)})
	orientedQuad(&m, mgl32.Vec3{0, 0, 1}, [4]mgl32.Vec3{p(0, 1, 1), p(1, 1, 1), p(1, 2, 1), p(0, 2, 1)})

	return world.Brush{ID: id, Solid: true, BrushMesh: m}
}

func stepShapedChunk() *world.Chunk {
	b := stepShapedBrush("step")
	if b.IsConvex() {
		panic("stepShapedBrush is meant to be concave — fixture is wrong")
	}
	ch := &world.Chunk{ID: "c", Brushes: []world.Brush{b}}
	ch.RecomputeBounds()
	return ch
}

// A body dropped just above the tower's top rests there — not on the slab's
// lower tread a metre below, which is what colliding against the concave
// solid's own vertex convex hull (ignoring the notch) would produce, since
// that hull's "top" at x<1 is the tower's y=2 regardless of where the
// resolver actually stops the body.
func TestConcaveBrushRestsOnTowerTop(t *testing.T) {
	ch := stepShapedChunk()
	const r, h = 0.3, 0.4
	above := mgl32.Vec3{0.5, 2.3, 0.5} // centred over the tower, x:[0,1]
	out, hitFloor, _ := pushOutOfChunkBrushes(ch, above, r, h)
	if !hitFloor {
		t.Fatal("body over the tower did not come to rest on anything")
	}
	// A few thousandths above depth: the resolver deliberately pushes past
	// the exact penetration depth by separationEpsilon to clear EPA's own
	// convergence tolerance (see pushOutOfChunkBrushes).
	if want := float32(2 + h); !near32(out.Y(), want, 3e-3) {
		t.Errorf("body over the tower rested at y=%v, want y=%v (the tower's top, y=2, plus the body's half-height)", out.Y(), want)
	}
}

// A body dropped over the slab's own exposed tread (x:[1,2], where the tower
// does not reach) rests a full unit lower than over the tower.
func TestConcaveBrushRestsOnSlabTread(t *testing.T) {
	ch := stepShapedChunk()
	const r, h = 0.3, 0.4
	above := mgl32.Vec3{1.5, 1.3, 0.5} // over the exposed tread, x:[1,2]
	out, hitFloor, _ := pushOutOfChunkBrushes(ch, above, r, h)
	if !hitFloor {
		t.Fatal("body over the tread did not come to rest on anything")
	}
	if want := float32(1 + h); !near32(out.Y(), want, 3e-3) {
		t.Errorf("body over the tread rested at y=%v, want y=%v (the tread's top, y=1, plus the body's half-height)", out.Y(), want)
	}
}

// The notch is the whole point: air to the right of the tower and above the
// tread (x:[1,2], y:[1,2]) is NOT part of the solid, and a body placed there
// — clear of both the tower's side and the tread below it — must be left
// alone rather than pushed out of what a convex-hull-of-the-verts shortcut
// would wrongly read as solid.
func TestConcaveBrushLeavesTheNotchOpen(t *testing.T) {
	ch := stepShapedChunk()
	const r, h = 0.15, 0.15
	inNotch := mgl32.Vec3{1.5, 1.5, 0.5}
	out, hitFloor, _ := pushOutOfChunkBrushes(ch, inNotch, r, h)
	if !vecNear(out, inNotch, 1e-4) {
		t.Errorf("a body in the shape's own notch was pushed to %v — the notch is being treated as solid", out)
	}
	if hitFloor {
		t.Error("a body floating in the open notch should not report a floor contact")
	}
}

func near32(a, b, tol float32) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}
