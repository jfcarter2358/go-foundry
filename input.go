package main

import (
	"math"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

var MovementSpeed = float32(0.1)
var KeyRotationSpeed = float32(0.025)
var MouseRotationSpeed = float32(0.003)

// Gravity system globals — read and written by input handling and the main loop.
var GravityEnabled = true
var IsOnGround = false
var JumpRequested = false

const GravityAccel = float32(0.010) // downward acceleration per frame
const MaxFallSpeed = float32(0.50)  // terminal fall speed per frame
const JumpVelocity = float32(0.20)  // upward impulse on jump

// maxPitch prevents the camera from crossing vertical (avoids gimbal flip).
const maxPitch = float32(math.Pi/2 - 0.01)

// RefreshWindowSize updates all engine state that depends on the window
// dimensions: viewport, projection matrix, half-width/height, and mouse warp.
func RefreshWindowSize() {
	WinWidth, WinHeight = Window.GetSize()
	WinHalfWidth = WinWidth / 2
	WinHalfHeight = WinHeight / 2
	gl.Viewport(0, 0, WinWidth, WinHeight)
	if WinHeight < 1 {
		WinHeight = 1
	}
	Projection = mgl32.Perspective(mgl32.DegToRad(45.0), float32(WinWidth)/float32(WinHeight), 0.1, 1000.0)
	gl.UniformMatrix4fv(ProjectionUniform, 1, false, &Projection[0])
	if Cam.MouseLock {
		Window.WarpMouseInWindow(WinHalfWidth, WinHalfHeight)
	}
}

// ToggleFullscreen switches between windowed and borderless fullscreen.
func ToggleFullscreen() {
	if Window.GetFlags()&sdl.WINDOW_FULLSCREEN_DESKTOP != 0 {
		Window.SetFullscreen(uint32(0))
	} else {
		Window.SetFullscreen(uint32(sdl.WINDOW_FULLSCREEN_DESKTOP))
	}
	RefreshWindowSize()
}

func HandleInput(keyStates []uint8) {
	// Scale movement and rotation by DeltaTime*60 so behavior is frame-rate independent
	// while preserving the same feel as the original 60 FPS-tuned constants.
	dtScale := DeltaTime * 60.0
	moveSpeed := MovementSpeed * dtScale
	rotSpeed := KeyRotationSpeed * dtScale

	fMove := float32(0.0)
	uMove := float32(0.0)
	rMove := float32(0.0)

	if keyStates[sdl.SCANCODE_ESCAPE] != 0 {
		Running = false
	}
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
		if !GravityEnabled {
			uMove += moveSpeed // free-fly up
		}
		// jump is handled via KEYDOWN event in HandleEvent
	}
	if keyStates[sdl.SCANCODE_LSHIFT] != 0 {
		if !GravityEnabled {
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

	MoveCamera(fMove, uMove, rMove)
	Cam.Matrix = mgl32.LookAtV(Cam.Position, Cam.Position.Add(Cam.Direction), WorldUp)
	gl.UniformMatrix4fv(Cam.Uniform, 1, false, &Cam.Matrix[0])
}

func HandleEvent(event sdl.Event) {
	switch t := event.(type) {
	case sdl.QuitEvent:
		Running = false

	case sdl.MouseMotionEvent:
		if !Cam.MouseLock {
			break
		}
		// Warp-to-center approach: measure delta from the window centre.
		// After processing we warp the cursor back to centre; that warp itself
		// generates another MOUSEMOTION event whose position IS the centre
		// (dx==0, dy==0), so we skip it here.  This avoids every pitfall of
		// SDL relative-mouse mode on X11/WSL2.
		dx := t.X - WinHalfWidth
		dy := t.Y - WinHalfHeight
		if dx == 0 && dy == 0 {
			break
		}
		Cam.Yaw += MouseRotationSpeed * float32(dx)
		Cam.Pitch -= MouseRotationSpeed * float32(dy)
		if Cam.Pitch > maxPitch {
			Cam.Pitch = maxPitch
		}
		if Cam.Pitch < -maxPitch {
			Cam.Pitch = -maxPitch
		}
		// Recompute direction immediately so movement this frame uses the
		// correct look direction (no one-frame rotation lag).
		Cam.Direction = DirectionFromAngles(Cam.Yaw, Cam.Pitch)
		Cam.Matrix = mgl32.LookAtV(Cam.Position, Cam.Position.Add(Cam.Direction), WorldUp)
		gl.UniformMatrix4fv(Cam.Uniform, 1, false, &Cam.Matrix[0])
		// Return cursor to centre for next delta measurement.
		Window.WarpMouseInWindow(WinHalfWidth, WinHalfHeight)

	case sdl.WindowEvent:
		// A secondary UIWindow (texture editor, etc.) reports its own size/
		// focus events too; only the main window's should drive the 3D
		// viewport and mouse-capture state. Its own close/resize/redraw are
		// handled by RouteWindowEvent (window.go) before HandleEvent ever
		// sees them — see the dispatch in main.go.
		if mw, err := Window.GetID(); err != nil || t.WindowID != mw {
			break
		}
		switch t.Event {
		case sdl.WINDOWEVENT_FOCUS_LOST:
			// Release capture but remember whether the current mode wanted it.
			applyMouseLock(false)
		case sdl.WINDOWEVENT_SIZE_CHANGED:
			RefreshWindowSize()
		case sdl.WINDOWEVENT_FOCUS_GAINED:
			// Restore only what the mode asked for.  Editor 2D/surface/scenery
			// modes never want capture, and a stray focus cycle from a right or
			// middle click must not hand them a grabbed cursor.  The warp inside
			// applyMouseLock also makes the first motion event after regaining
			// focus measure from the correct origin.
			applyMouseLock(Cam.MouseLockWanted)
		}

	case sdl.KeyboardEvent:
		if t.Type == sdl.KEYDOWN {
			switch t.Keysym.Scancode {
			case sdl.SCANCODE_F11:
				ToggleFullscreen()
			case sdl.SCANCODE_GRAVE: // tilde (~) key toggles level editor
				EditorToggle()
			case sdl.SCANCODE_SPACE:
				if !Editor.Active && GravityEnabled && IsOnGround {
					JumpRequested = true
				}
			case sdl.SCANCODE_F3:
				DebugToggle()
			// Biome lighting cost switches — see BiomeShadowSettings. Bound to
			// keys, not editor checkboxes, so they can be flipped while flying
			// around with the mouse captured and the F3 overlay's frame time
			// read directly against each one.
			case sdl.SCANCODE_F5:
				BiomeShadows.Receive = !BiomeShadows.Receive
			case sdl.SCANCODE_F6:
				BiomeShadows.CastRockMoss = !BiomeShadows.CastRockMoss
			case sdl.SCANCODE_F7:
				BiomeShadows.CastFoliage = !BiomeShadows.CastFoliage
			case sdl.SCANCODE_F8:
				// Cycle 0..ShadowCascades: 0 disables scatter casting entirely
				// without losing the per-kind switches above.
				BiomeShadows.CastCascades = (BiomeShadows.CastCascades + 1) % (ShadowCascades + 1)
			case sdl.SCANCODE_F9:
				// Cycle 1..3: how many LOD tiers (Near/Mid/Far) cast real
				// per-instance shadows — see BiomeShadowSettings.CastLODTiers.
				BiomeShadows.CastLODTiers = BiomeShadows.CastLODTiers%3 + 1
			case sdl.SCANCODE_F10:
				// Window-level multisample anti-aliasing — see antialias.go.
				ToggleAntiAlias()
			case sdl.SCANCODE_0:
				if !Editor.Active {
					GravityEnabled = !GravityEnabled
					if !GravityEnabled {
						IsOnGround = false
						JumpRequested = false
					}
				}
			}
		}
	}
}
