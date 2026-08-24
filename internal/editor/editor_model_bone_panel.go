package editor

// editor_model_bone_panel.go — the Bones section of the model editor panel, and
// the actions behind it.
//
// Kept out of editor_model_panel.go because it is the one section that is not a
// property sheet: it owns a hierarchy list, a weight-assignment workflow that
// reads *both* halves of the selection, and the undo bracketing for every bone
// mutation the editor can perform.
//
// Every mutation here goes through modelBoneEdit (editor_model_bone.go) rather
// than touching the document directly, so a bone moved from the panel carries
// its assigned vertices exactly the way a bone moved with the gizmo does.

import (
	"fmt"
	"foundry/internal/imguibackend"
	"foundry/internal/model"
	"math"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// modelBoneDisplayOrder returns bone indices depth-first from the roots, so the
// panel's list reads as a hierarchy. boneOrder() is not usable here: it sorts by
// depth, which is right for a topological walk but would list every root, then
// every child of any root, rather than putting each child under its parent.
func modelBoneDisplayOrder() []int {
	if modelDoc == nil {
		return nil
	}
	out := make([]int, 0, len(modelDoc.Bones))
	visited := make([]bool, len(modelDoc.Bones))

	var walk func(bi int)
	walk = func(bi int) {
		if visited[bi] {
			return
		}
		visited[bi] = true
		out = append(out, bi)
		for _, c := range modelDoc.BoneChildren(bi) {
			walk(c)
		}
	}
	for bi := range modelDoc.Bones {
		if !modelDoc.ValidBone(modelDoc.Bones[bi].Parent) {
			walk(bi)
		}
	}
	// Anything left is in a cycle, which validation forbids but a document
	// built by a future op could still hit. Listing them beats hiding them.
	for bi := range modelDoc.Bones {
		walk(bi)
	}
	return out
}

// ---------- actions ----------

// modelAddBone drops a root bone where new geometry lands, pointing up. Up
// because a skeleton is nearly always built along a standing figure, and
// because it gives extrude a direction to continue from on the first press.
func modelAddBone() {
	if modelDoc == nil {
		return
	}
	head := modelAddPosition()
	length := float32(1)
	if step := Editor.StepSize; Editor.SnapToGrid && step > 0 {
		length = step * float32(math.Max(1, math.Round(float64(1/step))))
	}
	ModelPushUndo()
	bi := modelDoc.AddBone(head, head.Add(mgl32.Vec3{0, length, 0}))
	if bi < 0 {
		modelUndoStack.pop()
		return
	}
	modelSelection.Mode = modelSelBone
	modelSelection.BoneJoint = modelBoneTail
	modelSelection.Bones = []int{bi}
	modelSetStatus("Added %s", modelDoc.Bones[bi].Name)
}

// modelExtrudeSelectedBones grows a connected child from each selected bone and
// leaves the new tails selected, so pressing E repeatedly walks a chain out and
// the gizmo drag in between places each joint.
func modelExtrudeSelectedBones() {
	if modelDoc == nil || len(modelSelection.Bones) == 0 {
		return
	}
	ModelPushUndo()
	var added []int
	for _, bi := range modelSelection.Bones {
		if child := modelDoc.ExtrudeBone(bi); child >= 0 {
			added = append(added, child)
		}
	}
	if len(added) == 0 {
		modelUndoStack.pop()
		modelSetStatus("Nothing to extrude from")
		return
	}
	modelSelection.Mode = modelSelBone
	modelSelection.Bones = added
	// The tail is the end you place next; the head is pinned to the parent.
	modelSelection.BoneJoint = modelBoneTail
	modelSetStatus("Extruded %d bone(s) — drag an axis to place the tail", len(added))
}

// modelDeleteSelectedBones removes the selected bones, keeping their children.
func modelDeleteSelectedBones() {
	if modelDoc == nil || len(modelSelection.Bones) == 0 {
		return
	}
	ModelPushUndo()
	n := len(modelSelection.Bones)
	modelDoc.DeleteBones(modelSelection.Bones)
	modelClearBoneSelection()
	modelSetStatus("Deleted %d bone(s)", n)
}

// ---------- the panel section ----------

func modelBoneSection() {
	if !imgui.CollapsingHeader("Bones##modelbones") {
		return
	}
	active := modelActiveBone()

	if imgui.Button("Add##modelboneadd") {
		modelAddBone()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A new root bone where the camera is looking.")
	}
	imgui.SameLine()

	imguibackend.EditorPushDisabled(active < 0)
	if imgui.Button("Extrude##modelboneext") {
		modelExtrudeSelectedBones()
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("E in Bone mode — a connected child from this bone's tail.\nDrag the gizmo afterwards to place it.")
	}
	imgui.SameLine()

	imguibackend.EditorPushDisabled(active < 0)
	if imgui.Button("Delete##modelbonedel") {
		modelDeleteSelectedBones()
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Children are kept and reparented, not deleted with it.")
	}

	imgui.Checkbox("Show armature##modelboneshow", &modelShowBones)
	imgui.Checkbox("Move assigned points##modelbonedeform", &modelBoneDeform)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("On: dragging a bone drags the points weighted to it.\nOff: the bone moves alone — for fitting a skeleton\ninto a mesh that is already finished.")
	}

	modelBoneList()

	if active >= 0 {
		imgui.Separator()
		modelBoneProperties(active)
	}

	imgui.Separator()
	modelBoneAssignment(active)
}

// modelBoneList draws the hierarchy. Clicking a row makes that bone active
// without changing the selection mode: picking the bone to assign to is
// something you do *while* a mesh selection is live.
func modelBoneList() {
	if len(modelDoc.Bones) == 0 {
		imgui.Text("No bones yet. Add one, then press E")
		imgui.Text("in Bone mode [4] to grow a chain.")
		return
	}
	for _, bi := range modelBoneDisplayOrder() {
		b := modelDoc.Bones[bi]
		prefix := strings.Repeat("  ", modelDoc.BoneDepth(bi))
		joiner := "  "
		if b.Parent >= 0 && !b.Connected {
			// Marks a child that hangs off its parent rather than continuing
			// it, which is otherwise only visible out in the viewport.
			joiner = "~ "
		} else if b.Parent >= 0 {
			joiner = "- "
		}
		label := fmt.Sprintf("%s%s%s##modelbonerow%d", prefix, joiner, b.Name, bi)
		if imgui.SelectableV(label, modelBoneSelected(bi), 0, imgui.Vec2{}) {
			modelSelectBone(bi, sdl.GetModState()&sdl.KMOD_CTRL != 0)
		}
	}
}

// modelBoneProperties is the per-bone sheet: name, parenting, roll and the two
// endpoints as numbers.
func modelBoneProperties(bi int) {
	b := &modelDoc.Bones[bi]

	name := b.Name
	if imgui.InputTextV("Name##modelbonename", &name,
		imgui.InputTextFlagsEnterReturnsTrue|imgui.InputTextFlagsAutoSelectAll, nil) {
		// On Enter rather than on every keystroke: renaming per character would
		// push an undo entry per character, and the uniqueness check would
		// suffix half-typed names.
		ModelPushUndo()
		modelDoc.RenameBone(bi, name)
	}

	modelBoneParentCombo(bi)

	connected := b.Connected
	imguibackend.EditorPushDisabled(b.Parent < 0)
	if imgui.Checkbox("Connected##modelboneconn", &connected) {
		modelSetBoneConnected(bi, connected)
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The head is welded to the parent's tail, so the\njoint between them moves as one.")
	}

	rollDeg := b.Roll * 180 / math.Pi
	if editorDragFloat("Roll (deg)##modelboneroll", &rollDeg, -360, 360, "%.1f") {
		ModelPushUndo()
		modelBoneEdit(func() { modelDoc.SetBoneRoll(bi, rollDeg*math.Pi/180) })
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Twists the bone's deform basis about its own axis.\nDecides which way a rotation will bend it.")
	}

	head := [3]float32{b.Head.X(), b.Head.Y(), b.Head.Z()}
	if editorDragFloat3("Head##modelbonehead", &head, 0, 0, "%.3f") {
		ModelPushUndo()
		modelBoneEdit(func() { modelDoc.SetBoneHead(bi, mgl32.Vec3{head[0], head[1], head[2]}) })
	}
	tail := [3]float32{b.Tail.X(), b.Tail.Y(), b.Tail.Z()}
	if editorDragFloat3("Tail##modelbonetail", &tail, 0, 0, "%.3f") {
		ModelPushUndo()
		modelBoneEdit(func() { modelDoc.SetBoneTail(bi, mgl32.Vec3{tail[0], tail[1], tail[2]}) })
	}
	imgui.Text(fmt.Sprintf("Length: %.3f", modelDoc.BoneLength(bi)))

	// Which end the gizmo grabs. Clicking a head or tail handle in the viewport
	// sets this too; the buttons are here for when the handles overlap on
	// screen and the one you want is behind the other.
	imgui.Text("Gizmo moves:")
	for _, joint := range []modelBoneJoint{modelBoneWhole, modelBoneHead, modelBoneTail} {
		imgui.SameLine()
		if imgui.RadioButton(fmt.Sprintf("%s##modelbonejoint%d", joint, int(joint)),
			modelSelection.BoneJoint == joint) {
			modelSelection.BoneJoint = joint
		}
	}
}

// modelBoneParentCombo reparents bi. Descendants are offered but refused by
// SetBoneParent; listing them greyed out would need a second pass for no gain,
// so the combo simply reports the refusal in the status line.
func modelBoneParentCombo(bi int) {
	current := "(root)"
	if p := modelDoc.Bones[bi].Parent; modelDoc.ValidBone(p) {
		current = modelDoc.Bones[p].Name
	}
	if !imgui.BeginCombo("Parent##modelboneparent", current) {
		return
	}
	if imgui.SelectableV("(root)", modelDoc.Bones[bi].Parent < 0, 0, imgui.Vec2{}) {
		modelReparentBone(bi, model.ModelBoneNoParent)
	}
	for other := range modelDoc.Bones {
		if other == bi {
			continue
		}
		selected := modelDoc.Bones[bi].Parent == other
		if !imgui.SelectableV(modelDoc.Bones[other].Name+"##modelboneparent", selected, 0, imgui.Vec2{}) {
			continue
		}
		modelReparentBone(bi, other)
	}
	imgui.EndCombo()
}

// modelBoneAssignment binds mesh vertices to the active bone.
//
// It reads the mesh selection through modelSelectedMeshVerts rather than
// modelSelectedVerts so it keeps working in Bone mode, where the gizmo's notion
// of "selected vertices" is deliberately empty.
func modelBoneAssignment(active int) {
	imgui.Text("Assign points")
	if len(modelDoc.Bones) == 0 {
		imgui.Text("Add a bone first.")
		return
	}

	verts := modelSelectedMeshVerts()
	imgui.Text(fmt.Sprintf("%d point(s) selected", len(verts)))
	if active >= 0 {
		imgui.Text(fmt.Sprintf("%s moves %d point(s)",
			modelDoc.Bones[active].Name, len(modelDoc.VertsBoundTo(active))))
	} else {
		imgui.Text("No active bone.")
	}

	editorDragFloat("Weight##modelboneweight", &modelAssignWeight, 0.01, 1, "%.2f")
	if imgui.IsItemHovered() {
		imgui.SetTooltip("1.0 binds the points rigidly to this bone.\nLess shares them with whatever else already claims them.")
	}

	imguibackend.EditorPushDisabled(active < 0 || len(verts) == 0)
	if imgui.Button("Assign##modelboneassign") {
		modelAssignSelectedToBone(active)
	}
	imgui.SameLine()
	if imgui.Button("Remove##modelboneunassign") {
		modelRemoveSelectedFromBone(active)
	}
	imgui.SameLine()
	if imgui.Button("Clear##modelboneclearw") {
		modelClearSelectedWeights()
	}
	imguibackend.EditorPopDisabled()

	imguibackend.EditorPushDisabled(active < 0)
	if imgui.Button("Select assigned##modelbonesel") {
		modelSelectBoneVerts(active)
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	if imgui.Button("Auto weights##modelboneauto") {
		modelAutoWeightAll()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Bind every point to the bones nearest it.\nA distance heuristic — check limbs that pass close\ntogether, and fix them with Assign.")
	}
}

func modelAllVertIndices() []int {
	out := make([]int, len(modelDoc.Verts))
	for i := range out {
		out[i] = i
	}
	return out
}
