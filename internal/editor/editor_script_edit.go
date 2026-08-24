package editor

import "foundry/internal/imguibackend"

// editor_script_edit.go — cut, copy, paste and select-all in the script editor's
// text area.
//
// Two halves, split for the reason the rest of this editor is split: the part
// that has opinions about text (scriptEditFor, below) decides what an operation
// does to a buffer and where the caret lands, and a test can ask it without a
// window; the part that talks to ImGui (scriptApplyEdit) only carries that
// answer across.
//
// The awkwardness this file exists to contain is that the buffer's text does not
// belong to us while it is being edited. InputTextMultiline keeps its own copy,
// its own caret and its own selection, and the *only* way in is the callback it
// runs each frame while it holds the keyboard focus. So a menu item cannot cut
// anything itself — it parks the operation and asks for that focus back, and the
// callback does the work a frame or two later (editor_script_ui.go).
//
// The keyboard shortcuts need none of this: ImGui implements Ctrl+X/C/V/A inside
// the widget already. All they ever needed was a clipboard that reaches the rest
// of the desktop, which is editor_clipboard.go.

// scriptEditOp is one editing operation the buffer's context menu can ask for.
type scriptEditOp int

const (
	scriptEditNone scriptEditOp = iota
	scriptEditCut
	scriptEditCopy
	scriptEditPaste
	scriptEditDelete
	scriptEditSelectAll
)

// scriptSel is a selection: a byte range within a buffer. Start may be greater
// than End — ImGui reports the range in the direction it was dragged — so
// callers work with the result of ordered() rather than the raw pair.
type scriptSel struct{ Start, End int }

// ordered returns the selection with Start <= End, clamped to a buffer of n
// bytes.
func (s scriptSel) ordered(n int) scriptSel {
	if s.Start > s.End {
		s.Start, s.End = s.End, s.Start
	}
	if s.Start < 0 {
		s.Start = 0
	}
	if s.End > n {
		s.End = n
	}
	if s.Start > s.End {
		s.Start = s.End
	}
	return s
}

// empty reports whether the selection covers nothing.
func (s scriptSel) empty() bool { return s.Start >= s.End }

// scriptEdit is what one operation does: replace the bytes in [Start, End) with
// Text, put the caret and selection at Sel, and — when Clip is set — leave that
// on the clipboard.
//
// Start == End with an empty Text means the buffer is not touched at all, which
// is what Copy and Select All amount to.
type scriptEdit struct {
	Start, End int
	Text       string
	Sel        scriptSel
	Clip       string
	SetClip    bool
}

// touchesBuffer reports whether applying this edit changes any text.
func (e scriptEdit) touchesBuffer() bool { return e.End > e.Start || e.Text != "" }

// scriptEditFor works out what op does to text, given the current selection and
// what the clipboard holds. ok is false when there is nothing to do — no
// selection to cut, or nothing on the clipboard to paste.
//
// Cut and Copy want a selection rather than falling back to the whole buffer.
// ImGui's own Ctrl+C does take the whole buffer when nothing is selected, but a
// menu row is read before it is clicked: "Copy", greyed out, says what will
// happen, and a Copy that silently took the entire file would not.
func scriptEditFor(op scriptEditOp, text string, sel scriptSel, clip string) (scriptEdit, bool) {
	sel = sel.ordered(len(text))
	switch op {
	case scriptEditCopy:
		if sel.empty() {
			return scriptEdit{}, false
		}
		// No splice: the caret and selection stay exactly where they were, so
		// copying twice in a row copies the same thing twice.
		return scriptEdit{Start: sel.Start, End: sel.Start, Sel: sel,
			Clip: text[sel.Start:sel.End], SetClip: true}, true

	case scriptEditCut:
		if sel.empty() {
			return scriptEdit{}, false
		}
		return scriptEdit{Start: sel.Start, End: sel.End,
			Sel:  scriptSel{Start: sel.Start, End: sel.Start},
			Clip: text[sel.Start:sel.End], SetClip: true}, true

	case scriptEditDelete:
		if sel.empty() {
			return scriptEdit{}, false
		}
		return scriptEdit{Start: sel.Start, End: sel.End,
			Sel: scriptSel{Start: sel.Start, End: sel.Start}}, true

	case scriptEditPaste:
		clip = imguibackend.NormalizeClipboardText(clip)
		if clip == "" {
			return scriptEdit{}, false
		}
		// Replacing the selection, not inserting beside it: pasting over a
		// selection is what every other text field does.
		at := sel.Start + len(clip)
		return scriptEdit{Start: sel.Start, End: sel.End, Text: clip,
			Sel: scriptSel{Start: at, End: at}}, true

	case scriptEditSelectAll:
		return scriptEdit{Sel: scriptSel{Start: 0, End: len(text)}}, true
	}
	return scriptEdit{}, false
}

// scriptApplyText is what an edit does to a plain string. The ImGui path does
// not use it — it splices the widget's own buffer, so that the widget's undo
// history sees a normal edit — but it is the same answer, which is what makes
// scriptEditFor testable.
func scriptApplyText(text string, e scriptEdit) string {
	if !e.touchesBuffer() {
		return text
	}
	return text[:e.Start] + e.Text + text[e.End:]
}
