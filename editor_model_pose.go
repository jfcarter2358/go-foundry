package main

// editor_model_pose.go — pose mode.
//
// Pose mode is a *document* mode, not a fourth selection mode: vertex, edge,
// face and bone all edit the rest document, and pose mode edits a transform laid
// over it. Keeping them on separate axes is what lets bone selection and the
// bone panel keep working unchanged while posing.
//
// ---------- modelDoc always holds the rest state ----------
//
// The posed mesh and the posed armature live here, in modelPoseVerts and
// modelPoseBones, and the document is never written to by posing. That is the
// whole design:
//
//   - Save writes the rest mesh without having to know pose mode exists.
//   - Undo snapshots keep working (the pose rides alongside — see
//     editor_model_undo.go).
//   - Every mesh op stays legal, because the mesh it would edit is still the one
//     on disk.
//
// The cost is that the handful of places that draw or pick the mesh have to ask
// for the *display* arrays rather than reading modelDoc directly. There are six,
// they are all listed against modelDisplayVerts/modelDisplayBones below, and the
// alternative — writing the pose into the document and restoring it on the way
// out — puts the user's file one crash away from being a posed mesh.
//
// Deformation here is CPU (skinVerts, model_skin.go). The runtime skins on the
// GPU instead (model_skin_gpu.go); the editor's copy is the one that has to hand
// ordinary []mgl32.Vec3 back to the wireframe and the picking code, and at one
// mesh and editor framerates there is nothing to gain from the hardware path.

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

var (
	// modelPoseMode is on when the viewport is showing a pose rather than the
	// rest document.
	modelPoseMode bool

	// modelPose is the pose being edited: what the gizmos write and what "Key
	// Pose" reads.
	modelPose ModelPose

	// The evaluated pose. Rebuilt by modelPoseEvaluate whenever the pose, the
	// armature or the mesh changes.
	modelPoseBones []ModelBone
	modelPoseVerts []mgl32.Vec3

	// modelPoseClip is the clip being authored, an index into
	// modelDoc.Animations, or -1.
	modelPoseClip = -1

	// modelPoseTime is the playhead, in seconds.
	modelPoseTime float32

	// modelPosePlaying runs the playhead from the frame clock.
	modelPosePlaying bool
)

// modelSetPoseMode enters or leaves pose mode.
//
// Entering forces bone selection: the mesh selection tools edit the rest
// document, and letting them run while the viewport shows a deformed mesh would
// mean dragging a vertex to a place it does not end up.
func modelSetPoseMode(on bool) {
	if modelPoseMode == on {
		return
	}
	// A drag half-finished in one mode must not be completed in the other.
	modelGizmoMouseUp()
	modelPoseMode = on
	if !on {
		modelPosePlaying = false
		// The document never changed, but the GPU buffers hold the posed mesh;
		// ask for a rebuild from the rest vertices.
		if modelDoc != nil {
			modelDoc.touch()
		}
		return
	}
	if modelDoc != nil {
		modelPose.resize(len(modelDoc.Bones))
		if modelPoseClip < 0 && len(modelDoc.Animations) > 0 {
			modelPoseClip = 0
		}
	}
	modelSetSelMode(modelSelBone)
	modelPoseEvaluate()
}

// modelPoseActive reports whether the viewport is showing a pose. Guards every
// display accessor, so a document with no armature cannot end up in a mode with
// nothing to drive it.
func modelPoseActive() bool {
	return modelPoseMode && modelDoc != nil && len(modelDoc.Bones) > 0
}

// modelPoseEvaluate rebuilds the posed armature and the posed mesh, and asks the
// renderer for a fresh upload.
//
// Everything downstream of a pose edit funnels through here — the gizmos, the
// panel's buttons, playback and scrubbing alike — so there is exactly one path
// from "the pose changed" to "the screen shows it".
func modelPoseEvaluate() {
	if !modelPoseActive() {
		modelPoseBones, modelPoseVerts = nil, nil
		return
	}
	modelPose.resize(len(modelDoc.Bones))
	deltas := modelDoc.PoseDeltas(modelPose)
	modelPoseBones = modelDoc.PosedBones(deltas)

	// Start from the rest mesh, then deform the bound vertices into it. The
	// copy is what leaves the unrigged parts of a partly-bound model where they
	// belong instead of at the origin.
	if len(modelPoseVerts) != len(modelDoc.Verts) {
		modelPoseVerts = make([]mgl32.Vec3, len(modelDoc.Verts))
	}
	copy(modelPoseVerts, modelDoc.Verts)
	modelDoc.ensureWeights()
	skinVerts(deltas, modelDoc.Weights, modelDoc.Verts, modelPoseVerts)

	// touch, not invalidate: the topology is the document's, unchanged. Only the
	// positions the GPU holds are stale.
	//
	// This makes playback recompile and re-upload the submeshes every frame,
	// which is more work than rewriting the positions in place would be
	// (modelSubmesh.SrcVerts exists to make that possible). It is deliberate:
	// the full rebuild also recomputes smooth normals from the deformed mesh,
	// and a position-only fast path would light a posed model off its rest
	// normals. At one model and editor framerates the rebuild is not worth
	// trading correct shading for; if a heavy character ever makes it matter,
	// the fast path has to skin the normal buffer too.
	modelDoc.touch()
}

// ---------- what the viewport reads ----------
//
// These two are the seam between "the document" and "what is on screen". The
// call sites that must go through them are the ones that draw or pick the mesh
// and the armature:
//
//	drawModelBones, modelBoneJointPos, modelPickBoneJoint, modelPickBoneBody
//	    (editor_model_bone.go)
//	drawModelWireframe, modelEditorRebuild (editor_model_render.go)
//
// Everything else — save, undo, every mesh op, UV editing — deliberately keeps
// reading modelDoc, because the rest document is what those act on.

// modelDisplayBones returns the armature as the viewport should show it.
func modelDisplayBones() []ModelBone {
	if modelPoseActive() && len(modelPoseBones) == len(modelDoc.Bones) {
		return modelPoseBones
	}
	if modelDoc == nil {
		return nil
	}
	return modelDoc.Bones
}

// modelDisplayVerts returns the mesh as the viewport should show it.
func modelDisplayVerts() []mgl32.Vec3 {
	if modelPoseActive() && len(modelPoseVerts) == len(modelDoc.Verts) {
		return modelPoseVerts
	}
	if modelDoc == nil {
		return nil
	}
	return modelDoc.Verts
}

// modelDisplayBone returns one posed bone.
func modelDisplayBone(bi int) (ModelBone, bool) {
	bones := modelDisplayBones()
	if bi < 0 || bi >= len(bones) {
		return ModelBone{}, false
	}
	return bones[bi], true
}

// ---------- posing ----------

// modelPoseBoneLocal returns the matrix taking bone bi's pose channels into
// model space: its parent's posed space composed with its own rest offset.
//
// This is the frame a gizmo has to convert into. A world-space drag means
// nothing to a channel expressed in the bone's own space, and converting through
// the bone's rest basis alone would ignore everything its parents are doing.
func modelPoseBoneLocal(bi int) mgl32.Mat4 {
	if modelDoc == nil || !modelDoc.validBone(bi) {
		return mgl32.Ident4()
	}
	rest := boneRestBasis(modelDoc.Bones[bi])
	parent := modelDoc.Bones[bi].Parent
	if !modelDoc.validBone(parent) {
		return rest
	}
	posed := modelDoc.PoseMatrices(modelPose)
	if parent >= len(posed) {
		return rest
	}
	return posed[parent].Mul4(boneRestBasis(modelDoc.Bones[parent]).Inv().Mul4(rest))
}

// modelPoseSet writes one bone's channels and re-evaluates.
func modelPoseSet(bi int, bp ModelBonePose) {
	if modelDoc == nil || !modelDoc.validBone(bi) {
		return
	}
	modelPose.set(bi, bp)
	modelPoseEvaluate()
}

// modelPoseReset returns the whole armature to rest.
func modelPoseReset() {
	if modelDoc == nil {
		return
	}
	modelPose = newModelPose(len(modelDoc.Bones))
	modelPoseEvaluate()
}

// modelPoseResetBones returns the given bones to rest, leaving the others posed.
func modelPoseResetBones(bones []int) {
	for _, bi := range bones {
		modelPose.set(bi, identBonePose())
	}
	modelPoseEvaluate()
}

// modelPoseClearChannel drops one channel on the given bones — the Alt+G/R/S
// gesture, which is how a pose gets un-picked one axis at a time rather than all
// at once.
func modelPoseClearChannel(bones []int, trans, rot, scale bool) {
	for _, bi := range bones {
		bp := modelPose.bone(bi)
		if trans {
			bp.Trans = mgl32.Vec3{}
		}
		if rot {
			bp.Rot = mgl32.QuatIdent()
		}
		if scale {
			bp.Scale = mgl32.Vec3{1, 1, 1}
		}
		modelPose.set(bi, bp)
	}
	modelPoseEvaluate()
}

// ---------- the clip being authored ----------

// modelActiveClip returns the clip the timeline is showing, or nil.
func modelActiveClip() *ModelClip {
	if modelDoc == nil {
		return nil
	}
	return modelDoc.Clip(modelPoseClip)
}

// modelPoseSetClip switches the timeline to another clip, dropping a keyframe
// selection that belonged to the old one.
func modelPoseSetClip(i int) {
	if modelPoseClip == i {
		return
	}
	modelPoseClip = i
	modelClearKeySelection()
	modelPoseSetTime(0)
}

// modelPoseSampleClip puts the pose where the playhead says it should be. This
// is the only path that moves the playhead, so scrubbing and playback cannot
// disagree about what a given time looks like.
func modelPoseSampleClip() {
	c := modelActiveClip()
	if c == nil || modelDoc == nil {
		return
	}
	c.SampleInto(&modelPose, len(modelDoc.Bones), modelPoseTime)
	modelPoseEvaluate()
}

// modelPoseSetTime moves the playhead and re-samples.
func modelPoseSetTime(t float32) {
	c := modelActiveClip()
	if c == nil {
		modelPoseTime = 0
		return
	}
	modelPoseTime = clampValue(t, 0, c.duration())
	modelPoseSampleClip()
}

// modelPoseFrame returns the playhead as a frame number on the clip's grid.
func modelPoseFrame() int {
	c := modelActiveClip()
	if c == nil {
		return 0
	}
	return int(modelPoseTime*c.fps() + 0.5)
}

// modelPoseSetFrame parks the playhead on a whole frame. Keys are placed at
// frame times, so an animator who scrubs and keys lands on the grid rather than
// a hair off it.
func modelPoseSetFrame(frame int) {
	c := modelActiveClip()
	if c == nil {
		return
	}
	if frame < 0 {
		frame = 0
	}
	modelPoseSetTime(float32(frame) / c.fps())
}

// modelPoseStepFrame nudges the playhead by whole frames, wrapping on a looping
// clip so stepping back from frame 0 lands on the end.
func modelPoseStepFrame(delta int) {
	c := modelActiveClip()
	if c == nil {
		return
	}
	n := c.frameCount()
	f := modelPoseFrame() + delta
	switch {
	case f < 0:
		if c.Loop {
			f = n
		} else {
			f = 0
		}
	case f > n:
		if c.Loop {
			f = 0
		} else {
			f = n
		}
	}
	modelPoseSetFrame(f)
}

// modelPoseTick advances playback by one frame's worth of wall time. Called from
// the editor window's Draw, which is where DeltaTime is in hand.
func modelPoseTick(dt float32) {
	if !modelPosePlaying || !modelPoseActive() {
		return
	}
	c := modelActiveClip()
	if c == nil {
		modelPosePlaying = false
		return
	}
	modelPoseTime += dt
	if d := c.duration(); modelPoseTime >= d {
		if c.Loop {
			modelPoseTime = c.wrapClipTime(modelPoseTime)
		} else {
			modelPoseTime = d
			modelPosePlaying = false
		}
	}
	modelPoseSampleClip()
}

// ---------- keying ----------

// modelKeyBones keys the given bones at the playhead, snapped to the clip's
// frame grid. Returns how many were keyed.
//
// Keying at the frame rather than at the raw playhead time is what keeps a
// re-key on the frame you are parked on replacing the key that is there —
// SetKey's epsilon is half a frame at 120fps, far tighter than the drift a
// scrubbed float accumulates.
func modelKeyBones(bones []int) int {
	c := modelActiveClip()
	if c == nil || modelDoc == nil {
		return 0
	}
	t := float32(modelPoseFrame()) / c.fps()
	n := 0
	for _, bi := range bones {
		if !modelDoc.validBone(bi) {
			continue
		}
		c.SetKey(bi, t, modelPose.bone(bi))
		n++
	}
	return n
}

// modelPosedBoneIndices lists the bones that have moved off rest — what "Key
// Pose" keys. Keying every bone instead would fill the timeline with tracks
// nobody touched and make the clip far harder to edit later.
func modelPosedBoneIndices() []int {
	var out []int
	for bi := range modelPose.Bones {
		if !modelPose.Bones[bi].isIdentity() {
			out = append(out, bi)
		}
	}
	return out
}

// modelKeyedBoneIndices lists the bones the active clip drives, ascending —
// the timeline's lanes.
func modelKeyedBoneIndices() []int {
	c := modelActiveClip()
	if c == nil {
		return nil
	}
	out := make([]int, 0, len(c.Tracks))
	for i := range c.Tracks {
		out = append(out, c.Tracks[i].Bone)
	}
	sortInts(out)
	return out
}

// sortInts sorts a small index slice in place. Insertion sort because these are
// bone counts, and it keeps the file free of a sort import for one call.
func sortInts(v []int) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

// ---------- keys ----------

// modelPoseHandleKey owns the keys that only mean something while posing.
// Returns true when it consumed the event.
//
// It runs ahead of the general model-editor switch because every one of these
// is taken by something else outside pose mode: I is free but Space, the arrows
// and Alt+G/R/S are not, and letting Alt+R fall through would swap the gizmo
// tool instead of clearing a rotation.
func modelPoseHandleKey(t sdl.KeyboardEvent, mods sdl.Keymod) bool {
	if mods&sdl.KMOD_ALT != 0 {
		switch t.Keysym.Scancode {
		case sdl.SCANCODE_G:
			modelPoseClearSelected(true, false, false, "translation")
			return true
		case sdl.SCANCODE_R:
			modelPoseClearSelected(false, true, false, "rotation")
			return true
		case sdl.SCANCODE_S:
			modelPoseClearSelected(false, false, true, "scale")
			return true
		}
	}
	switch t.Keysym.Scancode {
	case sdl.SCANCODE_DELETE:
		// Delete means "remove the selected keyframes" here. Outside pose mode
		// it deletes the selected *bones*, and since pose mode forces bone
		// selection, letting it fall through would quietly tear up the rig when
		// the user meant to drop a key.
		modelDeleteSelectedKeys()
		return true
	case sdl.SCANCODE_I:
		modelKeyPose()
		return true
	case sdl.SCANCODE_SPACE:
		modelTogglePlayback()
		return true
	case sdl.SCANCODE_LEFT:
		modelPosePlaying = false
		modelPoseStepFrame(-1)
		return true
	case sdl.SCANCODE_RIGHT:
		modelPosePlaying = false
		modelPoseStepFrame(1)
		return true
	}
	return false
}

// modelPoseClearSelected drops one channel on the selected bones, or on the
// whole armature when nothing is selected — the same "no selection means all"
// rule the delete-key path uses.
func modelPoseClearSelected(trans, rot, scale bool, what string) {
	targets := modelSelection.Bones
	if len(targets) == 0 && modelDoc != nil {
		targets = make([]int, len(modelDoc.Bones))
		for i := range targets {
			targets[i] = i
		}
	}
	if len(targets) == 0 {
		return
	}
	ModelPushUndo()
	modelPoseClearChannel(targets, trans, rot, scale)
	modelSetStatus("Cleared %s on %d bone(s)", what, len(targets))
}
