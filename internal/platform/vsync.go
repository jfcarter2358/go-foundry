package platform

// vsync.go — whether a buffer swap waits for the display's refresh.
//
// Until this file existed the engine never called SDL_GL_SetSwapInterval at
// all, which does not mean vsync was off: it means the driver picked. Mesa/GLX
// defaults to waiting for the vblank, most Windows drivers to whatever the
// control panel says, and a compositor can override either. So the frame rate
// was set by something outside the program with no way to see it or change it,
// and "is this 60 fps a real cost or just the monitor?" was unanswerable from
// inside the engine — which is exactly the question the F3 overlay exists to
// answer.
//
// The setting lives here rather than in render because a swap interval is a
// property of the window's presentation, not a rendering technique: it belongs
// beside the window, the GL context and the fullscreen toggle. It is also the
// reason uiwin has to care — see NewUIWindow.
//
// Like anti-aliasing (render/antialias.go) this is a live toggle and does not
// survive a restart; unlike anti-aliasing there is nothing about it fixed at
// window-creation time, so it is a plain SDL call with no tiering.
//
// Two things worth knowing before turning it off:
//
//   - Nothing else caps the frame rate. The main loop free-runs, so a light
//     scene will render as fast as the GPU allows and burn a core doing it.
//   - DeltaTime is clamped to a 1ms floor (see above), so past 1000 fps the
//     simulation starts running slower than the wall clock. That is a clamp
//     against a stalled frame, and vsync-off is the only way to hit it from
//     the other end.

import (
	"log"

	"github.com/veandco/go-sdl2/sdl"
)

// VSyncEnabled is the setting — what the engine asks the driver for. Defaults
// on, which is both the safer frame-pacing default and, on most drivers, what
// was already happening before anything here was called.
//
// Read it for "what was asked for"; read VSyncInterval for what is actually
// happening. They differ on a driver that refuses.
var VSyncEnabled = true

// VSyncInterval is the swap interval the driver reports after the last
// ApplyVSync: 0 no wait, 1 wait for one refresh, -1 adaptive (wait unless the
// frame is already late, i.e. tear rather than halve). The engine never asks
// for -1, but a driver forcing its own setting can still report it.
var VSyncInterval int

// VSyncSettable is false once a driver has refused a swap interval — vsync
// forced on or off outside the program, which happens with a compositor in the
// way or a driver control panel set to "always on". The toggle then does
// nothing and the UI says so rather than showing a checkbox that lies.
var VSyncSettable = true

// ApplyVSync pushes VSyncEnabled to the current GL context and reads back what
// the driver did with it. A GL context must be current; with no window at all
// (unit tests) it does nothing, leaving VSyncEnabled as the record of intent.
//
// Called once at startup, again whenever the setting is toggled, and once more
// after a secondary window has been created — see NewUIWindow for why the last
// one is not paranoia.
func ApplyVSync() {
	if Window == nil || Context == nil {
		return
	}
	want := 0
	if VSyncEnabled {
		want = 1
	}
	if err := sdl.GLSetSwapInterval(want); err != nil {
		VSyncSettable = false
		// Not fatal and not even unusual: the frame rate is then whatever the
		// driver decided, which is what it was before this file existed.
		log.Printf("WARNING: sdl.GLSetSwapInterval(%d): %v — vsync is not ours to set on this driver", want, err)
	} else {
		VSyncSettable = true
	}
	got, err := sdl.GLGetSwapInterval()
	if err != nil {
		// The driver reported an interval that is none of -1/0/1. Nothing
		// useful to show, so report the request rather than the nonsense.
		VSyncInterval = want
		return
	}
	VSyncInterval = got
}

// ToggleVSync flips the setting and applies it immediately (F12).
func ToggleVSync() {
	VSyncEnabled = !VSyncEnabled
	ApplyVSync()
}

// VSyncActive reports whether swaps are actually waiting for the display,
// according to the driver rather than according to the request. This is what a
// readout should show: on a driver that forces vsync it stays true with the
// checkbox unticked, which is the truth and the useful thing to see.
func VSyncActive() bool { return VSyncInterval != 0 }
