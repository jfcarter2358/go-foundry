package editor

// editor_ui_undo.go — undo for the UI editor.
//
// Whole-document snapshots on the shared boundedStack, the arrangement the
// model and particle editors use. A ui.Screen is plain data with no GL handles
// in it, so a Clone is a correct snapshot and there is no reason to round-trip
// it through YAML the way the level's undo has to.
//
// Unlike the level's undo, this one covers property edits as well as structural
// ones. A UI document is almost entirely properties — nudging an offset, picking
// a colour — and an undo that only covered add and delete would miss nearly
// everything an author does.

import (
	"foundry/internal/ui"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// uiSnapshot is a document plus the editor state that has to move with it.
//
// The selection is included because restoring a document without it leaves the
// panel pointing at whatever was selected in the *other* version. The view — pan,
// zoom, which toggles are on — deliberately is not: undo is about the document,
// and having the canvas jump around as well is disorienting.
type uiSnapshot struct {
	Doc *ui.Screen
	Sel string
}

var uiUndoStack, uiRedoStack boundedStack[uiSnapshot]

// UIPushUndo records the document as it stands, before a change.
//
// Call it immediately before the mutation and exactly once per user-visible
// action. A drag pushes on mouse-down, not per motion event — see uiBeginEdit.
func UIPushUndo() {
	if uiDoc == nil {
		return
	}
	uiUndoStack.push(uiSnapshot{Doc: uiDoc.Clone(), Sel: uiSel})
	uiRedoStack.clear()
}

// UIPopUndo discards the entry just pushed, for an operation that turned out to
// be a no-op.
func UIPopUndo() { uiUndoStack.pop() }

// UIClearUndoHistory drops everything, for a document swap. A stack spanning two
// documents would restore one screen's nodes into the other's tree.
func UIClearUndoHistory() {
	uiUndoStack.clear()
	uiRedoStack.clear()
	uiDragActive = ""
}

// UICanUndo and UICanRedo drive the toolbar buttons.
func UICanUndo() bool { return uiUndoStack.len() > 0 }
func UICanRedo() bool { return uiRedoStack.len() > 0 }

// UIUndo steps back one action.
func UIUndo() {
	snap, ok := uiUndoStack.pop()
	if !ok || uiDoc == nil {
		return
	}
	uiRedoStack.push(uiSnapshot{Doc: uiDoc.Clone(), Sel: uiSel})
	uiApplySnapshot(snap)
	uiStatus = "Undo"
}

// UIRedo steps forward one action.
func UIRedo() {
	snap, ok := uiRedoStack.pop()
	if !ok || uiDoc == nil {
		return
	}
	uiUndoStack.push(uiSnapshot{Doc: uiDoc.Clone(), Sel: uiSel})
	uiApplySnapshot(snap)
	uiStatus = "Redo"
}

// uiApplySnapshot installs a restored document.
//
// The selection is re-resolved by name rather than trusted: undoing past the
// creation of a node leaves the panel pointing at something that no longer
// exists, and a stale pointer there would be a crash rather than a blank panel.
func uiApplySnapshot(snap uiSnapshot) {
	uiDoc = snap.Doc
	uiDoc.Invalidate()

	uiSel = ""
	if snap.Sel != "" && uiDoc.Find(snap.Sel) != nil {
		uiSel = snap.Sel
	}

	uiPreview.Reset()
	uiPreview.Push(uiDoc)
	uiEditorInstallResolver()
}

// ---------- drag coalescing ----------

// uiDragActive names the control currently being dragged, or "".
//
// A slider dragged across the panel fires an edit every frame. Without this,
// one gesture would fill the undo stack with fifty entries and Ctrl+Z would
// walk back through the drag one frame at a time.
var uiDragActive string

// uiCanvasDragging is set while a canvas gesture is in progress. imgui knows
// nothing about those, so uiEndEdits has to be told separately.
var uiCanvasDragging bool

// uiBeginEdit pushes an undo entry at most once per continuous drag of one
// control.
func uiBeginEdit(label string) {
	if uiDragActive == label {
		return
	}
	uiDragActive = label
	UIPushUndo()
}

// uiEndEdits clears the drag latch once nothing is being held. Called once at
// the end of the frame, after the panel and the canvas have both had their say.
func uiEndEdits() {
	if !imgui.IsAnyItemActive() && !uiCanvasDragging {
		uiDragActive = ""
	}
	if uiDoc != nil {
		uiDoc.Clean()
	}
}

// uiEdited marks the document changed after a mutation.
func uiEdited() {
	if uiDoc != nil {
		uiDoc.Invalidate()
	}
}
