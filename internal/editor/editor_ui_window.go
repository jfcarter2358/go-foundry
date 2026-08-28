package editor

// editor_ui_window.go — the UI editor, in its own OS window.
//
// Like the model and particle editors, this window owns a document rather than
// being a second view onto Editor state: it holds a *ui.Screen, its own canvas
// view, and its own preview stack. The main window carries on rendering the
// level untouched while it is open, which is the point — a HUD is authored
// beside the level it is going into.
//
// The canvas is a *live native preview*: the same internal/uidraw pass the game
// uses, laid out by the same internal/ui solver, drawn through this window's own
// vertex array. Drawing the preview with imgui's draw list instead would have
// been less code and would have been wrong — it would put a second renderer
// between the author and what ships, and the two would disagree the moment
// either changed. The particle editor makes the same call for the same reason.
//
// Three things a secondary window supplies for itself, and where:
//
//   - a view transform: uiView below, because the canvas pans and zooms
//     independently of anything in the main window;
//   - a design resolution: uiDesignW/H, so a screen is laid out at the size it
//     will ship at rather than at whatever size this window happens to be;
//   - GL object ownership: the pass's program and buffers are shared; only the
//     vertex array is per-context, and uiwin hands it over around Draw.

import (
	"log"
	"path/filepath"

	"foundry/internal/platform"
	"foundry/internal/ui"
	"foundry/internal/uidraw"
	"foundry/internal/uiwin"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	uiWindowW = int32(1440)
	uiWindowH = int32(900)

	// Two rows: file actions above, view toggles and the design resolution
	// below. The same height the model and particle editors use, so the three
	// windows read as one tool with different middles.
	uiToolbarH = float32(54)

	// uiPanelWidth matches the other document editors'.
	uiPanelWidth = float32(300)

	// uiHierarchyWidth is the left column: a tree of names with a visibility
	// toggle, and nothing else.
	uiHierarchyWidth = float32(230)
)

// uiEditorWindow is the pop-out; nil when the editor is closed.
var uiEditorWindow *uiwin.UIWindow

// uiDoc is the screen being edited. It outlives the window, so popping the
// editor in and out returns to the same document rather than a fresh one.
var uiDoc *ui.Screen

// uiSel is the selected node's ID.
//
// A name, never an index or a pointer: undo restores a whole document, and a
// Show reloads one from disk, so anything else would be stale the moment either
// happened. It is the same rule internal/script's refs follow.
var uiSel string

// uiPreview is the stack the canvas draws. Its own stack, with its own
// resolver, which is exactly why ui.Stack.Resolve is a field rather than a
// package global — the game's reads the level and this one invents data.
var uiPreview ui.Stack

// uiList is the canvas's geometry, reused every frame.
var uiList uidraw.List

// uiStatus is the toolbar's right-hand message.
var uiStatus string

// The design resolution the canvas lays out at. Not the window size: an author
// needs the numbers on the properties panel to be the numbers the game will
// use, and those depend on the reference resolution rather than on how wide
// they happened to drag this window.
var (
	uiDesignW  = float32(1920)
	uiDesignH  = float32(1080)
	uiFreeSize bool
)

// View toggles. None of these are saved with the document; they are how you
// look at it, not part of it.
var (
	uiShowGrid     = true
	uiShowOutlines = true
	uiShowSafeArea bool
	uiShowRulers   bool
)

// uiView is the canvas pan and zoom.
type uiCanvasView struct {
	pan  mgl32.Vec2
	zoom float32
}

var uiView = uiCanvasView{zoom: 1}

// UIEditorOpen reports whether the window is up.
func UIEditorOpen() bool { return uiEditorWindow != nil }

// OpenUIEditorWindow opens the editor, or raises it if it is already up.
//
// It always has something to edit: with nothing loaded it starts on a bare
// screen, the way the model editor starts on a cube rather than on a file
// dialog.
func OpenUIEditorWindow() {
	if uiEditorWindow != nil {
		uiEditorWindow.Win.Raise()
		return
	}
	uiEnsureDoc()
	w, err := uiwin.NewUIWindow("UI Editor", uiWindowW, uiWindowH,
		drawUIEditorWindow, onUIEditorClose)
	if err != nil {
		log.Printf("open ui editor window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0.11, 0.12, 0.14, 1}
	w.HandleEvent = uiEditorHandleEvent
	// Assigned last: until this is set, UIEditorOpen has to keep saying no, or
	// a Draw triggered during construction would find a half-built window.
	uiEditorWindow = w
}

// OpenUIEditorOn opens the editor on a named screen — the level panel's Edit
// button beside a UI entry, and the include node's Edit source.
//
// Deferred through RequestWindowOpen because the caller is inside another
// window's Draw: creating a window there inserts into the map being ranged over
// and leaves the wrong GL context current.
func OpenUIEditorOn(name string) {
	if name == "" {
		return
	}
	uiwin.RequestWindowOpen(func() {
		if err := uiLoad(ui.UIPath(name)); err != nil {
			log.Printf("ui editor: %v", err)
			editorSetStatus("Could not open %s: %v", name, err)
			return
		}
		OpenUIEditorWindow()
	})
}

// CloseUIEditorWindow asks for the window to go away at the end of the frame.
// Safe to call from inside its own Draw.
func CloseUIEditorWindow() {
	if uiEditorWindow != nil {
		uiEditorWindow.CloseRequested = true
	}
}

// onUIEditorClose runs with this window's GL context current. The document
// stays in memory: closing the window is not discarding the work.
func onUIEditorClose() { uiEditorWindow = nil }

// uiEditorFocused reports whether this window owns keyboard input.
// sdl.GetKeyboardState is process-global, so without this a keypress meant for
// the UI editor would also reach the main window's fly camera.
func uiEditorFocused() bool {
	return uiEditorWindow != nil &&
		uiEditorWindow.Win.GetFlags()&sdl.WINDOW_INPUT_FOCUS != 0
}

// uiEnsureDoc guarantees there is something to edit.
func uiEnsureDoc() {
	if uiDoc != nil {
		return
	}
	uiDoc = ui.NewScreen("untitled")
	uiAdoptDocument(uiDoc)
}

// uiAdoptDocument installs a freshly loaded or created screen, discarding
// everything that referred to the previous one.
//
// Undo history goes with it: an undo stack that spanned two documents would
// restore one screen's nodes into the other's tree.
func uiAdoptDocument(s *ui.Screen) {
	uiDoc = s
	uiSel = ""
	UIClearUndoHistory()
	uiPreview.Reset()
	uiPreview.Push(uiDoc)
	uiEditorInstallResolver()
	uiFrameCanvas()
}

// uiSelected is the node the panel is editing, or nil.
func uiSelected() *ui.Node {
	if uiDoc == nil || uiSel == "" {
		return nil
	}
	return uiDoc.Find(uiSel)
}

// uiSelect points the panel at a node, refusing anything spliced in from an
// include: editing one would write the change into a file that is not the one
// being edited.
func uiSelect(n *ui.Node) {
	if n == nil {
		uiSel = ""
		return
	}
	if n.FromInclude() {
		return
	}
	uiSel = n.ID
}

// ---------- files ----------

// uiFileList caches the open dialog's listing. Set to nil to force a rescan.
var uiFileList []string

func uiScreenName() string {
	if uiDoc == nil || uiDoc.Path == "" {
		return "untitled"
	}
	return ui.ScreenName(uiDoc.Path)
}

func uiLoad(path string) error {
	s, err := ui.LoadScreen(path)
	if err != nil {
		return err
	}
	uiAdoptDocument(s)
	uiStatus = "Opened " + filepath.Base(path)
	return nil
}

// uiSave writes the document and tells everything that was using it.
//
// Invalidating the cache is what makes an edit to a prefab reach the menus that
// include it: without it, a screen shown later would come back from the cache
// with the old copy spliced in.
func uiSave(path string) error {
	if uiDoc == nil {
		return nil
	}
	if err := ui.SaveScreen(uiDoc, path); err != nil {
		return err
	}
	ui.InvalidateScreen(ui.ScreenName(path))
	uiFileList = nil
	uiStatus = "Saved " + filepath.Base(path)
	return nil
}

// ---------- the frame ----------

func drawUIEditorWindow() {
	w := uiEditorWindow
	if w == nil {
		return
	}
	uiEnsureDoc()
	if err := uidraw.Init(); err != nil {
		log.Printf("ui editor renderer: %v", err)
		return
	}

	// The preview stack always holds exactly this document. Re-pushing rather
	// than assuming, because Undo replaces the *ui.Screen wholesale.
	if uiPreview.Top() != uiDoc {
		uiPreview.Reset()
		uiPreview.Push(uiDoc)
	}

	area := uiDesignArea(w)
	uiPreview.Tick(platform.DeltaTime, area.W, area.H)
	uidraw.SyncFontAtlases()

	uiEditorDrawCanvas(w, area)

	// ImGui last, painting over the native preview.
	uiEditorToolbar(w)
	uiEditorHierarchy(w)
	uiEditorPanel(w)
	uiEditorDialogs(w)
	uiEndEdits()
}

// uiDesignArea is the rectangle the document is laid out in: the chosen design
// resolution, or the canvas viewport itself in Free mode.
func uiDesignArea(w *uiwin.UIWindow) ui.Rect {
	if uiFreeSize {
		r := uiCanvasRect(w)
		return ui.Rect{W: r.W, H: r.H}
	}
	return ui.Rect{W: uiDesignW, H: uiDesignH}
}

// uiCanvasRect is the viewport the canvas occupies, in this window's pixels.
// Computed from the layout constants rather than queried from imgui, because
// the native pass draws before any imgui window has been laid out.
func uiCanvasRect(w *uiwin.UIWindow) ui.Rect {
	return ui.Rect{
		X: uiHierarchyWidth,
		Y: uiToolbarH,
		W: float32(w.W) - uiHierarchyWidth - uiPanelWidth,
		H: float32(w.H) - uiToolbarH,
	}
}

// designToScreen maps a point in the document's own units to this window's
// pixels.
func (v uiCanvasView) designToScreen(p mgl32.Vec2, canvas, area ui.Rect) mgl32.Vec2 {
	s := v.scale(canvas, area)
	ox, oy := v.origin(canvas, area)
	return mgl32.Vec2{ox + p[0]*s, oy + p[1]*s}
}

// screenToDesign is designToScreen's inverse — what a click means.
func (v uiCanvasView) screenToDesign(p mgl32.Vec2, canvas, area ui.Rect) mgl32.Vec2 {
	s := v.scale(canvas, area)
	if s == 0 {
		return mgl32.Vec2{}
	}
	ox, oy := v.origin(canvas, area)
	return mgl32.Vec2{(p[0] - ox) / s, (p[1] - oy) / s}
}

// screenToDesignSize converts a pixel distance — a drag delta, a snap
// tolerance — into document units.
func (v uiCanvasView) screenToDesignSize(d float32, canvas, area ui.Rect) float32 {
	s := v.scale(canvas, area)
	if s == 0 {
		return 0
	}
	return d / s
}

// scale is document units to window pixels: the fit-to-viewport factor times
// the user's zoom.
func (v uiCanvasView) scale(canvas, area ui.Rect) float32 {
	if area.W <= 0 || area.H <= 0 {
		return v.zoom
	}
	fit := canvas.W / area.W
	if h := canvas.H / area.H; h < fit {
		fit = h
	}
	return fit * v.zoom * uiCanvasFitMargin
}

func (v uiCanvasView) origin(canvas, area ui.Rect) (x, y float32) {
	s := v.scale(canvas, area)
	x = canvas.X + (canvas.W-area.W*s)/2 + v.pan[0]
	y = canvas.Y + (canvas.H-area.H*s)/2 + v.pan[1]
	return x, y
}

// uiCanvasFitMargin leaves a little room round the document at zoom 1, so its
// edges and the handles on them are reachable rather than flush with the
// viewport border.
const uiCanvasFitMargin = 0.92

// uiFrameCanvas resets the view to fit the document.
func uiFrameCanvas() {
	uiView.pan = mgl32.Vec2{}
	uiView.zoom = 1
}

// uiEditorInstallResolver points the preview's bindings at whichever data
// source the toolbar has selected.
func uiEditorInstallResolver() {
	switch uiSampleData {
	case uiSampleLiteral:
		// No resolver: every widget shows exactly what the document says.
		uiPreview.Resolve = nil
	case uiSampleLive:
		uiPreview.Resolve = uiLiveResolver
	default:
		uiPreview.Resolve = uiSampleValue
	}
}

// uiEditorHandleEvent routes this window's input: canvas gestures first, then
// keyboard shortcuts.
func uiEditorHandleEvent(event sdl.Event) {
	switch t := event.(type) {
	case *sdl.MouseButtonEvent:
		uiCanvasMouseButton(t)
	case *sdl.MouseMotionEvent:
		uiCanvasMouseMotion(t)
	case *sdl.MouseWheelEvent:
		uiCanvasWheel(t)
	case *sdl.KeyboardEvent:
		if t.State == sdl.PRESSED {
			uiEditorHandleKey(*t)
		}
	}
}

func uiEditorHandleKey(t sdl.KeyboardEvent) {
	if imgui.CurrentIO().WantCaptureKeyboard() {
		return
	}
	ctrl := t.Keysym.Mod&sdl.KMOD_CTRL != 0
	shift := t.Keysym.Mod&sdl.KMOD_SHIFT != 0

	switch uiEditorShortcutFor(t.Keysym.Scancode, ctrl, shift) {
	case uiShortcutSave:
		uiRequestSave(false)
	case uiShortcutSaveAs:
		uiRequestSave(true)
	case uiShortcutOpen:
		uiShowOpenDialog = true
		uiFileList = nil
	case uiShortcutNew:
		uiShowNewDialog = true
	case uiShortcutUndo:
		UIUndo()
	case uiShortcutRedo:
		UIRedo()
	case uiShortcutDuplicate:
		uiDuplicateSelection()
	case uiShortcutDelete:
		uiDeleteSelection()
	case uiShortcutFrame:
		uiFrameCanvas()
	case uiShortcutToggleOutlines:
		uiShowOutlines = !uiShowOutlines
	case uiShortcutToggleGrid:
		uiShowGrid = !uiShowGrid
	}
}

// uiShortcut is a keypress resolved to what it means.
//
// Mapping to a value and then acting on it — rather than acting inside the
// switch — is what lets the whole table be unit-tested with no window, no GL
// and no imgui. The particle editor's is the same shape.
type uiShortcut int

const (
	uiShortcutNone uiShortcut = iota
	uiShortcutSave
	uiShortcutSaveAs
	uiShortcutOpen
	uiShortcutNew
	uiShortcutUndo
	uiShortcutRedo
	uiShortcutDuplicate
	uiShortcutDelete
	uiShortcutFrame
	uiShortcutToggleOutlines
	uiShortcutToggleGrid
)

func uiEditorShortcutFor(sc sdl.Scancode, ctrl, shift bool) uiShortcut {
	if ctrl {
		switch sc {
		case sdl.SCANCODE_S:
			if shift {
				return uiShortcutSaveAs
			}
			return uiShortcutSave
		case sdl.SCANCODE_O:
			return uiShortcutOpen
		case sdl.SCANCODE_N:
			return uiShortcutNew
		case sdl.SCANCODE_D:
			return uiShortcutDuplicate
		case sdl.SCANCODE_Z:
			if shift {
				return uiShortcutRedo
			}
			return uiShortcutUndo
		case sdl.SCANCODE_Y:
			return uiShortcutRedo
		}
		return uiShortcutNone
	}

	switch sc {
	case sdl.SCANCODE_DELETE, sdl.SCANCODE_X:
		return uiShortcutDelete
	case sdl.SCANCODE_F:
		return uiShortcutFrame
	case sdl.SCANCODE_B:
		return uiShortcutToggleOutlines
	case sdl.SCANCODE_G:
		return uiShortcutToggleGrid
	}
	return uiShortcutNone
}
