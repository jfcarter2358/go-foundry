package main

import (
	"fmt"
	"log"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

const MaxDirLights   = 4
const MaxPointLights = 8

// GlobalLight is a uniform ambient light applied equally to all surfaces.
type GlobalLight struct {
	Color      mgl32.Vec3
	Brightness float32
}

// DirectionalLight is a light with a fixed direction and infinite range (like the sun).
type DirectionalLight struct {
	Direction      mgl32.Vec3
	Color          mgl32.Vec3
	Brightness     float32
	CastShadows    bool
	ShadowStrength float32 // 0.0 = no shadow darkening, 1.0 = fully dark shadows
}

// PointLight is a positional light with a quadratic radius-based falloff.
type PointLight struct {
	Position       mgl32.Vec3
	Color          mgl32.Vec3
	Brightness     float32
	Radius         float32
	CastShadows    bool
	ShadowStrength float32 // 0.0 = no shadow darkening, 1.0 = fully dark shadows
}

// LightSet holds all lights in the scene.
type LightSet struct {
	Global      GlobalLight
	Directional []DirectionalLight
	Points      []PointLight
}

// uniformLoc fetches a uniform location and logs a warning if it is missing
// (-1), which means the name does not exist in the linked shader program.
// [SCENARIO 5] Storing the name in a local variable also ensures the backing
// array is not collected before gl.Str returns the pointer to the driver.
func uniformLoc(name string) int32 {
	loc := gl.GetUniformLocation(Program, gl.Str(name))
	if loc == -1 {
		log.Printf("WARNING: uniform %q not found in shader (location=-1)", name[:len(name)-1])
	}
	return loc
}

// UploadLights sends all light uniforms to the active shader program.
func UploadLights(ls LightSet) {
	log.Printf("uploading lights: global=1, directional=%d, point=%d",
		len(ls.Directional), len(ls.Points))

	gl.Uniform3f(uniformLoc("globalLightColor\x00"),
		ls.Global.Color[0], ls.Global.Color[1], ls.Global.Color[2])
	gl.Uniform1f(uniformLoc("globalLightBrightness\x00"), ls.Global.Brightness)

	numDir := len(ls.Directional)
	if numDir > MaxDirLights {
		numDir = MaxDirLights
	}
	gl.Uniform1i(uniformLoc("numDirLights\x00"), int32(numDir))
	for i := 0; i < numDir; i++ {
		d := ls.Directional[i]
		// [SCENARIO 5] Keep Sprintf result in a named variable so the string
		// backing array stays alive for the duration of the gl call.
		dirName := fmt.Sprintf("dirLightDir[%d]\x00", i)
		gl.Uniform3f(uniformLoc(dirName), d.Direction[0], d.Direction[1], d.Direction[2])
		colorName := fmt.Sprintf("dirLightColor[%d]\x00", i)
		gl.Uniform3f(uniformLoc(colorName), d.Color[0], d.Color[1], d.Color[2])
		brightnessName := fmt.Sprintf("dirLightBrightness[%d]\x00", i)
		gl.Uniform1f(uniformLoc(brightnessName), d.Brightness)
	}

	numPoint := len(ls.Points)
	if numPoint > MaxPointLights {
		numPoint = MaxPointLights
	}
	gl.Uniform1i(uniformLoc("numPointLights\x00"), int32(numPoint))
	for i := 0; i < numPoint; i++ {
		p := ls.Points[i]
		posName := fmt.Sprintf("pointLightPos[%d]\x00", i)
		gl.Uniform3f(uniformLoc(posName), p.Position[0], p.Position[1], p.Position[2])
		colorName := fmt.Sprintf("pointLightColor[%d]\x00", i)
		gl.Uniform3f(uniformLoc(colorName), p.Color[0], p.Color[1], p.Color[2])
		brightnessName := fmt.Sprintf("pointLightBrightness[%d]\x00", i)
		gl.Uniform1f(uniformLoc(brightnessName), p.Brightness)
		radiusName := fmt.Sprintf("pointLightRadius[%d]\x00", i)
		gl.Uniform1f(uniformLoc(radiusName), p.Radius)
	}

	log.Printf("light uniforms uploaded")
}
