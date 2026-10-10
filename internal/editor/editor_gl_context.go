package editor

// editor_gl_context.go — running level mutations against the main window's
// GL context.
//
// Any secondary window that mutates *level* state from inside its own Draw
// hits the same hazard: the entity editor re-stamps every placed instance of a
// type when the type is saved, and a mesh uploaded under that window's context
// would give the level entities VAO names the main window cannot draw. VAOs
// are the one class of GL object a shared context does not share (see
// window.go), so everything that can rebuild level geometry runs against the
// main context and only a window's own line/imgui drawing uses its own.
//
// This used to live in editor_2d_window.go, alongside the 2D map's own
// wrapper. The map is gone with the sector editor; the hazard is not, so the
// general form outlives it.

import (
	"foundry/internal/platform"
	"foundry/internal/uiwin"
)

// withMainGLFrom runs fn with the main window's GL context current, then hands
// w's back. A nil window means the caller is already on the main context and
// fn simply runs.
func withMainGLFrom(w *uiwin.UIWindow, fn func()) {
	if w == nil {
		fn()
		return
	}
	platform.Window.GLMakeCurrent(platform.Context)
	defer w.Win.GLMakeCurrent(w.GL)
	fn()
}
