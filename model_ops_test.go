package main

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// cubeTopFace returns the index of NewCubeDoc's +Y face.
func cubeTopFace(t *testing.T, m *ModelDoc) int {
	t.Helper()
	for fi := range m.Faces {
		if m.FaceNormal(fi).Y() > 0.9 {
			return fi
		}
	}
	t.Fatal("no +Y face on the cube")
	return -1
}

// twoQuadStrip returns two coplanar quads in the y=0 plane sharing edge {1,4},
// both wound so their normals point +Y.
func twoQuadStrip() *ModelDoc {
	uv := func() []mgl32.Vec2 { return make([]mgl32.Vec2, 4) }
	m := &ModelDoc{
		Verts: []mgl32.Vec3{
			{0, 0, 0}, {1, 0, 0}, {2, 0, 0}, {2, 0, 1}, {1, 0, 1}, {0, 0, 1},
		},
		Faces: []ModelFace{
			{Verts: []int{0, 5, 4, 1}, UVs: uv(), Texture: "default"},
			{Verts: []int{1, 4, 3, 2}, UVs: uv(), Texture: "default"},
		},
	}
	return m
}

func TestExtrudeSingleFace(t *testing.T) {
	m := NewCubeDoc()
	top := cubeTopFace(t, m)
	before := m.FaceNormal(top)

	got, _ := m.ExtrudeFaces([]int{top}, 1)

	if len(got) != 1 || got[0] != top {
		t.Fatalf("ExtrudeFaces returned %v, want the cap face %d back", got, top)
	}
	if len(m.Verts) != 12 {
		t.Errorf("verts = %d, want 12 (8 + 4 duplicated)", len(m.Verts))
	}
	if len(m.Faces) != 10 {
		t.Errorf("faces = %d, want 10 (6 + 4 sides)", len(m.Faces))
	}
	if after := m.FaceNormal(top); !vec3Close(before, after, 1e-5) {
		t.Errorf("cap normal changed: %v -> %v", before, after)
	}
	// The cap must have travelled exactly dist along its normal.
	for _, v := range m.Faces[top].Verts {
		if got := m.Verts[v].Y(); got != 1.5 {
			t.Errorf("cap vertex %d at y=%v, want 1.5", v, got)
		}
	}
	// And the result must still be a closed solid.
	for _, e := range m.Edges() {
		if n := len(m.FacesAtEdge(e)); n != 2 {
			t.Errorf("edge %v shared by %d faces after extrude, want 2", e, n)
		}
	}
}

func TestExtrudeZeroDistanceCreatesTopologyOnly(t *testing.T) {
	// The interactive flow is "E then drag": extrude by 0 to build the sides,
	// then move the cap. The topology must exist even though nothing moved.
	m := NewCubeDoc()
	top := cubeTopFace(t, m)
	m.ExtrudeFaces([]int{top}, 0)

	if len(m.Faces) != 10 {
		t.Errorf("faces = %d, want 10", len(m.Faces))
	}
	for _, v := range m.Faces[top].Verts {
		if m.Verts[v].Y() != 0.5 {
			t.Errorf("cap moved on a zero-distance extrude: %v", m.Verts[v])
		}
	}
}

func TestExtrudeCoplanarFacesSkipsInteriorEdge(t *testing.T) {
	m := twoQuadStrip()
	m.ExtrudeFaces([]int{0, 1}, 1)

	// 6 verts duplicated; 6 boundary edges walled in, and the shared edge {1,4}
	// must NOT produce a seventh — that quad would be buried inside the solid.
	if len(m.Verts) != 12 {
		t.Errorf("verts = %d, want 12", len(m.Verts))
	}
	if len(m.Faces) != 8 {
		t.Fatalf("faces = %d, want 8 (2 caps + 6 boundary sides)", len(m.Faces))
	}
	for fi := 2; fi < len(m.Faces); fi++ {
		has := map[int]bool{}
		for _, v := range m.Faces[fi].Verts {
			has[v] = true
		}
		if has[1] && has[4] {
			t.Errorf("face %d walls in the interior edge {1,4}: %v", fi, m.Faces[fi].Verts)
		}
	}
}

func TestExtrudeOpposingFacesRefused(t *testing.T) {
	// Two faces whose normals cancel have no single cap direction. Refusing is
	// the only safe answer; moving by a zero-length normal would silently do
	// nothing while still rewiring the mesh.
	m := NewCubeDoc()
	var top, bottom int
	for fi := range m.Faces {
		if m.FaceNormal(fi).Y() > 0.9 {
			top = fi
		}
		if m.FaceNormal(fi).Y() < -0.9 {
			bottom = fi
		}
	}
	if got, _ := m.ExtrudeFaces([]int{top, bottom}, 1); got != nil {
		t.Errorf("ExtrudeFaces on opposing faces returned %v, want nil", got)
	}
	if len(m.Verts) != 8 || len(m.Faces) != 6 {
		t.Errorf("mesh mutated by a refused extrude: %d verts, %d faces", len(m.Verts), len(m.Faces))
	}
}

func TestExtrudeSideInheritsSourceTexture(t *testing.T) {
	m := NewCubeDoc()
	top := cubeTopFace(t, m)
	m.SetFaceTexture([]int{top}, "brick")
	m.ExtrudeFaces([]int{top}, 1)

	for fi := 6; fi < len(m.Faces); fi++ {
		if m.Faces[fi].Texture != "brick" {
			t.Errorf("side face %d texture = %q, want brick", fi, m.Faces[fi].Texture)
		}
	}
}

func TestSubdivideQuadFaceRepairsNeighbours(t *testing.T) {
	m := NewCubeDoc()
	origVerts := append([]mgl32.Vec3(nil), m.Verts...)
	top := cubeTopFace(t, m)

	got := m.SubdivideFaces([]int{top})

	if len(got) != 4 {
		t.Fatalf("quad subdivided into %d faces, want 4", len(got))
	}
	// 4 edge midpoints + 1 centroid.
	if len(m.Verts) != 13 {
		t.Errorf("verts = %d, want 13", len(m.Verts))
	}
	if len(m.Faces) != 9 {
		t.Errorf("faces = %d, want 9 (5 untouched + 4 children)", len(m.Faces))
	}
	// Midpoint subdivision must not move the surface.
	for i, p := range origVerts {
		if !vec3Close(p, m.Verts[i], 1e-6) {
			t.Errorf("vertex %d moved: %v -> %v", i, p, m.Verts[i])
		}
	}
	// The four side faces each shared one edge with the top and must have
	// absorbed its midpoint, or the mesh has a T-junction crack.
	fiveVertFaces := 0
	for _, f := range m.Faces {
		if len(f.Verts) == 5 {
			fiveVertFaces++
		}
	}
	if fiveVertFaces != 4 {
		t.Errorf("%d neighbours repaired, want 4", fiveVertFaces)
	}
	// Still watertight.
	for _, e := range m.Edges() {
		if n := len(m.FacesAtEdge(e)); n != 2 {
			t.Errorf("edge %v shared by %d faces after subdivide, want 2", e, n)
		}
	}
}

func TestSubdivideTriangleGivesFourTriangles(t *testing.T) {
	m := &ModelDoc{
		Verts: []mgl32.Vec3{{0, 0, 0}, {1, 0, 0}, {0, 0, 1}},
		Faces: []ModelFace{{
			Verts: []int{0, 1, 2},
			UVs:   []mgl32.Vec2{{0, 0}, {1, 0}, {0, 1}},
		}},
	}
	got := m.SubdivideFaces([]int{0})

	if len(got) != 4 {
		t.Fatalf("triangle subdivided into %d faces, want 4", len(got))
	}
	// Three edge midpoints, no centre vertex.
	if len(m.Verts) != 6 {
		t.Errorf("verts = %d, want 6", len(m.Verts))
	}
	for i, f := range m.Faces {
		if len(f.Verts) != 3 {
			t.Errorf("child %d has %d verts, want a triangle", i, len(f.Verts))
		}
	}
}

func TestSubdivideInterpolatesUVs(t *testing.T) {
	m := &ModelDoc{
		Verts: []mgl32.Vec3{{0, 0, 0}, {1, 0, 0}, {1, 0, 1}, {0, 0, 1}},
		Faces: []ModelFace{{
			Verts:   []int{0, 1, 2, 3},
			UVs:     []mgl32.Vec2{{0, 0}, {1, 0}, {1, 1}, {0, 1}},
			Texture: "brick",
		}},
	}
	m.SubdivideFaces([]int{0})

	for i, f := range m.Faces {
		if f.Texture != "brick" {
			t.Errorf("child %d lost the parent texture", i)
		}
		if len(f.UVs) != len(f.Verts) {
			t.Fatalf("child %d has %d UVs for %d verts", i, len(f.UVs), len(f.Verts))
		}
		for _, uv := range f.UVs {
			if uv.X() < 0 || uv.X() > 1 || uv.Y() < 0 || uv.Y() > 1 {
				t.Errorf("child %d UV %v escaped the parent's 0..1 range", i, uv)
			}
		}
	}
}

func TestCatmullClarkCube(t *testing.T) {
	m := NewCubeDoc()
	min0, max0 := m.Bounds()

	m.CatmullClark()

	// 8 vertex points + 12 edge points + 6 face points.
	if len(m.Verts) != 26 {
		t.Errorf("verts = %d, want 26", len(m.Verts))
	}
	// Every n-gon becomes n quads: 6 faces x 4 corners.
	if len(m.Faces) != 24 {
		t.Errorf("faces = %d, want 24", len(m.Faces))
	}
	for i, f := range m.Faces {
		if len(f.Verts) != 4 {
			t.Errorf("face %d has %d verts, Catmull-Clark must produce quads", i, len(f.Verts))
		}
	}
	// Subdivision pulls towards the limit surface, so the result is strictly
	// inside the control cage — never outside it.
	for i, p := range m.Verts {
		for ax := 0; ax < 3; ax++ {
			if p[ax] < min0[ax]-1e-5 || p[ax] > max0[ax]+1e-5 {
				t.Errorf("vertex %d %v escaped the original bounds %v..%v", i, p, min0, max0)
			}
		}
	}
	// Still a closed solid.
	for _, e := range m.Edges() {
		if n := len(m.FacesAtEdge(e)); n != 2 {
			t.Errorf("edge %v shared by %d faces, want 2", e, n)
		}
	}
}

func TestCatmullClarkKeepsOpenMeshBoundary(t *testing.T) {
	// A lone quad is all boundary. The crease rule must keep it roughly where
	// it is rather than collapsing it towards its own centre.
	m := &ModelDoc{
		Verts: []mgl32.Vec3{{0, 0, 0}, {1, 0, 0}, {1, 0, 1}, {0, 0, 1}},
		Faces: []ModelFace{{Verts: []int{0, 1, 2, 3}, UVs: make([]mgl32.Vec2, 4)}},
	}
	m.CatmullClark()

	if len(m.Faces) != 4 {
		t.Errorf("faces = %d, want 4", len(m.Faces))
	}
	// 4 vertex points + 4 edge points + 1 face point.
	if len(m.Verts) != 9 {
		t.Errorf("verts = %d, want 9", len(m.Verts))
	}
	// Boundary corner rule is 3/4 P + 1/8 of each neighbouring boundary
	// midpoint: 0.75*(0,0,0) + 0.125*(0.5,0,0) + 0.125*(0,0,0.5). Pulled in,
	// but nowhere near the centre.
	want := mgl32.Vec3{0.0625, 0, 0.0625}
	if !vec3Close(m.Verts[0], want, 1e-5) {
		t.Errorf("boundary corner at %v, want %v", m.Verts[0], want)
	}
}

func TestDeleteFacesCompactsVertices(t *testing.T) {
	m := NewCubeDoc()
	top := cubeTopFace(t, m)
	m.DeleteFaces([]int{top})

	if len(m.Faces) != 5 {
		t.Errorf("faces = %d, want 5", len(m.Faces))
	}
	// The top's four corners are still used by the side faces, so nothing is
	// orphaned here and all 8 vertices survive.
	if len(m.Verts) != 8 {
		t.Errorf("verts = %d, want 8", len(m.Verts))
	}
	for fi, f := range m.Faces {
		for _, v := range f.Verts {
			if v < 0 || v >= len(m.Verts) {
				t.Fatalf("face %d references vertex %d after compaction (%d verts)", fi, v, len(m.Verts))
			}
		}
	}
}

func TestDeleteFacesDropsOrphanedVertices(t *testing.T) {
	// Two disjoint triangles; deleting one must drop its three vertices and
	// remap the survivor's indices down.
	m := &ModelDoc{
		Verts: []mgl32.Vec3{
			{0, 0, 0}, {1, 0, 0}, {0, 0, 1},
			{5, 0, 0}, {6, 0, 0}, {5, 0, 1},
		},
		Faces: []ModelFace{
			{Verts: []int{0, 1, 2}, UVs: make([]mgl32.Vec2, 3)},
			{Verts: []int{3, 4, 5}, UVs: make([]mgl32.Vec2, 3)},
		},
	}
	m.DeleteFaces([]int{0})

	if len(m.Verts) != 3 {
		t.Fatalf("verts = %d, want 3 after orphan compaction", len(m.Verts))
	}
	if got := m.Faces[0].Verts; got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Errorf("survivor indices = %v, want [0 1 2]", got)
	}
	if !vec3Close(m.Verts[0], mgl32.Vec3{5, 0, 0}, 1e-6) {
		t.Errorf("survivor vertex 0 = %v, want (5,0,0)", m.Verts[0])
	}
}

func TestMoveVertsAndSelectionHelpers(t *testing.T) {
	m := NewCubeDoc()
	top := cubeTopFace(t, m)

	verts := m.VertsOfFaces([]int{top})
	if len(verts) != 4 {
		t.Fatalf("VertsOfFaces returned %d verts, want 4", len(verts))
	}
	m.MoveVerts(verts, mgl32.Vec3{0, 2, 0})
	for _, v := range verts {
		if m.Verts[v].Y() != 2.5 {
			t.Errorf("vertex %d at y=%v, want 2.5", v, m.Verts[v].Y())
		}
	}

	edges := []edgeKey{makeEdgeKey(verts[0], verts[1])}
	if got := m.VertsOfEdges(edges); len(got) != 2 {
		t.Errorf("VertsOfEdges returned %v, want 2 endpoints", got)
	}
}

// cubeFaceAlong returns the cube face whose normal points along axis dir.
func cubeFaceAlong(t *testing.T, m *ModelDoc, axis int, dir float32) int {
	t.Helper()
	for fi := range m.Faces {
		if m.FaceNormal(fi)[axis]*dir > 0.9 {
			return fi
		}
	}
	t.Fatalf("no face along axis %d dir %v", axis, dir)
	return -1
}

// checkWatertight fails if any edge is not shared by exactly two faces.
func checkWatertight(t *testing.T, m *ModelDoc) {
	t.Helper()
	for _, e := range m.Edges() {
		if n := len(m.FacesAtEdge(e)); n != 2 {
			t.Errorf("edge %v shared by %d faces, want 2", e, n)
		}
	}
}

// A face whose neighbour was subdivided first carries a T-junction point
// mid-edge, so its loop is a pentagon even though it is still a quad. It must
// still split into four, reusing that point rather than fanning five wedges off
// a centre vertex — the diagonal-edged split this used to produce.
func TestSubdivideRepairedNeighbourStillSplitsInFour(t *testing.T) {
	m := NewCubeDoc()
	m.SubdivideFaces([]int{cubeTopFace(t, m)})

	side := cubeFaceAlong(t, m, 0, 1)
	if got := len(m.Faces[side].Verts); got != 5 {
		t.Fatalf("side face has %d verts, want the 5 of a repaired neighbour", got)
	}
	tJunction := m.Faces[side].Verts
	vertsBefore := len(m.Verts)

	got := m.SubdivideFaces([]int{side})

	if len(got) != 4 {
		t.Fatalf("repaired quad subdivided into %d faces, want 4", len(got))
	}
	for _, fi := range got {
		if n := len(m.Faces[fi].Verts); n != 4 {
			t.Errorf("child %d has %d verts, want a quad", fi, n)
		}
	}
	// Three fresh midpoints plus a centre: the fourth edge splits at the
	// T-junction point that is already there.
	if n := len(m.Verts) - vertsBefore; n != 4 {
		t.Errorf("added %d verts, want 4 (3 midpoints + centre)", n)
	}
	// That existing point must actually be used, or the two sides of the edge
	// disagree about where it splits.
	used := false
	for _, fi := range got {
		for _, v := range m.Faces[fi].Verts {
			for _, tj := range tJunction {
				if v == tj && v >= 8 { // 8.. are the previous subdivide's verts
					used = true
				}
			}
		}
	}
	if !used {
		t.Error("children ignored the neighbour's T-junction point")
	}
	checkWatertight(t, m)
}

// The reported case: a cube face flanked by two already-subdivided faces has a
// six-entry loop and used to split into six pieces with diagonal edges.
func TestSubdivideFaceWithTwoTJunctionsSplitsInFour(t *testing.T) {
	m := NewCubeDoc()
	m.SubdivideFaces([]int{cubeFaceAlong(t, m, 1, 1)})
	m.SubdivideFaces([]int{cubeFaceAlong(t, m, 1, -1)})

	side := cubeFaceAlong(t, m, 0, 1)
	if got := len(m.Faces[side].Verts); got != 6 {
		t.Fatalf("side face has %d verts, want 6 after two neighbours split", got)
	}
	origVerts := append([]mgl32.Vec3(nil), m.Verts...)

	got := m.SubdivideFaces([]int{side})

	if len(got) != 4 {
		t.Fatalf("doubly repaired quad subdivided into %d faces, want 4", len(got))
	}
	for _, fi := range got {
		f := m.Faces[fi]
		if len(f.Verts) != 4 {
			t.Errorf("child %d has %d verts, want a quad", fi, len(f.Verts))
		}
		// Every child edge must run along an axis; a diagonal is the bug.
		for i := range f.Verts {
			d := m.Verts[f.Verts[(i+1)%len(f.Verts)]].Sub(m.Verts[f.Verts[i]])
			axes := 0
			for a := 0; a < 3; a++ {
				if d[a] > 1e-5 || d[a] < -1e-5 {
					axes++
				}
			}
			if axes > 1 {
				t.Errorf("child %d edge %d runs diagonally: %v", fi, i, d)
			}
		}
	}
	for i, p := range origVerts {
		if !vec3Close(p, m.Verts[i], 1e-6) {
			t.Errorf("vertex %d moved: %v -> %v", i, p, m.Verts[i])
		}
	}
	checkWatertight(t, m)
}

// Repeatedly subdividing adjacent faces must not leave the mesh cracked, which
// is the failure mode reusing T-junction points has to avoid.
func TestSubdivideChainStaysWatertight(t *testing.T) {
	m := NewCubeDoc()
	for _, spec := range []struct {
		axis int
		dir  float32
	}{{1, 1}, {0, 1}, {2, 1}, {1, -1}, {0, -1}, {2, -1}} {
		m.SubdivideFaces([]int{cubeFaceAlong(t, m, spec.axis, spec.dir)})
		checkWatertight(t, m)
	}
}

// A genuine n-gon has no collinear points, so it must still fan into n quads:
// the corner detection must not collapse real corners.
func TestSubdividePentagonFansIntoFive(t *testing.T) {
	m := &ModelDoc{
		Verts: []mgl32.Vec3{
			{1, 0, 0}, {0.31, 0, 0.95}, {-0.81, 0, 0.59}, {-0.81, 0, -0.59}, {0.31, 0, -0.95},
		},
		Faces: []ModelFace{{Verts: []int{0, 1, 2, 3, 4}, UVs: make([]mgl32.Vec2, 5)}},
	}
	got := m.SubdivideFaces([]int{0})
	if len(got) != 5 {
		t.Fatalf("pentagon subdivided into %d faces, want 5", len(got))
	}
}
