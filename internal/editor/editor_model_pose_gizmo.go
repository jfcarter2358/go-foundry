package editor

// editor_model_pose_gizmo.go — the three gizmos, in pose mode.
//
// Each tool already branches to a bone-specific apply when the selection is
// bones (modelBoneGizmoApply and friends, editor_model_bone.go); these are the
// pose-mode branch taken ahead of those. The difference is where the result
// lands: the rest-mode functions move ModelBone head/tail placements, these
// write ModelBonePose channels and leave the document alone.
//
// The absolute-from-snapshot rule carries over unchanged, and matters more here
// than anywhere else: composing a rotation into a quaternion on every motion
// event would drift the pose as the mouse moved, and the drift would then be
// baked into a keyframe.

import (
	"foundry/internal/model"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// modelPoseDrag is the pose as it stood at mouse-down. Every motion event
// applies to this, never to the live pose.
var modelPoseDrag struct {
	Pose model.ModelPose
	// Local is each selected bone's pose-space frame, captured once at
	// mouse-down. Recomputing it per event would chase the bone as it moved and
	// make the drag accelerate away from the cursor.
	Local map[int]mgl32.Mat4
}

// modelPoseGizmoCapture snapshots the pose at the start of a drag.
func modelPoseGizmoCapture() {
	modelPoseDrag.Pose = modelPose.Clone()
	modelPoseDrag.Local = map[int]mgl32.Mat4{}
	for _, bi := range modelSelection.Bones {
		if modelDoc != nil && modelDoc.ValidBone(bi) {
			modelPoseDrag.Local[bi] = modelPoseBoneLocal(bi)
		}
	}
}

// modelPoseGizmoRelease drops the snapshot.
func modelPoseGizmoRelease() {
	modelPoseDrag.Pose = model.ModelPose{}
	modelPoseDrag.Local = nil
}

// modelPoseDragBone returns bone bi's captured channels and frame.
func modelPoseDragBone(bi int) (model.ModelBonePose, mgl32.Mat4, bool) {
	local, ok := modelPoseDrag.Local[bi]
	if !ok {
		return model.ModelBonePose{}, mgl32.Ident4(), false
	}
	return modelPoseDrag.Pose.Bone(bi), local, true
}

// ---------- move ----------

// modelPoseMoveApply slides the selected bones along a world axis.
//
// The channel is in the bone's own space, so the world offset is pulled back
// through the bone's pose-space frame. Direction only (w = 0): the frame's
// translation is where the bone already is, and including it would add the
// bone's own position to every drag.
//
// A connected bone is skipped. Its head *is* its parent's tail — that is what
// Connected means — so translating it would pull the joint apart, and the same
// motion is available by translating the parent.
func modelPoseMoveApply(axis int, delta float32) {
	if modelDoc == nil || axis < 0 || axis > 2 {
		return
	}
	world := gizmoAxes[axis].Mul(gizmoSnap(delta))
	moved := false
	for _, bi := range modelSelection.Bones {
		rest, local, ok := modelPoseDragBone(bi)
		if !ok {
			continue
		}
		if modelDoc.Bones[bi].Connected {
			continue
		}
		rest.Trans = rest.Trans.Add(model.XformDir(local.Inv(), world))
		modelPose.Set(bi, rest)
		moved = true
	}
	if moved {
		modelPoseEvaluate()
	}
}

// modelPoseAnyMovable reports whether any selected bone can be translated, so
// the panel can say why a drag did nothing rather than leaving it silent.
func modelPoseAnyMovable() bool {
	if modelDoc == nil {
		return false
	}
	for _, bi := range modelSelection.Bones {
		if modelDoc.ValidBone(bi) && !modelDoc.Bones[bi].Connected {
			return true
		}
	}
	return false
}

// ---------- rotate ----------

// modelPoseRotateApply turns the selected bones about a world axis.
//
// Every bone pivots about its own head rather than about the selection's
// centroid. That is what a limb does — a shoulder swings the arm from the
// shoulder — and it is also what makes the maths exact: the head is the pose
// frame's origin, so the rotation composes straight into the channel with no
// translation term to correct for.
//
// Selecting a whole chain therefore curls it: each bone takes the full angle in
// its own frame *and* inherits its parent's. That is the same thing every
// armature editor does with a multi-bone selection, and it is the reason the
// captured frames are not refreshed mid-drag — they only decide which way each
// bone's axis points, never how far it turns.
func modelPoseRotateApply(worldAxis mgl32.Vec3, delta float32) {
	if modelDoc == nil {
		return
	}
	moved := false
	for _, bi := range modelSelection.Bones {
		rest, local, ok := modelPoseDragBone(bi)
		if !ok {
			continue
		}
		// The world axis, expressed in the frame the channel lives in.
		axis := model.XformDir(local.Inv(), worldAxis)
		if axis.Len() < 1e-6 {
			continue
		}
		// Pre-multiplied: the new turn happens in the bone's rest frame, so
		// dragging the X ring always means the same axis however the bone has
		// already been rotated. Post-multiplying would compound in the bone's
		// *current* frame and make the ring's meaning drift mid-drag.
		rest.Rot = mgl32.QuatRotate(delta, axis.Normalize()).Mul(rest.EffectiveRot()).Normalize()
		modelPose.Set(bi, rest)
		moved = true
	}
	if moved {
		modelPoseEvaluate()
	}
}

// ---------- scale ----------

// modelPoseScaleAxes returns the axes the scale gizmo shows in pose mode: the
// active bone's own X, Y and Z rather than the world's.
//
// Bone space is the only place scale means anything to an animator. Y is along
// the bone, so the Y arm stretches it and pushes whatever hangs off its tail
// out — the "placement and scale of their bones" this whole mode is for. World
// axes would give three handles whose effect depends on which way the limb
// happens to be pointing.
func modelPoseScaleAxes() ([3]mgl32.Vec3, bool) {
	bi := modelActiveBone()
	if !modelPoseActive() || modelSelection.Mode != modelSelBone || bi < 0 {
		return worldBasis(), false
	}
	b, ok := modelDisplayBone(bi)
	if !ok {
		return worldBasis(), false
	}
	x, y, z, _, valid := model.BoneBasisAxes(b)
	if !valid {
		return worldBasis(), false
	}
	return [3]mgl32.Vec3{x, y, z}, true
}

// modelPoseScaleApply stretches the selected bones along their own axes.
func modelPoseScaleApply(s mgl32.Vec3) {
	if modelDoc == nil {
		return
	}
	// A bone scaled to nothing has no direction left to deform along, and the
	// pose could not be recovered by dragging back out.
	const minScale = 0.01
	moved := false
	for _, bi := range modelSelection.Bones {
		rest, _, ok := modelPoseDragBone(bi)
		if !ok {
			continue
		}
		base := rest.EffectiveScale()
		rest.Scale = mgl32.Vec3{
			float32(math.Max(float64(base.X()*s.X()), minScale)),
			float32(math.Max(float64(base.Y()*s.Y()), minScale)),
			float32(math.Max(float64(base.Z()*s.Z()), minScale)),
		}
		modelPose.Set(bi, rest)
		moved = true
	}
	if moved {
		modelPoseEvaluate()
	}
}
