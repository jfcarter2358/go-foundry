package main

// editor_model_actions.go — the model editor's selection-scoped operations.
//
// Every entry here is "do this to whatever is selected": the panel's buttons
// and the viewport's right-click menu (editor_model_context_menu.go) are two
// front ends onto the same list, and this file is what stops them drifting
// apart. A face op reachable from one and not the other, or one that pushes an
// undo entry in the panel but not in the menu, is the bug this file exists to
// make impossible.
//
// The rules every action here follows:
//
//   - it guards its own preconditions, because a menu row is clickable in
//     states a disabled panel button is not;
//   - it brackets its own undo with ModelPushUndo, and pops the entry again
//     when the op turns out to be a no-op;
//   - it reports what it did through modelSetStatus, since the menu closes on
//     click and the status line is the only trace left behind.

// ---------- selection ----------

// modelSelectAll selects everything in the current mode. Bone mode selects the
// whole armature rather than the whole mesh: the mode decides what "all" means.
func modelSelectAll() {
	if modelDoc == nil {
		return
	}
	switch modelSelection.Mode {
	case modelSelVertex:
		modelSelection.Verts = modelAllVertIndices()
	case modelSelEdge:
		modelSelection.Edges = append([]edgeKey(nil), modelDoc.Edges()...)
	case modelSelFace:
		modelSelection.Faces = modelAllFaceIndices()
	case modelSelBone:
		bones := make([]int, len(modelDoc.Bones))
		for i := range bones {
			bones[i] = i
		}
		modelSelection.Bones = bones
	}
	modelSetStatus("Selected %s", modelSelectionSummary())
}

// modelDeselectAll drops the current mode's half of the selection, leaving the
// other half alone — the same split modelClearMeshSelection and
// modelClearBoneSelection keep, and for the same reason: a bone picked to
// assign to must survive clearing the points that were assigned, and vice
// versa.
func modelDeselectAll() {
	if modelSelection.Mode == modelSelBone {
		modelClearBoneSelection()
		return
	}
	modelClearMeshSelection()
}

// modelHasSelection reports whether the current mode has anything selected.
func modelHasSelection() bool {
	switch modelSelection.Mode {
	case modelSelVertex:
		return len(modelSelection.Verts) > 0
	case modelSelEdge:
		return len(modelSelection.Edges) > 0
	case modelSelFace:
		return len(modelSelection.Faces) > 0
	default:
		return len(modelSelection.Bones) > 0
	}
}

// ---------- faces ----------

// modelSelectedFaces is the face selection, or nil in any other mode. Every
// face op below is gated on it, so none of them can act on a stale face list
// left behind by a mode switch.
func modelSelectedFaces() []int {
	if modelDoc == nil || modelSelection.Mode != modelSelFace {
		return nil
	}
	return modelSelection.Faces
}

func modelSetSelectedFaceTexture(tex string) {
	faces := modelSelectedFaces()
	if len(faces) == 0 {
		return
	}
	_ = ensureTextureLoaded(tex)
	ModelPushUndo()
	modelDoc.SetFaceTexture(faces, tex)
	modelSetStatus("Textured %d faces with %s", len(faces), tex)
}

func modelSetSelectedFaceSmooth(smooth bool) {
	faces := modelSelectedFaces()
	if len(faces) == 0 {
		return
	}
	ModelPushUndo()
	modelDoc.SetFaceSmooth(faces, smooth)
	if smooth {
		modelSetStatus("Smoothed %d faces", len(faces))
	} else {
		modelSetStatus("Flat-shaded %d faces", len(faces))
	}
}

// modelSelectedFaceSmooth reports the shading of the first selected face, which
// is what the panel's checkbox and the menu's tick both show.
func modelSelectedFaceSmooth() bool {
	faces := modelSelectedFaces()
	if len(faces) == 0 {
		return false
	}
	return modelDoc.Faces[faces[0]].Smooth
}

// modelSelectedFaceTexture is the first selected face's texture, seeding the
// panel's combo and ticking the menu's texture list.
func modelSelectedFaceTexture() string {
	faces := modelSelectedFaces()
	if len(faces) == 0 {
		return ""
	}
	return modelDoc.Faces[faces[0]].Texture
}

func modelSubdivideSelectedFaces() {
	faces := modelSelectedFaces()
	if len(faces) == 0 {
		return
	}
	ModelPushUndo()
	modelSelection.Faces = modelDoc.SubdivideFaces(faces)
	modelUVInvalidate() // the faces are renumbered and their UVs regenerated
	modelSetStatus("Subdivided %d faces", len(faces))
}

func modelDeleteSelectedFaces() {
	faces := modelSelectedFaces()
	if len(faces) == 0 {
		return
	}
	ModelPushUndo()
	modelDoc.DeleteFaces(faces)
	modelClearMeshSelection()
	modelSetStatus("Deleted %d faces", len(faces))
}

func modelUnwrapSelectedFaces(fitUnit bool) {
	faces := modelSelectedFaces()
	if len(faces) == 0 {
		return
	}
	ModelPushUndo()
	modelDoc.ModelPlanarUnwrap(faces, fitUnit)
	modelUVInvalidate()
	if fitUnit {
		modelSetStatus("Unwrapped %d faces to fill 0..1", len(faces))
	} else {
		modelSetStatus("Unwrapped %d faces at world scale", len(faces))
	}
}

func modelFlipSelectedFaceUV(flipU, flipV bool) {
	faces := modelSelectedFaces()
	if len(faces) == 0 {
		return
	}
	ModelPushUndo()
	modelDoc.ModelFlipUV(faces, flipU, flipV)
	modelUVInvalidate()
}

// ---------- skin weights ----------
//
// These read the mesh selection through modelSelectedMeshVerts, not
// modelSelectedVerts, for the reason given in the header of
// editor_model_bone_panel.go: picking the bone to assign to means being in Bone
// mode, where the gizmo's notion of "selected vertices" is deliberately empty.

func modelAssignSelectedToBone(bi int) {
	if modelDoc == nil || !modelDoc.validBone(bi) {
		return
	}
	verts := modelSelectedMeshVerts()
	if len(verts) == 0 {
		modelSetStatus("No points selected — pick some in vertex [1], edge [2] or face [3] mode first")
		return
	}
	ModelPushUndo()
	n := modelDoc.AssignVertsToBone(verts, bi, modelAssignWeight)
	modelSetStatus("Assigned %d point(s) to %s", n, modelDoc.Bones[bi].Name)
}

func modelRemoveSelectedFromBone(bi int) {
	if modelDoc == nil || !modelDoc.validBone(bi) {
		return
	}
	verts := modelSelectedMeshVerts()
	if len(verts) == 0 {
		return
	}
	ModelPushUndo()
	n := modelDoc.RemoveVertsFromBone(verts, bi)
	modelSetStatus("Removed %d point(s) from %s", n, modelDoc.Bones[bi].Name)
}

func modelClearSelectedWeights() {
	if modelDoc == nil {
		return
	}
	verts := modelSelectedMeshVerts()
	if len(verts) == 0 {
		return
	}
	ModelPushUndo()
	modelDoc.ClearWeights(verts)
	modelSetStatus("Unbound %d point(s)", len(verts))
}

// modelSelectBoneVerts replaces the mesh selection with the points a bone
// moves, switching to vertex mode to show them.
func modelSelectBoneVerts(bi int) {
	if modelDoc == nil || !modelDoc.validBone(bi) {
		return
	}
	modelSelection.Mode = modelSelVertex
	modelSelection.Verts = modelDoc.VertsBoundTo(bi)
	modelSelection.Edges = nil
	modelSelection.Faces = nil
	modelSetStatus("Selected %d point(s) of %s",
		len(modelSelection.Verts), modelDoc.Bones[bi].Name)
}

func modelAutoWeightAll() {
	if modelDoc == nil || len(modelDoc.Bones) == 0 {
		return
	}
	ModelPushUndo()
	n := modelDoc.AutoWeightVerts(modelAllVertIndices())
	modelSetStatus("Auto-weighted %d point(s) to %d bones", n, len(modelDoc.Bones))
}

// modelBoneWeightedCount is how many points a bone moves, shown on the menu row
// that selects them.
func modelBoneWeightedCount(bi int) int {
	if modelDoc == nil || !modelDoc.validBone(bi) {
		return 0
	}
	return len(modelDoc.VertsBoundTo(bi))
}

// ---------- bones ----------

// modelReparentBone hangs bi off parent, or makes it a root with
// modelBoneNoParent. A cycle is refused by SetBoneParent, and the undo entry
// goes back with it.
func modelReparentBone(bi, parent int) {
	if modelDoc == nil || !modelDoc.validBone(bi) {
		return
	}
	connected := modelDoc.Bones[bi].Connected
	if parent < 0 {
		connected = false
	}
	ModelPushUndo()
	ok := false
	modelBoneEdit(func() { ok = modelDoc.SetBoneParent(bi, parent, connected) })
	if !ok {
		modelUndoStack.pop()
		modelSetStatus("%s is below %s — that would make a loop",
			modelDoc.Bones[parent].Name, modelDoc.Bones[bi].Name)
		return
	}
	if parent < 0 {
		modelSetStatus("%s is a root bone now", modelDoc.Bones[bi].Name)
		return
	}
	modelSetStatus("%s hangs off %s now", modelDoc.Bones[bi].Name, modelDoc.Bones[parent].Name)
}

// modelSetBoneConnected welds a bone's head to its parent's tail, or unwelds it.
func modelSetBoneConnected(bi int, connected bool) {
	if modelDoc == nil || !modelDoc.validBone(bi) || modelDoc.Bones[bi].Parent < 0 {
		return
	}
	ModelPushUndo()
	parent := modelDoc.Bones[bi].Parent
	modelBoneEdit(func() { modelDoc.SetBoneParent(bi, parent, connected) })
}

// modelTogglePoseMode is the P key and the menu row behind it: the switch plus
// the status line that says which side of it the editor is now on, since
// nothing else in the viewport announces the change.
func modelTogglePoseMode() {
	modelSetPoseMode(!modelPoseMode)
	if modelPoseMode {
		modelSetStatus("Pose mode — the document keeps its rest shape")
	} else {
		modelSetStatus("Rest mode")
	}
}
