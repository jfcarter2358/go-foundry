package editor

// editor_ui_level_panel.go — the level's list of UI screens, in the main
// editor's properties panel.
//
// A level-scoped setting with nowhere else to live: it is not a property of a
// chunk, a sector or any placed object, so it sits beside the level's script
// list as one more thing the level itself says. The Edit button beside each
// entry is the same deep link the emitter panel has beside a .fpar.

import (
	"foundry/internal/ui"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorLevelUISection lists the screens this level puts up when play starts.
func editorLevelUISection(lvl *world.Level) {
	if lvl == nil {
		return
	}
	editorPropGroup("UI screens", 0, func() {
		editorTextMutedWrapped(
			"The .fui screens this level puts up when play starts. A menu a script opens later is not listed here — show_screen names it directly.")

		remove := -1
		for i, name := range lvl.UI {
			imgui.PushID("uiscreen" + name)
			imgui.Text(name)
			imgui.SameLine()
			if imgui.Button("Edit") {
				OpenUIEditorOn(name)
			}
			imgui.SameLine()
			if imgui.Button("x") {
				remove = i
			}
			imgui.PopID()
		}
		if remove >= 0 {
			EditorPushUndo()
			lvl.UI = append(lvl.UI[:remove], lvl.UI[remove+1:]...)
			editorSetStatus("Removed a UI screen from the level")
		}

		if imgui.Button("Add screen##uilevel") {
			imgui.OpenPopup("##uileveladd")
		}
		if imgui.BeginPopup("##uileveladd") {
			files := ui.ScanScreenFiles()
			if len(files) == 0 {
				editorTextMuted("No .fui files in resources/ui yet.")
			}
			for _, f := range files {
				name := f[:len(f)-len(ui.UIExt)]
				if levelHasUIScreen(lvl, name) {
					continue
				}
				if imgui.MenuItem(name) {
					EditorPushUndo()
					lvl.UI = append(lvl.UI, name)
					editorSetStatus("Added %s to the level", name)
				}
			}
			imgui.EndPopup()
		}
		imgui.SameLine()
		if imgui.Button("New screen##uilevelnew") {
			OpenUIEditorWindow()
		}
	})
}

func levelHasUIScreen(lvl *world.Level, name string) bool {
	for _, n := range lvl.UI {
		if n == name {
			return true
		}
	}
	return false
}
