package main

// editor.go — core level editor state machine.
//
// Toggle with the tilde (~) key. Two sub-modes:
//   Mode3D : fly-around entity/chunk/light placement
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
	Mode2D                          // 2D top-down sector editor
	ModeSurface2D                   // 2D surface editor for extrusions/terrain
)

// EditorState holds all editor runtime state.
type EditorState struct {
	Active bool
	Mode   EditorMode

	// --- 3D mode ---
	ActiveChunkIdx  int // chunk being displayed/edited
	ActiveSectorIdx int // sector selected in 3D/2D mode
	ActiveEdgeKind  string // "wall" or "portal"
	ActiveEdgeIdx   int    // index within sector.Walls or sector.Portals; -1 = none

	ActiveEntityIdx    int // index into level.Entities; -1 = none
	ActivePrimitiveIdx int // index into active chunk's Primitives; -1 = none

	// Saved gameplay camera state (restored on editor exit)
	SavedCamPos   mgl32.Vec3
	SavedCamYaw   float32
	SavedCamPitch float32

	// Entity definition catalog (loaded from resources/entities/*.yaml)
	EntityDefs     []EntityDef
	SelectedDefIdx int  // selected entry in the catalog panel
	PlacementMode  bool // true: next left-click places a new entity

	// Primitive placement state
	PrimPlacementMode bool   // true: next left-click places a primitive
	PrimPlacementType string // "rect_prism", "sphere", "cylinder", "plane"

	// Chunk creation dialog
	NewChunkID   string
	ShowNewChunk bool

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

	// Point connection state for ctrl+click workflow
	ConnectFromIdx int      // chunk point index to connect from; -1 = not connecting
	PendingEdges   [][2]int // edges not yet part of a sector (chunk point indices)

	// Navigation button state (reset each frame after applying)
	NavForward, NavBack, NavLeft, NavRight, NavUp, NavDown bool

	// Extrusion editing state (legacy 3D placement — kept for compatibility)
	ExtrusionMode      bool               // placing extrusion vertices
	PendingOuter       []ExtrusionPoint   // outer polygon being placed
	PendingInner       []ExtrusionPoint   // inner polygon being placed
	PlacingInner       bool               // false=placing outer, true=placing inner
	ActiveExtrusionIdx int                // selected extrusion on active wall; -1 = none

	// --- Surface 2D editor ---
	SurfaceKind       string           // "wall", "floor", "ceiling"
	SurfacePan        mgl32.Vec2       // pan center in surface coords (U,V or X,Z)
	SurfaceZoom       float32          // pixels per surface unit
	SurfaceEditLayer  string           // "both", "outer", "inner" — which extrusion points are draggable
	SurfacePlacing    string           // "extrusion", "terrain", "" (none)
	SurfacePendingPts []ExtrusionPoint // polygon vertices being placed
	SurfaceDragPt     int              // index of point being dragged; -1 = none
	SurfaceDragKind   string           // "outer", "inner", "terrain" — what's being dragged
	SurfaceDragExtIdx int              // extrusion index for outer/inner drag
	SurfaceLastMouse  [2]int32         // for pan
	SurfacePrevMode   EditorMode       // mode to return to on Escape
	ActiveTerrainPtIdx int             // selected terrain point index; -1 = none

	// Right-click drag rotation state (3D mode)
	MouseDragging3D    bool
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
}

// Editor is the global editor instance.
var Editor EditorState

// EditorInit must be called once after LoadLevel and after the OpenGL context
// is current. It initialises ImGui, gizmo shaders, and the entity catalog.
func EditorInit(level *Level, levelPath string) {
	Editor.level = level
	Editor.Active = false
	Editor.Mode = ModeGame
	Editor.ActiveChunkIdx = 0
	Editor.ActiveSectorIdx = -1
	Editor.ActiveEdgeIdx = -1
	Editor.ActiveEntityIdx = -1
	Editor.ActivePrimitiveIdx = -1
	Editor.PrimPlacementMode = false
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
	Editor.SurfaceZoom = 40.0
	Editor.SurfaceDragPt = -1
	Editor.ActiveTerrainPtIdx = -1
	Editor.SurfacePlacing = ""
	Editor.SurfaceEditLayer = "both"
	Editor.Zoom2D = 20.0   // 20 px per world unit
	Editor.PanelWidth = 360 // default side panel width
	Editor.StepSize = 0.25
	Editor.SnapToGrid = false
	Editor.EditFloorVerts = true
	Editor.LastTick = sdl.GetTicks()
	Editor.SavePath = levelPath

	ImGuiInit()
	GizmoInit()

	Editor.EntityDefs = ScanEntityDefs(executablePath + "/entities")
}

const panelGrabZone int32 = 6
const panelMinWidth float32 = 250

// panelLeftEdgeX returns the screen X coordinate of the panel's left edge.
func panelLeftEdgeX() int32 {
	return int32(float32(WinWidth) - Editor.PanelWidth)
}

// panelHandleResize checks SDL mouse events for drag-resizing the side panel.
// Returns true if the event was consumed by the resize handle.
func panelHandleResize(event sdl.Event) bool {
	edge := panelLeftEdgeX()

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
			newWidth := float32(WinWidth) - float32(t.X) - 5
			if newWidth < panelMinWidth {
				newWidth = panelMinWidth
			}
			maxWidth := float32(WinWidth) - 20
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
		// Restore gameplay camera
		Cam.Position = Editor.SavedCamPos
		Cam.Yaw = Editor.SavedCamYaw
		Cam.Pitch = Editor.SavedCamPitch
		updateCamDirection()
		updateCamMatrix()

		// Re-enable mouse lock for gameplay
		Cam.MouseLock = true
		sdl.ShowCursor(sdl.DISABLE)
		Window.SetGrab(true)
		Window.WarpMouseInWindow(WinHalfWidth, WinHalfHeight)

		GravityEnabled = true
		Editor.Active = false
		Editor.Mode = ModeGame
	} else {
		// Save gameplay camera
		Editor.SavedCamPos = Cam.Position
		Editor.SavedCamYaw = Cam.Yaw
		Editor.SavedCamPitch = Cam.Pitch

		// Release mouse for ImGui
		Cam.MouseLock = false
		sdl.ShowCursor(sdl.ENABLE)
		Window.SetGrab(false)

		// Enter 3D fly mode (gravity off)
		GravityEnabled = false
		IsOnGround = false
		JumpRequested = false

		Editor.Active = true
		Editor.Mode = Mode3D
		Editor.Pan2D = mgl32.Vec2{Cam.Position.X(), Cam.Position.Z()}
	}
}

// EditorHandleEvent processes one SDL event for the editor.
// Returns true if the editor consumed the event (game should ignore it).
func EditorHandleEvent(event sdl.Event) bool {
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
		if t.Type != sdl.KEYDOWN {
			break
		}
		switch t.Keysym.Scancode {
		case sdl.SCANCODE_TAB:
			// Toggle 2D / 3D sub-mode (surface mode is separate)
			if Editor.Mode == Mode3D {
				Editor.Mode = Mode2D
				Editor.Pan2D = mgl32.Vec2{Cam.Position.X(), Cam.Position.Z()}
			} else if Editor.Mode == Mode2D {
				Editor.Mode = Mode3D
			}
			// Tab in surface mode is handled by surface2D event handler
			// Always keep cursor free in editor — both modes need visible mouse
			// for selection, clicking edges, and placing vertices.
			Cam.MouseLock = false
			sdl.ShowCursor(sdl.ENABLE)
			Window.SetGrab(false)
			return true

		case sdl.SCANCODE_DELETE:
			if !imgui.CurrentIO().WantCaptureKeyboard() {
				EditorDeleteSelected()
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
					Editor.MouseDragging3D = true
					Editor.LastDragX = t.X
					Editor.LastDragY = t.Y
				} else if Editor.ExtrusionMode && editorExtrusionHandleClick(t) {
					// Extrusion placement consumed the click
				} else {
					Editor3DHandleClick(t)
				}
			} else if Editor.Mode == Mode2D {
				Editor2DHandleClick(t)
			}
		} else if t.Type == sdl.MOUSEBUTTONUP {
			if Editor.Mode == ModeSurface2D {
				Surface2DHandleRelease(t)
			} else if Editor.Mode == Mode3D && t.Button == sdl.ButtonRight {
				Editor.MouseDragging3D = false
			} else if Editor.Mode == Mode2D {
				Editor2DHandleRelease(t)
			}
		}

	case sdl.MouseMotionEvent:
		if Editor.Mode == ModeSurface2D {
			Surface2DHandleMouseMove(t)
		} else if Editor.Mode == Mode3D && Editor.MouseDragging3D {
			dx := t.X - Editor.LastDragX
			dy := t.Y - Editor.LastDragY
			Cam.Yaw += MouseRotationSpeed * float32(dx)
			Cam.Pitch -= MouseRotationSpeed * float32(dy)
			if Cam.Pitch > maxPitch {
				Cam.Pitch = maxPitch
			}
			if Cam.Pitch < -maxPitch {
				Cam.Pitch = -maxPitch
			}
			updateCamDirection()
			updateCamMatrix()
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
			Editor.Zoom2D *= float32(math.Pow(1.1, float64(t.Y)))
			if Editor.Zoom2D < 2 {
				Editor.Zoom2D = 2
			}
			if Editor.Zoom2D > 200 {
				Editor.Zoom2D = 200
			}
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

	if Editor.Mode == Mode3D {
		// Free-fly using existing HandleInput (gravity is already off)
		preMovePos := Cam.Position
		HandleInput(keyStates)
		_ = preMovePos

		// Apply navigation button movement
		dtScale := DeltaTime * 60.0
		navSpeed := MovementSpeed * dtScale
		if Editor.NavForward {
			MoveCamera(navSpeed, 0, 0)
		}
		if Editor.NavBack {
			MoveCamera(-navSpeed, 0, 0)
		}
		if Editor.NavLeft {
			MoveCamera(0, 0, navSpeed)
		}
		if Editor.NavRight {
			MoveCamera(0, 0, -navSpeed)
		}
		if Editor.NavUp {
			MoveCamera(0, navSpeed, 0)
		}
		if Editor.NavDown {
			MoveCamera(0, -navSpeed, 0)
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
	dt := DeltaTime

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
		Editor2DRenderLabels()
	}
	if Editor.Mode == ModeSurface2D {
		Surface2DRenderLabels()
	}
	ImGuiRender()
}

// EditorDeleteSelected removes the currently selected entity or edge.
func EditorDeleteSelected() {
	if Editor.level == nil {
		return
	}

	// 2D mode: edge deletion (wall → delete sector, portal → merge sectors)
	if Editor.Mode == Mode2D && Editor.ActiveEdgeIdx >= 0 &&
		Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(Editor.level.Chunks) {
		ch := &Editor.level.Chunks[Editor.ActiveChunkIdx]
		removeSelectedEdge(ch)
		return
	}

	// Primitive deletion
	if Editor.ActivePrimitiveIdx >= 0 && Editor.ActiveChunkIdx >= 0 &&
		Editor.ActiveChunkIdx < len(Editor.level.Chunks) {
		ch := &Editor.level.Chunks[Editor.ActiveChunkIdx]
		idx := Editor.ActivePrimitiveIdx
		if idx < len(ch.Primitives) {
			ch.Primitives[idx].Geometry.Delete()
			ch.Primitives = append(ch.Primitives[:idx], ch.Primitives[idx+1:]...)
			// Rebuild sector PrimitiveIndices
			rebuildPrimitiveIndices(ch)
			Editor.ActivePrimitiveIdx = -1
		}
		return
	}

	// Entity deletion
	if Editor.ActiveEntityIdx < 0 {
		return
	}
	idx := Editor.ActiveEntityIdx
	lvl := Editor.level
	if idx >= len(lvl.Entities) {
		return
	}
	lvl.Entities = append(lvl.Entities[:idx], lvl.Entities[idx+1:]...)
	Editor.ActiveEntityIdx = -1
}

// rebuildPrimitiveIndices recomputes Sector.PrimitiveIndices for all sectors in a chunk.
func rebuildPrimitiveIndices(ch *Chunk) {
	for si := range ch.Sectors {
		ch.Sectors[si].PrimitiveIndices = nil
	}
	for pi := range ch.Primitives {
		p := &ch.Primitives[pi]
		xz := mgl32.Vec2{p.Position.X(), p.Position.Z()}
		for si := range ch.Sectors {
			s := &ch.Sectors[si]
			pts := SectorPoints(ch, s)
			if pointInPolygon(xz, pts) {
				p.SectorID = s.ID
				s.PrimitiveIndices = append(s.PrimitiveIndices, pi)
				break
			}
		}
	}
}

// EditorLoadLevel loads a new level from the given YAML path, replacing the
// current level contents. Editor selection state is reset.
func EditorLoadLevel(path string) error {
	newLevel, err := LoadLevel(path)
	if err != nil {
		return err
	}

	// Replace the level contents in-place (Editor.level points to main's level)
	*Editor.level = newLevel

	// Upload lights to GPU
	UploadLights(Editor.level.Lights)

	// Find player entity
	PlayerEntity = nil
	for i := range Editor.level.Entities {
		if Editor.level.Entities[i].ID == "player" {
			PlayerEntity = &Editor.level.Entities[i]
			break
		}
	}

	// Reset editor selection state
	Editor.ActiveChunkIdx = 0
	Editor.ActiveSectorIdx = -1
	Editor.ActiveEdgeIdx = -1
	Editor.ActiveEntityIdx = -1
	Editor.ActivePrimitiveIdx = -1
	Editor.PrimPlacementMode = false
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
	Editor.SavePath = path

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
	gl.UniformMatrix4fv(Cam.Uniform, 1, false, &Cam.Matrix[0])
}
