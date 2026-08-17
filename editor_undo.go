package main

// editor_undo.go — structural undo/redo for the level editor and the
// standalone scenery compositor.
//
// Scope: add/delete/move of entities, primitives, sectors/walls/portals,
// chunks, biome sectors, and extrusion/terrain points. Property-panel field
// edits (texture pickers, sliders, numeric drags) are not tracked.
//
// Snapshots store the plain-data Raw* representation rather than a copy of
// runtime structs, since runtime Sector/Wall/Primitive geometry holds live
// OpenGL VAO/VBO handles that a struct copy would alias rather than
// duplicate. Restoring a snapshot re-derives all runtime geometry via
// rawToLevel / rawToSceneryPrimitive — the same path already used by
// "Open Level" and "Load Scenery".

import "github.com/veandco/go-sdl2/sdl"

// maxUndoDepth bounds memory use; older snapshots are dropped once exceeded.
const maxUndoDepth = 100

// boundedStack is a LIFO snapshot stack capped at maxUndoDepth entries —
// the push/pop bookkeeping shared by the level and scenery undo/redo stacks
// below (previously duplicated one pair of stacks per editor).
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

var sceneryUndoStack boundedStack[[]RawPrimitive]
var sceneryRedoStack boundedStack[[]RawPrimitive]

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
	*Editor.level = newLevel

	UploadLights(Editor.level.Lights)

	PlayerEntity = nil
	for i := range Editor.level.Entities {
		if Editor.level.Entities[i].ID == "player" {
			PlayerEntity = &Editor.level.Entities[i]
			break
		}
	}

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
	Editor.ActiveEntityIdx = -1
	Editor.ActivePrimitiveIdx = -1
	Editor.DragVertexSI = -1
	Editor.DragVertexVI = -1
	Editor.ActiveVertexSI = -1
	Editor.ActiveVertexVI = -1
	Editor.SelectedVertices = nil
	Editor.GizmoKind = gizmoKindNone
	Editor.GizmoDragAxis = -1
	Editor.GizmoDragging = false
	Editor.ConnectFromIdx = -1
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

// ---------- Scenery editor undo/redo ----------

// SceneryPushUndo snapshots the current scenery primitive set. Call
// immediately BEFORE a structural mutation in the scenery compositor.
func SceneryPushUndo() {
	sceneryUndoStack.push(sceneryPrimsToRaw(Editor.SceneryPrimitives))
	sceneryRedoStack.clear()
}

// SceneryClearUndoHistory discards all scenery undo/redo state. Called when
// entering the scenery editor or loading a different scenery asset.
func SceneryClearUndoHistory() {
	sceneryUndoStack.clear()
	sceneryRedoStack.clear()
}

func SceneryCanUndo() bool { return sceneryUndoStack.len() > 0 }
func SceneryCanRedo() bool { return sceneryRedoStack.len() > 0 }

func sceneryPrimsToRaw(prims []Primitive) []RawPrimitive {
	raw := make([]RawPrimitive, len(prims))
	for i := range prims {
		raw[i] = sceneryPrimToRaw(&prims[i])
	}
	return raw
}

// restoreSceneryPrimitives deletes the live GPU geometry for the current
// primitive set and rebuilds it from a raw snapshot.
func restoreSceneryPrimitives(raw []RawPrimitive) {
	for i := range Editor.SceneryPrimitives {
		Editor.SceneryPrimitives[i].Geometry.Delete()
	}
	prims := make([]Primitive, 0, len(raw))
	for _, rp := range raw {
		p := rawToSceneryPrimitive(rp)
		if err := p.Load(); err == nil {
			prims = append(prims, p)
		}
	}
	Editor.SceneryPrimitives = prims
	Editor.SceneryActivePrimIdx = -1
}

func SceneryUndo() {
	if sceneryUndoStack.len() == 0 {
		return
	}
	sceneryRedoStack.push(sceneryPrimsToRaw(Editor.SceneryPrimitives))
	prev, _ := sceneryUndoStack.pop()
	restoreSceneryPrimitives(prev)
}

func SceneryRedo() {
	if sceneryRedoStack.len() == 0 {
		return
	}
	sceneryUndoStack.push(sceneryPrimsToRaw(Editor.SceneryPrimitives))
	next, _ := sceneryRedoStack.pop()
	restoreSceneryPrimitives(next)
}
