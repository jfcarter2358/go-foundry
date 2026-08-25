package editor

// editor_chunk.go — chunk selection.
//
// A chunk is two things at once, and the difference is the whole reason this
// file exists:
//
//   - the *active* chunk (Editor.ActiveChunkIdx) is the one being edited. The
//     sector map draws it, a new sector joins its point pool, the biome layer
//     paints it. There is always one, and every mode needs to know which.
//   - the *selected* chunk (Editor.ChunkSelected) is the one the panel is
//     showing properties for and the gizmo is attached to. There may be none.
//
// The toolbar used to carry a dropdown for the first and the Chunks list the
// second, which meant two controls for one piece of state that could disagree
// about it. Now the list does both: clicking a row makes that chunk active and
// selects it, and the panel's context line reports which one that is.

import "foundry/internal/world"

// activeChunkIndexValid reports whether ActiveChunkIdx names a chunk that is
// really there. Called before every use, because undo replaces the level
// wholesale and a chunk deleted by it leaves the index pointing past the end.
func activeChunkIndexValid() bool {
	lvl := Editor.level
	return lvl != nil && Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks)
}

// selectedChunk returns the chunk the panel should show properties for, or nil
// when the chunk is merely active rather than selected.
func selectedChunk() *world.Chunk {
	if !Editor.ChunkSelected || !activeChunkIndexValid() {
		return nil
	}
	return &Editor.level.Chunks[Editor.ActiveChunkIdx]
}

// editorSelectChunk makes the chunk at idx both the active one and the
// selection.
//
// Selections that only mean something inside a chunk go with the change: a
// sector index, a face and a vertex handle are all positions in the chunk that
// was active, and carrying them across would leave the panel editing whatever
// happens to sit at that index in the new one. Entities and volumes are level-
// scoped, so they are dropped for the ordinary reason instead — something else
// is the selection now.
func editorSelectChunk(idx int) {
	lvl := Editor.level
	if lvl == nil || idx < 0 || idx >= len(lvl.Chunks) {
		return
	}
	if idx != Editor.ActiveChunkIdx {
		Editor.ActiveSectorIdx = -1
		Editor.ActiveEdgeIdx = -1
		Editor.ActiveEdgeKind = ""
		Editor.ActiveBiomeSectorIdx = -1
		Editor.BiomeDragVertexIdx = -1
		clearVertexSelection()
		clearFaceSelection()
	}
	editorClearSpawnSelection()
	editorClearEntitySelection()
	editorClearVolumeSelection()
	editorClearLightSelection()

	Editor.ActiveChunkIdx = idx
	Editor.ChunkSelected = true
	Editor.GizmoKind = gizmoKindChunk
	Editor.PanelFocus = panelFocusChunk
}

// editorClearChunkSelection drops the chunk from the selection, taking the
// gizmo with it so it cannot keep dragging something no longer selected. The
// chunk stays active — something has to be.
func editorClearChunkSelection() {
	Editor.ChunkSelected = false
	if Editor.GizmoKind == gizmoKindChunk {
		Editor.GizmoKind = gizmoKindNone
	}
}
