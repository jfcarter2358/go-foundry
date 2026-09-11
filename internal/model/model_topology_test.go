package model

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// gridPlane returns an n-by-n sheet of unit quads in the y=0 plane, every face
// wound so its normal points +Y. Face (x, z) is at index z*n + x.
func gridPlane(n int) *ModelDoc {
	m := &ModelDoc{}
	idx := func(x, z int) int { return z*(n+1) + x }
	for z := 0; z <= n; z++ {
		for x := 0; x <= n; x++ {
			m.Verts = append(m.Verts, mgl32.Vec3{float32(x), 0, float32(z)})
		}
	}
	for z := 0; z < n; z++ {
		for x := 0; x < n; x++ {
			m.Faces = append(m.Faces, ModelFace{
				Verts: []int{idx(x, z), idx(x, z+1), idx(x+1, z+1), idx(x+1, z)},
				UVs:   make([]mgl32.Vec2, 4),
			})
		}
	}
	return m
}

// facesAlong returns every face whose normal points along axis dir.
func facesAlong(m *ModelDoc, axis int, dir float32) []int {
	var out []int
	for fi := range m.Faces {
		if m.FaceNormal(fi)[axis]*dir > 0.9 {
			out = append(out, fi)
		}
	}
	return out
}

// A subdivided face joined back up is one face again: the centre point only its
// quarters used is gone, the midpoints stay on its outline (the neighbours
// still use them), and the mesh is as closed as it was.
func TestJoinSubdividedFaceBackIntoOne(t *testing.T) {
	m := NewCubeDoc()
	quarters := m.SubdivideFaces([]int{cubeTopFace(t, m)})
	if len(m.Verts) != 13 {
		t.Fatalf("subdivide left %d verts, want 13 (8 corners + 4 midpoints + a centre)", len(m.Verts))
	}

	joined, refused, edit := m.JoinFaces(quarters, nil)
	if refused != 0 || len(joined) != 1 {
		t.Fatalf("JoinFaces = %v joined, %d refused; want one face and no refusals", joined, refused)
	}
	if !edit.Changed() {
		t.Fatal("the edit reports no change")
	}
	if len(m.Faces) != 6 {
		t.Errorf("%d faces, want the cube's 6", len(m.Faces))
	}
	if len(m.Verts) != 12 {
		t.Errorf("%d verts, want 12 — the centre only the quarters used should be gone", len(m.Verts))
	}
	top := joined[0]
	if n := len(m.Faces[top].Verts); n != 8 {
		t.Errorf("the joined face has %d corners, want 8: four corners and four midpoints", n)
	}
	if got := m.FaceNormal(top); !vec3Close(got, mgl32.Vec3{0, 1, 0}, 1e-5) {
		t.Errorf("the joined face faces %v, want +Y — the outline was walked the wrong way", got)
	}
	if len(edit.FaceSrc) != len(m.Faces) || edit.FaceSrc[top] != quarters[0] {
		t.Errorf("FaceSrc = %v; the joined face should come from the first quarter listed, %d",
			edit.FaceSrc, quarters[0])
	}
	checkWatertight(t, m)
}

// And dissolving the midpoints left on its outline gets back the cube: each is
// a point on a line — two edges meet there once the quarters are one face.
func TestDissolveMidpointsRestoresTheCube(t *testing.T) {
	m := NewCubeDoc()
	quarters := m.SubdivideFaces([]int{cubeTopFace(t, m)})
	joined, _, _ := m.JoinFaces(quarters, nil)

	var mids []int
	for _, v := range m.Faces[joined[0]].Verts {
		if _, _, ok := m.LineNeighbours(v); ok {
			mids = append(mids, v)
		}
	}
	if len(mids) != 4 {
		t.Fatalf("%d of the joined face's corners are points on a line, want its 4 midpoints", len(mids))
	}

	dissolved, edit := m.DissolveVerts(mids)
	if len(dissolved) != 4 {
		t.Fatalf("dissolved %v, want all four midpoints", dissolved)
	}
	if len(m.Verts) != 8 || len(m.Faces) != 6 {
		t.Errorf("%d verts, %d faces; want the cube's 8 and 6", len(m.Verts), len(m.Faces))
	}
	for fi := range m.Faces {
		if n := len(m.Faces[fi].Verts); n != 4 {
			t.Errorf("face %d has %d corners, want a quad", fi, n)
		}
		if n := len(m.Faces[fi].UVs); n != 4 {
			t.Errorf("face %d has %d UVs for 4 corners", fi, n)
		}
	}
	for _, v := range mids {
		if edit.Vert(v) != -1 {
			t.Errorf("dissolved point %d still maps to %d", v, edit.Vert(v))
		}
	}
	checkWatertight(t, m)
}

// A cube corner is where three edges meet. Taking it out would change which
// faces exist rather than how finely a line is cut, so it is left alone.
func TestDissolveLeavesACornerAlone(t *testing.T) {
	m := NewCubeDoc()
	dissolved, edit := m.DissolveVerts([]int{0, 6})
	if len(dissolved) != 0 || edit.Changed() {
		t.Fatalf("dissolved %v off a cube; its corners are not points on a line", dissolved)
	}
	if len(m.Verts) != 8 || len(m.Faces) != 6 {
		t.Errorf("the cube changed: %d verts, %d faces", len(m.Verts), len(m.Faces))
	}
}

// Several points along one edge dissolve all the way to its ends, because each
// is asked about against the loop as the ones before it left it.
func TestDissolveARunOfPointsAlongOneEdge(t *testing.T) {
	m := &ModelDoc{
		Verts: []mgl32.Vec3{{0, 0, 0}, {0, 0, 1}, {1, 0, 1}, {1, 0, 0}, {0.33, 0, 0}, {0.66, 0, 0}},
		Faces: []ModelFace{{Verts: []int{0, 1, 2, 3, 5, 4}}},
	}
	dissolved, edit := m.DissolveVerts([]int{4, 5})
	if len(dissolved) != 2 {
		t.Fatalf("dissolved %v, want both points on the edge", dissolved)
	}
	if got := m.Faces[0].Verts; len(got) != 4 {
		t.Errorf("the face is %v, want a quad", got)
	}
	if len(m.Verts) != 4 || edit.Vert(3) != 3 {
		t.Errorf("%d verts and corner 3 at %d, want 4 verts with the corners unmoved", len(m.Verts), edit.Vert(3))
	}
}

// Two quads side by side join into one hexagon. No point is interior to the
// pair — every one is on the outline — so none is removed.
func TestJoinTwoQuads(t *testing.T) {
	m := twoQuadStrip()
	joined, refused, edit := m.JoinFaces([]int{1, 0}, nil)
	if len(joined) != 1 || refused != 0 {
		t.Fatalf("JoinFaces = %v, %d refused", joined, refused)
	}
	if len(m.Faces) != 1 || len(m.Faces[0].Verts) != 6 {
		t.Fatalf("faces = %+v, want one six-cornered face", m.Faces)
	}
	if len(m.Verts) != 6 || edit.VertRemap != nil {
		t.Errorf("%d verts, remap %v: nothing should have been removed", len(m.Verts), edit.VertRemap)
	}
	if edit.FaceSrc[0] != 1 {
		t.Errorf("the joined face comes from %d, want 1 — the face listed first", edit.FaceSrc[0])
	}
}

// accept is how a brush refuses an outline that is not a brush face. A refused
// group leaves the document exactly as it was.
func TestJoinHonoursAccept(t *testing.T) {
	m := twoQuadStrip()
	joined, refused, edit := m.JoinFaces([]int{0, 1}, func([]mgl32.Vec3) bool { return false })
	if len(joined) != 0 || refused != 1 || edit.Changed() {
		t.Fatalf("JoinFaces = %v, %d refused, changed %v; want the one group refused", joined, refused, edit.Changed())
	}
	if len(m.Faces) != 2 {
		t.Errorf("%d faces after a refused join, want the 2 it started with", len(m.Faces))
	}
}

// A ring of faces has two outlines, and a face can only have one.
func TestJoinRefusesARing(t *testing.T) {
	m := gridPlane(3)
	ring := []int{0, 1, 2, 3, 5, 6, 7, 8} // every face but the middle one
	joined, refused, _ := m.JoinFaces(ring, nil)
	if len(joined) != 0 || refused != 1 {
		t.Fatalf("JoinFaces on a ring = %v joined, %d refused; want it refused", joined, refused)
	}
	if len(m.Faces) != 9 {
		t.Errorf("%d faces, want the grid's 9 untouched", len(m.Faces))
	}
}

// Faces that only touch at a corner share no edge, so they are two groups of
// one, and a group of one has nothing to join.
func TestJoinFacesTouchingAtACornerIsNotAJoin(t *testing.T) {
	m := gridPlane(3)
	joined, refused, edit := m.JoinFaces([]int{0, 4}, nil)
	if len(joined) != 0 || refused != 0 || edit.Changed() {
		t.Errorf("JoinFaces = %v, %d refused, changed %v; want nothing at all", joined, refused, edit.Changed())
	}
}

// Two separate groups in one call both join.
func TestJoinTwoGroupsAtOnce(t *testing.T) {
	m := gridPlane(3)
	joined, refused, _ := m.JoinFaces([]int{0, 1, 7, 8}, nil)
	if len(joined) != 2 || refused != 0 {
		t.Fatalf("JoinFaces = %v, %d refused; want two joined faces", joined, refused)
	}
	if len(m.Faces) != 7 {
		t.Errorf("%d faces, want 9 - 4 + 2", len(m.Faces))
	}
}

// A joined face keeps its corners' UVs when every face in it had them, and
// states none rather than zeros when one did not.
func TestJoinCarriesUVsOnlyWhenEveryFaceHadThem(t *testing.T) {
	m := twoQuadStrip()
	for fi := range m.Faces {
		for i := range m.Faces[fi].UVs {
			m.Faces[fi].UVs[i] = m.Verts[m.Faces[fi].Verts[i]].Vec2()
		}
	}
	m.JoinFaces([]int{0, 1}, nil)
	f := m.Faces[0]
	if len(f.UVs) != len(f.Verts) {
		t.Fatalf("%d UVs for %d corners", len(f.UVs), len(f.Verts))
	}
	for i, v := range f.Verts {
		if f.UVs[i] != m.Verts[v].Vec2() {
			t.Errorf("corner %d (vertex %d) has UV %v, want %v", i, v, f.UVs[i], m.Verts[v].Vec2())
		}
	}

	m = twoQuadStrip()
	m.Faces[1].UVs = nil
	m.JoinFaces([]int{0, 1}, nil)
	if m.Faces[0].UVs != nil {
		t.Errorf("UVs %v invented for a face half of which had none", m.Faces[0].UVs)
	}
}

// Merging the two ends of a cube edge collapses that edge: the two faces along
// it become triangles, the point lands where it was asked to, and the mesh is
// still closed.
func TestMergeCollapsesAnEdge(t *testing.T) {
	m := NewCubeDoc()
	at := m.VertsCentroid([]int{4, 5})
	kept, edit := m.MergeVerts([]int{4, 5}, at)
	if kept < 0 {
		t.Fatal("MergeVerts refused two valid points")
	}
	if len(m.Verts) != 7 {
		t.Errorf("%d verts, want 7", len(m.Verts))
	}
	if m.Verts[kept] != at {
		t.Errorf("merged point at %v, want %v", m.Verts[kept], at)
	}
	if edit.Vert(5) != -1 || edit.Vert(4) != kept {
		t.Errorf("remap: 4 -> %d, 5 -> %d; want 4 -> %d and 5 removed", edit.Vert(4), edit.Vert(5), kept)
	}
	tris := 0
	for fi := range m.Faces {
		if len(m.Faces[fi].Verts) == 3 {
			tris++
		}
	}
	if len(m.Faces) != 6 || tris != 2 {
		t.Errorf("%d faces, %d triangles; want 6 faces, the two along the edge now triangles", len(m.Faces), tris)
	}
	checkWatertight(t, m)
}

// Merging opposite corners of a quad pinches it into two slivers with no area,
// and both go; its neighbour just has a corner renumbered.
func TestMergeAcrossAFaceRemovesIt(t *testing.T) {
	m := twoQuadStrip() // faces {0,5,4,1} and {1,4,3,2}
	kept, edit := m.MergeVerts([]int{5, 1}, m.Verts[5])
	if kept < 0 {
		t.Fatal("MergeVerts refused")
	}
	if len(m.Faces) != 1 {
		t.Fatalf("%d faces, want only the neighbour left", len(m.Faces))
	}
	if edit.FaceSrc[0] != 1 {
		t.Errorf("the surviving face came from %d, want 1", edit.FaceSrc[0])
	}
	if n := len(m.Faces[0].Verts); n != 4 {
		t.Errorf("the neighbour has %d corners, want its 4", n)
	}
	for _, v := range m.Faces[0].Verts {
		if v == edit.Vert(1) && m.Verts[v] != (mgl32.Vec3{0, 0, 1}) {
			t.Errorf("the merged point is at %v, want where 5 was", m.Verts[v])
		}
	}
}

// The survivor is bound to every bone the merged points were.
func TestMergeBlendsWeights(t *testing.T) {
	m := twoQuadStrip()
	m.EnsureWeights()
	m.Weights[2] = []ModelVertexWeight{{Bone: 0, Weight: 1}}
	m.Weights[3] = []ModelVertexWeight{{Bone: 1, Weight: 1}}
	kept, _ := m.MergeVerts([]int{2, 3}, m.VertsCentroid([]int{2, 3}))
	if len(m.Weights) != len(m.Verts) {
		t.Fatalf("%d weight entries for %d verts", len(m.Weights), len(m.Verts))
	}
	if w0, w1 := m.WeightOf(kept, 0), m.WeightOf(kept, 1); w0 != 0.5 || w1 != 0.5 {
		t.Errorf("merged weights %v / %v, want an even split", w0, w1)
	}
}

// One point is not a merge.
func TestMergeNeedsTwoPoints(t *testing.T) {
	m := NewCubeDoc()
	if kept, edit := m.MergeVerts([]int{3, 3, -1, 99}, mgl32.Vec3{}); kept != -1 || edit.Changed() {
		t.Errorf("MergeVerts on one valid point = %d, changed %v", kept, edit.Changed())
	}
}
