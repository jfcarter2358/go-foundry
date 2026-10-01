package textureui

// texture_editor_ui.go — the texture editor's ImGui panels and its OS window.
//
// Entry point is OpenTextureEditor, wired to a "Textures" button in the main
// toolbar (editor_ui.go). Only one document is open at a time — see
// activeTexEditor — matching the plan's single texture-editor-window design.
//
// This file is the only GL-touching part of the texture editor beyond
// go's upload queue: it owns a canvas preview texture and a small
// checkerboard texture, both created lazily the first time the window draws.

import (
	"fmt"
	"image/color"
	"log"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"

	"foundry/internal/fileutil"
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/imguibackend"
	"foundry/internal/texdoc"
	"foundry/internal/texture"
	"foundry/internal/uiwin"
)

var (
	ActiveTexEditor  *texture.TexEditorState
	texEditorWindow  *uiwin.UIWindow
	texEditorPreview uint32 // flattened-preview GL texture, shown on the canvas
	texEditorChecker uint32 // small tiled checkerboard behind the canvas
)

// Canvas view state (zoom in screen-pixels-per-texel, pan in screen pixels).
var (
	texEditorZoom float32 = 8
	texEditorPanX float32
	texEditorPanY float32
)

// The canvas zoom range, shared by the wheel and "Frame selection".
const (
	texEditorMinZoom float32 = 0.25
	texEditorMaxZoom float32 = 64
)

// Paint-input state, tracked across frames while a gesture is in progress.
var (
	texEditorPainting  bool
	texEditorDragStart [2]int
)

// Bump-channel view state. texEditorBumpShowNormal swaps the canvas between
// the height field being painted and the normal map derived from it — the
// height is what the brush writes, the normal map is what the engine
// samples, and being able to see the second while painting the first is how
// you tell whether the strength is anywhere near right.
var texEditorBumpShowNormal bool

// texEditorShowUVLayout draws the UV islands of whatever wears this texture over
// the canvas — an open model, or unwrapped brushes — so a texture unwrapped from
// a mesh can be painted region by region instead of blind. On by default: the
// overlay only appears at all when something open is laid out on *this*
// texture, which is precisely the case where it is wanted.
var texEditorShowUVLayout = true

// Dialog state.
var (
	texEditorShowNewDialog    bool
	texEditorNewName                = "untitled"
	texEditorNewSizeIdx       int32 = 3 // index into texEditorSizeOptions; 512
	texEditorShowOpenDialog   bool
	texEditorShowImportDialog bool
	texEditorShowConvertAll   bool
	texEditorConvertNames     []string // what the Convert All prompt will convert
	texEditorConvertReport    string   // the last run's result, shown in the prompt
	texEditorConvertLenient   bool     // convert even normal maps that come back far off
	texEditorShowLayerImport  bool
	texEditorLayerImportPath  string
	texEditorLayerImportFind  string
	texEditorLayerImportFit   int // texdoc.ImportFit
	texEditorLayerImportInto  int // 0 = new layer, 1 = replace the active layer
	texEditorShowSaveAsDialog bool
	texEditorSaveAsName       string
	texEditorShowMismatch     bool
	texEditorShowUnsaved      bool
	texEditorUnsavedThen      func() // what the unsaved-changes prompt is holding back
	texEditorSaveAsThen       func() // run once a Save As the prompt started succeeds
	texEditorPendingOpen      *texture.TexEditorState
	texEditorStatusMsg        string
	texEditorStatusTime       uint32
)

// Palette-editing state. The picked colour is applied to the in-memory palette
// live, so the canvas and any bound world texture track the slider, but it is
// only written to textures/palette.pal once the popup closes — a colour picker
// drag would otherwise rewrite the file every frame it moved.
var (
	texEditorPaletteEditIdx    uint8
	texEditorPaletteEditOpen   bool       // OpenPopup on the next frame, outside the swatch ID scope
	texEditorPaletteEditActive bool       // the popup is up; persist when it goes away
	texEditorPaletteEditOrig   color.RGBA // the slot's colour before this edit, for Revert/Cancel
	texEditorPaletteEditCol    [4]float32 // staging value the picker writes into
)

var TexEditorSizeOptions = []int32{64, 128, 256, 512, 1024, 2048}

func texEditorStatus(msg string) {
	texEditorStatusMsg = msg
	texEditorStatusTime = sdl.GetTicks()
}

// TextureEditorStatus puts a message on this window's status line from outside
// the package — what File > Reload Textures answers with, since the work it
// asks for is done by the editor package on the frame loop rather than here.
func TextureEditorStatus(msg string) { texEditorStatus(msg) }

// ---------- window lifecycle ----------

// TextureEditorOpen reports whether the texture editor window is up. Used by
// the F1 keybinding reference (keybindings.go) to foreground this window's
// shortcuts while it has focus.
func TextureEditorOpen() bool { return texEditorWindow != nil }

// OpenTextureEditor opens the texture editor window, creating it (and a
// blank starting document, if none is open yet) on first use, or just
// raising it if it's already open.
func OpenTextureEditor() {
	if texEditorWindow != nil {
		texEditorWindow.Win.Raise()
		return
	}
	w, err := uiwin.NewUIWindow("Texture Editor", 1000, 700, drawTextureEditorUI, onTextureEditorClose)
	if err != nil {
		log.Printf("open texture editor: %v", err)
		return
	}
	w.HandleEvent = texEditorHandleEvent
	w.ConfirmClose = func() bool {
		if !TextureEditorDirty() {
			return true
		}
		CloseTextureEditor()
		return false
	}
	texEditorWindow = w
	if ActiveTexEditor == nil {
		if ed, err := texture.NewTexEditorDoc("untitled", 512, 512); err == nil {
			ActiveTexEditor = ed
		}
	} else {
		ActiveTexEditor.PreviewDirty = true
	}
}

// onTextureEditorClose runs with the texture editor window's GL context
// current (see CloseUIWindow) so it can free its own preview textures. Closing
// the window by hand asks about unsaved changes first (texEditorConfirmDiscard),
// so anything still unsaved here was either discarded at that prompt or is
// going with the app on quit: the texture goes back to its saved version on
// everything wearing it, rather than leaving the model and 3D editors showing
// paint that is on no disk. The saved document is kept, so reopening the window
// carries on from it.
func onTextureEditorClose() {
	texEditorShowUnsaved = false
	texEditorUnsavedThen = nil
	texEditorSaveAsThen = nil
	texEditorDiscardUnsaved()
	if texEditorPreview != 0 {
		gl.DeleteTextures(1, &texEditorPreview)
		texEditorPreview = 0
	}
	if texEditorChecker != 0 {
		gl.DeleteTextures(1, &texEditorChecker)
		texEditorChecker = 0
	}
	texEditorWindow = nil
}

// texEditorDiscardUnsaved reverts the open document to its saved version, live
// texture included — see RevertToSaved. A document that cannot be read back is
// kept as it is: better stale paint on screen than paint lost.
func texEditorDiscardUnsaved() {
	ed := ActiveTexEditor
	if ed == nil || !ed.Doc.Dirty() {
		return
	}
	saved, err := ed.RevertToSaved()
	if err != nil {
		log.Printf("texture editor: revert %s: %v", ed.BoundTexture, err)
		return
	}
	ActiveTexEditor = saved
}

// texEditorConfirmDiscard runs then straight away when the open document has
// nothing unsaved to lose, and otherwise holds it behind the Save / Discard /
// Cancel prompt (drawTexEditorUnsavedDialog). A second request while the
// prompt is up replaces the first: the prompt answers for the latest thing
// asked.
func texEditorConfirmDiscard(then func()) {
	if !TextureEditorDirty() {
		then()
		return
	}
	texEditorUnsavedThen = then
	texEditorShowUnsaved = true
}

// TexEditorAdoptDoc makes ed the open document. The one it replaces is closed,
// so unsaved changes to it are asked about exactly as closing the window asks —
// unless ed is the same texture, whose upload supersedes them anyway (and whose
// file may just have been rewritten, as Re-unwrap does, which saving the old
// document from the prompt would clobber).
func TexEditorAdoptDoc(ed *texture.TexEditorState) {
	if old := ActiveTexEditor; old != nil && old != ed &&
		(old.BoundTexture == "" || old.BoundTexture != ed.BoundTexture) {
		texEditorConfirmDiscard(func() {
			texEditorDiscardUnsaved()
			texEditorAdoptNow(ed)
		})
		return
	}
	texEditorAdoptNow(ed)
}

func texEditorAdoptNow(ed *texture.TexEditorState) {
	ActiveTexEditor = ed
	ed.PreviewDirty = true
	texEditorZoom = 8
	texEditorPanX, texEditorPanY = 0, 0
	texEditorStatus(fmt.Sprintf("Opened %s", ed.Doc.Name))
}

// ---------- per-frame refresh ----------

func ensureTexEditorGLObjects() {
	if texEditorChecker == 0 {
		pix := []byte{
			200, 200, 200, 255, 120, 120, 120, 255,
			120, 120, 120, 255, 200, 200, 200, 255,
		}
		gl.GenTextures(1, &texEditorChecker)
		gl.BindTexture(gl.TEXTURE_2D, texEditorChecker)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.REPEAT)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.REPEAT)
		gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, 2, 2, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(pix))
	}
	if texEditorPreview == 0 {
		gl.GenTextures(1, &texEditorPreview)
		gl.BindTexture(gl.TEXTURE_2D, texEditorPreview)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	}
}

// texEditorRefreshPreview re-flattens the active document, updates the
// canvas preview texture, and — if the doc is bound to a live engine texture
// — queues a world upload and refreshes the transparency-routing flag. Runs
// at most once per frame, gated on PreviewDirty (set by any paint/structural
// mutation), so an idle frame (e.g. just panning) costs nothing.
func texEditorRefreshPreview() {
	ed := ActiveTexEditor
	if ed == nil || !ed.PreviewDirty {
		return
	}
	ensureTexEditorGLObjects()

	flat := ed.Doc.Flatten()
	rgba := texture.RgbaFromPaletted(flat)

	// The canvas shows whichever channel is being painted. On the bump
	// channel that is the height field or the normal map derived from it —
	// never the albedo, which would leave a brush stroke with nothing visible
	// happening under it.
	shown := rgba
	if ed.Doc.Channel() == texdoc.ChanBump {
		if texEditorBumpShowNormal {
			shown = ed.Doc.NormalMap()
		} else {
			shown = ed.Doc.HeightImageFrame(ed.Doc.ActiveFrameIndex())
		}
	}
	gl.BindTexture(gl.TEXTURE_2D, texEditorPreview)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, int32(ed.Doc.W), int32(ed.Doc.H), 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(shown.Pix))

	if ed.BoundTexture != "" {
		gfx.UpdateTextureHasAlpha(ed.BoundTexture, &ed.Doc.Pal, flat)
		gfx.QueueTextureUpload(ed.BoundTexture, rgba)
		gfx.TouchAnimatedTexture(ed.BoundTexture)
		if ed.BumpDirty {
			texture.SyncLiveNormalMap(ed.Doc, ed.BoundTexture)
		}
	}
	ed.PreviewDirty = false
	ed.BumpDirty = false
}

// ---------- keyboard shortcuts ----------

// texEditorShortcut names the action a key chord maps to in this window.
type texEditorShortcut int

const (
	texShortcutNone texEditorShortcut = iota
	texShortcutUndo
	texShortcutRedo
	texShortcutCopy
	texShortcutCut
	texShortcutPaste
	texShortcutSelectAll
	texShortcutDeselect
	texShortcutInvertSelection
	texShortcutEraseSelection
	texShortcutNudgeLeft
	texShortcutNudgeRight
	texShortcutNudgeUp
	texShortcutNudgeDown
)

// texEditorShortcutFor resolves one key chord to an action. Split out from the
// SDL handler below so the mapping is testable without a window: everything
// here is a pure function of the chord.
//
// typing is "a text field (layer name, texture name, ...) actually has focus",
// the one case where Ctrl+Z belongs to imgui's own text-edit undo instead. It
// is deliberately narrower than the WantCaptureKeyboard check the other
// windows use — this window is built entirely out of imgui widgets, so
// WantCaptureKeyboard is true almost all the time and would swallow every
// shortcut.
func texEditorShortcutFor(sc sdl.Scancode, ctrl, shift, typing bool) texEditorShortcut {
	if typing {
		return texShortcutNone
	}
	if !ctrl {
		// The bare keys all act on the selection; the handler ignores them
		// when there isn't one.
		switch sc {
		case sdl.SCANCODE_DELETE, sdl.SCANCODE_BACKSPACE:
			return texShortcutEraseSelection
		case sdl.SCANCODE_ESCAPE:
			return texShortcutDeselect
		case sdl.SCANCODE_LEFT:
			return texShortcutNudgeLeft
		case sdl.SCANCODE_RIGHT:
			return texShortcutNudgeRight
		case sdl.SCANCODE_UP:
			return texShortcutNudgeUp
		case sdl.SCANCODE_DOWN:
			return texShortcutNudgeDown
		}
		return texShortcutNone
	}
	switch sc {
	case sdl.SCANCODE_Z:
		if shift {
			return texShortcutRedo
		}
		return texShortcutUndo
	case sdl.SCANCODE_Y:
		return texShortcutRedo
	case sdl.SCANCODE_C:
		return texShortcutCopy
	case sdl.SCANCODE_X:
		return texShortcutCut
	case sdl.SCANCODE_V:
		return texShortcutPaste
	case sdl.SCANCODE_A:
		return texShortcutSelectAll
	case sdl.SCANCODE_D:
		return texShortcutDeselect
	case sdl.SCANCODE_I:
		if shift {
			return texShortcutInvertSelection
		}
	}
	return texShortcutNone
}

// texEditorHandleEvent runs this window's keyboard shortcuts: Ctrl+Z undo,
// Ctrl+Shift+Z / Ctrl+Y redo, matching the main editor (editor.go) and the
// 2D/model windows.
//
// Wired as the UIWindow's HandleEvent, so it reads SDL events directly —
// routed by RouteWindowEvent with this window's imgui context current, after
// imgui has already seen them. Polling imgui's own key state instead does not
// work here: this backend feeds io.KeysDown by SDL scancode (ImguiConfigureIO in
// editor_imgui.go), so IsKeyPressed takes a scancode, not the imgui.Key enum
// that names the mapping.
func texEditorHandleEvent(event sdl.Event) {
	t, ok := event.(sdl.KeyboardEvent)
	if !ok || t.Type != sdl.KEYDOWN {
		return
	}
	ed := ActiveTexEditor
	if ed == nil {
		return
	}
	mods := sdl.GetModState()
	shift := mods&sdl.KMOD_SHIFT != 0
	sc := texEditorShortcutFor(t.Keysym.Scancode,
		mods&sdl.KMOD_CTRL != 0, shift, imgui.CurrentIO().WantTextInput())
	if sc == texShortcutNone {
		return
	}
	// Every shortcut edits the document or the selection, and one landing
	// mid-drag would act on texels the still-open gesture is about to write
	// over (an undo restoring what the stroke then commits on top of), so
	// close the gesture first.
	texEditorFinishStroke(ed)
	nudge := 1
	if shift {
		nudge = 8
	}
	switch sc {
	case texShortcutUndo:
		ed.Undo()
	case texShortcutRedo:
		ed.Redo()
	case texShortcutCopy:
		texEditorCopy(ed)
	case texShortcutCut:
		texEditorCut(ed)
	case texShortcutPaste:
		texEditorPaste(ed)
	case texShortcutSelectAll:
		ed.SelectAll()
	case texShortcutDeselect:
		ed.Deselect()
	case texShortcutInvertSelection:
		ed.InvertSelection()
	case texShortcutEraseSelection:
		if ed.HasSelection() {
			ed.EraseSelection()
		}
	case texShortcutNudgeLeft:
		ed.Nudge(-nudge, 0)
	case texShortcutNudgeRight:
		ed.Nudge(nudge, 0)
	case texShortcutNudgeUp:
		ed.Nudge(0, -nudge)
	case texShortcutNudgeDown:
		ed.Nudge(0, nudge)
	}
}

// texEditorFinishStroke commits an in-progress paint gesture, if any, so an
// undo taken while the mouse is still down operates on a settled document.
func texEditorFinishStroke(ed *texture.TexEditorState) {
	if !texEditorPainting {
		return
	}
	// A move or scale is a stroke too (EndMove/EndScale are EndStroke); a
	// selection being drawn has nothing to commit and is simply abandoned.
	ed.EndStroke()
	texEditorPainting = false
	texEditorMoving = false
	texEditorScaling = -1
	texEditorLasso = nil
}

// ---------- top-level layout ----------

func drawTextureEditorUI() {
	w := texEditorWindow
	if w == nil {
		return
	}
	texEditorRefreshPreview()

	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: 0})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W), Y: float32(w.H)})
	imgui.BeginV("##texeditor_root", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|imgui.WindowFlagsNoMove|imgui.WindowFlagsNoCollapse|
			imgui.WindowFlagsMenuBar)

	drawTextureEditorMenuBar()
	imgui.Separator()

	if ActiveTexEditor == nil {
		imgui.Text("No document open - use File > New, Open, or Import PNG.")
		imgui.End()
		return
	}

	imgui.BeginChildV("##texeditor_frames", imgui.Vec2{X: 0, Y: 110}, true, 0)
	drawFramesPanel()
	imgui.EndChild()

	imgui.BeginChildV("##texeditor_left", imgui.Vec2{X: 200, Y: 0}, true, 0)
	drawToolsPanel()
	imgui.Separator()
	drawChannelPanel()
	imgui.Separator()
	if ActiveTexEditor.Doc.Channel() == texdoc.ChanBump {
		drawHeightPanel()
	} else {
		drawPalettePanel()
	}
	imgui.EndChild()

	imgui.SameLine()
	imgui.BeginChildV("##texeditor_canvas", imgui.Vec2{X: -220, Y: 0}, true, 0)
	drawCanvasPanel()
	imgui.EndChild()

	imgui.SameLine()
	imgui.BeginChildV("##texeditor_layers", imgui.Vec2{X: 0, Y: 0}, true, 0)
	drawLayersPanel()
	imgui.EndChild()

	imgui.End()
}

// ---------- menu bar + file dialogs ----------

// The parts of the menu bar every editor shares live in the editor package,
// which imports this one — so they reach it the way UVGuideForTexture does,
// installed by the editor at startup. Nil (a test, a tool) draws without them.
var (
	// SharedMenuBar draws the Window menu and the editor switcher strip.
	SharedMenuBar func()
	// SaveAllMenuItem draws File > Save All.
	SaveAllMenuItem func()
	// ReloadTexturesMenuItem draws File > Reload Textures, which re-reads
	// textures/ for files written outside the editor.
	ReloadTexturesMenuItem func()
	// MenuBarDoc draws the document name and save chip at the bar's right edge.
	MenuBarDoc func(name string, dirty bool)
)

// CloseTextureEditor asks for the window to go at the end of the frame, once
// any unsaved changes have been saved or discarded at the prompt. The saved
// document stays in memory.
func CloseTextureEditor() {
	if texEditorWindow == nil {
		return
	}
	texEditorConfirmDiscard(func() {
		if texEditorWindow != nil {
			texEditorWindow.CloseRequested = true
		}
	})
}

// TextureEditorDirty reports whether the open document has unsaved changes.
func TextureEditorDirty() bool {
	return ActiveTexEditor != nil && ActiveTexEditor.Doc.Dirty()
}

// SaveTextureEditor saves the open document, as File > Save does: a texture
// never saved under a name opens the Save As dialog instead.
func SaveTextureEditor() { texEditorDoSave() }

func drawTextureEditorMenuBar() {
	ed := ActiveTexEditor

	if imgui.BeginMenuBar() {
		texEditorMenus(ed)
		if SharedMenuBar != nil {
			SharedMenuBar()
		}
		if ed != nil && MenuBarDoc != nil {
			MenuBarDoc(ed.Doc.Name, ed.Doc.Dirty())
		}
		imgui.EndMenuBar()
	}

	// The status line always takes its row, empty or not, so the panels
	// below do not jump each time a message comes and goes.
	if texEditorStatusMsg != "" && sdl.GetTicks()-texEditorStatusTime >= 3000 {
		texEditorStatusMsg = ""
	}
	imgui.Text(texEditorStatusMsg)

	drawTexEditorNewDialog()
	drawTexEditorOpenDialog()
	drawTexEditorImportDialog()
	drawTexEditorConvertAllDialog()
	drawTexEditorSaveAsDialog()
	drawTexEditorMismatchDialog()
	drawTexEditorUnsavedDialog()
}

// texEditorMenus draws the texture editor's own menus.
func texEditorMenus(ed *texture.TexEditorState) {
	if imgui.BeginMenu("File") {
		if imgui.MenuItem("New...") {
			texEditorNewName = "untitled"
			texEditorNewSizeIdx = 3
			texEditorShowNewDialog = true
		}
		if imgui.MenuItem("Open...") {
			texEditorShowOpenDialog = true
		}
		if imgui.MenuItem("Import PNG...") {
			texEditorShowImportDialog = true
		}
		if imgui.MenuItem("Convert All PNGs to .ftex...") {
			texEditorConvertNames = texture.PNGsWithoutFtex()
			texEditorConvertReport = ""
			texEditorShowConvertAll = true
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Import every PNG in textures/ that has no .ftex yet and save it\nas one, as Import PNG + Save would, one by one.")
		}
		imgui.Separator()
		if imgui.MenuItemV("Save", "", false, ed != nil) {
			texEditorDoSave()
		}
		if imgui.MenuItemV("Save As...", "", false, ed != nil) {
			texEditorSaveAsName = ed.Doc.Name
			texEditorShowSaveAsDialog = true
		}
		if SaveAllMenuItem != nil {
			SaveAllMenuItem()
		}
		imgui.Separator()
		if ReloadTexturesMenuItem != nil {
			ReloadTexturesMenuItem()
		}
		imgui.Separator()
		if imgui.MenuItem("Close Window") {
			CloseTextureEditor()
		}
		imgui.EndMenu()
	}

	if imgui.BeginMenu("Edit") {
		// Each action closes an open stroke first, as its shortcut does.
		act := func(f func()) {
			texEditorFinishStroke(ed)
			f()
		}
		on := ed != nil
		if imgui.MenuItemV("Undo", "Ctrl+Z", false, on && ed.CanUndo()) {
			act(ed.Undo)
		}
		if imgui.MenuItemV("Redo", "Ctrl+Y", false, on && ed.CanRedo()) {
			act(ed.Redo)
		}
		imgui.Separator()
		if imgui.MenuItemV("Cut", "Ctrl+X", false, on && ed.HasSelection()) {
			act(func() { texEditorCut(ed) })
		}
		if imgui.MenuItemV("Copy", "Ctrl+C", false, on) {
			act(func() { texEditorCopy(ed) })
		}
		if imgui.MenuItemV("Paste", "Ctrl+V", false, on) {
			act(func() { texEditorPaste(ed) })
		}
		if imgui.MenuItemV("Erase Selection", "Del", false, on && ed.HasSelection()) {
			act(ed.EraseSelection)
		}
		imgui.Separator()
		if imgui.MenuItemV("Select All", "Ctrl+A", false, on) {
			act(ed.SelectAll)
		}
		if imgui.MenuItemV("Deselect", "Ctrl+D", false, on && ed.HasSelection()) {
			act(ed.Deselect)
		}
		if imgui.MenuItemV("Invert Selection", "Ctrl+Shift+I", false, on) {
			act(ed.InvertSelection)
		}
		imgui.EndMenu()
	}

	if imgui.BeginMenu("View") {
		if imgui.MenuItemV("UV Layout", "", texEditorShowUVLayout, true) {
			texEditorShowUVLayout = !texEditorShowUVLayout
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Draw the UV islands of every face that wears this texture\nover the canvas.")
		}
		imgui.EndMenu()
	}
}

func texEditorDoSave() {
	ed := ActiveTexEditor
	if ed == nil {
		return
	}
	if ed.BoundTexture == "" {
		texEditorSaveAsName = ed.Doc.Name
		texEditorShowSaveAsDialog = true
		return
	}
	if err := ed.Save(ed.BoundTexture); err != nil {
		texEditorStatus(fmt.Sprintf("Save failed: %v", err))
	} else {
		texEditorStatus(fmt.Sprintf("Saved %s", ed.BoundTexture))
	}
}

func texEditorDoOpen(name string) {
	ed, err := texture.OpenTexEditorDoc(name)
	if err != nil {
		texEditorStatus(fmt.Sprintf("Open failed: %v", err))
		return
	}
	if texture.PaletteMismatch(ed.Doc) {
		texEditorPendingOpen = ed
		texEditorShowMismatch = true
		return
	}
	TexEditorAdoptDoc(ed)
}

func drawTexEditorNewDialog() {
	imguibackend.WithModalPopup("New Texture", &texEditorShowNewDialog, func() {
		imgui.Text("Name:")
		imgui.SetNextItemWidth(200)
		imgui.InputText("##newtexname", &texEditorNewName)
		imgui.Text("Size (square):")
		sizeLabels := make([]string, len(TexEditorSizeOptions))
		for i, s := range TexEditorSizeOptions {
			sizeLabels[i] = fmt.Sprintf("%dx%d", s, s)
		}
		imgui.SetNextItemWidth(150)
		imguibackend.Combo("##newtexsize", &texEditorNewSizeIdx, sizeLabels)
		if imgui.Button("Create") && texEditorNewName != "" {
			sz := int(TexEditorSizeOptions[texEditorNewSizeIdx])
			ed, err := texture.NewTexEditorDoc(texEditorNewName, sz, sz)
			if err != nil {
				texEditorStatus(fmt.Sprintf("New failed: %v", err))
			} else {
				TexEditorAdoptDoc(ed)
			}
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##newtex") {
			imgui.CloseCurrentPopup()
		}
	})
}

func drawTexEditorOpenDialog() {
	imguibackend.WithModalPopup("Open Texture", &texEditorShowOpenDialog, func() {
		names := texture.ScanFtexFiles()
		if len(names) == 0 {
			imgui.Text("No .ftex files found in textures/.")
		}
		for _, n := range names {
			if imguibackend.SelectableV(n, false, 0, imgui.Vec2{}) {
				texEditorDoOpen(n)
				imgui.CloseCurrentPopup()
			}
		}
		imgui.Separator()
		if imgui.Button("Cancel##opentex") {
			imgui.CloseCurrentPopup()
		}
	})
}

func drawTexEditorImportDialog() {
	imguibackend.WithModalPopup("Import PNG", &texEditorShowImportDialog, func() {
		names := make([]string, 0, len(gfx.TextureNames))
		for n := range gfx.TextureNames {
			names = append(names, n)
		}
		sort.Strings(names)
		imgui.Text("Import as a new single-layer document (quantised to the current palette):")
		for _, n := range names {
			if imguibackend.SelectableV(n, false, 0, imgui.Vec2{}) {
				path := filepath.Join(fileutil.ResourcesPath, gfx.TextureNames[n])
				ed, err := texture.ImportTexEditorDoc(path)
				if err != nil {
					texEditorStatus(fmt.Sprintf("Import failed: %v", err))
				} else {
					TexEditorAdoptDoc(ed)
				}
				imgui.CloseCurrentPopup()
			}
		}
		imgui.Separator()
		if imgui.Button("Cancel##importtex") {
			imgui.CloseCurrentPopup()
		}
	})
}

// texEditorConverting is a Convert All run in flight. The conversion runs on
// its own goroutine — a 2048² normal map takes seconds to rebuild a height
// field from, and a project can have a hundred — so the dialog polls this
// each frame for progress and, at the end, the result.
var texEditorConverting struct {
	sync.Mutex
	running     bool
	done, total int
	current     string
	result      *texture.PNGConversion
}

// drawTexEditorConvertAllDialog confirms File > Convert All PNGs to .ftex,
// runs it, and shows what happened in the same window.
func drawTexEditorConvertAllDialog() {
	imguibackend.WithModalPopup("Convert All PNGs", &texEditorShowConvertAll, func() {
		c := &texEditorConverting
		c.Lock()
		running, done, total, current, result := c.running, c.done, c.total, c.current, c.result
		c.result = nil
		c.Unlock()
		if result != nil {
			texEditorFinishConvertAll(*result)
		}
		if running {
			imgui.Text(fmt.Sprintf("Converting %s...", current))
			frac := float32(0)
			if total > 0 {
				frac = float32(done) / float32(total)
			}
			imgui.ProgressBarV(frac, imgui.Vec2{X: 360, Y: 0}, fmt.Sprintf("%d / %d", done, total))
			return
		}

		if texEditorConvertReport != "" {
			imgui.BeginChildV("##convertreport", imgui.Vec2{X: 520, Y: 260}, true, 0)
			imgui.Text(texEditorConvertReport)
			imgui.EndChild()
			if imgui.Button("Close##convertall") {
				imgui.CloseCurrentPopup()
			}
			return
		}
		if len(texEditorConvertNames) == 0 {
			imgui.Text("Every PNG in textures/ already has an .ftex.")
			if imgui.Button("Close##convertall") {
				imgui.CloseCurrentPopup()
			}
			return
		}
		imgui.Text(fmt.Sprintf("Convert %d PNG(s) in textures/ that have no .ftex:", len(texEditorConvertNames)))
		imgui.BeginChildV("##convertnames", imgui.Vec2{X: 360, Y: 200}, true, 0)
		for _, n := range texEditorConvertNames {
			imgui.Text(n)
		}
		imgui.EndChild()
		imgui.Text("Each is quantised to the current palette, and its PNG is\n" +
			"rewritten from the .ftex - the original colours are not kept.\n" +
			"A _normal map beside it becomes the .ftex's bump channel, and is\n" +
			"rewritten from that.")
		imgui.Checkbox("Convert normal maps that can't be matched closely", &texEditorConvertLenient)
		if imgui.IsItemHovered() {
			imgui.SetTooltip(fmt.Sprintf("An .ftex stores height, so a normal map comes back as the nearest\n"+
				"surface it could be the slope of. Noisy maps drawn straight into the\n"+
				"normals come back smoother. Unticked, a texture whose map would be\n"+
				"more than %d degrees off on average is left as it is.", texture.DefaultMaxNormalError))
		}
		if imgui.Button("Convert") {
			texEditorStartConvertAll()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##convertall") {
			imgui.CloseCurrentPopup()
		}
	})
}

// texEditorStartConvertAll starts the conversion on its own goroutine. It
// only touches disk; the reload that follows it needs the GL context, so
// texEditorFinishConvertAll does that back on the frame loop.
func texEditorStartConvertAll() {
	opts := texture.ConvertOptions{MaxNormalError: texture.DefaultMaxNormalError}
	if texEditorConvertLenient {
		opts.MaxNormalError = 0
	}
	c := &texEditorConverting
	opts.Progress = func(done, total int, name string) {
		c.Lock()
		c.done, c.total, c.current = done, total, name
		c.Unlock()
	}
	c.Lock()
	c.running, c.done, c.total, c.current = true, 0, len(texEditorConvertNames), ""
	c.Unlock()
	go func() {
		r := texture.ConvertPNGsToFtex(opts)
		c.Lock()
		c.running, c.result = false, &r
		c.Unlock()
	}()
}

// texEditorFinishConvertAll reloads textures, so everything wearing one draws
// what it now is on disk, and writes up the run for the dialog.
func texEditorFinishConvertAll(r texture.PNGConversion) {
	// On the frame loop, like Reload Textures: the uploads go to the main GL
	// context, not this pop-out window's.
	uiwin.RequestWindowOpen(func() { texture.ReloadTextures() })

	var b strings.Builder
	fmt.Fprintf(&b, "Converted %d texture(s).", len(r.Converted))
	for _, name := range r.Converted {
		if e, ok := r.NormalError[name]; ok {
			fmt.Fprintf(&b, "\n  %s  (normal map within %.1f deg)", name, e)
		} else {
			fmt.Fprintf(&b, "\n  %s", name)
		}
	}
	for _, name := range sortedKeys(r.Skipped) {
		fmt.Fprintf(&b, "\nSkipped %s: %s", name, r.Skipped[name])
	}
	for _, name := range sortedKeys(r.Failed) {
		fmt.Fprintf(&b, "\nFailed %s: %v", name, r.Failed[name])
		log.Printf("WARNING: convert %s to .ftex: %v", name, r.Failed[name])
	}
	texEditorConvertReport = b.String()
	texEditorStatus(fmt.Sprintf("Converted %d PNG(s) to .ftex", len(r.Converted)))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// drawTexEditorLayerImportDialog imports an image into the open document's
// checked-out channel: a new layer above the selected one, or over the
// selected one. Colour is quantised to the palette; on the bump channel the
// image's brightness becomes the height.
func drawTexEditorLayerImportDialog() {
	imguibackend.WithModalPopup("Import PNG into Layer", &texEditorShowLayerImport, func() {
		ed := ActiveTexEditor
		if ed == nil {
			imgui.CloseCurrentPopup()
			return
		}
		d := ed.Doc
		if d.Channel() == texdoc.ChanBump {
			imgui.Text("Bump channel: the image's brightness becomes the height.")
		} else {
			imgui.Text("Colour channel: the image is quantised to the current palette.")
		}
		imgui.Text(fmt.Sprintf("Canvas is %dx%d.", d.W, d.H))

		imgui.Text("Into:")
		imgui.SameLine()
		imgui.RadioButtonInt("New layer", &texEditorLayerImportInto, 0)
		imgui.SameLine()
		imgui.RadioButtonInt(fmt.Sprintf("Replace \"%s\"", d.Layers[d.Active].Name), &texEditorLayerImportInto, 1)
		imgui.Text("Size:")
		imgui.SameLine()
		imgui.RadioButtonInt("Stretch to canvas", &texEditorLayerImportFit, int(texdoc.FitStretch))
		imgui.SameLine()
		imgui.RadioButtonInt("Actual size (centred)", &texEditorLayerImportFit, int(texdoc.FitActualSize))

		full := texEditorLayerImportInto == 0 && len(d.Layers) >= texdoc.TexMaxLayers
		if full {
			imgui.Text(fmt.Sprintf("The layer stack is full (%d) — pick Replace, or delete a layer.", texdoc.TexMaxLayers))
		}
		importPath := func(path string) {
			if err := ed.ImportLayerImage(path, texdoc.ImportFit(texEditorLayerImportFit), texEditorLayerImportInto == 1); err != nil {
				texEditorStatus(fmt.Sprintf("Import failed: %v", err))
			} else {
				texEditorStatus(fmt.Sprintf("Imported %s", filepath.Base(path)))
			}
			imgui.CloseCurrentPopup()
		}

		imgui.Separator()
		imgui.Text("File path (absolute, or relative to the project):")
		imgui.SetNextItemWidth(360)
		imgui.InputText("##layerimportpath", &texEditorLayerImportPath)
		imgui.SameLine()
		imguibackend.EditorPushDisabled(full || texEditorLayerImportPath == "")
		if imgui.Button("Import##layerimportpath") {
			path := texEditorLayerImportPath
			if !filepath.IsAbs(path) {
				path = filepath.Join(fileutil.ResourcesPath, path)
			}
			importPath(path)
		}
		imguibackend.EditorPopDisabled()

		imgui.Text("Or a project texture:")
		imgui.SetNextItemWidth(200)
		imgui.InputTextWithHint("##layerimportfind", "filter", &texEditorLayerImportFind)
		names := make([]string, 0, len(gfx.TextureNames))
		find := strings.ToLower(texEditorLayerImportFind)
		for n := range gfx.TextureNames {
			if strings.Contains(strings.ToLower(n), find) {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		imgui.BeginChildV("##layerimportlist", imgui.Vec2{X: 420, Y: 240}, true, 0)
		imguibackend.EditorPushDisabled(full)
		for _, n := range names {
			if imguibackend.SelectableV(n, false, 0, imgui.Vec2{}) {
				importPath(filepath.Join(fileutil.ResourcesPath, gfx.TextureNames[n]))
			}
		}
		imguibackend.EditorPopDisabled()
		imgui.EndChild()

		if imgui.Button("Cancel##layerimport") {
			imgui.CloseCurrentPopup()
		}
	})
}

func drawTexEditorSaveAsDialog() {
	imguibackend.WithModalPopup("Save Texture As", &texEditorShowSaveAsDialog, func() {
		imgui.SetNextItemWidth(200)
		imgui.InputText("##saveastexname", &texEditorSaveAsName)
		if imgui.Button("Save##saveastex") && texEditorSaveAsName != "" {
			if ed := ActiveTexEditor; ed != nil {
				if err := ed.Save(texEditorSaveAsName); err != nil {
					texEditorStatus(fmt.Sprintf("Save failed: %v", err))
				} else {
					texEditorStatus(fmt.Sprintf("Saved %s", texEditorSaveAsName))
					if then := texEditorSaveAsThen; then != nil {
						texEditorSaveAsThen = nil
						then()
					}
				}
			}
			texEditorSaveAsThen = nil
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##saveastex") {
			texEditorSaveAsThen = nil
			imgui.CloseCurrentPopup()
		}
	})
}

func drawTexEditorMismatchDialog() {
	imguibackend.WithModalPopup("Palette Mismatch", &texEditorShowMismatch, func() {
		imgui.Text("This file's embedded palette differs from the current global palette.")
		if imgui.Button("Adopt file's palette globally") {
			if err := texture.AdoptDocPaletteGlobally(texEditorPendingOpen.Doc); err != nil {
				texEditorStatus(err.Error())
			}
			TexEditorAdoptDoc(texEditorPendingOpen)
			texEditorPendingOpen = nil
			imgui.CloseCurrentPopup()
		}
		if imgui.Button("Remap art to current palette") {
			texture.RemapDocToGlobalPalette(texEditorPendingOpen.Doc)
			TexEditorAdoptDoc(texEditorPendingOpen)
			texEditorPendingOpen = nil
			imgui.CloseCurrentPopup()
		}
		if imgui.Button("Cancel##mismatch") {
			texEditorPendingOpen = nil
			imgui.CloseCurrentPopup()
		}
	})
}

// ---------- tools panel ----------

// Indexed by texture.TexTool, so the order here is the enum's.
var texEditorToolNames = []string{"Brush", "Eraser", "Fill", "Line", "Rect", "Eyedropper", "Spray", "Rect Select", "Lasso"}

func drawToolsPanel() {
	ed := ActiveTexEditor
	imgui.Text("Tools")
	for i, name := range texEditorToolNames {
		tool := texture.TexTool(i)
		// Decide push/pop once, before the click can change ed.Tool — a
		// click on a not-yet-selected row must not push a color (it wasn't
		// highlighted going in) but a re-check after Button() would see the
		// just-updated ed.Tool and pop anyway, unbalancing imgui's global
		// color stack and asserting on the next push/pop anywhere.
		selected := tool == ed.Tool
		if selected {
			imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.6, Z: 0.2, W: 1})
		}
		if imgui.Button(name) {
			ed.Tool = tool
		}
		if selected {
			imgui.PopStyleColor()
		}
	}
	imgui.Separator()
	brushSize := int32(ed.BrushSize)
	// Wrap the explanatory labels and stretch the sliders to the panel: a
	// fixed width overflows the child and scrolls it sideways.
	imgui.PushTextWrapPos()
	imgui.Text("Brush size (no hardness — quantisation would turn a soft edge into speckle):")
	imgui.PopTextWrapPos()
	imgui.SetNextItemWidth(-1)
	if imgui.SliderInt("##brushsize", &brushSize, 1, 32) {
		ed.BrushSize = int(brushSize)
	}

	if ed.Tool == texture.ToolSpray {
		density := int32(ed.SprayDensity)
		imgui.PushTextWrapPos()
		imgui.Text("Spray density (dabs/frame while held):")
		imgui.PopTextWrapPos()
		imgui.SetNextItemWidth(-1)
		if imgui.SliderInt("##spraydensity", &density, 1, 64) {
			ed.SprayDensity = int(density)
		}
	}

	imgui.Separator()
	drawSelectionPanel()
}

// drawSelectionPanel carries the selection's commands. Always shown, not just
// with a select tool up: a selection keeps fencing the brush in after you
// switch to it, and the way out of that has to be in reach.
func drawSelectionPanel() {
	ed := ActiveTexEditor
	has := ed.HasSelection()
	imgui.Text("Selection")
	if ed.Tool.IsSelectTool() {
		imgui.PushTextWrapPos()
		imgui.Text("Drag to select (Shift adds, Alt/Ctrl subtracts). Drag inside the selection, or use the arrow keys, to move what it holds.")
		imgui.PopTextWrapPos()
	} else if has {
		imgui.PushTextWrapPos()
		imgui.Text("Painting only reaches inside the selection.")
		imgui.PopTextWrapPos()
	}

	const bw = float32(88)
	if imgui.ButtonV("All##selall", imgui.Vec2{X: bw}) {
		ed.SelectAll()
	}
	imgui.SameLine()
	imguibackend.EditorPushDisabled(!has)
	if imgui.ButtonV("None##selnone", imgui.Vec2{X: bw}) {
		ed.Deselect()
	}
	if imgui.ButtonV("Invert##selinv", imgui.Vec2{X: bw}) {
		ed.InvertSelection()
	}
	imgui.SameLine()
	if imgui.ButtonV("Delete##seldel", imgui.Vec2{X: bw}) {
		ed.EraseSelection()
	}
	if imgui.ButtonV("Cut##selcut", imgui.Vec2{X: bw}) {
		texEditorCut(ed)
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	if imgui.ButtonV("Copy##selcopy", imgui.Vec2{X: bw}) {
		texEditorCopy(ed)
	}
	imguibackend.EditorPushDisabled(!texture.ClipboardFull())
	if imgui.ButtonV("Paste##selpaste", imgui.Vec2{X: bw}) {
		texEditorPaste(ed)
	}
	imguibackend.EditorPopDisabled()
}

func texEditorCopy(ed *texture.TexEditorState) {
	if !ed.Copy() {
		return
	}
	if ed.HasSelection() {
		texEditorStatus("Copied selection")
	} else {
		texEditorStatus("Copied the whole layer")
	}
}

func texEditorCut(ed *texture.TexEditorState) {
	if !ed.HasSelection() {
		texEditorStatus("Cut needs a selection")
		return
	}
	if ed.Cut() {
		texEditorStatus("Cut selection")
	}
}

func texEditorPaste(ed *texture.TexEditorState) {
	if !ed.Paste() {
		return
	}
	// Pasting is only half the job: the paste is selected and floating, and
	// the select tools are what drag it into place.
	if !ed.Tool.IsSelectTool() {
		ed.Tool = texture.ToolSelectRect
	}
	texEditorStatus("Pasted — drag it or use the arrow keys to place it")
}

// ---------- channel panel ----------

// drawChannelPanel picks which of the document's two layer stacks the tools
// paint on, and carries the bump channel's own settings.
//
// The two channels share every tool, every layer control and the whole undo
// stack — a height field is painted with the same brush, line and fill a
// colour is, because it is the same kind of thing: one byte per texel. All
// that changes is what the byte means and what the canvas shows.
func drawChannelPanel() {
	ed := ActiveTexEditor
	d := ed.Doc

	imgui.Text("Channel")
	for _, c := range []struct {
		name string
		ch   texdoc.TexChannel
	}{{"Color", texdoc.ChanColor}, {"Bump", texdoc.ChanBump}} {
		// Same push/pop-before-the-click discipline as the tool buttons: the
		// selection must be read once, before the click can change it, or the
		// style stack unbalances.
		selected := d.Channel() == c.ch
		if selected {
			imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.6, Z: 0.2, W: 1})
		}
		if imgui.ButtonV(c.name, imgui.Vec2{X: 88, Y: 0}) && !selected {
			ed.SetChannel(c.ch)
		}
		if selected {
			imgui.PopStyleColor()
		}
		if c.ch == texdoc.ChanColor {
			imgui.SameLine()
		}
	}

	if d.Channel() != texdoc.ChanBump {
		if d.HasBump() {
			imgui.PushTextWrapPos()
			imgui.Text("This texture has a bump map.")
			imgui.PopTextWrapPos()
		}
		return
	}

	imgui.PushTextWrapPos()
	imgui.Text("Painting height: lighter is higher. The engine samples the normal map derived from it, not the height itself.")
	imgui.PopTextWrapPos()

	strength := d.BumpStrength()
	imgui.SetNextItemWidth(-1)
	// Past the slider's range only when an imported normal map put it there;
	// the range grows to hold it rather than snapping it back on first touch.
	if imgui.SliderFloat("##bumpstrength", &strength, 0.25, max(texdoc.BumpSliderMax, strength)) {
		ed.SetBumpStrength(strength)
	}
	imgui.Text(fmt.Sprintf("Strength %.2f", d.BumpStrength()))

	if imgui.Checkbox("Show normal map", &texEditorBumpShowNormal) {
		ed.PreviewDirty = true
	}

	if imgui.Button("Delete Bump Map") {
		ed.DisableBump()
		texEditorStatus("Bump map deleted (Ctrl+Z to bring it back) — save to remove it from the world")
	}
}

// ---------- height panel ----------

// drawHeightPanel replaces the palette grid while the bump channel is being
// painted. A height is not a palette index — the bump channel stores the
// value straight, which is what keeps it out of reach of a palette edit or a
// remap — so it gets a grayscale ramp of its own rather than 256 swatches
// that happen to have colours in them.
func drawHeightPanel() {
	ed := ActiveTexEditor
	imgui.Text("Height")

	const cell = float32(14)
	spacing := float32(2)
	imgui.PushStyleVarVec2(imgui.StyleVarItemSpacing, imgui.Vec2{X: spacing, Y: spacing})
	perRow := int((imgui.ContentRegionAvail().X + spacing) / (cell + spacing))
	if perRow < 1 {
		perRow = 1
	}
	// 32 stops rather than all 256: the ramp is a coarse picker, and the
	// slider below reaches every value in between.
	const stops = 32
	for i := 0; i < stops; i++ {
		if i%perRow != 0 {
			imgui.SameLine()
		}
		v := uint8(i * 255 / (stops - 1))
		g := float32(v) / 255
		imgui.PushID(fmt.Sprintf("heightstop%d", i))
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: g, Y: g, Z: g, W: 1})
		if imgui.ButtonV("##hstop", imgui.Vec2{X: cell, Y: cell}) {
			ed.BumpIdx = v
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(fmt.Sprintf("Height %d", v))
		}
		imgui.PopStyleColor()
		imgui.PopID()
	}
	imgui.PopStyleVar()

	h := int32(ed.BumpIdx)
	imgui.SetNextItemWidth(-1)
	if imgui.SliderInt("##heightvalue", &h, 0, 255) {
		ed.BumpIdx = uint8(h)
	}
	imgui.PushTextWrapPos()
	imgui.Text(fmt.Sprintf("Height %d. 0 is \"nothing here\" — it lets the layers below show through, the way palette slot 0 does on the color channel. The eraser writes it.", ed.BumpIdx))
	imgui.PopTextWrapPos()
}

// ---------- palette panel ----------

func drawPalettePanel() {
	ed := ActiveTexEditor
	imgui.Text("Palette")

	// Lay the swatch grid out against the panel's real width instead of a
	// fixed 16 per row: a row wider than the child clips (and lets imgui
	// scroll the whole panel sideways, which reads as the panel being cut
	// off at its left edge).
	const cell = float32(14)
	spacing := float32(2)
	imgui.PushStyleVarVec2(imgui.StyleVarItemSpacing, imgui.Vec2{X: spacing, Y: spacing})
	perRow := int((imgui.ContentRegionAvail().X + spacing) / (cell + spacing))
	if perRow < 1 {
		perRow = 1
	}
	for i := 0; i < texdoc.TexPaletteSz; i++ {
		if i%perRow != 0 {
			imgui.SameLine()
		}
		c := texdoc.GlobalPalette[i]
		imgui.PushID(fmt.Sprintf("palslot%d", i))
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{
			X: float32(c.R) / 255, Y: float32(c.G) / 255, Z: float32(c.B) / 255, W: float32(c.A) / 255,
		})
		if imgui.ButtonV("##swatch", imgui.Vec2{X: cell, Y: cell}) {
			ed.PrimaryIdx = uint8(i)
		}
		if imgui.IsItemHovered() {
			if imgui.IsMouseClicked(1) {
				ed.SecondaryIdx = uint8(i)
			}
			if imgui.IsMouseDoubleClicked(0) {
				texEditorBeginPaletteEdit(uint8(i))
			}
			imgui.SetTooltip(fmt.Sprintf("Slot %d — %d, %d, %d, %d\nClick: primary   Right-click: secondary   Double-click: edit", i, c.R, c.G, c.B, c.A))
		}
		imgui.PopStyleColor()
		imgui.PopID()
	}
	imgui.PopStyleVar()

	imgui.Text(fmt.Sprintf("Primary: %d   Secondary: %d", ed.PrimaryIdx, ed.SecondaryIdx))

	// An explicit button beside the double-click: the grid gives no hint that
	// a swatch is editable at all, and at 14px a double-click is fiddly.
	imguibackend.EditorPushDisabled(ed.PrimaryIdx == 0)
	if imgui.Button("Edit Color...") {
		texEditorBeginPaletteEdit(ed.PrimaryIdx)
	}
	imguibackend.EditorPopDisabled()

	// OpenPopup has to happen out here, after the grid has popped its
	// per-swatch ID: a popup opened under one ID stack and begun under
	// another never appears, which is why the double-click did nothing.
	drawTexEditorPaletteColorPopup()

	imgui.Separator()
	if imgui.Button("Rebuild All Textures") {
		errs := texture.RebuildAllTextures()
		if len(errs) == 0 {
			texEditorStatus("Rebuilt all textures against the current palette")
		} else {
			texEditorStatus(fmt.Sprintf("Rebuilt with %d error(s): %v", len(errs), errs[0]))
		}
	}
}

// texEditorBeginPaletteEdit arms the colour popup for slot idx, snapshotting
// the colour it starts on so Revert/Cancel have something to go back to.
func texEditorBeginPaletteEdit(idx uint8) {
	if idx == 0 {
		// Slot 0 is the transparent slot by convention: it is what the eraser
		// writes and what every fresh layer is made of, so a document that
		// gave it a colour would come back with its erased areas painted in.
		texEditorStatus("Slot 0 is the transparent slot and can't be recoloured")
		return
	}
	texEditorPaletteEditIdx = idx
	texEditorPaletteEditOrig = texdoc.GlobalPalette[idx]
	texEditorPaletteEditCol = texEditorColorToVec4(texEditorPaletteEditOrig)
	texEditorPaletteEditOpen = true
}

// texEditorApplyPaletteColor writes c into the global palette and into the open
// document's own copy of it, in memory only. Indices never move on a palette
// edit, so no art has to be re-quantised — the next flatten picks the new
// colour up on its own.
func texEditorApplyPaletteColor(idx uint8, c color.RGBA) {
	texdoc.GlobalPalette[idx] = c
	if ed := ActiveTexEditor; ed != nil {
		// The document carries its own copy of the palette it was authored
		// against (see the mismatch dialog) — keep it in lockstep so editing a
		// slot doesn't immediately make the open document count as mismatched.
		ed.Doc.Pal[idx] = c
		ed.PreviewDirty = true
	}
}

// texEditorCommitPaletteColor persists the edited slot to textures/palette.pal.
func texEditorCommitPaletteColor() {
	idx := texEditorPaletteEditIdx
	c := texdoc.GlobalPalette[idx]
	if c == texEditorPaletteEditOrig {
		return
	}
	if err := texture.SetPaletteColor(idx, c); err != nil {
		texEditorStatus(fmt.Sprintf("Palette save failed: %v", err))
		return
	}
	texEditorStatus(fmt.Sprintf("Slot %d saved — run Rebuild All Textures to repaint art already on disk", idx))
}

func drawTexEditorPaletteColorPopup() {
	if texEditorPaletteEditOpen {
		imgui.OpenPopup("Edit Palette Color")
		texEditorPaletteEditOpen = false
		texEditorPaletteEditActive = true
	}
	if !imgui.BeginPopup("Edit Palette Color") {
		// Dismissed by clicking away rather than through a button: keep the
		// edit and write it out, the same way every other live-edit widget in
		// the editor commits on release.
		if texEditorPaletteEditActive {
			texEditorPaletteEditActive = false
			texEditorCommitPaletteColor()
		}
		return
	}
	idx := texEditorPaletteEditIdx
	imgui.Text(fmt.Sprintf("Palette slot %d", idx))
	if imgui.ColorPicker4V("##palcolor", &texEditorPaletteEditCol,
		imgui.ColorPickerFlagsAlphaBar|imgui.ColorPickerFlagsAlphaPreviewHalf|imgui.ColorPickerFlagsNoLabel) {
		texEditorApplyPaletteColor(idx, texEditorVec4ToColor(texEditorPaletteEditCol))
	}
	imgui.PushTextWrapPosV(imgui.CursorPosX() + 260)
	imgui.Text("The palette is global: this recolours every texel on this slot, in this document and in the world, as you drag. Art already saved to disk keeps the old colour until Rebuild All Textures.")
	imgui.PopTextWrapPos()
	if imgui.Button("Done##palcolor") {
		texEditorPaletteEditActive = false
		texEditorCommitPaletteColor()
		imgui.CloseCurrentPopup()
	}
	imgui.SameLine()
	if imgui.Button("Revert##palcolor") {
		texEditorApplyPaletteColor(idx, texEditorPaletteEditOrig)
		texEditorPaletteEditCol = texEditorColorToVec4(texEditorPaletteEditOrig)
	}
	imgui.SameLine()
	if imgui.Button("Cancel##palcolor") {
		texEditorApplyPaletteColor(idx, texEditorPaletteEditOrig)
		// Cleared before the close so the "dismissed by clicking away" branch
		// above doesn't then write the reverted colour back out.
		texEditorPaletteEditActive = false
		imgui.CloseCurrentPopup()
	}
	imgui.EndPopup()
}

func texEditorColorToVec4(c color.RGBA) [4]float32 {
	return [4]float32{float32(c.R) / 255, float32(c.G) / 255, float32(c.B) / 255, float32(c.A) / 255}
}

func texEditorVec4ToColor(v [4]float32) color.RGBA {
	q := func(f float32) uint8 { return uint8(geom.Clamp01(f)*255 + 0.5) }
	return color.RGBA{R: q(v[0]), G: q(v[1]), B: q(v[2]), A: q(v[3])}
}

// ---------- frames panel (animation stack) ----------

// drawFramesPanel draws the animation stack: one document, several frames,
// each frame its own layer stack — the layers panel below always shows the
// frame selected here. A single-frame document is simply not animated; the
// strip still draws (with just "1" in it and every frame-count button
// disabled) so turning a static texture into an animated one is just
// clicking Add Frame, not a separate mode to switch into first.
func drawFramesPanel() {
	ed := ActiveTexEditor
	d := ed.Doc
	n := d.FrameCount()
	active := d.ActiveFrameIndex()

	imgui.Text(fmt.Sprintf("Frames (%d/%d)", n, texdoc.TexMaxFrames))
	if fps := d.FPS(); fps > 0 {
		imgui.SameLine()
		imgui.Text(fmt.Sprintf("— animated, %.0f fps (one full cycle per second)", fps))
	}

	imguibackend.EditorPushDisabled(n >= texdoc.TexMaxFrames)
	if imgui.Button("Add Frame") {
		if err := ed.AddFrame(); err != nil {
			texEditorStatus(err.Error())
		}
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	imguibackend.EditorPushDisabled(n >= texdoc.TexMaxFrames)
	if imgui.Button("Duplicate Frame") {
		if err := ed.DuplicateFrame(active); err != nil {
			texEditorStatus(err.Error())
		}
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	imguibackend.EditorPushDisabled(n <= 1)
	if imgui.Button("Delete Frame") {
		if err := ed.DeleteFrame(active); err != nil {
			texEditorStatus(err.Error())
		}
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	imguibackend.EditorPushDisabled(active == 0)
	if imgui.Button("< Move##frameleft") {
		if err := ed.MoveFrame(active, false); err != nil {
			texEditorStatus(err.Error())
		}
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	imguibackend.EditorPushDisabled(active == n-1)
	if imgui.Button("Move >##frameright") {
		if err := ed.MoveFrame(active, true); err != nil {
			texEditorStatus(err.Error())
		}
	}
	imguibackend.EditorPopDisabled()

	// The strip itself: one selectable button per frame, wrapping to the
	// panel's actual width rather than a fixed count per row — the same
	// width-driven wrap the palette grid uses (drawPalettePanel).
	const btnW = float32(56)
	const spacing = float32(4)
	perRow := int((imgui.ContentRegionAvail().X + spacing) / (btnW + spacing))
	if perRow < 1 {
		perRow = 1
	}
	for i := 0; i < n; i++ {
		if i%perRow != 0 {
			imgui.SameLine()
		}
		imgui.PushID(fmt.Sprintf("texframe%d", i))
		selected := i == active
		if selected {
			imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.6, Z: 0.2, W: 1})
		}
		if imgui.ButtonV(fmt.Sprintf("%d", i+1), imgui.Vec2{X: btnW, Y: 0}) {
			if err := ed.SetActiveFrame(i); err != nil {
				texEditorStatus(err.Error())
			}
		}
		if selected {
			imgui.PopStyleColor()
		}
		imgui.PopID()
	}
}

// ---------- layers panel ----------

var texEditorBlendNames = []string{"Normal", "Multiply", "Add", "Screen"}

// texEditorLayerNoun names a new layer after the channel it lands on, so a
// document's two stacks don't both fill up with "Layer 3".
func texEditorLayerNoun(d *texdoc.TexDoc) string {
	if d.Channel() == texdoc.ChanBump {
		return "Height"
	}
	return "Layer"
}

func drawLayersPanel() {
	ed := ActiveTexEditor
	d := ed.Doc
	// Each channel has its own layer stack in each frame, so the heading has
	// to say which one is on screen — otherwise switching channels looks like
	// the layers were silently replaced.
	channel := "Color"
	if d.Channel() == texdoc.ChanBump {
		channel = "Bump"
	}
	if d.FrameCount() > 1 {
		imgui.Text(fmt.Sprintf("%s layers (%d/%d) — frame %d/%d", channel, len(d.Layers), texdoc.TexMaxLayers, d.ActiveFrameIndex()+1, d.FrameCount()))
	} else {
		imgui.Text(fmt.Sprintf("%s layers (%d/%d)", channel, len(d.Layers), texdoc.TexMaxLayers))
	}

	imguibackend.EditorPushDisabled(len(d.Layers) >= texdoc.TexMaxLayers)
	if imgui.Button("Add") {
		if err := ed.AddLayer(fmt.Sprintf("%s %d", texEditorLayerNoun(d), len(d.Layers)+1)); err != nil {
			texEditorStatus(err.Error())
		}
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	imguibackend.EditorPushDisabled(len(d.Layers) >= texdoc.TexMaxLayers)
	if imgui.Button("Duplicate") {
		if err := ed.DuplicateLayer(d.Active); err != nil {
			texEditorStatus(err.Error())
		}
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	imguibackend.EditorPushDisabled(len(d.Layers) <= 1)
	if imgui.Button("Delete") {
		if err := ed.DeleteLayer(d.Active); err != nil {
			texEditorStatus(err.Error())
		}
	}
	imguibackend.EditorPopDisabled()
	imguibackend.EditorPushDisabled(d.Active <= 0)
	if imgui.Button("Merge down") {
		if err := ed.MergeLayerDown(d.Active); err != nil {
			texEditorStatus(err.Error())
		}
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHoveredV(imgui.HoveredFlagsAllowWhenDisabled) {
		imgui.SetTooltip("Merge the selected layer into the one below it.")
	}
	imgui.SameLine()
	if imgui.Button("Import PNG...##layer") {
		texEditorShowLayerImport = true
	}
	if imgui.IsItemHoveredV(imgui.HoveredFlagsAllowWhenDisabled) {
		imgui.SetTooltip("Bring an image into a new layer, or over the selected one.")
	}
	drawTexEditorLayerImportDialog()
	imgui.Separator()

	// Bottom-first, matching storage order (index 0 = bottom).
	for i := range d.Layers {
		l := &d.Layers[i]
		imgui.PushID(fmt.Sprintf("texlayer%d", i))

		visible := l.Visible
		if imgui.Checkbox("##vis", &visible) {
			l.Visible = visible
			ed.PreviewDirty = true
			ed.BumpDirty = true
		}
		imgui.SameLine()
		selected := i == d.Active
		if imguibackend.SelectableV(l.Name, selected, 0, imgui.Vec2{X: 0, Y: 0}) {
			d.Active = i
		}

		imgui.SetNextItemWidth(160)
		if imgui.InputText("##name", &l.Name) {
			ed.Doc.MarkDirty()
		}

		opacity := l.Opacity
		imgui.SetNextItemWidth(160)
		if imgui.SliderFloat("Opacity##op", &opacity, 0, 1) {
			l.Opacity = opacity
			ed.PreviewDirty = true
			ed.BumpDirty = true
		}

		blend := int32(l.Blend)
		imgui.SetNextItemWidth(120)
		if imguibackend.Combo("Blend##bl", &blend, texEditorBlendNames) {
			l.Blend = texdoc.TexBlend(blend)
			ed.PreviewDirty = true
			ed.BumpDirty = true
		}

		imguibackend.EditorPushDisabled(i == len(d.Layers)-1)
		if imgui.Button("Up") {
			if err := ed.MoveLayer(i, true); err != nil {
				texEditorStatus(err.Error())
			}
		}
		imguibackend.EditorPopDisabled()
		imgui.SameLine()
		imguibackend.EditorPushDisabled(i == 0)
		if imgui.Button("Down") {
			if err := ed.MoveLayer(i, false); err != nil {
				texEditorStatus(err.Error())
			}
		}
		imguibackend.EditorPopDisabled()

		imgui.PopID()
		imgui.Separator()
	}
}

// ---------- canvas panel ----------

func drawCanvasPanel() {
	ed := ActiveTexEditor
	ensureTexEditorGLObjects()

	// Leave a row under the canvas for the size readout and the UV buttons
	// (Frame selection, Fill UV); a canvas taking the whole height pushes
	// them off the bottom of the panel.
	avail := imgui.ContentRegionAvail()
	avail.Y -= imgui.FrameHeightWithSpacing()
	if avail.X < 50 {
		avail.X = 50
	}
	if avail.Y < 50 {
		avail.Y = 50
	}
	origin := imgui.CursorScreenPos()

	zoom := texEditorZoom
	canvasW := float32(ed.Doc.W) * zoom
	canvasH := float32(ed.Doc.H) * zoom
	topLeft := imgui.Vec2{X: origin.X + texEditorPanX, Y: origin.Y + texEditorPanY}
	botRight := imgui.Vec2{X: topLeft.X + canvasW, Y: topLeft.Y + canvasH}

	dl := imgui.WindowDrawList()
	const squareTexels = float32(8)
	checkerUV := imgui.Vec2{X: float32(ed.Doc.W) / squareTexels / 2, Y: float32(ed.Doc.H) / squareTexels / 2}
	white := imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 1, Z: 1, W: 1})
	dl.AddImageV(imgui.TextureID(texEditorChecker), topLeft, botRight, imgui.Vec2{}, checkerUV, white)
	dl.AddImageV(imgui.TextureID(texEditorPreview), topLeft, botRight, imgui.Vec2{}, imgui.Vec2{X: 1, Y: 1}, white)

	drawTexEditorUVLayout(dl, topLeft, canvasW, canvasH)
	drawTexEditorUVSelection(dl, topLeft, canvasW, canvasH)
	drawTexEditorSelection(dl, ed, topLeft, zoom)

	// Live preview of an in-progress line/rect drag, in screen space only —
	// the document itself isn't touched until mouse-up (StampLine/StampRect
	// commit through the normal undo path there).
	if texEditorPainting && (ed.Tool == texture.ToolLine || ed.Tool == texture.ToolRect) {
		sx := topLeft.X + float32(texEditorDragStart[0])*zoom + zoom/2
		sy := topLeft.Y + float32(texEditorDragStart[1])*zoom + zoom/2
		mp := imgui.MousePos()
		yellow := imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 1, Z: 0, W: 1})
		if ed.Tool == texture.ToolLine {
			dl.AddLineV(imgui.Vec2{X: sx, Y: sy}, mp, yellow, 2)
		} else {
			dl.AddRectV(imgui.Vec2{X: sx, Y: sy}, mp, yellow, 0, 0, 2)
		}
	}

	imgui.InvisibleButton("##texcanvas", avail)
	hovered := imgui.IsItemHovered()
	active := imgui.IsItemActive()

	if hovered {
		_, wheelY := imgui.CurrentIO().MouseWheel()
		if wheelY != 0 {
			old := zoom
			zoom *= float32(math.Pow(1.15, float64(wheelY)))
			zoom = min(max(zoom, texEditorMinZoom), texEditorMaxZoom)
			mp := imgui.MousePos()
			localX := mp.X - topLeft.X
			localY := mp.Y - topLeft.Y
			texEditorPanX -= localX * (zoom/old - 1)
			texEditorPanY -= localY * (zoom/old - 1)
			texEditorZoom = zoom
		}
		if imgui.IsMouseDown(2) {
			d := imgui.CurrentIO().MouseDelta()
			texEditorPanX += d.X
			texEditorPanY += d.Y
		}
	}

	if ed.Tool.IsSelectTool() {
		// Unlike the paint tools, a selection gesture keeps its own last
		// position, so a release off the canvas finishes where the drag was
		// rather than snapping back to where it started.
		if hovered || active || texEditorPainting {
			mp := imgui.MousePos()
			texEditorHandleSelectInput(ed, (mp.X-topLeft.X)/zoom, (mp.Y-topLeft.Y)/zoom, zoom, active)
		}
	} else if hovered || active {
		mp := imgui.MousePos()
		tx := int(math.Floor(float64((mp.X - topLeft.X) / zoom)))
		ty := int(math.Floor(float64((mp.Y - topLeft.Y) / zoom)))
		texEditorHandlePaintInput(ed, tx, ty, active)
	} else if texEditorPainting {
		// Mouse left the canvas mid-gesture; treat like a release so the
		// stroke commits instead of leaving edits uncommitted to undo.
		texEditorHandlePaintInput(ed, texEditorDragStart[0], texEditorDragStart[1], false)
	}

	imgui.Text(fmt.Sprintf("%d x %d   zoom %.0f%%", ed.Doc.W, ed.Doc.H, zoom*100/8))

	// Only offered when there is a layout to show. A permanent checkbox that
	// did nothing on most textures would be worse than no checkbox.
	if n := len(UVGuideForTexture(ed.BoundTexture)); n > 0 {
		imgui.SameLine()
		imgui.Checkbox(fmt.Sprintf("UV layout (%d faces)##texuvlayout", n), &texEditorShowUVLayout)
	}
	if sel := UVSelectionForTexture(ed.BoundTexture); len(sel) > 0 {
		imgui.SameLine()
		if imgui.Button(fmt.Sprintf("Frame selection (%d)##texuvframe", len(sel))) {
			texEditorFrameUVs(sel, avail)
		}
		imgui.SameLine()
		if imgui.Button("Fill UV##texuvfill") {
			texEditorFillUVSelection(ed, sel)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Fill the selected faces' regions of the active layer\n" +
				"with the current colour.")
		}
	}
}

// texEditorFillUVSelection paints the islands of the faces selected in a
// viewport with the colour the tools are loaded with, on the active layer.
func texEditorFillUVSelection(ed *texture.TexEditorState, sel [][]mgl32.Vec2) {
	if ed.FillUVIslands(sel, ed.PaintIndex()) {
		texEditorStatus(fmt.Sprintf("Filled %d face(s)", len(sel)))
	} else {
		texEditorStatus("Fill UV: the selected faces already have that colour")
	}
}

// texEditorUVSelectionFill and texEditorUVSelectionEdge mark the islands of the
// faces selected in a viewport: the layout's cyan would not stand out from
// itself, so the selection takes the viewports' own selection amber.
var (
	texEditorUVSelectionFill = imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 0.75, Z: 0.1, W: 0.3})
	texEditorUVSelectionEdge = imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 0.75, Z: 0.1, W: 1})
)

// drawTexEditorUVSelection fills and outlines the UV island of every face
// selected in an open document that wears this texture, so selecting a face on
// the model shows at once which texels paint it.
//
// It ignores the "UV layout" checkbox on purpose: hiding the full layout to see
// the paint clearly should not also hide the one region being asked about. It
// is drawn in the layout's space (see drawTexEditorUVLayout) and, like it, never
// touches the document.
func drawTexEditorUVSelection(dl imgui.DrawList, topLeft imgui.Vec2, canvasW, canvasH float32) {
	ed := ActiveTexEditor
	if ed == nil {
		return
	}
	toScreen := func(p mgl32.Vec2) imgui.Vec2 {
		return imgui.Vec2{X: topLeft.X + p.X()*canvasW, Y: topLeft.Y + p.Y()*canvasH}
	}
	for _, loop := range UVSelectionForTexture(ed.BoundTexture) {
		for _, t := range uvLoopTriangles(loop) {
			dl.AddTriangleFilled(toScreen(loop[t[0]]), toScreen(loop[t[1]]), toScreen(loop[t[2]]), texEditorUVSelectionFill)
		}
		for i := range loop {
			dl.AddLineV(toScreen(loop[i]), toScreen(loop[(i+1)%len(loop)]), texEditorUVSelectionEdge, 2)
		}
	}
}

// uvLoopTriangles triangulates one face's UV loop for filling. A face may be
// concave, so a fan would spill outside it; EarClip wants counter-clockwise, and
// a face's winding in UV space depends on how the unwrap laid it down, so the
// loop is reversed when it arrives the other way round.
func uvLoopTriangles(loop []mgl32.Vec2) [][3]int {
	n := len(loop)
	if n < 3 {
		return nil
	}
	var area float32
	for i := range loop {
		a, b := loop[i], loop[(i+1)%n]
		area += a.X()*b.Y() - b.X()*a.Y()
	}
	poly, index := loop, func(i int) int { return i }
	if area < 0 {
		poly = make([]mgl32.Vec2, n)
		for i := range loop {
			poly[i] = loop[n-1-i]
		}
		index = func(i int) int { return n - 1 - i }
	}
	idx := geom.EarClip(poly)
	tris := make([][3]int, 0, len(idx)/3)
	for i := 0; i+2 < len(idx); i += 3 {
		tris = append(tris, [3]int{index(idx[i]), index(idx[i+1]), index(idx[i+2])})
	}
	return tris
}

// texEditorFrameUVs zooms and pans the canvas so the given UV islands fill the
// view with a margin — "take me to the face I selected". Zoom stays within the
// wheel's range so the next scroll does not jump.
func texEditorFrameUVs(loops [][]mgl32.Vec2, view imgui.Vec2) {
	ed := ActiveTexEditor
	if ed == nil || ed.Doc.W == 0 || ed.Doc.H == 0 {
		return
	}
	lo := mgl32.Vec2{float32(math.Inf(1)), float32(math.Inf(1))}
	hi := mgl32.Vec2{float32(math.Inf(-1)), float32(math.Inf(-1))}
	for _, loop := range loops {
		for _, p := range loop {
			lo = mgl32.Vec2{min(lo.X(), p.X()), min(lo.Y(), p.Y())}
			hi = mgl32.Vec2{max(hi.X(), p.X()), max(hi.Y(), p.Y())}
		}
	}
	if lo.X() > hi.X() {
		return
	}
	// Texels, with a quarter of the island's size (at least 2 texels) around it.
	w, h := float32(ed.Doc.W), float32(ed.Doc.H)
	x0, x1, y0, y1 := lo.X()*w, hi.X()*w, lo.Y()*h, hi.Y()*h
	padX := max((x1-x0)/4, 2)
	padY := max((y1-y0)/4, 2)
	x0, x1, y0, y1 = x0-padX, x1+padX, y0-padY, y1+padY

	zoom := min(view.X/(x1-x0), view.Y/(y1-y0))
	zoom = min(max(zoom, texEditorMinZoom), texEditorMaxZoom)
	texEditorZoom = zoom
	texEditorPanX = view.X/2 - (x0+x1)/2*zoom
	texEditorPanY = view.Y/2 - (y0+y1)/2*zoom
}

// texEditorUVLayoutColor is a translucent cyan: bright enough to read over any
// painted colour, transparent enough that it does not hide what is underneath.
// The overlay is a guide, not part of the image — it is drawn on the canvas and
// never into the document, so nothing here can be painted over or saved.
var texEditorUVLayoutColor = imgui.PackedColorFromVec4(imgui.Vec4{X: 0.2, Y: 0.9, Z: 1, W: 0.65})

// drawTexEditorUVLayout outlines the UV island of every open face — a model's
// or a brush's — that maps onto this texture.
//
// UV (0,0) is drawn at the canvas's top-left corner and V grows downwards,
// which is the same convention the sampler uses: the flattened image is
// uploaded with its top row first, so texture coordinate V=0 is that top row.
// The outline therefore lands on the texels the face actually samples.
func drawTexEditorUVLayout(dl imgui.DrawList, topLeft imgui.Vec2, canvasW, canvasH float32) {
	ed := ActiveTexEditor
	if !texEditorShowUVLayout || ed == nil {
		return
	}
	loops := UVGuideForTexture(ed.BoundTexture)
	if len(loops) == 0 {
		return
	}
	for _, loop := range loops {
		for i := range loop {
			a, b := loop[i], loop[(i+1)%len(loop)]
			dl.AddLineV(
				imgui.Vec2{X: topLeft.X + a.X()*canvasW, Y: topLeft.Y + a.Y()*canvasH},
				imgui.Vec2{X: topLeft.X + b.X()*canvasW, Y: topLeft.Y + b.Y()*canvasH},
				texEditorUVLayoutColor, 1)
		}
	}
}

// texEditorHandlePaintInput drives one tool's gesture state machine from the
// current texel under the cursor and the left mouse button's state.
func texEditorHandlePaintInput(ed *texture.TexEditorState, tx, ty int, active bool) {
	leftDown := imgui.IsMouseDown(0) && active

	// The tools write whatever the checked-out channel's picker is loaded
	// with: a palette index on the color channel, a height on the bump one.
	paintIdx := ed.PaintIndex()

	switch ed.Tool {
	case texture.ToolBrush, texture.ToolEraser, texture.ToolSpray:
		idx := paintIdx
		if ed.Tool == texture.ToolEraser {
			idx = 0
		}
		if leftDown {
			if !texEditorPainting {
				ed.BeginStroke()
				texEditorPainting = true
			}
			if ed.Tool == texture.ToolSpray {
				ed.StampSpray(tx, ty, idx)
			} else {
				ed.StampBrush(tx, ty, idx)
			}
		} else if texEditorPainting {
			ed.EndStroke()
			texEditorPainting = false
		}

	case texture.ToolFill:
		if leftDown && !texEditorPainting {
			ed.BeginStroke()
			ed.Fill(tx, ty, paintIdx)
			ed.EndStroke()
			texEditorPainting = true // one fill per mouse-down, not per frame
		} else if !leftDown {
			texEditorPainting = false
		}

	case texture.ToolLine, texture.ToolRect:
		if leftDown {
			if !texEditorPainting {
				texEditorDragStart = [2]int{tx, ty}
				texEditorPainting = true
			}
		} else if texEditorPainting {
			ed.BeginStroke()
			if ed.Tool == texture.ToolLine {
				ed.StampLine(texEditorDragStart[0], texEditorDragStart[1], tx, ty, paintIdx)
			} else {
				ed.StampRect(texEditorDragStart[0], texEditorDragStart[1], tx, ty, paintIdx, false)
			}
			ed.EndStroke()
			texEditorPainting = false
		}

	case texture.ToolEyedropper:
		if leftDown {
			if idx, ok := ed.Eyedropper(tx, ty); ok {
				ed.SetPaintIndex(idx)
			}
		}
	}
}

// drawTexEditorUnsavedDialog is the prompt texEditorConfirmDiscard puts up in
// front of anything that would drop the open document's unsaved changes.
//
// Save carries on only if the save worked; a texture never saved under a name
// goes through Save As first, and carries on from there. Discard reverts the
// texture on everything wearing it to its saved version. Cancel leaves the
// document, the window and the live texture exactly as they were.
func drawTexEditorUnsavedDialog() {
	imguibackend.WithModalPopup("Unsaved Changes##texunsaved", &texEditorShowUnsaved, func() {
		ed := ActiveTexEditor
		then := texEditorUnsavedThen
		if ed == nil || then == nil {
			texEditorUnsavedThen = nil
			imgui.CloseCurrentPopup()
			return
		}
		imgui.Text(fmt.Sprintf("Save changes to %s?", ed.Doc.Name))
		if ed.BoundTexture != "" {
			imgui.Text("Discarding puts the texture back as it was last saved\n" +
				"on everything that wears it.")
		}
		imgui.Separator()
		if imgui.Button("Save##texunsaved") {
			texEditorUnsavedThen = nil
			if ed.BoundTexture == "" {
				texEditorSaveAsThen = then
				texEditorDoSave() // opens Save As
			} else {
				texEditorDoSave()
				if !ed.Doc.Dirty() {
					then()
				}
			}
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Discard##texunsaved") {
			texEditorUnsavedThen = nil
			texEditorDiscardUnsaved()
			then()
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##texunsaved") {
			texEditorUnsavedThen = nil
			imgui.CloseCurrentPopup()
		}
	})
}
