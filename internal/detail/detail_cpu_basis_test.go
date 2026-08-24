package detail

import (
	"foundry/internal/gfx"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// detail_cpu_basis_test.go — the CPU port of the displacement shader.
//
// Split from internal/world's detail_test.go, which covers the DetailSpec
// *document*: these exercise detail_cpu.go, which is a step above it.

// TestDispNormalIsFaceConstant checks that a NON-planar quad — a floor whose
// four corners have independent heights, split into two triangles — reports one
// normal for the whole face rather than each triangle's own. The displacement
// shaders push along this single normal so the two triangles stay welded along
// their shared diagonal instead of tearing open.
func TestDispNormalIsFaceConstant(t *testing.T) {
	// Quad (0,0)-(2,0)-(2,2)-(0,2) with corner heights 0,0,1,0 — the fan splits
	// it on the (0,0)-(2,2) diagonal into two triangles at different tilts.
	v := func(x, y, z float32) []float32 { return []float32{x, y, z, 0, 0} }
	var verts []float32
	tri := func(a, b, c []float32) {
		verts = append(verts, a...)
		verts = append(verts, b...)
		verts = append(verts, c...)
	}
	p00, p20, p22, p02 := v(0, 0, 0), v(2, 0, 0), v(2, 1, 2), v(0, 0, 2)
	tri(p00, p20, p22)
	tri(p00, p22, p02)

	// The two triangles really are non-coplanar, or the test proves nothing.
	triNrm := func(a, b, c []float32) mgl32.Vec3 {
		pa, pb, pc := Vtx(a, 0), Vtx(b, 0), Vtx(c, 0)
		return pb.Sub(pa).Cross(pc.Sub(pa)).Normalize()
	}
	n1, n2 := triNrm(p00, p20, p22), triNrm(p00, p22, p02)
	if n1.Sub(n2).Len() < 1e-3 {
		t.Fatalf("test quad is planar (%v vs %v); it cannot exercise the seam", n1, n2)
	}

	geo := &gfx.Geometry{Vertices: verts}
	n := DispNormalFor(geo)
	if math.Abs(float64(n.Len()-1)) > 1e-4 {
		t.Fatalf("dispNormal must be unit length, got %v (len %.4f)", n, n.Len())
	}
	// It is the face's average plane, so it matches neither triangle exactly and
	// sits between them.
	if n.Sub(n1).Len() < 1e-4 || n.Sub(n2).Len() < 1e-4 {
		t.Fatalf("dispNormal %v is a single triangle's normal, not the face average", n)
	}
	if n.Dot(n1) < 0.9 || n.Dot(n2) < 0.9 {
		t.Fatalf("dispNormal %v should lie between the triangle normals %v / %v", n, n1, n2)
	}
	// Cached repeat must be identical, not merely close — the shader relies on
	// every patch of the face getting bit-identical uniforms.
	if again := DispNormalFor(geo); again != n {
		t.Fatalf("dispNormal not stable across calls: %v then %v", n, again)
	}
}

// TestComputeFaceBasis checks the plane basis + bounds for a flat floor quad in
// the XZ plane (y = 1), spanning X:[0,2], Z:[0,3], wound CCW seen from above.
func TestComputeFaceBasis(t *testing.T) {
	// Triangle-fan quad (0,0)-(2,0)-(2,3)-(0,3) at y=1; 5 floats/vertex [x,y,z,u,v].
	v := func(x, z float32) []float32 { return []float32{x, 1, z, 0, 0} }
	var verts []float32
	tri := func(a, b, c []float32) {
		verts = append(verts, a...)
		verts = append(verts, b...)
		verts = append(verts, c...)
	}
	p00, p20, p23, p03 := v(0, 0), v(2, 0), v(2, 3), v(0, 3)
	tri(p00, p20, p23)
	tri(p00, p23, p03)

	b := ComputeFaceBasis(verts)
	if !b.Ok {
		t.Fatal("ComputeFaceBasis failed on a valid quad")
	}
	// Normal should be vertical (|n.y| ~ 1).
	if math.Abs(float64(b.N.Y())) < 0.99 {
		t.Fatalf("expected vertical normal, got %v", b.N)
	}
	// Bounding rectangle should cover the 2x3 extent (order of u/v may swap).
	uv := []float64{float64(b.UMax), float64(b.VMax)}
	got2, got3 := false, false
	for _, e := range uv {
		if math.Abs(e-2) < 1e-3 {
			got2 = true
		}
		if math.Abs(e-3) < 1e-3 {
			got3 = true
		}
	}
	if !got2 || !got3 {
		t.Fatalf("expected extents {2,3}, got uMax=%.3f vMax=%.3f", b.UMax, b.VMax)
	}
}

// TestComputeBoundaryEdges verifies a triangle-fan quad yields exactly its 4
// perimeter edges (the shared diagonal is interior and excluded), so the edge
// taper pins the true polygon boundary.
func TestComputeBoundaryEdges(t *testing.T) {
	v := func(x, z float32) []float32 { return []float32{x, 1, z, 0, 0} }
	var verts []float32
	tri := func(a, b, c []float32) {
		verts = append(verts, a...)
		verts = append(verts, b...)
		verts = append(verts, c...)
	}
	p00, p20, p23, p03 := v(0, 0), v(2, 0), v(2, 3), v(0, 3)
	tri(p00, p20, p23) // shares diagonal p00-p23
	tri(p00, p23, p03)

	edges := ComputeBoundaryEdges(verts)
	if len(edges) != 4 {
		t.Fatalf("expected 4 boundary edges for a quad fan, got %d", len(edges))
	}
	// The diagonal p00-p23 must NOT appear among the boundary edges.
	c00, c23 := mgl32.Vec3{0, 1, 0}, mgl32.Vec3{2, 1, 3}
	for _, e := range edges {
		onDiag := func(p mgl32.Vec3) bool { return approxV(p, c00) || approxV(p, c23) }
		if onDiag(e[0]) && onDiag(e[1]) {
			t.Fatalf("interior diagonal p00-p23 wrongly reported as boundary: %v", e)
		}
	}
}

func approxV(a, b mgl32.Vec3) bool {
	return math.Abs(float64(a.X()-b.X())) < 1e-3 &&
		math.Abs(float64(a.Y()-b.Y())) < 1e-3 &&
		math.Abs(float64(a.Z()-b.Z())) < 1e-3
}
