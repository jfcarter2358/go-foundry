package main

// editor_orthoview.go — shared screen↔world transform math for the editor's
// orthographic 2D views: the top-down map (editor_2d.go) and the per-surface
// UV view (editor_surface2d.go). Both views pan/zoom independently (Editor's
// Pan2D/Zoom2D vs SurfacePan/SurfaceZoom) but do the exact same math, so it
// lives here once as orthoView and each view's own functions (editor2DOrtho,
// surfaceOrtho, etc.) just supply their own pan/zoom and delegate.

import "github.com/go-gl/mathgl/mgl32"

// orthoView is a pan/zoom pair describing one orthographic top-down camera.
type orthoView struct {
	pan  mgl32.Vec2
	zoom float32
}

// ortho returns the projection×view matrix for this view.
func (v orthoView) ortho() mgl32.Mat4 {
	halfW := float32(WinWidth) / 2 / v.zoom
	halfH := float32(WinHeight) / 2 / v.zoom
	cx, cy := v.pan.X(), v.pan.Y()
	return mgl32.Ortho(-halfW, halfW, -halfH, halfH, -100, 100).
		Mul4(mgl32.LookAtV(
			mgl32.Vec3{cx, 50, cy},
			mgl32.Vec3{cx, 0, cy},
			mgl32.Vec3{0, 0, -1},
		))
}

// worldToScreen converts a world-space XZ (or surface UV) point to screen pixels.
func (v orthoView) worldToScreen(wx, wz float32) (sx, sy float32) {
	halfW := float32(WinWidth) / 2
	halfH := float32(WinHeight) / 2
	sx = (wx-v.pan.X())*v.zoom + halfW
	sy = (wz-v.pan.Y())*v.zoom + halfH
	return
}

// screenToWorld converts screen pixels to world-space XZ (or surface UV).
func (v orthoView) screenToWorld(sx, sy int32) (wx, wz float32) {
	halfW := float32(WinWidth) / 2
	halfH := float32(WinHeight) / 2
	wx = (float32(sx)-halfW)/v.zoom + v.pan.X()
	wz = (float32(sy)-halfH)/v.zoom + v.pan.Y()
	return
}

// screenToWorldSize converts a pixel distance to world units at this view's zoom.
func (v orthoView) screenToWorldSize(screenPx float32) float32 {
	return screenPx / v.zoom
}
