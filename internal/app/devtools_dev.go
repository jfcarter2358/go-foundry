//go:build !release

package app

// devtools_dev.go — the development build's half of the editor seam.
//
// internal/editor, internal/textureui, internal/imguibackend and internal/uiwin
// are imported from exactly one place in the engine: this file. Everything in
// internal/app that used to reach for them now goes through the wrappers below,
// and devtools_release.go answers the same set with stubs — which is what lets
// `go build -tags release` drop the editor, the four sub-editors and the whole
// of imgui out of the binary without a single #ifdef in the frame loop.
//
// The wrappers are deliberately thin and deliberately boring. Anything with
// judgement in it belongs on the far side of the seam, in the package that owns
// the thing, so that the two files here stay a pair that can be read side by
// side and checked for agreement at a glance.

import (
	"foundry/internal/editor"
	"foundry/internal/imguibackend"
	"foundry/internal/render"
	"foundry/internal/uiwin"
	"foundry/internal/world"

	"github.com/veandco/go-sdl2/sdl"
)

// devAvailable says whether this binary has an editor in it at all. Read by the
// startup path, which has a different level to open and a different window to
// title depending on the answer.
const devAvailable = true

// devActive reports whether the editor owns the screen, the camera and the
// input this frame. It is the gate on gameplay, on the runtime UI, on the
// script tick and on drawing the authoring-only overlays.
func devActive() bool { return editor.Editor.Active }

// devWireframe reports the 3D editor's wireframe shading, which the frame loop
// has to know about because it changes the polygon mode around the whole world
// pass and skips the scatter and displacement passes.
func devWireframe() bool {
	return editor.Editor.Active && editor.Editor.Mode == editor.Mode3D && editor.Editor.Wireframe
}

// devInit stands the editor up on the level just loaded, and opens it: a
// development build boots into the 3D view rather than into gameplay, because
// a blank level has nothing to play.
func devInit(level *world.Level, levelPath, levelsDir string) {
	editor.EditorInit(level, levelPath, levelsDir)
	editor.EditorToggle()
}

// devShutdown tears down imgui. Deferred by Run, so it runs on the way out of
// the frame loop while the GL context is still alive.
func devShutdown() { imguibackend.ImGuiShutdown() }

// devToggle is the tilde key.
func devToggle() { editor.EditorToggle() }

// devTick runs the editor's camera and tools for this frame.
func devTick(keyStates []uint8) { editor.EditorTick(keyStates) }

// devRender draws the editor's overlays and its imgui UI.
func devRender() { editor.EditorRender() }

// devHandleEvent gives one SDL event to the editor.
func devHandleEvent(event sdl.Event) { editor.EditorHandleEvent(event) }

// devEndDrags ends any drag in progress, for the focus-lost case where the
// button-up that would have ended it goes to whatever took focus instead.
func devEndDrags() { editor.EditorEndDrags() }

// devAdoptLoadedLevel re-points the editor at a level that has just replaced
// the one its selections were naming.
func devAdoptLoadedLevel() { editor.EditorAdoptLoadedLevel() }

// devRenderMarkers draws the authoring-only markers for objects with no mesh of
// their own. Called only when devActive.
func devRenderMarkers(lvl *world.Level, cull *render.Frustum) {
	editor.RenderEntityMarkers(lvl, cull)
}

// devDrainWork applies the work the sub-editors deferred out of their own draw
// calls — a model editor asking to open the texture editor on a fresh atlas,
// and any window waiting to be created. Both have to happen here, between
// frames, with the main GL context current.
func devDrainWork() {
	editor.DrainModelTexEditorRequest()
	uiwin.DrainWindowOpens()
}

// devRouteEvent offers an event to the F1 reference and then to any secondary
// window, and reports whether either consumed it.
//
// The reference goes first, ahead of everything, so F1 answers from every mode
// and every window and a sub-editor cannot eat it; it also swallows the Escape
// that closes it, so that Escape is not also read as "cancel".
func devRouteEvent(event sdl.Event) bool {
	if uiwin.KeybindHelpHandleEvent(event) {
		return true
	}
	return uiwin.RouteWindowEvent(event, uiwin.MainWindowID)
}

// devSetMainWindowID tells the secondary-window router which window is the
// main one, so it can leave that window's events alone.
func devSetMainWindowID(id uint32) { uiwin.MainWindowID = id }

// devUpdateFocus points the F1 reference at the focused window before anything
// draws.
func devUpdateFocus() { uiwin.KeybindHelpUpdateFocus(uiwin.MainWindowID) }

// devRenderOverlays draws the debug overlay and the F1 reference, both of which
// are imgui and both of which are independent of the editor. Called only when
// the editor is closed — when it is open, EditorRender has already drawn them
// inside its own imgui frame.
func devRenderOverlays(dt float32) {
	if !render.DebugOverlayVisible && !uiwin.KeybindHelpVisibleIn(uiwin.MainWindowID) {
		return
	}
	imguibackend.ImGuiNewFrame(dt)
	render.DebugRenderOverlay()
	uiwin.KeybindHelpRender()
	imguibackend.ImGuiRender()
}

// devRenderSecondaryWindows draws the sub-editor windows, each in its own
// GL/imgui context. Last in the frame, after the main window's swap, because it
// leaves the main contexts current again on the way out.
func devRenderSecondaryWindows(dt float32) { uiwin.RenderSecondaryWindows(dt) }
