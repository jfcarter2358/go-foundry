package editor

// editor_yaml_window.go — the level YAML editor's window and its keys.
//
// The lifecycle is the script editor's (editor_script_window.go): a handle that
// is nil while the window is shut, an Open that raises rather than duplicates,
// and a document that outlives the window, so closing and reopening resumes on
// the same unsaved text.
//
// The keys are where this editor departs from that one, and all of them for one
// reason — every key this window adds is one the text widget also wants:
//
//   - Tab indents two spaces instead of inserting a tab, which YAML forbids.
//   - Ctrl+Z and Ctrl+Y go to this window's own history (editor_yaml_undo.go)
//     rather than the widget's, which forgets itself whenever any other field
//     takes the keyboard.
//   - Enter keeps the new line's indent, and deepens it after `key:`.
//   - Escape never reverts the buffer (the script editor's lesson), and in the
//     find box it closes the find bar with the match selected.
//
// All of them are taken in FilterEvent, before ImGui has fed the key to the
// widget; by the time HandleEvent runs, the widget has already inserted the tab
// or undone its own idea of the last edit.

import (
	"log"

	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	yamlWindowW = 1000
	yamlWindowH = 760
)

// yamlEditorWindow is nil when the editor is closed.
var yamlEditorWindow *uiwin.UIWindow

// LevelYAMLEditorOpen reports whether the window is up.
func LevelYAMLEditorOpen() bool { return yamlEditorWindow != nil }

// OpenLevelYAMLEditor opens the editor on the level the level editor has open,
// or raises it if it is already up.
//
// With no document yet it starts on that level's file. A level that has never
// been saved — the blank one a development build starts on — has no file to
// read, so the buffer starts from the level as the editor holds it, and saving
// it creates the file.
func OpenLevelYAMLEditor() {
	if yamlEditorWindow != nil {
		yamlEditorWindow.Win.Raise()
		return
	}
	if yamlEd.doc == nil {
		yamlOpenLevelEditorLevel()
	}
	w, err := uiwin.NewUIWindow("Level YAML", yamlWindowW, yamlWindowH,
		drawYAMLEditorWindow, onYAMLEditorClose)
	if err != nil {
		log.Printf("open level YAML editor window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0.11, 0.11, 0.13, 1}
	w.HandleEvent = yamlEditorHandleEvent
	w.FilterEvent = yamlEditorFilterEvent
	yamlEditorWindow = w
}

// CloseLevelYAMLEditor asks for the window to go at the end of the frame.
func CloseLevelYAMLEditor() {
	if yamlEditorWindow != nil {
		yamlEditorWindow.CloseRequested = true
	}
}

// onYAMLEditorClose runs with this window's GL context current. The document
// stays: closing the window is not discarding unsaved work.
func onYAMLEditorClose() {
	yamlEditorWindow = nil
	yamlEd.textValid = false
	yamlEd.active = false
	yamlEd.find.fieldActive = false
	yamlComplClose()
}

// yamlEditorHandleEvent handles the window's Ctrl chords and F3, after ImGui
// has had the event. None of these are keys the text widget uses.
func yamlEditorHandleEvent(event sdl.Event) {
	t, ok := event.(sdl.KeyboardEvent)
	if !ok || t.Type != sdl.KEYDOWN {
		return
	}
	mod := sdl.GetModState()
	ctrl, shift := mod&sdl.KMOD_CTRL != 0, mod&sdl.KMOD_SHIFT != 0
	switch {
	case t.Keysym.Scancode == sdl.SCANCODE_F3:
		yamlFindStep(shift)
	case !ctrl:
	case t.Keysym.Scancode == sdl.SCANCODE_S:
		yamlEditorSave()
	case t.Keysym.Scancode == sdl.SCANCODE_O:
		yamlEd.showOpen = true
	case t.Keysym.Scancode == sdl.SCANCODE_F:
		yamlFindOpen(false)
	case t.Keysym.Scancode == sdl.SCANCODE_H:
		yamlFindOpen(true)
	case t.Keysym.Scancode == sdl.SCANCODE_G:
		yamlEd.gotoLine = ""
		yamlEd.showGoto = true
	}
}

// yamlEditorFilterEvent takes the keys the text widget must not see. See the
// file's header for which and why.
func yamlEditorFilterEvent(event sdl.Event) bool {
	if yamlEd.active {
		if k, ok := event.(sdl.KeyboardEvent); ok && k.Type == sdl.KEYDOWN {
			if yamlComplHandleKey(k) {
				return true
			}
		}
		if yamlComplHandleMouse(event) {
			return true
		}
	}

	t, ok := event.(sdl.KeyboardEvent)
	if !ok || t.Type != sdl.KEYDOWN {
		return false
	}
	mod := sdl.GetModState()
	ctrl, shift := mod&sdl.KMOD_CTRL != 0, mod&sdl.KMOD_SHIFT != 0

	switch sc := t.Keysym.Scancode; {
	case sc == sdl.SCANCODE_ESCAPE:
		if yamlEd.find.fieldActive {
			yamlFindClose(true)
			return true
		}
		// Swallowed while the buffer is being typed in: ImGui would revert it.
		return yamlEd.active

	case ctrl && (sc == sdl.SCANCODE_Z || sc == sdl.SCANCODE_Y):
		// The buffer's history, whenever the buffer is what is being edited —
		// or when nothing is, so Ctrl+Z after clicking Replace All undoes the
		// replace. A find or replace box keeps its own Ctrl+Z.
		if !yamlEd.active && imgui.CurrentIO().WantTextInput() {
			return false
		}
		if sc == sdl.SCANCODE_Y || shift {
			yamlQueueOp(yamlRedoOp())
		} else {
			yamlQueueOp(yamlUndoOp())
		}
		return true

	case !yamlEd.active:
		return false

	case sc == sdl.SCANCODE_TAB && !ctrl:
		if shift {
			yamlQueueOp(yamlOp{Name: "dedent", Record: yamlRecordSealed, NeedsSel: true, Fn: yamlDedent})
		} else {
			yamlQueueOp(yamlOp{Name: "indent", Record: yamlRecordSealed, NeedsSel: true, Fn: yamlIndent})
		}
		return true

	case ctrl && sc == sdl.SCANCODE_SLASH:
		yamlQueueOp(yamlOp{Name: "comment", Record: yamlRecordSealed, NeedsSel: true, Fn: yamlToggleComment})
		return true

	case (sc == sdl.SCANCODE_RETURN || sc == sdl.SCANCODE_KP_ENTER) && !ctrl:
		// Let ImGui insert the newline; the callback indents after it.
		yamlEd.enterPressed = true
	}
	return false
}

// yamlUndoOp and yamlRedoOp move through the history. They read the text at
// the moment they run, not when the key was pressed, so a keystroke typed in
// the same frame cannot make them undo the wrong thing.
func yamlUndoOp() yamlOp {
	return yamlOp{Name: "undo", Record: yamlRecordNone,
		Fn: func(text string, sel scriptSel) (string, scriptSel, bool) {
			out, caret, ok := yamlEd.doc.undo.Undo(text)
			return out, scriptSel{caret, caret}, ok
		}}
}

func yamlRedoOp() yamlOp {
	return yamlOp{Name: "redo", Record: yamlRecordNone,
		Fn: func(text string, sel scriptSel) (string, scriptSel, bool) {
			out, caret, ok := yamlEd.doc.undo.Redo(text)
			return out, scriptSel{caret, caret}, ok
		}}
}
