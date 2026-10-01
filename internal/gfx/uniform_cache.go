package gfx

// uniform_cache.go — uniform locations, looked up once per program.
//
// The per-frame uploads (lights, fog volumes, both kinds of shadow) name their
// uniforms by string, and many are array elements named on the fly —
// "pointShadowMap[3]". glGetUniformLocation is a string lookup inside the
// driver behind a cgo call, and at a few hundred a frame over five programs it
// was the single largest CPU cost of a frame once the passes were fixed. A
// location never changes for the life of a linked program, so it is asked once.
//
// Keyed by program name alone, which is sound because every secondary window
// shares objects with the main context (uiwin/window.go), so a name means one
// program everywhere. A name can be reused once its program is deleted, so every
// link of a program that is ever asked about here calls ForgetUniformLocations
// first.

import (
	"github.com/go-gl/gl/v4.3-core/gl"
)

var uniformLocs = map[uint32]map[string]int32{}

// UniformLoc is glGetUniformLocation(prog, name), remembered. name must be
// NUL-terminated, as gl.Str requires.
func UniformLoc(prog uint32, name string) int32 {
	m := uniformLocs[prog]
	if m == nil {
		m = map[string]int32{}
		uniformLocs[prog] = m
	}
	if loc, ok := m[name]; ok {
		return loc
	}
	loc := gl.GetUniformLocation(prog, gl.Str(name))
	m[name] = loc
	return loc
}

// ForgetUniformLocations drops what UniformLoc remembers of prog. Called
// wherever a program is linked, because the name it links under may be one a
// deleted program had.
func ForgetUniformLocations(prog uint32) { delete(uniformLocs, prog) }
