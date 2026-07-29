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

var levelUndoStack []RawLevel
var levelRedoStack []RawLevel

var sceneryUndoStack [][]RawPrimitive
var sceneryRedoStack [][]RawPrimitive

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
	levelUndoStack = append(levelUndoStack, raw)
	if len(levelUndoStack) > maxUndoDepth {
		levelUndoStack = levelUndoStack[len(levelUndoStack)-maxUndoDepth:]
	}
	levelRedoStack = nil
}

// EditorClearUndoHistory discards all undo/redo state. Called when a
// different level is loaded so undo can't reach across files.
func EditorClearUndoHistory() {
	levelUndoStack = nil
	levelRedoStack = nil
}

func EditorCanUndo() bool { return len(levelUndoStack) > 0 }
func EditorCanRedo() bool { return len(levelRedoStack) > 0 }

// EditorUndo restores the most recently pushed snapshot, moving the current
// state onto the redo stack.
func EditorUndo() {
	if Editor.level == nil || len(levelUndoStack) == 0 {
		return
	}
	if cur, err := levelToRaw(Editor.level); err == nil {
		levelRedoStack = append(levelRedoStack, cur)
	}
	prev := levelUndoStack[len(levelUndoStack)-1]
	levelUndoStack = levelUndoStack[:len(levelUndoStack)-1]
	restoreLevelSnapshot(prev)
}

// EditorRedo re-applies the most recently undone snapshot.
func EditorRedo() {
	if Editor.level == nil || len(levelRedoStack) == 0 {
		return
	}
	if cur, err := levelToRaw(Editor.level); err == nil {
		levelUndoStack = append(levelUndoStack, cur)
	}
	next := levelRedoStack[len(levelRedoStack)-1]
	levelRedoStack = levelRedoStack[:len(levelRedoStack)-1]
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
	Editor.ActiveEntityIdx = -1
	Editor.ActivePrimitiveIdx = -1
	Editor.DragVertexSI = -1
	Editor.DragVertexVI = -1
	Editor.ActiveVertexSI = -1
	Editor.ActiveVertexVI = -1
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
	sceneryUndoStack = append(sceneryUndoStack, sceneryPrimsToRaw(Editor.SceneryPrimitives))
	if len(sceneryUndoStack) > maxUndoDepth {
		sceneryUndoStack = sceneryUndoStack[len(sceneryUndoStack)-maxUndoDepth:]
	}
	sceneryRedoStack = nil
}

// SceneryClearUndoHistory discards all scenery undo/redo state. Called when
// entering the scenery editor or loading a different scenery asset.
func SceneryClearUndoHistory() {
	sceneryUndoStack = nil
	sceneryRedoStack = nil
}

func SceneryCanUndo() bool { return len(sceneryUndoStack) > 0 }
func SceneryCanRedo() bool { return len(sceneryRedoStack) > 0 }

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
	if len(sceneryUndoStack) == 0 {
		return
	}
	sceneryRedoStack = append(sceneryRedoStack, sceneryPrimsToRaw(Editor.SceneryPrimitives))
	prev := sceneryUndoStack[len(sceneryUndoStack)-1]
	sceneryUndoStack = sceneryUndoStack[:len(sceneryUndoStack)-1]
	restoreSceneryPrimitives(prev)
}

func SceneryRedo() {
	if len(sceneryRedoStack) == 0 {
		return
	}
	sceneryUndoStack = append(sceneryUndoStack, sceneryPrimsToRaw(Editor.SceneryPrimitives))
	next := sceneryRedoStack[len(sceneryRedoStack)-1]
	sceneryRedoStack = sceneryRedoStack[:len(sceneryRedoStack)-1]
	restoreSceneryPrimitives(next)
}
