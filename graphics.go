package main

// graphics.go — the world shader program, and the GL handles every draw call
// needs to reach.
//
// LoadProgram compiles and links the program and caches each uniform's
// location; the locations live here beside it rather than in main.go, because
// the code that reads them is Geometry.Draw (geometry.go), not the bootstrap.
// Textures and Program are here for the same reason: they are what a draw call
// binds, and nothing about them belongs to the application's startup.

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// Program is the linked world shader program, and Textures maps a texture name
// to its uploaded GL handle. Both are written once at startup and read by every
// draw call.
var Program uint32
var Textures = map[string]uint32{}

// Projection is the perspective matrix; kept global for frustum extraction.
var Projection mgl32.Mat4

// ProjectionUniform is the shader uniform location for the projection matrix.
// Kept global so RefreshWindowSize can re-upload it after a resize or fullscreen toggle.
//
// AlphaUniform is the shader uniform location for the per-geometry alpha multiplier.
//
// SmoothUniform selects the normal the fragment stage shades with: the
// interpolated per-vertex normal when set, the flat screen-space derivative
// facet when not. Every draw through Geometry sets it, so a smoothed surface
// never leaks its setting onto the geometry drawn after it.
//
// AlphaSharpenUniform gates fragment.glsl's alpha-cutout anti-aliasing. Set
// per pass rather than per draw — on for the opaque pass, where a fragment's
// alpha is a coverage mask (SetAlphaToCoverage, antialias.go) and cutout
// silhouettes want a one-pixel edge; off for the sorted transparent pass,
// where alpha is blend weight and sharpening it would quantise real
// translucency to opaque-or-gone.
var ProjectionUniform int32
var AlphaUniform int32
var SmoothUniform int32
var AlphaSharpenUniform int32

// Water shader uniform locations. TimeUniform and CamPosUniform are set once
// per frame; the rest are set per-draw from Geometry.Water.
var (
	TimeUniform          int32
	CamPosUniform        int32
	IsWaterUniform       int32
	WaveAmplitudeUniform int32
	WaveLengthUniform    int32
	WaveSpeedUniform     int32
	ReflectivityUniform  int32
	ReflectColorUniform  int32
)

func LoadProgram(vertexShaderPath, fragmentShaderPath string) error {
	vertexShaderBytes, err := os.ReadFile(vertexShaderPath)
	if err != nil {
		return fmt.Errorf("read vertex shader: %w", err)
	}
	fragmentShaderBytes, err := os.ReadFile(fragmentShaderPath)
	if err != nil {
		return fmt.Errorf("read fragment shader: %w", err)
	}

	// The vertex stage gets the shared skinning source spliced in after its
	// #version line, so an animated entity deforms on the GPU. Static geometry
	// is unaffected: skinPosition is the identity until a draw call sets the
	// `skinned` uniform (skin_common.glsl).
	vertexSource, err := injectSkinning(strings.ReplaceAll(strings.TrimRight(string(vertexShaderBytes), "\n"), "\r\n", "\n"))
	if err != nil {
		return err
	}
	fragmentSource := strings.ReplaceAll(strings.TrimRight(string(fragmentShaderBytes), "\n"), "\r\n", "\n")

	// [SCENARIO 1] Log shader source so the exact text sent to the driver is
	// visible in foundry.log when a compile failure occurs.
	log.Printf("--- vertex shader source ---\n%s\n---", vertexSource)

	vertexShader, err := CompileShader(vertexSource, gl.VERTEX_SHADER)
	if err != nil {
		return fmt.Errorf("vertex shader: %w", err)
	}
	log.Printf("vertex shader compiled OK")

	log.Printf("--- fragment shader source ---\n%s\n---", fragmentSource)

	fragmentShader, err := CompileShader(fragmentSource, gl.FRAGMENT_SHADER)
	if err != nil {
		return fmt.Errorf("fragment shader: %w", err)
	}
	log.Printf("fragment shader compiled OK")

	Program = gl.CreateProgram()
	gl.AttachShader(Program, vertexShader)
	gl.AttachShader(Program, fragmentShader)
	gl.LinkProgram(Program)

	var status int32
	gl.GetProgramiv(Program, gl.LINK_STATUS, &status)
	if status == gl.FALSE {
		var logLength int32
		gl.GetProgramiv(Program, gl.INFO_LOG_LENGTH, &logLength)
		infoLog := strings.Repeat("\x00", int(logLength+1))
		gl.GetProgramInfoLog(Program, logLength, nil, gl.Str(infoLog))
		return fmt.Errorf("program link failed: %v", infoLog)
	}

	gl.DeleteShader(vertexShader)
	gl.DeleteShader(fragmentShader)
	return nil
}

func CompileShader(source string, shaderType uint32) (uint32, error) {
	typeName := "vertex"
	if shaderType == gl.FRAGMENT_SHADER {
		typeName = "fragment"
	}

	shader := gl.CreateShader(shaderType)
	csources, free := gl.Strs(source + "\x00")
	gl.ShaderSource(shader, 1, csources, nil)
	free()
	gl.CompileShader(shader)

	var status int32
	gl.GetShaderiv(shader, gl.COMPILE_STATUS, &status)
	if status == gl.FALSE {
		var logLength int32
		gl.GetShaderiv(shader, gl.INFO_LOG_LENGTH, &logLength)
		infoLog := strings.Repeat("\x00", int(logLength+1))
		gl.GetShaderInfoLog(shader, logLength, nil, gl.Str(infoLog))
		// [SCENARIO 1 & 2 & 3] Compiler error includes the driver's message so
		// dFdx/dFdy failures and version rejections appear verbatim in the log.
		return 0, fmt.Errorf("%s shader compile error: %v", typeName, infoLog)
	}

	return shader, nil
}

// Where the world shader's two stages are read from, relative to
// fileutil.ExecutablePath. Named here rather than at the call site so a build that wants
// to swap them has one place to do it.
var vertexShaderPath = "shaders/vertex.glsl"
var fragmentShaderPath = "shaders/fragment.glsl"

// readShaderFile is shared by every program in the engine, not just the world
// shader — the shadow, detail, gizmo and skinning programs all read through it.
func readShaderFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(strings.TrimRight(string(data), "\n"), "\r\n", "\n"), nil
}

// injectAfterVersion inserts snippet immediately after the shader's first line
// (the #version directive), so shared uniforms/functions are declared before use.
func injectAfterVersion(src, snippet string) string {
	idx := strings.Index(src, "\n")
	if idx < 0 {
		return src + "\n" + snippet
	}
	return src[:idx+1] + "\n" + snippet + "\n" + src[idx+1:]
}

// compileProgramFromFiles links a standalone program from a vertex/fragment
// pair on disk. The gizmo and model-preview shaders both use it; the world
// program has LoadProgram above, which also caches uniform locations.
// compileProgramFromFiles loads two shader files and links a GL program
// without touching the global Program variable.
func compileProgramFromFiles(vertPath, fragPath string) (uint32, error) {
	vb, err := os.ReadFile(vertPath)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", vertPath, err)
	}
	fb, err := os.ReadFile(fragPath)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", fragPath, err)
	}
	vertSrc := strings.TrimRight(string(vb), "\n")
	fragSrc := strings.TrimRight(string(fb), "\n")

	vert, err := CompileShader(vertSrc, gl.VERTEX_SHADER)
	if err != nil {
		return 0, err
	}
	frag, err := CompileShader(fragSrc, gl.FRAGMENT_SHADER)
	if err != nil {
		return 0, err
	}
	prog := gl.CreateProgram()
	gl.AttachShader(prog, vert)
	gl.AttachShader(prog, frag)
	gl.LinkProgram(prog)
	gl.DeleteShader(vert)
	gl.DeleteShader(frag)

	var status int32
	gl.GetProgramiv(prog, gl.LINK_STATUS, &status)
	if status == gl.FALSE {
		var logLen int32
		gl.GetProgramiv(prog, gl.INFO_LOG_LENGTH, &logLen)
		infoLog := strings.Repeat("\x00", int(logLen+1))
		gl.GetProgramInfoLog(prog, logLen, nil, gl.Str(infoLog))
		return 0, fmt.Errorf("link failed: %s", infoLog)
	}
	return prog, nil
}
