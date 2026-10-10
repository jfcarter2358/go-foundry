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
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/glctx"
)

// The window and the context themselves are internal/glctx's: one creator for
// the whole engine, because the thread rules below are the same wherever a
// headless context is wanted and each copy of them got a different part wrong.
//
// It used to be one window and context PER TEST, which worked for as long as
// every GL test in the engine was compile-only. The first test that actually
// drew and read a pixel back found the two problems with that: contexts and
// windows accumulate, and each new one silently displaces the previous as
// current on the thread that made it, so which context a later test was drawing
// into depended on how the runtime happened to schedule goroutines onto threads.
// The symptom was an intermittent segfault inside the driver at glReadPixels, on
// a test whose own logic was fine.
//
// Sequential tests are what makes one shared context correct: a GL context may
// be current on only one thread at a time, so nothing here may call
// t.Parallel().

// Context makes the test binary's GL 4.3 context current on this test's thread,
// creating it on first use — or skips the test when the opt-in env var is unset.
//
// Nothing is torn down on the way out: a Go test does not necessarily run on
// the process's main OS thread, and tearing an SDL video subsystem down from
// another thread segfaults in the driver. The test binary is about to exit, so
// the OS reclaims everything.
func Context(t *testing.T) {
	t.Helper()
	if os.Getenv("FOUNDRY_GL_TEST") == "" {
		t.Skip("set FOUNDRY_GL_TEST=1 to run (needs a display and a GL 4.3 driver)")
	}
	// Acquire locks this goroutine's thread for the rest of the test, so nothing
	// it does afterwards can be rescheduled onto a thread the context is not
	// current on — and Release gives it up again, which is not optional: GLX
	// refuses to make a context current on a second thread while it is still
	// current on the first ("BadAccess: attempt to access private resource
	// denied"), so a test that kept it would take every test after it down.
	if err := glctx.Acquire(64, 64); err != nil {
		t.Fatalf("GL context: %v", err)
	}
	t.Cleanup(glctx.Release)

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
