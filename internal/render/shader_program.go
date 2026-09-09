package render

// shader_program.go — the two things every renderer in this package needs
// before it can draw: a program with its uniform locations cached, and the
// link step that produces one.
//
// These used to live in detail_render.go, which was simply the first pass to
// need them; the grass, scatter, sky, sprite and biome-light renderers all
// borrowed them from there. Removing the per-face detail pass took that file
// with it, so what everything shares lives here instead — beside nothing in
// particular, which is what a shared helper should be beside.

import (
	"fmt"
	"os"
	"strings"

	"github.com/go-gl/gl/v4.1-core/gl"
)

// progUniforms wraps a GL program with a lazy uniform-location cache.
type progUniforms struct {
	prog uint32
	loc  map[string]int32
}

func (p *progUniforms) U(name string) int32 {
	if l, ok := p.loc[name]; ok {
		return l
	}
	l := gl.GetUniformLocation(p.prog, gl.Str(name+"\x00"))
	p.loc[name] = l
	return l
}

// mustReadShader reads a shader stage off disk, normalising line endings.
//
// It panics rather than returning an error: a missing or unreadable stage is a
// broken install, not a condition a frame can recover from, and every caller
// here runs at startup where there is nothing to fall back to.
func mustReadShader(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("shader read %s: %v", path, err))
	}
	return strings.ReplaceAll(strings.TrimRight(string(b), "\n"), "\r\n", "\n")
}

// linkStages links already-compiled shader objects into a program and deletes
// them, since the program holds its own reference once attached.
func linkStages(shaders ...uint32) (uint32, error) {
	prog := gl.CreateProgram()
	for _, s := range shaders {
		gl.AttachShader(prog, s)
	}
	gl.LinkProgram(prog)
	for _, s := range shaders {
		gl.DeleteShader(s)
	}
	var status int32
	gl.GetProgramiv(prog, gl.LINK_STATUS, &status)
	if status == gl.FALSE {
		var n int32
		gl.GetProgramiv(prog, gl.INFO_LOG_LENGTH, &n)
		logMsg := strings.Repeat("\x00", int(n+1))
		gl.GetProgramInfoLog(prog, n, nil, gl.Str(logMsg))
		return 0, fmt.Errorf("link failed: %s", logMsg)
	}
	return prog, nil
}
