package main

// window.go — secondary OS windows sharing the main GL context.
//
// SDL_GL_SHARE_WITH_CURRENT_CONTEXT makes GL programs, buffers and textures
// shared across contexts; VAOs are never shared, hence the per-window
// VAO/VBO/EBO here. imguiShaderProgram and imguiFontTexture (editor_imgui.go)
// stay package globals since they ARE shared GL objects, reused by every
// window's renderer, and every imgui.Context shares the same default font
// atlas (imgui-go v4 Context.go), so a secondary context's font glyphs sample
// that same texture correctly.
//
// Frame order lives in main.go: the main window renders and swaps first,
// then RenderSecondaryWindows runs each open UIWindow in turn, restoring the
// main GL/imgui context before returning so nothing else in the frame loop
// has to know secondary windows exist.

import (
	"fmt"

	"github.com/go-gl/gl/v4.1-core/gl"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// UIWindow is one secondary OS window with its own SDL window, GL context,
// and imgui context.
type UIWindow struct {
	Win           *sdl.Window
	GL            sdl.GLContext
	Ctx           *imgui.Context
	VAO, VBO, EBO uint32
	ID            uint32
	W, H          int32
	Draw          func() // per-frame UI body; called with this window's imgui context current
	OnClose       func()
}

// uiWindows holds every open secondary window, keyed by SDL window ID so
// event routing (main.go/input.go) and per-frame rendering can look them up
// directly.
var uiWindows = map[uint32]*UIWindow{}

// NewUIWindow creates a new OS window sharing the main GL context, its own
// imgui context, and per-window VAO/VBO/EBO. Must be called with the main GL
// context current; restores it before returning. draw is invoked once per
// frame from RenderSecondaryWindows.
func NewUIWindow(title string, w, h int32, draw func(), onClose func()) (*UIWindow, error) {
	if err := sdl.GLSetAttribute(sdl.GL_SHARE_WITH_CURRENT_CONTEXT, 1); err != nil {
		return nil, fmt.Errorf("GLSetAttribute(SHARE_WITH_CURRENT_CONTEXT): %w", err)
	}

	win, err := sdl.CreateWindow(title, sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED,
		w, h, sdl.WINDOW_OPENGL|sdl.WINDOW_RESIZABLE)
	if err != nil {
		return nil, fmt.Errorf("sdl.CreateWindow(%q): %w", title, err)
	}

	glCtx, err := win.GLCreateContext()
	if err != nil {
		win.Destroy()
		return nil, fmt.Errorf("GLCreateContext for %q: %w", title, err)
	}

	id, err := win.GetID()
	if err != nil {
		sdl.GLDeleteContext(glCtx)
		win.Destroy()
		return nil, fmt.Errorf("GetID for %q: %w", title, err)
	}

	// win.GLCreateContext leaves glCtx current; imgui/device-object setup
	// below runs against it. Pass the main context's already-built font
	// atlas explicitly — see sharedFontAtlas (editor_imgui.go) for why nil
	// asserts here despite the imgui-go doc comment suggesting it wouldn't.
	ctx := imgui.CreateContext(&sharedFontAtlas)
	ctx.SetCurrent()
	io := imgui.CurrentIO()
	io.SetIniFilename("")
	mapImGuiKeys(io)

	uw := &UIWindow{Win: win, GL: glCtx, Ctx: ctx, ID: id, W: w, H: h, Draw: draw, OnClose: onClose}

	gl.GenBuffers(1, &uw.VBO)
	gl.GenBuffers(1, &uw.EBO)
	gl.GenVertexArrays(1, &uw.VAO)

	uiWindows[uw.ID] = uw

	// Hand control back to the main window's context/imgui context — the
	// caller is typically mid-frame on the main loop.
	Window.GLMakeCurrent(Context)
	imguiContext.SetCurrent()

	return uw, nil
}

// CloseUIWindow destroys w's GL/imgui resources and removes it from the
// registry. Safe to call from event handling or from a window's own Draw.
func CloseUIWindow(w *UIWindow) {
	if _, ok := uiWindows[w.ID]; !ok {
		return
	}
	delete(uiWindows, w.ID)
	w.Win.GLMakeCurrent(w.GL)
	if w.OnClose != nil {
		w.OnClose() // runs with w's context current, so it can free its own GL resources
	}

	if w.VAO != 0 {
		gl.DeleteVertexArrays(1, &w.VAO)
	}
	if w.VBO != 0 {
		gl.DeleteBuffers(1, &w.VBO)
	}
	if w.EBO != 0 {
		gl.DeleteBuffers(1, &w.EBO)
	}
	w.Ctx.Destroy()
	sdl.GLDeleteContext(w.GL)
	w.Win.Destroy()

	// Restore the main context so whatever runs next in the frame is correct.
	Window.GLMakeCurrent(Context)
	imguiContext.SetCurrent()
}

// UIWindowByID returns the open secondary window with the given SDL window
// ID, or nil if none matches (i.e. the event belongs to the main window).
func UIWindowByID(id uint32) *UIWindow { return uiWindows[id] }

// RenderSecondaryWindows draws every open UIWindow's UI for this frame.
// Called once per frame after the main window has swapped. Restores the main
// GL/imgui context before returning.
func RenderSecondaryWindows(dt float32) {
	if len(uiWindows) == 0 {
		return
	}
	for _, w := range uiWindows {
		w.Win.GLMakeCurrent(w.GL)
		w.Ctx.SetCurrent()

		ww, wh := w.Win.GetSize()
		w.W, w.H = ww, wh
		gl.Viewport(0, 0, ww, wh)
		gl.ClearColor(0.15, 0.15, 0.17, 1.0)
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)

		uiWindowNewFrame(w, dt)
		if w.Draw != nil {
			w.Draw()
		}
		imgui.Render()
		imguiRenderDrawData(imgui.RenderedDrawData(), w.VAO, w.VBO, w.EBO)

		w.Win.GLSwap()
	}

	Window.GLMakeCurrent(Context)
	imguiContext.SetCurrent()
}

// uiWindowNewFrame feeds SDL state into w's imgui IO. Uses the global mouse
// position minus this window's screen position — sdl.GetMouseState is
// relative to whichever window has focus, which is wrong for any window that
// doesn't, so both this and the main window's ImGuiNewFrame (editor_imgui.go)
// use the global variant.
func uiWindowNewFrame(w *UIWindow, dt float32) {
	io := imgui.CurrentIO()
	gx, gy, mbuttons := sdl.GetGlobalMouseState()
	wx, wy := w.Win.GetPosition()
	imguiNewFrameCommon(io, float32(gx-wx), float32(gy-wy), mbuttons, w.W, w.H, dt)
}

// RouteWindowEvent dispatches an SDL event to the secondary window it names,
// if any. Returns true if the event belonged to a secondary window (and was
// consumed here), false if it's the main window's event and should continue
// through the normal EditorHandleEvent/HandleEvent path.
func RouteWindowEvent(event sdl.Event, mainWindowID uint32) bool {
	wid, ok := eventWindowID(event)
	if !ok || wid == mainWindowID {
		return false
	}
	w := UIWindowByID(wid)
	if w == nil {
		// Not a window we're tracking (e.g. a stray event during teardown).
		return false
	}

	if we, ok := event.(sdl.WindowEvent); ok && we.Event == sdl.WINDOWEVENT_CLOSE {
		CloseUIWindow(w)
		return true
	}

	w.Win.GLMakeCurrent(w.GL)
	w.Ctx.SetCurrent()
	ImGuiProcessEvent(event)
	Window.GLMakeCurrent(Context)
	imguiContext.SetCurrent()
	return true
}

// eventWindowID extracts the WindowID field common to SDL input/window
// events, if the event type carries one.
func eventWindowID(event sdl.Event) (uint32, bool) {
	switch t := event.(type) {
	case sdl.WindowEvent:
		return t.WindowID, true
	case sdl.KeyboardEvent:
		return t.WindowID, true
	case sdl.TextInputEvent:
		return t.WindowID, true
	case sdl.MouseMotionEvent:
		return t.WindowID, true
	case sdl.MouseButtonEvent:
		return t.WindowID, true
	case sdl.MouseWheelEvent:
		return t.WindowID, true
	}
	return 0, false
}
