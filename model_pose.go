package main

// model_pose.go — posing the armature.
//
// A pose is not a second copy of the skeleton. It is one local transform per
// bone, expressed in that bone's own rest space and composed down the hierarchy,
// so rotating a shoulder carries the elbow and the hand with it. Storing posed
// head/tail placements instead would give no propagation at all, and would make
// a rotating limb *shorten* between keyframes: interpolating two endpoint
// positions cuts across the arc the bone actually sweeps.
//
// ---------- why this does not use boneMatrix ----------
//
// boneMatrix (model_bone.go) folds the bone's length into its Y scale, which is
// exactly right for the editor's rest-drag path: `boneDelta` compares two rest
// placements, and a length difference between them *is* a stretch the mesh
// should follow.
//
// It is wrong for pose composition. Applying a rotation R inside a basis carrying
// a non-uniform scale S produces S·R·S⁻¹ — a shear, not a rotation — so a posed
// limb would skew instead of swinging. Pose channels therefore live in
// boneRestBasis: the same origin, direction and roll, with no length scale. The
// bone's length does not vanish; it moves into its children's local offsets,
// which is what makes a pose scale along Y stretch the bone *and* push whatever
// hangs off its tail out with it.
//
// ---------- the zero value ----------
//
// mgl32.Quat's zero value is {W: 0, V: 0} — not the identity. A zero-valued
// ModelBonePose would therefore annihilate whatever it multiplied, so
// ModelBonePose.matrix treats a zero rotation and a zero scale as identity and
// every constructor here fills identities explicitly. Anything that grows a pose
// must go through newModelPose or ModelPose.resize.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// boneRestBasis maps pose space to model space: origin at the head, orthonormal,
// +Y along the bone. This is boneMatrix without the length in the Y scale — see
// the file header for why the difference matters.
func boneRestBasis(b ModelBone) mgl32.Mat4 {
	x, y, z, _, ok := boneBasisAxes(b)
	if !ok {
		return mgl32.Translate3D(b.Head.X(), b.Head.Y(), b.Head.Z())
	}
	h := b.Head
	return mgl32.Mat4{
		x[0], x[1], x[2], 0,
		y[0], y[1], y[2], 0,
		z[0], z[1], z[2], 0,
		h[0], h[1], h[2], 1,
	}
}

// ---------- the pose ----------

// ModelBonePose is one bone's departure from its rest placement, in the bone's
// own rest space (boneRestBasis). Trans slides the bone, Rot swings it about its
// head, and Scale stretches what it deforms — Y along the bone's length, which
// is the axis an animator reaches for.
type ModelBonePose struct {
	Trans mgl32.Vec3
	Rot   mgl32.Quat
	Scale mgl32.Vec3
}

// identBonePose is the rest placement: no offset, no rotation, no stretch.
func identBonePose() ModelBonePose {
	return ModelBonePose{Rot: mgl32.QuatIdent(), Scale: mgl32.Vec3{1, 1, 1}}
}

// isIdentity reports whether the bone is at rest. Drives what the .fmdl writer
// omits and which bones "Key Pose" bothers to key.
func (p ModelBonePose) isIdentity() bool {
	const eps = 1e-6
	if p.Trans.Len() > eps {
		return false
	}
	if s := p.scale(); mgl32.Abs(s.X()-1) > eps || mgl32.Abs(s.Y()-1) > eps || mgl32.Abs(s.Z()-1) > eps {
		return false
	}
	// A quaternion and its negation are the same rotation, so compare |w| rather
	// than w: a pose that accumulated a turn past 2pi is still the identity.
	return mgl32.Abs(mgl32.Abs(p.rot().W)-1) <= eps
}

// rot returns the rotation, reading a zero value as the identity.
func (p ModelBonePose) rot() mgl32.Quat {
	if p.Rot.W == 0 && p.Rot.V == (mgl32.Vec3{}) {
		return mgl32.QuatIdent()
	}
	return p.Rot
}

// scale returns the scale, reading a zero value as no stretch.
func (p ModelBonePose) scale() mgl32.Vec3 {
	if p.Scale == (mgl32.Vec3{}) {
		return mgl32.Vec3{1, 1, 1}
	}
	return p.Scale
}

// normalized returns the pose with its zero-value fields filled in and its
// rotation renormalised — the form everything downstream can assume.
func (p ModelBonePose) normalized() ModelBonePose {
	return ModelBonePose{Trans: p.Trans, Rot: p.rot().Normalize(), Scale: p.scale()}
}

// matrix is the local transform: translate, then rotate about the head, then
// scale. Scale is innermost so stretching does not drag the bone off its joint.
func (p ModelBonePose) matrix() mgl32.Mat4 {
	s := p.scale()
	return mgl32.Translate3D(p.Trans.X(), p.Trans.Y(), p.Trans.Z()).
		Mul4(p.rot().Mat4()).
		Mul4(mgl32.Scale3D(s.X(), s.Y(), s.Z()))
}

// ModelPose is a whole armature's departure from rest, index-parallel to
// ModelDoc.Bones.
type ModelPose struct{ Bones []ModelBonePose }

// newModelPose returns a rest pose for an n-bone armature.
func newModelPose(n int) ModelPose {
	p := ModelPose{Bones: make([]ModelBonePose, n)}
	for i := range p.Bones {
		p.Bones[i] = identBonePose()
	}
	return p
}

// clone deep-copies the pose. ModelBonePose holds no slices, so copying the
// backing array is a complete snapshot.
func (p ModelPose) clone() ModelPose {
	if p.Bones == nil {
		return ModelPose{}
	}
	return ModelPose{Bones: append([]ModelBonePose(nil), p.Bones...)}
}

// resize grows or shrinks the pose to match an armature of n bones, filling any
// new entries with the identity rather than the zero value.
func (p *ModelPose) resize(n int) {
	if len(p.Bones) == n {
		return
	}
	if len(p.Bones) > n {
		p.Bones = p.Bones[:n]
		return
	}
	grown := make([]ModelBonePose, n)
	copy(grown, p.Bones)
	for i := len(p.Bones); i < n; i++ {
		grown[i] = identBonePose()
	}
	p.Bones = grown
}

// isRest reports whether every bone is at its rest placement.
func (p ModelPose) isRest() bool {
	for _, b := range p.Bones {
		if !b.isIdentity() {
			return false
		}
	}
	return true
}

// bone returns bone bi's channels, or the identity when the pose is shorter than
// the armature — which happens for a frame or two whenever a bone is added.
func (p ModelPose) bone(bi int) ModelBonePose {
	if bi < 0 || bi >= len(p.Bones) {
		return identBonePose()
	}
	return p.Bones[bi]
}

// set writes bone bi's channels, growing the pose if it has fallen behind the
// armature.
func (p *ModelPose) set(bi int, bp ModelBonePose) {
	if bi < 0 {
		return
	}
	if bi >= len(p.Bones) {
		p.resize(bi + 1)
	}
	p.Bones[bi] = bp
}

// ---------- evaluation ----------

// PoseMatrices returns one model-space matrix per bone: where the pose puts that
// bone's space.
//
//	restLocal[i] = boneRestBasis(parent)⁻¹ · boneRestBasis(i)
//	posed[i]     = posed[parent] · restLocal[i] · pose[i]
//
// The walk is boneOrder()'s, because ModelDoc.Bones is deliberately not sorted
// parents-first and a child evaluated before its parent would compose against a
// matrix that is not there yet. Entries start as the identity so a hand-edited
// file containing a cycle — which validation forbids but cannot un-write —
// degrades to an unposed bone instead of collapsing the mesh to a point.
func (m *ModelDoc) PoseMatrices(p ModelPose) []mgl32.Mat4 {
	n := len(m.Bones)
	posed := make([]mgl32.Mat4, n)
	rest := make([]mgl32.Mat4, n)
	for i := range m.Bones {
		posed[i] = mgl32.Ident4()
		rest[i] = boneRestBasis(m.Bones[i])
	}
	for _, bi := range m.boneOrder() {
		parent := m.Bones[bi].Parent
		local := rest[bi]
		base := mgl32.Ident4()
		if m.validBone(parent) {
			local = rest[parent].Inv().Mul4(rest[bi])
			base = posed[parent]
		}
		posed[bi] = base.Mul4(local).Mul4(p.bone(bi).matrix())
	}
	return posed
}

// PoseDeltas returns the linear-blend-skinning palette: the transform taking
// each bone's rest space to where the pose puts it.
//
// This is the same quantity boneDelta produces for a rest-skeleton drag, so it
// feeds skinVerts (model_skin.go) through the same door — as that file's header
// promised it would.
func (m *ModelDoc) PoseDeltas(p ModelPose) []mgl32.Mat4 {
	posed := m.PoseMatrices(p)
	out := make([]mgl32.Mat4, len(posed))
	for i := range posed {
		out[i] = posed[i].Mul4(boneRestBasis(m.Bones[i]).Inv())
	}
	return out
}

// PosedBones returns the armature as the deltas place it, for the overlay and
// for bone picking. Connected joints stay welded without any explicit sync: a
// connected child's head sits on its parent's tail in the parent's rest space,
// so composing through the parent carries it along.
func (m *ModelDoc) PosedBones(deltas []mgl32.Mat4) []ModelBone {
	out := cloneBones(m.Bones)
	for i := range out {
		if i >= len(deltas) {
			break
		}
		d := deltas[i]
		out[i].Head = xformPoint(d, m.Bones[i].Head)
		out[i].Tail = xformPoint(d, m.Bones[i].Tail)
		out[i].Roll = posedRoll(m.Bones[i], d, out[i].Head, out[i].Tail)
	}
	return out
}

// xformPoint applies a 4x4 to a position (w = 1).
func xformPoint(m mgl32.Mat4, p mgl32.Vec3) mgl32.Vec3 {
	v := m.Mul4x1(mgl32.Vec4{p.X(), p.Y(), p.Z(), 1})
	return mgl32.Vec3{v.X(), v.Y(), v.Z()}
}

// xformDir applies a 4x4 to a direction (w = 0), so translation is ignored.
func xformDir(m mgl32.Mat4, v mgl32.Vec3) mgl32.Vec3 {
	r := m.Mul4x1(mgl32.Vec4{v.X(), v.Y(), v.Z(), 0})
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// posedRoll recovers the twist of a bone after its delta has been applied.
//
// The head/tail pair captures every part of the transform except the rotation
// about the bone's own axis — the one thing Roll exists to record, and the one
// the overlay's octahedron ring is drawn to show. Measuring the transformed rest
// X axis against the untwisted basis of the new direction recovers it exactly.
func posedRoll(rest ModelBone, d mgl32.Mat4, head, tail mgl32.Vec3) float32 {
	dir := tail.Sub(head)
	if dir.Len() < 1e-6 {
		return rest.Roll
	}
	dir = dir.Normalize()

	restX, _, _, _, ok := boneBasisAxes(rest)
	if !ok {
		return rest.Roll
	}
	x := xformDir(d, restX)
	if x.Len() < 1e-6 {
		return rest.Roll
	}
	x = x.Normalize()

	// The untwisted pair boneBasisAxes would build for this direction, before any
	// roll: u from perpAxis, w = u x dir. It writes a roll of r as
	// u*cos(r) - w*sin(r) (right-handed about +Y — see boneBasisAxes), so the
	// angle back out is an atan2 carrying the same negation.
	u := perpAxis(dir)
	if u.Len() < 1e-6 {
		u = mgl32.Vec3{1, 0, 0}
	}
	u = u.Normalize()
	w := u.Cross(dir)
	return float32(math.Atan2(float64(-x.Dot(w)), float64(x.Dot(u))))
}
