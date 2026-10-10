//go:build !release

package render

// debug_overlay.go — the F3 overlay, drawn with imgui.
//
// Split from debug.go so that a release build, which has no imgui in it, can
// answer DebugRenderOverlay with a stub and still keep every counter debug.go
// collects. See debug_overlay_release.go.
//
// What the rows SAY is not here either: DebugOverlayLines builds them, because
// a release build draws the same rows through the runtime's own screen-space
// pass (internal/devui) and the two drawings must not drift.

import (
	"foundry/internal/hooks"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// DebugRenderOverlay draws the debug HUD using ImGui.
// Must be called between ImGuiNewFrame and ImGuiRender.
func DebugRenderOverlay() {
	if !DebugOverlayVisible {
		return
	}

	// Position at top-left with some padding. In the editor the full-width
	// toolbar owns the first 28px, and an overlay parked on top of it would
	// eat the clicks meant for the mode buttons — drop below it instead.
	top := float32(10)
	if hooks.EditorActive() {
		top = 38
	}
	imgui.SetNextWindowPosV(imgui.Vec2{X: 10, Y: top}, imgui.ConditionAlways, imgui.Vec2{})
	imgui.SetNextWindowBgAlpha(0.6)

	flags := imgui.WindowFlagsNoDecoration |
		imgui.WindowFlagsAlwaysAutoResize |
		imgui.WindowFlagsNoSavedSettings |
		imgui.WindowFlagsNoFocusOnAppearing |
		imgui.WindowFlagsNoNav |
		imgui.WindowFlagsNoMove

	if imgui.BeginV("##debug_overlay", nil, flags) {
		for _, l := range DebugOverlayLines() {
			if l.Rule {
				imgui.Separator()
				continue
			}
			imgui.Text(l.Text)
		}
	}
	imgui.End()
}
