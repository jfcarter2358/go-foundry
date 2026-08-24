package main

// editor_undo.go — structural undo/redo for the level editor.
//
// Scope: add/delete/move of sectors/walls/portals, chunks, biome sectors, the
// spawn point, and extrusion/terrain points. Property-panel field edits
// (texture pickers, sliders, numeric drags) are not tracked.
//
// Snapshots store the plain-data Raw* representation rather than a copy of
// runtime structs, since runtime Sector/Wall geometry holds live OpenGL
// VAO/VBO handles that a struct copy would alias rather than duplicate.
// Restoring a snapshot re-derives all runtime geometry via rawToLevel — the
// same path already used by "Open Level".

import "github.com/veandco/go-sdl2/sdl"

// maxUndoDepth bounds memory use; older snapshots are dropped once exceeded.
const maxUndoDepth = 100

// boundedStack is a LIFO snapshot stack capped at maxUndoDepth entries —
// the push/pop bookkeeping shared by the level undo and redo stacks below.
type boundedStack[T any] struct {
	items []T
}

func (s *boundedStack[T]) push(v T) {
	s.items = append(s.items, v)
	if len(s.items) > maxUndoDepth {
		s.items = s.items[len(s.items)-maxUndoDepth:]
	}
}

func (s *boundedStack[T]) pop() (T, bool) {
	var zero T
	if len(s.items) == 0 {
		return zero, false
	}
	v := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return v, true
}

func (s *boundedStack[T]) len() int { return len(s.items) }
func (s *boundedStack[T]) clear()   { s.items = nil }

var levelUndoStack boundedStack[RawLevel]
var levelRedoStack boundedStack[RawLevel]

// ---------- Level (3D/2D/Surface editor) undo/redo ----------

// EditorPushUndo snapshots the current level state onto the undo stack and
// clears the redo stack. Call immediately BEFORE a structural mutation.
func EditorPushUndo() {
	if Editor.level == nil {
		return
	}
	raw, err := levelToRaw(Editor.level)
	if err != nil {
		return
	}
	levelUndoStack.push(raw)
	levelRedoStack.clear()
}

// EditorClearUndoHistory discards all undo/redo state. Called when a
// different level is loaded so undo can't reach across files.
func EditorClearUndoHistory() {
	levelUndoStack.clear()
	levelRedoStack.clear()
}

func EditorCanUndo() bool { return levelUndoStack.len() > 0 }
func EditorCanRedo() bool { return levelRedoStack.len() > 0 }

// EditorUndo restores the most recently pushed snapshot, moving the current
// state onto the redo stack.
func EditorUndo() {
	if Editor.level == nil || levelUndoStack.len() == 0 {
		return
	}
	if cur, err := levelToRaw(Editor.level); err == nil {
		levelRedoStack.push(cur)
	}
	prev, _ := levelUndoStack.pop()
	restoreLevelSnapshot(prev)
}

// EditorRedo re-applies the most recently undone snapshot.
func EditorRedo() {
	if Editor.level == nil || levelRedoStack.len() == 0 {
		return
	}
	if cur, err := levelToRaw(Editor.level); err == nil {
		levelUndoStack.push(cur)
	}
	next, _ := levelRedoStack.pop()
	restoreLevelSnapshot(next)
}

// restoreLevelSnapshot rebuilds runtime level state from raw and resets
// editor selection/drag state so stale indices can't crash the UI.
func restoreLevelSnapshot(raw RawLevel) {
	newLevel, err := rawToLevel(raw)
	if err != nil {
		return
	}
	// Built before the swap so a failed rebuild leaves the current level intact,
	// released after it so the outgoing entities' GPU buffers do not leak — see
	// EditorLoadLevel's note.
	DeleteEntities(Editor.level)
	*Editor.level = newLevel

	UploadLights(Editor.level.Lights)

	// The player is engine-owned and survives the snapshot swap; only its
	// position follows the restored spawn point.
	ApplySpawnPoint(Editor.level)

	if Editor.ActiveChunkIdx >= len(Editor.level.Chunks) {
		Editor.ActiveChunkIdx = len(Editor.level.Chunks) - 1
	}
	if Editor.ActiveChunkIdx < 0 && len(Editor.level.Chunks) > 0 {
		Editor.ActiveChunkIdx = 0
	}
	Editor.ActiveSectorIdx = -1
	Editor.ActiveEdgeIdx = -1
	Editor.ActiveEdgeKind = ""
	Editor.ActiveSurface = ""
	Editor.SelectedFaces = nil
	editorClearSpawnSelection()
	editorClearEntitySelection()
	Editor.EntityPlacementMode = false
	Editor.DragVertexSI = -1
	Editor.DragVertexVI = -1
	Editor.ActiveVertexSI = -1
	Editor.ActiveVertexVI = -1
	Editor.SelectedVertices = nil
	Editor.GizmoKind = gizmoKindNone
	Editor.GizmoDragAxis = -1
	Editor.GizmoDragging = false
	Editor.PendingEdges = nil
	Editor.ActiveExtrusionIdx = -1
	Editor.ExtrusionMode = false
	Editor.PendingOuter = nil
	Editor.PendingInner = nil
	Editor.PlacingInner = false
	Editor.BiomeDrawing = false
	Editor.BiomeDrawPoints = nil
	Editor.ActiveBiomeSectorIdx = -1
	Editor.BiomeDragVertexIdx = -1
	Editor.ActiveTerrainPtIdx = -1
	Editor.SurfaceDragPt = -1

	// The surface editor targets a specific wall/floor/ceiling by index;
	// those indices are no longer trustworthy after a snapshot swap.
	if Editor.Mode == ModeSurface2D {
		Editor.Mode = Mode2D
	}

	Editor.StatusMsg = "Undo/redo applied"
	Editor.StatusTime = sdl.GetTicks()
}
