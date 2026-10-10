package editor

// editor_entity_editor_undo.go — undo/redo for the entity editor.
//
// Whole-document snapshots on the generic boundedStack, exactly as the model
// and particle editors do it. A diff would be smaller and would not be worth
// it: an entity definition is a few dozen fields and a couple of short lists,
// and the point of the snapshot approach is that a new field needs no undo code
// at all.
//
// The one thing that has to be right is the clone being deep, which
// EntityDef.Clone is — and can be, because nothing in a definition holds GPU
// state. The *preview* does, and it is not in the snapshot: it is rebuilt from
// the restored document, which is the same path every other edit takes.

import "foundry/internal/world"

// entityDefSnapshot is a restorable editor state: the document, and what the
// outline had selected. The selection rides along because an undo that put the
// document back but left the panel pointing at a model that no longer exists
// would show the wrong fields for a frame and then jump.
type entityDefSnapshot struct {
	Def *world.EntityDef
	Sel entityEditorSelection
}

var (
	entityUndoStack boundedStack[entityDefSnapshot]
	entityRedoStack boundedStack[entityDefSnapshot]
)

// EntityDefPushUndo records the current document before a change. Call it
// immediately BEFORE the mutation, the same contract EditorPushUndo has.
func EntityDefPushUndo() {
	if entityDef == nil {
		return
	}
	entityUndoStack.push(entityDefSnapshot{Def: entityDef.Clone(), Sel: entitySel})
	entityRedoStack.clear()
}

// EntityDefClearUndoHistory drops both stacks — what opening a different
// definition does, since a snapshot of the old one would restore the wrong
// type under the new one's name.
func EntityDefClearUndoHistory() {
	entityUndoStack.clear()
	entityRedoStack.clear()
}

// EntityDefCanUndo / EntityDefCanRedo report whether there is anything to do,
// so the toolbar can grey its buttons rather than offering a no-op.
func EntityDefCanUndo() bool { return entityUndoStack.len() > 0 }
func EntityDefCanRedo() bool { return entityRedoStack.len() > 0 }

// EntityDefUndo restores the previous document.
func EntityDefUndo() {
	snap, ok := entityUndoStack.pop()
	if !ok || entityDef == nil {
		return
	}
	entityRedoStack.push(entityDefSnapshot{Def: entityDef.Clone(), Sel: entitySel})
	entityApplySnapshot(snap)
	entityStatus = "Undo"
}

// EntityDefRedo puts back what an undo took away.
func EntityDefRedo() {
	snap, ok := entityRedoStack.pop()
	if !ok || entityDef == nil {
		return
	}
	entityUndoStack.push(entityDefSnapshot{Def: entityDef.Clone(), Sel: entitySel})
	entityApplySnapshot(snap)
	entityStatus = "Redo"
}

// entityApplySnapshot swaps in a snapshot and puts the selection somewhere
// valid — the restored document may have fewer models than the one being
// replaced, and a stale index would read off the end on the next draw.
func entityApplySnapshot(snap entityDefSnapshot) {
	// Ctrl+Z is reachable mid-drag, and a drag that outlived the document it
	// grabbed would go on writing into the restored one.
	entityGizmoMouseUp()
	entityDef = snap.Def
	entitySel = snap.Sel
	entityClampSelection()
	entityMarkDirty()
	entityPreviewInvalidate()
	// The document's brushes have been replaced, so the live ones the viewport
	// is drawing and the tools are editing are now the wrong ones. See
	// entityGeomDirty.
	entityInvalidateGeometry()
	// The effects have been replaced too, so the live simulations are running
	// the wrong documents. Rebuilt from the restored list on the next frame.
	entityEmitterPreviewRelease()
}

// entityClampSelection drops a selection that no longer points at anything.
func entityClampSelection() {
	if entityDef == nil {
		entitySel = entityEditorSelection{}
		return
	}
	n := 0
	switch entitySel.Kind {
	case entitySelModel:
		n = len(entityDef.Models)
	case entitySelBrush:
		n = entityBrushCount()
	case entitySelEmitter:
		n = len(entityDef.Emitters)
	case entitySelVolume:
		n = len(entityDef.Volumes)
	case entitySelSeat:
		if entityDef.Vehicle != nil {
			n = len(entityDef.Vehicle.Seats)
		}
	case entitySelWaypoints:
		n = len(entityDef.WaypointAnimations)
	default:
		return
	}
	if entitySel.Idx < 0 || entitySel.Idx >= n {
		entitySel = entityEditorSelection{}
		return
	}
	// A point past the end of a path the snapshot holds fewer of falls back to
	// the path selected whole.
	if entitySel.Kind == entitySelWaypoints && entitySel.Sub >= len(entityDef.WaypointAnimations[entitySel.Idx].Points) {
		entitySel.Sub = -1
	}
}
