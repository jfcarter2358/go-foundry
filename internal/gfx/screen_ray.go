package gfx

// screen_ray.go — between a point on the screen and the world behind it.
//
// Picking needs the ray through a pixel, and pinning a marker to a unit needs
// the pixel a point lands on. Both are built from the camera's own basis and
// the field of view rather than from Projection: a headless run (foundry sim,
// foundry play) never uploads a projection, and a pick there has to agree
// with one in the game.
//
// The basis comes off the view matrix, not Cam.Direction/Cam.Up, for the
// reason uidraw's viewHeading gives: the matrix is what was drawn with, in
// world space, whoever wrote it — the player's eye, a seat in a banking ship
// or the script camera rig.

import (
	"math"

	"foundry/internal/platform"

	"github.com/go-gl/mathgl/mgl32"
)

// viewBasis is the camera's eye, forward, right and up in world space, and
// false before anything has built a view matrix.
func viewBasis() (eye, fwd, right, up mgl32.Vec3, ok bool) {
	inv := Cam.Matrix.Inv()
	if inv == (mgl32.Mat4{}) {
		return eye, fwd, right, up, false
	}
	eye = inv.Col(3).Vec3()
	right = inv.Col(0).Vec3().Normalize()
	up = inv.Col(1).Vec3().Normalize()
	fwd = inv.Col(2).Vec3().Mul(-1).Normalize()
	return eye, fwd, right, up, true
}

// lens is tan of half the vertical field of view, and the window's aspect.
func lens() (tanHalf, aspect float32) {
	h := platform.WinHeight
	if h < 1 {
		h = 1
	}
	fov := clampFOV(FieldOfView)
	return float32(math.Tan(float64(mgl32.DegToRad(fov)) / 2)), float32(platform.WinWidth) / float32(h)
}

// ScreenRay is the ray from the eye through a point on the screen, in window
// pixels from the top-left: its origin and unit direction in world space. The
// centre of the window is exactly the camera's own direction, which is what
// makes a pick through a captured cursor and a hitscan from the reticle the
// same ray.
func ScreenRay(px, py float32) (origin, dir mgl32.Vec3, ok bool) {
	eye, fwd, right, up, ok := viewBasis()
	if !ok {
		return eye, fwd, false
	}
	w, h := float32(platform.WinWidth), float32(platform.WinHeight)
	if w < 1 || h < 1 {
		return eye, fwd, true
	}
	tanHalf, aspect := lens()
	nx := px/w*2 - 1
	ny := 1 - py/h*2
	d := fwd.Add(right.Mul(nx * tanHalf * aspect)).Add(up.Mul(ny * tanHalf))
	return eye, d.Normalize(), true
}

// WorldToScreen is where a world point lands on the window, in pixels from the
// top-left, and its distance in front of the eye along the view axis. False
// for a point on or behind the eye plane, which has no place on screen.
func WorldToScreen(p mgl32.Vec3) (px, py, depth float32, ok bool) {
	eye, fwd, right, up, ok := viewBasis()
	if !ok {
		return 0, 0, 0, false
	}
	rel := p.Sub(eye)
	depth = rel.Dot(fwd)
	if depth <= 1e-4 {
		return 0, 0, depth, false
	}
	tanHalf, aspect := lens()
	nx := rel.Dot(right) / (depth * tanHalf * aspect)
	ny := rel.Dot(up) / (depth * tanHalf)
	w, h := float32(platform.WinWidth), float32(platform.WinHeight)
	return (nx + 1) / 2 * w, (1 - ny) / 2 * h, depth, true
}
