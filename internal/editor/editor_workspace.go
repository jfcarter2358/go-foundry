package editor

// editor_workspace.go — the geometry-editing state, and which window owns it.
//
// Everything about editing brushes used to be fields on the one global Editor:
// which level, which chunk, what is selected, which selection unit clicks
// address, whether the stamp is armed, what the gizmo is dragging. That was
// exactly right while there was one viewport that edited brushes.
//
// There are two now. The entity editor builds a *type* out of brushes and
// models (world/entity_brush.go), and an author who has blocked out a room in
// the level editor should not have to learn a second set of tools to block out
// a ship's hold. So the state moves here, into a brushWorkspace, and each
// window holds one:
//
//	Editor.brushWorkspace   the level editor's — the level, its chunks, its
//	                        selections. Still reachable as Editor.level,
//	                        Editor.SelectedFaces and so on, because
//	                        EditorState embeds this struct.
//	entityWS                the entity editor's, over a scratch level holding
//	                        one chunk of the type's brushes.
//
// Everything below the selection — the stamps, extrude, inset, subdivide,
// picking, the face and vertex resolvers, the convexity check, the panels —
// reads ws() rather than Editor, and so serves whichever window is asking.
//
// # How "whichever window is asking" is decided
//
// The level editor's workspace is the default and the resting state. The entity
// editor makes its own current for the duration of its draw and its event
// handling, and puts it back:
//
//	defer entityUseWorkspace()()
//
// Two call sites, both at the top of a function that owns a whole frame or a
// whole event. That is the whole mechanism, and it is deliberately not a
// "current window" the two windows both write: a workspace left current after
// the window that installed it has returned would silently point the level
// editor's own tools at a type's geometry, and the failure would look like the
// level editor being haunted rather than like a missing restore.
//
// # What a window has to supply
//
// The tools need a camera to cast a pick ray through, a projection to size
// screen-space handles against, somewhere to put a status line, and an undo
// stack to push onto. Those are the four things that genuinely differ between a
// window drawing a level and a window drawing one entity, and they are function
// fields rather than an interface so that constructing a workspace is a struct
// literal rather than a type.

import (
	"foundry/internal/gfx"
	"foundry/internal/platform"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// brushWorkspace is one window's geometry-editing state.
//
// The field names are unchanged from when they lived on EditorState, and
// EditorState embeds this, so every level-editor-only file that says
// Editor.level or Editor.SelectedFaces still compiles and still means the level
// editor. What changed is that the *shared* files say ws() instead.
type brushWorkspace struct {
	// level is what this workspace edits. The level editor's is the open level;
	// the entity editor's is a scratch level holding exactly one chunk, whose
	// brushes are the type's own.
	level *world.Level

	// ActiveChunkIdx is the chunk being edited. Always valid — a new brush
	// joins it, and the entity workspace has only ever one.
	ActiveChunkIdx int
	// ActiveBrushIdx/ActiveFaceIdx address the primary selected face within the
	// active chunk; -1 for none. Both are *derived*: they mirror
	// SelectedFaces[0]. Write them through editor_face_sel.go, never directly.
	ActiveBrushIdx int
	ActiveFaceIdx  int

	// Brush faces selected by clicking them in face mode. The first entry is
	// the primary and mirrors ActiveBrushIdx/ActiveFaceIdx; ctrl+click adds the
	// rest. Empty = no face selection.
	SelectedFaces []faceSel

	// Brush vertices selected by clicking their handle boxes. The first entry
	// is the primary one; ctrl+click adds the rest.
	SelectedVertices []vertexSel

	// Whole brushes selected by clicking them in object mode. The first entry
	// is the primary; ctrl+click adds the rest. See editor_brush_sel.go.
	SelectedBrushes []brushSel

	// EditContext is whether a click and a gizmo address whole objects, the
	// points, edges and faces one brush is made of, or the same three units of
	// its detail mesh. Object mode is the zero value and Tab switches; see
	// editor_3d_view.go.
	EditContext editorEditContext

	// DetailBrush is the brush detail mode is shaping — meaningless in any
	// other context. See editor_detail.go.
	DetailBrush brushSel

	// SelMode is what a click selects: points, edges, or faces and objects. See
	// the editorSel* constants (editor_3d_view.go).
	SelMode editorSelMode

	// BrushPlaceActive is true while the brush stamp is armed, and it survives
	// the click that uses it: blocking out a room is a dozen boxes in a row.
	// BrushPlaceKind is which primitive it stamps.
	BrushPlaceActive bool
	BrushPlaceKind   string

	// GizmoState is the move/rotate/scale tool and whatever drag is in
	// progress. Per workspace because a drag belongs to the viewport it was
	// started in, and because the two windows can have different tools in hand.
	GizmoState

	// Wireframe draws the geometry as lines, which also makes every point and
	// edge in range selectable instead of only the ones the camera can see.
	// See editor_3d_visibility.go.
	Wireframe bool

	// StepSize is the drag quantum and, with SnapToGrid, the snap quantum.
	StepSize   float32
	SnapToGrid bool

	// PanelFocus is a one-shot request from a selection change to open (and
	// scroll to) a properties section. Cleared by the panel that applies it.
	PanelFocus string

	// ---------- what the owning window supplies ----------
	//
	// nil in every one of these means "the level editor's", so the zero
	// workspace behaves as the level editor's did and a test fixture does not
	// have to build a camera.

	// rayFn casts a pick ray through this window's own camera and projection.
	rayFn func(sx, sy int32) (origin, dir mgl32.Vec3)
	// projViewFn, camPosFn and viewSizeFn are what screen-space handle sizing
	// and the overlay drawing need: where the eye is, what it projects with,
	// and how many pixels wide the viewport is.
	projViewFn func() mgl32.Mat4
	camPosFn   func() mgl32.Vec3
	viewSizeFn func() (int32, int32)
	// projYYFn is the projection's [1][1] term — the vertical focal length that
	// turns a pixel radius into a world-space one. Separate from projViewFn
	// because the sizing maths wants the projection alone, unmultiplied by the
	// view.
	projYYFn func() float32
	// undoFn snapshots the document before a structural edit, and statusFn puts
	// a line where this window shows one.
	undoFn   func()
	statusFn func(format string, args ...any)
	// editingFn reports whether this workspace's viewport is live at all — the
	// level editor's is false while the game is being played, and the entity
	// editor's is false when its window is shut.
	editingFn func() bool
	// placementArmedFn reports whether some OTHER tool in this window has armed
	// a click, so the gizmo stands aside for it. The level editor has half a
	// dozen (spawn, volume, entity, sprite, sound, emitter, prefab, scenery);
	// the entity editor has only the brush stamp, which this already knows
	// about.
	placementArmedFn func() bool
	// disarmOthersFn puts away every OTHER click-to-place tool in this window,
	// so two are never armed at once and "which one answers the click" is never
	// a question about the order of a chain of ifs.
	disarmOthersFn func()
	// panelEdgeFn is the x below which the cursor is over the viewport rather
	// than over this window's side panel. The stamp's ghost and the gizmo's
	// hover test both need it, and the two windows' panels are different widths
	// on different sides of different windows.
	panelEdgeFn func() int32
	// changedFn is called after an edit rewrites geometry, for whatever the
	// window has to rebuild — the entity editor's preview, and nothing at all
	// for the level editor, whose chunks own their own buffers.
	changedFn func()
}

// activeWS is the workspace the shared tools act on. Never nil: it starts as
// the level editor's and every window that installs its own restores this one.
var activeWS = &Editor.brushWorkspace

// ws is the workspace every shared geometry file reads instead of Editor.
func ws() *brushWorkspace { return activeWS }

// use installs w as the active workspace and returns the restore. Called as
// `defer w.use()()` at the top of a window's draw or event handler.
func (w *brushWorkspace) use() func() {
	prev := activeWS
	activeWS = w
	return func() { activeWS = prev }
}

// ---------- the window's four services, with the level editor's defaults ----------

func (w *brushWorkspace) ray(sx, sy int32) (origin, dir mgl32.Vec3) {
	if w.rayFn != nil {
		return w.rayFn(sx, sy)
	}
	return screenRay(sx, sy)
}

func (w *brushWorkspace) projView() mgl32.Mat4 {
	if w.projViewFn != nil {
		return w.projViewFn()
	}
	return gfx.Projection.Mul4(gfx.Cam.Matrix)
}

func (w *brushWorkspace) camPos() mgl32.Vec3 {
	if w.camPosFn != nil {
		return w.camPosFn()
	}
	return gfx.Cam.Position
}

func (w *brushWorkspace) viewSize() (int32, int32) {
	if w.viewSizeFn != nil {
		return w.viewSizeFn()
	}
	return platform.WinWidth, platform.WinHeight
}

func (w *brushWorkspace) projYY() float32 {
	if w.projYYFn != nil {
		return w.projYYFn()
	}
	return gfx.Projection[5]
}

// pushUndo snapshots before a structural edit. The one call every mutating tool
// makes, so a tool written for one window is undoable in both.
func (w *brushWorkspace) pushUndo() {
	if w.undoFn != nil {
		w.undoFn()
		return
	}
	EditorPushUndo()
}

func (w *brushWorkspace) setStatus(format string, args ...any) {
	if w.statusFn != nil {
		w.statusFn(format, args...)
		return
	}
	editorSetStatus(format, args...)
}

// editing reports whether this workspace's viewport is live and can be clicked
// in.
//
// For the level editor that is its mode: Mode3D is the viewport, and ModeGame
// is the level being played, where a click is a gameplay input and not a
// selection.
func (w *brushWorkspace) editing() bool {
	if w.editingFn != nil {
		return w.editingFn()
	}
	return Editor.Mode == Mode3D
}

// placementArmed reports whether any click-to-place tool in this window is
// waiting for a click, the brush stamp included.
func (w *brushWorkspace) placementArmed() bool {
	if w.BrushPlaceActive {
		return true
	}
	if w.placementArmedFn != nil {
		return w.placementArmedFn()
	}
	return Editor.SpawnPlacementMode || Editor.EntityPlacementMode ||
		Editor.VolumePlacementMode || Editor.SpritePlacementMode ||
		Editor.EmitterPlacementMode || Editor.SoundPlacementMode ||
		Editor.PrefabPlacementMode || Editor.BrushActive
}

// disarmOtherPlacements puts away every other click-to-place tool in this
// window. The level editor has eight; the entity editor has none besides the
// brush stamp itself.
func (w *brushWorkspace) disarmOtherPlacements() {
	if w.disarmOthersFn != nil {
		w.disarmOthersFn()
		return
	}
	editorDisarmBrush()
	Editor.EntityPlacementMode = false
	Editor.PrefabPlacementMode = false
	Editor.VolumePlacementMode = false
	Editor.SpritePlacementMode = false
	Editor.EmitterPlacementMode = false
	Editor.SoundPlacementMode = false
	Editor.SpawnPlacementMode = false
}

// cursorOverPanel reports whether a viewport x is actually over this window's
// side panel, where a ray would be cast through a pixel the viewport does not
// own.
func (w *brushWorkspace) cursorOverPanel(mx int32) bool {
	if w.panelEdgeFn != nil {
		return mx >= w.panelEdgeFn()
	}
	return mx >= panelLeftEdgeX()
}

// geometryChanged tells the owning window that an edit rewrote brushes, so it
// can rebuild whatever it derives from them.
//
// The level editor needs nothing: a brush owns its own GPU buffers and the
// tools reload the one they touched. The entity editor's preview is a separate
// materialisation of the same brushes and has to be re-stamped.
func (w *brushWorkspace) geometryChanged() {
	if w.changedFn != nil {
		w.changedFn()
	}
}

// activeChunkOf is activeChunk() for an arbitrary workspace — the chunk new
// geometry joins and existing geometry is looked up in.
func (w *brushWorkspace) activeChunk() *world.Chunk {
	if w.level == nil || w.ActiveChunkIdx < 0 || w.ActiveChunkIdx >= len(w.level.Chunks) {
		return nil
	}
	return &w.level.Chunks[w.ActiveChunkIdx]
}
