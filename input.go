package main

import (
	"fmt"

	"github.com/go-gl/gl/v3.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

var MovementSpeed = float32(0.05)
var RotationSpeed = float32(0.0000005)

func HandleInput(keyStates []uint8) {
	fMove := float32(0.0)
	uMove := float32(0.0)
	rMove := float32(0.0)

	if keyStates[sdl.SCANCODE_ESCAPE] != 0 {
		Running = false
	}
	if keyStates[sdl.SCANCODE_W] != 0 {
		fMove += MovementSpeed
	}
	if keyStates[sdl.SCANCODE_A] == 1 {
		rMove += MovementSpeed
	}
	if keyStates[sdl.SCANCODE_S] == 1 {
		fMove -= MovementSpeed
	}
	if keyStates[sdl.SCANCODE_D] == 1 {
		rMove -= MovementSpeed
	}
	if keyStates[sdl.SCANCODE_LEFT] == 1 {
		Cam.Yaw -= RotationSpeed
	}
	if keyStates[sdl.SCANCODE_RIGHT] == 1 {
		Cam.Yaw += RotationSpeed
	}
	if keyStates[sdl.SCANCODE_UP] == 1 {
		Cam.Pitch -= RotationSpeed
	}
	if keyStates[sdl.SCANCODE_DOWN] == 1 {
		Cam.Pitch += RotationSpeed
	}

	MoveCamera(fMove, uMove, rMove)
	Cam.Matrix = mgl32.LookAtV(Cam.Position, Cam.Position.Add(Cam.Direction), mgl32.Vec3{0.0, 1.0, 0.0})
	gl.UniformMatrix4fv(Cam.Uniform, 1, false, &Cam.Matrix[0])
}

func HandleEvent(event sdl.Event) {
	switch t := event.(type) {
	case sdl.QuitEvent:
		Running = false
	case sdl.MouseMotionEvent:
		if Cam.MouseLock {
			xRel, yRel, _ := sdl.GetRelativeMouseState()
			fmt.Printf("x rel: %d, y rel: %d; ", xRel, yRel)
			Cam.Yaw += RotationSpeed * float32(xRel)
			Cam.Pitch += RotationSpeed * float32(yRel)
		}

	case sdl.KeyboardEvent:
		keyCode := t.Keysym.Sym
		// keys := ""

		// Modifier keys
		// switch t.Keysym.Mod {
		// case sdl.KMOD_LALT:
		// 	keys += "Left Alt"
		// case sdl.KMOD_LCTRL:
		// 	keys += "Left Control"
		// case sdl.KMOD_LSHIFT:
		// 	keys += "Left Shift"
		// case sdl.KMOD_LGUI:
		// 	keys += "Left Meta or Windows key"
		// case sdl.KMOD_RALT:
		// 	keys += "Right Alt"
		// case sdl.KMOD_RCTRL:
		// 	keys += "Right Control"
		// case sdl.KMOD_RSHIFT:
		// 	keys += "Right Shift"
		// case sdl.KMOD_RGUI:
		// 	keys += "Right Meta or Windows key"
		// case sdl.KMOD_NUM:
		// 	keys += "Num Lock"
		// case sdl.KMOD_CAPS:
		// 	keys += "Caps Lock"
		// case sdl.KMOD_MODE:
		// 	keys += "AltGr Key"
		// }

		if keyCode < 10000 {
			// If the key is held down, this will fire
			if t.Repeat > 0 {
				// skip
			} else {
				if t.State == sdl.RELEASED {
					// skip
				} else if t.State == sdl.PRESSED {
					// skip
				}
			}

		}
	}
}
