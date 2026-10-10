package editor

// editor_model_bone.go — selecting, dragging and drawing the armature.
//
// Bones are a fourth selection mode rather than a separate editor because the
// skeleton and the mesh are one document: assigning vertices to a bone needs
// both selections alive at once, and a second window would mean keeping two
// copies of the same model in step.
//
// That is also why the bone selection survives a mode switch. Switching between
// vertex, edge and face converts the mesh selection (modelSetSelMode); switching
// to or from Bone leaves the *other* half alone, so the workflow the panel is
// built around — pick a bone, switch to vertex mode, select points, assign —
// never loses what it is assigning to.
//
// Picking and the gizmo reuse the mesh path's viewport-parameterised helpers
// (modelViewport, modelProjView, modelGizmoBox) for the reasons given in the
// header of editor_model_pick.go.

import (
	"foundry/internal/geom"
	"foundry/internal/gizmo"
	"foundry/internal/model"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// modelBoneJoint is which end of a bone the gizmo moves.
type modelBoneJoint int

const (
	modelBoneWhole modelBoneJoint = iota
	modelBoneHead
	modelBoneTail
)

func (j modelBoneJoint) String() string {
	switch j {
	case modelBoneHead:
		return "Head"
	case modelBoneTail:
		return "Tail"
	default:
		return "Whole"
	}
}

// Editor-wide bone options.
var (
	// modelShowBones draws the armature over the mesh. On by default and
	// independent of the selection mode, so the skeleton stays visible while
	// the mesh it deforms is being edited.
	modelShowBones = true

	// modelBoneDeform makes a bone drag carry its assigned vertices with it.
	//
	// This is the difference between posing and editing the rest skeleton. With
	// it on, dragging a bone is a preview of the deform — which is the point of
	// assigning vertices at all. With it off, the bone moves alone, which is
	// what fitting a skeleton into an already-finished mesh needs.
	modelBoneDeform = true

	// modelAssignWeight is the influence the panel's Assign button hands out.
	modelAssignWeight = float32(1)
)

const (
	modelBoneJointPx = float32(4) // half-extent of a head/tail handle
	modelBonePickPx  = float32(9) // click slop for a bone body
)

var (
	modelBoneColor       = mgl32.Vec3{0.45, 0.56, 0.78}
	modelBoneDimColor    = mgl32.Vec3{0.30, 0.33, 0.42}
	modelBoneActiveColor = mgl32.Vec3{1, 0.88, 0.35}
	modelBoneParentColor = mgl32.Vec3{0.32, 0.34, 0.40}
	modelBoneMirrorColor = mgl32.Vec3{0.62, 0.50, 0.82}
)

// ---------- selection ----------

// modelActiveBone is the bone the panel's per-bone controls act on: the most
// recently clicked one. Returns -1 when nothing is selected.
func modelActiveBone() int {
	n := len(modelSelection.Bones)
	if modelDoc == nil || n == 0 {
		return -1
	}
	bi := modelSelection.Bones[n-1]
	if bi < 0 || bi >= len(modelDoc.Bones) {
		return -1
	}
	return bi
}

// modelBoneSelected reports whether bi is in the selection.
func modelBoneSelected(bi int) bool {
	for _, b := range modelSelection.Bones {
		if b == bi {
			return true
		}
	}
	return false
}

// modelSelectBone makes bi the active bone, replacing the selection unless
// additive.
func modelSelectBone(bi int, additive bool) {
	modelSelection.Bones = toggleInt(modelSelection.Bones, bi, additive)
}

// ---------- picking ----------

// modelBoneJointPos returns the world position of one end of a bone.
func modelBoneJointPos(bi int, joint modelBoneJoint) (mgl32.Vec3, bool) {
	// The *displayed* bone, so in pose mode the handles sit on the posed
	// skeleton rather than on the rest one underneath it.
	b, ok := modelDisplayBone(bi)
	if !ok {
		return mgl32.Vec3{}, false
	}
	switch joint {
	case modelBoneHead:
		return b.Head, true
	case modelBoneTail:
		return b.Tail, true
	default:
		return b.Head.Add(b.Tail).Mul(0.5), true
	}
}

// modelPickBoneJoint returns the head or tail handle nearest the cursor.
//
// Joints are tested before bodies because they sit *on* a body and are the
// smaller target: a click near the end of a bone almost always means the joint,
// and losing that to the body underneath would make a chain impossible to shape.
func modelPickBoneJoint(mx, my int32) (int, modelBoneJoint, bool) {
	w, h, ok := modelViewport()
	if !ok || modelDoc == nil {
		return 0, modelBoneWhole, false
	}
	projView := modelProjView()
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	camPos := modelCam.position()

	bestBone, bestJoint := -1, modelBoneWhole
	bestD := modelPickPx
	bestDepth := float32(math.MaxFloat32)
	for bi := range modelDisplayBones() {
		for _, joint := range []modelBoneJoint{modelBoneHead, modelBoneTail} {
			p, _ := modelBoneJointPos(bi, joint)
			sp, ok := projectToScreenIn(projView, p, w, h)
			if !ok {
				continue
			}
			d := sp.Sub(cursor).Len()
			if d > modelPickPx {
				continue
			}
			depth := p.Sub(camPos).Len()
			if d < bestD-1 || (d <= bestD+1 && depth < bestDepth) {
				bestBone, bestJoint, bestD, bestDepth = bi, joint, d, depth
			}
		}
	}
	return bestBone, bestJoint, bestBone >= 0
}

// modelPickBoneBody returns the bone whose screen-space segment the cursor is
// nearest, within modelBonePickPx.
func modelPickBoneBody(mx, my int32) (int, bool) {
	w, h, ok := modelViewport()
	if !ok || modelDoc == nil {
		return 0, false
	}
	projView := modelProjView()
	camPos := modelCam.position()

	best := -1
	bestD := modelBonePickPx
	bestDepth := float32(math.MaxFloat32)
	bones := modelDisplayBones()
	for bi := range bones {
		b := bones[bi]
		sa, okA := projectToScreenIn(projView, b.Head, w, h)
		sb, okB := projectToScreenIn(projView, b.Tail, w, h)
		if !okA || !okB {
			continue
		}
		d := distToSegment2D(float32(mx), float32(my), sa.X(), sa.Y(), sb.X(), sb.Y())
		if d > modelBonePickPx {
			continue
		}
		depth := b.Head.Add(b.Tail).Mul(0.5).Sub(camPos).Len()
		if d < bestD-1 || (d <= bestD+1 && depth < bestDepth) {
			best, bestD, bestDepth = bi, d, depth
		}
	}
	return best, best >= 0
}

// modelBoneClick resolves a left-click in bone mode. Returns true when it hit
// something.
func modelBoneClick(mx, my int32, additive bool) bool {
	if modelDoc == nil {
		return false
	}
	if bi, joint, ok := modelPickBoneJoint(mx, my); ok {
		modelSelection.BoneJoint = joint
		modelSelectBone(bi, additive)
		return true
	}
	if bi, ok := modelPickBoneBody(mx, my); ok {
		modelSelection.BoneJoint = modelBoneWhole
		modelSelectBone(bi, additive)
		return true
	}
	if !additive {
		modelClearBoneSelection()
	}
	return false
}

// ---------- the gizmo, in bone mode ----------

// modelBoneDrag is the state a bone drag is replayed from: the armature and the
// mesh as they stood when the drag began.
//
// The drag applies an *absolute* offset to this snapshot on every motion event
// rather than an incremental one to the live state. That is what makes grid
// snapping land on the grid instead of on multiples of whatever the mouse
// happened to do, and it is what lets the skinning below re-deform the original
// mesh each frame instead of deforming an already-deformed one and compounding
// the error.
var modelBoneDrag struct {
	Bones []model.ModelBone
	Verts []mgl32.Vec3
}

// modelBoneGizmoOrigin is where the translate gizmo sits in bone mode: the
// centroid of whichever joint the selection is pointed at.
func modelBoneGizmoOrigin() (mgl32.Vec3, bool) {
	if modelDoc == nil || len(modelSelection.Bones) == 0 {
		return mgl32.Vec3{}, false
	}
	var c mgl32.Vec3
	n := 0
	for _, bi := range modelSelection.Bones {
		if p, ok := modelBoneJointPos(bi, modelSelection.BoneJoint); ok {
			c = c.Add(p)
			n++
		}
	}
	if n == 0 {
		return mgl32.Vec3{}, false
	}
	return c.Mul(1 / float32(n)), true
}

// modelBoneGizmoCapture snapshots the rest state at mouse-down.
func modelBoneGizmoCapture() {
	if modelDoc == nil {
		return
	}
	modelBoneDrag.Bones = model.CloneBones(modelDoc.Bones)
	modelBoneDrag.Verts = append([]mgl32.Vec3(nil), modelDoc.Verts...)
}

// modelBoneGizmoApply moves the selected joints along one world axis by delta
// from the snapshot, then re-skins the mesh from that same snapshot.
//
// Deltas are computed for *every* bone, not just the dragged ones, so a
// connected child dragged along by its parent's tail deforms its own vertices
// too — otherwise a forearm would stay put while the elbow it hangs off moved.
func modelBoneGizmoApply(axis int, delta float32) {
	if modelDoc == nil || len(modelBoneDrag.Bones) == 0 {
		return
	}
	leads := modelBoneLeads()
	for _, bi := range leads {
		if bi < 0 || bi >= len(modelBoneDrag.Bones) || bi >= len(modelDoc.Bones) {
			continue
		}
		modelPlaceBoneJoint(bi, modelBoneDrag.Bones[bi], axis, delta)
	}
	// The partners are placed from the leads as they now stand, not offset from
	// the snapshot, so each motion event lands them afresh like the leads.
	modelMirrorBoneMove(leads)
	modelDoc.SyncConnectedBones()
	if modelBoneDeform {
		modelDoc.SkinFrom(modelBoneDrag.Bones, modelBoneDrag.Verts)
	}
}

// modelPlaceBoneJoint puts one bone where an axis drag of delta from rest takes
// it, snapping to the grid the way the mesh path does.
//
// A whole-bone move snaps the head and carries the tail by the same offset. The
// alternative — snapping both ends independently — would quietly change the
// bone's length every time it crossed a grid line, and a bone's length is the
// scale of everything it deforms.
func modelPlaceBoneJoint(bi int, rest model.ModelBone, axis int, delta float32) {
	switch modelSelection.BoneJoint {
	case modelBoneHead:
		modelDoc.SetBoneHead(bi, axisMoved(rest.Head, axis, delta))
	case modelBoneTail:
		modelDoc.SetBoneTail(bi, axisMoved(rest.Tail, axis, delta))
	default:
		head := axisMoved(rest.Head, axis, delta)
		// Set both ends absolutely rather than calling MoveBone, which is
		// relative and would compound across motion events.
		modelDoc.SetBoneHead(bi, head)
		modelDoc.Bones[bi].Tail = rest.Tail.Add(head.Sub(rest.Head))
	}
}

// modelBoneGizmoRelease drops the snapshot at mouse-up.
func modelBoneGizmoRelease() {
	modelBoneDrag.Bones = nil
	modelBoneDrag.Verts = nil
}

// modelNudgeBone offsets one bone the way the gizmo would, honouring which
// joint the selection is pointed at. The caller brackets it with modelBoneEdit.
func modelNudgeBone(bi, axis int, delta float32) {
	if modelDoc == nil || !modelDoc.ValidBone(bi) {
		return
	}
	modelPlaceBoneJoint(bi, modelDoc.Bones[bi], axis, delta)
}

// modelBoneEdit runs a bone mutation that did not come from the gizmo — a panel
// nudge, a numeric entry — and re-skins from the state just before it, so every
// path that moves a bone moves its vertices the same way.
func modelBoneEdit(fn func()) {
	if modelDoc == nil {
		return
	}
	rest := model.CloneBones(modelDoc.Bones)
	restVerts := append([]mgl32.Vec3(nil), modelDoc.Verts...)
	fn()
	if modelBoneDeform {
		modelDoc.SkinFrom(rest, restVerts)
	}
}

// ---------- drawing ----------

// drawModelBones draws the armature. Called from the depth-disabled overlay
// pass: a skeleton is inside the mesh it deforms, so depth-testing it would
// hide every bone that matters.
func drawModelBones() {
	if modelDoc == nil || !modelShowBones || len(modelDisplayBones()) == 0 {
		return
	}
	boneMode := modelSelection.Mode == modelSelBone
	active := modelActiveBone()
	var partners map[int]bool
	if boneMode && !modelPoseActive() {
		partners = modelBoneMirrorPartners()
	}

	bones := modelDisplayBones()
	for bi := range bones {
		b := bones[bi]

		col := modelBoneDimColor
		if boneMode {
			col = modelBoneColor
			switch {
			case bi == active:
				col = modelBoneActiveColor
			case modelBoneSelected(bi):
				col = modelSelColor
			case partners[bi]:
				// Not selected, but the next edit will move it.
				col = modelBoneMirrorColor
			}
		} else if modelBoneSelected(bi) {
			// Still worth marking outside bone mode: this is the bone the
			// panel's Assign button will bind the mesh selection to.
			col = modelSelColor
		}
		drawBoneOctahedron(b, col)

		// A non-connected child is drawn back to its parent's tail, so a
		// skeleton reads as a hierarchy rather than as loose sticks — unless it
		// grew from the parent's head, where the two already visibly meet and
		// the line would only retrace the parent.
		if !b.Connected && b.Parent >= 0 && b.Parent < len(bones) &&
			b.Head.Sub(bones[b.Parent].Head).Len() > 1e-5 {
			gizmo.GizmoLine(bones[b.Parent].Tail, b.Head, modelBoneParentColor)
		}

		if boneMode {
			modelGizmoBox(b.Head, modelBoneJointPx, col)
			modelGizmoBox(b.Tail, modelBoneJointPx, col)
		}
	}
}

// drawBoneOctahedron outlines one bone as the usual armature solid: a ring a
// tenth of the way along, spanned from the head and drawn out to the tail.
//
// The shape is worth the twelve lines a plain segment would not need — it shows
// which end is the head, how long the bone is and, through the ring, which way
// its roll has twisted the deform basis. All three matter when the same
// skeleton is later driven by animation data.
func drawBoneOctahedron(b model.ModelBone, col mgl32.Vec3) {
	length := b.Tail.Sub(b.Head).Len()
	if length < 1e-6 {
		return
	}
	basis := model.BoneMatrix(b)
	r := length * 0.1

	var ring [4]mgl32.Vec3
	for i, o := range [4]mgl32.Vec2{{1, 0}, {0, 1}, {-1, 0}, {0, -1}} {
		// Bone space: X and Z are unit, Y carries the length, so the ring sits
		// at a tenth of the bone with a radius a tenth of it.
		p := basis.Mul4x1(mgl32.Vec4{o.X() * r, 0.1, o.Y() * r, 1})
		ring[i] = mgl32.Vec3{p.X(), p.Y(), p.Z()}
	}
	for i := 0; i < 4; i++ {
		gizmo.GizmoLine(b.Head, ring[i], col)
		gizmo.GizmoLine(ring[i], ring[(i+1)%4], col)
		gizmo.GizmoLine(ring[i], b.Tail, col)
	}
}

// modelWeightColor is the blue-to-red ramp weight painting has used since
// forever: cold means barely influenced, hot means rigidly attached. Reusing
// the convention means the display needs no legend.
func modelWeightColor(w float32) mgl32.Vec3 {
	w = geom.ClampValue(w, 0, 1)
	stops := [4]mgl32.Vec3{
		{0.10, 0.20, 0.90}, // 0.00 blue
		{0.10, 0.85, 0.85}, // 0.33 cyan
		{0.20, 0.90, 0.20}, // 0.67 green
		{0.95, 0.20, 0.10}, // 1.00 red
	}
	t := w * 3
	i := int(t)
	if i >= 3 {
		return stops[3]
	}
	f := t - float32(i)
	return stops[i].Mul(1 - f).Add(stops[i+1].Mul(f))
}
