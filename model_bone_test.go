package main

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// boneChain returns a document with a three-bone connected chain running up the
// Y axis, one unit per bone.
func boneChain(t *testing.T) (*ModelDoc, [3]int) {
	t.Helper()
	m := NewCubeDoc()
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

func TestAddBoneRefusesZeroLength(t *testing.T) {
	m := NewCubeDoc()
	if bi := m.AddBone(mgl32.Vec3{1, 1, 1}, mgl32.Vec3{1, 1, 1}); bi >= 0 {
		t.Fatalf("AddBone accepted a zero-length bone, returned %d", bi)
	}
	if len(m.Bones) != 0 {
		t.Errorf("armature grew to %d bones", len(m.Bones))
	}
}

func TestExtrudeBoneContinuesTheChain(t *testing.T) {
	m, idx := boneChain(t)

	for i := 1; i < 3; i++ {
		child, parent := m.Bones[idx[i]], m.Bones[idx[i-1]]
		if child.Parent != idx[i-1] {
			t.Errorf("bone %d parent = %d, want %d", i, child.Parent, idx[i-1])
		}
		if !child.Connected {
			t.Errorf("bone %d is not connected to its parent", i)
		}
		if !vec3Close(child.Head, parent.Tail, 1e-6) {
			t.Errorf("bone %d head %v is not on the parent's tail %v", i, child.Head, parent.Tail)
		}
		// Same direction and length as the bone it grew from, so a chain walks
		// out in a straight line until the user bends it.
		if got, want := m.BoneLength(idx[i]), m.BoneLength(idx[i-1]); math.Abs(float64(got-want)) > 1e-6 {
			t.Errorf("bone %d length = %v, want %v", i, got, want)
		}
	}
	if got := m.Bones[idx[2]].Tail; !vec3Close(got, mgl32.Vec3{0, 3, 0}, 1e-6) {
		t.Errorf("chain tip at %v, want {0 3 0}", got)
	}
}

func TestBoneNamesStayUnique(t *testing.T) {
	m, idx := boneChain(t)
	seen := map[string]bool{}
	for _, bi := range idx {
		name := m.Bones[bi].Name
		if name == "" {
			t.Errorf("bone %d has no name", bi)
		}
		if seen[name] {
			t.Errorf("duplicate bone name %q", name)
		}
		seen[name] = true
	}
}

func TestSetBoneTailDragsConnectedChild(t *testing.T) {
	m, idx := boneChain(t)

	m.SetBoneTail(idx[0], mgl32.Vec3{0.5, 1, 0})

	if got := m.Bones[idx[1]].Head; !vec3Close(got, mgl32.Vec3{0.5, 1, 0}, 1e-6) {
		t.Errorf("child head = %v, want it dragged to {0.5 1 0}", got)
	}
	// Only the head follows — the child stretches rather than translating, the
	// same as dragging a joint in any armature editor.
	if got := m.Bones[idx[1]].Tail; !vec3Close(got, mgl32.Vec3{0, 2, 0}, 1e-6) {
		t.Errorf("child tail = %v, want it left at {0 2 0}", got)
	}
}

func TestSetBoneHeadOfConnectedMovesTheJoint(t *testing.T) {
	m, idx := boneChain(t)

	m.SetBoneHead(idx[1], mgl32.Vec3{0, 1.5, 0})

	if got := m.Bones[idx[0]].Tail; !vec3Close(got, mgl32.Vec3{0, 1.5, 0}, 1e-6) {
		t.Errorf("parent tail = %v, want it to follow the joint to {0 1.5 0}", got)
	}
	if got := m.Bones[idx[2]].Head; !vec3Close(got, mgl32.Vec3{0, 2, 0}, 1e-6) {
		t.Errorf("grandchild head moved to %v; only its own parent's tail should reach it", got)
	}
}

func TestDeleteBoneReparentsChildren(t *testing.T) {
	m, idx := boneChain(t)
	tipTail := m.Bones[idx[2]].Tail

	m.DeleteBones([]int{idx[1]})

	if len(m.Bones) != 2 {
		t.Fatalf("bones = %d, want 2", len(m.Bones))
	}
	root, tip := m.BoneIndexByName("Bone"), -1
	for i := range m.Bones {
		if i != root {
			tip = i
		}
	}
	if m.Bones[tip].Parent != root {
		t.Errorf("survivor parent = %d, want the root at %d", m.Bones[tip].Parent, root)
	}
	if m.Bones[tip].Connected {
		t.Error("survivor is still marked connected, but the joint it was welded to is gone")
	}
	if !vec3Close(m.Bones[tip].Tail, tipTail, 1e-6) {
		t.Errorf("survivor moved to %v, want it left at %v", m.Bones[tip].Tail, tipTail)
	}
}

func TestSetBoneParentRejectsCycles(t *testing.T) {
	m, idx := boneChain(t)

	if m.SetBoneParent(idx[0], idx[2], false) {
		t.Error("parenting the root under its own grandchild was accepted")
	}
	if m.Bones[idx[0]].Parent != modelBoneNoParent {
		t.Errorf("root parent = %d after a refused reparent", m.Bones[idx[0]].Parent)
	}
	if m.SetBoneParent(idx[0], idx[0], false) {
		t.Error("a bone was allowed to become its own parent")
	}
}

func TestSetBoneParentDetachesToRoot(t *testing.T) {
	m, idx := boneChain(t)

	if !m.SetBoneParent(idx[2], modelBoneNoParent, false) {
		t.Fatal("detaching to root was refused")
	}
	b := m.Bones[idx[2]]
	if b.Parent != modelBoneNoParent || b.Connected {
		t.Errorf("detached bone = parent %d connected %v", b.Parent, b.Connected)
	}
}

func TestBoneOrderPutsParentsFirst(t *testing.T) {
	m, idx := boneChain(t)
	// Reparent so the array order and the hierarchy order disagree.
	if !m.SetBoneParent(idx[0], idx[2], false) {
		// Expected to be refused — build the disagreement the other way.
		m.Bones = append(m.Bones, ModelBone{
			Name: "Extra", Parent: idx[2],
			Head: mgl32.Vec3{1, 0, 0}, Tail: mgl32.Vec3{2, 0, 0},
		})
	}
	pos := map[int]int{}
	for i, bi := range m.boneOrder() {
		pos[bi] = i
	}
	for bi := range m.Bones {
		if p := m.Bones[bi].Parent; p >= 0 && pos[p] > pos[bi] {
			t.Errorf("bone %d is listed before its parent %d", bi, p)
		}
	}
}

func TestBoneMatrixIsAnOrthonormalFrameAlongTheBone(t *testing.T) {
	b := ModelBone{Head: mgl32.Vec3{1, 2, 3}, Tail: mgl32.Vec3{1, 2, 5}} // 2 long, +Z
	mat := boneMatrix(b)

	origin := mat.Mul4x1(mgl32.Vec4{0, 0, 0, 1})
	if !vec3Close(mgl32.Vec3{origin.X(), origin.Y(), origin.Z()}, b.Head, 1e-6) {
		t.Errorf("origin = %v, want the head %v", origin, b.Head)
	}
	// Bone space +Y is the bone, scaled by its length: (0,1,0) lands on the tail.
	tip := mat.Mul4x1(mgl32.Vec4{0, 1, 0, 1})
	if !vec3Close(mgl32.Vec3{tip.X(), tip.Y(), tip.Z()}, b.Tail, 1e-6) {
		t.Errorf("bone-space +Y maps to %v, want the tail %v", tip, b.Tail)
	}

	col := func(i int) mgl32.Vec3 { return mgl32.Vec3{mat[i*4], mat[i*4+1], mat[i*4+2]} }
	x, z := col(0), col(2)
	for _, c := range []struct {
		name string
		v    mgl32.Vec3
	}{{"X", x}, {"Z", z}} {
		if d := math.Abs(float64(c.v.Len() - 1)); d > 1e-5 {
			t.Errorf("%s axis length = %v, want unit", c.name, c.v.Len())
		}
	}
	if d := math.Abs(float64(x.Dot(z))); d > 1e-5 {
		t.Errorf("X and Z are not perpendicular: dot = %v", x.Dot(z))
	}
}

func TestBoneRollTwistsTheFrameButNotTheBone(t *testing.T) {
	b := ModelBone{Head: mgl32.Vec3{}, Tail: mgl32.Vec3{0, 2, 0}}
	rolled := b
	rolled.Roll = float32(math.Pi / 2)

	tip := boneMatrix(rolled).Mul4x1(mgl32.Vec4{0, 1, 0, 1})
	if !vec3Close(mgl32.Vec3{tip.X(), tip.Y(), tip.Z()}, b.Tail, 1e-6) {
		t.Errorf("roll moved the bone itself: tip = %v", tip)
	}
	plain := boneMatrix(b).Mul4x1(mgl32.Vec4{1, 0, 0, 0})
	turned := boneMatrix(rolled).Mul4x1(mgl32.Vec4{1, 0, 0, 0})
	pv := mgl32.Vec3{plain.X(), plain.Y(), plain.Z()}
	tv := mgl32.Vec3{turned.X(), turned.Y(), turned.Z()}
	if math.Abs(float64(pv.Dot(tv))) > 1e-5 {
		t.Errorf("a quarter-turn roll left X at %v (was %v)", tv, pv)
	}
}

func TestBoneDeltaIsIdentityWhenNothingMoved(t *testing.T) {
	m, _ := boneChain(t)
	for i, d := range m.BoneDeltas(cloneBones(m.Bones)) {
		for j := 0; j < 16; j++ {
			want := float32(0)
			if j%5 == 0 {
				want = 1
			}
			if math.Abs(float64(d[j]-want)) > 1e-5 {
				t.Fatalf("bone %d delta is not the identity: %v", i, d)
			}
		}
	}
}

func TestCloneBonesIsIndependent(t *testing.T) {
	m, idx := boneChain(t)
	snap := cloneBones(m.Bones)

	m.SetBoneTail(idx[0], mgl32.Vec3{5, 5, 5})

	if !vec3Close(snap[idx[0]].Tail, mgl32.Vec3{0, 1, 0}, 1e-6) {
		t.Errorf("the snapshot moved with the document: %v", snap[idx[0]].Tail)
	}
}

func TestDocCloneCopiesTheArmature(t *testing.T) {
	m, idx := boneChain(t)
	m.AssignVertsToBone([]int{0, 1}, idx[0], 1)

	c := m.Clone()
	c.SetBoneTail(idx[0], mgl32.Vec3{9, 9, 9})
	c.AssignVertsToBone([]int{0}, idx[1], 1)

	if !vec3Close(m.Bones[idx[0]].Tail, mgl32.Vec3{0, 1, 0}, 1e-6) {
		t.Errorf("editing the clone moved the original's bone to %v", m.Bones[idx[0]].Tail)
	}
	if got := m.WeightOf(0, idx[1]); got != 0 {
		t.Errorf("editing the clone rewrote the original's weights: %v", got)
	}
}
