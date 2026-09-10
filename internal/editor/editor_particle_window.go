package editor

// editor_particle_window.go — the particle editor, in its own OS window.
//
// Like the model editor and unlike the 2D sector pop-out, this window owns a
// document rather than being a second view onto Editor state: it holds a
// *particle.Doc, its own camera, and its own preview simulation. The main window
// carries on rendering the level untouched while it is open, which is the point
// — an effect is authored beside the level it is going into.
//
// Three things a secondary window supplies for itself, and where:
//
//   - a camera and projection: particleCam below, because the global Cam is
//     being driven by the main window every frame;
//   - a picking ray: not needed. Nothing here is clicked in 3D; the whole
//     document is edited through the panel and the ramp strips;
//   - GL object ownership: the preview draws through the shared sprite pass,
//     whose VAO and programs belong to the main context. That is deliberate —
//     see previewDraw.

import (
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/particle"
	"foundry/internal/platform"
	"foundry/internal/uiwin"
	"log"
	"math"
	"path/filepath"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	particleWindowW = int32(1280)
	particleWindowH = int32(800)

	// Two rows: file actions above, playback and view below. One row cannot
	// hold both at this width — the same reason the model editor's toolbar is
	// two rows.
	particleToolbarH = float32(54)

	// particlePanelWidth matches the model editor's, so the two windows read as
	// the same tool with different middles.
	particlePanelWidth = float32(300)

	// particleLayerListWidth is the left column. Narrow: it holds names and two
	// letter-wide toggles, nothing else.
	particleLayerListWidth = float32(190)
)

// particleEditorWindow is the pop-out; nil when the editor is closed.
var particleEditorWindow *uiwin.UIWindow

// particleDoc is the effect being edited. It outlives the window, so popping
// the editor in and out returns to the same effect rather than a fresh one.
var particleDoc *particle.Doc

// particleSystems is the preview simulation, one per layer, rebuilt whenever the
// document's layer count changes.
var particleSystems []*particle.System

// particleActiveLayer indexes particleDoc.Layers.
var particleActiveLayer int

// particleSolo is the index of the layer being soloed, or -1. Soloing is a view
// state rather than document state — it is how you look at one layer of three,
// not something an effect ships with. Muting is the one that is saved.
var particleSolo = -1

// Playback state. Speed is a view control; nothing about it is saved.
var (
	particlePlaying  = true
	particleSpeed    = float32(1)
	particleTime     float32
	particleStepOnce bool
)

// Preview scene toggles.
var (
	particleShowGrid   = true
	particleShowGround = true
	particleShowShape  = false
	particleBgColor    = [3]float32{0.10, 0.11, 0.13}
)

// particleStatus is the line under the toolbar.
var particleStatus string

// particleCam frames the effect. Pitched slightly down so a fountain reads as
// rising rather than as coming at you.
var particleCam = particleCamera{Dist: 4, Yaw: mgl32.DegToRad(35), Pitch: mgl32.DegToRad(-15), Target: mgl32.Vec3{0, 0.8, 0}}

var (
	particleOrbiting bool
	particlePanning  bool
)

// ---------- lifecycle ----------

// ParticleEditorOpen reports whether the window is up.
func ParticleEditorOpen() bool { return particleEditorWindow != nil }

// OpenParticleEditorWindow opens the editor, or raises it if it is already up.
//
// It always has something to edit: with nothing loaded it starts on a working
// one-layer effect, the way the model editor starts on a cube rather than on a
// file dialog.
func OpenParticleEditorWindow() {
	if particleEditorWindow != nil {
		particleEditorWindow.Win.Raise()
		return
	}
	particleEnsureDoc()
	w, err := uiwin.NewUIWindow("Particle Editor", particleWindowW, particleWindowH,
		drawParticleEditorWindow, onParticleEditorClose)
	if err != nil {
		log.Printf("open particle editor window: %v", err)
		return
	}
	w.ClearColor = [4]float32{particleBgColor[0], particleBgColor[1], particleBgColor[2], 1}
	w.HandleEvent = particleEditorHandleEvent
	// Assigned last: until this is set, ParticleEditorOpen has to keep saying
	// no, or a Draw triggered during construction would find a half-built
	// window.
	particleEditorWindow = w
}

// OpenParticleEditorOn opens the editor on a named effect — the level editor's
// Edit button beside an emitter.
//
// Deferred through RequestWindowOpen because the caller is inside another
// window's Draw: creating a window there inserts into the map being ranged over
// and leaves the wrong GL context current. Same handoff requestTexEditor
// makes.
func OpenParticleEditorOn(effect string) {
	if effect == "" {
		return
	}
	uiwin.RequestWindowOpen(func() {
		if err := particleLoad(particle.ParticlePath(effect)); err != nil {
			log.Printf("particle editor: %v", err)
			editorSetStatus("Could not open %s: %v", effect, err)
			return
		}
		OpenParticleEditorWindow()
	})
}

// CloseParticleEditorWindow asks for the window to go away at the end of the
// frame. Safe to call from inside its own Draw.
func CloseParticleEditorWindow() {
	if particleEditorWindow != nil {
		particleEditorWindow.CloseRequested = true
	}
}

// onParticleEditorClose runs with this window's GL context current. The
// document stays in memory: closing the window is not discarding the work.
func onParticleEditorClose() {
	particleEditorWindow = nil
}

// particleEditorFocused reports whether this window owns keyboard input.
// sdl.GetKeyboardState is process-global, so without this a keypress meant for
// the particle editor would also reach the main window's fly camera.
func particleEditorFocused() bool {
	return particleEditorWindow != nil &&
		particleEditorWindow.Win.GetFlags()&sdl.WINDOW_INPUT_FOCUS != 0
}

// particleEnsureDoc guarantees there is something to edit.
func particleEnsureDoc() {
	if particleDoc == nil {
		particleDoc = particle.NewDoc("effect")
		particleActiveLayer = 0
		particleRebuildSystems()
	}
}

// ---------- the document ----------

// particleRebuildSystems throws away the preview simulation and builds a fresh
// one from the document. Called when the layer count changes; a parameter tweak
// goes through particleSyncSystems instead, which keeps what is in flight.
func particleRebuildSystems() {
	particleSystems = nil
	if particleDoc == nil {
		return
	}
	for i := range particleDoc.Layers {
		particleSystems = append(particleSystems,
			particle.NewSystem(particleDoc.Layers[i], particleDoc.Gravity, particleDoc.Seed+uint32(i)))
	}
	particleTime = 0
}

// particleSyncSystems pushes the document's current parameters into the running
// simulation without resetting it.
//
// That is the whole feel of the editor: drag the size ramp and the particles
// already in flight change size, rather than the change only reaching the next
// ones. An effect authored the other way is authored by waiting.
func particleSyncSystems() {
	if particleDoc == nil {
		return
	}
	if len(particleSystems) != len(particleDoc.Layers) {
		particleRebuildSystems()
		return
	}
	for i := range particleDoc.Layers {
		particleSystems[i].SetLayer(particleDoc.Layers[i], particleDoc.Gravity)
	}
}

// particleActive returns the layer being edited, or nil.
func particleActive() *particle.Layer {
	if particleDoc == nil || particleActiveLayer < 0 || particleActiveLayer >= len(particleDoc.Layers) {
		return nil
	}
	return &particleDoc.Layers[particleActiveLayer]
}

// particleLayerVisible reports whether a layer should be simulated and drawn,
// which is muting and soloing together.
func particleLayerVisible(i int) bool {
	if particleSolo >= 0 {
		return i == particleSolo
	}
	return i < len(particleDoc.Layers) && !particleDoc.Layers[i].Muted
}

// particleRestart clears every live particle and rewinds.
func particleRestart() {
	for _, s := range particleSystems {
		s.Reset()
	}
	particleTime = 0
}

// particleLoad reads a document from disk and makes it the one being edited.
func particleLoad(path string) error {
	d, err := particle.LoadParticle(path)
	if err != nil {
		return err
	}
	particleDoc = d
	particleActiveLayer = 0
	particleSolo = -1
	ParticleClearUndoHistory()
	particleRebuildSystems()
	particleStatus = "Opened " + filepath.Base(path)
	return nil
}

// particleSave writes the document and tells the level editor, so emitters in
// the level being authored beside this window pick the change up without being
// reloaded by hand.
func particleSave(path string) error {
	if particleDoc == nil {
		return nil
	}
	if err := particle.SaveParticle(particleDoc, path); err != nil {
		return err
	}
	particleDoc.Path = path
	particleDoc.Clean()
	particleStatus = "Saved " + filepath.Base(path)
	ReloadEmittersUsing(particleEffectName())
	return nil
}

// particleEffectName is the document's name as an emitter would spell it —
// the base name with the extension trimmed.
func particleEffectName() string {
	if particleDoc == nil {
		return ""
	}
	if particleDoc.Path == "" {
		return particleDoc.ID
	}
	b := filepath.Base(particleDoc.Path)
	return b[:len(b)-len(filepath.Ext(b))]
}

// ---------- the camera ----------

// particleCamera frames the effect. Orbit only: a particle effect is a thing
// you walk around, not one you fly inside, and leaving fly out keeps the
// controls to two drags.
type particleCamera struct {
	Target     mgl32.Vec3
	Dist       float32
	Yaw, Pitch float32
}

func (c particleCamera) forward() mgl32.Vec3 {
	return gfx.DirectionFromAngles(c.Yaw, c.Pitch)
}

func (c particleCamera) position() mgl32.Vec3 {
	return c.Target.Sub(c.forward().Mul(c.Dist))
}

func (c particleCamera) view() mgl32.Mat4 {
	return mgl32.LookAtV(c.position(), c.Target, mgl32.Vec3{0, 1, 0})
}

func (c particleCamera) proj(w, h int32) mgl32.Mat4 {
	if h <= 0 {
		h = 1
	}
	return mgl32.Perspective(mgl32.DegToRad(60), float32(w)/float32(h), 0.05, 500)
}

func (c particleCamera) right() mgl32.Vec3 {
	return c.forward().Cross(mgl32.Vec3{0, 1, 0}).Normalize()
}

func (c *particleCamera) orbit(dx, dy int32) {
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

func (c *particleCamera) pan(dx, dy int32) {
	up := c.right().Cross(c.forward()).Normalize()
	scale := c.Dist * 0.0015
	c.Target = c.Target.
		Sub(c.right().Mul(float32(dx) * scale)).
		Add(up.Mul(float32(dy) * scale))
}

func (c *particleCamera) zoom(delta int32) {
	c.Dist *= float32(math.Pow(0.9, float64(delta)))
	if c.Dist < 0.3 {
		c.Dist = 0.3
	}
	if c.Dist > 200 {
		c.Dist = 200
	}
}

// ---------- input ----------

// particleEditorHandleEvent handles this window's mouse and keys.
func particleEditorHandleEvent(event sdl.Event) {
	io := imgui.CurrentIO()
	switch t := event.(type) {
	case sdl.MouseButtonEvent:
		if io.WantCaptureMouse() && t.Type == sdl.MOUSEBUTTONDOWN {
			return
		}
		down := t.Type == sdl.MOUSEBUTTONDOWN
		switch t.Button {
		case sdl.ButtonMiddle:
			// Middle drag orbits, shift+middle pans — the model editor's
			// convention, so muscle memory carries between the two windows.
			if down && sdl.GetModState()&sdl.KMOD_SHIFT != 0 {
				particlePanning = true
			} else {
				particleOrbiting = down
			}
			if !down {
				particleOrbiting, particlePanning = false, false
			}
		case sdl.ButtonRight:
			particleOrbiting = down
		}

	case sdl.MouseMotionEvent:
		if particlePanning {
			particleCam.pan(t.XRel, t.YRel)
		} else if particleOrbiting {
			particleCam.orbit(t.XRel, t.YRel)
		}

	case sdl.MouseWheelEvent:
		if !io.WantCaptureMouse() {
			particleCam.zoom(t.Y)
		}

	case sdl.KeyboardEvent:
		if t.Type != sdl.KEYDOWN || io.WantCaptureKeyboard() {
			return
		}
		particleEditorHandleKey(t)
	}
}

// particleShortcut is what a keypress means here. Returned as a value rather
// than acted on inline so the mapping can be unit-tested without a GL context —
// the arrangement texEditorShortcutFor uses, and the cleanest of the three.
type particleShortcut int

const (
	particleShortcutNone particleShortcut = iota
	particleShortcutPlayPause
	particleShortcutRestart
	particleShortcutStep
	particleShortcutSave
	particleShortcutOpen
	particleShortcutNew
	particleShortcutUndo
	particleShortcutRedo
	particleShortcutFrameCamera
	particleShortcutToggleShapes
)

// particleShortcutFor maps a keypress to an action.
func particleShortcutFor(sc sdl.Scancode, ctrl, shift bool) particleShortcut {
	if ctrl {
		switch sc {
		case sdl.SCANCODE_S:
			return particleShortcutSave
		case sdl.SCANCODE_O:
			return particleShortcutOpen
		case sdl.SCANCODE_N:
			return particleShortcutNew
		case sdl.SCANCODE_Z:
			if shift {
				return particleShortcutRedo
			}
			return particleShortcutUndo
		case sdl.SCANCODE_Y:
			return particleShortcutRedo
		}
		return particleShortcutNone
	}
	switch sc {
	case sdl.SCANCODE_SPACE:
		return particleShortcutPlayPause
	case sdl.SCANCODE_R:
		return particleShortcutRestart
	case sdl.SCANCODE_PERIOD:
		return particleShortcutStep
	case sdl.SCANCODE_F:
		return particleShortcutFrameCamera
	case sdl.SCANCODE_B:
		return particleShortcutToggleShapes
	}
	return particleShortcutNone
}

func particleEditorHandleKey(t sdl.KeyboardEvent) {
	mod := sdl.GetModState()
	switch particleShortcutFor(t.Keysym.Scancode, mod&sdl.KMOD_CTRL != 0, mod&sdl.KMOD_SHIFT != 0) {
	case particleShortcutPlayPause:
		particlePlaying = !particlePlaying
	case particleShortcutRestart:
		particleRestart()
	case particleShortcutStep:
		particleStepOnce = true
		particlePlaying = false
	case particleShortcutSave:
		particleSaveCurrent()
	case particleShortcutOpen:
		particleShowOpenDialog = true
	case particleShortcutNew:
		particleNewDoc()
	case particleShortcutUndo:
		ParticleUndo()
	case particleShortcutRedo:
		ParticleRedo()
	case particleShortcutFrameCamera:
		particleCam.Target = mgl32.Vec3{0, 0.8, 0}
		particleCam.Dist = 4
	case particleShortcutToggleShapes:
		particleShowShape = !particleShowShape
	}
}

// ---------- the frame ----------

// drawParticleEditorWindow is this window's whole frame, in the order the model
// editor's is: simulate, then the 3D view, then ImGui over the top.
func drawParticleEditorWindow() {
	w := particleEditorWindow
	if w == nil {
		return
	}
	particleEnsureDoc()

	// Advance before drawing, so what is shown is this frame's state rather
	// than last frame's. The collider is swapped to this window's own ground
	// plane for the duration — see withPreviewCollider.
	withPreviewCollider(func() { particleTick(platform.DeltaTime) })

	view := particleCam.view()
	proj := particleCam.proj(w.W, w.H)
	projView := proj.Mul4(view)

	gl.Enable(gl.DEPTH_TEST)
	if particleShowGrid {
		drawParticleGrid()
	}
	if particleShowGround {
		drawParticleGround()
	}
	if particleShowShape {
		drawParticleShapeOutline()
	}
	gizmo.GizmoFlush(projView)

	previewDraw(projView)

	gl.Disable(gl.DEPTH_TEST)

	// ImGui last, painting over the 3D view.
	particleEditorToolbar()
	particleEditorPanel()
	particleLayerListWindow()
	particleTransportWindow()
	particleDialogs()
}

// particleTick advances the preview simulation.
//
// Driven from here rather than from world.EmitterUpdate, because this document
// is not in any level: the editor owns its own systems so an effect can be
// authored with no level open at all.
func particleTick(dt float32) {
	if particleDoc == nil {
		return
	}
	if particleDoc.Dirty() {
		// A parameter changed this frame. Push it into the running simulation
		// rather than restarting, so the change reaches what is already in
		// flight.
		particleSyncSystems()
		particleDoc.Clean()
	}
	if len(particleSystems) != len(particleDoc.Layers) {
		particleRebuildSystems()
	}

	step := dt * particleSpeed
	if !particlePlaying {
		if !particleStepOnce {
			return
		}
		// One frame at 60Hz, whatever the window is actually running at, so
		// stepping is reproducible.
		step = 1.0 / 60.0
		particleStepOnce = false
	}
	if step <= 0 {
		return
	}

	origin := mgl32.Ident4()
	for i, s := range particleSystems {
		if !particleLayerVisible(i) {
			continue
		}
		s.Step(step, origin)
	}
	particleTime += step
}

// particleLiveCount is how many particles are in flight across every visible
// layer — the transport bar's readout, and the number an author watches while
// deciding on a cap.
func particleLiveCount() int {
	n := 0
	for i, s := range particleSystems {
		if particleLayerVisible(i) {
			n += s.Count()
		}
	}
	return n
}

// particleMaxCount is the sum of every visible layer's cap, so the readout can
// say 312/500 rather than just 312.
func particleMaxCount() int {
	n := 0
	if particleDoc == nil {
		return 0
	}
	for i := range particleDoc.Layers {
		if particleLayerVisible(i) {
			n += particleDoc.Layers[i].EffectiveMax()
		}
	}
	return n
}
