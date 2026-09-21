package editor

// editor_yaml_undo.go — the level YAML editor's own undo history.
//
// The script editor leaves undo to ImGui's text widget, and in that editor that
// is good enough. Here it is not, because ImGui keeps exactly one text-edit
// state for the whole context: the moment the find box, the replace box or any
// other field takes the keyboard, the buffer's undo history belongs to that
// field instead and is gone. An editor whose Ctrl+Z stops working after
// Replace All — the one edit most in need of an undo — is worse than one with
// no Replace All.
//
// So the buffer's history is kept here, as a list of splices, and Ctrl+Z and
// Ctrl+Y are taken away from ImGui before it can see them. Every change to the
// text lands in the history the same way, whoever made it: typing, a paste, a
// completion, a replace, an indent. The frame loop compares the widget's text
// with the document's and records the difference.
//
// A splice rather than a copy of the text, because a level is two megabytes and
// a hundred snapshots of it is a fifth of a gigabyte for one window's undo.

// yamlUndoMax caps the history. Splices are small, so this is about how far
// back anybody would reasonably press Ctrl+Z, not about memory.
const yamlUndoMax = 1000

// yamlUndoMergeMS is how close together two keystrokes must be to undo as one.
// Typing a word and then pressing Ctrl+Z should take the word back, not its
// last letter.
const yamlUndoMergeMS = 1000

// yamlEdit is one change: at byte At, Old was replaced by New.
type yamlEdit struct {
	At       int
	Old, New string
	// When is when it was made, for merging.
	When uint32
	// Sealed stops anything merging into this edit — set on anything that is
	// not ordinary typing, so a replace is never folded into the letters typed
	// just before it.
	Sealed bool
}

// yamlUndo is the history.
type yamlUndo struct {
	done, undone []yamlEdit
}

// yamlDiff returns the single splice that turns a into b: the common prefix
// and suffix are left alone and what lies between is the edit.
func yamlDiff(a, b string) (at int, old, new string) {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	return p, a[p : len(a)-s], b[p : len(b)-s]
}

// Record notes that the text went from before to after. sealed marks an edit
// that must stand alone in the history.
func (u *yamlUndo) Record(before, after string, now uint32, sealed bool) {
	if before == after {
		return
	}
	at, old, new := yamlDiff(before, after)
	// More than a character at once is a paste, a cut or a newline and its
	// indent — a step of its own, which the typing after it does not join.
	if len(old) > 1 || len(new) > 1 {
		sealed = true
	}
	e := yamlEdit{At: at, Old: old, New: new, When: now, Sealed: sealed}
	u.undone = u.undone[:0]
	if n := len(u.done); n > 0 && !sealed && u.merge(&u.done[n-1], e) {
		return
	}
	u.done = append(u.done, e)
	if len(u.done) > yamlUndoMax {
		u.done = append(u.done[:0], u.done[len(u.done)-yamlUndoMax:]...)
	}
}

// merge folds e into last when the two are one run of typing or of
// backspacing, and reports whether it did.
func (u *yamlUndo) merge(last *yamlEdit, e yamlEdit) bool {
	if last.Sealed || e.When-last.When > yamlUndoMergeMS {
		return false
	}
	// A new line starts a new undo step, the way it does in every editor:
	// Ctrl+Z after typing three lines takes back the last one.
	if len(e.New) > 0 && e.New[0] == '\n' {
		return false
	}
	switch {
	case last.Old == "" && e.Old == "" && e.At == last.At+len(last.New):
		// Typing on: "ab" then "c" at its end.
		last.New += e.New
	case last.New == "" && e.New == "" && e.At+len(e.Old) == last.At:
		// Backspacing: "x" at 5, then "y" at 4.
		last.At, last.Old = e.At, e.Old+last.Old
	case last.New == "" && e.New == "" && e.At == last.At:
		// Forward delete: "x" at 5, then "y" at 5.
		last.Old += e.Old
	default:
		return false
	}
	last.When = e.When
	return true
}

// Seal stops the next edit merging into the last one — called when the caret
// moves somewhere else, so typing in two places is two undo steps.
func (u *yamlUndo) Seal() {
	if n := len(u.done); n > 0 {
		u.done[n-1].Sealed = true
	}
}

// CanUndo and CanRedo say whether there is anything to take back or put back.
func (u *yamlUndo) CanUndo() bool { return len(u.done) > 0 }
func (u *yamlUndo) CanRedo() bool { return len(u.undone) > 0 }

// Undo takes the last edit back out of text, returning the text before it and
// where the caret belongs. ok is false with nothing to undo, or when the text
// does not hold what the edit left behind — a keystroke landed in the same
// frame and has not been recorded yet. Nothing is lost then: the history is
// left as it was, and the next press finds the keystroke recorded.
func (u *yamlUndo) Undo(text string) (out string, caret int, ok bool) {
	n := len(u.done)
	if n == 0 {
		return text, 0, false
	}
	e := u.done[n-1]
	if e.At+len(e.New) > len(text) || text[e.At:e.At+len(e.New)] != e.New {
		return text, 0, false
	}
	u.done = u.done[:n-1]
	e.Sealed = true
	u.undone = append(u.undone, e)
	return text[:e.At] + e.Old + text[e.At+len(e.New):], e.At + len(e.Old), true
}

// Redo puts the last undone edit back.
func (u *yamlUndo) Redo(text string) (out string, caret int, ok bool) {
	n := len(u.undone)
	if n == 0 {
		return text, 0, false
	}
	e := u.undone[n-1]
	if e.At+len(e.Old) > len(text) || text[e.At:e.At+len(e.Old)] != e.Old {
		return text, 0, false
	}
	u.undone = u.undone[:n-1]
	u.done = append(u.done, e)
	return text[:e.At] + e.New + text[e.At+len(e.Old):], e.At + len(e.New), true
}

// Clear forgets the whole history, for a document replaced wholesale.
func (u *yamlUndo) Clear() {
	u.done, u.undone = nil, nil
}
