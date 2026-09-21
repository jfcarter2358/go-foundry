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
	"os"

	"foundry/internal/editor"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/imguibackend"
	"foundry/internal/pack"
	"foundry/internal/project"
	"foundry/internal/release"
	"foundry/internal/render"
	"foundry/internal/uiwin"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
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

// devRunCommand runs one of the development binary's command-line tools in
// place of the engine, and reports whether args named one. There are two:
// `build-release`, which builds a release of the project this binary is running
// from (internal/release) — headless, so the editor's Release window can run it
// as a child process and a terminal can run it with no editor at all — and
// `find-unused`, which lists the project's assets that nothing uses.
func devRunCommand(args []string) (exitCode int, ran bool) {
	if len(args) == 0 {
		return 0, false
	}
	switch args[0] {
	case release.Command:
		return release.Main(commandProject(), args[1:]), true
	case pack.UnusedCommand:
		return pack.UnusedMain(commandProject(), args[1:], os.Stdout), true
	}
	return 0, false
}

// commandProject is the project a command works on unless told otherwise
// (-root): the one beside the executable, as always, or — for an editor
// installed apart from its projects — the one in the working directory.
func commandProject() string {
	if !project.IsProject(fileutil.ExecutablePath) {
		if wd, err := os.Getwd(); err == nil && project.IsProject(wd) {
			return wd
		}
	}
	return fileutil.ExecutablePath
}

// devPendingProject is the project File > Open Project or New Project asked
// to reopen on, once this process has shut down, or "".
func devPendingProject() string { return editor.PendingProject }

// devInit stands the editor up on the level just loaded, and opens it: a
// development build boots into the 3D view rather than into gameplay, because
// a blank level has nothing to play.
func devInit(level *world.Level, levelPath, levelsDir string) {
	editor.EditorInit(level, levelPath, levelsDir)
	editor.EditorToggle()
}

// devShutdown closes any sub-editor window still open and then tears down
// imgui. Deferred by Run, so it runs on the way out of the frame loop while the
// GL context is still alive — which is what each window's OnClose needs to free
// its own GL objects.
//
// The windows go first, and they go at all, because they are separate OS
// windows: closing the 3D editor has to close the 2D map, the script editor and
// the rest with it rather than leaving them on screen with no frame loop behind
// them.
func devShutdown() {
	// A release build still running has nothing left to show its output to.
	editor.CancelReleaseBuild()
	// Nor a push or pull the Git window started.
	editor.CancelGitOperation()
	uiwin.CloseAllUIWindows()
	imguibackend.ImGuiShutdown()
}

// devToggle is the tilde key.
func devToggle() { editor.EditorToggle() }

// devInterceptQuit takes a script's quit_game away from the process and reports
// that it did. In a development build gameplay is a play session over the
// editor, so the game's Quit button ends the session — the same exit tilde
// takes, snapshot restored — rather than closing the editor and every sub-editor
// window with it. With the editor already open there is no session to end, and
// the request is dropped rather than read as "quit the engine".
//
// The window's own close button is not a script's request and still quits.
func devInterceptQuit() bool {
	if !editor.Editor.Active {
		editor.EditorToggle()
	}
	return true
}

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

// devRenderBrushGhost draws the scenery brush's translucent preview through the
// frame loop's own drawOrQueue, so it sorts against the world's transparent
// surfaces rather than blending over them. Called only when devActive.
func devRenderBrushGhost(draw func(g *gfx.Geometry, model mgl32.Mat4, doubleSided bool)) {
	editor.RenderSceneryGhost(draw)
}

// devDrainWork applies the work the sub-editors deferred out of their own draw
// calls — an Unwrap & Paint asking to open the texture editor on a fresh atlas,
// a model saved in the model editor that the level is wearing, and any window
// waiting to be created. All have to happen here, between frames, with the main
// GL context current.
func devDrainWork() {
	editor.DrainTexEditorRequest()
	editor.DrainModelReloads()
	uiwin.DrainWindowOpens()
}

// devRouteEvent offers an event to the F1 reference and then to any secondary
// window, and reports whether either consumed it.
//
// The reference goes first, ahead of everything, so F1 answers from every mode
// and every window and a sub-editor cannot eat it; it also swallows the Escape
// that closes it, so that Escape is not also read as "cancel".
func devRouteEvent(event sdl.Event) bool {
	editor.NoteInputEvent(event)
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
