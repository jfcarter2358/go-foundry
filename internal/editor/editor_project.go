package editor

// editor_project.go — File > Open Project and New Project.
//
// Switching projects is a restart on the chosen folder (internal/project):
// every registry the engine has — textures, models, entity types, scripts — was
// filled from this project at boot, and starting again is the one way to empty
// them that cannot miss one. So all this dialog does is pick the folder, check
// it, make sure nothing unsaved is lost on the way, and ask the frame loop to
// end; the app starts the new process once this one has let go of its window.

import (
	"fmt"
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/imguibackend"
	"foundry/internal/platform"
	"foundry/internal/project"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// PendingProject is the project to reopen on once this process has shut down,
// or "". Read by the app on the way out.
var PendingProject string

var (
	showProjectDialog bool
	projectDialogNew  bool
	projectDialogPath string
	projectDialogErr  string
	// projectDiscardArmed is the second click that leaves unsaved work behind.
	projectDiscardArmed bool
	projectRecent       []string
	// projectBrowser is the Browse... folder chooser, drawn inside the dialog.
	projectBrowser folderBrowser
)

// editorOpenProjectDialog opens the dialog, for opening a project or, with
// isNew, making one.
func editorOpenProjectDialog(isNew bool) {
	projectDialogNew = isNew
	projectDialogErr = ""
	projectDiscardArmed = false
	projectDialogPath = ""
	projectBrowser = folderBrowser{}
	if isNew {
		projectDialogPath = filepath.Join(filepath.Dir(fileutil.ProjectPath), "New Project")
	}
	projectRecent = nil
	if dir, err := fileutil.UserDataDir(project.DataDirName); err == nil {
		for _, d := range project.LoadRecent(dir) {
			if !strings.EqualFold(filepath.Clean(d), filepath.Clean(fileutil.ProjectPath)) && project.IsProject(d) {
				projectRecent = append(projectRecent, d)
			}
		}
	}
	showProjectDialog = true
}

// editorProjectMenuItems are File's project entries.
func editorProjectMenuItems() {
	if imgui.MenuItem("Open Project...") {
		editorOpenProjectDialog(false)
	}
	editorMenuTooltip("Open another project folder: one holding resources/game.yaml,\n" +
		"such as a clone of your game's repository.\nCurrently: " + fileutil.ProjectPath)
	if imgui.MenuItem("New Project...") {
		editorOpenProjectDialog(true)
	}
	editorMenuTooltip("Make a new, empty project in a folder, and open it.")
}

// editorProjectDialog draws the dialog, when it is open.
func editorProjectDialog() {
	title := "Open Project##projectdlg"
	if projectDialogNew {
		title = "New Project##projectdlg"
	}
	imguibackend.WithModalPopup(title, &showProjectDialog, func() {
		editorTextMuted("Current: " + fileutil.ProjectPath)
		if !projectDialogNew && len(projectRecent) > 0 {
			imgui.Text("Recent")
			for i, d := range projectRecent {
				// DontClosePopups: a plain Selectable closes the popup it is
				// clicked in (imgui_widgets.cpp, "Automatically close popups"),
				// which dismissed this dialog the moment a project was picked
				// and left the editor on the one already open. AllowDoubleClick
				// makes the obvious gesture open it outright.
				const flags = imgui.SelectableFlagsDontClosePopups | imgui.SelectableFlagsAllowDoubleClick
				if imgui.SelectableV(fmt.Sprintf("%s##recent%d", d, i), projectDialogPath == d, flags, imgui.Vec2{}) {
					projectDialogPath = d
					projectDialogErr = ""
					projectDiscardArmed = false
					projectBrowser.open = false
					if imgui.IsMouseDoubleClicked(0) {
						projectDialogConfirm(d, unsavedEditorNames())
					}
				}
			}
			imgui.Separator()
		}
		if projectDialogNew {
			imgui.Text("Folder for the new project (created if it does not exist):")
		} else {
			imgui.Text("Project folder — the one holding resources/:")
		}
		imgui.SetNextItemWidth(460)
		if imgui.InputText("##projectpath", &projectDialogPath) {
			projectDialogErr = ""
			projectDiscardArmed = false
		}
		imgui.SameLine()
		if imgui.Button("Browse...##projectdlg") {
			projectBrowser.start(projectBrowserStart())
		}
		if dir, ok := projectBrowser.draw("projectdlg", 220); ok {
			projectDialogPath = dir
			projectBrowser.open = false
			projectDialogErr = ""
			projectDiscardArmed = false
		}

		if projectDialogErr != "" {
			editorTextColored(releaseErrorColor, projectDialogErr)
		}
		unsaved := unsavedEditorNames()
		if len(unsaved) > 0 {
			editorTextColored(releaseWarnColor, "Unsaved: "+strings.Join(unsaved, ", ")+
				" — the editor restarts on the new project, and unsaved work is lost.")
		}

		label := "Open"
		if projectDialogNew {
			label = "Create and Open"
		}
		if projectDiscardArmed {
			label += " Without Saving"
		}
		if imgui.Button(label) {
			projectDialogConfirm(projectDialogPath, unsaved)
		}
		imgui.SameLine()
		if len(unsaved) > 0 && imgui.Button("Save All") {
			// Not from inside the button: saves upload into GL contexts of
			// their own (see EditorSaveAll).
			uiwin.RequestWindowOpen(EditorSaveAll)
			projectDiscardArmed = false
		}
		imgui.SameLine()
		if imgui.Button("Cancel##projectdlg") {
			imgui.CloseCurrentPopup()
		}
	})
}

// projectDialogConfirm is the Open button, and a double-click on a recent
// project: check the folder, insist once on unsaved work, and ask the frame
// loop to end so the app can start again on it.
func projectDialogConfirm(path string, unsaved []string) {
	dir := projectPathFromInput(path)
	var err error
	if projectDialogNew {
		err = project.CheckNew(dir)
	} else {
		err = project.CheckOpen(dir)
	}
	switch {
	case err != nil:
		projectDialogErr = err.Error()
	case len(unsaved) > 0 && !projectDiscardArmed:
		projectDiscardArmed = true
	default:
		if projectDialogNew {
			if err = project.Create(dir); err != nil {
				projectDialogErr = err.Error()
				return
			}
		}
		PendingProject = dir
		platform.Running = false
		imgui.CloseCurrentPopup()
	}
}

// projectPathFromInput is what the text field means as a folder. Windows
// Explorer's "Copy as path" wraps the path in quotes and a shell would have
// eaten them, so they come off here rather than becoming part of the name.
func projectPathFromInput(s string) string {
	dir := strings.TrimSpace(s)
	if len(dir) >= 2 && strings.HasPrefix(dir, `"`) && strings.HasSuffix(dir, `"`) {
		dir = strings.TrimSpace(dir[1 : len(dir)-1])
	}
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Clean(dir)
}

// projectBrowserStart is where Browse... opens: whatever is in the field, or
// failing that the folder holding the project that is open, so its siblings —
// the other checkouts, most likely — are one listing away.
func projectBrowserStart() string {
	if dir := projectPathFromInput(projectDialogPath); dir != "" {
		return dir
	}
	return filepath.Dir(fileutil.ProjectPath)
}
