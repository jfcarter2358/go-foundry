package editor

import "foundry/internal/model"

// editor_model_undo.go — undo/redo for the model editor.
//
// This reuses boundedStack[T] (editor_undo.go) but snapshots *ModelDoc directly
// rather than a Raw* form. The level and scenery stacks go through Raw types
// because their runtime structs embed live Geometry, and a struct copy would
// alias the GL handles instead of duplicating them. ModelDoc has no such
// problem: it is plain vertex/face data with no GPU state, so Clone() is
// already a safe, complete snapshot and the Raw round-trip would only add a
// lossy conversion between the document and itself.

// modelUndoStack/modelRedoStack hold whole-document snapshots.
var (
	modelUndoStack boundedStack[modelSnapshot]
	modelRedoStack boundedStack[modelSnapshot]
)

// modelSnapshot is one undo entry. The pose rides alongside the document rather
// than inside it because modelDoc deliberately holds only the rest state
// (editor_model_pose.go) — without this, undoing a pose edit would restore a
// document that was never wrong and leave the pose that was.
//
// The clip index comes too, so undoing across a clip switch puts the timeline
// back on the clip the edit was made in. Keyframes need nothing extra: they live
// in ModelDoc.Animations, which Clone already deep-copies.
type modelSnapshot struct {
	Doc  *model.ModelDoc
	Pose model.ModelPose
	Clip int
}

// modelTakeSnapshot captures the editor's undoable state.
func modelTakeSnapshot() modelSnapshot {
	return modelSnapshot{Doc: modelDoc.Clone(), Pose: modelPose.Clone(), Clip: modelPoseClip}
}

// ModelPushUndo snapshots the document and clears the redo stack. Call
// immediately BEFORE a mutation — and exactly once per user-visible action, so
// a gizmo drag pushes on mouse-down rather than on every motion event.
func ModelPushUndo() {
	if modelDoc == nil {
		return
	}
	modelUndoStack.push(modelTakeSnapshot())
	modelRedoStack.clear()
}

// ModelDiscardUndo drops the most recent undo entry without restoring it.
//
// For an action that pushed an entry and then turned out to change nothing —
// an unwrap that could not fit, a mesh op that refused its input. Calling
// ModelUndo there would work, but it would also push a redo entry and clear
// the selection, so a failed action would leave the editor visibly different
// from how it found it.
func ModelDiscardUndo() {
	modelUndoStack.pop()
}

// ModelUndo restores the previous document state.
func ModelUndo() {
	prev, ok := modelUndoStack.pop()
	if !ok || modelDoc == nil {
		return
	}
	modelRedoStack.push(modelTakeSnapshot())
	modelRestore(prev)
}

// ModelRedo re-applies the most recently undone state.
func ModelRedo() {
	next, ok := modelRedoStack.pop()
	if !ok || modelDoc == nil {
		return
	}
	modelUndoStack.push(modelTakeSnapshot())
	modelRestore(next)
}

func ModelCanUndo() bool { return modelUndoStack.len() > 0 }
func ModelCanRedo() bool { return modelRedoStack.len() > 0 }

// ModelClearUndoHistory drops both stacks — used when a different document is
// loaded, since its snapshots describe a mesh that is no longer open.
func ModelClearUndoHistory() {
	modelUndoStack.clear()
	modelRedoStack.clear()
}

// modelRestore swaps in a snapshot. The selection is cleared rather than
// remapped: indices are positional, and an undone subdivide or extrude has
// renumbered the faces the selection referred to.
func modelRestore(snap modelSnapshot) {
	modelDoc = snap.Doc
	modelDoc.Invalidate() // schedules the GPU rebuild in the next Draw
	modelClearSelection()
	modelClearPendingExtrude()
	// The UV panel caches the selection's UVs; a restored document has
	// different ones, so the cache has to go with the selection.
	modelUVInvalidate()

	modelPose = snap.Pose
	modelPose.Resize(len(modelDoc.Bones))
	modelPoseClip = snap.Clip
	if modelPoseClip >= len(modelDoc.Animations) {
		modelPoseClip = len(modelDoc.Animations) - 1
	}
	// Playback across an undo would immediately overwrite the pose that was
	// just restored, so stop it and re-derive the deformed mesh once.
	modelPosePlaying = false
	// The restored document has different keyframes; anything still selected may
	// refer to one that is no longer there.
	modelPruneKeySelection()
	modelPoseEvaluate()
}
