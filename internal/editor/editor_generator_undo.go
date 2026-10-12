package editor

// editor_generator_undo.go — undo/redo for the Generator editor.
//
// Whole-document snapshots on boundedStack, as every document editor does it.
// A generator is a few stages of parameters, so a snapshot is a few
// kilobytes; the clone is deep (procgen.TestCloneDoesNotShare holds it), so a
// snapshot never shares a spline's points or a list's items with the live
// document.
//
// Drags push once: genBeginEdit is latched by the widget's ID and cleared
// when nothing is active (genEndEdits), the particle editor's pattern — a
// slider dragged across a hundred values is one undo step, not a hundred.

import (
	"foundry/internal/procgen"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// generatorSnapshot is a restorable state: the document and which stage was
// selected (so an undo lands where the change was made).
type generatorSnapshot struct {
	Doc      *procgen.Doc
	Selected int
}

var (
	generatorUndoStack boundedStack[generatorSnapshot]
	generatorRedoStack boundedStack[generatorSnapshot]
)

// GeneratorPushUndo records the document before a change. Call it BEFORE the
// mutation.
func GeneratorPushUndo() {
	if genDoc == nil {
		return
	}
	generatorUndoStack.push(generatorSnapshot{Doc: genDoc.Clone(), Selected: genSelected})
	generatorRedoStack = boundedStack[generatorSnapshot]{}
}

// GeneratorClearUndoHistory drops both stacks: what opening another document
// does.
func GeneratorClearUndoHistory() {
	generatorUndoStack = boundedStack[generatorSnapshot]{}
	generatorRedoStack = boundedStack[generatorSnapshot]{}
	genDragActive = ""
}

// GeneratorCanUndo and GeneratorCanRedo report whether there is anything to do.
func GeneratorCanUndo() bool { return generatorUndoStack.len() > 0 }
func GeneratorCanRedo() bool { return generatorRedoStack.len() > 0 }

// GeneratorUndo restores the previous document.
func GeneratorUndo() {
	snap, ok := generatorUndoStack.pop()
	if !ok || genDoc == nil {
		return
	}
	generatorRedoStack.push(generatorSnapshot{Doc: genDoc.Clone(), Selected: genSelected})
	genApplySnapshot(snap)
	genStatus = "Undo"
}

// GeneratorRedo puts back what an undo took away.
func GeneratorRedo() {
	snap, ok := generatorRedoStack.pop()
	if !ok || genDoc == nil {
		return
	}
	generatorUndoStack.push(generatorSnapshot{Doc: genDoc.Clone(), Selected: genSelected})
	genApplySnapshot(snap)
	genStatus = "Redo"
}

// genApplySnapshot swaps a snapshot in, keeping the name, which belongs to
// the file rather than to any state of it.
func genApplySnapshot(snap generatorSnapshot) {
	name := genDoc.Name
	genDoc = snap.Doc
	genDoc.Name = name
	genSelected = snap.Selected
	if genSelected >= len(genDoc.Stages) {
		genSelected = len(genDoc.Stages) - 1
	}
	genDragActive = ""
	genAfterEdit()
}

// genDragActive is the ID of the widget being dragged, so a drag pushes undo
// once.
var genDragActive string

// genBeginEdit pushes undo for an edit made through the widget id, at most
// once per continuous drag.
func genBeginEdit(id string) {
	if genDragActive == id {
		return
	}
	genDragActive = id
	GeneratorPushUndo()
}

// genEndEdits clears the drag latch once nothing is being dragged. Called
// once a frame. The 2D view's own drags (a spline handle) are not imgui
// items, so they hold the latch through genViewDragging.
func genEndEdits() {
	if !imgui.IsAnyItemActive() && !genViewDragging() {
		genDragActive = ""
	}
}
