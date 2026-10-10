package editor

// editor_material_window.go — the material editor, in its own OS window.
//
// The same shape as the particle and model editors: the window owns a
// *material.Doc rather than being a second view onto Editor state, so a
// material is authored beside the level that is going to wear it, with the
// level window still drawing.
//
// What is different here, and is the point of the whole editor, is that the
// preview draws through the ENGINE'S OWN world program — a second copy of it
// (gfx.CompileWorldProgram), set up by render.PrepareWorldProgramForPreview.
// The surface in this window is shaded by fragment.glsl, with the material
// resolved by the same gfx.SurfaceState the level uses, so what moves when a
// slider moves is the thing the game will draw and not an approximation of it.
//
// Three things a secondary window supplies for itself, and where:
//
//   - a camera and projection: materialCam below, since the global Cam belongs
//     to the level window;
//   - GL objects that cannot be shared: the preview meshes' VAOs, built in this
//     window's context on first use and released in onMaterialEditorClose. The
//     PROGRAM is shared and so is compiled once wherever it is first needed;
//   - a picking ray: not needed. Nothing here is clicked in 3D.

import (
	"fmt"
	"log"
	"math"
	"path/filepath"

	"foundry/internal/gfx"
	"foundry/internal/material"
	"foundry/internal/platform"
	"foundry/internal/uiwin"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	materialWindowW = int32(1180)
	materialWindowH = int32(760)

	// The shared two-row top bar (editor_menubar.go).
	materialToolbarH = editorTopBarH

	// materialPanelWidth matches the model and particle editors', so the three
	// windows read as the same tool with different middles.
	materialPanelWidth = float32(340)

	// materialListWidth is the left column: names and nothing else.
	materialListWidth = float32(190)
)

// materialEditorWindow is the pop-out; nil when the editor is closed.
var materialEditorWindow *uiwin.UIWindow

// materialDoc is the material being edited. It outlives the window, so popping
// the editor in and out returns to the same material rather than a fresh one.
var materialDoc *material.Doc

// materialPath is where materialDoc was loaded from or last saved to, or "" for
// one that has never been written.
var materialPath string

// materialStatus is the line under the toolbar.
var materialStatus string

// materialFileList caches the materials/ listing for the Open dialog, cleared
// whenever a save could have added one.
var materialFileList []string

// Dialog state.
var (
	materialShowOpenDialog   bool
	materialShowSaveAsDialog bool
	materialSaveAsName       string
	materialShowNewDialog    bool
	materialNewName          string
	materialNewDomain        = int32(material.DomainSurface)
)

// Preview scene toggles. Shape is which mesh the surface is shown on; a plane
// reads a tiling and a sphere reads the lighting, and neither alone is enough
// to judge a material by.
var (
	materialShape    = materialShapeSphere
	materialShowGrid = true
	// Off: a swatch is something you inspect, and a surface that will not hold
	// still is harder to judge than one that will. Orbit it instead, or turn
	// this on to watch a scroll or a wave move past a fixed point.
	materialSpin       = false
	materialSpinAngle  float32
	materialBgColor    = [3]float32{0.11, 0.12, 0.14}
	materialPreviewLit = true
)

// materialCam frames the preview. Orbit only, like the particle editor's: a
// swatch is a thing you walk around.
var materialCam = materialCamera{Dist: 3.2, Yaw: mgl32.DegToRad(35), Pitch: mgl32.DegToRad(-18)}

var (
	materialOrbiting bool
	materialPanning  bool
)

// ---------- lifecycle ----------

// MaterialEditorOpen reports whether the window is up.
func MaterialEditorOpen() bool { return materialEditorWindow != nil }

// OpenMaterialEditorWindow opens the editor, or raises it if it is already up.
//
// It always has something to edit: with nothing loaded it starts on a plain
// surface material, the way the model editor starts on a cube rather than on a
// file dialog.
func OpenMaterialEditorWindow() {
	if materialEditorWindow != nil {
		materialEditorWindow.Win.Raise()
		return
	}
	materialEnsureDoc()
	w, err := uiwin.NewUIWindow("Material Editor", materialWindowW, materialWindowH,
		drawMaterialEditorWindow, onMaterialEditorClose)
	if err != nil {
		log.Printf("open material editor window: %v", err)
		return
	}
	w.ClearColor = [4]float32{materialBgColor[0], materialBgColor[1], materialBgColor[2], 1}
	w.HandleEvent = materialEditorHandleEvent
	// Assigned last: until this is set, MaterialEditorOpen has to keep saying
	// no, or a Draw triggered during construction would find a half-built
	// window.
	materialEditorWindow = w
}

// OpenMaterialEditorOn opens the editor on a named material — the Edit button
// beside a face's or a sprite's material picker.
//
// Deferred through RequestWindowOpen because the caller is inside another
// window's Draw: creating a window there inserts into the map being ranged over
// and leaves the wrong GL context current.
func OpenMaterialEditorOn(name string) {
	if name == "" {
		return
	}
	uiwin.RequestWindowOpen(func() {
		if err := materialLoad(material.MaterialPath(name)); err != nil {
			log.Printf("material editor: %v", err)
			editorSetStatus("Could not open %s: %v", name, err)
			return
		}
		OpenMaterialEditorWindow()
	})
}

// CloseMaterialEditorWindow asks for the window to go away at the end of the
// frame. Safe to call from inside its own Draw.
func CloseMaterialEditorWindow() {
	if materialEditorWindow != nil {
		materialEditorWindow.CloseRequested = true
	}
}

// onMaterialEditorClose runs with this window's GL context current, which is
// what the mesh release needs. The document stays in memory: closing the
// window is not discarding the work.
func onMaterialEditorClose() {
	materialFreePreviewMeshes()
	// The preview slot is the editor's, so it goes with the window. Leaving it
	// installed would keep an unsaved document resolving — invisibly, since
	// nothing lists it — for the rest of the session.
	material.SetPreview(nil)
	materialEditorWindow = nil
}

// materialEditorFocused reports whether this window owns keyboard input.
// sdl.GetKeyboardState is process-global, so without this a keypress meant for
// the material editor would also reach the level window's fly camera.
func materialEditorFocused() bool {
	return materialEditorWindow != nil &&
		materialEditorWindow.Win.GetFlags()&sdl.WINDOW_INPUT_FOCUS != 0
}

// materialEnsureDoc guarantees there is something to edit.
func materialEnsureDoc() {
	if materialDoc == nil {
		materialDoc = materialNewDoc("material", material.DomainSurface)
		materialMarkSaved()
	}
}

// materialNewDoc is a fresh document of one domain, with the texture left
// empty — a material that supplies no texture is the useful default, since it
// can then be worn by surfaces that keep their own art.
func materialNewDoc(name string, d material.Domain) *material.Doc {
	return &material.Doc{Name: name, Domain: d}
}

// materialName is the document's name as a level would spell it.
func materialName() string {
	if materialDoc == nil {
		return ""
	}
	if materialPath == "" {
		return materialDoc.Name
	}
	b := filepath.Base(materialPath)
	return b[:len(b)-len(filepath.Ext(b))]
}

// ---------- load and save ----------

// materialLoad reads a document from disk and makes it the one being edited.
func materialLoad(path string) error {
	d, err := material.ReadMaterial(path)
	if err != nil {
		return err
	}
	materialDoc = d
	materialPath = path
	MaterialClearUndoHistory()
	materialMarkSaved()
	materialStatus = "Opened " + filepath.Base(path)
	return nil
}

// materialSave writes the document and installs it in the live library, so
// every surface, sprite and particle already wearing it changes in the level
// window on the next frame — see material.Put, which bumps the generation every
// cached resolution is stamped with.
func materialSave(path string) error {
	if materialDoc == nil {
		return nil
	}
	materialDoc.Name = nameFromMaterialPath(path)
	if err := material.WriteMaterialTo(path, materialDoc); err != nil {
		return err
	}
	materialPath = path
	materialPublish()
	materialMarkSaved()
	materialStatus = "Saved " + filepath.Base(path)
	return nil
}

// materialPublish installs the document under its own name and loads whatever
// textures it newly refers to. Called on save, and on any edit while Live
// Update is on.
func materialPublish() {
	if materialDoc == nil {
		return
	}
	material.Put(materialDoc.Clone())
	gfx.EnsureMaterialTextures()
	// A sky material is folded into the level's Sky rather than read at draw
	// time, so it has to be told to look again.
	invalidateSkyMaterialForEditor()
}

func nameFromMaterialPath(path string) string {
	b := filepath.Base(path)
	return b[:len(b)-len(filepath.Ext(b))]
}

// materialSaveCurrent writes to the file the document came from, or opens Save
// As when it has never been written.
func materialSaveCurrent() {
	if materialDoc == nil {
		return
	}
	if materialPath == "" {
		materialSaveAsName = materialDoc.Name
		materialShowSaveAsDialog = true
		return
	}
	if err := materialSave(materialPath); err != nil {
		materialStatus = err.Error()
	}
}

// ---------- the camera ----------

// materialCamera frames the swatch. Orbit only, for the reason
// particleCamera is: two drags is the whole control scheme.
type materialCamera struct {
	Target     mgl32.Vec3
	Dist       float32
	Yaw, Pitch float32
}

func (c materialCamera) forward() mgl32.Vec3 { return gfx.DirectionFromAngles(c.Yaw, c.Pitch) }

func (c materialCamera) position() mgl32.Vec3 {
	return c.Target.Sub(c.forward().Mul(c.Dist))
}

func (c materialCamera) view() mgl32.Mat4 {
	return mgl32.LookAtV(c.position(), c.Target, mgl32.Vec3{0, 1, 0})
}

func (c materialCamera) proj(w, h int32) mgl32.Mat4 {
	if h <= 0 {
		h = 1
	}
	return mgl32.Perspective(mgl32.DegToRad(50), float32(w)/float32(h), 0.02, 200)
}

func (c *materialCamera) orbit(dx, dy int32) {
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

func (c *materialCamera) pan(dx, dy int32) {
	right := c.forward().Cross(mgl32.Vec3{0, 1, 0}).Normalize()
	up := right.Cross(c.forward()).Normalize()
	k := c.Dist * 0.0018
	c.Target = c.Target.Sub(right.Mul(float32(dx) * k)).Add(up.Mul(float32(dy) * k))
}

func (c *materialCamera) zoom(steps int32) {
	c.Dist *= float32(math.Pow(0.9, float64(steps)))
	if c.Dist < 0.3 {
		c.Dist = 0.3
	}
	if c.Dist > 60 {
		c.Dist = 60
	}
}

// ---------- input ----------

// materialEditorHandleEvent is the window's raw SDL input path. It runs with
// this window's ImGui context current, so the WantCapture checks below refer to
// its own panels rather than the level window's.
func materialEditorHandleEvent(event sdl.Event) {
	if materialEditorWindow == nil {
		return
	}
	io := imgui.CurrentIO()

	switch t := event.(type) {
	case sdl.KeyboardEvent:
		if t.Type != sdl.KEYDOWN || io.WantCaptureKeyboard() {
			return
		}
		materialEditorKey(t)

	case sdl.MouseButtonEvent:
		if t.Type == sdl.MOUSEBUTTONDOWN {
			if io.WantCaptureMouse() {
				return
			}
			switch t.Button {
			case sdl.ButtonLeft, sdl.ButtonRight:
				materialOrbiting = true
			case sdl.ButtonMiddle:
				materialPanning = true
			}
			return
		}
		// Button-up is not gated on WantCaptureMouse: releasing over a panel
		// must still end a drag that began on the viewport, or the camera would
		// stay stuck to the mouse.
		switch t.Button {
		case sdl.ButtonLeft, sdl.ButtonRight:
			materialOrbiting = false
		case sdl.ButtonMiddle:
			materialPanning = false
		}

	case sdl.MouseMotionEvent:
		switch {
		case materialOrbiting:
			materialCam.orbit(t.XRel, t.YRel)
		case materialPanning:
			materialCam.pan(t.XRel, t.YRel)
		}

	case sdl.MouseWheelEvent:
		if io.WantCaptureMouse() {
			return
		}
		materialCam.zoom(t.Y)
	}
}

func materialEditorKey(e sdl.KeyboardEvent) {
	ctrl := e.Keysym.Mod&(sdl.KMOD_LCTRL|sdl.KMOD_RCTRL) != 0
	switch {
	case ctrl && e.Keysym.Sym == sdl.K_s:
		materialSaveCurrent()
	case ctrl && e.Keysym.Sym == sdl.K_o:
		materialShowOpenDialog, materialFileList = true, nil
	case ctrl && e.Keysym.Sym == sdl.K_n:
		materialShowNewDialog = true
	case ctrl && e.Keysym.Sym == sdl.K_z:
		MaterialUndo()
	case ctrl && e.Keysym.Sym == sdl.K_y:
		MaterialRedo()
	case e.Keysym.Sym == sdl.K_SPACE:
		materialSpin = !materialSpin
	}
}

// ---------- the frame ----------

func drawMaterialEditorWindow() {
	w := materialEditorWindow
	if w == nil {
		return
	}
	materialEnsureDoc()

	if materialSpin {
		materialSpinAngle += platform.DeltaTime * 0.4
		if materialSpinAngle > 2*math.Pi {
			materialSpinAngle -= 2 * math.Pi
		}
	}

	materialDrawPreview(w)

	// ImGui last, painting over the 3D view.
	materialEditorToolbar()
	materialEditorPanel()
	materialListWindow()
	materialPreview2DWindow()
	materialDialogs()
}

// materialStatusf sets the status line.
func materialStatusf(format string, args ...any) {
	materialStatus = fmt.Sprintf(format, args...)
}
