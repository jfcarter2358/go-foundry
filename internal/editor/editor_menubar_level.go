package editor

// editor_menubar_level.go — the level editor's own menus: File, Edit, View and
// Build. The rest of its menu bar is editor_menubar.go's, shared with every
// other editor.

import (
	"foundry/internal/levelio"
	"foundry/internal/uiwin"
	"foundry/internal/world"
	"path/filepath"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorShadingTooltip explains the Solid/Wireframe toggle, wherever it is
// offered — the tool row's button and the View menu's item.
const editorShadingTooltip = "Z — Solid hides the far side of the world, and only the\n" +
	"points and edges you can see can be clicked.\n" +
	"Wireframe draws it as lines and lets you click any of them."

// editorCameraTooltip explains the camera toggle for the mode it is in.
func editorCameraTooltip(flying bool) string {
	if flying {
		return "RMB/MMB drag to look, WASD fly, Space/Shift up and down,\n" +
			"wheel dollies, F frames the selection."
	}
	return "RMB/MMB drag to orbit, Shift+MMB pan, wheel zoom,\n" +
		"WASD slides the pivot, F frames the selection."
}

func editorLevelFileMenu(lvl *world.Level) {
	if !imgui.BeginMenu("File") {
		return
	}
	if imgui.MenuItem("Open Level...") {
		Editor.LevelFiles = levelio.ScanLevelFiles(Editor.LevelsDir)
		Editor.NewLevelName = ""
		Editor.ShowLevelPicker = true
	}
	editorMenuTooltip("Open a level from levels/, or create a new one.")
	imgui.Separator()
	if imgui.MenuItem("Save") {
		if err := editorSaveLevel(lvl, Editor.SavePath); err != nil {
			editorSetStatus("Save failed: %v", err)
		} else {
			editorSetStatus("Saved to %s", Editor.SavePath)
		}
	}
	if imgui.MenuItem("Save As...") {
		Editor.SaveAsName = filepath.Base(Editor.SavePath)
		Editor.ShowSaveAs = true
	}
	editorSaveAllMenuItem()
	imgui.Separator()
	editorProjectMenuItems()
	// No Exit. Quitting is the window's own close button, the same as every
	// other window the editor opens — an Exit a few pixels from Save is a
	// level lost to a misclick, and the title bar is where anyone looks for it.
	imgui.EndMenu()
}

func editorLevelEditMenu() {
	if !imgui.BeginMenu("Edit") {
		return
	}
	editorUndoRedoItems(EditorCanUndo(), EditorCanRedo(), EditorUndo, EditorRedo)
	imgui.EndMenu()
}

// editorLevelViewMenu is the 3D view's display state, which the tool row
// below also offers as buttons.
func editorLevelViewMenu() {
	if !imgui.BeginMenu("View") {
		return
	}
	if imgui.MenuItemV("Wireframe", "Z", Editor.Wireframe, true) {
		editorSetWireframe(!Editor.Wireframe)
	}
	editorMenuTooltip(editorShadingTooltip)
	if imgui.MenuItemV("Bounds", "B", Editor.ShowBounds, true) {
		editorSetShowBounds(!Editor.ShowBounds)
	}
	editorMenuTooltip(editorBoundsTooltip)
	flying := editorCam3D.Fly
	if imgui.MenuItemV("Fly Camera", "V", flying, true) {
		editorSetFlyCam(!flying)
	}
	editorMenuTooltip(editorCameraTooltip(flying))
	imgui.EndMenu()
}

// editorLevelBuildMenu is compiling the level, looking at the result, and
// shipping it — the three steps in the order they are taken.
func editorLevelBuildMenu() {
	if !imgui.BeginMenu("Build") {
		return
	}
	if imgui.MenuItem("Compile") {
		msg, err := EditorCompileLevel()
		if err != nil {
			editorSetStatus("Compile failed: %v", err)
		} else {
			editorSetStatus("%s", msg)
		}
	}
	editorMenuTooltip("Build the level's BSP, visibility and baked lighting,\n" +
		"and write it beside the level file. Reports leaks.")

	// The numbers Compile prints say whether the level is sealed; only the
	// eye catches a seam or a surface CSG should not have taken, and without
	// this the first sight of a compiled level is the shipped build.
	if imgui.MenuItemV("Preview Compiled", "", EditorPreviewingCompiled(), true) {
		editorTogglePreviewCompiled()
	}
	editorMenuTooltip("Draw the level the way a release build would:\n" +
		"compiled surfaces, culled by the compiled visibility set.\n" +
		"Any edit turns it off.")

	imgui.Separator()
	relLabel := "Release..."
	if ReleaseBuildRunning() {
		relLabel = "Release (building...)"
	}
	if imgui.MenuItemV(relLabel+"##buildrel", "", ReleaseWindowOpen(), true) {
		uiwin.RequestWindowOpen(OpenReleaseWindow)
	}
	editorMenuTooltip("Build a release of this project: compile every level, pack the\n" +
		"content beside the game binary, and archive it for each platform.")
	imgui.EndMenu()
}

// editorTogglePreviewCompiled flips the compiled preview and reports how it
// went on the status line.
func editorTogglePreviewCompiled() {
	msg, err := EditorSetCompiledPreview(!EditorPreviewingCompiled())
	if err != nil {
		editorSetStatus("Preview failed: %v", err)
	} else {
		editorSetStatus("%s", msg)
	}
}
