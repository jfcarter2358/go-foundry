package main

// editor.go — core level editor state machine.
//
// Toggle with the tilde (~) key. Two sub-modes:
//   Mode3D : fly-around chunk/light/spawn editing
//   Mode2D : top-down orthographic sector editor for the active chunk
//
// The editor owns all input while active; normal gameplay logic is bypassed.

import (
	"math"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// EditorMode distinguishes the two editor sub-modes.
type EditorMode int

const (
	ModeGame      EditorMode = iota // editor inactive — normal gameplay
	Mode3D                          // 3D fly-around editor
	Mode2D                          // 2D top-down sector editor (+ biome painting)
	ModeSurface2D                   // 2D surface editor for extrusions/terrain
)

// GizmoState is the 3D viewport's gizmo interaction state — embedded
// (anonymously) in EditorState so existing
// Editor.GizmoKind-style field access keeps working unchanged via Go's
// field promotion; see RESTRUCTURE_PROPOSAL.md §5 Phase A.
//
// The three tools (move, rotate, scale) share this one set of fields: only one
// gizmo is on screen at a time, and only one drag can be in progress. What the
// shared fields mean depends on GizmoDragTool — see GizmoDragStartS.
type GizmoState struct {
	GizmoKind     string          // what the gizmo targets; see the gizmoKind* constants
	GizmoTool     editorGizmoTool // which gizmo is on screen; see the editorTool* constants
	GizmoDragging bool            // an axis drag is in progress
	GizmoDragTool editorGizmoTool // the tool the in-progress drag belongs to
	// GizmoDragAxis is the handle being dragged: 0=X, 1=Y, 2=Z, and for the
	// scale tool also modelScaleUniformAxis for the centre handle. -1 = not
	// dragging.
	GizmoDragAxis   int
	GizmoDragOrigin mgl32.Vec3 // gizmo position when the drag started
	// GizmoDragStartS is the measure the drag is an offset from, in whatever
	// unit its tool works in: distance along the axis for move, an angle in
	// radians for rotate, and the reference a factor is a ratio of for scale.
	GizmoDragStartS   float32
	GizmoDragStartPos mgl32.Vec3 // spawn-point position, chunk origin or entity position at mouse-down
	// GizmoDragStartRot/Scale are the entity's own rotation and scale at
	// mouse-down. A vertex selection has neither — rotating it moves the points
	// themselves — so these are only read on the entity path.
	GizmoDragStartRot   mgl32.Vec3
	GizmoDragStartScale mgl32.Vec3
	GizmoDragPoints     []mgl32.Vec2 // per selected vertex: chunk point XZ at mouse-down
	GizmoDragOffsets    []float32    // per selected vertex: height offset at mouse-down
	// GizmoDragWorld is per selected vertex: its world position at mouse-down.
	// Rotate and scale work from this rather than from the XZ/offset pair,
	// since both are defined on positions relative to the selection's centre.
	GizmoDragWorld []mgl32.Vec3
}

// Gizmo2DState is the top-down map's gizmo interaction state, embedded in
// EditorState the way GizmoState is.
//
// It is deliberately separate from the 3D gizmo's rather than shared: the two
// views can both be on screen at once (the 2D map pops out into a window of its
// own), each resolves the cursor through its own projection, and a drag started
// in one must not be continued by motion events arriving from the other.
type Gizmo2DState struct {
	Gizmo2DDragging bool            // a handle drag is in progress on the map
	Gizmo2DTool     editorGizmoTool // the tool the in-progress drag belongs to
	// Gizmo2DAxis is the handle being dragged: 0=X, 2=Z (world axes, matching
	// gizmoAxes), gizmo2DPlaneAxis for the free-move square, and
	// modelScaleUniformAxis for scale's centre handle. -1 = not dragging.
	Gizmo2DAxis   int
	Gizmo2DCenter mgl32.Vec2 // selection centre in world XZ at mouse-down
	// Gizmo2DStartS is the measure the drag is an offset from, in its tool's
	// unit: world units along the axis, radians, or a scale reference.
	Gizmo2DStartS     float32
	Gizmo2DStartWorld mgl32.Vec2   // cursor position in world XZ at mouse-down
	Gizmo2DStartPts   []mgl32.Vec3 // per selected vertex: world position at mouse-down
	Gizmo2DStartSpawn mgl32.Vec3   // spawn-point position at mouse-down
	// The selected entity's own transform at mouse-down. Kept apart from the
	// 3D gizmo's snapshot of the same thing for the reason this whole struct
	// is: the map can be a window of its own, so a drag can be in progress in
	// each view at once.
	Gizmo2DStartEntity      mgl32.Vec3
	Gizmo2DStartEntityRot   mgl32.Vec3
	Gizmo2DStartEntityScale mgl32.Vec3
}

// SurfaceEditorState is the per-surface (wall/floor/ceiling) UV-space 2D
// editor's state (editor_surface2d.go) — embedded in EditorState, see
// GizmoState's doc comment above for why.
type SurfaceEditorState struct {
	SurfaceKind        string           // "wall", "floor", "ceiling"
	SurfacePan         mgl32.Vec2       // pan center in surface coords (U,V or X,Z)
	SurfaceZoom        float32          // pixels per surface unit
	SurfaceEditLayer   string           // "both", "outer", "inner" — which extrusion points are draggable
	SurfacePlacing     string           // "extrusion", "terrain", "" (none)
	SurfacePendingPts  []ExtrusionPoint // polygon vertices being placed
	SurfaceDragPt      int              // index of point being dragged; -1 = none
	SurfaceDragKind    string           // "outer", "inner", "terrain" — what's being dragged
	SurfaceDragExtIdx  int              // extrusion index for outer/inner drag
	SurfaceLastMouse   [2]int32         // for pan
	SurfacePrevMode    EditorMode       // mode to return to on Escape
	ActiveTerrainPtIdx int              // selected terrain point index; -1 = none
}

// BiomeLayerState is the 2D biome-sector polygon layer's state
// (editor_biome.go) — embedded in EditorState, see GizmoState's doc comment
// above for why.
type BiomeLayerState struct {
	BiomeLayerActive     bool         // 2D view edits the biome-sector layer instead of structural geometry
	BiomeDrawing         bool         // placing points for a new sector polygon
	BiomeDrawPoints      []mgl32.Vec2 // points placed so far for the in-progress polygon
	ActiveBiomeSectorIdx int          // index into the active chunk's BiomeSectors; -1 = none
	BiomeDragVertexIdx   int          // vertex index being dragged on the selected sector; -1 = none
}

// EditorState holds all editor runtime state.
type EditorState struct {
	Active bool
	Mode   EditorMode

	// --- 3D mode ---
	ActiveChunkIdx  int    // chunk being displayed/edited
	ActiveSectorIdx int    // sector selected in 3D/2D mode
	ActiveEdgeKind  string // "wall" or "portal"
	ActiveEdgeIdx   int    // index within sector.Walls or sector.Portals; -1 = none

	// Face of the selected sector picked by a 3D click: "floor", "ceiling",
	// "wall", "portal", or "" for none. Walls/portals also set ActiveEdgeKind.
	//
	// Derived: this and ActiveEdgeKind/ActiveEdgeIdx mirror SelectedFaces[0].
	// Write it through editor_face_sel.go, never directly.
	ActiveSurface string

	// Sector faces selected by clicking them in face mode. The first entry is
	// the primary and mirrors ActiveSurface (and, for a wall or portal,
	// ActiveEdgeKind/Idx); ctrl+click adds the rest. Empty = no face selection.
	SelectedFaces []faceSel

	// Sector vertices selected by clicking their handle boxes in 3D. The first
	// entry is the primary one and mirrors ActiveVertexSI/VI; ctrl+click adds
	// the rest. Empty = no vertex selection.
	SelectedVertices []vertexSel

	// What a 3D click selects: points, edges, or faces and objects. See the
	// editorSel* constants (editor_3d_view.go).
	SelMode editorSelMode

	// Draw the world as lines rather than solid, which also makes every point
	// and edge in range selectable instead of only the ones the camera can
	// actually see. See editor_3d_visibility.go.
	Wireframe bool

	// --- Move/rotate/scale gizmos ---
	GizmoState
	Gizmo2DState
	// One-shot request from a selection change to open (and scroll to) a
	// properties section in the side panel. See the panelFocus* constants;
	// cleared by the panel once it has been applied.
	PanelFocus string

	// Directional light whose open tree node is showing in the light panel;
	// -1 = none. Drives the on-screen direction indicator only.
	SelectedDirLightIdx int

	// Saved gameplay camera state (restored on editor exit)
	SavedCamPos   mgl32.Vec3
	SavedCamYaw   float32
	SavedCamPitch float32

	// Spawn-point placement state: true when the next left-click drops (or
	// moves) the level's spawn marker.
	SpawnPlacementMode bool
	// SpawnSelected is whether the spawn marker is the current selection, which
	// is what gives it the gizmo and its properties section.
	SpawnSelected bool

	// --- Entities ---
	// ActiveEntityIdx indexes level.Entities; -1 = none selected. An index
	// rather than a pointer because undo swaps the whole level out from under
	// the editor, and a pointer into the old one would outlive it.
	ActiveEntityIdx int
	// EntityPlacementMode is true when the next left-click drops an instance of
	// EntityPlaceType into the level.
	EntityPlacementMode bool
	EntityPlaceType     string
	// EntityDefNames is the definition catalog, rescanned rather than watched —
	// see editorRefreshEntityDefs.
	EntityDefNames []string
	EntityDefIdx   int32
	// NewEntityID is the ID field of the placement section, empty for "name it
	// after its type".
	NewEntityID string
	// Text/type fields of the selected entity's "add a tag" and "add an
	// attribute" rows. They live on the editor rather than in imgui's own
	// storage because both are read when their button is pressed, which is a
	// different frame from the one the text was typed in.
	NewTagText  string
	NewAttrName string
	NewAttrType int32

	// Chunk creation dialog
	NewChunkID   string
	ShowNewChunk bool

	// Chunk save/import/delete
	ShowSaveChunk    bool
	SaveChunkName    string
	ShowDeleteChunk  bool
	ShowImportChunk  bool
	ImportChunkFiles []string
	ImportChunkIdx   int32

	// --- 2D mode ---
	Pan2D        mgl32.Vec2 // center of the 2D view in world XZ
	Zoom2D       float32    // pixels per world unit
	DragVertexSI int        // sector index of dragged vertex; -1 = none (set by EditorInit)
	DragVertexVI int        // vertex index within that sector; -1 = none (set by EditorInit)
	DragIsFloor  bool       // true = dragging floor-height dot; false = ceiling

	// Persistent vertex selection (separate from drag — survives mouse release)
	ActiveVertexSI int // sector index of selected vertex; -1 = none
	ActiveVertexVI int // vertex index of selected vertex; -1 = none

	// Last mouse position (2D mode panning)
	LastMouse2D [2]int32

	// In 2D mode, whether the floor vertices (true) or ceiling vertices (false) are being edited
	EditFloorVerts bool

	// Draw a box around every selectable point instead of only the ones the
	// cursor is near, so overlapping/hard-to-see points can be found. In 2D
	// the box is each chunk point's click-proximity radius; in 3D it is every
	// vertex handle within pick range of the camera, plus the edge prisms
	// joining them.
	ShowPointSelectRadius bool

	// Point connection state for ctrl+click workflow
	PendingEdges [][2]int // edges not yet part of a sector (chunk point indices)

	// Navigation button state (reset each frame after applying)
	NavForward, NavBack, NavLeft, NavRight, NavUp, NavDown bool

	// Extrusion editing state (legacy 3D placement — kept for compatibility)
	ExtrusionMode      bool             // placing extrusion vertices
	PendingOuter       []ExtrusionPoint // outer polygon being placed
	PendingInner       []ExtrusionPoint // inner polygon being placed
	PlacingInner       bool             // false=placing outer, true=placing inner
	ActiveExtrusionIdx int              // selected extrusion on active wall; -1 = none

	// --- Surface 2D editor ---
	SurfaceEditorState

	// Right-click drag rotation state (3D mode)
	MouseDragging3D bool
	// Middle-button camera drag (3D mode): turns like the right-drag does, or
	// pans when shift was held at mouse-down — PanDragging3D is latched there
	// so letting go of shift mid-drag does not change what the drag means.
	MiddleDragging3D     bool
	PanDragging3D        bool
	LastDragX, LastDragY int32

	// Drag step / snap-to-grid
	StepSize   float32 // drag speed; also snap quantum
	SnapToGrid bool    // round values to nearest StepSize multiple

	// Side panel width (resizable by dragging the left edge)
	PanelWidth    float32
	PanelDragging bool

	// Frame timing
	LastTick uint32

	// Level reference (set by EditorInit)
	level *Level

	// Pending save path
	SavePath string

	// Status message (shown briefly in the toolbar after save/load)
	StatusMsg  string
	StatusTime uint32

	// Level picker dialog
	ShowLevelPicker bool
	LevelFiles      []string
	NewLevelName    string
	LevelsDir       string

	// Save As dialog
	ShowSaveAs bool
	SaveAsName string

	// --- 2D biome sector layer ---
	BiomeLayerState
}

// Editor is the global editor instance.
var Editor EditorState

// EditorInit must be called once after LoadLevel and after the OpenGL context
// is current. It initialises ImGui and the gizmo shaders.
func EditorInit(level *Level, levelPath string, levelsDir string) {
	Editor.level = level
	Editor.LevelsDir = levelsDir
	Editor.Active = false
	Editor.Mode = ModeGame
	Editor.ActiveChunkIdx = 0
	Editor.ActiveSectorIdx = -1
	Editor.ActiveEdgeIdx = -1
	Editor.ActiveSurface = ""
	Editor.SelectedFaces = nil
	Editor.PanelFocus = ""
	Editor.SelectedDirLightIdx = -1
	Editor.SpawnPlacementMode = false
	Editor.SpawnSelected = false
	Editor.ActiveEntityIdx = -1
	Editor.EntityPlacementMode = false
	Editor.EntityPlaceType = ""
	Editor.DragVertexSI = -1
	Editor.DragVertexVI = -1
	Editor.ActiveVertexSI = -1
	Editor.ActiveVertexVI = -1
	Editor.SelectedVertices = nil
	Editor.GizmoKind = gizmoKindNone
	Editor.GizmoTool = editorToolMove
	Editor.GizmoDragAxis = -1
	Editor.GizmoDragging = false
	Editor.Gizmo2DAxis = -1
	Editor.Gizmo2DDragging = false
	Editor.PendingEdges = nil
	Editor.ActiveExtrusionIdx = -1
	Editor.ExtrusionMode = false
	Editor.PendingOuter = nil
	Editor.PendingInner = nil
	Editor.PlacingInner = false
	Editor.SurfaceZoom = 40.0
	Editor.SurfaceDragPt = -1
	Editor.ActiveTerrainPtIdx = -1
	Editor.SurfacePlacing = ""
	Editor.SurfaceEditLayer = "both"
	Editor.Zoom2D = 20.0    // 20 px per world unit
	Editor.PanelWidth = 360 // default side panel width
	Editor.StepSize = 0.25
	Editor.SnapToGrid = false
	Editor.EditFloorVerts = true
	Editor.LastTick = sdl.GetTicks()
	Editor.SavePath = levelPath

	ImGuiInit()
	GizmoInit()
	GrassInit()
	DetailInit()

	// Biome sector layer defaults
	Editor.BiomeLayerActive = false
	Editor.BiomeDrawing = false
	Editor.BiomeDrawPoints = nil
	Editor.ActiveBiomeSectorIdx = -1
	Editor.BiomeDragVertexIdx = -1

}

const panelGrabZone int32 = 6
const panelMinWidth float32 = 250

// panelLeftEdgeX returns the screen X coordinate of the panel's left edge.
func panelLeftEdgeX() int32 {
	return int32(float32(WinWidth) - Editor.PanelWidth)
}

// panelHandleResize checks SDL mouse events for drag-resizing the main
// window's side panel. Returns true if the event was consumed by the resize
// handle.
func panelHandleResize(event sdl.Event) bool {
	return panelHandleResizeAt(event, WinWidth)
}

// panelHandleResizeAt is panelHandleResize against a window of width viewW —
// the popped-out 2D editor (editor_2d_window.go) hosts the same panel in a
// window of its own size. Editor.PanelWidth is shared: the panel is the same
// width wherever it is drawn, and only one window can be dragging it at once.
func panelHandleResizeAt(event sdl.Event, viewW int32) bool {
	edge := int32(float32(viewW) - Editor.PanelWidth)

	switch t := event.(type) {
	case sdl.MouseButtonEvent:
		if t.Button != 1 { // left mouse button
			return false
		}
		if t.Type == sdl.MOUSEBUTTONDOWN {
			if t.X >= edge-panelGrabZone && t.X <= edge+panelGrabZone && t.Y > 28 {
				Editor.PanelDragging = true
				return true
			}
		} else if t.Type == sdl.MOUSEBUTTONUP && Editor.PanelDragging {
			Editor.PanelDragging = false
			return true
		}

	case sdl.MouseMotionEvent:
		if Editor.PanelDragging {
			newWidth := float32(viewW) - float32(t.X) - 5
			if newWidth < panelMinWidth {
				newWidth = panelMinWidth
			}
			maxWidth := float32(viewW) - 20
			if newWidth > maxWidth {
				newWidth = maxWidth
			}
			Editor.PanelWidth = newWidth
			return true
		}
		// Show resize cursor when hovering near the edge, reset otherwise
		if t.X >= edge-panelGrabZone && t.X <= edge+panelGrabZone && t.Y > 28 {
			sdl.SetCursor(sdl.CreateSystemCursor(sdl.SYSTEM_CURSOR_SIZEWE))
		} else if !Editor.PanelDragging {
			sdl.SetCursor(sdl.CreateSystemCursor(sdl.SYSTEM_CURSOR_ARROW))
		}
	}

	return false
}

// EditorToggle turns the editor on or off (called on tilde keypress).
func EditorToggle() {
	if Editor.Active {
		// Entering gameplay puts the player on the level's spawn point. Only a
		// level with no spawn marker falls back to restoring wherever the
		// camera was before the editor was opened.
		if !ApplySpawnPoint(Editor.level) {
			Cam.Position = Editor.SavedCamPos
			Cam.Yaw = Editor.SavedCamYaw
			Cam.Pitch = Editor.SavedCamPitch
			updateCamDirection()
			updateCamMatrix()
		}

		// Re-enable mouse lock for gameplay
		SetMouseLock(true)

		// The pop-out only makes sense while the editor is up.
		CloseEditor2DWindow()

		GravityEnabled = true
		Editor.Active = false
		Editor.Mode = ModeGame
	} else {
		// Save gameplay camera
		Editor.SavedCamPos = Cam.Position
		Editor.SavedCamYaw = Cam.Yaw
		Editor.SavedCamPitch = Cam.Pitch

		// Release mouse for ImGui
		SetMouseLock(false)

		// Enter 3D fly mode (gravity off)
		GravityEnabled = false
		IsOnGround = false
		JumpRequested = false

		Editor.Active = true
		Editor.Mode = Mode3D
		Editor.Pan2D = mgl32.Vec2{Cam.Position.X(), Cam.Position.Z()}
		// Re-derive the orbit pivot from where the player left the camera, so
		// an orbit carried over from the last editing session does not yank the
		// view back to somewhere they have since walked away from.
		editorCamEnter()
	}
}

// EditorHandleEvent processes one SDL event for the editor.
// Returns true if the editor consumed the event (game should ignore it).
func EditorHandleEvent(event sdl.Event) bool {
	editorSyncPoppedOutMode()

	// Check panel resize before ImGui so the drag handle takes priority.
	if panelHandleResize(event) {
		return true
	}

	// Always feed into ImGui
	consumed := ImGuiProcessEvent(event)

	switch t := event.(type) {
	case sdl.KeyboardEvent:
		// Surface 2D mode intercepts keys first
		if Editor.Mode == ModeSurface2D && surface2DHandleKey(t) {
			return true
		}
		// Extrusion placement mode intercepts keys next
		if Editor.ExtrusionMode && editorExtrusionHandleKey(t) {
			return true
		}
		// Biome sector polygon placement intercepts keys next
		if Editor.Mode == Mode2D && biomeHandleKey(t) {
			return true
		}
		if t.Type != sdl.KEYDOWN {
			break
		}

		// Ctrl+Z / Ctrl+Y (or Ctrl+Shift+Z) — undo/redo.
		if !imgui.CurrentIO().WantCaptureKeyboard() {
			mods := sdl.GetModState()
			if mods&sdl.KMOD_CTRL != 0 {
				switch t.Keysym.Scancode {
				case sdl.SCANCODE_Z:
					if mods&sdl.KMOD_SHIFT != 0 {
						EditorRedo()
					} else {
						EditorUndo()
					}
					return true
				case sdl.SCANCODE_Y:
					EditorRedo()
					return true
				}
			}
		}

		switch t.Keysym.Scancode {
		case sdl.SCANCODE_TAB:
			// Toggle 2D / 3D sub-mode (surface mode is separate)
			if Editor.Mode == Mode3D {
				if Editor2DPoppedOut() {
					// The 2D view already has a window; the main one stays 3D.
					editor2DWindow.Win.Raise()
					return true
				}
				Editor.Mode = Mode2D
				Editor.Pan2D = mgl32.Vec2{Cam.Position.X(), Cam.Position.Z()}
			} else if Editor.Mode == Mode2D {
				Editor.Mode = Mode3D
			}
			// Tab in surface mode is handled by surface2D event handler
			// Always keep cursor free in editor — both modes need visible mouse
			// for selection, clicking edges, and placing vertices.
			SetMouseLock(false)
			return true

		case sdl.SCANCODE_DELETE:
			if !imgui.CurrentIO().WantCaptureKeyboard() {
				EditorDeleteSelected()
			}

		// View switches, again on the model editor's shortcuts: 1/2/3 pick the
		// selection unit, G/R/C pick the gizmo, Z swaps shading, V swaps
		// camera, F frames what is selected. Same guard as the tool keys —
		// typing an id into a panel field must not also change the view
		// underneath it.
		//
		// 1/2/3 and G/R/C answer in both viewports: the selection and the tool
		// are shared between them (editor_3d_view.go, editor_sel_tool.go), so
		// binding them to the 3D view alone would leave the 2D map able to show
		// a mode it could not switch.
		case sdl.SCANCODE_1:
			if editorSelKeysActive() {
				editorSetSelMode(editorSelPoint)
				return true
			}
		case sdl.SCANCODE_2:
			if editorSelKeysActive() {
				editorSetSelMode(editorSelEdge)
				return true
			}
		case sdl.SCANCODE_3:
			if editorSelKeysActive() {
				editorSetSelMode(editorSelFace)
				return true
			}
		case sdl.SCANCODE_G:
			if editorSelKeysActive() {
				editorSetGizmoTool(editorToolMove)
				return true
			}
		case sdl.SCANCODE_R:
			if editorSelKeysActive() {
				editorSetGizmoTool(editorToolRotate)
				return true
			}
		case sdl.SCANCODE_C:
			if editorSelKeysActive() {
				editorSetGizmoTool(editorToolScale)
				return true
			}
		// J joins the picked points into edges — the map's replacement for the
		// ctrl+click chain that used to draw them one at a time. Shift+J also
		// closes the loop, which is what turns an outline into a sector.
		case sdl.SCANCODE_J:
			if Editor.Mode == Mode2D && !imgui.CurrentIO().WantCaptureKeyboard() {
				EditorJoinSelectedPoints(sdl.GetModState()&sdl.KMOD_SHIFT != 0)
				return true
			}
		case sdl.SCANCODE_Z:
			// Bare Z only: Ctrl+Z is undo, and that is handled above.
			if Editor.Mode == Mode3D && !imgui.CurrentIO().WantCaptureKeyboard() {
				editorSetWireframe(!Editor.Wireframe)
				return true
			}
		case sdl.SCANCODE_V:
			if Editor.Mode == Mode3D && !imgui.CurrentIO().WantCaptureKeyboard() {
				editorSetFlyCam(!editorCam3D.Fly)
				return true
			}
		case sdl.SCANCODE_F:
			if Editor.Mode == Mode3D && !imgui.CurrentIO().WantCaptureKeyboard() {
				editorFrameSelection()
				return true
			}
		}

	case sdl.MouseButtonEvent:
		if imgui.CurrentIO().WantCaptureMouse() {
			break
		}
		if t.Type == sdl.MOUSEBUTTONDOWN {
			if Editor.Mode == ModeSurface2D {
				Surface2DHandleClick(t)
			} else if Editor.Mode == Mode3D {
				if t.Button == sdl.ButtonRight {
					// Right-drag turns the camera; a right-click that does not
					// drag opens the context menu instead, which is what
					// editor3DContextPressed is watching for.
					Editor.MouseDragging3D = true
					Editor.LastDragX = t.X
					Editor.LastDragY = t.Y
					editor3DContextPressed(t.X, t.Y)
				} else if t.Button == sdl.ButtonMiddle {
					// Middle-drag turns as well, and shift+middle pans — the
					// model editor's gestures, so the two viewports are flown
					// and orbited with the same hand.
					Editor.MiddleDragging3D = true
					Editor.PanDragging3D = sdl.GetModState()&sdl.KMOD_SHIFT != 0
					Editor.LastDragX = t.X
					Editor.LastDragY = t.Y
				} else if Editor.ExtrusionMode && editorExtrusionHandleClick(t) {
					// Extrusion placement consumed the click
				} else if editorGizmoMouseDown(t) {
					// Grabbed an axis arm — the click must not re-select.
				} else {
					Editor3DHandleClick(t)
				}
			} else if Editor.Mode == Mode2D {
				Editor2DHandleClick(t)
			}
		} else if t.Type == sdl.MOUSEBUTTONUP {
			if t.Button == sdl.ButtonLeft && editorGizmoDragging() {
				editorGizmoMouseUp()
				return true
			}
			if Editor.Mode == ModeSurface2D {
				Surface2DHandleRelease(t)
			} else if Editor.Mode == Mode3D && t.Button == sdl.ButtonRight {
				Editor.MouseDragging3D = false
				editor3DContextReleased(t.X, t.Y)
			} else if Editor.Mode == Mode3D && t.Button == sdl.ButtonMiddle {
				// Not gated on WantCaptureMouse: releasing over the side panel
				// must still end a drag that began on the viewport, or the
				// camera would stay stuck to the mouse.
				Editor.MiddleDragging3D = false
				Editor.PanDragging3D = false
			} else if Editor.Mode == Mode2D {
				Editor2DHandleRelease(t)
			}
		}

	case sdl.MouseMotionEvent:
		// Ahead of everything else, including the gizmo: a right-button gesture
		// that travels is a camera turn, not a context click, and that has to
		// be recorded whatever else the motion is doing.
		editor3DContextMoved(t.X, t.Y)
		// An axis drag owns the mouse until release.
		if editorGizmoDragging() {
			editorGizmoMouseMove(t.X, t.Y)
			return true
		}
		if Editor.Mode == ModeSurface2D {
			Surface2DHandleMouseMove(t)
		} else if Editor.Mode == Mode3D && (Editor.MouseDragging3D || Editor.MiddleDragging3D) {
			dx := t.X - Editor.LastDragX
			dy := t.Y - Editor.LastDragY
			// One rotation path for both camera modes: the angles change either
			// way, and editorCamLook decides whether that turns the head in
			// place or swings the eye around the pivot.
			if Editor.PanDragging3D {
				editorCamPan(dx, dy)
			} else {
				editorCamLook(dx, dy)
			}
			Editor.LastDragX = t.X
			Editor.LastDragY = t.Y
		} else if Editor.Mode == Mode3D && Cam.MouseLock {
			// 3D fly: handled by existing HandleEvent (mouse look)
		} else if Editor.Mode == Mode2D {
			Editor2DHandleMouseMove(t)
		}

	case sdl.MouseWheelEvent:
		if Editor.Mode == ModeSurface2D && !imgui.CurrentIO().WantCaptureMouse() {
			Editor.SurfaceZoom *= float32(math.Pow(1.1, float64(t.Y)))
			if Editor.SurfaceZoom < 2 {
				Editor.SurfaceZoom = 2
			}
			if Editor.SurfaceZoom > 500 {
				Editor.SurfaceZoom = 500
			}
			return true
		}
		if Editor.Mode == Mode2D && !imgui.CurrentIO().WantCaptureMouse() {
			Editor2DZoom(t.Y)
			return true
		}
		if Editor.Mode == Mode3D && !imgui.CurrentIO().WantCaptureMouse() {
			editorCamZoom(t.Y)
			return true
		}
	}

	return consumed
}

// EditorTick is called every frame when the editor is active.
// In 3D mode it handles fly-camera input; in 2D mode it handles pan.
func EditorTick(keyStates []uint8) {
	now := sdl.GetTicks()
	// dt := float32(now-Editor.LastTick) / 1000.0
	Editor.LastTick = now

	if imgui.CurrentIO().WantCaptureKeyboard() {
		return // ImGui is capturing text input
	}

	// sdl.GetKeyboardState is global, not per-window: without this, typing in
	// a secondary window (the texture editor, the popped-out 2D editor) would
	// also fly the main window's camera.
	if Window.GetFlags()&sdl.WINDOW_INPUT_FOCUS == 0 {
		return
	}

	if Editor.Mode == Mode3D {
		if Editor.Mode == Mode3D && !editorCam3D.Fly {
			// Orbiting: the same keys, but they slide the pivot rather than
			// the eye (editor_3d_camera.go).
			editorOrbitKeys(keyStates)
		} else {
			// Free-fly using existing HandleInput (gravity is already off)
			HandleInput(keyStates)
		}

		// Apply navigation button movement
		dtScale := DeltaTime * 60.0
		navSpeed := MovementSpeed * dtScale
		if Editor.NavForward {
			editorNavMove(Editor.Mode, navSpeed, 0, 0)
		}
		if Editor.NavBack {
			editorNavMove(Editor.Mode, -navSpeed, 0, 0)
		}
		if Editor.NavLeft {
			editorNavMove(Editor.Mode, 0, 0, navSpeed)
		}
		if Editor.NavRight {
			editorNavMove(Editor.Mode, 0, 0, -navSpeed)
		}
		if Editor.NavUp {
			editorNavMove(Editor.Mode, 0, navSpeed, 0)
		}
		if Editor.NavDown {
			editorNavMove(Editor.Mode, 0, -navSpeed, 0)
		}
		if Editor.NavForward || Editor.NavBack || Editor.NavLeft || Editor.NavRight || Editor.NavUp || Editor.NavDown {
			Cam.Matrix = mgl32.LookAtV(Cam.Position, Cam.Position.Add(Cam.Direction), mgl32.Vec3{0, 1, 0})
			gl.UniformMatrix4fv(Cam.Uniform, 1, false, &Cam.Matrix[0])
		}
		Editor.NavForward = false
		Editor.NavBack = false
		Editor.NavLeft = false
		Editor.NavRight = false
		Editor.NavUp = false
		Editor.NavDown = false
	} else if Editor.Mode == Mode2D {
		// Pan with WASD in 2D
		panSpeed := float32(5.0) / Editor.Zoom2D * 10
		if keyStates[sdl.SCANCODE_W] != 0 {
			Editor.Pan2D[1] -= panSpeed
		}
		if keyStates[sdl.SCANCODE_S] != 0 {
			Editor.Pan2D[1] += panSpeed
		}
		if keyStates[sdl.SCANCODE_A] != 0 {
			Editor.Pan2D[0] -= panSpeed
		}
		if keyStates[sdl.SCANCODE_D] != 0 {
			Editor.Pan2D[0] += panSpeed
		}
	}
}

// EditorRender draws all editor overlays, then the ImGui UI.
func EditorRender() {
	editorSyncPoppedOutMode()

	dt := DeltaTime

	// Which handles the camera can see is answered once per frame and cached
	// for the rest of it, including the clicks that land before the next one
	// (editor_3d_visibility.go).
	editorVisNewFrame()

	// --- 3D gizmos (drawn on top of world, depth-tested) ---
	if Editor.Mode == Mode3D {
		projView := Projection.Mul4(Cam.Matrix)
		Editor3DRenderGizmos(projView)
		GizmoFlush(projView)
	}

	// --- 2D orthographic sector map ---
	if Editor.Mode == Mode2D {
		gl.Disable(gl.DEPTH_TEST)
		Editor2DRender()
		gl.Enable(gl.DEPTH_TEST)
	}

	// --- 2D surface editor ---
	if Editor.Mode == ModeSurface2D {
		gl.Disable(gl.DEPTH_TEST)
		Surface2DRender()
		gl.Enable(gl.DEPTH_TEST)
	}

	// --- ImGui UI ---
	ImGuiNewFrame(dt)
	EditorDrawUI()
	if Editor.Mode == Mode2D {
		Editor2DRenderBiomeOverlay()
		Editor2DRenderLabels()
	}
	if Editor.Mode == ModeSurface2D {
		Surface2DRenderLabels()
	}
	// The F3 overlay is drawn here rather than in main.go's non-editor branch
	// so it is available in every editor mode too — frame time and draw counts
	// matter most while you are building the thing that costs them.
	DebugRenderOverlay()
	KeybindHelpRender()
	ImGuiRender()
}

// EditorDeleteSelected removes the current selection: a biome sector, an edge,
// or the level's spawn point.
func EditorDeleteSelected() {
	if Editor.level == nil {
		return
	}

	// 2D mode, biome layer active: delete the selected biome sector
	if Editor.Mode == Mode2D && Editor.BiomeLayerActive {
		if ch := activeChunk(); ch != nil && Editor.ActiveBiomeSectorIdx >= 0 &&
			Editor.ActiveBiomeSectorIdx < len(ch.BiomeSectors) {
			EditorPushUndo()
			deleteBiomeSector(ch, Editor.ActiveBiomeSectorIdx)
		}
		return
	}

	// 2D mode: edge deletion (wall → delete sector, portal → merge sectors)
	if Editor.Mode == Mode2D && Editor.ActiveEdgeIdx >= 0 &&
		Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(Editor.level.Chunks) {
		ch := &Editor.level.Chunks[Editor.ActiveChunkIdx]
		EditorPushUndo()
		removeSelectedEdge(ch)
		return
	}

	// Spawn-point deletion
	if Editor.SpawnSelected && Editor.level.Spawn != nil {
		EditorPushUndo()
		Editor.level.Spawn.Delete()
		Editor.level.Spawn = nil
		editorClearSpawnSelection()
		return
	}

	// Entity deletion
	if activeEntity() != nil {
		deleteActiveEntity()
	}
}

// EditorLoadLevel loads a new level from the given YAML path, replacing the
// current level contents. Editor selection state is reset.
func EditorLoadLevel(path string) error {
	newLevel, err := LoadLevel(path)
	if err != nil {
		return err
	}

	// Entity meshes are the one thing in a level that is uploaded per *entity*
	// rather than per surface, so the outgoing level's have to be released
	// explicitly — overwriting the struct would drop the handles, not free them.
	DeleteEntities(Editor.level)

	// Replace the level contents in-place (Editor.level points to main's level)
	*Editor.level = newLevel

	// Upload lights to GPU
	UploadLights(Editor.level.Lights)

	// The player is engine-owned, so opening a level does not replace it — it
	// only moves it onto the new level's spawn point (or leaves it be when
	// there isn't one).
	ApplySpawnPoint(Editor.level)

	// Reset editor selection state
	Editor.ActiveChunkIdx = 0
	Editor.ActiveSectorIdx = -1
	Editor.ActiveEdgeIdx = -1
	Editor.ActiveSurface = ""
	Editor.SelectedFaces = nil
	Editor.PanelFocus = ""
	Editor.SelectedDirLightIdx = -1
	Editor.SpawnPlacementMode = false
	Editor.SpawnSelected = false
	Editor.ActiveEntityIdx = -1
	Editor.EntityPlacementMode = false
	Editor.DragVertexSI = -1
	Editor.DragVertexVI = -1
	Editor.ActiveVertexSI = -1
	Editor.ActiveVertexVI = -1
	Editor.SelectedVertices = nil
	Editor.GizmoKind = gizmoKindNone
	Editor.GizmoDragAxis = -1
	Editor.GizmoDragging = false
	Editor.Gizmo2DAxis = -1
	Editor.Gizmo2DDragging = false
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
	Editor.SavePath = path

	EditorClearUndoHistory()

	return nil
}

// ---------- camera helpers used by editor toggle ----------

func updateCamDirection() {
	yaw64 := float64(Cam.Yaw)
	pitch64 := float64(Cam.Pitch)
	Cam.Direction = mgl32.Vec3{
		float32(math.Cos(yaw64) * math.Cos(pitch64)),
		float32(math.Sin(pitch64)),
		float32(math.Sin(yaw64) * math.Cos(pitch64)),
	}
}

func updateCamMatrix() {
	Cam.Matrix = mgl32.LookAtV(Cam.Position, Cam.Position.Add(Cam.Direction), mgl32.Vec3{0, 1, 0})
	if Program == 0 {
		// No world shader, so nowhere to upload to. Only a headless test gets
		// here — a GL call with no context current segfaults in the driver
		// rather than returning an error, so the guard has to be on this side.
		return
	}
	gl.UniformMatrix4fv(Cam.Uniform, 1, false, &Cam.Matrix[0])
}
