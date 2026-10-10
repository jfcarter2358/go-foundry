package glctx

// The GL version every context the engine makes is asked for, and refuses to
// run below. 4.3 is where compute shaders, storage buffers and indirect
// multi-draws are core, so a GPU pass may use them without a CPU stand-in for
// drivers that lack them. Every shader is `#version 430` to match.

import (
	"fmt"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	MajorVersion = 4
	MinorVersion = 3
)

// RequestCoreProfile asks SDL for a MajorVersion.MinorVersion core context.
// The attributes are SDL-global and apply to every context made after it —
// the main window's, the shared UI windows', the offscreen one — so call it
// before the first.
func RequestCoreProfile() {
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_MAJOR_VERSION, MajorVersion)
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_MINOR_VERSION, MinorVersion)
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_PROFILE_MASK, sdl.GL_CONTEXT_PROFILE_CORE)
}

// CheckVersion reports an error naming the driver when the current context is
// older than required. A driver may hand back less than RequestCoreProfile
// asked for rather than fail, so the context is checked, not the request.
// Call after gl.Init.
func CheckVersion() error {
	var major, minor int32
	gl.GetIntegerv(gl.MAJOR_VERSION, &major)
	gl.GetIntegerv(gl.MINOR_VERSION, &minor)
	if major > MajorVersion || (major == MajorVersion && minor >= MinorVersion) {
		return nil
	}
	return fmt.Errorf("OpenGL %d.%d is required; the driver gave %d.%d (%s, %s)",
		MajorVersion, MinorVersion, major, minor,
		gl.GoStr(gl.GetString(gl.RENDERER)), gl.GoStr(gl.GetString(gl.VERSION)))
}
