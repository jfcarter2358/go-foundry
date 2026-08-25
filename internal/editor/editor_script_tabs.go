package editor

// editor_script_tabs.go — the script editor's open documents.
//
// One buffer at a time was the shape of this editor until now, and it made the
// commonest thing an author does — reading one script while writing another,
// copying a clause across, checking what an imported file actually defines —
// cost a round trip through the file list and back, losing the cursor both
// ways. Opening a second file now opens a second tab.
//
// The set is deliberately not a map keyed by path: the tab strip has an order
// the author chose, an unsaved document has no path at all, and Save As gives a
// document a path it did not have. A slice in tab order with an identity that
// is neither the path nor the index (scriptDoc.id) is what survives all three.
//
// Documents outlive the window, exactly as the single buffer did: closing the
// editor is not the same as discarding unsaved work.

import (
	"fmt"

	"foundry/internal/imguibackend"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

var (
	// scriptDocs are the open tabs, in the order the strip shows them.
	scriptDocs []*scriptDoc

	// activeScript is the document the buffer, the diagnostics pane and every
	// action work on. Always one of scriptDocs once the editor has been opened.
	activeScript *scriptDoc

	// scriptTabSelect asks the strip to switch to this document on the next
	// frame it draws. Opening a file cannot simply assign activeScript: ImGui's
	// tab bar decides which tab is selected from its own state, and would put
	// the selection straight back the following frame.
	scriptTabSelect *scriptDoc

	// scriptTabClosing is the unsaved document the close prompt is about.
	scriptTabClosing        *scriptDoc
	scriptEditorShowConfirm bool
)

// scriptEditorEnsureDoc guarantees there is something to edit.
//
// The editor always has a buffer, the way the model editor always has a mesh:
// opening it on a file dialog would make the commonest case — try something out
// — the one that takes the most steps.
func scriptEditorEnsureDoc() {
	if activeScript != nil && scriptDocIndex(activeScript) >= 0 {
		return
	}
	if len(scriptDocs) > 0 {
		activeScript = scriptDocs[len(scriptDocs)-1]
		return
	}
	d := NewScriptDoc()
	d.reparseNow(Editor.level)
	scriptDocs = append(scriptDocs, d)
	activeScript = d
}

// scriptDocIndex returns d's position in the tab strip, or -1.
func scriptDocIndex(d *scriptDoc) int {
	for i, other := range scriptDocs {
		if other == d {
			return i
		}
	}
	return -1
}

// scriptDocForPath returns the open document with this path, or nil. Only ever
// asked about saved documents: an unsaved one has no path to match.
func scriptDocForPath(path string) *scriptDoc {
	if path == "" {
		return nil
	}
	for _, d := range scriptDocs {
		if d.Path == path {
			return d
		}
	}
	return nil
}

// scriptEditorShowDoc opens d as a tab and selects it, or selects the tab it is
// already in.
func scriptEditorShowDoc(d *scriptDoc) {
	if d == nil {
		return
	}
	if scriptDocIndex(d) < 0 {
		scriptDocs = append(scriptDocs, d)
	}
	activeScript = d
	scriptTabSelect = d
}

// scriptEditorAdoptEmptyTab replaces a single untouched starter buffer with d.
//
// Opening the editor and then opening a file should not leave a tab holding the
// starter text nobody asked for. Only when it is the *only* tab and has not
// been edited: anything else is work, however little of it there is.
func scriptEditorAdoptEmptyTab(d *scriptDoc) {
	if len(scriptDocs) != 1 || scriptDocs[0] == d {
		return
	}
	only := scriptDocs[0]
	if only.Path != "" || only.Text != scriptStarterText {
		return
	}
	scriptDocs = scriptDocs[:0]
}

// scriptEditorCloseDoc closes a tab, asking first if it has unsaved work.
func scriptEditorCloseDoc(d *scriptDoc) {
	if d == nil {
		return
	}
	if d.Dirty() {
		scriptTabClosing = d
		scriptEditorShowConfirm = true
		return
	}
	scriptEditorDropDoc(d)
}

// scriptEditorDropDoc closes a tab without asking.
//
// The neighbour to the left inherits the selection rather than the one to the
// right, which is where the eye already is after closing something.
func scriptEditorDropDoc(d *scriptDoc) {
	i := scriptDocIndex(d)
	if i < 0 {
		return
	}
	scriptDocs = append(scriptDocs[:i], scriptDocs[i+1:]...)
	if scriptTabClosing == d {
		scriptTabClosing = nil
	}
	if activeScript != d {
		return
	}
	activeScript = nil
	if len(scriptDocs) > 0 {
		activeScript = scriptDocs[max(i-1, 0)]
		scriptTabSelect = activeScript
	}
	scriptEditorEnsureDoc()
}

// drawScriptEditorTabs draws the tab strip and settles which document is
// active.
func drawScriptEditorTabs() {
	if !imgui.BeginTabBarV("##scripttabs", imgui.TabBarFlagsReorderable|
		imgui.TabBarFlagsFittingPolicyScroll|imgui.TabBarFlagsTabListPopupButton) {
		return
	}
	defer imgui.EndTabBar()

	// Closes are collected and acted on after the loop: dropping a document
	// while the strip is mid-draw would leave ImGui holding a tab that no
	// longer has anything behind it.
	var closing []*scriptDoc

	for _, d := range scriptDocs {
		open := true
		flags := imgui.TabItemFlagsNone
		if d.Dirty() {
			flags |= imgui.TabItemFlagsUnsavedDocument
		}
		if scriptTabSelect == d {
			flags |= imgui.TabItemFlagsSetSelected
		}
		// The ID is the document's, not its name: Save As renames a tab, and a
		// tab that changed ID would be a different tab to ImGui — losing its
		// place in the strip and its scroll position.
		if imgui.BeginTabItemV(fmt.Sprintf("%s###scriptdoc%d", d.TabName(), d.id), &open, flags) {
			activeScript = d
			imgui.EndTabItem()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(scriptTabTooltip(d))
		}
		if !open {
			closing = append(closing, d)
		}
	}
	if scriptTabSelect != nil {
		scriptTabSelect = nil
	}

	// A trailing + for a new buffer, where every tabbed editor puts one.
	if imgui.TabItemButtonV("+##scriptnewtab", imgui.TabItemFlagsTrailing|imgui.TabItemFlagsNoTooltip) {
		d := NewScriptDoc()
		d.reparseNow(Editor.level)
		scriptEditorShowDoc(d)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Open another buffer. Ctrl+N creates a named file instead.")
	}

	for _, d := range closing {
		scriptEditorCloseDoc(d)
	}
	scriptEditorEnsureDoc()
}

// scriptEditorCycleTab moves the selection one tab along, wrapping at both
// ends — the shape every tabbed editor's Ctrl+Tab has.
func scriptEditorCycleTab(back bool) {
	if len(scriptDocs) < 2 {
		return
	}
	i := scriptDocIndex(activeScript)
	if i < 0 {
		return
	}
	step := 1
	if back {
		step = -1
	}
	next := (i + step + len(scriptDocs)) % len(scriptDocs)
	scriptEditorShowDoc(scriptDocs[next])
}

// scriptTabTooltip is what hovering a tab says: where the file is, and whether
// it has been written.
func scriptTabTooltip(d *scriptDoc) string {
	if d.Path == "" {
		return "Never saved — Ctrl+Shift+S gives it a name."
	}
	if d.Dirty() {
		return d.Path + "\nUnsaved changes."
	}
	return d.Path
}

// drawScriptEditorCloseConfirm asks before a tab with unsaved work goes away.
//
// A tab's × is one click from a lost afternoon, and this editor has already
// been in the business of throwing edits away silently once — see the Escape
// handling in editor_script_window.go.
func drawScriptEditorCloseConfirm() {
	imguibackend.WithModalPopup("Close without saving?", &scriptEditorShowConfirm, func() {
		d := scriptTabClosing
		if d == nil {
			imgui.CloseCurrentPopup()
			return
		}
		imgui.Text(d.Name() + " has unsaved changes.")
		imgui.Separator()
		if imgui.Button("Save and close") {
			// An unnamed buffer has nowhere to be saved to, so the Save As
			// dialog takes over and the tab stays open behind it.
			if d.Path == "" {
				scriptEditorShowSaveAs = true
				scriptTabClosing = nil
				imgui.CloseCurrentPopup()
				return
			}
			if err := d.Save(""); err != nil {
				scriptEditorStatus("Save failed: %v", err)
				imgui.CloseCurrentPopup()
				return
			}
			scriptEditorAfterSave(d)
			scriptEditorDropDoc(d)
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Discard") {
			scriptEditorDropDoc(d)
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##closescript") {
			scriptTabClosing = nil
			imgui.CloseCurrentPopup()
		}
	})
}
