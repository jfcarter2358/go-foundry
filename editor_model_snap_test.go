package main

import (
	"foundry/internal/model"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// withGridSnap turns snapping on at the given step for the duration of a test.
func withGridSnap(t *testing.T, on bool, step float32) {
	t.Helper()
	prevOn, prevStep := Editor.SnapToGrid, Editor.StepSize
	Editor.SnapToGrid, Editor.StepSize = on, step
	t.Cleanup(func() { Editor.SnapToGrid, Editor.StepSize = prevOn, prevStep })
}

func TestAxisMovedLandsOnTheGridNotBesideIt(t *testing.T) {
	withGridSnap(t, true, 0.1)

	// A full step from an off-grid start must arrive at a grid line, which
	// means travelling less than a full step.
	got := axisMoved(mgl32.Vec3{0.03, 0, 0}, 0, 0.1)

	if math.Abs(float64(got.X()-0.1)) > 1e-5 {
		t.Errorf("moved to %v, want the grid line at 0.1", got.X())
	}
}

func TestAxisMovedIsInertWhenSnappingIsOff(t *testing.T) {
	withGridSnap(t, false, 0.1)

	got := axisMoved(mgl32.Vec3{0.03, 0, 0}, 0, 0.037)
	if math.Abs(float64(got.X()-0.067)) > 1e-6 {
		t.Errorf("x = %v, want the raw offset applied (0.067)", got.X())
	}
}

func TestSnappedDragLandsEveryVertexOnTheGrid(t *testing.T) {
	m := model.NewCubeDoc()
	// Push each corner off the grid by a different amount, so a snap applied
	// to the selection's centroid could not put them all back on it.
	for i := range m.Verts {
		m.Verts[i] = m.Verts[i].Add(mgl32.Vec3{float32(i) * 0.017, 0, 0})
	}
	withModelDoc(t, m)
	withGridSnap(t, true, 0.25)

	top := cubeTopFace(t, m)
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{top}

	modelNudgeSelection(0, 0.25)

	for _, vi := range m.Faces[top].Verts {
		steps := float64(m.Verts[vi].X()) / 0.25
		if math.Abs(steps-math.Round(steps)) > 1e-4 {
			t.Errorf("corner %d left at x=%.4f, which is not on the grid", vi, m.Verts[vi].X())
		}
	}
}

func TestNudgeMovesASelectionRigidlyOntoTheGrid(t *testing.T) {
	m := model.NewCubeDoc()
	// Shove the whole cube off the grid so snapping has something to correct.
	for i := range m.Verts {
		m.Verts[i] = m.Verts[i].Add(mgl32.Vec3{0, 0.03, 0})
	}
	withModelDoc(t, m)
	withGridSnap(t, true, 0.1)

	top := cubeTopFace(t, m)
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{top}
	before := append([]mgl32.Vec3(nil), m.Verts...)

	modelNudgeSelection(1, 0.1)

	// The face centre lands on a grid line...
	steps := float64(m.FaceCenter(top).Y()) / 0.1
	if math.Abs(steps-math.Round(steps)) > 1e-4 {
		t.Errorf("face centre at y=%v is not on the grid", m.FaceCenter(top).Y())
	}
	// ...and, because this face's corners all started at the same height, every
	// one of them moved by the same offset and the face kept its shape. (Corners
	// at *different* offsets are each snapped onto the grid separately, exactly
	// as the 3D view does — see TestSnappedDragLandsEveryVertexOnTheGrid.)
	want := m.Verts[m.Faces[top].Verts[0]].Sub(before[m.Faces[top].Verts[0]])
	for _, vi := range m.Faces[top].Verts {
		if got := m.Verts[vi].Sub(before[vi]); !vec3Close(got, want, 1e-5) {
			t.Errorf("corner %d moved by %v, want %v", vi, got, want)
		}
	}
	// Unselected corners stay put.
	for _, vi := range []int{0, 1, 2, 3} {
		if !vec3Close(m.Verts[vi], before[vi], 1e-6) {
			t.Errorf("unselected vertex %d moved to %v", vi, m.Verts[vi])
		}
	}
}

func TestNudgeWithoutSnappingMovesTheFullStep(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	withGridSnap(t, false, 0.1)

	modelSelection.Mode = modelSelVertex
	modelSelection.Verts = []int{0}
	before := m.Verts[0]

	modelNudgeSelection(1, 0.37)

	if got := m.Verts[0].Sub(before); !vec3Close(got, mgl32.Vec3{0, 0.37, 0}, 1e-6) {
		t.Errorf("moved by %v, want the exact step {0 0.37 0}", got)
	}
}

func TestNudgeInBoneModeMovesTheBoneAndItsPoints(t *testing.T) {
	m, bi := skinnedCube(t)
	withModelDoc(t, m)
	withGridSnap(t, false, 0.1)

	modelSelection.Mode = modelSelBone
	modelSelection.Bones = []int{bi}
	modelSelection.BoneJoint = modelBoneWhole
	beforeHead := m.Bones[bi].Head
	beforeVert := m.Verts[0]

	modelNudgeSelection(0, 0.25)

	if got := m.Bones[bi].Head.Sub(beforeHead); !vec3Close(got, mgl32.Vec3{0.25, 0, 0}, 1e-5) {
		t.Errorf("bone head moved by %v, want {0.25 0 0}", got)
	}
	if got := m.Verts[0].Sub(beforeVert); !vec3Close(got, mgl32.Vec3{0.25, 0, 0}, 1e-5) {
		t.Errorf("bound vertex moved by %v, want it carried along", got)
	}
}

func TestNudgeInBoneModeCanMoveJustTheTail(t *testing.T) {
	m, bi := skinnedCube(t)
	withModelDoc(t, m)
	withGridSnap(t, false, 0.1)

	modelSelection.Mode = modelSelBone
	modelSelection.Bones = []int{bi}
	modelSelection.BoneJoint = modelBoneTail
	head := m.Bones[bi].Head

	modelNudgeSelection(0, 0.25)

	if !vec3Close(m.Bones[bi].Head, head, 1e-6) {
		t.Errorf("head moved to %v while nudging the tail", m.Bones[bi].Head)
	}
	if got := m.Bones[bi].Tail.X(); math.Abs(float64(got-0.25)) > 1e-5 {
		t.Errorf("tail x = %v, want 0.25", got)
	}
}
