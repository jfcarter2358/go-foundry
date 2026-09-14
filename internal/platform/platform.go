package platform

// go — the window, the GL context, the keyboard, and the clock.
//
// These are the handles that everything above them reaches for and nothing
// owns: the renderer needs the window size to build a projection, the editor
// needs it to unproject a click, the pickers need the key state to read a
// modifier, and every subsystem that moves needs DeltaTime. They lived in
// main.go, which made "the application's bootstrap" a dependency of code that
// has nothing to do with starting up.
//
// They are still package-level variables — this is a relocation, not a
// de-globalisation. What it buys is a single owner and a single import for the
// things a frame is drawn against.

import "github.com/veandco/go-sdl2/sdl"

// Running is cleared to leave the main loop.
var Running = true

// KeyStates is SDL's keyboard snapshot for this frame, refreshed once per frame
// and indexed by scancode.
var KeyStates []uint8

// The main window and its GL context, and the dimensions every projection and
// unprojection is built from. WinHalfWidth/WinHalfHeight are cached because the
// mouse-warp path recomputes them every frame otherwise.
var WinTitle string = "Foundry Engine"
var WinWidth, WinHeight int32 = 1600, 900
var WinHalfHeight int32 = WinHeight / 2
var WinHalfWidth int32 = WinWidth / 2
var Window *sdl.Window
var Context sdl.GLContext

// DeltaTime is the elapsed time in seconds since the last frame.
// Clamped to [0.001, 0.05] to prevent physics instability.
var DeltaTime float32 = 1.0 / 60.0
var LastFrameTick uint32

// ---------- movement tuning and gravity state ----------
//
// Read by the editor's fly camera and the player controller alike, and written
// by the settings panel, so they sit below both rather than inside either.

var MovementSpeed = float32(0.1)
var KeyRotationSpeed = float32(0.025)
var MouseRotationSpeed = float32(0.003)

// The player's own mouse settings, on top of MouseRotationSpeed, for gameplay
// look only — the editor's cameras keep the raw speed. Written by the game's
// settings screen (@level.prop.mouse_*), saved to controls.yaml by the app.
//
// MouseSensitivity multiplies the turn per pixel. MouseSmoothing is 0 for the
// raw mouse and 1 for the heaviest smoothing offered: the time constant the
// look eases out over (see app/mouse_look.go). MouseInvertY flips pitch.
var (
	MouseSensitivity = float32(1)
	MouseSmoothing   = float32(0.35)
	MouseInvertY     = false
	// ControlSettingsChanged is set by anything that writes the three above,
	// and cleared by the app once it has saved them.
	ControlSettingsChanged = false
)

// Gravity system globals — read and written by input handling and the main loop.
var GravityEnabled = true
var IsOnGround = false
var JumpRequested = false

// PlayerControlEnabled is whether the player's input reaches the game. Off,
// walking, jumping, driving, the interact key and looking around — mouse and
// arrow keys both — all go quiet. Set from the spawn point on entering play,
// and by a script handing control back.
var PlayerControlEnabled = true

const GravityAccel = float32(0.010) // downward acceleration per frame
const MaxFallSpeed = float32(0.50)  // terminal fall speed per frame
const JumpVelocity = float32(0.20)  // upward impulse on jump

// ---------- fullscreen ----------
//
// The toggle itself lives in the app layer, because switching modes has to be
// followed by rebuilding the projection from the new window size — which is
// app's job, not this package's. The editor's settings panel sits below app in
// the import graph and so cannot call it directly, so app installs it here.
// Same inversion as world.CurrentLevel and hooks.EditorActive.

// ToggleFullscreenFunc is installed by the app layer at startup. Nil until then.
var ToggleFullscreenFunc func()

// RequestToggleFullscreen switches between windowed and borderless fullscreen,
// and does nothing when no app layer has installed the toggle (the editor's
// unit tests, which have no window).
func RequestToggleFullscreen() {
	if ToggleFullscreenFunc != nil {
		ToggleFullscreenFunc()
	}
}

// IsFullscreen reports the window's current mode, so a checkbox can show it
// rather than tracking a copy that drifts when F11 is pressed instead.
func IsFullscreen() bool {
	return Window != nil && Window.GetFlags()&sdl.WINDOW_FULLSCREEN_DESKTOP != 0
}
