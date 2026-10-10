package uidraw

// uidraw_world.go — where a point in the 3D world is on the screen, for
// widgets pinned to one (ui.Node.SetWorldAnchor).

import (
	"math"

	"foundry/internal/gfx"
	"foundry/internal/ui"

	"github.com/go-gl/mathgl/mgl32"
)

// InstallWorldProject hands the ui package the world camera's projection.
// Installed by Init with the video driver, for the same reason: ui lays
// things out and must not import the renderer that owns the camera.
func InstallWorldProject() {
	ui.WorldProject = projectWorld
	ui.ViewHeading = viewHeading
}

// viewHeading is the world camera's eye and yaw, read off its view matrix
// rather than Cam.Position/Cam.Yaw: aboard a moving frame those are the
// frame's coordinates, and a compass tape has to agree with the world.
func viewHeading() (mgl32.Vec3, float32, bool) {
	inv := gfx.Cam.Matrix.Inv()
	if inv == (mgl32.Mat4{}) {
		return mgl32.Vec3{}, 0, false
	}
	eye := inv.Col(3).Vec3()
	fwd := inv.Col(2).Vec3().Mul(-1)
	if fwd.X()*fwd.X()+fwd.Z()*fwd.Z() < 1e-8 {
		// Looking straight up or down there is no heading. First-person
		// pitch stops short of it; anything else gets the authored layout.
		return eye, 0, false
	}
	return eye, float32(math.Atan2(float64(-fwd.X()), float64(-fwd.Z()))), true
}

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
