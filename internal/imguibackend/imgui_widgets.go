package imguibackend

// imgui_widgets.go — ImGui idioms shared by every panel in the program.
//
// These are here rather than in editor_ui.go because the texture editor uses
// them too, and it is not part of the level editor. A widget helper that two
// unrelated windows both need belongs below both of them; one that only the
// level editor uses belongs in editor_ui.go, which is where the rest still are.

import "github.com/inkyblackness/imgui-go/v4"

// EditorPushDisabled visually dims and blocks input on the widgets drawn
// until the matching EditorPopDisabled. This imgui-go build has the
// BeginDisabled/EndDisabled API commented out, so combine PushItemFlag
// (blocks interaction) with a dimmed alpha (visual feedback) instead.
func EditorPushDisabled(disabled bool) {
	imgui.PushItemFlag(imgui.ItemFlagsDisabled, disabled)
	alpha := float32(1.0)
	if disabled {
		alpha = 0.4
	}
	imgui.PushStyleVarFloat(imgui.StyleVarAlpha, alpha)
}

func EditorPopDisabled() {
	imgui.PopStyleVar()
	imgui.PopItemFlag()
}

// WithModalPopup draws an auto-resizing modal popup titled title, opening it
// (once) when *show is true. show is cleared immediately so the OpenPopup
// call fires exactly once per request; body is only invoked while the popup
// is actually open. This is the "if show { OpenPopup }; if Begin { body;
// End }" shape every popup in the editor and texture editor used to
// duplicate individually.
func WithModalPopup(title string, show *bool, body func()) {
	if show != nil && *show {
		imgui.OpenPopup(title)
		*show = false
	}
	if imgui.BeginPopupModalV(title, nil, imgui.WindowFlagsAlwaysAutoResize) {
		body()
		imgui.EndPopup()
	}
}
