package editor

// editor_ui_panel.go — the UI editor's imgui chrome: toolbar, hierarchy tree,
// properties panel and file dialogs.
//
// Absolutely-positioned borderless windows against the OS window's size, no
// docking — the layout every other document editor uses, so the three read as
// one tool. Properties follow SETTINGS_LAYOUT.md's ordering: Identity,
// Transform, then whatever is specific to the kind, then Scripting, then
// Actions.
//
// Every widget that can be dragged goes through uiBeginEdit, so one gesture is
// one undo entry rather than one per frame.

import (
	"fmt"
	"path/filepath"
	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/ui"
	"foundry/internal/uifont"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// Dialog flags.
var (
	uiShowNewDialog    bool
	uiShowOpenDialog   bool
	uiShowSaveAsDialog bool
	uiSaveAsName       string
	uiNewName          string
)

// uiRequestSave saves, falling through to Save As when the document has never
// been written — inventing a filename for someone is worse than asking.
func uiRequestSave(forceDialog bool) {
	if uiDoc == nil {
		return
	}
	if forceDialog || uiDoc.Path == "" {
		uiSaveAsName = uiScreenName()
		uiShowSaveAsDialog = true
		uiFileList = nil
		return
	}
	if err := uiSave(uiDoc.Path); err != nil {
		uiStatus = "Save failed: " + err.Error()
	}
}

// ---------- toolbar ----------

func uiEditorToolbar(w *uiwin.UIWindow) {
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: 0})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W), Y: uiToolbarH})
	imgui.BeginV("##uitoolbar", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|imgui.WindowFlagsNoMove|
			imgui.WindowFlagsNoScrollbar|imgui.WindowFlagsNoSavedSettings)

	if imgui.Button("New") {
		uiNewName = "untitled"
		uiShowNewDialog = true
	}
	imgui.SameLine()
	if imgui.Button("Open") {
		uiShowOpenDialog = true
		uiFileList = nil
	}
	imgui.SameLine()
	if imgui.Button("Save") {
		uiRequestSave(false)
	}
	imgui.SameLine()
	if imgui.Button("Save As") {
		uiRequestSave(true)
	}

	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	imguibackend.EditorPushDisabled(!UICanUndo())
	if imgui.Button("Undo") {
		UIUndo()
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	imguibackend.EditorPushDisabled(!UICanRedo())
	if imgui.Button("Redo") {
		UIRedo()
	}
	imguibackend.EditorPopDisabled()

	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	name := uiScreenName()
	if uiDoc != nil && uiDoc.Dirty() {
		name += " *"
	}
	imgui.Text(name)

	// Pop In, right-aligned, matching the other document editors.
	imgui.SetCursorPos(imgui.Vec2{X: float32(w.W) - 90, Y: 6})
	if imgui.Button("Pop In") {
		CloseUIEditorWindow()
	}

	// Row two: view toggles, design resolution, sample data, status.
	imgui.SetCursorPos(imgui.Vec2{X: 8, Y: 30})
	imgui.Checkbox("Grid##uigrid", &uiShowGrid)
	imgui.SameLine()
	imgui.Checkbox("Outlines##uioutlines", &uiShowOutlines)
	imgui.SameLine()
	imgui.Checkbox("Safe area##uisafe", &uiShowSafeArea)

	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	uiResolutionCombo()

	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	if imgui.Button("Data: " + uiSampleModeNames[uiSampleData]) {
		uiSampleData = (uiSampleData + 1) % uiSampleMode(len(uiSampleModeNames))
		uiEditorInstallResolver()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("What bound widgets read.\n" +
			"Sample: invented values that move, so a fill direction is visible.\n" +
			"Literal: bindings off — every widget shows what the document says.\n" +
			"Live: the open play session, if there is one.")
	}

	if uiStatus != "" {
		imgui.SameLine()
		editorToolbarGroupSep()
		imgui.SameLine()
		editorTextMuted(uiStatus)
	}

	imgui.End()
}

// uiResolutions are the design sizes the canvas offers, plus Free.
var uiResolutions = []struct {
	Name string
	W, H float32
}{
	{"1920x1080", 1920, 1080},
	{"2560x1440", 2560, 1440},
	{"1366x768", 1366, 768},
	{"1280x720", 1280, 720},
	{"1080x1920", 1080, 1920},
}

func uiResolutionCombo() {
	label := "Free"
	if !uiFreeSize {
		label = fmt.Sprintf("%.0fx%.0f", uiDesignW, uiDesignH)
	}
	imgui.PushItemWidth(130)
	if imgui.BeginCombo("##uires", label) {
		for _, r := range uiResolutions {
			if imgui.SelectableV(r.Name, !uiFreeSize && uiDesignW == r.W && uiDesignH == r.H,
				0, imgui.Vec2{}) {
				uiDesignW, uiDesignH, uiFreeSize = r.W, r.H, false
				uiFrameCanvas()
			}
		}
		if imgui.SelectableV("Free", uiFreeSize, 0, imgui.Vec2{}) {
			uiFreeSize = true
			uiFrameCanvas()
		}
		imgui.EndCombo()
	}
	imgui.PopItemWidth()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The resolution the canvas lays out at.\n" +
			"Not this window's size: the numbers on the panel have to be\n" +
			"the numbers the game will use.")
	}
}

// ---------- hierarchy ----------

func uiEditorHierarchy(w *uiwin.UIWindow) {
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: uiToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: uiHierarchyWidth, Y: float32(w.H) - uiToolbarH})
	imgui.BeginV("Hierarchy##uihierarchy", nil,
		imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize|imgui.WindowFlagsNoSavedSettings)

	if imgui.Button("+ Add") {
		imgui.OpenPopup("##uiadd")
	}
	if imgui.BeginPopup("##uiadd") {
		for k := ui.KindPanel; int(k) < len(ui.KindNames); k++ {
			if imgui.MenuItem(ui.KindName(k)) {
				uiAddNode(k)
			}
		}
		imgui.EndPopup()
	}
	imgui.SameLine()
	imguibackend.EditorPushDisabled(uiSel == "" || uiDoc == nil || uiSel == uiDoc.Root.ID)
	if imgui.Button("^") {
		uiMoveSelection(-1)
	}
	imgui.SameLine()
	if imgui.Button("v") {
		uiMoveSelection(1)
	}
	imgui.SameLine()
	if imgui.Button("x") {
		uiDeleteSelection()
	}
	imguibackend.EditorPopDisabled()

	imgui.Separator()
	if uiDoc != nil {
		uiHierarchyNode(&uiDoc.Root)
	}
	imgui.End()
}

func uiHierarchyNode(n *ui.Node) {
	imgui.PushID(n.ID)
	defer imgui.PopID()

	// The eye, first, so the row's clickable label starts at a fixed offset.
	vis := n.Visible
	if imgui.Checkbox("##vis", &vis) && !n.FromInclude() {
		uiBeginEdit("hierarchy.visible")
		n.Visible = vis
		uiEdited()
	}
	imgui.SameLine()

	label := n.ID + "  " + ui.KindName(n.Kind)
	flags := imgui.TreeNodeFlagsOpenOnArrow | imgui.TreeNodeFlagsSpanAvailWidth |
		imgui.TreeNodeFlagsDefaultOpen
	if len(n.Children) == 0 {
		flags |= imgui.TreeNodeFlagsLeaf
	}
	if n.ID == uiSel {
		flags |= imgui.TreeNodeFlagsSelected
	}

	if n.FromInclude() {
		// Spliced nodes are shown so the tree matches what is on the canvas,
		// but muted and inert: editing one would write the change into a file
		// that is not the one being edited.
		editorTextMuted(label)
		for i := range n.Children {
			uiHierarchyNode(&n.Children[i])
		}
		return
	}

	open := imgui.TreeNodeV(label, flags)
	if imgui.IsItemClicked() {
		uiSel = n.ID
	}
	uiHierarchyDragDrop(n)
	uiHierarchyContextMenu(n)

	if open {
		for i := range n.Children {
			uiHierarchyNode(&n.Children[i])
		}
		imgui.TreePop()
	}
}

// uiHierarchyDragDrop lets a row be dropped onto another to reparent it.
func uiHierarchyDragDrop(n *ui.Node) {
	if uiDoc == nil || n.ID == uiDoc.Root.ID {
		// The root cannot be dragged; it can still be dropped onto.
		uiHierarchyDropTarget(n)
		return
	}
	if imgui.BeginDragDropSource(0) {
		uiDragPayload = n.ID
		imgui.SetDragDropPayload("uinode", []byte(n.ID), 0)
		imgui.Text(n.ID)
		imgui.EndDragDropSource()
	}
	uiHierarchyDropTarget(n)
}

// uiDragPayload carries the dragged node's ID. imgui's payload is a byte slice
// whose lifetime is imgui's; keeping our own copy avoids reasoning about that.
var uiDragPayload string

func uiHierarchyDropTarget(n *ui.Node) {
	if imgui.BeginDragDropTarget() {
		if imgui.AcceptDragDropPayload("uinode", 0) != nil && uiDragPayload != "" {
			uiReparent(uiDragPayload, n.ID)
			uiDragPayload = ""
		}
		imgui.EndDragDropTarget()
	}
}

func uiHierarchyContextMenu(n *ui.Node) {
	if !imgui.BeginPopupContextItemV("##uictx"+n.ID, 1) {
		return
	}
	uiSel = n.ID
	root := uiDoc != nil && n.ID == uiDoc.Root.ID
	if !root {
		if imgui.MenuItem("Duplicate") {
			uiDuplicateSelection()
		}
		if imgui.MenuItem("Move up") {
			uiMoveSelection(-1)
		}
		if imgui.MenuItem("Move down") {
			uiMoveSelection(1)
		}
		if imgui.MenuItem("Wrap in panel") {
			uiWrapInPanel()
		}
		imgui.Separator()
		if imgui.MenuItem("Delete") {
			uiDeleteSelection()
		}
		imgui.Separator()
	}
	if imgui.BeginMenu("Add child") {
		for k := ui.KindPanel; int(k) < len(ui.KindNames); k++ {
			if imgui.MenuItem(ui.KindName(k)) {
				uiSel = n.ID
				uiAddNode(k)
			}
		}
		imgui.EndMenu()
	}
	imgui.EndPopup()
}

// ---------- dialogs ----------

func uiEditorDialogs(w *uiwin.UIWindow) {
	imguibackend.WithModalPopup("New screen##uinew", &uiShowNewDialog, func() {
		imgui.InputText("Name##uinewname", &uiNewName)
		if imgui.Button("Create") && uiNewName != "" {
			uiAdoptDocument(ui.NewScreen(uiNewName))
			uiStatus = "New screen " + uiNewName
			uiShowNewDialog = false
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel") {
			uiShowNewDialog = false
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Open screen##uiopen", &uiShowOpenDialog, func() {
		if uiFileList == nil {
			uiFileList = ui.ScanScreenFiles()
		}
		if len(uiFileList) == 0 {
			editorTextMutedWrapped("No .fui files in resources/ui yet.")
		}
		for _, f := range uiFileList {
			if imgui.SelectableV(f, false, 0, imgui.Vec2{}) {
				if err := uiLoad(ui.UIPath(strings.TrimSuffix(f, ui.UIExt))); err != nil {
					uiStatus = "Open failed: " + err.Error()
				}
				uiShowOpenDialog = false
				imgui.CloseCurrentPopup()
			}
		}
		imgui.Separator()
		if imgui.Button("Cancel") {
			uiShowOpenDialog = false
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Save screen as##uisaveas", &uiShowSaveAsDialog, func() {
		imgui.InputText("Name##uisaveasname", &uiSaveAsName)
		editorTextMuted("Saved to " + filepath.Base(ui.UIDir()) + "/" + uiSaveAsName + ui.UIExt)
		if imgui.Button("Save") && uiSaveAsName != "" {
			if uiDoc != nil && uiDoc.ID == "untitled" {
				uiDoc.ID = uiSaveAsName
			}
			if err := uiSave(ui.UIPath(uiSaveAsName)); err != nil {
				uiStatus = "Save failed: " + err.Error()
			}
			uiShowSaveAsDialog = false
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel") {
			uiShowSaveAsDialog = false
			imgui.CloseCurrentPopup()
		}
	})
}

// uiFontCombo picks a font from resources/fonts, with an entry for "the
// theme's".
func uiFontCombo(label string, name *string) bool {
	changed := false
	shown := *name
	if shown == "" {
		shown = "(theme)"
	}
	if imgui.BeginCombo(label, shown) {
		if imgui.SelectableV("(theme)", *name == "", 0, imgui.Vec2{}) {
			*name, changed = "", true
		}
		for _, f := range uifont.ScanFontFiles() {
			n := strings.TrimSuffix(f, uifont.FontExt)
			if imgui.SelectableV(n, *name == n, 0, imgui.Vec2{}) {
				*name, changed = n, true
			}
		}
		imgui.EndCombo()
	}
	return changed
}
