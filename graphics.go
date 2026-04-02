package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/go-gl/gl/v4.1-core/gl"
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

	vertexSource := strings.ReplaceAll(strings.TrimRight(string(vertexShaderBytes), "\n"), "\r\n", "\n")
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
