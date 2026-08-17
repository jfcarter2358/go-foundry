package main

// texture_editor_ui.go — the texture editor's ImGui panels and its OS window.
//
// Entry point is OpenTextureEditor, wired to a "Textures" button in the main
// toolbar (editor_ui.go). Only one document is open at a time — see
// activeTexEditor — matching the plan's single texture-editor-window design.
//
// This file is the only GL-touching part of the texture editor beyond
// texture.go's upload queue: it owns a canvas preview texture and a small
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
)

var (
	activeTexEditor  *texEditorState
	texEditorWindow  *UIWindow
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
	texEditorPendingOpen      *texEditorState
	texEditorPaletteEditIdx   uint8
	texEditorStatusMsg        string
	texEditorStatusTime       uint32
)

var texEditorSizeOptions = []int32{64, 128, 256, 512, 1024}

func texEditorStatus(msg string) {
	texEditorStatusMsg = msg
	texEditorStatusTime = sdl.GetTicks()
}

// ---------- window lifecycle ----------

// OpenTextureEditor opens the texture editor window, creating it (and a
// blank starting document, if none is open yet) on first use, or just
// raising it if it's already open.
func OpenTextureEditor() {
	if texEditorWindow != nil {
		texEditorWindow.Win.Raise()
		return
	}
	w, err := NewUIWindow("Texture Editor", 1000, 700, drawTextureEditorUI, onTextureEditorClose)
	if err != nil {
		log.Printf("open texture editor: %v", err)
		return
	}
	texEditorWindow = w
	if activeTexEditor == nil {
		if ed, err := NewTexEditorDoc("untitled", 512, 512); err == nil {
			activeTexEditor = ed
		}
	} else {
		activeTexEditor.previewDirty = true
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

func texEditorAdoptDoc(ed *texEditorState) {
	activeTexEditor = ed
	ed.previewDirty = true
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
// at most once per frame, gated on previewDirty (set by any paint/structural
// mutation), so an idle frame (e.g. just panning) costs nothing.
func texEditorRefreshPreview() {
	ed := activeTexEditor
	if ed == nil || !ed.previewDirty {
		return
	}
	ensureTexEditorGLObjects()

	flat := ed.Doc.Flatten()
	rgba := rgbaFromPaletted(flat)
	gl.BindTexture(gl.TEXTURE_2D, texEditorPreview)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, int32(ed.Doc.W), int32(ed.Doc.H), 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(rgba.Pix))

	if ed.BoundTexture != "" {
		UpdateTextureHasAlpha(ed.BoundTexture, &ed.Doc.Pal, flat)
		QueueTextureUpload(ed.BoundTexture, rgba)
	}
	ed.previewDirty = false
}

// texEditorHandleShortcuts drives Ctrl+Z / Ctrl+Shift+Z / Ctrl+Y undo-redo for
// the active document. Gated on WantTextInput rather than the broader
// WantCaptureKeyboard (unlike the main editor's equivalent in editor.go) —
// this whole window is built out of imgui widgets, so WantCaptureKeyboard is
// true almost all the time; WantTextInput narrows it to "a text field (layer
// name, texture name, ...) actually has focus", which is the only case where
// Ctrl+Z should be left to the OS/imgui text-edit undo instead.
func texEditorHandleShortcuts() {
	ed := activeTexEditor
	if ed == nil {
		return
	}
	io := imgui.CurrentIO()
	if io.WantTextInput() {
		return
	}
	if !io.KeyCtrlPressed() {
		return
	}
	switch {
	case imgui.IsKeyPressedV(imgui.KeyZ, false) && io.KeyShiftPressed():
		ed.Redo()
	case imgui.IsKeyPressedV(imgui.KeyZ, false):
		ed.Undo()
	case imgui.IsKeyPressedV(imgui.KeyY, false):
		ed.Redo()
	}
}

// ---------- top-level layout ----------

func drawTextureEditorUI() {
	w := texEditorWindow
	if w == nil {
		return
	}
	texEditorRefreshPreview()
	texEditorHandleShortcuts()

	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: 0})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W), Y: float32(w.H)})
	imgui.BeginV("##texeditor_root", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|imgui.WindowFlagsNoMove|imgui.WindowFlagsNoCollapse)

	drawTextureEditorMenuBar()
	imgui.Separator()

	if activeTexEditor == nil {
		imgui.Text("No document open — use New, Open, or Import PNG above.")
		imgui.End()
		return
	}

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
	ed := activeTexEditor

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

	editorPushDisabled(ed == nil)
	if imgui.Button("Save") {
		texEditorDoSave()
	}
	editorPopDisabled()
	imgui.SameLine()

	editorPushDisabled(ed == nil)
	if imgui.Button("Save As...") {
		if ed != nil {
			texEditorSaveAsName = ed.Doc.Name
		}
		texEditorShowSaveAsDialog = true
	}
	editorPopDisabled()
	imgui.SameLine()

	editorPushDisabled(ed == nil || !ed.CanUndo())
	if imgui.Button("Undo") {
		ed.Undo()
	}
	editorPopDisabled()
	imgui.SameLine()

	editorPushDisabled(ed == nil || !ed.CanRedo())
	if imgui.Button("Redo") {
		ed.Redo()
	}
	editorPopDisabled()
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
	ed := activeTexEditor
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
	ed, err := OpenTexEditorDoc(name)
	if err != nil {
		texEditorStatus(fmt.Sprintf("Open failed: %v", err))
		return
	}
	if PaletteMismatch(ed.Doc) {
		texEditorPendingOpen = ed
		texEditorShowMismatch = true
		return
	}
	texEditorAdoptDoc(ed)
}

func drawTexEditorNewDialog() {
	withModalPopup("New Texture", &texEditorShowNewDialog, func() {
		imgui.Text("Name:")
		imgui.SetNextItemWidth(200)
		imgui.InputText("##newtexname", &texEditorNewName)
		imgui.Text("Size (square):")
		sizeLabels := make([]string, len(texEditorSizeOptions))
		for i, s := range texEditorSizeOptions {
			sizeLabels[i] = fmt.Sprintf("%dx%d", s, s)
		}
		imgui.SetNextItemWidth(150)
		imgui.Combo("##newtexsize", &texEditorNewSizeIdx, sizeLabels)
		if imgui.Button("Create") && texEditorNewName != "" {
			sz := int(texEditorSizeOptions[texEditorNewSizeIdx])
			ed, err := NewTexEditorDoc(texEditorNewName, sz, sz)
			if err != nil {
				texEditorStatus(fmt.Sprintf("New failed: %v", err))
			} else {
				texEditorAdoptDoc(ed)
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
	withModalPopup("Open Texture", &texEditorShowOpenDialog, func() {
		names := ScanFtexFiles()
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
	withModalPopup("Import PNG", &texEditorShowImportDialog, func() {
		names := make([]string, 0, len(textureNames))
		for n := range textureNames {
			names = append(names, n)
		}
		sort.Strings(names)
		imgui.Text("Import as a new single-layer document (quantised to the current palette):")
		for _, n := range names {
			if imgui.SelectableV(n, false, 0, imgui.Vec2{}) {
				path := filepath.Join(executablePath, textureNames[n])
				ed, err := ImportTexEditorDoc(path)
				if err != nil {
					texEditorStatus(fmt.Sprintf("Import failed: %v", err))
				} else {
					texEditorAdoptDoc(ed)
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
	withModalPopup("Save Texture As", &texEditorShowSaveAsDialog, func() {
		imgui.SetNextItemWidth(200)
		imgui.InputText("##saveastexname", &texEditorSaveAsName)
		if imgui.Button("Save##saveastex") && texEditorSaveAsName != "" {
			if ed := activeTexEditor; ed != nil {
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
	withModalPopup("Palette Mismatch", &texEditorShowMismatch, func() {
		imgui.Text("This file's embedded palette differs from the current global palette.")
		if imgui.Button("Adopt file's palette globally") {
			if err := AdoptDocPaletteGlobally(texEditorPendingOpen.Doc); err != nil {
				texEditorStatus(err.Error())
			}
			texEditorAdoptDoc(texEditorPendingOpen)
			texEditorPendingOpen = nil
			imgui.CloseCurrentPopup()
		}
		if imgui.Button("Remap art to current palette") {
			RemapDocToGlobalPalette(texEditorPendingOpen.Doc)
			texEditorAdoptDoc(texEditorPendingOpen)
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
	ed := activeTexEditor
	imgui.Text("Tools")
	for i, name := range texEditorToolNames {
		tool := TexTool(i)
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
	imgui.Text("Brush size (no hardness — quantisation would turn a soft edge into speckle):")
	imgui.SetNextItemWidth(160)
	if imgui.SliderInt("##brushsize", &brushSize, 1, 32) {
		ed.BrushSize = int(brushSize)
	}

	if ed.Tool == ToolSpray {
		density := int32(ed.SprayDensity)
		imgui.Text("Spray density (dabs/frame while held):")
		imgui.SetNextItemWidth(160)
		if imgui.SliderInt("##spraydensity", &density, 1, 64) {
			ed.SprayDensity = int(density)
		}
	}
}

// ---------- palette panel ----------

func drawPalettePanel() {
	ed := activeTexEditor
	imgui.Text("Palette")

	const cell = float32(14)
	for i := 0; i < texPaletteSz; i++ {
		if i%16 != 0 {
			imgui.SameLine()
		}
		c := globalPalette[i]
		imgui.PushID(fmt.Sprintf("palslot%d", i))
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{
			X: float32(c.R) / 255, Y: float32(c.G) / 255, Z: float32(c.B) / 255, W: float32(c.A) / 255,
		})
		if imgui.ButtonV("##swatch", imgui.Vec2{X: cell, Y: cell}) {
			ed.PrimaryIdx = uint8(i)
		}
		if imgui.IsItemHovered() && imgui.IsMouseClicked(1) {
			ed.SecondaryIdx = uint8(i)
		}
		if imgui.IsItemHovered() && imgui.IsMouseDoubleClicked(0) {
			texEditorPaletteEditIdx = uint8(i)
			imgui.OpenPopup("Edit Palette Color")
		}
		imgui.PopStyleColor()
		imgui.PopID()
	}

	imgui.Text(fmt.Sprintf("Primary: %d   Secondary: %d", ed.PrimaryIdx, ed.SecondaryIdx))

	if imgui.BeginPopup("Edit Palette Color") {
		idx := texEditorPaletteEditIdx
		c := globalPalette[idx]
		col := [4]float32{float32(c.R) / 255, float32(c.G) / 255, float32(c.B) / 255, float32(c.A) / 255}
		imgui.Text(fmt.Sprintf("Slot %d", idx))
		if imgui.ColorEdit4("Color", &col) {
			newC := color.RGBA{
				R: uint8(clamp01(col[0]) * 255), G: uint8(clamp01(col[1]) * 255),
				B: uint8(clamp01(col[2]) * 255), A: uint8(clamp01(col[3]) * 255),
			}
			if err := SetPaletteColor(idx, newC); err != nil {
				texEditorStatus(err.Error())
			}
			// Live-doc palette carries its own copy (see the palette-mismatch
			// flow) — keep it in lockstep with the global one it was authored
			// against so this document doesn't immediately count as mismatched.
			if ed != nil {
				ed.Doc.Pal[idx] = newC
				ed.previewDirty = true
			}
		}
		imgui.EndPopup()
	}

	imgui.Separator()
	if imgui.Button("Rebuild All Textures") {
		errs := RebuildAllTextures()
		if len(errs) == 0 {
			texEditorStatus("Rebuilt all textures against the current palette")
		} else {
			texEditorStatus(fmt.Sprintf("Rebuilt with %d error(s): %v", len(errs), errs[0]))
		}
	}
}

// ---------- layers panel ----------

var texEditorBlendNames = []string{"Normal", "Multiply", "Add", "Screen"}

func drawLayersPanel() {
	ed := activeTexEditor
	d := ed.Doc
	imgui.Text(fmt.Sprintf("Layers (%d/%d)", len(d.Layers), texMaxLayers))

	editorPushDisabled(len(d.Layers) >= texMaxLayers)
	if imgui.Button("Add") {
		if err := ed.AddLayer(fmt.Sprintf("Layer %d", len(d.Layers)+1)); err != nil {
			texEditorStatus(err.Error())
		}
	}
	editorPopDisabled()
	imgui.SameLine()
	editorPushDisabled(len(d.Layers) >= texMaxLayers)
	if imgui.Button("Duplicate") {
		if err := ed.DuplicateLayer(d.Active); err != nil {
			texEditorStatus(err.Error())
		}
	}
	editorPopDisabled()
	imgui.SameLine()
	editorPushDisabled(len(d.Layers) <= 1)
	if imgui.Button("Delete") {
		if err := ed.DeleteLayer(d.Active); err != nil {
			texEditorStatus(err.Error())
		}
	}
	editorPopDisabled()
	imgui.Separator()

	// Bottom-first, matching storage order (index 0 = bottom).
	for i := range d.Layers {
		l := &d.Layers[i]
		imgui.PushID(fmt.Sprintf("texlayer%d", i))

		visible := l.Visible
		if imgui.Checkbox("##vis", &visible) {
			l.Visible = visible
			ed.previewDirty = true
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
			ed.previewDirty = true
		}

		blend := int32(l.Blend)
		imgui.SetNextItemWidth(120)
		if imgui.Combo("Blend##bl", &blend, texEditorBlendNames) {
			l.Blend = TexBlend(blend)
			ed.previewDirty = true
		}

		editorPushDisabled(i == len(d.Layers)-1)
		if imgui.Button("Up") {
			if err := ed.MoveLayer(i, true); err != nil {
				texEditorStatus(err.Error())
			}
		}
		editorPopDisabled()
		imgui.SameLine()
		editorPushDisabled(i == 0)
		if imgui.Button("Down") {
			if err := ed.MoveLayer(i, false); err != nil {
				texEditorStatus(err.Error())
			}
		}
		editorPopDisabled()

		imgui.PopID()
		imgui.Separator()
	}
}

// ---------- canvas panel ----------

func drawCanvasPanel() {
	ed := activeTexEditor
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

	// Live preview of an in-progress line/rect drag, in screen space only —
	// the document itself isn't touched until mouse-up (StampLine/StampRect
	// commit through the normal undo path there).
	if texEditorPainting && (ed.Tool == ToolLine || ed.Tool == ToolRect) {
		sx := topLeft.X + float32(texEditorDragStart[0])*zoom + zoom/2
		sy := topLeft.Y + float32(texEditorDragStart[1])*zoom + zoom/2
		mp := imgui.MousePos()
		yellow := imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 1, Z: 0, W: 1})
		if ed.Tool == ToolLine {
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
}

// texEditorHandlePaintInput drives one tool's gesture state machine from the
// current texel under the cursor and the left mouse button's state.
func texEditorHandlePaintInput(ed *texEditorState, tx, ty int, active bool) {
	leftDown := imgui.IsMouseDown(0) && active

	switch ed.Tool {
	case ToolBrush, ToolEraser, ToolSpray:
		idx := ed.PrimaryIdx
		if ed.Tool == ToolEraser {
			idx = 0
		}
		if leftDown {
			if !texEditorPainting {
				ed.BeginStroke()
				texEditorPainting = true
			}
			if ed.Tool == ToolSpray {
				ed.StampSpray(tx, ty, idx)
			} else {
				ed.StampBrush(tx, ty, idx)
			}
		} else if texEditorPainting {
			ed.EndStroke()
			texEditorPainting = false
		}

	case ToolFill:
		if leftDown && !texEditorPainting {
			ed.BeginStroke()
			ed.Fill(tx, ty, ed.PrimaryIdx)
			ed.EndStroke()
			texEditorPainting = true // one fill per mouse-down, not per frame
		} else if !leftDown {
			texEditorPainting = false
		}

	case ToolLine, ToolRect:
		if leftDown {
			if !texEditorPainting {
				texEditorDragStart = [2]int{tx, ty}
				texEditorPainting = true
			}
		} else if texEditorPainting {
			ed.BeginStroke()
			if ed.Tool == ToolLine {
				ed.StampLine(texEditorDragStart[0], texEditorDragStart[1], tx, ty, ed.PrimaryIdx)
			} else {
				ed.StampRect(texEditorDragStart[0], texEditorDragStart[1], tx, ty, ed.PrimaryIdx, false)
			}
			ed.EndStroke()
			texEditorPainting = false
		}

	case ToolEyedropper:
		if leftDown {
			if idx, ok := ed.Eyedropper(tx, ty); ok {
				ed.PrimaryIdx = idx
			}
		}
	}
}
