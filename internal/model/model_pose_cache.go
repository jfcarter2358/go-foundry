package model

// model_pose_cache.go — the part of posing that does not depend on the pose.
//
// PoseMatrices composes, per bone, a local rest offset (the parent's rest basis
// inverted, times the bone's own) and PoseDeltas multiplies by the inverse of
// each bone's rest basis. None of that changes from one frame to the next — only
// the pose does — yet it used to be rebuilt on every call: a topological sort
// (two allocations and a reflection-driven sort.SliceStable), two or three
// BoneRestBasis evaluations per bone, and two Mat4.Inv per bone. For a level of
// animated soldiers that was most of the cost of animating them.
//
// poseRig holds those pose-independent pieces. It is computed with exactly the
// expressions PoseMatrices and PoseDeltas used inline, in the same order, so a
// cached evaluation is bit-identical to an uncached one; the tests compare the
// two over the shipped models.
//
// ---------- staleness ----------
//
// ModelDoc.Bones is an exported slice that the model editor writes all over —
// drags, rolls, reparents, extrusions, mirrors, undo snapshots, skeleton links —
// so a generation counter that every write path has to remember to bump would be
// one forgotten bump away from posing against a stale skeleton. Instead the rig
// keeps a copy of the only bone fields posing reads (Parent, Head, Tail, Roll)
// and is reused only while they still match the live armature exactly. That
// comparison is a handful of float compares per bone — far cheaper than the
// basis construction and inversions it saves — and it cannot go stale whatever
// writes the bones, or however.
//
// ---------- concurrency ----------
//
// Several entities share one ModelDoc, and the entity update poses them in
// parallel (collision/entity_update.go). The rig is therefore immutable once
// built and published through an atomic pointer (ModelDoc.poseRig): a reader
// that finds it stale builds a fresh one and swaps it in; two racing readers
// merely both build it, and each poses against a rig computed from the same
// bones. Nothing here
// writes the document itself, so concurrent posing is safe as long as nothing
// edits the bones at the same time — which only the editor does, on its own
// thread, between frames.

import "github.com/go-gl/mathgl/mgl32"

// poseRigBone is the slice of a ModelBone posing depends on. Name and Connected
// are left out on purpose: renaming a bone or flipping its connected flag does
// not move it (SyncConnectedBones rewrites Head when the flag matters, and Head
// is compared).
type poseRigBone struct {
	parent     int
	head, tail mgl32.Vec3
	roll       float32
}

// poseRig is the pose-independent part of PoseMatrices/PoseDeltas for one
// armature. Every slice is index-parallel to ModelDoc.Bones except order.
type poseRig struct {
	key   []poseRigBone
	order []int // boneOrder(): parents before children
	// local is rest[parent]⁻¹·rest[bi] for a bone with a valid parent, and
	// rest[bi] itself for a root — the two cases PoseMatrices distinguishes.
	local []mgl32.Mat4
	// parent is the bone's parent when that parent is valid, or -1, so the walk
	// does not re-ask ValidBone.
	parent []int
	// restInv is BoneRestBasis(bi)⁻¹, PoseDeltas' right-hand factor.
	restInv []mgl32.Mat4
}

// matches reports whether the rig was built from exactly these bones.
func (r *poseRig) matches(bones []ModelBone) bool {
	if len(r.key) != len(bones) {
		return false
	}
	for i := range bones {
		b := &bones[i]
		k := &r.key[i]
		if k.parent != b.Parent || k.head != b.Head || k.tail != b.Tail || k.roll != b.Roll {
			return false
		}
	}
	return true
}

// rig returns the pose-independent data for the armature as it stands, building
// it if the bones changed since it was last built.
func (m *ModelDoc) rig() *poseRig {
	if r := m.poseRig.Load(); r != nil && r.matches(m.Bones) {
		return r
	}
	r := m.buildPoseRig()
	m.poseRig.Store(r)
	return r
}

// buildPoseRig evaluates every pose-independent factor, with the expressions
// PoseMatrices and PoseDeltas have always used.
func (m *ModelDoc) buildPoseRig() *poseRig {
	n := len(m.Bones)
	r := &poseRig{
		key:     make([]poseRigBone, n),
		order:   m.boneOrder(),
		local:   make([]mgl32.Mat4, n),
		parent:  make([]int, n),
		restInv: make([]mgl32.Mat4, n),
	}
	rest := make([]mgl32.Mat4, n)
	for i := range m.Bones {
		b := m.Bones[i]
		r.key[i] = poseRigBone{parent: b.Parent, head: b.Head, tail: b.Tail, roll: b.Roll}
		rest[i] = BoneRestBasis(b)
	}
	for i := range m.Bones {
		parent := m.Bones[i].Parent
		r.local[i] = rest[i]
		r.parent[i] = -1
		if m.ValidBone(parent) {
			r.local[i] = rest[parent].Inv().Mul4(rest[i])
			r.parent[i] = parent
		}
		r.restInv[i] = BoneRestBasis(m.Bones[i]).Inv()
	}
	return r
}

// posedInto is PoseMatrices writing into posed, which must hold len(m.Bones)
// entries.
func (r *poseRig) posedInto(posed []mgl32.Mat4, p ModelPose) {
	for i := range posed {
		posed[i] = mgl32.Ident4()
	}
	for _, bi := range r.order {
		base := mgl32.Ident4()
		if pi := r.parent[bi]; pi >= 0 {
			base = posed[pi]
		}
		posed[bi] = base.Mul4(r.local[bi]).Mul4(p.Bone(bi).matrix())
	}
}

// PoseDeltasInto is PoseDeltas without the allocations: it writes the palette
// into dst and uses scratch for the model-space matrices, growing scratch only
// when the armature outgrew it, and returns both (store the scratch back so the
// next call reuses it). dst keeps its length: when it is shorter or longer than
// the armature — a palette sized before the editor added a bone — only the
// overlap is written, which is what copy(dst, PoseDeltas(p)) used to do.
func (m *ModelDoc) PoseDeltasInto(dst, scratch []mgl32.Mat4, p ModelPose) ([]mgl32.Mat4, []mgl32.Mat4) {
	r := m.rig()
	n := len(m.Bones)
	if cap(scratch) < n {
		scratch = make([]mgl32.Mat4, n)
	}
	posed := scratch[:n]
	r.posedInto(posed, p)
	for i := range dst {
		if i >= n {
			break
		}
		dst[i] = posed[i].Mul4(r.restInv[i])
	}
	return dst, scratch
}
