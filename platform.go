package main

// platform.go — the window, the GL context, the keyboard, and the clock.
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

// KeyStates is SDL's keyboard snapshot for this frame, indexed by scancode, and
// event is the event being dispatched. Both are refreshed once per frame.
var KeyStates []uint8
var event sdl.Event

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
var lastFrameTick uint32

// ---------- movement tuning and gravity state ----------
//
// Read by the editor's fly camera and the player controller alike, and written
// by the settings panel, so they sit below both rather than inside either.

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
