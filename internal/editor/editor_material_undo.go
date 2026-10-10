package editor

// editor_material_undo.go — undo/redo for the material editor.
//
// Whole-document snapshots on the generic boundedStack, exactly as the model
// and particle editors do it. A .fmat is a handful of effects and a few dozen
// numbers, so a diff would be smaller and would not be worth it — and the
// snapshot approach is what lets a new effect kind arrive with no undo code at
// all, which is the whole point of the schema-driven design.
//
// The clone has to be deep, or a snapshot would share its parameter maps with
// the live document and record nothing. material.TestCloneDoesNotShare
// asserts it.

import "foundry/internal/material"

// materialSnapshot is a restorable editor state: the document, and nothing
// else. The camera, the preview shape and which headers are expanded are
// deliberately not in it — undoing a parameter change should not also move the
// view or collapse what you were looking at.
type materialSnapshot struct {
	Doc *material.Doc
}

var (
	materialUndoStack boundedStack[materialSnapshot]
	materialRedoStack boundedStack[materialSnapshot]
)

// MaterialPushUndo records the current document before a change. Call it
// immediately BEFORE the mutation, the same contract EditorPushUndo has.
func MaterialPushUndo() {
	if materialDoc == nil {
		return
	}
	materialUndoStack.push(materialSnapshot{Doc: materialDoc.Clone()})
	materialRedoStack = boundedStack[materialSnapshot]{}
}

// MaterialPopUndo discards the entry just pushed, for an operation that turned
// out to be a no-op.
func MaterialPopUndo() { materialUndoStack.pop() }

// MaterialClearUndoHistory drops both stacks — what opening a different
// document does, since a snapshot of the old one would restore the wrong file.
func MaterialClearUndoHistory() {
	materialUndoStack = boundedStack[materialSnapshot]{}
	materialRedoStack = boundedStack[materialSnapshot]{}
}

// MaterialCanUndo and MaterialCanRedo report whether there is anything to do.
func MaterialCanUndo() bool { return materialUndoStack.len() > 0 }
func MaterialCanRedo() bool { return materialRedoStack.len() > 0 }

// MaterialUndo restores the previous document.
func MaterialUndo() {
	snap, ok := materialUndoStack.pop()
	if !ok || materialDoc == nil {
		return
	}
	materialRedoStack.push(materialSnapshot{Doc: materialDoc.Clone()})
	materialApplySnapshot(snap)
	materialStatus = "Undo"
}

// MaterialRedo puts back what an undo took away.
func MaterialRedo() {
	snap, ok := materialRedoStack.pop()
	if !ok || materialDoc == nil {
		return
	}
	materialUndoStack.push(materialSnapshot{Doc: materialDoc.Clone()})
	materialApplySnapshot(snap)
	materialStatus = "Redo"
}

// materialApplySnapshot swaps in a snapshot, keeping the name — which belongs
// to the FILE being edited rather than to any one state of it, so an undo must
// not take the document back to a name it was saved under earlier.
func materialApplySnapshot(snap materialSnapshot) {
	name := materialDoc.Name
	materialDoc = snap.Doc
	materialDoc.Name = name
	// Undo changes what the swatch should show, and while Live Update is on it
	// changes what the LEVEL shows too.
	materialAfterEdit()
}
