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
	"testing"

	"foundry/internal/gfx"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/veandco/go-sdl2/sdl"
)

// Context stands up a hidden GL 4.1 context, or skips the test when the opt-in
// env var is unset.
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
	runtime.LockOSThread()

	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Fatalf("sdl.Init: %v", err)
	}
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_MAJOR_VERSION, 4)
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_MINOR_VERSION, 1)
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_PROFILE_MASK, sdl.GL_CONTEXT_PROFILE_CORE)

	win, err := sdl.CreateWindow("gl test", sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED,
		64, 64, sdl.WINDOW_OPENGL|sdl.WINDOW_HIDDEN)
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}
	if _, err := win.GLCreateContext(); err != nil {
		t.Fatalf("GLCreateContext: %v", err)
	}
	if err := gl.Init(); err != nil {
		t.Fatalf("gl.Init: %v", err)
	}

	// Every test gets its own context and none are shared, so the GL names the
	// registry is holding were minted in a context this one cannot reach:
	// binding one is GL_INVALID_OPERATION, not a stale image. Clearing the
	// registry makes the next upload mint a name here instead.
	for name := range gfx.Textures {
		delete(gfx.Textures, name)
	}
	for name := range gfx.TextureSizes {
		delete(gfx.TextureSizes, name)
	}
}
