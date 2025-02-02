package main

import (
	"fmt"
	_ "image/png"
	"math"
	"os"
	"path/filepath"

	"github.com/go-gl/gl/v3.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

var textureNames = map[string]string{
	"square": "textures/square.png",
}
var Walls = []Wall{}
var Textures = map[string]uint32{}
var vertexShaderPath = "shaders/vertex.glsl"
var fragmentShaderPath = "shaders/fragment.glsl"
var Running = true
var Program uint32
var KeyStates []uint8
var event sdl.Event
var WinTitle string = "Go-SDL2 + Go-GL"
var WinWidth, WinHeight int32 = 800, 600
var WinHalfHeight int32 = WinHeight / 2
var WinHalfWidth int32 = WinWidth / 2

func main() {
	// Setup the texture map
	Textures = make(map[string]uint32)

	// Get the current executable location
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	executablePath := filepath.Dir(executable)

	var window *sdl.Window
	var context sdl.GLContext

	if err = sdl.Init(sdl.INIT_EVERYTHING); err != nil {
		panic(err)
	}
	defer sdl.Quit()

	window, err = sdl.CreateWindow(WinTitle, sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED,
		WinWidth, WinHeight, sdl.WINDOW_OPENGL)
	if err != nil {
		panic(err)
	}
	defer window.Destroy()
	context, err = window.GLCreateContext()
	if err != nil {
		panic(err)
	}
	defer sdl.GLDeleteContext(context)

	if err = gl.Init(); err != nil {
		panic(err)
	}

	gl.Enable(gl.DEPTH_TEST)
	gl.ClearColor(0.2, 0.2, 0.3, 1.0)
	gl.ClearDepth(1)
	gl.DepthFunc(gl.LEQUAL)
	gl.Viewport(0, 0, int32(WinWidth), int32(WinHeight))

	if err := LoadProgram(fmt.Sprintf("%s/%s", executablePath, vertexShaderPath), fmt.Sprintf("%s/%s", executablePath, fragmentShaderPath)); err != nil {
		panic(err)
	}

	gl.UseProgram(Program)

	projection := mgl32.Perspective(mgl32.DegToRad(45.0), float32(WinWidth)/float32(WinHeight), 0.1, 1000.0)
	projectionUniform := gl.GetUniformLocation(Program, gl.Str("projection\x00"))
	gl.UniformMatrix4fv(projectionUniform, 1, false, &projection[0])

	LoadCamera(mgl32.Vec3{0, 0, 3}, float32(math.Pi), float32(math.Pi/2.0))

	LoadWall(
		[][]float32{
			{1.0, 1.0, 1.0, 1.0, 0.0},
			{1.0, -1.0, 1.0, 1.0, 1.0},
			{-1.0, -1.0, 1.0, 0.0, 1.0},
			{-1.0, 1.0, 1.0, 0.0, 0.0},
		},
		"square",
	)

	gl.BindFragDataLocation(Program, 0, gl.Str("outputColor\x00"))

	// Load the textures
	for name, path := range textureNames {
		if err := LoadTexture(name, fmt.Sprintf("%s/%s", executablePath, path)); err != nil {
			panic(err)
		}
	}

	for Running {
		sdl.PumpEvents()
		KeyStates = sdl.GetKeyboardState()
		HandleInput(KeyStates)
		for event = sdl.PollEvent(); event != nil; event = sdl.PollEvent() {
			HandleEvent(event)
		}
		for _, wall := range Walls {
			DrawWall(wall)
		}
		window.GLSwap()
	}
}
