package editor

// editor_script_tree.go — the script editor's file list, as a folder tree.
//
// A flat list of every `.flang` under resources/scripts/ was fine at a dozen
// files and stops being fine at fifty: `doors/lift.flang` and
// `enemies/lift_guard.flang` sort nowhere near each other, and the only way to
// see what a folder holds is to read past everything it does not.
//
// The folders are the real ones on disk. That is a deliberate choice over a
// grouping kept only in the editor: `import("shared/math.flang")` already
// resolves through directories, so a folder here is the same folder the
// language sees, and an author who moves a file into one has moved it for
// scripts too, not just for the sidebar. What the editor adds is that a folder
// with nothing in it yet still shows — which is every folder, for the minute
// between making it and putting something in it.
//
// The tree itself is rebuilt from the scan rather than kept: the scan is a few
// dozen strings, it changes whenever anything is saved, created or moved, and a
// cached tree that could disagree with the folder is worth less than no cache.

import (
	"sort"
	"strconv"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/script"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// scriptTreeNode is one folder in the sidebar. Files are leaves and are held as
// their names; Kids are the sub-folders, in name order.
type scriptTreeNode struct {
	Name  string // the folder's own name, "" for the root
	Path  string // slash-separated, relative to the scripts folder
	Kids  []*scriptTreeNode
	Files []scriptTreeFile
}

// scriptTreeFile is one script in a folder: what to show, and what to open.
type scriptTreeFile struct {
	Name string // the base name, with its extension
	Rel  string // slash-separated, relative to the scripts folder
}

// buildScriptTree folds a scan of file paths and folder paths into a tree.
//
// dirs is taken as well as files so that an empty folder survives — see the
// file comment. Both are relative to the scripts folder and slash-separated,
// which is what both scans return.
func buildScriptTree(files, dirs []string) *scriptTreeNode {
	root := &scriptTreeNode{}
	for _, dir := range dirs {
		scriptTreeFolder(root, dir)
	}
	for _, rel := range files {
		rel = strings.TrimPrefix(rel, "/")
		dir, name := "", rel
		if i := strings.LastIndex(rel, "/"); i >= 0 {
			dir, name = rel[:i], rel[i+1:]
		}
		node := root
		if dir != "" {
			node = scriptTreeFolder(root, dir)
		}
		node.Files = append(node.Files, scriptTreeFile{Name: name, Rel: rel})
	}
	scriptTreeSort(root)
	return root
}

// scriptTreeFolder returns the node for a folder path, creating it and every
// folder above it.
func scriptTreeFolder(root *scriptTreeNode, dir string) *scriptTreeNode {
	node := root
	var walked string
	for _, part := range strings.Split(dir, "/") {
		if part == "" {
			continue
		}
		if walked == "" {
			walked = part
		} else {
			walked += "/" + part
		}
		next := (*scriptTreeNode)(nil)
		for _, kid := range node.Kids {
			if kid.Name == part {
				next = kid
				break
			}
		}
		if next == nil {
			next = &scriptTreeNode{Name: part, Path: walked}
			node.Kids = append(node.Kids, next)
		}
		node = next
	}
	return node
}

// scriptTreeSort puts folders and files in name order, recursively — the scans
// arrive sorted, but a folder created from a file path can land out of order.
func scriptTreeSort(n *scriptTreeNode) {
	sort.Slice(n.Kids, func(i, j int) bool { return n.Kids[i].Name < n.Kids[j].Name })
	sort.Slice(n.Files, func(i, j int) bool { return n.Files[i].Name < n.Files[j].Name })
	for _, kid := range n.Kids {
		scriptTreeSort(kid)
	}
}

// scriptTreeCount returns how many scripts are in a folder and everything under
// it, for the count beside a collapsed folder.
func scriptTreeCount(n *scriptTreeNode) int {
	total := len(n.Files)
	for _, kid := range n.Kids {
		total += scriptTreeCount(kid)
	}
	return total
}

// ---------- drawing ----------

// drawScriptEditorFileList draws the sidebar: the folder tree, and the buttons
// that add to it.
func drawScriptEditorFileList() {
	if imgui.Button("Rescan##scriptfiles") {
		scriptEditorRescan()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Re-read resources/scripts/ from disk.")
	}
	imgui.SameLine()
	if imgui.Button("New Folder") {
		scriptEditorNewFolderIn = ""
		scriptEditorShowNewFolder = true
	}
	if scriptEditorFiles == nil || scriptEditorDirs == nil {
		scriptEditorRescan()
	}
	imgui.Separator()

	if len(scriptEditorFiles) == 0 && len(scriptEditorDirs) == 0 {
		editorTextWrapped("No scripts yet. New creates one in resources/scripts/.")
		return
	}

	imgui.BeginChildV("##scripttree", imgui.Vec2{}, false, 0)
	tree := buildScriptTree(scriptEditorFiles, scriptEditorDirs)
	drawScriptTreeBody(tree)
	imgui.EndChild()
}

// drawScriptTreeBody draws one folder's contents: its sub-folders, then its
// files. Folders first is the convention every file browser shares, and it
// keeps a folder from hiding between two scripts.
func drawScriptTreeBody(n *scriptTreeNode) {
	for _, kid := range n.Kids {
		drawScriptTreeFolder(kid)
	}
	for _, f := range n.Files {
		drawScriptTreeFile(f)
	}
}

// drawScriptTreeFolder draws one folder and, when open, everything in it.
func drawScriptTreeFolder(n *scriptTreeNode) {
	imgui.PushID("dir/" + n.Path)
	defer imgui.PopID()

	if scriptTreeHolds(n, activeScriptPath()) {
		// The folder the open document is in starts open, so opening a file
		// from anywhere — the Open dialog, a clicked error in the log — leaves
		// the sidebar showing where that file actually lives.
		imgui.SetNextItemOpen(true, imgui.ConditionOnce)
	}
	// A plain tree node: one click anywhere on the row opens the folder, which
	// is what a file list without drag-and-drop should do. SpanAvailWidth only
	// widens the hit box to the row, so the click does not have to land on the
	// name itself.
	open := imgui.TreeNodeV(n.Name+"/", imgui.TreeNodeFlagsSpanAvailWidth)

	// Before the count, so the menu hangs off the folder's own row rather than
	// off the text drawn beside it.
	drawScriptTreeFolderMenu(n)

	// The count goes on the same row so a collapsed folder still says whether
	// it is worth opening.
	imgui.SameLine()
	editorTextMuted(scriptTreeCountLabel(n))

	if !open {
		return
	}
	drawScriptTreeBody(n)
	imgui.TreePop()
}

// scriptTreeCountLabel is the muted count beside a folder.
func scriptTreeCountLabel(n *scriptTreeNode) string {
	switch c := scriptTreeCount(n); c {
	case 0:
		return "empty"
	case 1:
		return "1 script"
	default:
		return strconv.Itoa(c) + " scripts"
	}
}

// drawScriptTreeFile draws one script row: its name, whether it is open, and
// what the engine thinks of it.
func drawScriptTreeFile(f scriptTreeFile) {
	imgui.PushID("file/" + f.Rel)
	defer imgui.PopID()

	path := fileutil.ScriptsDirName + "/" + f.Rel
	doc := scriptDocForPath(path)
	current := doc != nil && doc == activeScript

	label := f.Name
	switch {
	case doc != nil && doc.Dirty():
		label += " *"
	case doc != nil:
		// Open but clean: still worth telling apart from a file on disk that
		// nothing has looked at.
		label += " ·"
	}
	if imgui.SelectableV(label, current, 0, imgui.Vec2{}) {
		scriptEditorOpenFile(f.Rel)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip(path)
	}
	drawScriptTreeFileMenu(f, path)

	// A script the level actually uses is worth telling apart from one sitting
	// in the folder unreferenced.
	if diags := script.ScriptDiagnostics(path); len(diags) > 0 {
		imgui.SameLine()
		editorTextColored(imgui.Vec4{X: 1, Y: 0.4, Z: 0.4, W: 1}, "!")
		if imgui.IsItemHovered() {
			imgui.SetTooltip(diags.Error())
		}
	} else if issues := script.ScriptIssues(path); len(issues) > 0 {
		// Only when it compiled: a script with parse errors has not been
		// linted, and two markers on one row would say the same thing twice.
		imgui.SameLine()
		editorTextColored(scriptIssueColor, "?")
		if imgui.IsItemHovered() {
			imgui.SetTooltip(script.ScriptIssuesText(issues))
		}
	}
}

// drawScriptTreeFolderMenu is a folder's right-click menu.
func drawScriptTreeFolderMenu(n *scriptTreeNode) {
	if !imgui.BeginPopupContextItemV("##dirmenu", imgui.PopupFlagsMouseButtonRight) {
		return
	}
	ctxDrawMenuItems([]ctxMenuItem{
		{Label: n.Path + "/"},
		{Separator: true},
		{Label: "New script here…", Do: func() {
			scriptEditorNewName = n.Path + "/"
			scriptEditorShowNew = true
		}},
		{Label: "New folder here…", Do: func() {
			scriptEditorNewFolderIn = n.Path
			scriptEditorShowNewFolder = true
		}},
	})
	imgui.EndPopup()
}

// drawScriptTreeFileMenu is a script's right-click menu.
//
// Move is a rename with a folder in it, which is all a move ever was on disk —
// and the only thing the tree needs in order to be an organiser rather than a
// viewer.
func drawScriptTreeFileMenu(f scriptTreeFile, path string) {
	if !imgui.BeginPopupContextItemV("##filemenu", imgui.PopupFlagsMouseButtonRight) {
		return
	}
	ctxDrawMenuItems([]ctxMenuItem{
		{Label: f.Rel},
		{Separator: true},
		{Label: "Open", Do: func() { scriptEditorOpenFile(f.Rel) }},
		{Label: "Move or rename…", Do: func() {
			scriptEditorMoveFrom = f.Rel
			scriptEditorMoveTo = f.Rel
			scriptEditorShowMove = true
		}},
	})
	imgui.EndPopup()
}

// scriptTreeHolds reports whether a folder contains that path, at any depth.
func scriptTreeHolds(n *scriptTreeNode, rel string) bool {
	if rel == "" {
		return false
	}
	return strings.HasPrefix(rel, n.Path+"/")
}

// activeScriptPath is the open document's path relative to the scripts folder,
// or "" when it has never been saved.
func activeScriptPath() string {
	if activeScript == nil || activeScript.Path == "" {
		return ""
	}
	return strings.TrimPrefix(activeScript.Path, fileutil.ScriptsDirName+"/")
}
