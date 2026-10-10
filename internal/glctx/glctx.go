package glctx

// Package glctx makes a GL context with no visible window.
//
// It is the thing every headless renderer needs first and the thing each one got
// subtly wrong on its own: a context may be current on only one thread at a
// time, and the goroutine that touches GL has to be pinned to the thread it was
// made current on, or the "current" context becomes whichever thread last
// touched one. The symptom is a segfault inside the driver at a readback, on
// code whose own logic is fine.
//
// internal/gltest is the test-side wrapper around this, and `foundry render` is
// the tool-side one. Neither makes its own window any more.

import (
	"fmt"
	"runtime"
	"sync"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/veandco/go-sdl2/sdl"
)

// One window and one context per process, for the reason above: contexts and
// windows accumulate (nothing here is ever destroyed — see Release), and each
// new one silently displaces the previous as current on the thread that made it.
var (
	once sync.Once
	win  *sdl.Window
	ctx  sdl.GLContext
	err  error
)

func create(w, h int32) {
	if e := sdl.Init(sdl.INIT_VIDEO); e != nil {
		err = fmt.Errorf("sdl.Init: %w", e)
		return
	}
	RequestCoreProfile()

	win, err = sdl.CreateWindow("foundry (offscreen)", sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED,
		w, h, sdl.WINDOW_OPENGL|sdl.WINDOW_HIDDEN)
	if err != nil {
		return
	}
	if ctx, err = win.GLCreateContext(); err != nil {
		return
	}
	// Loads the function pointers into package globals, so it is a
	// once-per-process call rather than a once-per-context one.
	if err = gl.Init(); err != nil {
		return
	}
	err = CheckVersion()
}

// Acquire creates the process's offscreen context if it does not exist yet and
// makes it current on this goroutine's thread, which it locks.
//
// The caller must call Release on the same goroutine when it is done. Failing to
// is not merely untidy: GLX refuses to make a context current on a second thread
// while it is still current on the first, so the next Acquire anywhere in the
// process fails with "BadAccess: attempt to access private resource denied".
func Acquire(w, h int32) error {
	runtime.LockOSThread()
	once.Do(func() { create(w, h) })
	if err != nil {
		runtime.UnlockOSThread()
		return err
	}
	if e := win.GLMakeCurrent(ctx); e != nil {
		runtime.UnlockOSThread()
		return fmt.Errorf("GLMakeCurrent: %w", e)
	}
	return nil
}

// Release gives the context up and unlocks the thread.
//
// The window and the context themselves are left alive: a goroutine is not
// necessarily on the process's main OS thread, and tearing an SDL video
// subsystem down from another one segfaults in the driver. A process that is
// about to exit loses them to the OS, which is the only teardown that has ever
// worked here.
func Release() {
	if win != nil {
		_ = win.GLMakeCurrent(nil)
	}
	runtime.UnlockOSThread()
}

// Available reports whether a context could be made, without keeping it: for a
// tool that would rather say "no display" than fail mid-render.
func Available() bool {
	if e := Acquire(64, 64); e != nil {
		return false
	}
	Release()
	return true
}
