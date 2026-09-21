package uidraw

// uidraw_world.go — where a point in the 3D world is on the screen, for
// widgets pinned to one (ui.Node.SetWorldAnchor).

import (
	"foundry/internal/gfx"
	"foundry/internal/ui"

	"github.com/go-gl/mathgl/mgl32"
)

// InstallWorldProject hands the ui package the world camera's projection.
// Installed by Init with the video driver, for the same reason: ui lays
// things out and must not import the renderer that owns the camera.
func InstallWorldProject() { ui.WorldProject = projectWorld }

// projectWorld is p through the world camera and the world's lens — the one the
// scene was drawn with, not the viewmodel's — as fractions of the view from the
// top-left. A point on or behind the eye plane has no place on screen.
func projectWorld(p mgl32.Vec3) (u, v float32, ok bool) {
	c := gfx.Projection.Mul4(gfx.Cam.Matrix).Mul4x1(p.Vec4(1))
	if c.W() <= 1e-4 {
		return 0, 0, false
	}
	x, y := c.X()/c.W(), c.Y()/c.W()
	return x*0.5 + 0.5, 0.5 - y*0.5, true
}
