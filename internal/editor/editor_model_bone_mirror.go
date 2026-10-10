package editor

// editor_model_bone_mirror.go — mirrored bone placement.
//
// With mirror on, every edit that places a bone in the rest skeleton — a gizmo
// drag of any of the three tools, a nudge, a typed endpoint or roll — is made to
// the selected bones and then copied, reflected, onto their opposite-side
// partners. Pairs are found by name (model_bone_mirror.go), so a partner does
// not have to be selected, and bones with no side move alone.
//
// Pose mode is left out on purpose: a pose is a moment of an animation, and a
// walk cycle is exactly the thing that must *not* move both legs together.

import (
	"foundry/internal/model"

	"foundry/internal/imguibackend"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

var (
	// modelBoneMirror copies bone placement edits onto opposite-side partners.
	modelBoneMirror = false

	// modelBoneMirrorAxis is the model axis the mirror plane is normal to. X,
	// because a figure built facing down Z is symmetric left to right.
	modelBoneMirrorAxis = 0
)

var modelBoneMirrorAxisNames = []string{"X", "Y", "Z"}

// modelBoneLeads returns the selected bones an edit is applied to directly.
//
// With mirror on, a pair with both halves selected is represented once: moving
// both by the same offset would pull them the same way instead of mirroring
// them, and the half left out is placed by the mirror pass anyway. The active
// bone wins its pair, so the one the user last clicked is the one under their
// hand.
func modelBoneLeads() []int {
	if !modelBoneMirror || modelDoc == nil {
		return modelSelection.Bones
	}
	out := make([]int, 0, len(modelSelection.Bones))
	taken := make(map[int]bool, len(modelSelection.Bones))
	for i := len(modelSelection.Bones) - 1; i >= 0; i-- {
		bi := modelSelection.Bones[i]
		if taken[bi] {
			continue
		}
		if p := modelDoc.MirrorBone(bi); p >= 0 && taken[p] {
			continue
		}
		taken[bi] = true
		out = append(out, bi)
	}
	// Back into selection order, so the result reads like the selection it
	// came from.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// modelMirrorBoneLeads copies the given parts of each lead onto its partner.
// A no-op with mirror off.
func modelMirrorBoneLeads(leads []int, head, tail, roll bool) {
	if !modelBoneMirror || modelDoc == nil {
		return
	}
	for _, bi := range leads {
		if p := modelDoc.MirrorBone(bi); p >= 0 {
			modelDoc.MirrorBoneOnto(bi, p, modelBoneMirrorAxis, head, tail, roll)
		}
	}
}

// modelMirrorBoneMove mirrors a translate: only the joint the gizmo is pointed
// at, so dragging a tail does not also snap the partner's head across.
func modelMirrorBoneMove(leads []int) {
	switch modelSelection.BoneJoint {
	case modelBoneHead:
		modelMirrorBoneLeads(leads, true, false, false)
	case modelBoneTail:
		modelMirrorBoneLeads(leads, false, true, false)
	default:
		modelMirrorBoneLeads(leads, true, true, false)
	}
}

// modelBoneMirrorPartners returns the partners of the selected bones that are
// not selected themselves — the bones an edit will move without the user having
// picked them, which the viewport marks so that is never a surprise.
func modelBoneMirrorPartners() map[int]bool {
	if !modelBoneMirror || modelDoc == nil {
		return nil
	}
	out := map[int]bool{}
	for _, bi := range modelSelection.Bones {
		if p := modelDoc.MirrorBone(bi); p >= 0 && !modelBoneSelected(p) {
			out[p] = true
		}
	}
	return out
}

// modelBoneMirrorControls is the panel's mirror toggle and axis.
func modelBoneMirrorControls() {
	imgui.Checkbox("Mirror##modelbonemirror", &modelBoneMirror)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Placing a bone places its opposite-side partner too:\n" +
			"Arm.L and Arm.R, Leg_l and Leg_r, LeftHand and RightHand.\n" +
			"Add and Extrude make pairs while this is on. Pose mode is not mirrored.")
	}
	imgui.SameLine()
	axis := int32(modelBoneMirrorAxis)
	imgui.PushItemWidth(60)
	if imguibackend.Combo("Across##modelbonemirroraxis", &axis, modelBoneMirrorAxisNames) {
		modelBoneMirrorAxis = int(axis)
	}
	imgui.PopItemWidth()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The mirror plane passes through the model origin,\nacross this axis. .L is the positive side.")
	}
}

// ---------- extrude ----------

// modelExtrudeFromHead is where a bare E grows from: the head when the head
// handle is the grabbed joint, otherwise the tail.
func modelExtrudeFromHead() bool {
	return modelSelection.BoneJoint == modelBoneHead
}

// modelExtrudeBones grows a child from one end of each selected bone — and,
// with mirror on, from the same end of each partner — and leaves the new tails
// selected, so pressing E repeatedly walks a chain out and the gizmo drag in
// between places each joint.
func modelExtrudeBones(fromHead bool) {
	if modelDoc == nil || len(modelSelection.Bones) == 0 {
		return
	}
	ModelPushUndo()
	var added []int
	pairs := 0
	for _, bi := range modelBoneLeads() {
		child := -1
		if modelBoneMirror {
			var mirrored int
			child, mirrored = modelDoc.ExtrudeBoneMirrored(bi, fromHead)
			if mirrored >= 0 {
				pairs++
			}
		} else {
			child = modelDoc.ExtrudeBoneFrom(bi, fromHead)
		}
		if child >= 0 {
			added = append(added, child)
		}
	}
	if len(added) == 0 {
		modelUndoStack.pop()
		modelSetStatus("Nothing to extrude from")
		return
	}
	modelSelection.Mode = modelSelBone
	// Only the lead side is selected; its partner follows through the mirror.
	modelSelection.Bones = added
	// The tail is the end you place next; the head is pinned where it grew.
	modelSelection.BoneJoint = modelBoneTail
	end := "tail"
	if fromHead {
		end = "head"
	}
	if pairs > 0 {
		modelSetStatus("Extruded %d bone(s) from the %s, %d mirrored — drag an axis to place the tail", len(added), end, pairs)
		return
	}
	modelSetStatus("Extruded %d bone(s) from the %s — drag an axis to place the tail", len(added), end)
}

// modelExtrudeSelectedBones is E in bone mode: extrude from whichever end is
// grabbed.
func modelExtrudeSelectedBones() { modelExtrudeBones(modelExtrudeFromHead()) }

// modelExtrudeSelectedBonesFromHead is Shift+E in bone mode.
func modelExtrudeSelectedBonesFromHead() { modelExtrudeBones(true) }

// modelForkSelectedBones grows a mirrored .L/.R pair from the grabbed end of
// each selected bone that sits on the mirror plane.
//
// This is the one way to start a pair from the middle of a skeleton. Extruding
// a centre bone makes another centre bone — which is what a spine wants — so
// arms and legs need an operation of their own.
func modelForkSelectedBones() {
	if modelDoc == nil || len(modelSelection.Bones) == 0 {
		return
	}
	fromHead := modelExtrudeFromHead()
	ModelPushUndo()
	var added []int
	for _, bi := range modelSelection.Bones {
		if pos, _ := modelDoc.ForkBone(bi, fromHead, modelBoneMirrorAxis); pos >= 0 {
			added = append(added, pos)
		}
	}
	if len(added) == 0 {
		modelUndoStack.pop()
		modelSetStatus("Cannot fork: the joint is not on the %s mirror plane",
			modelBoneMirrorAxisNames[modelBoneMirrorAxis])
		return
	}
	modelSelection.Mode = modelSelBone
	modelSelection.Bones = added
	modelSelection.BoneJoint = modelBoneTail
	if !modelBoneMirror {
		// Forking is only half useful with mirror off — the pair would come
		// apart the first time either half was dragged — so turn it on.
		modelBoneMirror = true
		modelSetStatus("Forked %d mirrored pair(s); mirror turned on", len(added))
		return
	}
	modelSetStatus("Forked %d mirrored pair(s) — drag an axis to place them", len(added))
}

// modelAddBoneAt adds a root bone, and its mirror image when mirror is on and
// the bone is off the plane. Returns the bone at the given endpoints.
func modelAddBoneAt(head, tail mgl32.Vec3) int {
	if modelBoneMirror {
		bi, _ := modelDoc.AddBonePair(head, tail, modelBoneMirrorAxis)
		return bi
	}
	return modelDoc.AddBone(head, tail)
}

// modelBoneForkable reports whether the joint of bone bi a fork would grow from
// sits on the mirror plane.
func modelBoneForkable(bi int) bool {
	if modelDoc == nil || !modelDoc.ValidBone(bi) {
		return false
	}
	b := modelDoc.Bones[bi]
	joint := b.Tail
	if modelExtrudeFromHead() {
		joint = b.Head
	}
	return model.MirrorBonePoint(joint, modelBoneMirrorAxis).Sub(joint).Len() < 2e-4
}
