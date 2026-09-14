package editor

import (
	"foundry/internal/model"
	"foundry/internal/modeltest"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// withBoneMirror sets the mirror toggle for the duration of a test.
func withBoneMirror(t *testing.T, on bool) {
	t.Helper()
	prevOn, prevAxis := modelBoneMirror, modelBoneMirrorAxis
	modelBoneMirror, modelBoneMirrorAxis = on, 0
	t.Cleanup(func() { modelBoneMirror, modelBoneMirrorAxis = prevOn, prevAxis })
}

// armPair returns a cube with a left/right upper arm and forearm, each pair
// mirrored across X.
func armPair(t *testing.T) (m *model.ModelDoc, upperL, upperR, lowerL, lowerR int) {
	t.Helper()
	m = model.NewCubeDoc()
	upperL, upperR = m.AddBonePair(mgl32.Vec3{0.25, 1, 0}, mgl32.Vec3{1, 1, 0}, 0)
	lowerL, lowerR = m.ExtrudeBoneMirrored(upperL, false)
	if upperR < 0 || lowerR < 0 {
		t.Fatal("could not build the arm pair")
	}
	return m, upperL, upperR, lowerL, lowerR
}

func assertMirrored(t *testing.T, m *model.ModelDoc, a, b int) {
	t.Helper()
	if !vec3Close(m.Bones[b].Head, model.MirrorBonePoint(m.Bones[a].Head, 0), 1e-5) ||
		!vec3Close(m.Bones[b].Tail, model.MirrorBonePoint(m.Bones[a].Tail, 0), 1e-5) {
		t.Errorf("%s %v-%v is not the mirror of %s %v-%v",
			m.Bones[b].Name, m.Bones[b].Head, m.Bones[b].Tail,
			m.Bones[a].Name, m.Bones[a].Head, m.Bones[a].Tail)
	}
}

func TestMirroredNudgeMovesThePartnerTheOtherWay(t *testing.T) {
	m, _, _, lowerL, lowerR := armPair(t)
	withModelDoc(t, m)
	withGridSnap(t, false, 0.1)
	withBoneMirror(t, true)

	modelSelection.Mode = modelSelBone
	modelSelection.Bones = []int{lowerL}
	modelSelection.BoneJoint = modelBoneTail

	modelNudgeSelection(0, 0.5)
	modelNudgeSelection(2, 0.25)

	if got := m.Bones[lowerL].Tail; !vec3Close(got, mgl32.Vec3{2.25, 1, 0.25}, 1e-5) {
		t.Errorf("left tail at %v", got)
	}
	assertMirrored(t, m, lowerL, lowerR)
}

func TestMirrorOffLeavesThePartnerAlone(t *testing.T) {
	m, _, _, lowerL, lowerR := armPair(t)
	withModelDoc(t, m)
	withGridSnap(t, false, 0.1)
	withBoneMirror(t, false)

	before := m.Bones[lowerR]
	modelSelection.Mode = modelSelBone
	modelSelection.Bones = []int{lowerL}
	modelSelection.BoneJoint = modelBoneTail
	modelNudgeSelection(0, 0.5)

	if m.Bones[lowerR] != before {
		t.Errorf("partner moved with mirror off: %v -> %v", before, m.Bones[lowerR])
	}
}

func TestMirroredDragOfAJointKeepsBothElbowsTogether(t *testing.T) {
	m, upperL, upperR, lowerL, lowerR := armPair(t)
	withModelDoc(t, m)
	withGridSnap(t, false, 0.1)
	withBoneMirror(t, true)

	modelSelection.Mode = modelSelBone
	modelSelection.Bones = []int{lowerL}
	modelSelection.BoneJoint = modelBoneHead // the elbow

	modelBoneGizmoCapture()
	// Several motion events, as a real drag sends: each is absolute from the
	// snapshot, and the partner must end where the last one puts it.
	for _, d := range []float32{0.1, 0.4, -0.3} {
		modelBoneGizmoApply(1, d)
	}
	modelBoneGizmoRelease()

	for _, pair := range [][2]int{{upperL, upperR}, {lowerL, lowerR}} {
		assertMirrored(t, m, pair[0], pair[1])
	}
	if !vec3Close(m.Bones[upperR].Tail, m.Bones[lowerR].Head, 1e-6) {
		t.Error("the right elbow came apart")
	}
	if got := m.Bones[lowerL].Head.Y(); got < 0.69 || got > 0.71 {
		t.Errorf("left elbow y = %v, want 0.7", got)
	}
}

func TestMirroredRotateMirrorsTheRoll(t *testing.T) {
	m, _, _, lowerL, lowerR := armPair(t)
	withModelDoc(t, m)
	withModelViewport(t)
	withGridSnap(t, false, 0.1)
	withBoneMirror(t, true)
	modelGizmoTool = modelToolRotate

	modelSelection.Mode = modelSelBone
	modelSelection.Bones = []int{lowerL}
	modelSelection.BoneJoint = modelBoneHead

	grabRing(t, 1)
	qx, qy := ringMouse(t, 1, 0.3)
	modelGizmoMouseMove(qx, qy)
	modelGizmoMouseUp()

	assertMirrored(t, m, lowerL, lowerR)
	if want := model.MirrorBoneRoll(m.Bones[lowerL], 0); m.Bones[lowerR].Roll != want {
		t.Errorf("partner roll %v, want %v", m.Bones[lowerR].Roll, want)
	}
}

func TestBoneLeadsTakeOneOfEachSelectedPair(t *testing.T) {
	m, upperL, upperR, lowerL, _ := armPair(t)
	withModelDoc(t, m)
	withBoneMirror(t, true)

	spine := m.AddBone(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})
	modelSelection.Bones = []int{upperL, spine, lowerL, upperR} // upperR is active

	got := modelBoneLeads()
	want := []int{spine, lowerL, upperR}
	if len(got) != len(want) {
		t.Fatalf("leads = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("leads = %v, want %v", got, want)
		}
	}
}

func TestMirroredDragWithBothSidesSelectedStillMirrors(t *testing.T) {
	m, _, _, lowerL, lowerR := armPair(t)
	withModelDoc(t, m)
	withGridSnap(t, false, 0.1)
	withBoneMirror(t, true)

	modelSelection.Mode = modelSelBone
	modelSelection.Bones = []int{lowerR, lowerL}
	modelSelection.BoneJoint = modelBoneTail

	modelNudgeSelection(0, 0.5)

	// Nudged like any other selection, both tails would go +X — the right one
	// inward. The active (left) one leads instead and the right mirrors it.
	if got := m.Bones[lowerL].Tail.X(); got < 2.249 {
		t.Errorf("left tail x = %v, want 2.25", got)
	}
	assertMirrored(t, m, lowerL, lowerR)
}

func TestExtrudeKeysGrowFromTheChosenEnd(t *testing.T) {
	m := model.NewCubeDoc()
	pelvis := m.AddBone(mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, 1.5, 0})
	withModelDoc(t, m)
	withBoneMirror(t, false)

	modelSelection.Mode = modelSelBone
	modelSelection.Bones = []int{pelvis}
	modelSelection.BoneJoint = modelBoneWhole
	modelExtrudeSelectedBonesFromHead()

	leg := modelActiveBone()
	if leg < 0 || m.Bones[leg].Parent != pelvis || !vec3Close(m.Bones[leg].Head, mgl32.Vec3{0, 1, 0}, 1e-6) {
		t.Fatalf("Shift+E did not grow a child from the head: %+v", m.Bones)
	}

	// A bare E on a grabbed head handle does the same.
	modelSelection.Bones = []int{pelvis}
	modelSelection.BoneJoint = modelBoneHead
	modelExtrudeSelectedBones()
	if bi := modelActiveBone(); bi == leg || !vec3Close(m.Bones[bi].Head, mgl32.Vec3{0, 1, 0}, 1e-6) {
		t.Errorf("E on the head handle grew from %v", m.Bones[bi].Head)
	}

	// And on the tail, from the tail.
	modelSelection.Bones = []int{pelvis}
	modelSelection.BoneJoint = modelBoneTail
	modelExtrudeSelectedBones()
	if bi := modelActiveBone(); !m.Bones[bi].Connected || !vec3Close(m.Bones[bi].Head, mgl32.Vec3{0, 1.5, 0}, 1e-6) {
		t.Errorf("E on the tail grew from %v", m.Bones[bi].Head)
	}
}

func TestMirroredExtrudeGrowsThePartnerToo(t *testing.T) {
	m, _, _, lowerL, lowerR := armPair(t)
	withModelDoc(t, m)
	withBoneMirror(t, true)

	modelSelection.Mode = modelSelBone
	modelSelection.Bones = []int{lowerL}
	modelSelection.BoneJoint = modelBoneTail
	n := len(m.Bones)
	modelExtrudeSelectedBones()

	if len(m.Bones) != n+2 {
		t.Fatalf("extrude added %d bones, want 2", len(m.Bones)-n)
	}
	hand := modelActiveBone()
	if len(modelSelection.Bones) != 1 || m.Bones[hand].Parent != lowerL {
		t.Fatalf("selection %v; want just the left hand", modelSelection.Bones)
	}
	partner := m.MirrorBone(hand)
	if partner < 0 || m.Bones[partner].Parent != lowerR {
		t.Fatalf("the new bone %q has no partner on the right forearm", m.Bones[hand].Name)
	}
}

func TestForkTurnsMirrorOnAndPairsTheArms(t *testing.T) {
	m, spine := modeltest.SkinnedCube(t)
	withModelDoc(t, m)
	withBoneMirror(t, false)
	withGridSnap(t, false, 0.1)

	modelSelection.Mode = modelSelBone
	modelSelection.Bones = []int{spine}
	modelSelection.BoneJoint = modelBoneTail
	if !modelBoneForkable(spine) {
		t.Fatal("a spine tail on X = 0 is not forkable")
	}
	modelForkSelectedBones()

	if !modelBoneMirror {
		t.Error("fork left mirror off")
	}
	arm := modelActiveBone()
	partner := m.MirrorBone(arm)
	if partner < 0 {
		t.Fatalf("forked bone %q has no partner", m.Bones[arm].Name)
	}
	// The pair it made should now move together.
	modelNudgeSelection(1, 0.25)
	assertMirrored(t, m, arm, partner)
}

func TestForkRefusedOffThePlaneLeavesNoUndoEntry(t *testing.T) {
	m := model.NewCubeDoc()
	bi := m.AddBone(mgl32.Vec3{1, 0, 0}, mgl32.Vec3{1, 1, 0})
	withModelDoc(t, m)
	withBoneMirror(t, true)

	modelSelection.Mode = modelSelBone
	modelSelection.Bones = []int{bi}
	modelSelection.BoneJoint = modelBoneTail
	depth := modelUndoStack.len()
	modelForkSelectedBones()

	if len(m.Bones) != 1 {
		t.Errorf("fork off the plane added bones: %d", len(m.Bones))
	}
	if modelUndoStack.len() != depth {
		t.Error("a refused fork left an undo entry behind")
	}
}
