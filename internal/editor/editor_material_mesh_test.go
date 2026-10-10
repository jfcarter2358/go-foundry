package editor

// editor_material_mesh_test.go — the material editor's preview meshes.
//
// These are built by hand rather than loaded, so nothing but a test checks
// them, and both ways they can be wrong are invisible in a screenshot taken
// from one angle: a sphere whose longitude only sweeps half a turn is a bowl
// that looks like a sphere until you orbit it, and a surface wound inside out
// lights from the wrong side rather than disappearing (the engine draws with
// backface culling off, and fragment.glsl flips its facet normal by
// gl_FrontFacing, so the error survives all the way to "it looks a bit odd").
//
// The winding is the thing worth pinning. Geometry.Load welds its vertex
// normals from the triangle order, and these meshes are smooth-shaded, so an
// inward winding is an inward normal on every vertex.

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// meshTri is one triangle's three corners.
func meshTri(verts [][]float32, indices []int, i int) (a, b, c mgl32.Vec3) {
	at := func(k int) mgl32.Vec3 {
		v := verts[indices[k]]
		return mgl32.Vec3{v[0], v[1], v[2]}
	}
	return at(i), at(i + 1), at(i + 2)
}

// triNormal is the facet normal of a triangle wound a -> b -> c.
func triNormal(a, b, c mgl32.Vec3) mgl32.Vec3 {
	return b.Sub(a).Cross(c.Sub(a))
}

func TestPreviewMeshesAreWellFormed(t *testing.T) {
	for _, m := range []struct {
		name  string
		build func() ([][]float32, []int)
	}{
		{"sphere", materialSphereMesh},
		{"cube", materialCubeMesh},
		{"plane", materialPlaneMesh},
	} {
		verts, indices := m.build()
		if len(verts) == 0 || len(indices) == 0 {
			t.Errorf("%s: empty", m.name)
			continue
		}
		if len(indices)%3 != 0 {
			t.Errorf("%s: %d indices is not whole triangles", m.name, len(indices))
		}
		for i, v := range verts {
			if len(v) != 5 {
				t.Fatalf("%s: vertex %d has %d floats, want the [X,Y,Z,U,V] record", m.name, i, len(v))
			}
		}
		for _, idx := range indices {
			if idx < 0 || idx >= len(verts) {
				t.Fatalf("%s: index %d out of range (%d vertices)", m.name, idx, len(verts))
			}
		}
	}
}

func TestTheSphereIsAWholeSphere(t *testing.T) {
	// The bug this exists for: sweeping longitude over pi instead of 2*pi
	// makes a half sphere that reads as a whole one from the front.
	verts, _ := materialSphereMesh()

	// Every octant has to have vertices WELL inside it. The threshold is what
	// makes this mean anything: counting "not positive" as negative lets the
	// ring of vertices at z = 0 stand in for the whole z < 0 half, and a bowl
	// passes. A vertex only votes for an octant when it is clear of all three
	// planes.
	const clear = 0.3
	var octant [8]int
	for _, v := range verts {
		if math.Abs(float64(v[0])) < clear ||
			math.Abs(float64(v[1])) < clear ||
			math.Abs(float64(v[2])) < clear {
			continue
		}
		i := 0
		if v[0] > 0 {
			i |= 1
		}
		if v[1] > 0 {
			i |= 2
		}
		if v[2] > 0 {
			i |= 4
		}
		octant[i]++
	}
	for i, n := range octant {
		if n == 0 {
			t.Errorf("octant %d has no vertices — the sphere does not close", i)
		}
	}

	// And every vertex is on the unit sphere, which is what says the radius
	// and the trigonometry agree.
	for _, v := range verts {
		r := mgl32.Vec3{v[0], v[1], v[2]}.Len()
		if math.Abs(float64(r)-1) > 1e-4 {
			t.Fatalf("a vertex is at radius %v, want 1", r)
		}
	}
}

func TestEveryPreviewMeshTriangleFacesOutward(t *testing.T) {
	// A closed solid centred on the origin: the facet normal of a correctly
	// wound triangle points away from the centre, so its dot with the
	// triangle's own centroid is positive.
	for _, m := range []struct {
		name  string
		build func() ([][]float32, []int)
	}{
		{"sphere", materialSphereMesh},
		{"cube", materialCubeMesh},
	} {
		verts, indices := m.build()
		bad := 0
		for i := 0; i < len(indices); i += 3 {
			a, b, c := meshTri(verts, indices, i)
			n := triNormal(a, b, c)
			if n.Len() < 1e-9 {
				continue // a degenerate triangle at a pole says nothing
			}
			centroid := a.Add(b).Add(c).Mul(1.0 / 3.0)
			if n.Dot(centroid) <= 0 {
				bad++
			}
		}
		if bad > 0 {
			t.Errorf("%s: %d triangles are wound inside out", m.name, bad)
		}
	}
}

func TestThePlaneFacesUp(t *testing.T) {
	// The plane is not closed, so it is checked against the direction it is
	// meant to face rather than against a centre: it is the top of a floor,
	// and wound the other way it lights from underneath.
	verts, indices := materialPlaneMesh()
	for i := 0; i < len(indices); i += 3 {
		a, b, c := meshTri(verts, indices, i)
		n := triNormal(a, b, c)
		if n.Y() <= 0 {
			t.Errorf("triangle %d faces down (normal %v)", i/3, n)
		}
	}
}

func TestPreviewMeshUVsSpanTheirTexture(t *testing.T) {
	// A mesh whose UVs never leave one texel shows nothing of a material's
	// tiling, its scroll or its sheet.
	for _, m := range []struct {
		name    string
		build   func() ([][]float32, []int)
		wantMax float32
	}{
		{"sphere", materialSphereMesh, 1},
		{"cube", materialCubeMesh, 1},
		{"plane", materialPlaneMesh, 1},
	} {
		verts, _ := m.build()
		var maxU, maxV float32
		for _, v := range verts {
			if v[3] > maxU {
				maxU = v[3]
			}
			if v[4] > maxV {
				maxV = v[4]
			}
		}
		if maxU < m.wantMax || maxV < m.wantMax {
			t.Errorf("%s: UVs reach only %v x %v", m.name, maxU, maxV)
		}
	}
}

func TestTheSwatchStartsStill(t *testing.T) {
	// A surface that will not hold still is harder to judge than one that
	// will, so the spin is opt-in.
	if materialSpin {
		t.Error("the material preview should not start spinning")
	}
}
