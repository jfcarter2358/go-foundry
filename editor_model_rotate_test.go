package main

import (
	"foundry/internal/geom"
	"foundry/internal/model"
	"foundry/internal/modeltest"
	"foundry/internal/uiwin"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// withModelViewport stands up a viewport and camera so picking and the gizmos
// can be driven without a GL context — modelViewport reads only W and H.
func withModelViewport(t *testing.T) {
	t.Helper()
	prevWin, prevCam, prevTool, prevWire := modelEditorWindow, modelCam, modelGizmoTool, modelWireframe
	modelEditorWindow = &uiwin.UIWindow{W: 1280, H: 800}
	modelCam = modelCamera{Dist: 4, Yaw: mgl32.DegToRad(35), Pitch: mgl32.DegToRad(-25)}
	modelInvalidateVisibility()
	t.Cleanup(func() {
		modelEditorWindow, modelCam = prevWin, prevCam
		modelGizmoTool, modelWireframe = prevTool, prevWire
		modelGizmoMouseUp()
		modelInvalidateVisibility()
	})
}

// ringMouse returns a screen point on the given rotation ring.
func ringMouse(t *testing.T, axis int, turns float64) (int32, int32) {
	t.Helper()
	origin, ok := modelGizmoOrigin()
	if !ok {
		t.Fatal("no gizmo origin")
	}
	w, h, _ := modelViewport()
	u, v := modelRingBasis(gizmoAxes[axis])
	p := modelRingPoint(origin, u, v, modelGizmoArmLength(origin), turns*2*math.Pi)
	sp, ok := projectToScreenIn(modelProjView(), p, w, h)
	if !ok {
		t.Fatal("ring point is off screen")
	}
	return int32(sp.X()), int32(sp.Y())
}

// grabRing starts a drag on one ring, away from the six points where the three
// rings cross each other and picking is genuinely ambiguous.
func grabRing(t *testing.T, axis int) {
	t.Helper()
	mx, my := ringMouse(t, axis, 0.125)
	if !modelGizmoMouseDown(mx, my) {
		t.Fatalf("the rotate gizmo did not grab ring %d", axis)
	}
	if modelGizmo.Axis != axis {
		t.Fatalf("grabbed ring %d, wanted %d", modelGizmo.Axis, axis)
	}
}

func TestRotateRingIsPickable(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	modelGizmoTool = modelToolRotate
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{modeltest.CubeTopFace(t, m)}

	for axis := 0; axis < 3; axis++ {
		mx, my := ringMouse(t, axis, 0.125)
		if got := modelPickRotateRing(mx, my); got != axis {
			t.Errorf("click on ring %d picked %d", axis, got)
		}
	}
}

func TestRotateTurnsTheSelectionAboutItsCentre(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	withGridSnap(t, false, 0.1)
	modelGizmoTool = modelToolRotate

	top := modeltest.CubeTopFace(t, m)
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{top}
	centre := m.FaceCenter(top)
	before := append([]mgl32.Vec3(nil), m.Verts...)

	// Grab the Y ring and drag a quarter of the way round it.
	grabRing(t, 1)
	qx, qy := ringMouse(t, 1, 0.375)
	modelGizmoMouseMove(qx, qy)

	// A rotation preserves each corner's distance from the pivot...
	for _, vi := range m.Faces[top].Verts {
		wantR := before[vi].Sub(centre).Len()
		gotR := m.Verts[vi].Sub(centre).Len()
		if math.Abs(float64(gotR-wantR)) > 1e-4 {
			t.Errorf("corner %d radius %v -> %v", vi, wantR, gotR)
		}
	}
	// ...and a quarter turn about Y must actually have moved them.
	moved := false
	for _, vi := range m.Faces[top].Verts {
		if !vec3Close(m.Verts[vi], before[vi], 1e-3) {
			moved = true
		}
	}
	if !moved {
		t.Error("a quarter turn left every corner where it was")
	}
	// Vertices outside the selection are untouched.
	for _, vi := range []int{0, 1, 2, 3} {
		if !vec3Close(m.Verts[vi], before[vi], 1e-6) {
			t.Errorf("unselected vertex %d moved to %v", vi, m.Verts[vi])
		}
	}
	modelGizmoMouseUp()
}

func TestRotateSnapsToFifteenDegrees(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	withGridSnap(t, true, 0.25)
	modelGizmoTool = modelToolRotate

	top := modeltest.CubeTopFace(t, m)
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{top}
	centre := m.FaceCenter(top)
	v0 := m.Faces[top].Verts[0]
	start := m.Verts[v0].Sub(centre)

	grabRing(t, 1)
	// Somewhere that is deliberately not a multiple of 15 degrees.
	dx, dy := ringMouse(t, 1, 0.236)
	modelGizmoMouseMove(dx, dy)
	modelGizmoMouseUp()

	now := m.Verts[v0].Sub(centre)
	// Angle turned, measured in the XZ plane the Y ring lives in.
	a0 := math.Atan2(float64(start.Z()), float64(start.X()))
	a1 := math.Atan2(float64(now.Z()), float64(now.X()))
	deg := float64(geom.WrapAngle(float32(a1-a0))) * 180 / math.Pi
	steps := deg / modelRotateSnapDeg
	if math.Abs(steps-math.Round(steps)) > 1e-3 {
		t.Errorf("turned %.4f degrees, which is not a multiple of %d", deg, modelRotateSnapDeg)
	}
}

func TestRotateSwingsABoneAboutItsHead(t *testing.T) {
	m, bi := modeltest.SkinnedCube(t)
	withModelDoc(t, m)
	withModelViewport(t)
	withGridSnap(t, false, 0.1)
	modelGizmoTool = modelToolRotate

	modelSelection.Mode = modelSelBone
	modelSelection.Bones = []int{bi}
	modelSelection.BoneJoint = modelBoneHead // pivot on the joint
	head := m.Bones[bi].Head
	length := m.BoneLength(bi)

	grabRing(t, 0)
	qx, qy := ringMouse(t, 0, 0.325)
	modelGizmoMouseMove(qx, qy)
	modelGizmoMouseUp()

	if !vec3Close(m.Bones[bi].Head, head, 1e-5) {
		t.Errorf("head moved to %v; it was the pivot", m.Bones[bi].Head)
	}
	if got := m.BoneLength(bi); math.Abs(float64(got-length)) > 1e-4 {
		t.Errorf("bone length %v -> %v; a rotation must not stretch it", length, got)
	}
	if vec3Close(m.Bones[bi].Tail, mgl32.Vec3{0, 0.5, 0}, 1e-3) {
		t.Error("the tail did not swing")
	}
}

func TestRotateAboutABonesOwnAxisBecomesRoll(t *testing.T) {
	m := model.NewCubeDoc()
	// A bone lying along +X, so the X ring turns it about its own axis.
	bi := m.AddBone(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 0, 0})
	withModelDoc(t, m)
	withModelViewport(t)
	withGridSnap(t, false, 0.1)
	modelGizmoTool = modelToolRotate

	modelSelection.Mode = modelSelBone
	modelSelection.Bones = []int{bi}
	modelSelection.BoneJoint = modelBoneHead
	tail := m.Bones[bi].Tail

	grabRing(t, 0)
	qx, qy := ringMouse(t, 0, 0.325)
	modelGizmoMouseMove(qx, qy)
	modelGizmoMouseUp()

	// Turning a bone about the axis it already points along cannot move its
	// endpoints, so the whole rotation has to land in the roll.
	if !vec3Close(m.Bones[bi].Tail, tail, 1e-4) {
		t.Errorf("tail moved to %v turning about the bone's own axis", m.Bones[bi].Tail)
	}
	if m.Bones[bi].Roll == 0 {
		t.Error("roll is still zero; the twist was lost")
	}
}

func TestWrapAngleFoldsTheSeam(t *testing.T) {
	// Crossing from just under +pi to just over -pi is a small step, not a
	// near-full turn the other way.
	got := geom.WrapAngle(float32(-math.Pi+0.05) - float32(math.Pi-0.05))
	if math.Abs(float64(got)-0.1) > 1e-5 {
		t.Errorf("wrapped to %v, want about 0.1", got)
	}
}

func TestSwitchingToolsCancelsADragInProgress(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	modelGizmoTool = modelToolRotate
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{modeltest.CubeTopFace(t, m)}

	grabRing(t, 1)
	modelSetGizmoTool(modelToolMove)

	if modelGizmoDragging() {
		t.Error("a rotation was left in progress after switching to the move tool")
	}
}
