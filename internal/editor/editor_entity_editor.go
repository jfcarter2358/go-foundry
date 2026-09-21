package editor

// editor_entity_editor.go — the entity editor, in its own OS window.
//
// What it edits is a *type*, not a placement. The level editor's entity panel
// has always edited an instance: change a barrel there and you have changed one
// barrel, in one level, and the change is written into that level file as an
// override. This window edits resources/entities/<name>.yaml, so the change
// reaches every barrel in the game and every level that has yet to place one.
//
// An entity type is assembled from five kinds of thing, and the point of a
// window is that all of them are visible at once, against the same origin, at
// the same scale:
//
//	models   .fmdl/.obj attachments, each with its own transform and its own
//	         named animations — what the thing looks like
//	emitters the effects it carries (world/entity_emitter.go) — a thruster
//	         plume, a torch flame — running here, against the same origin, so
//	         one is aimed by eye rather than by placing the type in a level
//	brushes  the type's own solid (world/entity_brush.go) — floors to stand on,
//	         walls to bump into, a door a script slides open. Shaped here with
//	         the level editor's own tools, and what makes a ship with a
//	         walkable hold a type rather than a level's hand-wiring
//	script    the .flang file driving it, with its parameters bound here as
//	         the type's defaults
//	data      tags, attributes, bounds, collision flags, and a vehicle block
//
// Like the model and particle editors and unlike the 2D sector pop-out, this
// window owns a document rather than being a second view onto Editor state: it
// holds an *world.EntityDef, its own camera, and its own GL geometry. The main
// window carries on rendering the level untouched while it is open, which is
// the point — a type is authored beside the level it is going into, and Save
// pushes it straight into every instance already placed there.
//
// The three things a secondary window supplies for itself, and where:
//
//   - a camera and projection: entityCam below, because the global Cam is being
//     driven by the main window every frame;
//   - a picking ray: entityCamera.ray, since screenRay (editor_3d.go) reads the
//     main window's viewport globals;
//   - GL object ownership: see the header of editor_entity_editor_render.go.

import (
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/platform"
	"foundry/internal/uiwin"
	"foundry/internal/world"
	"log"
	"math"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	entityWindowW = int32(1360)
	entityWindowH = int32(840)

	// The shared two-row top bar (editor_menubar.go): menus above, the 3D
	// view's tools and the status line below.
	entityToolbarH = editorTopBarH

	// entityPanelWidth is the right-hand properties column, and
	// entityOutlineWidth the left-hand list of what the type is made of. The
	// properties side is wider because it holds labelled drag fields; the
	// outline holds names and a delete button.
	entityPanelWidth   = float32(330)
	entityOutlineWidth = float32(210)
)

// entityEditorWindow is the pop-out; nil when the editor is closed.
var entityEditorWindow *uiwin.UIWindow

// entityDef is the type being edited. It outlives the window, so popping the
// editor in and out returns to the same definition rather than a fresh one.
var entityDef *world.EntityDef

// entityDefSaved is the name the definition was last read from or written to,
// or "" for one that has never been saved. Kept apart from entityDef.Name,
// which is the *id* an author may retype at any moment: Save writes to the name
// in the field, so renaming and saving is how a type is forked.
var entityDefSaved string

// entityDefSource is the placed entity a document was seeded from by Make Type
// (OpenEntityEditorFrom), and the level it stands in — nil for a document that
// was opened or started fresh. The first save turns that placement into an
// instance of the type it just wrote, so the level is showing what was saved
// rather than a typeless copy of it. See entityAdoptSource.
var entityDefSource struct {
	level *world.Level
	id    string
}

// entityPreview is the definition stamped out as a live entity — uploaded
// meshes, playing animations, materialised chunk geometry. It is what the
// viewport actually draws, rebuilt from the document whenever the document
// changes shape. See editor_entity_editor_render.go.
var entityPreview *world.Entity

// entitySel is what the outline has selected, and what the properties column
// shows under the type-wide groups.
var entitySel entityEditorSelection

// entityEditorSelection names one part of the definition.
type entityEditorSelection struct {
	Kind entitySelKind
	Idx  int
	// Sub is the point inside a path (entitySelWaypoints), or -1 for the path
	// selected whole. Unread by every other kind.
	Sub int
}

type entitySelKind int

const (
	entitySelNone entitySelKind = iota
	entitySelModel
	// entitySelBrush is one of the type's own solids. Idx indexes the scratch
	// chunk's brush list, which is the same index the shared object-mode
	// selection uses — see editor_entity_editor_brushes.go, and entitySelectBrush
	// for why the two are always set together.
	entitySelBrush
	// entitySelEmitter is one of the type's particle effects. Idx indexes
	// entityDef.Emitters, and the live preview simulation beside it — see
	// editor_entity_editor_emitters.go.
	entitySelEmitter
	entitySelSeat
	// entitySelVolume is one of the type's carried regions. Idx indexes
	// entityDef.Volumes — see editor_entity_editor_volumes.go.
	entitySelVolume
	// entitySelWaypoints is one of the type's paths, with Sub the point inside
	// it. Idx indexes entityDef.WaypointAnimations — see
	// editor_entity_editor_waypoints.go.
	entitySelWaypoints
)

// entityStatus is the line under the toolbar.
var entityStatus string

// View toggles.
var (
	entityShowGrid    = true
	entityShowBounds  = true
	entityShowModels  = true
	entityShowBrushes = true
	entityShowSeats   = true
	entityShowEffects = true
	entityShowVolumes = true
	entityPlayAnims   = true
	entityBgColor     = [3]float32{0.13, 0.14, 0.16}
	entityWireOverlay bool
)

// entityCam frames the type. Pitched down a little so a thing standing on the
// ground plane reads as standing on it.
var entityCam = entityCamera{Dist: 6, Yaw: mgl32.DegToRad(35), Pitch: mgl32.DegToRad(-18)}

var (
	entityOrbiting bool
	entityPanning  bool
)

// ---------- lifecycle ----------

// EntityEditorOpen reports whether the window is up.
func EntityEditorOpen() bool { return entityEditorWindow != nil }

// OpenEntityEditorWindow opens the editor, or raises it if it is already up.
//
// It always has something to edit: with nothing loaded it starts on a working
// blank type, the way the model editor starts on a cube rather than on a file
// dialog. A type with no models and no chunks is still a legitimate thing to
// save — a trigger, a script anchor, a spawn marker — so the blank state is a
// destination and not just a placeholder.
func OpenEntityEditorWindow() {
	if entityEditorWindow != nil {
		entityEditorWindow.Win.Raise()
		return
	}
	entityEnsureDoc()
	w, err := uiwin.NewUIWindow("Entity Editor", entityWindowW, entityWindowH,
		drawEntityEditorWindow, onEntityEditorClose)
	if err != nil {
		log.Printf("open entity editor window: %v", err)
		return
	}
	w.ClearColor = [4]float32{entityBgColor[0], entityBgColor[1], entityBgColor[2], 1}
	w.HandleEvent = entityEditorHandleEvent
	// Assigned last: until this is set EntityEditorOpen has to keep saying no,
	// or a Draw triggered during construction would find a half-built window.
	entityEditorWindow = w
	entityPreviewInvalidate()
}

// OpenEntityEditorOn opens the editor on a named definition — the level
// editor's "Edit Type" beside a placed entity, and the catalog's edit row.
//
// Deferred through RequestWindowOpen because the caller is inside another
// window's Draw: creating a window there inserts into the map being ranged over
// and leaves the wrong GL context current.
func OpenEntityEditorOn(name string) {
	if name == "" {
		OpenEntityEditorNew()
		return
	}
	uiwin.RequestWindowOpen(func() {
		if err := entityLoadDef(name); err != nil {
			log.Printf("entity editor: %v", err)
			editorSetStatus("Could not open entity type %s: %v", name, err)
			return
		}
		OpenEntityEditorWindow()
	})
}

// OpenEntityEditorNew opens the editor on a fresh type — the catalog's
// "New Type..." row.
//
// It starts a new document rather than raising whatever was already open,
// because "there isn't a type for this yet" is the whole reason that row is
// there. Deferred like the rest: the caller is inside the level editor's Draw.
func OpenEntityEditorNew() {
	uiwin.RequestWindowOpen(func() {
		entityNewDoc()
		OpenEntityEditorWindow()
	})
}

// CloseEntityEditorWindow asks for the window to go away at the end of the
// frame. Safe to call from inside its own Draw.
func CloseEntityEditorWindow() {
	if entityEditorWindow != nil {
		entityEditorWindow.CloseRequested = true
	}
}

// onEntityEditorClose runs with this window's GL context current, which is what
// makes releasing the preview's buffers here correct — they were uploaded in
// this context and are not reachable from any other.
//
// The *document* stays in memory: closing the window is not discarding the
// work.
func onEntityEditorClose() {
	entityGizmoMouseUp()
	entityPreviewRelease()
	entityPreviewVolumes.Delete()
	entityEditorWindow = nil
}

// entityEditorFocused reports whether this window owns keyboard input.
// sdl.GetKeyboardState is process-global, so without this a keypress meant for
// the entity editor would also reach the main window's fly camera.
func entityEditorFocused() bool {
	return entityEditorWindow != nil &&
		entityEditorWindow.Win.GetFlags()&sdl.WINDOW_INPUT_FOCUS != 0
}

// entityEnsureDoc guarantees there is something to edit.
func entityEnsureDoc() {
	if entityDef == nil {
		entityNewDoc()
	}
}

// entityNewDefaultDef is the type a fresh document starts as: visible, solid
// enough to stand on the floor, and a metre-ish box so the bounds cage is
// something to see rather than a point at the origin.
func entityNewDefaultDef(name string) *world.EntityDef {
	return &world.EntityDef{
		Name:         name,
		BoundingKind: world.BoundingAABB,
		BoundHalfX:   0.5,
		BoundHalfY:   0.5,
		BoundHalfZ:   0.5,
		BoundOffset:  mgl32.Vec3{0, 0.5, 0},
		Visible:      true,
		Collision:    world.DefaultEntityCollision(),
	}
}

// ---------- the camera ----------

// entityCamera frames the type being built. Orbit and fly, for the same reason
// the model editor's has both: orbit to look at a barrel from every side, fly
// to get inside a ship whose hold you are laying out.
type entityCamera struct {
	Target mgl32.Vec3 // orbit pivot
	Pos    mgl32.Vec3 // eye, authoritative in fly mode
	Dist   float32
	Yaw    float32 // radians
	Pitch  float32 // radians
	Fly    bool
}

func (c entityCamera) forward() mgl32.Vec3 {
	return gfx.DirectionFromAngles(c.Yaw, c.Pitch)
}

func (c entityCamera) position() mgl32.Vec3 {
	if c.Fly {
		return c.Pos
	}
	return c.Target.Sub(c.forward().Mul(c.Dist))
}

func (c entityCamera) view() mgl32.Mat4 {
	eye := c.position()
	return mgl32.LookAtV(eye, eye.Add(c.forward()), gfx.WorldUp)
}

func (c entityCamera) proj(w, h int32) mgl32.Mat4 {
	aspect := float32(1)
	if h > 0 {
		aspect = float32(w) / float32(h)
	}
	return mgl32.Perspective(mgl32.DegToRad(60), aspect, 0.02, 2000)
}

func (c entityCamera) right() mgl32.Vec3 {
	r := c.forward().Cross(gfx.WorldUp)
	if r.Len() < 1e-5 {
		return mgl32.Vec3{1, 0, 0}
	}
	return r.Normalize()
}

func (c entityCamera) up() mgl32.Vec3 {
	return c.right().Cross(c.forward()).Normalize()
}

// ray is screenRay (editor_3d.go) against this window's own camera and
// projection instead of the main window's globals.
func (c entityCamera) ray(sx, sy, w, h int32) (origin, dir mgl32.Vec3) {
	if w <= 0 || h <= 0 {
		return c.position(), mgl32.Vec3{0, 0, -1}
	}
	ndcX := (float32(sx)/float32(w))*2 - 1
	ndcY := 1 - (float32(sy)/float32(h))*2

	eye := c.proj(w, h).Inv().Mul4x1(mgl32.Vec4{ndcX, ndcY, -1, 1})
	eye[2], eye[3] = -1, 0

	wv := c.view().Inv().Mul4x1(eye)
	return c.position(), mgl32.Vec3{wv.X(), wv.Y(), wv.Z()}.Normalize()
}

// setFly switches modes without moving the eye or changing where it looks.
func (c *entityCamera) setFly(fly bool) {
	if c.Fly == fly {
		return
	}
	if fly {
		c.Pos = c.position()
	} else {
		c.Target = c.Pos.Add(c.forward().Mul(c.Dist))
	}
	c.Fly = fly
}

func (c *entityCamera) orbit(dx, dy int32) {
	c.Yaw += float32(dx) * 0.005
	c.Pitch -= float32(dy) * 0.005
	const limit = float32(math.Pi/2) - 0.01
	if c.Pitch > limit {
		c.Pitch = limit
	}
	if c.Pitch < -limit {
		c.Pitch = -limit
	}
}

func (c *entityCamera) pan(dx, dy int32) {
	scale := c.Dist * 0.0015
	c.Target = c.Target.
		Sub(c.right().Mul(float32(dx) * scale)).
		Add(c.up().Mul(float32(dy) * scale))
	if c.Fly {
		c.Pos = c.Pos.
			Sub(c.right().Mul(float32(dx) * scale)).
			Add(c.up().Mul(float32(dy) * scale))
	}
}

func (c *entityCamera) zoom(delta int32) {
	if c.Fly {
		c.Pos = c.Pos.Add(c.forward().Mul(float32(delta) * c.Dist * 0.1))
		return
	}
	c.Dist *= float32(math.Pow(0.9, float64(delta)))
	if c.Dist < 0.2 {
		c.Dist = 0.2
	}
	if c.Dist > 1000 {
		c.Dist = 1000
	}
}

func (c *entityCamera) look(dx, dy int32) {
	c.Yaw += platform.MouseRotationSpeed * float32(dx)
	c.Pitch -= platform.MouseRotationSpeed * float32(dy)
	if c.Pitch > gfx.MaxPitch {
		c.Pitch = gfx.MaxPitch
	}
	if c.Pitch < -gfx.MaxPitch {
		c.Pitch = -gfx.MaxPitch
	}
}

// move travels the camera, with forward flattened to the horizontal plane
// exactly as MoveCamera does so looking down does not drive the camera into the
// floor.
//
// Orbiting slides the *pivot* by the same vector rather than doing nothing,
// which is what editorCamMove (editor_3d_camera.go) settled on and for the same
// reason: a hold big enough to walk about inside is not a thing to be stuck
// circling, and keys that die in half the modes read as broken rather than as
// modal.
func (c *entityCamera) move(f, u, r float32) {
	d := c.forward()
	forward := mgl32.Vec3{d.X(), 0, d.Z()}
	if forward.Len() > 1e-6 {
		forward = forward.Normalize()
	}
	delta := forward.Mul(f).Add(c.right().Mul(r)).Add(gfx.WorldUp.Mul(u))
	if c.Fly {
		c.Pos = c.Pos.Add(delta)
		return
	}
	c.Target = c.Target.Add(delta)
}

// frame points the camera at everything the type contains, so an imported hull
// is on screen whatever scale it was authored at.
func (c *entityCamera) frame() {
	c.Fly = false
	min, max, ok := entityPreviewBounds()
	if !ok {
		c.Target = mgl32.Vec3{0, 0.5, 0}
		c.Dist = 6
		return
	}
	c.Target = min.Add(max).Mul(0.5)
	radius := max.Sub(min).Len() / 2
	if radius < 0.25 {
		radius = 0.25
	}
	// 60 degree vertical FOV, with a margin so the type does not touch the
	// window edges.
	c.Dist = radius / float32(math.Tan(float64(mgl32.DegToRad(30)))) * 1.5
}

// ---------- input ----------

func entityEditorHandleEvent(event sdl.Event) {
	// Every path below can reach the shared brush tools, and they resolve
	// geometry through ws(). See editor_entity_editor_input.go.
	defer entityUseWorkspace()()

	io := imgui.CurrentIO()
	switch t := event.(type) {
	case sdl.MouseButtonEvent:
		if io.WantCaptureMouse() && t.Type == sdl.MOUSEBUTTONDOWN {
			return
		}
		down := t.Type == sdl.MOUSEBUTTONDOWN
		switch t.Button {
		case sdl.ButtonLeft:
			// The gizmo owns the left button first — a click that lands on a
			// handle is a drag, not a selection. A click that misses every
			// handle falls through to the geometry, so picking a brush, a face,
			// an edge or a corner works here the way it works in the 3D view.
			if down {
				mod := sdl.GetModState()
				ctrl, shift := mod&sdl.KMOD_CTRL != 0, mod&sdl.KMOD_SHIFT != 0
				// A path's arrows before the geometry: they are drawn over the
				// hull, and win a click the way they do in the 3D view.
				if !entityGizmoMouseDown(t.X, t.Y) && !entityWaypointClick(t.X, t.Y, ctrl, shift) {
					entityGeometryClick(t.X, t.Y, ctrl)
				}
			} else {
				entityGizmoMouseUp()
			}
		case sdl.ButtonMiddle:
			// Middle drag orbits, shift+middle pans — the model and particle
			// editors' convention, so muscle memory carries between windows.
			if down && sdl.GetModState()&sdl.KMOD_SHIFT != 0 {
				entityPanning = true
			} else {
				entityOrbiting = down
			}
			if !down {
				entityOrbiting, entityPanning = false, false
			}
		case sdl.ButtonRight:
			// Right-drag looks or orbits, the gesture both other viewports
			// use — and a right-click that does not travel opens the context
			// menu instead, which is what the gesture below is watching for.
			entityOrbiting = down
			if down {
				entityContextPressed(t.X, t.Y)
			} else {
				entityContextReleased(t.X, t.Y)
			}
		}

	case sdl.MouseMotionEvent:
		entityMouseX, entityMouseY = t.X, t.Y
		entityContextMoved(t.X, t.Y)
		switch {
		case entityGizmoDragging():
			// A drag owns the mouse until release, ahead of the camera: the
			// two cannot both act on the same motion.
			entityGizmoMouseMove(t.X, t.Y)
		case entityPanning:
			entityCam.pan(t.XRel, t.YRel)
		case entityOrbiting && entityCam.Fly:
			entityCam.look(t.XRel, t.YRel)
		case entityOrbiting:
			entityCam.orbit(t.XRel, t.YRel)
		}

	case sdl.MouseWheelEvent:
		if !io.WantCaptureMouse() {
			entityCam.zoom(t.Y)
		}

	case sdl.KeyboardEvent:
		if t.Type != sdl.KEYDOWN || io.WantCaptureKeyboard() {
			return
		}
		mod := sdl.GetModState()
		// The geometry shortcuts go first, so E, I, B, Tab and 1/2/3 mean here
		// what they mean in the 3D view. Each reports whether it acted, and an
		// unconsumed press falls through to this window's own bindings — which
		// is what leaves B as the bounds toggle when no face is selected.
		if entityGeometryHandleKey(t.Keysym.Scancode,
			mod&sdl.KMOD_CTRL != 0, mod&sdl.KMOD_SHIFT != 0) {
			return
		}
		entityEditorHandleKey(t)
	}
}

// entityShortcut is what a keypress means here. Returned as a value rather than
// acted on inline so the mapping can be unit-tested with no GL context — the
// arrangement particleShortcutFor uses.
type entityShortcut int

const (
	entityShortcutNone entityShortcut = iota
	entityShortcutNew
	entityShortcutOpen
	entityShortcutSave
	entityShortcutSaveAs
	entityShortcutUndo
	entityShortcutRedo
	entityShortcutFrameCamera
	entityShortcutToggleFly
	entityShortcutToggleGrid
	entityShortcutToggleBounds
	entityShortcutToolMove
	entityShortcutToolRotate
	entityShortcutToolScale
	entityShortcutToggleUVMode
	entityShortcutTogglePlay
	entityShortcutDeleteSelection
)

// entityShortcutFor maps a keypress to an action.
func entityShortcutFor(sc sdl.Scancode, ctrl, shift bool) entityShortcut {
	if ctrl {
		switch sc {
		case sdl.SCANCODE_N:
			return entityShortcutNew
		case sdl.SCANCODE_O:
			return entityShortcutOpen
		case sdl.SCANCODE_S:
			if shift {
				return entityShortcutSaveAs
			}
			return entityShortcutSave
		case sdl.SCANCODE_Z:
			if shift {
				return entityShortcutRedo
			}
			return entityShortcutUndo
		case sdl.SCANCODE_Y:
			return entityShortcutRedo
		}
		return entityShortcutNone
	}
	switch sc {
	case sdl.SCANCODE_F:
		return entityShortcutFrameCamera
	case sdl.SCANCODE_V:
		return entityShortcutToggleFly
	case sdl.SCANCODE_G:
		// G is the move tool everywhere else in the editor, and this window
		// grew a gizmo after it had already given the key to the grid. The tool
		// wins and the grid moves to Shift+G: muscle memory carried in from the
		// other two viewports is worth more than the toggle's shorter key.
		if shift {
			return entityShortcutToggleGrid
		}
		return entityShortcutToolMove
	case sdl.SCANCODE_R:
		return entityShortcutToolRotate
	case sdl.SCANCODE_C:
		return entityShortcutToolScale
	case sdl.SCANCODE_U:
		return entityShortcutToggleUVMode
	case sdl.SCANCODE_B:
		return entityShortcutToggleBounds
	case sdl.SCANCODE_P:
		// P rather than the Space the model and particle editors play on:
		// Space rises here (entityEditorMoveKeys), and a key that flew the
		// camera and started the animations with one press would be the worse
		// half of both bindings.
		return entityShortcutTogglePlay
	case sdl.SCANCODE_DELETE, sdl.SCANCODE_X:
		return entityShortcutDeleteSelection
	}
	return entityShortcutNone
}

func entityEditorHandleKey(t sdl.KeyboardEvent) {
	mod := sdl.GetModState()
	switch entityShortcutFor(t.Keysym.Scancode, mod&sdl.KMOD_CTRL != 0, mod&sdl.KMOD_SHIFT != 0) {
	case entityShortcutNew:
		entityNewDoc()
	case entityShortcutOpen:
		entityShowOpenDialog = true
		entityDefFileList = nil
	case entityShortcutSave:
		entitySaveCurrent()
	case entityShortcutSaveAs:
		entitySaveAsName = entityDefName()
		entityShowSaveAsDialog = true
	case entityShortcutUndo:
		EntityDefUndo()
	case entityShortcutRedo:
		EntityDefRedo()
	case entityShortcutFrameCamera:
		entityCam.frame()
	case entityShortcutToggleFly:
		entityCam.setFly(!entityCam.Fly)
	case entityShortcutToggleGrid:
		entityShowGrid = !entityShowGrid
	case entityShortcutToolMove:
		entitySetGizmoTool(editorToolMove)
	case entityShortcutToolRotate:
		entitySetGizmoTool(editorToolRotate)
	case entityShortcutToolScale:
		entitySetGizmoTool(editorToolScale)
	case entityShortcutToggleUVMode:
		entityToggleUVMode()
	case entityShortcutToggleBounds:
		entityShowBounds = !entityShowBounds
	case entityShortcutTogglePlay:
		entityPlayAnims = !entityPlayAnims
	case entityShortcutDeleteSelection:
		entityDeleteSelection()
	}
}

// entityEditorMoveKeys drives the camera from held keys, once a frame.
// Read here rather than from an event because holding a key is a state, not an
// event, and SDL's key repeat is far too coarse to fly with.
//
// The bindings are the ones every other camera in the program answers to:
// WASD, Space up, Shift down, Ctrl to go faster. This window spelled up and
// down Q and E, which was the one set nobody could guess from the other two
// viewports — and E had since become extrude, so holding it to rise reshaped
// the brush under the cursor instead. Ctrl is the speed modifier for the reason
// the model editor gives: Shift is spoken for by the descent.
func entityEditorMoveKeys(dt float32) {
	if !entityEditorFocused() {
		return
	}
	// A name being typed in the panel is not a camera command. Checked here
	// rather than in the event path because these keys are read as held state,
	// which no keyboard-capture gate has already filtered.
	if imgui.CurrentIO().WantCaptureKeyboard() {
		return
	}
	keys := sdl.GetKeyboardState()
	mods := sdl.GetModState()
	speed := float32(6) * dt
	if mods&sdl.KMOD_CTRL != 0 {
		speed *= 3
	}
	var f, u, r float32
	if keys[sdl.SCANCODE_W] != 0 {
		f += speed
	}
	if keys[sdl.SCANCODE_S] != 0 {
		f -= speed
	}
	if keys[sdl.SCANCODE_D] != 0 {
		r += speed
	}
	if keys[sdl.SCANCODE_A] != 0 {
		r -= speed
	}
	if keys[sdl.SCANCODE_SPACE] != 0 {
		u += speed
	}
	// Not while panning: Shift+MMB is the pan gesture, and holding it to drag
	// the view is not also a request to sink.
	if !entityPanning && mods&sdl.KMOD_SHIFT != 0 {
		u -= speed
	}
	if f != 0 || u != 0 || r != 0 {
		entityCam.move(f, u, r)
	}
}

// ---------- the frame ----------

// drawEntityEditorWindow is this window's whole frame, in the order the model
// and particle editors' are: advance, then the 3D view, then ImGui over it.
func drawEntityEditorWindow() {
	w := entityEditorWindow
	if w == nil {
		return
	}
	// Installed for the whole frame: the panels, the overlays and the gizmo all
	// reach the shared brush tools, and every one of them resolves geometry
	// through ws(). See editor_workspace.go.
	defer entityUseWorkspace()()
	entityEnsureDoc()
	entityEditorMoveKeys(platform.DeltaTime)

	// Rebuilt here rather than at the moment of the edit: a panel widget runs
	// many times during a drag, and uploading a mesh per frame of one is the
	// difference between a responsive slider and a stalled one. The flag says
	// "the document has changed shape"; this is the one place that acts on it.
	entityPreviewSync()
	if entityPlayAnims {
		entityPreviewAnimate(platform.DeltaTime)
	}

	// The effects, after the pose: a bone-mounted emitter reads this frame's
	// palette, so stepping it before UpdateAnimations would hang a flame off
	// last frame's hand.
	entityEmitterPreviewSync()
	if entityPlayAnims {
		entityEmitterPreviewTick(platform.DeltaTime)
	}
	// A path preview, after the re-seed above has brought the brushes back at
	// rest if the document was replaced, and before anything draws them. Not
	// under the Play toggle: a path preview has its own transport.
	entityWaypointPreviewTick(platform.DeltaTime)

	view := entityCam.view()
	proj := entityCam.proj(w.W, w.H)
	projView := proj.Mul4(view)

	gl.Enable(gl.DEPTH_TEST)
	entityDrawPreview(view, proj, entityCam.position())
	if entityShowGrid {
		drawEntityGrid()
	}
	if entityShowBounds {
		drawEntityBoundsCage()
	}
	if entityShowSeats {
		drawEntitySeatMarkers()
	}
	if entityShowEffects {
		drawEntityEmitterMarkers()
	}
	if entityShowVolumes {
		drawEntityVolumeCages()
	}
	drawEntityGeometryOutline()
	gizmo.GizmoFlush(projView)

	// The particles after the gizmo flush and while depth testing is still on:
	// they are transparent geometry and have to sort against the hull they come
	// out of, and drawing them before the lines would have the lines punch
	// holes in a plume.
	if entityShowEffects {
		entityDrawEmitterParticles(projView)
	}
	gl.Disable(gl.DEPTH_TEST)

	// What is selected, and the handles it can be grabbed by.
	drawEntityGeometryOverlays(projView)
	// The paths likewise: an arrow inside the hull it moves is still one to
	// see and click, and its first point is usually inside the door.
	if entityShowWaypoints {
		drawEntityWaypointMarkers()
	}

	// The gizmo after that flush, with depth testing off: a handle inside the
	// hull is still a handle you need to be able to see and grab, and picking
	// has no depth test to agree with.
	drawEntityGizmo(entityMouseX, entityMouseY)
	gizmo.GizmoFlush(projView)

	// ImGui last, painting over the 3D view.
	entityEditorToolbar()
	entityEditorOutline()
	entityEditorPanel()
	entityWaypointTimelineWindow()
	entityEditorDialogs()
	// After the panels, so the menu's rows are built from the state they are
	// showing rather than from last frame's.
	entityContextMenu()
	entityEndEdits()

	// The document's copy of the brushes, taken once at the end of the frame.
	// Not sooner: a tool pushes its undo entry before it edits, and the
	// document has to still be holding the pre-edit state at that moment. See
	// editor_entity_editor_brushes.go.
	entityCommitBrushes()
}
