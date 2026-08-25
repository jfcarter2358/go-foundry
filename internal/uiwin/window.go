package uiwin

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
	"foundry/internal/gizmo"
	"foundry/internal/imguibackend"
	"foundry/internal/platform"
	"log"

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
	// GizmoVAO is this window's vertex array for the editor's line renderer
	// (editor_gizmo.go). Like the imgui VAO above it can't be shared with the
	// main context, so any window that draws gizmo lines binds this one
	// instead — see GizmoVAOOverride.
	GizmoVAO uint32
	ID       uint32
	W, H     int32
	// ClearColor is the RGBA this window's framebuffer is cleared to each
	// frame. Defaults to the editor's panel grey; the 2D editor window uses
	// black so its map isn't drawn over a backdrop.
	ClearColor  [4]float32
	Draw        func()          // per-frame UI body; called with this window's imgui context current
	HandleEvent func(sdl.Event) // optional: window-specific input, run after imgui sees the event
	// FilterEvent is optional, and runs *before* imgui sees the event, with
	// this window's imgui context current. Returning true consumes the event:
	// neither imgui nor HandleEvent is given it.
	//
	// There is exactly one reason to want this rather than HandleEvent, and it
	// is keys imgui acts on by itself. Escape inside an InputText reverts the
	// field to what it held when it was focused — which for a script buffer
	// means silently throwing away every edit since the last save — and by the
	// time HandleEvent runs, imgui has already done it. The only way to have a
	// different Escape is to take the key away first.
	FilterEvent func(sdl.Event) bool
	OnClose     func()
	// CloseRequested asks for this window to be destroyed after the current
	// frame. Set it instead of calling CloseUIWindow from inside Draw — the
	// frame's imgui.Render still needs the context that CloseUIWindow tears
	// down.
	CloseRequested bool
}

// uiWindows holds every open secondary window, keyed by SDL window ID so
// event routing (main.go/input.go) and per-frame rendering can look them up
// directly.
var uiWindows = map[uint32]*UIWindow{}

// MainWindowID is the SDL window ID of Window, the primary game/editor window.
// Set once at startup; anything that has to tell the main window apart from a
// secondary one (event routing, the F1 overlay) compares against it.
var MainWindowID uint32

// NewUIWindow creates a new OS window sharing the main GL context, its own
// imgui context, and per-window VAO/VBO/EBO. Must be called with the main GL
// context current; restores it before returning. draw is invoked once per
// frame from RenderSecondaryWindows.
func NewUIWindow(title string, w, h int32, draw func(), onClose func()) (*UIWindow, error) {
	if err := sdl.GLSetAttribute(sdl.GL_SHARE_WITH_CURRENT_CONTEXT, 1); err != nil {
		return nil, fmt.Errorf("GLSetAttribute(SHARE_WITH_CURRENT_CONTEXT): %w", err)
	}
	// Ask for a real depth buffer. Nothing else in the app sets GL_DEPTH_SIZE,
	// and SDL's default of 16 bits is enough for the 2D editor and the texture
	// editor because neither depth-tests at all. The model editor does, and at
	// 16 bits a dense mesh z-fights against its own wireframe.
	if err := sdl.GLSetAttribute(sdl.GL_DEPTH_SIZE, 24); err != nil {
		log.Printf("WARNING: sdl.GLSetAttribute(GL_DEPTH_SIZE) for %q: %v", title, err)
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
	// atlas explicitly — see SharedFontAtlas (editor_imgui.go) for why nil
	// asserts here despite the imgui-go doc comment suggesting it wouldn't.
	ctx := imgui.CreateContext(&imguibackend.SharedFontAtlas)
	ctx.SetCurrent()
	io := imgui.CurrentIO()
	io.SetIniFilename("")
	imguibackend.ImguiConfigureIO(io)

	uw := &UIWindow{
		Win: win, GL: glCtx, Ctx: ctx, ID: id, W: w, H: h,
		ClearColor: [4]float32{0.15, 0.15, 0.17, 1.0},
		Draw:       draw, OnClose: onClose,
	}

	gl.GenBuffers(1, &uw.VBO)
	gl.GenBuffers(1, &uw.EBO)
	gl.GenVertexArrays(1, &uw.VAO)
	gl.GenVertexArrays(1, &uw.GizmoVAO)

	uiWindows[uw.ID] = uw

	// Hand control back to the main window's context/imgui context — the
	// caller is typically mid-frame on the main loop.
	platform.Window.GLMakeCurrent(platform.Context)
	imguibackend.ImguiContext.SetCurrent()

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
	if w.GizmoVAO != 0 {
		gl.DeleteVertexArrays(1, &w.GizmoVAO)
	}
	w.Ctx.Destroy()
	sdl.GLDeleteContext(w.GL)
	w.Win.Destroy()

	// Restore the main context so whatever runs next in the frame is correct.
	platform.Window.GLMakeCurrent(platform.Context)
	imguibackend.ImguiContext.SetCurrent()
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
	var closing []*UIWindow
	for _, w := range uiWindows {
		w.Win.GLMakeCurrent(w.GL)
		w.Ctx.SetCurrent()

		ww, wh := w.Win.GetSize()
		w.W, w.H = ww, wh
		gl.Viewport(0, 0, ww, wh)
		gl.ClearColor(w.ClearColor[0], w.ClearColor[1], w.ClearColor[2], w.ClearColor[3])
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)

		uiWindowNewFrame(w, dt)
		// Gizmo lines drawn by this window's Draw go through its own VAO.
		gizmo.GizmoVAOOverride = w.GizmoVAO
		if w.Draw != nil {
			w.Draw()
		}
		gizmo.GizmoVAOOverride = 0
		// The F1 reference belongs to whichever window has focus, so a
		// secondary window draws it inside its own frame.
		KeybindHelpRenderInWindow(w)
		imgui.Render()
		imguibackend.ImguiRenderDrawData(imgui.RenderedDrawData(), w.VAO, w.VBO, w.EBO)

		w.Win.GLSwap()

		if w.CloseRequested {
			closing = append(closing, w)
		}
	}

	platform.Window.GLMakeCurrent(platform.Context)
	imguibackend.ImguiContext.SetCurrent()

	// Deferred closes happen only now that every context has finished its
	// frame — CloseUIWindow destroys the imgui context the loop above was
	// still rendering with.
	for _, w := range closing {
		CloseUIWindow(w)
	}
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
	imguibackend.ImguiNewFrameCommon(io, float32(gx-wx), float32(gy-wy), mbuttons, w.W, w.H, dt)
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
	if w.FilterEvent != nil && w.FilterEvent(event) {
		platform.Window.GLMakeCurrent(platform.Context)
		imguibackend.ImguiContext.SetCurrent()
		return true
	}
	imguibackend.ImGuiProcessEvent(event)
	if w.HandleEvent != nil {
		// Runs with w's imgui context current, so WantCaptureMouse/Keyboard
		// checks inside it refer to this window's UI, not the main one's.
		w.HandleEvent(event)
	}
	platform.Window.GLMakeCurrent(platform.Context)
	imguibackend.ImguiContext.SetCurrent()
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

// ---------- deferred opens ----------

// PendingWindowOpens are open requests parked by code running inside some
// window's own Draw.
//
// A window cannot be opened from there. NewUIWindow creates a GL context and an
// imgui context, makes each current in turn, and then restores *the main
// window's* — so the caller, which was mid-frame on its own context, carries on
// issuing imgui calls against a context that has no frame in progress. That is
// a segfault on the very next widget, not a subtle corruption. Creating the
// window also inserts into the map RenderSecondaryWindows is ranging over.
//
// So the request is parked and drained from the main loop, where the main
// context is genuinely current and nothing is iterating the registry. This
// generalises what modelPendingTexEditor did for one case: the hazard applies to
// any window opening any other, and a shared door is one place to get it right
// rather than several places to get it wrong.
var PendingWindowOpens []func()

// RequestWindowOpen asks the main loop to run open at the top of the next frame.
//
// Safe to call from inside any window's Draw or event handler. Code that is
// already on the main window's frame — the editor's own toolbar — can call the
// Open function directly; this is for everywhere else.
func RequestWindowOpen(open func()) {
	if open == nil {
		return
	}
	PendingWindowOpens = append(PendingWindowOpens, open)
}

// DrainWindowOpens runs every parked open request. Called once per frame from
// the main loop with the main GL and imgui contexts current.
func DrainWindowOpens() {
	if len(PendingWindowOpens) == 0 {
		return
	}
	reqs := PendingWindowOpens
	PendingWindowOpens = nil
	for _, open := range reqs {
		open()
	}
}
