package gltest

// Package gltest stands up a real GL context for the shader smoke tests.
//
// go build cannot see inside a .glsl file, and the shaders are compiled lazily
// on first use, so a typo in one of them would otherwise surface as a black
// window rather than as a build failure. These tests catch that — but they need
// a display, so they are opt-in.
//
// It is an ordinary package rather than a _test.go helper because more than one
// package's tests need it, and a test helper does not cross a package boundary.

import (
	"os"
	"runtime"
	"sync"
	"testing"

	"foundry/internal/gfx"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/veandco/go-sdl2/sdl"
)

// One window and one context for the whole test binary, made current on
// whichever OS thread the calling test has locked itself to.
//
// It used to be one of each PER TEST, which worked for as long as every GL test
// in the engine was compile-only. The first test that actually drew and read a
// pixel back found the two problems with that: contexts and windows accumulate
// (nothing is ever destroyed — see the note on teardown below), and each new
// one silently displaces the previous as current on the thread that made it,
// so which context a later test was drawing into depended on how the runtime
// happened to schedule goroutines onto threads. The symptom was an intermittent
// segfault inside the driver at glReadPixels, on a test whose own logic was
// fine.
//
// Sequential tests are what makes one shared context correct: a GL context may
// be current on only one thread at a time, so nothing here may call
// t.Parallel().
var (
	glOnce sync.Once
	glWin  *sdl.Window
	glCtx  sdl.GLContext
	glErr  error
)

func initGL() {
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		glErr = err
		return
	}
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_MAJOR_VERSION, 4)
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_MINOR_VERSION, 1)
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_PROFILE_MASK, sdl.GL_CONTEXT_PROFILE_CORE)

	glWin, glErr = sdl.CreateWindow("gl test", sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED,
		64, 64, sdl.WINDOW_OPENGL|sdl.WINDOW_HIDDEN)
	if glErr != nil {
		return
	}
	if glCtx, glErr = glWin.GLCreateContext(); glErr != nil {
		return
	}
	// Loads the function pointers into package globals, so it is a
	// once-per-process call rather than a once-per-context one.
	glErr = gl.Init()
}

// Context makes the test binary's GL 4.1 context current on this test's thread,
// creating it on first use — or skips the test when the opt-in env var is unset.
//
// Nothing is torn down on the way out: a Go test does not necessarily run on
// the process's main OS thread, and tearing an SDL video subsystem down from
// another thread segfaults in the driver. The test binary is about to exit, so
// the OS reclaims everything.
func Context(t *testing.T) {
	t.Helper()
	if os.Getenv("FOUNDRY_GL_TEST") == "" {
		t.Skip("set FOUNDRY_GL_TEST=1 to run (needs a display and a GL 4.1 driver)")
	}
	// Locked for the rest of the test, so nothing this test does afterwards can
	// be rescheduled onto a thread the context is not current on.
	runtime.LockOSThread()

	glOnce.Do(initGL)
	if glErr != nil {
		t.Fatalf("GL context: %v", glErr)
	}
	if err := glWin.GLMakeCurrent(glCtx); err != nil {
		t.Fatalf("GLMakeCurrent: %v", err)
	}
	// Released again at the end of the test, and this is not optional: GLX
	// refuses to make a context current on a second thread while it is still
	// current on the first ("BadAccess: attempt to access private resource
	// denied"), so a test that kept it would take every test after it down.
	t.Cleanup(func() {
		_ = glWin.GLMakeCurrent(nil)
		runtime.UnlockOSThread()
	})

	// The context is shared now, so the names in these registries are still
	// valid — but a test that expects to upload a texture and get a fresh one
	// should get a fresh one, and a test that never uploads should not inherit
	// whatever the last test left under the same name. Clearing them costs a
	// re-upload and leaks the old objects until the process exits, which for a
	// test binary is free.
	for name := range gfx.Textures {
		delete(gfx.Textures, name)
	}
	for name := range gfx.TextureSizes {
		delete(gfx.TextureSizes, name)
	}
	for key := range gfx.Cubemaps {
		delete(gfx.Cubemaps, key)
	}

	// gfx.Program is reset for the same isolation, and for one more reason: the
	// guards around it (gfx.UpdateCamMatrix, render.RenderSky's restore) read
	// "0 means there is no world shader". A test that draws without loading one
	// must see 0 there, not the handle the previous test happened to leave.
	gfx.Program = 0
}
