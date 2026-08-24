package main

// editor_model_window.go — the model editor, popped out into its own OS window.
//
// Unlike the 2D sector editor (editor_2d_window.go), this window is not a second
// view onto Editor state: it owns a document of its own (ModelDoc), its own
// camera, and its own GL geometry. That makes it closer to the texture editor —
// there is no in-main-window "model mode", so none of the Editor.Mode bracketing
// the 2D pop-out needs applies here, and the main window carries on rendering
// the level untouched while this window is open.
//
// The three things a secondary window has to supply for itself, and where:
//
//   - a camera and projection: modelCam below, because the global Cam is being
//     driven by the main window every frame;
//   - a picking ray: modelCamera.ray, since screenRay (editor_3d.go) reads the
//     main window's WinWidth/WinHeight/Projection globals;
//   - GL object ownership: see the header of editor_model_render.go.

import (
	"fmt"
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/platform"
	"log"
	"math"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	modelWindowW = int32(1280)
	modelWindowH = int32(800)

	// Two rows: file actions above, selection and camera below. One row cannot
	// hold both at this window's default width — the right-hand "Pop In" is
	// positioned absolutely and would sit on top of whatever overflowed.
	modelToolbarH = float32(54)
)

// modelEditorWindow is the pop-out; nil when the model editor is closed.
var modelEditorWindow *UIWindow

// modelDoc is the mesh being edited. It outlives the window, so popping the
// editor in and out again returns to the same model rather than a fresh cube.
var modelDoc *ModelDoc

// modelCam is the model editor's view. Pitch starts slightly downward so a
// fresh cube reads as a solid rather than edge-on.
var modelCam = modelCamera{Dist: 4, Yaw: mgl32.DegToRad(35), Pitch: mgl32.DegToRad(-25)}

// modelOrbiting/modelPanning track a middle-mouse drag in progress.
var (
	modelOrbiting bool
	modelPanning  bool
	modelLooking  bool // right-drag mouse-look, as in the 3D view
)

// modelMouseX/Y is the last cursor position seen in this window, kept because
// the gizmo highlights the arm under the cursor and Draw has no event to read.
var modelMouseX, modelMouseY int32

// ---------- camera ----------

// modelCamera frames the document. It has two modes because model work needs
// both: orbit for inspecting a single object from all sides, fly for getting
// inside a large one or lining a face up with the view.
//
// Yaw and Pitch are the *look* direction in radians, the same convention as the
// global Cam (camera.go), which is what lets fly mode reuse DirectionFromAngles
// and the editor's own MouseRotationSpeed/MaxPitch. Orbit derives the eye from
// them (eye = Target - look*Dist), so switching modes never jumps the view.
type modelCamera struct {
	Target mgl32.Vec3 // orbit pivot
	Pos    mgl32.Vec3 // eye, authoritative in fly mode
	Dist   float32    // orbit radius
	Yaw    float32    // radians
	Pitch  float32    // radians
	Fly    bool
}

// forward is the unit look direction.
func (c modelCamera) forward() mgl32.Vec3 {
	return gfx.DirectionFromAngles(c.Yaw, c.Pitch)
}

// position returns the eye point.
func (c modelCamera) position() mgl32.Vec3 {
	if c.Fly {
		return c.Pos
	}
	return c.Target.Sub(c.forward().Mul(c.Dist))
}

func (c modelCamera) view() mgl32.Mat4 {
	eye := c.position()
	return mgl32.LookAtV(eye, eye.Add(c.forward()), gfx.WorldUp)
}

func (c modelCamera) proj(w, h int32) mgl32.Mat4 {
	aspect := float32(1)
	if h > 0 {
		aspect = float32(w) / float32(h)
	}
	return mgl32.Perspective(mgl32.DegToRad(60), aspect, 0.01, 500)
}

// right and up are the camera's screen-aligned axes, used to pan in the plane
// the user is looking at rather than along world axes.
func (c modelCamera) right() mgl32.Vec3 {
	r := c.forward().Cross(gfx.WorldUp)
	if r.Len() < 1e-5 {
		return mgl32.Vec3{1, 0, 0}
	}
	return r.Normalize()
}

func (c modelCamera) up() mgl32.Vec3 {
	return c.right().Cross(c.forward()).Normalize()
}

// ray returns the world-space picking ray through pixel (sx, sy) of a w x h
// viewport. This is screenRay (editor_3d.go) against this window's own camera
// and projection instead of the main window's globals.
func (c modelCamera) ray(sx, sy, w, h int32) (origin, dir mgl32.Vec3) {
	if w <= 0 || h <= 0 {
		return c.position(), mgl32.Vec3{0, 0, -1}
	}
	ndcX := (float32(sx)/float32(w))*2 - 1
	ndcY := 1 - (float32(sy)/float32(h))*2

	eye := c.proj(w, h).Inv().Mul4x1(mgl32.Vec4{ndcX, ndcY, -1, 1})
	eye[2], eye[3] = -1, 0

	world := c.view().Inv().Mul4x1(eye)
	return c.position(), mgl32.Vec3{world.X(), world.Y(), world.Z()}.Normalize()
}

// setFly switches modes without moving the eye or changing where it looks. Only
// the derived half of the state is recomputed: fly adopts the orbit eye as its
// position, orbit puts its pivot back out in front at the current distance.
func (c *modelCamera) setFly(fly bool) {
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

// frame points the camera at the whole document, so an imported model is
// visible whatever scale it was authored at. Always lands in orbit mode: the
// point of framing is to look at the object as a whole.
func (c *modelCamera) frame(m *ModelDoc) {
	c.Fly = false
	if m == nil || len(m.Verts) == 0 {
		c.Target = mgl32.Vec3{}
		c.Dist = 4
		return
	}
	min, max := m.Bounds()
	c.Target = min.Add(max).Mul(0.5)
	radius := max.Sub(min).Len() / 2
	if radius < 0.1 {
		radius = 0.1
	}
	// 60 degree vertical FOV, with a margin so the model does not touch the
	// window edges.
	c.Dist = radius / float32(math.Tan(float64(mgl32.DegToRad(30)))) * 1.6
}

// look applies a mouse-look delta, using the editor's own sensitivity and pitch
// clamp so flying here feels identical to flying in the 3D view.
func (c *modelCamera) look(dx, dy int32) {
	c.Yaw += platform.MouseRotationSpeed * float32(dx)
	c.Pitch -= platform.MouseRotationSpeed * float32(dy)
	c.Pitch = geom.ClampValue(c.Pitch, -gfx.MaxPitch, gfx.MaxPitch)
}

// move flies the eye. f/u/r are forward, up and right amounts; forward is
// flattened to the horizontal plane exactly as MoveCamera (camera.go) does, so
// looking down does not drive the camera into the floor.
func (c *modelCamera) move(f, u, r float32) {
	if !c.Fly {
		return
	}
	d := c.forward()
	forward := mgl32.Vec3{d.X(), 0, d.Z()}
	if forward.Len() > 1e-6 {
		forward = forward.Normalize()
	}
	right := mgl32.Vec3{d.Z(), 0, -d.X()}
	if right.Len() > 1e-6 {
		right = right.Normalize()
	}
	c.Pos = c.Pos.Add(forward.Mul(f)).Add(gfx.WorldUp.Mul(u)).Add(right.Mul(r))
}

// ---------- lifecycle ----------

// ModelEditorOpen reports whether the model editor currently has a window.
func ModelEditorOpen() bool { return modelEditorWindow != nil }

// OpenModelEditorWindow opens the model editor, or raises it if it is already
// up. The editor always has something to edit: with no document loaded it
// starts on a cube, the same way the app boots into a blank level rather than a
// file dialog.
func OpenModelEditorWindow() {
	if modelEditorWindow != nil {
		modelEditorWindow.Win.Raise()
		return
	}
	if modelDoc == nil {
		modelDoc = NewCubeDoc()
		modelCam.frame(modelDoc)
	}
	w, err := NewUIWindow("Model Editor", modelWindowW, modelWindowH,
		drawModelEditorWindow, onModelEditorClose)
	if err != nil {
		log.Printf("open model editor window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0.11, 0.11, 0.13, 1}
	w.HandleEvent = modelEditorHandleEvent
	modelEditorWindow = w
}

// CloseModelEditorWindow asks for the pop-out to go away at the end of the
// frame. Safe to call from inside the window's own Draw — see
// UIWindow.CloseRequested.
func CloseModelEditorWindow() {
	if modelEditorWindow != nil {
		modelEditorWindow.CloseRequested = true
	}
}

// onModelEditorClose runs with the window's GL context still current, which is
// what makes it the right place to free the mesh's VAOs. The document itself
// stays in memory so reopening resumes where the user left off; marking it dirty
// is what schedules the rebuild that reopening will need.
func onModelEditorClose() {
	modelEditorFreeGeometry()
	if modelDoc != nil {
		modelDoc.invalidate()
	}
	modelEditorWindow = nil
}

// modelEditorFocused reports whether this window owns keyboard input.
// sdl.GetKeyboardState is global, so without this check a keypress meant for the
// model editor would also reach the main window's fly camera.
func modelEditorFocused() bool {
	return modelEditorWindow != nil &&
		modelEditorWindow.Win.GetFlags()&sdl.WINDOW_INPUT_FOCUS != 0
}

// ---------- per-frame ----------

// drawModelEditorWindow renders one frame of the model editor. Called by
// RenderSecondaryWindows with this window's GL and ImGui contexts current and
// its framebuffer already cleared.
func drawModelEditorWindow() {
	w := modelEditorWindow
	if w == nil {
		return
	}
	if modelDoc == nil {
		modelDoc = NewCubeDoc()
		modelCam.frame(modelDoc)
	}
	if err := modelEditorEnsureProgram(); err != nil {
		log.Printf("model editor: %v", err)
		CloseModelEditorWindow()
		return
	}
	// Playback before the rebuild check, so the frame it advances to is the
	// frame this same frame uploads — otherwise every pose would show one frame
	// late.
	modelPoseTick(platform.DeltaTime)

	// Rebuild once per frame at most, however many edits landed since the last
	// one. The surface editor rebuilds per mouse-motion event; at mesh sizes
	// this editor will see, once a frame is both cheaper and simpler to reason
	// about.
	if modelDoc.Dirty() {
		modelEditorRebuild()
	}

	modelEditorFly()
	// Before any overlay reads it. Cached against the eye position and the
	// mesh revision, so this is free unless one of them moved.
	modelUpdateVisibility()

	view := modelCam.view()
	proj := modelCam.proj(w.W, w.H)
	projView := proj.Mul4(view)

	gl.Enable(gl.DEPTH_TEST)
	if !modelWireframe {
		// Solid shading is what hides the mesh's own far side — and therefore
		// what the visibility pass above is describing. In wireframe there is
		// no surface to hide behind, so every edge and point stays visible and
		// selectable.
		drawModelMesh(view, proj, modelCam.position())
	}
	drawModelGrid()
	drawModelWireframe()
	gizmo.GizmoFlush(projView)

	// Handles and the gizmo draw without depth testing, so a vertex on the far
	// side of the mesh is still visible and therefore still grabbable.
	gl.Disable(gl.DEPTH_TEST)
	drawModelBones()
	drawModelSelection(modelMouseX, modelMouseY)
	gizmo.GizmoFlush(projView)
	gl.Enable(gl.DEPTH_TEST)

	modelEditorToolbar()
	modelEditorPanel()
	// After the panel, so the strip lands on top of the viewport and its width
	// can be measured against the panel it stops beside.
	modelAnimTimelineWindow()
	// Last of the ImGui windows, so the popup opens over the panels as well
	// as the viewport.
	modelContextMenu()
	modelDialogs()
}

// modelFlySpeed is the per-frame fly distance, scaled by DeltaTime the same way
// HandleInput (input.go) scales the 3D view's, so the two feel alike. Holding
// shift is the usual "go faster".
func modelFlySpeed() float32 {
	speed := platform.MovementSpeed * platform.DeltaTime * 60
	if speed <= 0 {
		speed = platform.MovementSpeed
	}
	return speed
}

// modelEditorFly applies WASD/Space/Ctrl movement once per frame.
//
// Gated on this window having input focus: sdl.GetKeyboardState is global, so
// without the check a W held here would also drive the main window's camera.
// EditorTick makes the mirror-image check for the same reason.
func modelEditorFly() {
	if !modelCam.Fly || !modelEditorFocused() || platform.KeyStates == nil {
		return
	}
	if imgui.CurrentIO().WantCaptureKeyboard() {
		return
	}
	speed := modelFlySpeed()
	// Ctrl is the "go faster" modifier here rather than Shift, because Shift is
	// spoken for: it descends, the same as the gameplay fly camera (input.go)
	// and the F1 reference, so up and down are the same two keys everywhere.
	if platform.KeyStates[sdl.SCANCODE_LCTRL] != 0 || platform.KeyStates[sdl.SCANCODE_RCTRL] != 0 {
		speed *= 4
	}

	var f, u, r float32
	if platform.KeyStates[sdl.SCANCODE_W] != 0 {
		f += speed
	}
	if platform.KeyStates[sdl.SCANCODE_S] != 0 {
		f -= speed
	}
	// The 3D view's A/D map to +right/-right through MoveCamera's right vector,
	// which points camera-left; matching it keeps the strafe direction the same
	// in both windows.
	if platform.KeyStates[sdl.SCANCODE_A] != 0 {
		r += speed
	}
	if platform.KeyStates[sdl.SCANCODE_D] != 0 {
		r -= speed
	}
	if platform.KeyStates[sdl.SCANCODE_SPACE] != 0 {
		u += speed
	}
	// Not while panning: Shift+MMB is the pan gesture, and the user holding it
	// to drag the view is not also asking to sink.
	if !modelPanning && (platform.KeyStates[sdl.SCANCODE_LSHIFT] != 0 || platform.KeyStates[sdl.SCANCODE_RSHIFT] != 0) {
		u -= speed
	}
	modelCam.move(f, u, r)
}

// modelEditorToolbar draws the two-row top bar. Level-wide actions stay with
// the main window's toolbar; this one carries only what belongs to the model.
func modelEditorToolbar() {
	w := modelEditorWindow
	if w == nil {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: 0})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W), Y: modelToolbarH})
	imgui.BeginV("##modeleditor_toolbar", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|
			imgui.WindowFlagsNoMove|imgui.WindowFlagsNoScrollbar)

	firstRowY := imgui.CursorPosY()

	// --- row 1: the file menu ---
	modelEditorMenuBar()

	const popInBtnWidth = 90
	imgui.SetCursorPos(imgui.Vec2{X: float32(w.W) - popInBtnWidth - 8, Y: firstRowY})
	if imgui.ButtonV("Pop In", imgui.Vec2{X: popInBtnWidth, Y: 0}) {
		CloseModelEditorWindow()
	}

	// --- row 2: what the mouse is doing ---
	id := "(untitled)"
	if modelDoc != nil && modelDoc.ID != "" {
		id = modelDoc.ID
	}
	imgui.Text(id)
	imgui.SameLine()
	imgui.Text("|")

	for _, mode := range []modelSelMode{modelSelVertex, modelSelEdge, modelSelFace, modelSelBone} {
		imgui.SameLine()
		// Latch the comparison before the button: clicking it changes the mode,
		// and re-testing afterwards would pop a style color that was never
		// pushed. The ImGui style stack is global across contexts, so an
		// unbalanced push asserts in a different window.
		active := modelSelection.Mode == mode
		if active {
			imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.85, Y: 0.55, Z: 0.1, W: 1})
		}
		if imgui.Button(fmt.Sprintf("%s [%d]", mode, int(mode)+1)) {
			modelSetSelMode(mode)
		}
		if active {
			imgui.PopStyleColor()
		}
	}

	imgui.SameLine()
	imgui.Text("|")
	for _, tool := range modelGizmoTools {
		imgui.SameLine()
		active := modelGizmoTool == tool
		if active {
			imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.85, Y: 0.55, Z: 0.1, W: 1})
		}
		if imgui.Button(fmt.Sprintf("%s [%s]", tool, modelToolKey(tool))) {
			modelSetGizmoTool(tool)
		}
		if active {
			imgui.PopStyleColor()
		}
	}

	// Which frame the scale arms are in. Only shown with that tool up, since
	// it is the only one the switch reaches, and shown as a button because the
	// keyboard route to it (a second C) is invisible otherwise.
	if modelGizmoTool == modelToolScale {
		imgui.SameLine()
		if modelScaleLocal {
			imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.55, Z: 0.55, W: 1})
		}
		if imgui.Button(fmt.Sprintf("%s axes [C]", modelScaleLocalLabel())) {
			modelSetScaleLocal(!modelScaleLocal)
		}
		if modelScaleLocal {
			imgui.PopStyleColor()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("C again — scale along the selection's own axes instead\n" +
				"of the world's: across a face and through its normal, or\n" +
				"along an edge or bone. A vertex selection has no axes of\n" +
				"its own, so it keeps the world's.")
		}
	}

	imgui.SameLine()
	imgui.Text("|")
	imgui.SameLine()
	if modelWireframe {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.45, Z: 0.7, W: 1})
	}
	if imgui.Button(modelShadingLabel()) {
		modelSetWireframe(!modelWireframe)
	}
	if modelWireframe {
		imgui.PopStyleColor()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Z — Solid hides the far side of the mesh, and only points\nand edges you can see can be clicked.\nWireframe shows the whole cage and lets you click any of it.")
	}

	imgui.SameLine()
	flying := modelCam.Fly
	if flying {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.55, Z: 0.35, W: 1})
	}
	if imgui.Button(modelCameraLabel(flying)) {
		modelCam.setFly(!flying)
	}
	if flying {
		imgui.PopStyleColor()
	}
	// The camera hints used to run along this row as text. They are a tooltip
	// now because the row has to carry the tool and shading toggles too, and at
	// this window's default width one line cannot hold both.
	if imgui.IsItemHovered() {
		if flying {
			imgui.SetTooltip("RMB drag to look, WASD fly, Space/Shift up and down,\n" +
				"Ctrl faster, F frame. RMB without a drag opens the\nselection's menu.")
		} else {
			imgui.SetTooltip("RMB/MMB drag to orbit, Shift+MMB pan, wheel zoom,\n" +
				"F frame. RMB without a drag opens the selection's menu.")
		}
	}

	imgui.End()
}

// modelToolKey is the shortcut shown on a tool button.
func modelToolKey(tool modelGizmoToolKind) string {
	switch tool {
	case modelToolRotate:
		return "R"
	case modelToolScale:
		return "C"
	default:
		return "G"
	}
}

// modelShadingLabel names the shading button by the mode it is in.
func modelShadingLabel() string {
	if modelWireframe {
		return "Wireframe [Z]"
	}
	return "Solid [Z]"
}

// modelSetWireframe switches shading. The visibility cache has to go with it:
// the answer changes without the camera or the mesh having moved.
func modelSetWireframe(on bool) {
	if modelWireframe == on {
		return
	}
	modelWireframe = on
	modelInvalidateVisibility()
}

// ---------- input ----------

// modelEditorHandleEvent is the window's raw SDL input path. Runs with this
// window's ImGui context current, so the WantCapture* checks below refer to its
// own panels rather than the main window's.
func modelEditorHandleEvent(event sdl.Event) {
	if modelEditorWindow == nil {
		return
	}
	io := imgui.CurrentIO()

	switch t := event.(type) {
	case sdl.KeyboardEvent:
		if t.Type != sdl.KEYDOWN || io.WantCaptureKeyboard() {
			return
		}
		modelEditorHandleKey(t)

	case sdl.MouseButtonEvent:
		if t.Type == sdl.MOUSEBUTTONDOWN {
			if io.WantCaptureMouse() {
				return
			}
			switch t.Button {
			case sdl.ButtonMiddle:
				if sdl.GetModState()&sdl.KMOD_SHIFT != 0 {
					modelPanning = true
				} else {
					modelOrbiting = true
				}
			case sdl.ButtonRight:
				// Right-drag to look, the same gesture the 3D view uses — and
				// a right-click that does not drag opens the context menu
				// instead, which is what modelContextPressed is watching for.
				modelLooking = true
				modelContextPressed(t.X, t.Y)
			case sdl.ButtonLeft:
				// The gizmo gets first refusal: its arms sit on top of the mesh,
				// so a click that grabs an arm must not also reselect whatever
				// face is behind it.
				if modelGizmoMouseDown(t.X, t.Y) {
					return
				}
				modelEditorClick(t.X, t.Y, sdl.GetModState()&sdl.KMOD_CTRL != 0)
			}
			return
		}
		// Button-up is not gated on WantCaptureMouse: releasing over a panel
		// must still end a drag that began on the viewport, or the camera would
		// stay stuck to the mouse.
		switch t.Button {
		case sdl.ButtonMiddle:
			modelOrbiting, modelPanning = false, false
		case sdl.ButtonRight:
			modelLooking = false
			modelContextReleased(t.X, t.Y)
		case sdl.ButtonLeft:
			modelGizmoMouseUp()
		}

	case sdl.MouseMotionEvent:
		modelEditorHandleMotion(t)

	case sdl.MouseWheelEvent:
		if io.WantCaptureMouse() {
			return
		}
		modelEditorZoom(t.Y)
	}
}

func modelEditorHandleKey(t sdl.KeyboardEvent) {
	mods := sdl.GetModState()
	if mods&sdl.KMOD_CTRL != 0 {
		switch t.Keysym.Scancode {
		case sdl.SCANCODE_Z:
			if mods&sdl.KMOD_SHIFT != 0 {
				ModelRedo()
			} else {
				ModelUndo()
			}
			return
		case sdl.SCANCODE_Y:
			ModelRedo()
			return
		case sdl.SCANCODE_A:
			modelSelectAll()
			return
		}
	}
	if modelEditorSelectionKey(t) {
		return
	}
	// Pose-mode keys, ahead of the general ones: I, Space and the arrows all
	// mean something else outside it, and Alt+G/R/S would otherwise be read as
	// bare tool switches.
	if modelPoseActive() && modelPoseHandleKey(t, mods) {
		return
	}
	switch t.Keysym.Scancode {
	case sdl.SCANCODE_P:
		modelTogglePoseMode()
	case sdl.SCANCODE_V:
		modelCam.setFly(!modelCam.Fly)
	case sdl.SCANCODE_G:
		modelSetGizmoTool(modelToolMove)
	case sdl.SCANCODE_R:
		modelSetGizmoTool(modelToolRotate)
	case sdl.SCANCODE_C:
		// C rather than the conventional S, which doubles as fly-mode's "back"
		// and so could only work while not flying. C is on no movement key, so
		// the three tool shortcuts all behave the same whichever camera mode is
		// current. The camera toggle moved to V to make room.
		//
		// Pressed again, with the scale tool already up, it swings the arms
		// onto the selection's own axes and back — the same "second press
		// re-aims" shape E has for extrude.
		modelScaleToolKey()
	case sdl.SCANCODE_Z:
		modelSetWireframe(!modelWireframe)
	case sdl.SCANCODE_E:
		if mods&sdl.KMOD_SHIFT != 0 {
			// Shift+E always extrudes, where a bare E on an extrude that is
			// still selected re-aims it instead. This is how a run of segments
			// is stacked without going back to the mouse.
			modelExtrudeAgain()
		} else {
			modelStartExtrude()
		}
	case sdl.SCANCODE_DELETE:
		switch {
		case modelSelection.Mode == modelSelBone:
			modelDeleteSelectedBones()
		case modelSelection.Mode == modelSelFace && len(modelSelection.Faces) > 0:
			ModelPushUndo()
			modelDoc.DeleteFaces(modelSelection.Faces)
			modelClearMeshSelection()
		}
	case sdl.SCANCODE_F:
		modelCam.frame(modelDoc)
	case sdl.SCANCODE_TAB:
		// Tab is "switch view" throughout the editor; the other view from here
		// is the main window, so hand focus back to it.
		platform.Window.Raise()
	}
}

func modelEditorHandleMotion(t sdl.MouseMotionEvent) {
	modelMouseX, modelMouseY = t.X, t.Y
	// A right-button gesture that travels is a look, not a context click.
	modelContextMoved(t.X, t.Y)

	switch {
	case modelGizmoDragging():
		modelGizmoMouseMove(t.X, t.Y)

	case modelLooking, modelOrbiting:
		// One rotation path for both: in fly mode the eye stays put and the
		// look direction turns, in orbit mode the same turn swings the eye
		// around the pivot, because position() derives it from the look.
		modelCam.look(t.XRel, t.YRel)

	case modelPanning:
		// Pan in the plane the camera is looking at, scaled by distance so the
		// model tracks the cursor at any zoom level.
		scale := modelCam.Dist * 0.0015
		delta := modelCam.right().Mul(-float32(t.XRel) * scale).
			Add(modelCam.up().Mul(float32(t.YRel) * scale))
		if modelCam.Fly {
			modelCam.Pos = modelCam.Pos.Add(delta)
		} else {
			modelCam.Target = modelCam.Target.Add(delta)
		}
	}
}

func modelEditorZoom(wheelY int32) {
	if wheelY == 0 {
		return
	}
	if modelCam.Fly {
		// Flying has no orbit radius to shrink, so the wheel moves the eye
		// along the look direction instead.
		modelCam.Pos = modelCam.Pos.Add(modelCam.forward().Mul(float32(wheelY) * modelFlySpeed() * 4))
		return
	}
	// Multiplicative so each notch covers the same visual fraction whether the
	// camera is close in or far out.
	factor := float32(math.Pow(0.88, float64(wheelY)))
	modelCam.Dist = geom.ClampValue(modelCam.Dist*factor, 0.05, 400)
}

// modelCameraLabel names the camera button by the mode it is in.
func modelCameraLabel(flying bool) string {
	if flying {
		return "Fly [V]"
	}
	return "Orbit [V]"
}
