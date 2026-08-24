package modeltest

// Package modeltest holds ModelDoc fixtures that more than one package's tests
// build on.
//
// An ordinary package rather than _test.go helpers: the model editor, the
// script runtime and internal/model itself all want the same rigged cube, and a
// test helper does not cross a package boundary. Everything here is
// deterministic and GL-free.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/model"
)

// AllVerts is every vertex index in the document — the usual argument to
// AssignVertsToBone when a test wants one bone driving the whole mesh.
func AllVerts(m *model.ModelDoc) []int {
	out := make([]int, len(m.Verts))
	for i := range out {
		out[i] = i
	}
	return out
}

// SkinnedCube returns a cube with one bone running up through it and every
// vertex bound rigidly to that bone.
func SkinnedCube(t *testing.T) (*model.ModelDoc, int) {
	t.Helper()
	m := model.NewCubeDoc()
	bi := m.AddBone(mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{0, 0.5, 0})
	if bi < 0 {
		t.Fatal("AddBone refused")
	}
	m.AssignVertsToBone(AllVerts(m), bi, 1)
	return m, bi
}

// BoneChain returns a cube with three bones extruded end to end, and their
// indices in root-to-tip order.
func BoneChain(t *testing.T) (*model.ModelDoc, [3]int) {
	t.Helper()
	m := model.NewCubeDoc()
	var idx [3]int
	idx[0] = m.AddBone(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})
	if idx[0] < 0 {
		t.Fatal("AddBone refused a unit bone")
	}
	idx[1] = m.ExtrudeBone(idx[0])
	idx[2] = m.ExtrudeBone(idx[1])
	if idx[1] < 0 || idx[2] < 0 {
		t.Fatalf("ExtrudeBone failed: %v", idx)
	}
	return m, idx
}

// CubeTopFace returns the index of the cube's +Y face.
func CubeTopFace(t *testing.T, m *model.ModelDoc) int {
	t.Helper()
	for fi := range m.Faces {
		if m.FaceNormal(fi).Y() > 0.9 {
			return fi
		}
	}
	t.Fatal("no +Y face on the cube")
	return -1
}
