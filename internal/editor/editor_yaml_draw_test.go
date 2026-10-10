package editor

// The level YAML editor, driven for real: whole ImGui frames of the window with
// no display and no GL, keys fed through the same SDL filter the window uses
// and characters through ImGui's own input queue. What this covers is the part
// the pure tests in editor_yaml_test.go cannot — the hand-offs between the
// text widget, its callback and the document: an edit made while the widget
// holds the keyboard has to go through the callback, one made while it does
// not has to go straight into the document, and getting either wrong shows up
// as text that silently reverts a frame later.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/game"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// withYAMLEditor runs body with the YAML editor's window state standing in for
// an open window, inside a fresh ImGui context, and puts everything back after.
func withYAMLEditor(t *testing.T, text string, body func(d *yamlDoc)) {
	t.Helper()
	withImgui(func() {
		io := imgui.CurrentIO()
		for _, k := range []struct{ im, sc int }{
			{imgui.KeyEnter, int(sdl.SCANCODE_RETURN)},
			{imgui.KeyBackspace, int(sdl.SCANCODE_BACKSPACE)},
			{imgui.KeyEscape, int(sdl.SCANCODE_ESCAPE)},
			{imgui.KeyTab, int(sdl.SCANCODE_TAB)},
		} {
			io.KeyMap(k.im, k.sc)
		}
		prevWin, prevPath := yamlEditorWindow, Editor.SavePath
		yamlEditorWindow = &uiwin.UIWindow{W: 1000, H: 760}
		Editor.SavePath = ""
		defer func() {
			yamlEditorWindow, Editor.SavePath = prevWin, prevPath
			yamlEd.doc, yamlEd.levelDoc, yamlEd.gameDoc = nil, nil, nil
			sdl.SetModState(0)
		}()
		d := &yamlDoc{Path: filepath.Join(t.TempDir(), "test.yaml"), Text: text, saved: text}
		yamlSwitchTo(d)
		body(d)
	})
}

// yamlFrames draws n whole frames of the window.
func yamlFrames(n int) {
	io := imgui.CurrentIO()
	for i := 0; i < n; i++ {
		io.SetDisplaySize(imgui.Vec2{X: 1000, Y: 760})
		io.SetDeltaTime(1.0 / 60.0)
		imgui.NewFrame()
		drawYAMLEditorWindow()
		imgui.Render()
	}
}

// yamlKey sends a key press through the window's filter and, when the filter
// lets it through, on to ImGui — the order the real event loop uses.
func yamlKey(sc sdl.Scancode, mod sdl.Keymod) {
	sdl.SetModState(mod)
	defer sdl.SetModState(0)
	ev := sdl.KeyboardEvent{Type: sdl.KEYDOWN, Keysym: sdl.Keysym{Scancode: sc}}
	if yamlEditorFilterEvent(ev) {
		return
	}
	io := imgui.CurrentIO()
	io.KeyPress(int(sc))
	yamlFrames(1)
	io.KeyRelease(int(sc))
	yamlEditorHandleEvent(ev)
}

// yamlFocusAt gives the buffer the keyboard with the caret at off.
func yamlFocusAt(t *testing.T, off int) {
	t.Helper()
	yamlQueueOp(yamlSelectOp(off, off))
	yamlFrames(4)
	if !yamlEd.active || yamlEd.cursor != off {
		t.Fatalf("buffer not focused at %d: active %v cursor %d", off, yamlEd.active, yamlEd.cursor)
	}
}

func TestYAMLEditorTypingCompletionUndo(t *testing.T) {
	// A blank line in the first chunk, indented to its keys, to type a key on.
	start := strings.Replace(yamlTestLevel, "  origin:", "  \n  origin:", 1)
	withYAMLEditor(t, start, func(d *yamlDoc) {
		yamlFrames(2)

		at := strings.Index(d.Text, "  \n  origin:") + 2
		yamlFocusAt(t, at)
		imgui.CurrentIO().AddInputCharacters("ro")
		yamlFrames(2)
		if !strings.Contains(d.Text, "  ro\n  origin:") {
			t.Fatalf("typing did not reach the document:\n%s", d.Text)
		}
		if !yamlCompl.Open || len(yamlCompl.Items) == 0 || yamlCompl.Items[0].Text != "rotation" {
			t.Fatalf("completion: open %v items %v", yamlCompl.Open, yamlItemTexts(yamlCompl.Items))
		}

		// Tab accepts. The typed "ro" becomes the key and its colon.
		yamlKey(sdl.SCANCODE_TAB, 0)
		yamlFrames(2)
		if !strings.Contains(d.Text, "  rotation: \n  origin:") {
			t.Fatalf("accepting rotation gave:\n%s", d.Text)
		}
		if yamlCompl.Open {
			t.Error("the list reopened on the row just accepted")
		}

		// Ctrl+Z takes the completion back, then the typing.
		yamlKey(sdl.SCANCODE_Z, sdl.KMOD_LCTRL)
		yamlFrames(2)
		if !strings.Contains(d.Text, "  ro\n  origin:") {
			t.Fatalf("undoing the completion gave:\n%s", d.Text)
		}
		yamlKey(sdl.SCANCODE_Z, sdl.KMOD_LCTRL)
		yamlFrames(2)
		if d.Text != start {
			t.Fatalf("undoing the typing gave:\n%s", d.Text)
		}
		// And Ctrl+Y puts the typing back.
		yamlKey(sdl.SCANCODE_Y, sdl.KMOD_LCTRL)
		yamlFrames(2)
		if !strings.Contains(d.Text, "  ro\n  origin:") {
			t.Fatalf("redo gave:\n%s", d.Text)
		}

		// Escape does not revert the buffer, the way it would ImGui's default.
		yamlKey(sdl.SCANCODE_ESCAPE, 0)
		yamlFrames(2)
		if !strings.Contains(d.Text, "  ro\n  origin:") {
			t.Fatal("Escape reverted the buffer")
		}
	})
}

func TestYAMLEditorEnterAndTab(t *testing.T) {
	withYAMLEditor(t, yamlTestLevel, func(d *yamlDoc) {
		yamlFrames(2)
		// Enter at the end of `  - id: box` opens a line at the key's column.
		at := strings.Index(d.Text, "  - id: box") + len("  - id: box")
		yamlFocusAt(t, at)
		yamlKey(sdl.SCANCODE_RETURN, 0)
		yamlFrames(2)
		if !strings.Contains(d.Text, "  - id: box\n    \n    verts:") {
			t.Fatalf("Enter gave:\n%s", d.Text[:200])
		}
		// Tab indents with spaces; YAML has no tabs.
		yamlKey(sdl.SCANCODE_TAB, 0)
		yamlFrames(2)
		if !strings.Contains(d.Text, "  - id: box\n      \n    verts:") || strings.Contains(d.Text, "\t") {
			t.Fatalf("Tab gave:\n%q", d.Text[:120])
		}
	})
}

func TestYAMLEditorFindReplaceAllUndo(t *testing.T) {
	withYAMLEditor(t, yamlTestLevel, func(d *yamlDoc) {
		yamlFrames(2)
		yamlFindOpen(true)
		yamlEd.find.opts.Query = "id"
		yamlEd.find.opts.Word = true
		yamlEd.find.repl = "ident"
		yamlFrames(2)
		if n := len(yamlEd.find.matches); n != 5 {
			t.Fatalf("%d matches for the word id", n)
		}
		yamlReplaceEverything()
		yamlFrames(2)
		if strings.Contains(d.Text, " id:") || strings.Count(d.Text, "ident:") != 5 {
			t.Fatalf("replace all gave:\n%s", d.Text)
		}

		// Undo with the find bar shut and nothing holding the keyboard: the
		// whole replace comes back as one step.
		yamlFindClose(false)
		yamlFrames(1)
		imgui.CurrentIO().SetMousePosition(imgui.Vec2{X: 999, Y: 759})
		yamlEd.wantFocus = false
		yamlFrames(1)
		yamlKey(sdl.SCANCODE_Z, sdl.KMOD_LCTRL)
		yamlFrames(2)
		if d.Text != yamlTestLevel {
			t.Fatalf("undoing replace all gave:\n%s", d.Text)
		}
	})
}

func TestYAMLEditorSave(t *testing.T) {
	withYAMLEditor(t, yamlTestLevel, func(d *yamlDoc) {
		yamlFrames(2)
		at := strings.Index(d.Text, "  origin:")
		yamlFocusAt(t, at)
		imgui.CurrentIO().AddInputCharacters("  rotation: 2\n")
		yamlFrames(2)
		if !d.Dirty() {
			t.Fatal("typing did not dirty the document")
		}
		yamlKey(sdl.SCANCODE_S, sdl.KMOD_LCTRL)
		got, err := os.ReadFile(d.Path)
		if err != nil || string(got) != d.Text || d.Dirty() {
			t.Fatalf("Ctrl+S: err %v, dirty %v, status %q, text:\n%s", err, d.Dirty(), yamlEd.status, d.Text)
		}

		// A file that would not load is not written.
		imgui.CurrentIO().AddInputCharacters("x: [")
		yamlFrames(2)
		yamlKey(sdl.SCANCODE_S, sdl.KMOD_LCTRL)
		after, _ := os.ReadFile(d.Path)
		if string(after) != string(got) || !yamlEd.statusErr {
			t.Fatalf("a broken file was saved (status %q)", yamlEd.status)
		}
	})
}

func TestYAMLEditorToggleGameManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(fileutil.ResourcesDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	gamePath := game.ProjectPath(root)
	if err := os.WriteFile(gamePath, []byte("title: Foundry\nstart_level: intro\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prevRoot := fileutil.ProjectPath
	fileutil.ProjectPath = root
	t.Cleanup(func() { fileutil.ProjectPath = prevRoot })

	withYAMLEditor(t, yamlTestLevel, func(level *yamlDoc) {
		yamlFrames(2)
		at := strings.Index(level.Text, "chunks:")
		yamlFocusAt(t, at)
		imgui.CurrentIO().AddInputCharacters("#")
		yamlFrames(2)

		yamlShowGame()
		yamlFrames(3)
		g := yamlEd.doc
		if g.Kind != yamlKindGame || g.Path != gamePath || !strings.HasPrefix(g.Text, "title: Foundry") {
			t.Fatalf("game doc: %+v", g)
		}
		yamlFocusAt(t, len(g.Text))
		imgui.CurrentIO().AddInputCharacters("version: 1.2.3")
		yamlFrames(2)

		// Back to the level: its unsaved edit and its caret are where they were.
		yamlShowLevel()
		yamlFrames(3)
		if yamlEd.doc != level || !strings.Contains(level.Text, "#chunks:") {
			t.Fatalf("level lost its edit:\n%s", level.Text)
		}
		if yamlEd.cursor != at+1 {
			t.Errorf("level caret %d, want %d", yamlEd.cursor, at+1)
		}

		// And game.yaml saves on its own, without touching the level's file.
		yamlShowGame()
		yamlFrames(2)
		yamlEditorSave()
		data, _ := os.ReadFile(gamePath)
		if !strings.Contains(string(data), "version: 1.2.3") {
			t.Errorf("game.yaml on disk:\n%s (status %q)", data, yamlEd.status)
		}
		if !level.Dirty() {
			t.Error("saving game.yaml saved the level")
		}
	})
}
