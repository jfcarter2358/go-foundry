package model

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestMirrorBoneNameSwapsTheSide(t *testing.T) {
	cases := map[string]string{
		"Arm.L":      "Arm.R",
		"Arm.R.003":  "Arm.L.003",
		"leg_l":      "leg_r",
		"L_Hand":     "R_Hand",
		"Foot-R":     "Foot-L",
		"LeftHand":   "RightHand",
		"armRight":   "armLeft",
		"Thumb_left": "Thumb_right",
		"Right.Toe":  "Left.Toe",
	}
	for in, want := range cases {
		got, ok := MirrorBoneName(in)
		if !ok || got != want {
			t.Errorf("MirrorBoneName(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestMirrorBoneNameIgnoresNamesWithoutASide(t *testing.T) {
	// The awkward ones are names that merely contain a side token: a trailing
	// letter with no separator, and a side word that is part of a longer word.
	for _, name := range []string{"Spine", "Bone.001", "BallR", "Cleft", "Leftover", "L", ".L", "Bone"} {
		if got, ok := MirrorBoneName(name); ok {
			t.Errorf("MirrorBoneName(%q) = %q; it has no side", name, got)
		}
	}
}

func TestAddBonePairMirrorsAndNamesBothSides(t *testing.T) {
	m := NewCubeDoc()
	lead, partner := m.AddBonePair(mgl32.Vec3{0.5, 1, 0}, mgl32.Vec3{1.5, 1, 0.25}, 0)
	if lead < 0 || partner < 0 {
		t.Fatalf("AddBonePair = %d, %d", lead, partner)
	}
	if got := m.Bones[lead].Name; got != "Bone.L" {
		t.Errorf("positive-side bone named %q, want Bone.L", got)
	}
	if got := m.Bones[partner].Name; got != "Bone.R" {
		t.Errorf("negative-side bone named %q, want Bone.R", got)
	}
	if !vec3Close(m.Bones[partner].Head, mgl32.Vec3{-0.5, 1, 0}, 1e-6) ||
		!vec3Close(m.Bones[partner].Tail, mgl32.Vec3{-1.5, 1, 0.25}, 1e-6) {
		t.Errorf("partner at %v-%v, not the mirror image", m.Bones[partner].Head, m.Bones[partner].Tail)
	}
	if m.MirrorBone(lead) != partner || m.MirrorBone(partner) != lead {
		t.Error("the two bones do not find each other as partners")
	}

	// A second pair must not break the naming: both sides get the same number.
	a, b := m.AddBonePair(mgl32.Vec3{-0.5, 2, 0}, mgl32.Vec3{-1, 2, 0}, 0)
	if m.Bones[a].Name != "Bone.R.001" || m.Bones[b].Name != "Bone.L.001" {
		t.Errorf("second pair named %q / %q", m.Bones[a].Name, m.Bones[b].Name)
	}
}

func TestAddBonePairOnThePlaneIsASingleBone(t *testing.T) {
	m := NewCubeDoc()
	lead, partner := m.AddBonePair(mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 1}, 0)
	if lead < 0 || partner != -1 {
		t.Fatalf("AddBonePair on the plane = %d, %d; want one bone", lead, partner)
	}
	if len(m.Bones) != 1 {
		t.Errorf("armature has %d bones, want 1", len(m.Bones))
	}
}

func TestMirrorBoneRollMirrorsTheBasis(t *testing.T) {
	// Directions on both sides of PerpAxis's switch, since the reference axis
	// it picks is what makes a plain negation wrong for some of them.
	bones := []ModelBone{
		{Head: mgl32.Vec3{0.2, 0, 0}, Tail: mgl32.Vec3{1, 0.3, 0.2}, Roll: 0.7},
		{Head: mgl32.Vec3{0.2, 0, 0}, Tail: mgl32.Vec3{0.25, 1, 0.05}, Roll: -1.2},
		{Head: mgl32.Vec3{0.1, 0.4, 0.3}, Tail: mgl32.Vec3{0.3, -0.6, 0.1}, Roll: 2.5},
	}
	for axis := 0; axis < 3; axis++ {
		for _, b := range bones {
			x, _, z, _, _ := BoneBasisAxes(b)
			mirrored := ModelBone{
				Head: MirrorBonePoint(b.Head, axis),
				Tail: MirrorBonePoint(b.Tail, axis),
				Roll: MirrorBoneRoll(b, axis),
			}
			mx, _, mz, _, _ := BoneBasisAxes(mirrored)
			if !vec3Close(mx, MirrorBonePoint(x, axis).Mul(-1), 1e-4) {
				t.Errorf("axis %d, bone %v: mirrored X %v, want %v", axis, b, mx, MirrorBonePoint(x, axis).Mul(-1))
			}
			if !vec3Close(mz, MirrorBonePoint(z, axis), 1e-4) {
				t.Errorf("axis %d, bone %v: mirrored Z %v, want %v", axis, b, mz, MirrorBonePoint(z, axis))
			}
		}
	}
}

func TestExtrudeBoneFromHeadBranchesAwayFromTheBone(t *testing.T) {
	m := NewCubeDoc()
	pelvis := m.AddBone(mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, 1.5, 0})
	leg := m.ExtrudeBoneFrom(pelvis, true)
	if leg < 0 {
		t.Fatal("ExtrudeBoneFrom(head) refused")
	}
	b := m.Bones[leg]
	if b.Parent != pelvis {
		t.Errorf("parent = %d, want %d", b.Parent, pelvis)
	}
	if b.Connected {
		t.Error("a head-grown child is connected; Connected welds it to the parent's tail")
	}
	if !vec3Close(b.Head, mgl32.Vec3{0, 1, 0}, 1e-6) {
		t.Errorf("head at %v, want the parent's head", b.Head)
	}
	if !vec3Close(b.Tail, mgl32.Vec3{0, 0.5, 0}, 1e-6) {
		t.Errorf("tail at %v, want it pointing away from the parent", b.Tail)
	}

	// Syncing connected joints must leave it where it grew.
	m.SetBoneTail(pelvis, mgl32.Vec3{0, 2, 0})
	if !vec3Close(m.Bones[leg].Head, mgl32.Vec3{0, 1, 0}, 1e-6) {
		t.Errorf("moving the parent's tail moved the head-grown child to %v", m.Bones[leg].Head)
	}
}

func TestExtrudeBoneMirroredGrowsBothSidesAsAPair(t *testing.T) {
	m := NewCubeDoc()
	l, r := m.AddBonePair(mgl32.Vec3{0.2, 1, 0}, mgl32.Vec3{1, 1, 0}, 0)
	for _, fromHead := range []bool{false, true} {
		child, mirror := m.ExtrudeBoneMirrored(l, fromHead)
		if child < 0 || mirror < 0 {
			t.Fatalf("fromHead=%v: ExtrudeBoneMirrored = %d, %d", fromHead, child, mirror)
		}
		if m.Bones[child].Parent != l || m.Bones[mirror].Parent != r {
			t.Errorf("fromHead=%v: parents %d/%d, want %d/%d", fromHead,
				m.Bones[child].Parent, m.Bones[mirror].Parent, l, r)
		}
		if m.MirrorBone(child) != mirror {
			t.Errorf("fromHead=%v: children %q and %q are not a pair", fromHead,
				m.Bones[child].Name, m.Bones[mirror].Name)
		}
		if !vec3Close(m.Bones[mirror].Tail, MirrorBonePoint(m.Bones[child].Tail, 0), 1e-6) {
			t.Errorf("fromHead=%v: children are not mirror images", fromHead)
		}
	}
}

func TestExtrudeBoneMirroredWithoutAPartnerIsAPlainExtrude(t *testing.T) {
	m := NewCubeDoc()
	spine := m.AddBone(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})
	child, mirror := m.ExtrudeBoneMirrored(spine, false)
	if child < 0 || mirror != -1 {
		t.Fatalf("ExtrudeBoneMirrored on a centre bone = %d, %d", child, mirror)
	}
	if len(m.Bones) != 2 {
		t.Errorf("armature has %d bones, want 2", len(m.Bones))
	}
}

func TestForkBoneMakesAMirroredPairOnThePlane(t *testing.T) {
	m := NewCubeDoc()
	spine := m.AddBone(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 2, 0})
	m.RenameBone(spine, "Chest")

	pos, neg := m.ForkBone(spine, false, 0)
	if pos < 0 || neg < 0 {
		t.Fatalf("ForkBone = %d, %d", pos, neg)
	}
	if m.Bones[pos].Name != "Chest.L" || m.Bones[neg].Name != "Chest.R" {
		t.Errorf("fork named %q / %q", m.Bones[pos].Name, m.Bones[neg].Name)
	}
	for _, bi := range []int{pos, neg} {
		if !m.Bones[bi].Connected || m.Bones[bi].Parent != spine {
			t.Errorf("%s is not a connected child of the spine", m.Bones[bi].Name)
		}
		if math.Abs(float64(m.BoneLength(bi)-2)) > 1e-5 {
			t.Errorf("%s length %v, want the parent's", m.Bones[bi].Name, m.BoneLength(bi))
		}
	}
	if !vec3Close(m.Bones[pos].Tail, mgl32.Vec3{2, 2, 0}, 1e-6) ||
		!vec3Close(m.Bones[neg].Tail, mgl32.Vec3{-2, 2, 0}, 1e-6) {
		t.Errorf("fork tails %v / %v, want out along ±X", m.Bones[pos].Tail, m.Bones[neg].Tail)
	}

	// Off the plane there is no mirror image to connect to.
	if a, b := m.ForkBone(pos, false, 0); a != -1 || b != -1 {
		t.Errorf("ForkBone off the plane = %d, %d; want refused", a, b)
	}
}

func TestMirrorBoneOntoCarriesTheConnectedParent(t *testing.T) {
	m := NewCubeDoc()
	upper, upperR := m.AddBonePair(mgl32.Vec3{0.2, 1, 0}, mgl32.Vec3{1, 1, 0}, 0)
	lower, lowerR := m.ExtrudeBoneMirrored(upper, false)

	// Move the elbow on the left, then mirror the forearm's head across.
	m.SetBoneHead(lower, mgl32.Vec3{1, 0.7, 0.3})
	m.MirrorBoneOnto(lower, lowerR, 0, true, false, false)

	if !vec3Close(m.Bones[lowerR].Head, mgl32.Vec3{-1, 0.7, 0.3}, 1e-6) {
		t.Errorf("right forearm head at %v", m.Bones[lowerR].Head)
	}
	if !vec3Close(m.Bones[upperR].Tail, m.Bones[lowerR].Head, 1e-6) {
		t.Errorf("right elbow came apart: upper tail %v, forearm head %v",
			m.Bones[upperR].Tail, m.Bones[lowerR].Head)
	}
}
