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

	"github.com/go-gl/gl/v4.1-core/gl"
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

// Paint-input state, tracked across frames while a gesture is in progress.
var (
	texEditorPainting  bool
	texEditorDragStart [2]int
)

// texEditorShowUVLayout draws the open model's UV islands over the canvas, so a
// texture unwrapped from a mesh can be painted region by region instead of
// blind. On by default: the overlay only appears at all when the model editor
// happens to be open on a model wearing *this* texture, which is precisely the
// case where it is wanted.
var texEditorShowUVLayout = true

// Dialog state.
var (
	texEditorShowNewDialog    bool
	texEditorNewName                = "untitled"
	texEditorNewSizeIdx       int32 = 3 // index into texEditorSizeOptions; 512
	texEditorShowOpenDialog   bool
	texEditorShowImportDialog bool
	texEditorShowSaveAsDialog bool
	texEditorSaveAsName       string
	texEditorShowMismatch     bool
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

var TexEditorSizeOptions = []int32{64, 128, 256, 512, 1024}

func texEditorStatus(msg string) {
	texEditorStatusMsg = msg
	texEditorStatusTime = sdl.GetTicks()
}

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
// current (see CloseUIWindow) so it can free its own preview textures. The
// document itself is kept in memory — closing the window doesn't discard
// unsaved work, matching "closing hides the window and keeps the document in
// memory" from the plan.
func onTextureEditorClose() {
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

func TexEditorAdoptDoc(ed *texture.TexEditorState) {
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
	gl.BindTexture(gl.TEXTURE_2D, texEditorPreview)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, int32(ed.Doc.W), int32(ed.Doc.H), 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(rgba.Pix))

	if ed.BoundTexture != "" {
		gfx.UpdateTextureHasAlpha(ed.BoundTexture, &ed.Doc.Pal, flat)
		gfx.QueueTextureUpload(ed.BoundTexture, rgba)
		gfx.TouchAnimatedTexture(ed.BoundTexture)
	}
	ed.PreviewDirty = false
}

// ---------- keyboard shortcuts ----------

// texEditorShortcut names the action a key chord maps to in this window.
type texEditorShortcut int

const (
	texShortcutNone texEditorShortcut = iota
	texShortcutUndo
	texShortcutRedo
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
	if !ctrl || typing {
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
	switch texEditorShortcutFor(t.Keysym.Scancode,
		mods&sdl.KMOD_CTRL != 0, mods&sdl.KMOD_SHIFT != 0, imgui.CurrentIO().WantTextInput()) {
	case texShortcutUndo:
		// An undo mid-drag would restore texels the still-open stroke is about
		// to snapshot over, so close the gesture first.
		texEditorFinishStroke(ed)
		ed.Undo()
	case texShortcutRedo:
		texEditorFinishStroke(ed)
		ed.Redo()
	}
}

// texEditorFinishStroke commits an in-progress paint gesture, if any, so an
// undo taken while the mouse is still down operates on a settled document.
func texEditorFinishStroke(ed *texture.TexEditorState) {
	if !texEditorPainting {
		return
	}
	ed.EndStroke()
	texEditorPainting = false
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
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|imgui.WindowFlagsNoMove|imgui.WindowFlagsNoCollapse)

	drawTextureEditorMenuBar()
	imgui.Separator()

	if ActiveTexEditor == nil {
		imgui.Text("No document open — use New, Open, or Import PNG above.")
		imgui.End()
		return
	}

	imgui.BeginChildV("##texeditor_frames", imgui.Vec2{X: 0, Y: 110}, true, 0)
	drawFramesPanel()
	imgui.EndChild()

	imgui.BeginChildV("##texeditor_left", imgui.Vec2{X: 200, Y: 0}, true, 0)
	drawToolsPanel()
	imgui.Separator()
	drawPalettePanel()
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

func drawTextureEditorMenuBar() {
	ed := ActiveTexEditor

	if imgui.Button("New") {
		texEditorNewName = "untitled"
		texEditorNewSizeIdx = 3
		texEditorShowNewDialog = true
	}
	imgui.SameLine()
	if imgui.Button("Open...") {
		texEditorShowOpenDialog = true
	}
	imgui.SameLine()
	if imgui.Button("Import PNG...") {
		texEditorShowImportDialog = true
	}
	imgui.SameLine()

	imguibackend.EditorPushDisabled(ed == nil)
	if imgui.Button("Save") {
		texEditorDoSave()
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()

	imguibackend.EditorPushDisabled(ed == nil)
	if imgui.Button("Save As...") {
		if ed != nil {
			texEditorSaveAsName = ed.Doc.Name
		}
		texEditorShowSaveAsDialog = true
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()

	imguibackend.EditorPushDisabled(ed == nil || !ed.CanUndo())
	if imgui.Button("Undo") {
		ed.Undo()
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()

	imguibackend.EditorPushDisabled(ed == nil || !ed.CanRedo())
	if imgui.Button("Redo") {
		ed.Redo()
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()

	if ed != nil {
		title := ed.Doc.Name
		if ed.Doc.Dirty() {
			title += " *"
		}
		imgui.Text(title)
		imgui.SameLine()
	}
	if texEditorStatusMsg != "" {
		if sdl.GetTicks()-texEditorStatusTime < 3000 {
			imgui.Text(texEditorStatusMsg)
		} else {
			texEditorStatusMsg = ""
		}
	}

	drawTexEditorNewDialog()
	drawTexEditorOpenDialog()
	drawTexEditorImportDialog()
	drawTexEditorSaveAsDialog()
	drawTexEditorMismatchDialog()
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
		imgui.Combo("##newtexsize", &texEditorNewSizeIdx, sizeLabels)
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
			if imgui.SelectableV(n, false, 0, imgui.Vec2{}) {
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
			if imgui.SelectableV(n, false, 0, imgui.Vec2{}) {
				path := filepath.Join(fileutil.ExecutablePath, gfx.TextureNames[n])
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
				}
			}
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##saveastex") {
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

var texEditorToolNames = []string{"Brush", "Eraser", "Fill", "Line", "Rect", "Eyedropper", "Spray"}

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

func drawLayersPanel() {
	ed := ActiveTexEditor
	d := ed.Doc
	if d.FrameCount() > 1 {
		imgui.Text(fmt.Sprintf("Layers (%d/%d) — frame %d/%d", len(d.Layers), texdoc.TexMaxLayers, d.ActiveFrameIndex()+1, d.FrameCount()))
	} else {
		imgui.Text(fmt.Sprintf("Layers (%d/%d)", len(d.Layers), texdoc.TexMaxLayers))
	}

	imguibackend.EditorPushDisabled(len(d.Layers) >= texdoc.TexMaxLayers)
	if imgui.Button("Add") {
		if err := ed.AddLayer(fmt.Sprintf("Layer %d", len(d.Layers)+1)); err != nil {
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
	imgui.Separator()

	// Bottom-first, matching storage order (index 0 = bottom).
	for i := range d.Layers {
		l := &d.Layers[i]
		imgui.PushID(fmt.Sprintf("texlayer%d", i))

		visible := l.Visible
		if imgui.Checkbox("##vis", &visible) {
			l.Visible = visible
			ed.PreviewDirty = true
		}
		imgui.SameLine()
		selected := i == d.Active
		if imgui.SelectableV(l.Name, selected, 0, imgui.Vec2{X: 0, Y: 0}) {
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
		}

		blend := int32(l.Blend)
		imgui.SetNextItemWidth(120)
		if imgui.Combo("Blend##bl", &blend, texEditorBlendNames) {
			l.Blend = texdoc.TexBlend(blend)
			ed.PreviewDirty = true
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

	avail := imgui.ContentRegionAvail()
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
			if zoom < 0.25 {
				zoom = 0.25
			}
			if zoom > 64 {
				zoom = 64
			}
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

	if hovered || active {
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
}

// texEditorUVLayoutColor is a translucent cyan: bright enough to read over any
// painted colour, transparent enough that it does not hide what is underneath.
// The overlay is a guide, not part of the image — it is drawn on the canvas and
// never into the document, so nothing here can be painted over or saved.
var texEditorUVLayoutColor = imgui.PackedColorFromVec4(imgui.Vec4{X: 0.2, Y: 0.9, Z: 1, W: 0.65})

// drawTexEditorUVLayout outlines the UV island of every face the open model
// maps onto this texture.
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

	switch ed.Tool {
	case texture.ToolBrush, texture.ToolEraser, texture.ToolSpray:
		idx := ed.PrimaryIdx
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
			ed.Fill(tx, ty, ed.PrimaryIdx)
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
				ed.StampLine(texEditorDragStart[0], texEditorDragStart[1], tx, ty, ed.PrimaryIdx)
			} else {
				ed.StampRect(texEditorDragStart[0], texEditorDragStart[1], tx, ty, ed.PrimaryIdx, false)
			}
			ed.EndStroke()
			texEditorPainting = false
		}

	case texture.ToolEyedropper:
		if leftDown {
			if idx, ok := ed.Eyedropper(tx, ty); ok {
				ed.PrimaryIdx = idx
			}
		}
	}
}
