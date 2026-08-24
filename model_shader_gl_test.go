package main

// model_shader_gl_test.go — an opt-in smoke test that the model editor's
// shaders actually compile and link on this machine's driver.
//
// go build cannot see inside a .glsl file, and the model editor compiles its
// program lazily on first open, so a typo in model_vert.glsl would otherwise
// only surface as a black window at the moment someone clicks "Model".
//
// It needs a real GL context, which means a display, so it is opt-in:
//
//	FOUNDRY_GL_TEST=1 go test -run TestModelShadersCompile .
//
// Skipped by default so plain `go test ./...` stays headless-safe.

import (
	"os"
	"runtime"
	"testing"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/veandco/go-sdl2/sdl"
)

// glTestContext stands up a hidden GL 4.1 context, or skips the test when the
// opt-in env var is unset. Nothing is torn down on the way out: a Go test does
// not necessarily run on the process's main OS thread, and tearing an SDL video
// subsystem down from another thread segfaults in the driver. The test binary
// is about to exit, so the OS reclaims everything.
func glTestContext(t *testing.T) {
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
}

func TestModelShadersCompile(t *testing.T) {
	glTestContext(t)

	prog, err := compileProgramFromFiles(
		"resources/shaders/model_vert.glsl",
		"resources/shaders/model_frag.glsl",
	)
	if err != nil {
		t.Fatalf("model editor shaders failed to build: %v", err)
	}
	if prog == 0 {
		t.Fatal("compileProgramFromFiles returned program 0")
	}

	// Every uniform the renderer sets must exist, or drawModelMesh would be
	// writing to -1 and the mesh would render with a stale matrix.
	for _, name := range []string{"projection", "view", "model", "tex", "camPos", "tint", "smoothShading"} {
		if loc := gl.GetUniformLocation(prog, gl.Str(name+"\x00")); loc < 0 {
			t.Errorf("uniform %q not found in the linked program", name)
		}
	}

	// Attribute locations must match vertex.glsl's, since Geometry.Load builds
	// its VAO against the world program's indices.
	for name, want := range map[string]int32{"vert": 0, "vertTexCoord": 1, "vertNormal": 2} {
		if got := gl.GetAttribLocation(prog, gl.Str(name+"\x00")); got != want {
			t.Errorf("attribute %q at location %d, want %d (must match vertex.glsl)", name, got, want)
		}
	}
}

// TestSkinnedShadersCompileAndLink is the check the text-level tests cannot
// make: that skin_common.glsl is valid GLSL, that splicing it into all three
// entity passes still links, and that its uniforms and attributes come out where
// the Go side expects them.
//
// The bone-array uniform is the one worth proving at runtime: MAX_BONES is
// chosen against GL 4.1's *guaranteed* vertex uniform budget, and a driver that
// could not fit it would fail to link here rather than silently at the moment
// someone loads a rigged model.
func TestSkinnedShadersCompileAndLink(t *testing.T) {
	glTestContext(t)

	prevExe := executablePath
	executablePath = "resources"
	t.Cleanup(func() { executablePath = prevExe })

	if _, err := skinCommonSource(); err != nil {
		t.Fatalf("skin_common.glsl did not load: %v", err)
	}

	// The colour pass, through the real loader.
	if err := LoadProgram("resources/shaders/vertex.glsl", "resources/shaders/fragment.glsl"); err != nil {
		t.Fatalf("world program with skinning failed to build: %v", err)
	}
	for _, name := range []string{"skinned", "bones"} {
		if loc := gl.GetUniformLocation(Program, gl.Str(name+"\x00")); loc < 0 {
			t.Errorf("uniform %q missing from the world program; applySkinUniforms would write to -1", name)
		}
	}
	for name, want := range map[string]int32{"vert": 0, "vertTexCoord": 1, "vertNormal": 2, "boneIdx": 3, "boneWt": 4} {
		if got := gl.GetAttribLocation(Program, gl.Str(name+"\x00")); got != want {
			t.Errorf("attribute %q at location %d, want %d", name, got, want)
		}
	}

	// Both shadow passes, through the same linkProgram the engine uses.
	var dirProg, cubeProg uint32
	if err := linkProgram("resources/shaders/shadow_vert.glsl", "resources/shaders/shadow_frag.glsl", &dirProg); err != nil {
		t.Fatalf("cascade shadow program with skinning failed to build: %v", err)
	}
	if err := linkProgram("resources/shaders/shadow_cube_vert.glsl", "resources/shaders/shadow_cube_frag.glsl", &cubeProg); err != nil {
		t.Fatalf("point shadow program with skinning failed to build: %v", err)
	}
	for _, p := range []struct {
		name string
		prog uint32
	}{{"cascade", dirProg}, {"point", cubeProg}} {
		for _, name := range []string{"skinned", "bones"} {
			if loc := gl.GetUniformLocation(p.prog, gl.Str(name+"\x00")); loc < 0 {
				t.Errorf("%s shadow program has no %q uniform; an animated entity's shadow would not follow it",
					p.name, name)
			}
		}
	}
}
