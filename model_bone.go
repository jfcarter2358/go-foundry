package main

// model_bone.go — the model editor's skeleton.
//
// A ModelDoc carries an armature alongside its mesh rather than in a document
// of its own. Blender separates the two because an armature is reusable across
// objects; here a model is a single self-contained .fmdl that the level places
// as one thing, so a second document would only ever be a file the user has to
// remember to keep in step with the first.
//
// A bone is a head/tail segment, not a matrix. The two encode the same rest
// placement, but the segment is the one that survives editing: dragging a tail
// changes the bone's direction *and* its length, and doing that to a matrix
// means decomposing it, editing one factor and recomposing — losing the roll
// every time the direction passes through the pole. boneMatrix (below) derives
// the matrix on demand, which is the direction that never loses information.
//
// Parent indices are NOT required to be sorted before their children. Keeping
// them sorted would mean reindexing the whole array — and every vertex weight
// and selection that points into it — each time a bone is reparented. Instead
// the array is unordered and boneOrder() computes a parent-before-child walk
// for the two places that need one (connected-joint sync and pose evaluation).
// The one invariant that *is* enforced everywhere is acyclicity.

import (
	"fmt"
	"foundry/internal/geom"
	"math"
	"sort"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

// modelBoneNoParent is the Parent value of a root bone.
const modelBoneNoParent = -1

// ModelBone is one segment of the armature, in model space.
//
// Connected means the head is not independent state: it *is* the parent's tail,
// and the two move as one joint. That is what an extruded chain produces, and
// it is why a limb stays attached when you drag the joint between two of its
// bones. A non-connected child still follows its parent in a pose, but its head
// may sit anywhere — the offset arm hanging off a spine, say.
type ModelBone struct {
	Name      string
	Parent    int // index into ModelDoc.Bones, or modelBoneNoParent
	Head      mgl32.Vec3
	Tail      mgl32.Vec3
	Roll      float32 // radians about the bone axis; orients the deform basis
	Connected bool
}

// ---------- queries ----------

// BoneCount returns the number of bones in the armature.
func (m *ModelDoc) BoneCount() int { return len(m.Bones) }

// validBone reports whether bi indexes an existing bone.
func (m *ModelDoc) validBone(bi int) bool { return bi >= 0 && bi < len(m.Bones) }

// BoneLength returns the head-to-tail distance of bone bi.
func (m *ModelDoc) BoneLength(bi int) float32 {
	if !m.validBone(bi) {
		return 0
	}
	return m.Bones[bi].Tail.Sub(m.Bones[bi].Head).Len()
}

// BoneChildren returns the indices of the bones parented to bi, ascending.
func (m *ModelDoc) BoneChildren(bi int) []int {
	var out []int
	for i := range m.Bones {
		if m.Bones[i].Parent == bi {
			out = append(out, i)
		}
	}
	return out
}

// BoneDepth returns how many parents bone bi has above it; 0 for a root. The
// step cap makes it total even on a cycle, which validation forbids but a
// hand-edited file can still contain.
func (m *ModelDoc) BoneDepth(bi int) int {
	if !m.validBone(bi) {
		return 0
	}
	d := 0
	for p := m.Bones[bi].Parent; m.validBone(p) && d <= len(m.Bones); p = m.Bones[p].Parent {
		d++
	}
	return d
}

// boneOrder returns every bone index with parents before their children.
//
// Sorting by depth is enough: a bone's depth is always greater than its
// parent's, so any depth-ascending order is a valid topological order. Stable,
// so bones at the same depth keep their array order and the walk is
// deterministic.
func (m *ModelDoc) boneOrder() []int {
	out := make([]int, len(m.Bones))
	depth := make([]int, len(m.Bones))
	for i := range m.Bones {
		out[i] = i
		depth[i] = m.BoneDepth(i)
	}
	sort.SliceStable(out, func(a, b int) bool { return depth[out[a]] < depth[out[b]] })
	return out
}

// boneIsDescendant reports whether bi is bj, or sits anywhere below it. This is
// the cycle guard every reparent goes through.
func (m *ModelDoc) boneIsDescendant(bi, bj int) bool {
	steps := 0
	for p := bi; m.validBone(p) && steps <= len(m.Bones); p = m.Bones[p].Parent {
		if p == bj {
			return true
		}
		steps++
	}
	return false
}

// BoneIndexByName returns the bone with the given name, or -1.
func (m *ModelDoc) BoneIndexByName(name string) int {
	for i := range m.Bones {
		if m.Bones[i].Name == name {
			return i
		}
	}
	return -1
}

// ---------- naming ----------

// defaultBoneName is what a bone with no name of its own gets.
const defaultBoneName = "Bone"

// boneNameStem strips a ".001"-style suffix, so extruding "Arm.003" produces
// "Arm.004" rather than "Arm.003.001".
func boneNameStem(name string) string {
	if name == "" {
		return defaultBoneName
	}
	if i := strings.LastIndex(name, "."); i > 0 && len(name)-i == 4 {
		if _, err := fmt.Sscanf(name[i+1:], "%03d", new(int)); err == nil {
			return name[:i]
		}
	}
	return name
}

// uniqueBoneName returns stem, or the first free "stem.NNN" after it. Names are
// unique because the animation channels this is all leading to will key on
// them: two bones called "Arm" would make a saved pose ambiguous.
func (m *ModelDoc) uniqueBoneName(stem string) string {
	stem = boneNameStem(stem)
	if m.BoneIndexByName(stem) < 0 {
		return stem
	}
	for n := 1; n < 1000; n++ {
		candidate := fmt.Sprintf("%s.%03d", stem, n)
		if m.BoneIndexByName(candidate) < 0 {
			return candidate
		}
	}
	return fmt.Sprintf("%s.%d", stem, len(m.Bones))
}

// ---------- editing ----------

// AddBone appends a root bone and returns its index. A zero-length bone is
// refused: it has no direction, so it could neither be drawn nor define a
// deform basis, and the editor would show an invisible thing to drag.
func (m *ModelDoc) AddBone(head, tail mgl32.Vec3) int {
	if tail.Sub(head).Len() < 1e-6 {
		return -1
	}
	m.Bones = append(m.Bones, ModelBone{
		Name:   m.uniqueBoneName(defaultBoneName),
		Parent: modelBoneNoParent,
		Head:   head,
		Tail:   tail,
	})
	return len(m.Bones) - 1
}

// ExtrudeBone grows a connected child from bone bi's tail, pointing the same
// way and the same length, and returns its index.
//
// This is the operation a skeleton is actually built with: press E down a limb
// and each press continues the chain from where the last one ended. Connected
// is what makes the chain behave like one — dragging a joint moves both the
// bone that ends there and the bone that starts there.
func (m *ModelDoc) ExtrudeBone(bi int) int {
	if !m.validBone(bi) {
		return -1
	}
	parent := m.Bones[bi]
	dir := parent.Tail.Sub(parent.Head)
	if dir.Len() < 1e-6 {
		// A degenerate parent has no direction to continue; +Y is the same
		// default AddBone's callers use.
		dir = mgl32.Vec3{0, 1, 0}
	}
	m.Bones = append(m.Bones, ModelBone{
		Name:      m.uniqueBoneName(parent.Name),
		Parent:    bi,
		Head:      parent.Tail,
		Tail:      parent.Tail.Add(dir),
		Roll:      parent.Roll,
		Connected: true,
	})
	return len(m.Bones) - 1
}

// DeleteBones removes the given bones, reparenting their children rather than
// taking the whole subtree with them — deleting one bone in the middle of a
// spine should not silently delete both arms.
//
// A child that was connected to a deleted bone loses that flag: the joint it
// was welded to no longer exists, and re-welding it to the grandparent's tail
// would teleport it somewhere the user never put it.
//
// Vertex weights and animation tracks follow: assignments to a deleted bone are
// dropped and the remaining ones are reindexed, so nothing is left pointing at a
// bone that has gone.
func (m *ModelDoc) DeleteBones(bones []int) {
	del := make(map[int]bool, len(bones))
	for _, bi := range bones {
		if m.validBone(bi) {
			del[bi] = true
		}
	}
	if len(del) == 0 {
		return
	}

	// Reparent survivors first, while the old indices still mean something.
	for i := range m.Bones {
		if del[i] {
			continue
		}
		b := &m.Bones[i]
		for steps := 0; m.validBone(b.Parent) && del[b.Parent] && steps <= len(m.Bones); steps++ {
			b.Parent = m.Bones[b.Parent].Parent
			b.Connected = false
		}
	}

	remap := make([]int, len(m.Bones))
	kept := make([]ModelBone, 0, len(m.Bones)-len(del))
	for i := range m.Bones {
		if del[i] {
			remap[i] = -1
			continue
		}
		remap[i] = len(kept)
		kept = append(kept, m.Bones[i])
	}
	for i := range kept {
		if kept[i].Parent >= 0 {
			kept[i].Parent = remap[kept[i].Parent]
		}
	}
	m.Bones = kept
	m.remapWeightBones(remap)
	m.remapAnimBones(remap)
	m.syncConnectedBones()
}

// SetBoneParent reparents child under parent (modelBoneNoParent for a root).
// Returns false when the move would create a cycle, which is the one thing
// boneOrder and every pose walk rely on being impossible.
func (m *ModelDoc) SetBoneParent(child, parent int, connected bool) bool {
	if !m.validBone(child) {
		return false
	}
	if parent == modelBoneNoParent {
		m.Bones[child].Parent = modelBoneNoParent
		m.Bones[child].Connected = false
		return true
	}
	if !m.validBone(parent) || m.boneIsDescendant(parent, child) {
		return false
	}
	m.Bones[child].Parent = parent
	m.Bones[child].Connected = connected
	m.syncConnectedBones()
	return true
}

// RenameBone gives bone bi a unique name. An empty name falls back to the
// default stem rather than being stored, so no bone is ever nameless.
func (m *ModelDoc) RenameBone(bi int, name string) {
	if !m.validBone(bi) {
		return
	}
	name = strings.TrimSpace(name)
	if name == m.Bones[bi].Name {
		return
	}
	if name == "" {
		name = defaultBoneName
	}
	// Clear the old name first so renaming "Arm" to "Arm" via a case change
	// does not collide with itself.
	m.Bones[bi].Name = ""
	m.Bones[bi].Name = m.uniqueBoneName(name)
}

// SetBoneHead moves bone bi's head.
//
// On a connected bone the head is the shared joint, so moving it moves the
// parent's tail too — otherwise the chain would come apart at exactly the point
// the user grabbed to shape it.
func (m *ModelDoc) SetBoneHead(bi int, p mgl32.Vec3) {
	if !m.validBone(bi) {
		return
	}
	b := &m.Bones[bi]
	if b.Connected && m.validBone(b.Parent) {
		m.Bones[b.Parent].Tail = p
	}
	b.Head = p
	m.syncConnectedBones()
}

// SetBoneTail moves bone bi's tail, dragging the heads of any bones connected
// to it.
func (m *ModelDoc) SetBoneTail(bi int, p mgl32.Vec3) {
	if !m.validBone(bi) {
		return
	}
	m.Bones[bi].Tail = p
	m.syncConnectedBones()
}

// MoveBone offsets a whole bone. Connected children keep their heads on this
// bone's tail and so stretch to follow, which is the same behaviour as dragging
// the tail alone.
func (m *ModelDoc) MoveBone(bi int, delta mgl32.Vec3) {
	if !m.validBone(bi) {
		return
	}
	m.SetBoneHead(bi, m.Bones[bi].Head.Add(delta))
	m.Bones[bi].Tail = m.Bones[bi].Tail.Add(delta)
	m.syncConnectedBones()
}

// SetBoneRoll sets the twist of the deform basis about the bone axis.
func (m *ModelDoc) SetBoneRoll(bi int, roll float32) {
	if m.validBone(bi) {
		m.Bones[bi].Roll = roll
	}
}

// syncConnectedBones restores the invariant that a connected bone's head is its
// parent's tail. Walking parents-first matters: a joint moved at the top of a
// chain has to reach the bottom in one pass.
func (m *ModelDoc) syncConnectedBones() {
	for _, bi := range m.boneOrder() {
		b := &m.Bones[bi]
		if b.Connected && m.validBone(b.Parent) {
			b.Head = m.Bones[b.Parent].Tail
		}
	}
}

// cloneBones deep-copies the armature. ModelBone holds no slices, so a plain
// copy of the backing array is a complete snapshot.
func cloneBones(src []ModelBone) []ModelBone {
	if src == nil {
		return nil
	}
	return append([]ModelBone(nil), src...)
}

// ---------- the deform basis ----------

// boneMatrix maps bone space to model space: the origin sits at the head, +Y
// runs along the bone and is scaled by its length, and X/Z are the roll-rotated
// perpendicular axes.
//
// +Y along the bone is the usual armature convention (Blender's is the same),
// and it is worth keeping even though nothing reads the axis yet: an animation
// channel that rotates "the elbow" is a rotation about a bone-space axis, and
// which axis that is has to mean the same thing here and in the animation data.
//
// Length rides in the Y scale rather than being normalised away so that
// stretching a bone stretches what it deforms — pull a tail and the assigned
// vertices follow it out instead of sliding along a fixed-length segment.
func boneMatrix(b ModelBone) mgl32.Mat4 {
	x, y, z, length, ok := boneBasisAxes(b)
	if !ok {
		// Degenerate: no direction to build a basis from, so the bone is a pure
		// translation. Invertible, which is all the delta below needs.
		return mgl32.Translate3D(b.Head.X(), b.Head.Y(), b.Head.Z())
	}
	yl := y.Mul(length)
	h := b.Head
	return mgl32.Mat4{
		x[0], x[1], x[2], 0,
		yl[0], yl[1], yl[2], 0,
		z[0], z[1], z[2], 0,
		h[0], h[1], h[2], 1,
	}
}

// boneBasisAxes returns the bone's orthonormal axes and its length. +Y runs
// head-to-tail; X and Z are the roll-rotated perpendicular pair. ok is false for
// a zero-length bone, which has no direction to build a basis from.
//
// Split out of boneMatrix because model_pose.go needs the same axes *without*
// the length folded into Y — see boneRestBasis for why posing cannot use the
// scaled form.
func boneBasisAxes(b ModelBone) (x, y, z mgl32.Vec3, length float32, ok bool) {
	dir := b.Tail.Sub(b.Head)
	length = dir.Len()
	if length < 1e-6 {
		return x, y, z, 0, false
	}
	y = dir.Mul(1 / length)

	x = geom.PerpAxis(y)
	if x.Len() < 1e-6 {
		x = mgl32.Vec3{1, 0, 0}
	}
	x = x.Normalize()
	z = x.Cross(y)

	// Twist the perpendicular pair about the bone axis. z is rebuilt from the
	// rolled x rather than rolled itself, so the basis stays exactly orthonormal
	// however the roll accumulates.
	//
	// The subtraction is what makes Roll a *right-handed* turn about +Y, the same
	// sense as mgl32.HomogRotate3D and as an animation channel's rotation about
	// the bone axis (model_pose.go). Adding instead would twist the deform basis
	// the opposite way from every other rotation in the codebase — and would
	// silently mis-sign modelBoneRotateApply (editor_model_rotate.go), which
	// already folds a right-handed twist straight into Roll.
	c := float32(math.Cos(float64(b.Roll)))
	s := float32(math.Sin(float64(b.Roll)))
	x = x.Mul(c).Sub(z.Mul(s)).Normalize()
	z = x.Cross(y)
	return x, y, z, length, true
}

// BoneMatrix returns bone bi's rest basis, or the identity for a bad index.
func (m *ModelDoc) BoneMatrix(bi int) mgl32.Mat4 {
	if !m.validBone(bi) {
		return mgl32.Ident4()
	}
	return boneMatrix(m.Bones[bi])
}

// boneDelta returns the transform taking a bone from its rest placement to its
// current one — now * rest⁻¹, the matrix that moves whatever the bone deforms.
//
// This is the same quantity an animated pose will produce; the only difference
// is where "now" comes from. That is deliberate: the skinning in model_skin.go
// takes deltas, so the editor's drag path and a future animation player feed it
// through the same door.
func boneDelta(rest, now ModelBone) mgl32.Mat4 {
	return boneMatrix(now).Mul4(boneMatrix(rest).Inv())
}

// BoneDeltas returns one delta per bone against the given rest pose. Bones with
// no counterpart in rest get the identity, so a skeleton that gained a bone
// mid-drag does not deform by a garbage transform.
func (m *ModelDoc) BoneDeltas(rest []ModelBone) []mgl32.Mat4 {
	out := make([]mgl32.Mat4, len(m.Bones))
	for i := range m.Bones {
		if i < len(rest) {
			out[i] = boneDelta(rest[i], m.Bones[i])
		} else {
			out[i] = mgl32.Ident4()
		}
	}
	return out
}

// ---------- geometry helpers ----------

// closestPointOnSegment3D returns the point on segment ab nearest p, and the
// distance to it.
func closestPointOnSegment3D(p, a, b mgl32.Vec3) (mgl32.Vec3, float32) {
	ab := b.Sub(a)
	denom := ab.Dot(ab)
	if denom < 1e-12 {
		return a, p.Sub(a).Len()
	}
	t := geom.ClampValue(p.Sub(a).Dot(ab)/denom, 0, 1)
	q := a.Add(ab.Mul(t))
	return q, p.Sub(q).Len()
}

// distToBone returns the distance from p to bone bi's segment.
func (m *ModelDoc) distToBone(p mgl32.Vec3, bi int) float32 {
	if !m.validBone(bi) {
		return float32(math.MaxFloat32)
	}
	_, d := closestPointOnSegment3D(p, m.Bones[bi].Head, m.Bones[bi].Tail)
	return d
}
