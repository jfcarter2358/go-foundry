package editor

// editor_aitree_undo.go — undo/redo for the AI editor.
//
// Whole-document snapshots on the generic boundedStack, exactly as the model
// and particle editors do it, and for the same reason: a .fai is a few kilobytes
// of names and short expressions, and the snapshot approach means a new verb, a
// new field or a new kind of node needs no undo code at all.
//
// The one thing that has to be right is the clone being deep — Doc.Clone copies
// every tree, node and field map, or a snapshot would share them with the live
// document and record nothing. TestAIUndoSnapshotIsDeep asserts it.

import "foundry/internal/aitree"

// aiSnapshot is a restorable editor state: the document, which tree was on the
// canvas, and what was selected. The view — pan and zoom — is deliberately not
// in it: undoing an edit should not also move the camera out from under the
// hand that made it.
type aiSnapshot struct {
	Doc       *aitree.Doc
	TreeIndex int
	Selection map[string]bool
}

var (
	aiUndoStack boundedStack[aiSnapshot]
	aiRedoStack boundedStack[aiSnapshot]
)

// AIPushUndo records the current document before a change. Call it immediately
// BEFORE the mutation, the same contract EditorPushUndo has.
func AIPushUndo() {
	if aiDoc == nil {
		return
	}
	aiUndoStack.push(aiTakeSnapshot())
	aiRedoStack = boundedStack[aiSnapshot]{}
}

// AIPopUndo discards the entry just pushed, for an operation that turned out to
// be a no-op.
func AIPopUndo() { aiUndoStack.pop() }

// AIClearUndoHistory drops both stacks — what opening a different document
// does, since a snapshot of the old one would restore the wrong file.
func AIClearUndoHistory() {
	aiUndoStack = boundedStack[aiSnapshot]{}
	aiRedoStack = boundedStack[aiSnapshot]{}
}

// AICanUndo reports whether there is anything to undo.
func AICanUndo() bool { return aiUndoStack.len() > 0 }

// AICanRedo reports whether there is anything to redo.
func AICanRedo() bool { return aiRedoStack.len() > 0 }

// AIUndo restores the previous document.
func AIUndo() {
	snap, ok := aiUndoStack.pop()
	if !ok || aiDoc == nil {
		return
	}
	aiRedoStack.push(aiTakeSnapshot())
	aiApplySnapshot(snap)
	aiStatus = "Undo"
}

// AIRedo puts back what an undo took away.
func AIRedo() {
	snap, ok := aiRedoStack.pop()
	if !ok || aiDoc == nil {
		return
	}
	aiUndoStack.push(aiTakeSnapshot())
	aiApplySnapshot(snap)
	aiStatus = "Redo"
}

func aiTakeSnapshot() aiSnapshot {
	sel := make(map[string]bool, len(aiSelection))
	for k, v := range aiSelection {
		sel[k] = v
	}
	return aiSnapshot{Doc: aiDoc.Clone(), TreeIndex: aiTreeIndex, Selection: sel}
}

// aiApplySnapshot swaps in a snapshot and puts the editor's own indices
// somewhere valid — the restored document may have fewer trees than the one
// being replaced, and a stale index would read off the end on the next draw.
func aiApplySnapshot(snap aiSnapshot) {
	path := aiDoc.Path
	rev := aiDoc.Rev
	aiDoc = snap.Doc
	aiDoc.Path = path
	// Rev only ever goes up. It is the change signal the save chip and the
	// recompile are keyed on, and an undo that put it back would leave both
	// looking at a document that is no longer there.
	aiDoc.Rev = rev + 1

	aiTreeIndex = snap.TreeIndex
	if aiTreeIndex >= len(aiDoc.Trees) {
		aiTreeIndex = len(aiDoc.Trees) - 1
	}
	if aiTreeIndex < 0 {
		aiTreeIndex = 0
	}
	// A selection is only meaningful against the tree it was taken on, and a
	// node it names may have been what the undo brought back or took away.
	aiSelection = map[string]bool{}
	if t := aiCurrentTree(); t != nil {
		for id := range snap.Selection {
			if t.Node(id) != nil {
				aiSelection[id] = true
			}
		}
	}
	aiRefreshProgram()
}
