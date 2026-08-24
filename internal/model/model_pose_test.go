package model

import (
	"foundry/internal/geom"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// mat4Close reports whether two matrices agree to within eps on every entry.
func mat4Close(a, b mgl32.Mat4, eps float32) bool {
	for i := range a {
		if mgl32.Abs(a[i]-b[i]) > eps {
			return false
		}
	}
	return true
}

// localAxis returns bone bi's rest-space axis a expressed as a pose rotation of
// the given angle, which is how an animation channel names "bend the elbow".
func localRot(angle float32, axis mgl32.Vec3) mgl32.Quat {
	return mgl32.QuatRotate(angle, axis.Normalize())
}

func TestIdentityPoseLeavesEverythingAtRest(t *testing.T) {
	m, idx := boneChain(t)
	m.AutoWeightVerts(modelAllVerts(m))
	rest := append([]mgl32.Vec3(nil), m.Verts...)

	deltas := m.PoseDeltas(NewModelPose(len(m.Bones)))
	for _, bi := range idx {
		if !mat4Close(deltas[bi], mgl32.Ident4(), 1e-5) {
			t.Errorf("bone %d: identity pose produced a non-identity delta:\n%v", bi, deltas[bi])
		}
	}

	if SkinVerts(deltas, m.Weights, rest, m.Verts) {
		// SkinVerts reports movement whenever it wrote a bound vertex, so the
		// meaningful check is that the positions are unchanged.
		for i := range m.Verts {
			if !vec3Close(m.Verts[i], rest[i], 1e-5) {
				t.Fatalf("vertex %d moved under an identity pose: %v -> %v", i, rest[i], m.Verts[i])
			}
		}
	}
}

// TestPosingAParentCarriesItsChildren is the whole point of hierarchical pose
// evaluation, and the thing the flat boneDelta path cannot do.
//
// With only the root posed, every descendant's delta must equal the root's:
// posed[child] = posed[parent] · restBasis[parent]⁻¹ · restBasis[child], so the
// restBasis[child]⁻¹ in the delta cancels and what is left is the parent's own
// delta. A child left behind would show up here as an identity.
func TestPosingAParentCarriesItsChildren(t *testing.T) {
	m, idx := boneChain(t)

	pose := NewModelPose(len(m.Bones))
	pose.Bones[idx[0]].Rot = localRot(mgl32.DegToRad(35), mgl32.Vec3{1, 0, 0})
	deltas := m.PoseDeltas(pose)

	if mat4Close(deltas[idx[0]], mgl32.Ident4(), 1e-5) {
		t.Fatal("the posed root got an identity delta")
	}
	for _, bi := range []int{idx[1], idx[2]} {
		if !mat4Close(deltas[bi], deltas[idx[0]], 1e-5) {
			t.Errorf("bone %d did not follow its parent:\ngot  %v\nwant %v", bi, deltas[bi], deltas[idx[0]])
		}
	}
}

func TestPoseKeepsConnectedJointsWelded(t *testing.T) {
	m, idx := boneChain(t)

	pose := NewModelPose(len(m.Bones))
	pose.Bones[idx[0]].Rot = localRot(mgl32.DegToRad(50), mgl32.Vec3{1, 0, 0})
	pose.Bones[idx[1]].Rot = localRot(mgl32.DegToRad(-30), mgl32.Vec3{0, 0, 1})
	pose.Bones[idx[2]].Scale = mgl32.Vec3{1, 1.5, 1}
	posed := m.PosedBones(m.PoseDeltas(pose))

	// A connected child's head is its parent's tail. Nothing in the evaluator
	// enforces this — it falls out of composing through the parent — so it is
	// the check that the composition is right.
	for _, pair := range [][2]int{{idx[0], idx[1]}, {idx[1], idx[2]}} {
		if !vec3Close(posed[pair[1]].Head, posed[pair[0]].Tail, 1e-5) {
			t.Errorf("bone %d came off its parent's tail: head %v, parent tail %v",
				pair[1], posed[pair[1]].Head, posed[pair[0]].Tail)
		}
	}
}

// TestPoseScaleStretchesTheBoneAndMovesItsChildren covers the reason pose space
// drops the length from BoneRestBasis: the stretch has to reach the children.
func TestPoseScaleStretchesTheBoneAndMovesItsChildren(t *testing.T) {
	m, idx := boneChain(t)

	pose := NewModelPose(len(m.Bones))
	pose.Bones[idx[0]].Scale = mgl32.Vec3{1, 2, 1}
	posed := m.PosedBones(m.PoseDeltas(pose))

	// The chain runs up +Y one unit per bone, so doubling the root's length puts
	// its tail at 2 and — because the child inherits the scale — the next tail
	// at 4 rather than 3.
	if want := (mgl32.Vec3{0, 2, 0}); !vec3Close(posed[idx[0]].Tail, want, 1e-5) {
		t.Errorf("scaled root tail = %v, want %v", posed[idx[0]].Tail, want)
	}
	if want := (mgl32.Vec3{0, 2, 0}); !vec3Close(posed[idx[1]].Head, want, 1e-5) {
		t.Errorf("child head did not follow the stretch: %v, want %v", posed[idx[1]].Head, want)
	}
	if want := (mgl32.Vec3{0, 4, 0}); !vec3Close(posed[idx[1]].Tail, want, 1e-5) {
		t.Errorf("child tail = %v, want %v (inherited scale)", posed[idx[1]].Tail, want)
	}
}

func TestPoseScaleStretchesBoundVertices(t *testing.T) {
	m := NewCubeDoc()
	bi := m.AddBone(mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{0, 0.5, 0})
	if bi < 0 {
		t.Fatal("AddBone refused a unit bone")
	}
	verts := modelAllVerts(m)
	if n := m.AssignVertsToBone(verts, bi, 1); n != len(verts) {
		t.Fatalf("bound %d of %d vertices", n, len(verts))
	}
	rest := append([]mgl32.Vec3(nil), m.Verts...)

	pose := NewModelPose(len(m.Bones))
	pose.Bones[bi].Scale = mgl32.Vec3{1, 3, 1}
	out := append([]mgl32.Vec3(nil), rest...)
	SkinVerts(m.PoseDeltas(pose), m.Weights, rest, out)

	// The bone runs from y=-0.5 to y=+0.5 and scales about its head, so a rigid
	// vertex at the far end travels from +0.5 to -0.5 + 3*1 = +2.5.
	for i := range out {
		wantY := -0.5 + (rest[i].Y()-(-0.5))*3
		if mgl32.Abs(out[i].Y()-wantY) > 1e-5 {
			t.Errorf("vertex %d y = %v, want %v", i, out[i].Y(), wantY)
		}
		if mgl32.Abs(out[i].X()-rest[i].X()) > 1e-5 || mgl32.Abs(out[i].Z()-rest[i].Z()) > 1e-5 {
			t.Errorf("vertex %d moved off the bone axis: %v -> %v", i, rest[i], out[i])
		}
	}
}

// TestPoseEvaluatesChildrenBeforeParentsInTheArray pins the invariant model_bone.go
// calls out: Bones is not sorted parents-first, so the evaluator must walk
// boneOrder() rather than the array.
func TestPoseEvaluatesChildrenBeforeParentsInTheArray(t *testing.T) {
	m := NewCubeDoc()
	// Build the chain, then reparent so index 0 is the *child* of index 1.
	child := m.AddBone(mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, 2, 0})
	parent := m.AddBone(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})
	if child != 0 || parent != 1 {
		t.Fatalf("unexpected bone indices: child %d, parent %d", child, parent)
	}
	if !m.SetBoneParent(child, parent, true) {
		t.Fatal("SetBoneParent refused a valid reparent")
	}

	pose := NewModelPose(len(m.Bones))
	pose.Bones[parent].Rot = localRot(mgl32.DegToRad(40), mgl32.Vec3{1, 0, 0})
	deltas := m.PoseDeltas(pose)

	if !mat4Close(deltas[child], deltas[parent], 1e-5) {
		t.Errorf("a child stored before its parent did not follow it:\ngot  %v\nwant %v",
			deltas[child], deltas[parent])
	}
	posed := m.PosedBones(deltas)
	if !vec3Close(posed[child].Head, posed[parent].Tail, 1e-5) {
		t.Errorf("joint came apart: child head %v, parent tail %v", posed[child].Head, posed[parent].Tail)
	}
}

func TestPosedBonesRecoversRoll(t *testing.T) {
	m := NewCubeDoc()
	bi := m.AddBone(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})
	m.SetBoneRoll(bi, 0.3)

	pose := NewModelPose(len(m.Bones))
	// A rotation about the bone's own axis is pure twist: the endpoints do not
	// move, so Roll is the only place it can be recorded.
	const twist = float32(0.4)
	pose.Bones[bi].Rot = localRot(twist, mgl32.Vec3{0, 1, 0})
	posed := m.PosedBones(m.PoseDeltas(pose))

	if !vec3Close(posed[bi].Head, m.Bones[bi].Head, 1e-5) || !vec3Close(posed[bi].Tail, m.Bones[bi].Tail, 1e-5) {
		t.Fatalf("a pure twist moved the bone: %v -> %v", m.Bones[bi], posed[bi])
	}
	got := geom.WrapAngle(posed[bi].Roll - m.Bones[bi].Roll)
	if mgl32.Abs(got-twist) > 1e-4 {
		t.Errorf("recovered twist = %v, want %v", got, twist)
	}
}

// TestBoneRollTurnsTheSameWayAsEveryOtherRotation pins the convention
// BoneBasisAxes documents: Roll is a right-handed turn about the bone's +Y, so
// setting Roll and posing a rotation about the bone axis twist the same way.
// They used to disagree, which also mis-signed modelBoneRotateApply.
func TestBoneRollTurnsTheSameWayAsEveryOtherRotation(t *testing.T) {
	const r = float32(0.6)
	b := ModelBone{Head: mgl32.Vec3{}, Tail: mgl32.Vec3{0, 1, 0}}
	rolled := b
	rolled.Roll = r

	// The X axis BoneRestBasis builds for a rolled bone must match the one a
	// right-handed rotation of r about +Y produces from the unrolled basis.
	fromRoll := XformDir(BoneRestBasis(rolled), mgl32.Vec3{1, 0, 0})
	fromQuat := XformDir(BoneRestBasis(b).Mul4(localRot(r, mgl32.Vec3{0, 1, 0}).Mat4()), mgl32.Vec3{1, 0, 0})
	if !vec3Close(fromRoll, fromQuat, 1e-5) {
		t.Errorf("Roll and a pose rotation twist opposite ways: %v vs %v", fromRoll, fromQuat)
	}
}

// TestBonePoseZeroValueIsRest guards the trap in mgl32.Quat: its zero value is
// {W: 0, V: 0}, which is not the identity and would annihilate whatever it
// multiplied.
func TestBonePoseZeroValueIsRest(t *testing.T) {
	var zero ModelBonePose
	if !zero.IsIdentity() {
		t.Error("a zero-valued ModelBonePose is not reported as rest")
	}
	if !mat4Close(zero.matrix(), mgl32.Ident4(), 1e-6) {
		t.Errorf("a zero-valued ModelBonePose did not produce the identity:\n%v", zero.matrix())
	}

	m, idx := boneChain(t)
	deltas := m.PoseDeltas(ModelPose{Bones: make([]ModelBonePose, len(m.Bones))})
	for _, bi := range idx {
		if !mat4Close(deltas[bi], mgl32.Ident4(), 1e-5) {
			t.Errorf("bone %d: a zero-valued pose collapsed the armature:\n%v", bi, deltas[bi])
		}
	}
}

func TestBonePoseIsIdentityDetectsEachChannel(t *testing.T) {
	cases := []struct {
		name string
		pose ModelBonePose
	}{
		{"translation", ModelBonePose{Trans: mgl32.Vec3{0, 0.01, 0}, Rot: mgl32.QuatIdent(), Scale: mgl32.Vec3{1, 1, 1}}},
		{"rotation", ModelBonePose{Rot: localRot(0.01, mgl32.Vec3{1, 0, 0}), Scale: mgl32.Vec3{1, 1, 1}}},
		{"scale", ModelBonePose{Rot: mgl32.QuatIdent(), Scale: mgl32.Vec3{1, 1.01, 1}}},
	}
	for _, c := range cases {
		if c.pose.IsIdentity() {
			t.Errorf("%s: isIdentity missed a posed channel", c.name)
		}
	}
	// A full turn is the identity rotation with w = -1, which a naive w == 1
	// test would call posed.
	full := ModelBonePose{Rot: localRot(2*float32(math.Pi), mgl32.Vec3{1, 0, 0}), Scale: mgl32.Vec3{1, 1, 1}}
	if !full.IsIdentity() {
		t.Error("a full turn was not recognised as the rest rotation")
	}
}

func TestPoseResizeFillsIdentities(t *testing.T) {
	p := NewModelPose(1)
	p.Resize(3)
	if len(p.Bones) != 3 {
		t.Fatalf("resize gave %d bones, want 3", len(p.Bones))
	}
	for i, b := range p.Bones {
		if !b.IsIdentity() {
			t.Errorf("bone %d was not filled with the identity: %+v", i, b)
		}
	}
	p.Resize(2)
	if len(p.Bones) != 2 {
		t.Errorf("shrink gave %d bones, want 2", len(p.Bones))
	}
}

func TestBoneRestBasisIsOrthonormal(t *testing.T) {
	// The whole reason pose space exists: BoneMatrix folds the length into Y,
	// which would turn a pose rotation into a shear.
	b := ModelBone{Head: mgl32.Vec3{1, 2, 3}, Tail: mgl32.Vec3{1, 5, 3}, Roll: 0.7}
	basis := BoneRestBasis(b)
	cols := [3]mgl32.Vec3{
		{basis[0], basis[1], basis[2]},
		{basis[4], basis[5], basis[6]},
		{basis[8], basis[9], basis[10]},
	}
	for i, c := range cols {
		if mgl32.Abs(c.Len()-1) > 1e-5 {
			t.Errorf("axis %d has length %v, want 1 (BoneMatrix's Y scale leaked in)", i, c.Len())
		}
	}
	for i := 0; i < 3; i++ {
		if d := cols[i].Dot(cols[(i+1)%3]); mgl32.Abs(d) > 1e-5 {
			t.Errorf("axes %d and %d are not perpendicular (dot %v)", i, (i+1)%3, d)
		}
	}
	// BoneMatrix, by contrast, must still carry the length — the rest-drag path
	// depends on it.
	scaled := BoneMatrix(b)
	if y := (mgl32.Vec3{scaled[4], scaled[5], scaled[6]}); mgl32.Abs(y.Len()-3) > 1e-5 {
		t.Errorf("BoneMatrix Y axis has length %v, want the bone length 3", y.Len())
	}
}
