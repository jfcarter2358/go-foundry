package nanite

import "testing"

// tetra is a closed tetrahedron, wound outward, with vertex 0 split into two
// pool entries at the same point (a UV seam): the test matches by position,
// so the seam must not open it.
func tetra() *Mesh {
	p := [][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {0, 0, 1}, {0, 0, 0}}
	m := &Mesh{}
	for i, v := range p {
		m.Vertices = append(m.Vertices, v[0], v[1], v[2], float32(i), 0)
	}
	// Each face counter-clockwise seen from outside. Vertex 4 is vertex 0's
	// seam twin.
	m.Indices = []uint32{
		0, 2, 1, // z = 0 plane, normal -Z
		4, 1, 3, // y = 0 plane, normal -Y
		0, 3, 2, // x = 0 plane, normal -X
		1, 2, 3, // the slanted face, normal +(1,1,1)
	}
	return m
}

func TestAClosedMeshIsClosedAcrossItsSeams(t *testing.T) {
	if !tetra().Closed() {
		t.Fatal("a closed tetrahedron with a seam-split corner reads as open")
	}
}

func TestAnOpenOrInconsistentMeshIsNot(t *testing.T) {
	open := tetra()
	open.Indices = open.Indices[:9]
	if open.Closed() {
		t.Error("a tetrahedron missing a face reads as closed")
	}

	flipped := tetra()
	flipped.Indices[10], flipped.Indices[11] = flipped.Indices[11], flipped.Indices[10]
	if flipped.Closed() {
		t.Error("a tetrahedron with one face wound inward reads as closed")
	}

	// A fin: a fifth triangle on an existing edge makes it three-way.
	fin := tetra()
	fin.Vertices = append(fin.Vertices, 2, 2, 2, 0, 0)
	fin.Indices = append(fin.Indices, 1, 2, 5)
	if fin.Closed() {
		t.Error("a mesh with an edge shared three ways reads as closed")
	}

	if (&Mesh{}).Closed() {
		t.Error("an empty mesh reads as closed")
	}
}

func TestADegenerateTriangleDoesNotOpenAMesh(t *testing.T) {
	m := tetra()
	m.Indices = append(m.Indices, 0, 4, 1) // corners 0 and 4 are one point
	if !m.Closed() {
		t.Fatal("a degenerate triangle opened a closed mesh")
	}
}
