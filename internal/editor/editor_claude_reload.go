package editor

// editor_claude_reload.go — keeping the editor honest about what Claude changed.
//
// Claude works on files. The editor works on documents it loaded from those
// files and holds in memory. The moment those two disagree, everything after it
// is confusing in a way that is hard to trace: a script that does not behave as
// the one on screen, a save that quietly undoes a change made a minute ago.
//
// So every file a tool writes is routed back here and turned into the reload the
// editor already has for that kind of file. The rules:
//
//   - Never overwrite unsaved work. A document with edits of its own gets a
//     banner offering the reload, and keeps its edits until someone says
//     otherwise. This is the same bargain the Git window strikes after a
//     checkout, and for the same reason.
//   - Reload what is cheap and safe silently. A script or a model the editor is
//     holding but nobody has edited can simply be re-read; being asked about it
//     would be noise.
//   - The level is neither. Reloading it throws away the editor's undo history,
//     so it is always offered rather than taken.
//
// Draining happens once a frame from the editor's main loop, not from the
// panel's window: reloads touch GL objects and level state, and the panel's
// window has its own context current.

import (
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/imguibackend"
	"foundry/internal/model"
	"foundry/internal/script"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// claudeReload is what the last drain found and could not do silently.
var claudeReload struct {
	// levelChanged is set when the open level's own file was rewritten.
	levelChanged bool
	// otherChanged names files that changed while the editor held unsaved edits
	// to them.
	conflicts []string
	// reloaded counts what was re-read silently, for the status line.
	reloaded int
}

// ClaudeDrainFileChanges applies what Claude's tools wrote to the editor's own
// documents. Called once a frame from the editor's main loop, beside
// DrainModelReloads.
func ClaudeDrainFileChanges() {
	if claudeState.sess == nil {
		return
	}
	paths, ranCommand := claudeState.sess.DrainTouched()
	if ranCommand {
		// A command can write anything and names nothing. Rather than rescan the
		// project on every command — a generator run rewrites hundreds of files
		// and the scan would cost more than the reload — the scripts and the
		// level are treated as suspect, which is what a generator actually
		// touches in this project.
		claudeReload.levelChanged = claudeReload.levelChanged || Editor.SavePath != ""
		claudeReloadScripts()
	}
	for _, p := range paths {
		claudeApplyFileChange(p)
	}
	if claudeReload.reloaded > 0 {
		// Said once per drain rather than per file: a generator run rewrites
		// hundreds and the point is only that the editor has kept up.
		claudeSetStatus("Reloaded %s changed by Claude.", plural(claudeReload.reloaded, "file"))
		claudeReload.reloaded = 0
	}
}

// claudeApplyFileChange routes one written file to the right reload.
func claudeApplyFileChange(path string) {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(fileutil.ProjectPath, path)
	}
	abs = filepath.Clean(abs)
	ext := strings.ToLower(filepath.Ext(abs))

	switch {
	case abs == filepath.Clean(Editor.SavePath):
		// The open level. Always offered, never taken: a reload is a new undo
		// history, and the editor may be holding work of its own.
		claudeReload.levelChanged = true

	case ext == fileutil.ScriptExt:
		if scriptEditorHoldsUnsaved(abs) {
			claudeNoteConflict(abs)
			return
		}
		claudeReloadScripts()

	case ext == ".fmdl":
		if modelEditorHolds(abs) && modelEditorDirty() {
			claudeNoteConflict(abs)
			return
		}
		requestModelReload(abs)
		claudeReload.reloaded++

	case ext == ".fpar":
		ReloadEmittersUsing(claudeAssetName(abs))
		claudeReload.reloaded++

	case ext == ".png", ext == ".ftex":
		// Textures are re-read by the renderer's own cache when the file's
		// timestamp moves; nothing to do here beyond noting it happened.
		claudeReload.reloaded++

	case ext == ".yaml", ext == ".yml":
		// An entity type, a level that is not the open one, or game.yaml. The
		// entity types are the ones the editor holds live.
		if strings.Contains(abs, string(filepath.Separator)+"entities"+string(filepath.Separator)) {
			claudeReload.reloaded += ReloadEntitiesOfType(claudeAssetName(abs), nil)
		}
	}
}

// claudeAssetName is the name an asset is known by: its path under resources/,
// without the extension, which is how a level names a particle effect or an
// entity type.
func claudeAssetName(abs string) string {
	rel := fileutil.RelAssetPath(abs)
	rel = strings.TrimSuffix(rel, filepath.Ext(rel))
	// An entity type is named by its file's base name, not its folder.
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[i+1:]
	}
	return rel
}

func claudeReloadScripts() {
	script.ScriptReload()
	if ScriptEditorOpen() {
		scriptEditorRescan()
	}
	claudeRefreshCleanScriptDocs()
	claudeReload.reloaded++
}

// claudeRefreshCleanScriptDocs re-reads the open script tabs that have no
// unsaved edits.
//
// script.ScriptReload reloads what the *engine* runs, and scriptEditorRescan
// rescans the file *list*; neither touches an open buffer. Without this a tab
// sitting on a script Claude has just rewritten keeps showing the old text, and
// the next save in that tab writes the old text back over the new — the worst
// version of the editor and the disk disagreeing, because it silently undoes
// work.
//
// A dirty tab is left alone. Its unsaved edits are the author's, and they win;
// claudeNoteConflict has already named the file.
func claudeRefreshCleanScriptDocs() {
	for _, d := range scriptDocs {
		if d.Path == "" || d.Dirty() {
			continue
		}
		src, err := script.ReadScriptFile(fileutil.ScriptPath(d.Path))
		if err != nil {
			continue
		}
		text := string(src)
		if text == d.Text {
			continue
		}
		// SetText starts the reparse debounce, so the new text is checked
		// against the level the same way typing it would be.
		d.SetText(text, sdl.GetTicks())
		d.saved = text
	}
}

func claudeNoteConflict(abs string) {
	name := fileutil.RelAssetPath(abs)
	for _, c := range claudeReload.conflicts {
		if c == name {
			return
		}
	}
	claudeReload.conflicts = append(claudeReload.conflicts, name)
}

// claudeDrawUnsavedWarning is the band above the input box: what the editor is
// holding that Claude cannot see, and what Claude changed that the editor has
// not taken.
//
// Both halves are about the same gap, from the two directions, and both belong
// where a message is about to be sent — which is the moment either one matters.
func claudeDrawUnsavedWarning() {
	if names := unsavedEditorNames(); len(names) > 0 {
		editorTextColored(claudeWarnColor,
			"Unsaved: "+strings.Join(names, ", ")+" — Claude reads the files on disk.")
		imgui.SameLine()
		if imgui.Button("Save All##claudesaveall") {
			uiwin.RequestWindowOpen(EditorSaveAll)
		}
	}
	if claudeReload.levelChanged {
		editorTextColored(claudeWarnColor, "Claude changed this level's file.")
		imgui.SameLine()
		imguibackend.EditorPushDisabled(Editor.SavePath == "" || EditorLevelLoading())
		if imgui.Button("Reload level##claudereload") {
			EditorBeginLoadLevel(Editor.SavePath)
			claudeReload.levelChanged = false
		}
		imguibackend.EditorPopDisabled()
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Reopen the level from disk. Unsaved edits to it are lost,\nand the undo history starts again.")
		}
		imgui.SameLine()
		if imgui.Button("Keep mine##claudekeep") {
			claudeReload.levelChanged = false
		}
	}
	if len(claudeReload.conflicts) > 0 {
		editorTextColored(claudeWarnColor,
			"Changed on disk while you have unsaved edits: "+strings.Join(claudeReload.conflicts, ", "))
		imgui.SameLine()
		if imgui.Button("Dismiss##claudeconflicts") {
			claudeReload.conflicts = nil
		}
	}
}

// scriptEditorHoldsUnsaved reports whether a script tab has unsaved edits to the
// file at abs. Those edits win: re-reading the file would throw away work the
// person can see on screen in favour of work they cannot.
func scriptEditorHoldsUnsaved(abs string) bool {
	for _, d := range scriptDocs {
		if d.Path == "" || !d.Dirty() {
			continue
		}
		if sameFile(fileutil.ScriptPath(d.Path), abs) {
			return true
		}
	}
	return false
}

// modelEditorHolds reports whether the model editor has the model at abs open.
func modelEditorHolds(abs string) bool {
	if modelDoc == nil {
		return false
	}
	path := modelDoc.Path
	if path == "" && modelDoc.ID != "" {
		path = model.ModelPath(modelDoc.ID)
	}
	return path != "" && sameFile(path, abs)
}

// sameFile compares two paths as the filesystem would. Both sides reach this
// from different directions — one from a tool's argument, one from the editor's
// own state — and only one of them is guaranteed absolute.
func sameFile(a, b string) bool {
	if absA, err := filepath.Abs(a); err == nil {
		a = absA
	}
	if absB, err := filepath.Abs(b); err == nil {
		b = absB
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
