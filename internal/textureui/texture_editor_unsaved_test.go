package textureui

import (
	"testing"

	"foundry/internal/texture"
)

func unsavedTestDoc(t *testing.T, bound string, dirty bool) *texture.TexEditorState {
	t.Helper()
	ed, err := texture.NewTexEditorDoc("t", 8, 8)
	if err != nil {
		t.Fatal(err)
	}
	ed.BoundTexture = bound
	if dirty {
		ed.Doc.MarkDirty()
	}
	return ed
}

func resetUnsavedState(t *testing.T) {
	prev := ActiveTexEditor
	t.Cleanup(func() {
		ActiveTexEditor = prev
		texEditorShowUnsaved = false
		texEditorUnsavedThen = nil
		texEditorSaveAsThen = nil
	})
}

// Nothing to lose, nothing to ask: the action runs at once.
func TestConfirmDiscardCleanRunsNow(t *testing.T) {
	resetUnsavedState(t)
	ActiveTexEditor = unsavedTestDoc(t, "a", false)
	ran := false
	texEditorConfirmDiscard(func() { ran = true })
	if !ran || texEditorShowUnsaved {
		t.Fatalf("ran=%v prompt=%v; want ran, no prompt", ran, texEditorShowUnsaved)
	}
}

// Unsaved changes hold the action behind the prompt.
func TestConfirmDiscardDirtyPrompts(t *testing.T) {
	resetUnsavedState(t)
	ActiveTexEditor = unsavedTestDoc(t, "a", true)
	ran := false
	texEditorConfirmDiscard(func() { ran = true })
	if ran || !texEditorShowUnsaved || texEditorUnsavedThen == nil {
		t.Fatalf("ran=%v prompt=%v; want held behind the prompt", ran, texEditorShowUnsaved)
	}
}

// Switching away from a dirty document asks; re-adopting the same texture
// (Re-unwrap, which has just rewritten its file) does not.
func TestAdoptDocPromptsOnlyForAnotherTexture(t *testing.T) {
	resetUnsavedState(t)
	old := unsavedTestDoc(t, "a", true)

	ActiveTexEditor = old
	TexEditorAdoptDoc(unsavedTestDoc(t, "b", false))
	if ActiveTexEditor != old || !texEditorShowUnsaved {
		t.Fatal("adopting another texture over unsaved changes did not prompt")
	}

	texEditorShowUnsaved, texEditorUnsavedThen = false, nil
	same := unsavedTestDoc(t, "a", false)
	TexEditorAdoptDoc(same)
	if ActiveTexEditor != same || texEditorShowUnsaved {
		t.Fatal("re-adopting the same texture should replace it without a prompt")
	}
}
