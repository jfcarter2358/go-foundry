package editor

// editor_script_window.go — the script editor's window lifecycle.
//
// Follows the model editor and texture editor exactly: a package-level handle
// that is nil when closed, an Open that raises rather than duplicating, a Close
// that requests rather than acts, and an OnClose that runs with this window's GL
// context current. RenderSecondaryWindows and RouteWindowEvent pick the window
// up on their own the moment NewUIWindow returns, so there is nothing else to
// register.

import (
	"foundry/internal/uiwin"
	"log"

	"github.com/veandco/go-sdl2/sdl"
)

const (
	scriptWindowW = 900
	scriptWindowH = 700
)

// scriptEditorWindow is nil when the editor is closed.
var scriptEditorWindow *uiwin.UIWindow

// ScriptEditorOpen reports whether the window is up.
func ScriptEditorOpen() bool { return scriptEditorWindow != nil }

// OpenScriptEditorWindow opens the script editor, or raises it if it is already
// up.
//
// The editor always has something to edit: with nothing loaded it starts on a
// working handler, the same way the model editor starts on a cube rather than on
// a file dialog. The open documents outlive the window, so closing and
// reopening resumes on the same tabs — the same choice the texture and model
// editors make.
func OpenScriptEditorWindow() {
	if scriptEditorWindow != nil {
		scriptEditorWindow.Win.Raise()
		return
	}
	scriptEditorEnsureDoc()
	w, err := uiwin.NewUIWindow("Script Editor", scriptWindowW, scriptWindowH,
		drawScriptEditorWindow, onScriptEditorClose)
	if err != nil {
		log.Printf("open script editor window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0.11, 0.11, 0.13, 1}
	w.HandleEvent = scriptEditorHandleEvent
	w.FilterEvent = scriptEditorFilterEvent
	// Assigned last: until this is set, ScriptEditorOpen has to keep saying no,
	// or a Draw triggered during construction would find a half-built window.
	scriptEditorWindow = w
}

// CloseScriptEditorWindow asks for the window to go away at the end of the
// frame. Safe to call from inside the window's own Draw — see
// UIWindow.CloseRequested.
func CloseScriptEditorWindow() {
	if scriptEditorWindow != nil {
		scriptEditorWindow.CloseRequested = true
	}
}

// onScriptEditorClose runs with this window's GL context current. The documents
// stay in memory: closing the window is not the same as discarding unsaved
// work.
func onScriptEditorClose() {
	scriptEditorWindow = nil
	// Nothing is drawn while the window is shut, so where the text was drawn
	// last is about to be a stale answer the gutter would align itself to.
	scriptBufTextValid = false
	scriptBufActive = false
}

// scriptEditorFocused reports whether this window owns keyboard input.
// sdl.GetKeyboardState is process-global, so without this check a keypress meant
// for the script editor would also reach the main window's fly camera.
func scriptEditorFocused() bool {
	return scriptEditorWindow != nil &&
		scriptEditorWindow.Win.GetFlags()&sdl.WINDOW_INPUT_FOCUS != 0
}

// scriptEditorHandleEvent handles this window's keyboard shortcuts.
//
// Every one of them is a Ctrl chord, and that is not an accident: the text area
// wants nearly every bare key, and a shortcut that fired while the author was
// typing would be maddening. ImGui skips its own handling of a key whenever
// Ctrl is down, so a chord is safe mid-word in a way that no bare key is.
func scriptEditorHandleEvent(event sdl.Event) {
	t, ok := event.(sdl.KeyboardEvent)
	if !ok || t.Type != sdl.KEYDOWN {
		return
	}
	ctrl := sdl.GetModState()&sdl.KMOD_CTRL != 0

	// Ctrl chords are safe while typing; bare keys are not.
	if ctrl {
		switch t.Keysym.Scancode {
		case sdl.SCANCODE_S:
			if sdl.GetModState()&sdl.KMOD_SHIFT != 0 {
				scriptEditorShowSaveAs = true
			} else {
				scriptEditorSave()
			}
		case sdl.SCANCODE_O:
			scriptEditorShowOpen = true
		case sdl.SCANCODE_N:
			scriptEditorShowNew = true
		case sdl.SCANCODE_R:
			scriptEditorReloadAll()
		case sdl.SCANCODE_W:
			scriptEditorCloseDoc(activeScript)
		case sdl.SCANCODE_TAB:
			// Safe while typing, despite the text area taking Tab: ImGui skips
			// its insert-a-tab branch whenever Ctrl is down.
			scriptEditorCycleTab(sdl.GetModState()&sdl.KMOD_SHIFT != 0)
		}
	}
	// No bare keys. Escape in particular does *not* close the window: this
	// editor holds unsaved text, and a window that a hand resting on Escape can
	// make disappear is a window you cannot trust to still be there. The title
	// bar's close button is the way out, and it is the only one.
}

// scriptEditorFilterEvent takes Escape away from ImGui while the buffer is
// being typed in, and does nothing with it.
//
// ImGui's InputText treats Escape as "revert": it deactivates the field and
// puts back the text it held when it was focused. In a one-line box for a
// number that is a kindness. In a script buffer it silently discards every edit
// since the file was last saved, which is the single most expensive keystroke
// this editor had — and it cannot be undone afterwards, because the widget's
// undo history is deactivated along with it.
//
// It has to happen here rather than in HandleEvent: by the time that runs,
// ImGuiProcessEvent has already fed the key in and the revert has happened.
// Only while the buffer is actually active, so Escape still closes a modal
// dialog or a context menu — ImGui owns those, and owning them is right.
func scriptEditorFilterEvent(event sdl.Event) bool {
	t, ok := event.(sdl.KeyboardEvent)
	// Only the press is taken. Letting the release through costs nothing —
	// ImGui reads it as clearing a key it never saw pressed — and it means a
	// buffer that became active between the two cannot be left holding an
	// Escape that is down forever.
	if !ok || t.Type != sdl.KEYDOWN ||
		t.Keysym.Scancode != sdl.SCANCODE_ESCAPE || !scriptBufActive {
		return false
	}
	// Swallowed and nothing more. Escape while writing a script means nothing
	// in this editor, which is the whole point: the keystroke that used to
	// throw the buffer away now does not do anything at all.
	return true
}
