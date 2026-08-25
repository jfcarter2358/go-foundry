package gfx

import (
	"foundry/internal/platform"
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
	// Up is the camera's up vector. It is WorldUp on foot, and the frame's
	// rotated up when the viewer is riding something that can pitch or roll —
	// gfx cannot see a frame, so whoever owns the viewer writes it here.
	// The zero value reads as WorldUp so a Camera that never sets it behaves
	// exactly as it did before there was one.
	Up        mgl32.Vec3
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
	ApplyMouseLock(lock)
}

// ApplyMouseLock changes the actual capture state without touching the wanted
// state — used when focus is lost and regained.
func ApplyMouseLock(lock bool) {
	Cam.MouseLock = lock
	if lock {
		sdl.ShowCursor(sdl.DISABLE)
		platform.Window.SetGrab(true)
		platform.Window.WarpMouseInWindow(platform.WinHalfWidth, platform.WinHalfHeight)
	} else {
		sdl.ShowCursor(sdl.ENABLE)
		platform.Window.SetGrab(false)
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

	Cam.Up = WorldUp
	Cam.Uniform = gl.GetUniformLocation(Program, gl.Str("camera\x00"))
	UpdateCamMatrix()
}

func MoveCamera(f, u, r float32) {
	Cam.Direction = DirectionFromAngles(Cam.Yaw, Cam.Pitch)
	forward := mgl32.Vec3{Cam.Direction.X(), 0.0, Cam.Direction.Z()}
	right := mgl32.Vec3{Cam.Direction.Z(), 0.0, -Cam.Direction.X()}
	delta := forward.Mul(f).Add(WorldUp.Mul(u)).Add(right.Mul(r))
	Cam.Position = Cam.Position.Add(delta)
}

// UpdateCamDirection and UpdateCamMatrix recompute the camera basis and upload
// the view matrix. They live here rather than with the editor toggle that first
// needed them: the spawn-point preview and the 3D fly camera call them too.
// ---------- camera helpers used by editor toggle ----------

func UpdateCamDirection() {
	yaw64 := float64(Cam.Yaw)
	pitch64 := float64(Cam.Pitch)
	Cam.Direction = mgl32.Vec3{
		float32(math.Cos(yaw64) * math.Cos(pitch64)),
		float32(math.Sin(pitch64)),
		float32(math.Sin(yaw64) * math.Cos(pitch64)),
	}
}

// CamUp is the up vector to build the view matrix with: Cam.Up when it has
// been set, WorldUp otherwise. The fallback is what lets every caller that
// predates a tiltable camera keep working untouched.
func CamUp() mgl32.Vec3 {
	if Cam.Up == (mgl32.Vec3{}) {
		return WorldUp
	}
	return Cam.Up
}

// UpdateCamMatrix rebuilds the view matrix from Cam.Position/Direction/Up and
// uploads it. This is the only place the gameplay/editor view matrix is built —
// the sites that used to inline LookAtV all route through here, which is what
// keeps the up vector from silently disagreeing between them.
func UpdateCamMatrix() {
	Cam.Matrix = mgl32.LookAtV(Cam.Position, Cam.Position.Add(Cam.Direction), CamUp())
	if Program == 0 {
		// No world shader, so nowhere to upload to. Only a headless test gets
		// here — a GL call with no context current segfaults in the driver
		// rather than returning an error, so the guard has to be on this side.
		return
	}
	gl.UniformMatrix4fv(Cam.Uniform, 1, false, &Cam.Matrix[0])
}

// MaxPitch prevents the camera from crossing vertical (avoids gimbal flip).
const MaxPitch = float32(math.Pi/2 - 0.01)

// HandleInput applies this frame's held keys to the camera.
//
// It lives with the camera rather than with the rest of input handling because
// that is all it touches, and because the editor's free-fly mode calls it too —
// putting it in the application shell made the editor depend on the shell for
// what is really a camera operation.
//
// This is the free-fly spelling: the camera *is* the thing being moved.
// Gameplay uses MoveIntent instead and moves the player, because a body and the
// eyes riding it stop agreeing the moment the body is standing on something
// that can rotate.
func HandleInput(keyStates []uint8) {
	Cam.Position = Cam.Position.Add(MoveIntent(keyStates))
	UpdateCamMatrix()
}

// MoveIntent reads this frame's keys and returns the displacement they ask for,
// without applying it.
//
// Look keys are applied directly — a rotation is not a displacement, and
// nothing downstream wants to defer it — and Cam.Direction is refreshed before
// the basis is built so a turn takes effect on the same frame it was made.
//
// The returned vector is in the camera's own yaw-relative basis with WorldUp as
// its vertical, which is world space while the viewer is standing on the world.
// A caller whose viewer is standing in a rotating frame rotates it first.
func MoveIntent(keyStates []uint8) mgl32.Vec3 {
	// Scale movement and rotation by DeltaTime*60 so behavior is frame-rate independent
	// while preserving the same feel as the original 60 FPS-tuned constants.
	dtScale := platform.DeltaTime * 60.0
	moveSpeed := platform.MovementSpeed * dtScale
	rotSpeed := platform.KeyRotationSpeed * dtScale

	fMove := float32(0.0)
	uMove := float32(0.0)
	rMove := float32(0.0)

	if keyStates[sdl.SCANCODE_W] != 0 {
		fMove += moveSpeed
	}
	if keyStates[sdl.SCANCODE_A] != 0 {
		rMove += moveSpeed
	}
	if keyStates[sdl.SCANCODE_S] != 0 {
		fMove -= moveSpeed
	}
	if keyStates[sdl.SCANCODE_D] != 0 {
		rMove -= moveSpeed
	}
	if keyStates[sdl.SCANCODE_SPACE] != 0 {
		if !platform.GravityEnabled {
			uMove += moveSpeed // free-fly up
		}
		// jump is handled via KEYDOWN event in HandleEvent
	}
	if keyStates[sdl.SCANCODE_LSHIFT] != 0 {
		if !platform.GravityEnabled {
			uMove -= moveSpeed // free-fly down
		}
	}
	if keyStates[sdl.SCANCODE_LEFT] != 0 {
		Cam.Yaw -= rotSpeed
	}
	if keyStates[sdl.SCANCODE_RIGHT] != 0 {
		Cam.Yaw += rotSpeed
	}
	if keyStates[sdl.SCANCODE_UP] != 0 {
		Cam.Pitch += rotSpeed
	}
	if keyStates[sdl.SCANCODE_DOWN] != 0 {
		Cam.Pitch -= rotSpeed
	}

	Cam.Direction = DirectionFromAngles(Cam.Yaw, Cam.Pitch)
	forward := mgl32.Vec3{Cam.Direction.X(), 0.0, Cam.Direction.Z()}
	right := mgl32.Vec3{Cam.Direction.Z(), 0.0, -Cam.Direction.X()}
	return forward.Mul(fMove).Add(WorldUp.Mul(uMove)).Add(right.Mul(rMove))
}
