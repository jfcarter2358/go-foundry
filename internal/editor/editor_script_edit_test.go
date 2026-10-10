package editor

import (
	"foundry/internal/imguibackend"
	"testing"
)

// The buffer's editing operations, checked without a window: what each one does
// to the text, and where it leaves the caret. The ImGui side only carries these
// answers across, so this is where the behaviour is actually pinned down.

func TestScriptEditCopyLeavesBufferAlone(t *testing.T) {
	const text = "log(\"hi\")\n"
	e, ok := scriptEditFor(scriptEditCopy, text, scriptSel{Start: 4, End: 8}, "")
	if !ok {
		t.Fatal("copy of a selection reported nothing to do")
	}
	if !e.SetClip || e.Clip != "\"hi\"" {
		t.Errorf("clipboard = %q (set=%v), want %q", e.Clip, e.SetClip, "\"hi\"")
	}
	if got := scriptApplyText(text, e); got != text {
		t.Errorf("copy changed the buffer: %q", got)
	}
	// The selection survives, so copying twice copies the same thing twice.
	if e.Sel != (scriptSel{Start: 4, End: 8}) {
		t.Errorf("selection = %+v, want 4..8", e.Sel)
	}
}

func TestScriptEditCut(t *testing.T) {
	const text = "abcdef"
	e, ok := scriptEditFor(scriptEditCut, text, scriptSel{Start: 2, End: 4}, "")
	if !ok {
		t.Fatal("cut reported nothing to do")
	}
	if e.Clip != "cd" || !e.SetClip {
		t.Errorf("clipboard = %q, want %q", e.Clip, "cd")
	}
	if got := scriptApplyText(text, e); got != "abef" {
		t.Errorf("text = %q, want %q", got, "abef")
	}
	if e.Sel != (scriptSel{Start: 2, End: 2}) {
		t.Errorf("caret = %+v, want collapsed at 2", e.Sel)
	}
}

func TestScriptEditDeleteDoesNotTouchClipboard(t *testing.T) {
	e, ok := scriptEditFor(scriptEditDelete, "abcdef", scriptSel{Start: 1, End: 3}, "")
	if !ok {
		t.Fatal("delete reported nothing to do")
	}
	if e.SetClip {
		t.Error("delete wrote to the clipboard")
	}
	if got := scriptApplyText("abcdef", e); got != "adef" {
		t.Errorf("text = %q, want %q", got, "adef")
	}
}

func TestScriptEditPasteReplacesSelection(t *testing.T) {
	const text = "log(old)\n"
	e, ok := scriptEditFor(scriptEditPaste, text, scriptSel{Start: 4, End: 7}, "new")
	if !ok {
		t.Fatal("paste reported nothing to do")
	}
	if got := scriptApplyText(text, e); got != "log(new)\n" {
		t.Errorf("text = %q, want %q", got, "log(new)\n")
	}
	if e.Sel != (scriptSel{Start: 7, End: 7}) {
		t.Errorf("caret = %+v, want collapsed at 7 (after the pasted text)", e.Sel)
	}
}

func TestScriptEditPasteAtCaretInserts(t *testing.T) {
	e, ok := scriptEditFor(scriptEditPaste, "ab", scriptSel{Start: 1, End: 1}, "XY")
	if !ok {
		t.Fatal("paste reported nothing to do")
	}
	if got := scriptApplyText("ab", e); got != "aXYb" {
		t.Errorf("text = %q, want %q", got, "aXYb")
	}
}

// Text copied out of a Windows program arrives with CRLF, and a stray carriage
// return is an invisible parse error — so paste normalises before it splices.
func TestScriptEditPasteNormalizesLineEndings(t *testing.T) {
	e, ok := scriptEditFor(scriptEditPaste, "", scriptSel{}, "a\r\nb\rc")
	if !ok {
		t.Fatal("paste reported nothing to do")
	}
	if got := scriptApplyText("", e); got != "a\nb\nc" {
		t.Errorf("text = %q, want %q", got, "a\nb\nc")
	}
	if e.Sel.Start != len("a\nb\nc") {
		t.Errorf("caret = %d, want %d (end of the normalised text)", e.Sel.Start, len("a\nb\nc"))
	}
}

func TestScriptEditSelectAll(t *testing.T) {
	const text = "one\ntwo\n"
	e, ok := scriptEditFor(scriptEditSelectAll, text, scriptSel{Start: 2, End: 2}, "")
	if !ok {
		t.Fatal("select all reported nothing to do")
	}
	if e.touchesBuffer() {
		t.Error("select all spliced the buffer")
	}
	if e.Sel != (scriptSel{Start: 0, End: len(text)}) {
		t.Errorf("selection = %+v, want the whole buffer", e.Sel)
	}
}

// Cut and Copy want a real selection rather than falling back to the whole
// buffer the way ImGui's own Ctrl+C does — the menu greys them out instead.
func TestScriptEditNothingToDo(t *testing.T) {
	cases := []struct {
		name string
		op   scriptEditOp
		sel  scriptSel
		clip string
	}{
		{"cut with no selection", scriptEditCut, scriptSel{Start: 3, End: 3}, ""},
		{"copy with no selection", scriptEditCopy, scriptSel{Start: 3, End: 3}, ""},
		{"delete with no selection", scriptEditDelete, scriptSel{Start: 3, End: 3}, ""},
		{"paste with an empty clipboard", scriptEditPaste, scriptSel{Start: 3, End: 3}, ""},
		{"no operation at all", scriptEditNone, scriptSel{Start: 0, End: 4}, "x"},
	}
	for _, c := range cases {
		if _, ok := scriptEditFor(c.op, "abcdef", c.sel, c.clip); ok {
			t.Errorf("%s: reported something to do", c.name)
		}
	}
}

// ImGui reports the selection in the direction it was dragged, and its bounds
// can outrun a buffer that shrank under it.
func TestScriptSelOrdered(t *testing.T) {
	cases := []struct {
		in, want scriptSel
		N        int
	}{
		{scriptSel{Start: 5, End: 2}, scriptSel{Start: 2, End: 5}, 10},
		{scriptSel{Start: -3, End: 4}, scriptSel{Start: 0, End: 4}, 10},
		{scriptSel{Start: 2, End: 99}, scriptSel{Start: 2, End: 10}, 10},
		{scriptSel{Start: 8, End: 9}, scriptSel{Start: 4, End: 4}, 4},
	}
	for _, c := range cases {
		if got := c.in.ordered(c.N); got != c.want {
			t.Errorf("%+v.ordered(%d) = %+v, want %+v", c.in, c.N, got, c.want)
		}
	}
}

// A backwards selection has to survive the round trip too, since that is how
// half of all selections are made.
func TestScriptEditBackwardsSelection(t *testing.T) {
	e, ok := scriptEditFor(scriptEditCut, "abcdef", scriptSel{Start: 4, End: 2}, "")
	if !ok {
		t.Fatal("cut reported nothing to do")
	}
	if e.Clip != "cd" {
		t.Errorf("clipboard = %q, want %q", e.Clip, "cd")
	}
	if got := scriptApplyText("abcdef", e); got != "abef" {
		t.Errorf("text = %q, want %q", got, "abef")
	}
}

func TestNormalizeClipboardText(t *testing.T) {
	cases := map[string]string{
		"plain":        "plain",
		"a\r\nb":       "a\nb",
		"a\rb":         "a\nb",
		"a\r\n\r\nb":   "a\n\nb",
		"trailing\r\n": "trailing\n",
	}
	for in, want := range cases {
		if got := imguibackend.NormalizeClipboardText(in); got != want {
			t.Errorf("NormalizeClipboardText(%q) = %q, want %q", in, got, want)
		}
	}
}
