package editor

import "testing"

// The open-document set, checked without a window. The tab strip only draws
// what these leave behind, so which document is active after a close, and
// whether opening a file twice opens it twice, are settled here.

// resetScriptDocs puts the package's tab state back to empty, and restores it
// afterwards — these are package globals, and a test that left one set would
// be a test that broke the next one.
func resetScriptDocs(t *testing.T) {
	t.Helper()
	docs, active, sel := scriptDocs, activeScript, scriptTabSelect
	t.Cleanup(func() { scriptDocs, activeScript, scriptTabSelect = docs, active, sel })
	scriptDocs, activeScript, scriptTabSelect = nil, nil, nil
}

// namedDoc is a saved document with the given path and no unsaved edits.
func namedDoc(path, text string) *scriptDoc {
	d := NewScriptDoc()
	d.Path = path
	d.Text = text
	d.saved = text
	return d
}

func TestScriptEditorEnsureDocOpensOne(t *testing.T) {
	resetScriptDocs(t)
	scriptEditorEnsureDoc()
	if len(scriptDocs) != 1 || activeScript == nil {
		t.Fatalf("ensure left %d docs, active=%v", len(scriptDocs), activeScript != nil)
	}
	// A second call must not stack up buffers nobody asked for.
	first := activeScript
	scriptEditorEnsureDoc()
	if len(scriptDocs) != 1 || activeScript != first {
		t.Errorf("ensure opened a second buffer: %d docs", len(scriptDocs))
	}
}

func TestScriptEditorShowDocIsIdempotent(t *testing.T) {
	resetScriptDocs(t)
	a := namedDoc("scripts/a.flang", "a")
	scriptEditorShowDoc(a)
	scriptEditorShowDoc(a)
	if len(scriptDocs) != 1 {
		t.Fatalf("showing the same document twice opened %d tabs", len(scriptDocs))
	}
	if activeScript != a || scriptTabSelect != a {
		t.Error("showing a document must both select it and ask the strip to follow")
	}
}

func TestScriptDocForPath(t *testing.T) {
	resetScriptDocs(t)
	a := namedDoc("scripts/a.flang", "a")
	scriptEditorShowDoc(a)
	scriptEditorShowDoc(namedDoc("scripts/b.flang", "b"))

	if got := scriptDocForPath("scripts/a.flang"); got != a {
		t.Error("an open document was not found by its path")
	}
	if got := scriptDocForPath("scripts/missing.flang"); got != nil {
		t.Error("a path nothing is open on must not match")
	}
	// An unsaved buffer has no path, and must not be matched by the empty one.
	scriptEditorShowDoc(NewScriptDoc())
	if got := scriptDocForPath(""); got != nil {
		t.Error("the empty path matched an unsaved buffer")
	}
}

// Closing the tab you are looking at should land on its left-hand neighbour,
// which is where the eye already is.
func TestScriptEditorDropDocPicksTheLeftNeighbour(t *testing.T) {
	resetScriptDocs(t)
	a := namedDoc("scripts/a.flang", "a")
	b := namedDoc("scripts/b.flang", "b")
	c := namedDoc("scripts/c.flang", "c")
	for _, d := range []*scriptDoc{a, b, c} {
		scriptEditorShowDoc(d)
	}

	scriptEditorShowDoc(b)
	scriptEditorDropDoc(b)
	if activeScript != a {
		t.Errorf("after closing b, active = %v, want a", activeScript)
	}
	if len(scriptDocs) != 2 {
		t.Errorf("%d tabs left, want 2", len(scriptDocs))
	}

	// Closing the leftmost has no left neighbour to fall back to.
	scriptEditorShowDoc(a)
	scriptEditorDropDoc(a)
	if activeScript != c {
		t.Errorf("after closing the first tab, active = %v, want c", activeScript)
	}
}

// The editor always has a buffer: closing the last tab opens an empty one
// rather than leaving the window with nothing to draw.
func TestScriptEditorDropLastDocLeavesABuffer(t *testing.T) {
	resetScriptDocs(t)
	a := namedDoc("scripts/a.flang", "a")
	scriptEditorShowDoc(a)
	scriptEditorDropDoc(a)
	if len(scriptDocs) != 1 || activeScript == nil {
		t.Fatalf("closing the last tab left %d docs", len(scriptDocs))
	}
	if activeScript == a {
		t.Error("the closed document is still active")
	}
}

// A tab with unsaved work is never dropped on the spot — the prompt is armed
// instead, and the document stays open behind it.
func TestScriptEditorCloseDocAsksWhenDirty(t *testing.T) {
	resetScriptDocs(t)
	defer func() { scriptTabClosing, scriptEditorShowConfirm = nil, false }()

	d := namedDoc("scripts/a.flang", "saved")
	scriptEditorShowDoc(d)
	d.Text = "edited"

	scriptEditorCloseDoc(d)
	if len(scriptDocs) != 1 {
		t.Fatal("an unsaved tab was closed without asking")
	}
	if scriptTabClosing != d || !scriptEditorShowConfirm {
		t.Error("the close prompt was not armed for the document being closed")
	}

	// Clean, it goes straight away.
	d.saved = d.Text
	scriptEditorCloseDoc(d)
	if scriptDocIndex(d) >= 0 {
		t.Error("a saved tab should close without a prompt")
	}
}

// Opening the editor and then opening a file should not leave the starter
// buffer behind — but an edited one is work, however little of it there is.
func TestScriptEditorAdoptEmptyTab(t *testing.T) {
	resetScriptDocs(t)
	scriptEditorEnsureDoc()
	starter := activeScript

	opened := namedDoc("scripts/a.flang", "a")
	scriptEditorAdoptEmptyTab(opened)
	scriptEditorShowDoc(opened)
	if scriptDocIndex(starter) >= 0 {
		t.Error("an untouched starter buffer should have been replaced")
	}

	resetScriptDocs(t)
	scriptEditorEnsureDoc()
	edited := activeScript
	edited.Text = scriptStarterText + "\n-- mine\n"

	second := namedDoc("scripts/b.flang", "b")
	scriptEditorAdoptEmptyTab(second)
	scriptEditorShowDoc(second)
	if scriptDocIndex(edited) < 0 {
		t.Error("an edited buffer must not be thrown away by opening a file")
	}
}

func TestScriptEditorCycleTabWraps(t *testing.T) {
	resetScriptDocs(t)
	a := namedDoc("scripts/a.flang", "a")
	b := namedDoc("scripts/b.flang", "b")
	for _, d := range []*scriptDoc{a, b} {
		scriptEditorShowDoc(d)
	}

	scriptEditorShowDoc(a)
	scriptEditorCycleTab(false)
	if activeScript != b {
		t.Error("forwards from the first tab should reach the second")
	}
	scriptEditorCycleTab(false)
	if activeScript != a {
		t.Error("forwards from the last tab should wrap to the first")
	}
	scriptEditorCycleTab(true)
	if activeScript != b {
		t.Error("backwards from the first tab should wrap to the last")
	}
}

// Every document gets its own ImGui identity, or two tabs would share a cursor
// and an undo stack.
func TestScriptDocIDsAreDistinct(t *testing.T) {
	a, b := NewScriptDoc(), NewScriptDoc()
	if a.id == b.id || a.id == 0 {
		t.Errorf("document ids %d and %d are not distinct and non-zero", a.id, b.id)
	}
}

func TestScriptDocTabNameDropsFolders(t *testing.T) {
	if got := namedDoc("scripts/doors/lift.flang", "").TabName(); got != "lift.flang" {
		t.Errorf("TabName = %q, want the base name", got)
	}
	if got := NewScriptDoc().TabName(); got != "(unsaved)" {
		t.Errorf("an unsaved buffer's tab reads %q", got)
	}
}
