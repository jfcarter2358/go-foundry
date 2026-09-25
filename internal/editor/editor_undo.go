package editor

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

import (
	"foundry/internal/levelio"
	"foundry/internal/script"
	"foundry/internal/world"

	"github.com/veandco/go-sdl2/sdl"
)

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

var levelUndoStack boundedStack[world.RawLevel]
var levelRedoStack boundedStack[world.RawLevel]

// ---------- Level (3D/2D/Surface editor) undo/redo ----------

// EditorPushUndo snapshots the current level state onto the undo stack and
// clears the redo stack. Call immediately BEFORE a structural mutation.
// inPlaySession reports whether a play session is currently open. Undo is
// refused while one is: a snapshot taken over script-mutated state would poison
// the stack permanently, and leaving play restores the level underneath it
// anyway.
//
// Gated on the session rather than on !Editor.Active, which is also true before
// the editor has ever been opened — including in every headless test.
func inPlaySession() bool { return script.PlaySession.Valid }

// editorUndoRedirect diverts EditorPushUndo to a document editor's own undo
// stack for the duration of that editor's panel.
//
// The tags, attributes and script-parameter widgets in editor_script_panel.go
// are shared by every object that has any — a sector, a volume, an entity — and
// they push undo themselves, which is right: the caller cannot know whether the
// user actually changed anything inside them. The entity editor reuses those
// same widgets on a *definition*, which is not in the level at all, so their
// push has to land somewhere else. Setting this around the panel is what does
// that, and it is a redirect rather than a parameter because threading an undo
// callback through eight widget signatures would change every existing call
// site to say the thing they already mean by default.
//
// Safe as a global because only one window's Draw runs at a time, and the
// setter clears it with a defer.
var editorUndoRedirect func()

// withUndoRedirect runs body with pushes going to push instead of the level's
// undo stack.
func withUndoRedirect(push func(), body func()) {
	prev := editorUndoRedirect
	editorUndoRedirect = push
	defer func() { editorUndoRedirect = prev }()
	body()
}

func EditorPushUndo() {
	if editorUndoRedirect != nil {
		editorUndoRedirect()
		return
	}
	if inPlaySession() {
		return
	}
	if Editor.level == nil {
		return
	}
	// The compiled preview is a picture of the level as it was when it was
	// built, and this is the moment the level stops being that. Leaving it up
	// would show the author their last compile while they edited past it.
	dropCompiledPreview()
	raw, err := editorLevelToRaw()
	if err != nil {
		return
	}
	levelUndoStack.push(raw)
	levelRedoStack.clear()
}

// EditorPopUndo discards the entry EditorPushUndo just added, for an operation
// that turned out to be a no-op.
//
// Every mesh op has to push its undo entry *before* it knows whether it will do
// anything — the check is inside the op — so without this an author would build
// up a stack of undo steps that restore the level to exactly what it already
// was, and pressing Ctrl+Z would appear to do nothing several times over.
func EditorPopUndo() {
	levelUndoStack.pop()
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
	if inPlaySession() {
		return
	}
	if Editor.level == nil || levelUndoStack.len() == 0 {
		return
	}
	if cur, err := editorLevelToRaw(); err == nil {
		levelRedoStack.push(cur)
	}
	prev, _ := levelUndoStack.pop()
	restoreLevelSnapshot(prev)
}

// EditorRedo re-applies the most recently undone snapshot.
func EditorRedo() {
	if inPlaySession() {
		return
	}
	if Editor.level == nil || levelRedoStack.len() == 0 {
		return
	}
	if cur, err := editorLevelToRaw(); err == nil {
		levelUndoStack.push(cur)
	}
	next, _ := levelRedoStack.pop()
	restoreLevelSnapshot(next)
}

// restoreLevelSnapshot rebuilds runtime level state from raw and resets
// editor selection/drag state so stale indices can't crash the UI.
// editorResetSelectionState clears every selection, drag and mode that names
// something by index. A snapshot swap invalidates all of them at once.
func editorResetSelectionState() {
	if Editor.ActiveChunkIdx >= len(Editor.level.Chunks) {
		Editor.ActiveChunkIdx = len(Editor.level.Chunks) - 1
	}
	if Editor.ActiveChunkIdx < 0 && len(Editor.level.Chunks) > 0 {
		Editor.ActiveChunkIdx = 0
	}
	Editor.ActiveBrushIdx = -1
	Editor.ActiveFaceIdx = -1
	Editor.SelectedFaces = nil
	Editor.SelectedBrushes = nil
	editorClearSpawnSelection()
	editorClearSealSelection()
	editorClearEntitySelection()
	editorClearVolumeSelection()
	editorClearSpriteSelection()
	editorClearEmitterSelection()
	editorClearSoundSelection()
	editorClearEffectSelection()
	// An undo or redo puts it back by ID afterwards — see restoreLevelSnapshot.
	editorClearWaypointSelection()
	// The previewed object was rebuilt at rest by the snapshot — every one is
	// taken with it at rest (withWaypointPreviewAtRest) — so the preview
	// holds nothing any more. The playhead stays where it was: an undo of a
	// retimed key should not also throw away the moment being looked at.
	Editor.WaypointPreview.Playing, Editor.WaypointPreview.Posed = false, false
	// The brush is deliberately not disarmed here: an undo of a stamp is part
	// of scattering, and putting the tool away after every Ctrl+Z would be its
	// own annoyance. Only the *selection* is stale after a snapshot swap.
	editorClearScenerySelection()
	Editor.EntityPlacementMode = false
	Editor.ActivePrefabIdx = -1
	Editor.PrefabPlacementMode = false
	Editor.VolumePlacementMode = false
	Editor.SpritePlacementMode = false
	Editor.DecalPlacementMode = false
	Editor.EmitterPlacementMode = false
	Editor.SoundPlacementMode = false
	Editor.EffectPlacementMode = false
	Editor.SelectedVertices = nil
	Editor.GizmoKind = gizmoKindNone
	Editor.GizmoDragAxis = -1
	Editor.GizmoDragging = false
	// Detail mode names a brush by index and shapes a mesh a snapshot may not
	// have had at all — undoing past the seed leaves the mode pointed at a
	// brush with nothing to carve. Staying in it would be a viewport where
	// nothing is clickable, so the snapshot swap puts the author back in
	// object mode along with everything else that was named by index.
	if editorInDetailMode() {
		Editor.EditContext = editorObjectMode
	}
	Editor.DetailBrush = brushSel{chunkIdx: -1, brushIdx: -1}
}

// restoreLevelSnapshot applies an undo/redo snapshot: rebuild the level and
// clear the editor's selection.
//
// It deliberately does not apply the spawn point. The editor's viewpoint *is*
// the engine camera (editor_3d_camera.go moves gfx.Cam directly), so spawning
// here would throw the author across the level on every Ctrl+Z — away from the
// very thing they just undid. Nothing needs it either: the player is engine-
// owned and not simulated while the editor is up, and EditorToggle puts it on
// the spawn on the way into play.
func restoreLevelSnapshot(raw world.RawLevel) {
	wpID, wpPoint := "", Editor.ActiveWaypointIdx
	if a := activeWaypointAnimation(); a != nil {
		wpID = a.ID
	}
	if err := levelio.ApplyRawLevel(raw, Editor.level); err != nil {
		return
	}
	editorResetSelectionState()
	editorReselectWaypoint(wpID, wpPoint)

	Editor.StatusMsg = "Undo/redo applied"
	Editor.StatusTime = sdl.GetTicks()
}

// EditorAdoptLoadedLevel points the editor at a level that has just been
// replaced under it.
//
// Called when the game loads a level while the editor is open on the same
// struct: every selection the editor holds names an object by index, and a
// replaced level has re-instantiated all of them. Exported because the loader
// lives in internal/app, which cannot reach editorResetSelectionState — and
// should not have to know what is in it.
func EditorAdoptLoadedLevel() {
	editorResetSelectionState()
	EditorClearUndoHistory()
	levelMarkSaved()
}
