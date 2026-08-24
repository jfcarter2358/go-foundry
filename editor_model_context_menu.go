package main

// editor_model_context_menu.go — the model editor's right-click menu.
//
// The menu is the selection's own toolbox: what it offers is decided entirely
// by what is selected, so a face selection is asked about extrudes and
// textures, a bone about parenting and weights, and a pose about keyframes.
// The panel on the right is still the place to *type numbers*; this is the
// place to reach an action without crossing the window to find its button.
//
// The rows are ctxMenuItems and the popup is drawn by ctxMenuFrame
// (editor_context_menu.go), which the 3D editor's menu shares — the two
// viewports offer different actions, but a menu that looked or behaved
// differently between them would be its own bug. What lives here is only the
// half that is about a mesh: which rows a vertex, edge, face, bone or pose
// selection gets.
//
// Every row's Do calls into editor_model_actions.go rather than editing the
// document here, so the menu and the panel cannot disagree about what an
// action does or whether it is undoable.

import (
	"fmt"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// modelCtxPopupID names the popup. It is "##"-prefixed because a popup's name
// is also its title, and this one has no title bar to show it in.
const modelCtxPopupID = "##modelctxmenu"

// modelRightPress is this viewport's right-button gesture.
var modelRightPress ctxGesture

// ---------- input glue ----------

// modelContextPressed arms the gesture at a right-button press on the viewport.
func modelContextPressed(mx, my int32) { modelRightPress.press(mx, my) }

// modelContextMoved is fed every motion event; travel makes the gesture a look.
func modelContextMoved(mx, my int32) { modelRightPress.motion(mx, my) }

// modelContextReleased ends the gesture, opening the menu when it was a click.
// The window's event path has nothing to do with the answer — a look and a
// click both simply end here — but returning it is what lets a test say which
// of the two the gesture was read as.
func modelContextReleased(mx, my int32) bool {
	if !modelRightPress.release() {
		return false
	}
	modelOpenContextMenu(mx, my)
	return true
}

// modelOpenContextMenu queues the menu at (mx, my).
//
// A click on empty air with nothing selected would open a menu of greyed-out
// rows, so the click first tries to select what it landed on. It only does so
// when nothing is selected: right-clicking beside a careful multi-face
// selection must not throw it away.
func modelOpenContextMenu(mx, my int32) {
	if modelDoc == nil {
		return
	}
	if !modelHasSelection() {
		modelEditorClick(mx, my, false)
	}
	modelRightPress.open(mx, my)
}

// ---------- rendering ----------

// modelContextMenu draws the popup. Called once per frame from the window's
// Draw, after the panels, so its rows are built from the same state they show.
func modelContextMenu() {
	if modelDoc == nil {
		return
	}
	ctxMenuFrame(modelCtxPopupID, &modelRightPress, modelContextMenuItems)
}

// ---------- the menu ----------

// modelContextMenuItems builds the menu for the current selection.
//
// The shape is the same whatever is selected: a heading saying what the menu is
// about, the actions that belong to *this* kind of selection, then the blocks
// every selection shares — what to select, which tool, and undo. Keeping the
// shared blocks in a fixed place is what lets muscle memory work across modes.
func modelContextMenuItems() []ctxMenuItem {
	if modelDoc == nil {
		return nil
	}
	items := []ctxMenuItem{{Label: modelContextHeading()}, {Separator: true}}
	items = append(items, modelDocMenuItems()...)
	items = append(items, ctxMenuItem{Separator: true})

	switch {
	case modelPoseActive():
		// Pose mode forces bone selection, and the mesh underneath is a
		// deformation of the document rather than the document — so the rest
		// bone edits are not on offer here, only the pose ones.
		items = append(items, modelPoseMenuItems()...)
	case modelSelection.Mode == modelSelBone:
		items = append(items, modelBoneMenuItems()...)
	case modelSelection.Mode == modelSelFace:
		items = append(items, modelFaceMenuItems()...)
		items = append(items, ctxMenuItem{Separator: true})
		items = append(items, modelWeightMenuItems()...)
	default:
		items = append(items, modelWeightMenuItems()...)
	}

	items = append(items, ctxMenuItem{Separator: true})
	items = append(items, modelSelectionMenuItems()...)
	items = append(items, ctxMenuItem{Separator: true})
	items = append(items, modelViewMenuItems()...)
	items = append(items, ctxMenuItem{Separator: true})
	items = append(items,
		ctxMenuItem{Label: "Undo", Shortcut: "Ctrl+Z", Disabled: !ModelCanUndo(), Do: ModelUndo},
		ctxMenuItem{Label: "Redo", Shortcut: "Ctrl+Shift+Z", Disabled: !ModelCanRedo(), Do: ModelRedo},
	)
	return items
}

// modelContextHeading is the caption at the top: what the menu is about.
func modelContextHeading() string {
	if modelPoseActive() {
		return fmt.Sprintf("Pose — %s", modelSelectionSummary())
	}
	return fmt.Sprintf("%s select — %s", modelSelection.Mode, modelSelectionSummary())
}

// ---------- faces ----------

func modelFaceMenuItems() []ctxMenuItem {
	faces := modelSelectedFaces()
	none := len(faces) == 0
	smooth := modelSelectedFaceSmooth()

	return []ctxMenuItem{
		{
			Label: "Extrude", Shortcut: "E", Disabled: none,
			Tooltip: "Push the selected faces along their shared normal,\nthen drag the gizmo to set the depth.",
			Do:      modelExtrudeAgain,
		},
		{Label: "Subdivide", Disabled: none,
			Tooltip: "Midpoint split. The surface does not move.",
			Do:      modelSubdivideSelectedFaces},
		{Label: "Delete", Shortcut: "Del", Disabled: none, Do: modelDeleteSelectedFaces},
		{Separator: true},
		{Label: "Smooth", Checked: smooth, Disabled: none,
			Do: func() { modelSetSelectedFaceSmooth(!smooth) }},
		{Label: "UV", Disabled: none, Sub: []ctxMenuItem{
			{Label: "Unwrap (world scale)", Do: func() { modelUnwrapSelectedFaces(false) }},
			{Label: "Unwrap to 0..1", Do: func() { modelUnwrapSelectedFaces(true) }},
			{Separator: true},
			{Label: "Flip U", Do: func() { modelFlipSelectedFaceUV(true, false) }},
			{Label: "Flip V", Do: func() { modelFlipSelectedFaceUV(false, true) }},
		}},
	}
}

// modelTextureMenuItems is the texture list, ticked at the one the model wears.
func modelTextureMenuItems() []ctxMenuItem {
	return ctxTextureMenuItems(modelTexture(), modelSetTexture)
}

// modelDocMenuItems are the whole-document rows. They sit above the
// selection-specific block and are never disabled, because the texture a model
// wears and the atlas it is unwrapped onto belong to the document — asking for
// either has nothing to do with what happens to be selected.
func modelDocMenuItems() []ctxMenuItem {
	return []ctxMenuItem{
		{Label: "Texture", Sub: modelTextureMenuItems(),
			Tooltip: "The one image the whole model is painted with."},
		{Label: "Unwrap && Paint...", Do: modelOpenUnwrapDialog,
			Tooltip: "Flatten the whole mesh into one texture and open it\nin the texture editor. The model updates as you paint."},
	}
}

// ---------- weights ----------

// modelWeightMenuItems is the vertex/edge/face half of rigging: bind what is
// selected to a bone. It is offered in every mesh mode because the points a
// bone moves are just as often chosen as a face or an edge loop as one by one.
func modelWeightMenuItems() []ctxMenuItem {
	verts := modelSelectedMeshVerts()
	noVerts := len(verts) == 0
	noBones := len(modelDoc.Bones) == 0

	assign := make([]ctxMenuItem, 0, len(modelDoc.Bones)+2)
	assign = append(assign,
		ctxMenuItem{Custom: modelAssignWeightSlider},
		ctxMenuItem{Separator: true},
	)
	remove := make([]ctxMenuItem, 0, len(modelDoc.Bones))
	for _, bi := range modelBoneDisplayOrder() {
		bone := bi
		name := modelDoc.Bones[bi].Name
		assign = append(assign, ctxMenuItem{
			Label: name,
			Do:    func() { modelAssignSelectedToBone(bone) },
		})
		remove = append(remove, ctxMenuItem{
			Label:    name,
			Disabled: modelBoneWeightedCount(bone) == 0,
			Do:       func() { modelRemoveSelectedFromBone(bone) },
		})
	}

	return []ctxMenuItem{
		{Label: fmt.Sprintf("Assign %d point(s) to bone", len(verts)),
			Disabled: noVerts || noBones, Sub: assign,
			Tooltip: "Bind the selected points to a bone at the weight above,\nso posing that bone moves them."},
		{Label: "Remove from bone", Disabled: noVerts || noBones, Sub: remove},
		{Label: "Clear weights", Disabled: noVerts, Do: modelClearSelectedWeights,
			Tooltip: "Unbind the selected points from every bone."},
		{Label: "Auto weights (whole mesh)", Disabled: noBones, Do: modelAutoWeightAll,
			Tooltip: "Bind every point to the bones nearest it — a distance\nheuristic, worth checking where limbs pass close."},
	}
}

// modelAssignWeightSlider is the weight the assign rows below it hand out. It
// sits inside the submenu rather than beside it because it is only ever read on
// the way to clicking one of them.
func modelAssignWeightSlider() {
	imgui.PushItemWidth(110)
	editorDragFloat("Weight##modelctxweight", &modelAssignWeight, 0.01, 1, "%.2f")
	imgui.PopItemWidth()
}

// ---------- bones ----------

func modelBoneMenuItems() []ctxMenuItem {
	active := modelActiveBone()
	noBone := active < 0
	name := ""
	connected := false
	hasParent := false
	if !noBone {
		b := modelDoc.Bones[active]
		name, connected, hasParent = b.Name, b.Connected, b.Parent >= 0
	}
	verts := modelSelectedMeshVerts()

	joints := make([]ctxMenuItem, 0, 3)
	for _, j := range []modelBoneJoint{modelBoneWhole, modelBoneHead, modelBoneTail} {
		joint := j
		joints = append(joints, ctxMenuItem{
			Label:   joint.String(),
			Checked: modelSelection.BoneJoint == joint,
			Do:      func() { modelSelection.BoneJoint = joint },
		})
	}

	items := []ctxMenuItem{
		{Label: "Add bone", Do: modelAddBone,
			Tooltip: "A new root bone where the camera is looking."},
		{Label: "Extrude bone", Shortcut: "E", Disabled: noBone, Do: modelExtrudeSelectedBones,
			Tooltip: "A connected child from this bone's tail."},
		{Label: "Delete bone(s)", Shortcut: "Del", Disabled: noBone, Do: modelDeleteSelectedBones,
			Tooltip: "Children are kept and reparented, not deleted with it."},
		{Separator: true},
		{Label: "Parent to", Disabled: noBone, Sub: modelBoneParentMenuItems(active)},
		{Label: "Connected to parent", Checked: connected, Disabled: noBone || !hasParent,
			Do:      func() { modelSetBoneConnected(active, !connected) },
			Tooltip: "The head is welded to the parent's tail, so the joint\nbetween them moves as one."},
		{Label: "Gizmo moves", Disabled: noBone, Sub: joints},
		{Separator: true},
	}

	assignLabel := "Assign selected points"
	selectLabel := "Select this bone's points"
	if !noBone {
		assignLabel = fmt.Sprintf("Assign %d point(s) to %s", len(verts), name)
		selectLabel = fmt.Sprintf("Select %s's %d point(s)", name, modelBoneWeightedCount(active))
	}
	items = append(items,
		ctxMenuItem{Label: assignLabel, Disabled: noBone || len(verts) == 0,
			Do:      func() { modelAssignSelectedToBone(active) },
			Tooltip: "Weight above is set in the vertex, edge and face menus."},
		ctxMenuItem{Label: "Remove selected points", Disabled: noBone || len(verts) == 0,
			Do: func() { modelRemoveSelectedFromBone(active) }},
		ctxMenuItem{Label: selectLabel, Disabled: noBone,
			Do: func() { modelSelectBoneVerts(active) }},
		ctxMenuItem{Label: "Auto weights (whole mesh)", Disabled: len(modelDoc.Bones) == 0,
			Do: modelAutoWeightAll},
		ctxMenuItem{Separator: true},
		ctxMenuItem{Label: "Move assigned points", Checked: modelBoneDeform,
			Do:      func() { modelBoneDeform = !modelBoneDeform },
			Tooltip: "On: dragging a bone drags the points weighted to it.\nOff: the bone moves alone."},
		ctxMenuItem{Label: "Show armature", Checked: modelShowBones,
			Do: func() { modelShowBones = !modelShowBones }},
		ctxMenuItem{Label: "Pose mode", Shortcut: "P", Checked: modelPoseMode,
			Disabled: len(modelDoc.Bones) == 0, Do: modelTogglePoseMode},
	)
	return items
}

// modelBoneParentMenuItems lists the bones bi could hang off. Descendants are
// listed too and refused by SetBoneParent, the same as the panel's combo — see
// modelBoneParentCombo for why filtering them out is not worth a second pass.
func modelBoneParentMenuItems(bi int) []ctxMenuItem {
	if modelDoc == nil || !modelDoc.validBone(bi) {
		return nil
	}
	parent := modelDoc.Bones[bi].Parent
	items := []ctxMenuItem{{
		Label:   "(root)",
		Checked: parent < 0,
		Do:      func() { modelReparentBone(bi, modelBoneNoParent) },
	}}
	for _, other := range modelBoneDisplayOrder() {
		if other == bi {
			continue
		}
		to := other
		items = append(items, ctxMenuItem{
			Label:   modelDoc.Bones[other].Name,
			Checked: parent == other,
			Do:      func() { modelReparentBone(bi, to) },
		})
	}
	return items
}

// ---------- pose ----------

func modelPoseMenuItems() []ctxMenuItem {
	noBone := len(modelSelection.Bones) == 0
	noClip := modelActiveClip() == nil
	playLabel := "Play"
	if modelPosePlaying {
		playLabel = "Pause"
	}

	return []ctxMenuItem{
		{Label: "Key pose", Shortcut: "I", Disabled: noClip, Do: modelKeyPose,
			Tooltip: "Keys every bone that has moved off its rest placement."},
		{Label: "Key selected bone(s)", Disabled: noClip || noBone, Do: func() {
			ModelPushUndo()
			n := modelKeyBones(modelSelection.Bones)
			modelSetStatus("Keyed %d bone(s) at frame %d", n, modelPoseFrame())
		}},
		{Label: "Delete key", Disabled: noClip, Do: func() { modelDeleteSelectedKeys() }},
		{Label: playLabel, Shortcut: "Space", Disabled: noClip, Do: modelTogglePlayback},
		{Separator: true},
		{Label: "Clear translation", Shortcut: "Alt+G",
			Do: func() { modelPoseClearSelected(true, false, false, "translation") }},
		{Label: "Clear rotation", Shortcut: "Alt+R",
			Do: func() { modelPoseClearSelected(false, true, false, "rotation") }},
		{Label: "Clear scale", Shortcut: "Alt+S",
			Do: func() { modelPoseClearSelected(false, false, true, "scale") }},
		{Label: "Reset selected bone(s)", Disabled: noBone, Do: func() {
			ModelPushUndo()
			modelPoseResetBones(modelSelection.Bones)
		}},
		{Label: "Reset whole pose", Do: func() {
			ModelPushUndo()
			modelPoseReset()
			modelSetStatus("Pose reset to rest")
		}},
		{Separator: true},
		{Label: "Leave pose mode", Shortcut: "P", Do: modelTogglePoseMode,
			Tooltip: "Back to the rest document, which posing never wrote to."},
	}
}

// ---------- shared blocks ----------

func modelSelectionMenuItems() []ctxMenuItem {
	modes := make([]ctxMenuItem, 0, 4)
	for i, m := range []modelSelMode{modelSelVertex, modelSelEdge, modelSelFace, modelSelBone} {
		mode := m
		modes = append(modes, ctxMenuItem{
			Label:    mode.String(),
			Shortcut: fmt.Sprintf("%d", i+1),
			Checked:  modelSelection.Mode == mode,
			// Locked while posing: the mesh tools edit the rest document, and
			// dragging a vertex on a deformed mesh would put it somewhere it
			// does not end up.
			Disabled: modelPoseActive(),
			Do:       func() { modelSetSelMode(mode) },
		})
	}
	return []ctxMenuItem{
		{Label: "Select all", Shortcut: "Ctrl+A", Do: modelSelectAll},
		{Label: "Deselect all", Disabled: !modelHasSelection(), Do: modelDeselectAll},
		{Label: "Select mode", Sub: modes,
			Tooltip: "Vertex, edge and face are three views of one mesh —\nswitching converts the selection rather than dropping it."},
	}
}

func modelViewMenuItems() []ctxMenuItem {
	tools := make([]ctxMenuItem, 0, len(modelGizmoTools)+2)
	for _, t := range modelGizmoTools {
		tool := t
		tools = append(tools, ctxMenuItem{
			Label:    tool.String(),
			Shortcut: modelToolKey(tool),
			Checked:  modelGizmoTool == tool,
			Do:       func() { modelSetGizmoTool(tool) },
		})
	}
	tools = append(tools,
		ctxMenuItem{Separator: true},
		ctxMenuItem{Label: "Scale on the selection's own axes", Shortcut: "C",
			Checked: modelScaleLocal,
			Do:      func() { modelSetScaleLocal(!modelScaleLocal) }},
	)

	add := make([]ctxMenuItem, 0, len(modelPrimitiveKinds)+2)
	for _, k := range modelPrimitiveKinds {
		kind := k
		add = append(add, ctxMenuItem{
			Label: kind.String(),
			Do:    func() { modelAddPrimitive(kind) },
		})
	}
	add = append(add,
		ctxMenuItem{Separator: true},
		ctxMenuItem{Label: "Bone", Do: modelAddBone},
	)

	return []ctxMenuItem{
		{Label: "Transform tool", Sub: tools},
		{Label: "Wireframe", Shortcut: "Z", Checked: modelWireframe,
			Do:      func() { modelSetWireframe(!modelWireframe) },
			Tooltip: "Solid hides the mesh's far side, and hidden points and\nedges cannot be clicked. Wireframe shows all of it."},
		{Label: "Frame model", Shortcut: "F", Do: func() { modelCam.frame(modelDoc) }},
		{Label: "Add", Sub: add,
			Tooltip: "Adds to the current model, where the camera is looking."},
	}
}
