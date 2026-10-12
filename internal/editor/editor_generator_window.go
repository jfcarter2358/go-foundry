package editor

// editor_generator_window.go — the Generator editor, in its own OS window.
//
// A generator (.fgen, internal/procgen) is a document of stages — terrain,
// rocks, scatter, textures — each an instance of a registered stage kind with
// its own parameters. This window edits one, previews the selected stage as it
// is edited, runs the document, and splices what it places into its target
// level. The same shape as the material editor, which it is modelled on:
//
//   - the window owns a *procgen.Doc (genDoc) that outlives it, so popping the
//     editor in and out returns to the same document;
//   - the parameter panel is built from the stage kind's schema
//     (editor_generator_params.go) — a new stage kind, or a new parameter on
//     one, needs no UI code;
//   - undo is whole-document snapshots (editor_generator_undo.go);
//   - save state is the shared fingerprint chip (editor_save_state.go).
//
// What is particular to it:
//
//   - two long-running jobs, both off the UI thread (editor_generator_job.go):
//     the RUN, behind a progress modal with per-stage progress, a log and
//     Cancel, and the PREVIEW, re-run automatically (debounced) whenever the
//     document changes, cancelling a stale one;
//   - a viewport with two modes: a top-down 2D view of the preview's image,
//     point and line layers in which splines are edited directly
//     (editor_generator_view2d.go), and a 3D view of its meshes through the
//     engine's world program (editor_generator_view3d.go);
//   - the splice into the target level (editor_generator_splice.go), through
//     the level editor's own undoable reload when the level is open.
//
// GL objects: the 2D layers' textures are shared objects and are made in
// this window's context when a preview lands; the 3D meshes carry VAOs, which
// are never shared, so they are built here and released in
// onGeneratorEditorClose.

import (
	"fmt"
	"log"
	"path/filepath"

	"foundry/internal/procgen"
	_ "foundry/internal/procgen/stages" // the built-in stage kinds
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	generatorWindowW = int32(1320)
	generatorWindowH = int32(820)

	generatorToolbarH = editorTopBarH
	// The left column: generator files over the stage list.
	generatorListWidth = float32(230)
	// The right column: the selected stage's parameters.
	generatorPanelWidth = float32(380)
	// The strip under the viewport: the preview's notes and the problems.
	generatorFooterH = float32(96)
)

// generatorEditorWindow is the pop-out; nil when the editor is closed.
var generatorEditorWindow *uiwin.UIWindow

// The document and where it lives.
var (
	genDoc    *procgen.Doc
	genPath   string // "" for a document never written
	genStatus string
	// genRev counts edits. The save chip and the preview's debounce key off
	// it, since a document has no edit counter of its own.
	genRev uint64
	// genSelected is the selected stage's index, or -1.
	genSelected = -1
	// genFileList caches the generators/ listing; nil means rescan.
	genFileList []string
)

// Dialogs.
var (
	genShowNew      bool
	genNewName      string
	genNewTemplate  = procgen.BlankTemplate
	genShowOpen     bool
	genShowSaveAs   bool
	genSaveAsName   string
	genConfirmForce bool
)

// The viewport's mode.
const (
	genView2D = iota
	genView3D
)

var genViewMode = genView2D

// ---------- lifecycle ----------

// GeneratorEditorOpen reports whether the window is up.
func GeneratorEditorOpen() bool { return generatorEditorWindow != nil }

// OpenGeneratorEditorWindow opens the editor, or raises it. It always has
// something to edit: with nothing loaded it starts on a new blank generator.
func OpenGeneratorEditorWindow() {
	if generatorEditorWindow != nil {
		generatorEditorWindow.Win.Raise()
		return
	}
	genEnsureDoc()
	w, err := uiwin.NewUIWindow("Generator", generatorWindowW, generatorWindowH,
		drawGeneratorEditorWindow, onGeneratorEditorClose)
	if err != nil {
		log.Printf("open generator editor window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0.10, 0.11, 0.13, 1}
	w.HandleEvent = generatorEditorHandleEvent
	generatorEditorWindow = w
}

// OpenGeneratorEditorOn opens the editor on a named generator. Deferred
// through RequestWindowOpen, since a caller is usually inside another
// window's Draw.
func OpenGeneratorEditorOn(name string) {
	uiwin.RequestWindowOpen(func() {
		if err := genLoad(procgen.DocPath(name)); err != nil {
			editorSetStatus("Could not open generator %s: %v", name, err)
			return
		}
		OpenGeneratorEditorWindow()
	})
}

// CloseGeneratorEditorWindow asks the window to go at the end of the frame.
func CloseGeneratorEditorWindow() {
	if generatorEditorWindow != nil {
		generatorEditorWindow.CloseRequested = true
	}
}

// onGeneratorEditorClose runs with this window's context current. A run in
// flight keeps going — closing the window is not cancelling the work — and
// its result (and the splice) is picked up when the window is next drawn.
func onGeneratorEditorClose() {
	genFreeView3D()
	genFreeLayerTextures()
	generatorEditorWindow = nil
}

// generatorEditorFocused reports whether this window owns keyboard input.
func generatorEditorFocused() bool {
	return generatorEditorWindow != nil &&
		generatorEditorWindow.Win.GetFlags()&sdl.WINDOW_INPUT_FOCUS != 0
}

// genEnsureDoc guarantees there is something to edit.
func genEnsureDoc() {
	if genDoc == nil {
		genSetDoc(&procgen.Doc{Name: "generator", Seed: 1}, "")
	}
}

// genSetDoc makes d the document being edited, from path ("" for new).
func genSetDoc(d *procgen.Doc, path string) {
	genDoc, genPath = d, path
	genSelected = -1
	if len(d.Stages) > 0 {
		genSelected = 0
	}
	genRev++
	GeneratorClearUndoHistory()
	generatorMarkSaved()
	genSpline = genSplineRef{}
	genRequestPreview()
}

// genName is the document's name as it would be saved.
func genName() string {
	if genDoc == nil {
		return ""
	}
	if genPath != "" {
		return procgen.NameFromPath(genPath)
	}
	return genDoc.Name
}

// genSelectedStage is the selected stage, or nil.
func genSelectedStage() *procgen.Stage {
	if genDoc == nil || genSelected < 0 || genSelected >= len(genDoc.Stages) {
		return nil
	}
	return &genDoc.Stages[genSelected]
}

// genAfterEdit is called after anything changes the document: the save
// chip's signal, and the preview's.
func genAfterEdit() {
	genRev++
	genRequestPreview()
}

// ---------- load and save ----------

func genLoad(path string) error {
	d, err := procgen.ReadDoc(path)
	if err != nil {
		return err
	}
	genSetDoc(d, path)
	genStatus = "Opened " + filepath.Base(path)
	return nil
}

func genSave(path string) error {
	if genDoc == nil {
		return nil
	}
	name := procgen.NameFromPath(path)
	if !procgen.ValidDocName(name) {
		return fmt.Errorf("%q: a generator's name is lower-case letters, digits and underscores", name)
	}
	genDoc.Name = name
	if err := procgen.WriteDoc(path, genDoc); err != nil {
		return err
	}
	genPath = path
	genFileList = nil
	generatorMarkSaved()
	genStatus = "Saved " + filepath.Base(path)
	return nil
}

// genSaveCurrent writes to the document's file, or opens Save As.
func genSaveCurrent() {
	if genDoc == nil {
		return
	}
	if genPath == "" {
		genSaveAsName = genDoc.Name
		genShowSaveAs = true
		return
	}
	if err := genSave(genPath); err != nil {
		genStatus = err.Error()
	}
}

// ---------- save state ----------

// generatorSaved is the generator's save chip state (editor_save_state.go's
// savedState). Kept here rather than in that file's var block, which belongs
// to every editor.
var generatorSaved savedState

func generatorKey() any { return docKey{genDoc, genRev} }

func generatorMarkSaved() {
	if genDoc != nil {
		generatorSaved.baseline(generatorKey(), procgen.Fingerprint(genDoc))
	}
}

func generatorEditorDirty() bool {
	if genDoc == nil {
		return false
	}
	generatorSaved.refresh(generatorKey(), 200, func() uint64 { return procgen.Fingerprint(genDoc) })
	return generatorSaved.dirty()
}

// ---------- input ----------

// generatorEditorHandleEvent is the window's raw input: shortcuts, and the
// 3D view's orbit camera. The 2D view takes its input through imgui.
func generatorEditorHandleEvent(event sdl.Event) {
	if generatorEditorWindow == nil {
		return
	}
	io := imgui.CurrentIO()
	switch t := event.(type) {
	case sdl.KeyboardEvent:
		if t.Type != sdl.KEYDOWN || io.WantCaptureKeyboard() {
			return
		}
		generatorEditorKey(t)
	default:
		if genViewMode == genView3D {
			genView3DHandleEvent(event)
		}
	}
}

func generatorEditorKey(e sdl.KeyboardEvent) {
	ctrl := e.Keysym.Mod&(sdl.KMOD_LCTRL|sdl.KMOD_RCTRL) != 0
	switch {
	case ctrl && e.Keysym.Sym == sdl.K_s:
		genSaveCurrent()
	case ctrl && e.Keysym.Sym == sdl.K_o:
		genShowOpen, genFileList = true, nil
	case ctrl && e.Keysym.Sym == sdl.K_n:
		genShowNew, genNewName = true, ""
	case ctrl && e.Keysym.Sym == sdl.K_z:
		GeneratorUndo()
	case ctrl && e.Keysym.Sym == sdl.K_y:
		GeneratorRedo()
	case e.Keysym.Sym == sdl.K_F5:
		genBeginRun(ctrl, nil)
	case e.Keysym.Sym == sdl.K_TAB:
		genViewMode = 1 - genViewMode
	}
}

// ---------- the frame ----------

func drawGeneratorEditorWindow() {
	w := generatorEditorWindow
	if w == nil {
		return
	}
	genEnsureDoc()
	genStepJobs()
	phStepJobs() // the asset lists' Poly Haven thumbnails
	genEndEdits()

	if genViewMode == genView3D {
		genDrawView3D(w)
	}

	generatorToolbar()
	generatorListWindow()
	generatorPanel()
	generatorViewportWindow()
	generatorFooter()
	generatorDialogs()
	genDrawRunModal()
}

// genStatusf sets the status line.
func genStatusf(format string, args ...any) {
	genStatus = fmt.Sprintf(format, args...)
}
