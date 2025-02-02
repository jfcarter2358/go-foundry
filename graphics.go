package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/go-gl/gl/v3.3-core/gl"
)

func LoadProgram(vertexShaderPath, fragmentShaderPath string) error {
	vertexShaderBytes, err := os.ReadFile(vertexShaderPath)
	if err != nil {
		return err
	}
	fragmentShaderBytes, err := os.ReadFile(fragmentShaderPath)
	if err != nil {
		return err
	}
	vertexShader, err := CompileShader(string(vertexShaderBytes), gl.VERTEX_SHADER)
	if err != nil {
		return err
	}

	fragmentShader, err := CompileShader(string(fragmentShaderBytes), gl.FRAGMENT_SHADER)
	if err != nil {
		return err
	}

	Program = gl.CreateProgram()

	gl.AttachShader(Program, vertexShader)
	gl.AttachShader(Program, fragmentShader)
	gl.LinkProgram(Program)

	var status int32
	gl.GetProgramiv(Program, gl.LINK_STATUS, &status)
	if status == gl.FALSE {
		var logLength int32
		gl.GetProgramiv(Program, gl.INFO_LOG_LENGTH, &logLength)

		log := strings.Repeat("\x00", int(logLength+1))
		gl.GetProgramInfoLog(Program, logLength, nil, gl.Str(log))

		return fmt.Errorf("failed to link program: %v", log)
	}

	gl.DeleteShader(vertexShader)
	gl.DeleteShader(fragmentShader)

	return nil
}

func CompileShader(source string, shaderType uint32) (uint32, error) {
	shader := gl.CreateShader(shaderType)

	csources, free := gl.Strs(source)
	gl.ShaderSource(shader, 1, csources, nil)
	free()
	gl.CompileShader(shader)

	var status int32
	gl.GetShaderiv(shader, gl.COMPILE_STATUS, &status)
	if status == gl.FALSE {
		var logLength int32
		gl.GetShaderiv(shader, gl.INFO_LOG_LENGTH, &logLength)

		log := strings.Repeat("\x00", int(logLength+1))
		gl.GetShaderInfoLog(shader, logLength, nil, gl.Str(log))

		return 0, fmt.Errorf("failed to compile %v: %v", source, log)
	}

	return shader, nil
}
