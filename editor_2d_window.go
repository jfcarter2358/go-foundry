package main

// editor_2d_window.go — the 2D sector editor, which lives in an OS window of
// its own, the same way the texture editor does (texture_editor_ui.go).
//
// Motivation: the 2D map used to be drawn on top of the live 3D render in the
// main window, so only one of the two views could be looked at and Tab flipped
// between them. It now lives here and nowhere else: the map gets its own
// window cleared to black, the main window is always the 3D view, and both are
// usable side by side on two screens.
//
// Nothing here duplicates the 2D editor itself — the window reuses
// Editor2DRender / Editor2DPanel / Editor2DHandleClick unchanged. What it
// does supply is the three things those functions implicitly took from the
// main window:
//
//   - viewport size: editor2DViewportSize feeds editor2DView (editor_2d.go)
//     and the side panel, so screen↔world math uses this window's pixels;
//   - a GL context of its own for the gizmo line renderer (UIWindow.GizmoVAO);
//   - Editor.Mode: the shared handlers branch on it, so the window brackets
//     everything it calls in withEditor2DMode.

import (
	"fmt"
	"foundry/internal/gfx"
	"foundry/internal/platform"
	"foundry/internal/uiwin"
	"log"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// editor2DWindow is the 2D sector editor's window; nil when it is not open,
// which is also when there is no 2D editing going on at all.
var editor2DWindow *uiwin.UIWindow

const (
	editor2DWindowW = int32(1280)
	editor2DWindowH = int32(800)
)

// editor2DViewportSize returns the pixel size of the surface the 2D map is
// drawn into — this window, whenever it is open. The main-window fallback is
// only there for the headless tests, which drive the 2D handlers directly
// without ever opening a window.
func editor2DViewportSize() (w, h float32) {
	if editor2DWindow != nil {
		return float32(editor2DWindow.W), float32(editor2DWindow.H)
	}
	return float32(platform.WinWidth), float32(platform.WinHeight)
}

// Editor2DPoppedOut reports whether the 2D sector editor is open, which — now
// that the map has no home in the main window — is the same question as
// whether the 2D editor is available at all.
func Editor2DPoppedOut() bool { return editor2DWindow != nil }

// OpenEditor2DWindow opens the 2D sector editor in its own window, or raises
// it if it is already open. This is the only way into the 2D map.
func OpenEditor2DWindow() {
	if editor2DWindow != nil {
		editor2DWindow.Win.Raise()
		return
	}
	w, err := uiwin.NewUIWindow("2D Sector Editor", editor2DWindowW, editor2DWindowH,
		drawEditor2DWindow, onEditor2DWindowClose)
	if err != nil {
		log.Printf("open 2D editor window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0, 0, 0, 1}
	w.HandleEvent = editor2DWindowHandleEvent
	editor2DWindow = w

	// Open the map on whatever the 3D camera is looking at, the way Tab used
	// to when the two views shared the main window.
	Editor.Pan2D = mgl32.Vec2{gfx.Cam.Position.X(), gfx.Cam.Position.Z()}
}

// CloseEditor2DWindow asks for the 2D editor to go away at the end of the
// frame; the main window keeps showing the 3D view either way. Safe to call
// from inside the window's own Draw (see UIWindow.CloseRequested).
func CloseEditor2DWindow() {
	if editor2DWindow != nil {
		editor2DWindow.CloseRequested = true
	}
}

// onEditor2DWindowClose runs when the window goes away — via the "Close"
// button, the OS close button, or the editor being switched off. There is
// nothing to hand back: the main window was already in 3D and stays there.
func onEditor2DWindowClose() {
	editor2DWindow = nil
}

// withEditor2DMode runs fn with Editor.Mode forced to Mode2D, restoring the
// previous mode afterwards. Every shared editor entry point this window calls
// (panels, click handlers, delete) branches on Editor.Mode, and Mode2D is this
// window's alone — the main window stays in Mode3D throughout.
//
// If fn leaves a *different* mode behind, it wasn't stale state — it was a
// panel button asking for another workspace ("Edit Floor Surface", "Switch to
// 3D Editor"). Those all render in the main window, so the request is handed
// to it rather than undone.
//
// Outside these brackets Mode2D is never the main window's mode; see
// editorSyncMainWindowMode.
func withEditor2DMode(fn func()) {
	prev := Editor.Mode
	Editor.Mode = Mode2D
	defer func() {
		if Editor.Mode != Mode2D {
			platform.Window.Raise()
			return
		}
		Editor.Mode = prev
	}()
	fn()
}

// editorSyncMainWindowMode keeps the main window out of Mode2D. The map is
// this window's alone — the main window has no code left to draw or drive it,
// and the screen↔world math (editor2DViewportSize) answers for this one — so a
// main window in Mode2D would show a blank 3D view with no side panel.
//
// Called at the top of the main window's per-frame editor entry points, so
// every path that sets Mode2D behind our back (undo restoring a mode, leaving
// the surface editor into the mode it was entered from) lands back in 3D
// rather than having to be individually taught the rule.
func editorSyncMainWindowMode() {
	if Editor.Mode == Mode2D {
		Editor.Mode = Mode3D
	}
}

// withMainGLContext runs fn with the *main* window's GL context current, then
// hands this window's back.
//
// Editing from this window is not purely CPU work: dragging a vertex, drawing
// a sector, painting a biome or undoing any of it rebuilds level geometry,
// and Geometry.Build generates a VAO (geometry.go). VAOs are the one class of
// GL object a shared context does not share (see window.go), so a VAO born in
// this window's context would be an invalid name in the 3D view that has to
// draw it. Everything that can mutate the level therefore runs against the
// main context; only this window's own line/imgui drawing uses its own.
//
// The imgui context is left alone — it is independent of the GL one, so
// WantCaptureMouse/Keyboard inside fn still refer to this window's UI.
func withMainGLContext(fn func()) {
	w := editor2DWindow
	if w == nil {
		fn()
		return
	}
	platform.Window.GLMakeCurrent(platform.Context)
	defer w.Win.GLMakeCurrent(w.GL)
	fn()
}

// editor2DWindowFocused reports whether this window currently owns keyboard
// input, which is what decides whether WASD pans the map here or flies the
// camera in the main window.
func editor2DWindowFocused() bool {
	return editor2DWindow != nil &&
		editor2DWindow.Win.GetFlags()&sdl.WINDOW_INPUT_FOCUS != 0
}

// ---------- per-frame ----------

// drawEditor2DWindow renders one frame of the 2D editor: the map through the
// gizmo line renderer, then this window's ImGui layer. Called by
// RenderSecondaryWindows with the window's GL/imgui contexts current and its
// framebuffer already cleared to black.
func drawEditor2DWindow() {
	if Editor.level == nil || !Editor.Active {
		return
	}
	withEditor2DMode(func() {
		editor2DWindowPan()

		// Map geometry first, so the ImGui panels below land on top of it.
		// This is the only part that draws into *this* window's framebuffer.
		gl.Disable(gl.DEPTH_TEST)
		Editor2DRender()
		gl.Enable(gl.DEPTH_TEST)

		// The panels only record imgui draw lists here, but their widgets can
		// rebuild level geometry as a side effect — that has to happen on the
		// main context. See withMainGLContext.
		withMainGLContext(func() {
			editor2DWindowToolbar()
			Editor2DPanel()
			Editor2DRenderBiomeOverlay()
			Editor2DRenderLabels()
			// Last, so the popup opens over the panel as well as the map.
			Editor2DContextMenu()
		})
	})
}

// editor2DWindowPan applies WASD panning while this window has focus.
// EditorTick flies the main window's camera on the same keys, but it skips
// input entirely when the main window is unfocused, so the two never both act
// on a keypress.
func editor2DWindowPan() {
	if !editor2DWindowFocused() || imgui.CurrentIO().WantCaptureKeyboard() || platform.KeyStates == nil {
		return
	}
	panSpeed := float32(5.0) / Editor.Zoom2D * 10
	if platform.KeyStates[sdl.SCANCODE_W] != 0 {
		Editor.Pan2D[1] -= panSpeed
	}
	if platform.KeyStates[sdl.SCANCODE_S] != 0 {
		Editor.Pan2D[1] += panSpeed
	}
	if platform.KeyStates[sdl.SCANCODE_A] != 0 {
		Editor.Pan2D[0] -= panSpeed
	}
	if platform.KeyStates[sdl.SCANCODE_D] != 0 {
		Editor.Pan2D[0] += panSpeed
	}
}

// editor2DWindowToolbar draws this window's slim top bar. The main window's
// toolbar keeps owning level-wide actions (save/load, the other editors); this
// one only carries what is 2D-specific plus the way out.
func editor2DWindowToolbar() {
	w := editor2DWindow
	if w == nil {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: 0})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W), Y: 28})
	imgui.BeginV("##editor2d_toolbar", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|
			imgui.WindowFlagsNoMove|imgui.WindowFlagsNoScrollbar)

	toolbarY := imgui.CursorPosY()

	if ch := activeChunk(); ch != nil {
		imgui.Text(fmt.Sprintf("Chunk: %s", ch.ID))
	} else {
		imgui.Text("Chunk: (none selected)")
	}
	imgui.SameLine()
	imgui.Text(fmt.Sprintf("| Zoom: %.0f px/unit", Editor.Zoom2D))
	imgui.SameLine()
	imgui.Text("| Shift+click: add point  Ctrl+click: multi-select  J: join  RMB: menu")

	imgui.SameLine()
	imgui.Separator()
	for _, mode := range editorSelModes {
		m := mode
		imgui.SameLine()
		if editorActiveToggleButton(
			fmt.Sprintf("%s [%s]##selmode2dwin", m, editorSelModeKey(m)), Editor.SelMode == m) {
			editorSetSelMode(m)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(editorSelModeTooltip(m))
		}
	}
	imgui.SameLine()
	imgui.Separator()
	editorGizmoToolButtons()

	const closeBtnWidth = 90
	imgui.SetCursorPos(imgui.Vec2{X: float32(w.W) - closeBtnWidth - 8, Y: toolbarY})
	if imgui.ButtonV("Close", imgui.Vec2{X: closeBtnWidth, Y: 0}) {
		CloseEditor2DWindow()
	}

	imgui.End()
}

// ---------- input ----------

// editor2DWindowHandleEvent is this window's input path: what used to be the
// Mode2D branches of EditorHandleEvent (editor.go), minus everything that
// belongs to the main window's modes. Runs with this window's imgui context current, so the
// WantCapture* checks refer to its own panels.
func editor2DWindowHandleEvent(event sdl.Event) {
	w := editor2DWindow
	if w == nil || Editor.level == nil {
		return
	}
	// The side panel's drag-to-resize edge takes priority over map input.
	if panelHandleResizeAt(event, w.W) {
		return
	}

	// Every branch below can mutate the level (and so rebuild geometry).
	withEditor2DMode(func() { withMainGLContext(func() { editor2DWindowInput(event) }) })
}

// editor2DWindowInput is editor2DWindowHandleEvent's body, running with
// Mode2D forced and the main GL context current.
func editor2DWindowInput(event sdl.Event) {
	io := imgui.CurrentIO()

	switch t := event.(type) {
	case sdl.KeyboardEvent:
		// Biome polygon placement intercepts keys first, as in the main window.
		if biomeHandleKey(t) {
			return
		}
		if t.Type != sdl.KEYDOWN || io.WantCaptureKeyboard() {
			return
		}
		mods := sdl.GetModState()
		if mods&sdl.KMOD_CTRL != 0 {
			switch t.Keysym.Scancode {
			case sdl.SCANCODE_Z:
				if mods&sdl.KMOD_SHIFT != 0 {
					EditorRedo()
				} else {
					EditorUndo()
				}
				return
			case sdl.SCANCODE_Y:
				EditorRedo()
				return
			}
		}
		switch t.Keysym.Scancode {
		case sdl.SCANCODE_DELETE:
			EditorDeleteSelected()
		// The selection unit and the gizmo tool, on the same keys the main
		// window binds them to (editor.go). This window has its own event loop,
		// so without these the map would show modes it could not switch.
		case sdl.SCANCODE_1:
			editorSetSelMode(editorSelPoint)
		case sdl.SCANCODE_2:
			editorSetSelMode(editorSelEdge)
		case sdl.SCANCODE_3:
			editorSetSelMode(editorSelFace)
		case sdl.SCANCODE_J:
			EditorJoinSelectedPoints(mods&sdl.KMOD_SHIFT != 0)
		case sdl.SCANCODE_G:
			editorSetGizmoTool(editorToolMove)
		case sdl.SCANCODE_R:
			editorSetGizmoTool(editorToolRotate)
		case sdl.SCANCODE_C:
			editorSetGizmoTool(editorToolScale)
		}

	case sdl.MouseButtonEvent:
		if io.WantCaptureMouse() {
			return
		}
		if t.Type == sdl.MOUSEBUTTONDOWN {
			Editor2DHandleClick(t)
		} else if t.Type == sdl.MOUSEBUTTONUP {
			Editor2DHandleRelease(t)
		}

	case sdl.MouseMotionEvent:
		Editor2DHandleMouseMove(t)

	case sdl.MouseWheelEvent:
		if io.WantCaptureMouse() {
			return
		}
		Editor2DZoom(t.Y)
	}
}
