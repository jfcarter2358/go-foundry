package editor

// editor_aitree_window.go — the AI editor, in its own OS window.
//
// It owns a document (*aitree.Doc) the way the particle and model editors own
// theirs: the main window carries on with the level, and a behaviour is drawn
// beside the level it is going into.
//
// The middle of this window is a graph canvas rather than a 3D view
// (editor_aitree_canvas.go), which makes it the one editor here that needs no
// camera, no picking ray and no GL objects of its own. What it does need, and
// what the other two do not, is to be *live*: the document is recompiled every
// frame it changes (aitree.Compile), so a mistyped expression is underlined as
// it is typed and a broken node is marked on the canvas rather than in a log
// after a playtest.
//
// Layout:
//
//	 ┌───────────────────────────────────────────────────────────┐
//	 │ File Edit View Tree Window │ Level Texture … AI     name  │  top bar
//	 ├──────────┬──────────────────────────────┬─────────────────┤
//	 │ trees    │                              │ the node's form │
//	 │ params   │          canvas              │                 │
//	 │ problems │                              │                 │
//	 └──────────┴──────────────────────────────┴─────────────────┘

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"foundry/internal/aitree"
	"foundry/internal/fileutil"
	"foundry/internal/imguibackend"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	aiWindowW = int32(1400)
	aiWindowH = int32(880)

	// The shared two-row top bar (editor_menubar.go).
	aiToolbarH = editorTopBarH

	// aiPanelWidth is the inspector on the right; aiListWidth the tree and
	// problem list on the left. The inspector is wider than the model
	// editor's because a node's form is labelled expressions, which need room
	// to be read rather than only edited.
	aiPanelWidth = float32(360)
	aiListWidth  = float32(230)
)

// aiEditorWindow is the pop-out; nil when the editor is closed.
var aiEditorWindow *uiwin.UIWindow

// aiDoc is the document being edited. It outlives the window, so popping the
// editor in and out returns to the same behaviour rather than a fresh one.
var aiDoc *aitree.Doc

// aiProg is aiDoc compiled. Recomputed whenever the document's Rev changes —
// see aiRefreshProgram — and never nil once there is a document.
var aiProg *aitree.Program

// aiProgRev is the Rev aiProg was built from.
var aiProgRev uint64

// aiTreeIndex is which of the document's trees is on the canvas.
var aiTreeIndex int

// aiSelection is the ids of the selected nodes in the current tree. A set
// rather than a slice: every hit test asks "is this one in it".
var aiSelection = map[string]bool{}

// aiStatus is the line under the toolbar.
var aiStatus string

// Dialog state.
var (
	aiShowOpenDialog   bool
	aiShowSaveAsDialog bool
	aiSaveAsName       string
	aiFileList         []string
)

// ---------- lifecycle ----------

// AIEditorOpen reports whether the window is up.
func AIEditorOpen() bool { return aiEditorWindow != nil }

// OpenAIEditorWindow opens the editor, or raises it if it is already up.
//
// It always has something to edit: with nothing loaded it starts on a working
// one-root document, the way the model editor starts on a cube rather than on a
// file dialog.
func OpenAIEditorWindow() {
	if aiEditorWindow != nil {
		aiEditorWindow.Win.Raise()
		return
	}
	aiEnsureDoc()
	w, err := uiwin.NewUIWindow("AI Editor", aiWindowW, aiWindowH, drawAIEditorWindow, onAIEditorClose)
	if err != nil {
		log.Printf("open AI editor window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0.09, 0.10, 0.12, 1}
	w.HandleEvent = aiEditorHandleEvent
	// Assigned last, on the particle editor's rule: until this is set,
	// AIEditorOpen has to keep saying no, or a Draw triggered during
	// construction would find a half-built window.
	aiEditorWindow = w
}

// OpenAIEditorOn opens the editor on a named document — the Edit button beside
// an entity's AI Tree field.
//
// Deferred through RequestWindowOpen because the caller is inside another
// window's Draw: creating a window there inserts into the map being ranged over
// and leaves the wrong GL context current.
func OpenAIEditorOn(name string) {
	if name == "" {
		return
	}
	uiwin.RequestWindowOpen(func() {
		if err := aiLoad(aitree.AIPath(name)); err != nil {
			log.Printf("AI editor: %v", err)
			editorSetStatus("Could not open %s: %v", name, err)
			return
		}
		OpenAIEditorWindow()
	})
}

// CloseAIEditorWindow asks for the window to go away at the end of the frame.
// Safe to call from inside its own Draw.
func CloseAIEditorWindow() {
	if aiEditorWindow != nil {
		aiEditorWindow.CloseRequested = true
	}
}

// onAIEditorClose runs with this window's GL context current. The document
// stays in memory: closing the window is not discarding the work.
func onAIEditorClose() { aiEditorWindow = nil }

// aiEditorFocused reports whether this window owns keyboard input.
// sdl.GetKeyboardState is process-global, so without this a keypress meant for
// the AI editor would also reach the main window's fly camera.
func aiEditorFocused() bool {
	return aiEditorWindow != nil &&
		aiEditorWindow.Win.GetFlags()&sdl.WINDOW_INPUT_FOCUS != 0
}

// aiEnsureDoc guarantees there is something to edit.
func aiEnsureDoc() {
	if aiDoc == nil {
		aiDoc = aitree.NewDoc("behaviour")
		aiTreeIndex = 0
		aiSelection = map[string]bool{}
		aiRefreshProgram()
		aiMarkSaved()
	}
}

// ---------- the document ----------

// aiRefreshProgram recompiles the document if it has changed since the last
// compile. Called once at the top of every frame's draw, which is what makes
// the canvas's markers and the inspector's messages this frame's answer.
//
// Compiling a document is parsing a few dozen short expressions; it is well
// under a frame even for a large behaviour, and gating it on Rev means a
// document nobody is editing costs one integer comparison.
func aiRefreshProgram() {
	if aiDoc == nil {
		aiProg = nil
		return
	}
	if aiProg != nil && aiProgRev == aiDoc.Rev {
		return
	}
	aiProg = aitree.Compile(aiDoc)
	aiProgRev = aiDoc.Rev
}

// aiCurrentTree is the tree on the canvas, or nil.
func aiCurrentTree() *aitree.Tree {
	if aiDoc == nil || aiTreeIndex < 0 || aiTreeIndex >= len(aiDoc.Trees) {
		return nil
	}
	return aiDoc.Trees[aiTreeIndex]
}

// aiSelectedNode is the one selected node, or nil when none or several are.
// The inspector edits one node; a multiple selection is for moving and
// deleting.
func aiSelectedNode() *aitree.Node {
	t := aiCurrentTree()
	if t == nil || len(aiSelection) != 1 {
		return nil
	}
	for id := range aiSelection {
		return t.Node(id)
	}
	return nil
}

// aiSelectOnly makes one node the whole selection.
func aiSelectOnly(id string) {
	aiSelection = map[string]bool{}
	if id != "" {
		aiSelection[id] = true
	}
}

// aiDocName is the document's name as an entity would spell it.
func aiDocName() string {
	if aiDoc == nil {
		return ""
	}
	if aiDoc.Path == "" {
		return aiDoc.ID
	}
	return aitree.AIName(aiDoc.Path)
}

// aiNewDoc starts a fresh document.
func aiNewDoc() {
	aiDoc = aitree.NewDoc("behaviour")
	aiTreeIndex = 0
	aiSelection = map[string]bool{}
	AIClearUndoHistory()
	aiResetView()
	aiRefreshProgram()
	aiMarkSaved()
	aiStatus = "New behaviour"
}

// aiLoad reads a document from disk and makes it the one being edited.
func aiLoad(path string) error {
	d, err := aitree.LoadAI(path)
	if err != nil {
		return err
	}
	aiDoc = d
	aiTreeIndex = 0
	aiSelection = map[string]bool{}
	AIClearUndoHistory()
	aiResetView()
	aiRefreshProgram()
	aiMarkSaved()
	aiStatus = "Opened " + filepath.Base(path)
	return nil
}

// aiSave writes the document and tells the rest of the editor, so an entity in
// the level being authored beside this window picks the change up.
func aiSave(path string) error {
	if aiDoc == nil {
		return nil
	}
	if err := aitree.SaveAI(aiDoc, path); err != nil {
		return err
	}
	aiDoc.Path = path
	aiMarkSaved()
	aiStatus = "Saved " + filepath.Base(path)
	// The pickers' caches are stale the moment a new file exists, or an old
	// one says something different.
	Editor.AITreeNames = nil
	aiSummaryCache = nil
	aiFileList = nil
	return nil
}

// aiSaveCurrent saves over the open file, or asks for a name when there is not
// one yet.
func aiSaveCurrent() {
	if aiDoc == nil {
		return
	}
	if aiDoc.Path == "" {
		aiSaveAsName = aiDoc.ID
		aiShowSaveAsDialog = true
		return
	}
	if err := aiSave(aiDoc.Path); err != nil {
		aiStatus = err.Error()
	}
}

// ---------- the frame ----------

func drawAIEditorWindow() {
	w := aiEditorWindow
	if w == nil {
		return
	}
	aiEnsureDoc()
	aiRefreshProgram()

	aiEditorToolbar()
	aiEditorListPanel()
	aiEditorCanvasWindow()
	aiEditorInspector()
	aiEditorDialogs()
}

// aiEditorToolbar is the shared two-row top bar.
func aiEditorToolbar() {
	w := aiEditorWindow
	if w == nil {
		return
	}
	editorTopBarBegin("##aitoolbar", float32(w.W))

	if imgui.BeginMenuBar() {
		aiEditorMenus()
		editorMenuBarShared(editorKindAI)
		name := aiDocName()
		if name == "" {
			name = "(unsaved)"
		}
		editorMenuBarDoc(name, aiEditorDirty())
		imgui.EndMenuBar()
	}

	// Second row: the handful of toggles worth one click, then the status.
	imgui.Checkbox("Snap##aisnap", &aiSnapToGrid)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Drop nodes on the grid.")
	}
	imgui.SameLine()
	imgui.Checkbox("Grid##aigrid", &aiShowGrid)
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	if imgui.Button("Tidy##aitidy") {
		aiTidyTree()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Lay this tree out from its root: each branch below the node it\n" +
			"comes from, `then` to the left and `else` to the right. What a node\n" +
			"links to is the document; where it sits is only the drawing, so this\n" +
			"can never change what the behaviour does.")
	}
	imgui.SameLine()
	if imgui.Button("Frame All##aiframe") {
		aiFrameAll()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Pan and zoom so the whole tree is on screen.")
	}
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	aiProblemSummary()
	editorStatusText(aiStatus)

	imgui.End()
}

// aiProblemSummary is the counter beside the toggles: how many nodes have
// something wrong with them, in red when any do.
func aiProblemSummary() {
	if aiProg == nil {
		return
	}
	errs, warns := 0, 0
	for _, d := range aiProg.Diags {
		if d.Warning {
			warns++
		} else {
			errs++
		}
	}
	switch {
	case errs > 0:
		imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 1, Y: 0.45, Z: 0.4, W: 1})
		imgui.Text(fmt.Sprintf("%d problem(s)", errs))
		imgui.PopStyleColor()
	case warns > 0:
		imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 0.95, Y: 0.8, Z: 0.35, W: 1})
		imgui.Text(fmt.Sprintf("%d warning(s)", warns))
		imgui.PopStyleColor()
	default:
		editorTextMuted("No problems")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Everything the document says that it should not. The list on the\nleft names each one; clicking it goes to the node.")
	}
}

// aiEditorMenus draws the AI editor's own menus.
func aiEditorMenus() {
	if imgui.BeginMenu("File") {
		if imgui.MenuItemV("New", "Ctrl+N", false, true) {
			aiNewDoc()
		}
		if imgui.MenuItemV("Open...", "Ctrl+O", false, true) {
			aiShowOpenDialog = true
			aiFileList = nil
		}
		imgui.Separator()
		if imgui.MenuItemV("Save", "Ctrl+S", false, true) {
			aiSaveCurrent()
		}
		if imgui.MenuItem("Save As...") {
			aiSaveAsName = aiDocName()
			aiShowSaveAsDialog = true
		}
		editorSaveAllMenuItem()
		editorCloseWindowItem(CloseAIEditorWindow)
		imgui.EndMenu()
	}

	if imgui.BeginMenu("Edit") {
		if imgui.MenuItemV("Undo", "Ctrl+Z", false, AICanUndo()) {
			AIUndo()
		}
		if imgui.MenuItemV("Redo", "Ctrl+Y", false, AICanRedo()) {
			AIRedo()
		}
		imgui.Separator()
		if imgui.MenuItemV("Duplicate", "Ctrl+D", false, len(aiSelection) > 0) {
			aiDuplicateSelection()
		}
		if imgui.MenuItemV("Delete", "Del", false, len(aiSelection) > 0) {
			aiDeleteSelection()
		}
		imgui.Separator()
		if imgui.MenuItemV("Select All", "Ctrl+A", false, aiCurrentTree() != nil) {
			aiSelectAll()
		}
		imgui.EndMenu()
	}

	if imgui.BeginMenu("View") {
		if imgui.MenuItemV("Grid", "", aiShowGrid, true) {
			aiShowGrid = !aiShowGrid
		}
		if imgui.MenuItemV("Snap to Grid", "", aiSnapToGrid, true) {
			aiSnapToGrid = !aiSnapToGrid
		}
		imgui.Separator()
		if imgui.MenuItemV("Frame All", "F", false, true) {
			aiFrameAll()
		}
		if imgui.MenuItemV("Reset Zoom", "", false, true) {
			aiZoom = 1
		}
		imgui.EndMenu()
	}

	if imgui.BeginMenu("Tree") {
		if imgui.MenuItem("Add Event Root...") {
			aiShowAddEventMenu = true
		}
		editorMenuTooltip("A tree fired when that message reaches the entity.")
		if imgui.MenuItem("Add Helper Tree") {
			aiAddTree(&aitree.Tree{Name: aiFreeHelperName(), Root: ""})
		}
		editorMenuTooltip("A question or a routine with a shape of its own, drawn once\nand run from anywhere with `run` or asked with the `ask` check.")
		imgui.Separator()
		if imgui.MenuItemV("Delete This Tree", "", false, aiCurrentTree() != nil) {
			aiDeleteCurrentTree()
		}
		imgui.Separator()
		if imgui.MenuItemV("Tidy Layout", "", false, aiCurrentTree() != nil) {
			aiTidyTree()
		}
		imgui.EndMenu()
	}
}

// ---------- dialogs ----------

func aiEditorDialogs() {
	imguibackend.WithModalPopup("Open Behaviour##aiopendlg", &aiShowOpenDialog, func() {
		if aiFileList == nil {
			aiFileList = aitree.ScanAIFiles()
		}
		if len(aiFileList) == 0 {
			editorTextMutedWrapped("Nothing in " + aitree.AIDirName + "/ yet. Save one first.")
		}
		for _, name := range aiFileList {
			if !imguibackend.Selectable(name) {
				continue
			}
			if err := aiLoad(aitree.AIPath(name)); err != nil {
				aiStatus = err.Error()
			}
			imgui.CloseCurrentPopup()
		}
		imgui.Separator()
		if imgui.Button("Cancel##aiopencancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Save Behaviour As##aisavedlg", &aiShowSaveAsDialog, func() {
		imgui.InputTextV("Name##aisavename", &aiSaveAsName, 0, nil)
		editorTextMutedWrapped("Written to " + aitree.AIDirName + "/<name>" + aitree.AIExt +
			"\nAn entity type reaches it with `ai_tree: <name>`.")
		if imgui.Button("Save##aisavego") && aiSaveAsName != "" {
			if aiDoc.ID == "" || aiDoc.ID == "behaviour" {
				aiDoc.ID = aiSaveAsName
			}
			if err := aiSave(aitree.AIPath(aiSaveAsName)); err != nil {
				aiStatus = err.Error()
			}
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##aisavecancel") {
			imgui.CloseCurrentPopup()
		}
	})

	aiAddEventDialog()
}

// ---------- input ----------

// aiShortcut is what a keypress means here. Returned as a value rather than
// acted on inline so the mapping can be unit-tested without a window — the
// arrangement particleShortcutFor uses.
type aiShortcut int

const (
	aiShortcutNone aiShortcut = iota
	aiShortcutSave
	aiShortcutOpen
	aiShortcutNew
	aiShortcutUndo
	aiShortcutRedo
	aiShortcutDuplicate
	aiShortcutSelectAll
	aiShortcutDelete
	aiShortcutFrameAll
)

// aiShortcutFor maps a keypress to an action.
func aiShortcutFor(sc sdl.Scancode, ctrl, shift bool) aiShortcut {
	if ctrl {
		switch sc {
		case sdl.SCANCODE_S:
			return aiShortcutSave
		case sdl.SCANCODE_O:
			return aiShortcutOpen
		case sdl.SCANCODE_N:
			return aiShortcutNew
		case sdl.SCANCODE_Z:
			// Ctrl+Shift+Z is redo, the way it is everywhere else here.
			if shift {
				return aiShortcutRedo
			}
			return aiShortcutUndo
		case sdl.SCANCODE_Y:
			return aiShortcutRedo
		case sdl.SCANCODE_D:
			return aiShortcutDuplicate
		case sdl.SCANCODE_A:
			return aiShortcutSelectAll
		}
		return aiShortcutNone
	}
	switch sc {
	case sdl.SCANCODE_DELETE, sdl.SCANCODE_BACKSPACE:
		return aiShortcutDelete
	case sdl.SCANCODE_F:
		return aiShortcutFrameAll
	}
	return aiShortcutNone
}

// aiEditorHandleEvent is the window's keyboard shortcuts. Dragging on the
// canvas is done through ImGui in the canvas itself; this is only the keys that
// have to work wherever the mouse is.
func aiEditorHandleEvent(event sdl.Event) {
	t, ok := event.(sdl.KeyboardEvent)
	if !ok || t.Type != sdl.KEYDOWN {
		return
	}
	// A key going into a text field is that field's, not a shortcut.
	if imgui.CurrentIO().WantCaptureKeyboard() {
		return
	}
	mod := sdl.GetModState()
	switch aiShortcutFor(t.Keysym.Scancode, mod&sdl.KMOD_CTRL != 0, mod&sdl.KMOD_SHIFT != 0) {
	case aiShortcutSave:
		aiSaveCurrent()
	case aiShortcutOpen:
		aiShowOpenDialog, aiFileList = true, nil
	case aiShortcutNew:
		aiNewDoc()
	case aiShortcutUndo:
		AIUndo()
	case aiShortcutRedo:
		AIRedo()
	case aiShortcutDuplicate:
		aiDuplicateSelection()
	case aiShortcutSelectAll:
		aiSelectAll()
	case aiShortcutDelete:
		aiDeleteSelection()
	case aiShortcutFrameAll:
		aiFrameAll()
	}
}

// ---------- what the rest of the editor asks ----------

// AITreeNamesList is every document in resources/ai/, for a picker. Cached on
// the Editor so the folder is not scanned every frame a combo is open.
func AITreeNamesList() []string {
	if Editor.AITreeNames == nil {
		Editor.AITreeNames = aitree.ScanAIFiles()
		if Editor.AITreeNames == nil {
			Editor.AITreeNames = []string{}
		}
	}
	return Editor.AITreeNames
}

// AIEditorDocName is the open document's name, for the save chip and Save All.
func AIEditorDocName() string { return aiDocName() }

// aiDescribeDoc is the one-line summary the inspector and the entity panel
// show: how many roots and helpers a behaviour has.
func aiDescribeDoc(d *aitree.Doc) string {
	if d == nil {
		return ""
	}
	roots, helpers := 0, 0
	for _, t := range d.Trees {
		if t.IsHelper() {
			helpers++
		} else {
			roots++
		}
	}
	parts := []string{fmt.Sprintf("%d root(s)", roots)}
	if helpers > 0 {
		parts = append(parts, fmt.Sprintf("%d helper(s)", helpers))
	}
	if len(d.Params) > 0 {
		parts = append(parts, fmt.Sprintf("%d parameter(s)", len(d.Params)))
	}
	return strings.Join(parts, ", ")
}

var _ = fileutil.ResourcesPath
