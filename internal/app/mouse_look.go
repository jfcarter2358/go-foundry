package app

// mouse_look.go — turning the player's view with the mouse, smoothed.
//
// The motion handler used to turn the camera by every event's delta the moment
// it arrived. That is as raw as a mouse gets, and at a few events a frame with
// integer pixel deltas it reads as twitchy — worse zoomed in, where one pixel
// covers more of the screen. Now the handler only banks the motion, and the
// frame loop spends it once a frame: all of it, with smoothing off, or an
// exponentially-eased share of it, so a flick still ends up exactly where it
// was aimed but gets there over a few tens of milliseconds instead of a jolt.
//
// Nothing is thrown away except when the player cannot look (control off, a
// pause), so smoothing never changes how far a given movement turns — only how
// quickly the turn is delivered.

import (
	"math"

	"foundry/internal/gfx"
	"foundry/internal/platform"
)

// maxLookSmoothingSeconds is the time constant MouseSmoothing = 1 means. At
// 0.08 s a flick is 95% delivered in a quarter of a second, which is about as
// heavy as still feels like aiming rather than steering.
const maxLookSmoothingSeconds = 0.08

// pendingLookX and pendingLookY are motion banked by the event handler and not
// yet turned into view rotation, in pixels.
var pendingLookX, pendingLookY float64

// bankMouseLook records one motion event's delta for the frame loop to spend.
func bankMouseLook(dx, dy int32) {
	pendingLookX += float64(dx)
	pendingLookY += float64(dy)
}

// dropMouseLook forgets banked motion: the player could not look while it was
// made, so it must not arrive as a lurch once they can.
func dropMouseLook() {
	pendingLookX, pendingLookY = 0, 0
}

// lookShare is how much of the banked motion one frame of dt spends.
func lookShare(smoothing, dt float32) float64 {
	tau := float64(clamp01(smoothing)) * maxLookSmoothingSeconds
	if tau <= 1e-6 || dt <= 0 {
		return 1
	}
	return 1 - math.Exp(-float64(dt)/tau)
}

// lookZoomScale slows the turn in proportion to how far the camera is zoomed,
// so a pixel of mouse moves the reticle about the same distance across the
// screen whether aiming down sights or not.
func lookZoomScale() float64 {
	def := math.Tan(float64(gfx.DefaultFieldOfView) * math.Pi / 360)
	cur := math.Tan(float64(gfx.FieldOfView) * math.Pi / 360)
	if def <= 0 || cur <= 0 {
		return 1
	}
	return cur / def
}

// applyMouseLook spends this frame's share of the banked motion on the camera.
func applyMouseLook(dt float32) {
	if pendingLookX == 0 && pendingLookY == 0 {
		return
	}
	share := lookShare(platform.MouseSmoothing, dt)
	x, y := pendingLookX*share, pendingLookY*share
	pendingLookX -= x
	pendingLookY -= y
	// Less than a hundredth of a pixel left is nothing anyone can see, and
	// leaving it would keep this running forever on an exponential tail.
	if math.Abs(pendingLookX) < 0.01 && math.Abs(pendingLookY) < 0.01 {
		x += pendingLookX
		y += pendingLookY
		dropMouseLook()
	}

	speed := float64(platform.MouseRotationSpeed*platform.MouseSensitivity) * lookZoomScale()
	if platform.MouseInvertY {
		y = -y
	}
	gfx.Cam.Yaw += float32(x * speed)
	gfx.Cam.Pitch -= float32(y * speed)
	gfx.AbsorbRecoilPitch(-float32(y * speed))
	if gfx.Cam.Pitch > gfx.MaxPitch {
		gfx.Cam.Pitch = gfx.MaxPitch
	}
	if gfx.Cam.Pitch < -gfx.MaxPitch {
		gfx.Cam.Pitch = -gfx.MaxPitch
	}
	gfx.Cam.Direction = gfx.DirectionFromAngles(gfx.Cam.Yaw, gfx.Cam.Pitch)
	gfx.UpdateCamMatrix()
}

func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
