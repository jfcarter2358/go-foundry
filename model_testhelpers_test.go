package main

// model_testhelpers_test.go — helpers the editor's model tests share.
//
// A near-identical vec3Close lives in internal/model's own tests. Test helpers
// cannot cross a package boundary without becoming part of the package's API,
// and a four-line float comparison is not worth exporting for.

import (
	"testing"

	"foundry/internal/model"

	"github.com/go-gl/mathgl/mgl32"
)

// vec3Close reports whether two vectors agree to within eps on every axis.
func vec3Close(a, b mgl32.Vec3, eps float32) bool {
	for i := 0; i < 3; i++ {
		d := a[i] - b[i]
		if d < -eps || d > eps {
			return false
		}
	}
	return true
}

// cubeTopFace returns the index of the cube's +Y face. internal/model's own
// tests define the same three lines; a test helper cannot cross a package
// boundary without becoming part of that package's API.
func cubeTopFace(t *testing.T, m *model.ModelDoc) int {
	t.Helper()
	for fi := range m.Faces {
		if m.FaceNormal(fi).Y() > 0.9 {
			return fi
		}
	}
	t.Fatal("no +Y face on the cube")
	return -1
}

func boneChain(t *testing.T) (*model.ModelDoc, [3]int) {
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

// modelAllVerts is every vertex index in the document — the usual argument to
// AssignVertsToBone when a test wants one bone driving the whole mesh.
func modelAllVerts(m *model.ModelDoc) []int {
	out := make([]int, len(m.Verts))
	for i := range out {
		out[i] = i
	}
	return out
}

// skinnedCube returns a cube with one bone running up through it and every
// vertex bound rigidly to that bone.
func skinnedCube(t *testing.T) (*model.ModelDoc, int) {
	t.Helper()
	m := model.NewCubeDoc()
	bi := m.AddBone(mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{0, 0.5, 0})
	if bi < 0 {
		t.Fatal("AddBone refused")
	}
	m.AssignVertsToBone(modelAllVerts(m), bi, 1)
	return m, bi
}

// vecNear reports whether two vectors agree within tol. internal/world's tests
// carry the same three lines — see the note on vec3Close.
func vecNear(a, b mgl32.Vec3, tol float32) bool {
	d := a.Sub(b)
	return mgl32.Abs(d.X()) <= tol && mgl32.Abs(d.Y()) <= tol && mgl32.Abs(d.Z()) <= tol
}

// nearlyEqual reports whether two scalars agree within tol. internal/world's
// tests carry the same three lines — see the note on vec3Close.
func nearlyEqual(a, b, tol float32) bool {
	d := a - b
	return d <= tol && d >= -tol
}
