package world

// surface_subdivide_test.go — subdividing a wall face that a neighbouring
// subdivide has already put a T-junction point on.
//
// The reported bug: quarter a wall face, then quarter each of those in turn.
// The first came out in four; its neighbours came out in five, and the one
// diagonally opposite in six. Each of those faces had grown one T-junction
// point per already-subdivided neighbour (the watertightness repair), and a fan
// from the raw ring made one wedge per ring point rather than one quad per
// corner.

import "testing"

// quadFaces counts how many of a mesh's faces have exactly four CORNERS —
// which, after a subdivide, is what "quartered" has to mean, since a child is
// allowed to carry a neighbour's T-junction point along one of its sides.
func cornerCount(m *SurfaceMesh, fi int) int {
	f := &m.Faces[fi]
	return len(m.surfaceCornerSpans(f))
}

func TestSubdividingAQuadMakesFourQuads(t *testing.T) {
	f := testWallFrame()
	m := SeedSurfaceMesh(f)
	out := m.SurfaceSubdivideFaces([]int{0})
	if len(out) != 4 {
		t.Fatalf("subdividing one quad made %d faces, want 4", len(out))
	}
	for _, fi := range out {
		if got := cornerCount(&m, fi); got != 4 {
			t.Errorf("child face %d has %d corners, want 4", fi, got)
		}
	}
}

// The regression. Every child must quarter into four, however many of its
// neighbours were subdivided before it.
func TestSubdividingEachChildInTurnStillMakesFour(t *testing.T) {
	f := testWallFrame()
	m := SeedSurfaceMesh(f)
	quarters := m.SurfaceSubdivideFaces([]int{0})
	if len(quarters) != 4 {
		t.Fatalf("first subdivide made %d faces, want 4", len(quarters))
	}

	for i, fi := range quarters {
		before := len(m.Faces)
		out := m.SurfaceSubdivideFaces([]int{fi})
		if len(out) != 4 {
			t.Errorf("quarter %d (face %d) subdivided into %d faces, want 4", i, fi, len(out))
		}
		if got := len(m.Faces) - before; got != 3 {
			t.Errorf("quarter %d added %d faces to the mesh, want 3 (four children, one in place)",
				i, got)
		}
		for _, ci := range out {
			if got := cornerCount(&m, ci); got != 4 {
				t.Errorf("quarter %d child %d has %d corners, want 4", i, ci, got)
			}
		}
	}
}

// A subdivide must not leave a crack: every point a face's edge passes through
// has to be in the ring of the face on the other side of that edge too. This is
// what the T-junction reuse in splitSurfaceFace buys — without it the second
// subdivide would put a fresh point beside the neighbour's and the two would
// come apart when either is dragged.
func TestSubdividedNeighboursStayWatertight(t *testing.T) {
	f := testWallFrame()
	m := SeedSurfaceMesh(f)
	quarters := m.SurfaceSubdivideFaces([]int{0})
	m.SurfaceSubdivideFaces([]int{quarters[0]})

	// Every undirected edge of every face, and which faces use it.
	users := map[surfaceEdge]int{}
	for fi := range m.Faces {
		vs := m.Faces[fi].Verts
		for i := range vs {
			users[makeSurfaceEdge(vs[i], vs[(i+1)%len(vs)])]++
		}
	}
	// An interior edge is walked by exactly two faces; a boundary edge by one.
	// Three or more means two faces met the same span at different points.
	for e, n := range users {
		if n > 2 {
			t.Errorf("edge %v is used by %d faces; the mesh is not a surface", e, n)
		}
	}

	// No duplicate points: reusing the neighbour's T-junction point rather than
	// making a new one at the same place is the whole mechanism.
	seen := map[[3]float32]int{}
	for i, p := range m.Points {
		k := [3]float32{p.U, p.V, p.Depth}
		if j, ok := seen[k]; ok {
			t.Errorf("points %d and %d are both at %v — a T-junction point was duplicated", j, i, k)
		}
		seen[k] = i
	}
}
