package textureui

import (
	"testing"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// texture_editor_shortcut_test.go — the texture editor's keyboard shortcuts.
//
// Here rather than with the rest of the texture editor's tests because the
// mapping lives in texture_editor_ui.go, which is the ImGui window and stays
// out of internal/texture along with everything else that draws.

func TestShortcutMapping(t *testing.T) {
	cases := []struct {
		name                string
		sc                  sdl.Scancode
		ctrl, shift, typing bool
		want                texEditorShortcut
	}{
		{"ctrl+z undoes", sdl.SCANCODE_Z, true, false, false, texShortcutUndo},
		{"ctrl+shift+z redoes", sdl.SCANCODE_Z, true, true, false, texShortcutRedo},
		{"ctrl+y redoes", sdl.SCANCODE_Y, true, false, false, texShortcutRedo},
		{"bare z paints nothing", sdl.SCANCODE_Z, false, false, false, texShortcutNone},
		{"ctrl+z in a text field is the field's", sdl.SCANCODE_Z, true, false, true, texShortcutNone},
		{"unrelated chord", sdl.SCANCODE_A, true, false, false, texShortcutNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := texEditorShortcutFor(c.sc, c.ctrl, c.shift, c.typing); got != c.want {
				t.Errorf("texEditorShortcutFor = %v, want %v", got, c.want)
			}
		})
	}
}

// The imgui key enum is not an SDL scancode: this backend feeds io.KeysDown by
// scancode, so a shortcut written against imgui.KeyZ watches whatever key
// happens to sit at that scancode instead. Pin the two apart so a future
// rewrite back to imgui key polling has to notice.
func TestImguiKeyEnumIsNotAScancode(t *testing.T) {
	if int(imgui.KeyZ) == int(sdl.SCANCODE_Z) {
		t.Skip("imgui.KeyZ happens to equal SCANCODE_Z in this imgui-go version")
	}
	if texEditorShortcutFor(sdl.Scancode(imgui.KeyZ), true, false, false) == texShortcutUndo {
		t.Error("imgui.KeyZ used as a scancode resolved to undo; the mapping is scancode-based")
	}
}
