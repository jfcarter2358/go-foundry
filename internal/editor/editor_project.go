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
)

// editorOpenProjectDialog opens the dialog, for opening a project or, with
// isNew, making one.
func editorOpenProjectDialog(isNew bool) {
	projectDialogNew = isNew
	projectDialogErr = ""
	projectDiscardArmed = false
	projectDialogPath = ""
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
				if imgui.SelectableV(fmt.Sprintf("%s##recent%d", d, i), projectDialogPath == d, 0, imgui.Vec2{}) {
					projectDialogPath = d
					projectDialogErr = ""
				}
			}
			imgui.Separator()
		}
		if projectDialogNew {
			imgui.Text("Folder for the new project (created if it does not exist):")
		} else {
			imgui.Text("Project folder — the one holding resources/:")
		}
		imgui.SetNextItemWidth(560)
		if imgui.InputText("##projectpath", &projectDialogPath) {
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
			dir := strings.TrimSpace(projectDialogPath)
			if abs, err := filepath.Abs(dir); err == nil && dir != "" {
				dir = abs
			}
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
					err = project.Create(dir)
				}
				if err != nil {
					projectDialogErr = err.Error()
					break
				}
				PendingProject = dir
				platform.Running = false
				imgui.CloseCurrentPopup()
			}
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
