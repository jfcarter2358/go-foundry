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
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/imguibackend"
	"foundry/internal/material"
	"foundry/internal/model"
	"foundry/internal/procgen"
	"foundry/internal/score"
	"foundry/internal/script"
	"foundry/internal/texture"
	"foundry/internal/tracker"
	"foundry/internal/world"
	"foundry/internal/textureui"
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
	if claudeDeferReloads() {
		claudePlayDefer(paths, ranCommand)
		return
	}
	claudeApplyFileChanges(paths, ranCommand)
}

// claudeApplyFileChanges is the drain proper: one batch of written files turned
// into the reloads the editor has for them.
func claudeApplyFileChanges(paths []string, ranCommand bool) {
	if ranCommand {
		// A command can write anything and names nothing. Rather than rescan the
		// project on every command — a generator run rewrites hundreds of files
		// and the scan would cost more than the reload — the scripts, the
		// textures and the level are treated as suspect, which is what a
		// generator actually touches in this project.
		claudeReload.levelChanged = claudeReload.levelChanged || Editor.SavePath != ""
		claudeReloadScripts()
		claudeReloadTextures()
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

// claudeDeferredChanges is what Claude changed while a play session was up.
//
// Play is live state, and almost none of it is on disk. The script runtime
// holds every object's scratch dict, its timers, its `:spawn:` marks and the
// player's own attributes, and script.ScriptReload throws the lot away and
// starts the level's scripts again from nothing — ScriptRuntimeLoad resets
// first. Under a player halfway through a level that is not a hot reload, it is
// a reset, and one nobody asked for: Claude finishing a file is not the author
// pressing anything. Re-instantiating every entity of a type, or putting a
// model's mesh back, lands the same way.
//
// So a play session gets the world as it was when Play was pressed, and what
// Claude wrote is parked here until the session ends. Nothing is lost by
// waiting: leaving play applies it, and entering play again loads every script
// from disk regardless.
//
// The author's own hot reload is deliberately not covered by this. Saving in
// the script editor while the game runs is most of the point of having the
// script editor (scriptEditorAfterSave), and someone who presses Ctrl+S knows
// what they just changed.
type claudeDeferredChanges struct {
	// paths are the written files, absolute and deduplicated — a generator left
	// running across a long session would otherwise list the same file a
	// hundred times.
	paths []string
	// command records that a command ran, which names nothing and may have
	// changed anything.
	command bool
	// textures records an explicit ask for the whole textures directory.
	textures bool
}

var claudePlayDeferred claudeDeferredChanges

// claudeDeferReloads reports whether reloads have to wait. A development build
// is in gameplay exactly when the editor is not active, and that is the session
// whose state the reloads would throw away.
func claudeDeferReloads() bool { return !Editor.Active }

// claudePlayDefer parks one batch of changes until play ends.
func claudePlayDefer(paths []string, ranCommand bool) {
	claudePlayDeferred.command = claudePlayDeferred.command || ranCommand
	for _, p := range paths {
		abs := claudeAbs(p)
		if slices.Contains(claudePlayDeferred.paths, abs) {
			continue
		}
		claudePlayDeferred.paths = append(claudePlayDeferred.paths, abs)
	}
}

// claudePlayDeferCount is how many changes are waiting on the end of play.
func claudePlayDeferCount() int {
	n := len(claudePlayDeferred.paths)
	if claudePlayDeferred.command {
		n++
	}
	if claudePlayDeferred.textures {
		n++
	}
	return n
}

// claudePlayDeferNote is what a tool is told when its reload is held back, so
// that Claude does not read the silence as a failure and write the file again.
func claudePlayDeferNote(what string) string {
	return "A play session is running, so the editor is holding the level as it was when Play was " +
		"pressed — reloading now would restart every script under the player. It will re-read " +
		what + " as soon as they leave play."
}

// ClaudeApplyDeferredFileChanges applies everything Claude changed during a play
// session. EditorToggle calls it on the way out of play, after the level has
// been put back and the script runtime reset, so the reloads land on the
// authored level rather than on the session's copy of it.
func ClaudeApplyDeferredFileChanges() {
	d := claudePlayDeferred
	claudePlayDeferred = claudeDeferredChanges{}
	if d.textures && !d.command {
		// The command case reloads textures anyway, so it is not done twice.
		claudeReloadTextures()
	}
	claudeApplyFileChanges(d.paths, d.command)
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

	case ext == model.SkeletonExt:
		// The models on the rig are what draws, so they are what reloads: the
		// skeleton itself is never uploaded. modelEditorDirty covers the open
		// document's bones and clips too, which is why it guards this as well.
		if modelDoc != nil && modelDoc.Skeleton == claudeAssetName(abs) && modelEditorDirty() {
			claudeNoteConflict(abs)
			return
		}
		claudeReload.reloaded += requestSkeletonReload(claudeAssetName(abs), "")

	case ext == ".fpar":
		ReloadEmittersUsing(claudeAssetName(abs))
		claudeReload.reloaded++

	case ext == material.MaterialExt:
		name := claudeAssetName(abs)
		if MaterialEditorOpen() && materialName() == name && materialEditorDirty() {
			claudeNoteConflict(abs)
			return
		}
		// Re-reading the library is all a material needs: everything wearing
		// one resolves it live and notices through the generation counter.
		// The sky is the exception, and InvalidateSkyMaterial is what tells it.
		if err := material.Reload(name); err != nil {
			claudeNoteConflict(abs)
			return
		}
		gfx.EnsureMaterialTextures()
		invalidateSkyMaterialForEditor()
		if MaterialEditorOpen() && materialName() == name {
			// The editor is showing this material and has no edits of its own,
			// so it should be showing what Claude wrote.
			if err := materialLoad(abs); err != nil {
				claudeNoteConflict(abs)
				return
			}
		}
		claudeReload.reloaded++

	case ext == procgen.Ext:
		// A generator document: the Generator editor re-reads it if it holds
		// it unedited (editor_generator_claude.go).
		if !claudeGeneratorFileChanged(abs) {
			claudeNoteConflict(abs)
			return
		}
		claudeReload.reloaded++

	case ext == world.BiomeExt:
		// A biome: re-read, and every terrain painted with it regrows. A file
		// that does not parse keeps the biome as it was — biome_save is the
		// tool that says why before anything is written.
		if _, err := reloadBiome(claudeAssetName(abs)); err != nil {
			log.Printf("WARNING: biome: %v", err)
			claudeNoteConflict(abs)
			return
		}
		claudeReload.reloaded++

	case ext == ".png", ext == ".ftex":
		// A texture, if it is in textures/ — a PNG anywhere else is a
		// screenshot or a document's own image and belongs to nothing here.
		//
		// Nothing picks these up on its own: the picker offers what the startup
		// scan found, and a texture already resident on the GPU is never read
		// twice, so without this a new texture is invisible and a rewritten one
		// keeps drawing yesterday's image until the editor restarts. This is
		// the case the texture reload was written for.
		if filepath.Dir(abs) != filepath.Clean(fileutil.TexturesDir()) {
			return
		}
		name := claudeAssetName(abs)
		if gfx.IsNormalMapName(name) {
			// A normal map is reached through the texture it belongs to, and
			// is never a texture in its own right — see gfx.NormalMapSuffix.
			name = strings.TrimSuffix(name, gfx.NormalMapSuffix)
		}
		if textureEditorHoldsUnsaved(name) {
			claudeNoteConflict(abs)
			return
		}
		if err := texture.ReloadTexture(name); err != nil {
			log.Printf("WARNING: texture reload: %v", err)
			claudeNoteConflict(abs)
			return
		}
		claudeReload.reloaded++

	case ext == score.FileExt:
		// A score: the library drops it and the Score editor re-reads it
		// if it holds it unedited (editor_score_claude.go).
		if !scoreFileChanged(abs) {
			claudeNoteConflict(abs)
			return
		}
		claudeReload.reloaded++

	case ext == tracker.InstrumentExt:
		// An instrument: what plays it and what reads its drum map hear the
		// new one (editor_score_claude.go).
		scoreInstrumentChanged()
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

// claudeReloadPath is the reload tool's route in: one file, or a directory,
// which is how Claude asks for every texture at once after a generator run has
// written a batch of them.
func claudeReloadPath(path string) string {
	abs := claudeAbs(path)
	if st, err := os.Stat(abs); err == nil && st.IsDir() {
		if filepath.Clean(abs) != filepath.Clean(fileutil.TexturesDir()) {
			return "Nothing in " + path + " is something the editor reloads by directory. " +
				"Name a file, or textures/ for every texture."
		}
		if claudeDeferReloads() {
			claudePlayDeferred.textures = true
			return claudePlayDeferNote("every texture")
		}
		r := texture.ReloadTextures()
		if !r.Changed() {
			return "The editor's textures already match what is on disk."
		}
		return "Reloaded textures: " + r.Summary() + "."
	}
	if claudeDeferReloads() {
		claudePlayDefer([]string{abs}, false)
		return claudePlayDeferNote(path)
	}
	claudeApplyFileChange(abs)
	return "The editor will re-read " + path + "."
}

// claudeReloadTextures re-reads textures/ in full. For when the file that
// changed is not named: a generator run, or an explicit ask for the lot.
func claudeReloadTextures() {
	r := texture.ReloadTextures()
	claudeReload.reloaded += len(r.Added) + len(r.Reloaded) + len(r.Removed)
	for _, err := range r.Errs {
		log.Printf("WARNING: texture reload: %v", err)
	}
	// A texture being painted right now is the one case a silent reload can
	// lose work — the document keeps its edits, but the image on the walls
	// has just gone back to what was last saved. Worth saying.
	for _, name := range r.Reloaded {
		if textureEditorHoldsUnsaved(name) {
			claudeNoteConflict(filepath.Join(fileutil.TexturesDir(), name+".png"))
		}
	}
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
	if n := claudePlayDeferCount(); n > 0 {
		// Said here rather than silently held, because from Claude's side the
		// file is written and from the author's side nothing has changed: the
		// gap between the two is exactly what this band is for.
		editorTextColored(claudeWarnColor,
			"Play session running — "+plural(n, "change")+" from Claude will be re-read when you leave play.")
	}
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

// textureEditorHoldsUnsaved reports whether the texture editor is painting the
// named texture and has changes nobody has saved. Those changes are ahead of
// what is on disk, so re-reading the file would replace what is on screen with
// something older — the one direction a reload must never go.
func textureEditorHoldsUnsaved(name string) bool {
	ed := textureui.ActiveTexEditor
	return ed != nil && ed.Doc != nil && ed.Doc.Name == name && textureui.TextureEditorDirty()
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
