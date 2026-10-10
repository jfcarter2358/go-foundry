package model

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// splitQuads is two unit quads side by side that touch along x=1 but keep
// their own copies of the shared corners — the shape of the generated dropship
// and of any mesh imported with its seams split.
func splitQuads() *ModelDoc {
	return &ModelDoc{
		Verts: []mgl32.Vec3{
			{0, 0, 0}, {1, 0, 0}, {1, 1, 0}, {0, 1, 0}, // left quad
			{1, 0, 0}, {2, 0, 0}, {2, 1, 0}, {1, 1, 0}, // right quad; 4 twins 1, 7 twins 2
		},
		Faces: []ModelFace{
			{Verts: []int{0, 1, 2, 3}},
			{Verts: []int{4, 5, 6, 7}},
		},
	}
}

func TestWithCoincidentFollowsSplitCopies(t *testing.T) {
	m := splitQuads()
	got := m.WithCoincident([]int{1})
	if len(got) != 2 || got[0] != 1 || got[1] != 4 {
		t.Fatalf("WithCoincident([1]) = %v, want [1 4]", got)
	}
	// Moving that set keeps the two quads meeting at the corner.
	for _, v := range got {
		m.SetVertPos(v, m.Verts[v].Add(mgl32.Vec3{0, -0.3, 0}))
	}
	if m.Verts[1] != m.Verts[4] {
		t.Errorf("the seam opened: %v vs %v", m.Verts[1], m.Verts[4])
	}
	if m.Verts[2] != (mgl32.Vec3{1, 1, 0}) || m.Verts[7] != (mgl32.Vec3{1, 1, 0}) {
		t.Errorf("an unselected corner moved: %v, %v", m.Verts[2], m.Verts[7])
	}
}

func TestWithCoincidentToleratesFloatNoise(t *testing.T) {
	m := splitQuads()
	m.Verts[4] = m.Verts[4].Add(mgl32.Vec3{2e-5, -1e-5, 0})
	if got := m.WithCoincident([]int{1}); len(got) != 2 {
		t.Errorf("a twin 2e-5 away was not followed: %v", got)
	}
	m.Verts[4] = mgl32.Vec3{1.01, 0, 0}
	if got := m.WithCoincident([]int{1}); len(got) != 1 {
		t.Errorf("a vertex 1 cm away was pulled along: %v", got)
	}
}

func TestWithCoincidentLeavesAZeroDepthExtrudeBase(t *testing.T) {
	// E then drag: the cap sits on its base until it is dragged, and the base
	// must stay put when it is.
	m := NewCubeDoc()
	top := cubeTopFace(t, m)
	caps, _ := m.ExtrudeFaces([]int{top}, 0)
	capVerts := m.VertsOfFaces(caps)
	got := m.WithCoincident(capVerts)
	if len(got) != len(capVerts) {
		t.Errorf("WithCoincident pulled the extrude base in: cap %v, got %v", capVerts, got)
	}
}

func TestWithCoincidentIgnoresBadIndices(t *testing.T) {
	m := splitQuads()
	if got := m.WithCoincident([]int{-1, 99}); got != nil {
		t.Errorf("WithCoincident of out-of-range indices = %v, want nil", got)
	}
}

func TestWeldCoincidentJoinsSplitQuads(t *testing.T) {
	m := splitQuads()
	welded, edit := m.WeldCoincident()
	if welded != 2 {
		t.Fatalf("welded = %d, want 2", welded)
	}
	if !edit.Changed() {
		t.Fatal("the weld reported no change")
	}
	if len(m.Verts) != 6 || len(m.Faces) != 2 {
		t.Fatalf("after weld: %d verts, %d faces; want 6 and 2", len(m.Verts), len(m.Faces))
	}
	// The two quads now share their middle edge.
	shared := 0
	for _, e := range m.Edges() {
		if len(m.FacesAtEdge(e)) == 2 {
			shared++
		}
	}
	if shared != 1 {
		t.Errorf("%d shared edges after weld, want 1", shared)
	}
	if again, _ := m.WeldCoincident(); again != 0 {
		t.Errorf("a second weld found %d more points", again)
	}
}

func TestWeldCoincidentDropsACollapsedExtrudeSide(t *testing.T) {
	// A zero-depth extrude welded shut is the cube it started as: the cap
	// lands back on the base and the zero-height sides go.
	m := NewCubeDoc()
	m.ExtrudeFaces([]int{cubeTopFace(t, m)}, 0)
	if welded, _ := m.WeldCoincident(); welded != 4 {
		t.Fatalf("welded = %d, want 4", welded)
	}
	if len(m.Verts) != 8 {
		t.Errorf("verts = %d, want 8", len(m.Verts))
	}
	for _, e := range m.Edges() {
		if e.A == e.B {
			t.Errorf("a zero-length edge survived: %v", e)
		}
	}
}

func TestWeldCoincidentBlendsWeights(t *testing.T) {
	m := splitQuads()
	m.Bones = []ModelBone{{Name: "a"}, {Name: "b"}}
	m.EnsureWeights()
	m.Weights[1] = []ModelVertexWeight{{Bone: 0, Weight: 1}}
	m.Weights[4] = []ModelVertexWeight{{Bone: 1, Weight: 1}}
	_, edit := m.WeldCoincident()
	ws := m.Weights[edit.Vert(1)]
	if len(ws) != 2 {
		t.Fatalf("welded point weights = %v, want both bones", ws)
	}
}

func TestWeldCoincidentLeavesACleanMeshAlone(t *testing.T) {
	m := NewCubeDoc()
	if welded, edit := m.WeldCoincident(); welded != 0 || edit.Changed() {
		t.Errorf("weld on a clean cube: %d welded, changed=%v", welded, edit.Changed())
	}
}
