package main

import (
	"math"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

type Camera struct {
	Matrix    mgl32.Mat4
	Uniform   int32
	Position  mgl32.Vec3
	Direction mgl32.Vec3
	Yaw       float32
	Pitch     float32
	MouseLock bool
	// MouseLockWanted is the lock state the current mode asks for.  MouseLock
	// itself is dropped temporarily whenever the window loses focus; on regaining
	// focus we restore this rather than assuming gameplay.  Without it, any
	// focus-lost/gained cycle (which X11/WSL2 can emit on a right or middle
	// click) would grab the cursor while the editor is in a 2D mode.
	MouseLockWanted bool
}

var Cam Camera

// WorldUp is the engine-wide up direction (Y-up convention).
var WorldUp = mgl32.Vec3{0.0, 1.0, 0.0}

// DirectionFromAngles converts yaw/pitch to a unit direction vector.
func DirectionFromAngles(yaw, pitch float32) mgl32.Vec3 {
	y64 := float64(yaw)
	p64 := float64(pitch)
	return mgl32.Vec3{
		float32(math.Cos(y64) * math.Cos(p64)),
		float32(math.Sin(p64)),
		float32(math.Sin(y64) * math.Cos(p64)),
	}
}

// SetMouseLock enables or disables mouse-look capture and records the request
// so focus changes can restore it.  Locking hides the cursor, grabs the window
// so it stays confined, and warps to centre; see the warp-to-center note in
// LoadCamera for why SDL relative mouse mode is avoided.
func SetMouseLock(lock bool) {
	Cam.MouseLockWanted = lock
	applyMouseLock(lock)
}

// applyMouseLock changes the actual capture state without touching the wanted
// state — used when focus is lost and regained.
func applyMouseLock(lock bool) {
	Cam.MouseLock = lock
	if lock {
		sdl.ShowCursor(sdl.DISABLE)
		Window.SetGrab(true)
		Window.WarpMouseInWindow(WinHalfWidth, WinHalfHeight)
	} else {
		sdl.ShowCursor(sdl.ENABLE)
		Window.SetGrab(false)
	}
}

func LoadCamera(position mgl32.Vec3, pitch, yaw float32) {
	Cam.Yaw = yaw
	Cam.Pitch = pitch
	Cam.Position = position
	Cam.Direction = DirectionFromAngles(yaw, pitch)

	// Use the warp-to-center approach instead of SDL relative mouse mode.
	// SetRelativeMouseMode is unreliable on WSL2/X11 and can produce huge
	// accumulated deltas on the first event.  Instead we:
	//   1. Hide the cursor explicitly
	//   2. Grab the window so the cursor stays confined
	//   3. Warp to center; after each motion event we warp back to center and
	//      measure delta as (t.X - halfW, t.Y - halfH)
	SetMouseLock(true)

	Cam.Matrix = mgl32.LookAtV(Cam.Position, Cam.Position.Add(Cam.Direction), WorldUp)
	Cam.Uniform = gl.GetUniformLocation(Program, gl.Str("camera\x00"))
	gl.UniformMatrix4fv(Cam.Uniform, 1, false, &Cam.Matrix[0])
}

func MoveCamera(f, u, r float32) {
	Cam.Direction = DirectionFromAngles(Cam.Yaw, Cam.Pitch)
	forward := mgl32.Vec3{Cam.Direction.X(), 0.0, Cam.Direction.Z()}
	right := mgl32.Vec3{Cam.Direction.Z(), 0.0, -Cam.Direction.X()}
	delta := forward.Mul(f).Add(WorldUp.Mul(u)).Add(right.Mul(r))
	Cam.Position = Cam.Position.Add(delta)
}
