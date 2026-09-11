package editor

// Drawing the UI editor's properties panel, for real, with no window and no GL.
//
// The Scripting group is the shared one from editor_script_panel.go, drawn
// inside an undo redirect. A group left open, or a redirect left installed,
// would only show the moment somebody selected a widget — so this draws the
// panel for a widget and for the root, which is the screen's panel.

import (
	"testing"

	"foundry/internal/ui"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// drawUIPropertiesOnce runs one full imgui frame with the properties tab in it.
func drawUIPropertiesOnce() {
	io := imgui.CurrentIO()
	io.SetDisplaySize(imgui.Vec2{X: 1440, Y: 900})
	io.SetDeltaTime(1.0 / 60.0)
	imgui.NewFrame()
	imgui.Begin("props")
	uiPropertiesTab()
	imgui.End()
	imgui.Render()
}

func TestUIPropertiesDrawWithScripting(t *testing.T) {
	uiTestDoc(t)
	btn := ui.NewNode("play", ui.KindButton)
	btn.Script = "scripts/load_button.flang"
	uiDoc.Root.Children = append(uiDoc.Root.Children, btn)
	uiDoc.Script = "scripts/menu.flang"

	for _, sel := range []string{"box", "play", uiDoc.Root.ID} {
		t.Run(sel, func(t *testing.T) {
			uiSel = sel
			withImgui(drawUIPropertiesOnce)
			if editorUndoRedirect != nil {
				t.Error("the properties panel left the undo redirect installed")
			}
		})
	}
}
